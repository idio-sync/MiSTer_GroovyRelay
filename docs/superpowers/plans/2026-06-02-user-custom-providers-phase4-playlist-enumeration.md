# User Custom Providers — Phase 4: Playlist Enumeration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make user-provider `playlist` channels (skipped since Phase 3) enumerate their videos via `yt-dlp --flat-playlist`, drop unsafe enumerated page URLs, cache the sanitized result, and appear as castable catalog items — with no playback-path changes (Phase 3 already revalidates every resolved user media URL).

**Architecture:** Add an `EnumeratePlaylist` capability to the yt-dlp resolver returning a new `ytdlp.PlaylistEntry` slice; extend the streams `streamResolver` interface and `fakeResolver` to match. Add a sanitized playlist cache (spec §4.6) and a small `userPlaylistEnumerator` seam that enumerates-live-then-caches during refresh and serves-cache-only (non-blocking) at startup, with serve-stale on transient failure. Enumerated entries are canonicalized to YouTube watch URLs when possible; non-YouTube page URLs must pass the existing user-provider URL gate before they are cached or later handed to yt-dlp. Make `buildUserCatalog` enumeration-aware (playlist channels fill from the enumerator; `direct`/`single` unchanged), then thread the enumerator through the existing startup/remote/catalog refresh paths exactly as bundled providers are threaded.

**Tech Stack:** Go 1.26, `internal/adapters/url/ytdlp` (yt-dlp Runner), package `internal/adapters/streams`. Tests use the existing `fakeResolver`/`stubRunner` harnesses; no network and no new integration-tagged tests (spec §12: "drive with the existing fake-resolver harness").

---

## Background: what is already merged (do NOT re-create)

- **Phase 1/2/3 are merged.** `userProviderType="user"`, `kindPlaylist`/`kindSingle`/`kindDirect`, `isUserProviderID`, `normalizeUserProvider`, `userProviderStore`, `validateUserProviderHost`/`validateUserProviderIP`/`userDirectInputPolicy`, and the Phase 3 catalog wiring (`buildUserCatalog`, `Catalog()` exposure, snapshot merge, play-time `revalidateResolvedUserURLs`).
- **The §4.6 cache-key helpers already exist** — `userProviderCacheKey(providerID)` AND `userPlaylistCacheKey(providerID, channelID)` are in `internal/adapters/streams/user_cache_key.go:9-15` (using `sha256Hex` from cache.go), with coverage in `user_cache_key_test.go` (`TestUserCacheKeys_ValidateAndStable`). **Do NOT re-create `userPlaylistCacheKey`** — Phase 4 only *consumes* it.
- **`buildUserCatalog` currently SKIPS playlist channels** with an info log (`provider_user.go:319-323`): `case kindPlaylist: slog.Info(...); continue`. This phase replaces that skip with enumeration.
- **Play-time media security needs no change.** `playback.go:485` already calls `a.revalidateResolvedUserURLs(ctx, resolved.URL, resolved.AudioURL)` for any `isUserProviderID(q.ProviderID)`. Playlist items are `Direct:false`, so they flow through the same yt-dlp resolve path as `single` items and inherit that recheck. **No `playback.go` edits in Phase 4.** Phase 4 still validates enumerated *page* URLs before caching because yt-dlp dereferences those pages before the resolved-media recheck can run.
- **`Resolve` uses `--no-playlist`** (`ytdlp/resolver.go:115`), so `--flat-playlist` is genuinely a new code path.

## Scope (Phase 4 = backend enumeration only)

**In scope:** `EnumeratePlaylist` resolver capability; sanitized playlist cache (§4.6); enumeration-aware `buildUserCatalog`; wiring into startup (cache-only, non-blocking) and refresh (live + cache, serve-stale); per-channel failure is logged and the provider stays usable.

**Out of scope (later phases, per Phase 3 plan §"Out of scope"):**
- Chassis routes, `UserProviderEditor` interface, the authoring form, the `catalog`/`providerStatus` SSE envelope that surfaces "Enumerating…"/error chips to an open drawer — **Phase 5/6**. Phase 4 represents the "pending" state simply as "channel has 0 items until the next refresh populates it" and the "error" state as "logged + serve-stale + provider still usable"; there is no UI to surface them yet.
- `is_live` chips / Verify dry-run — **Phase 5** (`is_live` is derived at play time, never persisted — spec §9 item 5).
- Auto-enable / hot-start — **Phase 5**.

## Known residual (documented, addressed in Phase 5)

- **`AllowRemoteManifest=false` + playlist channels.** The refresh loop only starts when `cfg.Enabled && cfg.AllowRemoteManifest` (`adapter.go:278`). With remote manifest off, the loop does not run, so live enumeration is driven only by a manual `RefreshNow(providerID)` (which enumerates — it calls `refreshCatalogsDefault`). In the normal case (remote manifest on, required by the bundled providers), the loop's first job runs immediately (`lastManifest.IsZero()`), so playlists populate within the first refresh cycle. Promptly populating local-only setups is folded into Phase 5's lifecycle work (`EnsureStarted`). This phase does not change the loop-start gate.

## Security invariants (unchanged from Phase 2/3)

- Enumeration runs `yt-dlp` against the operator-supplied playlist page URL — the **same operator/yt-dlp trust boundary already accepted** for authored `single` items (spec §7.3). Enumeration is time-bounded by `CatalogRequestTimeoutSeconds` (spec §6).
- Enumerated item page URLs are sanitized before cache/use: YouTube IDs become canonical watch URLs; non-YouTube URLs must pass `validateUserProviderHost` (http(s), no userinfo, no blocked IP literals). Unsafe entries are dropped and never cached.
- Enumerated items are `Direct:false`. At play time each resolved media `URL`/`AudioURL` is rechecked by the already-merged `revalidateResolvedUserURLs` (Phase 3, Task 8). Phase 4 adds no new FFmpeg-facing URL path.
- Enumeration errors are logged with the playlist page URL redacted before surfacing stderr-derived messages.

## Workflow conventions

- Go 1.26. `go test -race` CANNOT run locally (no cgo/gcc) — CI-only gate. Use plain `go test`. Run `go vet ./...` and `go test ./internal/adapters/streams/... ./internal/adapters/url/ytdlp/...` locally; keep all four CI gates green.
- `docs/superpowers/` is gitignored — commit the plan and any new docs with `git add -f`. Stage ONLY the intended paths; verify with `git diff --cached --name-only` before each commit.
- `a.mu` is never held across network I/O. Enumeration is network I/O, so it happens in the snapshot/catalog **build** phase BEFORE `a.mu` is taken; the lock is acquired only to install the result (mirrors the existing `fetchProviderPlaylist` → build → `a.mu.Lock()` → store pattern in `refreshCatalogsDefault`).

---

## File Structure

**New**
- `internal/adapters/streams/playlist_enum.go` — `cachedPlaylistItem` + `encodePlaylistItems`/`decodePlaylistItems`, `playlistEntriesToItems` (canonicalize/drop unsafe entries), redacted playlist-error logging helper, and the `userPlaylistEnumerator` seam (live-enumerate+cache / cache-only / serve-stale). (The cache KEY, `userPlaylistCacheKey`, already exists in `user_cache_key.go` — reused, not re-created.)
- `internal/adapters/streams/playlist_enum_test.go` — round-trip, entry-mapping, and enumerator (live/cache-only/serve-stale) tests. (Cache-key validity is already covered by `user_cache_key_test.go`.)

**Modified**
- `internal/adapters/url/ytdlp/resolver.go` — `PlaylistEntry` type + `EnumeratePlaylist` method.
- `internal/adapters/url/ytdlp/resolver_test.go` — `EnumeratePlaylist` argv + NDJSON parse tests.
- `internal/adapters/streams/adapter.go` — extend the `streamResolver` interface with `EnumeratePlaylist`.
- `internal/adapters/streams/test_helpers_test.go` — extend `fakeResolver` with `EnumeratePlaylist` + a compile-time `var _ streamResolver = (*ytdlp.Resolver)(nil)` assertion.
- `internal/adapters/streams/provider_user.go` — enumeration-aware `buildUserCatalog(ctx, def, enum)` (playlist channels enumerate; `direct`/`single` unchanged; remove the skip).
- `internal/adapters/streams/provider_user_test.go` — update `TestBuildUserCatalog_KindsProduceCorrectItems` for the new signature + playlist-now-appears behavior.
- `internal/adapters/streams/refresh.go` — `buildProviderCatalog` user case becomes cache-only; `buildCachedOrSeedSnapshot` takes `ctx` and a cache-only enumerator; `buildRemoteSnapshot` takes a live enumerator; `refreshCatalogsDefault` builds a live enumerator for user providers.
- `internal/adapters/streams/refresh_test.go` — update the existing `buildRemoteSnapshot` call (signature change) + add merge/enumeration tests.

