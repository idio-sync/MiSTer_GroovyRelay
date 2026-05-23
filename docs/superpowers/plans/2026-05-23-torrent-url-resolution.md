# Torrent URL Resolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Support HTTP(S) URLs that resolve to `.torrent` metainfo by fetching the metainfo safely, then streaming through the existing Torrent adapter playback path.

**Architecture:** First extract the existing hardened Streams fetcher/address classifier into `internal/sourcefetch` so Torrent, Streams, and DLNA/HLS share one guarded fetch/address policy. Then add Torrent URL fetch and quick-cast wiring on top of that shared package, preserving magnet/upload behavior and the existing Torrent traffic gates.

**Tech Stack:** Go stdlib (`net/http`, `net/netip`, `crypto/tls`, `mime`, `io`), existing adapter registry/quick-cast interfaces, `github.com/anacrolix/torrent`, existing Go unit tests.

---

## File Structure

- Create `internal/sourcefetch/fetch.go`
  - Owns the shared guarded fetcher, deny prefixes, public/private classification, redirect walking, no-proxy transport pinning, TLS `ServerName` preservation, and capped body reads.
- Create `internal/sourcefetch/fetch_test.go`
  - Ports and expands the current Streams fetcher tests, including normative deny-prefix coverage and HTTPS `ServerName` assertions.
- Modify `internal/adapters/streams/fetch.go`
  - Replace private implementation with thin wrapper aliases around `internal/sourcefetch`, preserving existing Streams call sites.
- Modify `internal/adapters/streams/fetch_test.go`
  - Keep Streams-specific tests focused on wrapper behavior and host allowlist semantics; shared security tests move to `internal/sourcefetch`.
- Modify `internal/adapters/dlna/urlvalidator.go`
  - Replace local IP classification with calls into `internal/sourcefetch` while preserving DLNA's `PolicyPrivateOnly` / `PolicyAllowPublic` behavior and SOAP-facing sentinels.
- Modify `internal/adapters/dlna/playlist.go`
  - Replace HLS dial-time classification with the shared classifier while preserving HLS redirect and cache behavior.
- Modify `internal/adapters/dlna/urlvalidator_test.go` and `internal/adapters/dlna/playlist_security_test.go`
  - Add focused regression checks for newly denied shared prefixes and keep existing DLNA semantics green.
- Create `internal/adapters/torrent/url_fetcher.go`
  - Owns Torrent-specific URL acceptance, `HEAD`/`GET` sequencing, content-type checks, safe error conversion, and shared fetcher integration.
- Create `internal/adapters/torrent/url_fetcher_test.go`
  - Covers Torrent URL predicates, `HEAD` behavior, oversized responses, userinfo rejection, safe errors, and public-only fetch policy.
- Modify `internal/adapters/torrent/adapter.go`
  - Add a `torrentURLFetcher` seam to `Adapter` with a production default.
- Modify `internal/adapters/torrent/session.go`
  - Add `startTorrentURL` and a shared `startTorrentBytesWithConfig` helper so the post-fetch gate recheck is explicit.
- Modify `internal/adapters/torrent/session_test.go`
  - Add tests for gate checks, post-fetch gate revocation, and metainfo start flow.
- Modify `internal/adapters/torrent/playback_provider.go`
  - Add the `Torrent URL` quick-cast tab and handler branch.
- Modify `internal/adapters/torrent/playback_provider_test.go`
  - Cover tab rendering, disabled reasons, empty input, and successful URL quick-cast.
- Modify `README.md`
  - Mention HTTP(S) `.torrent` URLs alongside magnet and uploaded `.torrent` support.
- Modify `docs/torrent.md`
  - Add operator-facing Torrent URL behavior, constraints, and safety notes.

## Commit Plan

Commit after each task:

1. `feat(sourcefetch): add shared guarded fetcher`
2. `refactor(streams): use shared source fetcher`
3. `refactor(dlna): use shared address classifier`
4. `feat(torrent): add guarded torrent url fetcher`
5. `feat(torrent): start sessions from torrent urls`
6. `feat(torrent): expose torrent url quick cast`
7. `docs(torrent): document torrent url casting`

## Task 1: Add Shared Guarded Fetcher

**Files:**
- Create: `internal/sourcefetch/fetch.go`
- Create: `internal/sourcefetch/fetch_test.go`
- Reference: `internal/adapters/streams/fetch.go`
- Reference: `internal/adapters/streams/fetch_test.go`

- [ ] **Step 1: Create failing classifier and capped-body tests**

Create `internal/sourcefetch/fetch_test.go` with this initial content:

```go
package sourcefetch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestIsPublicRoutableRejectsNormativeDeniedPrefixes(t *testing.T) {
	for _, raw := range []string{
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
	} {
		t.Run(raw, func(t *testing.T) {
			if IsPublicRoutable(netip.MustParseAddr(raw)) {
				t.Fatalf("%s classified as public routable", raw)
			}
		})
	}
}

func TestReadCappedBodyUsesLimitReader(t *testing.T) {
	body := strings.NewReader("12345")
	_, err := ReadCappedBody(body, 4)
	if err == nil {
		t.Fatal("ReadCappedBody accepted a body over the cap")
	}
	if !strings.Contains(err.Error(), "exceeds 4 bytes") {
		t.Fatalf("error = %q, want cap message", err.Error())
	}
}
```

- [ ] **Step 2: Run the focused tests and verify they fail**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/sourcefetch -run "Test(IsPublicRoutableRejectsNormativeDeniedPrefixes|ReadCappedBodyUsesLimitReader)" -count=1
```

Expected: FAIL because `internal/sourcefetch` and the exported functions do not exist.

- [ ] **Step 3: Implement classifier and capped body**

Create `internal/sourcefetch/fetch.go` with this initial content:

```go
package sourcefetch

import (
	"fmt"
	"io"
	"net/netip"
)

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

