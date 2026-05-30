# Plex Auto-Advance (Continuous Play) — Design

**Date:** 2026-05-29
**Status:** Approved (brainstorming) — ready for implementation plan
**Scope:** Plex adapter only (v1). Jellyfin deferred to a later phase.

## Problem

When the currently-playing item ends, the bridge stops. If the source is a
Plex playlist, album, TV show, or artist radio, the user expects the next
item to play automatically — as it would on any media player.

The root cause is that Plex Companion is **controller-driven**: the Plex app
(phone/web/Plexamp) is normally responsible for pushing the next item when a
track ends. When that app is closed or backgrounded, nothing advances the
queue, and playback simply stops. The fix is to let the **relay itself**
advance the queue autonomously, gated by an operator-visible toggle.

## Goals

- When an item finishes (clean EOF) and auto-advance is enabled, play the
  next item in the Plex play queue.
- A single mechanism covers playlists, albums, TV-show "up next," and artist
  radio — Plex's `playQueue` already flattens all of these into one ordered
  list.
- Expose the feature as a persisted, hot-swappable toggle that is visible on
  the receiver chassis (transport row), not buried in settings.
- Degrade safely: any failure falls back to today's behavior (stop), never a
  crash or a runaway skip loop.

## Non-goals (v1)

- **Jellyfin support.** Jellyfin's WebSocket protocol does not expose a
  Plex-style play queue; auto-advance there needs its own design and is
  deferred.
- **Gapless / prefetch playback.** No pre-spawning of the next pipeline. A
  brief gap between items (ffmpeg teardown → new INIT/SWITCHRES handshake) is
  acceptable on 15 kHz analog hardware that re-syncs modelines between items
  anyway.
- **Queue looping / repeat-mode awareness.** End of queue stops.

## Decisions (from brainstorming)

| Question | Decision |
|----------|----------|
| Services in v1 | **Plex only** |
| Toggle persistence | **Persisted config setting** (survives restart), hot-swappable |
| Toggle placement | **Transport-row button** on the chassis, plus a small VFD `AUTO` indicator |
| Default state | **OFF** (preserves current behavior until opt-in) |
| End of queue | **Stop** (no loop) |
| Mechanism | **Reactive** — reuse the existing `skipNext` queue-advance path, triggered from EOF, gated by a `StartSessionIf*` guard (not a timing race) |
| Naming | Button: **CONTINUOUS**; VFD segment: **AUTO** |

## Architecture

The feature is implemented entirely in the **Plex adapter** plus a small
chassis-UI addition. Core stays adapter-agnostic (core imports no adapter
package, per design §4.5); the only core touch-point is the existing
`SessionRequest.OnStop(reason string)` callback, which already exists.

### Component 1 — EOF-triggered advance (Plex adapter)

The Plex adapter already builds a `PlayMediaRequest` carrying `ContainerKey`,
`PlayQueueID`, and `PlayQueueItemID`. The EOF hook needs that queue context,
but it **must not read it from `c.lastPlay` (or `Manager.Status()`) at OnStop
time** — by the time the plane-exit goroutine runs, a foreign adapter or a new
cast may have overwritten that shared field, and we'd advance the wrong queue.
The codebase already establishes the correct pattern: existing `OnStop`
closures snapshot the request into a captured local at construction time
(`captured := p` — see `companion.go:499-503`, with the explicit comment that
"Reading lastPlay or Manager.Status() from inside OnStop is unsafe"). Auto-
advance reuses that exact capture: the queue context (`ContainerKey`,
`PlayQueueID`, `PlayQueueItemID`, `AdapterRef`, server/token) is closed over,
not re-read.

**Advance path:**

```
ffmpeg exits cleanly
  → core fires OnStop(reason="eof")
  → adapter: reason == "eof"?                     (user-stop/error → never advance)
  → adapter: is auto_advance enabled?             (read current config value)
       no  → done (stop — current behavior)
       yes → spawn goroutine; brief settle delay (~1s, see Open items)
              → fetchPlayQueue(captured.ContainerKey)          [uses CAPTURED ctx]
              → nextPlayQueueItem(after captured.PlayQueueItemID)
                   ├─ no next (end of queue / empty ContainerKey)
                   │     → log "auto-advance: end of queue" → stop
                   └─ next exists → build next PlayMediaRequest (same path
                        as handlePlayMedia, so it gets its OWN auto-advance
                        OnStop wiring — the chain continues past item 2)
                        → Manager.StartSessionIfIdle(nextReq)
                             ├─ true  → next item plays
                             └─ false → a controller command already started
                                        something; stand down (guard wins)
```

