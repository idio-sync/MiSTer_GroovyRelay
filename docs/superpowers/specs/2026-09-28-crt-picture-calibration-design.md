# CRT Picture Calibration (Test Pattern + Adjustment Pad) Design

**Date:** 2026-09-28
**Status:** Implemented (phases 1–7). Builds on the picture size/position
settings shipped in `0713beb8` (`[bridge.video] picture_*`). Verified with
unit, JS behaviour and fake-MiSTer integration tests plus a local UI render;
not yet verified on a real CRT (preview latency, pattern legibility). See
**As built** for small departures from this design.
**Scope:** A **Calibrate on CRT** mode in the settings drawer. It puts a
geometry test pattern on the CRT and lets the operator adjust picture size
and position with a service-menu style pad. Each change shows on the tube
within a fraction of a second, and nothing is written until **Save**.

## Background

`0713beb8` added four settings: `picture_h_size` / `picture_v_size` (80–100%,
shrink inside the raster) and `picture_h_offset` / `picture_v_offset`
(pixels / field lines). `config.VideoConfig.PictureRect` is the single source
of the geometry. For real casts, ffmpeg scales and pads the picture to that
rect in both output paths, and the OSD keeps its title-safe inset inside it.

Today the operator types numbers, saves (which restarts the cast), and judges
the result against whatever content is playing. That is slow, and most
content gives no clear edge to line up against.

## Goals

1. A test pattern on the CRT whose outer border sits exactly where
   `PictureRect` will place real content.
2. Restart-free preview. Adjustments reach the CRT without respawning ffmpeg
   or re-sending INIT/SWITCHRES, so the tube does not resync on every press.
3. Never block or disturb a real cast. Calibration is the lowest-priority
   session.
4. Never leave the pattern on the CRT indefinitely.

## Non-goals

- Hardware raster shift (moving sync within the porches via SWITCHRES).
  Revisit only if shrinking proves insufficient on real sets.
- Per-modeline geometry. One global set; per-modeline overrides can be
  added later without breaking the config.
- Restart-free placement for real casts. They keep the ffmpeg placement and
  its `ScopeRestartCast` save semantics.
- Calibrating against live content (overlaying the pattern on a cast).
- Other test patterns (colour bars, convergence, grey ramp).
- Moving the OSD with the draft during calibration. The start banner is
  suppressed; the volume overlay, if triggered, uses the saved geometry.

## User decisions

| Topic | Decision |
| --- | --- |
| Values scope | One global set of values (percent sizes, px / field-line offsets). |
| Busy bridge | **Calibrate** only starts when the bridge is idle. The button is disabled while a cast plays. |
| Cast arrives mid-calibration | The cast wins, calibration ends, and the unsaved values stay in the panel and can still be saved. |
| Persistence | Adjustments are a server-held draft. Disk is written only on **Save**. |
| Abandoned session | Calibration ends by itself after 5 minutes without a start or adjustment. |
| Control style | A D-pad with a Position/Size mode toggle (TV service menu), plus exact numeric inputs. No sliders. |
| Preview mechanism | The pattern is drawn in Go straight into the raster at the draft rect and fed to the data plane in place of ffmpeg. There are no restarts. |

## Architecture

```
chassis (routes + SSE + calibration.js)
   │  Start / Preview / Save / Cancel / Snapshot
   ▼
calibration.Controller ──Start/Stop──► core.Manager ──► dataplane.Plane
   │                                        │                 ▲
   │ Preview: redraw                        │ req.Frames      │ frames
   ▼                                        ▼                 │
calibration.Pattern (FrameSource) ─────────────────────────────┘
   │
   └── Save: BridgeSaver.SaveTouched (four picture_* fields)
```

Calibration is a normal core session, except that the data plane pulls
frames from a Go `FrameSource` instead of an ffmpeg child. The source holds
one pre-rendered raster frame. **Preview** redraws that frame at the new rect
and swaps it in; the plane picks it up on the frames that follow. The plane
is never restarted, so INIT and SWITCHRES are sent once, at Start.

**Why the pattern still matches real content:** the pattern is drawn at
`config.PictureRect(draft)` in raster pixels. Real content is padded by
ffmpeg to the same `PictureRect`. Both use the one geometry function, and a
test pins each side to it (see Testing).

