package aux

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type proxyTokenKind string

const (
	proxyTokenProbe proxyTokenKind = "probe"
	proxyTokenPlay  proxyTokenKind = "play"
)

type proxyToken struct {
	token     string
	kind      proxyTokenKind
	upstream  string
	expiresAt time.Time
	used      bool
	cancel    context.CancelFunc
}

type proxyStore struct {
	mu     sync.Mutex
	tokens []proxyToken
	now    func() time.Time
}

func newProxyHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 0,
			}).DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableKeepAlives:     true,
		},
		// No whole-body Timeout: the play token's GET is long-lived for the
		// duration of the cast. Per-stage timeouts above bound the dial /
		// TLS / header phases.
	}
}

func (a *Adapter) MountPublicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /internal/aux-proxy/", a.handleProxy)
}

func (a *Adapter) mintProxyURL(kind proxyTokenKind, upstream string, ttl time.Duration) (string, error) {
	switch kind {
	case proxyTokenProbe, proxyTokenPlay:
	default:
		return "", fmt.Errorf("unknown proxy token kind %q", kind)
	}
	u, err := validateStreamURL(upstream)
	if err != nil {
		return "", err
	}
	token, err := randomProxyToken()
	if err != nil {
		return "", err
	}
	_, cancel := context.WithCancel(context.Background())
	a.proxy.add(proxyToken{
		token:     token,
		kind:      kind,
		upstream:  u.String(),
		expiresAt: a.proxy.currentTime().Add(ttl),
		cancel:    cancel,
	})
	return fmt.Sprintf("http://127.0.0.1:%d/internal/aux-proxy/?kind=%s&aux_token=%s", a.httpPort, kind, url.QueryEscape(token)), nil
}

func (a *Adapter) handleProxy(w http.ResponseWriter, r *http.Request) {
	if !remoteAddrIsLoopback(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	rawToken := r.URL.Query().Get("aux_token")
	kind := proxyTokenKind(r.URL.Query().Get("kind"))
	if kind == "" {
		kind = proxyTokenPlay
	}
	tok, ok := a.proxy.consume(rawToken, kind)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tok.upstream, nil)
	if err != nil {
		http.Error(w, "bad upstream", http.StatusBadGateway)
		return
	}
	resp, err := a.proxyHTTP.Do(req)
	if err != nil {
		http.Error(w, "AUX input unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	// 3xx rejection. Only fires for non-redirected 3xx (304 Not Modified etc.)
	// because CheckRedirect: http.ErrUseLastResponse stops the client at the
	// first 3xx response. Defense in depth: if a future maintainer drops
	// CheckRedirect, this block keeps SSRF rejection working.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		http.Error(w, "AUX input redirected", http.StatusBadGateway)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Error(w, "AUX input returned non-2xx status", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	_, _ = io.Copy(w, resp.Body)
}

func (s *proxyStore) add(tok proxyToken) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = append(s.tokens, tok)
}

// consume looks up a token by constant-time per-slot comparison. The whole-table
// iteration is not constant-time across hit-then-rejected vs absent outcomes;
// loopback binding + short TTL make whole-table timing attacks impractical, and
// constant-time-across-rejection would force iterating every slot for every
// request even in the steady state.
func (s *proxyStore) consume(raw string, kind proxyTokenKind) (proxyToken, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.currentTime()
	for i := range s.tokens {
		tok := &s.tokens[i]
		if subtle.ConstantTimeCompare([]byte(tok.token), []byte(raw)) != 1 {
			continue
		}
		if tok.used || tok.kind != kind || now.After(tok.expiresAt) {
			return proxyToken{}, false
		}
		tok.used = true
		return *tok, true
	}
	return proxyToken{}, false
}

func (s *proxyStore) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func randomProxyToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint proxy token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func remoteAddrIsLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
