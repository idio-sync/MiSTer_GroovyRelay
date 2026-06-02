package streams

import "testing"

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
		"http://169.254.169.254/latest/meta",     // link-local / cloud metadata
		"http://[::ffff:169.254.169.254]/x",      // IPv4-mapped metadata (must Unmap)
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