---

## Task 1: `EnumeratePlaylist` on the yt-dlp resolver

**Files:**
- Modify: `internal/adapters/url/ytdlp/resolver.go` (add `PlaylistEntry` after `Resolution` ~line 62; add method after `Resolve` ~line 223)
- Test: `internal/adapters/url/ytdlp/resolver_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/url/ytdlp/resolver_test.go`:

```go
// flatPlaylistNDJSON is two yt-dlp --flat-playlist --dump-json entries
// (one JSON object per line, no enclosing array).
const flatPlaylistNDJSON = `{"id":"dQw4w9WgXcQ","url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ","title":"First"}
{"id":"abcdefghijk","url":"https://www.youtube.com/watch?v=abcdefghijk","title":"Second"}`

func TestEnumeratePlaylist_BuildsArgvAndParses(t *testing.T) {
	r := &stubRunner{stdouts: [][]byte{[]byte(flatPlaylistNDJSON)}}
	res := Resolver{Binary: "/usr/local/bin/yt-dlp", Timeout: 5 * time.Second, Runner: r}

	entries, err := res.EnumeratePlaylist(context.Background(), "https://youtube.com/playlist?list=PL1", "", 50)
	if err != nil {
		t.Fatalf("EnumeratePlaylist: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(r.calls))
	}
	got := r.calls[0]
	mustContain(t, got, "--flat-playlist")
	mustContain(t, got, "--dump-json")
	mustContain(t, got, "--playlist-end")
	mustContain(t, got, "50")
	mustContain(t, got, "https://youtube.com/playlist?list=PL1")
	// --no-playlist would defeat enumeration; it must NOT be present.
	for _, a := range got {
		if a == "--no-playlist" {
			t.Fatal("argv contains --no-playlist; flat-playlist enumeration needs the playlist")
		}
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].ID != "dQw4w9WgXcQ" || entries[0].Title != "First" ||
		entries[0].URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("entries[0] = %+v", entries[0])
	}
	if entries[1].ID != "abcdefghijk" {
		t.Fatalf("entries[1].ID = %q, want abcdefghijk", entries[1].ID)
	}
}

func TestEnumeratePlaylist_CookiesAndCap(t *testing.T) {
	r := &stubRunner{stdouts: [][]byte{[]byte(flatPlaylistNDJSON)}}
	res := Resolver{Binary: "/usr/local/bin/yt-dlp", Timeout: 5 * time.Second, Runner: r}

	entries, err := res.EnumeratePlaylist(context.Background(), "https://youtube.com/playlist?list=PL1", "/data/cookies.txt", 1)
	if err != nil {
		t.Fatalf("EnumeratePlaylist: %v", err)
	}
	mustContain(t, r.calls[0], "--cookies")
	mustContain(t, r.calls[0], "/data/cookies.txt")
	mustContain(t, r.calls[0], "1") // --playlist-end 1
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (capped by maxItems)", len(entries))
	}
}

func TestEnumeratePlaylist_ErrorSummarizesStderr(t *testing.T) {
	r := &stubRunner{
		stderrs: [][]byte{[]byte("ERROR: [youtube:tab] PL1: The playlist does not exist")},
		errs:    []error{errors.New("exit status 1")},
	}
	res := Resolver{Binary: "/usr/local/bin/yt-dlp", Timeout: 5 * time.Second, Runner: r}
	if _, err := res.EnumeratePlaylist(context.Background(), "https://youtube.com/playlist?list=PL1", "", 50); err == nil {
		t.Fatal("EnumeratePlaylist err = nil, want playlist-does-not-exist error")
	} else if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %q, want it to summarize stderr", err.Error())
	}
}

func TestEnumeratePlaylist_BinaryNotConfigured(t *testing.T) {
	res := Resolver{Timeout: 5 * time.Second, Runner: &stubRunner{}}
	if _, err := res.EnumeratePlaylist(context.Background(), "https://youtube.com/playlist?list=PL1", "", 50); err == nil {
		t.Fatal("err = nil, want binary-not-configured error")
	}
}

func TestEnumeratePlaylist_RespectsCallerDeadlineOverResolverTimeout(t *testing.T) {
	r := &stubRunner{
		stdouts: [][]byte{[]byte(flatPlaylistNDJSON)},
		delays:  []time.Duration{40 * time.Millisecond},
	}
	res := Resolver{Binary: "yt-dlp", Timeout: 5 * time.Millisecond, Runner: r}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	entries, err := res.EnumeratePlaylist(ctx, "https://youtube.com/playlist?list=PL1", "", 50)
	if err != nil {
		t.Fatalf("EnumeratePlaylist with caller deadline: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/url/ytdlp/ -run TestEnumeratePlaylist`
Expected: FAIL — `res.EnumeratePlaylist undefined (type Resolver has no field or method EnumeratePlaylist)`.

- [ ] **Step 3: Implement `PlaylistEntry` and `EnumeratePlaylist`**

In `internal/adapters/url/ytdlp/resolver.go`, add `"strconv"` to the import block (alongside the existing `bytes`, `context`, `encoding/json`, `fmt`, `os/exec`, `strings`, `time`).

Add the type immediately after the `Resolution` struct (after line 62):

```go
// PlaylistEntry is one entry from a flat-playlist enumeration: the item's
// stable ID and (where the extractor provides it) a page URL and title.
// Media is NOT resolved here — EnumeratePlaylist lists entries; Resolve
// dereferences each at play time.
type PlaylistEntry struct {
	ID    string // yt-dlp "id" (e.g. an 11-char YouTube video ID)
	URL   string // yt-dlp "url" (page URL or bare ID, extractor-dependent)
	Title string // yt-dlp "title"
}
```

Add the method after `Resolve` (after line 223, before `firstNonEmptyStr`):

```go
// EnumeratePlaylist lists a playlist's entries WITHOUT resolving media, using
// yt-dlp --flat-playlist. It is bounded by maxItems (--playlist-end). If ctx
// already has a deadline, that caller deadline is authoritative; otherwise the
// resolver's Timeout is used as a fallback.
//
// argv: --flat-playlist --dump-json --playlist-end <maxItems>
//
//	[--cookies <cookiesPath> if non-empty] <pageURL>
//
// Unlike Resolve, this MUST NOT pass --no-playlist (that would collapse the
// playlist to a single entry) and does not pass -f/--js-runtimes (flat
// enumeration runs no signature/format extraction). Output is yt-dlp's
// --dump-json stream: one JSON object per playlist entry, decoded in order.
func (r *Resolver) EnumeratePlaylist(ctx context.Context, pageURL, cookiesPath string, maxItems int) ([]PlaylistEntry, error) {
	binary := r.Binary
	if r.BinaryResolver != nil {
		resolved, err := r.BinaryResolver.Resolve()
		if err != nil {
			return nil, fmt.Errorf("ytdlp: resolve binary: %w", err)
		}
		binary = resolved
	}
	if binary == "" {
		return nil, fmt.Errorf("ytdlp: binary not configured")
	}
	if maxItems < 1 {
		maxItems = 1
	}

	args := []string{
		"--flat-playlist",
		"--dump-json",
		"--playlist-end", strconv.Itoa(maxItems),
	}
	if cookiesPath != "" {
		args = append(args, "--cookies", cookiesPath)
	}
	args = append(args, pageURL)

	runCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && r.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.Timeout)
	}
	defer cancel()

	stdout, stderr, err := r.Runner.Run(runCtx, binary, args...)
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			if _, inheritedDeadline := ctx.Deadline(); inheritedDeadline {
				return nil, fmt.Errorf("ytdlp: enumerate timed out")
			}
			return nil, fmt.Errorf("ytdlp: enumerate timed out after %s", r.Timeout)
		}
		// stderr may echo the input URL; callers redact before logging
		// (same contract as Resolve).
		return nil, fmt.Errorf("ytdlp: %s", summarizeStderr(stderr))
	}

	// --dump-json emits one JSON object per entry (NDJSON). json.Decoder
	// reads consecutive values across whitespace/newlines.
	entries := make([]PlaylistEntry, 0, maxItems)
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for dec.More() {
		var raw struct {
			ID    string `json:"id"`
			URL   string `json:"url"`
			Title string `json:"title"`
		}
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("ytdlp: unparseable flat-playlist json: %w", err)
		}
		if raw.ID == "" && raw.URL == "" {
			continue
		}
		entries = append(entries, PlaylistEntry{ID: raw.ID, URL: raw.URL, Title: raw.Title})
		if len(entries) >= maxItems {
			break
		}
	}
	return entries, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/url/ytdlp/ -run TestEnumeratePlaylist`