func IsPublicRoutable(addr netip.Addr) bool {
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
```

- [ ] **Step 4: Run the focused tests and verify they pass**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/sourcefetch -run "Test(IsPublicRoutableRejectsNormativeDeniedPrefixes|ReadCappedBodyUsesLimitReader)" -count=1
```

Expected: PASS.

- [ ] **Step 5: Add failing guarded fetcher tests**

Append these tests and helpers to `internal/sourcefetch/fetch_test.go`:

```go
func TestFetcherRejectsUserinfoAndIPLiteral(t *testing.T) {
	f := Fetcher{Resolver: staticResolver{"example.test": []string{"93.184.216.34"}}}
	for _, raw := range []string{
		"https://user:pass@example.test/file.json",
		"https://127.0.0.1/file.json",
		"http://[::1]/file.json",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := f.Fetch(context.Background(), http.MethodGet, raw, Limits{
				MaxBytes:       1024,
				AllowedSchemes: []string{"http", "https"},
				MaxRedirects:   3,
			}, Condition{})
			if err == nil {
				t.Fatalf("Fetch accepted %s", raw)
			}
		})
	}
}

func TestFetcherDoesNotHonorEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	dialer := &recordingDialer{}
	f := Fetcher{
		Resolver:    staticResolver{"media.example": []string{"93.184.216.34"}},
		DialContext: dialer.DialContext,
	}
	_, _ = f.Fetch(context.Background(), http.MethodGet, "https://media.example/catalog.json", Limits{
		MaxBytes:       1024,
		AllowedSchemes: []string{"https"},
		MaxRedirects:   3,
	}, Condition{})
	if strings.Contains(dialer.addr, "127.0.0.1") {
		t.Fatalf("dialed proxy address %q", dialer.addr)
	}
}

func TestFetcherPinsTLSNameAndDialsValidatedIP(t *testing.T) {
	dialer, transport := newPinnedFetchServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "media.example" {
			t.Fatalf("Host = %q, want media.example", r.Host)
		}
		_, _ = w.Write([]byte("ok"))
	})
	f := Fetcher{
		Resolver:    staticResolver{"media.example": []string{"93.184.216.34"}},
		Transport:   transport,
		DialContext: dialer.DialContext,
	}
	_, _ = f.Fetch(context.Background(), http.MethodGet, "https://media.example/catalog.json", Limits{
		MaxBytes:       1024,
		AllowedSchemes: []string{"https"},
		MaxRedirects:   3,
	}, Condition{})
	if !dialer.dialed("93.184.216.34") {
		t.Fatalf("dialed %v, want validated IP", dialer.addrs)
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("base transport used InsecureSkipVerify")
	}
}

func TestFetcherRedirectRevalidatesEachHop(t *testing.T) {
	dialer, transport := newPinnedFetchServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://private.example/catalog.json", http.StatusFound)
	})
	f := Fetcher{
		Resolver: staticResolver{
			"public.example":  []string{"93.184.216.34"},
			"private.example": []string{"192.168.1.20"},
		},
		Transport:   transport,
		DialContext: dialer.DialContext,
	}
	_, err := f.Fetch(context.Background(), http.MethodGet, "https://public.example/catalog.json", Limits{
		MaxBytes:       1024,
		AllowedSchemes: []string{"https"},
		MaxRedirects:   3,
	}, Condition{})
	if err == nil {
		t.Fatal("redirect to private address accepted")
	}
}

type staticResolver map[string][]string

func (r staticResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if ips, ok := r[host]; ok {
		return ips, nil
	}
	return nil, fmt.Errorf("unexpected lookup for %s", host)
}

type recordingDialer struct {
	addrs []string
	addr  string
}

func (d *recordingDialer) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	d.addr = address
	d.addrs = append(d.addrs, address)
	return nil, errors.New("dial stopped by test")
}

func (d *recordingDialer) dialed(fragment string) bool {
	for _, addr := range d.addrs {
		if strings.Contains(addr, fragment) {
			return true
		}
	}
	return false
}

type pinnedDialer struct {
	serverAddr string
	addrs      []string
}

func (d *pinnedDialer) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	d.addrs = append(d.addrs, address)
	var nd net.Dialer
	return nd.DialContext(ctx, network, d.serverAddr)
}

func (d *pinnedDialer) dialed(fragment string) bool {
	for _, addr := range d.addrs {
		if strings.Contains(addr, fragment) {
			return true
		}
	}
	return false
}

func newPinnedFetchServer(t *testing.T, h http.HandlerFunc) (*pinnedDialer, *http.Transport) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	serverHostPort := strings.TrimPrefix(srv.URL, "https://")
	dialer := &pinnedDialer{serverAddr: serverHostPort}
	transport := srv.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	return dialer, transport
}
```

- [ ] **Step 6: Run the guarded fetcher tests and verify they fail**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/sourcefetch -run "TestFetcher" -count=1
```

Expected: FAIL because `Fetcher`, `Limits`, and `Condition` do not exist.

- [ ] **Step 7: Implement guarded fetcher API**

Update `internal/sourcefetch/fetch.go` so it has one package declaration, one import block, and the exported types below. Keep the classifier and `ReadCappedBody` from earlier in the file.

```go
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
)

type Resolver interface {
	LookupHost(context.Context, string) ([]string, error)
}

