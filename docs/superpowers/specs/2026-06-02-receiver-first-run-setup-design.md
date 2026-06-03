# Receiver First-Run Setup — Design

**Status:** Brainstormed; awaiting implementation plan.
**Scope:** Bring the receiver chassis UI (`/receiver/*`) to first-run parity with
the legacy `/ui/*` setup wizard, so a fresh install can be fully configured from
the chassis alone. This removes the last functional blocker before the chassis
can become the primary UI (the cutover itself is a separate, later sub-project).
**Repo location:** Committed under `docs/superpowers/specs/`. That directory is
normally gitignored (`.gitignore` line 35); this spec is force-added (`git add -f`)
intentionally, following the receiver-chassis rollout convention so the design is
reviewable from the repo.

## Background

The chassis UI at `/receiver/*` is feature-complete for live playback, telemetry,
casting, presets, history, and a full settings drawer. The settings drawer already
exposes every control a first-run install needs:

- Bridge/MiSTer settings save + connectivity probe
  (`POST /receiver/settings/bridge`, `POST /receiver/settings/action/probe-mister`).
- Per-adapter config save (`POST /receiver/settings/adapter/{name}`).
- Per-adapter link/pairing flows — Plex PIN, Jellyfin credentials
  (`POST /receiver/settings/adapter/{name}/link/start`, etc.).

What the chassis lacks is **first-run handling**: nothing forces or guides a fresh
install toward configuration, and nothing marks setup complete. The legacy `/ui/*`
surface has all of this — a guarded four-step wizard (`internal/ui/setup.go`) plus a
redirect middleware (`internal/ui/middleware.go` `firstRunGuard`) that 302s every
page to `/ui/setup` until configured.

First-run state is a single on-disk sentinel owned by `*uiserver.BridgeSaver`
(`IsFirstRun()` / `DismissFirstRun()`, `internal/uiserver/bridge_saver.go`).
`cmd/mister-groovy-relay/main.go` builds **one** `BridgeSaver` instance and hands the
same pointer to both `ui.Server` and `chassis.Server`. Therefore the chassis can read
and dismiss the *same* first-run flag the legacy UI uses — no new persistence, and
completing setup in either UI satisfies both.

This design adds a **thin first-run gate** to the chassis that reuses the existing
settings drawer rather than porting the four-step wizard. The receiver renders
normally but in a guided "setup mode" until the install is configured.

## Goals

1. A fresh install opening `/receiver` is guided to configure MiSTer host + at least
   one source, entirely within the chassis.
2. Reuse the existing settings-drawer handlers for all configuration. No new config
   forms.
3. Reuse the existing shared first-run sentinel. No new on-disk state; the chassis and
   `/ui` stay consistent.
4. Casting is gated until the install is configured, with a clear "Finish setup"
   completion moment.
5. No regressions to `/ui/*` or to chassis unit tests that do not wire a first-run
   controller.

## Non-goals

- Removing `/ui/setup`, `firstRunGuard`, or `internal/ui` setup code. That is cutover
  work. This spec only brings the chassis to parity so the cutover *can* retire them.
- A multi-step chassis wizard with its own page routing. The settings drawer is the
  configuration surface.
- A hard redirect gate (the chassis renders the receiver in setup mode rather than
  hiding it behind a takeover screen).
- Requiring the MiSTer connectivity probe to succeed before completion.
- SSE-driven live setup status. Setup mode is resolved on page load; the checklist is
  refreshed by an explicit status fetch after drawer saves.
- Per-adapter "picked but not configured" tracking (a known limitation of the legacy
  wizard that we are not reproducing).

## Done When

- On a fresh install (`IsFirstRun() == true`), `GET /receiver` renders the chassis with
  a welcome banner, the settings drawer auto-opened, and cast actions disabled.
- The welcome banner shows a two-item checklist: MiSTer host set, and ≥1 source enabled.
- Cast-initiation POSTs return `409` while setup mode is active (first-run sentinel
  still set), including after criteria are met but before Finish is clicked.
- Once host is set and ≥1 adapter is enabled, `POST /receiver/setup/finish` dismisses
  the sentinel and the operator lands on the normal receiver.
- Completing setup via `/ui/setup` also clears chassis setup mode (shared sentinel),
  and vice versa.