Expected: PASS (all five sub-tests).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/url/ytdlp/resolver.go internal/adapters/url/ytdlp/resolver_test.go
git commit -m "feat(ytdlp): EnumeratePlaylist via yt-dlp --flat-playlist"
```

---

## Task 2: Extend the `streamResolver` interface + `fakeResolver`

**Files:**
- Modify: `internal/adapters/streams/adapter.go` (interface ~line 38-40)
- Modify: `internal/adapters/streams/test_helpers_test.go` (`fakeResolver` ~line 272-296)
- Test: `internal/adapters/streams/test_helpers_test.go` (compile-time assertion)

- [ ] **Step 1: Write the compile-time assertion**

Append to `internal/adapters/streams/test_helpers_test.go`:

```go
// Compile-time proof the production resolver satisfies the streams interface
// after EnumeratePlaylist is added to both.
var _ streamResolver = (*ytdlp.Resolver)(nil)
```

(`test_helpers_test.go` already imports `.../internal/adapters/url/ytdlp`.)

- [ ] **Step 2: Run the intentional compile checkpoint**

The assertion alone still compiles before the interface is extended. The intentional failing checkpoint happens after Step 3a and before Step 3b:

Run: `go vet ./internal/adapters/streams/`
Expected after Step 3a only: FAIL — `*fakeResolver does not implement streamResolver (missing method EnumeratePlaylist)`.

(Order Step 3a then 3b; the package will not compile in between, which is the failing state.)

- [ ] **Step 3a: Extend the interface**

In `internal/adapters/streams/adapter.go`, replace the `streamResolver` interface (lines 38-40):

```go
type streamResolver interface {
	Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*ytdlp.Resolution, error)
	EnumeratePlaylist(ctx context.Context, pageURL, cookiesPath string, maxItems int) ([]ytdlp.PlaylistEntry, error)
}
```

- [ ] **Step 3b: Extend `fakeResolver`**

In `internal/adapters/streams/test_helpers_test.go`, replace the `fakeResolver` struct (lines 272-279) with:

```go
type fakeResolver struct {
	res       *ytdlp.Resolution
	err       error
	responses []fakeResolveResponse
	calls     int
	pageURLs  []string
	format    string

	// EnumeratePlaylist stubbing.
	enumEntries  map[string][]ytdlp.PlaylistEntry // keyed by pageURL
	enumErr      error
	enumCalls    int
	enumPageURLs []string
	enumMaxItems []int
}
```

Add the method immediately after the existing `Resolve` method (after line 296):

```go
func (f *fakeResolver) EnumeratePlaylist(_ context.Context, pageURL, _ string, maxItems int) ([]ytdlp.PlaylistEntry, error) {
	f.enumCalls++
	f.enumPageURLs = append(f.enumPageURLs, pageURL)
	f.enumMaxItems = append(f.enumMaxItems, maxItems)
	if f.enumErr != nil {
		return nil, f.enumErr
	}
	entries := f.enumEntries[pageURL]
	if maxItems > 0 && len(entries) > maxItems {
		entries = entries[:maxItems]
	}
	return entries, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go vet ./internal/adapters/streams/ && go test ./internal/adapters/streams/ -run TestNew_BuildsUserProviderStore`
Expected: PASS (package compiles; the `var _ streamResolver` assertion holds).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/adapter.go internal/adapters/streams/test_helpers_test.go
git commit -m "feat(streams): add EnumeratePlaylist to streamResolver + fakeResolver"
```

---

## Task 3: Playlist cache key + item encode/decode

**Files:**
- Create: `internal/adapters/streams/playlist_enum.go`
- Test: `internal/adapters/streams/playlist_enum_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/streams/playlist_enum_test.go`:

```go
package streams

import (
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
)

func TestPlaylistItems_RoundTrip(t *testing.T) {
	t.Parallel()
	in := []StreamItem{
		{ID: "dQw4w9WgXcQ", Title: "First", URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ", SourceID: "dQw4w9WgXcQ"},
		{ID: "abcdefghijk", Title: "Second", URL: "https://www.youtube.com/watch?v=abcdefghijk", SourceID: "abcdefghijk"},
	}
	raw, err := encodePlaylistItems(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := decodePlaylistItems(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 || out[0].ID != "dQw4w9WgXcQ" || out[1].URL != "https://www.youtube.com/watch?v=abcdefghijk" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	for _, it := range out {
		if it.Direct {
			t.Fatalf("decoded item Direct=true, want false (playlist items resolve via yt-dlp)")
		}
	}
}

func TestPlaylistEntriesToItems_YouTubeURLCapAndSafety(t *testing.T) {
	t.Parallel()
	entries := []ytdlp.PlaylistEntry{
		{ID: "dQw4w9WgXcQ", URL: "dQw4w9WgXcQ", Title: "Yt"},              // ID → canonical watch URL
		{ID: "", URL: "abcdefghijk", Title: "URL bare ID"},                // bare URL ID → canonical watch URL
		{ID: "", URL: "https://example.com/vid.mp4", Title: "Generic"},    // generic safe URL kept
		{ID: "", URL: "file:///etc/passwd", Title: "File"},                // dropped: scheme
		{ID: "", URL: "https://user:pass@example.com/s", Title: "Creds"},  // dropped: userinfo
		{ID: "", URL: "http://127.0.0.1:8080/meta", Title: "Loopback"},    // dropped: blocked IP
		{ID: "", URL: "http://169.254.169.254/latest", Title: "Metadata"}, // dropped: link-local IP
		{ID: "", URL: "", Title: "Empty"},                                 // dropped
	}
	items := playlistEntriesToItems(entries, 0)
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3 (unsafe/empty entries dropped): %+v", len(items), items)
	}
	if items[0].URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" || items[0].SourceID != "dQw4w9WgXcQ" || items[0].Direct {
		t.Fatalf("items[0] = %+v", items[0])
	}
	if items[1].URL != "https://www.youtube.com/watch?v=abcdefghijk" || items[1].ID != "abcdefghijk" {
		t.Fatalf("items[1] = %+v", items[1])
	}
	if items[2].URL != "https://example.com/vid.mp4" || items[2].ID != "https://example.com/vid.mp4" {
		t.Fatalf("items[2] = %+v", items[2])
	}
	for _, it := range items {
		if strings.Contains(it.URL, "127.0.0.1") || strings.Contains(it.URL, "169.254.169.254") || strings.HasPrefix(it.URL, "file:") {
			t.Fatalf("unsafe item leaked through: %+v", it)
		}
	}
	if capped := playlistEntriesToItems(entries, 1); len(capped) != 1 {
		t.Fatalf("capped = %d, want 1", len(capped))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run 'TestPlaylistItems_RoundTrip|TestPlaylistEntriesToItems'`
Expected: FAIL — `undefined: encodePlaylistItems` / `playlistEntriesToItems`.

- [ ] **Step 3: Implement the codec + entry mapping**

`userPlaylistCacheKey` already exists in `user_cache_key.go` (see Background) — do NOT define it here.

Create `internal/adapters/streams/playlist_enum.go`. Task 3 imports only what Task 3 uses; Task 4 adds `context`, `log/slog`, `net/url`, and `time` when it appends the enumerator and redaction helper:

```go
package streams

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
)

// cachedPlaylistItem is the on-disk shape of an enumerated playlist item.
// StreamItem has no JSON tags, so cache persistence uses this explicit struct
// (and stays stable if StreamItem gains in-memory-only fields later).
type cachedPlaylistItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	SourceID string `json:"source_id"`
}

func encodePlaylistItems(items []StreamItem) ([]byte, error) {
	out := make([]cachedPlaylistItem, 0, len(items))
	for _, it := range items {
		out = append(out, cachedPlaylistItem{ID: it.ID, Title: it.Title, URL: it.URL, SourceID: it.SourceID})
	}
	return json.Marshal(out)
}

func decodePlaylistItems(raw []byte) ([]StreamItem, error) {
	var in []cachedPlaylistItem
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("decode playlist items: %w", err)
	}
	out := make([]StreamItem, 0, len(in))
	for _, c := range in {
		out = append(out, StreamItem{ID: c.ID, Title: c.Title, URL: c.URL, SourceID: c.SourceID, Direct: false})
	}
	return out, nil
}

func youtubeWatchURL(id string) string {
	return "https://www.youtube.com/watch?v=" + id
}

// playlistEntriesToItems maps flat-playlist entries to non-direct StreamItems.
// A YouTube-ID entry (from either "id" or a bare "url") gets the canonical
// watch URL (matching the youtube-channel-json builder,
// provider_youtube_channel_json.go:78). Non-YouTube entries must pass the same
// syntactic user-provider URL gate used at authoring time before they can be
// cached or later dereferenced by yt-dlp; unsafe entries are dropped. Items are
// Direct:false → resolved by yt-dlp at play time, where the §7.2 user resolved
// media-URL recheck (playback.go) applies.
func playlistEntriesToItems(entries []ytdlp.PlaylistEntry, maxItems int) []StreamItem {
	items := make([]StreamItem, 0, len(entries))
	for _, e := range entries {
		if maxItems > 0 && len(items) >= maxItems {
			break
		}
		id, pageURL := strings.TrimSpace(e.ID), strings.TrimSpace(e.URL)
		switch {
		case youtubeIDRE.MatchString(id):
			pageURL = youtubeWatchURL(id)
		case youtubeIDRE.MatchString(pageURL):
			id = pageURL
			pageURL = youtubeWatchURL(id)
		case pageURL == "":
			continue
		default:
			if err := validateUserProviderHost(pageURL); err != nil {
				continue
			}
		}
		if id == "" {
			id = pageURL
		}
		items = append(items, StreamItem{ID: id, Title: e.Title, URL: pageURL, SourceID: id, Direct: false})
	}
	return items
}

// (userPlaylistEnumerator follows in Task 4.)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run 'TestPlaylistItems_RoundTrip|TestPlaylistEntriesToItems'`
Expected: PASS. Run `go test ./internal/adapters/streams/ -run TestUserCacheKeys` too — the pre-existing cache-key test must stay green (proves `userPlaylistCacheKey` was not disturbed).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/playlist_enum.go internal/adapters/streams/playlist_enum_test.go
git commit -m "feat(streams): user playlist item codec + entry mapping"
```

---

## Task 4: `userPlaylistEnumerator` (live-enumerate+cache / cache-only / serve-stale)

**Files:**
- Modify: `internal/adapters/streams/playlist_enum.go` (add the enumerator seam and redaction helper)
- Test: `internal/adapters/streams/playlist_enum_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/playlist_enum_test.go`:

```go
func ytEntries(ids ...string) []ytdlp.PlaylistEntry {
	out := make([]ytdlp.PlaylistEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, ytdlp.PlaylistEntry{ID: id, URL: id, Title: id})
	}
	return out
}

