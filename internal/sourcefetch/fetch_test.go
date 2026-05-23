package sourcefetch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestIsPublicRoutableRejectsNormativeDeniedPrefixes(t *testing.T) {
	t.Parallel()

	denied := []string{
		"0.0.0.0",
		"10.0.0.1",
		"100.64.0.1",
		"127.0.0.1",
		"169.254.169.254",
		"172.16.0.1",
		"192.0.0.8",
		"192.0.2.1",
		"192.88.99.1",
		"192.168.1.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"224.0.0.1",
		"240.0.0.1",
		"::",
		"::1",
		"64:ff9b::1",
		"64:ff9b:1::1",
		"100::1",
		"2001::1",
		"2001:db8::1",
		"2002::1",
		"3fff::1",
		"fc00::1",
		"fe80::1",
		"ff00::1",
		"::ffff:192.168.1.2",
	}

	for _, raw := range denied {
		addr := netip.MustParseAddr(raw)
		if IsPublicRoutable(addr) {
			t.Fatalf("IsPublicRoutable(%q) = true, want false", raw)
		}
	}
}

func TestReadCappedBodyUsesLimitReader(t *testing.T) {
	t.Parallel()

	_, err := ReadCappedBody(strings.NewReader("12345"), 4)
	if err == nil {
		t.Fatal("ReadCappedBody() error = nil, want capped body error")
	}
	if !strings.Contains(err.Error(), "exceeds 4 bytes") {
		t.Fatalf("ReadCappedBody() error = %q, want exceeds 4 bytes", err)
	}
}

func TestFetcherRejectsUserinfoAndIPLiteral(t *testing.T) {
	ctx := context.Background()
	fetcher := Fetcher{
		Resolver: staticResolver{"media.example": {"93.184.216.34"}},
	}
	limits := Limits{MaxBytes: 1}

	for _, rawURL := range []string{
		"https://user:pass@media.example/file.torrent",
		"https://93.184.216.34/file.torrent",
		"https://[2606:2800:220:1:248:1893:25c8:1946]/file.torrent",
	} {
		_, err := fetcher.Fetch(ctx, http.MethodGet, rawURL, limits, Condition{})
		if err == nil {
			t.Fatalf("Fetch(%q) error = nil, want rejection", rawURL)
		}
	}
}

func TestFetcherDoesNotHonorEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")

	dialer := &recordingDialer{err: errors.New("dial stopped")}
	fetcher := Fetcher{
		Resolver:    staticResolver{"media.example": {"93.184.216.34"}},
		DialContext: dialer.DialContext,
	}

	_, err := fetcher.Fetch(context.Background(), http.MethodGet, "https://media.example/file.torrent", Limits{MaxBytes: 1}, Condition{})
	if err == nil || !strings.Contains(err.Error(), "dial stopped") {
		t.Fatalf("Fetch() error = %v, want dial stopped", err)
	}
	if len(dialer.addrs) != 1 {
		t.Fatalf("dial count = %d, want 1", len(dialer.addrs))
	}
	if dialer.addrs[0] != "93.184.216.34:443" {
		t.Fatalf("dialed addr = %q, want validated target IP", dialer.addrs[0])
	}
}

func TestValidateTargetHonorsAllowedSchemes(t *testing.T) {
	fetcher := Fetcher{
		Resolver: staticResolver{"media.example": {"93.184.216.34"}},
	}
	limits := Limits{
		AllowedSchemes: []string{"http", "https"},
	}

	if _, err := fetcher.ValidateTarget(context.Background(), mustURL(t, "http://media.example/catalog.json"), limits); err != nil {
		t.Fatalf("ValidateTarget(http) error = %v, want nil", err)
	}
	if _, err := fetcher.ValidateTarget(context.Background(), mustURL(t, "ftp://media.example/catalog.json"), limits); err == nil {
		t.Fatal("ValidateTarget(ftp) error = nil, want rejection")
	}
}

func TestValidateTargetNormalizesAllowedHosts(t *testing.T) {
	fetcher := Fetcher{
		Resolver: staticResolver{"trusted.example": {"93.184.216.34"}},
	}
	limits := Limits{
		AllowedHosts: map[string]struct{}{"trusted.example": {}},
	}

	if _, err := fetcher.ValidateTarget(context.Background(), mustURL(t, "https://Trusted.Example./catalog.json"), limits); err != nil {
		t.Fatalf("ValidateTarget(trusted host) error = %v, want nil", err)
	}
	if _, err := fetcher.ValidateTarget(context.Background(), mustURL(t, "https://other.example/catalog.json"), limits); err == nil {
		t.Fatal("ValidateTarget(other host) error = nil, want allowlist rejection")
	}
}

func TestResolvePublicTargetIPRejectsMixedAnswers(t *testing.T) {
	for name, answers := range map[string][]string{
		"mixed-private":     {"93.184.216.34", "192.168.1.1"},
		"mixed-special-use": {"93.184.216.34", "198.51.100.1"},
	} {
		resolver := staticResolver{name: answers}
		if _, err := ResolvePublicTargetIP(context.Background(), resolver, name, false); err == nil {
			t.Fatalf("ResolvePublicTargetIP(%s) error = nil, want mixed answer rejection", name)
		}
	}
}

