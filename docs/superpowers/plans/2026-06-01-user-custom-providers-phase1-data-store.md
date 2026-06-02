# User Custom Providers — Phase 1: Data Model & Store — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the persistent data layer for user-authored Streams providers — new
definition fields, a validated `{data_dir}/user_providers.json` store, and the
ID/slug/cache-key/palette helpers — fully unit-tested, before any catalog
merge, routes, or UI.

**Architecture:** A new `userProviderStore` mirrors the existing `presetStore`
(in-memory slice + atomic temp/rename writes + self-healing load). User
providers are plain `ProviderDefinition`s carrying `Type:"user"` with inline
groups/channels; this phase only persists and validates them — it does **not**
wire them into the running adapter's catalogs (that is Phase 2). All work is
package-internal to `internal/adapters/streams`, so the package builds and its
unit tests pass on their own.

**Tech Stack:** Go 1.26, `encoding/json`, `crypto/sha256`, `net/netip` (later
phases), the in-repo `internal/config.WriteAtomic` atomic writer, and the
`adapters.FieldError`/`adapters.QuickCastError` error types. Tests are standard
`testing` table tests run with `go test ./internal/adapters/streams/...`.

**Spec:** `docs/superpowers/specs/2026-06-01-user-custom-providers-design.md`
(this phase implements §4.1–4.6 data-model/store/cache-key parts; §4.7 kind
auto-detection is included as a pure helper).

---

## File Structure

**Modify**
- `internal/adapters/streams/provider.go` — add two persisted fields:
  `ProviderDefinition.BadgeColor` and `ChannelDefinition.Kind`.

**Create**
- `internal/adapters/streams/provider_user.go` — constants and pure helpers for
  user providers: the `user` type value, channel-kind constants, the badge-color
  palette + validation, glyph validation, slug/ID generation, and the syntactic
  channel-kind auto-detector. No I/O.
- `internal/adapters/streams/provider_user_test.go` — table tests for the above.
- `internal/adapters/streams/user_cache_key.go` — `userProviderCacheKey` /
  `userPlaylistCacheKey` producing sanitized keys that satisfy the existing
  `cacheKeyPattern`.
- `internal/adapters/streams/user_cache_key_test.go` — tests incl. a property
  check that emitted keys pass `validateCacheKey`.
- `internal/adapters/streams/user_provider_store.go` — the persistent store:
  load/validate/snapshot/Put/Delete with atomic writes and limit enforcement.
- `internal/adapters/streams/user_provider_store_test.go` — store tests.

**Boundaries:** `provider_user.go` is pure (no `os`, no `net`); the store is the
only file in this phase that touches the filesystem. The catalog builder, host
validation, routes, and UI deliberately live in later phases.

---

## Conventions for every task

- Run tests with: `go test ./internal/adapters/streams/ -run <TestName> -v`
  (from repo root `c:\Users\Jake\Git\MiSTer_GroovyRelay`).
- The package is `streams`; all new files start with `package streams`.
- Commit after each task with the message shown. Stage only the files that task
  touched (`git add <paths>`), then `git commit`.
- These are docs under a gitignored tree’s sibling `internal/` (NOT gitignored) —
  normal `git add` works for `internal/...` files.

---

### Task 1: Add `BadgeColor` and `Kind` definition fields

**Files:**
- Modify: `internal/adapters/streams/provider.go:23-65`
- Test: `internal/adapters/streams/provider_user_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/streams/provider_user_test.go`:

```go
package streams

import (
	"encoding/json"
	"testing"
)

func TestProviderDefinition_NewFieldsRoundTrip(t *testing.T) {
	raw := `{
		"id": "user:f1-tv",
		"type": "user",
		"display_name": "F1 TV",
		"badge_color": "amber",
		"groups": [{"id": "races", "name": "Races", "order": 0}],
		"channels": [{"id": "live", "name": "Live", "kind": "single", "url": "https://twitch.tv/formula1", "order": 0}]
	}`
	var def ProviderDefinition
	if err := json.Unmarshal([]byte(raw), &def); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if def.BadgeColor != "amber" {
		t.Errorf("BadgeColor = %q, want amber", def.BadgeColor)
	}
	if len(def.Channels) != 1 || def.Channels[0].Kind != "single" {
		t.Errorf("Channels[0].Kind = %q, want single", def.Channels[0].Kind)
	}

	out, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back ProviderDefinition
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if back.BadgeColor != "amber" || back.Channels[0].Kind != "single" {
		t.Errorf("round-trip lost new fields: %+v", back)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestProviderDefinition_NewFieldsRoundTrip -v`
