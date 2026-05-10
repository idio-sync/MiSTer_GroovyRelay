package dlna

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// urlvalidator_test.go covers validateMediaURL: scheme rejection,
// address classification, redirect-chase prevalidation including the
// SSRF-defense path (a redirect to 169.254.169.254 must be rejected).
//
// DNS is mocked via dnsResolverFunc injection; HTTP redirect chases
// are exercised against httptest.NewServer instances bound to
// 127.0.0.1 (the validator must accept these for the test infra to
// work, so the validator's loopback rejection is bypassed
// specifically for the test client by injecting a resolver that
// reports the test-server host as private).

// newTestValidator returns a validator with the given resolver and a
// permissive client. For tests that don't exercise redirect chases,
// pass a resolver that maps a fixed hostname to a chosen IP.
func newTestValidator(resolver dnsResolverFunc) *urlValidator {
	return &urlValidator{
		resolver: resolver,
		client: &http.Client{
			Timeout: 0,
		},
	}
}

// staticResolver returns a resolver that always returns the given
// IPs, regardless of the input hostname. Useful for classification
// tests where the hostname doesn't matter — we just want to model
// the resolved IP set.
func staticResolver(ips ...string) dnsResolverFunc {
	parsed := make([]net.IP, 0, len(ips))
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil {
			panic("staticResolver: bad IP " + s)
		}
		parsed = append(parsed, ip)
	}
	return func(ctx context.Context, host string) ([]net.IP, error) {
		return parsed, nil
	}
}

// failingResolver always returns a DNS error. Used to cover the
// "host won't resolve" branch in parseAndClassify.
func failingResolver(ctx context.Context, host string) ([]net.IP, error) {
	return nil, fmt.Errorf("simulated DNS failure")
}

func TestValidateMediaURL_BadScheme(t *testing.T) {
	cases := []string{
		"file:///etc/passwd",
		"ftp://example.com/foo",
		"rtsp://example.com/stream",
		"data:text/plain,hello",
	}
	v := newTestValidator(staticResolver("192.168.1.1"))
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := v.validate(context.Background(), raw, PolicyPrivateOnly)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrSchemeNotAllowed) {
				t.Errorf("err = %v, want errors.Is ErrSchemeNotAllowed", err)
			}
		})
	}
}

func TestValidateMediaURL_MalformedURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"garbage no scheme", "not a url"},
		{"broken brackets", "http://[broken"},
		{"relative path", "/foo/bar"},
	}
	v := newTestValidator(staticResolver("192.168.1.1"))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.validate(context.Background(), tc.raw, PolicyPrivateOnly)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrInvalidURL) {
				t.Errorf("err = %v, want errors.Is ErrInvalidURL", err)
			}
		})
	}
}

func TestValidateMediaURL_LoopbackRejected(t *testing.T) {
	// Loopback is rejected under both policies.
	for _, policy := range []AddressPolicy{PolicyPrivateOnly, PolicyAllowPublic} {
		v := newTestValidator(staticResolver("127.0.0.1"))
		_, err := v.validate(context.Background(), "http://localhost/foo", policy)
		if err == nil {
			t.Fatalf("policy=%v: expected error, got nil", policy)
		}
		if !errors.Is(err, ErrAddressNotAllowed) {
			t.Errorf("policy=%v: err = %v, want ErrAddressNotAllowed", policy, err)
		}
	}
}

func TestValidateMediaURL_LinkLocalRejected(t *testing.T) {
	// 169.254.169.254 is the cloud-metadata SSRF target. Must reject.
	v := newTestValidator(staticResolver("169.254.169.254"))
	_, err := v.validate(context.Background(), "http://metadata.local/foo", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrAddressNotAllowed) {
		t.Errorf("err = %v, want ErrAddressNotAllowed", err)
	}
}

func TestValidateMediaURL_MulticastRejected(t *testing.T) {
	v := newTestValidator(staticResolver("224.0.0.1"))
	_, err := v.validate(context.Background(), "http://mcast.example/foo", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrAddressNotAllowed) {
		t.Errorf("err = %v, want ErrAddressNotAllowed", err)
	}
}

func TestValidateMediaURL_UnspecifiedRejected(t *testing.T) {
	v := newTestValidator(staticResolver("0.0.0.0"))
	_, err := v.validate(context.Background(), "http://any.example/foo", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrAddressNotAllowed) {
		t.Errorf("err = %v, want ErrAddressNotAllowed", err)
	}
}

