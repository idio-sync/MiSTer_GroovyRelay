package hlsbuffer

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenericPublicValidationRejectsLocalTargets(t *testing.T) {
	v := URLValidator{Resolver: staticResolver(map[string][]net.IP{
		"loopback.example": {net.ParseIP("127.0.0.1")},
		"lan.example":      {net.ParseIP("192.168.1.20")},
		"metadata.example": {net.ParseIP("169.254.169.254")},
		"public.example":   {net.ParseIP("93.184.216.34")},
	})}
	cases := []string{
		"http://127.0.0.1/live.m3u8",
		"http://[::1]/live.m3u8",
		"http://loopback.example/live.m3u8",
		"http://lan.example/live.m3u8",
		"http://metadata.example/live.m3u8",
		"file:///tmp/live.m3u8",
		"http://user:pass@public.example/live.m3u8",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := v.Validate(context.Background(), raw, TrustModeGenericPublic)
			if err == nil {
				t.Fatalf("Validate(%q) error = nil, want rejection", raw)
			}
		})
	}
}

func TestGenericPublicValidationRejectsRedirectToLocalTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1/private.m3u8", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	v := URLValidator{
		Resolver: staticResolver(map[string][]net.IP{
			"public.example": {net.ParseIP("93.184.216.34")},
		}),
		Client: rewriteHostClient(t, srv),
	}
	raw := strings.Replace(srv.URL, "127.0.0.1", "public.example", 1) + "/live.m3u8"

	_, err := v.Validate(context.Background(), raw, TrustModeGenericPublic)
	if err == nil {
		t.Fatal("Validate redirect error = nil, want local redirect rejection")
	}
	if !strings.Contains(err.Error(), "disallowed") && !strings.Contains(err.Error(), "public") {
		t.Fatalf("Validate redirect error = %q, want address-class rejection", err)
	}
}

func TestGenericPublicValidationDialsValidatedIPAndPreservesHost(t *testing.T) {
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("server addr: %v", err)
	}
	var dialed []string
	v := URLValidator{
		Resolver: staticResolver(map[string][]net.IP{
			"media.example": {net.ParseIP("93.184.216.34")},
		}),
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, strings.TrimPrefix(srv.URL, "http://"))
		},
	}

	if _, err := v.Validate(context.Background(), "http://media.example:"+port+"/live.m3u8", TrustModeGenericPublic); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(dialed) != 1 || dialed[0] != "93.184.216.34:"+port {
		t.Fatalf("dialed = %v, want validated public IP", dialed)
	}
	if gotHost != "media.example:"+port {
		t.Fatalf("Host = %q, want original host", gotHost)
	}
}

func TestFetchBytesDialsValidatedIPAndPreservesHost(t *testing.T) {
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		_, _ = w.Write([]byte("#EXTM3U\n"))
	}))
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("server addr: %v", err)
	}
	var dialed []string
	v := URLValidator{
		Resolver: staticResolver(map[string][]net.IP{
			"media.example": {net.ParseIP("93.184.216.34")},
		}),
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, strings.TrimPrefix(srv.URL, "http://"))
		},
	}

	body, finalURL, err := fetchBytes(context.Background(), "http://media.example:"+port+"/playlist.m3u8", 1024, 0, TrustModeGenericPublic, v)
	if err != nil {
		t.Fatalf("fetchBytes: %v", err)
	}
	if string(body) != "#EXTM3U\n" {
		t.Fatalf("body = %q", body)
	}
	if finalURL != "http://media.example:"+port+"/playlist.m3u8" {
		t.Fatalf("finalURL = %q", finalURL)
	}
	if len(dialed) != 1 || dialed[0] != "93.184.216.34:"+port {
		t.Fatalf("dialed = %v, want validated public IP", dialed)
	}
	if gotHost != "media.example:"+port {
		t.Fatalf("Host = %q, want original host", gotHost)
	}
}

func TestBundledToonamiValidationRejectsWrongHostOrPath(t *testing.T) {
	v := URLValidator{}
	cases := []string{
		"http://example.com:3000/est/playlist.m3u8",
		"http://api.toonamiaftermath.com:3000/evil/playlist.m3u8",
		"http://api.toonamiaftermath.com/est/playlist.m3u8",
		"http://user@api.toonamiaftermath.com:3000/est/playlist.m3u8",
		"http://api.toonamiaftermath.com:3000/est/playlist.m3u8?token=x",
		// Wowza origin pool: only /livehttporigin/{channel}/ paths and the
		// streamDelay query are allowed; everything else must be rejected.
		"http://asp6.toonamiaftermath.com:1934/evil/x.m3u8",
		"http://other.toonamiaftermath.com:1934/livehttporigin/est/x.m3u8",
		"http://asp6.toonamiaftermath.com:8080/livehttporigin/est/x.m3u8",
		"http://asp6.toonamiaftermath.com:1934/livehttporigin/est/x.m3u8?token=x",
		"http://asp6.toonamiaftermath.com:1934/livehttporigin/est/x.m3u8?streamDelay=180&token=x",
		"http://aspx.toonamiaftermath.com:1934/livehttporigin/est/x.m3u8",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := v.ValidateReference(context.Background(), raw, TrustModeBundledToonami)
			if err == nil {
				t.Fatalf("ValidateReference(%q) error = nil, want rejection", raw)
			}
		})
	}
}

func TestBundledToonamiValidationAcceptsKnownHosts(t *testing.T) {
	v := URLValidator{}
	cases := []string{
		// API host: master playlists for each bundled channel.
		"http://api.toonamiaftermath.com:3000/est/playlist.m3u8",
		"http://api.toonamiaftermath.com:3000/pst/playlist.m3u8",
		"http://api.toonamiaftermath.com:3000/movies/playlist.m3u8",
		"http://api.toonamiaftermath.com:3000/radio/playlist.m3u8",
		// Wowza origin pool: variant playlists and segments live here. The
		// hostname digit is load-balanced (asp1..N) so any digit must pass.
		"http://asp6.toonamiaftermath.com:1934/livehttporigin/est/gYAkxI-najmUm-chunklist.m3u8",
		"http://asp1.toonamiaftermath.com:1934/livehttporigin/movies/gYAkxI-l6sJtY-chunklist.m3u8",
		"http://asp12.toonamiaftermath.com:1934/livehttporigin/radio/x-media-seg.ts",
		// pst attaches ?streamDelay=180 to its variant URL.
		"http://asp6.toonamiaftermath.com:1934/livehttporigin/est/gYAkxI-VQnF27-chunklist.m3u8?streamDelay=180",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			got, err := v.ValidateReference(context.Background(), raw, TrustModeBundledToonami)
			if err != nil {
				t.Fatalf("ValidateReference(%q) error = %v, want nil", raw, err)
			}
			if got != raw {
				t.Fatalf("ValidateReference(%q) = %q, want unchanged", raw, got)
			}
		})
	}
}

func staticResolver(mapping map[string][]net.IP) DNSResolver {
	return func(_ context.Context, host string) ([]net.IP, error) {
		if ip := net.ParseIP(host); ip != nil {
			return []net.IP{ip}, nil
		}
		return mapping[host], nil
	}
}

func rewriteHostClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	serverAddr := strings.TrimPrefix(srv.URL, "http://")
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, serverAddr)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}
