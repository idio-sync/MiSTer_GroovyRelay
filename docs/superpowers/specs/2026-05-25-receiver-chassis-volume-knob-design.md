# Receiver Chassis Volume Knob - Phase 1 Addendum Design

**Status:** Brainstormed; awaiting implementation plan.
**Scope:** Adds a physical, receiver-style volume knob to the right side of the new `/receiver` transport row. The knob controls the persisted global `bridge.audio.output_volume` value (`0..100`) that already hot-swaps into the active cast.
**Repo location:** Committed under `docs/superpowers/specs/`. That directory is normally gitignored; this spec is force-added per the receiver chassis rollout convention.

## Background

The receiver chassis UI is being built in parallel at `/receiver`. Phase 1 already established the live console patterns this spec should reuse:

- `GET /receiver/events` is the shared SSE stream.
- Chassis scripts attach to the shared `EventSource` exposed by `vfd-live.js`.
- Mutating controls use same-origin protected `POST /receiver/...` routes.
- The chassis package stays independent from `internal/ui` and `internal/uiserver`; `main.go` wires narrow adapters.

The bridge already has global output-volume plumbing:

- `config.BridgeConfig.Audio.OutputVolume` stores `bridge.audio.output_volume`.
- `core.Manager.SetOutputVolume(int)` validates `0..100`, updates in-memory bridge config, and hot-swaps the active data plane.
- `uiserver.BridgeSaver.SaveOutputVolume(int)` persists only that field and preserves concurrent bridge config changes.
- `/ui/playback/volume` already writes through the same saver.

This spec brings that existing capability into the receiver UI without changing `/ui/*`.

## User Decisions

| Topic | Decision |
| --- | --- |
| Volume scope | Global persisted `bridge.audio.output_volume`, not per-session temporary volume. |
| Placement | Far right of the transport row. |
| Visual style | Physical high-end receiver knob with brushed metal, pointer notch, and tick marks. |
| Readout | No always-on numeric readout; the knob angle and tick ring are the visual readout. |
| Accessibility | Use a focusable native range/input surface under the physical control so keyboard and screen-reader users can operate it. |

## Goals

1. Render a physical volume knob at the right end of `internal/chassis/templates/transport.html`, before the existing setup gear button.
2. Control the existing global `bridge.audio.output_volume` value in the range `0..100`.
3. Persist accepted changes through `uiserver.BridgeSaver.SaveOutputVolume` via a new narrow chassis `VolumeSaver` interface.
4. Read current volume through a live `VolumeViewer` interface, with `*core.Manager` satisfying it via a new `OutputVolume() int` method. This keeps `/ui` changes and `/receiver` changes synchronized after startup.
5. Add `POST /receiver/volume`, same-origin protected, form-encoded with `output_volume=<0..100>`, returning `204 No Content` on success.
6. Add a `volume` named SSE event to `/receiver/events`, emitted on connect and whenever the authoritative volume changes.
7. Add `internal/chassis/static/volume-knob.js` to handle knob interaction, coalesced saves, SSE updates, and rollback on failed save.

## Non-Goals

- Per-cast, per-adapter, or temporary session volume.
- A mute button, balance control, tone controls, loudness switch, or gain curve redesign.
- An always-visible numeric display such as `VOL 73`.
- Replacing or removing `/ui/playback/volume`.
- Settings drawer integration for `audio.output_volume`; the existing settings surface remains unchanged.
- High-frequency audio telemetry or VU/spectrum changes.
- Final `/ui` to `/receiver` cutover.

## Done When

- Loading `/receiver` shows a volume knob whose initial position matches the current global output volume.
- The knob sits at the right of the transport row and reads visually as part of the same 80s/90s high-end AV receiver chassis.
- Adjusting the knob posts to `/receiver/volume`, persists `bridge.audio.output_volume`, and hot-swaps the active cast through existing core/data-plane plumbing.
- Multiple open `/receiver` tabs synchronize via the `volume` SSE event within the existing chassis tick cadence.
- Changing volume through `/ui/playback/volume` is reflected on `/receiver` because the chassis reads current volume from live manager state, not only from startup config.
- Invalid POSTs are rejected: malformed form, missing field, non-integer value, and out-of-range values all return JSON errors.
- Save failures log details server-side, return a generic JSON error, and the client snaps back to the last authoritative value.
- `internal/chassis/` still has zero production imports from `internal/ui` or `internal/uiserver`.
- CSS selector scope and template tests remain green.