Expected: FAIL — `def.BadgeColor undefined` / `Channels[0].Kind undefined` (compile error).

- [ ] **Step 3: Add the fields**

In `internal/adapters/streams/provider.go`, add `BadgeColor` to `ProviderDefinition`
(after `FallbackLabel`):

```go
	FallbackLabel       string              `json:"fallback_label,omitempty"`
	BadgeColor          string              `json:"badge_color,omitempty"`
```

And add `Kind` to `ChannelDefinition` (after `GroupID`):

```go
	GroupID     string   `json:"group_id,omitempty"`
	Kind        string   `json:"kind,omitempty"`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestProviderDefinition_NewFieldsRoundTrip -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/provider.go internal/adapters/streams/provider_user_test.go
git commit -m "feat(streams): add BadgeColor and channel Kind definition fields"
```

---

### Task 2: User-provider constants + channel-kind auto-detection

**Files:**
- Create: `internal/adapters/streams/provider_user.go`
- Test: `internal/adapters/streams/provider_user_test.go` (append)

- [ ] **Step 1: Write the failing test (append)**

Append to `internal/adapters/streams/provider_user_test.go`:

```go
func TestDetectChannelKind(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://cdn.example.com/live/stream.m3u8", kindDirect},
		{"https://cdn.example.com/live/stream.m3u", kindDirect},
		{"https://cdn.example.com/live/manifest.mpd", kindDirect},
		{"https://www.youtube.com/playlist?list=PLabc123", kindPlaylist},
		{"https://www.youtube.com/watch?v=abc&list=PLxyz", kindPlaylist},
		{"https://www.twitch.tv/formula1", kindSingle},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", kindSingle},
		{"https://vimeo.com/12345", kindSingle},
		{"   ", kindSingle},
	}
	for _, c := range cases {
		if got := detectChannelKind(c.url); got != c.want {
			t.Errorf("detectChannelKind(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestDetectChannelKind -v`
Expected: FAIL — `undefined: detectChannelKind` / `kindDirect` (compile error).

- [ ] **Step 3: Create `provider_user.go` with constants + detector**

Create `internal/adapters/streams/provider_user.go`:

```go
package streams

import (
	"net/url"
	"strings"
)

// userProviderType is the ProviderDefinition.Type value for user-authored
// providers. Its Groups/Channels are inline (not fetched), and its ID is
// always userProviderIDPrefix + slug.
const userProviderType = "user"

// userProviderIDPrefix namespaces user provider IDs so they can never
// collide with or shadow bundled/remote provider IDs in the merged maps.
const userProviderIDPrefix = "user:"

// Channel kinds (ChannelDefinition.Kind) for user providers.
const (
	kindPlaylist = "playlist"
	kindSingle   = "single"
	kindDirect   = "direct"
)

// Limits (spec §10).
const (
	maxUserProviders        = 32
	maxChannelsPerProvider  = 100
)

// detectChannelKind infers a channel Kind from a URL using purely syntactic
// rules (no network). First match wins (spec §4.7):
//  1. direct  — path ends in a recognized HLS/DASH manifest suffix.
//  2. playlist — YouTube list= URL.
//  3. single  — everything else (the default).
func detectChannelKind(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return kindSingle
	}
	path := strings.ToLower(u.Path)
	for _, suf := range []string{".m3u8", ".m3u", ".mpd"} {
		if strings.HasSuffix(path, suf) {
			return kindDirect
		}
	}
	host := strings.ToLower(u.Hostname())
	if isYouTubeHost(host) && u.Query().Get("list") != "" {
		return kindPlaylist
	}
	return kindSingle
}

func isYouTubeHost(host string) bool {
	host = strings.TrimPrefix(host, "www.")
	return host == "youtube.com" || host == "m.youtube.com" || host == "music.youtube.com"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestDetectChannelKind -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/provider_user.go internal/adapters/streams/provider_user_test.go
git commit -m "feat(streams): user-provider constants and channel-kind auto-detection"
```

