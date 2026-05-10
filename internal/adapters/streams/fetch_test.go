package streams

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestFetchRejectsLoopbackByDefault(t *testing.T) {
	f := secureFetcher{resolver: staticResolver{"example.test": []string{"127.0.0.1"}}}
	_, err := f.Fetch(t.Context(), "https://example.test/catalog.json", fetchLimits{MaxBytes: 1024})
	if err == nil {
		t.Fatal("loopback target accepted")
	}
}

func TestFetchRejectsSpecialUseIPRanges(t *testing.T) {
	for _, addr := range []string{
		"::1",
		"::",
		"fc00::1",
		"fe80::1",
		"ff00::1",
		"::ffff:192.168.1.2",
		"203.0.113.10",
		"198.18.0.1",
		"198.51.100.1",
		"192.0.2.1",
		"2001:db8::1",
	} {
		t.Run(addr, func(t *testing.T) {
			if isPublicIP(netip.MustParseAddr(addr)) {
				t.Fatalf("%s classified public", addr)
			}
		})
	}
}

func TestFetchRejectsRedirectToPrivateIP(t *testing.T) {
	dialer, transport := newPinnedFetchServer(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Host != "public.example" {
			t.Fatalf("unexpected first request host %s", req.Host)
		}
		http.Redirect(w, req, "https://private.example/catalog.json", http.StatusFound)
	})
	f := secureFetcher{
		resolver: sequenceResolver{
			"public.example":  {[]string{"93.184.216.34"}},
			"private.example": {[]string{"192.168.1.10"}},
		},
		transport:   transport,
		dialContext: dialer.DialContext,
	}
	_, err := f.Fetch(t.Context(), "https://public.example/catalog.json", fetchLimits{MaxBytes: 1024})
	if err == nil {
		t.Fatal("redirect revalidation did not reject private target")
	}
	if !dialer.dialed("93.184.216.34") {
		t.Fatalf("dialed %v, want validated public IP", dialer.addrs)
	}
}

func TestFetchResolvesOnceAndDialsValidatedIP(t *testing.T) {
	resolver := &rebindResolver{
		first:  []string{"93.184.216.34"},
		second: []string{"192.168.1.20"},
	}
	dialer := &recordingDialer{}
	f := secureFetcher{resolver: resolver, dialContext: dialer.DialContext}
	_, _ = f.Fetch(t.Context(), "https://media.example/catalog.json", fetchLimits{MaxBytes: 1024})
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
	if !strings.Contains(dialer.addr, "93.184.216.34") {
		t.Fatalf("dialed %q, want validated IP 93.184.216.34", dialer.addr)
	}
}

func TestFetchInjectedHTTPTransportDialsValidatedIP(t *testing.T) {
	resolver := &rebindResolver{
		first:  []string{"93.184.216.34"},
		second: []string{"192.168.1.20"},
	}
	baseTransportDialer := &recordingDialer{}
	dialer := &recordingDialer{}
	f := secureFetcher{
		resolver:    resolver,
		transport:   &http.Transport{DialContext: baseTransportDialer.DialContext},
		dialContext: dialer.DialContext,
	}

	_, _ = f.Fetch(t.Context(), "https://media.example/catalog.json", fetchLimits{MaxBytes: 1024})

	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
	if !strings.Contains(dialer.addr, "93.184.216.34") {
		t.Fatalf("dialed %q, want validated IP 93.184.216.34", dialer.addr)
	}
	if baseTransportDialer.addr != "" {
		t.Fatalf("base transport dialed %q; standard transports must be pinned to the validated IP", baseTransportDialer.addr)
	}
}

func TestSecureFetcherTransportRequiresHTTPTransport(t *testing.T) {
	var f secureFetcher
	var _ *http.Transport = f.transport
}

