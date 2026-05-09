# Companion Extension Mini Remote Design

**Date:** 2026-05-09  
**Status:** Design approved for spec; implementation plan not started  
**Scope:** Enhance `extension/firefox/` into a PR2-aligned mini remote and add a narrow bridge-side JSON companion API under `/ui/companion/*`.

## Problem

The companion browser extension currently works as a simple cast helper: it casts the active tab, link targets, and video `src` URLs to the URL adapter, can launch GroovyMiSTer, and can open the bridge UI. Once a cast is active, the extension has little awareness of what the bridge is doing. The operator must open the full web UI to see playback state, pause, stop, replay, seek, or recast from history.

The bridge already has the ingredients for a better extension experience: URL adapter controls, URL history, core session status, launch support, and the PR2 visual system for the main UI. The extension should become a small, fast remote that feels like part of GroovyRelay rather than a generic browser popup.

## Goals

- Make the extension popup adaptive:
  - Active playback opens as a "Now playing" remote.
  - Idle bridge opens as a "Cast this tab" launcher.
- Match the PR2 UI redesign language:
  - Warm-dark hue-80 palette.
  - Amber live/status accents.
  - Space Grotesk, Inter Tight, and JetBrains Mono where practical in the extension.
  - Compact numbered readouts and utility-first labels.
  - Subtle CRT-inspired polish that honors `prefers-reduced-motion`.
- Add a narrow JSON companion API so the popup does not parse HTML fragments.
- Show any active session, including Plex/Jellyfin/URL/future DLNA, with controls enabled only when the server reports they are safe.
- Support URL adapter controls from the popup: pause, resume, stop, replay, and seek when available.
- Include URL history in the popup with one-click recast and delete.
- Keep context-menu casting and migrate it to the same JSON play endpoint.
- Keep launch core, open Web UI, and settings available.

## Non-Goals

- Cookie management in the extension. Cookies remain pasteable in the Web UI.
- Full bridge dashboard parity. The popup is a remote, not a replacement for `/ui/`.
- Multi-bridge management.
- Browser-side LAN discovery.
- Authentication beyond the existing extension-origin plus `X-Bridge-Extension: 1` CSRF/CORS path.
- A broad public JSON API for arbitrary clients. This surface is companion-extension shaped.
- A JavaScript framework rewrite. The extension stays plain HTML/CSS/JS with tests.

## Decisions Captured During Brainstorm

| Topic | Decision |
|---|---|
| Product shape | Mini remote/control surface |
| Popup priority | Active sessions show remote first; idle sessions show cast launcher first |
| Foreign sessions | Hybrid: show any active session, enable controls only from server-provided capability flags |
| Visual direction | PR2-aligned warm instrument style |
| Backend integration | Add a small bridge-side JSON companion API |
| History/cookies | Include history; exclude cookies |

## Visual Design

The approved visual target is option A, refined against `docs/specs/2026-05-08-ui-redesign-pr2-design.md`.

**Visual thesis:** a warm-dark utility panel with amber live signal, mono technical readouts, dense but calm spacing, and PR2-style status language.

**Content plan:**

- Active state: brand/status row, "Now playing", title/source, live stats, progress/seek, controls, recent activity or compact history.
- Idle state: brand/status row, "Cast this tab", active tab URL, primary cast action, bridge/MiSTer/URL health tiles, launch/open/settings actions, recent history.
- Unconfigured state: PR2-styled empty state with one configure action.

**Interaction thesis:**

- Popup swaps between active and idle layouts based on current bridge state.
- Controls update in place with inline status instead of relying on notifications.
- Status polling is quiet and pauses during in-flight commands to avoid flicker.

### Extension Styling Bindings

The extension should not import the full web UI CSS bundle. It should define a compact popup-local token set that mirrors PR2:

```css
:root {
  --gr-bg:        oklch(0.20 0.008 80);
  --gr-surface:   oklch(0.25 0.010 80);
  --gr-surface-2: oklch(0.29 0.012 80);
  --gr-border:    oklch(0.36 0.015 80);
  --gr-text:      oklch(0.94 0.012 85);
  --gr-dim:       oklch(0.65 0.012 80);
  --gr-amber:     oklch(0.78 0.16 80);
  --gr-ok:        oklch(0.72 0.14 150);
  --gr-err:       oklch(0.65 0.22 25);
}
```

Package the same WOFF2 font assets already used by the web UI into the extension so the popup matches PR2 without reaching across extension boundaries at runtime. Keep them as local extension assets and reuse the PR2 type roles:

- Space Grotesk for brand and main titles.
- Inter Tight for labels, buttons, and body copy.
- JetBrains Mono for URLs, times, counters, and tile numbers.

Motion should be CSS-only:

- One-shot entrance fade/slide on popup open.
- Amber live chip pulse only while playing.
- Button press/settle transition.
- Instant end states under `prefers-reduced-motion: reduce`.

## Bridge-Side Companion API

Add `internal/ui/companion.go` and register routes from `Server.Mount`.

All routes live under `/ui/companion/*` and use a companion-specific extension gate. This gate is stricter than the normal UI read path: every non-`OPTIONS` companion request, including `GET /ui/companion/status`, must include both an extension-scheme `Origin` and `X-Bridge-Extension: 1`. CORS preflight requests still return `204` with the existing extension CORS headers. Rejected companion requests return JSON `403`.

The regular UI remains LAN-readable as before. The companion API is different because it exposes URL history and session metadata in a machine-readable form.

### Routes

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/ui/companion/status` | Popup view model: configuration, session state, capabilities, health, history |
| `POST` | `/ui/companion/play` | Cast a URL through the URL adapter |
| `POST` | `/ui/companion/control` | Pause, resume, stop, replay, or seek |
| `POST` | `/ui/companion/history/play` | Recast a URL history entry |
| `POST` | `/ui/companion/history/delete` | Delete a URL history entry |
| `POST` | `/ui/companion/launch` | Launch GroovyMiSTer over SSH |

### Server Dependencies

Extend `ui.Config` with explicit companion dependencies. The UI package must not import concrete URL adapter internals or inspect unexported history/control state.

```go
type CompanionSessionProvider interface {
	Status() core.SessionStatus
}

type CompanionPlayResult struct {
	AdapterRef  string
	ResolvedVia string
}

type CompanionHistoryEntry struct {
	ID         string
	Title      string
	URLDisplay string
	LastPlayed time.Time
}

type CompanionSessionDisplay struct {
	AdapterName   string
	Title         string
	SourceDisplay string
	ResolvedVia   string
}

type CompanionURLSource interface {
	CompanionPlay(ctx context.Context, rawURL, mode string) (CompanionPlayResult, error)
	CompanionPause(ctx context.Context) error
	CompanionResume(ctx context.Context) error
	CompanionStop(ctx context.Context) error
	CompanionReplay(ctx context.Context) (CompanionPlayResult, error)
	CompanionSeek(ctx context.Context, offsetMs int) error
	CompanionHistory() []CompanionHistoryEntry
	CompanionHistoryPlay(ctx context.Context, id string) (CompanionPlayResult, error)
	CompanionHistoryDelete(ctx context.Context, id string) error
	CompanionLastURLDisplay() string
}