func TestValidateMediaURL_PrivateAccepted_PrivateOnlyPolicy(t *testing.T) {
	// 192.168.1.1 doesn't actually answer in the test environment,
	// so we point the URL at a real httptest server (loopback) but
	// inject a resolver that returns 192.168.1.1. The HEAD request
	// then dials 192.168.1.1, which will fail to connect — but the
	// validator still classified the address as private and would
	// have accepted it on a real network. To exercise the success
	// path end-to-end we need the resolver to point at a real
	// reachable address that ALSO classifies as private, which
	// loopback does NOT (loopback is rejected). Instead, we use the
	// httptest server's actual loopback address as a resolved IP
	// AND we accept that the validator will reject 127.0.0.1 — so
	// here we just exercise parseAndClassify directly to confirm
	// the private-IP success path.
	v := newTestValidator(staticResolver("192.168.1.1", "10.0.0.5"))
	_, isPrivate, err := v.parseAndClassify(context.Background(), "http://example.local/foo", PolicyPrivateOnly)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !isPrivate {
		t.Errorf("isPrivate = false, want true")
	}
}

func TestValidateMediaURL_PublicRejected_PrivateOnlyPolicy(t *testing.T) {
	v := newTestValidator(staticResolver("1.2.3.4"))
	_, _, err := v.parseAndClassify(context.Background(), "http://public.example/foo", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrAddressNotAllowed) {
		t.Errorf("err = %v, want ErrAddressNotAllowed", err)
	}
}

func TestValidateMediaURL_PublicAccepted_AllowPublicPolicy(t *testing.T) {
	v := newTestValidator(staticResolver("1.2.3.4"))
	_, isPrivate, err := v.parseAndClassify(context.Background(), "http://public.example/foo", PolicyAllowPublic)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if isPrivate {
		t.Errorf("isPrivate = true, want false (public address)")
	}
}

func TestValidateMediaURL_HostnameMultipleIPs_AllPrivate(t *testing.T) {
	v := newTestValidator(staticResolver("192.168.1.1", "10.0.0.1", "172.16.0.1"))
	_, isPrivate, err := v.parseAndClassify(context.Background(), "http://multi.local/foo", PolicyPrivateOnly)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !isPrivate {
		t.Errorf("isPrivate = false, want true")
	}
}

func TestValidateMediaURL_HostnameMultipleIPs_MixedPublicPrivate(t *testing.T) {
	v := newTestValidator(staticResolver("192.168.1.1", "1.2.3.4"))
	_, _, err := v.parseAndClassify(context.Background(), "http://mixed.example/foo", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrAddressNotAllowed) {
		t.Errorf("err = %v, want ErrAddressNotAllowed", err)
	}
}

func TestValidateMediaURL_HostnameMultipleIPs_DisallowedAmongPrivate(t *testing.T) {
	// 169.254.169.254 mixed with private 192.168.1.1 must reject
	// even under PolicyAllowPublic — link-local is always rejected.
	v := newTestValidator(staticResolver("192.168.1.1", "169.254.169.254"))
	_, _, err := v.parseAndClassify(context.Background(), "http://sneaky.example/foo", PolicyAllowPublic)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrAddressNotAllowed) {
		t.Errorf("err = %v, want ErrAddressNotAllowed", err)
	}
}

func TestValidateMediaURL_CGNATAcceptedAsPrivate(t *testing.T) {
	// RFC6598 100.64.0.0/10 is treated as private — operator's ISP
	// CGNAT shouldn't be classified as the public internet.
	v := newTestValidator(staticResolver("100.64.0.1"))
	_, isPrivate, err := v.parseAndClassify(context.Background(), "http://cgnat.example/foo", PolicyPrivateOnly)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !isPrivate {
		t.Errorf("isPrivate = false, want true (CGNAT is private)")
	}
}

func TestValidateMediaURL_ResolverFailureMappedToAddressDenial(t *testing.T) {
	v := newTestValidator(failingResolver)
	_, _, err := v.parseAndClassify(context.Background(), "http://no.dns/foo", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrAddressNotAllowed) {
		t.Errorf("err = %v, want ErrAddressNotAllowed", err)
	}
}

// ---- Redirect chase ----
//
// Redirect-chase tests use httptest.NewServer, which binds to
// loopback. To make the validator accept the loopback host for the
// HTTP request itself, we inject a resolver that maps the test
// server's host to a private RFC1918 address — but that would make
// the dial go to the wrong address. Instead, we accept that the
// HTTP-level chase tests ALSO need to bypass the loopback rejection
// for the test server's actual host.
//
// Approach: write a special test resolver that:
//   - maps the httptest server's hostname to a synthetic 192.168.x.x
//     IP for CLASSIFICATION purposes
//   - but the actual http.Client dial uses the URL's host:port via
//     the OS resolver, which on loopback works.
// This is awkward but unavoidable given the validator's correct
// rejection of loopback. The CHEATING resolver lies to the
// classifier; the http.Client still dials loopback because that's
// what's encoded in the URL. The validator tolerates this divergence
// because parseAndClassify happens before Do, and Do uses the URL
// string verbatim.