func TestFetchRemoteProviderRespectsHostAllowlistAfterRedirect(t *testing.T) {
	// Spec §"RemoteProviderAllowedHosts": when the allowlist is non-empty,
	// remote-added providers may fetch catalogs only from those hosts after
	// redirect resolution. Allowlist must re-apply on each redirect hop.
	allow, err := normalizeHostSet([]string{"trusted.example"})
	if err != nil {
		t.Fatalf("normalizeHostSet: %v", err)
	}
	dialer, transport := newPinnedFetchServer(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Host == "trusted.example" {
			http.Redirect(w, req, "https://untrusted.example/catalog.json", http.StatusFound)
			return
		}
		t.Fatalf("untrusted host should never be fetched, got %s", req.Host)
	})
	f := secureFetcher{
		resolver: staticResolver{
			"trusted.example":   []string{"93.184.216.34"},
			"untrusted.example": []string{"8.8.8.8"},
		},
		transport:   transport,
		dialContext: dialer.DialContext,
	}
	_, err = f.Fetch(t.Context(), "https://trusted.example/catalog.json", fetchLimits{
		MaxBytes:     1024,
		AllowedHosts: allow,
	})
	if err == nil {
		t.Fatal("redirect to non-allowlisted host should be rejected")
	}
	if !dialer.dialed("93.184.216.34") {
		t.Fatalf("dialed %v, want trusted host validated IP", dialer.addrs)
	}
}

func TestFetchRejectsTooManyRedirects(t *testing.T) {
	dialer, transport := newPinnedFetchServer(t, func(w http.ResponseWriter, req *http.Request) {
		next := map[string]string{
			"one.example":   "https://two.example/catalog.json",
			"two.example":   "https://three.example/catalog.json",
			"three.example": "https://four.example/catalog.json",
			"four.example":  "https://five.example/catalog.json",
		}[req.Host]
		http.Redirect(w, req, next, http.StatusFound)
	})
	f := secureFetcher{
		resolver: staticResolver{
			"one.example":   []string{"93.184.216.34"},
			"two.example":   []string{"93.184.216.35"},
			"three.example": []string{"93.184.216.36"},
			"four.example":  []string{"93.184.216.37"},
			"five.example":  []string{"93.184.216.38"},
		},
		transport:   transport,
		dialContext: dialer.DialContext,
	}
	_, err := f.Fetch(t.Context(), "https://one.example/catalog.json", fetchLimits{MaxBytes: 1024})
	if err == nil {
		t.Fatal("redirect chain over three hops accepted")
	}
	if len(dialer.addrs) != 4 {
		t.Fatalf("dial count = %d, want 4", len(dialer.addrs))
	}
}

func TestFetchRejectsResponseOverMaxBytes(t *testing.T) {
	dialer, transport := newPinnedFetchServer(t, func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("12345"))
	})
	f := secureFetcher{
		resolver:    staticResolver{"media.example": []string{"93.184.216.34"}},
		transport:   transport,
		dialContext: dialer.DialContext,
	}
	_, err := f.Fetch(t.Context(), "https://media.example/catalog.json", fetchLimits{MaxBytes: 4})
	if err == nil {
		t.Fatal("response larger than max bytes accepted")
	}
}

type staticResolver map[string][]string

func (r staticResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if ips, ok := r[host]; ok {
		return ips, nil
	}
	return nil, fmt.Errorf("unexpected lookup for %s", host)
}

type sequenceResolver map[string][][]string

func (r sequenceResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	seq := r[host]
	if len(seq) == 0 {
		return nil, fmt.Errorf("unexpected lookup for %s", host)
	}
	out := seq[0]
	r[host] = seq[1:]
	return out, nil
}

type rebindResolver struct {
	first  []string
	second []string
	calls  int
}

func (r *rebindResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	r.calls++
	if r.calls == 1 {
		return r.first, nil
	}
	return r.second, nil
}

type recordingDialer struct {
	addr string
}

func (d *recordingDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d.addr = addr
	return nil, errors.New("stop after recording dial address")
}

type testServerDialer struct {
	serverAddr string
	addrs      []string
}

func (d *testServerDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d.addrs = append(d.addrs, addr)
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, d.serverAddr)
}

func (d *testServerDialer) dialed(ip string) bool {
	for _, addr := range d.addrs {
		if strings.Contains(addr, ip) {
			return true
		}
	}
	return false
}

func newPinnedFetchServer(t *testing.T, handler http.HandlerFunc) (*testServerDialer, *http.Transport) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	dialer := &testServerDialer{serverAddr: server.Listener.Addr().String()}
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	return dialer, transport
}