### Geometry type (config)

Pull the four values into `config.PictureGeometry{HSize, VSize float64;
HOffset, VOffset int}`:

- `Rect`, `Validate` and the `Effective*` helpers move onto it.
- `VideoConfig.Picture()` returns the persisted geometry.
- `VideoConfig.PictureRect` delegates to it, so existing callers are
  unchanged.
- The calibration draft and the save path both use this one type.

### Data plane: frame sources

Add an exported seam next to the existing ffmpeg one (`spawnProcess` /
`processHandle`):

```go
// FrameSource supplies raw raster frames (OutputWidth × OutputHeight,
// BytesPerPixel, progressive) in place of an ffmpeg child. ReadFrame fills
// dst with the current frame; it must not block for long.
type FrameSource interface {
    ReadFrame(dst []byte)
}
```

- **`PlaneConfig.Frames FrameSource`.** When set, `Run` wraps it in an
  in-process `processHandle` instead of calling `spawnProcess`. That handle's
  `VideoPipe` is a reader that emits `ReadFrame` output one whole frame at a
  time. `AudioPipe` is empty, and `Done` closes on `Stop`.
- **Everything downstream is unchanged:** prebuffer, field extraction for
  interlaced modes, OSD stamping, LZ4 / delta-LZ4, pacing and ACK handling.
  A static pattern costs almost nothing on the wire, because delta-LZ4 sends
  near-empty deltas between changes.
- **Audio is off** for frame-source sessions (`AudioRate` 0), so the plane
  runs video-only.

### Core additions (small, adapter-agnostic)

- **`SessionRequest.Frames dataplane.FrameSource`.** When set:
  - `probeForStart` skips ffprobe, crop detection and the visualizer filter
    check. There is no URL to probe.
  - `startPlaneLocked` passes the source to `PlaneConfig.Frames` and builds a
    minimal `SpawnSpec` (output size, field order, fps expression) that the
    plane still reads.
  - Audio is disabled.
  - `validateSessionRequest` rejects `Frames` combined with any URL or
    capture input.
- **`SessionRequest.QuietOSD bool`.** `announceStartLocked` skips the channel
  banner and `PLAY ▶`, which would otherwise cover the pattern's corner for
  four seconds.

Calibration requests set `Source: "calibration"`, `Title: "Test pattern"`,
`QuietOSD: true` and a fresh `AdapterRef` per Start. It is not an adapter:
there is no registry entry, TOML section, lamp or history entry.

### Test pattern (`internal/calibration/pattern.go`)

`Pattern` implements `FrameSource`:

- It holds the modeline's raster size (`HActive × VActive`) and whether the
  mode is interlaced.
- It keeps two frame buffers.
- `SetRect(r)` renders into the back buffer, then swaps it in under a mutex.
- `ReadFrame` copies the front buffer.
- Rendering is a single pass over one frame (≈1 MB at 720×480), about a
  millisecond, so `Preview` renders synchronously.

It is drawn on black. Every shape is defined in the logical 4:3 square-pixel
canvas (`H×4/3 × H`) and mapped into the rect (`x = r.X + u·r.W/logicalW`,
`y = r.Y + v·r.H/logicalH`). This is the same mapping ffmpeg's letterbox fit,
anamorphic stretch and placement give real content, so a size draft that
distorts aspect distorts the pattern identically. Anything mapped outside
the raster is clipped.

- **Edge border:** a white rectangle on the rect's outermost pixels.
  Operator instruction: adjust until it is just visible on all four sides.
- **Crosshatch:** a 16×12 grid of square cells (4:3), mid-grey.
- **Centre:** a cross, plus a circle of diameter 0.8·H. If the circle looks
  round, the aspect is right.
- **Safe areas:** 95% (action-safe) and 90% (title-safe) outlines of the rect,
  in two distinct colours.

Line weight rule: horizontal lines are 2 raster lines on interlaced modelines,
so each field carries one and the line doesn't flicker at 30 Hz, and 1 on
progressive. Vertical lines are 2 px. The pattern does not pass through
ffmpeg's interlace low-pass, so this rule replaces it. No text in v1.

### Controller (`internal/calibration`)