## UX Design

The transport row becomes:

```text
Transport label | buttons | seek rail | time display | volume knob | setup
```

The knob is a compact physical control, not a screen widget. It should use:

- circular brushed-metal cap;
- inset pointer notch or groove;
- surrounding tick marks from low to high;
- small `VOLUME` label;
- no visible percentage text in normal operation.

Value maps to rotation over a fixed arc:

```text
0   -> -135deg
50  ->    0deg
100 -> +135deg
```

The visual angle is the operator-facing readout. The native accessible range still exposes the numeric value to assistive technology through standard browser semantics.

### Interaction

The physical knob should feel tactile:

- Pointer drag updates the visual angle immediately.
- Wheel over the focused/hovered knob adjusts in small steps.
- Keyboard operation goes through the native range: arrows adjust by `1`, PageUp/PageDown by `10`, Home to `0`, End to `100`.
- The client coalesces saves while dragging so the active cast can update live without writing on every pointer move. It should always send a final save on commit.
- The last authoritative server value is retained. If a save fails, the client returns to that value.

Concrete coalescing rule: trailing-edge throttle at 200 ms while the user is dragging or pressing keys, plus one unconditional final POST on pointer-up / blur / keyboard commit. This caps in-flight saves at five per second, leaves headroom under the 250 ms chassis tick, and guarantees the last-seen value is what reaches disk. An implementation may choose a slightly different debounce constant, but the final-commit guarantee and the ≤5 saves/sec ceiling are required, and the test suite should pin the final-commit behaviour rather than the rate.

### Client Save State

`volume-knob.js` should use a single-flight queued save model:

- `synced`: no pending local edits and no in-flight POST; SSE `volume` updates apply immediately and become the last authoritative value.
- `dragging`: pointer, keyboard, or wheel input owns the visual angle locally. Incoming SSE values are recorded as remote authoritative state but do not move the knob while the local edit is active.
- `saving`: one POST is in flight. If the user changes the value again, store only the latest queued value and send it after the in-flight request completes. Incoming SSE `volume` events update the last-authoritative tracker but DO NOT move the visual knob — they are deferred until the queued/in-flight POST resolves and the state returns to `synced`. This prevents the tab from snapping backwards to a stale fan-out of its own earlier POST while a newer value is still pending.
- `failed`: if the latest pending/final commit fails, restore the last authoritative value and clear queued local edits, then return to `synced` so subsequent SSE values move the knob again.

Older successful POST responses must not overwrite a newer queued or final local value. Pointer release, blur, or keyboard commit sends one final save for the latest value even if an intermediate save already ran. Closing the tab mid-drag may lose any queued-but-unsent value — that is acceptable because the user has no expectation that intermediate dragging values are persisted; the next session reads `bridge.audio.output_volume` from the last *completed* save.

### Accessibility

The native range must remain focusable and operable. Do not implement it with `display: none`, `visibility: hidden`, `hidden`, or `aria-hidden="true"`.

Acceptable patterns are a visually hidden range that remains in the tab order or a transparent range layered over the physical knob. It must have a programmatic name, either an associated visible `VOLUME` label or `aria-label="Volume"`. Focus must be visible on the physical knob shell when the range receives focus.

### Responsive Behavior

The knob belongs to the transport strip. It should not move into the VFD/source row.

- Wide layouts: knob appears between the time display and setup gear.
- Mid-width layouts: knob shrinks before wrapping.
- Narrow layouts: knob remains in the transport strip grid alongside controls and setup. If necessary, the time display may stay hidden as it already does at smaller widths.

Implementation must update the existing transport grid columns and the `600px` / `420px` grid-area breakpoints so the new volume element has an explicit placement and cannot overlap the seek rail or gear button.