func TestEnumerator_LiveEnumeratesCachesAndIsServedFromCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pageURL := "https://youtube.com/playlist?list=PL1"
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		pageURL: ytEntries("dQw4w9WgXcQ", "abcdefghijk"),
	}}
	cfg := DefaultConfig()
	live := userPlaylistEnumerator{resolver: fr, cacheDir: dir, cfg: cfg}

	items, err := live.channelItems(context.Background(), "user:mix", "list", pageURL)
	if err != nil {
		t.Fatalf("live channelItems: %v", err)
	}
	if len(items) != 2 || items[0].URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("live items = %+v", items)
	}
	if fr.enumCalls != 1 {
		t.Fatalf("enumCalls = %d, want 1", fr.enumCalls)
	}

	// A cache-only enumerator (resolver nil) now serves the written cache.
	cacheOnly := userPlaylistEnumerator{cacheDir: dir, cfg: cfg}
	cached, err := cacheOnly.channelItems(context.Background(), "user:mix", "list", pageURL)
	if err != nil {
		t.Fatalf("cache-only channelItems: %v", err)
	}
	if len(cached) != 2 {
		t.Fatalf("cache-only items = %d, want 2 (served from cache)", len(cached))
	}
}

func TestEnumerator_CacheOnlyEmptyWhenUncached(t *testing.T) {
	t.Parallel()
	cacheOnly := userPlaylistEnumerator{cacheDir: t.TempDir(), cfg: DefaultConfig()}
	items, err := cacheOnly.channelItems(context.Background(), "user:mix", "list", "https://youtube.com/playlist?list=PL1")
	if err != nil {
		t.Fatalf("channelItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("uncached cache-only items = %d, want 0", len(items))
	}
}

func TestEnumerator_ServeStaleOnLiveFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pageURL := "https://youtube.com/playlist?list=PL1"

	// Seed the cache via a successful live run.
	ok := userPlaylistEnumerator{
		resolver: &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{pageURL: ytEntries("dQw4w9WgXcQ")}},
		cacheDir: dir, cfg: DefaultConfig(),
	}
	if _, err := ok.channelItems(context.Background(), "user:mix", "list", pageURL); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A subsequent live run that fails must serve the stale cache AND report
	// the error (for logging), not empty the channel.
	failing := userPlaylistEnumerator{
		resolver: &fakeResolver{enumErr: fmt.Errorf("yt-dlp: playlist temporarily unavailable")},
		cacheDir: dir, cfg: DefaultConfig(),
	}
	items, err := failing.channelItems(context.Background(), "user:mix", "list", pageURL)
	if err == nil {
		t.Fatal("err = nil, want the transient enumerate error surfaced for logging")
	}
	if len(items) != 1 {
		t.Fatalf("serve-stale items = %d, want 1 (prior cache retained)", len(items))
	}
}

func TestEnumerator_LiveFailureNoCacheReturnsError(t *testing.T) {
	t.Parallel()
	failing := userPlaylistEnumerator{
		resolver: &fakeResolver{enumErr: fmt.Errorf("yt-dlp: private playlist")},
		cacheDir: t.TempDir(), cfg: DefaultConfig(),
	}
	items, err := failing.channelItems(context.Background(), "user:mix", "list", "https://youtube.com/playlist?list=PL1")
	if err == nil {
		t.Fatal("err = nil, want error when enumeration fails with no cache")
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0", len(items))
	}
}

func TestPlaylistErrorForLog_RedactsPageURL(t *testing.T) {
	t.Parallel()
	pageURL := "https://example.com/playlist?token=secret"
	got := playlistErrorForLog(fmt.Errorf("yt-dlp failed for %s", pageURL), pageURL)
	if strings.Contains(got, "token=secret") || strings.Contains(got, pageURL) {
		t.Fatalf("playlist error log leaked page URL/query: %q", got)
	}
	if !strings.Contains(got, "https://example.com/playlist") {
		t.Fatalf("playlist error log lost useful URL context: %q", got)
	}
}
```

These tests need `"context"`, `"fmt"`, and `"strings"` in the test file's import block — add them.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run 'TestEnumerator|TestPlaylistErrorForLog'`
Expected: FAIL — `undefined: userPlaylistEnumerator` / `undefined: playlistErrorForLog`.

- [ ] **Step 3: Implement the enumerator**

In `internal/adapters/streams/playlist_enum.go`, add `"context"`, `"log/slog"`, `"net/url"`, and `"time"` to the import block (joining the `encoding/json`, `fmt`, `"strings"`, and `ytdlp` imports from Task 3), then append:

```go
// userPlaylistEnumerator resolves a user playlist channel's items. It is the
// single seam through which buildUserCatalog gets playlist items, so the catalog
// builder stays free of network/cache concerns.
//
//   - resolver == nil (startup, "cache-only"): returns cached items if present,
//     else nil. Startup never blocks on yt-dlp; the background refresh fills
//     uncached playlist channels (see Task 6 + the AllowRemoteManifest residual).
//   - resolver != nil (refresh, "live"): enumerates via yt-dlp, caches on
//     success, and SERVES STALE (returns the prior cache) on failure so a
//     transient yt-dlp error never empties a working channel. The returned
//     error is advisory (for logging); callers keep the provider usable.
type userPlaylistEnumerator struct {
	resolver    streamResolver // nil → cache-only
	cookiesPath string
	cacheDir    string
	cfg         Config
}

func (e userPlaylistEnumerator) cached(providerID, channelID, pageURL string) ([]StreamItem, bool) {
	raw, _, ok := readConditionalCache(e.cacheDir, userPlaylistCacheKey(providerID, channelID), pageURL)
	if !ok {
		return nil, false
	}
	items, err := decodePlaylistItems(raw)
	if err != nil {
		return nil, false
	}
	return items, true
}

func (e userPlaylistEnumerator) channelItems(ctx context.Context, providerID, channelID, pageURL string) ([]StreamItem, error) {
	cachedItems, cachedOK := e.cached(providerID, channelID, pageURL)

	if e.resolver == nil {
		if cachedOK {
			return cachedItems, nil
		}
		return nil, nil
	}

	maxItems := e.cfg.MaxItemsPerChannel
	timeout := time.Duration(e.cfg.CatalogRequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	enumCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	entries, err := e.resolver.EnumeratePlaylist(enumCtx, pageURL, e.cookiesPath, maxItems)
	if err != nil {
		if cachedOK {
			return cachedItems, fmt.Errorf("enumerate playlist %q/%q (serving %d cached): %w",
				providerID, channelID, len(cachedItems), err)
		}
		return nil, fmt.Errorf("enumerate playlist %q/%q: %w", providerID, channelID, err)
	}

	items := playlistEntriesToItems(entries, maxItems)
	// Cache write failure is non-fatal: return the freshly enumerated items now
	// and let the next refresh cycle re-attempt the write.
	if raw, encErr := encodePlaylistItems(items); encErr == nil {
		meta := CacheMetadata{SourceURL: pageURL, FetchedAt: time.Now().UTC()}
		if wErr := writeCacheFile(e.cacheDir, userPlaylistCacheKey(providerID, channelID), raw, meta); wErr != nil {
			slog.Warn("user_providers: playlist cache write failed",
				"provider", providerID, "channel", channelID, "err", wErr)
		}
	}
	return items, nil
}

func playlistErrorForLog(err error, pageURL string) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(err.Error(), pageURL, redactPlaylistPageURL(pageURL))
}

func redactPlaylistPageURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<redacted-url>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run 'TestEnumerator|TestPlaylistErrorForLog'`
Expected: PASS (all enumerator and redaction tests).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/playlist_enum.go internal/adapters/streams/playlist_enum_test.go
git commit -m "feat(streams): userPlaylistEnumerator (live+cache, cache-only, serve-stale)"
```

---

## Task 5: Enumeration-aware `buildUserCatalog`

Replace the playlist-skip with enumeration. The builder consumes a `userPlaylistEnumerator` for playlist channels; `direct`/`single` are unchanged. The pure dispatcher `buildProviderCatalog` keeps a cache-only path so existing callers and the Phase 3 dispatch test still compile (live wiring is Task 6).

**Files:**
- Modify: `internal/adapters/streams/provider_user.go` (`buildUserCatalog` ~line 306-352; imports)
- Modify: `internal/adapters/streams/refresh.go` (`buildProviderCatalog` user case ~line 628)
- Test: `internal/adapters/streams/provider_user_test.go` (rewrite `TestBuildUserCatalog_KindsProduceCorrectItems`)

- [ ] **Step 1: Write the failing test**

In `internal/adapters/streams/provider_user_test.go`, first **DELETE the entire Phase 3 `TestBuildUserCatalog_KindsProduceCorrectItems` function** (the one whose body calls `buildUserCatalog(userCatalogTestDef())` and asserts the playlist channel is skipped — around line 240). Then add the two tests below (they call the new 3-arg `buildUserCatalog`):

```go
func TestBuildUserCatalog_PlaylistEnumeratesDirectAndSingleUnchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	playlistURL := "https://www.youtube.com/playlist?list=PL123"
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		playlistURL: ytEntries("dQw4w9WgXcQ", "abcdefghijk"),
	}}
	enum := userPlaylistEnumerator{resolver: fr, cacheDir: dir, cfg: DefaultConfig()}

	cat, err := buildUserCatalog(context.Background(), userCatalogTestDef(), enum)
	if err != nil {
		t.Fatalf("buildUserCatalog: %v", err)
	}
	if cat.ProviderID != "user:mix" || cat.Name != "Mix" {
		t.Fatalf("identity = (%q,%q)", cat.ProviderID, cat.Name)
	}
	// All three channels now appear (playlist no longer skipped).
	if len(cat.Channels) != 3 {
		t.Fatalf("len(Channels) = %d, want 3", len(cat.Channels))
	}
	byID := map[string]Channel{}
	for _, ch := range cat.Channels {
		byID[ch.ID] = ch
	}
	if live := byID["live"]; len(live.Items) != 1 || !live.Items[0].Direct {
		t.Fatalf("direct channel = %+v", live.Items)
	}
	if vid := byID["vid"]; len(vid.Items) != 1 || vid.Items[0].Direct {
		t.Fatalf("single channel = %+v", vid.Items)
	}
	list, ok := byID["list"]
	if !ok {
		t.Fatal("playlist channel 'list' missing — it must be enumerated, not skipped")
	}
	if len(list.Items) != 2 || list.Items[0].URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" || list.Items[0].Direct {
		t.Fatalf("playlist items = %+v, want 2 non-direct enumerated items", list.Items)
	}
	if fr.enumCalls != 1 {
		t.Fatalf("enumCalls = %d, want 1 (only the playlist channel enumerates)", fr.enumCalls)
	}
}

func TestBuildUserCatalog_PlaylistEnumerationFailureKeepsProvider(t *testing.T) {
	t.Parallel()
	enum := userPlaylistEnumerator{
		resolver: &fakeResolver{enumErr: fmt.Errorf("private playlist")},
		cacheDir: t.TempDir(), cfg: DefaultConfig(),
	}
	cat, err := buildUserCatalog(context.Background(), userCatalogTestDef(), enum)
	if err != nil {
		t.Fatalf("buildUserCatalog must not fail on a single playlist error: %v", err)
	}
	if len(cat.Channels) != 3 {
		t.Fatalf("len(Channels) = %d, want 3 (provider stays usable)", len(cat.Channels))
	}
	for _, ch := range cat.Channels {
		if ch.ID == "list" && len(ch.Items) != 0 {
			t.Fatalf("failed playlist should have 0 items, got %d", len(ch.Items))
		}
	}
}
```

`provider_user_test.go` needs `"context"`, `"fmt"`, and the `ytdlp` import — add any missing. (`ytEntries` is defined in `playlist_enum_test.go`, same package.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestBuildUserCatalog`
Expected: FAIL — compile error `too many arguments in call to buildUserCatalog` (still the Phase 3 1-arg signature).

- [ ] **Step 3: Rewrite `buildUserCatalog` and the dispatcher**

In `internal/adapters/streams/provider_user.go`, add `"context"` to the import block, then REPLACE `buildUserCatalog` (lines 306-352) with:

```go
// buildUserCatalog turns a normalized user ProviderDefinition into a
// ProviderCatalog, branching per ChannelDefinition.Kind:
//   - direct → one StreamItem{URL, Direct:true} (straight to FFmpeg + the user
//     direct policy at play time).
//   - single → one StreamItem{URL, Direct:false} (resolved by yt-dlp at play time).
//   - playlist → enum.channelItems enumerates the playlist (live or cached). A
//     per-channel enumeration error is logged with the page URL redacted, and
//     the channel renders with 0 items or stale items; the rest of the provider
//     stays usable (spec §6).
//
// Network work (if any) is confined to enum.channelItems; the builder itself is
// otherwise pure and must run BEFORE a.mu is taken (no lock across I/O).
func buildUserCatalog(ctx context.Context, def ProviderDefinition, enum userPlaylistEnumerator) (ProviderCatalog, error) {
	if def.Type != userProviderType {
		return ProviderCatalog{}, fmt.Errorf("provider %q type %q is unsupported", def.ID, def.Type)
	}
	groupByID := make(map[string]ChannelGroup, len(def.Groups))
	groups := make([]ChannelGroup, 0, len(def.Groups))
	for _, group := range def.Groups {
		g := ChannelGroup{ID: group.ID, Name: group.Name, Order: group.Order}
		groupByID[group.ID] = g
		groups = append(groups, g)
	}
	channels := make([]Channel, 0, len(def.Channels))
	for _, chDef := range def.Channels {
		ch := channelFromDefinition(chDef.ID, chDef, true, def)
		switch chDef.Kind {
		case kindDirect:
			ch.Items = []StreamItem{{ID: ch.ID, Title: ch.Name, URL: chDef.URL, SourceID: ch.ID, Direct: true}}
		case kindSingle:
			ch.Items = []StreamItem{{ID: ch.ID, Title: ch.Name, URL: chDef.URL, SourceID: ch.ID, Direct: false}}
		case kindPlaylist:
			items, err := enum.channelItems(ctx, def.ID, ch.ID, chDef.URL)
			if err != nil {
				slog.Warn("streams user provider: playlist enumeration error (channel kept, may be empty or stale)",
					"provider", def.ID, "channel", ch.ID, "err", playlistErrorForLog(err, chDef.URL))
			}
			ch.Items = items
		default:
			return ProviderCatalog{}, fmt.Errorf("provider %q channel %q: invalid kind %q", def.ID, ch.ID, chDef.Kind)
		}
		if ch.GroupID != "" {
			if _, ok := groupByID[ch.GroupID]; !ok {
				return ProviderCatalog{}, fmt.Errorf("provider %q channel %q references unknown group %q", def.ID, ch.ID, ch.GroupID)
			}
		}
		channels = append(channels, ch)
	}
	sortChannelGroups(groups)
	sortChannels(channels)
	return ProviderCatalog{
		ProviderID: def.ID,
		Name:       def.DisplayName,
		Groups:     groups,
		Channels:   channels,
		UpdatedAt:  time.Now(),
	}, nil
}
```