Depends on narrow interfaces, not concrete types:

```go
type Sessions interface { // *core.Manager
    StartSessionIfIdleSnapshot(core.SessionRequest) (core.SessionStatus, bool, error)
    StopIfSession(ref string, gen uint64) (bool, error)
}
type Saver interface { SavePicture(config.PictureGeometry) (adapters.ApplyScope, error) }
```

`SavePicture` is a thin `main.go` wrapper over `BridgeSaver.SaveTouched`
writing the four fields. The saved baseline comes from the bridge config, and
the modeline comes from it too at Start.

**States:** `idle` → `active` → `ended` → (`active` via Start | `idle` via
Cancel/Save).

| Call | Behaviour |
| --- | --- |
| `Start()` | Build a `Pattern` for the current modeline at the saved rect, then call `StartSessionIfIdleSnapshot` with it. Not matched → `ErrBusy`. The draft starts from the saved values, and the (`ref`, `generation`) pair is recorded. |
| `Preview(g)` | Validate `g`. Active only (else `ErrNotActive`). Store it as the draft and call `pattern.SetRect(g.Rect(...))`. No Manager call. |
| `Save()` | Active or ended. Stop the session (`StopIfSession`), then `SavePicture(draft)`. On success → `idle`. On failure → `ended{reason: save-failed}` with the draft kept, so Save can be retried. |
| `Cancel()` | Stop the session and discard the draft → `idle`. |
| inactivity | 5 min after the last Start/Preview → stop, `ended{reason: timeout}`. |
| `OnStop` hook | `preempted` → `ended{cast}`, `stopped` → `ended{stopped}`, `error` or `eof` → `ended{error}`. Draft kept. Callbacks for any generation other than the recorded one are ignored. |

Stops are guarded by (`ref`, `generation`), so a cast that lands in between is
never stopped by the controller. The controller mutex is never held across
Manager calls, following the rule that `Manager.mu` is never held across I/O.

### Chassis routes

All are same-origin guarded like `/ui/audio/dsp`. Bodies are JSON.

| Route | Result |
| --- | --- |
| `POST /ui/calibration/start` | `200` + snapshot · `409 BUSY` (not idle) |
| `POST /ui/calibration/preview` `{hSize,vSize,hOffset,vOffset}` | `204` · `400` + field errors (same bounds and messages as the settings decoders) · `409 NOT ACTIVE` |
| `POST /ui/calibration/save` | `200 {scope}` · `409` (idle) · `500 WRITE FAILED` |
| `POST /ui/calibration/cancel` | `204` (idempotent) |

A new SSE event `calibration` is emitted on change by the existing snapshot
loop:

```json
{"state":"active",
 "draft":{"hSize":92.5,"vSize":95,"hOffset":3,"vOffset":-1},
 "saved":{"hSize":100,"vSize":100,"hOffset":0,"vOffset":0},
 "endReason":""}
```

`endReason` is one of `cast`, `stopped`, `timeout`, `error`, `save-failed`.

### UI (settings-av + `static/calibration.js`)

A **Picture geometry** block replaces the four plain number rows. At rest it
shows the saved values, a **Calibrate on CRT** button, and the four numeric
inputs, which still autosave as today for people who know their numbers. The
button is disabled with an inline reason while the `state` event is not
idle.

While calibrating, the block becomes the pad:

- **`[Position | Size]` toggle and D-pad.**
  - Position: 1 px / 1 field line per press.
  - Size: left/right = narrower/wider, up/down = shorter/taller, 0.5% per
    press.
  - Centre button resets the current mode.
  - Shift+arrow or long-press = ×4.
  - Arrow keys drive the pad while it has focus.
- **Numeric inputs** mirror the draft and are editable. Changes go through
  `/preview`, not settings autosave.
- **Mini diagram:** a 4:3 box showing the picture rect in the raster.
- **Instruction line:** "Adjust until the white border just shows on all four
  edges and the circle looks round."
- **Reset / Cancel / Save.** Reset sets the draft to 100%/0 and still needs
  Save.
- **Previews are sent at most one in flight:** a press during an in-flight
  POST replaces the queued value (latest wins). No debounce delay is needed,
  because the server applies a preview in about a millisecond.