// hostMappingResolver returns a resolver that maps specific
// hostnames to specific IPs. Hostname not in the map gets a default
// 192.168.99.1 (private) so any incidental httptest hostname is
// classified as private.
func hostMappingResolver(t *testing.T, mapping map[string]string) dnsResolverFunc {
	return func(ctx context.Context, host string) ([]net.IP, error) {
		// Strip optional :port — httptest URLs are host:port, but
		// url.URL.Hostname() should already strip that. Defensive.
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
		if ip, ok := mapping[host]; ok {
			parsed := net.ParseIP(ip)
			if parsed == nil {
				return nil, fmt.Errorf("test resolver: bad IP %q", ip)
			}
			return []net.IP{parsed}, nil
		}
		// Default: pretend it's private so the classifier accepts.
		return []net.IP{net.ParseIP("192.168.99.1")}, nil
	}
}

func TestValidateMediaURL_RedirectChainAccepted(t *testing.T) {
	// Three-hop redirect chain: srv1 -> srv2 -> srv3 -> 200 OK.
	// Validator should follow all three and return Hops=3.
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv3.Close)
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv3.URL+"/3", http.StatusFound)
	}))
	t.Cleanup(srv2.Close)
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv2.URL+"/2", http.StatusFound)
	}))
	t.Cleanup(srv1.Close)
	srv0 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv1.URL+"/1", http.StatusFound)
	}))
	t.Cleanup(srv0.Close)

	v := newTestValidator(hostMappingResolver(t, nil))
	got, err := v.validate(context.Background(), srv0.URL+"/0", PolicyPrivateOnly)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got.Hops != 3 {
		t.Errorf("Hops = %d, want 3", got.Hops)
	}
	if !strings.HasPrefix(got.FinalURL, srv3.URL) {
		t.Errorf("FinalURL = %q, want prefix %q", got.FinalURL, srv3.URL)
	}
}

func TestValidateMediaURL_RedirectChainExceedsMax(t *testing.T) {
	// Four-hop chain — should fail with ErrTooManyRedirects.
	var srvs [5]*httptest.Server
	srvs[4] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srvs[4].Close)
	for i := 3; i >= 0; i-- {
		next := srvs[i+1].URL + "/next"
		i := i // capture
		srvs[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, next, http.StatusFound)
		}))
		t.Cleanup(srvs[i].Close)
	}
	v := newTestValidator(hostMappingResolver(t, nil))
	_, err := v.validate(context.Background(), srvs[0].URL+"/start", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Errorf("err = %v, want ErrTooManyRedirects", err)
	}
}

func TestValidateMediaURL_RedirectToDisallowedScheme(t *testing.T) {
	// 302 to file:// — validator must reject before following.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	v := newTestValidator(hostMappingResolver(t, nil))
	_, err := v.validate(context.Background(), srv.URL+"/start", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrSchemeNotAllowed) {
		t.Errorf("err = %v, want ErrSchemeNotAllowed", err)
	}
}

func TestValidateMediaURL_RedirectToCloudMetadata(t *testing.T) {
	// SSRF DEFENSE TEST: a public-looking initial URL that 302s to
	// 169.254.169.254 must be rejected. This is the headline reason
	// the adapter prevalidates instead of trusting FFmpeg.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://metadata.google.internal/computeMetadata/v1/", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	v := newTestValidator(hostMappingResolver(t, map[string]string{
		"metadata.google.internal": "169.254.169.254",
	}))
	_, err := v.validate(context.Background(), srv.URL+"/start", PolicyAllowPublic)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrAddressNotAllowed) {
		t.Errorf("err = %v, want ErrAddressNotAllowed", err)
	}
}

func TestValidateMediaURL_TimeoutOnRedirectFetch(t *testing.T) {
	// Server blocks until the request's context is cancelled. The
	// test cancels via a parent context with a short timeout. Using
	// r.Context().Done() (rather than a t.Cleanup channel) lets the
	// server handler return as soon as the client gives up, so
	// httptest.Server.Close doesn't have an active connection
	// blocking its drain loop on test exit.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	v := newTestValidator(hostMappingResolver(t, nil))
	_, err := v.validate(ctx, srv.URL+"/hang", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrRedirectFetchFail) {
		t.Errorf("err = %v, want ErrRedirectFetchFail", err)
	}
}

// ---- Public entry point smoke test ----

func TestValidateMediaURL_DefaultEntryPoint_RejectsBadScheme(t *testing.T) {
	// Smoke-test the public entry point uses defaultDNSResolver; we
	// only need to confirm the wiring works for an early-rejection
	// case (which never triggers DNS).
	_, err := validateMediaURL(context.Background(), "ftp://example.com/foo", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrSchemeNotAllowed) {
		t.Errorf("err = %v, want ErrSchemeNotAllowed", err)
	}
}

