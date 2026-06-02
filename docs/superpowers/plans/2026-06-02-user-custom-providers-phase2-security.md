# User Custom Providers — Phase 2: Security Hardening — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the §7 security primitives for user providers — a LAN-aware host
validator that rejects `file://`/loopback/link-local/metadata URLs, a no-`file`
FFmpeg input policy for user direct streams, and enforcement of the validator in
the store's `normalizeUserProvider` — so that when Phase 3 wires user channels
into the castable catalog, they are already safe. Pure, package-internal,
fully unit-tested; no adapter wiring, no playback changes, no UI.

**Architecture:** Two new pure helpers in a new `url_security.go`
(`validateUserProviderHost`, `userDirectInputPolicy`) plus a one-line call into
the existing `normalizeUserProvider` so the `userProviderStore` rejects unsafe
URLs at both save and load. The validator is IP-literal-aware via `net/netip`
(allow public + RFC1918/LAN, block loopback/link-local/metadata/unspecified/
multicast and any non-`http(s)` scheme); hostnames pass this syntactic gate and
get a DNS-resolving recheck at play time in Phase 3. `userDirectInputPolicy`
mirrors the existing bundled `directHLSInputPolicy()` minus the `file` protocol.

**Tech Stack:** Go 1.26, `net/url`, `net/netip`, the in-repo
`core.MediaInputPolicy` type (alias of `ffmpeg.MediaInputPolicy`). Tests are
standard `testing` table tests run with `go test ./internal/adapters/streams/`.

**Spec:** `docs/superpowers/specs/2026-06-01-user-custom-providers-design.md`
§7 (this phase implements §7.1's host rules as a reusable validator and §7.2's
`userDirectInputPolicy`; the *wiring* of the policy and the play-time
resolved-URL/redirect revalidation are Phase 3).

**Depends on:** Phase 1 (merged) — `normalizeUserProvider`, `userProviderStore`,
the `ProviderDefinition`/`ChannelDefinition` fields.

---

## Why this is Phase 2 (not catalog integration)

Phase 1's `normalizeUserProvider` only checks that a channel URL is non-empty;
it does NOT reject `file://`, loopback, or link-local hosts. Direct playback
uses `directHLSInputPolicy()`, which **allows the `file` protocol**. So the
moment Phase 3 merges user providers into the live `catalogs` map (making them
castable), a `user_providers.json` containing `file:///etc/shadow.m3u8` or a
loopback/metadata URL would play through FFmpeg with no guard. This phase closes
that gap *first* so no insecure intermediate state ever ships.

---

## File Structure

**Create**
- `internal/adapters/streams/url_security.go` — `validateUserProviderHost(raw)`
  (scheme/userinfo/IP-class checks) and `userDirectInputPolicy()` (no-`file`
  `core.MediaInputPolicy`). Pure; no store/adapter/playback coupling.
- `internal/adapters/streams/url_security_test.go` — the accept/reject matrix
  and the policy assertions.

**Modify**
- `internal/adapters/streams/provider_user.go` — call `validateUserProviderHost`
  for each channel inside `normalizeUserProvider`.
- `internal/adapters/streams/provider_user_test.go` — add a normalize/Put case
  proving an unsafe URL is rejected and a LAN URL is accepted.

**Boundaries:** `url_security.go` is the only new file; it imports `core` (for
the policy type), `net/url`, `net/netip`, `fmt`, `strings`, `time`. No new
behavior on the playback or catalog paths — Phase 3 consumes these primitives.

---

## Conventions for every task

- Run tests with: `go test ./internal/adapters/streams/ -run <TestName> -v`
  (from repo root `c:\Users\Jake\Git\MiSTer_GroovyRelay`).
- Package is `streams`; new files start with `package streams`.
- **Do NOT run `go test -race`** — the race detector needs cgo/gcc not available
  locally; it is a CI-only gate. Use plain `go test`.
- Commit after each task with the message shown, staging only the files that
  task touched.

---

### Task 1: LAN-aware host validator `validateUserProviderHost`

**Files:**
- Create: `internal/adapters/streams/url_security.go`
- Test: `internal/adapters/streams/url_security_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/streams/url_security_test.go`:

```go
package streams

import "testing"

func TestValidateUserProviderHost(t *testing.T) {
	accept := []string{
		"https://twitch.tv/formula1",
		"https://www.youtube.com/playlist?list=PLabc",
		"http://example.com/live/stream.m3u8",
		"http://10.0.0.5/s.m3u8",            // RFC1918 LAN
		"http://192.168.1.40:8080/s.m3u8",   // RFC1918 LAN + port
		"http://172.16.0.1/s.m3u8",          // RFC1918 LAN
		"http://[fc00::1]/s.m3u8",           // IPv6 ULA (private)
		"http://8.8.8.8/s.m3u8",             // public IPv4 literal
	}
	for _, u := range accept {
		if err := validateUserProviderHost(u); err != nil {
			t.Errorf("validateUserProviderHost(%q) unexpected error: %v", u, err)
		}
	}

	reject := []string{
		"",                                   // empty
		"://nope",                            // unparseable
		"file:///etc/shadow.m3u8",            // file scheme
		"ftp://example.com/x",                // non-http scheme
		"http://user:pass@example.com/x",     // userinfo
		"http://127.0.0.1/x",                 // loopback v4
		"http://[::1]/x",                     // loopback v6
		"http://169.254.169.254/latest/meta", // link-local / cloud metadata
		"http://[fe80::1]/x",                 // link-local v6
		"http://0.0.0.0/x",                   // unspecified
		"http://224.0.0.1/x",                 // multicast
		"https:///x",                         // empty host
	}
	for _, u := range reject {
		if err := validateUserProviderHost(u); err == nil {
			t.Errorf("validateUserProviderHost(%q) expected error, got nil", u)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestValidateUserProviderHost -v`
Expected: FAIL — `undefined: validateUserProviderHost` (build failed).

- [ ] **Step 3: Create `url_security.go` with the validator**

Create `internal/adapters/streams/url_security.go`:

```go
package streams

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"
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
	// the play-time resolved-IP recheck (Phase 3).
	if addr, err := netip.ParseAddr(host); err == nil {
		if err := validateUserProviderIP(addr); err != nil {
			return err
		}
	}
	return nil
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
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestValidateUserProviderHost -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/url_security.go internal/adapters/streams/url_security_test.go
git commit -m "feat(streams): LAN-aware host validator for user provider URLs"
```

---

### Task 2: No-`file` input policy `userDirectInputPolicy`

**Files:**
- Modify: `internal/adapters/streams/url_security.go`
- Test: `internal/adapters/streams/url_security_test.go` (append)

Reference: the existing bundled policy in `internal/adapters/streams/playback.go`
(`directHLSInputPolicy`) returns `core.MediaInputPolicy` with
`ProtocolWhitelist: []string{"file", "http", "https", "tcp", "tls", "crypto"}`,
`DisableRedirects: true`, `DisableReconnect: true`, `RWTimeout: 5*time.Second`,
`BlockedHeaders: []string{"Cookie", "Authorization", "Proxy-Authorization", "Referer"}`.
The user policy is identical **minus `file`** (user direct streams are not
bundled/host-locked, so FFmpeg must never open local files).

- [ ] **Step 1: Write the failing test (append)**

Append to `internal/adapters/streams/url_security_test.go`:

```go
func TestUserDirectInputPolicy(t *testing.T) {
	p := userDirectInputPolicy()

	for _, proto := range p.ProtocolWhitelist {
		if proto == "file" {
			t.Fatalf("userDirectInputPolicy must not whitelist the file protocol; got %v", p.ProtocolWhitelist)
		}
	}
	wantProtocols := map[string]bool{"http": true, "https": true, "tcp": true, "tls": true, "crypto": true}
	if len(p.ProtocolWhitelist) != len(wantProtocols) {
		t.Errorf("ProtocolWhitelist = %v, want exactly %v", p.ProtocolWhitelist, wantProtocols)
	}
	for _, proto := range p.ProtocolWhitelist {
		if !wantProtocols[proto] {
			t.Errorf("unexpected protocol %q in whitelist", proto)
		}
	}

	if !p.DisableRedirects {
		t.Error("DisableRedirects must be true")
	}
	if !p.DisableReconnect {
		t.Error("DisableReconnect must be true")
	}
	if p.RWTimeout <= 0 {
		t.Error("RWTimeout must be set")
	}
	blocked := map[string]bool{}
	for _, h := range p.BlockedHeaders {
		blocked[h] = true
	}
	for _, h := range []string{"Cookie", "Authorization", "Proxy-Authorization", "Referer"} {
		if !blocked[h] {
			t.Errorf("BlockedHeaders must include %q; got %v", h, p.BlockedHeaders)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams/ -run TestUserDirectInputPolicy -v`
