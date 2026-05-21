# Receiver Chassis VFD Live — Phase 1 / Spec 2 Design

**Status:** Brainstormed; awaiting implementation plan.
**Scope:** First sub-project of Phase 1 (Live Console). Stands up an SSE stream at `GET /receiver/events`, replaces `idleSnapshot()` with a session-aware `snapshotFromSession()`, and adds a `vfd-live.js` client that updates the VFD's title / marquee / queue and toggles `body.receiver.idle` ↔ `body.receiver.live` when bridge session state changes.
**Repo location:** Committed under `docs/superpowers/specs/`. That directory is normally gitignored (`.gitignore` line 35); this spec is force-added per the convention established by the Phase 0 design doc.

## Background

[Phase 0](2026-05-21-receiver-chassis-foundation-design.md) landed the chassis foundation at `/receiver/*` — full visual mockup in idle state, no live data. This spec is the first of three Phase 1 sub-specs that wire the live console:

- **Spec 2 (this).** VFD live + idle display. Builds the SSE transport that Spec 5 (telemetry) will extend.
- **Spec 3.** Transport controls (play / pause / stop / seek wired to the existing global playback dispatch).
- **Spec 4.** Visualizer mode selector wired to `bridge.visualizer.mode`.

Specs 3 and 4 can run in parallel after Spec 2 establishes the SSE pattern.

The existing `/ui/*` now-playing banner uses **htmx polling** against `/ui/playback/banner` to stay current. The chassis intentionally diverges with SSE because Phase 2 (telemetry meters) will push spectrum / goniometer / throughput / ACK at 12-30 Hz, and polling at that cadence is wasteful. Establishing SSE in Spec 2 means Spec 5 reuses the same transport rather than introducing a second one.

## Goals

1. Mount `GET /receiver/events` as a long-lived SSE stream (`text/event-stream`). One connection per browser tab.
2. Server emits named events on the stream: `state` (`idle` / `live` body-class controller) and `vfd` (title / marquee / queue fields). Future specs add `spectrum`, `goniometer`, `throughput`, `ack`, `transport` to the same stream.
3. Replace `idleSnapshot(cfg, now)` with `snapshotFromSession(cfg, sv, now)` — reads real session state via a narrow `SessionViewer` interface that `*core.Manager` satisfies structurally. Falls back to idle content when `sv == nil` or the bridge session state is idle.
4. Client-side: `chassis.js` is unchanged. A new `vfd-live.js` subscribes to the SSE stream, calls `window.Chassis.State.set()` on `state` events, and updates VFD DOM text via `data-vfd-*` attribute hooks.
5. SSE auto-reconnects natively; server sends a fresh initial snapshot on every new connection so the client always recovers correct state.

## Non-goals

- Transport controls (play / pause / stop / seek POSTs) — Spec 3.
- Visualizer mode wiring — Spec 4.
- Telemetry events (spectrum / goniometer / throughput / ACK / audio scope) — Spec 5.
- High-frequency event optimisations (`Last-Event-ID` resume, binary framing, batching) — Phase 5 polish.
- Adding a publish/subscribe broker to `core.Manager` — Phase 1 uses a server-side diff ticker. If Spec 5's telemetry rates demand a broker, that spec can introduce one.
- Server-rendered partial fragments via the SSE stream — Phase 1 ships JSON payloads + client-side DOM text swaps. Spec 8 (settings drawer) can revisit if forms benefit from server-rendered fragments.
- Cross-link from `/ui/*` to `/receiver/*`. Final cutover spec decides.

## Done When

- `GET /receiver/events` returns `Content-Type: text/event-stream`, sends an initial snapshot, then emits `state` and `vfd` events whenever session state changes.
- A cast active in the bridge shows as `<body class="receiver live">` with the real title in the VFD within ~500 ms of session start.
- Closing the cast reverts to `<body class="receiver idle">` with `STANDBY` title within ~500 ms of session end.
- Disconnecting and reconnecting the SSE stream re-syncs state immediately — no stale display.
- No regressions on `/ui/*` or on the chassis Phase 0 idle visual.
- `internal/chassis/` still has zero production imports from `internal/ui` / `internal/uiserver`. `TestProductionImports_NoCrossPackageCoupling` re-runs green.

## Architecture

### Files added

```
internal/chassis/
├── events.go              # NEW: SSE handler, encoder, diff ticker
├── events_test.go         # NEW: SSE handler tests
├── session.go             # NEW: SessionViewer interface + snapshotFromSession()
└── static/
    └── vfd-live.js        # NEW: EventSource subscriber + DOM update hooks
```

### Files modified

- `internal/chassis/server.go` — Add `Session SessionViewer` field to `Config`. Optional (nil → idle-only mode). Store on `Server` for handlers.
- `internal/chassis/data.go` — Move `idleSnapshot` body into a private helper. Add `snapshotFromSession(cfg, sv, now)` that calls `sv.StatusHomeView()`, maps to `ReceiverPageData`, falls back to `idleSnapshot` when `sv == nil` or state is idle.
- `internal/chassis/handler.go` — `handleIndex` now calls `snapshotFromSession` instead of `idleSnapshot` directly.
- `internal/chassis/templates/vfd.html` — Add `data-vfd-title`, `data-vfd-marquee`, `data-vfd-queue` attribute hooks. CSS-equivalent classes stay in place for styling.
- `internal/chassis/templates/shell.html` — Add `<script defer src="/receiver/static/vfd-live.js?v={{.Version}}">` after the existing `chassis.js` tag.
- `cmd/mister-groovy-relay/main.go` — Pass `Session: coreMgr` into `chassis.Config`.

