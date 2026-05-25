package aux

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestAdapterProvidesPublicRoutes(t *testing.T) {
	var _ adapters.PublicRouteProvider = (*Adapter)(nil)
}

func TestMintProxyURLMintsDistinctSingleUseTokens(t *testing.T) {
	a := newTestAdapter(t)
	a.now = func() time.Time { return time.Unix(1_700_000_000, 0) }

	probeURL, err := a.mintProxyURL(proxyTokenProbe, "http://capture-host:8090/aux.wav", 5*time.Second)
	if err != nil {
		t.Fatalf("mint probe proxy URL: %v", err)
	}
	playURL, err := a.mintProxyURL(proxyTokenPlay, "http://capture-host:8090/aux.wav", time.Minute)
	if err != nil {
		t.Fatalf("mint play proxy URL: %v", err)
	}

	probe := parseProxyURL(t, probeURL)
	play := parseProxyURL(t, playURL)
	if probe.Host != "127.0.0.1:32500" || play.Host != "127.0.0.1:32500" {
		t.Fatalf("proxy hosts = %q, %q; want 127.0.0.1:32500", probe.Host, play.Host)
	}
	if probe.Path != "/internal/aux-proxy/" || play.Path != "/internal/aux-proxy/" {
		t.Fatalf("proxy paths = %q, %q; want /internal/aux-proxy/", probe.Path, play.Path)
	}
	if probe.Query().Get("kind") != string(proxyTokenProbe) {
		t.Fatalf("probe kind = %q, want %q", probe.Query().Get("kind"), proxyTokenProbe)
	}
	if play.Query().Get("kind") != string(proxyTokenPlay) {
		t.Fatalf("play kind = %q, want %q", play.Query().Get("kind"), proxyTokenPlay)
	}

	probeToken := probe.Query().Get("aux_token")
	playToken := play.Query().Get("aux_token")
	if probeToken == "" || playToken == "" {
		t.Fatalf("minted empty token(s): probe=%q play=%q", probeToken, playToken)
	}
	if probeToken == playToken {
		t.Fatalf("probe and play tokens matched: %q", probeToken)
	}
	if _, ok := a.proxy.consume(probeToken, proxyTokenPlay); ok {
		t.Fatal("probe token consumed as play token")
	}
	if _, ok := a.proxy.consume(probeToken, proxyTokenProbe); !ok {
		t.Fatal("probe token was not consumable as probe token")
	}
	if _, ok := a.proxy.consume(probeToken, proxyTokenProbe); ok {
		t.Fatal("probe token was consumable twice")
	}
	if _, ok := a.proxy.consume(playToken, proxyTokenPlay); !ok {
		t.Fatal("play token was not consumable as play token")
	}
}

func TestProxyTokenEqualRejectsDifferentLength(t *testing.T) {
	if !proxyTokenEqual("abcdef", "abcdef") {
		t.Fatal("proxyTokenEqual rejected exact token")
	}
	if proxyTokenEqual("abcdef", "abc") {
		t.Fatal("proxyTokenEqual accepted short raw token")
	}
	if proxyTokenEqual("abcdef", "abcdef0") {
		t.Fatal("proxyTokenEqual accepted long raw token")
	}
	if proxyTokenEqual("abcdef", "abcdeg") {
		t.Fatal("proxyTokenEqual accepted same-length mismatch")
	}
}

func TestProxyTokenEqualReadsStoredTokenLength(t *testing.T) {
	calls := 0
	if proxyTokenEqualWithRaw("abcdef", 3, func(i int) byte {
		calls++
		if i < len("abc") {
			return "abc"[i]
		}
		return 0
	}) {
		t.Fatal("proxyTokenEqualWithRaw accepted short raw token")
	}
	if calls != len("abcdef") {
		t.Fatalf("raw byte reads = %d, want stored token length %d", calls, len("abcdef"))
	}
}