Expected: FAIL — `undefined: userDirectInputPolicy`.

- [ ] **Step 3: Add `userDirectInputPolicy` to `url_security.go`**

Add the `core` and `time` imports to `url_security.go`'s import block:

```go
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
```

Append the function:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/streams/ -run TestUserDirectInputPolicy -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/url_security.go internal/adapters/streams/url_security_test.go
git commit -m "feat(streams): no-file user direct input policy"
```

---

### Task 3: Enforce `validateUserProviderHost` in `normalizeUserProvider`

**Files:**
- Modify: `internal/adapters/streams/provider_user.go`
- Test: `internal/adapters/streams/provider_user_test.go` (append) and/or
  `internal/adapters/streams/user_provider_store_test.go`

This makes the `userProviderStore` reject unsafe URLs at BOTH save (`Put`) and
load (`newUserProviderStore` drops the invalid provider, self-healing).

- [ ] **Step 1: Write the failing test (append)**

Append to `internal/adapters/streams/user_provider_store_test.go`:

```go
func TestUserStore_PutRejectsUnsafeURL(t *testing.T) {
	st, _ := newTestStore(t)
	def := sampleDef()
	def.Channels[0].URL = "file:///etc/shadow.m3u8"
	def.Channels[0].Kind = "" // let kind auto-detect; URL must still be rejected
	if _, err := st.Put(def); err == nil {
		t.Error("expected error for file:// channel url")
	}

	def2 := sampleDef()
	def2.Channels[0].URL = "http://127.0.0.1:8080/stream.m3u8"
	if _, err := st.Put(def2); err == nil {
		t.Error("expected error for loopback channel url")
	}
}

func TestUserStore_PutAcceptsLANURL(t *testing.T) {
	st, _ := newTestStore(t)
	def := sampleDef()
	def.Channels[0].URL = "http://192.168.1.40:8080/stream.m3u8"
	if _, err := st.Put(def); err != nil {
		t.Errorf("LAN url should be accepted, got: %v", err)
	}
}
```

(`sampleDef()` and `newTestStore` already exist from Phase 1 in this test file.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/streams/ -run 'TestUserStore_PutRejectsUnsafeURL|TestUserStore_PutAcceptsLANURL' -v`
Expected: FAIL — the `file://` and loopback Puts currently succeed (no host
validation yet), so `TestUserStore_PutRejectsUnsafeURL` fails its assertions.

- [ ] **Step 3: Call the validator in `normalizeUserProvider`**

In `internal/adapters/streams/provider_user.go`, inside `normalizeUserProvider`'s
channel loop, immediately AFTER the existing empty-URL check
(`if strings.TrimSpace(ch.URL) == "" { ... "url required" ... }`) and BEFORE the
kind auto-detection, insert:

```go
		if err := validateUserProviderHost(ch.URL); err != nil {
			return ProviderDefinition{}, fmt.Errorf("provider %q channel %q url: %w", def.ID, ch.Name, err)
		}
```

(No new imports — `fmt` is already imported in `provider_user.go`.)

- [ ] **Step 4: Run the new tests to verify they pass**

Run: `go test ./internal/adapters/streams/ -run 'TestUserStore_PutRejectsUnsafeURL|TestUserStore_PutAcceptsLANURL' -v`
Expected: PASS

- [ ] **Step 5: Run the full package to confirm no regressions**

