package streams

import (
	"context"
	"fmt"
	"testing"
)

// stubHostResolver implements hostResolver (sourcefetch.Resolver) for tests.
type stubHostResolver struct {
	hosts map[string][]string
	err   error
}

func (s stubHostResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	if ips, ok := s.hosts[host]; ok {
		return ips, nil
	}
	return nil, fmt.Errorf("no such host %q", host)
}

func TestValidateUserProviderHost(t *testing.T) {
	accept := []string{
		"https://twitch.tv/formula1",
		"https://www.youtube.com/playlist?list=PLabc",
		"http://example.com/live/stream.m3u8",
		"http://10.0.0.5/s.m3u8",            // RFC1918 LAN
		"http://192.168.1.40:8080/s.m3u8",   // RFC1918 LAN + port
		"http://172.16.0.1/s.m3u8",          // RFC1918 LAN
		"http://[fc00::1]/s.m3u8",           // IPv6 ULA (private)
		"http://8.8.8.8/s.m3u8",             // public IPv4 literal
		"http://[2001:4860:4860::8888]/s.m3u8", // public global-unicast IPv6
	}
	for _, u := range accept {
		if err := validateUserProviderHost(u); err != nil {
			t.Errorf("validateUserProviderHost(%q) unexpected error: %v", u, err)
		}
	}

	reject := []string{
		"",                                       // empty
		"://nope",                                // empty scheme
		"file:///etc/shadow.m3u8",                // file scheme
		"ftp://example.com/x",                    // non-http scheme
		"http://user:pass@example.com/x",         // userinfo
		"http://127.0.0.1/x",                     // loopback v4
		"http://[::1]/x",                         // loopback v6
		"http://[::ffff:127.0.0.1]/x",            // IPv4-mapped loopback (must Unmap)
		"http://[::127.0.0.1]/x",                 // IPv4-compatible loopback (deprecated form)
		"http://169.254.169.254/latest/meta",     // link-local / cloud metadata
		"http://[::ffff:169.254.169.254]/x",      // IPv4-mapped metadata (must Unmap)
		"http://[::169.254.169.254]/x",           // IPv4-compatible metadata (deprecated form)
		"http://[fe80::1]/x",                     // link-local v6
		"http://0.0.0.0/x",                       // unspecified
		"http://224.0.0.1/x",                     // multicast
		"https:///x",                             // empty host
	}
	for _, u := range reject {
		if err := validateUserProviderHost(u); err == nil {
			t.Errorf("validateUserProviderHost(%q) expected error, got nil", u)
		}
	}
}

func TestValidateUserProviderResolvedHost(t *testing.T) {
	t.Parallel()
	resolver := stubHostResolver{hosts: map[string][]string{
		"public.example.com":   {"93.184.216.34"},
		"lan.example.com":      {"192.168.1.50"},
		"rebind.example.com":   {"127.0.0.1"},
		"metadata.example.com": {"169.254.169.254"},
		"mixed.example.com":    {"93.184.216.34", "127.0.0.1"},
	}}
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"public hostname", "https://public.example.com/v.m3u8", false},
		{"lan hostname allowed", "https://lan.example.com/v.m3u8", false},
		{"ip literal public", "https://93.184.216.34/v.m3u8", false},
		{"ip literal lan", "http://10.0.0.5/v.m3u8", false},
		{"decimal loopback literal", "http://2130706433/v.m3u8", true},
		{"dns rebind to loopback", "https://rebind.example.com/v.m3u8", true},
		{"dns to cloud metadata", "https://metadata.example.com/v", true},
		{"any resolved ip blocked fails", "https://mixed.example.com/v.m3u8", true},
		{"ipv4-mapped loopback literal", "http://[::ffff:127.0.0.1]/v", true},
		{"ipv4-compatible loopback literal", "http://[::127.0.0.1]/v", true},
		{"unresolvable host", "https://nope.example.com/v", true},
		{"userinfo rejected", "https://user:pass@public.example.com/v", true},
		{"empty host", "https:///v", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUserProviderResolvedHost(context.Background(), resolver, tc.url)
			if tc.wantErr && err == nil {
				t.Fatalf("url %q: got nil error, want error", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("url %q: got error %v, want nil", tc.url, err)
			}
		})
	}
}

func TestUserDirectInputPolicy(t *testing.T) {
	p := userDirectInputPolicy()

	for _, proto := range p.ProtocolWhitelist {
		if proto == "file" {
			t.Fatalf("userDirectInputPolicy must not whitelist the file protocol; got %v", p.ProtocolWhitelist)
		}
	}
	wantProtocols := map[string]bool{"http": true, "https": true, "tcp": true, "tls": true, "crypto": true}
	if len(p.ProtocolWhitelist) != len(wantProtocols) {
		t.Errorf("ProtocolWhitelist = %v, want exactly %v", p.ProtocolWhitelist, wantProtocols)
	}
	for _, proto := range p.ProtocolWhitelist {
		if !wantProtocols[proto] {
			t.Errorf("unexpected protocol %q in whitelist", proto)
		}
	}

	if !p.DisableRedirects {
		t.Error("DisableRedirects must be true")
	}
	if !p.DisableReconnect {
		t.Error("DisableReconnect must be true")
	}
	if p.RWTimeout <= 0 {
		t.Error("RWTimeout must be set")
	}
	blocked := map[string]bool{}
	for _, h := range p.BlockedHeaders {
		blocked[h] = true
	}
	for _, h := range []string{"Cookie", "Authorization", "Proxy-Authorization", "Referer"} {
		if !blocked[h] {
			t.Errorf("BlockedHeaders must include %q; got %v", h, p.BlockedHeaders)
		}
	}
}
