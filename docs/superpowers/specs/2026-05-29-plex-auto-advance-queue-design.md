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
| Mechanism | **Reactive** — reuse the existing `skipNext` queue-advance path, triggered from EOF |
| Naming | Button: **CONTINUOUS**; VFD segment: **AUTO** |

## Architecture

The feature is implemented entirely in the **Plex adapter** plus a small
chassis-UI addition. Core stays adapter-agnostic (core imports no adapter
package, per design §4.5); the only core touch-point is the existing
`SessionRequest.OnStop(reason string)` callback, which already exists.

### Component 1 — EOF-triggered advance (Plex adapter)

The Plex adapter already builds a `PlayMediaRequest` carrying `ContainerKey`,
`PlayQueueID`, and `PlayQueueItemID`, but today uses them only for manual
`skipNext`/`skipPrevious`. We capture this queue context into the
active-session bookkeeping so the EOF hook can advance.

**Advance path:**

```
ffmpeg exits cleanly
  → core fires OnStop(reason="eof")
  → adapter: is auto_advance enabled?            (read current config value)
       no  → done (stop — current behavior)
       yes → start grace window (~1.5s)
              ├─ controller sends skipNext/playMedia during window
              │     → stand down (controller wins; no double-advance)
              └─ window elapses with no controller command:
                   → fetchPlayQueue(ContainerKey)
                   → nextPlayQueueItem(after current PlayQueueItemID)
                        ├─ next exists → build PlayMediaRequest
                        │                 → Manager.StartSession()
                        └─ no next (end of queue) → stop
```

This **reuses the existing `fetchPlayQueue` / `nextPlayQueueItem` /
`restartFromPlayQueueItem` code** that `skipNext` already relies on. Auto-
advance is "trigger the skip-next logic from EOF instead of from an HTTP
command."

**Controller coordination (the load-bearing detail).** If the Plex app is
still connected and *it* sends a `skipNext` when the track ends, advancing
ourselves too would double-skip. The grace window resolves this: on EOF we
wait briefly; if a controller `skipNext`/`playMedia` arrives, the controller
wins and we stand down. Otherwise we advance. This makes the feature work
both standalone (app closed) and alongside an active controller.

**Concurrency discipline.** The advance runs fire-and-forget from the
plane-exit goroutine (the established pattern). `Manager.StartSession` runs
`probeForStart` (ffprobe) *before* taking `Manager.mu`, preserving the
"`Manager.mu` is never held across network I/O" invariant (CLAUDE.md).

**Timeline sync.** The existing 1 Hz timeline broker reports the new item's
metadata/state back to Plex automatically, so a still-connected controller's
UI stays in sync with the item the relay chose.

### Component 2 — Config & scope

A persisted boolean owned by the **Plex adapter** (`[adapters.plex]`), not a
bridge-wide flag — adapters own their own behavior, and core must not have to
interpret an adapter concept. Jellyfin will add its own field when its
auto-advance lands.

```toml
[adapters.plex]
auto_advance = false   # default OFF
```

- **Field schema** (`Fields()`): `Key: "auto_advance"`, `Kind: KindBool`,
  `Default: false`, `Section: "Playback"` — mirrors the TFF/BFF field
  pattern.
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
| End of queue | `nextPlayQueueItem` returns none → stop |
| Controller still active | Grace window stands down → controller wins |
| Toggle flipped off mid-cast | Next EOF reads `false` → stop normally |
| playQueue fetch fails | Log, stop gracefully (no retry storm) |
| Next item unplayable (probe fails) | `StartSession` fails as for a manual cast; do **not** auto-skip further (no runaway loop) → stop and report |

**Principle:** auto-advance is best-effort. Every failure degrades to today's
behavior (stop) — never a crash, never an infinite skip loop.

## Testing

- **Unit (core):** `OnStop` reason routing — EOF arms; user-stop and error do
  not. Toggle on/off gates the advance.
- **Unit (plex adapter):** given a mock playQueue, `nextPlayQueueItem`
  selection; end-of-queue returns none; empty `ContainerKey` no-ops.
  Grace-window: a controller `skipNext` arriving mid-window cancels the
  auto-advance.
- **Unit (config/UI):** `auto_advance` round-trips through `AdapterSaver`;
  scope resolves to `ScopeHotSwap`; button and drawer read the same value.
- **Integration (`tests/integration`, fake-mister):** an item that ends →
  with toggle on, the next item's INIT/SWITCHRES handshake is observed; with
  toggle off, the plane stays down. Reuses the existing fake-mister harness.
- **Race:** the EOF-triggered `StartSession` crosses the plane-exit goroutine
  into the manager; must pass `go test -race` (CI).

## Files (anticipated)

- `internal/adapters/plex/` — capture queue context into session
  bookkeeping; EOF-triggered advance with grace window; reuse
  `fetchPlayQueue` / `nextPlayQueueItem`.
- `internal/adapters/plex/` `Fields()` + scope table — new `auto_advance`
  field at `ScopeHotSwap`.
- `internal/config/` — `[adapters.plex]` decode for the new field (via the
  adapter's `DecodeConfig`).
- `internal/chassis/` — transport-row toggle route/handler, button partial,
  VFD `AutoAdvance` view-model field.
- Tests across the above plus `tests/integration`.

## Open items for the implementation plan

- Exact grace-window duration (~1.5s starting point; tune against real
  controller behavior).
- Precise naming/placement of the chassis route under the in-progress
  chassis phase structure (coordinate with the receiver-chassis foundation
  spec).