type CompanionDisplayProvider interface {
	CompanionDisplay(adapterRef string) CompanionSessionDisplay
}
```

`core.Manager` satisfies `CompanionSessionProvider`. The URL adapter implements `CompanionURLSource` by factoring its existing `castURL`, pause/resume/stop/replay/seek, and history logic out of HTML handlers into reusable methods. `CompanionDisplayProvider` is optional; v1 can use it for URL display/title if implemented and otherwise fall back to `AdapterRef`.

`ui.Config` gains:

```go
CompanionSession CompanionSessionProvider
CompanionURL     CompanionURLSource
```

`MisterLauncher` remains the launch dependency. The registry remains the source for adapter lifecycle badges.

### Status Response

`GET /ui/companion/status` returns a single popup view model:

```json
{
  "configured": true,
  "bridge_url": "http://192.168.1.50:32500",
  "session": {
    "state": "playing",
    "adapter_ref": "url:abc123",
    "adapter_name": "url",
    "title": "Night of the Living Dead",
    "source_display": "archive.org/details/night-of-the-living-dead",
    "position_ms": 1122000,
    "duration_ms": 2645000,
    "started_at": "2026-05-09T01:02:03Z",
    "capabilities": {
      "can_pause": true,
      "can_resume": false,
      "can_stop": true,
      "can_replay": true,
      "can_seek": true
    }
  },
  "health": {
    "bridge": "online",
    "mister": "unknown",
    "url_adapter": "enabled"
  },
  "history": [
    {
      "id": "0",
      "title": "Night of the Living Dead",
      "url_display": "archive.org/details/night-of-the-living-dead",
      "last_played": "2026-05-09T01:02:03Z"
    }
  ]
}
```

Fields that are expensive or unavailable may be omitted or returned as empty strings. The extension treats missing optional fields as unavailable.

### Status Data Sources

Use existing state first:

- `CompanionSessionProvider.Status()` for state, adapter ref, position, duration, and started time.
- `CompanionURLSource` for URL history, last URL display, URL-owned controls, and URL ownership checks.
- Adapter registry for enabled/disabled/running/error status.
- `MisterLauncher` wiring only for launch; do not probe MiSTer on every popup open.

If PR2's `StatusHomeView()` lands first, the companion status route should reuse it for richer title/modeline/stat fields. If this mini-remote lands first, it should add only the minimal title/display fields it needs and remain compatible with the later PR2 aggregator.

### Capability Rules

Capabilities are server-owned. The extension never infers permission from adapter names.

- URL-owned active session:
  - `can_pause` when playing and URL adapter can pause.
  - `can_resume` when paused.
  - `can_stop` while playing or paused.
  - `can_replay` when the URL adapter has a last URL and no foreign ownership conflict.
  - `can_seek` when duration is known and URL adapter can seek.
- Foreign active session:
  - Show status for any adapter.
  - V1 renders foreign sessions read-only: all control capability flags are `false`.
  - A later adapter-owned companion-control interface may expose safe controls for Plex, Jellyfin, or DLNA, but that is out of scope for this first mini-remote pass.
- Idle:
  - No session controls.
  - Show cast, launch, open UI, settings, and history.

This read-only default prevents the popup from accidentally exposing global manager controls for sessions whose owning adapter has stronger protocol semantics than the URL adapter.

### Mutating Request Shapes

`POST /ui/companion/play`

```json
{ "url": "https://youtu.be/...", "mode": "auto" }
```

Delegates to the URL adapter's existing cast logic and returns:

```json
{ "ok": true, "adapter_ref": "url:abc123", "resolved_via": "ytdlp" }
```

`POST /ui/companion/control`

```json
{ "action": "seek", "offset_ms": 90000 }
```

Allowed actions: `pause`, `resume`, `stop`, `replay`, `seek`.

`POST /ui/companion/history/play`

```json
{ "id": "0" }
```

The `id` is a stable opaque history identifier from the status response. It must not be the current list index because URL history reorders on recast. Implement this by adding an `id` field to URL history entries, generated as a random 128-bit value when an entry is created. Existing history entries loaded from older files receive an id during load and are saved on the next history mutation.

The extension sends only the id back. The response does not expose raw URLs just to recast them.

`POST /ui/companion/history/delete`

```json
{ "id": "0" }
```

`POST /ui/companion/launch` has no body.

### Error Responses

Return JSON for all companion application responses:

```json
{ "ok": false, "error": "active session belongs to another adapter" }
```

CORS preflight is the only exception: `OPTIONS /ui/companion/*` returns `204` with no JSON body.

Status codes:

- `400` for malformed JSON, unsupported action, bad URL, or missing required field.
- `403` from the companion extension gate.
- `404` for unknown history id.
- `409` for state/capability conflicts.
- `500` for bridge-side failures.

Errors should be short, operator-readable, and redact URL credentials.

### Companion Extension Gate

Add a helper near `cors.go` / `csrf.go`:

```go
func companionExtensionGate(next http.Handler) http.Handler
```

Behavior:

- `OPTIONS` uses `handleExtensionCORSPreflight`.
- Non-`OPTIONS` requires `isExtensionOrigin(r.Header.Get("Origin"))`.
- Non-`OPTIONS` requires `X-Bridge-Extension: 1`.
- Success sets extension CORS headers and calls the route.
- Failure returns `403` JSON with `Content-Type: application/json`.

Because this gate already requires the exact extension-origin/header pair used by the existing CSRF bypass, companion POST routes should not rely on the generic `csrfMiddleware` for rejection behavior. If the implementation still composes a CSRF check for defense in depth, it must be a JSON-aware variant so the companion error contract remains true.

## Extension Architecture

Keep the existing `extension/firefox/` structure and extend it in place.

### Files

```text
extension/firefox/src/
  popup/
    popup.html      # Update markup for adaptive shell
    popup.css       # Replace generic light UI with PR2-aligned popup CSS
    popup.js        # State machine, polling, render helpers, command handlers
  options/
    options.html    # Restyle to PR2 compact settings page
    options.js      # Keep validation/test behavior; test endpoint moves to companion status
  lib/
    bridge.js       # Companion API client: status/play/control/history/launch
  background.js     # Context menus call companion play endpoint
```

No new framework and no build-step change beyond including the packaged font assets in the extension archive.

All companion client calls, including `GET /ui/companion/status`, send `X-Bridge-Extension: 1`. The browser supplies the extension-scheme `Origin`; the companion extension gate validates both.

### Popup State Machine

States:

- `unconfigured`: no `bridgeURL` in storage.
- `loading`: bridge URL exists; status request in flight.
- `unreachable`: status request failed or timed out.
- `idle`: bridge reachable, no active session.
- `active`: bridge reachable, session playing or paused.
- `commanding`: transient overlay/disabled state during a command.

Open flow:

1. Read `bridgeURL`.
2. Query active tab URL and companion status in parallel.
3. Render `unconfigured`, `unreachable`, `idle`, or `active`.
4. Poll status every 2 seconds while popup is open.
5. Suspend polling while a command is in flight, then refresh once.

### Popup Layout

Active layout:

- Brand row: `GroovyRelay` plus `LIVE` / `PAUSED` chip.
- Now playing: title when available, otherwise redacted source display.
- Source line: adapter name/ref and resolver mode when available.
- Progress: position/duration and seek range only when `duration_ms > 0 && can_seek`.
- Controls: pause/resume, stop, replay, seek.
- Secondary: compact recent history and Web UI/settings affordances.

Idle layout:

- Brand row: `GroovyRelay` plus `IDLE` chip.
- Cast this tab: active tab display and primary cast button.
- Bridge tiles: Bridge, MiSTer, URL adapter. Use `unknown` rather than probing expensively.
- Actions: launch core, open Web UI, settings.
- History: recent URL casts with recast/delete.

Unreachable layout:

- Keep active tab display and settings/open Web UI affordances.
- Show connection error in the bridge tile area.
- Disable cast/control actions because the bridge cannot be reached.

### Context Menu Casting

`background.js` should use the same `play(url, "auto")` companion client as the popup. Notifications remain appropriate for background context menu actions, but messages should use the same error formatter as the popup.

## Data Flow

### Cast Tab

```text
popup active tab URL
  -> POST /ui/companion/play
  -> URL adapter castURL(...)
  -> core.Manager.StartSession(...)
  -> JSON result
  -> popup refreshes GET /ui/companion/status
```

### Control

```text
popup button/seek input
  -> POST /ui/companion/control
  -> server validates action + capability + ownership
  -> manager or URL adapter control method
  -> JSON result
  -> popup refreshes status
```

### History

```text
popup history id
  -> POST /ui/companion/history/play or delete
  -> URL adapter history operation
  -> JSON result
  -> popup refreshes status
```

## Error Handling

- Bridge unreachable: render inline, keep settings/open UI available, disable bridge actions.
- Command timeout: restore controls after failure and show inline error.
- Capability conflict: render the `409` message near the controls.
- Bad active tab URL: disable cast button and show a concise reason.
- Missing optional status fields: hide optional UI rather than rendering placeholders.
- History id disappeared between poll and click: show "history entry no longer exists" and refresh.
- URL credentials: bridge returns redacted display values; extension never logs raw URLs.

## Accessibility

- Popup controls are real buttons and range inputs.
- Status messages use `role="status"` and `aria-live="polite"`.
- Disabled controls use actual `disabled` attributes plus visible dimming.
- Color is not the only state indicator: chips include text, buttons include labels, and errors include text.
- All tap targets should stay at least 32px tall inside the popup.

## Testing Strategy

### Go

- `GET /ui/companion/status`:
  - unconfigured/idle response.
  - URL active response with capabilities.
  - foreign active response with read-only controls.
  - history entries redacted and shaped correctly.
- `POST /ui/companion/play`:
  - valid JSON delegates to URL adapter and returns adapter ref.
  - malformed JSON and bad URL return `400`.
  - companion extension gate rejects missing header, non-extension origin, and extension origin without header with JSON `403`.
  - CORS preflight returns `204` with extension CORS headers.
  - every error path redacts URL credentials.
- `POST /ui/companion/control`:
  - pause/resume/stop/replay/seek happy paths.
  - foreign ownership conflict returns `409`.
  - seek without duration returns `409`.
  - unsupported action returns `400`.
- History:
  - recast by stable id.
  - delete by stable id.
  - missing id returns `404`.
  - stale id after history reorder does not act on the wrong entry.
  - old history file entries without ids receive stable ids.
- Launch:
  - success returns JSON ok.
  - launcher error returns JSON error with appropriate status.

### Extension

- `bridge.js` tests for status/play/control/history/launch success and error shapes.
- Popup tests:
  - unconfigured state.
  - unreachable state.
  - idle cast-first state.
  - active remote-first state.
  - disabled controls from capability flags.
  - seek hidden when duration is missing.
  - history recast/delete.
  - polling pauses during commands and refreshes after.
- Background tests:
  - link/video context menu uses companion play.
  - notifications use shared error formatting.
- Options tests:
  - URL validation unchanged.
  - test connection uses companion status.
- Manifest/release tests:
  - `package.json` and `manifest.json` versions stay in sync.
  - build/package checks include the new font assets.

### Manual Verification

- Load unpacked extension in Firefox and Chromium-family browser.
- Verify popup visual match against the approved PR2-aligned companion mockup.
- Cast a direct MP4 URL, pause/resume/seek/stop/replay from popup.
- Cast a YouTube URL through yt-dlp mode and recast from history.
- Start a Plex or Jellyfin session and confirm the popup shows it without unsafe controls.
- Confirm cookies remain managed through the Web UI.

## Rollout

This can land independently of the broader PR2 UI work as long as the popup-local CSS mirrors the PR2 tokens. If PR2's richer status aggregator lands first, reuse it. If not, the companion API should implement the minimal view model and stay compatible with later aggregation.

The extension version should bump because the signed extension payload changes.

## Open Questions

None.