func TestProxyStoreConsumeRejectsDifferentLengthWithoutUsingToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := proxyStore{now: func() time.Time { return now }}
	store.add(proxyToken{
		token:     "abcdef",
		kind:      proxyTokenPlay,
		upstream:  "http://capture-host:8090/aux.wav",
		expiresAt: now.Add(time.Minute),
	})

	if _, ok := store.consume("abc", proxyTokenPlay); ok {
		t.Fatal("short raw token consumed stored token")
	}
	if _, ok := store.consume("abcdef0", proxyTokenPlay); ok {
		t.Fatal("long raw token consumed stored token")
	}
	if _, ok := store.consume("abcdef", proxyTokenPlay); !ok {
		t.Fatal("exact same-length raw token was not consumed")
	}
}

func TestProxyRejectsUpstreamRedirect(t *testing.T) {
	var requests int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		http.Redirect(w, r, "http://evil.test/", http.StatusFound)
	}))
	defer upstream.Close()

	a := newTestAdapter(t)
	proxyURL, err := a.mintProxyURL(proxyTokenPlay, upstream.URL, time.Minute)
	if err != nil {
		t.Fatalf("mint proxy URL: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, proxyURL, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	rr := httptest.NewRecorder()

	a.handleProxy(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "AUX input redirected") {
		t.Fatalf("body = %q, want AUX input redirected", rr.Body.String())
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("upstream requests = %d, want 1", got)
	}
}

func TestProxyRejectsUpstreamNon2xx(t *testing.T) {
	var requests int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		http.Error(w, "missing aux", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	a := newTestAdapter(t)
	proxyURL, err := a.mintProxyURL(proxyTokenPlay, upstream.URL, time.Minute)
	if err != nil {
		t.Fatalf("mint proxy URL: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, proxyURL, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	rr := httptest.NewRecorder()

	a.handleProxy(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "AUX input returned non-2xx status") {
		t.Fatalf("body = %q, want AUX input returned non-2xx status", rr.Body.String())
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("upstream requests = %d, want 1", got)
	}
}

func TestProxyRejectsNonLoopbackClient(t *testing.T) {
	var requests int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("aux"))
	}))
	defer upstream.Close()

	a := newTestAdapter(t)
	proxyURL, err := a.mintProxyURL(proxyTokenPlay, upstream.URL, time.Minute)
	if err != nil {
		t.Fatalf("mint proxy URL: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, proxyURL, nil)
	req.RemoteAddr = "192.0.2.1:54321"
	rr := httptest.NewRecorder()

	a.handleProxy(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%q", rr.Code, rr.Body.String())
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("upstream requests after forbidden request = %d, want 0", got)
	}

	req = httptest.NewRequest(http.MethodGet, proxyURL, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	rr = httptest.NewRecorder()
	a.handleProxy(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("loopback retry status = %d, want 200 body=%q", rr.Code, rr.Body.String())
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("upstream requests after loopback retry = %d, want 1", got)
	}
}

func TestProxyHTTPClientHasBoundedStageTimeouts(t *testing.T) {
	client := newProxyHTTPClient()
	if client.CheckRedirect == nil {
		t.Fatal("CheckRedirect is nil")
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	if err := client.CheckRedirect(req, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect error = %v, want http.ErrUseLastResponse", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *http.Transport", client.Transport)
	}
	if transport.DialContext == nil {
		t.Fatal("Transport.DialContext is nil")
	}
	if transport.TLSHandshakeTimeout != 5*time.Second {
		t.Fatalf("TLSHandshakeTimeout = %v, want 5s", transport.TLSHandshakeTimeout)
	}
	if transport.ResponseHeaderTimeout != 5*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v, want 5s", transport.ResponseHeaderTimeout)
	}
	if transport.ExpectContinueTimeout != time.Second {
		t.Fatalf("ExpectContinueTimeout = %v, want 1s", transport.ExpectContinueTimeout)
	}
	if !transport.DisableKeepAlives {
		t.Fatal("DisableKeepAlives = false, want true")
	}
	if client.Timeout != 0 {
		t.Fatalf("client.Timeout = %v, want 0", client.Timeout)
	}
}

func parseProxyURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}