(The `"time"` import in `provider_user.go` is still used; `"log/slog"` likewise. Keep both.)

In `internal/adapters/streams/refresh.go`, update the `buildProviderCatalog` user case (line 628) to the cache-only path (no cacheDir → playlist channels empty; live wiring is Task 6). `refresh.go` already imports `"context"`, so no import change is needed there:

```go
	case userProviderType:
		return buildUserCatalog(context.Background(), def, userPlaylistEnumerator{cfg: cfg})
```

The 1→3-arg signature change also breaks two existing Phase 3 callers in `internal/adapters/streams/playback_test.go` — the helpers `installUserDirectAdapter` (~line 1430) and `installUserSingleAdapter` (~line 1491), which each call `buildUserCatalog(def)`. Update BOTH (their channels are `direct`/`single`, so the enumerator is never invoked — a zero-value cache-only enumerator is correct):

```go
	cat, err := buildUserCatalog(context.Background(), def, userPlaylistEnumerator{})
```

(`playback_test.go` already imports `context`; no import change needed.) The `provider_user_test.go` call inside the deleted `TestBuildUserCatalog_KindsProduceCorrectItems` is removed in Step 1, so no other live callers remain on the old signature.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters/streams/ -run 'TestBuildUserCatalog|TestBuildProviderCatalog_DispatchesUserType'`
Expected: PASS. (`TestBuildProviderCatalog_DispatchesUserType` still passes: its def's playlist channel now renders with 0 items via the cache-only path, and the test only asserts `ProviderID`.)

- [ ] **Step 5: Run the full package to catch the dispatcher's other callers**

Run: `go test ./internal/adapters/streams/`
Expected: PASS. The three build-path callers still route user providers through `buildProviderCatalog(def, nil, cfg)` → cache-only, so the package compiles and prior catalog tests are unaffected (no user playlist fixtures rely on live items yet).

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/streams/provider_user.go internal/adapters/streams/refresh.go internal/adapters/streams/provider_user_test.go internal/adapters/streams/playback_test.go
git commit -m "feat(streams): enumerate user playlist channels in buildUserCatalog"
```

---

## Task 6: Thread the live enumerator through the build & refresh paths

Give the real call sites a properly-configured enumerator: cache-only (non-blocking) at startup, live (resolver + cookies + cacheDir) on remote/catalog refresh. This is the change that makes playlists populate from yt-dlp.

**Files:**
- Modify: `internal/adapters/streams/refresh.go` (`buildStartupSnapshot` ~491, `buildCachedOrSeedSnapshot` ~499, `buildRemoteSnapshot` ~567, `refreshCatalogsDefault` ~345)
- Modify: `internal/adapters/streams/refresh_test.go` (existing `buildRemoteSnapshot` call ~line passing `nil`)
- Test: `internal/adapters/streams/refresh_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/refresh_test.go`:

```go
func userPlaylistDef() ProviderDefinition {
	return ProviderDefinition{
		ID: "user:mix", Type: userProviderType, DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
		Channels: []ChannelDefinition{
			{ID: "list", Name: "List", Kind: kindPlaylist, URL: "https://www.youtube.com/playlist?list=PL123"},
		},
	}
}

func userPlaylistSnapshotTestConfig() Config {
	cfg := DefaultConfig()
	// buildRemoteSnapshot merges bundled providers first. Disable the bundled
	// fetch-backed providers so these unit tests stay offline/no-network while
	// still exercising the user playlist path. Toonami is direct/inline.
	cfg.Providers["mtv-rewind"] = ProviderConfig{Disabled: true}
	cfg.Providers["cartoon-rewind"] = ProviderConfig{Disabled: true}
	return cfg
}

func playlistChannelItems(t *testing.T, cats []ProviderCatalog, providerID, channelID string) []StreamItem {
	t.Helper()
	for _, c := range cats {
		if c.ProviderID != providerID {
			continue
		}
		if ch := c.Channel(channelID); ch != nil {
			return ch.Items
		}
	}
	t.Fatalf("channel %q/%q not found in catalogs", providerID, channelID)
	return nil
}

func TestBuildRemoteSnapshot_EnumeratesUserPlaylistLive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fr := &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		"https://www.youtube.com/playlist?list=PL123": ytEntries("dQw4w9WgXcQ", "abcdefghijk"),
	}}
	cfg := userPlaylistSnapshotTestConfig()
	enum := userPlaylistEnumerator{resolver: fr, cacheDir: dir, cfg: cfg}
	snap, err := buildRemoteSnapshot(t.Context(), cfg, Manifest{Version: 1}, dir, []ProviderDefinition{userPlaylistDef()}, enum)
	if err != nil {
		t.Fatalf("buildRemoteSnapshot: %v", err)
	}
	items := playlistChannelItems(t, snap.Catalogs, "user:mix", "list")
	if len(items) != 2 {
		t.Fatalf("live remote snapshot playlist items = %d, want 2", len(items))
	}
	if fr.enumCalls != 1 {
		t.Fatalf("enumCalls = %d, want 1", fr.enumCalls)
	}
}

func TestBuildStartupSnapshot_PlaylistUsesCacheOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pageURL := "https://www.youtube.com/playlist?list=PL123"

	// No cache yet → cache-only startup yields an empty (but present) channel.
	_, cats, err := buildStartupSnapshot(t.Context(), DefaultConfig(), dir, []ProviderDefinition{userPlaylistDef()})
	if err != nil {
		t.Fatalf("buildStartupSnapshot: %v", err)
	}
	if items := playlistChannelItems(t, cats, "user:mix", "list"); len(items) != 0 {
		t.Fatalf("uncached startup items = %d, want 0", len(items))
	}

	// Seed the cache (as a live refresh would), then startup serves it.
	seed := userPlaylistEnumerator{
		resolver: &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{pageURL: ytEntries("dQw4w9WgXcQ")}},
		cacheDir: dir, cfg: DefaultConfig(),
	}
	if _, err := seed.channelItems(t.Context(), "user:mix", "list", pageURL); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, cats2, err := buildStartupSnapshot(t.Context(), DefaultConfig(), dir, []ProviderDefinition{userPlaylistDef()})
	if err != nil {
		t.Fatalf("buildStartupSnapshot (cached): %v", err)
	}
	if items := playlistChannelItems(t, cats2, "user:mix", "list"); len(items) != 1 {
		t.Fatalf("cached startup items = %d, want 1", len(items))
	}
}

func TestRefreshCatalogsDefault_EnumeratesUserPlaylist(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	a.resolver = &fakeResolver{enumEntries: map[string][]ytdlp.PlaylistEntry{
		"https://www.youtube.com/playlist?list=PL123": ytEntries("dQw4w9WgXcQ", "abcdefghijk"),
	}}
	a.replaceDefinitionsForTest([]ProviderDefinition{userPlaylistDef()})

	status := a.refreshCatalogsDefault(t.Context(), []string{"user:mix"}, "manual")
	if status.Err != nil {
		t.Fatalf("refreshCatalogsDefault: %v", status.Err)
	}
	cat := a.Catalog()
	var found bool
	for _, p := range cat {
		if p.ID != "user:mix" {
			continue
		}
		for _, g := range p.Groups {
			for _, ch := range g.Channels {
				if ch.ID == "list" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("playlist channel 'list' not exposed after refresh")
	}
	// Items live on the internal catalog (Catalog() is the chassis view; assert
	// items via the stored ProviderCatalog).
	a.mu.Lock()
	items := a.catalogs["user:mix"].Channel("list").Items
	a.mu.Unlock()
	if len(items) != 2 {
		t.Fatalf("refreshed playlist items = %d, want 2", len(items))
	}
}
```

