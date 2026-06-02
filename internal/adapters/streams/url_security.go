package streams

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// validateUserProviderHost enforces the "allow LAN, block internals" posture
// (spec §7.1) on a user-supplied stream/page URL. It is purely syntactic and
// does NO DNS resolution: an IP-literal host is classified and accepted only if
// public or RFC1918/ULA-private; a hostname passes this gate and is re-checked
// against the resolved IP at play time (Phase 3). Rejects any non-http(s)
// scheme (including file://), URL userinfo, and empty hosts.
func validateUserProviderHost(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("scheme %q is not allowed (only http and https)", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("userinfo is not allowed in url")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url host is required")
	}
	// Only IP-literal hosts can be classified here; hostnames are deferred to
	// the play-time resolved-IP recheck (Phase 3). Unmap() canonicalizes
	// IPv4-mapped IPv6 literals (e.g. ::ffff:127.0.0.1) so the IPv4-form checks
	// fire — without it, IsLoopback()/IsLinkLocalUnicast() evaluate the raw
	// IPv6 bits and an attacker bypasses the loopback/metadata blocks.
	if addr, err := netip.ParseAddr(host); err == nil {
		if err := validateUserProviderIP(addr.Unmap()); err != nil {
			return err
		}
	}
	return nil
}

// userDirectInputPolicy is the MediaInputPolicy for user-authored `direct`
// (m3u8/HLS) channels (spec §7.2). It mirrors the bundled directHLSInputPolicy()
// in playback.go but OMITS the `file` protocol — user direct streams are not
// bundled or host-locked, so FFmpeg must never be allowed to open local files.
// Phase 3 wires this onto the user direct-item SessionRequest; defining it here
// keeps the security primitives together and independently testable.
func userDirectInputPolicy() core.MediaInputPolicy {
	return core.MediaInputPolicy{
		ProtocolWhitelist: []string{"http", "https", "tcp", "tls", "crypto"},
		DisableRedirects:  true,
		DisableReconnect:  true,
		RWTimeout:         5 * time.Second,
		BlockedHeaders:    []string{"Cookie", "Authorization", "Proxy-Authorization", "Referer"},
	}
}

