# Receiver Chassis Transport Controls — Phase 1 / Spec 3 Design

**Status:** Brainstormed; awaiting implementation plan.
**Scope:** Second sub-project of Phase 1 (Live Console). Wires the chassis transport row (previous / next / pause-resume / stop / replay / seek-bar) to the bridge's playback dispatch via two new POST endpoints, and pushes a new `transport` SSE event on the Spec 2 stream so multi-tab views stay in sync.
**Repo location:** Committed under `docs/superpowers/specs/`. That directory is normally gitignored (`.gitignore` line 35); this spec is force-added per the convention established by the Phase 0 and Spec 2 design docs.

## Background

[Phase 0](2026-05-21-receiver-chassis-foundation-design.md) shipped the static chassis at `/receiver`. [Spec 2](2026-05-21-receiver-chassis-vfd-live-design.md) wired the VFD to live bridge state via SSE at `GET /receiver/events`. This spec is the second of three Phase 1 sub-specs that finish the live console:

- **Spec 2 (done).** VFD live + idle display. Established the SSE transport.
- **Spec 3 (this).** Transport controls — play / pause / stop / seek / previous / next / replay wired to the existing global playback dispatch.
- **Spec 4 (in parallel).** Visualizer mode selector wired to `bridge.visualizer.mode`.

The existing `/ui/playback/action` endpoint dispatches via `adapter.HandlePlaybackAction()` after a snapshot + provider-lookup dance done inline in `internal/ui/playback.go`. Spec 3 hoists that dispatch into a cycle-free `internal/playback` dispatcher service that composes the core status viewer with `adapters.Registry`, so both `/ui` and the chassis can call the same method without teaching `internal/core` about adapter types. The chassis stays narrow: two small interfaces, two new SSE-friendly POST handlers, one new client-side script.

## Goals

