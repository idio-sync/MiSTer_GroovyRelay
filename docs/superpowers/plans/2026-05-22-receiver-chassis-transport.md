# Receiver Chassis Transport Controls (Phase 1 / Spec 3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **Import hygiene:** When snippets show imports for an existing Go file, merge the new names into the existing import block and run `gofmt`; do not create a second `import` declaration.

**Goal:** Wire the chassis transport row (previous / next / pause-resume / stop / replay / seek) to the bridge playback dispatch via two same-origin POST endpoints, and push a new `transport` SSE event on the existing `/receiver/events` stream so multi-tab views stay synchronized. Refactor `/ui/playback`'s inline dispatch to share a single `internal/playback.Dispatcher` with the chassis, preserving `/ui`'s HTML response shape byte-identically.

**Architecture:** A new `internal/playback` package owns the snapshot + provider-lookup + dispatch dance (formerly inline in `internal/ui/playback.go`). Both consumers (`/ui` and chassis) depend on it through narrow, structurally-satisfied interfaces. The chassis snapshot cache extends with a `Transport` field populated via the dispatcher's `PlaybackViewForSnapshot(ctx, snap)` method, so the Spec 2 invariant of one `Manager.mu` acquisition per chassis tick is preserved. Two existing chassis POST helpers (`requireSameOrigin` middleware + `writeJSONError`) are reused from Spec 4. Client-side, `transport.js` follows Spec 4's `visualizer-bank.js` pattern of attaching named-event listeners to the shared `window.Chassis.events.source` exposed by `vfd-live.js`.

**Tech Stack:** Go 1.26 stdlib (`net/http`, `encoding/json`, `errors`, `context`, `sync`, `time`), zero new dependencies. Browser-side: vanilla ES2022 + `EventSource` (already in use since Spec 2).

**Spec:** [docs/superpowers/specs/2026-05-22-receiver-chassis-transport-design.md](../specs/2026-05-22-receiver-chassis-transport-design.md).

**Phase 1 plan references (format model):** [docs/superpowers/plans/2026-05-21-receiver-chassis-vfd-live.md](2026-05-21-receiver-chassis-vfd-live.md), [docs/superpowers/plans/2026-05-21-receiver-chassis-visualizer-mode.md](2026-05-21-receiver-chassis-visualizer-mode.md).

---

## File Structure

**New files:**

| Path | Responsibility |
|---|---|
| `internal/playback/dispatcher.go` | `Dispatcher` type + `NewDispatcher` + `PlaybackView` / `PlaybackViewForSnapshot` / `HandlePlaybackAction` + private `playbackProviderForSnapshot` helper |
| `internal/playback/dispatcher_test.go` | source-first lookup, stale-session sentinel, unsupported-action sentinel, lock-release contract, legacy-message normalization, negative-offset clamp tests |
| `internal/chassis/transport.go` | `handleTransportAction` + `handleTransportSeek` POST handlers |
| `internal/chassis/transport_test.go` | Layer 1 handler tests (status mapping, validation, 204 success, Cache-Control, Content-Type) |
| `internal/chassis/static/transport.js` | EventSource attach for `transport` event; button-click POSTs to `/receiver/transport/action`; seek-drag POSTs to `/receiver/transport/seek` |

**Files modified:**

| Path | Change |
|---|---|
| `internal/adapters/playback.go` | Add `ErrActiveSessionChanged` and `ErrPlaybackActionUnsupported` typed sentinels |
| `internal/adapters/streams/playback_provider.go` | Migrate stale-session + unsupported-verb error paths to errors.Is-friendly sentinels |
| `internal/adapters/torrent/playback_provider.go` | Same migration |
| `internal/adapters/url/playback_provider.go` | Same migration |
| `internal/adapters/streams/playback_provider_test.go` | Update assertions to use `errors.Is(err, sentinel)` |
| `internal/adapters/torrent/playback_provider_test.go` | Same |
| `internal/adapters/url/playback_provider_test.go` | Same |
| `internal/ui/server.go` | Add `Config.Playback PlaybackService` interface + default-build wiring in `New` |
| `internal/ui/playback.go` | `handlePlaybackMutation` + `buildPlaybackBannerData` delegate to `s.playback`; delete inline `playbackProviderForSnapshot` |
| `internal/chassis/session.go` | Add `TransportViewer` + `TransportController` interfaces |
| `internal/chassis/server.go` | `Config` gains `TransportViewer` + `TransportController` fields; `Server` stores them; `Mount` registers `POST /receiver/transport/{action,seek}` via `requireSameOrigin` |
| `internal/chassis/data.go` | Rename `TransportData.PlayState` → `State`; add `OffsetMS`, `DurationMS`, `ActionsEnabled`, `AdapterRef`, `Generation`; add `ActionsEnabled` struct; update `idleSnapshot()` to empty-string placeholders |
| `internal/chassis/chassis_test.go` | Update existing `TestIdleSnapshot_*` and `TestHandleIndex_*` fixtures for the new field/placeholder shape; add template assertions for `data-transport-*` hooks |
| `internal/chassis/events.go` | Add `transportEnvelope` + `actionsEnabledE` + `transportEnvelopeFrom` + `transportChanged`; `handleEvents` initial-snapshot emits `transport`; diff loop emits `transport` on change |
| `internal/chassis/events_test.go` | Update existing `TestHandleEvents_*` + `TestSnapshotCache_*` tests that count initial-snapshot events (3 → 4); add new transport-event tests |
| `internal/chassis/session.go` | Extend `snapshotFromSession` to populate `Transport` via `TransportViewer.PlaybackViewForSnapshot` |
| `internal/chassis/templates/transport.html` | Add `data-transport-*` hooks, two `data-state-icon` spans, `{{if not ...}}disabled{{end}}` markup |
| `internal/chassis/templates/shell.html` | Add `<script defer src=".../transport.js?v=...">` after `vfd-live.js`; add `<meta name="chassis-adapter-ref" ...>` and `<meta name="chassis-generation" ...>` |
| `internal/chassis/static/chassis.css` | Add CSS for icon swap and disabled-seek pointer-events guard |
| `internal/chassis/import_check_test.go` | Extend `rules` table for `internal/playback` boundary |
| `cmd/mister-groovy-relay/main.go` | Build `playback.NewDispatcher(coreMgr, reg)` once; pass to `ui.Config.Playback`, `chassis.Config.TransportViewer`, `chassis.Config.TransportController` |
| `tests/integration/chassis_test.go` | Add Layer 3 tests for the POST routes + SSE-reflects-action |

**Files unchanged:** All `internal/core/*` production code (no new methods on `Manager`). `internal/chassis/static/vfd-live.js` (Spec 4 already exposes the EventSource sharing surface).

---

## Task 1: Sentinels + provider migration

**Files:**
- Modify: `internal/adapters/playback.go`
- Modify: `internal/adapters/streams/playback_provider.go`, `internal/adapters/streams/playback_provider_test.go`
- Modify: `internal/adapters/torrent/playback_provider.go`, `internal/adapters/torrent/playback_provider_test.go`
- Modify: `internal/adapters/url/playback_provider.go`, `internal/adapters/url/playback_provider_test.go`

- [ ] **Step 1: Write a failing test asserting the sentinels are exported and `errors.Is`-friendly**

Append to the existing `internal/adapters/playback_test.go`:

```go
package adapters

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrActiveSessionChanged_IsErrorsIsFriendly(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("provider context: %w", ErrActiveSessionChanged)
	if !errors.Is(wrapped, ErrActiveSessionChanged) {
		t.Fatalf("wrapped sentinel should satisfy errors.Is")
	}
	if ErrActiveSessionChanged.Error() != ErrActiveSessionChangedMessage {
		t.Errorf("Error() = %q, want %q", ErrActiveSessionChanged.Error(), ErrActiveSessionChangedMessage)
	}
}

func TestErrPlaybackActionUnsupported_IsErrorsIsFriendly(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("provider context: %w", ErrPlaybackActionUnsupported)
	if !errors.Is(wrapped, ErrPlaybackActionUnsupported) {
		t.Fatalf("wrapped sentinel should satisfy errors.Is")
	}
}

func TestUnsupportedPlaybackActionError_PreservesMessageAndUnwraps(t *testing.T) {
	t.Parallel()
	const msg = "streams adapter does not support previous"
	err := UnsupportedPlaybackActionError(msg)
	if err.Error() != msg {
		t.Fatalf("Error() = %q, want %q", err.Error(), msg)
	}
	if !errors.Is(err, ErrPlaybackActionUnsupported) {
		t.Fatalf("UnsupportedPlaybackActionError should unwrap to ErrPlaybackActionUnsupported")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/adapters/ -run 'TestErrActiveSessionChanged|TestErrPlaybackActionUnsupported|TestUnsupportedPlaybackActionError'`
Expected: FAIL — `ErrActiveSessionChanged` and `ErrPlaybackActionUnsupported` undefined.

- [ ] **Step 3: Add the sentinels to `internal/adapters/playback.go`**

Extend the existing `const`/`var` block at the top of the file. Add `"errors"` to imports if not already present.

```go
import (
	"context"
	"errors"
)

const (
	// existing constants (PlaybackActionPause, ..., ErrActiveSessionChangedMessage) stay as-is
	ErrActiveSessionChangedMessage = "active session changed"
)

// ErrActiveSessionChanged is the typed sentinel form of
// ErrActiveSessionChangedMessage. Providers return or wrap this when the
// caller's adapter_ref + generation no longer matches the active session;
// the playback dispatcher maps it to HTTP 409 for the chassis and to the
// existing inline-error banner message for /ui.
var ErrActiveSessionChanged = errors.New(ErrActiveSessionChangedMessage)

// ErrPlaybackActionUnsupported is returned by the dispatcher when the
// active adapter does not implement PlaybackControlProvider, and by
// providers that recognize the action verb but don't support it on the
// active session. Maps to HTTP 422 on the chassis; /ui surfaces the
// provider's existing inline message.
var ErrPlaybackActionUnsupported = errors.New("active adapter does not expose playback controls")

type playbackActionUnsupportedError struct {
	message string
}

func (e playbackActionUnsupportedError) Error() string {
	if e.message != "" {
		return e.message
	}
	return ErrPlaybackActionUnsupported.Error()
}

func (e playbackActionUnsupportedError) Unwrap() error {
	return ErrPlaybackActionUnsupported
}

// UnsupportedPlaybackActionError returns an error whose visible message
// is exactly the supplied provider message, while errors.Is still matches
// ErrPlaybackActionUnsupported. Providers use this instead of fmt.Errorf
// with %w so /ui's legacy banner text stays byte-for-byte compatible.
func UnsupportedPlaybackActionError(message string) error {
	return playbackActionUnsupportedError{message: message}
}
```

- [ ] **Step 4: Run the test to verify the sentinels pass**

Run: `go test ./internal/adapters/ -run 'TestErrActiveSessionChanged|TestErrPlaybackActionUnsupported|TestUnsupportedPlaybackActionError'`
Expected: PASS.

- [ ] **Step 5: Migrate `streams` provider**

Edit `internal/adapters/streams/playback_provider.go`.

**`StreamsError` requires special handling.** The streams provider currently returns errors via `playbackError(...)`, which returns `*StreamsError`. `*StreamsError` has no `Unwrap` method (`internal/adapters/streams/errors.go`), so wrapping the sentinel inside the message string alone does NOT satisfy `errors.Is`. Two options:

**Option A (recommended).** For the stale-session and unsupported-action paths specifically, bypass `playbackError` and return the typed sentinel directly:

```go
// Before, inside ensureOwnsCoreSession:
return playbackError("", adapters.ErrActiveSessionChangedMessage)
// After:
return adapters.ErrActiveSessionChanged

// HandlePlaybackAction keeps its existing two-value return shape:
if err := a.ensureOwnsCoreSession(req.AdapterRef, req.Generation); err != nil {
	return adapters.PlaybackActionResult{}, err
}

// Before (representative unsupported-action return):
return adapters.PlaybackActionResult{}, fmt.Errorf("streams adapter does not support %s", req.Action)
// After:
return adapters.PlaybackActionResult{}, adapters.UnsupportedPlaybackActionError(fmt.Sprintf("streams adapter does not support %s", req.Action))
```

This loses the `*StreamsError` type for these two error paths. Verify no `errors.As(err, &StreamsError{})` callers depend on those specific paths (`rg -n 'errors\.As.*StreamsError'` should find zero production hits; the only callers are inside `internal/adapters/streams/` itself).

**Option B (more invasive).** Add an `Unwrap() error` method to `*StreamsError` so wrapping a sentinel inside `StreamsError.Message` (via a new `Cause error` field, say) participates in `errors.Is`. This preserves the streams-specific error type for downstream callers. Larger change; defer if option A is sufficient.

Use **Option A** by default — its scope is small and the streams-specific error type carries no semantic load on these two paths (both are sentinel-recognized at the dispatcher anyway).

Update `internal/adapters/streams/playback_provider_test.go` assertions:

```go
// Before:
if err == nil || err.Error() != adapters.ErrActiveSessionChangedMessage { t.Fatal(...) }
// After:
if !errors.Is(err, adapters.ErrActiveSessionChanged) { t.Fatalf("want stale-session sentinel, got %v", err) }
```

Add `"errors"` to test imports if not already present.

- [ ] **Step 6: Migrate `torrent` provider**

Same pattern as Step 5. Find `fmt.Errorf("%s", adapters.ErrActiveSessionChangedMessage)` and switch to `adapters.ErrActiveSessionChanged`. Find unsupported-verb returns and use `adapters.UnsupportedPlaybackActionError(<existing message>)` so `errors.Is(err, adapters.ErrPlaybackActionUnsupported)` succeeds without changing `/ui`'s visible banner text. Update `playback_provider_test.go` to use `errors.Is`.

- [ ] **Step 7: Migrate `url` provider**

Same pattern. Five occurrences of `fmt.Errorf("%s", adapters.ErrActiveSessionChangedMessage)` per the earlier search — switch each to `adapters.ErrActiveSessionChanged`. Use `adapters.UnsupportedPlaybackActionError(<existing message>)` for unsupported verbs. Update `playback_provider_test.go` to use `errors.Is`.

- [ ] **Step 8: Run all adapter playback tests**

Run: `go test ./internal/adapters/...`
Expected: PASS — all three providers + the new sentinel tests.

- [ ] **Step 9: Run full repo tests to confirm no caller broke**

Run: `go test ./...`
Expected: PASS — `/ui/playback_test.go` still green because the legacy string message text didn't change (the sentinel's `Error()` returns the same string).

- [ ] **Step 10: Commit**

```bash
git add internal/adapters/playback.go internal/adapters/playback_test.go \
  internal/adapters/streams/playback_provider.go internal/adapters/streams/playback_provider_test.go \
  internal/adapters/torrent/playback_provider.go internal/adapters/torrent/playback_provider_test.go \
  internal/adapters/url/playback_provider.go internal/adapters/url/playback_provider_test.go
git commit -m "$(cat <<'EOF'
feat(adapters): typed playback error sentinels + provider migration

Phase 1 / Spec 3 task 1. Adds errors.Is-friendly sentinels:
- ErrActiveSessionChanged (typed form of ErrActiveSessionChangedMessage).
- ErrPlaybackActionUnsupported (new; surfaces "no playback controls" /
  "unsupported verb" provider responses to the dispatcher).

Migrates the three current playback providers (streams, torrent, url):
stale-session returns now use the typed sentinel directly; unsupported
verb returns use UnsupportedPlaybackActionError so errors.Is detects
ErrPlaybackActionUnsupported while the existing provider-specific
message text is preserved for /ui's banner display.

Behaviour unchanged for /ui (Error() string identical via sentinel
declaration). Dispatcher migration in the next task starts depending
on these sentinels.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `playback.Dispatcher` — `NewDispatcher` + `PlaybackView` + source-first lookup

**Files:**
- Create: `internal/playback/dispatcher.go`
- Create: `internal/playback/dispatcher_test.go`

- [ ] **Step 1: Write failing tests for `PlaybackView` and the source-first policy**

Create `internal/playback/dispatcher_test.go`:

```go
package playback

import (
	"context"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// fakeStatusViewer is the test double for StatusViewer.
type fakeStatusViewer struct{ view core.StatusHomeView }

func (f *fakeStatusViewer) StatusHomeView() core.StatusHomeView { return f.view }

type countingStatusViewer struct {
	calls int
	view  core.StatusHomeView
}

func (f *countingStatusViewer) StatusHomeView() core.StatusHomeView {
	f.calls++
	return f.view
}

// fakeProvider implements adapters.PlaybackControlProvider.
type fakeProvider struct {
	name        string
	bannerOK    bool
	bannerView  adapters.PlaybackBannerAdapterView
	lastSnap    adapters.PlaybackBannerSnapshot
	lastReq     adapters.PlaybackActionRequest
	actionErr   error
	actionResult adapters.PlaybackActionResult
}

func (p *fakeProvider) PlaybackBanner(ctx context.Context, snap adapters.PlaybackBannerSnapshot) (adapters.PlaybackBannerAdapterView, bool) {
	p.lastSnap = snap
	return p.bannerView, p.bannerOK
}

func (p *fakeProvider) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	p.lastReq = req
	return p.actionResult, p.actionErr
}

type fakeAdapter struct {
	*fakeProvider
	adapterName string
}