- Chassis unit tests that do not wire a first-run controller observe no setup mode
  (setup mode is off by default).
- `go vet`, `go test`, `go test -race`, and integration tests stay green.

## "Configured enough" rule

Setup is complete when **both** hold, mirroring the legacy
`internal/ui/setup.go` `firstIncompleteStep` logic exactly:

1. `BridgeSaver.Current().MiSTer.Host != ""`, and
2. at least one adapter in `Registry.List()` reports `IsEnabled() == true`.

`Config.Registry` (`*adapters.Registry`) is already passed to the chassis and already
used by `idleSnapshot`, so this check needs no new dependency and introduces no new
cross-package import (preserving `import_check_test.go`).

## Architecture

### First-run controller interface (chassis-owned, type-asserted)

Add a narrow optional interface in `internal/chassis`, matching the package's existing
pattern of small structurally-satisfied interfaces:

```go
// FirstRunController is the optional first-run sentinel backing receiver
// setup mode. Production passes *uiserver.BridgeSaver, which satisfies it
// structurally. When nil/unwired (unit-test fixtures), setup mode is never
// active and the chassis behaves exactly as before this feature.
type FirstRunController interface {
    IsFirstRun() bool
    DismissFirstRun() error
}
```

The `Server` resolves it once (e.g. in `New`) by type-asserting `cfg.BridgeSaver`
against `FirstRunController`, storing a possibly-nil `firstRun FirstRunController`.

A single helper governs all setup-mode behavior:

```go
// firstRunActive reports whether the receiver should render/enforce setup
// mode: a first-run controller is wired AND the sentinel is still set.
func (s *Server) firstRunActive() bool { return s.firstRun != nil && s.firstRun.IsFirstRun() }
```

Setup mode is keyed on the **sentinel**, not on whether the configured-enough rule is
met. This keeps a single explicit completion moment: setup mode (banner + open drawer +
cast gate) stays until the operator clicks Finish and `DismissFirstRun()` is called.
The configured-enough rule governs only the *checklist ticks* and whether *Finish is
allowed* — not whether setup mode is showing. (Keying on criteria instead would make the
Finish button vestigial and would re-trigger setup mode if a source were later disabled.)

A pre-configured install whose sentinel is still set (e.g. config edited by hand,
wizard never run) sees setup mode with both checklist items already ticked and Finish
enabled — one click dismisses it. This matches how the legacy `/ui` guard already
behaves for such installs.

### Page data + index handler

Extend `ReceiverPageData`:

```go
SetupMode   bool
SetupStatus SetupStatus // {HostSet, SourceEnabled bool}
```

`handleIndex` sets `SetupMode = s.firstRunActive()` and populates `SetupStatus` from
the configured-enough sub-checks. Setup mode is a **page-load** concern only; the SSE
diff stream (`/receiver/events`) is untouched. Completion reloads the page, so live
SSE setup state is unnecessary.

### Routes

Mounted in `Server.Mount`, same-origin guarded like the existing chassis POST routes:

- `GET /receiver/setup/status` → JSON `{"hostSet":bool,"sourceEnabled":bool,"complete":bool}`.
  Returns the current configured-enough sub-checks. Used by `setup.js` to refresh the
  banner checklist and enable/disable the Finish button after drawer saves. When no
  first-run controller is wired or first-run is already dismissed, returns
  `complete:true` (nothing to do).
- `POST /receiver/setup/finish` → re-validates the configured-enough rule server-side.
  If complete, calls `DismissFirstRun()` and returns `200`. If not, returns `409` with a
  short body naming the unmet item ("set a MiSTer host" / "enable a source"). The
  server is the source of truth; the disabled-button state is only an affordance.

### Cast-initiation gate

Add a small wrapper mirroring the existing `requireSameOrigin` wrapper:

```go
// requireSetupComplete refuses cast-initiation actions with 409 while
// first-run setup mode is active (sentinel still set). No-op once the
// sentinel is dismissed or when no first-run controller is wired.
func (s *Server) requireSetupComplete(next http.Handler) http.Handler // gates on firstRunActive()
```

Wrap the cast-initiation handlers in `Mount` (composing with `requireSameOrigin`):

