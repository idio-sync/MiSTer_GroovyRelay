package sourcefetch

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

	"golang.org/x/net/idna"
)

type Resolver interface {
	LookupHost(context.Context, string) ([]string, error)
}

type Limits struct {
	MaxBytes       int64
	MaxRedirects   int
	AllowedSchemes []string
	AllowLocalURLs bool
	// AllowedHosts keys must be normalized with NormalizeHost.
	AllowedHosts map[string]struct{}
	UserAgent    string
}

type Condition struct {
	ETag         string
	LastModified string
}

type Response struct {
	Body         []byte
	FinalURL     string
	ContentType  string
	ETag         string
	LastModified string
	NotModified  bool
	StatusCode   int
}

type Fetcher struct {
	Resolver    Resolver
	Transport   *http.Transport
	DialContext func(context.Context, string, string) (net.Conn, error)
}

type Target struct {
	URL        *url.URL
	Hostname   string
	ResolvedIP string
	DialAddr   string
}

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

func (f Fetcher) Fetch(ctx context.Context, method, rawURL string, limits Limits, condition Condition) (Response, error) {
	if method != http.MethodHead && limits.MaxBytes <= 0 {
		return Response{}, fmt.Errorf("max bytes must be positive")
	}
	if limits.MaxRedirects < 0 {
		return Response{}, fmt.Errorf("max redirects must be nonnegative")
	}

	current, err := url.Parse(rawURL)
	if err != nil {
		return Response{}, fmt.Errorf("invalid URL")
	}
	for redirects := 0; ; {
		target, err := f.ValidateTarget(ctx, current, limits)
		if err != nil {
			return Response{}, err
		}

		resp, err := f.roundTrip(ctx, method, target, condition, limits.UserAgent)
		if err != nil {
			return Response{}, err
		}

		if IsRedirectStatus(resp.StatusCode) {
			location := resp.Header.Get("Location")
			_ = resp.Body.Close()
			if redirects >= limits.MaxRedirects {
				return Response{}, fmt.Errorf("too many redirects")
			}
			next, err := current.Parse(location)
			if err != nil {
				return Response{}, fmt.Errorf("invalid redirect location")
			}
			current = next
			redirects++
			continue
		}

		defer resp.Body.Close()
		out := Response{
			FinalURL:     target.URL.String(),
			ContentType:  resp.Header.Get("Content-Type"),
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
			StatusCode:   resp.StatusCode,
		}
		if resp.StatusCode == http.StatusNotModified {
			out.NotModified = true
			return out, nil
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return Response{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
		}
		if method != http.MethodHead {
			body, err := ReadCappedBody(resp.Body, limits.MaxBytes)
			if err != nil {
				return Response{}, err
			}
			out.Body = body
		}
		return out, nil
	}
}

func (f Fetcher) ValidateTarget(ctx context.Context, u *url.URL, limits Limits) (Target, error) {
	if u == nil {
		return Target{}, fmt.Errorf("URL is required")
	}
	if u.User != nil {
		return Target{}, fmt.Errorf("URL userinfo is not allowed")
	}
	scheme := strings.ToLower(u.Scheme)
	if !schemeAllowed(scheme, limits) {
		return Target{}, fmt.Errorf("scheme %q is not allowed", scheme)
	}

	rawHostname := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if rawHostname == "" {
		return Target{}, fmt.Errorf("host is required")
	}
	hostname := rawHostname
	if literal, err := netip.ParseAddr(rawHostname); err == nil {
		if !limits.AllowLocalURLs {
			return Target{}, fmt.Errorf("IP literal hosts are not allowed")
		}
		hostname = literal.Unmap().String()
	} else {
		normalized, err := NormalizeHost(rawHostname)
		if err != nil {
			return Target{}, err
		}
		hostname = normalized
	}

	if len(limits.AllowedHosts) != 0 {
		if _, ok := limits.AllowedHosts[hostname]; !ok {
			return Target{}, fmt.Errorf("host %q is not in allowlist", hostname)
		}
	}

	addr, err := ResolvePublicTargetIP(ctx, f.Resolver, hostname, limits.AllowLocalURLs)
	if err != nil {
		return Target{}, err
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return Target{
		URL:        cloneURL(u),
		Hostname:   hostname,
		ResolvedIP: addr.String(),
		DialAddr:   net.JoinHostPort(addr.String(), port),
	}, nil
}

func NormalizeHost(host string) (string, error) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return "", fmt.Errorf("host is required")
	}
	if strings.ContainsAny(host, `/\?#@`) || strings.Contains(host, "*") || strings.Contains(host, ":") {
		return "", fmt.Errorf("host %q is not a valid DNS name", host)
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", fmt.Errorf("IP literal hosts are not allowed")
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", err
	}
	ascii = strings.TrimSuffix(strings.ToLower(ascii), ".")
	if ascii == "" {
		return "", fmt.Errorf("host is required")
	}
	if _, err := netip.ParseAddr(ascii); err == nil {
		return "", fmt.Errorf("IP literal hosts are not allowed")
	}
	return ascii, nil
}