func (a *fakeAdapter) Name() string { return a.adapterName }
func (a *fakeAdapter) DisplayName() string { return a.adapterName }
func (a *fakeAdapter) Fields() []adapters.FieldDef { return nil }
func (a *fakeAdapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error { return nil }
func (a *fakeAdapter) IsEnabled() bool { return true }
func (a *fakeAdapter) Start(ctx context.Context) error { return nil }
func (a *fakeAdapter) Stop() error { return nil }
func (a *fakeAdapter) Status() adapters.Status { return adapters.Status{} }
func (a *fakeAdapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

func registerFakeProvider(t *testing.T, reg *adapters.Registry, name string, provider *fakeProvider) *fakeAdapter {
	t.Helper()
	adapter := &fakeAdapter{fakeProvider: provider, adapterName: name}
	if err := reg.Register(adapter); err != nil {
		t.Fatalf("Register(%q): %v", name, err)
	}
	return adapter
}

func TestDispatcher_PlaybackView_NoActiveSession(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(&fakeStatusViewer{view: core.StatusHomeView{State: core.StateIdle}}, adapters.NewRegistry())
	_, ok := d.PlaybackView(context.Background())
	if ok {
		t.Fatalf("PlaybackView with idle status should return ok=false")
	}
}

func TestDispatcher_PlaybackView_DelegatesToOwningProvider(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{
		name:       "plex",
		bannerOK:   true,
		bannerView: adapters.PlaybackBannerAdapterView{Title: "live title"},
	}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)

	d := NewDispatcher(&fakeStatusViewer{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Source:     "plex",
		AdapterRef: "plex:abc",
		Generation: 7,
	}}, reg)

	view, ok := d.PlaybackView(context.Background())
	if !ok {
		t.Fatalf("PlaybackView with live session + matching provider should return ok=true")
	}
	if view.Title != "live title" {
		t.Errorf("PlaybackView returned title %q, want %q", view.Title, "live title")
	}
	if provider.lastSnap.AdapterRef != "plex:abc" || provider.lastSnap.Generation != 7 {
		t.Errorf("provider received wrong snapshot: %+v", provider.lastSnap)
	}
}
```

**Implementer note on the Registry API.** The snippets above use a real `adapters.Adapter` fake because `adapters.Registry.Register` accepts `Register(Adapter)`, not a bare playback provider. Keep later tests on the same `registerFakeProvider` helper so the plan stays copy-safe; compare provider lookup identity against the returned `*fakeAdapter`, not the embedded `*fakeProvider`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/playback/`
Expected: FAIL — package `playback` doesn't exist yet.

- [ ] **Step 3: Implement `Dispatcher` with `PlaybackView` and `playbackProviderForSnapshot`**

Create `internal/playback/dispatcher.go`:

```go
// Package playback provides the shared playback view + action dispatcher
// used by both /ui and the chassis. It sits above core (consumes
// StatusViewer) and above adapters (consumes Registry), so neither
// internal/core nor internal/adapters imports back into playback.
//
// The dispatcher mirrors what /ui/playback's handlePlaybackBanner did
// inline before Spec 3: snapshot the active session, find the owning
// adapter via the source-first policy (with a legacy adapter-ref-prefix
// fallback for sessions installed before Source was first-class), then
// either return the provider's banner view or dispatch a playback action.
package playback

import (
	"context"
	"strings"  // used by the source-first prefix scan ported from /ui

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// StatusViewer is the narrow read-only view of session state the
// dispatcher needs. *core.Manager satisfies it structurally via
// StatusHomeView(). Tests inject fakes.
type StatusViewer interface {
	StatusHomeView() core.StatusHomeView
}

// Dispatcher composes a StatusViewer with the adapter registry and
// exposes both read (PlaybackView, PlaybackViewForSnapshot) and write
// (HandlePlaybackAction) surfaces.
type Dispatcher struct {
	status   StatusViewer
	registry *adapters.Registry
}

func NewDispatcher(status StatusViewer, registry *adapters.Registry) *Dispatcher {
	return &Dispatcher{status: status, registry: registry}
}

// PlaybackView snapshots the active session, looks up the owning
// playback provider via the source-first policy, and returns the
// provider's banner view. Returns (_, false) when there is no view to
// render — caller should render read-only / disabled controls. The
// bool deliberately collapses three distinct underlying conditions (no
// active session / no playback provider for the active adapter /
// provider returned owns=false): callers do not distinguish among them
// visually.
func (d *Dispatcher) PlaybackView(ctx context.Context) (adapters.PlaybackBannerAdapterView, bool) {
	snap := d.status.StatusHomeView()
	return d.PlaybackViewForSnapshot(ctx, snap)
}

// PlaybackViewForSnapshot is the snapshot-already-acquired variant of
// PlaybackView. Callers that already have a fresh core.StatusHomeView
// (e.g. the chassis snapshot refresher, which acquires one per tick
// anyway) pass it through to avoid a second Manager.mu acquisition.
// Same return contract as PlaybackView.
func (d *Dispatcher) PlaybackViewForSnapshot(ctx context.Context, snap core.StatusHomeView) (adapters.PlaybackBannerAdapterView, bool) {
	if snap.State == core.StateIdle {
		return adapters.PlaybackBannerAdapterView{}, false
	}
	provider, ok := d.playbackProviderForSnapshot(snap)
	if !ok {
		return adapters.PlaybackBannerAdapterView{}, false
	}
	pbSnap := snapshotForProvider(snap)
	return provider.PlaybackBanner(ctx, pbSnap)
}

// playbackProviderForSnapshot resolves the adapter that owns the active
// session and exposes the PlaybackControlProvider interface, if any.
//
// Source-first policy (mirrors /ui/playback's prior inline impl): when
// snap.Source names a registered adapter, that adapter is the sole
// candidate — if it doesn't implement PlaybackControlProvider, this
// returns (nil, false) rather than falling through to the legacy
// adapter-ref prefix scan. The scan runs as a legacy fallback when
// Source is empty or names no registered adapter (older sessions
// installed before Source was a first-class field on core.StatusHomeView).
//
// Takes core.StatusHomeView (not adapters.PlaybackBannerSnapshot like
// /ui's original): the public dispatcher methods accept core snapshots,
// and the conversion to PlaybackBannerSnapshot happens once at the
// provider.PlaybackBanner / provider.HandlePlaybackAction call site
// inside PlaybackViewForSnapshot.
func (d *Dispatcher) playbackProviderForSnapshot(snap core.StatusHomeView) (adapters.PlaybackControlProvider, bool) {
	if d.registry == nil || snap.AdapterRef == "" {
		return nil, false
	}
	if snap.Source != "" {
		if a, ok := d.registry.Get(snap.Source); ok {
			p, ok := a.(adapters.PlaybackControlProvider)
			return p, ok
		}
	}
	for _, a := range d.registry.List() {
		if adapterRefBelongsTo(a.Name(), snap.AdapterRef) {
			p, ok := a.(adapters.PlaybackControlProvider)
			return p, ok
		}
	}
	return nil, false
}

func adapterRefBelongsTo(adapterName, ref string) bool {
	return strings.HasPrefix(ref, adapterName+":") || strings.HasPrefix(ref, adapterName+"/")
}

// snapshotForProvider converts the core view into the adapter-facing
// PlaybackBannerSnapshot type. The conversion is mechanical — copy
// the equivalent helper from /ui/playback.go if one exists, or write
// the field-for-field copy here. The implementer should search for
// currentPlaybackSnapshot / PlaybackBannerSnapshot in /ui to find the
// existing conversion.
func snapshotForProvider(snap core.StatusHomeView) adapters.PlaybackBannerSnapshot {
	return adapters.PlaybackBannerSnapshot{
		State:      snap.State,
		AdapterRef: snap.AdapterRef,
		Source:     snap.Source,
		Title:      snap.Title,
		Position:   snap.Position,
		Duration:   snap.Duration,
		StartedAt:  snap.StartedAt,
		MediaKind:  snap.MediaKind,
		Modeline:   snap.Modeline,
		Generation: snap.Generation,
	}
}

```

**Implementer note:** The body of `playbackProviderForSnapshot` and `snapshotForProvider` must match `/ui/playback.go`'s existing implementations byte-for-byte (the source-first policy is non-trivial and Spec 3 explicitly relies on the same semantics). Copy and adjust import paths only.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/playback/`
Expected: PASS.

- [ ] **Step 5: Add focused source-first policy tests**

Task 2 Step 3 already implemented the source-first lookup. Add the focused policy tests below so the exact no-fallthrough and legacy-ref-scan cases are locked down before `/ui` delegates to the dispatcher.

Append to `dispatcher_test.go`:

```go
func TestDispatcher_PlaybackProviderForSnapshot_SourceFirstPolicy(t *testing.T) {
	t.Parallel()
	plex := &fakeProvider{name: "plex"}
	dlna := &fakeProvider{name: "dlna"}
	reg := adapters.NewRegistry()
	plexAdapter := registerFakeProvider(t, reg, "plex", plex)
	registerFakeProvider(t, reg, "dlna", dlna)

	d := NewDispatcher(nil, reg)
	snap := core.StatusHomeView{Source: "plex", AdapterRef: "plex:abc"}

	p, ok := d.playbackProviderForSnapshot(snap)
	if !ok || p != plexAdapter {
		t.Errorf("source-first: got provider %v ok=%v, want plex adapter", p, ok)
	}
}

func TestDispatcher_PlaybackProviderForSnapshot_LegacyRefScanFallback(t *testing.T) {
	t.Parallel()
	plex := &fakeProvider{name: "plex"}
	reg := adapters.NewRegistry()
	plexAdapter := registerFakeProvider(t, reg, "plex", plex)

	d := NewDispatcher(nil, reg)
	// Empty Source triggers the legacy adapter-ref-prefix scan.
	snap := core.StatusHomeView{Source: "", AdapterRef: "plex:abc"}

	p, ok := d.playbackProviderForSnapshot(snap)
	if !ok || p != plexAdapter {
		t.Errorf("legacy fallback: got provider %v ok=%v, want plex adapter", p, ok)
	}
}

func TestDispatcher_PlaybackViewForSnapshot_DoesNotAcquireFreshSnapshot(t *testing.T) {
	t.Parallel()
	status := &countingStatusViewer{}
	provider := &fakeProvider{name: "plex", bannerOK: true}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)
	d := NewDispatcher(status, reg)

	_, _ = d.PlaybackViewForSnapshot(context.Background(), core.StatusHomeView{
		State: core.StatePlaying, Source: "plex", AdapterRef: "plex:abc", Generation: 7,
	})
	if status.calls != 0 {
		t.Fatalf("PlaybackViewForSnapshot called StatusHomeView %d times, want 0", status.calls)
	}
}
```

- [ ] **Step 6: Run the new policy tests**

Run: `go test ./internal/playback/ -run 'TestDispatcher_PlaybackProviderForSnapshot|TestDispatcher_PlaybackViewForSnapshot'`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/playback/dispatcher.go internal/playback/dispatcher_test.go
git commit -m "$(cat <<'EOF'
feat(playback): Dispatcher with PlaybackView + source-first lookup

Phase 1 / Spec 3 task 2. New internal/playback package houses the
snapshot + provider-lookup dance that was inline in /ui/playback.go.
Dispatcher composes a StatusViewer (satisfied by *core.Manager) with
adapters.Registry and exposes:

- PlaybackView(ctx) — snapshots internally, looks up the owning
  provider, returns its banner view (or false).
- PlaybackViewForSnapshot(ctx, snap) — variant for callers that
  already have a snapshot (chassis refresher), avoiding a redundant
  Manager.mu acquisition.

The private playbackProviderForSnapshot helper preserves /ui's
source-first policy verbatim, with the legacy adapter-ref-prefix
scan as the no-Source fallback. /ui migration follows in later tasks.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Dispatcher `HandlePlaybackAction` + clamp + legacy-message normalization

**Files:**
- Modify: `internal/playback/dispatcher.go`
- Modify: `internal/playback/dispatcher_test.go`

- [ ] **Step 1: Write failing tests for `HandlePlaybackAction`**

Append to `internal/playback/dispatcher_test.go`:

```go
func TestDispatcher_HandlePlaybackAction_StaleGenerationReturnsSentinel(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{name: "plex", bannerOK: true}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)
	d := NewDispatcher(&fakeStatusViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Source: "plex", AdapterRef: "plex:abc", Generation: 7,
	}}, reg)

	_, err := d.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{
		Action: "pause", AdapterRef: "plex:abc", Generation: 99, // stale
	})
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("err = %v, want ErrActiveSessionChanged", err)
	}
}

func TestDispatcher_HandlePlaybackAction_UnsupportedAdapterReturnsSentinel(t *testing.T) {
	t.Parallel()
	// Provider registered but does NOT implement PlaybackControlProvider —
	// implementer simulates this via a non-provider type in the registry,
	// OR by leaving Source empty so the legacy scan finds nothing.
	reg := adapters.NewRegistry()
	d := NewDispatcher(&fakeStatusViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Source: "plex", AdapterRef: "plex:abc", Generation: 7,
	}}, reg)

	_, err := d.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{
		Action: "pause", AdapterRef: "plex:abc", Generation: 7,
	})
	if !errors.Is(err, adapters.ErrPlaybackActionUnsupported) {
		t.Fatalf("err = %v, want ErrPlaybackActionUnsupported", err)
	}
}

func TestDispatcher_HandlePlaybackAction_DispatchesToProvider(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{
		name:         "plex",
		bannerOK:     true,
		actionResult: adapters.PlaybackActionResult{Message: "Paused"},
	}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)
	d := NewDispatcher(&fakeStatusViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Source: "plex", AdapterRef: "plex:abc", Generation: 7,
	}}, reg)

	result, err := d.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{
		Action: "pause", AdapterRef: "plex:abc", Generation: 7,
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if result.Message != "Paused" {
		t.Errorf("result.Message = %q, want %q", result.Message, "Paused")
	}
	if provider.lastReq.Action != "pause" {
		t.Errorf("provider received action %q, want pause", provider.lastReq.Action)
	}
}

func TestDispatcher_HandlePlaybackAction_ClampsNegativeOffsetToZero(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{name: "plex", bannerOK: true}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)
	d := NewDispatcher(&fakeStatusViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Source: "plex", AdapterRef: "plex:abc", Generation: 7,
	}}, reg)

	_, err := d.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{
		Action: adapters.PlaybackActionSeek, AdapterRef: "plex:abc", Generation: 7, OffsetMS: -500,
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if provider.lastReq.OffsetMS != 0 {
		t.Errorf("clamped OffsetMS = %d, want 0", provider.lastReq.OffsetMS)
	}
}

func TestDispatcher_HandlePlaybackAction_NormalizesLegacyStaleMessage(t *testing.T) {
	t.Parallel()
	// A legacy provider that hasn't migrated yet returns the plain
	// string "active session changed" without wrapping the sentinel.
	// The dispatcher normalizes this so external callers get the
	// typed sentinel regardless.
	provider := &fakeProvider{
		name:      "plex",
		bannerOK:  true,
		actionErr: fmt.Errorf("active session changed"),
	}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)
	d := NewDispatcher(&fakeStatusViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Source: "plex", AdapterRef: "plex:abc", Generation: 7,
	}}, reg)

	_, err := d.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{
		Action: "pause", AdapterRef: "plex:abc", Generation: 7,
	})
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("err = %v, want normalized to ErrActiveSessionChanged", err)
	}
}
```

Add `"errors"` and `"fmt"` to the test file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/playback/ -run TestDispatcher_HandlePlaybackAction`
Expected: FAIL — `HandlePlaybackAction` not yet implemented.

- [ ] **Step 3: Implement `HandlePlaybackAction` with clamp + sentinel normalization**

Append to `internal/playback/dispatcher.go` and add `"errors"` to its import block:

```go
// HandlePlaybackAction validates the caller's adapter_ref + generation
// against the current snapshot, looks up the owning provider, and
// dispatches the action.
//
// Returns:
//   - ErrActiveSessionChanged when generation/adapter_ref mismatch.
//     Callers map to HTTP 409 (chassis) or inline banner (/ui).
//   - ErrPlaybackActionUnsupported when the active adapter has no
//     PlaybackControlProvider. Callers map to HTTP 422 (chassis) or
//     inline banner (/ui).
//   - Provider-emitted errors (which providers wrap with sentinels per
//     their migration) bubble up unchanged.
//
// Negative OffsetMS is clamped to 0 before provider dispatch so both
// /ui and chassis observe the same behaviour.
//
// Locking: snapshot acquisition happens via StatusViewer.StatusHomeView
// (which takes Manager.mu briefly). The provider call runs after the
// snapshot is released — the dispatcher never holds Manager.mu across
// the provider call.
func (d *Dispatcher) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	if req.OffsetMS < 0 {
		req.OffsetMS = 0
	}
	snap := d.status.StatusHomeView()
	if snap.AdapterRef != req.AdapterRef || snap.Generation != req.Generation {
		return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
	}
	provider, ok := d.playbackProviderForSnapshot(snap)
	if !ok {
		return adapters.PlaybackActionResult{}, adapters.ErrPlaybackActionUnsupported
	}
	result, err := provider.HandlePlaybackAction(ctx, req)
	if err != nil {
		// Compatibility shim: legacy providers that emit the plain
		// string without wrapping the sentinel get normalized so the
		// caller-side errors.Is checks still fire correctly. Drop this
		// once every provider is verified to wrap the sentinel.
		if !errors.Is(err, adapters.ErrActiveSessionChanged) && err.Error() == adapters.ErrActiveSessionChangedMessage {
			return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
		}
	}
	return result, err
}
```

Task 2 deliberately omitted `"errors"` so its compile pass stays green; Task 3 adds it when `HandlePlaybackAction` starts using `errors.Is`.

- [ ] **Step 4: Run all dispatcher tests**

Run: `go test ./internal/playback/`
Expected: PASS — all PlaybackView + HandlePlaybackAction tests.

- [ ] **Step 5: Run race + full repo**

Run: `go test -race ./internal/playback/ && go test ./...`
Expected: PASS. (`-race` may be unavailable on Windows without CGO; CI exercises it.)

- [ ] **Step 6: Commit**

```bash
git add internal/playback/dispatcher.go internal/playback/dispatcher_test.go
git commit -m "$(cat <<'EOF'
feat(playback): Dispatcher.HandlePlaybackAction with clamp + sentinel normalization

Phase 1 / Spec 3 task 3. Write surface for the dispatcher:
- Validates adapter_ref + generation against StatusHomeView snapshot;
  returns ErrActiveSessionChanged on mismatch.
- Maps no-provider to ErrPlaybackActionUnsupported.
- Clamps negative OffsetMS to 0 so both /ui and chassis observe
  identical behaviour without per-handler duplication.
- Legacy-message normalizer: a provider that emits the plain
  "active session changed" string without wrapping the sentinel is
  still detected as ErrActiveSessionChanged downstream. Compatibility
  shim — providers are expected to migrate in task 1.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `ui.Config` gains `PlaybackService` + default Dispatcher

**Files:**
- Modify: `internal/ui/server.go`

- [ ] **Step 1: Write a failing test asserting the Config field exists and defaults work**

