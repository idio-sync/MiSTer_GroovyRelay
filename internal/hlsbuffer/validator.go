package hlsbuffer

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
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
}

func (v URLValidator) Validate(ctx context.Context, rawURL string, mode TrustMode) (string, error) {
	finalURL, err := v.ValidateReference(ctx, rawURL, mode)
	if err != nil {
		return "", err
	}

	client := v.noRedirectClient()
	maxRedirects := v.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	for hop := 0; hop <= maxRedirects; hop++ {
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

func (v URLValidator) noRedirectClient() *http.Client {
	source := v.Client
	if source == nil {
		source = http.DefaultClient
	}
	return &http.Client{
		Transport: source.Transport,
		Timeout:   firstNonZeroDuration(source.Timeout, 10*time.Second),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
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
	resolver := v.Resolver
	if resolver == nil {
		resolver = defaultResolver
	}
	ips, err := resolver(ctx, host)
	if err != nil {
		return fmt.Errorf("hls url: resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("hls url: resolve %q: no addresses", host)
	}
	for _, ip := range ips {
		class := classifyAddress(ip)
		if class != addressClassPublic {
			return fmt.Errorf("hls url: %s resolves to disallowed address %s", host, ip)
		}
	}
	return nil
}

func defaultResolver(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

func validateBundledToonamiURL(u *url.URL) error {
	if u.Host != "api.toonamiaftermath.com:3000" {
		return fmt.Errorf("hls url: Toonami Aftermath host must be api.toonamiaftermath.com:3000")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("hls url: Toonami Aftermath URL query and fragment are not allowed")
	}
	for _, prefix := range []string{"/est/", "/pst/", "/movies/", "/radio/"} {
		if strings.HasPrefix(u.EscapedPath(), prefix) {
			return nil
		}
	}
	return fmt.Errorf("hls url: Toonami Aftermath path is not in an approved channel prefix")
}

func resolveURLReference(parentURL, ref string) (string, error) {
	base, err := url.Parse(parentURL)
	if err != nil {
		return "", fmt.Errorf("hls url: bad parent URL: %w", err)
	}
	child, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", fmt.Errorf("hls url: bad child URL: %w", err)
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