type Limits struct {
	MaxBytes       int64
	MaxRedirects   int
	AllowedSchemes []string
	AllowLocalURLs bool
	AllowedHosts   map[string]struct{}
	UserAgent      string
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
	StatusCode    int
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
```

Add the remaining implementation by moving the body of the existing Streams functions into this package and applying these required exported names:

- `secureFetcher.FetchConditional` becomes `Fetcher.Fetch`.
- `fetchLimits` becomes `Limits`.
- `fetchCondition` becomes `Condition`.
- `fetchResponse` becomes `Response`.
- `validatedFetchTarget` becomes `Target`.
- `resolveValidatedIP` becomes `ResolvePublicTargetIP`.
- `isRedirectStatus` becomes `IsRedirectStatus`.
- `cloneURL` remains private.

Ensure these concrete behavior changes while moving:

```go
func (f Fetcher) Fetch(ctx context.Context, method string, rawURL string, limits Limits, condition Condition) (Response, error) {
	if limits.MaxBytes <= 0 && method != http.MethodHead {
		return Response{}, fmt.Errorf("max bytes must be positive")
	}
	if limits.MaxRedirects < 0 {
		return Response{}, fmt.Errorf("max redirects must be non-negative")
	}
	current, err := url.Parse(rawURL)
	if err != nil {
		return Response{}, err
	}
	var previousScheme string
	for redirects := 0; ; {
		target, err := f.ValidateTarget(ctx, current, previousScheme, limits)
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
				return Response{}, err
			}
			previousScheme = current.Scheme
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
			StatusCode:    resp.StatusCode,
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
```

In `roundTripper`, explicitly disable proxies and compression after cloning:

```go
transport.Proxy = nil
transport.DisableCompression = true
```

In `pinTransportToTarget`, preserve TLS verification:

```go
tlsConfig := transport.TLSClientConfig
if tlsConfig == nil {
	tlsConfig = &tls.Config{}
} else {
	tlsConfig = tlsConfig.Clone()
}
tlsConfig.ServerName = target.Hostname
transport.TLSClientConfig = tlsConfig
```

- [ ] **Step 8: Run sourcefetch tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/sourcefetch -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/sourcefetch/fetch.go internal/sourcefetch/fetch_test.go
git -c user.name=idio-sync -c user.email=jedivoodoo@gmail.com commit -m "feat(sourcefetch): add shared guarded fetcher"
```

## Task 2: Migrate Streams To Shared Fetcher

**Files:**
- Modify: `internal/adapters/streams/fetch.go`
- Modify: `internal/adapters/streams/fetch_test.go`

- [ ] **Step 1: Write a failing wrapper compatibility test**

Add this test to `internal/adapters/streams/fetch_test.go`:

```go
func TestStreamsFetcherUsesSharedDeniedPrefixes(t *testing.T) {
	f := secureFetcher{resolver: staticResolver{"example.test": []string{"192.0.0.8"}}}
	_, err := f.Fetch(t.Context(), "https://example.test/catalog.json", fetchLimits{MaxBytes: 1024})
	if err == nil {
		t.Fatal("streams fetcher accepted 192.0.0.0/24, want shared classifier rejection")
	}
}
```

- [ ] **Step 2: Run the new Streams test and verify it fails or the package does not compile**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run TestStreamsFetcherUsesSharedDeniedPrefixes -count=1
```

Expected before migration: either FAIL because the old classifier accepts `192.0.0.8`, or compile failure if Task 1 removed copied symbols during local edits.

- [ ] **Step 3: Replace Streams private fetcher with aliases**

Edit `internal/adapters/streams/fetch.go` so it becomes a wrapper over `internal/sourcefetch`. Keep local names for existing Streams call sites:

```go
package streams

import (
	"context"
	"net"
	"net/http"
	"net/netip"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/sourcefetch"
)

const maxFetchRedirects = 3

type hostResolver = sourcefetch.Resolver
type fetchLimits = sourcefetch.Limits
type fetchCondition = sourcefetch.Condition
type fetchResponse = sourcefetch.Response
type validatedFetchTarget = sourcefetch.Target

type secureFetcher struct {
	resolver    hostResolver
	transport   *http.Transport
	dialContext func(context.Context, string, string) (net.Conn, error)
}

func (f secureFetcher) Fetch(ctx context.Context, rawURL string, limits fetchLimits) ([]byte, error) {
	resp, err := f.FetchConditional(ctx, rawURL, limits, fetchCondition{})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (f secureFetcher) FetchConditional(ctx context.Context, rawURL string, limits fetchLimits, condition fetchCondition) (fetchResponse, error) {
	limits.MaxRedirects = maxFetchRedirects
	if len(limits.AllowedSchemes) == 0 {
		if limits.AllowLocalURLs {
			limits.AllowedSchemes = []string{"http", "https"}
		} else {
			limits.AllowedSchemes = []string{"https"}
		}
	}
	return sourcefetch.Fetcher{
		Resolver:    f.resolver,
		Transport:   f.transport,
		DialContext: f.dialContext,
	}.Fetch(ctx, http.MethodGet, rawURL, limits, condition)
}

func isPublicIP(addr netip.Addr) bool {
	return sourcefetch.IsPublicRoutable(addr)
}
```

If any Streams tests reference removed helpers such as `readCappedBody`, update the tests to call `sourcefetch.ReadCappedBody` only in `internal/sourcefetch` tests. Do not keep a duplicate deny-prefix list in `internal/adapters/streams`.

- [ ] **Step 4: Run Streams fetch tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "TestFetch|TestStreamsFetcherUsesSharedDeniedPrefixes" -count=1
```

Expected: PASS.

- [ ] **Step 5: Run all Streams tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/streams/fetch.go internal/adapters/streams/fetch_test.go
git -c user.name=idio-sync -c user.email=jedivoodoo@gmail.com commit -m "refactor(streams): use shared source fetcher"
```

## Task 3: Migrate DLNA Address Classification

**Files:**
- Modify: `internal/adapters/dlna/urlvalidator.go`
- Modify: `internal/adapters/dlna/playlist.go`
- Modify: `internal/adapters/dlna/urlvalidator_test.go`
- Modify: `internal/adapters/dlna/playlist_security_test.go`

- [ ] **Step 1: Add failing DLNA shared-prefix regression tests**

Add this test to `internal/adapters/dlna/urlvalidator_test.go`:

```go
func TestValidateMediaURL_RejectsSharedDeniedPublicLookingRanges(t *testing.T) {
	cases := map[string]string{
		"benchmark":    "198.18.0.1",
		"old-6to4":     "192.88.99.1",
		"future-use":   "240.0.0.1",
		"documentation": "192.0.2.1",
	}
	for name, ip := range cases {
		t.Run(name, func(t *testing.T) {
			v := &urlValidator{
				resolver: func(context.Context, string) ([]net.IP, error) {
					return []net.IP{net.ParseIP(ip)}, nil
				},
				client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
				})},
			}
			_, err := v.validate(context.Background(), "http://media.example/movie.mp4", PolicyAllowPublic)
			if err == nil {
				t.Fatalf("%s accepted, want shared denied range rejection", ip)
			}
		})
	}
}
```

If `roundTripFunc` is not already defined in that test file, add:

```go
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
```

- [ ] **Step 2: Run the DLNA regression and verify it fails**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/dlna -run TestValidateMediaURL_RejectsSharedDeniedPublicLookingRanges -count=1
```