## Architecture

### Files Added

```text
internal/chassis/
├── volume.go                  # VolumeViewer, VolumeSaver, handler, JSON errors
├── volume_test.go             # handler, snapshot, SSE-facing unit tests
└── static/
    └── volume-knob.js         # knob UI, save queue, SSE subscriber
```

### Files Modified

| File | Change |
| --- | --- |
| `internal/core/manager.go` | Add `OutputVolume() int`, returning `m.bridge.Audio.OutputVolume` under the manager lock. |
| `internal/core/manager_test.go` | Cover `OutputVolume()` and ensure `SetOutputVolume()` remains authoritative. |
| `internal/chassis/server.go` | Add optional `VolumeViewer` and `VolumeSaver` to `Config`; store on `Server`; mount `POST /receiver/volume` through `requireSameOrigin`. All three `snapshotFromSession` call sites (`handleIndex`, the snapshot refresher, and `handleEvents` in `events.go`) must pass `s.volumeViewer`. |
| `internal/chassis/data.go` | Add `OutputVolume int` to `TransportData`; idle transport data falls back to `cfg.Bridge.Audio.OutputVolume`. The knob is rendered by `transport.html`, which receives `.Transport` from `shell.html`, so the template reads `.OutputVolume`. |
| `internal/chassis/session.go` | Extend `snapshotFromSession` to populate `Transport.OutputVolume` from `VolumeViewer.OutputVolume()` when available, otherwise from startup config. This must apply to both idle and live states. Today the signature is `snapshotFromSession(cfg, sv, vv, tv, aux, now)` and the live branch overwrites `base.Transport` while idle keeps `idleSnapshot`'s value — see [Wiring `snapshotFromSession`](#wiring-snapshotfromsession) below. |
| `internal/chassis/events.go` | Add `volumeEnvelope`, `volumeEnvelopeFrom`, `volumeChanged`, and initial/diff-loop `volume` event emission. |
| `internal/chassis/events_test.go` | Update initial event expectations and add changed-volume SSE coverage. |
| `internal/chassis/templates/transport.html` | Add the knob markup after `.seek-time` and before `.gear-btn`. |
| `internal/chassis/templates/shell.html` | Load `/receiver/static/volume-knob.js?v={{.Version}}` after `transport.js`. |
| `internal/chassis/static/chassis.css` | Add scoped knob, tick-ring, focusable range, responsive transport layout rules. |
| `internal/chassis/chassis_test.go` | Assert initial `Transport.OutputVolume` and template hooks. |
| `internal/chassis/import_check_test.go` | Preserve no-cross-import rules if the table needs updating for new files. |
| `cmd/mister-groovy-relay/main.go` | Wire `VolumeViewer: coreMgr` and `VolumeSaver: &volumeSaverAdapter{bs: saver}`. |

### Interfaces

```go
// internal/chassis/volume.go
type VolumeViewer interface {
    OutputVolume() int
}

type VolumeSaver interface {
    SaveOutputVolume(volume int) error
}
```

`*core.Manager` satisfies `VolumeViewer` after adding `OutputVolume()`.

`main.go` owns the `VolumeSaver` adapter over `uiserver.BridgeSaver`. Use the same concrete-field shape as the existing `visualizerSaverAdapter` in `cmd/mister-groovy-relay/main.go`:

```go
type volumeSaverAdapter struct {
    bs *uiserver.BridgeSaver
}

func (a *volumeSaverAdapter) SaveOutputVolume(volume int) error {
    _, err := a.bs.SaveOutputVolume(volume)
    return err
}
```

`main.go` already imports `uiserver`, so the concrete field adds no new dependency. It keeps `internal/chassis` free of `internal/uiserver` and matches the precedent verbatim — reviewers and future grep should see one pattern, not two.

`BridgeSaver.SaveOutputVolume` returns `adapters.ApplyScope`; today `scopeForBridgeField("audio.output_volume")` always resolves to `ScopeHotSwap`, and the hot-swap is fully performed inside `applyHotSwapSideEffects` (which calls `core.Manager.SetOutputVolume`). The chassis discards the scope value for the same reason `visualizerSaverAdapter` does: there is no chassis-side action keyed on it.