- `POST /receiver/cast` (`handleCastPost`)
- `POST /receiver/preset/{slot}/cast` (`handlePresetCast`)
- `POST /receiver/streams/cast` (`handleStreamsCast`)
- `POST /receiver/localfiles/cast` (`handleReceiverLocalfilesCast`)
- `POST /receiver/history/play` (`handleHistoryPlayPost`)
- `POST /receiver/aux/start` (`handleAUXStartPost`)

Transport/seek and volume/visualizer/audio-DSP actions are **not** gated: transport and
seek require a live session that cannot exist before configuration, and the others are
harmless cosmetic controls with no output until a MiSTer host exists.

### Client surface

- **Welcome banner partial** rendered server-side when `.SetupMode`, plus a
  `body.receiver.setup` state class so first paint is correct (no flash of
  ungated UI). The banner carries the two-item checklist (ticking `HostSet` /
  `SourceEnabled`) and the "Finish setup" button (disabled until both tick).
- **Drawer auto-open:** under setup mode the shell renders the settings drawer in its
  existing open state (reuse the existing open class; no new drawer logic), focused on
  the network/MiSTer pane.
- **`setup.js`** (~40 lines, embedded under `internal/chassis/static/`, loaded only when
  needed): wires the Finish button (`POST /receiver/setup/finish` → on `200`, navigate
  to `/receiver`), and after any successful drawer save re-fetches
  `GET /receiver/setup/status` to update the checklist ticks and Finish enabled-state.

### Interaction with the legacy `/ui` guard

`firstRunGuard` on `/ui/*` is unchanged. During the interim where both UIs exist:

- A fresh install hitting `/` still redirects to `/ui/` → `firstRunGuard` → `/ui/setup`.
- An operator who instead opens `/receiver` gets chassis setup mode.
- Both dismiss the **same** sentinel, so finishing in either place clears the other.

When the cutover later retires `/ui`, the chassis path stands alone with no change to
this design.

## Testing

Go handler tests (`internal/chassis`):

- `firstRunActive`: true when controller wired + `IsFirstRun()` + not configured; false
  when controller nil, when `IsFirstRun()` false, and when already configured.
- `GET /receiver/setup/status`: correct JSON for each of the four
  (hostSet × sourceEnabled) states, and `complete:true` when no controller is wired.
- `POST /receiver/setup/finish`: `409` (with unmet-item body) when incomplete; `200` +
  `DismissFirstRun()` called when complete; idempotent/`200` when already dismissed.
- Cast-initiation handlers: `409` under active setup mode; pass through once configured
  and once dismissed.
- `handleIndex`: `SetupMode`/`SetupStatus` reflect controller + config state; a
  nil-controller fixture renders no setup mode (default off).

Static/JS:

- CSS scope assertion covers the new `body.receiver.setup` selectors
  (`css_scope_test.go` already enforces `body.receiver`-scoping).
- `setup.js` behavior test under the existing `node --test` testdata convention
  (manual; not in CI), covering Finish POST and status-refresh wiring.

## Design Decisions Worth Revisiting

### New `FirstRunController` interface vs. extending `BridgeSettingsSaver`

Chosen: a separate type-asserted interface. Keeps the configuration-saving contract
(`BridgeSettingsSaver`) orthogonal to first-run lifecycle, and lets existing test
fixtures that only implement `BridgeSettingsSaver` continue to compile and run with
setup mode safely off. Extending `BridgeSettingsSaver` would force every fixture to grow
first-run methods. A reviewer who prefers one fatter interface has a defensible case;
the default stays separate.

### Soft setup mode vs. hard redirect gate

Chosen: soft. The chassis renders the receiver in setup mode (banner + open drawer +
disabled cast) rather than hiding it behind a takeover screen the way `/ui`'s
`firstRunGuard` does. The chassis aesthetic — watching the receiver power on — is part
of the product, and a fresh install with no host produces no output regardless, so the
gate is primarily guidance. Server-side `409` on cast-initiation handlers provides the
hard backstop. A reviewer who wants strict parity with the `/ui` hard gate should raise
it explicitly.

### Drawer reuse vs. dedicated chassis wizard

Chosen: reuse the drawer. Every needed control already exists there, so a dedicated
wizard would duplicate forms and routing for no functional gain. The trade-off is that
the configuration UX is the drawer's, not a linear stepper; given the two-item
completion rule that is acceptable.
