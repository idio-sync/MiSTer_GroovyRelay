package dlna

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// urlvalidator.go implements server-side prevalidation for media URLs
// passed to SetAVTransportURI before they reach core.StartSession (and
// therefore FFmpeg). Spec source: docs/superpowers/specs/
// 2026-05-03-dlna-mediarenderer-design.md §SetAVTransportURI /
// Validation rules (lines ~318-348) and §Security (lines ~505-526).
//
// Why this lives in the adapter, not in core or in package ffmpeg:
// MediaInputPolicy.DisableRedirects is documented as a NO-OP at the
// FFmpeg argv layer (internal/ffmpeg/policy.go:34-72). The safety
// contract requires the adapter to chase redirects ITSELF and
// re-classify each Location target's resolved IP — FFmpeg's
// -protocol_whitelist alone does NOT re-run RFC1918 / loopback /
// link-local / metadata-endpoint rejection on a redirect target. A
// 302 from a public hostname to 169.254.169.254 (cloud metadata) is
// allowed by `-protocol_whitelist=https` and would bypass the
// original URL's IP-address check. validateMediaURL closes that gap
// before the URL is handed to core.
//
// Path A (spec line 341): hand FFmpeg only the prevalidated final URL
// after server-side redirect chase. The validating-proxy alternative
// (Path B) is explicitly out of scope for v1.

// validatorMaxRedirects is the redirect-chain hop cap from spec line
// 340. Three hops is generous for legitimate CDNs (e.g. Plex's media
// gateway emits 1-2 redirects) and tight enough that a chain looped
// at attacker-controlled hosts terminates quickly.
const validatorMaxRedirects = 3

// validatorRequestTimeout bounds each HEAD/GET hop in the redirect
// chase. A stalled remote must not pin SetAVTransportURI handling.
// The cumulative bound is roughly validatorRequestTimeout *
// (validatorMaxRedirects+1) which keeps total handler latency under
// 20s in the worst case.
const validatorRequestTimeout = 5 * time.Second

// AddressPolicy enumerates the address classes for media-source URL
// validation. Mirrors the spec's binary distinction between the
// default deny-public posture and the operator-opt-in
// allow_public_source_urls posture (spec line 339).
type AddressPolicy int

const (
	// PolicyPrivateOnly accepts RFC1918 / ULA / Carrier-grade NAT
	// (RFC6598) targets and always rejects loopback, link-local,
	// multicast, unspecified, and public-internet addresses. This is
	// the default when allow_public_source_urls=false.
	PolicyPrivateOnly AddressPolicy = iota

	// PolicyAllowPublic relaxes the public-internet rejection only.
	// Loopback, link-local, multicast, and unspecified are STILL
	// rejected — there is no operator setting that legitimizes a
	// 169.254.169.254 (cloud metadata) or 127.0.0.1 source URL.
	PolicyAllowPublic
)

// ValidatedURL is the result of a successful validation. FinalURL is
// what callers must hand to core.StartSession — it is the absolute
// URL after the redirect chase, with userinfo preserved. Hops counts
// the number of redirects followed (0 means the original URL was
// reachable and final). IsPrivate reports whether the resolved IPs
// at the final hop were all RFC1918/ULA/CGNAT — useful for log
// redaction and capability decisions.
type ValidatedURL struct {
	FinalURL  string
	Hops      int
	IsPrivate bool
}

// Typed sentinel errors. Validation returns these wrapped with
// fmt.Errorf("...: %w", ErrXxx) so the SOAP handler in P2.3 can
// errors.Is each one to its UPnP fault code without parsing strings.
//
// Mapping (defined in P2.3):
//   ErrInvalidURL        -> 402 Invalid Args
//   ErrSchemeNotAllowed  -> 716 Resource not found
//   ErrAddressNotAllowed -> 716 Resource not found
//   ErrTooManyRedirects  -> 716 Resource not found
//   ErrRedirectFetchFail -> 716 Resource not found
//
// We deliberately collapse three distinct rejection kinds onto 716
// because the spec treats "I can't reach this resource" as one
// condition from the controller's perspective; logs carry the
// detailed cause for operators.
var (
	ErrSchemeNotAllowed  = errors.New("dlna: URL scheme not allowed")
	ErrAddressNotAllowed = errors.New("dlna: URL resolves to disallowed address")
	ErrTooManyRedirects  = errors.New("dlna: redirect chain exceeds maximum hops")
	ErrRedirectFetchFail = errors.New("dlna: failed to follow redirect chain")
	ErrInvalidURL        = errors.New("dlna: URL is malformed")
)