`refresh_test.go` needs the `ytdlp` import — add it if missing. (`a.Catalog()`/`newTestAdapterWithCatalog`/`replaceDefinitionsForTest` exist from Phase 3.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run 'TestBuildRemoteSnapshot_EnumeratesUserPlaylistLive|TestBuildStartupSnapshot_PlaylistUsesCacheOnly|TestRefreshCatalogsDefault_EnumeratesUserPlaylist'`
Expected: FAIL — `too many arguments in call to buildRemoteSnapshot` (the test passes the new `enum` arg) and the refresh/startup paths still route user providers through the cache-only `buildProviderCatalog` (0 items, no enumeration).

- [ ] **Step 3: Thread the enumerator through all three paths**

In `internal/adapters/streams/refresh.go`:

(a) `buildStartupSnapshot` (line 491) — build a cache-only enumerator and pass `ctx` down:

```go
func buildStartupSnapshot(ctx context.Context, cfg Config, cacheDir string, userProviders []ProviderDefinition) ([]ProviderDefinition, []ProviderCatalog, error) {
	cached := loadCachedManifest(ctx, cfg, cacheDir)
	bundled := sanitizeManifestArtwork(ctx, bundledManifest(), cfg, validateProviderArtworkURLSyntax)
	manifest := mergeManifests(cfg, bundled, cached, nil, remoteProviderFactories())
	manifest.Providers = appendUserProviders(manifest.Providers, cfg, userProviders)
	enum := userPlaylistEnumerator{cacheDir: cacheDir, cfg: cfg} // resolver nil → cache-only, non-blocking
	return buildCachedOrSeedSnapshot(ctx, manifest.Providers, cfg, cacheDir, enum)
}
```

> **Why cache-only at startup is correct (not accidental):** `Start()` calls `buildStartupSnapshot` at `adapter.go:266`, *before* assigning `a.resolver` at line 272 — and we never want `Start()` to block on a yt-dlp subprocess. The startup enumerator therefore leaves `resolver` nil, which `channelItems` treats as "serve cache, else empty." Live enumeration is the refresh loop's responsibility (paths (c)/(d)/(e)); on a normal first run with `AllowRemoteManifest` on, the loop's first job runs immediately (`lastManifest` is zero) and fills the playlist channels.

(b) `buildCachedOrSeedSnapshot` (line 499) — take `ctx` + `enum`; route user providers through `buildUserCatalog`:

```go
func buildCachedOrSeedSnapshot(ctx context.Context, defs []ProviderDefinition, cfg Config, cacheDir string, enum userPlaylistEnumerator) ([]ProviderDefinition, []ProviderCatalog, error) {
	catalogs := make([]ProviderCatalog, 0, len(defs))
	for _, def := range defs {
		if def.Type == directStreamsProviderType || def.Type == userProviderType {
			cat, err := buildInlineCatalog(ctx, def, cfg, enum)
			if err != nil {
				return nil, nil, err
			}
			catalogs = append(catalogs, cat)
			continue
		}
		if raw, _, ok := readConditionalCache(cacheDir, catalogCacheKey(def.ID), def.PlaylistURL); ok {
			cat, err := buildProviderCatalog(def, raw, cfg)
			if err == nil {
				catalogs = append(catalogs, cat)
				continue
			}
		}
		if path, ok := bundledSeedPath(def.ID); ok {
			raw, err := seedFS.ReadFile(path)
			if err != nil {
				return nil, nil, fmt.Errorf("read bundled seed %q: %w", def.ID, err)
			}
			cat, err := buildProviderCatalog(def, raw, cfg)
			if err != nil {
				return nil, nil, err
			}
			catalogs = append(catalogs, cat)
		}
	}
	return defs, catalogs, nil
}

// buildInlineCatalog builds the locally-derived (no remote fetch) catalogs:
// direct-streams (pure) and user providers (playlist channels use enum). It
// centralizes the direct-vs-user branch shared by the startup, remote, and
// catalog-refresh paths.
func buildInlineCatalog(ctx context.Context, def ProviderDefinition, cfg Config, enum userPlaylistEnumerator) (ProviderCatalog, error) {
	if def.Type == userProviderType {
		return buildUserCatalog(ctx, def, enum)
	}
	return buildDirectStreamsCatalog(def)
}
```

(c) `buildRemoteSnapshot` (line 567) — add the `enum` param; route user providers through `buildInlineCatalog`:

```go
func buildRemoteSnapshot(ctx context.Context, cfg Config, remote Manifest, cacheDir string, userProviders []ProviderDefinition, enum userPlaylistEnumerator) (remoteSnapshot, error) {
	bundled := sanitizeManifestArtwork(ctx, bundledManifest(), cfg, validateProviderArtworkURLSyntax)
	remote = sanitizeManifestArtwork(ctx, remote, cfg, validateProviderArtworkURL)
	manifest := mergeManifests(cfg, bundled, nil, &remote, remoteProviderFactories())
	manifest.Providers = appendUserProviders(manifest.Providers, cfg, userProviders)
	out := remoteSnapshot{
		Definitions:   manifest.Providers,
		Catalogs:      make([]ProviderCatalog, 0, len(manifest.Providers)),
		CatalogBodies: map[string][]byte{},
		CatalogMetas:  map[string]CacheMetadata{},
	}
	for _, def := range manifest.Providers {
		if def.Type == directStreamsProviderType || def.Type == userProviderType {
			cat, err := buildInlineCatalog(ctx, def, cfg, enum)
			if err != nil {
				return remoteSnapshot{}, fmt.Errorf("provider %q build catalog: %w", def.ID, err)
			}
			out.Catalogs = append(out.Catalogs, cat)
			continue
		}
		raw, meta, err := fetchProviderPlaylist(ctx, def, cfg, cacheDir)
		if err != nil {
			return remoteSnapshot{}, err
		}
		cat, err := buildProviderCatalog(def, raw, cfg)
		if err != nil {
			return remoteSnapshot{}, err
		}
		out.Catalogs = append(out.Catalogs, cat)
		out.CatalogBodies[def.ID] = raw
		out.CatalogMetas[def.ID] = meta
	}
	return out, nil
}
```

(d) `refreshOnceDefault` (line 296) — build a live enumerator and pass it:

```go
	enum := userPlaylistEnumerator{resolver: a.resolver, cookiesPath: a.cookiesPath, cacheDir: a.cacheDir, cfg: cfg}
	snapshot, err := buildRemoteSnapshot(ctx, cfg, manifest, a.cacheDir, a.userStore.Snapshot(), enum)
```

(e) `refreshCatalogsDefault` (lines 345-361) — build ONE live enumerator before the loop (its fields are identical for every provider) and replace the user/direct branch with `buildInlineCatalog`:

```go
	enum := userPlaylistEnumerator{resolver: a.resolver, cookiesPath: a.cookiesPath, cacheDir: a.cacheDir, cfg: cfg}
	for _, def := range defs {
		if def.Type == directStreamsProviderType || def.Type == userProviderType {
			cat, err := buildInlineCatalog(ctx, def, cfg, enum)
			if err != nil {
				errs = append(errs, fmt.Errorf("provider %q build catalog: %w", def.ID, err))
				continue
			}
			a.mu.Lock()
			a.catalogs[cat.ProviderID] = cat
			if a.state != adapters.StateStopped {
				a.state = adapters.StateRunning
			}
			a.stateSince = time.Now()
			a.mu.Unlock()
			status.refreshedProviderIDs = append(status.refreshedProviderIDs, def.ID)
			continue
		}
		if !remoteAllowed {
			continue
		}
		// ... (unchanged remote-fetch path below)
```

> `a.resolver` and `a.cookiesPath` are set once (in `Start`/`New`) and never mutated, so reading them outside `a.mu` here matches the existing off-lock read in `playback.go:293`. `buildInlineCatalog` runs the enumeration BEFORE `a.mu.Lock()`, preserving "no lock across network I/O."

Finally, in `internal/adapters/streams/refresh_test.go`, update **BOTH** existing Phase 3 `buildRemoteSnapshot` callers to append a zero (cache-only) enumerator — there are two. Keep the direct-streams test's existing bundled-provider disables, and update the user-provider test to use `userPlaylistSnapshotTestConfig()` so it also cannot fetch bundled catalogs:
- ~line 174 (the call passing `nil` for userProviders): `buildRemoteSnapshot(t.Context(), cfg, remote, t.TempDir(), nil, userPlaylistEnumerator{})`
- ~line 1027, inside `TestBuildRemoteSnapshot_KeepsUserProviders` (passing `[]ProviderDefinition{user}`): `buildRemoteSnapshot(t.Context(), userPlaylistSnapshotTestConfig(), Manifest{Version: 1}, t.TempDir(), []ProviderDefinition{user}, userPlaylistEnumerator{})`

Confirm with `rg -n 'buildRemoteSnapshot\(' internal/adapters/streams` that no other caller remains on the 5-arg form (production caller `refreshOnceDefault` is updated in (d) above; the new Task 6 test passes 6 args already).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters/streams/`
Expected: PASS (full package — confirms the `buildRemoteSnapshot`/`buildCachedOrSeedSnapshot` signature changes compile across all call sites and the new enumeration tests pass).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/refresh.go internal/adapters/streams/refresh_test.go
git commit -m "feat(streams): wire live playlist enumeration into startup and refresh paths"
```

---

## Task 7: Catalog exposure + castability proof + full verification

A small end-to-end check that an enumerated playlist channel is exposed through `Catalog()` (the chassis view) AND is actually castable through the stored `ProviderCatalog`, plus the gating verification for the whole phase.

**Files:**
- Test: `internal/adapters/streams/catalog_test.go`

- [ ] **Step 1: Write the test**

Append to `internal/adapters/streams/catalog_test.go`:

```go
func TestCatalog_ExposesAndCastsEnumeratedPlaylistChannel(t *testing.T) {
	t.Parallel()
	a, core := newTestAdapterWithFakeCore(t)
	playlistURL := "https://www.youtube.com/playlist?list=PL123"
	resolver := &fakeResolver{
		res: &ytdlp.Resolution{URL: "https://media.example/video.mp4", Title: "First"},
		enumEntries: map[string][]ytdlp.PlaylistEntry{
			playlistURL: ytEntries("dQw4w9WgXcQ", "abcdefghijk"),
		},
	}
	a.resolver = resolver
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{"media.example": {"93.184.216.34"}}}
	a.replaceDefinitionsForTest([]ProviderDefinition{
		{
			ID: "user:mix", Type: userProviderType, DisplayName: "Mix", BadgeLabel: "MX", BadgeColor: "teal",
			Channels: []ChannelDefinition{
				{ID: "list", Name: "List", Kind: kindPlaylist, URL: playlistURL},
			},
		},
	})
	if status := a.refreshCatalogsDefault(t.Context(), []string{"user:mix"}, "manual"); status.Err != nil {
		t.Fatalf("refresh: %v", status.Err)
	}

	cat := a.Catalog()
	if len(cat) != 1 || cat[0].ID != "user:mix" {
		t.Fatalf("Catalog = %+v, want one user:mix provider", cat)
	}
	var listCh *adapters.CatalogChannel
	for gi := range cat[0].Groups {
		for ci := range cat[0].Groups[gi].Channels {
			if cat[0].Groups[gi].Channels[ci].ID == "list" {
				listCh = &cat[0].Groups[gi].Channels[ci]
			}
		}
	}
	if listCh == nil {
		t.Fatal("playlist channel 'list' not exposed via Catalog()")
	}
	// Playlist channels are mixed VOD, not provider-level live → Live=false.
	if listCh.Live {
		t.Errorf("playlist channel Live = true, want false")
	}

	if err := a.CastChannel(t.Context(), "user:mix", "list"); err != nil {
		t.Fatalf("CastChannel: %v", err)
	}
	if resolver.enumCalls != 1 {
		t.Fatalf("enumCalls = %d, want 1", resolver.enumCalls)
	}
	if resolver.calls != 1 || resolver.pageURLs[0] != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("resolver Resolve calls=%d urls=%v, want first enumerated watch URL", resolver.calls, resolver.pageURLs)
	}
	if core.lastReq.StreamURL != "https://media.example/video.mp4" {
		t.Fatalf("StreamURL = %q, want resolved media URL", core.lastReq.StreamURL)
	}
}
```

(`catalog_test.go` already imports `adapters` and is in the `streams` package; add `ytdlp` if missing.)

- [ ] **Step 2: Run the test**

Run: `go test ./internal/adapters/streams/ -run TestCatalog_ExposesAndCastsEnumeratedPlaylistChannel`
Expected: PASS.

- [ ] **Step 3: Full verification (all four CI gates' local equivalents)**

Run:
```bash
go vet ./...
go test ./internal/adapters/streams/... ./internal/adapters/url/ytdlp/...
go test ./...
```
Expected: vet clean; all PASS. (`go test -race` is the CI-only gate — not runnable locally without cgo. No integration-tagged test is added: enumeration is covered by the `fakeResolver` harness per spec §12, avoiding a network/yt-dlp dependency in `tests/integration`.)

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/streams/catalog_test.go
git commit -m "test(streams): prove enumerated user playlist castability"
```