---

### Task 3: Badge-color palette + glyph validation

**Files:**
- Modify: `internal/adapters/streams/provider_user.go`
- Test: `internal/adapters/streams/provider_user_test.go` (append)

- [ ] **Step 1: Write the failing test (append)**

```go
func TestNormalizeBadgeColor(t *testing.T) {
	cases := map[string]string{
		"amber":  "amber",
		"AMBER":  "amber",
		" teal ": "teal",
		"":       defaultBadgeColor,
		"fuchsia": defaultBadgeColor, // unknown token -> default
		"#ff0000": defaultBadgeColor, // raw hex rejected
	}
	for in, want := range cases {
		if got := normalizeBadgeColor(in); got != want {
			t.Errorf("normalizeBadgeColor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateGlyph(t *testing.T) {
	ok := []string{"F1", "CN", "TOM", "ABCD", "X"}
	for _, g := range ok {
		if err := validateGlyph(g); err != nil {
			t.Errorf("validateGlyph(%q) unexpected error: %v", g, err)
		}
	}
	bad := []string{"", "   ", "TOOLONG"}
	for _, g := range bad {
		if err := validateGlyph(g); err == nil {
			t.Errorf("validateGlyph(%q) expected error, got nil", g)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run 'TestNormalizeBadgeColor|TestValidateGlyph' -v`
Expected: FAIL — `undefined: normalizeBadgeColor` / `validateGlyph` / `defaultBadgeColor`.

- [ ] **Step 3: Add palette + glyph helpers to `provider_user.go`**

Append to `internal/adapters/streams/provider_user.go` (add `"fmt"` and
`"unicode/utf8"` to the import block):

```go
// badgeColorTokens is the curated palette (spec §8). Each token maps to a
// CRT-tuned .ic.u-<token> / .badge.u-<token> pair defined in chassis.css.
// Stored as a token, never raw hex, so the chassis never emits inline styles.
var badgeColorTokens = map[string]struct{}{
	"amber": {}, "red": {}, "teal": {}, "blue": {},
	"purple": {}, "green": {}, "cyan": {}, "slate": {},
}

const defaultBadgeColor = "slate"

// normalizeBadgeColor lowercases/trims a token and returns it if it is in the
// palette, else the default. Total (never errors) so malformed persisted data
// can never brick rendering (spec §4.3).
func normalizeBadgeColor(in string) string {
	t := strings.ToLower(strings.TrimSpace(in))
	if _, ok := badgeColorTokens[t]; ok {
		return t
	}
	return defaultBadgeColor
}

// validateGlyph enforces a 1–4 character (rune-counted) non-empty glyph.
func validateGlyph(g string) error {
	g = strings.TrimSpace(g)
	n := utf8.RuneCountInString(g)
	if n < 1 || n > 4 {
		return fmt.Errorf("glyph must be 1-4 characters, got %d", n)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run 'TestNormalizeBadgeColor|TestValidateGlyph' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/provider_user.go internal/adapters/streams/provider_user_test.go
git commit -m "feat(streams): badge-color palette and glyph validation"
```

---

### Task 4: Slug + stable ID generation

**Files:**
- Modify: `internal/adapters/streams/provider_user.go`
- Test: `internal/adapters/streams/provider_user_test.go` (append)

- [ ] **Step 1: Write the failing test (append)**

```go
func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"F1 TV":            "f1-tv",
		"Cartoon Network!": "cartoon-network",
		"  Lo-Fi  24/7  ":  "lo-fi-24-7",
		"???":              "provider",
	}
	for in, want := range cases {
		if got := slugify(in, "provider"); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueSlug(t *testing.T) {
	taken := map[string]bool{"f1-tv": true, "f1-tv-2": true}
	got := uniqueSlug("f1-tv", func(s string) bool { return taken[s] })
	if got != "f1-tv-3" {
		t.Errorf("uniqueSlug = %q, want f1-tv-3", got)
	}
	got = uniqueSlug("new", func(s string) bool { return taken[s] })
	if got != "new" {
		t.Errorf("uniqueSlug = %q, want new", got)
	}
}

func TestNewUserProviderID(t *testing.T) {
	taken := map[string]bool{"user:f1-tv": true}
	got := newUserProviderID("F1 TV", func(id string) bool { return taken[id] })
	if got != "user:f1-tv-2" {
		t.Errorf("newUserProviderID = %q, want user:f1-tv-2", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run 'TestSlugify|TestUniqueSlug|TestNewUserProviderID' -v`
