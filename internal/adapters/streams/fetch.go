package streams

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const maxFetchRedirects = 3

var deniedIPv4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

var deniedIPv6 = []netip.Prefix{
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
	netip.MustParsePrefix("::ffff:0:0/96"),
}

type hostResolver interface {
	LookupHost(context.Context, string) ([]string, error)
}

type fetchLimits struct {
	MaxBytes       int64
	AllowLocalURLs bool
	AllowedHosts   map[string]struct{}
}

type secureFetcher struct {
	resolver    hostResolver
	transport   *http.Transport
	dialContext func(context.Context, string, string) (net.Conn, error)
}

type fetchCondition struct {
	ETag         string
	LastModified string
}

type fetchResponse struct {
	Body         []byte
	ETag         string
	LastModified string
	NotModified  bool
}

type validatedFetchTarget struct {
	url        *url.URL
	hostname   string
	resolvedIP string
	dialAddr   string
}

func (f secureFetcher) Fetch(ctx context.Context, rawURL string, limits fetchLimits) ([]byte, error) {
	resp, err := f.FetchConditional(ctx, rawURL, limits, fetchCondition{})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (f secureFetcher) FetchConditional(ctx context.Context, rawURL string, limits fetchLimits, condition fetchCondition) (fetchResponse, error) {
	if limits.MaxBytes <= 0 {
		return fetchResponse{}, fmt.Errorf("max bytes must be positive")
	}

	current, err := url.Parse(rawURL)
	if err != nil {
		return fetchResponse{}, err
	}
	var previousScheme string
	for redirects := 0; ; {
		target, err := f.validateTarget(ctx, current, previousScheme, limits)
		if err != nil {
			return fetchResponse{}, err
		}

		resp, err := f.roundTrip(ctx, target, condition)
		if err != nil {
			return fetchResponse{}, err
		}
		if isRedirectStatus(resp.StatusCode) {
			location := resp.Header.Get("Location")
			_ = resp.Body.Close()
			if redirects >= maxFetchRedirects {
				return fetchResponse{}, fmt.Errorf("too many redirects")
			}
			next, err := current.Parse(location)
			if err != nil {
				return fetchResponse{}, err
			}
			previousScheme = current.Scheme
			current = next
			redirects++
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotModified {
			return fetchResponse{
				ETag:         resp.Header.Get("ETag"),
				LastModified: resp.Header.Get("Last-Modified"),
				NotModified:  true,
			}, nil
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fetchResponse{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
		}
		body, err := readCappedBody(resp.Body, limits.MaxBytes)
		if err != nil {
			return fetchResponse{}, err
		}
		return fetchResponse{
			Body:         body,
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
		}, nil
	}
}

func (f secureFetcher) validateTarget(ctx context.Context, u *url.URL, previousScheme string, limits fetchLimits) (validatedFetchTarget, error) {
	if u == nil {
		return validatedFetchTarget{}, fmt.Errorf("URL is required")
	}
	if u.User != nil {
		return validatedFetchTarget{}, fmt.Errorf("URL userinfo is not allowed")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && !(limits.AllowLocalURLs && scheme == "http") {
		return validatedFetchTarget{}, fmt.Errorf("scheme %q is not allowed", scheme)
	}
	if previousScheme == "https" && scheme == "http" && !limits.AllowLocalURLs {
		return validatedFetchTarget{}, fmt.Errorf("redirect from https to http is not allowed")
	}
	hostname := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if hostname == "" {
		return validatedFetchTarget{}, fmt.Errorf("host is required")
	}
	if len(limits.AllowedHosts) != 0 {
		normalized, err := normalizeConfigHost(hostname)
		if err != nil {
			return validatedFetchTarget{}, fmt.Errorf("host %q is not allowed: %w", hostname, err)
		}
		if _, ok := limits.AllowedHosts[normalized]; !ok {
			return validatedFetchTarget{}, fmt.Errorf("host %q is not in allowlist", normalized)
		}
	}

	addr, err := resolveValidatedIP(ctx, f.resolver, hostname, limits.AllowLocalURLs)
	if err != nil {
		return validatedFetchTarget{}, err
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return validatedFetchTarget{
		url:        cloneURL(u),
		hostname:   hostname,
		resolvedIP: addr.String(),
		dialAddr:   net.JoinHostPort(addr.String(), port),
	}, nil
}

func resolveValidatedIP(ctx context.Context, resolver hostResolver, hostname string, allowLocal bool) (netip.Addr, error) {
	if literal, err := netip.ParseAddr(hostname); err == nil {
		if !allowLocal {
			return netip.Addr{}, fmt.Errorf("IP literal hosts are not allowed")
		}
		if !allowLocal && !isPublicIP(literal) {
			return netip.Addr{}, fmt.Errorf("IP %s is not public", literal)
		}
		return literal, nil
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	hosts, err := resolver.LookupHost(ctx, hostname)
	if err != nil {
		return netip.Addr{}, err
	}
	for _, host := range hosts {
		addr, err := netip.ParseAddr(host)
		if err != nil {
			continue
		}
		if allowLocal || isPublicIP(addr) {
			return addr, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("host %q did not resolve to an allowed public IP", hostname)
}

func (f secureFetcher) roundTrip(ctx context.Context, target validatedFetchTarget, condition fetchCondition) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.url.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Host = target.url.Host
	if condition.ETag != "" {
		req.Header.Set("If-None-Match", condition.ETag)
	}
	if condition.LastModified != "" {
		req.Header.Set("If-Modified-Since", condition.LastModified)
	}

	rt, cleanup := f.roundTripper(target)
	if cleanup != nil {
		defer cleanup()
	}
	return rt.RoundTrip(req)
}

func (f secureFetcher) roundTripper(target validatedFetchTarget) (*http.Transport, func()) {
	if f.transport != nil {
		transport := f.transport.Clone()
		f.pinTransportToTarget(transport, target)
		return transport, transport.CloseIdleConnections
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	f.pinTransportToTarget(transport, target)
	return transport, transport.CloseIdleConnections
}

func (f secureFetcher) pinTransportToTarget(transport *http.Transport, target validatedFetchTarget) {
	transport.Dial = nil
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dial := f.dialContext
		if dial == nil {
			dialer := &net.Dialer{Timeout: 30 * time.Second}
			dial = dialer.DialContext
		}
		return dial(ctx, network, target.dialAddr)
	}
	transport.DialTLS = nil
	transport.DialTLSContext = nil

	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	tlsConfig.ServerName = target.hostname
	transport.TLSClientConfig = tlsConfig
}

func isPublicIP(addr netip.Addr) bool {
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	if addr.Is4() {
		for _, denied := range deniedIPv4 {
			if denied.Contains(addr) {
				return false
			}
		}
		return true
	}
	for _, denied := range deniedIPv6 {
		if denied.Contains(addr) {
			return false
		}
	}
	return true
}

func readCappedBody(body io.Reader, maxBytes int64) ([]byte, error) {
	limited := io.LimitReader(body, maxBytes+1)
	out, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > maxBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxBytes)
	}
	return out, nil
}

func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func cloneURL(u *url.URL) *url.URL {
	cloned := *u
	return &cloned
}