Expected: FAIL because the old DLNA classifier does not reject all shared prefixes.

- [ ] **Step 3: Replace DLNA classifier implementation**

In `internal/adapters/dlna/urlvalidator.go`, import the shared package:

```go
import "github.com/idio-sync/MiSTer_GroovyRelay/internal/sourcefetch"
```

Replace `classifyIP` with:

```go
func classifyIP(ip net.IP) ipClass {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return ipClassDisallowed
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsUnspecified() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() {
		return ipClassDisallowed
	}
	if addr.IsPrivate() || (addr.Is4() && cgnatNet.Contains(addr)) {
		return ipClassPrivate
	}
	if !sourcefetch.IsPublicRoutable(addr) {
		return ipClassDisallowed
	}
	return ipClassPublic
}
```

Keep `ipClassPrivate` behavior for RFC1918, ULA, and CGNAT so DLNA's `PolicyPrivateOnly` continues to accept LAN media URLs. The shared classifier is used to reject public-looking special-use ranges from the public bucket.

- [ ] **Step 4: Update HLS dial-time classifier**

In `internal/adapters/dlna/playlist.go`, keep the existing `classifyIP` calls. Because Step 3 changes `classifyIP`, HLS dial-time validation now inherits the shared deny-prefix policy without changing HLS fetch behavior.

- [ ] **Step 5: Run DLNA validator and HLS security tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/dlna -run "TestValidateMediaURL|TestPrepareHLSPlayback|TestHLSDial" -count=1
```

Expected: PASS.

- [ ] **Step 6: Run all DLNA tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/dlna -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/dlna/urlvalidator.go internal/adapters/dlna/playlist.go internal/adapters/dlna/urlvalidator_test.go internal/adapters/dlna/playlist_security_test.go
git -c user.name=idio-sync -c user.email=jedivoodoo@gmail.com commit -m "refactor(dlna): use shared address classifier"
```

## Task 4: Add Torrent URL Fetcher

**Files:**
- Create: `internal/adapters/torrent/url_fetcher.go`
- Create: `internal/adapters/torrent/url_fetcher_test.go`
- Modify: `internal/adapters/torrent/errors.go`

- [ ] **Step 1: Add failing Torrent URL predicate tests**

Create `internal/adapters/torrent/url_fetcher_test.go`:

```go
package torrent

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/sourcefetch"
)

func TestTorrentURLAcceptPredicate(t *testing.T) {
	tests := []struct {
		name        string
		original    string
		final       string
		contentType string
		want        bool
	}{
		{name: "original torrent path", original: "https://example.com/file.torrent", final: "https://example.com/file.torrent", contentType: "application/octet-stream", want: true},
		{name: "final torrent path", original: "https://example.com/download", final: "https://cdn.example/file.torrent", contentType: "application/octet-stream", want: true},
		{name: "bittorrent content type", original: "https://example.com/download", final: "https://example.com/download", contentType: "Application/X-Bittorrent; charset=binary", want: true},
		{name: "octet stream without torrent path", original: "https://example.com/download", final: "https://example.com/download", contentType: "application/octet-stream", want: false},
		{name: "ordinary binary", original: "https://example.com/video.mp4", final: "https://example.com/video.mp4", contentType: "video/mp4", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := torrentURLAcceptable(tt.original, tt.final, tt.contentType)
			if got != tt.want {
				t.Fatalf("torrentURLAcceptable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTorrentURLFetcherRejectsUnsafeInputBeforeSharedFetch(t *testing.T) {
	fetcher := &recordingSourceFetcher{}
	tf := torrentURLHTTPFetcher{source: fetcher}
	for _, raw := range []string{
		"ftp://example.com/file.torrent",
		"https://user:pass@example.com/file.torrent",
		"http://[::1]/file.torrent",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := tf.FetchTorrentURL(context.Background(), raw, maxTorrentUploadBytes)
			if err == nil {
				t.Fatalf("FetchTorrentURL accepted %s", raw)
			}
		})
	}
	if fetcher.calls != 0 {
		t.Fatalf("shared fetcher calls = %d, want 0", fetcher.calls)
	}
}

func TestTorrentURLFetcherRequiresHeadForNonTorrentPath(t *testing.T) {
	tf := torrentURLHTTPFetcher{source: &recordingSourceFetcher{
		headErr: errors.New("method not allowed"),
	}}
	_, err := tf.FetchTorrentURL(context.Background(), "https://example.com/download", maxTorrentUploadBytes)
	if err == nil || !strings.Contains(err.Error(), "must end in .torrent or support HEAD") {
		t.Fatalf("err = %v, want actionable HEAD error", err)
	}
}

func TestTorrentURLFetcherUsesCappedGetForTorrentPath(t *testing.T) {
	source := &recordingSourceFetcher{
		getResp: sourcefetch.Response{
			Body:        []byte("metainfo"),
			FinalURL:    "https://example.com/file.torrent",
			ContentType: "application/octet-stream",
		},
	}
	tf := torrentURLHTTPFetcher{source: source}
	got, err := tf.FetchTorrentURL(context.Background(), "https://example.com/file.torrent", maxTorrentUploadBytes)
	if err != nil {
		t.Fatalf("FetchTorrentURL: %v", err)
	}
	if string(got.Body) != "metainfo" || got.FinalURL != "https://example.com/file.torrent" || got.ContentType != "application/octet-stream" {
		t.Fatalf("result = %+v", got)
	}
	if source.heads != 0 {
		t.Fatalf("HEAD calls = %d, want 0 for explicit .torrent path", source.heads)
	}
	if source.gets != 1 || source.lastLimit.MaxBytes != maxTorrentUploadBytes {
		t.Fatalf("GET calls/limit = %d/%d", source.gets, source.lastLimit.MaxBytes)
	}
}

func TestTorrentURLFetcherRejectsOversizedSharedFetch(t *testing.T) {
	tf := torrentURLHTTPFetcher{source: &recordingSourceFetcher{
		getErr: sourcefetch.ErrBodyTooLarge,
	}}
	_, err := tf.FetchTorrentURL(context.Background(), "https://example.com/file.torrent", maxTorrentUploadBytes)
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrUploadTooLarge {
		t.Fatalf("err = %#v, want ErrUploadTooLarge", err)
	}
}

type recordingSourceFetcher struct {
	calls     int
	heads     int
	gets      int
	headResp  sourcefetch.Response
	headErr   error
	getResp   sourcefetch.Response
	getErr    error
	lastLimit sourcefetch.Limits
}

func (f *recordingSourceFetcher) Fetch(ctx context.Context, method string, rawURL string, limits sourcefetch.Limits, condition sourcefetch.Condition) (sourcefetch.Response, error) {
	f.calls++
	f.lastLimit = limits
	switch method {
	case http.MethodHead:
		f.heads++
		return f.headResp, f.headErr
	case http.MethodGet:
		f.gets++
		return f.getResp, f.getErr
	default:
		return sourcefetch.Response{}, errors.New("unexpected method")
	}
}
```