---

## Out of scope for Phase 4 (later phases)

- **Surfacing pending/error states to the operator** — the `catalog`/`providerStatus` SSE envelope and the "Enumerating…"/"✗ private" chips on an open drawer (spec §6, §8) — **Phase 6** (no UI exists yet; Phase 4 logs the error and serves stale/empty).
- **Verify dry-run + `is_live` chips** (spec §9 items 4-5) — **Phase 5**.
- **Authoring routes / form / auto-enable hot-start** — **Phase 5/6**.
- **Loop-start gate for `AllowRemoteManifest=false` local-only setups** — folded into Phase 5 lifecycle work (see "Known residual" above).

## Self-Review

**Spec coverage (§4.4 / §4.6 / §6 / §12):**
- §6 "EnumeratePlaylist via yt-dlp --flat-playlist, bounded by maxItems under CatalogRequestTimeoutSeconds" → Task 1 (resolver) + Task 4 (`channelItems` applies `--playlist-end MaxItemsPerChannel` and a `CatalogRequestTimeoutSeconds` context).
- §6 "cached exactly like other provider catalogs, refreshed on the catalog_refresh_hours cycle" → Task 3/4 (cache via `writeCacheFile`/`readConditionalCache`) + Task 6 (refresh paths re-enumerate; the existing `providerCatalogRefreshDuration` schedule drives freshness — no separate TTL logic needed).
- §6 "enumerations run sequentially within the existing refresh loop (no new parallelism)" → Task 5 enumerates channels in a sequential loop; Task 6 processes providers sequentially in the existing refresh loop; no goroutines added.
- §6 "failure → provider stays usable; previously-cached list retained on transient failure (serve-stale)" → Task 4 (`channelItems` serve-stale) + Task 5 (`buildUserCatalog` logs and keeps the channel).
- §4.6 "user-playlist-<sha256(providerID + \x00 + channelID)>, compatible with the cache-key validator, stable across renames" → `userPlaylistCacheKey` already merged in `user_cache_key.go:13` (covered by `user_cache_key_test.go`); Phase 4 consumes it in Task 4 (`userPlaylistEnumerator`) — it is NOT re-created.
- §4.4 "playlist → N enumerated items via yt-dlp resolve per item (existing)" → Task 3 (`playlistEntriesToItems`, Direct:false, canonicalized/sanitized page URLs) + the already-merged play-time path; §4.4 direct/single rows unchanged (Task 5 preserves them).
- §12 "Playlist enumeration: drive with the existing fake-resolver harness — enumerate, cache, TTL, private/region-locked error path" → Task 4 tests (live/cache-only/serve-stale/no-cache-error/redacted log error) + Task 6 (startup vs refresh).
- §7 security: no new FFmpeg URL path; enumerated item page URLs pass the user-provider URL gate before cache/use, then resolved media URLs reuse the Phase 3 play-time `revalidateResolvedUserURLs` (documented in Background; no `playback.go` change).

**Placeholder scan:** No placeholder imports, `TBD`/"handle edge cases"/"similar to Task N" instructions remain; every code step shows complete code and an exact `go test -run` invocation with its expected failure or pass.

**Type consistency:** `EnumeratePlaylist(ctx, pageURL, cookiesPath string, maxItems int) ([]ytdlp.PlaylistEntry, error)` is identical in the ytdlp method (Task 1), the `streamResolver` interface (Task 2), and `fakeResolver` (Task 2). `buildUserCatalog(ctx, def, enum)` (Task 5) matches every call site: `buildProviderCatalog` (Task 5), `buildInlineCatalog` (Task 6). `buildInlineCatalog(ctx, def, cfg, enum)`, `buildCachedOrSeedSnapshot(ctx, defs, cfg, cacheDir, enum)`, and `buildRemoteSnapshot(ctx, cfg, remote, cacheDir, userProviders, enum)` are used consistently across impl, the `refreshOnceDefault`/`buildStartupSnapshot` callers, and the updated `refresh_test.go` call. `userPlaylistEnumerator{resolver, cookiesPath, cacheDir, cfg}` field names match construction in `refreshOnceDefault`, `refreshCatalogsDefault`, `buildStartupSnapshot`, and all tests. `encodePlaylistItems`/`decodePlaylistItems`, `playlistEntriesToItems`, `playlistErrorForLog`, `cachedPlaylistItem` are defined once and reused; `userPlaylistCacheKey` is the pre-existing `user_cache_key.go` helper (not redefined). The two `buildUserCatalog` callers in `playback_test.go` (`installUserDirectAdapter`, `installUserSingleAdapter`) are updated to the 3-arg signature in Task 5, and both `buildRemoteSnapshot` callers in `refresh_test.go` (~174, ~1027) get the `enum` arg in Task 6 — leaving no caller on an old signature.
