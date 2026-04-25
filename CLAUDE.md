# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A WIP Plex-to-MiSTer cast-target bridge, written in Go. It advertises itself
as a Plex cast target on the LAN (GDM multicast + plex.tv device
registration), receives Plex Companion HTTP commands, transcodes the
selected source via a single FFmpeg process, and streams raw RGB fields
plus s16le PCM over the Groovy_MiSTer UDP protocol to a MiSTer FPGA
driving a 15 kHz analog CRT (v1: NTSC 480i only).

The bridge is stateless across restarts except for `device_uuid` and the
plex.tv auth token, both persisted in `bridge.data_dir`.

## Commands

```bash
# Build (requires Go 1.26)
make build              # both binaries
make build-bridge       # just cmd/mister-groovy-relay
make build-fake         # just cmd/fake-mister

# Lint + test
make lint               # go vet ./...
make test               # unit tests
go test -race ./...     # race detector (CI runs this)
make test-integration   # go test -tags=integration ./tests/integration/...
                        # ^ requires ffmpeg + ffprobe on PATH

# Single test
go test ./internal/core -run TestManager_Pause
go test -tags=integration ./tests/integration -run TestBasic_InitSwitchresClose

# Run without hardware (fake-mister loopback)
./fake-mister -addr :32100 -out ./dumps -png-every 60
# Then set bridge.mister.host = "127.0.0.1" in config.toml and run the bridge.

# Plex pairing (headless)
./mister-groovy-relay --config path/to/config.toml --link
```

CI (`.github/workflows/ci.yml`) runs `go vet`, `go test`, `go test -race`,
then `go test -tags=integration ./...` on every push/PR. Keep all four
green.

## Architecture — the big picture

Four layers, strictly separated:

1. **Adapters** — `internal/adapters/<name>/`. Translate a protocol (Plex
   today; Jellyfin/URL/DLNA planned) into a `core.SessionRequest`. Own
   their TOML section (`[adapters.<name>]`), their UI form schema
   (`Fields()`), their `ApplyScope` table, and their HTTP routes.
2. **Core** — `internal/core/`. Adapter-agnostic. `Manager` owns the
   session FSM and the data-plane lifecycle. **Core imports no adapter
   package** (spec §4.5); there is no `SourceAdapter` interface in core.
   Adapters depend on core, never the reverse.
3. **Data plane** — `internal/dataplane/`. `Plane.Run` spawns ffmpeg,
   performs the Groovy INIT→ACK handshake, sends SWITCHRES, then pumps
   one `BLIT_FIELD_VSYNC` per 59.94 Hz tick and gates AUDIO on ACK bit 6.
   One plane per active session; preemption drops the prior plane and
   awaits its goroutine before starting the next.
4. **Wire protocol** — `internal/groovy/` (packet builders, LZ4, ACK
   parsing, modelines) and `internal/groovynet/` (UDP sender bound to a
   stable source port, platform-specific socket tuning under
   `sender_linux.go` / `sender_windows.go` / `sender_other.go`).

Supporting packages:

- `internal/config/` — sectioned TOML loader (`[bridge]` + per-adapter
  sections), atomic writes (`os.Rename` semantics across platforms), and
  migration from legacy flat-format configs (original preserved as
  `config.toml.pre-ui-migration`).
- `internal/ffmpeg/` — `Probe`, `ProbeCrop`, and `Spawn` for the
  pipeline. Probe runs under a bounded context; crop probe failures
  degrade gracefully to letterbox.
- `internal/ui/` + `internal/uiserver/` — htmx + `html/template` Settings
  UI. `BridgeSaver` / `AdapterSaver` handle validate-then-write, sharing
  one mutex so bridge and adapter saves serialize against each other.
- `internal/fakemister/` + `cmd/fake-mister` — standalone UDP listener +
  PNG dumper that impersonates a MiSTer. Used by integration tests and
  for running the bridge without hardware.

### Invariants that matter when editing

- **One HTTP listener.** `main.go` binds a single socket on
  `bridge.ui.http_port`. Plex Companion mounts `/resources` and
  `/player/*`; UI mounts `/ui/*`. Don't add a second listener.
- **`source_port` stability.** The MiSTer keys its session by
  `<sender_ip>:<source_port>`. If that port changes across a restart, the
  MiSTer treats it as offline. Never bind ephemeral for the Groovy
  sender; `--network=host` is mandatory in Docker for the same reason.
- **`Manager.mu` is never held across network I/O.** `probeForStart` runs
  ffprobe **before** acquiring the lock; `startPlaneLocked` requires the
  caller to already have probed. When awaiting a previous plane's
  `Done()`, the lock is explicitly dropped and re-acquired. Preserve
  this discipline when touching `internal/core/manager.go`.
- **`ApplyScope` governs UI saves.** Three tiers, max-wins across
  changed fields (design §9.1): `ScopeHotSwap` (mutate in place; running
  goroutines re-read), `ScopeRestartCast` (`Manager.DropActiveCast`, next
  play rebuilds the pipeline), `ScopeRestartBridge` (UI toast tells the
  operator to restart the container). When adding a field, you must
  decide its scope and wire it in the adapter's `scopeForPlexField` (or
  equivalent) plus the Bridge field table.
- **Field-order hot-swap.** `tff`↔`bff` flips via `Plane.SetFieldOrder`
  while a cast is live — it's the flagship demo of `ScopeHotSwap`.
  `Manager.SetInterlaceFieldOrder` dual-writes: in-memory bridge config
  for future sessions + the live plane for the current one. Don't break
  either half.
- **Validate before disk write.** Adapters implementing
  `adapters.Validator` let the UI reject bad input without touching
  `config.toml`. Bridge-level saves follow the same contract.

### Startup sequence (cmd/mister-groovy-relay/main.go)

1. Load (or create-and-exit) the sectioned TOML config.
2. Load or generate the device UUID (persisted to `data_dir`).
3. If `--link`, run the plex.tv PIN flow and exit.
4. Build `groovynet.Sender` (binds `source_port`).
5. Build `core.Manager`.
6. Resolve `host_ip` (config override or default-route autodetect via
   UDP dial to `8.8.8.8:53`).
7. Build the adapter registry; for each registered adapter run
   `DecodeConfig` with the TOML primitive from its section.
8. Build the shared `http.ServeMux`; adapters mount Companion routes;
   `ui.Server` mounts `/ui/*`.
9. Start HTTP server, then start each enabled adapter's background work
   (timeline broker, GDM discovery, plex.tv registration loop).
10. On SIGINT/SIGTERM: drain HTTP, stop adapters in registration order.

## Where to read more

- `docs/specs/` and `docs/plans/` are the authoritative design docs.
  Every non-trivial behavior has a dated spec here; code comments
  frequently cite them by section (e.g. "design §7.1").
- `docs/references/` is **stale** — do not consult it.
- `README.md` covers deployment, config fields, and troubleshooting from
  the operator's perspective.