### `SessionViewer` interface

```go
// internal/chassis/session.go
package chassis

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/core"

// SessionViewer is the narrow read-only view of session state the
// chassis needs. *core.Manager satisfies this structurally via its
// StatusHomeView() method. Tests inject fakes; production wires
// *core.Manager. Mirrors internal/ui.StatusViewer.
type SessionViewer interface {
    StatusHomeView() core.StatusHomeView
}
```

Chassis already imports `internal/core` via Phase 0's `chassis.Config.Manager`. The `SessionViewer` interface adds no new transitive dependency.

### Field mapping from `core.StatusHomeView` to `VFDData`

`core.StatusHomeView` carries: `State`, `MediaKind`, `Title`, `AdapterRef`, `Source`, `Generation`, `Modeline`, `Position`, `Duration`, `StartedAt`, `BlitsTotal`, `FramesTotal`, `Underruns`, `WireBytes`, `LastACKAge` (see `internal/core/types.go:175-191`). It does **not** carry `Marquee`, `QueueCurrent`, or `QueueTotal`. Spec 2 maps as follows; later specs can replace placeholders with real values without re-shaping `VFDData` or the wire format.

| `VFDData` field | Source / formula | Notes |
|---|---|---|
| `State` | derived from `StatusHomeView.State` per the mapping table below | mirrors `ReceiverPageData.State`; see Issue note on naming |
| `Title` | `StatusHomeView.Title` when live; `"STANDBY"` when idle | direct field |
| `Marquee` | when live: server-formatted string `<UPPER(Source)> · <formatted Position>/<formatted Duration>` (e.g., `"PLEX · 04:23 / 09:56"`); when idle: the Phase 0 marquee hint | composed server-side in `snapshotFromSession`; later specs (3+) may extend with track-title / artist / etc. once available |
| `QueueCurrent` | `0` (placeholder for Phase 1) | Queue data is not on `StatusHomeView` today. Spec 3 (transport) or Spec 5 may surface real queue counts; the chassis wire format stays stable, only the source mapping changes. |
| `QueueTotal` | `0` (placeholder for Phase 1) | same as above |
| `SystemTime` | `now.Format("15:04")` (server-rendered for first paint; client ticker updates) | unchanged from Phase 0 |
| `Uptime` | `formatUptime(now.Sub(cfg.StartedAt))` (server-rendered + included in the `vfd` event payload — see Section 3) | unchanged from Phase 0 for initial render; the wire-format inclusion is what's new in Spec 2 |

### `core.State` → chassis `ReceiverState` mapping