Append to `internal/ui/server_test.go`:

```go
func TestUIConfig_PlaybackDefaultBuildsDispatcher(t *testing.T) {
	t.Parallel()
	// When Playback is nil, New constructs a Dispatcher using the
	// existing StatusViewer + Registry. Existing tests that supply
	// only those two fields keep working.
	cfg := minimalUIConfigForTest() // implementer: use whatever the existing test helper is
	cfg.Playback = nil               // explicit
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.playback == nil {
		t.Fatalf("Server.playback should be non-nil after New defaults the field")
	}
}
```

Note: `minimalUIConfigForTest()` is illustrative — use the existing test fixture pattern in `server_test.go`. The new field is `cfg.Playback`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/ui/ -run TestUIConfig_PlaybackDefaultBuildsDispatcher`
Expected: FAIL — `Config.Playback` undefined.

- [ ] **Step 3: Add the `PlaybackService` interface + Config field**

In `internal/ui/server.go`, declare a package-local interface and extend `Config` + `Server`. Add `"github.com/idio-sync/MiSTer_GroovyRelay/internal/playback"` to imports.

```go
// PlaybackService is the narrow interface the /ui playback handlers
// depend on. *playback.Dispatcher satisfies it structurally. Optional
// on Config: when nil, New constructs a Dispatcher from StatusViewer +
// Registry so existing test fixtures don't need updating.
type PlaybackService interface {
	PlaybackView(ctx context.Context) (adapters.PlaybackBannerAdapterView, bool)
	PlaybackViewForSnapshot(ctx context.Context, snap core.StatusHomeView) (adapters.PlaybackBannerAdapterView, bool)
	HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error)
}

// Config gains Playback alongside existing fields.
type Config struct {
	// ... existing fields stay ...
	Playback PlaybackService
}

// Server stores it.
type Server struct {
	// ... existing fields stay ...
	playback PlaybackService
}

// New defaults Playback when nil — but only when StatusViewer is also
// non-nil. Existing tests that construct ui.Server with StatusViewer=nil
// (status-home disabled mode) would crash if the default Dispatcher
// later called nil.StatusHomeView(); leave Playback nil in that case
// so handlePlaybackMutation / buildPlaybackBannerData's existing
// nil-guards continue to short-circuit.
func New(cfg Config) (*Server, error) {
	// ... existing validation ...
	if cfg.Playback == nil && cfg.StatusViewer != nil {
		cfg.Playback = playback.NewDispatcher(cfg.StatusViewer, cfg.Registry)
	}
	s := &Server{
		// ... existing fields ...
		playback: cfg.Playback,
	}
	// ... existing wiring ...
	return s, nil
}
```

**Important:** the call sites that consume `s.playback` (Tasks 5 + 6) must defend against nil — they already short-circuit when `s.cfg.StatusViewer == nil` (see existing `/ui/playback.go:265 / 291 / server.go:452` guards), so the new `s.playback == nil` branch is a no-op refactor: keep the existing guards, just swap the inline lookup for the dispatcher call inside the `StatusViewer != nil` branch.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/ui/ -run TestUIConfig_PlaybackDefaultBuildsDispatcher`
Expected: PASS.

- [ ] **Step 5: Run the full `/ui` test suite to confirm nothing broke**

Run: `go test ./internal/ui/...`
Expected: PASS — existing tests don't touch the new field and the default builder handles their fixtures.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/server.go internal/ui/server_test.go
git commit -m "$(cat <<'EOF'
feat(ui): Config gains PlaybackService field with default Dispatcher

Phase 1 / Spec 3 task 4. Adds a package-local PlaybackService interface
declaring the three playback.Dispatcher methods /ui consumes. Config
gains an optional Playback field; New defaults it to
playback.NewDispatcher(StatusViewer, Registry) when nil, so existing
test fixtures that only supply StatusViewer + Registry keep working
without modification.

Next tasks (5, 6) migrate the inline call sites in playback.go to
delegate to s.playback.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: `/ui` `handlePlaybackMutation` delegates to dispatcher

**Files:**
- Modify: `internal/ui/playback.go`

- [ ] **Step 1: Find the existing `handlePlaybackMutation` body**

Read `internal/ui/playback.go` around line 153-176 (`handlePlaybackMutation`). Current logic:

```go
func (s *Server) handlePlaybackMutation(w http.ResponseWriter, r *http.Request, req adapters.PlaybackActionRequest) {
	snap := s.currentPlaybackSnapshot()
	if snap.AdapterRef != req.AdapterRef || snap.Generation != req.Generation {
		s.renderPlaybackMessage(w, r, "err", adapters.ErrActiveSessionChangedMessage, false, "")
		return
	}
	provider, ok := s.playbackProviderForSnapshot(snap)
	if !ok {
		s.renderPlaybackMessage(w, r, "err", "active adapter does not expose playback controls", false, "")
		return
	}
	result, err := provider.HandlePlaybackAction(r.Context(), req)
	if err != nil {
		s.renderPlaybackMessage(w, r, "err", err.Error(), false, "")
		return
	}
	s.renderPlaybackMessage(w, r, "ok", result.Message, false, "")
}
```

- [ ] **Step 2: Replace the body to delegate via `s.playback`**

```go
func (s *Server) handlePlaybackMutation(w http.ResponseWriter, r *http.Request, req adapters.PlaybackActionRequest) {
	if s.playback == nil {
		// Preserve the existing nil-StatusViewer/status-disabled behavior:
		// the old inline path compared the request to an idle zero snapshot
		// and rendered the stale-session message instead of panicking.
		s.renderPlaybackMessage(w, r, "err", adapters.ErrActiveSessionChangedMessage, false, "")
		return
	}
	result, err := s.playback.HandlePlaybackAction(r.Context(), req)
	if err != nil {
		s.renderPlaybackMessage(w, r, "err", err.Error(), false, "")
		return
	}
	s.renderPlaybackMessage(w, r, "ok", result.Message, false, "")
}
```

The dispatcher already does the stale-generation check and returns `ErrActiveSessionChanged` (whose `.Error()` is the existing message text), and already maps no-provider to `ErrPlaybackActionUnsupported` (whose `.Error()` is the existing "active adapter does not expose playback controls" string). The inline pre-check is redundant; `renderPlaybackMessage` displays the same string either way. The explicit `s.playback == nil` branch is required because Task 4 intentionally leaves `s.playback` nil when `StatusViewer` is nil, preserving the existing status-disabled UI mode.

- [ ] **Step 3: Run the existing `/ui` playback tests**

Run: `go test ./internal/ui/ -run TestPlayback`
Expected: PASS — all existing playback tests stay green because the response shape is byte-identical.

- [ ] **Step 4: Run the full repo tests**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/playback.go
git commit -m "$(cat <<'EOF'
refactor(ui): handlePlaybackMutation delegates to playback.Dispatcher

