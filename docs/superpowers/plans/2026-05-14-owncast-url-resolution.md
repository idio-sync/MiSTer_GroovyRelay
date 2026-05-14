# Owncast URL Resolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the URL adapter play Owncast homepage URLs by resolving detected Owncast hosts to same-origin `/hls/stream.m3u8`.

**Architecture:** Add a small package-local Owncast resolver in `internal/adapters/url/owncast.go`. `castURLWithStarter` keeps streams handoff first, keeps yt-dlp hosts on the yt-dlp path, and applies the Owncast rewrite only on direct-play candidates.

**Tech Stack:** Go standard library HTTP/JSON, existing URL adapter tests, `go test`.

---

### File Structure

- Create `internal/adapters/url/owncast.go`: Owncast detection and same-origin HLS URL construction.
- Modify `internal/adapters/url/play.go`: call the resolver after route decision when the request will otherwise use direct playback.
- Modify `internal/adapters/url/play_test.go`: add route-level tests proving the URL adapter passes the rewritten or original URL into `core.SessionRequest`.
- Modify `docs/url-adapter.md`: document Owncast homepage convenience.

### Task 1: Owncast Homepage Rewrite

**Files:**
- Create: `internal/adapters/url/owncast.go`
- Modify: `internal/adapters/url/play.go`
- Test: `internal/adapters/url/play_test.go`

- [ ] **Step 1: Write failing tests**

Add tests to `internal/adapters/url/play_test.go`:

```go
func TestCastURL_OwncastHomepageResolvesToHLS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			t.Fatalf("unexpected probe path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"serverTime":"2026-05-14T00:00:00Z","versionNumber":"0.2.3","online":true}`))
	}))
	t.Cleanup(server.Close)
	a := newTestAdapter(t, &fakeCore{})

	ref, via, status, err := a.castURL(t.Context(), server.URL+"/", "auto")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if ref == "" || via != "direct" || status != http.StatusOK {
		t.Fatalf("ref=%q via=%q status=%d", ref, via, status)
	}
	if got, want := a.core.(*fakeCore).snapshot().StreamURL, server.URL+"/hls/stream.m3u8"; got != want {
		t.Fatalf("StreamURL = %q, want %q", got, want)
	}
}

func TestCastURL_OwncastDirectHLSIsUntouched(t *testing.T) {
	probes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes++
		http.Error(w, "should not probe direct hls", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	a := newTestAdapter(t, &fakeCore{})
	raw := server.URL + "/hls/stream.m3u8"

	_, _, _, err := a.castURL(t.Context(), raw, "auto")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if got := a.core.(*fakeCore).snapshot().StreamURL; got != raw {
		t.Fatalf("StreamURL = %q, want %q", got, raw)
	}
	if probes != 0 {
		t.Fatalf("Owncast probe count = %d, want 0", probes)
	}
}

func TestCastURL_NonOwncastHomepageFallsThrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"not owncast"}`))
	}))
	t.Cleanup(server.Close)
	a := newTestAdapter(t, &fakeCore{})
	raw := server.URL + "/"

	_, _, _, err := a.castURL(t.Context(), raw, "auto")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if got := a.core.(*fakeCore).snapshot().StreamURL; got != raw {
		t.Fatalf("StreamURL = %q, want %q", got, raw)
	}
}

func TestCastURL_OwncastProbeFailureFallsThrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	a := newTestAdapter(t, &fakeCore{})
	raw := server.URL + "/"

	_, _, _, err := a.castURL(t.Context(), raw, "auto")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if got := a.core.(*fakeCore).snapshot().StreamURL; got != raw {
		t.Fatalf("StreamURL = %q, want %q", got, raw)
	}
}

func TestCastURL_OwncastForcedDirectStillResolves(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"serverTime":"2026-05-14T00:00:00Z","versionNumber":"0.2.3","online":false}`))
	}))
	t.Cleanup(server.Close)
	a := newTestAdapter(t, &fakeCore{})

	_, _, _, err := a.castURL(t.Context(), server.URL+"/", "direct")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if got, want := a.core.(*fakeCore).snapshot().StreamURL, server.URL+"/hls/stream.m3u8"; got != want {
		t.Fatalf("StreamURL = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url -run Owncast -count=1
```

Expected: FAIL because homepage URLs still pass through unchanged.

- [ ] **Step 3: Add minimal implementation**

Create `internal/adapters/url/owncast.go`:

```go
package url

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	stdurl "net/url"
	"path"
	"strings"
	"time"
)

const owncastProbeTimeout = 1500 * time.Millisecond

var owncastProbeHTTPClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type owncastStatusResponse struct {
	ServerTime    string `json:"serverTime"`
	VersionNumber string `json:"versionNumber"`
	Online        *bool  `json:"online"`
}

func resolveOwncastHomepageURL(ctx context.Context, parsed *stdurl.URL) (string, bool) {
	if !isOwncastProbeCandidate(parsed) {
		return "", false
	}
	statusURL := owncastStatusURL(parsed)
	reqCtx, cancel := context.WithTimeout(ctx, owncastProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, statusURL, nil)
	if err != nil {
		return "", false
	}
	resp, err := owncastProbeHTTPClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", false
	}
	var status owncastStatusResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&status); err != nil {
		return "", false
	}
	if !status.looksLikeOwncast() {
		return "", false
	}
	return owncastHLSURL(parsed), true
}

func isOwncastProbeCandidate(u *stdurl.URL) bool {
	if u == nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	if u.Host == "" {
		return false
	}
	ext := strings.ToLower(path.Ext(u.EscapedPath()))
	if ext != "" {
		return false
	}
	return true
}

func owncastStatusURL(u *stdurl.URL) string {
	probe := *u
	probe.Path = "/api/status"
	probe.RawPath = ""
	probe.RawQuery = ""
	probe.Fragment = ""
	return probe.String()
}

func owncastHLSURL(u *stdurl.URL) string {
	hls := *u
	hls.Path = "/hls/stream.m3u8"
	hls.RawPath = ""
	hls.RawQuery = ""
	hls.Fragment = ""
	return hls.String()
}

func (s owncastStatusResponse) looksLikeOwncast() bool {
	return strings.TrimSpace(s.ServerTime) != "" &&
		strings.TrimSpace(s.VersionNumber) != "" &&
		s.Online != nil
}
```

Modify `internal/adapters/url/play.go` after `decideRoute` succeeds and before `streamURL := rawURL` is used:

```go
	if !useYtdlp {
		if owncastURL, ok := resolveOwncastHomepageURL(ctx, parsed); ok {
			rawURL = owncastURL
		}
	}
```

Ensure title derivation still uses the originally parsed submitted URL so homepage entries display the host.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url -run Owncast -count=1
```

Expected: PASS.

### Task 2: Documentation and Regression Suite

**Files:**
- Modify: `docs/url-adapter.md`
- Verify: `internal/adapters/url`

- [ ] **Step 1: Update docs**

In `docs/url-adapter.md`, update the accepted URL table so Direct media examples mention Owncast homepages:

```markdown
| Direct media | MP4, MKV, HLS `.m3u8`, DASH `.mpd`, Owncast homepage URLs | FFmpeg |
```

Add a short note below the table:

```markdown
Owncast sites can be pasted as their homepage URL. The adapter detects Owncast through the same-origin `/api/status` endpoint and plays `/hls/stream.m3u8`.
```

- [ ] **Step 2: Run package tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url -count=1
```

Expected: PASS.

- [ ] **Step 3: Run full test suite**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./...
```

Expected: PASS.