This **reuses the existing `fetchPlayQueue` / `nextPlayQueueItem` /
`restartFromPlayQueueItem` code** that `skipNext` already relies on. Auto-
advance is "trigger the skip-next logic from EOF instead of from an HTTP
command," with one addition: the start is **guarded**, not unconditional.

**Controller coordination (the load-bearing detail).** If the Plex app is
still connected and *it* sends a `skipNext`/`playMedia` when the track ends,
advancing ourselves too would double-skip. Two mechanisms combine:

1. **Correctness — the guard.** We call `Manager.StartSessionIfIdle(nextReq)`
   (`manager.go:906`), which starts the next item **only if no session became
   active in the meantime**. The check runs under `Manager.mu`, so it is
   deterministic and race-free: if a controller command already started a
   session, the guard returns `false` and we stand down. This — not timing —
   is what prevents a double-advance. (If we ever want to bind to the exact
   prior session, `StartSessionIfSession(req, AdapterRef, generation)` at
   `manager.go:898` is the stricter variant.)
2. **Smoothness — the settle delay.** A brief delay (~1s) before the guarded
   start lets a present controller win the race cleanly. Without it the local
   relay would usually beat the network controller, start the item, then get
   preempted to the *same* item — a visible restart. The delay makes the
   common "controller present" case glitch-free. The delay is an optimization;
   the guard is the guarantee. When the app is closed (the primary scenario),
   no controller command comes and we advance after the delay.

**Concurrency discipline.** The advance runs fire-and-forget in a goroutine
spawned from the `OnStop` closure — the closure itself never blocks (the core
contract, `types.go:178`). `StartSessionIfIdle` runs `probeForStart` (ffprobe)
with `Manager.mu` **not** held, preserving the "`Manager.mu` is never held
across network I/O" invariant (CLAUDE.md).

**Chain continuation.** The next item's `PlayMediaRequest` is built through the
same construction path as a controller `playMedia`, so it carries its own
captured context and its own auto-advance `OnStop`. Item 2's EOF therefore
advances to item 3 by the identical mechanism — the chain is self-perpetuating
and terminates only at end-of-queue, an unplayable item, or the toggle going
off.

**Timeline sync.** The existing 1 Hz timeline broker reports the new item's
metadata/state back to Plex automatically (it reads the freshly-remembered
session via `lastPlaySession`), so a still-connected controller's UI stays in
sync with the item the relay chose.

### Component 2 — Config & scope

A persisted boolean owned by the **Plex adapter** (`[adapters.plex]`), not a
bridge-wide flag — adapters own their own behavior, and core must not have to
interpret an adapter concept (core imports no adapter package, per design
§4.5).

**Why per-adapter, and the chassis-reads-it boundary.** The chassis button is
bridge-level UI but it reads/writes an adapter-level field. For v1 this is
deliberate and acceptable: Plex is the *only* adapter with play-queue
semantics, so the CONTINUOUS button is hardwired to `adapters.plex.auto_advance`.
When Jellyfin (or another queue-capable adapter) later gains auto-advance, the
button's binding becomes a real question — at that point the resolution is
likely a small bridge-level facade (e.g. "auto-advance applies to whichever
adapter owns the active cast") rather than a second button. That refactor is
explicitly **out of scope for v1** and noted here so the future maintainer
isn't surprised. We do *not* pre-build a bridge-level flag now (YAGNI, and it
would force core/chassis to know about adapter queue semantics prematurely).

```toml
[adapters.plex]
auto_advance = false   # default OFF
```

- **Field schema** (`Fields()`): `Key: "auto_advance"`, `Kind: KindBool`,
  `Default: false`, `Section: "Playback"` — mirrors the Plex adapter's
  existing `enabled` boolean toggle (a `KindBool` `ScopeHotSwap` field), the
  right analogy here. (TFF/BFF is a two-value *choice* field, not a boolean,
  so it is **not** the pattern to copy for a simple on/off toggle.)