1. Mount `POST /receiver/transport/action` and `POST /receiver/transport/seek` — JSON-responding endpoints that mirror `/ui/playback/{action,seek}`'s URL shape and `adapters.PlaybackActionRequest` contract, wrapped in chassis same-origin protection.
2. Add a `transport` named event to the existing SSE stream at `GET /receiver/events`. Payload carries the playing/paused/stopped state, server-formatted seek-bar fields, and a per-action enabled struct so the client can grey out unsupported buttons.
3. Encapsulate the snapshot + provider-lookup + dispatch dance currently inline in `internal/ui/playback.go` into two new methods on a shared `internal/playback.Dispatcher`: `PlaybackView(ctx)` (read) and `HandlePlaybackAction(ctx, req)` (write). The chassis depends on these structurally via new narrow interfaces (`TransportViewer`, `TransportController`).
4. Ship `internal/chassis/static/transport.js` — a ~150-line vanilla ES2022 client that subscribes to the new `transport` SSE event (via Spec 4's shared `window.Chassis.events.source` + `chassis:eventsource` CustomEvent surface), swaps DOM via new `data-transport-*` attribute hooks, and POSTs to the new endpoints on button click + seek-bar drag using raw seek milliseconds supplied by the server.
5. Refactor `/ui/playback`'s inline dispatch to delegate to the shared dispatcher. HTTP response shape for `/ui/playback/*` stays byte-identical so existing `/ui` tests pass without modification.

## Non-goals

- Visualizer mode wiring (`bridge.visualizer.mode`) — Spec 4.
- Telemetry events (spectrum / goniometer / throughput / ACK / audio scope) — Spec 5.
- Source cluster / input cluster / catalog / preset bank wiring — Phase 3.
- Settings drawer (the gear button in `transport.html`) — Spec 8.
- Volume control (`/ui/playback/volume` has no chassis equivalent yet). The chassis transport row mockup includes no volume widget; if a later spec adds one, that spec owns the wiring.
- Quick-cast / cast drawer integration (`/ui/playback/quick-cast`) — Phase 3.
- Cross-link or deletion of `/ui/*` — final cutover spec.
- Per-spec duplication of `playbackProviderForSnapshot` in both `/ui` and chassis — the shared dispatcher owns that logic now; the cutover spec can re-evaluate whether it should remain after `/ui` is deleted.

## Done When

- `POST /receiver/transport/action` accepts a same-origin form with `adapter_ref`, `generation`, `action` (one of `pause|resume|stop|previous|next|replay`) and returns `204 No Content` on success. (Matches Spec 4's `/receiver/visualizer` convention — successful POSTs return 204 with no body; the SSE stream pushes the resulting state change within ~250 ms.)
- `POST /receiver/transport/seek` accepts a same-origin form with `adapter_ref`, `generation`, `offset_ms` and returns the same JSON shape on success.
- Both routes return `409` on `generation` mismatch (stale session), `422` on unsupported action, `400` on malformed form, `500` on unexpected provider error — all with `{"error": "<message>"}` (Spec 4's `writeJSONError` shape).
- Cross-site, missing, or `Sec-Fetch-Site: none` POST attempts receive `403 application/json` with `{"error": "cross-site request blocked"}` from Spec 4's `requireSameOrigin` middleware, before form parsing or dispatch.
- A new SSE event `transport` is emitted on `/receiver/events` whenever any of state, seekFillPercent, elapsedTime, totalTime, percentPlayed, offsetMs, durationMs, actionsEnabled, adapterRef, or generation changes. Initial-snapshot sequence on connect now includes a `transport` event after the existing `state` and `vfd` events.
- Clicking pause / resume / stop / previous / next / replay in `/receiver` dispatches to the owning adapter via `playback.Dispatcher.HandlePlaybackAction` and reflects on the chassis within ~500 ms (one SSE tick).
- Dragging the seek-bar to a new position dispatches a seek with the resolved `offset_ms` and reflects on the chassis within ~500 ms.
- Multiple chassis tabs viewing the same active cast see synchronized button state, seek-bar position, and time labels.
- `/ui/playback/*` continues to serve the same HTML responses as before; `/ui/playback/banner` polling pattern is unchanged.
- `internal/chassis/` still has zero production imports of `internal/ui` / `internal/uiserver`. Phase 0's `TestProductionImports_NoCrossPackageCoupling` stays green.
- The full lint + test + race + integration matrix is green: `make lint && make test && go test -race ./... && make test-integration`.

## Architecture

### Files added

```
internal/playback/
├── dispatcher.go           # NEW: shared playback view + action dispatcher
└── dispatcher_test.go      # NEW: source-first lookup, stale-session, locking tests

internal/chassis/
├── transport.go            # NEW: POST handlers (handleTransportAction, handleTransportSeek)
├── transport_test.go       # NEW: Layer 1 handler tests
└── static/
    └── transport.js        # NEW: EventSource subscriber + button/seek click handlers
```

### Spec 4 reuse (already merged on main)

Spec 4 (visualizer mode) shipped before this spec writes its plan, and it established the conventions Spec 3 follows. Reused as-is:

- `internal/chassis/sameorigin.go` — `requireSameOrigin(next http.Handler)` middleware. Spec 3 wraps the two new POST routes through it.
- `internal/chassis/visualizer.go` — exports `writeJSONError(w, status, msg)`. Spec 3 calls it for all error responses. If a future spec adds a third JSON-responding handler, the implementation plan may hoist these helpers to a dedicated `internal/chassis/jsonresp.go`; not in scope for this spec.
- `internal/chassis/static/vfd-live.js` — Spec 4 already exposes `window.Chassis.events.source` (after `connect()`) and dispatches a `chassis:eventsource` CustomEvent on `document` so sibling scripts can attach `source.addEventListener('<name>', fn)` whether they boot before or after the EventSource opens. `transport.js` follows the same pattern as `visualizer-bank.js`; **no edits to `vfd-live.js` are required**.

### Files modified

- `internal/adapters/playback.go` — add two `errors.Is`-friendly sentinels: `ErrActiveSessionChanged = errors.New(ErrActiveSessionChangedMessage)` (so existing string-comparing callers and new typed-error callers both work) and `ErrPlaybackActionUnsupported = errors.New("active adapter does not support playback action")`. Existing playback providers do not need to migrate immediately; the dispatcher normalizes exact-string matches of the legacy `"active session changed"` message into the typed sentinel during rollout.
- `internal/core/manager.go` — no method additions. The dispatcher reads via `Manager.StatusHomeView()` (already exists per Spec 2) and writes via the existing `Manager.PauseIfSession / PlayIfSession / StopIfSession / SeekToIfSession` guarded helpers (already exist). No new methods on `Manager`. This avoids putting `internal/adapters` types onto `core.Manager` and keeps `internal/core`'s import surface unchanged.
- `internal/ui/server.go` — `Config` gains optional `Playback PlaybackService` (a package-local interface declaring the two dispatcher methods). When nil, `New` constructs `playback.NewDispatcher(cfg.StatusViewer, cfg.Registry)` so existing tests that only provide `StatusViewer` + `Registry` keep working.
- `internal/ui/playback.go` — the inline `playbackProviderForSnapshot` is deleted; all callers migrate to the dispatcher. There are **two distinct call sites** to update (not "two endpoints"): `handlePlaybackMutation` at line 159 dispatches the action via `s.playback.HandlePlaybackAction(ctx, req)`; `buildPlaybackBannerData` at line 335 fetches the provider's view via `s.playback.PlaybackView(ctx)`, then composes the remaining banner-specific fields (`QuickCastTabs`, `OutputVolume`, `ReadOnly`, `PollTrigger`, `Title`/`Subtitle`/`SourceDisplay` overrides) locally exactly as today. `buildPlaybackBannerData` itself is called from **four** places (`handlePlaybackBanner`, `handlePlaybackVolume`, `renderPlaybackMessage`, `shellDataForPath`); none of those four callers change — only the function's internals do. HTML response shape for `/ui/playback/*` is preserved byte-identically (same `renderPlaybackMessage` writes the same banner partial); `internal/ui/playback_test.go` stays green without modification.
- `internal/chassis/server.go` — `Config` gains two fields: `TransportViewer TransportViewer` and `TransportController TransportController`. Both optional (nil → idle-only controls with core-derived state when `Session` exists). Stored on `Server` for handlers. `Mount` registers the two new POST routes through `requireSameOrigin` (matches Spec 4's pattern for `/receiver/visualizer`).
- `internal/chassis/session.go` — adds the `TransportViewer` and `TransportController` interface declarations, sibling to the existing `SessionViewer`.
- `internal/chassis/data.go` — `ReceiverPageData` gains a `Transport TransportData` field, replacing the existing `Transport TransportData` struct shipped in Phase 0. The Phase-0 struct had `PlayState string`, `ElapsedTime string`, `TotalTime string`, `PercentPlayed string`, `SeekFillPercent int`. The new struct **renames `PlayState` → `State`**, **adds** `OffsetMS int`, `DurationMS int`, `ActionsEnabled ActionsEnabled`, `AdapterRef string`, `Generation uint64`, and **changes the idle placeholders** from `"--:--"` / `"---"` to empty strings (the templates render empty strings as the segmented display's ghost-only state, which matches the desired idle visual). `idleSnapshot()` is updated to the new values.
- `internal/chassis/chassis_test.go` — the existing Phase-0 `TestIdleSnapshot_*` fixtures assert the old `PlayState` / `"--:--"` / `"---"` values; update those assertions to the new field names and empty-string placeholders. Add new template assertions for the `data-transport-*` hooks (see Testing section).
- `internal/chassis/events.go` — `transportEnvelope` type; `transportEnvelopeFrom(TransportData)` flattener; `transportChanged(a, b TransportData) bool` field-by-field diff. `handleEvents` diff loop emits `transport` events on change.
- `internal/chassis/events_test.go` — existing Spec 2 tests that count events on initial connect (e.g. `TestHandleEvents_EmitsInitialSnapshotOnConnect`, `TestSnapshotCache_SeedsSynchronouslyBeforeFirstSSE`) update to expect three events (`state`, `vfd`, `transport`) instead of two.
- `internal/chassis/session.go` — `snapshotFromSession` now calls `SessionViewer.StatusHomeView()` once and passes the resulting snapshot to `TransportViewer.PlaybackViewForSnapshot(ctx, snap)` (see Locking discussion below — the for-snapshot variant skips re-acquiring `Manager.mu`). Maps adapter view + core state into `TransportData`.
- `internal/chassis/templates/transport.html` — replaces inline icon glyphs on the pause button with two `data-state-icon` spans; adds `data-transport-state`, `data-transport-action`, `data-transport-seek*`, `data-transport-elapsed`, `data-transport-total`, `data-transport-percent`, raw `data-transport-offset-ms` / `data-transport-duration-ms`, and `{{if not ...}}disabled{{end}}` markup.
- `internal/chassis/templates/shell.html` — adds `<script defer src="/receiver/static/transport.js?v={{.Version}}">` after `vfd-live.js`. Adds `<meta name="chassis-adapter-ref" content="{{.Transport.AdapterRef}}">` and `<meta name="chassis-generation" content="{{.Transport.Generation}}">` to `<head>` so the client knows the initial generation without waiting for the first SSE event.
- `internal/chassis/static/chassis.css` — adds CSS rules for the pause/resume icon swap based on `[data-transport-state]` and the disabled-seek pointer-events guard.
- `internal/chassis/import_check_test.go` — extends the `rules` table with three new entries: `internal/playback` forbidden from importing `internal/chassis`, `internal/ui`, `internal/uiserver`; `internal/core` forbidden from importing `internal/playback`; `internal/adapters` forbidden from importing `internal/playback`. These assert the no-import-cycle invariant the dispatcher's placement depends on.
- `cmd/mister-groovy-relay/main.go` — constructs `playbackDispatcher := playback.NewDispatcher(coreMgr, reg)` once, passes it to `ui.Config.Playback`, `chassis.Config.TransportViewer`, and `chassis.Config.TransportController`.

### `TransportViewer` and `TransportController` interfaces

```go
// internal/chassis/session.go (extending the file Spec 2 introduced)
package chassis

import (
    "context"

    "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// TransportViewer is the narrow read-only view of bridge playback state.
// *playback.Dispatcher satisfies this structurally via its PlaybackView method.
// Tests inject fakes; production wires the shared dispatcher.
type TransportViewer interface {
    PlaybackView(ctx context.Context) (adapters.PlaybackBannerAdapterView, bool)
}

// TransportController is the narrow write surface for playback actions.
// *playback.Dispatcher satisfies this structurally via its HandlePlaybackAction
// method. /ui/playback's handler delegates to the same dispatcher method,
// so the chassis and /ui dispatch via a single path.
type TransportController interface {
    HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error)
}
```

These join the existing `SessionViewer`. Three interfaces total on `chassis.Config` after Spec 3.

### `playback.Dispatcher.PlaybackView` and `HandlePlaybackAction`

```go
// internal/playback/dispatcher.go
package playback

type StatusViewer interface {
    StatusHomeView() core.StatusHomeView
}

type Dispatcher struct {
    status   StatusViewer
    registry *adapters.Registry
}

func NewDispatcher(status StatusViewer, registry *adapters.Registry) *Dispatcher { ... }

// PlaybackView snapshots the active session via StatusViewer, looks up the
// owning playback provider via the source-first policy, and returns the
// provider's banner view. Returns (_, false) when there is no view to render
// — caller should render read-only / disabled controls. The bool deliberately
// collapses three distinct underlying conditions (no active session / no
// playback provider for the active adapter / provider returned owns=false):
// callers do not distinguish among them visually, so the dispatcher does
// not propagate the distinction. Tests that need the distinction can assert
// against the StatusViewer + Registry directly.
func (d *Dispatcher) PlaybackView(ctx context.Context) (adapters.PlaybackBannerAdapterView, bool) { ... }

// PlaybackViewForSnapshot is the snapshot-already-acquired variant of
// PlaybackView. Callers that already have a fresh core.StatusHomeView (e.g.
// the chassis snapshot refresher, which acquires one per tick anyway) pass
// it through to avoid a second Manager.mu acquisition. Same return contract
// as PlaybackView.
func (d *Dispatcher) PlaybackViewForSnapshot(ctx context.Context, snap core.StatusHomeView) (adapters.PlaybackBannerAdapterView, bool) { ... }

// HandlePlaybackAction snapshots the active session, validates the caller's
// adapter_ref + generation against that snapshot, looks up the owning provider,
// and dispatches the action. Providers remain the final authority and must
// re-check ownership under their own locks or by calling core's guarded
// PauseIfSession / PlayIfSession / StopIfSession / SeekToIfSession helpers.
//
// Returns adapters.ErrActiveSessionChanged sentinel on generation mismatch
// (callers map to HTTP 409). Returns or wraps ErrPlaybackActionUnsupported
// when the active adapter doesn't expose playback controls or rejects the verb
// (callers map to HTTP 422). Provider errors that don't match either sentinel
// bubble up as-is (callers map to 500).
func (d *Dispatcher) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) { ... }
```

The private `playbackProviderForSnapshot` logic moves from `internal/ui/playback.go` into `internal/playback/dispatcher.go` (as `d.playbackProviderForSnapshot`). The source-first policy with legacy adapter-ref-prefix fallback is preserved exactly. `internal/ui/playback.go`'s `playbackProviderForSnapshot` is deleted and the call sites use `s.playback`.

Package-boundary invariant: `internal/core` does not import `internal/adapters` or `internal/playback`. The dispatcher sits above both packages and is wired by `main.go`; this avoids the import cycle that would result from putting `adapters.PlaybackActionRequest` or `adapters.Registry` on `core.Manager`.

Locking invariant: neither dispatcher method holds `core.Manager.mu` across the provider call. `StatusHomeView()` snapshots under the manager's internal lock and returns a value copy; the dispatcher validates `adapter_ref + generation` against that snapshot before lookup, then dispatches. Providers remain the final stale-session authority and must revalidate under their own locks or core's guarded helpers. This preserves the existing invariant that `Manager.mu` is never held across provider work.

Provider-view contract: `PlaybackBanner(ctx, snap)` must be state-cache-only and non-blocking. It may take adapter-local locks briefly, but must not perform fresh network I/O. The 250 ms chassis cache refresher calls `PlaybackView(ctx)` on every tick; adapters that need remote state must keep a cached view updated elsewhere.

### Sentinel errors: `ErrActiveSessionChanged` and `ErrPlaybackActionUnsupported`

The existing `internal/adapters/playback.go` exports `ErrActiveSessionChangedMessage` (a string constant). Spec 3 adds two `errors.Is`-friendly sentinel `error` variants:

```go
// internal/adapters/playback.go
var (
    ErrActiveSessionChanged      = errors.New(ErrActiveSessionChangedMessage)
    ErrPlaybackActionUnsupported = errors.New("active adapter does not support playback action")
)
```

`Dispatcher.HandlePlaybackAction` returns:
- `ErrActiveSessionChanged` when the caller's `adapter_ref + generation` doesn't match the active session (chassis maps to 409; `/ui` renders the existing inline error).
- `ErrPlaybackActionUnsupported` (possibly wrapped) when the active adapter has no `PlaybackControlProvider` registered or the provider rejects the action verb (chassis maps to 422; `/ui` renders an inline error).

Both consumers use `errors.Is(err, sentinel)` to detect. Provider-emitted errors that don't match either sentinel bubble through unchanged (chassis maps to 500).

Provider migration requirement: existing playback providers that currently return `fmt.Errorf("%s", adapters.ErrActiveSessionChangedMessage)` must switch to returning or wrapping `adapters.ErrActiveSessionChanged`. Unsupported verbs should return or wrap `ErrPlaybackActionUnsupported` instead of a plain `"unknown playback action"` error. The dispatcher includes a narrow compatibility normalizer for the exact legacy active-session message so a missed provider still maps to 409, but tests should assert providers use the sentinel directly.

### `ReceiverPageData.Transport` and `TransportData`

```go
// internal/chassis/data.go (extending the existing data types)

type TransportData struct {
    State           string         // "playing" | "paused" | "stopped"
    SeekFillPercent int            // 0..100
    ElapsedTime     string         // "04:23"
    TotalTime       string         // "09:56" or "--:--"
    PercentPlayed   string         // "44%"
    OffsetMS        int            // raw position for seek math; 0 on idle/unknown
    DurationMS      int            // raw duration for seek math; 0 on unknown
    ActionsEnabled  ActionsEnabled
    AdapterRef      string         // empty on idle
    Generation      uint64         // 0 on idle
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

Six explicit bools (not a map) so the diff is six trivial comparisons and the wire-format struct tags are stable.

### State and field derivation

`snapshotFromSession` is extended to populate `Transport`. The mapping pipeline:

1. `SessionViewer.StatusHomeView()` → `core.State`, `Position`, `Duration`, `AdapterRef`, `Generation`, etc.
2. `TransportViewer.PlaybackView(ctx)` → `adapters.PlaybackBannerAdapterView` with `Actions []PlaybackAction` and `Seek *PlaybackSeek`.
3. Compose:
   - `Transport.State`: from `core.State` via the mapping table below. Idle → "stopped"; playing → "playing"; paused → "paused".
   - `Transport.AdapterRef`, `Transport.Generation`: from `StatusHomeView`.
   - `Transport.SeekFillPercent`: integer percentage `100 * Position.Milliseconds() / Duration.Milliseconds()`, clamped to `[0, 100]`. Zero when duration is 0 or unknown.
   - `Transport.ElapsedTime`: `formatPlaybackPosition(StatusHomeView.Position)` (Spec 2 helper).
   - `Transport.TotalTime`: `formatPlaybackDuration(StatusHomeView.Duration)` (Spec 2 helper). `--:--` for unknown duration.
   - `Transport.PercentPlayed`: `fmt.Sprintf("%d%%", SeekFillPercent)`. Empty string when idle.
   - `Transport.OffsetMS`, `Transport.DurationMS`: prefer `PlaybackSeek.OffsetMS` / `DurationMS` when the provider supplies `Seek`; otherwise derive from `StatusHomeView.Position` / `Duration`. Clamp negative values to zero. These raw fields are what `transport.js` uses for seek math; the formatted display strings are never parsed.
   - `Transport.ActionsEnabled`: derived from the adapter view's Actions and Seek. See "Actions enabled derivation" below.

If `TransportViewer` is nil or `PlaybackView` returns `(_, false)`, the chassis still renders a valid Transport from `StatusHomeView`: state, elapsed time, total time, percent, offset, duration, adapter ref, and generation remain accurate for active read-only casts; only all six `ActionsEnabled` flags become false. If `SessionViewer` itself is nil, the chassis falls back to idle transport (`state="stopped"`, blank time fields, zero raw milliseconds, no adapter ref/generation), matching Spec 2's idle-only precedent.

### `core.State` → `Transport.State` mapping

| `core.State` | `Transport.State` |
|---|---|
| `StateIdle` | `"stopped"` |
| `StatePlaying` | `"playing"` |
| `StatePaused` | `"paused"` |

Spec 2 mapped paused → chassis "live"; Spec 3 maps it more finely to transport "paused". The two mappings are independent: chassis body class stays `live` during pause (Spec 2 invariant), while the transport-row button icon shows resume (Spec 3 contract).

**Value-space distinction (important).** The Spec 2 `state` SSE event uses the value space `idle|live` (controls the body class). The new Spec 3 `transport` event uses an independent value space `playing|paused|stopped` (controls the transport-row icons and disabled flags). Both events happen to have a `"state"` field on their payload, but the value vocabularies are disjoint — a client must never cross-wire them. The layer 1 test list includes `TestTransportEnvelope_StateValueSpaceDistinctFromBodyClass` to lock this in.

### Actions enabled derivation

The adapter's `PlaybackBannerAdapterView.Actions` is a `[]PlaybackAction` where each action has an `ID` (matching the `PlaybackActionPause / Resume / Stop / Replay / Previous / Next / Seek` constants) and an `Enabled bool`. Chassis maps these to the six `ActionsEnabled` bools:

| `ActionsEnabled` field | Source |
|---|---|
| `Previous` | `actions["previous"].Enabled`, defaults to `false` if absent. |
| `Next` | `actions["next"].Enabled`. |
| `PauseResume` | `actions["pause"].Enabled || actions["resume"].Enabled`. The adapter surfaces one or the other depending on current playing state; chassis enables the button if either is currently surfaced. |
| `Stop` | `actions["stop"].Enabled`. |
| `Replay` | `actions["replay"].Enabled`. |
| `Seek` | `view.Seek != nil && view.Seek.Enabled`. |

When the underlying call returned `(_, false)` (no provider), all six are `false`.

### `transportChanged` helper

```go
// internal/chassis/events.go

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

`ActionsEnabled` is a value-type struct of six bools; `==` works directly. The function performs **nine field-level comparisons** (eight scalars + one `ActionsEnabled` struct equality). End-to-end the wire-format surface has **14 testable deltas**: eight scalar fields plus six `ActionsEnabled` bool fields. The diff test (`TestTransportChanged_DetectsEveryFieldDelta`) is structured as 14 subtests, one per testable delta. Matches `vfdChanged`'s explicit-compare precedent.

### Snapshot cache integration

The Spec 2 snapshot cache (`Server.cache *snapshotCache`) already holds a full `ReceiverPageData`. Spec 3 extends `snapshotFromSession` to populate the new `Transport` field; the cache refresher's tick body is unchanged. Per-tick work is now one `Manager.StatusHomeView()` call plus one `Dispatcher.PlaybackViewForSnapshot(ctx, snap)` call. The for-snapshot variant deliberately accepts the caller's already-acquired snapshot so the per-tick `Manager.mu` acquisition count stays at exactly **one** — 4 Hz total regardless of connected-tab count, matching Spec 2's invariant. POST handlers, which don't have a fresh snapshot in hand, use the regular `Dispatcher.PlaybackView(ctx)` / `Dispatcher.HandlePlaybackAction(ctx, req)` variants that snapshot internally; those add per-click `Manager.mu` acquisitions on the order of user input (a handful per second worst case), well below the 4 Hz baseline.

`handleEvents` initial-snapshot block extends to a third emit:

```go
last := s.cache.Get()
if err := emit(w, "state", stateEnvelope{State: string(last.State)}); err != nil { return }
if err := emit(w, "vfd", vfdEnvelopeFrom(last.VFD)); err != nil { return }
if err := emit(w, "transport", transportEnvelopeFrom(last.Transport)); err != nil { return }
flusher.Flush()
```

The diff loop gains a third `if` block:

```go
if transportChanged(curr.Transport, last.Transport) {
    if err := emit(w, "transport", transportEnvelopeFrom(curr.Transport)); err != nil { return }
    last.Transport = curr.Transport
}
```

## SSE Wire Protocol

### New event: `transport`

```
event: transport
data: {"state":"playing","seekFillPercent":44,"elapsedTime":"04:23","totalTime":"09:56","percentPlayed":"44%","offsetMs":263000,"durationMs":596000,"actionsEnabled":{"previous":true,"next":true,"pauseResume":true,"stop":true,"replay":true,"seek":true},"adapterRef":"plex:abc123","generation":42}
```

`Content-Type: text/event-stream`, same connection headers as Spec 2. The event payload uses `encoding/json` with explicit `json:"<camelCase>"` struct tags on every field. Idle envelope:

```
event: transport
data: {"state":"stopped","seekFillPercent":0,"elapsedTime":"","totalTime":"","percentPlayed":"","offsetMs":0,"durationMs":0,"actionsEnabled":{"previous":false,"next":false,"pauseResume":false,"stop":false,"replay":false,"seek":false},"adapterRef":"","generation":0}
```

### Initial-snapshot sequence

Extends Spec 2's sequence by one record:

```
retry: 3000

event: state
data: {"state":"<current>"}

event: vfd
data: {...}

event: transport
data: {...}

```

The trailing blank line terminates the third record. SSE parsing on the client (`EventSource`) handles all three events with distinct listeners.

### Envelope struct (Go)

```go
// internal/chassis/events.go

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
```

The internal `TransportData` struct uses Go-idiomatic PascalCase; the envelope's `actionsEnabledE` exists solely to emit camelCase JSON. Spec 2's `vfdEnvelopeFrom` precedent for keeping wire and data types separate.

## POST Endpoint Contracts

### Route additions in `Mount`

```go
// internal/chassis/server.go Mount additions
mux.Handle("POST /receiver/transport/action",
    requireSameOrigin(http.HandlerFunc(s.handleTransportAction)))
mux.Handle("POST /receiver/transport/seek",
    requireSameOrigin(http.HandlerFunc(s.handleTransportSeek)))
```

Method-aware Go 1.22+ mux; non-POST requests fall through to a default 405.

### Same-origin protection

Transport POSTs reuse Spec 4's existing `requireSameOrigin` middleware at `internal/chassis/sameorigin.go` — same contract, same `{"error": "cross-site request blocked"}` 403 response shape, same `Sec-Fetch-Site: same-origin|same-site` allowlist. Spec 3 adds no new same-origin code; it just wraps the new POST routes through it in `Mount`.

The middleware is intentionally stricter than `/ui/*`'s CSRF middleware: no browser-extension bypass, no `Origin` fallback, no missing-header acceptance, and no `Sec-Fetch-Site: none`. The chassis POST endpoints are driven by bundled first-party JS; non-browser callers must opt in with `-H "Sec-Fetch-Site: same-origin"`. Spec 4's `sameorigin_test.go` already covers `same-origin` allowed, `same-site` allowed, `cross-site` rejected, `none` rejected, and missing header rejected — no new same-origin tests required at the transport-handler layer.

### `POST /receiver/transport/action`

| Form field | Type | Required | Notes |
|---|---|---|---|
| `adapter_ref` | string | yes | Identifies the cast the action targets. |
| `generation` | uint64 | yes | Monotonic per-cast. Stale → 409. |
| `action` | string | yes | One of `pause`, `resume`, `stop`, `previous`, `next`, `replay`. Rejects `seek` (must use `/seek` route) with 400. |

### `POST /receiver/transport/seek`

| Form field | Type | Required | Notes |
|---|---|---|---|
| `adapter_ref` | string | yes | Same as action. |
| `generation` | uint64 | yes | Same as action. |
| `offset_ms` | int | yes | The dispatcher (`Dispatcher.HandlePlaybackAction`) clamps negative `OffsetMS` to 0 before dispatching to the provider, so both `/ui` and chassis get identical handling without per-handler duplication. Out-of-range vs duration is the provider's call. |

The seek route sets `action = "seek"` server-side; client never passes it.

### Response envelope

Success: `204 No Content` with no body. The SSE `transport` event arrives within ~250 ms and is the canonical signal of state change.

Error: `{"error": "<message>"}` via Spec 4's existing `writeJSONError(w, status, msg)` helper. `Content-Type: application/json` (no charset, matching Spec 4). `Cache-Control: no-store` on all responses.

### HTTP status mapping

| Status | Condition |
|---|---|
| `204` | Action accepted by the provider. No body. |
| `400` | Malformed form (missing/invalid field) or `action = "seek"` on the action route. |
| `403` | Missing or unsupported `Sec-Fetch-Site` on the same-origin guard (Spec 4's `requireSameOrigin`). |
| `405` | Wrong method (mux default for non-POST). |
| `409` | `generation` mismatch — `Dispatcher.HandlePlaybackAction` returned (or wrapped) `adapters.ErrActiveSessionChanged`. |
| `422` | No `PlaybackControlProvider` for the active adapter OR the provider returned an action-not-supported error (`ErrPlaybackActionUnsupported`). |
| `500` | Unexpected provider error. |

### Error-to-status mapping in the handler

```go
_, err := s.transport.HandlePlaybackAction(r.Context(), req)
switch {
case err == nil:
    w.WriteHeader(http.StatusNoContent)
case errors.Is(err, adapters.ErrActiveSessionChanged):
    writeJSONError(w, http.StatusConflict, "active session changed")
case errors.Is(err, adapters.ErrPlaybackActionUnsupported):
    writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
default:
    log.Printf("chassis: transport action failed: action=%q err=%v", req.Action, err)
    writeJSONError(w, http.StatusInternalServerError, "internal dispatch failure")
}
```

The chassis discards the `PlaybackActionResult` because 204 has no body; the SSE stream confirms the action took effect. `/ui` continues to use the result message for its HTML banner re-render.

The 500 path logs the underlying error server-side but returns a generic message to the client (matches Spec 4's visualizer-save 500 handling at `internal/chassis/visualizer.go:73-75`). The 409 path returns a stable message string ("active session changed") rather than the underlying error text so the client UI can recognize it without parsing.

The `ErrPlaybackActionUnsupported` sentinel is added by this spec to `internal/adapters/playback.go` alongside the existing active-session sentinel; both consumers (chassis and `/ui`) use `errors.Is` to detect.

`Dispatcher.HandlePlaybackAction` returns the underlying `PlaybackActionResult` (which carries a human-readable result message) to its caller. The chassis handler discards the message because the response is 204; `/ui`'s handler continues to use it for the banner re-render. The dispatcher's result type does not change.

### Validation flow (server-side)

1. Parse `r.ParseForm()`. Bad form → 400.
2. Read `adapter_ref` (non-empty), `generation` (uint64 > 0), `action` (non-empty, validated against allowed set or `seek` rejection). Bad fields → 400.
3. Build `adapters.PlaybackActionRequest`.
4. Call `s.transport.HandlePlaybackAction(r.Context(), req)`.
5. Map error to status per the table above.
6. Marshal response body.

The chassis does **not** re-check `actionsEnabled` server-side before dispatch. The provider self-validates under its own locks (this is the existing `/ui/playback.go:164-169` design rationale). `actionsEnabled` is a *display hint* for the client — never a security gate.

## Client-Side Strategy

### Template hooks on `transport.html`

The pause/resume button gains two `data-state-icon` spans (one per direction) so CSS can toggle visibility without JS textContent edits. The `transport-strip` parent gains `data-transport-state` for the CSS selector parent. All five buttons gain `data-transport-action` for the click delegation handler. The seek-bar elements gain `data-transport-seek`, `data-transport-seek-fill`, `data-transport-offset-ms`, and `data-transport-duration-ms`. The time spans gain `data-transport-elapsed`, `data-transport-total`, and `data-transport-percent`. Buttons gain `{{if not .Transport.ActionsEnabled.X}}disabled{{end}}` so the cold render reflects current capability.

Concrete template after the change:

```html
{{define "transport"}}
{{htmlComment "chassis:transport"}}
<div class="transport-strip" aria-label="Transport controls" data-transport-state="{{.Transport.State}}">
  <span class="strip-label">Transport</span>
  <div class="transport-row">
    <button class="trn" type="button" data-transport-action="previous"
      {{if not .Transport.ActionsEnabled.Previous}}disabled{{end}}
      aria-label="Previous" title="Previous">&#x23ee;</button>
    <button class="trn" type="button" data-transport-action="next"
      {{if not .Transport.ActionsEnabled.Next}}disabled{{end}}
      aria-label="Next" title="Next">&#x23ed;</button>
    <button class="trn primary" type="button" data-transport-action="pauseResume"
      {{if not .Transport.ActionsEnabled.PauseResume}}disabled{{end}}
      aria-label="Pause or resume" title="Pause / Resume">
      <span data-state-icon="playing">&#x23f8;</span>
      <span data-state-icon="paused">&#x25b6;</span>
    </button>
    <button class="trn" type="button" data-transport-action="stop"
      {{if not .Transport.ActionsEnabled.Stop}}disabled{{end}}
      aria-label="Stop" title="Stop">&#x23f9;</button>
    <button class="trn" type="button" data-transport-action="replay"
      {{if not .Transport.ActionsEnabled.Replay}}disabled{{end}}
      aria-label="Replay" title="Replay">&#x27f2;</button>
  </div>
  <div class="seek-bar" data-transport-seek role="progressbar"
    data-transport-offset-ms="{{.Transport.OffsetMS}}"
    data-transport-duration-ms="{{.Transport.DurationMS}}"
    aria-label="Cast position" aria-valuemin="0" aria-valuemax="100"
    aria-valuenow="{{.Transport.SeekFillPercent}}" title="Cast position">
    <div class="fill" data-transport-seek-fill style="width: {{.Transport.SeekFillPercent}}%"></div>
    <div class="head"><span class="grip"></span></div>
  </div>
  <div class="seek-time">
    <span class="seg-display"><span class="seg-ghost" aria-hidden="true">88:88</span><span class="seg-text" data-transport-elapsed>{{.Transport.ElapsedTime}}</span></span>
    <span class="sep">/</span>
    <span class="total seg-display"><span class="seg-ghost" aria-hidden="true">88:88</span><span class="seg-text" data-transport-total>{{.Transport.TotalTime}}</span></span>
    <span class="pct" data-transport-percent title="Playback position">{{.Transport.PercentPlayed}}</span>
  </div>
  <button class="gear-btn" id="gear-btn" type="button">&#x2699; Setup</button>
</div>
{{end}}
```

The Phase 0 gear button (`#gear-btn`) is unchanged and stays out of scope (Spec 8 owns settings).

### CSS additions

```css
/* in chassis.css, scoped under body.receiver */
body.receiver [data-transport-state="playing"] [data-state-icon="paused"] { display: none; }
body.receiver [data-transport-state="paused"]  [data-state-icon="playing"] { display: none; }
body.receiver [data-transport-state="stopped"] [data-state-icon="paused"] { display: none; }
body.receiver .transport-strip [data-transport-action]:disabled { opacity: 0.35; cursor: not-allowed; }
body.receiver .seek-bar[data-transport-seek-disabled] { pointer-events: none; opacity: 0.5; }
```

The pause icon is the default for `playing` and `stopped` (the resume icon is hidden). For `paused`, the playing icon is hidden so the resume icon shows. Buttons that the markup writes `disabled` on get a visual cue.

### Meta-tag handoff for `adapter_ref` + `generation`

`shell.html` `<head>` additions:

```html
<meta name="chassis-adapter-ref" content="{{.Transport.AdapterRef}}">
<meta name="chassis-generation"  content="{{.Transport.Generation}}">
```

On boot, `transport.js` reads these into closure variables. The SSE `transport` event payload includes `adapterRef` + `generation`, so the closure values are kept in sync as the cast changes (or stops) without a page reload.

### `internal/chassis/static/transport.js`

```javascript
(() => {
  'use strict';

  if (!window.Chassis || !window.Chassis.events) {
    console.warn('transport: window.Chassis.events missing; vfd-live.js failed to load?');
    return;
  }

  let adapterRef = '';
  let generation = 0;
  let transportState = 'stopped';
  let offsetMs = 0;
  let durationMs = 0;

  // Read initial state from meta tags so first click works even if
  // the SSE stream hasn't delivered a transport event yet.
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

      // Per-action enabled flags toggle the `disabled` attribute.
      const enabled = data.actionsEnabled || {};
      for (const action of ['previous', 'next', 'pauseResume', 'stop', 'replay']) {
        const btn = strip.querySelector(`[data-transport-action="${action}"]`);
        if (btn) btn.disabled = !enabled[action];
      }
      const seekBar = strip.querySelector('[data-transport-seek]');
      if (seekBar) {
        if (enabled.seek) seekBar.removeAttribute('data-transport-seek-disabled');
        else seekBar.setAttribute('data-transport-seek-disabled', '');
        offsetMs = Number.isFinite(data.offsetMs) ? data.offsetMs : 0;
        durationMs = Number.isFinite(data.durationMs) ? data.durationMs : 0;
        seekBar.setAttribute('data-transport-offset-ms', String(offsetMs));
        seekBar.setAttribute('data-transport-duration-ms', String(durationMs));
      }

      // Suppress seek-bar fill updates while user is dragging.
      const fill = strip.querySelector('[data-transport-seek-fill]');
      const dragging = seekBar && seekBar.hasAttribute('data-seek-interacting');
      if (fill && !dragging) {
        fill.style.width = `${data.seekFillPercent || 0}%`;
        if (seekBar) seekBar.setAttribute('aria-valuenow', data.seekFillPercent || 0);
      }

      const elapsed = strip.querySelector('[data-transport-elapsed]');
      const total   = strip.querySelector('[data-transport-total]');
      const percent = strip.querySelector('[data-transport-percent]');
      if (elapsed) elapsed.textContent = data.elapsedTime || '';
      if (total)   total.textContent   = data.totalTime   || '';
      if (percent) percent.textContent = data.percentPlayed || '';
    } catch (err) { console.warn('transport: bad payload', ev.data, err); }
  }

  async function postForm(url, params) {
    try {
      const res = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams(params).toString(),
      });
      if (res.status === 204) return { ok: true };
      // Error responses carry {"error": "msg"} per Spec 4 convention.
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
      // Derive pause/resume from current transport state. We never
      // reach this with state="stopped" because handleClick guards.
      return transportState === 'paused' ? 'resume' : 'pause';
    }
    return action;
  }

  function handleClick(ev) {
    const btn = ev.target.closest('[data-transport-action]');
    if (!btn || btn.disabled) return;
    if (!adapterRef || generation === 0) return; // idle; no cast to control
    // Defense in depth: even if SSE hasn't yet flipped the button to
    // disabled, refuse pauseResume clicks against a stopped session.
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
      const x = (ev.clientX !== undefined ? ev.clientX : (ev.touches && ev.touches[0] ? ev.touches[0].clientX : 0));
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
      const rawDuration = parseInt(seekBar.getAttribute('data-transport-duration-ms') || String(durationMs), 10) || 0;
      if (rawDuration <= 0) return;
      const offset_ms = Math.round((p / 100) * rawDuration);
      postForm('/receiver/transport/seek', { adapter_ref: adapterRef, generation, offset_ms });
    }
    seekBar.addEventListener('pointerup', release);
    seekBar.addEventListener('pointercancel', release);
  }

  function boot() {
    const seekBar = document.querySelector('[data-transport-seek]');
    if (seekBar) {
      offsetMs = parseInt(seekBar.getAttribute('data-transport-offset-ms') || '0', 10) || 0;
      durationMs = parseInt(seekBar.getAttribute('data-transport-duration-ms') || '0', 10) || 0;
    }
    document.addEventListener('click', handleClick);
    bindSeekDrag(seekBar);
    // vfd-live.js (Spec 2 + Spec 4) owns the single EventSource per tab
    // and exposes it at window.Chassis.events.source after connect, plus
    // dispatches a 'chassis:eventsource' CustomEvent on document so
    // sibling scripts that boot first still receive it. Use the same
    // attach-then-listen pattern as visualizer-bank.js. See
    // "EventSource fan-out" below.
    function attachSource(src) {
      if (!src) return;
      src.addEventListener('transport', handleTransportEvent);
    }
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

### Pause/resume click derivation

The single button POSTs `action=pause` when current transport state is `playing`, `action=resume` when `paused`. The client reads its current state from the closure variable updated by the most recent SSE `transport` event. No `/toggle` endpoint; no server-side state inference.

If the button is clicked while transport state is `stopped`, the JS click handler short-circuits explicitly (`if (transportState === 'stopped') return;`) — defense in depth on top of the `disabled` attribute that the SSE-driven re-render sets. During the transition window between a live cast ending and the next SSE `transport` event arriving, the button could briefly remain not-yet-`disabled`; the explicit JS guard prevents a stale POST landing as `pause` against a stopped session. Even if that window is missed (e.g. immediately after page load before any SSE event), the dispatcher returns `ErrPlaybackActionUnsupported` and the chassis maps to 422; SSE corrects within ~250 ms.

### Seek drag UX

- `pointerdown` on `.seek-bar` (provided seek is enabled) sets `data-seek-interacting`. SSE seek-bar fill updates skip while this attribute is present.
- `pointermove` while dragging updates the `.fill` width and `aria-valuenow` visually only (optimistic, no POST yet).
- `pointerup` or `pointercancel` computes `offset_ms = (fill_percent / 100) * data-transport-duration-ms`, POSTs `/receiver/transport/seek`, and removes `data-seek-interacting`. The next SSE `transport` event ~250 ms later confirms. Formatted clock text is display-only and is never parsed for seek math.
- If `actionsEnabled.seek = false` (no duration, adapter doesn't support, idle), the seek-bar has `data-transport-seek-disabled` attribute which the CSS turns into `pointer-events: none`.

### EventSource fan-out: one connection per tab

Spec 2 ships a single `EventSource('/receiver/events')` in `vfd-live.js`. Spec 4 extended `vfd-live.js` to **expose the active EventSource on `window.Chassis.events.source` after `connect()` runs**, and to **dispatch a `chassis:eventsource` CustomEvent on `document`** with `detail.source` for sibling scripts that boot first. Spec 3 reuses this surface unchanged — **no edits to `vfd-live.js` are required**.

`transport.js` (matching `visualizer-bank.js` from Spec 4):

```javascript
function attachSource(src) {
  if (!src) return;
  src.addEventListener('transport', handleTransportEvent);
}
// Subscribe-after-connect path: events.source already set.
if (window.Chassis.events && window.Chassis.events.source) {
  attachSource(window.Chassis.events.source);
}
// Subscribe-before-connect path: listen for the CustomEvent.
document.addEventListener('chassis:eventsource', (ev) => {
  attachSource(ev.detail && ev.detail.source);
});
```

Both branches are present because script load order between deferred siblings is HTML-spec-deterministic but not robust against future tooling changes (bundlers, async imports, etc.). The dual-branch idiom is the same one Spec 4 ships in `visualizer-bank.js` and is the chassis convention.

If a future spec introduces a reconnect path that creates a new EventSource (e.g. `Chassis.events.reconnect()` from Spec 2), siblings must re-attach their listeners. Spec 4's pattern is to listen to `chassis:eventsource` continuously — every reconnect that re-dispatches the CustomEvent triggers a fresh `attachSource`. Spec 3's `transport.js` follows the same idiom; the implementation plan adds a small de-dup guard so the same handler isn't attached twice on a reconnect to the same EventSource object (matches `visualizer-bank.js`'s `nextSource === source` short-circuit).

### Server-side initial render: no flash

The chassis cold-render of `/receiver` includes `Transport` populated by `snapshotFromSession`. The buttons render with correct `disabled` attributes, the icons render correctly per `data-transport-state`, and the seek-bar shows the right fill — all before any JS executes. The SSE stream upgrades from there.

### Error handling philosophy

- Network failure on POST → `console.warn`. No retry on the client (operator clicks again if they care). SSE auto-reconnects independently.
- 409 (stale generation) → `console.warn`. The SSE will push the new generation; the next click uses the fresh value automatically.
- 422 (unsupported action) → `console.warn`. The button should have been disabled — if it wasn't, the client UI is out of sync with adapter capability (transient; SSE corrects within 250 ms).
- 500 → `console.warn`. No different from any other server-side error from the operator's standpoint.

No toast-style notification UI in Phase 1. Spec 8 (settings drawer) is the natural home for a status/notification surface if the project decides to add one.

## Testing Approach

### Layer 1 — Go unit and handler tests

**`internal/playback/dispatcher_test.go`**:

```
TestDispatcher_PlaybackView_NoActiveSession             # returns (_, false)
TestDispatcher_PlaybackView_DelegatesToOwningProvider   # fake provider receives snapshot
TestDispatcher_PlaybackView_NonBlockingProviderContract # fake asserts no Manager lock is held
TestDispatcher_PlaybackViewForSnapshot_SkipsExtraSnapshot  # call count = 0 on injected StatusViewer
TestDispatcher_HandlePlaybackAction_StaleGenerationReturnsSentinel
TestDispatcher_HandlePlaybackAction_NormalizesLegacyStaleMessage
TestDispatcher_HandlePlaybackAction_UnsupportedActionReturnsSentinel
TestDispatcher_HandlePlaybackAction_DispatchesToProvider
TestDispatcher_HandlePlaybackAction_ClampsNegativeOffsetToZero  # I1 fix: clamp in dispatcher, /ui inherits
TestDispatcher_PlaybackProviderForSnapshot_SourceFirstPolicy
TestDispatcher_PlaybackProviderForSnapshot_LegacyRefScanFallback
```

The provider-lookup-policy tests verify the source-first + legacy-fallback rules from the original `/ui/playback.go` carry over verbatim.

**`internal/chassis/transport_test.go`** (new file):

```
TestHandleTransportAction_RejectsNonPOST                 # 405
TestHandleTransportAction_RejectsMalformedForm           # 400 on missing fields
TestHandleTransportAction_RejectsSeekActionOnActionRoute # 400
TestHandleTransportAction_RejectsZeroGeneration          # 400
TestHandleTransportAction_SuccessReturns204NoBody        # 204 path
TestHandleTransportAction_ResponseSetsCacheControlNoStore
TestHandleTransportAction_ErrorResponseContentTypeIsJSON # matches Spec 4: "application/json" exact
TestHandleTransportAction_StaleGenerationReturns409
TestHandleTransportAction_UnsupportedActionReturns422
TestHandleTransportAction_DispatchesToController         # fake records action
TestHandleTransportAction_ProviderErrorReturns500
TestHandleTransportAction_500LogsUnderlyingErrorButHidesFromClient

TestHandleTransportSeek_ValidOffsetReturns204
TestHandleTransportSeek_NonIntegerOffsetReturns400
TestHandleTransportSeek_DispatchesToController           # action=seek + offset
TestHandleTransportSeek_StaleGenerationReturns409
# Note: TestHandleTransportSeek_NegativeOffsetClampsToZero moves to
# dispatcher_test.go above (clamp happens in the dispatcher per I1 fix);
# the chassis transport_test.go can still assert end-to-end via a
# dispatcher fake that records the post-clamp value.
```

Spec 4's `sameorigin_test.go` already covers `TestRequireSameOrigin_*` (allow/block paths) for the visualizer route; the same middleware wraps transport routes so no new same-origin tests are required at the transport-handler layer. Integration tests (Layer 3) verify the wrapping is in place.

**`internal/chassis/events_test.go`** extensions:

```
TestSnapshotFromSession_PopulatesTransportField
TestSnapshotFromSession_NoProviderKeepsReadOnlyStateAndTime
TestSnapshotFromSession_NilTransportViewerKeepsTransportZero
TestTransportEnvelope_JSONFormat                              # exact camelCase keys
TestTransportEnvelope_IncludesRawSeekMilliseconds
TestTransportEnvelope_StateValueSpaceDistinctFromBodyClass   # playing/paused/stopped, not idle/live
TestTransportChanged_DetectsEveryFieldDelta                   # fourteen subtests (8 scalars + 6 ActionsEnabled bools)
TestTransportChanged_IgnoresUnrelatedFields
TestHandleEvents_EmitsTransportEventOnConnect                 # initial snapshot has transport
TestHandleEvents_EmitsTransportEventOnStateTransition         # playing↔paused
TestHandleEvents_EmitsTransportEventOnSeekFillPercentChange
# Update existing Spec 2 tests that count events on initial connect:
TestHandleEvents_EmitsInitialSnapshotOnConnect                # now expects 3 events, not 2
TestSnapshotCache_SeedsSynchronouslyBeforeFirstSSE            # populated cache includes Transport
```

### Layer 2 — Template tests

**`internal/chassis/chassis_test.go`** extensions:

```
TestTransportTemplate_RendersDataAttributeHooks          # all data-transport-* present
TestTransportTemplate_RendersBothPauseStateIconSpans     # data-state-icon=playing|paused
TestTransportTemplate_DisablesButtonsFromActionsEnabled  # disabled attribute present/absent
TestTransportTemplate_SeekFillStyleReflectsPercent
TestTransportTemplate_RendersRawSeekMilliseconds
TestShellTemplate_LoadsTransportScript                   # ordered after vfd-live.js
TestShellTemplate_EmitsChassisMetaTags                   # adapter-ref + generation meta tags
```

**Static JS asset checks** (same package as existing static-asset tests):

```
TestTransportJS_SeekUsesRawDurationMsNotClockText
TestTransportJS_UsesSharedEventSourceSurface           # window.Chassis.events.source + chassis:eventsource pattern
TestTransportJS_RefusesPauseResumeClickWhenStopped     # I2 race window guard
```

(No new `vfd-live.js` tests — Spec 3 doesn't modify it. Spec 4's existing `TestVfdLiveJS_ExposesSharedSource` already covers the surface Spec 3 depends on.)

### Layer 3 — Integration coverage

`tests/integration/chassis_test.go` extensions (build tag `integration`):

```
TestReceiverTransport_PostActionDispatchesViaProvider
TestReceiverTransport_PostActionWithStaleGenerationReturns409
TestReceiverTransport_PostActionWithUnsupportedActionReturns422
TestReceiverTransport_PostActionCrossSiteReturns403
TestReceiverTransport_PostSeekDispatchesOffset
TestReceiverTransport_PostSeekRejectsBadOffset
TestReceiverTransport_SSEReflectsAction                  # POST pause; observe transport event with state=paused
TestReceiverTransport_SSEIncludesRawSeekMilliseconds
TestReceiverTransport_DoesNotShadowUIRoutes              # /ui/playback/* still serves HTML; /receiver/transport/* serves JSON
```

### Existing `/ui` test coverage

`internal/ui/playback_test.go` MUST remain green without modification. `ui.New` builds a default `playback.Dispatcher` from the existing `StatusViewer` + `Registry` when `Config.Playback` is nil, so existing tests and fakes keep the same construction path. The migration of `handlePlaybackMutation`'s inline body to `s.playback.HandlePlaybackAction` preserves the HTTP response shape (the same `renderPlaybackMessage` writes the same HTML banner). Internal refactor is invisible to `/ui`'s HTTP callers and test fakes.

If any specific test asserts internal call patterns (e.g., spies on `playbackProviderForSnapshot` directly), the test needs updating to reflect the relocation. Such tests are not expected, but the implementation plan flags this as a check.

### Cross-package import lint

`TestProductionImports_NoCrossPackageCoupling` (Phase 0) still green. Spec 3 adds:
- New chassis imports of `internal/adapters` (already imported via `Config.Registry` — no new transitive dependency). Used for `PlaybackBannerAdapterView` and `PlaybackActionRequest` types in interface signatures.
- No new import of `internal/ui` / `internal/uiserver`. Confirmed.

### CI integration

Same as Spec 2: `go vet`, `go test`, `go test -race`, `go test -tags=integration`. New tests fit into the existing matrix; no new CI jobs.

### Manual verification

Manual checklist for the PR description:

| # | Check | Pass condition |
|---|---|---|
| 1 | Start a Plex cast | Within ~1 s: `/receiver` shows live state (per Spec 2), transport row buttons enabled, pause button shows pause icon, seek-bar shows fill, time labels update. |
| 2 | Click pause | Within ~500 ms: pause button shows resume icon, marquee position stops advancing, percent text stops updating. |
| 3 | Click resume | Within ~500 ms: button shows pause icon, position resumes. |
| 4 | Click stop | Within ~500 ms: chassis state goes idle (Spec 2 mapping), transport state goes stopped, all buttons disabled, VFD shows STANDBY. |
| 5 | Drag seek-bar | While dragging: bar follows cursor; SSE doesn't fight it. On release: bar holds briefly, SSE confirms (~250 ms). Marquee position reflects new offset. |
| 6 | Click in middle of seek-bar (no drag) | Same as drag-release: position jumps, SSE confirms. |
| 7 | Adapter without seek support | `actionsEnabled.seek = false` → seek-bar has `data-transport-seek-disabled`, pointer-events disabled, drag/click no-ops. |
| 8 | Click previous / next / replay | If the adapter supports them: action dispatches and SSE reflects on subsequent transport event. If not supported: button is disabled, no click. |
| 9 | Multi-tab open during a cast | All tabs show the same transport state. Pause in one tab; all tabs see the SSE event and update simultaneously. |
| 10 | Stale generation race | Open `/receiver` in two tabs; in tab A, start a new cast; in tab B, click pause. Tab B's POST returns 409 (visible in DevTools console); SSE pushes new state; subsequent clicks use the fresh generation. |
| 11 | Network disconnect during a paused cast | DevTools → Network → Offline. After ~3 s the EventStream tab shows reconnect attempt; transport state in DOM is stale but accurate to last seen. Restore Online; SSE re-syncs. |
| 12 | `/ui/playback/banner` still works | Open `/ui` in another tab during a live cast; the existing now-playing banner polls and renders as before. Pause from `/ui`; both `/ui` and `/receiver` reflect the change. |

### Explicitly out of scope

- **No standalone Vitest/jsdom harness** for `transport.js`. Client behavior is covered by static asset checks, integration tests, manual browser verification, and the existing browser runtime. Spec 5 can introduce a richer JS harness if telemetry animation makes DOM timing harder to cover with static checks.
- **No load testing.** Spec 5 (telemetry) is where load characteristics will pay off.

### Acceptance gates for the PR

- All Layer 1-3 tests pass in CI.
- Manual checklist signed off in PR description, with screenshots of `/receiver` showing paused state during an actual cast, and DevTools Network → EventStream showing `transport` events.
- `TestProductionImports_NoCrossPackageCoupling` still green.
- `internal/ui/playback_test.go` still green without modification.

## Migration & Rollout

### Coexistence invariants

- `/ui/playback/banner` polling path is unchanged.
- `/ui/playback/action` and `/ui/playback/seek` continue to return HTML responses, dispatched internally via `playback.Dispatcher.HandlePlaybackAction` (refactor moves the dispatch core; HTTP response shape unchanged).
- `internal/chassis/` continues to import only `internal/core`, `internal/config`, `internal/adapters`. No new imports of `internal/ui` / `internal/uiserver`.
- Mount order in `cmd/mister-groovy-relay/main.go` unchanged: `ui.Server.Mount(mux)` first, `chassisSrv.Mount(mux)` second.

### Discovery and navigation

Still no cross-link from `/ui/*` to `/receiver/*`. Chassis remains preview-only from the operator's perspective. README preview note from Phase 0 covers Spec 3 — no docs change required.

### Asset caching and versioning

`transport.js` is served at `/receiver/static/transport.js?v={{.Version}}` — same cache-buster as `chassis.css`, `chassis.js`, `vfd-live.js`. `Cache-Control: public, max-age=31536000, immutable`. The handler ignores `?v=` (purely a cache buster).

### Config & flags

No new user-facing config fields. No new CLI flags. `/receiver/transport/*` routes are unconditionally mounted alongside the existing `/receiver/events` from Spec 2.

### Docs

- `internal/chassis/doc.go` — append a paragraph noting the new `/receiver/transport/*` POST endpoints and the `transport` SSE event.
- This design doc.

### Rollback strategy

Spec 3 is additive on the chassis side. The `internal/ui` refactor moves dispatch code into `internal/playback.Dispatcher` but preserves the HTTP response shape exactly; rollback is `git revert` of the merge commit. Operators relying on `/ui/*` see no change.

### Risk register

| Risk | Mitigation |
|---|---|
| Shared dispatcher migration silently changes `/ui` HTTP response shape | `internal/ui/playback_test.go` is the regression guard; CI catches any deviation. |
| Import cycle from centralizing playback dispatch | Dispatch lives in `internal/playback`, above `internal/core` and `internal/adapters`; `core` does not import adapter playback types. |
| Cross-site page can send transport POSTs | `requireSameOrigin` wraps both chassis transport routes; handler and integration tests assert 403 for cross-site, missing header, and `none`. |
| Race between client click and stale generation | Dispatcher validates `generation` against a fresh status snapshot before dispatch; providers revalidate through guarded core methods under their own locks; SSE pushes new generation continuously. |
| `vfd-live.js` evolves and breaks Spec 3's dependence on `window.Chassis.events.source` + `chassis:eventsource` CustomEvent | Spec 3 makes NO edits to `vfd-live.js` (Spec 4 already added the surface). The dependency is exercised end-to-end by integration tests (`TestReceiverTransport_SSEReflectsAction`); a static-asset test asserts `transport.js` uses the shared-EventSource pattern. If Spec 5 (telemetry) introduces a different EventSource sharing surface, the change is detected at integration-test time. |
| Seek drag math wrong on edge cases (zero duration, negative offset, rounding) | Raw `durationMs` is supplied by the server; Layer 1 tests cover negative-offset clamp, non-integer rejection. Layer 2/integration tests cover drag-on-bar-with-known-duration. Manual checklist covers live edge cases. |
| `playbackProviderForSnapshot` source-first policy regression during the move | Two Layer 1 tests (`TestDispatcher_PlaybackProviderForSnapshot_SourceFirstPolicy` + `TestDispatcher_PlaybackProviderForSnapshot_LegacyRefScanFallback`) carry the policy specification across the relocation. |

### Cutover handoff

When the final cutover spec runs, `/ui/playback/*` HTML responses can be deleted; the JSON-responding `/receiver/transport/*` is the successor. `playback.Dispatcher.HandlePlaybackAction` is already the unified dispatch path by then.

## Design Decisions Worth Revisiting

### Encapsulating dispatch in `internal/playback.Dispatcher`

Spec 3 hoists `playbackProviderForSnapshot` + the snapshot-validate-dispatch dance from `internal/ui/playback.go` into `internal/playback.Dispatcher`, not `core.Manager`. Putting it on `core.Manager` would force `internal/core` to import `internal/adapters` playback types while `internal/adapters` already imports `internal/core`, creating an import cycle. A reviewer who prefers "duplicate in chassis, leave `/ui` alone" has a defensible position — Phase 0 explicitly avoided cross-package coupling mid-rollout. The argument for the shared dispatcher:

- The logic composes two existing dependencies (`StatusHomeView()` and `adapters.Registry`) and belongs above both, not inside either.
- `/ui`'s inline implementation was a Phase 0 expediency, not a deliberate boundary.
- Two consumers (`/ui` and chassis) are now ready to call it; hoisting deduplicates immediately rather than at cutover.
- The HTTP response shape for `/ui/playback/*` stays identical — the refactor is invisible to operators.

The duplication alternative trades repeated logic for fewer files. Spec 3 chooses a small dispatcher because it gives both consumers one path while preserving the existing `core`/`adapters` package boundary.

### JSON responses on the chassis vs HTML on `/ui`

The chassis POST handlers respond `application/json` while `/ui/playback/*` responds `text/html` (a banner re-render). The asymmetry exists because:

- `/ui` uses htmx + targeted DOM swaps; HTML responses are its natural shape.
- Chassis uses SSE + JS-driven DOM updates; JSON is the natural shape.
- Mixing the two would force the chassis to render the banner partial it has no use for, or `/ui` to ditch htmx for fetch+JSON.

The trade-off is two response formats during the rollout. At cutover, `/ui/playback/*` retires and the JSON surface stands alone.

### One SSE stream with multiple named events vs separate streams per spec

`transport.js` subscribes to a `transport` event on the existing `/receiver/events` SSE stream. Alternative: open a second stream at `/receiver/transport/events`. The single-stream choice trades a slightly larger client surface (the `window.Chassis.events.source` exposure + `chassis:eventsource` CustomEvent Spec 4 introduced) for one connection per tab — reverse-proxy idle behaviour is simpler with one long-lived connection, and Spec 5 (telemetry) will further validate the single-stream pattern.

A reviewer who prefers per-spec streams has a defensible call. The current decision is the same one Spec 4 (visualizer) already made — and since Spec 4 shipped the shared-EventSource surface before Spec 3 writes its plan, the architecture is already validated.

### Pause/resume as one button vs two buttons

The Phase 0 mockup has one button labeled "Pause / Resume" with a swappable icon. Spec 3 honors that with a CSS-driven icon swap and client-side derivation of the action verb from current state. An alternative — two buttons with one always disabled — is simpler implementation-wise but contradicts the mockup. Honoring the mockup is the right call; the CSS icon swap is six lines.

## Open Questions for Subsequent Specs

- **Spec 4 (visualizer) — answered.** Spec 4 shipped before Spec 3's plan and established the shared `window.Chassis.events.source` + `chassis:eventsource` CustomEvent pattern. Spec 3 follows it verbatim. No shared client utility (e.g. `chassis-rpc.js`) until at least three sibling scripts have shipped.
- **Spec 5 (telemetry).** Telemetry event rates (12-30 Hz) may overwhelm the 250 ms diff ticker. The cache-fan-out lock guarantee from Spec 2 still holds, but the per-tab SSE write cadence becomes the bottleneck. Spec 5 may need a publish/subscribe broker in `core.Manager` to push events without polling. If Spec 5 introduces the broker, `playback.Dispatcher.PlaybackView` can be migrated to subscribe-once-and-push at that time.
- **Spec 5 (telemetry).** `transport` events at the current 4 Hz cadence may flicker if the underlying adapter updates position at sub-second resolution. Spec 5's broker (if introduced) can collapse multiple sub-tick updates into one SSE record.
- **Spec 8 (settings drawer).** The chassis-side notification surface (currently console-only) may want to become a toast/banner in Spec 8, fed by POST response messages. Today's `console.warn` falls back gracefully if the surface never lands.
- **Cutover spec.** Once `/ui/playback/*` is retired, the JSON-responding `/receiver/transport/*` stands alone. `playback.Dispatcher.HandlePlaybackAction` will already be the unified dispatch path — no additional refactor required at cutover.