Expected: FAIL — `undefined: slugify` / `uniqueSlug` / `newUserProviderID`.

- [ ] **Step 3: Add slug/ID helpers to `provider_user.go`**

Append (add `"strconv"` to imports):

```go
// slugify lowercases and reduces a display name to [a-z0-9-], collapsing
// runs of other characters to single hyphens and trimming edges. Returns
// fallback when nothing usable remains (so an ID is always derivable).
func slugify(name, fallback string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return fallback
	}
	return s
}

// uniqueSlug returns base, or base-2, base-3, ... until taken() is false.
func uniqueSlug(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		candidate := base + "-" + strconv.Itoa(n)
		if !taken(candidate) {
			return candidate
		}
	}
}

// newUserProviderID derives a locked "user:"-prefixed provider ID from a
// display name, de-duped against existing IDs via taken().
func newUserProviderID(displayName string, taken func(id string) bool) string {
	base := slugify(displayName, "provider")
	return uniqueSlug(userProviderIDPrefix+base, taken)
}

// newChannelID derives a locked channel ID (no "user:" prefix; scoped to its
// provider) from a channel name, de-duped against sibling channel IDs.
func newChannelID(channelName string, taken func(id string) bool) string {
	base := slugify(channelName, "channel")
	return uniqueSlug(base, taken)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run 'TestSlugify|TestUniqueSlug|TestNewUserProviderID' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/provider_user.go internal/adapters/streams/provider_user_test.go
git commit -m "feat(streams): slug and stable user provider/channel ID generation"
```

---

### Task 5: Sanitized cache keys for `user:` IDs

**Files:**
- Create: `internal/adapters/streams/user_cache_key.go`
- Test: `internal/adapters/streams/user_cache_key_test.go` (new)

Background: `cache.go`'s `validateCacheKey` only accepts `^[a-z0-9][a-z0-9-]{0,127}$`,
so a raw `user:f1-tv` (colon) is rejected. We hash the locked IDs into a key
that always validates (spec §4.6). `sha256Hex` already exists in `cache.go`.

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/streams/user_cache_key_test.go`:

```go
package streams

import "testing"