func TestFetcherRejectsRedirectUserinfoBeforeFollow(t *testing.T) {
	requests := 0
	server, dialer := newPinnedHTTPFetchServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "http://user:pass@media.example/file.torrent", http.StatusFound)
	}))
	defer server.Close()

	fetcher := Fetcher{
		Resolver:    staticResolver{"media.example": {"93.184.216.34"}},
		DialContext: dialer.DialContext,
	}
	limits := Limits{
		MaxBytes:       32,
		MaxRedirects:   3,
		AllowedSchemes: []string{"http", "https"},
	}

	_, err := fetcher.Fetch(context.Background(), http.MethodGet, "http://media.example/start", limits, Condition{})
	if err == nil {
		t.Fatal("Fetch() error = nil, want redirect userinfo rejection")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestFetcherDialsValidatedIPAndPreservesHostHeader(t *testing.T) {
	var gotHost string
	server, dialer := newPinnedHTTPFetchServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	fetcher := Fetcher{
		Resolver:    staticResolver{"media.example": {"93.184.216.34"}},
		DialContext: dialer.DialContext,
	}
	limits := Limits{
		MaxBytes:       32,
		AllowedSchemes: []string{"http", "https"},
	}

	resp, err := fetcher.Fetch(context.Background(), http.MethodGet, "http://media.example/file.torrent", limits, Condition{})
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil", err)
	}
	if string(resp.Body) != "ok" {
		t.Fatalf("body = %q, want ok", resp.Body)
	}
	if gotHost != "media.example" {
		t.Fatalf("Host header = %q, want media.example", gotHost)
	}
	if len(dialer.addrs) != 1 {
		t.Fatalf("dial count = %d, want 1", len(dialer.addrs))
	}
	if dialer.addrs[0] != "93.184.216.34:80" {
		t.Fatalf("dialed addr = %q, want validated target IP", dialer.addrs[0])
	}
}

func TestRoundTripperPinsTLSNameAndDisablesProxyAndCompression(t *testing.T) {
	baseTLS := &tls.Config{ServerName: "base.example"}
	base := &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: baseTLS,
	}
	fetcher := Fetcher{Transport: base}
	target := Target{
		URL:      mustURL(t, "https://media.example/file.torrent"),
		Hostname: "media.example",
		DialAddr: "93.184.216.34:443",
	}

	transport, cleanup := fetcher.roundTripper(target)
	defer cleanup()

	if transport == base {
		t.Fatal("roundTripper returned base transport, want clone")
	}
	if transport.Proxy != nil {
		t.Fatal("transport.Proxy != nil, want nil")
	}
	if !transport.DisableCompression {
		t.Fatal("transport.DisableCompression = false, want true")
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("transport.TLSClientConfig = nil")
	}
	if transport.TLSClientConfig.ServerName != "media.example" {
		t.Fatalf("TLS ServerName = %q, want media.example", transport.TLSClientConfig.ServerName)
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS InsecureSkipVerify = true, want false")
	}
	if base.TLSClientConfig != baseTLS || base.TLSClientConfig.ServerName != "base.example" {
		t.Fatal("base transport TLS config was mutated")
	}
	if base.Proxy == nil {
		t.Fatal("base transport Proxy was mutated")
	}
}

func TestFetcherRedirectRevalidatesEachHop(t *testing.T) {
	requests := 0
	server, dialer := newPinnedHTTPFetchServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "http://private.example/file.torrent", http.StatusFound)
	}))
	defer server.Close()

	fetcher := Fetcher{
		Resolver: staticResolver{
			"media.example":   {"93.184.216.34"},
			"private.example": {"192.168.1.1"},
		},
		DialContext: dialer.DialContext,
	}
	limits := Limits{
		MaxBytes:       32,
		MaxRedirects:   3,
		AllowedSchemes: []string{"http", "https"},
	}

	_, err := fetcher.Fetch(context.Background(), http.MethodGet, "http://media.example/start", limits, Condition{})
	if err == nil {
		t.Fatal("Fetch() error = nil, want redirect target IP rejection")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

type staticResolver map[string][]string

func (r staticResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	answers, ok := r[host]
	if !ok {
		return nil, fmt.Errorf("unexpected lookup for %q", host)
	}
	return answers, nil
}

type recordingDialer struct {
	addrs []string
	err   error
}

func (d *recordingDialer) DialContext(_ context.Context, _ string, addr string) (net.Conn, error) {
	d.addrs = append(d.addrs, addr)
	if d.err != nil {
		return nil, d.err
	}
	return nil, errors.New("recording dialer stopped")
}

type pinnedDialer struct {
	addr  string
	addrs []string
}

func (d *pinnedDialer) DialContext(ctx context.Context, network string, addr string) (net.Conn, error) {
	d.addrs = append(d.addrs, addr)
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, d.addr)
}

func newPinnedFetchServer(t *testing.T, handler http.Handler, tlsEnabled bool) (*httptest.Server, *pinnedDialer) {
	t.Helper()

	var server *httptest.Server
	if tlsEnabled {
		server = httptest.NewTLSServer(handler)
	} else {
		server = httptest.NewServer(handler)
	}
	t.Cleanup(server.Close)

	return server, &pinnedDialer{addr: server.Listener.Addr().String()}
}

func newPinnedHTTPFetchServer(t *testing.T, handler http.Handler) (*httptest.Server, *pinnedDialer) {
	t.Helper()

	return newPinnedFetchServer(t, handler, false)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return u
}