// validateUserProviderIP rejects IP ranges that must never be dereferenced from
// a LAN appliance (spec §7.1): loopback, link-local (incl. 169.254.169.254
// cloud metadata and fe80::/10), unspecified, and multicast. Private/LAN
// (10/8, 172.16/12, 192.168/16, fc00::/7) and public global-unicast are allowed.
func validateUserProviderIP(addr netip.Addr) error {
	switch {
	case addr.IsLoopback():
		return fmt.Errorf("loopback addresses are not allowed")
	case addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast():
		return fmt.Errorf("link-local addresses are not allowed")
	case addr.IsUnspecified():
		return fmt.Errorf("unspecified address is not allowed")
	case addr.IsMulticast():
		return fmt.Errorf("multicast addresses are not allowed")
	}
	// IPv4-compatible IPv6 (::a.b.c.d, deprecated per RFC 4291 §2.5.5.1) is NOT
	// an IPv4-mapped address, so Unmap() leaves it as-is and the IPv6 loopback
	// check above (which matches ::1) misses the embedded IPv4 — e.g.
	// ::127.0.0.1 would otherwise slip past. Re-classify the embedded IPv4 so it
	// is judged as 127.0.0.1. The canonical ::1 (loopback) and :: (unspecified)
	// are already handled by the switch above, so only genuine IPv4-compatible
	// forms reach here; the recursion terminates because the embedded address is
	// IPv4 (Is6() == false).
	if addr.Is6() {
		b := addr.As16()
		ipv4Compatible := true
		for _, x := range b[:12] {
			if x != 0 {
				ipv4Compatible = false
				break
			}
		}
		if ipv4Compatible {
			return validateUserProviderIP(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		}
	}
	return nil
}

// httpDoer is the testable boundary around *http.Client for the user-direct
// redirect prevalidation walk (resolveUserDirectURL). Production wiring uses a
// no-redirect client (newUserRedirectClient); tests inject a stub.
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

const (
	// maxUserRedirectHops bounds the adapter-side Location walk for user direct
	// streams (spec §7.2: "max 3 hops").
	maxUserRedirectHops = 3
	// userDirectProbeTimeout bounds each HEAD request in the redirect walk.
	userDirectProbeTimeout = 10 * time.Second
	// userResolvedHostLookupTimeout bounds DNS resolution during the play-time
	// resolved-URL recheck.
	userResolvedHostLookupTimeout = 10 * time.Second
)

// resolveUserDirectURL walks up to maxHops HTTP redirects for a user direct
// (m3u8/HLS) URL, re-running validateUserProviderResolvedHost on EVERY hop
// before any request is issued, and returns the final non-redirect URL to hand
// to FFmpeg. This is the adapter-side prevalidation the DisableRedirects policy
// contract requires (internal/ffmpeg/policy.go:34-60): FFmpeg emits no
// redirect-disabling flag, so the adapter must resolve Location chains itself.
func resolveUserDirectURL(ctx context.Context, doer httpDoer, resolver hostResolver, rawURL string, maxHops int) (string, error) {
	current := strings.TrimSpace(rawURL)
	for hop := 0; hop <= maxHops; hop++ {
		if err := validateUserProviderResolvedHost(ctx, resolver, current); err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, current, nil)
		if err != nil {
			return "", fmt.Errorf("build HEAD request: %w", err)
		}
		resp, err := doer.Do(req)
		if err != nil {
			return "", fmt.Errorf("probe redirect chain: %w", err)
		}
		status := resp.StatusCode
		location := resp.Header.Get("Location")
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		if status < 300 || status > 399 {
			return current, nil
		}
		if location == "" {
			return "", fmt.Errorf("redirect missing Location header")
		}
		base, err := url.Parse(current)
		if err != nil {
			return "", fmt.Errorf("parse current url: %w", err)
		}
		ref, err := url.Parse(location)
		if err != nil {
			return "", fmt.Errorf("invalid redirect location: %w", err)
		}
		current = base.ResolveReference(ref).String()
	}
	return "", fmt.Errorf("redirect chain exceeded %d hops", maxHops)
}

// newUserRedirectClient is the production httpDoer: a client that surfaces each
// redirect response (CheckRedirect returns ErrUseLastResponse) so the adapter
// validates and follows Location headers itself, bounded by a per-request
// timeout.
func newUserRedirectClient() *http.Client {
	return &http.Client{
		Timeout: userDirectProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// validateUserProviderResolvedHost enforces the §7.1 "allow LAN, block
// internals" posture against a URL at DEREFERENCE time. Unlike the syntactic
// validateUserProviderHost (authoring time), this RESOLVES hostnames and
// classifies every returned address — closing DNS-rebind, decimal/hex IP
// encodings, and hostnames that resolve to blocked ranges. An IP-literal host
// is classified directly; a hostname is resolved via resolver.LookupHost and
// EVERY resolved address must pass validateUserProviderIP(addr.Unmap()).
func validateUserProviderResolvedHost(ctx context.Context, resolver hostResolver, rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.User != nil {
		return fmt.Errorf("userinfo is not allowed in url")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("scheme %q is not allowed (only http and https)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url host is required")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return validateUserProviderIP(addr.Unmap())
	}
	ips, err := resolver.LookupHost(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("host %q resolved to no addresses", host)
	}
	for _, ipStr := range ips {
		addr, err := netip.ParseAddr(ipStr)
		if err != nil {
			return fmt.Errorf("resolved address %q: %w", ipStr, err)
		}
		if err := validateUserProviderIP(addr.Unmap()); err != nil {
			return fmt.Errorf("resolved address %q: %w", ipStr, err)
		}
	}
	return nil
}