func ResolvePublicTargetIP(ctx context.Context, resolver Resolver, hostname string, allowLocal bool) (netip.Addr, error) {
	if literal, err := netip.ParseAddr(hostname); err == nil {
		literal = literal.Unmap()
		if !allowLocal {
			return netip.Addr{}, fmt.Errorf("IP literal hosts are not allowed")
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
	if len(hosts) == 0 {
		return netip.Addr{}, fmt.Errorf("host %q did not resolve to any IPs", hostname)
	}

	var first netip.Addr
	for _, host := range hosts {
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("host %q resolved to invalid IP %q", hostname, host)
		}
		if !allowLocal && !IsPublicRoutable(addr) {
			return netip.Addr{}, fmt.Errorf("host %q resolved to non-public IP %s", hostname, addr)
		}
		addr = addr.Unmap()
		if !first.IsValid() {
			first = addr
		}
	}
	return first, nil
}

func (f Fetcher) roundTrip(ctx context.Context, method string, target Target, condition Condition, userAgent string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target.URL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("invalid request")
	}
	req.Host = target.URL.Host
	if condition.ETag != "" {
		req.Header.Set("If-None-Match", condition.ETag)
	}
	if condition.LastModified != "" {
		req.Header.Set("If-Modified-Since", condition.LastModified)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	rt, cleanup := f.roundTripper(target)
	if cleanup != nil {
		defer cleanup()
	}
	return rt.RoundTrip(req)
}

func (f Fetcher) roundTripper(target Target) (*http.Transport, func()) {
	var transport *http.Transport
	if f.Transport != nil {
		transport = f.Transport.Clone()
	} else {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	transport.Proxy = nil
	transport.DisableCompression = true
	transport.DisableKeepAlives = true
	f.pinTransportToTarget(transport, target)
	return transport, transport.CloseIdleConnections
}

func (f Fetcher) pinTransportToTarget(transport *http.Transport, target Target) {
	transport.Dial = nil
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		dial := f.DialContext
		if dial == nil {
			dialer := &net.Dialer{Timeout: 30 * time.Second}
			dial = dialer.DialContext
		}
		return dial(ctx, network, target.DialAddr)
	}
	transport.DialTLS = nil
	transport.DialTLSContext = nil

	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	tlsConfig.ServerName = target.Hostname
	tlsConfig.InsecureSkipVerify = false
	transport.TLSClientConfig = tlsConfig
}

func IsPublicRoutable(addr netip.Addr) bool {
	if addr.Is4In6() {
		return false
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

func ReadCappedBody(body io.Reader, maxBytes int64) ([]byte, error) {
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

func IsRedirectStatus(status int) bool {
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

func schemeAllowed(scheme string, limits Limits) bool {
	if scheme != "http" && scheme != "https" {
		return false
	}
	if len(limits.AllowedSchemes) != 0 {
		for _, allowed := range limits.AllowedSchemes {
			if scheme == strings.ToLower(strings.TrimSpace(allowed)) {
				return true
			}
		}
		return false
	}
	if limits.AllowLocalURLs {
		return scheme == "http" || scheme == "https"
	}
	return scheme == "https"
}

func cloneURL(u *url.URL) *url.URL {
	cloned := *u
	return &cloned
}
