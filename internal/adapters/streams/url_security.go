package streams

import (
	"fmt"
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
