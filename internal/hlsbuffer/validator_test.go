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

func TestBundledToonamiValidationRejectsWrongHostOrPath(t *testing.T) {
	v := URLValidator{}
	cases := []string{
		"http://example.com:3000/est/playlist.m3u8",
		"http://api.toonamiaftermath.com:3000/evil/playlist.m3u8",
		"http://api.toonamiaftermath.com/est/playlist.m3u8",
		"http://user@api.toonamiaftermath.com:3000/est/playlist.m3u8",
		"http://api.toonamiaftermath.com:3000/est/playlist.m3u8?token=x",
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