- **ApplyScope: `ScopeHotSwap`.** Flipping the toggle takes effect
  immediately; the EOF hook reads the *current* config value at the moment an
  item ends, so toggling mid-cast arms/disarms the next boundary live. Wire
  into `scopeForPlexField` (or the adapter's scope table).
- **Validation:** a bool needs none, but the save still flows through the
  existing validate-then-write `AdapterSaver` path so it serializes against
  other saves on the shared mutex.

### Component 3 — Chassis UI

**Transport-row button.** A dedicated toggle labeled **CONTINUOUS**, lit when
active and dim when off, beside the existing transport controls in
`internal/chassis/`. Tapping it POSTs to a new chassis route (e.g.
`POST /receiver/continuous/toggle`) which calls the **same `AdapterSaver`
path** the settings-drawer field uses, then returns the updated button
fragment via htmx swap.

**VFD indicator.** A small `AUTO` segment on the VFD lights when the toggle is
on. Low cost — the chassis already renders VFD state from live data; we add
one boolean to that view model.

**Single source of truth.** Both the button and the settings-drawer field
render from `adapters.plex.auto_advance`. On page load the button's lit/dim
state derives from config; after a toggle the htmx fragment re-renders from
the freshly-saved value. There is no separate UI state to drift, and the
button is just a second view onto the same config field.

**Wiring:**
- New htmx route + handler in `internal/chassis/`, following its existing
  transport-control handler pattern.
- Button partial template (lit/dim variants).
- VFD view model gains an `AutoAdvance bool`.
- Settings drawer auto-renders the field from `Fields()` — no extra work.

## Edge cases

| Case | Behavior |
|------|----------|
| User-initiated stop | `reason` is not `"eof"` → never auto-advance |
| ffmpeg error (`reason="error"`) | No advance; surfaces as today (don't mask a broken pipeline) |
| Single item, empty `ContainerKey` | Nothing to advance to → stop |
| End of queue | `nextPlayQueueItem` returns none → log "end of queue" at info → stop |
| Controller still active | `StartSessionIfIdle` guard returns `false` (controller already started a session) → stand down. Settle delay makes this glitch-free in the common case. |
| Toggle flipped off mid-cast | Next EOF reads `false` → stop normally (boolean read is atomic) |
| playQueue fetch fails | Log at warn (item key + error), stop gracefully (no retry storm) |
| Next item unplayable (probe fails) | `StartSessionIfIdle` fails as a manual cast would; log at warn with item key + error; do **not** auto-skip further (no runaway loop) → stop |

**Principle:** auto-advance is best-effort. Every failure degrades to today's
behavior (stop) — never a crash, never an infinite skip loop.

## Testing

- **Unit (core):** `OnStop` reason routing — EOF arms; user-stop and error do
  not. Toggle on/off gates the advance.
- **Unit (plex adapter):** given a mock playQueue, `nextPlayQueueItem`
  selection; end-of-queue returns none; empty `ContainerKey` no-ops.
  - *Captured-context:* the OnStop closure advances from the **captured**
    queue context, not `lastPlay`. Test: cast A (queue Qa), then overwrite
    `lastPlay` via a simulated cast B (queue Qb); fire A's OnStop(eof); assert
    the advance targets Qa's next item, never Qb's. This is the stale-context
    regression guard.
  - *Guard / controller coordination:* use a fake manager that records
    `StartSessionIfIdle` calls and can report itself non-idle. Assert: idle →
    advance starts the next item; non-idle (controller already cast) →
    `StartSessionIfIdle` returns `false` and no second session starts.
- **Unit (config/UI):** `auto_advance` round-trips through `AdapterSaver`;
  scope resolves to `ScopeHotSwap`; button and drawer read the same value
  (mirrors the existing `enabled` field's test).
- **Integration (`tests/integration`, fake-mister):** an item that ends →
  with toggle on, the next item's INIT/SWITCHRES handshake is observed; with
  toggle off, the plane stays down. Reuses the existing fake-mister harness.
- **Race:** the EOF-triggered goroutine calls `StartSessionIfIdle`, crossing
  the plane-exit goroutine into the manager; must pass `go test -race` (CI).

## Files (anticipated)

- `internal/adapters/plex/` — capture queue context into the OnStop closure
  (`captured := p` pattern); EOF-triggered advance guarded by
  `Manager.StartSessionIfIdle`; reuse `fetchPlayQueue` / `nextPlayQueueItem`;
  build the next request through the shared `playMedia` construction path so
  the chain self-perpetuates.
- `internal/adapters/plex/` `Fields()` + scope table — new `auto_advance`
  field at `ScopeHotSwap`.
- `internal/config/` — `[adapters.plex]` decode for the new field (via the
  adapter's `DecodeConfig`).
- `internal/chassis/` — transport-row toggle route/handler, button partial,
  VFD `AutoAdvance` view-model field.
- Tests across the above plus `tests/integration`.

## Open items for the implementation plan

- Exact settle-delay duration (~1s starting point; measure a real Plex
  controller's post-EOF `skipNext` latency from timeline/Companion logs and
  set the delay to ~120% of it). Correctness does not depend on this value —
  the `StartSessionIfIdle` guard does — so it is purely a smoothness tune.
  Record the chosen value and measurement method when it lands.
- Precise naming/placement of the chassis route under the in-progress
  chassis phase structure (coordinate with the receiver-chassis foundation
  spec).