func TestUserCacheKeys_ValidateAndStable(t *testing.T) {
	pk := userProviderCacheKey("user:f1-tv")
	if err := validateCacheKey(pk); err != nil {
		t.Errorf("provider cache key %q invalid: %v", pk, err)
	}
	if pk != userProviderCacheKey("user:f1-tv") {
		t.Error("provider cache key not deterministic")
	}

	ck := userPlaylistCacheKey("user:f1-tv", "highlights")
	if err := validateCacheKey(ck); err != nil {
		t.Errorf("playlist cache key %q invalid: %v", ck, err)
	}
	// Distinct (provider, channel) pairs must not collide.
	if userPlaylistCacheKey("user:f1-tv", "highlights") == userPlaylistCacheKey("user:f1-tv", "races") {
		t.Error("distinct channels share a playlist cache key")
	}
	if userPlaylistCacheKey("user:a", "b-c") == userPlaylistCacheKey("user:a-b", "c") {
		t.Error("ambiguous separator collision between (a,b-c) and (a-b,c)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestUserCacheKeys_ValidateAndStable -v`
Expected: FAIL — `undefined: userProviderCacheKey` / `userPlaylistCacheKey`.

- [ ] **Step 3: Create `user_cache_key.go`**

```go
package streams

// userProviderCacheKey and userPlaylistCacheKey derive cache keys for
// user-authored providers (spec §4.6). Raw "user:" IDs contain a colon and
// would fail cache.go's validateCacheKey, so we hash the locked IDs. The hash
// input uses a NUL separator that cannot appear in IDs, so (a, b-c) and
// (a-b, c) never collide. sha256Hex (cache.go) emits lowercase hex, so the
// emitted key matches ^[a-z0-9][a-z0-9-]{0,127}$.
func userProviderCacheKey(providerID string) string {
	return "user-provider-" + sha256Hex([]byte(providerID))
}

func userPlaylistCacheKey(providerID, channelID string) string {
	return "user-playlist-" + sha256Hex([]byte(providerID+"\x00"+channelID))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestUserCacheKeys_ValidateAndStable -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/user_cache_key.go internal/adapters/streams/user_cache_key_test.go
git commit -m "feat(streams): sanitized cache keys for user provider IDs"
```

---

### Task 6: The user-provider store (load / validate / snapshot / Put / Delete)

**Files:**
- Create: `internal/adapters/streams/user_provider_store.go`
- Test: `internal/adapters/streams/user_provider_store_test.go` (new)

This mirrors `preset_store.go`: in-memory slice, atomic persistence via
`config.WriteAtomic`, self-healing load (malformed file → empty store, never
fatal). It does NOT resolve against a catalog — user providers ARE the source
of truth. Validation enforces the `user:` prefix, the palette/glyph/kind rules,
and the §10 limits.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapters/streams/user_provider_store_test.go`:

```go
package streams

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) (*userProviderStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "user_providers.json")
	st, err := newUserProviderStore(path)
	if err != nil {
		t.Fatalf("newUserProviderStore: %v", err)
	}
	return st, path
}

func sampleDef() ProviderDefinition {
	return ProviderDefinition{
		Type:        userProviderType,
		DisplayName: "F1 TV",
		BadgeLabel:  "F1",
		BadgeColor:  "amber",
		Channels: []ChannelDefinition{
			{Name: "Live", Kind: kindSingle, URL: "https://twitch.tv/formula1"},
		},
	}
}

func TestUserStore_PutAssignsIDsAndPersists(t *testing.T) {
	st, path := newTestStore(t)
	saved, err := st.Put(sampleDef())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if saved.ID != "user:f1-tv" {
		t.Errorf("provider ID = %q, want user:f1-tv", saved.ID)
	}
	if saved.Channels[0].ID != "live" {
		t.Errorf("channel ID = %q, want live", saved.Channels[0].ID)
	}

	// Reload from disk: the provider survives a restart.
	reloaded, err := newUserProviderStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Snapshot(); len(got) != 1 || got[0].ID != "user:f1-tv" {
		t.Errorf("reloaded snapshot = %+v, want one user:f1-tv", got)
	}
}

func TestUserStore_PutRejectsBadColorAndKind(t *testing.T) {
	st, _ := newTestStore(t)
	def := sampleDef()
	def.Channels[0].Kind = "bogus"
	if _, err := st.Put(def); err == nil {
		t.Error("expected error for invalid channel kind")
	}
}

func TestUserStore_PutEnforcesLimits(t *testing.T) {
	st, _ := newTestStore(t)
	def := sampleDef()
	def.Channels = nil
	for i := 0; i < maxChannelsPerProvider+1; i++ {
		def.Channels = append(def.Channels, ChannelDefinition{Name: "Ch", Kind: kindSingle, URL: "https://twitch.tv/x"})
	}
	if _, err := st.Put(def); err == nil {
		t.Error("expected error exceeding maxChannelsPerProvider")
	}
}

func TestUserStore_UpdatePreservesProviderID(t *testing.T) {
	st, _ := newTestStore(t)
	saved, _ := st.Put(sampleDef())

	upd := saved
	upd.DisplayName = "Formula 1" // rename must NOT change the locked ID
	again, err := st.Put(upd)
	if err != nil {
		t.Fatalf("update Put: %v", err)
	}
	if again.ID != saved.ID {
		t.Errorf("rename changed ID: %q -> %q", saved.ID, again.ID)
	}
	if len(st.Snapshot()) != 1 {
		t.Errorf("update created a duplicate: %d providers", len(st.Snapshot()))
	}
}

func TestUserStore_Delete(t *testing.T) {
	st, _ := newTestStore(t)
	saved, _ := st.Put(sampleDef())
	ok, err := st.Delete(saved.ID)
	if err != nil || !ok {
		t.Fatalf("Delete ok=%v err=%v", ok, err)
	}
	if len(st.Snapshot()) != 0 {
		t.Error("provider not removed")
	}
	if ok, _ := st.Delete("user:missing"); ok {
		t.Error("Delete of missing ID returned ok=true")
	}
}

func TestUserStore_LoadDropsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user_providers.json")
	// Garbage file must not crash load; store comes up empty.
	if err := writeFileString(path, "{not json"); err != nil {
		t.Fatal(err)
	}
	st, err := newUserProviderStore(path)
	if err != nil {
		t.Fatalf("load garbage: %v", err)
	}
	if len(st.Snapshot()) != 0 {
		t.Error("expected empty store after malformed file")
	}
}
```

Add this tiny helper at the bottom of the test file (keeps tests self-contained):

```go
func writeFileString(path, s string) error {
	return osWriteFile(path, []byte(s))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/streams/ -run TestUserStore -v`
Expected: FAIL — `undefined: newUserProviderStore` / `userProviderStore` / `osWriteFile`.

- [ ] **Step 3: Create `user_provider_store.go`**

```go
package streams

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

const userManifestVersion = 1

// osWriteFile is a thin seam so tests can write fixtures without importing os.
var osWriteFile = os.WriteFile

type userManifestFile struct {
	Version   int                  `json:"version"`
	Providers []ProviderDefinition `json:"providers"`
}

// userProviderStore owns the in-memory user manifest and its atomic file
// persistence. It mirrors presetStore: load is self-healing (a missing or
// malformed file yields an empty store, never a fatal error), and writes go
// through config.WriteAtomic.
type userProviderStore struct {
	mu        sync.Mutex
	path      string
	providers []ProviderDefinition
	saveErrs  *onePerInstanceLog
}

// newUserProviderStore loads + validates the manifest at path. Invalid
// individual providers are dropped with a log; a broken file yields an empty
// store. Returns error only for truly unexpected conditions (none today).
func newUserProviderStore(path string) (*userProviderStore, error) {
	st := &userProviderStore{path: path, saveErrs: &onePerInstanceLog{}}

	body, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return st, nil
	case err != nil:
		slog.Warn("user_providers: read failed; starting empty", "err", err, "path", path)
		return st, nil
	}

	var doc userManifestFile
	if err := json.Unmarshal(body, &doc); err != nil {
		slog.Warn("user_providers: parse failed; starting empty", "err", err, "path", path)
		return st, nil
	}
	if doc.Version != userManifestVersion {
		slog.Warn("user_providers: version mismatch; starting empty",
			"got", doc.Version, "want", userManifestVersion, "path", path)
		return st, nil
	}

	seen := map[string]bool{}
	for _, def := range doc.Providers {
		norm, err := normalizeUserProvider(def, seen)
		if err != nil {
			slog.Info("user_providers: dropped invalid provider on load", "id", def.ID, "err", err)
			continue
		}
		seen[norm.ID] = true
		st.providers = append(st.providers, norm)
	}
	return st, nil
}