- **On `ended`:** show a short notice and keep the pad with the draft.
  - `cast`: "A cast started — calibration ended. Your unsaved values are still
    here."
  - `timeout`: "Calibration timed out."
  - Save and Cancel remain available.

JS lives in its own IIFE module and reaches drawer helpers only through
`window.Chassis.settings.*`. A behaviour test file goes in `testdata/`, run
with `node --test`.

## Error handling

- MiSTer unreachable (INIT timeout) → the plane exits → `ended{error}` with
  the event-log message. The pad keeps the draft.
- Invalid preview values → `400` field errors, and the draft and pattern are
  unchanged.
- Bridge restart mid-calibration → the controller state is in memory only.
  The pattern stops with the process, and the UI's SSE reconnect sees `idle`.

## Risks

- **Preview latency is set by the plane's video buffer.** An always-ready
  source keeps `videoCh` near its 24-frame capacity, so a change reaches the
  tube after up to ~0.4 s at 59.94 Hz. If that feels sluggish, the source
  can pace `ReadFrame` to the field cadence to hold the queue near the
  6-frame prebuffer (~0.1 s). Decide on hardware.
- **The pattern bypasses ffmpeg,** so pattern/content agreement rests on the
  shared `PictureRect` and on the tests that pin each side to it. Scaling
  softness differs between the two, but position and size do not.
- **Interlaced line rendering** of the pattern needs a check on a real tube
  (twitter with the 2-line rule, no low-pass).

## Testing

- `config`: `PictureGeometry` refactor keeps the existing `PictureRect`
  table green.
- `dataplane`: a `Frames` plane runs end-to-end on the existing stub seam
  (no ffmpeg). A mid-session source change appears in later fields, the
  plane stays video-only, and `Stop` ends it cleanly.
- `core`:
  - A `Frames` request skips probes and sets `PlaneConfig.Frames` with audio
    off.
  - Mixing `Frames` with a URL is rejected.
  - `QuietOSD` skips the banner and transport.
- `calibration`:
  - `pattern`: border pixels exactly on `PictureRect` edges for the default,
    shrunk, shifted and off-raster rects; line-weight rule per modeline;
    `SetRect` is visible to the next `ReadFrame`.
  - `controller`, with fake `Sessions` + fake clock: busy start, preview
    never calls Sessions, preemption keeps the draft, stale-generation
    `OnStop` ignored, timeout, save ordering (stop before write),
    save-failure retry.
- `chassis`: route status codes, same-origin rejection, validation messages,
  the `calibration` SSE envelope, template render (button disabled when
  busy).
- JS behaviour test (manual `node --test`): mode toggle, step sizes, Shift ×4,
  single-flight latest-wins, ended notices.
- Integration (build-tagged): a calibration session against `fakemister`
  delivers frames. After a preview, a PNG dump shows the border at the new
  rect, with no second INIT.
- The existing real-ffmpeg placement test already pins the content side.
- Hardware (manual): pattern legibility, preview latency, and that Save
  applies to the next real cast.

## As built

Small departures from the design above, all in the direction of simpler:

- `Saver.SavePicture` returns only an error (the scope is always
  `ScopeRestartCast`), and `POST /ui/calibration/save` answers
  `200 {ok, calibration}` with the new snapshot instead of `{scope}`. Start
  answers the same shape.
- `Snapshot` also carries `rasterWidth` and `fieldLines` for the configured
  modeline, so the UI diagram draws offsets to scale.
- `Start` on an ended calibration resumes its unsaved draft, which is what the
  pad's **Resume on CRT** button does.
- Holding a D-pad key auto-repeats one step at a time after 400 ms instead of
  jumping ×4. Shift still multiplies a press by 4.
- The four `picture_*` settings rows moved into their own **Picture size &
  position** section. They stay as autosaving fields when not calibrating
  and are hidden while the pad is open.

## Phases

1. Geometry type refactor.
2. Data-plane `FrameSource` seam + core hooks (`Frames`, `QuietOSD`).
3. Pattern renderer.
4. Controller.
5. Chassis routes + SSE event + `main.go` wiring.
6. UI block, `calibration.js`, CSS, behaviour tests.
7. README (replace the "no test pattern yet" note) + hardware check.