- [ ] **Step 2: Run the Torrent URL fetcher tests and verify they fail**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/torrent -run "TestTorrentURL" -count=1
```

Expected: FAIL because the Torrent URL fetcher types and helpers do not exist.

- [ ] **Step 3: Add shared body-too-large sentinel if missing**

In `internal/sourcefetch/fetch.go`, define:

```go
var ErrBodyTooLarge = errors.New("response body exceeds maximum size")
```

Update `ReadCappedBody` to wrap it:

```go
if int64(len(out)) > maxBytes {
	return nil, fmt.Errorf("%w: response body exceeds %d bytes", ErrBodyTooLarge, maxBytes)
}
```

Add `errors` to imports if needed.

- [ ] **Step 4: Implement `url_fetcher.go`**

Create `internal/adapters/torrent/url_fetcher.go`:

```go
package torrent

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/sourcefetch"
)

const torrentURLMaxRedirects = 3
const torrentURLFetchTimeout = 15 * time.Second
const torrentURLUserAgent = "MiSTer_GroovyRelay-torrent-url-fetcher/1"

type TorrentURLFetchResult struct {
	Body        []byte
	FinalURL    string
	ContentType string
}

type torrentURLFetcher interface {
	FetchTorrentURL(ctx context.Context, rawURL string, limit int64) (TorrentURLFetchResult, error)
}

type sourceFetcher interface {
	Fetch(ctx context.Context, method string, rawURL string, limits sourcefetch.Limits, condition sourcefetch.Condition) (sourcefetch.Response, error)
}

type torrentURLHTTPFetcher struct {
	source sourceFetcher
}

func (f torrentURLHTTPFetcher) FetchTorrentURL(ctx context.Context, rawURL string, limit int64) (TorrentURLFetchResult, error) {
	rawURL = strings.TrimSpace(rawURL)
	if err := validateTorrentURLInput(rawURL); err != nil {
		return TorrentURLFetchResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, torrentURLFetchTimeout)
	defer cancel()

	source := f.source
	if source == nil {
		source = sourcefetch.Fetcher{}
	}
	limits := sourcefetch.Limits{
		MaxBytes:       limit,
		MaxRedirects:   torrentURLMaxRedirects,
		AllowedSchemes: []string{"http", "https"},
		UserAgent:      torrentURLUserAgent,
	}

	if !torrentURLPathCandidate(rawURL) {
		head, err := source.Fetch(ctx, http.MethodHead, rawURL, limits, sourcefetch.Condition{})
		if err != nil {
			return TorrentURLFetchResult{}, &TorrentError{Kind: ErrBadInput, Message: "torrent URL must end in .torrent or support HEAD with a BitTorrent content type"}
		}
		if head.StatusCode < 200 || head.StatusCode > 299 || !torrentURLAcceptable(rawURL, head.FinalURL, head.ContentType) {
			return TorrentURLFetchResult{}, &TorrentError{Kind: ErrBadInput, Message: "URL does not look like a torrent file"}
		}
	}

	resp, err := source.Fetch(ctx, http.MethodGet, rawURL, limits, sourcefetch.Condition{})
	if err != nil {
		if errors.Is(err, sourcefetch.ErrBodyTooLarge) {
			return TorrentURLFetchResult{}, &TorrentError{Kind: ErrUploadTooLarge, Message: "torrent file exceeds 4 MiB"}
		}
		return TorrentURLFetchResult{}, &TorrentError{Kind: ErrBadInput, Message: "torrent URL fetch failed"}
	}
	if !torrentURLAcceptable(rawURL, resp.FinalURL, resp.ContentType) {
		return TorrentURLFetchResult{}, &TorrentError{Kind: ErrBadInput, Message: "URL does not look like a torrent file"}
	}
	return TorrentURLFetchResult{Body: resp.Body, FinalURL: resp.FinalURL, ContentType: resp.ContentType}, nil
}

func validateTorrentURLInput(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u == nil || !u.IsAbs() {
		return &TorrentError{Kind: ErrBadInput, Message: "invalid torrent URL"}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return &TorrentError{Kind: ErrBadInput, Message: "torrent URL must use http or https"}
	}
	if u.User != nil {
		return &TorrentError{Kind: ErrBadInput, Message: "torrent URL must not include credentials"}
	}
	host := u.Hostname()
	if host == "" {
		return &TorrentError{Kind: ErrBadInput, Message: "invalid torrent URL"}
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return &TorrentError{Kind: ErrBadInput, Message: "torrent URL resolves to a disallowed address"}
	}
	return nil
}