`core.State` has three values (`internal/core/state.go:10-13`); chassis has two (`StateIdle`, `StateLive` per Phase 0's `data.go`). The mapping:

| `core.State` | chassis `ReceiverState` | Why |
|---|---|---|
| `StateIdle` | `StateIdle` (`"idle"`) | no cast loaded; chassis shows STANDBY |
| `StatePlaying` | `StateLive` (`"live"`) | cast is loaded and running; chassis shows real title + marquee |
| `StatePaused` | `StateLive` (`"live"`) | cast is loaded but transport paused; chassis keeps showing the title (the pause is a transport state, not a session state) |

Spec 3 (transport controls) will add a separate `transport` event carrying the play/pause/stop state for the transport-row controls; the chassis body class stays `live` throughout a paused session.

### Locking and concurrency

`core.Manager.StatusHomeView()` acquires `m.mu.Lock()` (per `internal/core/manager.go` § StatusHomeView; the CLAUDE.md invariant "Manager.mu is never held across network I/O" is honored because the method is purely an in-memory snapshot read).

With one SSE connection per browser tab and a 250 ms diff ticker, each open tab acquires the lock 4×/sec. The Phase 1 expected load (≤5 simultaneous chassis viewers) keeps this comfortably bounded, but to decouple the cost from the tab fan-out, **Spec 2 adds a per-server snapshot cache:**

- `Server` gains a `snapshotCache` field with a single `ReceiverPageData` + last-update timestamp.
- A single background goroutine (started on `New`, stopped on a context-cancel hook from `Mount`) refreshes the cache every 250 ms by calling `s.session.StatusHomeView()` once and storing the resulting `ReceiverPageData`.
- All SSE handler goroutines read the cache (with a short `sync.RWMutex` `RLock`) rather than calling `StatusHomeView()` per-tab.
- Result: lock acquisitions on `core.Manager.mu` are 4/sec total regardless of tab count. Adding telemetry consumers in Spec 5 doesn't multiply lock pressure.

The cache is implementation-only — the wire format and external behaviour are identical. Tests can either use the cache (production path) or call `snapshotFromSession` directly for unit assertions.

### Cross-package boundary preserved

`internal/chassis/` still imports only `internal/core`, `internal/config`, `internal/adapters`. **No imports of `internal/ui` or `internal/uiserver`.** `TestProductionImports_NoCrossPackageCoupling` (Phase 0) enforces.

### Route additions

`Mount` gains one entry:

```go
mux.HandleFunc("GET /receiver/events", s.handleEvents)
```

Method-aware Go 1.22+ mux; non-GET requests fall through to a default 405.

### `Server` struct gains a session field

```go
type Server struct {
    cfg      Config
    session  SessionViewer   // nil → idle-only mode
    tmpl     *template.Template
    cssBytes []byte
}
```

`session == nil` is a legal state:

1. Tests that exercise rendering without wiring up a fake `SessionViewer`.
2. Offline / degraded modes where the bridge is up but session reporting isn't available.

`handleIndex` and `handleEvents` both check `s.session == nil` and produce idle output.

### Mount order

Unchanged from Phase 0: `ui.Server.Mount(mux)` first, `chassisSrv.Mount(mux)` second. Prefixes are disjoint (`/ui/*` vs `/receiver/*`); structural collision is impossible. `TestMount_DoesNotShadowUIRoutes` (Phase 0 integration test) continues to enforce.

## SSE Wire Protocol

### Connection headers

```
HTTP/1.1 200 OK
Content-Type: text/event-stream
Cache-Control: no-cache, no-store, must-revalidate
Connection: keep-alive
X-Accel-Buffering: no
```

`X-Accel-Buffering: no` defeats nginx + similar reverse-proxy buffering that would silently break SSE; harmless when the bridge is fronted directly.

### Event vocabulary

Two named events in Spec 2. Future specs extend the same stream.

```
event: state
data: {"state":"idle"}

event: vfd
data: {"title":"STANDBY","marquee":"MISTER LINK OK · 4MS · ...","queueCurrent":0,"queueTotal":0}
```

### Field semantics

| Event | Field | Type | Notes |
|---|---|---|---|
| `state` | `state` | `"idle"` \| `"live"` | Triggers `window.Chassis.State.set(...)` on the client. |
| `vfd` | `title` | string | `"STANDBY"` in idle; cast title (`core.StatusHomeView.Title`) when live. |
| `vfd` | `marquee` | string | Idle hint message in idle; server-formatted progress string in live (`"<SOURCE> · <Position>/<Duration>"`). Server-rendered (no client formatting). |
| `vfd` | `queueCurrent` | integer | `0` for Phase 1 (placeholder — `StatusHomeView` does not surface queue data yet). |
| `vfd` | `queueTotal` | integer | `0` for Phase 1 (placeholder). |
| `vfd` | `uptime` | string | `formatUptime(now - cfg.StartedAt)` (e.g. `"4H 12M"`). Included in every `vfd` event so the displayed uptime advances even when the page sits open for hours. |

`SystemTime` continues to be client-side via the minute-aligned ticker shipped in Phase 0's `chassis.js`. Phase 0 does **not** ship a corresponding `Uptime` ticker, so uptime is pushed via the SSE stream (added to `vfdChanged` so the diff ticker emits an update at minute boundaries when uptime advances).

### JSON encoding rules

- `encoding/json` with default escaping. Every event payload struct uses explicit `json:"<camelCase>"` struct tags so the wire format matches the examples regardless of Go field naming (Go's default encoder otherwise serializes `State string` as `"State"`, not `"state"`). HTML-unsafe characters (`<`, `>`, `&`) get unicode-escaped — fine for SSE because the payload is consumed by `EventSource.data` as JSON text, not interpreted as HTML.
- Example envelopes:
  ```go
  type stateEnvelope struct {
      State string `json:"state"`
  }
  type vfdEnvelope struct {
      Title        string `json:"title"`
      Marquee      string `json:"marquee"`
      QueueCurrent int    `json:"queueCurrent"`
      QueueTotal   int    `json:"queueTotal"`
      Uptime       string `json:"uptime"`
  }
  ```
- One event per blank-line-terminated SSE record (`event: <name>\ndata: <json>\n\n`).
- No multi-line `data:` continuations in Spec 2. If a future event payload exceeds ~8 KB, switch to multi-line `data:` records (SSE spec allows).

### Initial-snapshot sequence on connection

```
event: state
data: {"state":"<current>"}

event: vfd
data: {"title":"...","marquee":"...","queueCurrent":N,"queueTotal":M}

```

Trailing blank line terminates the second record. The client has a complete picture of current state without waiting for the next change.

### Heartbeat

Every 30 seconds the server emits an SSE comment, terminated by the blank line every SSE record requires:

```
: heartbeat

```

(The trailing blank line is significant — SSE record terminator.) The leading colon makes the line a comment per the SSE spec; clients ignore it. Reverse proxies see live traffic and don't time out the connection. 30 s is well under typical proxy idle timeouts (60-300 s). The pseudocode in "Server-side push mechanism" below emits the literal `": heartbeat\n\n"` accordingly.

### Reconnection

- Browser-native: `EventSource` auto-reconnects on disconnect with a default 3 s retry interval.
- Phase 1 does **not** implement `Last-Event-ID` resume. On every reconnect the server emits a fresh initial snapshot, so the client converges to the correct state regardless of what it missed during the disconnect. Acceptable because the events are tiny and infrequent in Phase 1; Spec 5 re-evaluates if telemetry's higher event rates make full re-sync wasteful.

### Server-side push mechanism

No broker in `core.Manager`. The chassis SSE handler runs a 250 ms diff ticker. Pseudocode:

```go
// handleEvents serves a long-lived SSE stream. The handler reads
// snapshots from the per-server cache (refreshed by a single
// background goroutine every 250 ms — see Architecture § Locking and
// concurrency) so N connected tabs don't multiply lock pressure on
// core.Manager.mu.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "streaming unsupported", http.StatusInternalServerError)
        return
    }
    setSSEHeaders(w)

    // Pin the browser reconnect interval so behaviour is uniform
    // across user agents (Chrome defaults to ~3 s, Firefox to ~5 s).
    if _, err := io.WriteString(w, "retry: 3000\n\n"); err != nil {
        return
    }

    last := s.snapshotCache.Get() // shared cache, RLock
    if err := emit(w, "state", stateEnvelope{State: string(last.State)}); err != nil {
        return
    }
    if err := emit(w, "vfd", vfdEnvelopeFrom(last.VFD)); err != nil {
        return
    }
    flusher.Flush()

    tick := time.NewTicker(250 * time.Millisecond)
    heartbeat := time.NewTicker(30 * time.Second)
    defer tick.Stop()
    defer heartbeat.Stop()

    for {
        select {
        case <-r.Context().Done():
            return
        case <-tick.C:
            curr := s.snapshotCache.Get()
            if curr.State != last.State {
                if err := emit(w, "state", stateEnvelope{State: string(curr.State)}); err != nil {
                    return // client disconnected mid-write
                }
                last.State = curr.State
            }
            if vfdChanged(curr.VFD, last.VFD) {
                if err := emit(w, "vfd", vfdEnvelopeFrom(curr.VFD)); err != nil {
                    return
                }
                last.VFD = curr.VFD
            }
            flusher.Flush()
        case <-heartbeat.C:
            if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
                return
            }
            flusher.Flush()
        }
    }
}

// emit writes one SSE record (event line + data line + terminating
// blank line). Returns the underlying io.Writer error so callers can
// detect mid-write client disconnects and bail cleanly.
func emit(w io.Writer, name string, payload any) error {
    body, err := json.Marshal(payload)
    if err != nil {
        return err
    }
    if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, body); err != nil {
        return err
    }
    return nil
}
```

250 ms tick → worst case 4 events / sec (state + vfd both changing). Realistic cadence: changes are usually one field at a time on the order of seconds apart. CPU and network cost: negligible. **Lock pressure on `core.Manager.mu` is constant at 4 Hz regardless of how many tabs are open**, because the snapshot cache is refreshed by a single goroutine and read-shared across all connected SSE handlers (see Architecture § Locking and concurrency).

Mid-write disconnects (TCP RST during `io.WriteString`) propagate through the `error` return on `emit` / `WriteString`. The handler returns immediately on any write error rather than continuing to push to a dead connection.

### Edge cases handled

1. `http.ResponseWriter` doesn't implement `http.Flusher` (some test recorders) → 500 immediately.
2. Client disconnects → `r.Context().Done()` fires; goroutine returns.
3. `s.session == nil` → `snapshotFromSession` returns idle on every tick; the stream is well-formed but never transitions away from idle. Test-friendly.

### `vfdChanged` helper

```go
// vfdChanged enumerates exactly the fields that participate in the
// `vfd` event payload. Other fields on VFDData (notably SystemTime,
// which is client-side; and the duplicated State that mirrors
// ReceiverPageData.State) are deliberately excluded.
func vfdChanged(a, b VFDData) bool {
    return a.Title != b.Title ||
           a.Marquee != b.Marquee ||
           a.QueueCurrent != b.QueueCurrent ||
           a.QueueTotal != b.QueueTotal ||
           a.Uptime != b.Uptime
}
```

Explicit field-level compare beats `reflect.DeepEqual` for two reasons: faster (no reflection), and explicitly enumerates which fields are part of the Phase 1 VFD wire-format surface. Future fields added to `VFDData` for unrelated reasons won't accidentally trigger spurious events.

State-level transitions are handled separately via the `state` event (driven by the top-level `ReceiverPageData.State` comparison in the diff ticker), so `VFDData.State` is intentionally excluded here.

### Internal naming: two `State` fields

Phase 0's `data.go` defines both `ReceiverPageData.State` (the body-class controller) and `VFDData.State` (a duplicate copy for templates that branch on `vfd-state--idle` vs `vfd-state--live` CSS classes). They are intentionally redundant: the top-level field drives the `state` event and the body class, while the VFD sub-struct copy lets the partial template render the right CSS class without descending back into the parent struct. Spec 2 keeps both — renaming would be a Phase 0 churn — but the diff ticker only consults the top-level `ReceiverPageData.State` for emitting `state` events. `vfdChanged` deliberately excludes `VFDData.State` to avoid double-firing on state transitions. A future cleanup spec (likely the cutover) can collapse the duplication.

### Wire format examples

**Idle, just connected:**

```
retry: 3000

event: state
data: {"state":"idle"}

event: vfd
data: {"title":"STANDBY","marquee":"MISTER LINK OK · 4MS · 12 PRESETS · 90 CHANNELS · PASTE URL OR PICK PRESET","queueCurrent":0,"queueTotal":0,"uptime":"4H 12M"}

: heartbeat

```

**Idle → live transition:**

```
event: state
data: {"state":"live"}

event: vfd
data: {"title":"FIRST DAY ON MTV","marquee":"PLEX · 04:23 / 09:56","queueCurrent":0,"queueTotal":0,"uptime":"4H 14M"}

```

**Live → live with title change (cast moves to a different item):**

```
event: vfd
data: {"title":"BURNING DOWN THE HOUSE","marquee":"PLEX · 00:08 / 04:01","queueCurrent":0,"queueTotal":0,"uptime":"4H 17M"}

```

`state` event is omitted when state doesn't change. Client tracks state via the most recent `state` event. The `retry: 3000` directive (emitted once on connect, before the first event) pins the browser's reconnect interval to 3 s so behaviour is uniform across user agents (Chrome defaults to ~3 s but Firefox uses ~5 s without this directive — Phase 1 wants the consistent shorter interval).

## Client-Side Update Strategy

### New file: `internal/chassis/static/vfd-live.js`

~60 lines vanilla ES2022. Loaded via `<script defer src="/receiver/static/vfd-live.js?v={{.Version}}">` *after* `chassis.js`. Attaches to `window.Chassis`. No new globals.

```javascript
(() => {
  'use strict';

  if (!window.Chassis) {
    console.warn('vfd-live: window.Chassis missing; chassis.js failed to load?');
    return;
  }

  let source = null;
  let backoffMs = 1000;

  function handleStateEvent(ev) {
    try {
      const { state } = JSON.parse(ev.data);
      if (state === 'idle' || state === 'live') {
        window.Chassis.State.set(state);
      }
    } catch (err) {
      console.warn('vfd-live: bad state payload', ev.data, err);
    }
  }

  function handleVfdEvent(ev) {
    try {
      const data = JSON.parse(ev.data);
      const title = document.querySelector('[data-vfd-title]');
      const marquee = document.querySelector('[data-vfd-marquee]');
      const queue = document.querySelector('[data-vfd-queue]');
      const uptime = document.querySelector('[data-vfd-uptime]');
      if (title) title.textContent = data.title || '';
      if (marquee) marquee.textContent = data.marquee || '';
      if (queue) queue.textContent = `${data.queueCurrent} / ${data.queueTotal}`;
      if (uptime) uptime.textContent = data.uptime || '';
    } catch (err) {
      console.warn('vfd-live: bad vfd payload', ev.data, err);
    }
  }

  function connect() {
    source = new EventSource('/receiver/events');
    source.addEventListener('state', handleStateEvent);
    source.addEventListener('vfd', handleVfdEvent);
    source.addEventListener('open', () => { backoffMs = 1000; });
    source.addEventListener('error', () => {
      if (source.readyState === EventSource.CLOSED) {
        backoffMs = Math.min(backoffMs * 2, 30000);
        console.info(`vfd-live: stream closed, browser will retry; backoff ${backoffMs}ms`);
      }
    });
  }

  window.Chassis.events = {
    reconnect() {
      if (source) source.close();
      connect();
    },
  };

  document.addEventListener('DOMContentLoaded', connect);
})();
```

### Why a separate file (not folded into `chassis.js`)

Phase 0 reserved this filename in its "Files later specs add (reference only)" section. Each subsequent spec gets its own JS file that attaches to `window.Chassis`. Spec 3 will add `transport.js`, Spec 4 `visualizer-bank.js`, Spec 5 `meter-animators.js`. Keeping them separate means each spec lands without touching the others — easy to revert one spec without affecting the rest.

### DOM update strategy: data-attribute hooks on the seg-text spans

The VFD partial template uses a two-span overlay pattern for the segmented-display effect: an `<span class="seg-ghost">` rendering the dim "all-segments-lit" background, and an `<span class="seg-text">` rendering the actual content brightly on top. The ghost span must survive live updates — JS writes that overwrite the outer container's `textContent` would delete the ghost overlay and break the visual.

Spec 2 places `data-vfd-*` attributes on the **`<span class="seg-text">` children**, not the outer containers. The JS query selectors target these spans specifically, leaving the sibling ghost spans untouched. Concrete template diff to `internal/chassis/templates/vfd.html`:

```html
<!-- Phase 0 (current): -->
<div class="title-line seg-display">
  <span class="seg-ghost" aria-hidden="true">~~~~~~~</span>
  <span class="seg-text">{{.Title}}</span>
</div>

<!-- Spec 2 — add data attribute on the inner span: -->
<div class="title-line seg-display">
  <span class="seg-ghost" aria-hidden="true">~~~~~~~</span>
  <span class="seg-text" data-vfd-title>{{.Title}}</span>
</div>
```

Same pattern for marquee (`<span class="seg-text" data-vfd-marquee>{{.Marquee}}</span>`), queue (`<span class="seg-text" data-vfd-queue>{{.QueueCurrent}} / {{.QueueTotal}}</span>`), and uptime (`<span class="seg-text" data-vfd-uptime>{{.Uptime}}</span>`). The outer divs and their CSS class hooks remain unchanged. Note `[data-system-time]` already targets the system-time `seg-text` span (Phase 0) — the chassis convention is consistent: every text-bearing span the JS updates gets a `data-*` attribute, and the JS only ever writes to those spans.

Reasons for the data-attribute pattern (over class-name JS selectors):

1. **Decoupled from styling.** `.title-line` is a CSS hook. If a later spec restyles the VFD, the JS query path doesn't break because `data-vfd-title` is purely a JS contract.
2. **Easier to grep for.** `grep data-vfd-` finds every VFD live-data integration point. `grep .title-line` mixes styling and behaviour.
3. **Matches HTML5 idiom.** `data-*` attributes are the spec-blessed mechanism for app-specific JS hooks. The chassis already uses `data-system-time` (Phase 0) and `data-viz` (visualizer bank).

Convention now established: **DOM elements that JavaScript updates carry a `data-vfd-*` / `data-transport-*` / `data-meter-*` attribute on the innermost text-bearing element (typically the `seg-text` span), so overlays and decorative siblings survive updates.**

### Initial render: server-side, no flash

The server renders the correct state into the HTML on first GET — `<body class="receiver idle">` with idle VFD content if no cast is active, `<body class="receiver live">` with the live cast title if active. The SSE stream upgrades from there but never *causes* the initial render. Result: no FOUC, no "idle flash before live state arrives" on cold load.

### Interaction with the existing `?dev=1` toggle

Phase 0's `chassis.js` dev toggle flips the `body.receiver.<state>` class on click. The SSE stream only emits a `state` event when the *server-side* state changes — it does not emit on every tick. Consequence:

- Operator opens `/receiver?dev=1`, no real session running. Server state is idle; `state: idle` is sent on connect.
- Operator clicks the dev toggle → body class flips to `live`. Server state is still idle. The diff ticker compares idle-to-idle and does not emit. **The dev toggle's flip persists indefinitely until either the operator clicks it again or a real state change happens server-side.**
- A real cast then starts → server emits `state: live`. Body is already `live`; no observable change.
- The cast ends → server emits `state: idle`. The body class flips back to `idle`, overriding the operator's dev toggle.

This is the right behaviour for the dev toggle's purpose (design iteration without a running cast), but the prose in earlier drafts of this spec described it incorrectly as "the server overwrites the manual flip on the next event." The accurate framing: **the body class is the source of truth; the server only emits `state` on transitions; the dev toggle wins when no transitions are happening, and the server wins as soon as one occurs.** No code change required — this is the natural behaviour of the existing pieces composing.

### Error handling philosophy

- JSON parse errors → console warning, skip the event. Connection stays open.
- `EventSource` `error` → browser-native retry; we add only logging.
- `chassis.js` not loaded → log and bail.

No retry-with-exponential-backoff on top of `EventSource` (the browser already retries). No catch-all `try/finally` wrapping every callback (the runtime handles unhandled errors gracefully and the operator sees them in DevTools).

## Testing Approach

### Layer 1 — Go unit and handler tests

`internal/chassis/events_test.go` (new file) + extensions to `chassis_test.go`:

```
TestSessionViewer_StatusHomeViewSatisfiesInterface
TestSnapshotFromSession_NilSessionFallsBackToIdle
TestSnapshotFromSession_LiveStateOverridesIdleDefaults
TestSnapshotFromSession_MapsStatusHomeViewToVFDData

TestHandleEvents_SetsCorrectHeaders
TestHandleEvents_EmitsInitialSnapshotOnConnect
TestHandleEvents_EmitsStateEventOnTransition
TestHandleEvents_EmitsVfdEventOnTitleChange
TestHandleEvents_EmitsHeartbeatComments
TestHandleEvents_RejectsNonFlushableResponseWriter
TestHandleEvents_TerminatesOnClientDisconnect
TestHandleEvents_NilSessionStreamsIdleOnly
TestHandleEvents_MultipleConcurrentConnections
TestHandleEvents_EmitsRetryDirectiveOnConnect
TestHandleEvents_BailsOnMidWriteDisconnect
TestSnapshotCache_SingleStatusHomeViewCallPerTickRegardlessOfTabs

TestVfdChanged_DetectsEveryFieldDelta
TestEmit_FormatsValidSSERecord
```

Notes:

- `TestHandleEvents_EmitsInitialSnapshotOnConnect` uses a custom `bytes.Buffer`-backed `ResponseWriter` that implements `http.Flusher`. Reads the buffer, asserts each SSE record.
- `TestHandleEvents_EmitsStateEventOnTransition` uses a `SessionViewer` mock that toggles between idle and live snapshots. Injects a controllable ticker (10 ms during tests) and runs the handler in a goroutine; captures the buffer after 50 ms.
- `TestHandleEvents_TerminatesOnClientDisconnect` cancels the request context and asserts the handler goroutine returns within 100 ms.
- `TestHandleEvents_NilSessionStreamsIdleOnly` guards the offline-friendly mode — chassis serves a usable preview even when no real `SessionViewer` is wired.
- `TestHandleEvents_MultipleConcurrentConnections` spins 5 SSE handler goroutines against the same `Server`, drives a state transition on the shared fake `SessionViewer`, and asserts every connection observes the transition. Catches accidental shared-state mutations and exposes the goroutine-per-tab cost in CI rather than production.
- `TestHandleEvents_EmitsRetryDirectiveOnConnect` asserts the first bytes the server writes are `retry: 3000\n\n` so browser reconnect cadence is pinned uniformly (Firefox otherwise defaults to ~5 s).
- `TestHandleEvents_BailsOnMidWriteDisconnect` injects a writer that returns `io.ErrClosedPipe` mid-stream and asserts the handler goroutine returns within 100 ms instead of continuing to push to a dead connection.
- `TestSnapshotCache_SingleStatusHomeViewCallPerTickRegardlessOfTabs` mocks `SessionViewer` with a call-counter and verifies the cache refresher invokes `StatusHomeView()` exactly 4×/sec total even with 5 connected SSE handlers (the lock-contention guard described in Architecture § Locking and concurrency).

### Layer 2 — Template tests

`TestTemplatesParse` (Phase 0) already covers new templates implicitly. Add one guard:

```
TestVfdTemplate_RendersDataAttributeHooks
```

Renders the VFD partial with synthetic `VFDData`, asserts the output contains `data-vfd-title`, `data-vfd-marquee`, `data-vfd-queue`. Catches a regression where attributes are accidentally removed during a future CSS-only refactor — without them, `vfd-live.js` silently no-ops on update events.

### Layer 3 — Integration coverage

```
//go:build integration
TestReceiverEvents_EndToEnd
TestReceiverEvents_DoesNotShadowUIRoutes
```

`TestReceiverEvents_EndToEnd` spins a full bridge server with a fake `SessionViewer`, opens an HTTP client to `/receiver/events`, reads N events from the stream, and asserts the wire format matches the protocol section. Uses `http.Client.Get` with the response body's `bufio.Reader` to scan event records by `\n\n` delimiters. Closes the request to cleanly terminate the handler goroutine.

`TestReceiverEvents_DoesNotShadowUIRoutes` is a small extension to the existing Phase 0 mount-isolation test. Verifies `GET /receiver/events` returns SSE while `GET /ui/playback/banner` still returns the existing now-playing HTML fragment — both UIs' live mechanisms coexist on the same mux.

### Manual verification

The PR description's invariant-based checklist extends Phase 0's:

- Start a cast (Plex push of a known title). Within ~1 s, `/receiver` flips `<body>` to `live` and the VFD shows the real title.
- Stop the cast. Within ~1 s, `/receiver` flips back to `idle` and the VFD shows `STANDBY`.
- Open `/receiver?dev=1`. The floating toggle still works when no cast is active. Once a cast starts, the server overrides the manual toggle on the next event.
- Simulate a network disconnect while `/receiver` is open: DevTools Network panel → Throttling dropdown → "Offline", or alternatively kill and restart the bridge process. Wait for the browser's native EventSource retry (~3 s with the pinned `retry: 3000` directive). The DevTools console shows the `vfd-live: stream closed... backoff` log line. Restore the network (or the bridge) — the stream reconnects, the server sends a fresh initial snapshot, and state re-syncs.
- DevTools Network → EventStream tab: confirm wire format matches the protocol section (state and vfd event names, JSON payloads, periodic `: heartbeat` comments).

### Explicitly out of scope

- **No JS unit tests** for `vfd-live.js`. ~60 lines, exercised by manual verification + the existing browser. Spec 5 (telemetry) is where JS test infrastructure pays off.
- **No load testing** for SSE. Single connection per browser tab; the bridge isn't expected to serve hundreds of simultaneous chassis viewers.
- **No `Last-Event-ID` resume tests.** Phase 1 doesn't ship the resume mechanism.

### CI integration

Same as Phase 0 — `go vet`, `go test`, `go test -race`, `go test -tags=integration`. The new tests fit into the existing matrix; no new jobs.

### Acceptance gates for the PR

- All Layer 1-3 tests pass in CI.
- Manual verification checklist signed off in PR description, with screenshots of `/receiver` showing live state during an actual cast.
- `TestProductionImports_NoCrossPackageCoupling` still green.
- `TestChassisCSS_AllSelectorsScoped` still green.

## Migration & Rollout

### Coexistence invariants

- `/ui/*` routes still return byte-identical responses; the existing now-playing banner continues to poll `/ui/playback/banner` independently.
- `internal/chassis/` adds two production Go files (`events.go`, `session.go`) plus `events_test.go` and one JS asset (`vfd-live.js`). Continues to import only `internal/core`, `internal/config`, `internal/adapters`. **No imports of `internal/ui` or `internal/uiserver`.**
- Mount order in `cmd/mister-groovy-relay/main.go` unchanged: `ui.Server.Mount(mux)` first, `chassisSrv.Mount(mux)` second.
- `chassis.Config.Manager *core.Manager` (Phase 0) remains threaded in. Spec 2 doesn't add new method calls on Manager; Spec 3 (transport) is the first spec that calls `Manager.Pause()` / `Play()` / `Stop()` / `SeekTo()`.

### Discovery and navigation

Still no cross-link from `/ui/*` to `/receiver/*`. The chassis is preview-only from the operator's perspective. The README preview note from Phase 0 covers Spec 2; no docs change required.

### Asset caching and versioning

`vfd-live.js` is served at `/receiver/static/vfd-live.js?v={{.Version}}` — same cache-buster mechanism as `chassis.css` and `chassis.js`. The handler ignores `?v=` (purely a cache buster). No new template preprocessing required because `vfd-live.js` has no version-substituted URLs inside it.

`Cache-Control: public, max-age=31536000, immutable` on the JS file; `Cache-Control: no-cache, no-store, must-revalidate` on the SSE response.

### Config & flags

No new config fields. `/receiver/events` is unconditionally mounted.

### Docs

- `internal/chassis/doc.go` — append a paragraph noting the SSE stream at `/receiver/events` and the `SessionViewer` interface.
- This design doc.

### Rollback strategy

Spec 2 is additive. Revert = revert the merge commit; nothing in `/ui/*`, the bridge dataplane, or adapters is touched. The chassis still renders correctly because Phase 0's `idleSnapshot` path is preserved as the fallback when `Session == nil` — even after revert, a chassis built without the SSE handler would just stay on idle content, matching the Phase 0 visual.

### Risk register

| Risk | Mitigation |
|---|---|
| SSE connection leaks goroutines if `r.Context().Done()` doesn't fire | Defer-cleanup pattern in the handler. `TestHandleEvents_TerminatesOnClientDisconnect` is the structural guard. |
| Reverse-proxy buffering breaks SSE (nginx default) | `X-Accel-Buffering: no` in response headers; `Cache-Control: no-cache, no-store`. Documented in PR; operators deploying behind nginx informed via README troubleshooting section. |
| Browser EventSource silently retrying every 3 s against a permanently-broken endpoint | Console logging in `vfd-live.js` `error` handler with exponential backoff tracking. The browser still retries every 3 s, but the operator sees one log line per backoff increment so unhealthy streams are diagnosable from DevTools. |
| `core.Manager.StatusHomeView()` returns slow/inconsistent data under contention | Already production code as of /ui's status home. Spec 2 uses the same path; if the existing consumer is fine, so is this one. |
| Test flakiness from goroutine + ticker timing in SSE handler tests | Use a controllable clock or inject a ticker channel. Existing `internal/dataplane` package already follows this pattern; mirror it. |
| `vfd-live.js` loads before `chassis.js` (deferred script ordering) | `<script defer>` preserves document-order across deferred scripts (HTML spec). The shell template declares `chassis.js` first, `vfd-live.js` second. The runtime also explicitly checks `window.Chassis` exists and bails with a warning if not. |

### Cutover handoff (still Phase 5)

When the final cutover spec runs, `/ui/playback/banner` (polling) and `/receiver/events` (SSE) both exist. Cutover deletes the former. The chassis SSE stream subsumes the live-state surface the now-playing banner provides today.

## Design Decisions Worth Revisiting

### SSE over htmx polling for VFD live data

Defensible 70/30 call. SSE is the right tool for the data rate Spec 5 (telemetry) will demand. For VFD live data alone, htmx polling at 2 s would have been adequate and would have reused the proven `/ui/playback/banner` pattern. The argument for SSE is that doing it once correctly in Spec 2 saves Spec 5 from designing a second transport.

A reviewer who prefers "extend the htmx pattern in Spec 2; introduce SSE only when Spec 5 actually needs it" has a defensible position. The current call is forward-looking; the alternative is incremental. Decision rationale: telemetry is half the remaining effort, designing its transport carefully now is worth front-loading.

### Diff ticker over publish/subscribe broker

Phase 1 uses a 250 ms diff ticker inside the SSE handler. No changes to `core.Manager`. Defensible because:

- Phase 1 event rates are low (state changes every few seconds at most).
- Diff-ticker scales to 4-10 Hz comfortably for Phase 1 data.
- Adding a broker to `core.Manager` is a non-trivial Phase-1-blocking change with implications for adapter-side concurrency.

If Spec 5's 12-30 Hz telemetry events overwhelm the diff ticker, Spec 5 can introduce a real broker then. The chassis's `SessionViewer` interface can grow a `Subscribe()` method at that time without breaking Phase 1 consumers.

### `data-vfd-*` attribute hooks vs class-name JS selectors

Picked attribute hooks for the decoupling and grep-ability reasons in Section 4. A reviewer who prefers "just use the class names that are already there" is making a defensible call. The current choice front-loads a small bit of consistency work to make later CSS refactors safer.

## Open Questions for Subsequent Specs

- **Spec 3 (transport).** Do POST endpoints (play / pause / stop / seek) emit a `transport` event on the SSE stream to confirm state changes, allowing the client to be fire-and-forget on POSTs? Recommended yes. Spec 3 will design.
- **Spec 5 (telemetry).** Does the diff-ticker scale to 12-30 Hz telemetry events per type, or does Spec 5 introduce a real publish/subscribe broker in `core.Manager`? Recommended: introduce the broker in Spec 5 only if Phase 2 implementation reveals performance issues; otherwise stick with the diff-ticker pattern.
- **Spec 5 (telemetry).** Does the chassis ever need `Last-Event-ID` resume? For low-rate events (Spec 2-4) the answer is no — full re-sync on reconnect is fine. For high-rate telemetry the answer is "maybe" — depends on whether brief disconnects during reconnect cause visible animation glitches.