### Wiring `snapshotFromSession`

The existing signature is:

```go
func snapshotFromSession(cfg Config, sv SessionViewer, vv VisualizerViewer,
    tv TransportViewer, aux AUXStarter, now time.Time) ReceiverPageData
```

It must grow a `VolumeViewer` parameter, conventionally placed next to `vv`:

```go
func snapshotFromSession(cfg Config, sv SessionViewer, vv VisualizerViewer,
    volv VolumeViewer, tv TransportViewer, aux AUXStarter, now time.Time) ReceiverPageData
```

Three call sites need updating in lockstep with the signature change:

1. `internal/chassis/server.go` — `handleIndex` (the page render).
2. `internal/chassis/server.go` — the snapshot refresher goroutine started by `Mount`.
3. `internal/chassis/events.go` — `handleEvents` initial-burst call at the top of the SSE handler.

Inside `snapshotFromSession`, populate `Transport.OutputVolume` *after* the live/idle switch so both branches converge. The current shape leaves `base.Transport` untouched on the idle path; the new code adds one assignment that runs regardless of state:

```go
if volv != nil {
    base.Transport.OutputVolume = volv.OutputVolume()
} else {
    base.Transport.OutputVolume = cfg.Bridge.Audio.OutputVolume
}
```

`buildTransportData` does not need to learn about volume — keeping the assignment in `snapshotFromSession` mirrors how `Visualizer.ActiveMode` is set (line 73 of today's session.go) and keeps `buildTransportData` focused on adapter-derived transport fields.

## Data Flow

### Initial Render

1. `handleIndex` builds a receiver snapshot.
2. `snapshotFromSession` calls `VolumeViewer.OutputVolume()` if wired, regardless of whether the bridge is idle, playing, or paused.
3. Templates render the knob with:
   - `data-volume-knob`;
   - `data-volume-value="{{.OutputVolume}}"`;
   - a focusable native range input with `min="0"`, `max="100"`, and `value="{{.OutputVolume}}"`;
   - CSS custom property or inline style for the initial angle.

The fallback to `cfg.Bridge.Audio.OutputVolume` exists for tests and offline constructions where no `VolumeViewer` is wired.

### Save Path

1. User changes the knob.
2. `volume-knob.js` clamps and rounds the value to an integer `0..100`.
3. The client updates the visual angle immediately.
4. The client posts:

```http
POST /receiver/volume
Content-Type: application/x-www-form-urlencoded

output_volume=73
```

5. `handleVolumePost` validates the form, invokes `VolumeSaver.SaveOutputVolume(73)`, and returns `204`.
6. `BridgeSaver.SaveOutputVolume` runs `saveLocked` (bridge_saver.go): validate, atomic disk write, then update in-memory bridge (`r.sec.Bridge = newCfg` and `r.core.UpdateBridge(newCfg)`), then `applyHotSwapSideEffects` which calls `core.Manager.SetOutputVolume(73)`.
7. The chassis snapshot cache reads `core.Manager.OutputVolume()` on the next tick (250 ms `chassisTickInterval`) and emits `event: volume` when the value changes.
8. All tabs apply the authoritative SSE value.

The chassis handler still validates locally for clean JSON errors. It may rely on `BridgeSaver.SaveOutputVolume` and `core.Manager.SetOutputVolume` as the final validation and hot-swap authority.

#### Partial-failure semantics

`saveLocked` writes disk before mutating in-memory state, so the rollback contract holds in the common failure mode (atomic disk write fails — disk and memory both untouched, client rollback works).

There is one narrower mode the chassis cannot fully reverse: if disk write succeeds and in-memory bridge state is updated, but `core.Manager.SetOutputVolume` then fails inside `applyHotSwapSideEffects`, the saver returns the wrapped `"output volume hot-swap"` error. The handler renders `500 internal save failure` and the client rolls back visually, but `m.bridge.Audio.OutputVolume` already advanced — so the next chassis tick reads the saved value and emits an SSE that immediately overrides the client's rollback.

This is acceptable: hot-swap failures on volume are extremely rare in practice (the path is a single in-process method that writes to a plane field), the persisted value is still the user's intent, and the SSE fan-out is the source of truth for all tabs. The spec's "client snaps back to last authoritative value" rule therefore covers total-failure cases (network drop, disk write failure, validation rejection); for the disk-write-succeeds-but-hot-swap-fails mode, the SSE will assert the saved value within one tick. Implementers should not introduce additional rollback machinery — let SSE be authoritative.

### SSE Payload

```go
type volumeEnvelope struct {
    OutputVolume int `json:"outputVolume"`
}
```

Initial SSE burst adds `volume` after the current Phase 1 events. The shipping order in `handleEvents` (events.go) is `state, vfd, source, visualizer, transport`; append `volume` after `transport`. If the meter telemetry spec ships first, preserve its placement and slot `volume` immediately before `meter`. The canonical order is:

```text
state, vfd, source, visualizer, transport, volume, meter
```

The exact order is less important than making the initial `volume` event unconditional and updating any `events_test.go` fixtures that assert the burst sequence.

## Error Handling

`POST /receiver/volume` follows the chassis JSON error shape:

| Condition | Status | Message |
| --- | --- | --- |
| `VolumeSaver` not configured | `503` | `volume save not configured` |
| Malformed form body | `400` | `malformed form body` |
| Missing `output_volume` | `400` | `missing output_volume field` |
| Non-integer `output_volume` | `400` | `output_volume must be an integer` |
| Out of range | `400` | `output_volume must be in 0..100` |
| Save failure | `500` | `internal save failure` |

The handler logs save failure details with the attempted volume. The response hides internal error text, matching the visualizer handler.

The client treats any non-204 response as failed. It logs the status/text to the console and restores the last authoritative value from initial render or SSE.

## Testing

### Go Unit Tests

- `internal/core/manager_test.go`
  - `OutputVolume()` returns the current manager bridge volume.
  - `SetOutputVolume()` updates the value visible through `OutputVolume()`.

- `internal/chassis/chassis_test.go`
  - idle snapshot includes configured `Transport.OutputVolume`.
  - live and idle snapshots use `VolumeViewer` value over startup config when wired.
  - `transport.html` renders `data-volume-*` hooks and accessible range attributes.

- `internal/chassis/volume_test.go`
  - accepts `0`, `50`, `100` and calls saver.
  - rejects missing, non-integer, and out-of-range input.
  - returns `503` when saver is missing.
  - returns `500` on saver failure without leaking the internal error string.
  - same-origin middleware blocks cross-site POSTs.

- `internal/chassis/events_test.go`
  - initial SSE includes `volume`.
  - changing only volume emits a `volume` event.
  - unchanged volume does not emit repeatedly.

- `internal/chassis/css_scope_test.go`
  - new selectors are `body.receiver` scoped.

### Client Verification

Manual browser verification is enough for `volume-knob.js` unless a JS test harness already exists when implementation begins:

- dragging rotates the knob and saves;
- wheel/key changes work;
- failed save rolls back;
- two tabs sync after one tab changes volume;
- narrow viewport keeps the knob in the transport strip without overlap.

If a lightweight JS test path exists by implementation time, add focused tests for save coalescing, stale success handling, failed final commit rollback, and SSE ignored/deferred during active drag.

### Regression Commands

Implementation should at minimum run:

```bash
go test ./internal/core ./internal/chassis ./internal/uiserver ./internal/ui
go test ./...
```

If the branch's normal verification matrix includes race or integration targets, run those before completion.

## Open Implementation Notes

- Prefer integer volume values. Do not introduce fractional volume to the config or data plane.
- Keep the knob JS independent from transport action dispatch. It can share the `EventSource` attach pattern, but volume saves go to `/receiver/volume`.
- Use the existing `writeJSONError` helper if it remains in `visualizer.go`; if implementation wants to move it to a shared chassis file, keep that refactor mechanical and covered by existing tests.
- Avoid a visible percentage readout. The native range value exists for accessibility, not as part of the visual receiver faceplate.
