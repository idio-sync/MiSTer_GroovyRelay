# Settings UI — Design Spec

**Date:** 2026-04-20
**Status:** Brainstorming deliverable. Pre-implementation.
**License:** GPL-3

## 1. Problem Statement

MiSTer_GroovyRelay is configured today by hand-editing `config.toml` in a
bind-mounted Docker volume and restarting the container. Plex account
linking requires an interactive CLI invocation (`docker run ... --link`)
that prints a 4-character code. Both are serviceable for a single-adapter
bridge with a handful of fields, but both friction points compound as
additional cast sources (Jellyfin, DLNA, direct URL) are added.

Two specific iteration loops suffer today:

- The CRT-tuning loop — `interlace_field_order`, `aspect_mode`, and the
  video modeline require trial and error with the eyeball on the tube.
  Every toggle is currently edit → save → `docker restart` → observe.
- First-run setup — one of the steps requires dropping to an interactive
  terminal inside the container.

This spec describes a LAN-accessible web UI that replaces both loops,
introduces an adapter registry to support future cast sources, and
refactors the flat TOML into a sectioned schema that scales past 5
adapters.

## 2. Scope

### In scope

- **Full settings editor** for all fields currently in `config.toml`,
  plus the (future) per-adapter fields for Jellyfin, DLNA, URL casting.
- **Two-tier apply model** — hot-swap where cheap (`interlace_field_order`,
  `device_name`), drop-and-rebuild where necessary (pipeline fields),
  restart-the-container prompt for socket-binding fields.
- **Per-adapter enable/disable** — each cast source is independently
  start/stop-able from the UI.
- **Plex account linking** via browser — the UI takes over the `--link`
  interactive flow; the CLI flag remains as a fallback.
- **Minimal runtime status** — per-adapter status dot (running / starting
  / disabled / error) in the sidebar, refreshed every 3s.
- **Config schema refactor** — flat keys → sectioned tables
  (`[bridge]`, `[adapters.<name>]`). One-shot auto-migration on first
  startup with a backup.
- **Adapter interface** — a small Go contract each cast source implements;
  registry-driven sidebar; hand-written `Fields()` schema per adapter.
- **Single-binary deployment** — no new Docker port, no new build step,
  no external CSS/JS framework. UI mounts on the existing `http_port`.

### Out of scope

- Authentication / authorization — LAN-trusted, same posture as the
  existing Plex Companion API on `:32500`. See §3 for the threat-model
  note and §14 for the follow-up path.
- Full observability dashboard — no "currently casting X" readout, no
  log tail, no FPS counters. Just the status dot.
- JSON API surface — server returns HTML fragments only. A future CLI
  or mobile client would require `/api/v1/*` to be added.
- Mobile / phone-first layout — the UI is desktop-first. It remains
  usable on a phone but is not adapted.
- Auto-restart of the container — the UI never self-restarts; when a
  field requires it, the user is prompted with the `docker restart`
  command. Orchestration is the user's business.
- Real browser end-to-end testing (Playwright etc.) — `httptest` +
  golden-file template tests cover server behavior; visual QA is human.

## 3. Constraints and Context

- **Single Go binary, single Docker image.** Everything the UI needs
  (templates, CSS, fonts, `htmx.min.js`) ships via `embed.FS`.