// Snapshot returns a deep-ish copy safe for the caller to read. The slice is
// fresh; ProviderDefinition values are copied (their inner slices are shared,
// which is acceptable because callers only read).
func (s *userProviderStore) Snapshot() []ProviderDefinition {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ProviderDefinition, len(s.providers))
	copy(out, s.providers)
	return out
}

// Put creates or replaces a user provider. A definition whose ID is empty (or
// not "user:"-prefixed) is treated as a create: a locked ID is assigned from
// the display name. A definition whose ID matches an existing user provider is
// an in-place update that preserves the locked provider ID. Channel IDs are
// assigned/preserved the same way within the provider. Returns the saved,
// normalized definition.
func (s *userProviderStore) Put(def ProviderDefinition) (ProviderDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	if isUserProviderID(def.ID) {
		for i, p := range s.providers {
			if p.ID == def.ID {
				idx = i
				break
			}
		}
	}
	if idx < 0 && len(s.providers) >= maxUserProviders {
		return ProviderDefinition{}, badRequest("BANK FULL",
			fmt.Sprintf("streams: at most %d user providers", maxUserProviders))
	}

	taken := func(id string) bool {
		for i, p := range s.providers {
			if i == idx {
				continue // allow the provider being updated to keep its ID
			}
			if p.ID == id {
				return true
			}
		}
		return false
	}
	if idx < 0 || def.ID == "" {
		def.ID = newUserProviderID(def.DisplayName, taken)
	}

	norm, err := normalizeUserProvider(def, nil)
	if err != nil {
		return ProviderDefinition{}, err
	}

	next := append([]ProviderDefinition(nil), s.providers...)
	if idx >= 0 {
		next[idx] = norm
	} else {
		next = append(next, norm)
	}
	if err := s.persistLocked(next); err != nil {
		return ProviderDefinition{}, err
	}
	s.providers = next
	return norm, nil
}