func torrentURLAcceptable(originalURL, finalURL, contentType string) bool {
	if torrentURLPathCandidate(originalURL) || torrentURLPathCandidate(finalURL) {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch strings.ToLower(mediaType) {
	case "application/x-bittorrent", "application/x-torrent":
		return true
	default:
		return false
	}
}

func torrentURLPathCandidate(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".torrent")
}

func sanitizeTorrentURLError(rawURL string, err error) error {
	if err == nil {
		return nil
	}
	if terr, ok := err.(*TorrentError); ok {
		return terr
	}
	return fmt.Errorf("%s", "torrent URL fetch failed")
}
```

- [ ] **Step 5: Run Torrent URL fetcher tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/torrent -run "TestTorrentURL" -count=1
```

Expected: PASS.

- [ ] **Step 6: Run sourcefetch tests after sentinel change**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/sourcefetch -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/sourcefetch/fetch.go internal/adapters/torrent/url_fetcher.go internal/adapters/torrent/url_fetcher_test.go internal/adapters/torrent/errors.go
git -c user.name=idio-sync -c user.email=jedivoodoo@gmail.com commit -m "feat(torrent): add guarded torrent url fetcher"
```

## Task 5: Start Torrent Sessions From Torrent URLs

**Files:**
- Modify: `internal/adapters/torrent/adapter.go`
- Modify: `internal/adapters/torrent/session.go`
- Modify: `internal/adapters/torrent/session_test.go`

- [ ] **Step 1: Add failing session tests**

Append to `internal/adapters/torrent/session_test.go`:

```go
func TestStartTorrentURLRequiresGatesBeforeFetch(t *testing.T) {
	fetcher := &fakeTorrentURLFetcher{}
	factoryCalls := 0
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   &recordingCore{},
		ClientFactory: func(ClientConfig) (TorrentClient, error) {
			factoryCalls++
			return &fakeTorrentClient{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.urlFetcher = fetcher
	_, err = a.startTorrentURL(context.Background(), "https://example.com/file.torrent")
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrDisabled {
		t.Fatalf("disabled err = %#v, want ErrDisabled", err)
	}
	a.SetEnabled(true)
	_, err = a.startTorrentURL(context.Background(), "https://example.com/file.torrent")
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrTrafficNotAcknowledged {
		t.Fatalf("traffic err = %#v, want ErrTrafficNotAcknowledged", err)
	}
	if fetcher.calls != 0 || factoryCalls != 0 {
		t.Fatalf("fetch/client touched before gates: fetch=%d factory=%d", fetcher.calls, factoryCalls)
	}
}

func TestStartTorrentURLRechecksGatesAfterFetchBeforeClient(t *testing.T) {
	fetcher := &fakeTorrentURLFetcher{body: []byte("metainfo")}
	factoryCalls := 0
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, startedTorrentConfig(), client, &recordingCore{})
	a.factory = func(ClientConfig) (TorrentClient, error) {
		factoryCalls++
		return client, nil
	}
	a.urlFetcher = fetcher
	fetcher.afterFetch = func() {
		a.mu.Lock()
		a.cfg.TrafficAcknowledged = false
		a.mu.Unlock()
	}
	_, err := a.startTorrentURL(context.Background(), "https://example.com/file.torrent")
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrTrafficNotAcknowledged {
		t.Fatalf("err = %#v, want ErrTrafficNotAcknowledged", err)
	}
	if factoryCalls != 0 || len(client.metainfo) != 0 {
		t.Fatalf("client touched after gate revoked: factory=%d metainfo=%d", factoryCalls, len(client.metainfo))
	}
}

func TestStartTorrentURLStartsFromFetchedBytes(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	client.metaTorrent = &fakeTorrent{
		hash:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		name:  "movie",
		files: []FileCandidate{{DisplayPath: "movie.mkv", Length: 10, Index: 0}},
	}
	a := newStartedTestAdapter(t, startedTorrentConfig(), client, core)
	a.urlFetcher = &fakeTorrentURLFetcher{body: []byte("metainfo")}
	started, err := a.startTorrentURL(context.Background(), "https://example.com/file.torrent")
	if err != nil {
		t.Fatalf("startTorrentURL: %v", err)
	}
	if started.AdapterRef == "" || len(core.reqs) != 1 {
		t.Fatalf("started=%#v core calls=%d", started, len(core.reqs))
	}
	if string(client.metainfo[0]) != "metainfo" {
		t.Fatalf("metainfo = %q", client.metainfo[0])
	}
}

type fakeTorrentURLFetcher struct {
	calls      int
	body       []byte
	err        error
	afterFetch func()
}

func (f *fakeTorrentURLFetcher) FetchTorrentURL(ctx context.Context, rawURL string, limit int64) (TorrentURLFetchResult, error) {
	f.calls++
	if f.afterFetch != nil {
		defer f.afterFetch()
	}
	if f.err != nil {
		return TorrentURLFetchResult{}, f.err
	}
	return TorrentURLFetchResult{Body: f.body, FinalURL: rawURL, ContentType: "application/x-bittorrent"}, nil
}
```

Modify `fakeTorrentClient` in the same file:

```go
type fakeTorrentClient struct {
	magnets      []string
	metainfo     [][]byte
	byHash       map[string]*fakeTorrent
	closes       int
	files        []FileCandidate
	waitErr      error
	addMagnetErr func(raw string) error
	metaTorrent  *fakeTorrent
}

func (f *fakeTorrentClient) AddMetaInfo(ctx context.Context, body []byte) (TorrentHandle, bool, error) {
	f.metainfo = append(f.metainfo, body)
	if f.byHash == nil {
		f.byHash = make(map[string]*fakeTorrent)
	}
	if f.metaTorrent != nil {
		f.byHash[f.metaTorrent.hash] = f.metaTorrent
		return f.metaTorrent, true, nil
	}
	t := &fakeTorrent{
		hash:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		name:  "movie",
		files: []FileCandidate{{DisplayPath: "movie.mkv", Length: 10, Index: 0}},
	}
	f.byHash[t.hash] = t
	return t, true, nil
}
```

- [ ] **Step 2: Run the session tests and verify they fail**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/torrent -run "TestStartTorrentURL" -count=1
```

Expected: FAIL because `Adapter.urlFetcher`, `startTorrentURL`, and helper code do not exist.

- [ ] **Step 3: Add adapter field and default fetcher**

In `internal/adapters/torrent/adapter.go`, add to `Adapter`:

```go
	urlFetcher torrentURLFetcher
```

In `New`, set:

```go
urlFetcher: torrentURLHTTPFetcher{},
```

- [ ] **Step 4: Refactor byte starts and implement URL starts**

In `internal/adapters/torrent/session.go`, replace `startTorrentBytes` with:

```go
func (a *Adapter) startTorrentBytes(ctx context.Context, body []byte) (*StartedSession, error) {
	cfg, err := a.snapshotForStart()
	if err != nil {
		return nil, err
	}
	return a.startTorrentBytesWithConfig(ctx, cfg, body)
}

func (a *Adapter) startTorrentBytesWithConfig(ctx context.Context, cfg Config, body []byte) (*StartedSession, error) {
	client, err := a.ensureClient(cfg)
	if err != nil {
		return nil, err
	}
	t, isNew, err := client.AddMetaInfo(ctx, body)
	if err != nil {
		return nil, &TorrentError{Kind: ErrBadInput, Message: "torrent file could not be added", Err: err}
	}
	return a.startTorrentHandle(ctx, cfg, t, isNew)
}
```

Add:

```go
func (a *Adapter) startTorrentURL(ctx context.Context, rawURL string) (*StartedSession, error) {
	if _, err := url.Parse(strings.TrimSpace(rawURL)); err != nil {
		return nil, &TorrentError{Kind: ErrBadInput, Message: "invalid torrent URL"}
	}
	if _, err := a.snapshotForStart(); err != nil {
		return nil, err
	}
	fetcher := a.urlFetcher
	if fetcher == nil {
		fetcher = torrentURLHTTPFetcher{}
	}
	res, err := fetcher.FetchTorrentURL(ctx, rawURL, maxTorrentUploadBytes)
	if err != nil {
		return nil, err
	}
	cfg, err := a.snapshotForStart()
	if err != nil {
		return nil, err
	}
	return a.startTorrentBytesWithConfig(ctx, cfg, res.Body)
}
```

Add imports if needed:

```go
import (
	"net/url"
	"strings"
)
```

Keep existing imports de-duplicated.

- [ ] **Step 5: Run the session tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/torrent -run "TestStartTorrent(URL|Bytes|Magnet)" -count=1
```

Expected: PASS.

- [ ] **Step 6: Run all Torrent tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/torrent -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/torrent/adapter.go internal/adapters/torrent/session.go internal/adapters/torrent/session_test.go
git -c user.name=idio-sync -c user.email=jedivoodoo@gmail.com commit -m "feat(torrent): start sessions from torrent urls"
```

## Task 6: Expose Torrent URL Quick Cast

**Files:**
- Modify: `internal/adapters/torrent/playback_provider.go`
- Modify: `internal/adapters/torrent/playback_provider_test.go`
- Modify: `internal/ui/playback_test.go`

- [ ] **Step 1: Add failing quick-cast provider tests**

Append to `internal/adapters/torrent/playback_provider_test.go`:

```go
func TestTorrentQuickCastTabsIncludeTorrentURL(t *testing.T) {
	a := &Adapter{cfg: Config{Enabled: true, TrafficAcknowledged: true}}
	tabs := a.QuickCastTabs()
	var found adapters.QuickCastTab
	for _, tab := range tabs {
		if tab.ID == "torrent-url" {
			found = tab
			break
		}
	}
	if found.ID == "" {
		t.Fatalf("torrent-url tab missing: %#v", tabs)
	}
	if !found.Enabled || found.Encoding != adapters.QuickCastEncodingForm {
		t.Fatalf("torrent-url tab = %#v", found)
	}
	if len(found.Fields) != 1 || found.Fields[0].Name != "torrent_url" || found.Fields[0].Type != "url" {
		t.Fatalf("torrent-url fields = %#v", found.Fields)
	}
}

func TestTorrentQuickCastRejectsEmptyTorrentURL(t *testing.T) {
	a := &Adapter{cfg: Config{Enabled: true, TrafficAcknowledged: true}}
	_, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{TabID: "torrent-url", Values: map[string]string{"torrent_url": " "}})
	if err == nil || !strings.Contains(err.Error(), "torrent_url is required") {
		t.Fatalf("err = %v, want torrent_url required", err)
	}
}

func TestTorrentQuickCastStartsTorrentURL(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	client.metaTorrent = &fakeTorrent{
		hash:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		name:  "movie",
		files: []FileCandidate{{DisplayPath: "movie.mkv", Length: 10, Index: 0}},
	}
	a := newStartedTestAdapter(t, startedTorrentConfig(), client, core)
	a.urlFetcher = &fakeTorrentURLFetcher{body: []byte("metainfo")}
	got, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{TabID: "torrent-url", Values: map[string]string{"torrent_url": "https://example.com/file.torrent"}})
	if err != nil {
		t.Fatalf("HandleQuickCast torrent-url: %v", err)
	}
	if got.Message != "torrent started" || got.AdapterRef == "" {
		t.Fatalf("result = %#v", got)
	}
	if len(core.reqs) != 1 {
		t.Fatalf("core starts = %d, want 1", len(core.reqs))
	}
}
```

- [ ] **Step 2: Run quick-cast tests and verify they fail**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/torrent -run "TestTorrentQuickCast.*TorrentURL|TestTorrentQuickCastTabsIncludeTorrentURL" -count=1
```

Expected: FAIL because the new tab and branch do not exist.

- [ ] **Step 3: Add tab and handler branch**

In `internal/adapters/torrent/playback_provider.go`, add the third tab to `QuickCastTabs()`:

```go
{
	ID:             "torrent-url",
	Label:          "Torrent URL",
	Enabled:        enabled && ack,
	DisabledReason: disabled,
	Encoding:       adapters.QuickCastEncodingForm,
	Fields: []adapters.QuickCastField{{
		Name:        "torrent_url",
		Label:       "Torrent URL",
		Type:        "url",
		Placeholder: "https://example.com/file.torrent",
		Required:    true,
	}},
},
```

Add this `HandleQuickCast` branch before `default`:

```go
case "torrent-url":
	raw := strings.TrimSpace(req.Values["torrent_url"])
	if raw == "" {
		return adapters.QuickCastResult{}, fmt.Errorf("torrent_url is required")
	}
	started, err := a.startTorrentURL(ctx, raw)
	if err != nil {
		return adapters.QuickCastResult{}, err
	}
	return adapters.QuickCastResult{Message: "torrent started", AdapterRef: started.AdapterRef}, nil
```

- [ ] **Step 4: Run Torrent quick-cast tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/torrent -run "TestTorrent(QuickCast|PlaybackBanner|Stop)" -count=1
```

Expected: PASS.

- [ ] **Step 5: Add UI rendering regression**

In `internal/ui/playback_test.go`, find the quick-cast tab rendering test that currently mentions `torrent-magnet`, and extend its fake tab list to include:

```go
{ID: "torrent-url", Label: "Torrent URL", Enabled: true, Encoding: adapters.QuickCastEncodingForm, Fields: []adapters.QuickCastField{{Name: "torrent_url", Label: "Torrent URL", Type: "url", Required: true}}},
```

Add assertions:

```go
if !strings.Contains(body, `hx-get="/ui/playback/banner?drawer=cast&tab=torrent-url"`) {
	t.Fatalf("torrent-url tab missing from quick-cast drawer:\n%s", body)
}
if !strings.Contains(body, `name="torrent_url"`) {
	t.Fatalf("torrent_url input missing from quick-cast drawer:\n%s", body)
}
```

- [ ] **Step 6: Run UI quick-cast tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/ui -run "TestQuickCast|TestPlayback" -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/torrent/playback_provider.go internal/adapters/torrent/playback_provider_test.go internal/ui/playback_test.go
git -c user.name=idio-sync -c user.email=jedivoodoo@gmail.com commit -m "feat(torrent): expose torrent url quick cast"
```

## Task 7: Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/torrent.md`

- [ ] **Step 1: Update README feature list and adapter table**

In `README.md`, change:

```markdown
- Torrent streaming (uploaded .torrent files and magnet links)
```

to:

```markdown
- Torrent streaming (magnet links, uploaded `.torrent` files, and HTTP(S) `.torrent` URLs)
```

Change the Torrent row in the adapter table from:

```markdown
| Torrent | Global Cast drawer magnet link or `.torrent` upload | Off | Requires explicit traffic acknowledgement. See [docs/torrent.md](docs/torrent.md). |
```

to:

```markdown
| Torrent | Global Cast drawer magnet link, `.torrent` upload, or HTTP(S) `.torrent` URL | Off | Requires explicit traffic acknowledgement. Remote torrent URLs are public HTTP(S) only. See [docs/torrent.md](docs/torrent.md). |
```

- [ ] **Step 2: Update docs/torrent.md**

Append this section after "Enablement":

```markdown
## Torrent URLs

The Torrent adapter can also cast an HTTP(S) URL that points to BitTorrent metainfo. Use the **Torrent URL** quick-cast tab and paste a URL such as:

```text
https://example.com/movie.torrent
```

Remote torrent URL fetching is public HTTP(S) only. The bridge rejects URL credentials, IP-literal hosts, private/local/link-local/multicast/special-use addresses, unsafe redirects, and responses over 4 MiB. If the URL path does not end in `.torrent`, the server must support `HEAD` and return a BitTorrent content type (`application/x-bittorrent` or `application/x-torrent`) before the bridge will download the body.

Servers that only allow presigned `GET` requests should use a URL path ending in `.torrent`. Otherwise the bridge returns an error instead of downloading an arbitrary body to sniff it.
```

- [ ] **Step 3: Run docs grep checks**

Run:

```bash
rg -n "HTTP\\(S\\).*torrent|Torrent URL|magnet links, uploaded" README.md docs/torrent.md
```

Expected: output includes README feature list/table and the new `docs/torrent.md` section.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/torrent.md
git -c user.name=idio-sync -c user.email=jedivoodoo@gmail.com commit -m "docs(torrent): document torrent url casting"
```

## Task 8: Full Verification

**Files:**
- No source edits unless verification exposes a failure.

- [ ] **Step 1: Run package test matrix**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/sourcefetch ./internal/adapters/streams ./internal/adapters/dlna ./internal/adapters/torrent ./internal/ui -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader Go tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run static checks if available**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe vet ./...
```

Expected: PASS.

- [ ] **Step 4: Inspect final diff**

Run:

```bash
git status --short
git log --oneline -8
```

Expected:

- only intentional changed files are present;
- no unrelated README or `.gitignore` edits are staged unless Task 7 intentionally touched README;
- commits match the commit plan.

## Self-Review Checklist

- Spec coverage:
  - Shared guarded fetcher: Tasks 1-3.
  - Normative deny prefixes and capped reader: Task 1.
  - Streams/DLNA migration: Tasks 2-3.
  - Torrent URL acceptance, `HEAD`/`GET`, safe errors: Task 4.
  - Post-fetch gate recheck and metainfo start: Task 5.
  - Quick-cast user surface: Task 6.
  - Docs: Task 7.
  - Verification: Task 8.
- No unresolved placeholder markers remain.
- Type consistency:
  - Shared fetcher type is `sourcefetch.Fetcher`.
  - Shared limits type is `sourcefetch.Limits`.
  - Torrent fetch result type is `TorrentURLFetchResult`.
  - Adapter seam is `torrentURLFetcher`.
  - Quick-cast tab ID is `torrent-url`.
  - Form field is `torrent_url`.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-23-torrent-url-resolution.md`. Two execution options:

**1. Subagent-Driven (recommended)** - dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** - execute tasks in this session using executing-plans, batch execution with checkpoints.
