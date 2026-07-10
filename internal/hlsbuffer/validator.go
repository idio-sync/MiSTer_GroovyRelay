package hlsbuffer

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const defaultMaxRedirects = 3

type TrustMode int

const (
	TrustModeGenericPublic TrustMode = iota + 1
	TrustModeBundledToonami
)

type DNSResolver func(ctx context.Context, host string) ([]net.IP, error)

type URLValidator struct {
	Resolver     DNSResolver
	Client       *http.Client
	MaxRedirects int
	DialContext  func(context.Context, string, string) (net.Conn, error)
}

func (v URLValidator) Validate(ctx context.Context, rawURL string, mode TrustMode) (string, error) {
	finalURL, err := v.ValidateReference(ctx, rawURL, mode)
	if err != nil {
		return "", err
	}

	maxRedirects := v.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	for hop := 0; hop <= maxRedirects; hop++ {
		client, err := v.noRedirectClientForURL(ctx, finalURL, mode)
		if err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, finalURL, nil)
		if err != nil {
			return "", fmt.Errorf("hls url: build HEAD request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("hls url: follow redirects: %w", err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode < 300 || resp.StatusCode > 399 {
			return finalURL, nil
		}
		location := resp.Header.Get("Location")
		if location == "" {
			return "", fmt.Errorf("hls url: redirect missing Location")
		}
		next, err := resolveURLReference(finalURL, location)
		if err != nil {
			return "", err
		}
		finalURL, err = v.ValidateReference(ctx, next, mode)
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("hls url: redirect chain exceeded %d hops", maxRedirects)
}

func (v URLValidator) ValidateReference(ctx context.Context, rawURL string, mode TrustMode) (string, error) {
	u, err := parseAbsoluteHTTPURL(rawURL)
	if err != nil {
		return "", err
	}
	if u.User != nil {
		return "", fmt.Errorf("hls url: userinfo is not allowed")
	}

	switch mode {
	case TrustModeGenericPublic:
		if err := v.requirePublicHost(ctx, u.Hostname()); err != nil {
			return "", err
		}
	case TrustModeBundledToonami:
		if err := validateBundledToonamiURL(u); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("hls url: unknown trust mode %d", mode)
	}
	return u.String(), nil
}

func (v URLValidator) noRedirectClientForURL(ctx context.Context, rawURL string, mode TrustMode) (*http.Client, error) {
	u, err := parseAbsoluteHTTPURL(rawURL)
	if err != nil {
		return nil, err
	}
	ips, err := v.resolveDialIPs(ctx, u.Hostname(), mode)
	if err != nil {
		return nil, err
	}

	source := v.Client
	var transport *http.Transport
	if source != nil {
		if t, ok := source.Transport.(*http.Transport); ok && t != nil {
			transport = t.Clone()
		}
	}
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	v.pinTransportToIPs(transport, u.Hostname(), portForURL(u), ips)

	timeout := 10 * time.Second
	if source != nil {
		timeout = firstNonZeroDuration(source.Timeout, timeout)
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func (v URLValidator) pinTransportToIPs(transport *http.Transport, hostname, port string, ips []net.IP) {
	baseDial := transport.DialContext
	transport.Proxy = nil
	transport.Dial = nil
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		dial := v.DialContext
		if dial == nil {
			dial = baseDial
		}
		if dial == nil {
			dialer := &net.Dialer{Timeout: 30 * time.Second}
			dial = dialer.DialContext
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("hls url: no dial address for host %q", hostname)
	}
	transport.DialTLS = nil
	transport.DialTLSContext = nil

	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	tlsConfig.ServerName = hostname
	tlsConfig.InsecureSkipVerify = false
	transport.TLSClientConfig = tlsConfig
}

func firstNonZeroDuration(v, fallback time.Duration) time.Duration {
	if v != 0 {
		return v
	}
	return fallback
}

func parseAbsoluteHTTPURL(rawURL string) (*url.URL, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, fmt.Errorf("hls url: empty URL")
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("hls url: malformed URL: %w", err)
	}
	if !u.IsAbs() {
		return nil, fmt.Errorf("hls url: URL must be absolute")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("hls url: scheme %q is not allowed", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("hls url: hostname is required")
	}
	return u, nil
}

func (v URLValidator) requirePublicHost(ctx context.Context, host string) error {
	_, err := v.resolvePublicIPs(ctx, host)
	return err
}

func (v URLValidator) resolveDialIPs(ctx context.Context, host string, mode TrustMode) ([]net.IP, error) {
	switch mode {
	case TrustModeGenericPublic:
		return v.resolvePublicIPs(ctx, host)
	case TrustModeBundledToonami:
		return v.resolveHost(ctx, host)
	default:
		return nil, fmt.Errorf("hls url: unknown trust mode %d", mode)
	}
}

func (v URLValidator) resolvePublicIPs(ctx context.Context, host string) ([]net.IP, error) {
	ips, err := v.resolveHost(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		class := classifyAddress(ip)
		if class != addressClassPublic {
			return nil, fmt.Errorf("hls url: %s resolves to disallowed address %s", host, ip)
		}
	}
	return ips, nil
}

func (v URLValidator) resolveHost(ctx context.Context, host string) ([]net.IP, error) {
	resolver := v.Resolver
	if resolver == nil {
		resolver = defaultResolver
	}
	ips, err := resolver(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("hls url: resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("hls url: resolve %q: no addresses", host)
	}
	return ips, nil
}

func portForURL(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

func defaultResolver(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// toonamiOriginHostRE matches the Wowza streaming-origin hosts that
// Toonami Aftermath returns inside its master playlists. The digit in
// `aspN` rotates across the load-balanced pool, so the regex covers any
// digit on the fixed port 1934.
var toonamiOriginHostRE = regexp.MustCompile(`^asp\d+\.toonamiaftermath\.com:1934$`)

var toonamiAPIChannelPrefixes = []string{"/est/", "/pst/", "/movies/", "/radio/"}

var toonamiOriginChannelPrefixes = []string{
	"/livehttporigin/est/",
	"/livehttporigin/pst/",
	"/livehttporigin/movies/",
	"/livehttporigin/radio/",
}

func validateBundledToonamiURL(u *url.URL) error {
	if u.Fragment != "" {
		return fmt.Errorf("hls url: Toonami Aftermath URL fragment is not allowed")
	}
	host := strings.ToLower(u.Host)
	switch {
	case host == "api.toonamiaftermath.com:3000":
		if u.RawQuery != "" {
			return fmt.Errorf("hls url: Toonami Aftermath API URL query is not allowed")
		}
		return checkToonamiPathPrefix(u, toonamiAPIChannelPrefixes)
	case toonamiOriginHostRE.MatchString(host):
		if err := checkToonamiOriginQuery(u); err != nil {
			return err
		}
		return checkToonamiPathPrefix(u, toonamiOriginChannelPrefixes)
	}
	return fmt.Errorf("hls url: Toonami Aftermath host %q is not allowed", host)
}

func checkToonamiPathPrefix(u *url.URL, prefixes []string) error {
	path := u.EscapedPath()
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return nil
		}
	}
	return fmt.Errorf("hls url: Toonami Aftermath path %q is not in an approved channel prefix", path)
}

// checkToonamiOriginQuery permits at most a single `streamDelay`
// parameter on the Wowza origin host — Toonami's `pst` channel ships
// with `?streamDelay=180` on its variant URL, and nothing else has ever
// appeared in production.
func checkToonamiOriginQuery(u *url.URL) error {
	if u.RawQuery == "" {
		return nil
	}
	q := u.Query()
	if len(q) != 1 {
		return fmt.Errorf("hls url: Toonami Aftermath origin allows only the streamDelay query parameter")
	}
	if _, ok := q["streamDelay"]; !ok {
		return fmt.Errorf("hls url: Toonami Aftermath origin allows only the streamDelay query parameter")
	}
	return nil
}

func resolveURLReference(parentURL, ref string) (string, error) {
	base, err := url.Parse(parentURL)
	if err != nil {
		return "", fmt.Errorf("hls url: bad parent URL")
	}
	child, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", fmt.Errorf("hls url: bad child URL %q", safeURIForError(ref))
	}
	return base.ResolveReference(child).String(), nil
}

type addressClass int

const (
	addressClassDisallowed addressClass = iota
	addressClassPrivate
	addressClassPublic
)

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

func classifyAddress(ip net.IP) addressClass {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return addressClassDisallowed
	}
	addr = addr.Unmap()

	switch {
	case addr.IsUnspecified(),
		addr.IsLoopback(),
		addr.IsLinkLocalUnicast(),
		addr.IsLinkLocalMulticast(),
		addr.IsMulticast():
		return addressClassDisallowed
	case addr.IsPrivate():
		return addressClassPrivate
	case addr.Is4() && cgnatPrefix.Contains(addr):
		return addressClassPrivate
	default:
		return addressClassPublic
	}
}