// Delete removes the provider with the given ID. Returns ok=false (no error)
// when the ID is absent.
func (s *userProviderStore) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, p := range s.providers {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, nil
	}
	next := append(s.providers[:idx:idx], s.providers[idx+1:]...)
	if err := s.persistLocked(next); err != nil {
		return false, err
	}
	s.providers = next
	return true, nil
}

func (s *userProviderStore) persistLocked(providers []ProviderDefinition) error {
	doc := userManifestFile{Version: userManifestVersion, Providers: providers}
	bodyBytes, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		s.saveErrs.Warn("user_providers: marshal failed", "err", err)
		return fmt.Errorf("user_providers: marshal: %w", err)
	}
	if err := config.WriteAtomic(s.path, bodyBytes); err != nil {
		s.saveErrs.Warn("user_providers: write failed", "err", err, "path", s.path)
		return fmt.Errorf("user_providers: write: %w", err)
	}
	return nil
}

func isUserProviderID(id string) bool {
	return len(id) > len(userProviderIDPrefix) && id[:len(userProviderIDPrefix)] == userProviderIDPrefix
}

func badRequest(chip, msg string) error {
	return &adapters.QuickCastError{Status: http.StatusBadRequest, Chip: chip, Message: msg}
}
```

- [ ] **Step 4: Add `normalizeUserProvider` to `provider_user.go`**

Append to `internal/adapters/streams/provider_user.go`:

```go
// normalizeUserProvider validates and canonicalizes a user provider for
// storage. When seen != nil (load path) it also de-dupes the provider ID
// against earlier-loaded IDs. It enforces: the "user:" prefix, the glyph and
// palette rules, the per-provider channel limit, valid/auto-detected channel
// kinds, and stable channel IDs (assign on create, preserve on update,
// de-dupe within the provider). Returns a normalized copy or an error.
func normalizeUserProvider(def ProviderDefinition, seen map[string]bool) (ProviderDefinition, error) {
	def.Type = userProviderType

	if !isUserProviderID(def.ID) {
		return ProviderDefinition{}, fmt.Errorf("provider id %q must start with %q", def.ID, userProviderIDPrefix)
	}
	if seen != nil {
		if seen[def.ID] {
			return ProviderDefinition{}, fmt.Errorf("duplicate provider id %q", def.ID)
		}
	}
	if strings.TrimSpace(def.DisplayName) == "" {
		return ProviderDefinition{}, fmt.Errorf("provider %q: display_name required", def.ID)
	}
	if err := validateGlyph(def.BadgeLabel); err != nil {
		return ProviderDefinition{}, fmt.Errorf("provider %q: %w", def.ID, err)
	}
	def.BadgeColor = normalizeBadgeColor(def.BadgeColor)

	if len(def.Channels) > maxChannelsPerProvider {
		return ProviderDefinition{}, fmt.Errorf("provider %q: at most %d channels", def.ID, maxChannelsPerProvider)
	}

	channelIDs := map[string]bool{}
	for i := range def.Channels {
		ch := def.Channels[i]
		if strings.TrimSpace(ch.Name) == "" {
			return ProviderDefinition{}, fmt.Errorf("provider %q: channel name required", def.ID)
		}
		if strings.TrimSpace(ch.URL) == "" {
			return ProviderDefinition{}, fmt.Errorf("provider %q channel %q: url required", def.ID, ch.Name)
		}
		if ch.Kind == "" {
			ch.Kind = detectChannelKind(ch.URL)
		}
		switch ch.Kind {
		case kindPlaylist, kindSingle, kindDirect:
		default:
			return ProviderDefinition{}, fmt.Errorf("provider %q channel %q: invalid kind %q", def.ID, ch.Name, ch.Kind)
		}
		if ch.ID == "" {
			ch.ID = newChannelID(ch.Name, func(id string) bool { return channelIDs[id] })
		} else if channelIDs[ch.ID] {
			return ProviderDefinition{}, fmt.Errorf("provider %q: duplicate channel id %q", def.ID, ch.ID)
		}
		channelIDs[ch.ID] = true
		def.Channels[i] = ch
	}
	return def, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/adapters/streams/ -run TestUserStore -v`
Expected: PASS (all six store tests)

- [ ] **Step 6: Run the full package + vet**

Run: `go test ./internal/adapters/streams/ && go vet ./internal/adapters/streams/`
Expected: PASS, no vet complaints. (Confirms the new code doesn't break existing streams tests.)

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/streams/user_provider_store.go internal/adapters/streams/user_provider_store_test.go internal/adapters/streams/provider_user.go
git commit -m "feat(streams): persistent user-provider store with validation and limits"
```

---

## Phase 1 done — what exists now

A package-internal, fully unit-tested data layer: new definition fields, the
`userProviderStore` (load/validate/snapshot/Put/Delete with atomic writes,
ID/glyph/palette/kind validation and limits), stable ID generation, syntactic
kind auto-detection, and sanitized cache keys. Nothing is wired into the running
adapter yet, the build is green, and `go test ./internal/adapters/streams/...`
passes — a clean, shippable increment.

---

## Roadmap — later phases (each its own plan via writing-plans)

These are intentionally **not** bite-sized here; each becomes a dedicated plan
document when Phase 1 is merged.

- **Phase 2 — Catalog integration & playlist enumeration.** Wire
  `userProviderStore` into the adapter (`New`/`Start`/snapshot merge); add the
  `user`-type catalog builder (`provider_user.go` → items per kind, mirroring
  `buildDirectStreamsCatalog`); change `Catalog()` to emit enabled bundled +
  user providers while `BundledCatalog()` stays settings-only; add
  `EnumeratePlaylist` to the resolver and cache results under the §4.6 keys.
- **Phase 3 — Security.** `validateUserProviderHost` (allow LAN, block
  loopback/link-local/metadata/`file`); `userDirectInputPolicy()` (no `file`);
  redirect prevalidation; resolved `URL`+`AudioURL` revalidation.
- **Phase 4 — Routes, interface, auto-enable hot-start.** `adapters.UserProviderEditor`
  interface; chassis routes (create/update/delete/verify/reorder); the
  `EnsureStarted(name)` capability and first-provider auto-enable with the
  restart-toast fallback; channel-delete preset-slot cleanup.
- **Phase 5 — Chassis UI.** Authoring form template + `provider-form.js`
  (auto-detect/verify/reorder), the `u-<token>` badge CSS, the `providerStatus`
  SSE envelope, and the `*.behavior.test.js` coverage.

---

## Self-review notes

- **Spec coverage (Phase 1 scope):** §4.1 file shape (store), §4.2 store
  pattern, §4.3 new fields + palette token, §4.4 kind values, §4.5 ID
  namespacing + stable provider/channel IDs + update-preserves-ID, §4.6
  sanitized cache keys, §4.7 syntactic auto-detection — all have tasks.
  Catalog merge, security, routes, UI are explicitly deferred to Phases 2–5.
- **No placeholders:** every step contains runnable code/commands.
- **Type consistency:** `userProviderType`, `kind*`, `defaultBadgeColor`,
  `newUserProviderID`/`newChannelID`, `userProviderCacheKey`/
  `userPlaylistCacheKey`, `normalizeUserProvider`, `userProviderStore.Put`/
  `Delete`/`Snapshot` are defined once and referenced consistently across
  tasks. `Put` deliberately handles both create and update (preserving the
  locked ID) so Phase 4’s route layer has a single entry point.