Run: `go test ./internal/adapters/streams/ && go vet ./internal/adapters/streams/`
Expected: PASS, vet clean. (Confirms no existing Phase 1 store/normalize test used
an unsafe URL that would now be rejected — they all use `https://twitch.tv/...`
hostnames, which pass the validator.)

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/streams/provider_user.go internal/adapters/streams/user_provider_store_test.go
git commit -m "feat(streams): reject unsafe URLs in user provider normalization"
```

---

## Phase 2 done — what exists now

The store now refuses to persist or load a user provider whose channel URL uses
a forbidden scheme (`file://` etc.), userinfo, or an IP literal in the
loopback/link-local/metadata/unspecified/multicast ranges, while allowing
public and RFC1918/LAN hosts. `userDirectInputPolicy()` exists for Phase 3 to
attach to user `direct` items so FFmpeg never opens local files. All
package-internal, build green, `go test ./internal/adapters/streams/...` passing.
Nothing is wired into the running adapter or playback yet — Phase 3 does that,
safely.

---

## Roadmap — later phases (each its own plan via writing-plans)

- **Phase 3 — Catalog integration (now safe).** Add `userStore *userProviderStore`
  to the adapter (`New` builds it from `{data_dir}/user_providers.json`); merge
  `userStore.Snapshot()` into `buildStartupSnapshot` after bundled+remote
  (respecting `cfg.Providers[id].Disabled`); add the `userProviderType` case to
  `buildProviderCatalog` → new `buildUserCatalog` (mirrors
  `buildDirectStreamsCatalog`: `direct`→`StreamItem{Direct:true}`,
  `single`→`StreamItem{Direct:false}`; **skip `playlist` channels** until Phase 4,
  with a log); attach `userDirectInputPolicy()` to user direct items and call
  `validateUserProviderHost` on resolved `URL`+`AudioURL` (with DNS-resolved IP
  recheck) before FFmpeg; change `Catalog()` to emit enabled bundled + user
  providers and teach `buildChassisCatalogProvider` to fall back to
  `def.BadgeLabel` + a `u-<BadgeColor>` class for user providers; keep
  `BundledCatalog()` settings-only.
- **Phase 4 — Playlist enumeration.** `EnumeratePlaylist` on the resolver
  (`yt-dlp --flat-playlist`), cached under the §4.6 `userPlaylistCacheKey`,
  refreshed on the existing cycle; `buildUserCatalog` now populates `playlist`
  channels from the cache with the async pending/error states.
- **Phase 5 — Routes, interface, auto-enable hot-start.** `UserProviderEditor`
  interface; chassis create/update/delete/verify/reorder routes; the
  `EnsureStarted` capability + first-provider auto-enable with the restart-toast
  fallback; channel-delete preset-slot cleanup.
- **Phase 6 — Chassis UI.** Authoring form + `provider-form.js`
  (auto-detect/verify/reorder), the `.ic.u-<token>`/`.badge.u-<token>` palette
  CSS, the `providerStatus` SSE envelope, and `*.behavior.test.js` coverage.

---

## Self-review notes

- **Spec coverage (Phase 2 scope):** §7.1 host rules → `validateUserProviderHost`
  (Task 1) + enforced in the store (Task 3); §7.2 no-`file` policy →
  `userDirectInputPolicy` (Task 2). The *wiring* of the policy and the play-time
  resolved-URL/redirect/DNS revalidation are explicitly Phase 3.
- **No placeholders:** every step has runnable code/commands.
- **Type consistency:** `validateUserProviderHost`/`validateUserProviderIP`,
  `userDirectInputPolicy` are each defined once in `url_security.go` and
  referenced consistently; `core.MediaInputPolicy` matches the type returned by
  the existing `directHLSInputPolicy()`.
- **Regression risk:** Task 3 adds validation to `normalizeUserProvider`, which
  runs on every `Put` and load. Phase 1 tests use `https://twitch.tv/...`
  hostnames and `https://...youtube.com/...` URLs (all pass the validator), and
  the channel-limit test errors before the channel loop — so Step 5 confirms no
  existing test breaks. If any does, it indicates a Phase 1 fixture using an
  unsafe URL that should be updated to a LAN/public one.