Phase 1 / Spec 3 task 5. /ui's inline snapshot + provider-lookup +
dispatch dance moves to playback.Dispatcher (introduced in tasks 2-3).
HTTP response shape is unchanged: the dispatcher's typed sentinels
serialize to the same Error() strings as the prior inline literals
("active session changed", "active adapter does not expose playback
controls"), and provider errors continue to bubble through to
renderPlaybackMessage exactly as before.

Existing /ui playback tests stay green without modification.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: `/ui` `buildPlaybackBannerData` delegates; delete inline lookup

**Files:**
- Modify: `internal/ui/playback.go`

- [ ] **Step 1: Find the inline provider lookup inside `buildPlaybackBannerData`**

Read `internal/ui/playback.go` around line 335-354. Current logic mid-function:

```go
if provider, ok := s.playbackProviderForSnapshot(snap); ok {
	if providerView, owns := provider.PlaybackBanner(ctx, snap); owns {
		if providerView.Title != "" {
			data.Title = providerView.Title
		}
		// ... rest of view-population code ...
	}
}
```

- [ ] **Step 2: Replace with a dispatcher call**

```go
if s.playback != nil {
	if providerView, ok := s.playback.PlaybackViewForSnapshot(ctx, view); ok {
		if providerView.Title != "" {
			data.Title = providerView.Title
		}
		if providerView.Subtitle != "" {
			data.Subtitle = providerView.Subtitle
		}
		if providerView.SourceDisplay != "" {
			data.SourceDisplay = providerView.SourceDisplay
		}
		data.Actions = providerView.Actions
		data.Seek = providerView.Seek
		if providerView.Seek != nil {
			data.HasTimeline = data.HasTimeline || providerView.Seek.DurationMS > 0
			data.PositionMS = providerView.Seek.OffsetMS
			data.DurationMS = providerView.Seek.DurationMS
		}
	}
}
```

`buildPlaybackBannerData` reads `view := s.cfg.StatusViewer.StatusHomeView()` upstream of the provider lookup (around line 290-310). Reuse that `view` variable directly. The `s.playback != nil` guard preserves the nil-`StatusViewer` / status-disabled mode from Task 4: when no playback service exists, the banner keeps its core-derived read-only defaults and the later `ReadOnly` branch behaves as before.

If for any reason the code path no longer has a `core.StatusHomeView` in hand at that call site (verify by reading the surrounding lines), fall back to `s.playback.PlaybackView(ctx)` — that variant snapshots internally for the extra cost of one `Manager.mu` acquisition per banner render (~µs; acceptable since `/ui` polls at 2s, not 4Hz).

Delete the now-unused local `snap := adapters.PlaybackBannerSnapshot{...}` block from `buildPlaybackBannerData`; after this replacement the function should compile without an unused `snap` variable.

- [ ] **Step 3: Delete the inline `playbackProviderForSnapshot` function from `playback.go`**

It's now duplicated in `internal/playback`. Search the rest of `/ui` for any remaining callers — there should be none after Tasks 5 + 6. If any remain, migrate them too.

- [ ] **Step 4: Delete the now-orphaned `currentPlaybackSnapshot` helper**

`internal/ui/playback.go:263` defines `func (s *Server) currentPlaybackSnapshot() adapters.PlaybackBannerSnapshot`. Its only consumer was `handlePlaybackMutation` (now refactored in Task 5 to call the dispatcher directly). After Task 5 + Step 3 above, this helper has zero callers in production code.

Verify with `rg -n "currentPlaybackSnapshot" internal/ui` — expected: zero matches outside the function definition itself (and possibly stale `_test.go` references which should also be cleaned up if they were calling it). Delete the function.

If the search surfaces unexpected callers, audit them: most likely they are vestigial test helpers that also migrate to the dispatcher or get removed. Don't leave dead code in `/ui` post-refactor.

- [ ] **Step 5: Run all `/ui` tests**

Run: `go test ./internal/ui/...`
Expected: PASS — banner HTML byte-identical to before.

- [ ] **Step 6: Run full repo**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/playback.go
git commit -m "$(cat <<'EOF'
refactor(ui): buildPlaybackBannerData via Dispatcher; delete inline lookup

Phase 1 / Spec 3 task 6. The second and final call site of the inline
playbackProviderForSnapshot migrates to s.playback.PlaybackView(ctx)
(or PlaybackViewForSnapshot when a core snapshot is in hand). The
inline helper is deleted. currentPlaybackSnapshot is also deleted —
it was only used by handlePlaybackMutation, which Task 5 already
refactored to delegate to the dispatcher.

Banner-specific data composition (QuickCastTabs, OutputVolume,
ReadOnly, PollTrigger, Title/Subtitle/SourceDisplay overrides) stays
in /ui; only the provider lookup + adapter-view fetch moved.

Existing /ui banner tests stay green; HTML response is byte-identical.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Chassis `TransportViewer` + `TransportController` interfaces + Config fields

**Files:**
- Modify: `internal/chassis/session.go`
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write a failing test asserting `*playback.Dispatcher` satisfies the new interfaces**

Append to `internal/chassis/chassis_test.go`:

```go
func TestTransportViewer_DispatcherSatisfiesInterface(t *testing.T) {
	t.Parallel()
	// Compile-time + runtime assertion that *playback.Dispatcher
	// satisfies the chassis TransportViewer and TransportController
	// interfaces structurally. Catches regressions if the dispatcher's
	// signatures change.
	var _ TransportViewer = (*playback.Dispatcher)(nil)
	var _ TransportController = (*playback.Dispatcher)(nil)

	cfg := nonZeroConfig()
	d := testTransportDispatcher(cfg) // helper defined below
	cfg.TransportViewer = d
	cfg.TransportController = d
	if cfg.TransportViewer == nil || cfg.TransportController == nil {
		t.Fatal("transport fields should be assignable from *playback.Dispatcher")
	}
}
```

And a small test-file-local helper near `nonZeroConfig()` (free function, not a method on `Config` — keeps it out of the production API surface):

```go
func testTransportDispatcher(cfg Config) *playback.Dispatcher {
	return playback.NewDispatcher(cfg.Manager, cfg.Registry)
}
```

Update the test above to call `testTransportDispatcher(cfg)` instead of `cfg.transportDispatcher()`.

Add `"github.com/idio-sync/MiSTer_GroovyRelay/internal/playback"` to the test file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestTransportViewer_DispatcherSatisfiesInterface`
Expected: FAIL — `TransportViewer`, `TransportController`, `Config.TransportViewer`, `Config.TransportController` undefined.

- [ ] **Step 3: Declare the interfaces in `session.go`**

Append to `internal/chassis/session.go`. Extend the imports.

```go
import (
	"context"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// TransportViewer is the narrow read-only view of bridge playback state.
// *playback.Dispatcher satisfies this structurally via its
// PlaybackViewForSnapshot method. Tests inject fakes; production wires
// the shared dispatcher built once in main.go.
type TransportViewer interface {
	PlaybackViewForSnapshot(ctx context.Context, snap core.StatusHomeView) (adapters.PlaybackBannerAdapterView, bool)
}

// TransportController is the narrow write surface for playback actions.
// *playback.Dispatcher satisfies this structurally via its
// HandlePlaybackAction method. /ui/playback's handler delegates to the
// same dispatcher, so the chassis and /ui dispatch via one canonical
// path.
type TransportController interface {
	HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error)
}
```

- [ ] **Step 4: Add `Config.TransportViewer` + `Config.TransportController` fields**

Edit `internal/chassis/server.go` `Config` struct. Add the two fields after the existing `Session` field; mirror Spec 4's `VisualizerViewer`/`VisualizerSaver` pattern.

```go
// Config gains TransportViewer + TransportController. Both optional:
// when nil, the chassis renders idle-only transport controls with
// core-derived state when Session is non-nil. *playback.Dispatcher
// satisfies both interfaces structurally; main.go wires that.
type Config struct {
	// ... existing fields stay ...
	Session SessionViewer

	VisualizerViewer VisualizerViewer
	VisualizerSaver  VisualizerSaver

	TransportViewer     TransportViewer
	TransportController TransportController
}
```

And mirror on `Server`:

```go
type Server struct {
	// ... existing fields stay ...
	visualizerViewer VisualizerViewer
	visualizerSaver  VisualizerSaver

	transportViewer     TransportViewer
	transportController TransportController
}
```

Wire them in `New`:

```go
s := &Server{
	// ... existing initializers ...
	visualizerViewer:    cfg.VisualizerViewer,
	visualizerSaver:     cfg.VisualizerSaver,
	transportViewer:     cfg.TransportViewer,
	transportController: cfg.TransportController,
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/chassis/ -run TestTransportViewer_DispatcherSatisfiesInterface`
Expected: PASS.

- [ ] **Step 6: Run full chassis tests**

Run: `go test ./internal/chassis/...`
Expected: PASS — existing tests don't touch the new fields.

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/session.go internal/chassis/server.go internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): TransportViewer + TransportController interfaces

Phase 1 / Spec 3 task 7. Two new narrow interfaces in chassis/session.go
sibling to Spec 2's SessionViewer:

- TransportViewer.PlaybackViewForSnapshot(ctx, snap) — read; the
  snapshot-already-acquired variant preserves Spec 2's one-StatusHomeView-
  per-tick invariant when the chassis refresher passes its snapshot
  through.
- TransportController.HandlePlaybackAction(ctx, req) — write; same
  signature /ui consumes, so a single *playback.Dispatcher satisfies
  both interfaces and both consumers' POST handlers dispatch through it.

Config gains TransportViewer + TransportController fields (optional;
nil falls back to read-only idle controls). Server stores them. main.go
wiring happens in task 16; downstream tasks (8-15) populate the data
path through them.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Migrate `TransportData` struct (rename `PlayState` → `State`, add fields, update idle placeholders)

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test for the new struct shape and idle placeholders**

Append to `internal/chassis/chassis_test.go`:

```go
func TestIdleSnapshot_TransportDataMatchesNewIdleShape(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()
	got := idleSnapshot(cfg, fixedNow)

	want := TransportData{
		State:           "stopped",
		SeekFillPercent: 0,
		ElapsedTime:     "",
		TotalTime:       "",
		PercentPlayed:   "",
		OffsetMS:        0,
		DurationMS:      0,
		ActionsEnabled:  ActionsEnabled{},
		AdapterRef:      "",
		Generation:      0,
	}
	if !reflect.DeepEqual(got.Transport, want) {
		t.Errorf("idleSnapshot Transport mismatch:\n got: %+v\nwant: %+v", got.Transport, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestIdleSnapshot_TransportDataMatchesNewIdleShape`
Expected: FAIL — `State` field doesn't exist (still `PlayState`); new fields undefined.

- [ ] **Step 3: Update `TransportData` and `ActionsEnabled` in `internal/chassis/data.go`**

Find the existing `TransportData` struct and replace it:

```go
type TransportData struct {
	State           string // "playing" | "paused" | "stopped"
	SeekFillPercent int    // 0..100
	ElapsedTime     string // "04:23"
	TotalTime       string // "09:56" or "--:--"
	PercentPlayed   string // "44%"
	OffsetMS        int    // raw position for seek math; 0 on idle/unknown
	DurationMS      int    // raw duration for seek math; 0 on unknown
	ActionsEnabled  ActionsEnabled
	AdapterRef      string // empty on idle
	Generation      uint64 // 0 on idle
}

type ActionsEnabled struct {
	Previous    bool
	Next        bool
	PauseResume bool
	Stop        bool
	Replay      bool
	Seek        bool
}
```

- [ ] **Step 4: Update `idleSnapshot()` to populate the new shape**

Find the existing `idleSnapshot()` body. The current code probably has:

```go
Transport: TransportData{
	PlayState:       "stopped",
	ElapsedTime:     "--:--",
	TotalTime:       "--:--",
	PercentPlayed:   "---",
	SeekFillPercent: 0,
}
```

Replace with:

```go
Transport: TransportData{
	State:           "stopped",
	SeekFillPercent: 0,
	ElapsedTime:     "",
	TotalTime:       "",
	PercentPlayed:   "",
	OffsetMS:        0,
	DurationMS:      0,
	ActionsEnabled:  ActionsEnabled{},
	AdapterRef:      "",
	Generation:      0,
}
```

- [ ] **Step 5: Update existing chassis_test.go fixtures**

Search `chassis_test.go` for `PlayState` and `--:--` and `"---"`. Update each assertion to the new field name and empty-string placeholders.

Common patterns to fix:

```go
// Before:
if got.Transport.PlayState != "stopped" { ... }
// After:
if got.Transport.State != "stopped" { ... }

// Before:
if got.Transport.ElapsedTime != "--:--" { ... }
// After:
if got.Transport.ElapsedTime != "" { ... }
```

Use `rg` first to find all occurrences:

```bash
rg -n 'PlayState|--:--|"---"' internal/chassis/chassis_test.go
```

Update each match.

- [ ] **Step 6: Update existing template-content assertions in `chassis_test.go`**

The Phase 0 template (`transport.html`) had `{{.PlayState}}` references in places (e.g. the title attribute or aria-label might reference state). Run:

```bash
rg -n 'PlayState|--:--|"---"' internal/chassis/templates/transport.html
```

If the template references `{{.PlayState}}` anywhere, update to `{{.State}}`. (This will be re-done more comprehensively in Task 13's template rewrite; just keep it building cleanly here.)

- [ ] **Step 7: Run the test to verify it passes**

Run: `go test ./internal/chassis/ -run TestIdleSnapshot_TransportDataMatchesNewIdleShape`
Expected: PASS.

- [ ] **Step 8: Run all chassis tests**

Run: `go test ./internal/chassis/...`
Expected: PASS — all existing tests still green after the field rename + placeholder change.

- [ ] **Step 9: Commit**

```bash
git add internal/chassis/data.go internal/chassis/chassis_test.go internal/chassis/templates/transport.html
git commit -m "$(cat <<'EOF'
feat(chassis): expand TransportData for Spec 3 wire format

Phase 1 / Spec 3 task 8. TransportData migrates to the Spec 3 shape:
- PlayState renamed to State (matches the SSE wire-format field name).
- Adds OffsetMS, DurationMS, ActionsEnabled, AdapterRef, Generation.
- Idle placeholders ("--:--", "---") become empty strings; the
  segmented-display ghost spans already render the all-segments-lit
  background when the value is empty, so the visual is preserved.

Existing Phase-0 chassis_test.go assertions update to the new field
names and empty-string placeholders. Subsequent tasks (9-11) populate
the new fields from live session + adapter view data.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: `transportEnvelope` + `transportEnvelopeFrom` + `transportChanged`

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

- [ ] **Step 1: Write failing tests for the envelope + flattener + diff**

Append to `internal/chassis/events_test.go`:

```go
func TestTransportEnvelopeFrom_FlattensTransportData(t *testing.T) {
	t.Parallel()
	// Mixed true/false ActionsEnabled values are deliberate: a uniform
	// all-true fixture would let common copy-paste swaps slip through.
	// This catches adjacent swaps where the values differ; the
	// transportChanged table below locks every bool field independently.
	td := TransportData{
		State:           "playing",
		SeekFillPercent: 44,
		ElapsedTime:     "04:23",
		TotalTime:       "09:56",
		PercentPlayed:   "44%",
		OffsetMS:        263000,
		DurationMS:      596000,
		ActionsEnabled: ActionsEnabled{
			Previous: true, Next: false, PauseResume: true,
			Stop: false, Replay: true, Seek: false,
		},
		AdapterRef: "plex:abc123",
		Generation: 42,
	}
	env := transportEnvelopeFrom(td)
	if env.State != "playing" || env.SeekFillPercent != 44 || env.OffsetMS != 263000 ||
		env.ElapsedTime != "04:23" || env.TotalTime != "09:56" || env.PercentPlayed != "44%" ||
		env.DurationMS != 596000 {
		t.Errorf("flatten lost scalars: %+v", env)
	}
	// Check all six ActionsEnabled fields independently so a copy-paste
	// swap (e.g. Previous ↔ Next) in transportEnvelopeFrom is caught.
	wantAE := actionsEnabledE{
		Previous: true, Next: false, PauseResume: true,
		Stop: false, Replay: true, Seek: false,
	}
	if env.ActionsEnabled != wantAE {
		t.Errorf("flatten lost ActionsEnabled bools: got %+v, want %+v", env.ActionsEnabled, wantAE)
	}
	if env.AdapterRef != "plex:abc123" || env.Generation != 42 {
		t.Errorf("flatten lost ref/generation: %+v", env)
	}
}

func TestTransportEnvelope_JSONFormatCamelCase(t *testing.T) {
	t.Parallel()
	env := transportEnvelope{
		State: "playing", SeekFillPercent: 44, ElapsedTime: "04:23", TotalTime: "09:56",
		PercentPlayed: "44%", OffsetMS: 263000, DurationMS: 596000,
		ActionsEnabled: actionsEnabledE{Previous: true, Next: true, PauseResume: true, Stop: true, Replay: true, Seek: true},
		AdapterRef: "plex:abc", Generation: 42,
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		`"state":"playing"`,
		`"seekFillPercent":44`,
		`"elapsedTime":"04:23"`,
		`"totalTime":"09:56"`,
		`"percentPlayed":"44%"`,
		`"offsetMs":263000`,
		`"durationMs":596000`,
		`"actionsEnabled":{`,
		`"previous":true`,
		`"next":true`,
		`"pauseResume":true`,
		`"stop":true`,
		`"replay":true`,
		`"seek":true`,
		`"adapterRef":"plex:abc"`,
		`"generation":42`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("envelope missing %q in JSON:\n%s", want, got)
		}
	}
}

func TestTransportEnvelope_StateValueSpaceDistinctFromBodyClass(t *testing.T) {
	t.Parallel()
	// transport.state uses playing|paused|stopped, not idle|live (the
	// state event's value space). This test locks the contract in.
	allowed := map[string]bool{"playing": true, "paused": true, "stopped": true}
	denied := []string{"idle", "live"}
	for _, v := range denied {
		if allowed[v] {
			t.Errorf("%q should NOT be a transport state value", v)
		}
	}
}

func TestTransportChanged_DetectsEveryFieldDelta(t *testing.T) {
	t.Parallel()
	base := TransportData{
		State: "playing", SeekFillPercent: 44, ElapsedTime: "04:23", TotalTime: "09:56",
		PercentPlayed: "44%", OffsetMS: 263000, DurationMS: 596000,
		ActionsEnabled: ActionsEnabled{Previous: true, Next: true, PauseResume: true, Stop: true, Replay: true, Seek: true},
		AdapterRef: "plex:abc", Generation: 42,
	}

	type tc struct {
		name   string
		mutate func(*TransportData)
	}
	tests := []tc{
		{"State", func(t *TransportData) { t.State = "paused" }},
		{"SeekFillPercent", func(t *TransportData) { t.SeekFillPercent = 45 }},
		{"ElapsedTime", func(t *TransportData) { t.ElapsedTime = "04:24" }},
		{"TotalTime", func(t *TransportData) { t.TotalTime = "09:57" }},
		{"PercentPlayed", func(t *TransportData) { t.PercentPlayed = "45%" }},
		{"OffsetMS", func(t *TransportData) { t.OffsetMS = 264000 }},
		{"DurationMS", func(t *TransportData) { t.DurationMS = 597000 }},
		{"AdapterRef", func(t *TransportData) { t.AdapterRef = "plex:def" }},
		{"Generation", func(t *TransportData) { t.Generation = 43 }},
		{"ActionsEnabled.Previous", func(t *TransportData) { t.ActionsEnabled.Previous = false }},
		{"ActionsEnabled.Next", func(t *TransportData) { t.ActionsEnabled.Next = false }},
		{"ActionsEnabled.PauseResume", func(t *TransportData) { t.ActionsEnabled.PauseResume = false }},
		{"ActionsEnabled.Stop", func(t *TransportData) { t.ActionsEnabled.Stop = false }},
		{"ActionsEnabled.Replay", func(t *TransportData) { t.ActionsEnabled.Replay = false }},
		{"ActionsEnabled.Seek", func(t *TransportData) { t.ActionsEnabled.Seek = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := base
			tt.mutate(&next)
			if !transportChanged(base, next) {
				t.Errorf("transportChanged should return true when %s changes", tt.name)
			}
		})
	}
}

func TestTransportChanged_IdenticalReturnsFalse(t *testing.T) {
	t.Parallel()
	td := TransportData{State: "playing"}
	if transportChanged(td, td) {
		t.Errorf("transportChanged should return false for identical inputs")
	}
}
```

Add `"encoding/json"` and `"strings"` to test imports if not already present.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run 'TestTransportEnvelope|TestTransportChanged'`
Expected: FAIL — types undefined.

- [ ] **Step 3: Add the envelope + flattener + diff to `events.go`**

Append to `internal/chassis/events.go`:

```go
// transportEnvelope is the JSON payload for the `transport` SSE event.
// Struct tags use camelCase per the Spec 3 wire-format contract.
type transportEnvelope struct {
	State           string          `json:"state"`
	SeekFillPercent int             `json:"seekFillPercent"`
	ElapsedTime     string          `json:"elapsedTime"`
	TotalTime       string          `json:"totalTime"`
	PercentPlayed   string          `json:"percentPlayed"`
	OffsetMS        int             `json:"offsetMs"`
	DurationMS      int             `json:"durationMs"`
	ActionsEnabled  actionsEnabledE `json:"actionsEnabled"`
	AdapterRef      string          `json:"adapterRef"`
	Generation      uint64          `json:"generation"`
}

// actionsEnabledE is the camelCase-tagged wire-format struct. Separate
// from the PascalCase ActionsEnabled (used internally) so the wire and
// data types stay decoupled — same precedent as Spec 2's vfdEnvelope.
type actionsEnabledE struct {
	Previous    bool `json:"previous"`
	Next        bool `json:"next"`
	PauseResume bool `json:"pauseResume"`
	Stop        bool `json:"stop"`
	Replay      bool `json:"replay"`
	Seek        bool `json:"seek"`
}

func transportEnvelopeFrom(t TransportData) transportEnvelope {
	return transportEnvelope{
		State:           t.State,
		SeekFillPercent: t.SeekFillPercent,
		ElapsedTime:     t.ElapsedTime,
		TotalTime:       t.TotalTime,
		PercentPlayed:   t.PercentPlayed,
		OffsetMS:        t.OffsetMS,
		DurationMS:      t.DurationMS,
		ActionsEnabled: actionsEnabledE{
			Previous: t.ActionsEnabled.Previous, Next: t.ActionsEnabled.Next,
			PauseResume: t.ActionsEnabled.PauseResume, Stop: t.ActionsEnabled.Stop,
			Replay: t.ActionsEnabled.Replay, Seek: t.ActionsEnabled.Seek,
		},
		AdapterRef: t.AdapterRef,
		Generation: t.Generation,
	}
}

// transportChanged returns true if any wire-format field differs.
// Performs ten field-level comparisons (nine scalars + one
// ActionsEnabled struct equality, which Go's == handles directly
// because all six bool fields are comparable). End-to-end the
// wire-format surface has 15 testable deltas (nine scalars + six
// ActionsEnabled bools).
func transportChanged(a, b TransportData) bool {
	return a.State != b.State ||
		a.SeekFillPercent != b.SeekFillPercent ||
		a.ElapsedTime != b.ElapsedTime ||
		a.TotalTime != b.TotalTime ||
		a.PercentPlayed != b.PercentPlayed ||
		a.OffsetMS != b.OffsetMS ||
		a.DurationMS != b.DurationMS ||
		a.AdapterRef != b.AdapterRef ||
		a.Generation != b.Generation ||
		a.ActionsEnabled != b.ActionsEnabled
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run 'TestTransportEnvelope|TestTransportChanged'`
Expected: PASS — all subtests.

- [ ] **Step 5: Run full chassis tests**

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): transport SSE envelope + flattener + diff helper

Phase 1 / Spec 3 task 9. Adds the wire-format types for the new
transport SSE event:
- transportEnvelope + actionsEnabledE: camelCase-tagged JSON shapes.
- transportEnvelopeFrom(TransportData) → transportEnvelope: flattener.
- transportChanged(a, b TransportData) bool: 10 field-level comparisons
  (9 scalars + ActionsEnabled struct equality), surfacing 15 testable
  deltas. Matches vfdChanged's explicit-compare precedent.

The wire format is locked in by 15 subtests asserting every field
mutation triggers the diff. Adapter view → TransportData mapping
lands in task 10; SSE emission lands in task 11.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: `snapshotFromSession` populates `Transport` via `TransportViewer`

**Files:**
- Modify: `internal/chassis/session.go`
- Modify: `internal/chassis/chassis_test.go`
- Modify: `internal/chassis/handler.go`
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/events.go`

- [ ] **Step 1: Write the failing test for live-state transport mapping**

Append to `internal/chassis/chassis_test.go`:

```go
// fakeTransportViewer is the test double for TransportViewer.
type fakeTransportViewer struct {
	view adapters.PlaybackBannerAdapterView
	ok   bool
}

func (f *fakeTransportViewer) PlaybackViewForSnapshot(ctx context.Context, snap core.StatusHomeView) (adapters.PlaybackBannerAdapterView, bool) {
	return f.view, f.ok
}

func TestSnapshotFromSession_PopulatesTransportFromAdapterView(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Live Title",
		Source:     "plex",
		Position:   4*time.Minute + 23*time.Second,
		Duration:   9*time.Minute + 56*time.Second,
		AdapterRef: "plex:abc",
		Generation: 42,
	}}
	tv := &fakeTransportViewer{
		ok: true,
		view: adapters.PlaybackBannerAdapterView{
			Actions: []adapters.PlaybackAction{
				{ID: "pause", Enabled: true},
				{ID: "stop", Enabled: true},
				{ID: "previous", Enabled: false},
				{ID: "next", Enabled: true},
				{ID: "replay", Enabled: false},
			},
			Seek: &adapters.PlaybackSeek{Enabled: true, OffsetMS: 263000, DurationMS: 596000},
		},
	}
	cfg.Session = sv
	cfg.TransportViewer = tv

	got := snapshotFromSession(cfg, sv, cfg.VisualizerViewer, tv, fixedNow)

	if got.Transport.State != "playing" {
		t.Errorf("State = %q, want playing", got.Transport.State)
	}
	if got.Transport.AdapterRef != "plex:abc" || got.Transport.Generation != 42 {
		t.Errorf("AdapterRef/Generation: got %q/%d, want plex:abc/42", got.Transport.AdapterRef, got.Transport.Generation)
	}
	if got.Transport.OffsetMS != 263000 || got.Transport.DurationMS != 596000 {
		t.Errorf("raw seek ms: got %d/%d, want 263000/596000", got.Transport.OffsetMS, got.Transport.DurationMS)
	}
	if got.Transport.SeekFillPercent != 44 || got.Transport.PercentPlayed != "44%" {
		t.Errorf("percent fields: got %d/%q, want 44/44%%", got.Transport.SeekFillPercent, got.Transport.PercentPlayed)
	}
	want := ActionsEnabled{PauseResume: true, Stop: true, Previous: false, Next: true, Replay: false, Seek: true}
	if got.Transport.ActionsEnabled != want {
		t.Errorf("ActionsEnabled mismatch:\n got: %+v\nwant: %+v", got.Transport.ActionsEnabled, want)
	}
}

func TestSnapshotFromSession_SeekPercentUsesFinalAdapterSeekValues(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Source: "plex",
		Position: 10 * time.Second, Duration: 100 * time.Second,
		AdapterRef: "plex:abc", Generation: 42,
	}}
	tv := &fakeTransportViewer{
		ok: true,
		view: adapters.PlaybackBannerAdapterView{
			Seek: &adapters.PlaybackSeek{Enabled: true, OffsetMS: 30000, DurationMS: 60000},
		},
	}

	got := snapshotFromSession(cfg, sv, cfg.VisualizerViewer, tv, fixedNow)

	if got.Transport.OffsetMS != 30000 || got.Transport.DurationMS != 60000 {
		t.Fatalf("raw seek ms: got %d/%d, want 30000/60000", got.Transport.OffsetMS, got.Transport.DurationMS)
	}
	if got.Transport.SeekFillPercent != 50 || got.Transport.PercentPlayed != "50%" {
		t.Errorf("percent fields should use final raw seek values; got %d/%q, want 50/50%%", got.Transport.SeekFillPercent, got.Transport.PercentPlayed)
	}
}

func TestSnapshotFromSession_NegativeAdapterSeekOffsetClampsBeforePercent(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Source: "plex",
		Position: 20 * time.Second, Duration: 100 * time.Second,
		AdapterRef: "plex:abc", Generation: 42,
	}}
	tv := &fakeTransportViewer{
		ok: true,
		view: adapters.PlaybackBannerAdapterView{
			Seek: &adapters.PlaybackSeek{Enabled: true, OffsetMS: -5000, DurationMS: 100000},
		},
	}

	got := snapshotFromSession(cfg, sv, cfg.VisualizerViewer, tv, fixedNow)

	if got.Transport.OffsetMS != 0 || got.Transport.DurationMS != 100000 {
		t.Fatalf("raw seek ms: got %d/%d, want 0/100000", got.Transport.OffsetMS, got.Transport.DurationMS)
	}
	if got.Transport.SeekFillPercent != 0 || got.Transport.PercentPlayed != "0%" {
		t.Errorf("percent fields should use clamped raw seek values; got %d/%q, want 0/0%%", got.Transport.SeekFillPercent, got.Transport.PercentPlayed)
	}
}

func TestSnapshotFromSession_NoProviderKeepsReadOnlyStateAndTime(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Title: "X", Source: "unknown",
		Position: 30 * time.Second, Duration: time.Minute,
		AdapterRef: "unknown:ref", Generation: 5,
	}}
	tv := &fakeTransportViewer{ok: false} // no provider

	got := snapshotFromSession(cfg, sv, cfg.VisualizerViewer, tv, fixedNow)

	if got.Transport.State != "playing" {
		t.Errorf("State should still be playing even without provider: %q", got.Transport.State)
	}
	if got.Transport.ElapsedTime == "" {
		t.Errorf("ElapsedTime should be derived from StatusHomeView when no provider; got empty")
	}
	want := ActionsEnabled{} // all false
	if got.Transport.ActionsEnabled != want {
		t.Errorf("ActionsEnabled should be all-false without provider: %+v", got.Transport.ActionsEnabled)
	}
}

func TestSnapshotFromSession_NilTransportViewerIdleKeepsTransportZero(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{State: core.StateIdle}}
	got := snapshotFromSession(cfg, sv, cfg.VisualizerViewer, nil, fixedNow) // nil TransportViewer

	if got.Transport.State != "stopped" {
		t.Errorf("State should be stopped when idle: %q", got.Transport.State)
	}
	if got.Transport.AdapterRef != "" || got.Transport.Generation != 0 {
		t.Errorf("idle ref/gen should be empty/0")
	}
}

func TestSnapshotFromSession_NilTransportViewerKeepsActiveReadOnlyStateAndTime(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	cfg := nonZeroConfig()

	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Title: "Read Only", Source: "plex",
		Position: 30 * time.Second, Duration: time.Minute,
		AdapterRef: "plex:abc", Generation: 9,
	}}
	got := snapshotFromSession(cfg, sv, cfg.VisualizerViewer, nil, fixedNow) // nil TransportViewer

	if got.Transport.State != "playing" {
		t.Errorf("State = %q, want playing", got.Transport.State)
	}
	if got.Transport.AdapterRef != "plex:abc" || got.Transport.Generation != 9 {
		t.Errorf("AdapterRef/Generation: got %q/%d, want plex:abc/9", got.Transport.AdapterRef, got.Transport.Generation)
	}
	if got.Transport.ElapsedTime != "00:30" || got.Transport.TotalTime != "01:00" {
		t.Errorf("time fields: got %q/%q, want 00:30/01:00", got.Transport.ElapsedTime, got.Transport.TotalTime)
	}
	if got.Transport.OffsetMS != 30000 || got.Transport.DurationMS != 60000 {
		t.Errorf("raw ms: got %d/%d, want 30000/60000", got.Transport.OffsetMS, got.Transport.DurationMS)
	}
	if got.Transport.ActionsEnabled != (ActionsEnabled{}) {
		t.Errorf("ActionsEnabled should be all-false with nil TransportViewer: %+v", got.Transport.ActionsEnabled)
	}
}
```

Add `"context"` to `internal/chassis/chassis_test.go` imports if not already present; `fakeTransportViewer` uses `context.Context`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestSnapshotFromSession`
Expected: FAIL — `snapshotFromSession` signature doesn't accept a `TransportViewer` yet.

- [ ] **Step 3: Extend `snapshotFromSession` signature + body**

Edit `internal/chassis/session.go`. The **current** signature (Spec 4) is `snapshotFromSession(cfg Config, sv SessionViewer, vv VisualizerViewer, now time.Time) ReceiverPageData`. **Add `tv TransportViewer` between `vv` and `now`** — DO NOT drop `vv`, Spec 4 needs it:

```go
func snapshotFromSession(cfg Config, sv SessionViewer, vv VisualizerViewer, tv TransportViewer, now time.Time) ReceiverPageData {
	base := idleSnapshot(cfg, now)
	if sv != nil {
		view := sv.StatusHomeView()
		switch view.State {
		case core.StatePlaying, core.StatePaused:
			base.State = StateLive
			base.VFD.State = string(StateLive)
			base.VFD.Title = view.Title
			base.VFD.Marquee = formatLiveMarquee(view)
			// QueueCurrent / QueueTotal stay 0/0 (Phase 1 placeholder).
			// SystemTime + Uptime are computed in idleSnapshot from `now`
			// and cfg.StartedAt; they remain valid in live state.
			base.Transport = buildTransportData(view, tv, context.Background())
		default:
			// idle/unknown keep idleSnapshot data and stopped transport.
		}
	}
	base.Visualizer.ActiveMode = liveVisualizerMode(cfg, vv)
	return base
}

// buildTransportData composes the new TransportData fields from the
// StatusHomeView + adapter view. Always populates State + time fields
// + AdapterRef + Generation. ActionsEnabled is derived from the
// adapter view when a provider exists; all-false otherwise.
func buildTransportData(view core.StatusHomeView, tv TransportViewer, ctx context.Context) TransportData {
	td := TransportData{
		State:       transportStateFromCore(view.State),
		AdapterRef:  view.AdapterRef,
		Generation:  view.Generation,
		ElapsedTime: formatPlaybackPosition(view.Position),
		TotalTime:   formatPlaybackDuration(view.Duration),
		OffsetMS:    clampMSNonNegative(int(view.Position / time.Millisecond)),
		DurationMS:  clampMSNonNegative(int(view.Duration / time.Millisecond)),
	}

	if tv == nil {
		finalizeTransportSeekFields(&td)
		return td
	}
	adapterView, ok := tv.PlaybackViewForSnapshot(ctx, view)
	if !ok {
		finalizeTransportSeekFields(&td)
		return td
	}
	// Prefer adapter-provided Seek raw ms when present.
	if adapterView.Seek != nil {
		if adapterView.Seek.DurationMS > 0 {
			td.DurationMS = clampMSNonNegative(adapterView.Seek.DurationMS)
		}
		td.OffsetMS = clampMSNonNegative(adapterView.Seek.OffsetMS)
	}
	td.ActionsEnabled = actionsEnabledFromAdapterView(adapterView)
	finalizeTransportSeekFields(&td)
	return td
}

func finalizeTransportSeekFields(td *TransportData) {
	td.SeekFillPercent = computeSeekFillPercent(time.Duration(td.OffsetMS)*time.Millisecond, time.Duration(td.DurationMS)*time.Millisecond)
	if td.SeekFillPercent > 0 || td.DurationMS > 0 {
		td.PercentPlayed = fmt.Sprintf("%d%%", td.SeekFillPercent)
	}
}

func transportStateFromCore(s core.State) string {
	switch s {
	case core.StatePlaying:
		return "playing"
	case core.StatePaused:
		return "paused"
	default:
		return "stopped"
	}
}

func computeSeekFillPercent(pos, dur time.Duration) int {
	if dur <= 0 {
		return 0
	}
	p := int(pos.Milliseconds() * 100 / dur.Milliseconds())
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

func clampMSNonNegative(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func actionsEnabledFromAdapterView(view adapters.PlaybackBannerAdapterView) ActionsEnabled {
	var ae ActionsEnabled
	for _, a := range view.Actions {
		switch a.ID {
		case adapters.PlaybackActionPause, adapters.PlaybackActionResume:
			if a.Enabled {
				ae.PauseResume = true
			}
		case adapters.PlaybackActionStop:
			ae.Stop = a.Enabled
		case adapters.PlaybackActionReplay:
			ae.Replay = a.Enabled
		case adapters.PlaybackActionPrevious:
			ae.Previous = a.Enabled
		case adapters.PlaybackActionNext:
			ae.Next = a.Enabled
		}
	}
	if view.Seek != nil && view.Seek.Enabled {
		ae.Seek = true
	}
	return ae
}
```

Add `"context"` and `"fmt"` to the imports if not already present (the file already imports `"time"` and `"strings"`).

**Update every `snapshotFromSession` caller** — production AND tests — to pass the new `tv` argument. The four production call sites are:

- `internal/chassis/handler.go:23` (`handleIndex`)
- `internal/chassis/server.go:91` area (synchronous seed in `New`)
- `internal/chassis/server.go:122` area (`startSnapshotRefresher` tick body)
- `internal/chassis/events.go:105` area (initial-snapshot refresh inside `handleEvents`)

For all four, pass `s.transportViewer` (or `cfg.TransportViewer` if called pre-Server-construction). For tests, `nil` is acceptable where the test doesn't exercise the transport path.

Use `rg -n "snapshotFromSession" internal/chassis` to confirm no call site is missed.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestSnapshotFromSession`
Expected: PASS — all four new tests + the existing snapshot tests.

- [ ] **Step 5: Run full chassis tests**

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/session.go internal/chassis/chassis_test.go internal/chassis/handler.go internal/chassis/server.go internal/chassis/events.go
git commit -m "$(cat <<'EOF'
feat(chassis): snapshotFromSession populates Transport from adapter view

Phase 1 / Spec 3 task 10. snapshotFromSession gains a TransportViewer
parameter; the new buildTransportData composes:

- State from core.State via the {Idle→stopped, Playing→playing,
  Paused→paused} mapping.
- ElapsedTime / TotalTime from Spec 2's formatPlaybackPosition /
  formatPlaybackDuration helpers.
- OffsetMS / DurationMS from PlaybackSeek when adapter supplies, else
  from StatusHomeView Position/Duration; both clamped non-negative.
- SeekFillPercent from integer (OffsetMS * 100 / DurationMS) after the
  final raw ms values are selected and clamped.
- PercentPlayed as "%d%%" formatted from the percent.
- ActionsEnabled mapped from adapters.PlaybackAction[].ID; missing
  actions default to false. PauseResume OR's pause/resume enable.
  Seek mirrors view.Seek.Enabled.

When TransportViewer is nil OR the dispatcher returns ok=false, the
chassis still renders read-only state + time fields from StatusHomeView
alone; ActionsEnabled stays all-false.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: `handleEvents` emits `transport` event (initial snapshot + diff loop)

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`
- Modify: `internal/chassis/server.go` (snapshot cache refresher passes transport viewer)

- [ ] **Step 1: Write failing tests for transport emission**

Append to `internal/chassis/events_test.go`:

```go
func TestHandleEvents_EmitsTransportEventOnInitialConnect(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "event: transport\n") {
		t.Errorf("initial snapshot missing transport event:\n%s", body)
	}
	if !strings.Contains(body, `"state":"stopped"`) {
		t.Errorf("initial transport event missing idle state:\n%s", body)
	}
}

func TestHandleEvents_EmitsTransportEventOnStateTransition(t *testing.T) {
	t.Parallel()
	sv := &mutableSessionViewer{view: core.StatusHomeView{State: core.StateIdle}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Mount(http.NewServeMux())
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	go func() {
		time.Sleep(150 * time.Millisecond)
		sv.set(core.StatusHomeView{State: core.StatePlaying, Title: "X", Source: "plex", AdapterRef: "plex:abc", Generation: 1})
		time.Sleep(350 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `"state":"stopped"`) {
		t.Errorf("missing initial stopped transport state")
	}
	if !strings.Contains(body, `"state":"playing"`) {
		t.Errorf("missing transition-to-playing transport state:\n%s", body)
	}
}
```

Also update existing Spec 2/4 tests that count events on initial connect. Find them with:

```bash
rg -n 'event: state|event: vfd|event: visualizer|initial snapshot' internal/chassis/events_test.go
```

For each test that asserts a specific count of events (e.g. "expected 3 records", "len(records) == 3"), update to expect 4. The most common patterns:

```go
// Before:
if !strings.Contains(body, "event: state") || !strings.Contains(body, "event: vfd") || !strings.Contains(body, "event: visualizer") { ... }
// After: add `!strings.Contains(body, "event: transport")` to the OR chain.
```

Spec 2's `TestHandleEvents_EmitsInitialSnapshotOnConnect` and `TestSnapshotCache_SeedsSynchronouslyBeforeFirstSSE` are the likely candidates. Update each to expect `transport` alongside `state`, `vfd`, `visualizer`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestHandleEvents_EmitsTransport`
Expected: FAIL — `handleEvents` doesn't emit `transport` yet.

- [ ] **Step 3: Extend `handleEvents` to emit `transport`**

In `internal/chassis/events.go`, find the initial-snapshot emission block (currently emits state, vfd, visualizer) and append a fourth emit:

```go
last := s.cache.Get()
if err := emit(w, "state", stateEnvelope{State: string(last.State)}); err != nil {
	return
}
if err := emit(w, "vfd", vfdEnvelopeFrom(last.VFD)); err != nil {
	return
}
if err := emit(w, "visualizer", vizEnvelope{Mode: last.Visualizer.ActiveMode}); err != nil {
	return
}
if err := emit(w, "transport", transportEnvelopeFrom(last.Transport)); err != nil {
	return
}
flusher.Flush()
```

Then in the diff loop (existing `case <-tick.C:`), add a transport-change block AFTER the existing state/vfd/visualizer blocks:

```go
case <-tick.C:
	curr := s.cache.Get()
	// existing state diff block ...
	// existing vfd diff block ...
	// existing visualizer diff block ...
	if transportChanged(curr.Transport, last.Transport) {
		if err := emit(w, "transport", transportEnvelopeFrom(curr.Transport)); err != nil {
			return
		}
		last.Transport = curr.Transport
	}
	flusher.Flush()
```

- [ ] **Step 4: Sanity-check the remaining `snapshotFromSession` call sites updated in Task 10**

Task 10 already updated `handleIndex`, `New`'s synchronous seed, `startSnapshotRefresher`, and the pre-Task-11 `handleEvents` initial-snapshot block to pass the new `s.transportViewer` argument. This task replaces the `handleEvents` initial snapshot with `s.cache.Get()`, so this step is verification only: confirm any remaining direct calls still use the five-argument signature.

Run: `rg -n "snapshotFromSession" internal/chassis` — every direct call should already pass five arguments (`cfg, session, visualizerViewer, transportViewer, now`). If any call still has four arguments, fix it before continuing.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestHandleEvents`
Expected: PASS — all initial-snapshot + transition tests including the new transport assertions.

- [ ] **Step 6: Run full chassis tests**

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go internal/chassis/server.go
git commit -m "$(cat <<'EOF'
feat(chassis): handleEvents emits transport SSE event

Phase 1 / Spec 3 task 11. handleEvents extends the initial-snapshot
block to emit a fourth record (state, vfd, visualizer, transport) and
the 250 ms diff loop now emits transport events when transportChanged
returns true (per task 9's 10-field transportChanged helper).

The snapshot cache refresher and synchronous seed in New both pass
s.transportViewer through to snapshotFromSession, so the cached
ReceiverPageData.Transport reflects current adapter view state on
every tick — at the same constant 4 Hz Manager.mu acquisition cadence
Spec 2 introduced.

Existing Spec 2/4 tests that count initial-snapshot events update from
3 to 4 (adding transport alongside state/vfd/visualizer).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: `handleTransportAction` POST handler

**Files:**
- Create: `internal/chassis/transport.go`
- Create: `internal/chassis/transport_test.go`

- [ ] **Step 1: Write failing tests for the action handler**

Create `internal/chassis/transport_test.go`:

```go
package chassis

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// fakeTransportController is the test double for TransportController.
type fakeTransportController struct {
	lastReq adapters.PlaybackActionRequest
	result  adapters.PlaybackActionResult
	err     error
}

func (f *fakeTransportController) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	f.lastReq = req
	return f.result, f.err
}

func TestHandleTransportAction_SuccessReturns204(t *testing.T) {
	t.Parallel()
	tc := &fakeTransportController{result: adapters.PlaybackActionResult{Message: "Paused"}}
	cfg := nonZeroConfig()
	cfg.TransportController = tc
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	body := "adapter_ref=plex:abc&generation=42&action=pause"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportAction(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 body should be empty; got: %s", rec.Body.String())
	}
	if tc.lastReq.Action != "pause" || tc.lastReq.AdapterRef != "plex:abc" || tc.lastReq.Generation != 42 {
		t.Errorf("dispatcher received wrong request: %+v", tc.lastReq)
	}
}

func TestHandleTransportAction_StaleGenerationReturns409(t *testing.T) {
	t.Parallel()
	tc := &fakeTransportController{err: adapters.ErrActiveSessionChanged}
	cfg := nonZeroConfig()
	cfg.TransportController = tc
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	body := "adapter_ref=plex:abc&generation=42&action=pause"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportAction(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("body should contain JSON error: %s", rec.Body.String())
	}
}

func TestHandleTransportAction_UnsupportedActionReturns422(t *testing.T) {
	t.Parallel()
	tc := &fakeTransportController{err: adapters.ErrPlaybackActionUnsupported}
	cfg := nonZeroConfig()
	cfg.TransportController = tc
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	body := "adapter_ref=plex:abc&generation=42&action=pause"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportAction(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestHandleTransportAction_ProviderErrorReturns500(t *testing.T) {
	t.Parallel()
	tc := &fakeTransportController{err: errors.New("provider exploded")}
	cfg := nonZeroConfig()
	cfg.TransportController = tc
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	body := "adapter_ref=plex:abc&generation=42&action=pause"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportAction(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	// Body must NOT leak the underlying error message.
	if strings.Contains(rec.Body.String(), "provider exploded") {
		t.Errorf("500 body must not leak internal error: %s", rec.Body.String())
	}
}

func TestHandleTransportAction_RejectsMalformedForm(t *testing.T) {
	t.Parallel()
	// Wire a controller so the nil-controller 500 branch doesn't preempt
	// the form-parse 400 we're actually testing for.
	cfg := nonZeroConfig()
	cfg.TransportController = &fakeTransportController{}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Missing generation field.
	body := "adapter_ref=plex:abc&action=pause"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportAction(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleTransportAction_RejectsSeekActionOnActionRoute(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.TransportController = &fakeTransportController{}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := "adapter_ref=plex:abc&generation=42&action=seek"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportAction(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (seek must use /seek route)", rec.Code)
	}
}

func TestHandleTransportAction_ResponseSetsCacheControlNoStore(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.TransportController = &fakeTransportController{}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := "adapter_ref=plex:abc&generation=42&action=pause"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportAction(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestHandleTransportAction_RejectsZeroGeneration(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.TransportController = &fakeTransportController{}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// generation=0 is structurally invalid (uint64 > 0 required) even
	// though the field is present. Distinct from "field missing" which
	// is covered by RejectsMalformedForm.
	body := "adapter_ref=plex:abc&generation=0&action=pause"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportAction(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleTransportAction_ErrorResponseContentTypeIsJSON(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.TransportController = &fakeTransportController{err: adapters.ErrActiveSessionChanged}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := "adapter_ref=plex:abc&generation=42&action=pause"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportAction(rec, req)

	// Spec 4's writeJSONError sets "application/json" exactly (no
	// charset). Lock the response Content-Type in.
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestHandleTransportAction`
Expected: FAIL — `handleTransportAction` not defined.

- [ ] **Step 3: Implement `handleTransportAction` in a new file**

Create `internal/chassis/transport.go`:

```go
package chassis

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

var allowedTransportActions = map[string]struct{}{
	adapters.PlaybackActionPause:    {},
	adapters.PlaybackActionResume:   {},
	adapters.PlaybackActionStop:     {},
	adapters.PlaybackActionPrevious: {},
	adapters.PlaybackActionNext:     {},
	adapters.PlaybackActionReplay:   {},
}

func transportNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// handleTransportAction dispatches a transport action (pause / resume /
// stop / previous / next / replay) to the playback dispatcher. Returns
// 204 on success. Wired through requireSameOrigin in Mount.
func (s *Server) handleTransportAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.transportController == nil {
		writeJSONError(w, http.StatusInternalServerError, "transport controller not configured")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed form body")
		return
	}
	adapterRef := strings.TrimSpace(r.PostFormValue("adapter_ref"))
	if adapterRef == "" {
		writeJSONError(w, http.StatusBadRequest, "adapter_ref required")
		return
	}
	gen, err := strconv.ParseUint(strings.TrimSpace(r.PostFormValue("generation")), 10, 64)
	if err != nil || gen == 0 {
		writeJSONError(w, http.StatusBadRequest, "generation required")
		return
	}
	action := strings.TrimSpace(r.PostFormValue("action"))
	if action == "" {
		writeJSONError(w, http.StatusBadRequest, "action required")
		return
	}
	if action == adapters.PlaybackActionSeek {
		writeJSONError(w, http.StatusBadRequest, "seek must use the /receiver/transport/seek route")
		return
	}
	if _, ok := allowedTransportActions[action]; !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown action")
		return
	}

	req := adapters.PlaybackActionRequest{
		Action:     action,
		AdapterRef: adapterRef,
		Generation: gen,
	}
	_, err = s.transportController.HandlePlaybackAction(r.Context(), req)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, adapters.ErrActiveSessionChanged):
		writeJSONError(w, http.StatusConflict, adapters.ErrActiveSessionChangedMessage)
	case errors.Is(err, adapters.ErrPlaybackActionUnsupported):
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		log.Printf("chassis: transport action failed: action=%q err=%v", action, err)
		writeJSONError(w, http.StatusInternalServerError, "internal dispatch failure")
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestHandleTransportAction`
Expected: PASS — all action-handler tests.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/transport.go internal/chassis/transport_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): handleTransportAction POST handler

Phase 1 / Spec 3 task 12. POST /receiver/transport/action handler:
- Form parsing + validation (adapter_ref, generation > 0, action in
  the allowed set, seek rejected with 400).
- Dispatches via TransportController.HandlePlaybackAction.
- Status mapping: ErrActiveSessionChanged → 409, ErrPlaybackAction-
  Unsupported → 422, success → 204, other errors → 500 (with the
  underlying message logged server-side but not leaked to the client).
- All handler and same-origin guard responses set Cache-Control: no-store.
- All non-success responses use Spec 4's writeJSONError shape.

Mount wiring through `transportNoStore(requireSameOrigin(...))` lands in task 16.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: `handleTransportSeek` POST handler

**Files:**
- Modify: `internal/chassis/transport.go`
- Modify: `internal/chassis/transport_test.go`

- [ ] **Step 1: Write failing tests for the seek handler**

Append to `internal/chassis/transport_test.go`:

```go
func TestHandleTransportSeek_ValidOffsetReturns204(t *testing.T) {
	t.Parallel()
	tc := &fakeTransportController{}
	cfg := nonZeroConfig()
	cfg.TransportController = tc
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	body := "adapter_ref=plex:abc&generation=42&offset_ms=12345"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/seek", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportSeek(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if tc.lastReq.Action != adapters.PlaybackActionSeek {
		t.Errorf("dispatcher action = %q, want seek", tc.lastReq.Action)
	}
	if tc.lastReq.OffsetMS != 12345 {
		t.Errorf("dispatcher OffsetMS = %d, want 12345", tc.lastReq.OffsetMS)
	}
}

func TestHandleTransportSeek_NonIntegerOffsetReturns400(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.TransportController = &fakeTransportController{}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := "adapter_ref=plex:abc&generation=42&offset_ms=notanumber"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/seek", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportSeek(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleTransportSeek_StaleGenerationReturns409(t *testing.T) {
	t.Parallel()
	tc := &fakeTransportController{err: adapters.ErrActiveSessionChanged}
	cfg := nonZeroConfig()
	cfg.TransportController = tc
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	body := "adapter_ref=plex:abc&generation=42&offset_ms=0"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/seek", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportSeek(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestHandleTransportSeek_UnsupportedActionReturns422(t *testing.T) {
	t.Parallel()
	tc := &fakeTransportController{err: adapters.ErrPlaybackActionUnsupported}
	cfg := nonZeroConfig()
	cfg.TransportController = tc
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	body := "adapter_ref=plex:abc&generation=42&offset_ms=0"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/seek", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportSeek(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

func TestHandleTransportSeek_ProviderErrorReturns500(t *testing.T) {
	t.Parallel()
	tc := &fakeTransportController{err: errors.New("provider exploded")}
	cfg := nonZeroConfig()
	cfg.TransportController = tc
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	body := "adapter_ref=plex:abc&generation=42&offset_ms=0"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/seek", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.handleTransportSeek(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run TestHandleTransportSeek`
Expected: FAIL — `handleTransportSeek` not defined.

- [ ] **Step 3: Implement `handleTransportSeek` in `transport.go`**

Append to `internal/chassis/transport.go`:

```go
// handleTransportSeek dispatches a seek action to the playback
// dispatcher. The dispatcher clamps negative offset_ms to zero before
// invoking the provider (so /ui and chassis get identical handling).
// Returns 204 on success. Wired through requireSameOrigin in Mount.
func (s *Server) handleTransportSeek(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.transportController == nil {
		writeJSONError(w, http.StatusInternalServerError, "transport controller not configured")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed form body")
		return
	}
	adapterRef := strings.TrimSpace(r.PostFormValue("adapter_ref"))
	if adapterRef == "" {
		writeJSONError(w, http.StatusBadRequest, "adapter_ref required")
		return
	}
	gen, err := strconv.ParseUint(strings.TrimSpace(r.PostFormValue("generation")), 10, 64)
	if err != nil || gen == 0 {
		writeJSONError(w, http.StatusBadRequest, "generation required")
		return
	}
	offset, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("offset_ms")))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "offset_ms must be an integer")
		return
	}

	req := adapters.PlaybackActionRequest{
		Action:     adapters.PlaybackActionSeek,
		AdapterRef: adapterRef,
		Generation: gen,
		OffsetMS:   offset,
	}
	_, err = s.transportController.HandlePlaybackAction(r.Context(), req)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, adapters.ErrActiveSessionChanged):
		writeJSONError(w, http.StatusConflict, adapters.ErrActiveSessionChangedMessage)
	case errors.Is(err, adapters.ErrPlaybackActionUnsupported):
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		log.Printf("chassis: transport seek failed: offset_ms=%d err=%v", offset, err)
		writeJSONError(w, http.StatusInternalServerError, "internal dispatch failure")
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis/ -run TestHandleTransportSeek`
Expected: PASS.

- [ ] **Step 5: Run full chassis tests**

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/transport.go internal/chassis/transport_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): handleTransportSeek POST handler

Phase 1 / Spec 3 task 13. POST /receiver/transport/seek handler.
Same form validation + status mapping as handleTransportAction;
offset_ms is parsed as int and passed through to the dispatcher
(which clamps negatives to zero per task 3). Action verb is hard-
coded to "seek" server-side.

Mount wiring lands in task 16 alongside the action route.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: Template + CSS updates

**Files:**
- Modify: `internal/chassis/templates/transport.html`
- Modify: `internal/chassis/templates/shell.html`
- Modify: `internal/chassis/static/chassis.css`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing tests for the new template hooks**

Append to `internal/chassis/chassis_test.go`:

```go
func TestTransportTemplate_RendersDataAttributeHooks(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	var buf bytes.Buffer
	data := ReceiverPageData{
		Transport: TransportData{
			State:           "playing",
			SeekFillPercent: 44,
			ElapsedTime:     "04:23",
			TotalTime:       "09:56",
			PercentPlayed:   "44%",
			OffsetMS:        263000,
			DurationMS:      596000,
			ActionsEnabled:  ActionsEnabled{Previous: true, Next: true, PauseResume: true, Stop: true, Replay: false, Seek: true},
		},
	}
	if err := tmpl.ExecuteTemplate(&buf, "transport", data.Transport); err != nil {
		t.Fatalf("execute transport partial: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`data-transport-state="playing"`,
		`data-transport-action="previous"`,
		`data-transport-action="next"`,
		`data-transport-action="pauseResume"`,
		`data-transport-action="stop"`,
		`data-transport-action="replay"`,
		`data-transport-seek`,
		`data-transport-seek-fill`,
		`data-transport-elapsed`,
		`data-transport-total`,
		`data-transport-percent`,
		`data-transport-offset-ms="263000"`,
		`data-transport-duration-ms="596000"`,
		`data-state-icon="playing"`,
		`data-state-icon="paused"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("transport partial missing %q; output:\n%s", want, body)
		}
	}
	// The Replay button has Enabled=false → must render `disabled`.
	replayDisabled := regexp.MustCompile(`(?s)<button[^>]*data-transport-action="replay"[^>]*disabled|<button[^>]*disabled[^>]*data-transport-action="replay"`)
	if !replayDisabled.MatchString(body) {
		t.Errorf("Replay button must render disabled when ActionsEnabled.Replay is false; output:\n%s", body)
	}
}

func TestTransportTemplate_SeekFillStyleReflectsPercent(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	// The seek-bar .fill div renders style="width: {{.SeekFillPercent}}%".
	// Render at three values to confirm the template wires the percent
	// through, not a hard-coded constant.
	cases := []struct {
		name    string
		percent int
		want    string
	}{
		{"zero", 0, `style="width: 0%"`},
		{"mid", 44, `style="width: 44%"`},
		{"full", 100, `style="width: 100%"`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "transport", TransportData{SeekFillPercent: tc.percent}); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("seek-bar fill missing %q; output:\n%s", tc.want, buf.String())
			}
		})
	}
}

func TestTransportTemplate_SeekDisabledReflectsActionsEnabled(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	cases := []struct {
		name     string
		enabled  bool
		want     string
		wantAria string
		forbid   string
	}{
		{name: "disabled", enabled: false, want: `data-transport-seek-disabled`, wantAria: `aria-disabled="true"`, forbid: `aria-disabled="false"`},
		{name: "enabled", enabled: true, want: `aria-disabled="false"`, wantAria: `aria-disabled="false"`, forbid: `data-transport-seek-disabled`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			data := TransportData{ActionsEnabled: ActionsEnabled{Seek: tc.enabled}}
			if err := tmpl.ExecuteTemplate(&buf, "transport", data); err != nil {
				t.Fatalf("execute: %v", err)
			}
			body := buf.String()
			if !strings.Contains(body, tc.want) {
				t.Errorf("seek enabled=%v missing %q; output:\n%s", tc.enabled, tc.want, body)
			}
			if !strings.Contains(body, tc.wantAria) {
				t.Errorf("seek enabled=%v missing aria state %q; output:\n%s", tc.enabled, tc.wantAria, body)
			}
			if strings.Contains(body, tc.forbid) {
				t.Errorf("seek enabled=%v should not render %q; output:\n%s", tc.enabled, tc.forbid, body)
			}
		})
	}
}

func TestShellTemplate_LoadsTransportScript(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	s.handleIndex(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `/receiver/static/transport.js?v=test-1.0.0`) {
		t.Errorf("shell missing versioned transport.js tag")
	}
	vfdIdx := strings.Index(body, "vfd-live.js?v=")
	tIdx := strings.Index(body, "transport.js?v=")
	if vfdIdx < 0 || tIdx < 0 {
		t.Fatalf("missing one of vfd-live.js or transport.js tag")
	}
	if tIdx <= vfdIdx {
		t.Errorf("transport.js must load AFTER vfd-live.js so the shared events.source is exposed first")
	}
}

func TestShellTemplate_EmitsChassisMetaTags(t *testing.T) {
	t.Parallel()
	sv := &fakeSessionViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Title: "X", Source: "plex",
		AdapterRef: "plex:abc", Generation: 42,
	}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	s.handleIndex(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `<meta name="chassis-adapter-ref" content="plex:abc"`) {
		t.Errorf("shell missing chassis-adapter-ref meta tag")
	}
	if !strings.Contains(body, `<meta name="chassis-generation" content="42"`) {
		t.Errorf("shell missing chassis-generation meta tag")
	}
}
```

Add `"regexp"` to imports if not already present.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis/ -run 'TestTransportTemplate|TestShellTemplate'`
Expected: FAIL — hooks not in templates yet.

- [ ] **Step 3: Update `transport.html`**

Replace the contents of `internal/chassis/templates/transport.html` with:

```html
{{define "transport"}}
{{htmlComment "chassis:transport"}}
<div class="transport-strip" aria-label="Transport controls" data-transport-state="{{.State}}">
  <span class="strip-label">Transport</span>
  <div class="transport-row">
    <button class="trn" type="button" data-transport-action="previous"
      {{if not .ActionsEnabled.Previous}}disabled{{end}}
      aria-label="Previous" title="Previous">&#x23ee;</button>
    <button class="trn" type="button" data-transport-action="next"
      {{if not .ActionsEnabled.Next}}disabled{{end}}
      aria-label="Next" title="Next">&#x23ed;</button>
    <button class="trn primary" type="button" data-transport-action="pauseResume"
      {{if not .ActionsEnabled.PauseResume}}disabled{{end}}
      aria-label="Pause or resume" title="Pause / Resume">
      <span data-state-icon="playing">&#x23f8;</span>
      <span data-state-icon="paused">&#x25b6;</span>
    </button>
    <button class="trn" type="button" data-transport-action="stop"
      {{if not .ActionsEnabled.Stop}}disabled{{end}}
      aria-label="Stop" title="Stop">&#x23f9;</button>
    <button class="trn" type="button" data-transport-action="replay"
      {{if not .ActionsEnabled.Replay}}disabled{{end}}
      aria-label="Replay" title="Replay">&#x27f2;</button>
  </div>
  <div class="seek-bar" data-transport-seek
    data-transport-offset-ms="{{.OffsetMS}}"
    data-transport-duration-ms="{{.DurationMS}}"
    {{if not .ActionsEnabled.Seek}}data-transport-seek-disabled aria-disabled="true"{{else}}aria-disabled="false"{{end}}
    role="progressbar" aria-label="Cast position" aria-valuemin="0" aria-valuemax="100"
    aria-valuenow="{{.SeekFillPercent}}" title="Cast position">
    <div class="fill" data-transport-seek-fill style="width: {{.SeekFillPercent}}%"></div>
    <div class="head"><span class="grip"></span></div>
  </div>
  <div class="seek-time">
    <span class="seg-display"><span class="seg-ghost" aria-hidden="true">88:88</span><span class="seg-text" data-transport-elapsed>{{.ElapsedTime}}</span></span>
    <span class="sep">/</span>
    <span class="total seg-display"><span class="seg-ghost" aria-hidden="true">88:88</span><span class="seg-text" data-transport-total>{{.TotalTime}}</span></span>
    <span class="pct" data-transport-percent title="Playback position">{{.PercentPlayed}}</span>
  </div>
  <button class="gear-btn" id="gear-btn" type="button">&#x2699; Setup</button>
</div>
{{end}}
```

- [ ] **Step 4: Add the script + meta tags to `shell.html`**

Find the `<head>` section. Add the meta tags (placement near the existing `<meta>` tags is fine):

```html
<meta name="chassis-adapter-ref" content="{{.Transport.AdapterRef}}">
<meta name="chassis-generation" content="{{.Transport.Generation}}">
```

Find the line that loads `vfd-live.js` (likely something like `<script defer src="/receiver/static/vfd-live.js?v={{.Version}}"></script>`). Immediately AFTER that line, add:

```html
<script defer src="/receiver/static/transport.js?v={{.Version}}"></script>
```

- [ ] **Step 5: Add CSS rules to `chassis.css`**

Append to `internal/chassis/static/chassis.css` (under the existing `body.receiver` scope):

```css
body.receiver [data-transport-state="playing"] [data-state-icon="paused"] { display: none; }
body.receiver [data-transport-state="paused"]  [data-state-icon="playing"] { display: none; }
body.receiver [data-transport-state="stopped"] [data-state-icon="paused"] { display: none; }
body.receiver .transport-strip [data-transport-action]:disabled { opacity: 0.35; cursor: not-allowed; }
body.receiver .seek-bar[data-transport-seek-disabled] { pointer-events: none; opacity: 0.5; }
```

- [ ] **Step 6: Run the template tests**

Run: `go test ./internal/chassis/ -run 'TestTransportTemplate|TestShellTemplate'`
Expected: PASS.

- [ ] **Step 7: Run all chassis tests**

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/chassis/templates/transport.html internal/chassis/templates/shell.html internal/chassis/static/chassis.css internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): transport template hooks + CSS + shell wiring

Phase 1 / Spec 3 task 14. Template + CSS updates for the new
transport surface:

- transport.html gains data-transport-* attribute hooks for the JS
  client (state, action, seek, seek-fill, elapsed, total, percent),
  raw OffsetMS/DurationMS attributes for seek math, two data-state-
  icon spans on the pause/resume button (CSS toggles via the parent
  data-transport-state), and {{if not ...}}disabled{{end}} markup so
  cold-render disabled state matches the live SSE state.
- shell.html loads transport.js after vfd-live.js (defer-order
  guarantee: vfd-live.js exposes Chassis.events.source first); adds
  chassis-adapter-ref + chassis-generation meta tags so the client
  can POST without waiting for the first SSE event.
- chassis.css adds CSS-only icon swap rules (no JS textContent
  manipulation) and the disabled-seek pointer-events guard.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 15: `transport.js` client

**Files:**
- Create: `internal/chassis/static/transport.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write a smoke test asserting the static asset is served**

Append to `internal/chassis/chassis_test.go`:

```go
func TestHandleStatic_TransportJSServed(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	req := httptest.NewRequest(http.MethodGet, "/receiver/static/transport.js", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "chassis:eventsource") {
		t.Errorf("transport.js should attach to the chassis:eventsource CustomEvent surface")
	}
	if !strings.Contains(body, "data-transport-action") {
		t.Errorf("transport.js should reference data-transport-action selectors")
	}
}

// Lock in two race-window mitigations the spec explicitly called out.
// Both are content checks on the served JS — cheap and high-signal.

func TestTransportJS_SeekUsesRawDurationMsNotClockText(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	req := httptest.NewRequest(http.MethodGet, "/receiver/static/transport.js", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()

	// Positive: the drag release reads data-transport-duration-ms.
	if !strings.Contains(body, "data-transport-duration-ms") {
		t.Errorf("transport.js seek math must read data-transport-duration-ms; not present in served JS")
	}
	// Negative: should NOT parse the formatted MM:SS clock text via a
	// .split(':') / Date.parse / etc. for seek math. If a future refactor
	// re-adds clock parsing, this test fires.
	for _, forbidden := range []string{".split(':')", "parseClockToMs", "MM:SS"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("transport.js must not parse formatted clock text for seek math; found %q", forbidden)
		}
	}
}

func TestTransportJS_RefusesPauseResumeClickWhenStopped(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	req := httptest.NewRequest(http.MethodGet, "/receiver/static/transport.js", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()

	// The click handler must short-circuit on transportState === 'stopped'
	// for the pauseResume button (defense-in-depth over the disabled
	// attribute). Look for the literal guard.
	if !strings.Contains(body, "transportState === 'stopped'") {
		t.Errorf("transport.js click handler must explicitly refuse pauseResume against stopped state")
	}
	if !strings.Contains(body, "getAttribute('data-transport-state')") {
		t.Errorf("transport.js boot must hydrate transportState from the cold-rendered data-transport-state before first click")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run 'TestHandleStatic_TransportJSServed|TestTransportJS'`
Expected: FAIL — file doesn't exist.

- [ ] **Step 3: Create `transport.js`**

Create `internal/chassis/static/transport.js`:

```javascript
// Receiver chassis transport controls. Phase 1 / Spec 3.
//
// Subscribes to the `transport` SSE event via Spec 4's shared
// window.Chassis.events.source + `chassis:eventsource` CustomEvent
// surface (no edits to vfd-live.js needed). Updates DOM via
// data-transport-* attribute hooks set in transport.html. POSTs to
// /receiver/transport/{action,seek} on button click + seek-bar drag.
(() => {
  'use strict';

  if (!window.Chassis) {
    console.warn('transport: window.Chassis missing; chassis.js failed to load?');
    return;
  }

  let adapterRef = '';
  let generation = 0;
  let transportState = 'stopped';
  let attachedSource = null; // dedupe guard for attachSource

  const metaRef = document.querySelector('meta[name="chassis-adapter-ref"]');
  const metaGen = document.querySelector('meta[name="chassis-generation"]');
  if (metaRef) adapterRef = metaRef.content || '';
  if (metaGen) generation = parseInt(metaGen.content || '0', 10) || 0;

  function handleTransportEvent(ev) {
    try {
      const data = JSON.parse(ev.data);
      const strip = document.querySelector('.transport-strip');
      if (!strip) return;

      adapterRef = data.adapterRef || '';
      generation = data.generation || 0;
      transportState = data.state || 'stopped';
      strip.setAttribute('data-transport-state', transportState);

      const enabled = data.actionsEnabled || {};
      for (const action of ['previous', 'next', 'pauseResume', 'stop', 'replay']) {
        const btn = strip.querySelector(`[data-transport-action="${action}"]`);
        if (btn) btn.disabled = !enabled[action];
      }
      const seekBar = strip.querySelector('[data-transport-seek]');
      if (seekBar) {
        if (enabled.seek) seekBar.removeAttribute('data-transport-seek-disabled');
        else seekBar.setAttribute('data-transport-seek-disabled', '');
        seekBar.setAttribute('aria-disabled', enabled.seek ? 'false' : 'true');
        if (typeof data.offsetMs === 'number') seekBar.setAttribute('data-transport-offset-ms', data.offsetMs);
        if (typeof data.durationMs === 'number') seekBar.setAttribute('data-transport-duration-ms', data.durationMs);
      }

      // Suppress fill updates while user is dragging.
      const fill = strip.querySelector('[data-transport-seek-fill]');
      const dragging = seekBar && seekBar.hasAttribute('data-seek-interacting');
      if (fill && !dragging) {
        fill.style.width = `${data.seekFillPercent || 0}%`;
        if (seekBar) seekBar.setAttribute('aria-valuenow', data.seekFillPercent || 0);
      }

      const elapsed = strip.querySelector('[data-transport-elapsed]');
      const total = strip.querySelector('[data-transport-total]');
      const percent = strip.querySelector('[data-transport-percent]');
      if (elapsed) elapsed.textContent = data.elapsedTime || '';
      if (total) total.textContent = data.totalTime || '';
      if (percent) percent.textContent = data.percentPlayed || '';
    } catch (err) {
      console.warn('transport: bad payload', ev.data, err);
    }
  }

  async function postForm(url, params) {
    try {
      const res = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams(params).toString(),
      });
      if (res.status === 204) return { ok: true };
      const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
      console.warn(`transport: ${url} failed (${res.status})`, body.error || '');
      return { ok: false, error: body.error, status: res.status };
    } catch (err) {
      console.warn(`transport: ${url} network error`, err);
      return { ok: false, error: String(err) };
    }
  }

  function actionForButton(btn) {
    const action = btn.getAttribute('data-transport-action');
    if (action === 'pauseResume') {
      return transportState === 'paused' ? 'resume' : 'pause';
    }
    return action;
  }

  function handleClick(ev) {
    const btn = ev.target.closest('[data-transport-action]');
    if (!btn || btn.disabled) return;
    if (!adapterRef || generation === 0) return;
    if (btn.getAttribute('data-transport-action') === 'pauseResume' && transportState === 'stopped') return;
    const action = actionForButton(btn);
    if (!action) return;
    postForm('/receiver/transport/action', { adapter_ref: adapterRef, generation, action });
  }

  function bindSeekDrag(seekBar) {
    if (!seekBar) return;
    let dragging = false;
    function pctFromEvent(ev) {
      const rect = seekBar.getBoundingClientRect();
      const x = ev.clientX !== undefined ? ev.clientX : (ev.touches && ev.touches[0] ? ev.touches[0].clientX : 0);
      const p = Math.max(0, Math.min(100, ((x - rect.left) / rect.width) * 100));
      return Math.round(p);
    }
    function applyVisualPercent(p) {
      const fill = seekBar.querySelector('[data-transport-seek-fill]');
      if (fill) fill.style.width = `${p}%`;
      seekBar.setAttribute('aria-valuenow', p);
    }
    seekBar.addEventListener('pointerdown', (ev) => {
      if (seekBar.hasAttribute('data-transport-seek-disabled')) return;
      if (!adapterRef || generation === 0) return;
      dragging = true;
      seekBar.setAttribute('data-seek-interacting', '');
      seekBar.setPointerCapture(ev.pointerId);
      applyVisualPercent(pctFromEvent(ev));
    });
    seekBar.addEventListener('pointermove', (ev) => {
      if (!dragging) return;
      applyVisualPercent(pctFromEvent(ev));
    });
    function release(ev) {
      if (!dragging) return;
      dragging = false;
      const p = pctFromEvent(ev);
      seekBar.removeAttribute('data-seek-interacting');
      const totalMs = parseInt(seekBar.getAttribute('data-transport-duration-ms') || '0', 10) || 0;
      if (totalMs <= 0) return;
      const offset_ms = Math.round((p / 100) * totalMs);
      postForm('/receiver/transport/seek', { adapter_ref: adapterRef, generation, offset_ms });
    }
    function cancel() {
      if (!dragging) return;
      dragging = false;
      seekBar.removeAttribute('data-seek-interacting');
    }
    seekBar.addEventListener('pointerup', release);
    seekBar.addEventListener('pointercancel', cancel);
  }

  function attachSource(src) {
    if (!src || src === attachedSource) return;
    if (attachedSource) {
      attachedSource.removeEventListener('transport', handleTransportEvent);
    }
    attachedSource = src;
    src.addEventListener('transport', handleTransportEvent);
  }

  function boot() {
    const strip = document.querySelector('.transport-strip');
    if (strip) {
      transportState = strip.getAttribute('data-transport-state') || transportState;
    }
    document.addEventListener('click', handleClick);
    bindSeekDrag(strip ? strip.querySelector('[data-transport-seek]') : document.querySelector('[data-transport-seek]'));
    if (window.Chassis.events && window.Chassis.events.source) {
      attachSource(window.Chassis.events.source);
    }
    document.addEventListener('chassis:eventsource', (ev) => {
      attachSource(ev.detail && ev.detail.source);
    });
  }

  document.addEventListener('DOMContentLoaded', boot);
})();
```

- [ ] **Step 4: Run the smoke + syntax checks**

Run:

```bash
go test ./internal/chassis/ -run 'TestHandleStatic_TransportJSServed|TestTransportJS'
node --check internal/chassis/static/transport.js
```

Expected: PASS — Go smoke tests pass and `node --check` reports no syntax errors.

- [ ] **Step 5: Run all chassis tests**

Run: `go test ./internal/chassis/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/static/transport.js internal/chassis/chassis_test.go
git commit -m "$(cat <<'EOF'
feat(chassis): transport.js EventSource subscriber + click/drag handlers

Phase 1 / Spec 3 task 15. Vanilla ES2022 IIFE (~150 lines) that:
- Reads initial adapter_ref + generation from <meta> tags and
  transportState from the cold-rendered transport strip so the first
  click works without waiting for the SSE.
- Attaches a `transport` event listener to the shared EventSource
  exposed by vfd-live.js (window.Chassis.events.source) or via the
  chassis:eventsource CustomEvent for the subscribe-before-connect
  case — same pattern as visualizer-bank.js (Spec 4).
- Updates DOM via data-transport-* selectors: state attribute on the
  strip parent (drives CSS icon swap), disabled flag on each button,
  fill width on the seek-bar, time text in the seg-text spans, raw
  offset/duration attrs for seek math.
- Refuses pauseResume clicks against stopped state (defense-in-depth
  over the disabled attribute).
- Seek drag: pointerdown sets data-seek-interacting (SSE skips fill
  updates while dragging); pointerup POSTs the resolved offset_ms
  computed from data-transport-duration-ms; pointercancel only clears
  drag state and never sends a seek.
- Static asset smoke tests cover the expected hooks and `node --check`
  verifies the script parses before browser testing.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 16: `Mount` registers POST routes + main.go wires Dispatcher + import-lint extension

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/import_check_test.go`
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Write a failing test asserting the new routes mount via requireSameOrigin**

Append to `internal/chassis/transport_test.go`:

```go
func TestMount_RegistersTransportRoutesThroughRequireSameOrigin(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	// Cross-site POST (no Sec-Fetch-Site header) must be rejected by
	// requireSameOrigin before the handler runs.
	body := "adapter_ref=plex:abc&generation=42&action=pause"
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Deliberately omit Sec-Fetch-Site.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("missing Sec-Fetch-Site: status = %d, want 403", rec.Code)
	}

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("missing Sec-Fetch-Site Cache-Control = %q, want no-store", got)
	}

	// Same-site POST passes the middleware. The response itself is
	// 500 (no transport controller wired in nonZeroConfig) — what
	// matters here is that we're NOT returning 403 from the middleware.
	req2 := httptest.NewRequest(http.MethodPost, "/receiver/transport/action", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("Sec-Fetch-Site", "same-origin")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code == http.StatusForbidden {
		t.Errorf("same-origin POST should pass requireSameOrigin; got 403")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis/ -run TestMount_RegistersTransportRoutes`
Expected: FAIL — routes not yet mounted.

- [ ] **Step 3: Extend `Mount` in `server.go`**

Find `Mount` and add the two new route registrations alongside the visualizer one:

```go
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /receiver", s.handleIndex)
	mux.HandleFunc("GET /receiver/{$}", s.handleIndex)
	mux.HandleFunc("GET /receiver/static/", s.handleStatic)
	mux.HandleFunc("GET /receiver/events", s.handleEvents)
	mux.Handle("POST /receiver/visualizer", requireSameOrigin(http.HandlerFunc(s.handleVisualizerPost)))
	mux.Handle("POST /receiver/transport/action", transportNoStore(requireSameOrigin(http.HandlerFunc(s.handleTransportAction))))
	mux.Handle("POST /receiver/transport/seek", transportNoStore(requireSameOrigin(http.HandlerFunc(s.handleTransportSeek))))
	s.cacheOnce.Do(s.startSnapshotRefresher)
}
```

- [ ] **Step 4: Extend `import_check_test.go`**

In `internal/chassis/import_check_test.go`, add three new entries to the `rules` slice:

```go
{
	fromPkg: modulePath + "/internal/playback",
	fromDir: filepath.Join(repoRoot, "internal", "playback"),
	forbidden: []string{
		modulePath + "/internal/chassis",
		modulePath + "/internal/ui",
		modulePath + "/internal/uiserver",
	},
},
{
	fromPkg: modulePath + "/internal/core",
	fromDir: filepath.Join(repoRoot, "internal", "core"),
	forbidden: []string{
		modulePath + "/internal/playback",
	},
},
{
	fromPkg: modulePath + "/internal/adapters",
	fromDir: filepath.Join(repoRoot, "internal", "adapters"),
	forbidden: []string{
		modulePath + "/internal/playback",
	},
},
```

- [ ] **Step 5: Wire the Dispatcher in `main.go`**

Edit `cmd/mister-groovy-relay/main.go`. Find the existing `chassisSrv, err := chassis.New(chassis.Config{...})` block and the existing `uiSrv, err := ui.New(ui.Config{...})` block. Add a `playback.NewDispatcher` line BEFORE both:

```go
playbackDispatcher := playback.NewDispatcher(coreMgr, reg)
```

Then pass it through to both consumers:

```go
uiSrv, err := ui.New(ui.Config{
	// ... existing fields stay ...
	Playback: playbackDispatcher,
})

chassisSrv, err := chassis.New(chassis.Config{
	// ... existing fields stay ...
	TransportViewer:     playbackDispatcher,
	TransportController: playbackDispatcher,
})
```

Add `"github.com/idio-sync/MiSTer_GroovyRelay/internal/playback"` to main.go's imports.

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/chassis/ -run 'TestMount_RegistersTransportRoutes|TestProductionImports_NoCrossPackageCoupling'`
Expected: PASS — route mounted, import boundaries enforced.

- [ ] **Step 7: Build the binary to confirm main.go compiles**

Run: `go build ./cmd/mister-groovy-relay`
Expected: clean build.

- [ ] **Step 8: Run full repo compile/test**

Run: `go test ./... && go build ./...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/chassis/server.go internal/chassis/import_check_test.go internal/chassis/transport_test.go cmd/mister-groovy-relay/main.go
git commit -m "$(cat <<'EOF'
feat(chassis): Mount transport routes + main.go wiring + import lint

Phase 1 / Spec 3 task 16. Three pieces of integration:

- chassis.Mount registers POST /receiver/transport/action and POST
  /receiver/transport/seek, each wrapped through Spec 4's
  requireSameOrigin middleware plus transportNoStore so even guard
  failures carry Cache-Control: no-store. Pattern matches the visualizer
  POST route precedent with the extra transport cache contract.
- main.go builds a single playback.NewDispatcher(coreMgr, reg) and
  passes it to ui.Config.Playback, chassis.Config.TransportViewer,
  and chassis.Config.TransportController — one canonical instance,
  three structural-satisfaction injection points.
- import_check_test.go extends the cross-package-coupling rules with
  three new entries: internal/playback may not import internal/chassis,
  ui, or uiserver; internal/core and internal/adapters may not import
  internal/playback. The no-import-cycle invariant the dispatcher's
  placement depends on is now lint-enforced.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 17: Integration tests (Layer 3)

**Files:**
- Modify: `tests/integration/chassis_test.go`

- [ ] **Step 1: Locate the existing Phase 0/1/2 integration test file**

The file already exists with `//go:build integration` and several `TestReceiver*` tests. Spec 3 appends new tests below the existing ones (before `collectClasses`).

- [ ] **Step 2: Add new integration tests**

Append to `tests/integration/chassis_test.go`:

```go
func TestReceiverTransport_PostActionDispatchesViaController(t *testing.T) {
	mux := http.NewServeMux()
	tc := &fakeIntegrationTransport{result: adapters.PlaybackActionResult{Message: "Paused"}}
	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:              config.BridgeConfig{},
		Manager:             &core.Manager{},
		Registry:            adapters.NewRegistry(),
		Version:             "integration-test",
		StartedAt:           time.Now(),
		HostIP:              "10.0.0.5",
		TransportController: tc,
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	defer chassisSrv.Close()
	chassisSrv.Mount(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := "adapter_ref=plex:abc&generation=42&action=pause"
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /receiver/transport/action: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if tc.lastReq.Action != "pause" || tc.lastReq.AdapterRef != "plex:abc" || tc.lastReq.Generation != 42 {
		t.Errorf("controller received wrong request: %+v", tc.lastReq)
	}
}

func TestReceiverTransport_PostActionStaleGenerationReturns409(t *testing.T) {
	mux := http.NewServeMux()
	tc := &fakeIntegrationTransport{err: adapters.ErrActiveSessionChanged}
	chassisSrv, _ := chassis.New(chassis.Config{
		Bridge: config.BridgeConfig{}, Manager: &core.Manager{}, Registry: adapters.NewRegistry(),
		Version: "test", StartedAt: time.Now(), HostIP: "10.0.0.5",
		TransportController: tc,
	})
	defer chassisSrv.Close()
	chassisSrv.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := "adapter_ref=plex:abc&generation=42&action=pause"
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestReceiverTransport_PostSeekDispatchesOffset(t *testing.T) {
	mux := http.NewServeMux()
	tc := &fakeIntegrationTransport{}
	chassisSrv, _ := chassis.New(chassis.Config{
		Bridge: config.BridgeConfig{}, Manager: &core.Manager{}, Registry: adapters.NewRegistry(),
		Version: "test", StartedAt: time.Now(), HostIP: "10.0.0.5",
		TransportController: tc,
	})
	defer chassisSrv.Close()
	chassisSrv.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := "adapter_ref=plex:abc&generation=42&offset_ms=12345"
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/receiver/transport/seek", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if tc.lastReq.Action != adapters.PlaybackActionSeek || tc.lastReq.OffsetMS != 12345 {
		t.Errorf("controller received wrong request: %+v", tc.lastReq)
	}
}

func TestReceiverTransport_SSEReflectsAction(t *testing.T) {
	mux := http.NewServeMux()
	// Live session viewer + transport viewer that reports the cast.
	sv := &fakeIntegrationSession{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Integration",
		Source:     "plex",
		AdapterRef: "plex:abc",
		Generation: 42,
	}}
	tv := &fakeIntegrationTransportViewer{view: adapters.PlaybackBannerAdapterView{
		Actions: []adapters.PlaybackAction{
			{ID: adapters.PlaybackActionPause, Enabled: true},
			{ID: adapters.PlaybackActionStop, Enabled: true},
		},
		Seek: &adapters.PlaybackSeek{Enabled: true},
	}, ok: true}
	tc := &fakeIntegrationTransport{}
	tc.onAction = func(req adapters.PlaybackActionRequest) {
		if req.Action != adapters.PlaybackActionPause {
			return
		}
		sv.set(core.StatusHomeView{
			State:      core.StatePaused,
			Title:      "Integration",
			Source:     "plex",
			AdapterRef: "plex:abc",
			Generation: 42,
		})
	}
	chassisSrv, _ := chassis.New(chassis.Config{
		Bridge: config.BridgeConfig{}, Manager: &core.Manager{}, Registry: adapters.NewRegistry(),
		Version: "test", StartedAt: time.Now(), HostIP: "10.0.0.5",
		Session:             sv,
		TransportViewer:     tv,
		TransportController: tc,
	})
	defer chassisSrv.Close()
	chassisSrv.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/receiver/events", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /receiver/events: %v", err)
	}
	defer resp.Body.Close()

	postBody := "adapter_ref=plex:abc&generation=42&action=pause"
	postReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/receiver/transport/action", strings.NewReader(postBody))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("Sec-Fetch-Site", "same-origin")
	postResp, err := srv.Client().Do(postReq)
	if err != nil {
		t.Fatalf("POST /receiver/transport/action: %v", err)
	}
	_ = postResp.Body.Close()
	if postResp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST status = %d, want 204", postResp.StatusCode)
	}

	rdr := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	var sawTransport, sawPaused bool
	for time.Now().Before(deadline) && !(sawTransport && sawPaused) {
		line, err := rdr.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE: %v", err)
		}
		if strings.HasPrefix(line, "event: transport") {
			sawTransport = true
		}
		if strings.Contains(line, `"state":"paused"`) {
			sawPaused = true
		}
	}
	if !sawTransport || !sawPaused {
		t.Errorf("expected post-driven transport event with state=paused within 2s; sawTransport=%v sawPaused=%v", sawTransport, sawPaused)
	}
}

// fakeIntegrationTransport stubs TransportController for integration tests.
type fakeIntegrationTransport struct {
	lastReq adapters.PlaybackActionRequest
	result  adapters.PlaybackActionResult
	err     error
	onAction func(adapters.PlaybackActionRequest)
}

func (f *fakeIntegrationTransport) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	f.lastReq = req
	if f.onAction != nil {
		f.onAction(req)
	}
	return f.result, f.err
}

// fakeIntegrationTransportViewer stubs TransportViewer for integration tests.
type fakeIntegrationTransportViewer struct {
	view adapters.PlaybackBannerAdapterView
	ok   bool
}

func (f *fakeIntegrationTransportViewer) PlaybackViewForSnapshot(ctx context.Context, snap core.StatusHomeView) (adapters.PlaybackBannerAdapterView, bool) {
	return f.view, f.ok
}
```

`fakeIntegrationSession` referenced in `TestReceiverTransport_SSEReflectsAction` already exists in the file — Spec 2's `TestReceiverEvents_LivePathReachesClient` defined it. Reuse it, but extend it with a `sync.Mutex` and `set(core.StatusHomeView)` helper so the POST controller can safely mutate the session while the SSE loop reads:

```go
type fakeIntegrationSession struct {
	mu   sync.Mutex
	view core.StatusHomeView
}

func (f *fakeIntegrationSession) StatusHomeView() core.StatusHomeView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.view
}

func (f *fakeIntegrationSession) set(view core.StatusHomeView) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.view = view
}
```

Extend imports with `bufio`, `context`, and `sync` if not already present.

Add lightweight Layer 3 table tests alongside the four snippets above for the remaining endpoint contract edges:

- Cross-site or missing `Sec-Fetch-Site` POST to `/receiver/transport/action` returns `403`, `Content-Type: application/json`, and `Cache-Control: no-store`.
- Controller returning `adapters.ErrPlaybackActionUnsupported` maps to `422`.
- Non-integer `offset_ms` on `/receiver/transport/seek` maps to `400`.
- `GET /receiver/transport/action` and `GET /receiver/transport/seek` return mux-provided `405`.
- `/ui/playback/banner` remains unshadowed when mounted with the chassis. If `TestMount_DoesNotShadowUIRoutes` or `TestReceiverEvents_DoesNotShadowUIRoutes` already covers the same mux composition in this file, extend that existing test rather than duplicating it.

- [ ] **Step 3: Run the integration tests**

Run: `go test -tags=integration ./tests/integration/...`
Expected: PASS — all existing + new tests.

- [ ] **Step 4: Commit**

```bash
git add tests/integration/chassis_test.go
git commit -m "$(cat <<'EOF'
test(chassis): integration coverage for /receiver/transport routes + SSE

Phase 1 / Spec 3 task 17. New integration tests booting full
httptest.Server with the chassis mounted:

- PostActionDispatchesViaController: end-to-end form POST → 204 →
  controller received the right adapters.PlaybackActionRequest.
- PostActionStaleGenerationReturns409: dispatcher returns sentinel;
  handler maps to 409.
- PostSeekDispatchesOffset: same shape with offset_ms in form, action
  set to "seek" server-side.
- SSEReflectsAction: with a live session viewer + transport viewer,
  a POST pause mutates the fake session and /receiver/events emits the
  follow-up transport event with state=paused within 2s.

Two test-local fakes mirror the chassis interfaces:
fakeIntegrationTransport (TransportController) and
fakeIntegrationTransportViewer (TransportViewer).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 18: Manual verification + PR description

**Files:**
- None (verification + PR prep only).

- [ ] **Step 1: Run the full CI matrix locally**

```bash
make lint
make test
go test -race ./...
make test-integration
```

Expected: all four exit 0. If any are red, return to the offending task.

- [ ] **Step 2: Build a fresh binary and start the bridge**

```bash
make build
./mister-groovy-relay --config path/to/test-config.toml
```

- [ ] **Step 3: Run the manual verification checklist**

Open the chassis at `http://<bridge-host>:<http_port>/receiver` and confirm each:

| # | Check | Pass condition |
|---|---|---|
| 1 | Start a Plex cast | Within ~1 s: chassis goes `live` (Spec 2), transport row buttons enabled, pause button shows pause icon, seek-bar fills, time labels populate. |
| 2 | Click pause | Within ~500 ms: button shows resume icon, marquee position stops advancing. |
| 3 | Click resume | Within ~500 ms: button shows pause icon, position resumes. |
| 4 | Click stop | Within ~500 ms: chassis state goes idle, transport state goes stopped, all buttons disabled. |
| 5 | Drag seek-bar | While dragging: bar follows cursor; SSE doesn't fight it. On release: bar holds, SSE confirms within ~250 ms. |
| 6 | Click in middle of seek-bar (no drag) | Position jumps; SSE confirms. |
| 7 | Adapter without seek support | `actionsEnabled.seek=false` → seek-bar has `data-transport-seek-disabled`, pointer-events disabled. |
| 8 | Click previous/next/replay if adapter supports | Action dispatches; SSE reflects. If not supported: button disabled, no click. |
| 9 | Multi-tab during a cast | All tabs show the same transport state; pause from one, all see the SSE event. |
| 10 | Stale generation race | Open two tabs; start a new cast in tab A; click pause in tab B → 409 in DevTools console; SSE pushes new state. |
| 11 | Cross-site POST simulation | Run from a shell on the bridge host: `curl -X POST http://localhost:<port>/receiver/transport/action -d 'adapter_ref=x&generation=1&action=pause'` (note: curl does NOT set `Sec-Fetch-Site` by default) → 403 `{"error":"cross-site request blocked"}`. Browser DevTools console fetches are unsuitable for this test because Chromium auto-sets `Sec-Fetch-Site: same-origin` on same-origin fetches. |
| 12 | `/ui/playback/banner` still works | Open `/ui` in another tab during a live cast; banner polls and renders. Pause from `/ui`; both `/ui` and `/receiver` reflect the change (single dispatcher). |

- [ ] **Step 4: Capture screenshots for the PR description**

1. Live state during a cast (VFD + visualizer + transport row all populated).
2. DevTools Network → EventStream during a cast (visible `state`, `vfd`, `visualizer`, `transport` events).
3. DevTools Network → response of a 204 POST (no body).
4. DevTools Network → response of a 409 stale-generation POST (`{"error":"active session changed"}`).

- [ ] **Step 5: Draft the PR description**

Use this template:

```markdown
# Phase 1 / Spec 3: Receiver Chassis Transport Controls

Implements docs/superpowers/specs/2026-05-22-receiver-chassis-transport-design.md.

## What's in

- Two new POST endpoints: `POST /receiver/transport/action` and `POST /receiver/transport/seek`, both wrapped in Spec 4's `requireSameOrigin` middleware, both 204-on-success / `{"error":"..."}`-on-failure.
- New `transport` SSE event on `/receiver/events`. Carries playing/paused/stopped state, server-formatted seek-bar fields, per-action enabled struct, raw offset/duration ms, adapter_ref + generation.
- New `internal/playback` package with a `Dispatcher` that owns the snapshot + provider-lookup + dispatch dance. `/ui/playback/{action,seek,banner}` and the chassis transport handlers both delegate to it. HTML response shape for `/ui/playback/*` is byte-identical.
- New typed sentinels in `internal/adapters/playback.go`: `ErrActiveSessionChanged`, `ErrPlaybackActionUnsupported`. Three playback providers (streams, torrent, url) migrated to wrap them.
- Chassis adds two narrow interfaces (`TransportViewer`, `TransportController`) satisfied structurally by `*playback.Dispatcher`. Same pattern as Spec 2's `SessionViewer` and Spec 4's `VisualizerViewer`.
- `transport.js` (~150 lines) attaches to Spec 4's shared `window.Chassis.events.source`, no `vfd-live.js` edits required.
- `transport.html` adds `data-transport-*` attribute hooks, two `data-state-icon` spans for the CSS-driven icon swap, raw `data-transport-offset-ms`/`-duration-ms` for seek math.
- `shell.html` adds `chassis-adapter-ref` and `chassis-generation` meta tags, while `transport.html` cold-renders `data-transport-state`, so the first click works without waiting for the first SSE event.

## Tests

- 20+ new Layer 1 tests (dispatcher: source-first policy, no-extra-snapshot, sentinel returns, clamp, legacy normalization; chassis: envelope JSON, transportChanged 15 deltas, transport SSE emission, POST handler status mapping, template hooks, smoke handler).
- Layer 3 integration coverage for POST dispatch, stale-generation 409, unsupported 422, bad seek offset 400, cross-site 403 + no-store, seek dispatch, GET 405, `/ui` non-shadowing, and POST-driven SSE reflection.
- Existing `/ui` playback tests + Spec 2/4 chassis tests stay green; only event-count assertions adjust from 3 → 4 events on initial connect.
- `TestProductionImports_NoCrossPackageCoupling` extended with three new rules enforcing the no-cycle invariant for `internal/playback`.

## Manual verification

Performed at 1920px in Chrome current. Screenshots attached.

- [ ] Live transport row during an active cast
- [ ] EventStream tab showing state/vfd/visualizer/transport events
- [ ] 204 POST response in Network tab
- [ ] 409 stale-generation POST in Network tab

State transitions observed <1s of click. Drag-seek + click-seek both confirmed. Multi-tab sync confirmed (5 tabs). Cross-site POST simulation returns 403.

## What's not in (deferred per spec)

- Telemetry meters — Spec 5.
- Source cluster / catalog / preset bank — Phase 3.
- Settings drawer (gear button stays inert) — Spec 8.
- Volume control — no chassis equivalent yet.
- Cross-link / deletion of `/ui/*` — final cutover spec.
```

- [ ] **Step 6: Open the PR**

```bash
git push -u origin feature/receiver-chassis-transport
gh pr create --title "Phase 1 / Spec 3: receiver chassis transport controls" --body "$(cat <pr-body.md)"
```

(Or your normal PR workflow.)

---

## Self-Review

Cross-checking the spec sections against tasks:

**Goals (spec §Goals 1-5):**
- POST endpoints + JSON / 204 contract → **Tasks 12-13.**
- New `transport` SSE event → **Tasks 9, 11.**
- Dispatcher encapsulation → **Tasks 2-3, 5-6.**
- `transport.js` client → **Task 15.**
- `/ui/playback` refactor preserves response shape → **Tasks 5-6.**

**Architecture (spec §Architecture):**
- `Dispatcher` with `PlaybackView` / `PlaybackViewForSnapshot` / `HandlePlaybackAction` → **Tasks 2-3.**
- `TransportViewer` / `TransportController` interfaces → **Task 7.**
- `Config` fields → **Task 7.**
- Sentinels + provider migration → **Task 1.**
- Source-first policy + legacy fallback → **Task 2.**
- Locking invariant (one StatusHomeView per tick) → **Tasks 2-3** (`PlaybackViewForSnapshot` signature), **Task 11** (refresher wires through).

**`TransportData` migration (spec §`ReceiverPageData.Transport`):**
- Field rename + new fields + idle placeholders → **Task 8.**
- State derivation pipeline → **Task 10.**
- Actions-enabled mapping → **Task 10.**

**SSE Wire Protocol (spec §SSE Wire Protocol):**
- `transport` envelope + flattener → **Task 9.**
- `transportChanged` diff → **Task 9.**
- Initial-snapshot four-record sequence → **Task 11.**

**POST Endpoint Contracts (spec §POST Endpoint Contracts):**
- `/receiver/transport/action` → **Task 12.**
- `/receiver/transport/seek` → **Task 13.**
- Negative-offset clamp in dispatcher → **Task 3.**
- 204 success, JSON errors, status mapping → **Tasks 12-13.**
- requireSameOrigin wrapping → **Task 16.**

**Client-Side Strategy (spec §Client-Side):**
- Template hooks → **Task 14.**
- CSS icon swap + disabled-seek guard → **Task 14.**
- Meta tags for initial generation handoff → **Task 14.**
- `transport.js` attach pattern → **Task 15.**
- Pause/Resume click derivation + stopped-state guard → **Task 15.**
- Seek drag UX → **Task 15.**

**Testing Approach (spec §Testing):**
- Layer 1: Tasks 1-13 each lead with the failing test.
- Layer 2 (template): **Task 14** + **Task 15** (smoke).
- Layer 3 (integration): **Task 17.**
- Manual verification: **Task 18.**

**Migration & Rollout (spec §Migration):**
- Coexistence with `/ui/*` → **Tasks 5-6** (refactor preserves response shape).
- Mount order preserved → **Task 16.**
- Cross-package import lint → **Task 16.**
- No new config fields → satisfied by omission throughout.

**Design Decisions Worth Revisiting (spec §):**
- Dispatcher in new package (no import cycle) → **Tasks 2-3.**
- 204 + JSON errors matching Spec 4 → **Tasks 12-13.**
- One stream / multiple named events → **Task 11** (transport joins state/vfd/visualizer).
- Pause/Resume as one button with CSS swap → **Task 14** + **Task 15.**

**Placeholder scan:** None remaining. Every step has concrete code, exact commands, expected output. The remaining notes-to-implementer are bounded instructions about matching existing local test fixtures or extending existing integration fakes, not unresolved design placeholders.

**Type consistency:**
- `Dispatcher` exposes `PlaybackView`, `PlaybackViewForSnapshot`, `HandlePlaybackAction` — consistent across Tasks 2, 3, 5, 6, 7, 11, 12, 13.
- `TransportViewer.PlaybackViewForSnapshot` (snapshot variant only) — Task 7, consumed in Task 10.
- `TransportController.HandlePlaybackAction` — Task 7, consumed in Tasks 12-13.
- `TransportData` fields (State, SeekFillPercent, ElapsedTime, TotalTime, PercentPlayed, OffsetMS, DurationMS, ActionsEnabled, AdapterRef, Generation) — consistent Tasks 8-11.
- `ActionsEnabled` fields (Previous, Next, PauseResume, Stop, Replay, Seek) — consistent Tasks 8-10, 14-15.
- `actionsEnabledE` wire-format struct — Task 9, used in Task 11.
- `transportEnvelope` / `transportEnvelopeFrom` / `transportChanged` — Task 9, consumed in Task 11.
- `writeJSONError` (Spec 4) — used in Tasks 12-13.
- `requireSameOrigin` (Spec 4) — used in Task 16.
- `errors.Is(err, adapters.ErrActiveSessionChanged | adapters.ErrPlaybackActionUnsupported)` — Task 3, Tasks 12-13.

No drift detected after the review fixes above.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-22-receiver-chassis-transport.md`. Two execution options:

**1. Subagent-Driven (recommended)** — Dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using `executing-plans`, batch execution with checkpoints.

Which approach?