- **No auth.** Matches the existing `:32500` Plex Companion API posture.
  Threat model: a hostile device on the LAN could re-pair the bridge to
  its own Plex account (which overwrites the owner's token and starts
  sending their library to the owner's CRT), rebind ports, or disable
  adapters. README gains an explicit line: *"The settings UI has no
  authentication. Only expose the `http_port` on networks you trust."*
- **No build pipeline.** No `npm`, no bundler. Templates are
  `html/template`; one vendored `htmx.min.js` (~14 KB) + three bundled
  woff2 fonts (~180 KB total) is the only client asset cost.
- **Runtime target:** any modern desktop browser (Chrome 120+, Firefox
  120+, Safari 17+). No IE, no polyfills.
- **Docker networking:** `--network=host` remains required for GDM
  multicast; the UI lives on the same `:32500` listener that already
  serves the Plex Companion API. No new port to document or expose.

## 4. Architecture

### 4.1 High-level

```
                 ┌────────────────────────────────────────┐
                 │            main.go (orchestrator)      │
                 │  - loads config                        │
                 │  - runs migration if legacy detected   │
                 │  - builds adapter registry             │
                 │  - starts core.Manager + HTTP server   │
                 └──────────────┬─────────────────────────┘
                                │
                 ┌──────────────┼────────────────────────────────┐
                 │              │                                │
                 ▼              ▼                                ▼
      ┌─────────────────┐  ┌─────────────────┐   ┌──────────────────────┐
      │ core.Manager    │  │ adapters.Reg    │   │ HTTP server :32500   │
      │ (adapter-       │  │ (Plex, and      │   │   /resources, /player│
      │  agnostic data  │  │  eventually     │   │     — Plex Companion │
      │  plane)         │  │  Jellyfin, etc.)│   │   /ui/*              │
      └─────────────────┘  └────┬────────────┘   │     — Settings UI    │
                                │                └──────────┬───────────┘
                                │                           │
                                │     ┌─────────────────────┘
                                ▼     ▼
                         ┌────────────────┐
                         │ ui.Server      │
                         │ - renders      │
                         │   templates    │
                         │ - dispatches   │
                         │   saves via    │
                         │   registry     │
                         └────────────────┘
```

### 4.2 Packages

- `internal/config/` — sectioned `Config` struct, `Load()` with legacy
  auto-migration, `WriteAtomic()`.
- `internal/adapters/` — the `Adapter` interface, `Registry`,
  `FieldDef` / `FieldKind` / `ApplyScope` types.
- `internal/adapters/plex/` — Plex adapter refactored to implement the
  interface; today's code lives here and is reshaped, not rewritten.
- `internal/ui/` — new. HTTP handlers, templates (embedded), static
  assets (embedded), per-adapter dispatch, linking driver.
- `internal/core/manager.go` — unchanged in shape; gains an
  `DropActiveCast()` helper called by bridge-scope restart-cast saves.

### 4.3 Why an adapter interface, not a fat central config struct

The codebase already organizes per-source code under
`internal/adapters/plex/`. Using a `map[string]toml.Primitive` in the
core config + an `Adapter` interface in the registry lets each adapter
declare its own fields, own its own validation, own its own lifecycle,
and own its own apply-scope rules without touching a central dispatch
table. Adding Jellyfin becomes "drop a new package, register it in
`main.go`" — a contained PR, not a cross-cutting one.

## 5. Config schema and migration

### 5.1 New sectioned shape

```toml
[bridge]
data_dir  = "/config"
host_ip   = ""                # optional; empty = auto-detect

[bridge.video]
modeline               = "NTSC_480i"
interlace_field_order  = "tff"
aspect_mode            = "auto"
rgb_mode               = "rgb888"
lz4_enabled            = true

[bridge.audio]
sample_rate = 48000
channels    = 2

[bridge.mister]
host        = "192.168.1.50"  # required
port        = 32100
source_port = 32101

[bridge.ui]
http_port = 32500

[adapters.plex]
enabled      = true
device_name  = "MiSTer"
device_uuid  = ""
profile_name = "Plex Home Theater"
server_url   = ""

[adapters.jellyfin]
enabled = false

[adapters.dlna]
enabled = false

[adapters.url]
enabled = false
```

### 5.2 Legacy → sectioned mapping

| Old flat key             | New location                           |
|--------------------------|----------------------------------------|
| `device_name`            | `adapters.plex.device_name`            |
| `device_uuid`            | `adapters.plex.device_uuid`            |
| `mister_host`            | `bridge.mister.host`                   |
| `mister_port`            | `bridge.mister.port`                   |
| `source_port`            | `bridge.mister.source_port`            |
| `http_port`              | `bridge.ui.http_port`                  |
| `host_ip`                | `bridge.host_ip`                       |
| `modeline`               | `bridge.video.modeline`                |
| `interlace_field_order`  | `bridge.video.interlace_field_order`   |
| `aspect_mode`            | `bridge.video.aspect_mode`             |
| `rgb_mode`               | `bridge.video.rgb_mode`                |
| `lz4_enabled`            | `bridge.video.lz4_enabled`             |
| `audio_sample_rate`      | `bridge.audio.sample_rate`             |
| `audio_channels`         | `bridge.audio.channels`                |
| `plex_profile_name`      | `adapters.plex.profile_name`           |
| `plex_server_url`        | `adapters.plex.server_url`             |
| `data_dir`               | `bridge.data_dir`                      |

### 5.3 Go struct shape

```go
// internal/config/config.go
type Config struct {
    Bridge   BridgeConfig              `toml:"bridge"`
    Adapters map[string]toml.Primitive `toml:"adapters"`
}

type BridgeConfig struct {
    DataDir string       `toml:"data_dir"`
    HostIP  string       `toml:"host_ip"`
    Video   VideoConfig  `toml:"video"`
    Audio   AudioConfig  `toml:"audio"`
    MiSTer  MisterConfig `toml:"mister"`
    UI      UIConfig     `toml:"ui"`
}
```

Each adapter package owns its own `Config` struct, defaults, and
`Validate()`. The core `Load()` uses `toml.Primitive` to defer decoding
of `[adapters.*]`; each adapter decodes its own slice during
registration. This keeps the core unaware of adapter field details.

### 5.4 One-shot migration

On `Load()`:

1. Read `config.toml`.
2. If the top-level has any known legacy flat key (e.g., `mister_host`)
   and the `[bridge]` table is absent, treat as legacy.
3. Back up the original to `config.toml.pre-ui-migration` (mode 0644).
4. Build the new sectioned struct from the legacy values, filling in
   defaults for anything missing.
5. Write the sectioned TOML atomically (tempfile + rename + fsync).
6. Log at INFO: *"Migrated legacy flat config to sectioned format;
   backup at config.toml.pre-ui-migration"*.
7. Continue startup against the in-memory sectioned config.

Detection is by presence of the `[bridge]` table — once it exists,
migration is never attempted again. Migration is one-shot and
idempotent; if re-run against an already-migrated file, it does
nothing.

### 5.5 Atomic writes

Every config write (UI saves, migration) goes through
`config.WriteAtomic(cfg, path)`:

1. Marshal to bytes.
2. Write to `<path>.tmp.<random>` in the same directory.
3. `fsync()` the tempfile.
4. `os.Rename(tmp, path)` — atomic on POSIX; Docker bind mounts are
   fine.
5. `fsync()` the parent directory.

A crash at any step leaves either the old file or the new file intact
— never torn.

## 6. Adapter interface

### 6.1 The contract

Lives at `internal/adapters/adapter.go`:

```go
type Adapter interface {
    // Name is the TOML key: [adapters.<name>]
    Name() string

    // DisplayName is shown in the UI sidebar.
    DisplayName() string

    // Fields declares the UI form schema, in render order.
    Fields() []FieldDef

    // DecodeConfig parses this adapter's TOML section. Called at
    // startup and after every UI save.
    DecodeConfig(raw json.RawMessage) error

    // Start begins serving. Idempotent; must respect ctx cancellation.
    Start(ctx context.Context) error

    // Stop gracefully shuts down listeners and any active cast.
    Stop() error

    // Status returns lifecycle state for the UI status dot.
    Status() Status

    // ApplyConfig receives a validated new config and decides what
    // scope of change is needed. Returns what scope was used.
    ApplyConfig(new json.RawMessage) (ApplyScope, error)
}

type Status struct {
    State     State     // Stopped | Starting | Running | Error
    LastError string
    Since     time.Time
}

type ApplyScope int
const (
    ScopeHotSwap ApplyScope = iota
    ScopeRestartCast
    ScopeRestartBridge
)
```

### 6.2 Field schema

```go
type FieldDef struct {
    Key         string
    Label       string
    Help        string
    Kind        FieldKind
    Enum        []string    // when Kind == KindEnum
    Default     any
    Required    bool
    ApplyScope  ApplyScope
    Placeholder string
    Section     string      // optional grouping in the panel
}

type FieldKind int
const (
    KindText FieldKind = iota
    KindInt
    KindBool
    KindEnum
    KindSecret
)
```

Hand-written `Fields()` per adapter (not struct-tag reflection) —
~50 total entries across 5 adapters, and hand-written lets each field
carry real human help text and correct `ApplyScope` metadata.

### 6.3 Registry

```go
type Registry struct {
    mu       sync.RWMutex
    order    []string
    adapters map[string]Adapter
}

func (r *Registry) Register(a Adapter) error
func (r *Registry) Get(name string) (Adapter, bool)
func (r *Registry) List() []Adapter   // preserves registration order
```

`main.go` at startup:

```go
reg := adapters.NewRegistry()
reg.Register(plex.New(bridgeCfg, logger))
// future: reg.Register(jellyfin.New(...))

for _, a := range reg.List() {
    raw := cfg.Adapters[a.Name()]
    if err := a.DecodeConfig(raw); err != nil { /* fatal */ }
    if a.IsEnabled() { go a.Start(ctx) }
}
```

### 6.4 Plex adapter refactor

- `plex.Adapter` gains `Name()/DisplayName()/Fields()/Status()/ApplyConfig()`.
- `plex.Config` moves into `internal/adapters/plex/config.go`, holding
  only Plex-specific fields.
- Existing `Start()/Stop()` signatures are preserved — the interface
  was written to match.
- `core.Manager` stays adapter-agnostic; adapters call into it to drive
  a cast session but Manager doesn't know Plex from Jellyfin.

## 7. HTTP surface

### 7.1 Path allocation

| Prefix                         | Owner                     |
|--------------------------------|---------------------------|
| `/resources`                   | Plex Companion (existing) |
| `/player/*`                    | Plex Companion (existing) |
| `/timeline/*`                  | Plex Companion (existing) |
| `/`                            | UI (redirects to `/ui/`)  |
| `/ui/`                         | UI shell                  |
| `/ui/static/*`                 | embedded CSS, fonts, htmx |
| `/ui/bridge`                   | GET bridge panel          |
| `/ui/bridge/save`              | POST bridge save          |
| `/ui/adapter/{name}`           | GET adapter panel         |
| `/ui/adapter/{name}/save`      | POST adapter save         |
| `/ui/adapter/{name}/toggle`    | POST enable/disable       |
| `/ui/adapter/{name}/status`    | GET status fragment       |
| `/ui/sidebar/status`           | GET full sidebar status   |
| `/ui/plex/link/start`          | POST begin PIN flow       |
| `/ui/plex/link/status`         | GET poll for token        |
| `/ui/plex/unlink`              | POST clear token          |

Future adapters add their own `/ui/{name}/*` setup routes (Jellyfin
will need e.g. `/ui/jellyfin/test-connection`).

### 7.2 Server

Single `net/http.ServeMux` (standard library, Go 1.22+ pattern matching
covers the `{name}` captures). Plex Companion handlers mount first;
`ui.Server.Mount(mux)` adds the UI routes.

### 7.3 Conventions

- **Content-Type: `text/html`** everywhere under `/ui/`. No JSON.
- **htmx fragments.** Every save returns either the updated panel HTML
  (with a toast) or the panel with inline field errors. Full-page
  reloads are avoided.
- **Per-adapter mutex.** Each adapter has a `sync.Mutex` held for the
  full save lifecycle (validate → write config → `ApplyConfig()`).
  Prevents concurrent saves on the same adapter from racing. Saves
  across different adapters proceed in parallel.
- **Status polling is cheap.** The sidebar container polls
  `GET /ui/sidebar/status` every 3s (`hx-trigger="every 3s"`) and
  swaps the full status-dot list in one round trip — N adapters cost
  one request, not N. Per-adapter `GET /ui/adapter/{name}/status`
  exists for the panel header (which shows the same state when the
  adapter's panel is open), polled by that fragment only while the
  panel is visible.

### 7.4 Embedded assets

```go
//go:embed templates/*.html static/*
var assets embed.FS
```

Single binary stays single binary. Template bundle:

```
internal/ui/
  templates/
    shell.html           # full page
    bridge-panel.html    # bridge config form
    adapter-panel.html   # generic, iterates FieldDef list
    status-badge.html    # dot + state text
    plex-link.html       # plex.tv PIN fragment (state-dependent)
    toast.html           # save-result banner
  static/
    app.css
    htmx.min.js
    fonts/
      SpaceGrotesk-600.woff2
      InterTight-400.woff2
      InterTight-500.woff2
      JetBrainsMono-400.woff2
```

## 8. UI layout and visual design

### 8.1 Aesthetic direction — "Engineer's Console"

The audience is homelab tinkerers aligning an analog CRT. The UI should
feel like a well-typeset technical dossier, not a SaaS settings page.
Concrete commitments:

- **Left-aligned, asymmetric layout.** Content capped at ~680px on the
  left edge of the panel column, not centered.
- **Numbered sections** (`01 — Identity`, `02 — Network`, ...) borrow
  an editorial convention to signal "this is a document, read it
  sequentially."
- **Read-first, click-to-edit.** Fields render as labeled values by
  default (value in mono, label dim). Clicking a value turns it into a
  plain `<input>` with a subtle accent underline. Enter commits;
  Escape cancels. Progressive disclosure, not a big grid of form
  boxes.
- **Three-letter status codes**: `RUN · since 14:22:07`, `ERR · port
  in use`, `OFF`, `---` (starting). In mono, uppercase. Color comes
  from the text itself (subtle, not glowing dots).

### 8.2 Shell layout

```
┌────────────────┬────────────────────────────────────────────┐
│  BRIDGE ──     │                                            │
│                │   Plex                                     │
│  ADAPTERS      │   ─────────────────────────────            │
│  ─ Plex        │                                            │
│  ─ Jellyfin    │   A Plex cast target advertised on LAN.    │
│  ─ DLNA        │                                            │
│  ─ URL         │   01 — Status                              │
│                │      RUN · since 14:22:07                  │
│                │                                            │
│                │   02 — Identity                            │
│                │      Device Name   MiSTer                  │
│                │      Profile       Plex Home Theater ▾     │
│                │                                            │
│                │   03 — plex.tv Account                     │
│                │      Linked · jake@example.com             │
│                │      ── Unlink                             │
│                │                                            │
│                │   04 — Server (optional)                   │
│                │      Pin URL       auto-discover           │
│                │                                            │
│                │                            Save Plex ▸     │
└────────────────┴────────────────────────────────────────────┘
```

Sidebar links use `hx-get` + `hx-target="#panel"` + `hx-push-url` — click swaps
only the right panel and updates the address bar.

### 8.3 Typography

Three bundled woff2 fonts (no system-UI fallback as first choice):

- **Display** — *Space Grotesk* 600 (section headings, adapter names).
- **Body** — *Inter Tight* 400/500 (help text, labels, buttons).
- **Mono** — *JetBrains Mono* 400 (values: IPs, ports, UUIDs, enum read
  states, status codes).

Purposeful mono for technical values — not as "developer tool"
shorthand, but because IPs, ports, and UUIDs literally are mono.

### 8.4 Color — tinted warm dark, dark by default

```css
:root {
  color-scheme: dark;
  --bg:        oklch(0.17 0.015 60);   /* near-black, warm tint */
  --surface:   oklch(0.22 0.018 60);
  --border:    oklch(0.32 0.022 60);
  --text:      oklch(0.92 0.012 75);
  --text-dim:  oklch(0.62 0.015 70);
  --accent:    oklch(0.78 0.14 65);    /* muted amber */
  --ok:        oklch(0.72 0.13 150);
  --warn:      oklch(0.78 0.15 85);
  --err:       oklch(0.65 0.22 25);
}
```

Neutrals tinted toward the amber accent hue (chroma 0.015 at hue 60–70)
for subconscious cohesion. No cyan, no purple, no neon. Amber chosen
for CRT-adjacent feel without being a glowing-phosphor pastiche.

### 8.5 Motion

- **Panel swap on sidebar click** — 180ms `ease-out-quart`, opacity
  0→1 + translateY(4px→0) via the CSS view-transitions API and htmx's
  `transition:true` swap modifier.
- **Toast entrance** — 240ms ease-out, translateY(-8px→0) + opacity,
  auto-dismiss after 4s with 120ms fade out (persistent toasts skip
  auto-dismiss).
- **Status text color** — 300ms transition on change.
- **Reduced motion** — all motion gated on
  `@media (prefers-reduced-motion: no-preference)`. Under reduce, swaps
  are instant.

### 8.6 Interaction details

- **One primary per view** — `Save Plex ▸` bottom right, accent-colored,
  small triangle glyph. Ghost style (`── Unlink`, em-dash prefix) for
  secondary actions. No filled secondary buttons.
- **Progressive disclosure** — advanced network settings (`host_ip`) tucked
  under a `⌄ Show advanced` toggle. 90% of users never see it.
- **Empty states teach.** Plex when unlinked reads: *"This adapter is
  configured but not linked. Linking authorizes the bridge to receive
  cast commands from your Plex account."*
- **First-run hint** — blue banner on the Bridge panel: *"Quick start:
  (1) set your MiSTer's IP below, (2) save, (3) go to Plex and link
  your account."* Dismissible; dismissal persisted as
  `data_dir/.first-run-complete`.

## 9. Apply-scope rules

### 9.1 Semantics

Every `FieldDef` declares one of three scopes. On save:

- **Hot-swap** — absorbed in place, active cast unaffected, toast:
  *"Saved — applied live"*.
- **Restart-cast** — if a cast is active it's dropped and rebuilt; toast:
  *"Saved — cast restarted"* (or *"Saved"* if no cast was active).
- **Restart-bridge** — written to disk; running process is unchanged;
  persistent toast: *"Saved. Restart the container to apply."*

**Max-scope-wins:** if a save touches fields with mixed scopes, the
scope used is the highest (restart-bridge > restart-cast > hot-swap).
Simple and correct.

### 9.2 Bridge fields

| Field                                  | Scope           |
|----------------------------------------|-----------------|
| `bridge.data_dir`                      | restart-bridge  |
| `bridge.host_ip`                       | restart-bridge  |
| `bridge.video.modeline`                | restart-cast    |
| `bridge.video.interlace_field_order`   | **hot-swap**    |
| `bridge.video.aspect_mode`             | restart-cast    |
| `bridge.video.rgb_mode`                | restart-cast    |
| `bridge.video.lz4_enabled`             | restart-cast    |
| `bridge.audio.sample_rate`             | restart-cast    |
| `bridge.audio.channels`                | restart-cast    |
| `bridge.mister.host`                   | restart-bridge  |
| `bridge.mister.port`                   | restart-bridge  |
| `bridge.mister.source_port`            | restart-bridge  |
| `bridge.ui.http_port`                  | restart-bridge  |

`interlace_field_order` is the hero hot-swap: the whole reason for the
two-tier model. Implementation: `videopipe.go` gains
`SetFieldOrder(order)` which takes a write lock on its field-polarity
flag. Next emitted field uses the new polarity. No pipeline rebuild.

Bridge-level restart-cast saves (e.g., audio sample rate) iterate the
registry and call `adapter.DropActiveCast()` on each — the shared
ffmpeg pipeline is drained and the next cast from any adapter rebuilds
with the new values.

### 9.3 Plex adapter fields

| Field                         | Scope            |
|-------------------------------|------------------|
| `adapters.plex.enabled`       | special (toggle) |
| `adapters.plex.device_name`   | hot-swap         |
| `adapters.plex.device_uuid`   | restart-bridge   |
| `adapters.plex.profile_name`  | restart-cast     |
| `adapters.plex.server_url`    | restart-cast     |

- `device_uuid` has a *Regenerate* button that warns: *"This orphans
  your current plex.tv device entry; you'll be re-linked on next
  start."* Users should almost never need it.
- The enable toggle is not a scoped save — `false→true` calls
  `Start()`; `true→false` calls `Stop()`.

### 9.4 Dispatcher

```go
func (a *Adapter) ApplyConfig(raw json.RawMessage) (ApplyScope, error) {
    var new plex.Config
    if err := json.Unmarshal(raw, &new); err != nil { return 0, err }
    if err := new.Validate(); err != nil { return 0, err }

    diff := a.cfg.Diff(&new)
    scope := ScopeHotSwap
    for _, key := range diff {
        if s := a.scopeFor(key); s > scope { scope = s }
    }

    switch scope {
    case ScopeHotSwap:
        a.applyHotSwap(&new)
    case ScopeRestartCast:
        if a.session.Active() { a.session.Drop("config change") }
        a.cfg = &new
    case ScopeRestartBridge:
        a.cfg = &new
    }
    return scope, nil
}
```

### 9.5 Restart-bridge UX

Persistent toast (no auto-dismiss):

```
Saved. Restart the container to apply.
    docker restart mister-groovy-relay
```

Command is selectable + copy-icon. No auto-restart: orphaning a
running Plex cast without warning is user-hostile, and Docker
orchestration is the operator's responsibility.

## 10. Plex linking flow

### 10.1 State machine

```
UNLINKED ──(POST /ui/plex/link/start)──► REQUESTED
                                             │
                                             │ (server polls plex.tv every 2s)
                                             │
                                             ▼
                                         LINKED
                                             │
                                             │ (POST /ui/plex/unlink)
                                             │
                                             ▼
                                         UNLINKED

error branches from REQUESTED:
  - PIN expired (15 min)  → UNLINKED  (toast: code expired)
  - plex.tv 5xx           → UNLINKED  (toast: plex.tv unreachable)
  - user abandons browser → UNLINKED  (garbage-collected after 15 min)
```

State is **in-memory**, one `*PendingLink` per adapter. Second click on
"Link" abandons any prior pending request; no persistence of pending
state across restarts (the flow is ~30 seconds; start over is fine).

### 10.2 Endpoints

| Method | Path                    | Returns                                                           |
|--------|-------------------------|-------------------------------------------------------------------|
| POST   | `/ui/plex/link/start`   | 200 + fragment with code + poll attribute. Starts polling goroutine. |
| GET    | `/ui/plex/link/status`  | 202 + retry fragment (pending) / 200 + linked fragment / 410 + try-again fragment. |
| POST   | `/ui/plex/unlink`       | 200 + unlinked fragment. Stops adapter, renames `plex.json` → `.plex.json.unlinked-<ts>`, restarts adapter. |

### 10.3 Fragment UX

Unlinked section reads *"OFF · not linked"* with a `── Link Plex
Account` link. Click swaps the section for the requested-state
fragment:

```
03 — plex.tv Account
     PEND · waiting for plex.tv

     Open  plex.tv/link  and enter this code:

     ┌─────────────┐
     │  G 7 Q R    │     ← JetBrains Mono, tracked wide, ~48px
     └─────────────┘

     Code expires in 14:47
     (auto-polling — this page will update when you approve)

     ── Cancel
```

Fragment has `hx-get="/ui/plex/link/status" hx-trigger="every 2s"
hx-target="closest section"`. Each poll refreshes the countdown or
transitions to linked/expired. Server-rendered countdown; no JS timer.

Linked section reads *"RUN · linked as jake@example.com"*. Account
email comes from `plex.tv/api/v2/user` on first use, cached in memory.

### 10.4 Unlink

1. `adapter.Stop()` — plex.tv deregister, /resources down, cast dropped.
2. Rename `data_dir/plex.json` → `.plex.json.unlinked-<timestamp>`
   (rename rather than delete, so accidental unlinks are recoverable).
3. Clear in-memory token.
4. `adapter.Start()` — adapter comes back up unlinked (OFF).
5. Toast: *"Unlinked. Plex casting disabled until re-linked."*

### 10.5 CLI `--link` stays

The existing `--link` flag remains for headless recovery (UI broken,
port rebound) and scripted automation (Unraid templates, Ansible).
Both paths call the same `plex.RequestLink()` and `plex.PollForToken()`
functions — CLI writes to stdout, UI writes HTML.

## 11. Validation and error handling

### 11.1 Error taxonomy

| Kind              | Source                                               | Surface                                                           |
|-------------------|------------------------------------------------------|-------------------------------------------------------------------|
| Validation error  | Value fails static rules (range, format, required).  | Inline, under the field. Form re-renders; nothing is persisted.   |
| Apply error       | Valid values, but `ApplyConfig()` fails at runtime.  | Persistent red toast + adapter status goes `ERR`. File is written; running state is not. |
| Transport error   | Disk I/O, template render, handler panic.            | Red toast: *"Save failed: <err>. Your changes were not persisted."* |

### 11.2 Validation lives in adapters

Each adapter has a `Config.Validate() error` returning `FieldErrors`
(slice of `FieldError{Key, Msg}`). The save handler calls
`Validate()` before any disk write. Invalid saves re-render the form
with per-field error text; input values are preserved (not reverted).

### 11.3 Write-before-apply

Save handler:

1. Parse form → candidate `Config`.
2. `candidate.Validate()`. If errors: re-render form with errors, stop.
3. Serialize full config → `WriteAtomic(path)`.
4. Call `adapter.ApplyConfig(candidate)`.
5. If apply fails: persistent toast *"Save persisted but apply failed:
   <err>. Running state is unchanged; on-disk config reflects your
   change. Fix and re-save."* Status badge goes `ERR`.
6. Re-render panel with the new values.

**File wins.** A crash between write and apply leaves the file
correct for the next startup to pick up. The alternative
(apply-then-write) loses user intent on crash. File-ahead-of-process
is clearly signaled by the `ERR` badge and recovers on either `docker
restart` or a fixing-save.

No rollback on apply failure (Option A in brainstorming). Rollback is
tempting but introduces races with concurrent saves and worse failure
modes when disk/permissions are the underlying problem.

### 11.4 Concurrent edits

Per-adapter mutex serializes saves within an adapter. Saves across
adapters proceed in parallel. No MVCC, no version tokens — the use
case (one admin on a LAN, occasionally) doesn't warrant it.

### 11.5 Secrets

v1 has no secret fields beyond the plex.tv token (which is written by
the linking flow, not edited in the form). For future adapters
(Jellyfin API key, URL basic-auth password) use
`FieldKind: KindSecret`:

- Rendered as `<input type="password">` with empty value + help text
  *"Leave empty to keep existing"*.
- On save, empty means unchanged; clearing requires a `── Clear` button.
- Stored plaintext in `config.toml` (matches today's `plex.json`
  posture). File-system permissions are the only protection — same
  as the existing token file. Revisit if v2 needs stronger.
- Never logged, even redacted.

### 11.6 Server-side logging

Every save logs at INFO: adapter name, changed field keys (not
values), applied scope, success/failure. Failures log at ERROR with
the full error chain. Values of `KindSecret` fields are never logged.

## 12. Testing strategy

### 12.1 Layers

1. **Unit** (`*_test.go`) — pure Go, no HTTP, no files.
2. **HTTP handler** — `httptest.NewRecorder` + real templates + fake
   registry.
3. **Golden-file template** — canonical data in, committed
   `.golden.html` out, diff on CI. Update via `go test -update`.
4. **Integration** — extend `tests/integration/` with full-bridge
   scenarios driven via HTTP against a `fakemister` + `fakePlexTV`.

No Playwright, no headless Chrome, no JS test runner. htmx is tested
indirectly via the HTML fragments the server returns.

### 12.2 High-value test coverage

**Config + migration (critical — data-loss risk):**

- `TestConfig_LegacyMigration` — every legacy field maps correctly.
- `TestConfig_MigrationIsIdempotent` — no-op on already-migrated file.
- `TestConfig_MigrationBackup` — backup bytes and mode.
- `TestConfig_WriteAtomic_NoPartialOnCrash` — fault-injected
  `io.Writer`, assert old or new always intact.
- `TestConfig_Validate` — table-driven, one row per rule.

Target ~90% branch coverage on legacy detection + field mapping.

**Adapter interface + registry:**

- `TestRegistry_RegisterDuplicate` — duplicate name errors.
- `TestRegistry_ListPreservesOrder`.
- `TestRegistry_Get_Missing` — `(nil, false)`.
- `go test -race` on concurrent reads + writes.

**Apply-scope:**

- `TestPlexAdapter_ApplyConfig_HotSwap` — device_name change returns
  `ScopeHotSwap`, no Stop/Start.
- `TestPlexAdapter_ApplyConfig_RestartCast` — profile_name change
  drops mocked active session.
- `TestPlexAdapter_ApplyConfig_MaxScopeWins` — mixed fields → max
  scope.
- `TestPlexAdapter_ApplyConfig_InvalidRejected` — malformed input, no
  state mutation.
- Table-driven `TestPlexAdapter_ScopeMatchesFieldDef` — every
  `FieldDef.ApplyScope` matches the dispatcher's declared scope.

**UI handlers:**

- `TestUIHandlers_BridgeSave_HappyPath` — POST valid, assert fragment
  + file content + toast text.
- `TestUIHandlers_BridgeSave_ValidationError` — POST invalid, assert
  inline field error, file unchanged.
- `TestUIHandlers_BridgeSave_ApplyFailure` — fake registry returns
  apply error, assert persistent toast + file written + `ERR` badge.
- `TestUIHandlers_AdapterToggle` — Start/Stop called exactly once.
- `TestUIHandlers_StatusBadge` — fragment per `State`.
- `TestUIHandlers_Concurrent_SameAdapter` — serialized by mutex.
- `TestUIHandlers_Unknown_Adapter` — 404 fragment, no panic.

**Plex linking:**

- `TestUIHandlers_LinkStart` — POST, assert PIN requested, fragment
  with code + poll attribute.
- `TestUIHandlers_LinkStatus_Pending` — 202 + retry fragment.
- `TestUIHandlers_LinkStatus_Linked` — 200 + linked fragment + token
  file mode 0600.
- `TestUIHandlers_LinkStatus_Expired` — 410 + try-again fragment.
- `TestUIHandlers_LinkStart_AbandonsPrevious`.
- `TestUIHandlers_Unlink` — token renamed + adapter restarted.

**Golden-file templates:**

One test per template, canonical struct in, `.golden.html` diff on
output. Files live at `internal/ui/templates/testdata/golden/*.html`
and are reviewed as part of any UI-touching PR.

**Integration:**

- `TestIntegration_Save_InterlaceFlip_LiveApply` — bridge + cast
  active + POST `bff`, assert next video field has flipped polarity,
  cast not dropped.
- `TestIntegration_Save_AspectMode_RestartsCast` — cast dropped +
  would rebuild.
- `TestIntegration_MigrationAtStartup` — stage legacy file + start,
  assert migration ran + bridge healthy.
- `TestIntegration_ToggleDisablesAdapter` — plex.tv deregister +
  /resources 410 after disable.

### 12.3 What is not tested

- CSS / visual — structure via golden HTML, styling by human eye.
- htmx client behavior — trusted upstream.
- Browser matrix — target "modern browser," no polyfills.
- Real Plex account integration — mocked plex.tv in tests; real
  linking QA'd once on first user and then trusted (the protocol is
  stable).

### 12.4 CI

`go test -race ./...` per recent convention (commit `9928243`). No
coverage gate number, but migration + apply-scope tests should hit
~90% branch coverage and are worth calling out as a review gate for
those files.

## 13. Migration and rollout

Migration is automatic on first startup after the new binary lands —
no flag, no user action. Users pulling the updated Docker image will
find:

1. Their bridge starts up.
2. It detects legacy flat `config.toml`, writes a backup to
   `config.toml.pre-ui-migration`, writes the new sectioned version.
3. The bridge continues running on the new config.
4. The UI is reachable at `http://<host>:32500/` (redirects to `/ui/`).

No breaking change to the Docker invocation. No new port. No new
volume. Existing `plex.json` token file is unaffected. The CLI
`--link` flag still works for headless recovery.

## 14. Future work / explicitly deferred

- **Authentication.** Option B from brainstorming (shared token in
  `data_dir/ui_token`, mode 0600, bearer cookie) is the obvious next
  step if a user asks or the README warning feels insufficient. v1
  ships without; the layer will be a middleware addition, not a
  refactor.
- **JSON API.** Under `/api/v1/*`, parallel to the HTML surface. Makes
  the settings remote-controllable by CLI or a future mobile client.
- **Jellyfin / DLNA / URL adapters.** Each lands as a new package
  implementing the same interface. The UI adapts automatically via the
  registry.
- **Observability dashboard.** "Currently casting X" readout, last-N
  log lines, FPS / packet counters. Full Section C from brainstorming
  — explicitly out of scope for v1.
- **Per-user or per-adapter auth scoping** (e.g., a viewer role that
  can see status but not change config). Only if the single-admin
  assumption breaks.
- **Secrets split to a separate file** with mode 0600, if any future
  adapter needs higher secret confidentiality than "config file
  permissions."

## 15. Glossary

- **Adapter** — a cast-source implementation (Plex, future Jellyfin,
  etc.) conforming to the `adapters.Adapter` interface.
- **Registry** — the runtime set of registered adapters, iterated by
  the UI to render the sidebar and dispatch saves.
- **Hot-swap / restart-cast / restart-bridge** — the three apply
  scopes (§9.1).
- **Field def** — the per-field UI schema entry carrying label, help
  text, kind, and apply scope.
- **Fragment** — a partial HTML response swapped in by htmx, as
  opposed to a full page.