// dnsResolverFunc resolves a hostname to a slice of IPs. The default
// implementation calls net.DefaultResolver.LookupIP; tests inject a
// stub so address-classification logic can be exercised without DNS
// or network access. The bare-function shape (vs an interface) keeps
// the call sites trivial and the test seam tiny.
type dnsResolverFunc func(ctx context.Context, host string) ([]net.IP, error)

// defaultDNSResolver wraps net.DefaultResolver.LookupIP with the
// "ip" network so both A and AAAA records contribute to the
// classification. Failure to resolve is propagated as-is — the
// caller maps it to ErrAddressNotAllowed (the most precise mapping
// for "we can't tell what this resolves to, so we can't say it's
// safe").
func defaultDNSResolver(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// urlValidator is the configurable validator. Production code uses
// validateMediaURL which constructs one with stdlib defaults; tests
// construct one directly to inject resolver/client stubs.
//
// The validator is stateless across calls (no cache, no rate
// limiting) so it is safe to share across goroutines, but the cheap
// per-call construction keeps the call sites in the SOAP handler
// straightforward.
type urlValidator struct {
	resolver dnsResolverFunc
	// client is the HTTP client used for the redirect chase. Tests
	// substitute a client backed by httptest. The CheckRedirect
	// callback is set per-call inside validate so it can capture the
	// per-call hop counter and the policy.
	client *http.Client
}

// validateMediaURL is the public entry point used by the SOAP
// handler. Constructs an urlValidator with stdlib defaults and runs
// the validation pass. Returns ValidatedURL on success or a typed
// error on rejection.
//
// Multiple-IP policy (spec line 339): if the hostname resolves to N
// addresses, EVERY address must pass the policy. Mixed public/private
// or any disallowed class causes rejection. Rationale: the eventual
// HTTP connection picks one of the resolved IPs (selection is
// dialer-controlled and racy), so accepting a "mostly safe" set
// would let an attacker DNS-rebind onto a public address that the
// dialer happens to pick. All-or-nothing closes that gap.
//
// Up to validatorMaxRedirects redirects are followed; each hop's
// Location header is re-parsed, re-resolved, and re-classified. A
// redirect to a disallowed scheme or address fails before FFmpeg
// sees the final URL.
func validateMediaURL(ctx context.Context, rawURL string, policy AddressPolicy) (ValidatedURL, error) {
	v := &urlValidator{
		resolver: defaultDNSResolver,
		// Timeout is per-request; the redirect chase reuses the same
		// client so each hop pays its own clock. Setting Client.Timeout
		// would bound the WHOLE chain instead, which masks per-hop
		// stalls — we want a stalled hop to fail fast on its own.
		client: &http.Client{
			// CheckRedirect is overwritten per-call inside validate.
			Timeout: 0,
		},
	}
	return v.validate(ctx, rawURL, policy)
}

// validate runs the full validation pipeline:
//   1. URL parse + scheme check (no DNS yet).
//   2. Hostname resolve + address policy on every resolved IP.
//   3. HEAD request with CheckRedirect that re-runs (1) + (2) for
//      every Location target, capped at validatorMaxRedirects.
//   4. Return the final URL on success.
//
// Step 3's HEAD is preferred over GET because we never want to
// download the media body during validation. Servers that don't
// support HEAD (rare for media gateways but possible) are handled by
// the trip — a 405 on HEAD is treated as "URL is reachable" and the
// final URL is accepted. We do NOT fall back to GET because a GET
// would start streaming media bytes through the validator's HTTP
// client and waste bandwidth before FFmpeg even runs.
func (v *urlValidator) validate(ctx context.Context, rawURL string, policy AddressPolicy) (ValidatedURL, error) {
	// Initial URL must parse and pass scheme + address checks before
	// we issue any network I/O. parseAndClassify runs DNS, so an
	// up-front malformed-URL rejection short-circuits resolver work.
	finalURL, isPrivate, err := v.parseAndClassify(ctx, rawURL, policy)
	if err != nil {
		return ValidatedURL{}, err
	}

	// hops counts redirects followed by the HTTP client. The
	// CheckRedirect callback updates this and short-circuits on
	// validation failure.
	var (
		hops          int
		redirectError error // set by CheckRedirect on classification failure
	)

	v.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// via has the requests issued before this one (oldest first).
		// On the Nth CheckRedirect call, len(via)==N: if we'd already
		// followed validatorMaxRedirects redirects we now have
		// len(via) == validatorMaxRedirects+1 because the initial
		// request is also in via. Reject when len(via) exceeds
		// validatorMaxRedirects (i.e. the previous requests + the
		// upcoming one would push us past the limit).
		if len(via) > validatorMaxRedirects {
			redirectError = ErrTooManyRedirects
			return ErrTooManyRedirects
		}
		// The next URL is in req.URL. Re-validate scheme + address
		// before allowing the client to follow.
		next, priv, cerr := v.parseAndClassify(ctx, req.URL.String(), policy)
		if cerr != nil {
			redirectError = cerr
			return cerr
		}
		// Update finalURL/isPrivate so the loop's last hop is what
		// the caller sees on success.
		finalURL = next
		isPrivate = priv
		hops = len(via)
		return nil
	}

	// Per-hop timeout via context, not Client.Timeout (see comment in
	// validateMediaURL). Each hop the CheckRedirect callback runs
	// after the previous response, so the request itself doesn't
	// need its own context derivation here — the parent ctx + the
	// validator's explicit timeout below covers it.
	hopCtx, cancel := context.WithTimeout(ctx, validatorRequestTimeout*time.Duration(validatorMaxRedirects+1))
	defer cancel()

	req, err := http.NewRequestWithContext(hopCtx, http.MethodHead, finalURL, nil)
	if err != nil {
		return ValidatedURL{}, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	// Don't leak any URL-bearing headers (spec line 347). The
	// validator request stays minimal: the dialer's User-Agent is
	// fine to leak (it identifies the bridge generically), but
	// Referer / Cookie / Authorization must not be sent.
	req.Header.Set("User-Agent", "MiSTer_GroovyRelay-DLNA-validator/1")

	resp, err := v.client.Do(req)
	if err != nil {
		// CheckRedirect errors come back from Do wrapped in a
		// *url.Error whose Err is the sentinel we returned. errors.Is
		// peels through that.
		if redirectError != nil {
			return ValidatedURL{}, redirectError
		}
		// Context deadline / network failure — collapse onto a single
		// sentinel so the SOAP handler maps to 716. The wrapped %v
		// preserves the underlying cause for logs.
		return ValidatedURL{}, fmt.Errorf("%w: %v", ErrRedirectFetchFail, err)
	}
	defer resp.Body.Close()

	// 405 Method Not Allowed on HEAD is acceptable — the URL is
	// reachable and the address checks have already passed. We
	// deliberately DO NOT fall back to GET (see validate's doc
	// comment). Any other 4xx/5xx is also acceptable here: the
	// validator's job is to accept-or-reject the URL based on
	// scheme/address and redirect safety, NOT to confirm media
	// availability — that's FFmpeg's job after StartSession. Letting
	// FFmpeg surface the real 404 produces a more accurate error
	// than us pre-rejecting on a HEAD that the server happens to
	// answer differently.

	return ValidatedURL{
		FinalURL:  finalURL,
		Hops:      hops,
		IsPrivate: isPrivate,
	}, nil
}

// parseAndClassify validates a single URL against the scheme and
// address policies. Used both for the initial URL and (via the
// CheckRedirect callback) for each redirect target. Returns the
// canonicalized URL string, whether the resolved IPs were all
// private, and a typed error on rejection.
//
// "Canonicalized" here means: re-emit via url.URL.String() so the
// final URL is consistent in case (scheme lowercased) and has any
// default port elided. We do NOT strip userinfo: a private-LAN
// gateway might legitimately use http://user:pass@host/ and core's
// log-redaction layer handles the userinfo on the way out.
func (v *urlValidator) parseAndClassify(ctx context.Context, rawURL string, policy AddressPolicy) (string, bool, error) {
	if strings.TrimSpace(rawURL) == "" {
		return "", false, fmt.Errorf("%w: empty URL", ErrInvalidURL)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if !u.IsAbs() {
		return "", false, fmt.Errorf("%w: URL is not absolute", ErrInvalidURL)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false, fmt.Errorf("%w: %q", ErrSchemeNotAllowed, u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return "", false, fmt.Errorf("%w: missing hostname", ErrInvalidURL)
	}

	// Resolve the hostname under a bounded context. If the host is
	// already a literal IP, LookupIP returns it directly without
	// network I/O — that's the common case in practice (DLNA
	// controllers usually pass host:port where host is an IP).
	resolveCtx, cancel := context.WithTimeout(ctx, validatorRequestTimeout)
	defer cancel()
	ips, err := v.resolver(resolveCtx, host)
	if err != nil {
		return "", false, fmt.Errorf("%w: resolve %q: %v", ErrAddressNotAllowed, host, err)
	}
	if len(ips) == 0 {
		return "", false, fmt.Errorf("%w: %q resolved to no addresses", ErrAddressNotAllowed, host)
	}

	allPrivate := true
	for _, ip := range ips {
		class := classifyIP(ip)
		switch class {
		case ipClassDisallowed:
			return "", false, fmt.Errorf("%w: %s -> %s (disallowed class)", ErrAddressNotAllowed, host, ip)
		case ipClassPrivate:
			// pass under either policy
		case ipClassPublic:
			if policy != PolicyAllowPublic {
				return "", false, fmt.Errorf("%w: %s -> %s (public, policy=PrivateOnly)", ErrAddressNotAllowed, host, ip)
			}
			allPrivate = false
		}
	}
	return u.String(), allPrivate, nil
}

// ipClass tags an IP for the address policy decision. Three values
// keep the policy table small and force the parseAndClassify switch
// to handle every class explicitly (no "default: pass" foot-gun).
type ipClass int

const (
	// ipClassDisallowed is the always-rejected set: loopback,
	// link-local, multicast, unspecified. No operator policy can
	// accept these for a media URL — they're either reachability
	// nonsense (multicast as a media SOURCE) or active SSRF vectors
	// (link-local 169.254.169.254 cloud metadata, loopback to the
	// bridge itself).
	ipClassDisallowed ipClass = iota

	// ipClassPrivate is RFC1918, ULA (fc00::/7), or RFC6598 CGNAT
	// (100.64.0.0/10). Accepted under both policies. CGNAT is
	// included because it's the address space ISPs deploy for
	// behind-NAT homes and it would surprise operators if their
	// carrier-deployed LAN suddenly counted as "public."
	ipClassPrivate

	// ipClassPublic is the rest of the unicast routable internet.
	// Accepted only when policy == PolicyAllowPublic.
	ipClassPublic
)

// cgnatNet is RFC6598 100.64.0.0/10 — Carrier-grade NAT space. Not
// covered by netip.Addr.IsPrivate(), so we check it explicitly.
var cgnatNet = netip.MustParsePrefix("100.64.0.0/10")

// classifyIP returns the ipClass for a single resolved IP. The
// stdlib's classification helpers cover most of the work; CGNAT and
// the IPv4-mapped-IPv6 form (::ffff:127.0.0.1, etc.) need explicit
// handling.
//
// Conversion to netip.Addr is done via AddrFromSlice + Unmap so an
// IPv4-mapped IPv6 address is normalized to its IPv4 form. Without
// Unmap, ::ffff:127.0.0.1 would NOT match IsLoopback (which checks
// 127.0.0.0/8 and ::1/128) — Unmap fixes that.
func classifyIP(ip net.IP) ipClass {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		// Malformed net.IP — treat as disallowed. parseAndClassify
		// already rejected len(ips)==0, so this only fires on a
		// resolver bug.
		return ipClassDisallowed
	}
	addr = addr.Unmap()

	switch {
	case addr.IsUnspecified():
		// 0.0.0.0 / ::. Always rejected: a media URL targeting "any
		// interface" makes no sense and is a known SSRF dodge in some
		// stacks.
		return ipClassDisallowed
	case addr.IsLoopback():
		// 127.0.0.0/8, ::1/128.
		return ipClassDisallowed
	case addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast():
		// 169.254.0.0/16 (IPv4 link-local, includes 169.254.169.254
		// cloud metadata) and fe80::/10 (IPv6 link-local). Always
		// rejected — this is the SSRF-defense headline.
		return ipClassDisallowed
	case addr.IsMulticast():
		// 224.0.0.0/4, ff00::/8. As a media SOURCE these don't
		// belong; the bridge talks unicast to ffmpeg.
		return ipClassDisallowed
	case addr.IsPrivate():
		// netip.Addr.IsPrivate covers RFC1918 (10/8, 172.16/12,
		// 192.168/16) and ULA (fc00::/7).
		return ipClassPrivate
	case addr.Is4() && cgnatNet.Contains(addr):
		// RFC6598 CGNAT — see cgnatNet doc comment.
		return ipClassPrivate
	default:
		return ipClassPublic
	}
}
