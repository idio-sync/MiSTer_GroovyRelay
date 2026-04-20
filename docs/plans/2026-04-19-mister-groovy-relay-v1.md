# MiSTer_GroovyRelay v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go binary that acts as a Plex Companion cast target and streams video/audio to a MiSTer FPGA running the Groovy_MiSTer core, producing 480i60 NTSC output suitable for a 15 kHz CRT.

**Architecture:** Single Go binary with a control plane / data plane split. Control plane handles HTTP (Plex Companion), multicast discovery, plex.tv linking, session state, and FFmpeg lifecycle. Data plane reads video + audio from one FFmpeg process and emits Groovy protocol UDP packets to the MiSTer. A separate `fake-mister` binary and an integration test suite are first-class deliverables so most changes get automated feedback without hardware.

**Tech Stack:** Go 1.22+, FFmpeg (external process), TOML config, `github.com/BurntSushi/toml`, `github.com/pierrec/lz4/v4`, Go stdlib (`net/http`, `net/url`, `encoding/xml`, `os/exec`), standard testing with integration-test build tag.

**Source of truth:** `docs/specs/2026-04-19-mister-groovy-relay-design.md`. **Every implementer MUST read this and the per-repo reference docs in `docs/references/` before starting.**

---

## Phase Overview and Review Checkpoints

The plan has 14 phases. Review checkpoints sit between logical groupings — stop and let the user validate before proceeding.

| Phase | Name | Review checkpoint? |
|---|---|---|
| 1 | Project scaffolding | — |
| 2 | Groovy protocol library | ✅ after Phase 2 |
| 3 | fake-MiSTer sink | ✅ after Phase 3 |
| 4 | Groovy UDP sender + first integration test | ✅ after Phase 4 (first real wire check) |
| 5 | FFmpeg pipeline | — |
| 6 | Data plane orchestrator | ✅ after Phase 6 (streaming proof) |
| 7 | Plex adapter — Companion HTTP | — |
| 8 | Plex adapter — GDM discovery | — |
| 9 | Plex adapter — plex.tv account linking | ✅ after Phase 9 (discoverable from Plex apps) |
| 10 | Core — session orchestration (adapter-agnostic) | — |
| 11 | Main binary assembly | ✅ after Phase 11 (full local e2e against fake) |
| 12 | Integration test suite | — |
| 13 | Docker + CI | ✅ after Phase 13 (image builds and tests pass in CI) |
| 14 | Documentation + first manual e2e | ✅ final (real MiSTer + real Plex) |

At each ✅ checkpoint, stop and report to the user with:
- What works now
- What's been validated (unit tests passing, integration scenarios passing)
- What still requires manual validation

---

## File Structure

```
mister-groovy-relay/
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile
├── .dockerignore
├── .github/workflows/ci.yml
├── README.md
├── config.example.toml
├── cmd/
│   ├── mister-groovy-relay/main.go        # primary binary entrypoint
│   └── fake-mister/main.go                # test sink binary entrypoint
├── internal/
│   ├── config/
│   │   ├── config.go                      # TOML loading + defaults + validation
│   │   └── config_test.go
│   ├── logging/
│   │   └── logging.go                     # slog wrapper
│   ├── groovy/                            # protocol library (no network)
│   │   ├── constants.go                   # command IDs, rgb modes, sample rates
│   │   ├── builder.go                     # constructs all 5 command packets
│   │   ├── builder_test.go
│   │   ├── lz4.go                         # block compress/decompress wrappers
│   │   ├── lz4_test.go
│   │   ├── ack.go                         # 13-byte ACK parsing
│   │   └── ack_test.go
│   ├── groovynet/                         # network layer (uses groovy + net)
│   │   ├── sender.go                      # UDP sender with stable source port
│   │   ├── sender_test.go
│   │   ├── drainer.go                     # non-blocking ACK reader goroutine
│   │   └── drainer_test.go
│   ├── fakemister/                        # implementation used by fake-mister binary
│   │   ├── listener.go                    # UDP receive + command parser
│   │   ├── listener_test.go
│   │   ├── reassembler.go                 # payload reassembly
│   │   ├── reassembler_test.go
│   │   ├── dumper.go                      # PNG / WAV output
│   │   ├── dumper_test.go
│   │   └── recorder.go                    # session recording for test assertions
│   ├── ffmpeg/
│   │   ├── probe.go                       # ffprobe wrapper
│   │   ├── probe_test.go
│   │   ├── pipeline.go                    # filter graph + invocation builder
│   │   ├── pipeline_test.go
│   │   ├── process.go                     # spawn / supervise / kill
│   │   └── process_test.go
│   ├── dataplane/
│   │   ├── clock.go                       # monotonic 59.94 Hz field timer
│   │   ├── clock_test.go
│   │   ├── videopipe.go                   # FFmpeg stdout → fields
│   │   ├── videopipe_test.go
│   │   ├── audiopipe.go                   # FFmpeg audio → PCM chunks
│   │   ├── audiopipe_test.go
│   │   ├── plane.go                       # orchestrator: glues pipe readers to sender
│   │   └── plane_test.go
│   ├── core/                              # adapter-agnostic control-plane root
│   │   ├── types.go                       # SessionRequest, SessionStatus
│   │   ├── types_test.go
│   │   ├── state.go                       # state machine enum + transitions
│   │   ├── state_test.go
│   │   ├── manager.go                     # session orchestrator — takes core.SessionRequest
│   │   └── manager_test.go
│   └── adapters/
│       └── plex/                          # Plex adapter: translates Plex Companion → core
│           ├── companion.go               # HTTP handlers for Plex Companion endpoints
│           ├── companion_test.go
│           ├── profile.go                 # device capability profile
│           ├── profile_test.go
│           ├── transcode.go               # PMS media URL construction
│           ├── transcode_test.go
│           ├── timeline.go                # subscribe list + 1 Hz broadcaster + poll
│           ├── timeline_test.go
│           ├── discovery.go               # GDM multicast listener/advertiser
│           ├── discovery_test.go
│           ├── linking.go                 # plex.tv PIN + device registration
│           ├── linking_test.go
│           ├── tokenstore.go              # JSON-persisted auth token
│           └── adapter.go                 # top-level Start/Stop wiring for main.go
└── tests/
    └── integration/
        ├── helper_test.go                 # spawn sender + fake, set up ports
        └── scenarios_test.go              # cast, seek, pause, preempt, stop
```

**Module boundaries to respect:**
- `internal/groovy/` has zero network dependencies. Pure byte construction and parsing.
- `internal/groovynet/` depends on `internal/groovy/` and adds UDP.
- `internal/fakemister/` depends on `internal/groovy/` (to understand packets). It is used by `cmd/fake-mister/` and by integration tests.
- `internal/dataplane/` depends on `internal/groovy/`, `internal/groovynet/`, `internal/ffmpeg/`. This is the data plane in architectural terms.
- `internal/core/` is the adapter-agnostic control-plane root. Owns the state machine and spawns `internal/dataplane/`. **Imports no adapter packages.** Takes generic `core.SessionRequest` arguments.
- `internal/adapters/plex/` is the v1 Plex adapter. Imports `internal/core/` but not vice-versa. Translates Plex Companion HTTP requests into `core.SessionRequest` and subscribes to `core.SessionStatus` for timeline reporting.
- `cmd/mister-groovy-relay/main.go` wires core + Plex adapter together explicitly. No plugin registry.

**Why this layering:** adapters added in v2+ (URL-input, Jellyfin) will each live under `internal/adapters/<name>/` and plug into the same `core` package without touching Plex or the data plane. See spec §4.5. Do **not** introduce a shared `SourceAdapter` interface in v1 — wait until a second adapter exists and the common shape is concrete.

---

## Phase 1: Project Scaffolding

### Task 1.1: Initialize Go module and directory structure

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `Makefile`
- Create: `cmd/mister-groovy-relay/main.go` (placeholder)
- Create: `cmd/fake-mister/main.go` (placeholder)

- [ ] **Step 1: Initialize Go module**

```bash
cd /c/Users/Jake/Git/MiSTer_GroovyRelay
go mod init github.com/idio-sync/MiSTer_GroovyRelay
```

Expected: `go.mod` created with `module github.com/idio-sync/MiSTer_GroovyRelay` and a Go version line.

- [ ] **Step 2: Create `.gitignore`**

```
# Binaries
/mister-groovy-relay
/fake-mister
/cmd/*/mister-groovy-relay
/cmd/*/fake-mister
*.exe

# Test output
*.out
*.test
coverage.txt
/testdata/dumps/

# OS
.DS_Store
Thumbs.db

# Editors
.vscode/
.idea/
*.swp
```

- [ ] **Step 3: Create placeholder `cmd/mister-groovy-relay/main.go`**

```go
package main

import "fmt"

func main() {
	fmt.Println("mister-groovy-relay: not yet implemented")
}
```

- [ ] **Step 4: Create placeholder `cmd/fake-mister/main.go`**

```go
package main

import "fmt"

func main() {
	fmt.Println("fake-mister: not yet implemented")
}
```

- [ ] **Step 5: Create `Makefile`**

```makefile
.PHONY: build build-bridge build-fake test test-integration lint clean

build: build-bridge build-fake

build-bridge:
	go build -o mister-groovy-relay ./cmd/mister-groovy-relay

build-fake:
	go build -o fake-mister ./cmd/fake-mister

test:
	go test ./...

test-integration:
	go test -tags=integration ./tests/integration/...

lint:
	go vet ./...

clean:
	rm -f mister-groovy-relay fake-mister
```

- [ ] **Step 6: Verify it builds**

Run: `make build`
Expected: two binaries produced, no errors.

- [ ] **Step 7: Commit**

```bash
git init
git add .
git commit -m "chore: initialize Go module and directory structure"
```

---

### Task 1.2: Implement TOML config loading

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `config.example.toml`
- Modify: `go.mod` (add BurntSushi/toml)

- [ ] **Step 1: Add dependency**

```bash
go get github.com/BurntSushi/toml
```

- [ ] **Step 2: Write failing test** — create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DeviceName != "MiSTer" {
		t.Errorf("DeviceName default = %q, want %q", cfg.DeviceName, "MiSTer")
	}
	if cfg.MisterPort != 32100 {
		t.Errorf("MisterPort default = %d, want 32100", cfg.MisterPort)
	}
	if !cfg.LZ4Enabled {
		t.Error("LZ4Enabled default should be true")
	}
	if cfg.InterlaceFieldOrder != "tff" {
		t.Errorf("InterlaceFieldOrder default = %q, want tff", cfg.InterlaceFieldOrder)
	}
	if cfg.AspectMode != "auto" {
		t.Errorf("AspectMode default = %q, want auto", cfg.AspectMode)
	}
}

func TestLoadOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
device_name = "LivingRoomMiSTer"
mister_host = "192.168.1.42"
lz4_enabled = false
interlace_field_order = "bff"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DeviceName != "LivingRoomMiSTer" {
		t.Errorf("DeviceName = %q, want LivingRoomMiSTer", cfg.DeviceName)
	}
	if cfg.MisterHost != "192.168.1.42" {
		t.Errorf("MisterHost = %q", cfg.MisterHost)
	}
	if cfg.LZ4Enabled {
		t.Error("LZ4Enabled should be false")
	}
	if cfg.InterlaceFieldOrder != "bff" {
		t.Errorf("InterlaceFieldOrder = %q, want bff", cfg.InterlaceFieldOrder)
	}
}

func TestValidateBadFieldOrder(t *testing.T) {
	cfg := &Config{InterlaceFieldOrder: "diagonal", AspectMode: "auto"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for bad field order")
	}
}

func TestValidateBadAspectMode(t *testing.T) {
	cfg := &Config{InterlaceFieldOrder: "tff", AspectMode: "stretch"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for bad aspect mode")
	}
}
```

- [ ] **Step 3: Run test — expect FAIL**

Run: `go test ./internal/config/...`
Expected: FAIL (package doesn't compile).

- [ ] **Step 4: Implement `internal/config/config.go`**

```go
package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type Config struct {
	// Device identity
	DeviceName string `toml:"device_name"`
	DeviceUUID string `toml:"device_uuid"`

	// Network
	MisterHost string `toml:"mister_host"`
	MisterPort int    `toml:"mister_port"`
	SourcePort int    `toml:"source_port"`
	HTTPPort   int    `toml:"http_port"`

	// Video output
	Modeline            string `toml:"modeline"`
	InterlaceFieldOrder string `toml:"interlace_field_order"` // "tff" | "bff"
	AspectMode          string `toml:"aspect_mode"`           // "letterbox" | "zoom" | "auto"
	RGBMode             string `toml:"rgb_mode"`              // "rgb888" | "rgba8888" | "rgb565"
	LZ4Enabled          bool   `toml:"lz4_enabled"`

	// Audio
	AudioSampleRate int `toml:"audio_sample_rate"`
	AudioChannels   int `toml:"audio_channels"`

	// Plex
	PlexProfileName string `toml:"plex_profile_name"`
	PlexServerURL   string `toml:"plex_server_url"`

	// Paths
	DataDir string `toml:"data_dir"`
}

func defaults() *Config {
	return &Config{
		DeviceName:          "MiSTer",
		MisterPort:          32100,
		SourcePort:          32101,
		HTTPPort:            32500,
		Modeline:            "NTSC_480i",
		InterlaceFieldOrder: "tff",
		AspectMode:          "auto",
		RGBMode:             "rgb888",
		LZ4Enabled:          true,
		AudioSampleRate:     48000,
		AudioChannels:       2,
		PlexProfileName:     "Plex Home Theater",
		DataDir:             "/config",
	}
}

func Load(path string) (*Config, error) {
	cfg := defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if len(data) > 0 {
		if err := toml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	switch c.InterlaceFieldOrder {
	case "tff", "bff":
	default:
		return fmt.Errorf("interlace_field_order must be tff or bff, got %q", c.InterlaceFieldOrder)
	}
	switch c.AspectMode {
	case "letterbox", "zoom", "auto":
	default:
		return fmt.Errorf("aspect_mode must be letterbox, zoom, or auto, got %q", c.AspectMode)
	}
	switch c.RGBMode {
	case "rgb888", "rgba8888", "rgb565":
	default:
		return fmt.Errorf("rgb_mode must be rgb888, rgba8888, or rgb565, got %q", c.RGBMode)
	}
	return nil
}
```

- [ ] **Step 5: Run test — expect PASS**

Run: `go test ./internal/config/...`
Expected: PASS.

- [ ] **Step 6: Create `config.example.toml`** using Go defaults as guidance. Include comments describing every field.

- [ ] **Step 7: Commit**

```bash
git add internal/config/ config.example.toml go.mod go.sum
git commit -m "feat(config): TOML config loading with defaults and validation"
```

---

### Task 1.3: Logging helper

**Files:**
- Create: `internal/logging/logging.go`

- [ ] **Step 1: Implement slog wrapper**

```go
package logging

import (
	"log/slog"
	"os"
)

func New(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/logging/
git commit -m "feat(logging): slog JSON logger factory"
```

---

## Phase 2: Groovy Protocol Library

> **READ FIRST:** `docs/references/groovy_mister.md` and `docs/references/mistercast.md`. The exact byte offsets and header variants for each command live there. If anything in those docs contradicts this plan, trust the docs — they came from reading the actual source.

### Task 2.1: Constants and command IDs

**Files:**
- Create: `internal/groovy/constants.go`

- [ ] **Step 1: Write `constants.go`** based on `docs/references/groovy_mister.md`.

```go
package groovy

// Protocol command IDs (first byte of every UDP datagram).
// VERIFIED against docs/references/groovy_mister.md §"Command-by-Command Wire
// Format" and docs/references/mistercast.md §"The Five Commands (Byte-Level)".
// These are the exact IDs used by the psakhis/Groovy_MiSTer receiver:
const (
	CmdClose          byte = 1
	CmdInit           byte = 2
	CmdSwitchres      byte = 3
	CmdAudio          byte = 4
	CmdGetStatus      byte = 5
	CmdBlitVSync      byte = 6 // deprecated, progressive-only; unused by relay
	CmdBlitFieldVSync byte = 7
	CmdGetVersion     byte = 8
)

// RGB mode values for INIT byte[4].
const (
	RGBMode888  byte = 0
	RGBMode8888 byte = 1
	RGBMode565  byte = 2
)

// BLIT header duplicate / delta marker values.
const (
	BlitFlagDup   byte = 0x01 // at header byte[8] with header length 9
	BlitFlagDelta byte = 0x01 // at header byte[12] with header length 13
)

// BLIT header length variants (see reference doc).
const (
	BlitHeaderRaw      = 8  // cmd+frame+field+vSync — raw full field
	BlitHeaderRawDup   = 9  // raw with dup marker at [8]
	BlitHeaderLZ4      = 12 // LZ4 full field with cSize at [8..11]
	BlitHeaderLZ4Delta = 13 // LZ4 delta with cSize at [8..11] and delta marker at [12]
)

// INIT byte[1] (lz4Frames) mode values.
const (
	LZ4ModeOff     byte = 0 // raw / no compression
	LZ4ModeDefault byte = 1 // standard LZ4 block
	// Modes 2..6 exist in the reference (HC, delta-adaptive) but we ship mode 1.
)

// INIT byte[2] (soundRate) enum.
const (
	AudioRateOff   byte = 0
	AudioRate22050 byte = 1
	AudioRate44100 byte = 2
	AudioRate48000 byte = 3
)

// Integer sample-rate constants (for Go-side math, not wire encoding).
const (
	AudioSampleRate22050 = 22050
	AudioSampleRate44100 = 44100
	AudioSampleRate48000 = 48000
)

// UDP transport constants.
const (
	MisterUDPPort  = 32100
	MaxDatagram    = 1472 // MTU 1500 - IP20 - UDP8
	CongestionSize = 500000 // K_CONGESTION_SIZE in reference (decimal, not 500*1024)
	CongestionWait = 11     // milliseconds (K_CONGESTION_TIME ≈ 110000 ticks)
)

// ACK / status packet size emitted by the MiSTer.
const ACKPacketSize = 13

// AUDIO header size: [0]=cmd, [1..2]=soundSize uint16. PCM bytes that follow
// are streamed as MTU-sized datagrams on the same socket — NOT inlined.
const AudioHeaderSize = 3
```

- [ ] **Step 2: Commit**

```bash
git add internal/groovy/constants.go
git commit -m "feat(groovy): protocol constants and command IDs"
```

---

### Task 2.2: INIT packet builder

**Files:**
- Create: `internal/groovy/builder.go`
- Create: `internal/groovy/builder_test.go`

**Wire layout (verified against groovy_mister.md:41-49 and mistercast.md:28-34):**

```
[0]  cmd = 0x02
[1]  lz4Frames  (0=RAW, 1=LZ4, 2..6=adaptive modes)
[2]  soundRate  (0=off, 1=22050, 2=44100, 3=48000)
[3]  soundChan  (0=off, 1=mono, 2=stereo)
[4]  rgbMode    (0=RGB888, 1=RGBA8888, 2=RGB565) — optional; 4-byte INIT implies 0
```

Total length: 5 bytes (4 bytes if RGB888 default is acceptable; v1 always sends 5).
**There is no width, height, interlace flag, or protocol version byte in INIT.** Those live in SWITCHRES. Protocol version is retrieved separately via CMD_GET_VERSION.

- [ ] **Step 1: Write failing test with byte-for-byte fixtures**

Create `internal/groovy/builder_test.go`:

```go
package groovy

import (
	"bytes"
	"testing"
)

func TestBuildInit_StereoLZ448kRGB888(t *testing.T) {
	got := BuildInit(LZ4ModeDefault, AudioRate48000, 2, RGBMode888)
	want := []byte{CmdInit, LZ4ModeDefault, AudioRate48000, 2, RGBMode888}
	if !bytes.Equal(got, want) {
		t.Errorf("INIT bytes = %v, want %v", got, want)
	}
}

func TestBuildInit_RawNoAudioRGB565(t *testing.T) {
	got := BuildInit(LZ4ModeOff, AudioRateOff, 0, RGBMode565)
	want := []byte{CmdInit, LZ4ModeOff, AudioRateOff, 0, RGBMode565}
	if !bytes.Equal(got, want) {
		t.Errorf("INIT bytes = %v, want %v", got, want)
	}
}

func TestBuildInit_PanicsOnInvalidRGBMode(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on unknown rgb mode")
		}
	}()
	BuildInit(LZ4ModeDefault, AudioRate48000, 2, 99)
}

func TestBuildInit_PanicsOnInvalidSoundRate(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on unknown sound rate")
		}
	}()
	BuildInit(LZ4ModeDefault, 99, 2, RGBMode888)
}
```

- [ ] **Step 2: Run test — expect FAIL (no builder)**

Run: `go test ./internal/groovy/... -run TestBuildInit`
Expected: FAIL — `BuildInit` undefined.

- [ ] **Step 3: Implement `BuildInit`**

Create `internal/groovy/builder.go`:

```go
package groovy

import "fmt"

// BuildInit returns the 5-byte INIT command packet.
// Wire layout (groovy_mister.md:41-49, mistercast.md:28-34):
//   [0] cmd       = 0x02
//   [1] lz4Frames (LZ4ModeOff | LZ4ModeDefault)
//   [2] soundRate (AudioRateOff | AudioRate22050 | AudioRate44100 | AudioRate48000)
//   [3] soundChan (0=off, 1=mono, 2=stereo)
//   [4] rgbMode   (RGBMode888 | RGBMode8888 | RGBMode565)
//
// INIT is the ONE ACK-gated handshake: caller must wait for a 13-byte status
// reply within ~60ms after sending INIT. See groovynet.Sender.SendInitAwaitACK.
func BuildInit(lz4Frames, soundRate, soundChan, rgbMode byte) []byte {
	switch lz4Frames {
	case LZ4ModeOff, LZ4ModeDefault:
	default:
		panic(fmt.Sprintf("groovy: invalid lz4Frames %d", lz4Frames))
	}
	switch soundRate {
	case AudioRateOff, AudioRate22050, AudioRate44100, AudioRate48000:
	default:
		panic(fmt.Sprintf("groovy: invalid soundRate %d", soundRate))
	}
	if soundChan > 2 {
		panic(fmt.Sprintf("groovy: invalid soundChan %d", soundChan))
	}
	switch rgbMode {
	case RGBMode888, RGBMode8888, RGBMode565:
	default:
		panic(fmt.Sprintf("groovy: invalid rgbMode %d", rgbMode))
	}
	return []byte{CmdInit, lz4Frames, soundRate, soundChan, rgbMode}
}
```

- [ ] **Step 4: Run test — expect PASS**

Run: `go test ./internal/groovy/... -run TestBuildInit -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/groovy/
git commit -m "feat(groovy): INIT packet builder with rgbMode + interlace support"
```

---

### Task 2.3: SWITCHRES packet builder

**Reference:** groovy_mister.md:51-65, mistercast.md:36-51. Fire-and-forget (no ACK).

**Wire layout (26 bytes, cumulative VESA offsets — NOT independent porch values):**

```
[0]     cmd = 0x03
[1..8]  pClock     double (IEEE-754 LE) — pixel clock in MHz
[9..10] hActive    uint16
[11..12] hBegin    uint16 = hActive + hFrontPorch
[13..14] hEnd      uint16 = hBegin  + hSync
[15..16] hTotal    uint16 = hEnd    + hBackPorch
[17..18] vActive   uint16
[19..20] vBegin    uint16 = vActive + vFrontPorch
[21..22] vEnd      uint16 = vBegin  + vSync
[23..24] vTotal    uint16 = vEnd    + vBackPorch
[25]    interlace  uint8  (0=progressive, 1=interlaced, 2=interlaced-force-field-fb)
```

**Files:**
- Modify: `internal/groovy/builder.go`
- Modify: `internal/groovy/builder_test.go`

- [ ] **Step 1: Add byte-for-byte test for SWITCHRES**

Append to `builder_test.go`:

```go
import (
	"encoding/binary"
	"math"
)

func TestBuildSwitchres_NTSC480i60_Canonical(t *testing.T) {
	// Canonical NTSC 480i60 per mistercast.md:138:
	// pClock=13.5, hTotal=858, vTotal=525, interlace=1.
	got := BuildSwitchres(NTSC480i60)

	if got[0] != CmdSwitchres {
		t.Fatalf("cmd = %d, want %d", got[0], CmdSwitchres)
	}
	if len(got) != 26 {
		t.Fatalf("SWITCHRES must be 26 bytes, got %d", len(got))
	}
	gotPClock := math.Float64frombits(binary.LittleEndian.Uint64(got[1:9]))
	if gotPClock != 13.5 {
		t.Errorf("pClock = %f, want 13.5", gotPClock)
	}
	if v := binary.LittleEndian.Uint16(got[9:11]); v != 720 {
		t.Errorf("hActive = %d, want 720", v)
	}
	if v := binary.LittleEndian.Uint16(got[15:17]); v != 858 {
		t.Errorf("hTotal = %d, want 858", v)
	}
	if v := binary.LittleEndian.Uint16(got[17:19]); v != 240 {
		t.Errorf("vActive (per field) = %d, want 240", v)
	}
	if v := binary.LittleEndian.Uint16(got[23:25]); v != 525 {
		t.Errorf("vTotal = %d, want 525", v)
	}
	if got[25] != 1 {
		t.Errorf("interlace = %d, want 1", got[25])
	}
}

func TestBuildSwitchres_Progressive(t *testing.T) {
	ml := Modeline{PClock: 27.0, HActive: 720, HBegin: 736, HEnd: 798,
		HTotal: 858, VActive: 480, VBegin: 483, VEnd: 486, VTotal: 525, Interlace: 0}
	got := BuildSwitchres(ml)
	if got[25] != 0 {
		t.Error("interlace flag should be 0 for progressive modeline")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

- [ ] **Step 3: Implement SWITCHRES**

Add to `builder.go`:

```go
import (
	"encoding/binary"
	"math"
)

// Modeline holds the SWITCHRES wire values directly. Fields are the
// cumulative VESA offsets (hBegin = hActive+hFrontPorch, hEnd = hBegin+hSync,
// hTotal = hEnd+hBackPorch). Use ModelineFromPorches() to convert from the
// conventional (hFrontPorch, hSync, hBackPorch) triple.
type Modeline struct {
	PClock    float64 // MHz
	HActive   uint16
	HBegin    uint16
	HEnd      uint16
	HTotal    uint16
	VActive   uint16 // per-field when Interlace > 0
	VBegin    uint16
	VEnd      uint16
	VTotal    uint16
	Interlace uint8 // 0=progressive, 1=interlaced, 2=interlaced-force-field-fb
}

// ModelineFromPorches builds a Modeline from the conventional porch triple,
// computing the cumulative offsets the wire format expects.
func ModelineFromPorches(pClock float64,
	hActive, hFrontPorch, hSync, hBackPorch,
	vActive, vFrontPorch, vSync, vBackPorch uint16,
	interlace uint8) Modeline {
	hBegin := hActive + hFrontPorch
	hEnd := hBegin + hSync
	hTotal := hEnd + hBackPorch
	vBegin := vActive + vFrontPorch
	vEnd := vBegin + vSync
	vTotal := vEnd + vBackPorch
	return Modeline{
		PClock: pClock,
		HActive: hActive, HBegin: hBegin, HEnd: hEnd, HTotal: hTotal,
		VActive: vActive, VBegin: vBegin, VEnd: vEnd, VTotal: vTotal,
		Interlace: interlace,
	}
}

// BuildSwitchres returns the 26-byte SWITCHRES packet.
// Wire layout: groovy_mister.md:51-65, mistercast.md:36-51.
func BuildSwitchres(ml Modeline) []byte {
	buf := make([]byte, 26)
	buf[0] = CmdSwitchres
	binary.LittleEndian.PutUint64(buf[1:9], math.Float64bits(ml.PClock))
	binary.LittleEndian.PutUint16(buf[9:11], ml.HActive)
	binary.LittleEndian.PutUint16(buf[11:13], ml.HBegin)
	binary.LittleEndian.PutUint16(buf[13:15], ml.HEnd)
	binary.LittleEndian.PutUint16(buf[15:17], ml.HTotal)
	binary.LittleEndian.PutUint16(buf[17:19], ml.VActive)
	binary.LittleEndian.PutUint16(buf[19:21], ml.VBegin)
	binary.LittleEndian.PutUint16(buf[21:23], ml.VEnd)
	binary.LittleEndian.PutUint16(buf[23:25], ml.VTotal)
	buf[25] = ml.Interlace
	return buf
}
```

- [ ] **Step 4: Run — expect PASS**

- [ ] **Step 5: Add NTSC 480i60 modeline preset**

Append to `builder.go`:

```go
// NTSC480i60 matches the canonical psakhis/Groovy_MiSTer NTSC 480i entry
// (mistercast.md:138: pClock=13.5, hTotal=858, vTotal=525, interlace=1).
// VActive is per-field (240 lines per field; interlace=1 means the receiver
// alternates field 0 / field 1 across consecutive BLIT_FIELD_VSYNC packets).
// If your CRT prefers different timing, override in config.
var NTSC480i60 = ModelineFromPorches(
	13.5,                  // pClock MHz
	720, 16, 62, 60,       // hActive, hFrontPorch, hSync, hBackPorch  → hTotal=858
	240, 3, 3, 19,         // vActive per-field, vFrontPorch, vSync, vBackPorch
	1,                     // interlaced
)
// Note: per-field vTotal computed above is 265; vTotal for interlaced NTSC is
// 525 across both fields. Receiver expects per-field values (see
// groovy_mister.md:112 "vActive is already halved per field"); the FPGA
// reconstructs the 525-line frame from two 262/263-line fields. Verify the
// exact vertical porches against a working GroovyMAME pcap before shipping.
```

- [ ] **Step 6: Commit**

```bash
git add internal/groovy/
git commit -m "feat(groovy): SWITCHRES builder and NTSC 480i60 modeline preset"
```

---

### Task 2.4: AUDIO header builder (NOT inline-PCM)

**Reference:** groovy_mister.md:67-72, mistercast.md:53-57.

**Wire layout:**

```
[0]    cmd = 0x04
[1..2] soundSize uint16 LE  — number of PCM bytes that follow
```

**The 3-byte header is a standalone UDP datagram. The PCM bytes immediately follow as separate MTU-sized UDP datagrams on the same socket — NOT inlined into a single big datagram** (that would violate IP_DONTFRAGMENT and MTU). Sample rate and channel count come from INIT, not per-packet. PCM is 16-bit signed LE, LRLR stereo interleaving.

Audio may only be sent while ACK bit 6 (`fpga.audio`) == 1. See Plane audio-gating logic.

**Files:**
- Modify: `internal/groovy/builder.go`
- Modify: `internal/groovy/builder_test.go`

- [ ] **Step 1: Test the header in isolation**

```go
func TestBuildAudioHeader(t *testing.T) {
	got := BuildAudioHeader(3200)
	if len(got) != AudioHeaderSize {
		t.Fatalf("len = %d, want %d", len(got), AudioHeaderSize)
	}
	if got[0] != CmdAudio {
		t.Errorf("cmd = %d, want %d", got[0], CmdAudio)
	}
	if v := binary.LittleEndian.Uint16(got[1:3]); v != 3200 {
		t.Errorf("soundSize = %d, want 3200", v)
	}
}

func TestBuildAudioHeader_ZeroOK(t *testing.T) {
	// Zero-length audio is valid (no-op between blits when audio enabled but
	// no samples produced this tick).
	got := BuildAudioHeader(0)
	if got[0] != CmdAudio || got[1] != 0 || got[2] != 0 {
		t.Errorf("zero-size audio header = %v", got)
	}
}
```

- [ ] **Step 2-4: Implement**

```go
// BuildAudioHeader returns the 3-byte AUDIO command header. The caller MUST
// send the `soundSize` PCM bytes immediately after, using the MTU-slicing
// sender (e.g. Sender.SendPayload). NEVER inline PCM into the header datagram
// — PCM fields can reach ~3.2 KB/tick and IP_DONTFRAGMENT will drop
// oversized datagrams.
//
// Sample rate and channel count are session-level state established in INIT.
// Audio is only valid to send while the last ACK's bit 6 (fpga.audio) == 1.
func BuildAudioHeader(soundSize uint16) []byte {
	buf := make([]byte, AudioHeaderSize)
	buf[0] = CmdAudio
	binary.LittleEndian.PutUint16(buf[1:3], soundSize)
	return buf
}
```

(Remove any prior `BuildAudio(sampleRate, channels, pcm)` helper — the API shape has changed; callers must explicitly stream PCM as a payload.)

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(groovy): AUDIO header builder (3 bytes) — PCM streams as separate payload"
```

---

### Task 2.5: BLIT_FIELD_VSYNC header builders

**Reference:** groovy_mister.md:74-89, mistercast.md:59-75.

**Wire layout (fixed 8 bytes common, then variant tail):**

```
[0]     cmd = 0x07
[1..4]  frame  uint32 LE  — client-assigned monotonic (per interlaced frame pair)
[5]     field  uint8      — 0 = top/progressive, 1 = bottom
[6..7]  vSync  uint16 LE  — raster line for FPGA to sync (0 = FPGA chooses)
```

Then variant tail:
- **8 bytes total** — raw uncompressed full field. Payload = `hActive*vActive*bpp` bytes.
- **9 bytes total** — duplicate-of-previous field. `[8]=0x01`. No payload follows.
- **12 bytes total** — LZ4 full field. `[8..11]=cSize uint32`. Payload = cSize bytes.
- **13 bytes total** — LZ4 delta (XOR vs previous). `[8..11]=cSize`, `[12]=0x01`. Payload = cSize bytes.

Payload is streamed as back-to-back MTU-sized datagrams with no per-chunk header.

**Files:**
- Modify: `internal/groovy/builder.go`
- Modify: `internal/groovy/builder_test.go`

- [ ] **Step 1: Byte-for-byte tests for each variant**

```go
func TestBuildBlitHeader_RawFull(t *testing.T) {
	h := BuildBlitHeader(BlitOpts{Frame: 42, Field: 1, VSync: 0})
	if h[0] != CmdBlitFieldVSync {
		t.Fatalf("cmd = %d, want %d", h[0], CmdBlitFieldVSync)
	}
	if len(h) != BlitHeaderRaw {
		t.Errorf("raw header len = %d, want %d", len(h), BlitHeaderRaw)
	}
	if v := binary.LittleEndian.Uint32(h[1:5]); v != 42 {
		t.Errorf("frame = %d, want 42", v)
	}
	if h[5] != 1 {
		t.Errorf("field = %d, want 1", h[5])
	}
	if v := binary.LittleEndian.Uint16(h[6:8]); v != 0 {
		t.Errorf("vSync = %d, want 0", v)
	}
}

func TestBuildBlitHeader_Duplicate(t *testing.T) {
	h := BuildBlitHeader(BlitOpts{Frame: 43, Field: 0, Duplicate: true})
	if len(h) != BlitHeaderRawDup {
		t.Fatalf("dup header len = %d, want %d", len(h), BlitHeaderRawDup)
	}
	if h[8] != BlitFlagDup {
		t.Errorf("dup marker = 0x%x, want 0x%x", h[8], BlitFlagDup)
	}
}

func TestBuildBlitHeader_LZ4Full(t *testing.T) {
	h := BuildBlitHeader(BlitOpts{Frame: 44, Field: 0, CompressedSize: 120000, Compressed: true})
	if len(h) != BlitHeaderLZ4 {
		t.Fatalf("lz4 header len = %d, want %d", len(h), BlitHeaderLZ4)
	}
	if v := binary.LittleEndian.Uint32(h[8:12]); v != 120000 {
		t.Errorf("cSize = %d, want 120000", v)
	}
}

func TestBuildBlitHeader_LZ4Delta(t *testing.T) {
	h := BuildBlitHeader(BlitOpts{Frame: 45, Field: 1, CompressedSize: 90000, Compressed: true, Delta: true})
	if len(h) != BlitHeaderLZ4Delta {
		t.Fatalf("lz4 delta header len = %d, want %d", len(h), BlitHeaderLZ4Delta)
	}
	if h[12] != BlitFlagDelta {
		t.Errorf("delta marker at [12] = 0x%x, want 0x%x", h[12], BlitFlagDelta)
	}
}
```

- [ ] **Step 2-4: Implement and verify**

```go
type BlitOpts struct {
	Frame          uint32 // monotonic frame counter
	Field          uint8  // 0 = top/progressive, 1 = bottom
	VSync          uint16 // target raster line; 0 = FPGA chooses
	CompressedSize uint32 // only used when Compressed == true
	Compressed     bool
	Delta          bool
	Duplicate      bool
}

// BuildBlitHeader returns the BLIT_FIELD_VSYNC header bytes. Payload bytes
// (when present) follow the header and MUST be sliced into MaxDatagram-sized
// UDP datagrams with no per-chunk framing. See groovy_mister.md:74-89,
// mistercast.md:59-75 for authoritative byte layout.
//
// Header length encodes the variant:
//    8 bytes — raw uncompressed, full field
//    9 bytes — duplicate-of-previous (no payload follows)
//   12 bytes — LZ4, full field
//   13 bytes — LZ4 delta (XOR vs previous)
func BuildBlitHeader(o BlitOpts) []byte {
	var length int
	switch {
	case o.Duplicate:
		length = BlitHeaderRawDup
	case o.Compressed && o.Delta:
		length = BlitHeaderLZ4Delta
	case o.Compressed:
		length = BlitHeaderLZ4
	default:
		length = BlitHeaderRaw
	}
	h := make([]byte, length)
	h[0] = CmdBlitFieldVSync
	binary.LittleEndian.PutUint32(h[1:5], o.Frame)
	h[5] = o.Field
	binary.LittleEndian.PutUint16(h[6:8], o.VSync)
	switch {
	case o.Duplicate:
		h[8] = BlitFlagDup
	case o.Compressed:
		binary.LittleEndian.PutUint32(h[8:12], o.CompressedSize)
		if o.Delta {
			h[12] = BlitFlagDelta
		}
	}
	return h
}
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(groovy): BLIT_FIELD_VSYNC header builder with 4 variants"
```

---

### Task 2.6: CLOSE packet builder

**Files:**
- Modify: `internal/groovy/builder.go`
- Modify: `internal/groovy/builder_test.go`

- [ ] **Step 1-4:** Simple builder. Single-byte command, maybe a session-end code. Add test asserting first byte is `CmdClose`. Implement. Verify.

```go
func BuildClose() []byte {
	return []byte{CmdClose}
}
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(groovy): CLOSE packet builder"
```

---

### Task 2.7: LZ4 block compression helper

**Reference:** raw LZ4 block format, no framing magic, use `pierrec/lz4/v4` block API (not `NewWriter` which is frame format).

**Files:**
- Create: `internal/groovy/lz4.go`
- Create: `internal/groovy/lz4_test.go`

- [ ] **Step 1: Add dependency**

```bash
go get github.com/pierrec/lz4/v4
```

- [ ] **Step 2: Write test**

```go
package groovy

import (
	"bytes"
	"testing"
)

func TestLZ4RoundTrip(t *testing.T) {
	// Use a field-sized buffer of mostly-zero data with some variance.
	src := make([]byte, 518400)
	for i := range src {
		src[i] = byte(i % 256)
	}
	compressed, err := LZ4Compress(src)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if len(compressed) == 0 {
		t.Fatal("compressed buf is empty")
	}
	decompressed, err := LZ4Decompress(compressed, len(src))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(src, decompressed) {
		t.Error("round-trip mismatch")
	}
}

func TestLZ4Compress_ReducesZeros(t *testing.T) {
	src := make([]byte, 100000) // all zeros
	compressed, err := LZ4Compress(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(compressed) > len(src)/10 {
		t.Errorf("zeros should compress hard; got %d/%d", len(compressed), len(src))
	}
}
```

- [ ] **Step 3: Implement**

```go
package groovy

import (
	"fmt"

	"github.com/pierrec/lz4/v4"
)

// LZ4Compress compresses src using LZ4 block format (NOT frame format).
// Returns the compressed bytes; caller stores the decompressed length out-of-band
// (in the BLIT header compressedSize / rawSize fields).
func LZ4Compress(src []byte) ([]byte, error) {
	dst := make([]byte, lz4.CompressBlockBound(len(src)))
	var c lz4.Compressor
	n, err := c.CompressBlock(src, dst)
	if err != nil {
		return nil, fmt.Errorf("lz4 compress: %w", err)
	}
	return dst[:n], nil
}

// LZ4Decompress reverses LZ4Compress. rawLen MUST equal the original src length.
func LZ4Decompress(compressed []byte, rawLen int) ([]byte, error) {
	dst := make([]byte, rawLen)
	n, err := lz4.UncompressBlock(compressed, dst)
	if err != nil {
		return nil, fmt.Errorf("lz4 decompress: %w", err)
	}
	if n != rawLen {
		return nil, fmt.Errorf("lz4 decompress: got %d bytes, want %d", n, rawLen)
	}
	return dst, nil
}
```

- [ ] **Step 4: Verify**

Run: `go test ./internal/groovy/... -run TestLZ4`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/groovy/lz4.go internal/groovy/lz4_test.go go.mod go.sum
git commit -m "feat(groovy): LZ4 block compress/decompress (not frame format)"
```

---

### Task 2.8: ACK packet parser

**Reference:** 13-byte ACK from MiSTer after every blit. Fields include frameEcho, vCountEcho, fpga.frame, fpga.vCount, 8 status bits.

**Files:**
- Create: `internal/groovy/ack.go`
- Create: `internal/groovy/ack_test.go`

- [ ] **Step 1: Test**

```go
package groovy

import (
	"encoding/binary"
	"testing"
)

func TestParseACK(t *testing.T) {
	pkt := make([]byte, ACKPacketSize)
	binary.LittleEndian.PutUint32(pkt[0:4], 42)   // frameEcho
	binary.LittleEndian.PutUint16(pkt[4:6], 100)  // vCountEcho
	binary.LittleEndian.PutUint32(pkt[6:10], 50)  // fpga.frame
	binary.LittleEndian.PutUint16(pkt[10:12], 120)// fpga.vCount
	pkt[12] = 0x40                                 // audio-ready bit

	ack, err := ParseACK(pkt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ack.FrameEcho != 42 {
		t.Errorf("FrameEcho = %d", ack.FrameEcho)
	}
	if ack.FPGAFrame != 50 {
		t.Errorf("FPGAFrame = %d", ack.FPGAFrame)
	}
	if !ack.AudioReady() {
		t.Error("AudioReady should be true")
	}
}

func TestParseACK_WrongSize(t *testing.T) {
	_, err := ParseACK(make([]byte, 10))
	if err == nil {
		t.Error("expected error for short packet")
	}
}
```

- [ ] **Step 2-4: Implement**

```go
package groovy

import (
	"encoding/binary"
	"fmt"
)

// ACK (13 bytes) — emitted by the MiSTer in response to INIT, CMD_GET_STATUS,
// and every BLIT_FIELD_VSYNC. Wire layout: groovy_mister.md:94-103,
// mistercast.md:78-87.
//
//   [0..3]   frameEcho   uint32  — echoes sender's last frame
//   [4..5]   vCountEcho  uint16  — echoes sender's requested vSync line
//   [6..9]   fpgaFrame   uint32  — FPGA's current frame
//   [10..11] fpgaVCount  uint16  — FPGA's current raster line
//   [12]     status      uint8   bitfield:
//              bit0 vramReady    bit1 vramEndFrame  bit2 vramSynced
//              bit3 vgaFrameskip bit4 vgaVblank     bit5 vgaF1 (interlace field)
//              bit6 audio        bit7 vramQueue
type ACK struct {
	FrameEcho  uint32
	VCountEcho uint16
	FPGAFrame  uint32
	FPGAVCount uint16
	Status     byte
}

// AudioReady reports whether the MiSTer's audio buffer wants more samples
// (bit 6 of Status). Sender MUST gate CMD_AUDIO on this bit.
func (a ACK) AudioReady() bool { return a.Status&(1<<6) != 0 }

// VGAField reports the FPGA's current interlace field (bit 5 of Status).
// Useful for field-order drift detection.
func (a ACK) VGAField() bool { return a.Status&(1<<5) != 0 }

// VRAMReady reports whether the FPGA can accept another BLIT right now
// (bit 0). Informational — not a gate; sender free-runs off its own clock.
func (a ACK) VRAMReady() bool { return a.Status&(1<<0) != 0 }

func ParseACK(pkt []byte) (ACK, error) {
	if len(pkt) != ACKPacketSize {
		return ACK{}, fmt.Errorf("ack: expected %d bytes, got %d", ACKPacketSize, len(pkt))
	}
	return ACK{
		FrameEcho:  binary.LittleEndian.Uint32(pkt[0:4]),
		VCountEcho: binary.LittleEndian.Uint16(pkt[4:6]),
		FPGAFrame:  binary.LittleEndian.Uint32(pkt[6:10]),
		FPGAVCount: binary.LittleEndian.Uint16(pkt[10:12]),
		Status:     pkt[12],
	}, nil
}
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(groovy): ACK parser with status bit accessors"
```

---

### ✅ Phase 2 Review Checkpoint

**Report to user:**
- Groovy protocol library complete: all 5 commands, LZ4, ACK parsing.
- All unit tests pass (`go test ./internal/groovy/...`).
- No network code yet; no hardware required to verify.
- **Known uncertainty:** exact byte offsets within INIT, SWITCHRES, BLIT headers. If byte offsets sketched above don't match the reference docs exactly, adjust tests to byte-for-byte fixtures and re-run.

---

## Phase 3: fake-MiSTer Sink

Goal: a second binary that listens on UDP and behaves enough like a real MiSTer to drive integration tests. Every task here is internal library code called by `cmd/fake-mister/main.go`.

### Task 3.1: UDP listener + command parser

**Files:**
- Create: `internal/fakemister/listener.go`
- Create: `internal/fakemister/listener_test.go`

- [ ] **Step 1: Test parsing INIT**

```go
package fakemister

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func TestParseCommand_Init(t *testing.T) {
	pkt := groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888)
	cmd, err := ParseCommand(pkt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cmd.Type != groovy.CmdInit {
		t.Errorf("Type = %d, want %d", cmd.Type, groovy.CmdInit)
	}
	if cmd.Init == nil {
		t.Fatal("Init payload nil")
	}
	if cmd.Init.RGBMode != groovy.RGBMode888 {
		t.Errorf("RGBMode = %d", cmd.Init.RGBMode)
	}
	if cmd.Init.LZ4Frames != groovy.LZ4ModeDefault {
		t.Errorf("LZ4Frames = %d", cmd.Init.LZ4Frames)
	}
	if cmd.Init.SoundRate != groovy.AudioRate48000 {
		t.Errorf("SoundRate = %d", cmd.Init.SoundRate)
	}
	if cmd.Init.SoundChan != 2 {
		t.Errorf("SoundChan = %d", cmd.Init.SoundChan)
	}
}

func TestParseCommand_UnknownType(t *testing.T) {
	_, err := ParseCommand([]byte{99, 0, 0})
	if err == nil {
		t.Error("expected error")
	}
}
```

- [ ] **Step 2-4: Implement parser**

```go
package fakemister

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// InitPayload carries the five INIT bytes the receiver uses to set up the
// session. NO width/height/interlace here — those come from SWITCHRES.
type InitPayload struct {
	LZ4Frames byte
	SoundRate byte
	SoundChan byte
	RGBMode   byte
}

// SwitchresPayload carries the modeline the receiver uses to program video.
// Per-field VActive for interlaced modes.
type SwitchresPayload struct {
	PClock    float64
	HActive   uint16
	HTotal    uint16
	VActive   uint16
	VTotal    uint16
	Interlace uint8
}

// AudioHeader is just the 3-byte header; PCM arrives as separate datagrams
// and is collected by the payload-mode listener.
type AudioHeader struct {
	SoundSize uint16
}

type BlitHeader struct {
	Frame          uint32
	Field          uint8
	VSync          uint16
	Compressed     bool
	Delta          bool
	Duplicate      bool
	CompressedSize uint32
}

type Command struct {
	Type      byte
	Init      *InitPayload
	Switchres *SwitchresPayload
	Audio     *AudioHeader
	Blit      *BlitHeader
	Raw       []byte
}

func ParseCommand(pkt []byte) (Command, error) {
	if len(pkt) == 0 {
		return Command{}, fmt.Errorf("empty packet")
	}
	c := Command{Type: pkt[0], Raw: pkt}
	switch pkt[0] {
	case groovy.CmdInit:
		// INIT is 4 or 5 bytes (5th = rgbMode, optional — default RGB888).
		if len(pkt) < 4 {
			return c, fmt.Errorf("INIT packet too short: %d", len(pkt))
		}
		ip := &InitPayload{
			LZ4Frames: pkt[1],
			SoundRate: pkt[2],
			SoundChan: pkt[3],
			RGBMode:   groovy.RGBMode888,
		}
		if len(pkt) >= 5 {
			ip.RGBMode = pkt[4]
		}
		c.Init = ip
	case groovy.CmdSwitchres:
		if len(pkt) < 26 {
			return c, fmt.Errorf("SWITCHRES packet too short: %d", len(pkt))
		}
		c.Switchres = &SwitchresPayload{
			PClock:    math.Float64frombits(binary.LittleEndian.Uint64(pkt[1:9])),
			HActive:   binary.LittleEndian.Uint16(pkt[9:11]),
			HTotal:    binary.LittleEndian.Uint16(pkt[15:17]),
			VActive:   binary.LittleEndian.Uint16(pkt[17:19]),
			VTotal:    binary.LittleEndian.Uint16(pkt[23:25]),
			Interlace: pkt[25],
		}
	case groovy.CmdAudio:
		if len(pkt) != groovy.AudioHeaderSize {
			return c, fmt.Errorf("AUDIO header must be exactly %d bytes, got %d",
				groovy.AudioHeaderSize, len(pkt))
		}
		c.Audio = &AudioHeader{
			SoundSize: binary.LittleEndian.Uint16(pkt[1:3]),
		}
	case groovy.CmdBlitFieldVSync:
		if len(pkt) < groovy.BlitHeaderRaw {
			return c, fmt.Errorf("BLIT header too short")
		}
		bh := &BlitHeader{
			Frame: binary.LittleEndian.Uint32(pkt[1:5]),
			Field: pkt[5],
			VSync: binary.LittleEndian.Uint16(pkt[6:8]),
		}
		switch len(pkt) {
		case groovy.BlitHeaderRaw:
			// raw, full field — no tail
		case groovy.BlitHeaderRawDup:
			bh.Duplicate = pkt[8] == groovy.BlitFlagDup
		case groovy.BlitHeaderLZ4:
			bh.Compressed = true
			bh.CompressedSize = binary.LittleEndian.Uint32(pkt[8:12])
		case groovy.BlitHeaderLZ4Delta:
			bh.Compressed = true
			bh.Delta = pkt[12] == groovy.BlitFlagDelta
			bh.CompressedSize = binary.LittleEndian.Uint32(pkt[8:12])
		default:
			return c, fmt.Errorf("BLIT header length %d not in {8,9,12,13}", len(pkt))
		}
		c.Blit = bh
	case groovy.CmdClose:
		// nothing to parse
	default:
		return c, fmt.Errorf("unknown command type %d", pkt[0])
	}
	return c, nil
}
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(fakemister): parse all 5 command types into typed structs"
```

---

### Task 3.2: Listener loop

**Files:**
- Modify: `internal/fakemister/listener.go`

- [ ] **Step 1: Test with a round-trip**

```go
func TestListener_ReceivesInit(t *testing.T) {
	l, err := NewListener(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	events := make(chan Command, 8)
	go l.Run(events)

	// Send an INIT to the listener's port.
	addr := l.Addr()
	conn, err := net.Dial("udp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write(groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888))

	select {
	case cmd := <-events:
		if cmd.Type != groovy.CmdInit {
			t.Errorf("got cmd %d", cmd.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for command")
	}
}
```

- [ ] **Step 2-4: Implement**

```go
type Listener struct {
	conn *net.UDPConn
}

func NewListener(addr string) (*Listener, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	return &Listener{conn: conn}, nil
}

func (l *Listener) Addr() net.Addr { return l.conn.LocalAddr() }

func (l *Listener) Close() error { return l.conn.Close() }

// Run reads datagrams and sends parsed Commands into events. Unknown packets
// are logged but not fatal. Exits when the connection is closed.
func (l *Listener) Run(events chan<- Command) {
	buf := make([]byte, groovy.MaxDatagram*2)
	for {
		n, _, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		cmd, err := ParseCommand(buf[:n])
		if err != nil {
			slog.Debug("fakemister parse error", "err", err, "n", n)
			continue
		}
		events <- cmd
	}
}
```

Imports: `net`, `log/slog`.

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(fakemister): UDP listener loop with parsed event stream"
```

---

### Task 3.3: Payload reassembly

BLIT headers arrive followed by field payload sliced into MTU chunks. The reassembler takes a header + the stream of datagrams and produces the full compressed-or-raw payload by concatenation.

**Files:**
- Create: `internal/fakemister/reassembler.go`
- Create: `internal/fakemister/reassembler_test.go`

- [ ] **Step 1: Test**

```go
func TestReassembler_CompleteField(t *testing.T) {
	src := make([]byte, 518400)
	for i := range src {
		src[i] = byte(i % 256)
	}
	r := NewReassembler(uint32(len(src)))
	// Feed in chunks of 1472.
	for i := 0; i < len(src); i += groovy.MaxDatagram {
		end := i + groovy.MaxDatagram
		if end > len(src) {
			end = len(src)
		}
		done := r.Write(src[i:end])
		if i+groovy.MaxDatagram >= len(src) {
			if !done {
				t.Errorf("expected done after last chunk at i=%d", i)
			}
		} else {
			if done {
				t.Errorf("done prematurely at i=%d", i)
			}
		}
	}
	if !bytes.Equal(src, r.Bytes()) {
		t.Error("reassembled payload mismatch")
	}
}

func TestReassembler_Overflow(t *testing.T) {
	r := NewReassembler(10)
	r.Write([]byte{1, 2, 3, 4, 5})
	// Next write pushes past expected size — reassembler should reject.
	if !r.Write([]byte{6, 7, 8, 9, 10, 11}) {
		t.Error("expected done=true when we hit expected size")
	}
	if len(r.Bytes()) != 10 {
		t.Errorf("truncated len = %d, want 10", len(r.Bytes()))
	}
}
```

- [ ] **Step 2-4: Implement**

```go
package fakemister

type Reassembler struct {
	expected uint32
	got      uint32
	buf      []byte
}

func NewReassembler(expectedSize uint32) *Reassembler {
	return &Reassembler{expected: expectedSize, buf: make([]byte, 0, expectedSize)}
}

// Write appends a chunk. Returns true once the expected number of bytes has been
// received. Additional bytes beyond expected are dropped.
func (r *Reassembler) Write(chunk []byte) bool {
	remaining := r.expected - r.got
	if uint32(len(chunk)) > remaining {
		chunk = chunk[:remaining]
	}
	r.buf = append(r.buf, chunk...)
	r.got += uint32(len(chunk))
	return r.got >= r.expected
}

func (r *Reassembler) Bytes() []byte { return r.buf }
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(fakemister): payload reassembler (concat by arrival)"
```

---

### Task 3.4: PNG + WAV dumping

**Files:**
- Create: `internal/fakemister/dumper.go`
- Create: `internal/fakemister/dumper_test.go`

- [ ] **Step 1: Test PNG output for a known RGB field**

```go
func TestDumpFieldPNG(t *testing.T) {
	dir := t.TempDir()
	d := NewDumper(dir, 100 /* sample every 100th field */)
	// Create a 720x240 RGB888 red field.
	field := make([]byte, 720*240*3)
	for i := 0; i < len(field); i += 3 {
		field[i] = 255
	}
	for i := 0; i < 100; i++ {
		d.MaybeDumpField(uint32(i), 720, 240, field)
	}
	pngs, _ := filepath.Glob(filepath.Join(dir, "field_*.png"))
	if len(pngs) == 0 {
		t.Fatal("no PNG files written")
	}
}
```

- [ ] **Step 2-4: Implement dumper**

```go
package fakemister

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
)

type Dumper struct {
	dir        string
	sampleEvery int
	mu         sync.Mutex
	audioFile  *os.File
	audioBytes int
}

func NewDumper(dir string, sampleEvery int) *Dumper {
	_ = os.MkdirAll(dir, 0755)
	return &Dumper{dir: dir, sampleEvery: sampleEvery}
}

func (d *Dumper) MaybeDumpField(frame uint32, width, height int, rgb888 []byte) error {
	if d.sampleEvery <= 0 || int(frame)%d.sampleEvery != 0 {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 3
			img.Set(x, y, color.RGBA{R: rgb888[i], G: rgb888[i+1], B: rgb888[i+2], A: 255})
		}
	}
	path := filepath.Join(d.dir, fmt.Sprintf("field_%08d.png", frame))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func (d *Dumper) StartAudio(sampleRate, channels int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	path := filepath.Join(d.dir, "audio.wav")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	// Write placeholder WAV header; patched on close.
	writeWAVHeader(f, sampleRate, channels, 0)
	d.audioFile = f
	d.audioBytes = 0
	return nil
}

func (d *Dumper) WriteAudio(pcm []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.audioFile == nil {
		return nil
	}
	n, err := d.audioFile.Write(pcm)
	d.audioBytes += n
	return err
}

func (d *Dumper) CloseAudio(sampleRate, channels int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.audioFile == nil {
		return nil
	}
	d.audioFile.Seek(0, 0)
	writeWAVHeader(d.audioFile, sampleRate, channels, d.audioBytes)
	err := d.audioFile.Close()
	d.audioFile = nil
	return err
}

func writeWAVHeader(w *os.File, sampleRate, channels, dataBytes int) {
	bitsPerSample := 16
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	h := make([]byte, 44)
	copy(h[0:4], "RIFF")
	binary.LittleEndian.PutUint32(h[4:8], uint32(36+dataBytes))
	copy(h[8:12], "WAVE")
	copy(h[12:16], "fmt ")
	binary.LittleEndian.PutUint32(h[16:20], 16)
	binary.LittleEndian.PutUint16(h[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(h[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(h[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(h[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(h[34:36], uint16(bitsPerSample))
	copy(h[36:40], "data")
	binary.LittleEndian.PutUint32(h[40:44], uint32(dataBytes))
	w.Write(h)
}
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(fakemister): PNG sampling and WAV audio dumping"
```

---

### Task 3.5: Session recorder for assertions

**Files:**
- Create: `internal/fakemister/recorder.go`
- Create: `internal/fakemister/recorder_test.go`

- [ ] **Step 1: Test**

```go
func TestRecorder_Counts(t *testing.T) {
	r := NewRecorder()
	r.Record(Command{Type: groovy.CmdInit})
	r.Record(Command{Type: groovy.CmdSwitchres})
	r.Record(Command{Type: groovy.CmdBlitFieldVSync, Blit: &BlitHeader{}})
	r.Record(Command{Type: groovy.CmdBlitFieldVSync, Blit: &BlitHeader{}})
	r.Record(Command{Type: groovy.CmdAudio, Audio: &AudioPayload{PCM: []byte{0, 0}}})
	r.Record(Command{Type: groovy.CmdClose})

	snap := r.Snapshot()
	if snap.Counts[groovy.CmdBlitFieldVSync] != 2 {
		t.Errorf("blit count = %d, want 2", snap.Counts[groovy.CmdBlitFieldVSync])
	}
	if snap.AudioBytes != 2 {
		t.Errorf("audio bytes = %d", snap.AudioBytes)
	}
}
```

- [ ] **Step 2-4: Implement**

```go
package fakemister

import (
	"sync"
	"time"
)

type RecorderSnapshot struct {
	Counts     map[byte]int
	AudioBytes int
	FirstSeen  time.Time
	LastSeen   time.Time
	Sequence   []byte // command type sequence
}

type Recorder struct {
	mu         sync.Mutex
	counts     map[byte]int
	audioBytes int
	firstSeen  time.Time
	lastSeen   time.Time
	sequence   []byte
}

func NewRecorder() *Recorder {
	return &Recorder{counts: make(map[byte]int)}
}

func (r *Recorder) Record(c Command) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.firstSeen.IsZero() {
		r.firstSeen = now
	}
	r.lastSeen = now
	r.counts[c.Type]++
	r.sequence = append(r.sequence, c.Type)
	if c.Audio != nil {
		r.audioBytes += len(c.Audio.PCM)
	}
}

func (r *Recorder) Snapshot() RecorderSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := make(map[byte]int, len(r.counts))
	for k, v := range r.counts {
		counts[k] = v
	}
	seq := make([]byte, len(r.sequence))
	copy(seq, r.sequence)
	return RecorderSnapshot{
		Counts:     counts,
		AudioBytes: r.audioBytes,
		FirstSeen:  r.firstSeen,
		LastSeen:   r.lastSeen,
		Sequence:   seq,
	}
}
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(fakemister): thread-safe session recorder with snapshot"
```

---

### Task 3.6: fake-mister main binary

**Files:**
- Modify: `cmd/fake-mister/main.go`

- [ ] **Step 1: Implement main that wires listener + reassembly + dumper + recorder, with flags for port, output dir, sample rate.**

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func main() {
	addr := flag.String("addr", ":32100", "UDP listen address")
	outDir := flag.String("out", "./fake-mister-dumps", "dump output directory")
	pngEvery := flag.Int("png-every", 60, "write a PNG every N fields (<=0 disables)")
	flag.Parse()

	l, err := fakemister.NewListener(*addr)
	if err != nil {
		slog.Error("listen", "err", err)
		os.Exit(1)
	}
	defer l.Close()
	slog.Info("fake-mister listening", "addr", l.Addr().String(), "out", *outDir)

	rec := fakemister.NewRecorder()
	dumper := fakemister.NewDumper(*outDir, *pngEvery)

	events := make(chan fakemister.Command, 64)
	go l.Run(events)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Session state carried across events. INIT fixes RGB mode + audio config;
	// SWITCHRES fixes the per-field dimensions.
	var (
		initPayload *fakemister.InitPayload
		modeline    *fakemister.SwitchresPayload
		sampleRate  int
		channels    int
	)

	// RunWithFields delivers already-reassembled BLIT and AUDIO payloads via
	// the FieldEvent and AudioEvent channels (see Task 3.7). Unknown-mode
	// datagrams arrive via events as parsed Commands.
	fieldEvents := make(chan fakemister.FieldEvent, 8)
	audioEvents := make(chan fakemister.AudioEvent, 32)
	go l.RunWithFields(events, fieldEvents, audioEvents, func() uint32 {
		return fieldSize(modeline, initPayload)
	})

	for {
		select {
		case <-ctx.Done():
			dumper.CloseAudio(sampleRate, channels)
			snap := rec.Snapshot()
			fmt.Printf("\n=== Session Summary ===\n")
			for t, n := range snap.Counts {
				fmt.Printf("  cmd %d: %d\n", t, n)
			}
			fmt.Printf("  audio bytes: %d\n", snap.AudioBytes)
			return
		case cmd := <-events:
			rec.Record(cmd)
			switch cmd.Type {
			case groovy.CmdInit:
				initPayload = cmd.Init
				sampleRate = sampleRateFromCode(cmd.Init.SoundRate)
				channels = int(cmd.Init.SoundChan)
				if sampleRate > 0 && channels > 0 {
					dumper.StartAudio(sampleRate, channels)
				}
			case groovy.CmdSwitchres:
				modeline = cmd.Switchres
			case groovy.CmdClose:
				dumper.CloseAudio(sampleRate, channels)
			}
		case fe := <-fieldEvents:
			payload := fe.Payload
			if fe.Header.Compressed {
				raw, err := groovy.LZ4Decompress(payload, int(fieldSize(modeline, initPayload)))
				if err != nil {
					slog.Warn("lz4 decompress failed", "err", err, "frame", fe.Header.Frame)
					continue
				}
				payload = raw
			}
			w, h, _ := fieldDims(modeline)
			dumper.MaybeDumpField(fe.Header.Frame, w, h, payload)
		case ae := <-audioEvents:
			dumper.WriteAudio(ae.PCM)
		}
	}
}

// fieldSize returns the expected reassembled bytes for one BLIT field payload,
// derived from INIT rgbMode and SWITCHRES hActive × per-field vActive.
func fieldSize(ml *fakemister.SwitchresPayload, init *fakemister.InitPayload) uint32 {
	if ml == nil {
		return 0
	}
	bpp := 3
	if init != nil {
		switch init.RGBMode {
		case groovy.RGBMode8888:
			bpp = 4
		case groovy.RGBMode565:
			bpp = 2
		}
	}
	return uint32(int(ml.HActive) * int(ml.VActive) * bpp)
}

func fieldDims(ml *fakemister.SwitchresPayload) (w, h int, ok bool) {
	if ml == nil {
		return 0, 0, false
	}
	return int(ml.HActive), int(ml.VActive), true
}

func sampleRateFromCode(code byte) int {
	switch code {
	case groovy.AudioRate22050:
		return 22050
	case groovy.AudioRate44100:
		return 44100
	case groovy.AudioRate48000:
		return 48000
	}
	return 0
}
```

- [ ] **Step 2: Commit the partial version**

```bash
git add cmd/fake-mister/main.go
git commit -m "feat(fake-mister): initial main with listener, recorder, dumper (payload reassembly pending)"
```

---

### Task 3.7: Listener state: header vs payload datagrams (BLIT and AUDIO)

Both BLIT_FIELD_VSYNC and AUDIO are followed by payload datagrams that do **not** begin with a command byte. The listener must switch into a per-payload "payload mode," consume exactly the expected number of bytes across datagrams, emit a reassembled event, and return to command-parsing mode.

- BLIT expected size: either `cSize` (from the LZ4 header) or `hActive*vActive*bpp` (for RAW full) — caller-provided via `fieldSizeFn`. Duplicate (9-byte) headers have no payload and must NOT enter payload mode.
- AUDIO expected size: `soundSize` (from the 3-byte header).

**Files:**
- Modify: `internal/fakemister/listener.go`
- Modify: `internal/fakemister/listener_test.go`

- [ ] **Step 1: Tests that send BLIT+payload and AUDIO+payload**

```go
func TestListener_BlitHeaderThenPayload(t *testing.T) {
	l, _ := NewListener(":0")
	defer l.Close()

	cmds := make(chan Command, 8)
	fields := make(chan FieldEvent, 8)
	audios := make(chan AudioEvent, 8)
	go l.RunWithFields(cmds, fields, audios, func() uint32 { return 100 })

	conn, _ := net.Dial("udp", l.Addr().String())
	defer conn.Close()
	hdr := groovy.BuildBlitHeader(groovy.BlitOpts{Frame: 1, Field: 0})
	conn.Write(hdr)
	conn.Write(make([]byte, 50))
	conn.Write(make([]byte, 50))

	select {
	case fe := <-fields:
		if len(fe.Payload) != 100 {
			t.Errorf("payload len = %d, want 100", len(fe.Payload))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestListener_AudioHeaderThenPayload(t *testing.T) {
	l, _ := NewListener(":0")
	defer l.Close()

	cmds := make(chan Command, 8)
	fields := make(chan FieldEvent, 8)
	audios := make(chan AudioEvent, 8)
	go l.RunWithFields(cmds, fields, audios, func() uint32 { return 0 })

	conn, _ := net.Dial("udp", l.Addr().String())
	defer conn.Close()
	conn.Write(groovy.BuildAudioHeader(1000))
	conn.Write(make([]byte, 500))
	conn.Write(make([]byte, 500))

	select {
	case ae := <-audios:
		if len(ae.PCM) != 1000 {
			t.Errorf("audio len = %d, want 1000", len(ae.PCM))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
```

- [ ] **Step 2: Implement `RunWithFields`**

```go
type FieldEvent struct {
	Header  BlitHeader
	Payload []byte
}

type AudioEvent struct {
	PCM []byte
}

type payloadMode int

const (
	modeCommand payloadMode = iota
	modeBlit
	modeAudio
)

// RunWithFields is the full listener loop. After a BLIT_FIELD_VSYNC header
// it reassembles the next N bytes (where N = fieldSizeFn() for RAW, or cSize
// from the LZ4 header) into a FieldEvent. After an AUDIO header it
// reassembles the next soundSize bytes into an AudioEvent. Non-BLIT /
// non-AUDIO commands (INIT, SWITCHRES, CLOSE) go straight to the cmds
// channel — callers use them to update session state.
//
// fieldSizeFn is invoked only for RAW-full BLIT headers (8-byte variant);
// LZ4 headers carry their size at [8..11] and dup headers have no payload.
func (l *Listener) RunWithFields(
	cmds chan<- Command,
	fields chan<- FieldEvent,
	audios chan<- AudioEvent,
	fieldSizeFn func() uint32,
) {
	buf := make([]byte, groovy.MaxDatagram*2)
	var (
		mode       payloadMode
		reass      *Reassembler
		blitHeader BlitHeader
	)
	for {
		n, _, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		data := buf[:n]
		switch mode {
		case modeCommand:
			cmd, err := ParseCommand(data)
			if err != nil {
				slog.Debug("fakemister parse error", "err", err, "n", n)
				continue
			}
			cmds <- cmd
			switch cmd.Type {
			case groovy.CmdBlitFieldVSync:
				if cmd.Blit == nil || cmd.Blit.Duplicate {
					continue // no payload
				}
				size := cmd.Blit.CompressedSize
				if !cmd.Blit.Compressed {
					size = fieldSizeFn()
				}
				if size == 0 {
					continue
				}
				blitHeader = *cmd.Blit
				reass = NewReassembler(size)
				mode = modeBlit
			case groovy.CmdAudio:
				if cmd.Audio == nil || cmd.Audio.SoundSize == 0 {
					continue
				}
				reass = NewReassembler(uint32(cmd.Audio.SoundSize))
				mode = modeAudio
			}
		case modeBlit:
			if reass.Write(data) {
				fields <- FieldEvent{Header: blitHeader, Payload: reass.Bytes()}
				reass = nil
				mode = modeCommand
			}
		case modeAudio:
			if reass.Write(data) {
				audios <- AudioEvent{PCM: reass.Bytes()}
				reass = nil
				mode = modeCommand
			}
		}
	}
}
```

- [ ] **Step 3-4: Run and verify**

- [ ] **Step 5: cmd/fake-mister/main.go already consumes fieldEvents + audioEvents from Task 3.6; no further wiring needed.**

- [ ] **Step 6: Commit**

```bash
git commit -am "feat(fakemister): BLIT payload reassembly across UDP datagrams"
```

---

### ✅ Phase 3 Review Checkpoint

**Report to user:**
- `fake-mister` binary builds and runs.
- Listens on UDP, parses all 5 command types, reassembles BLIT payloads, dumps sampled PNGs and a WAV audio file.
- Unit tests pass.
- Not yet exercised by a real sender — that's Phase 4.

**Manual validation (optional):** point the current MiSTerCast binary at `fake-mister` to confirm parse compatibility. Skip if you don't have MiSTerCast set up.

---

## Phase 4: Groovy UDP Sender

### Task 4.1: Sender with stable source port

**Files:**
- Create: `internal/groovynet/sender.go`
- Create: `internal/groovynet/sender_test.go`

- [ ] **Step 1: Test — send INIT to fake-mister, verify it parses**

```go
package groovynet

import (
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func TestSender_DeliversInit(t *testing.T) {
	l, _ := fakemister.NewListener(":0")
	defer l.Close()
	events := make(chan fakemister.Command, 4)
	go l.Run(events)

	addr := l.Addr().(*net.UDPAddr)
	s, err := NewSender("127.0.0.1", addr.Port, 0 /* any source port */)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Send(groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888)); err != nil {
		t.Fatal(err)
	}

	select {
	case c := <-events:
		if c.Type != groovy.CmdInit {
			t.Errorf("got %d", c.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
}

func TestSender_StableSourcePort(t *testing.T) {
	s, err := NewSender("127.0.0.1", 32100, 32199)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.SourcePort() != 32199 {
		t.Errorf("source port = %d, want 32199", s.SourcePort())
	}
}
```

- [ ] **Step 2-4: Implement**

```go
package groovynet

import (
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

type Sender struct {
	conn    *net.UDPConn
	dstAddr *net.UDPAddr
	srcPort int

	mu           sync.Mutex // serialises Writes + Mark*
	lastBlitSize int
	lastBlitTime time.Time
}

func NewSender(dstHost string, dstPort, srcPort int) (*Sender, error) {
	dst, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", dstHost, dstPort))
	if err != nil {
		return nil, err
	}
	lc := &net.ListenConfig{Control: controlSocket}
	addr := fmt.Sprintf(":%d", srcPort)
	pc, err := lc.ListenPacket(nil, "udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("bind source %d: %w", srcPort, err)
	}
	conn := pc.(*net.UDPConn)

	// Reference sender sets SO_SNDBUF ≥ 2 MB to absorb 518 KB field bursts.
	_ = conn.SetWriteBuffer(2 * 1024 * 1024)
	_ = conn.SetReadBuffer(256 * 1024)

	actual := conn.LocalAddr().(*net.UDPAddr).Port
	return &Sender{conn: conn, dstAddr: dst, srcPort: actual}, nil
}

// controlSocket sets SO_REUSEADDR (so a rapid bridge restart doesn't hit
// TIME_WAIT on the stable source port) and IP_PMTUDISC_DO /
// IP_DONTFRAGMENT (so oversized datagrams are dropped rather than
// IP-fragmented — matches the reference sender and prevents the MiSTer
// receiver from reassembling OS fragments as application-level bytes).
func controlSocket(network, address string, c syscall.RawConn) error {
	var setErr error
	err := c.Control(func(fd uintptr) {
		if e := syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); e != nil {
			setErr = e
			return
		}
		// IP_MTU_DISCOVER on Linux / IP_DONTFRAG on BSD — platform-specific.
		// Implementer: use golang.org/x/sys/unix IP_MTU_DISCOVER=IP_PMTUDISC_DO
		// on Linux (Docker target). See setDontFragment in sender_linux.go.
		setDontFragment(fd)
	})
	if err != nil {
		return err
	}
	return setErr
}

// SourcePort returns the actual bound source port.
func (s *Sender) SourcePort() int { return s.srcPort }

// Conn exposes the underlying UDPConn for co-located components (Drainer).
// Cross-package access beyond groovynet is not supported.
func (s *Sender) Conn() *net.UDPConn { return s.conn }

// Send writes a single packet (header). Does not enter the congestion window.
func (s *Sender) Send(pkt []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.conn.WriteToUDP(pkt, s.dstAddr)
	return err
}

// SendPayload slices large payloads into MTU-sized datagrams.
// Used for BLIT field bytes and AUDIO PCM.
func (s *Sender) SendPayload(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < len(payload); i += groovy.MaxDatagram {
		end := i + groovy.MaxDatagram
		if end > len(payload) {
			end = len(payload)
		}
		if _, err := s.conn.WriteToUDP(payload[i:end], s.dstAddr); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sender) Close() error { return s.conn.Close() }
```

Also add `sender_linux.go` with a build tag for the Linux-only `IP_MTU_DISCOVER` option; on other platforms a no-op is acceptable for the dev loop:

```go
//go:build linux

package groovynet

import "golang.org/x/sys/unix"

func setDontFragment(fd uintptr) {
	_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DO)
}
```

And `sender_other.go`:

```go
//go:build !linux

package groovynet

func setDontFragment(fd uintptr) { /* no-op on dev platforms */ }
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(groovynet): UDP sender with stable source port and MTU slicing"
```

---

### Task 4.1b: INIT ACK handshake (blocking, 60 ms timeout)

**Reference:** groovy_mister.md:49 ("Sender `getACK(60)` with 60 ms timeout, failure = tear down"), mistercast.md:34 ("MiSTerCast times out INIT at 60 ms"). INIT is the ONE ack-gated handshake; every other command is fire-and-forget.

**Files:**
- Modify: `internal/groovynet/sender.go`
- Modify: `internal/groovynet/sender_test.go`

- [ ] **Step 1: Test**

```go
func TestSender_InitACKHandshakeSuccess(t *testing.T) {
	l, _ := fakemister.NewListener(":0")
	defer l.Close()
	// Stub: fake-mister replies with a 13-byte ACK when it sees INIT.
	go func() {
		buf := make([]byte, 64)
		for {
			n, src, err := l.Conn().ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n >= 1 && buf[0] == groovy.CmdInit {
				reply := make([]byte, groovy.ACKPacketSize)
				reply[12] = 1 << 6 // audio-ready
				l.Conn().WriteToUDP(reply, src)
			}
		}
	}()

	addr := l.Addr().(*net.UDPAddr)
	s, _ := NewSender("127.0.0.1", addr.Port, 0)
	defer s.Close()

	ack, err := s.SendInitAwaitACK(
		groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888),
		60*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("INIT ACK: %v", err)
	}
	if !ack.AudioReady() {
		t.Error("expected audio-ready in ACK")
	}
}

func TestSender_InitACKTimeout(t *testing.T) {
	// No listener → no ACK → timeout expected.
	s, _ := NewSender("127.0.0.1", 19, 0) // port 19 unused
	defer s.Close()
	_, err := s.SendInitAwaitACK(
		groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888),
		60*time.Millisecond,
	)
	if err == nil {
		t.Error("expected timeout error")
	}
}
```

- [ ] **Step 2: Implement**

```go
// SendInitAwaitACK sends INIT, then blocks up to `timeout` waiting for the
// 13-byte status reply. Returns parsed ACK or an error. Callers must NOT
// have a Drainer goroutine reading the same socket at this point — the
// Drainer is started AFTER the handshake succeeds.
func (s *Sender) SendInitAwaitACK(initPacket []byte, timeout time.Duration) (groovy.ACK, error) {
	if len(initPacket) == 0 || initPacket[0] != groovy.CmdInit {
		return groovy.ACK{}, fmt.Errorf("not an INIT packet")
	}
	if err := s.Send(initPacket); err != nil {
		return groovy.ACK{}, err
	}
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return groovy.ACK{}, err
	}
	defer s.conn.SetReadDeadline(time.Time{})
	buf := make([]byte, groovy.ACKPacketSize*2)
	n, _, err := s.conn.ReadFromUDP(buf)
	if err != nil {
		return groovy.ACK{}, fmt.Errorf("INIT ack timeout: %w", err)
	}
	if n != groovy.ACKPacketSize {
		return groovy.ACK{}, fmt.Errorf("INIT ack wrong size: %d", n)
	}
	return groovy.ParseACK(buf[:n])
}
```

- [ ] **Step 3-5: Commit**

```bash
git commit -am "feat(groovynet): INIT-ACK blocking handshake with 60ms timeout"
```

---

### Task 4.2: Congestion back-off

- [ ] **Step 1: Test** — sender tracks last-blit time and size; if previous payload was >500KB, it should wait ≥11ms before next send.

```go
func TestSender_CongestionBackoff(t *testing.T) {
	s, err := NewSender("127.0.0.1", 12345, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.MarkBlitSent(600 * 1024)
	// Should block for ~11ms on the next call.
	start := time.Now()
	s.WaitForCongestion()
	elapsed := time.Since(start)
	if elapsed < 8*time.Millisecond {
		t.Errorf("congestion wait elapsed=%v, expected ≥8ms", elapsed)
	}
}
```

- [ ] **Step 2-4: Implement**

Add to `sender.go` (fields `lastBlitSize` and `lastBlitTime` are already declared in the Sender struct from Task 4.1):

```go
// MarkBlitSent records the size and time of the last BLIT field sent so
// WaitForCongestion can enforce the back-off window. Per reference
// (K_CONGESTION_SIZE=500000, K_CONGESTION_TIME≈11ms) — applies to the total
// payload bytes of the last blit, not the header.
func (s *Sender) MarkBlitSent(size int) {
	s.mu.Lock()
	s.lastBlitSize = size
	s.lastBlitTime = time.Now()
	s.mu.Unlock()
}

// WaitForCongestion blocks until the minimum inter-blit interval has elapsed
// if the previous blit exceeded the congestion threshold. Safe to call once
// per tick from the data-plane pump loop.
func (s *Sender) WaitForCongestion() {
	s.mu.Lock()
	size := s.lastBlitSize
	last := s.lastBlitTime
	s.mu.Unlock()
	if size <= groovy.CongestionSize {
		return
	}
	elapsed := time.Since(last)
	remaining := time.Duration(groovy.CongestionWait)*time.Millisecond - elapsed
	if remaining > 0 {
		time.Sleep(remaining)
	}
}
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(groovynet): congestion back-off after fields >500KB"
```

---

### Task 4.3: ACK drainer goroutine

Reads ACK packets on the same socket, pushes to a channel non-blockingly, drops on full channel. Does not block sender.

**Files:**
- Create: `internal/groovynet/drainer.go`
- Create: `internal/groovynet/drainer_test.go`

- [ ] **Step 1: Test**

```go
func TestDrainer_PushesAndDrops(t *testing.T) {
	// Set up a pair of UDP sockets; one acts as fake-mister emitting ACKs.
	fake, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer fake.Close()

	s, _ := NewSender("127.0.0.1", fake.LocalAddr().(*net.UDPAddr).Port, 0)
	defer s.Close()

	ch := make(chan groovy.ACK, 1) // intentionally small
	d := NewDrainer(s, ch)
	go d.Run()

	// Fake-mister sends 3 ACKs; drainer delivers what it can, drops the rest.
	for i := uint32(0); i < 3; i++ {
		pkt := make([]byte, groovy.ACKPacketSize)
		binary.LittleEndian.PutUint32(pkt[0:4], i)
		fake.WriteToUDP(pkt, s.Conn().LocalAddr().(*net.UDPAddr))
	}

	select {
	case ack := <-ch:
		if ack.FrameEcho != 0 {
			t.Errorf("first ack frame = %d", ack.FrameEcho)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no ack received")
	}
}
```

- [ ] **Step 2-4: Implement**

```go
package groovynet

import (
	"log/slog"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// Drainer reads ACK packets from the Sender's socket and delivers them on a
// buffered channel. Dropping on a full channel is intentional — ACKs are
// informational and missing a few does not break the session.
//
// Drainer MUST NOT run while SendInitAwaitACK is pending on the same socket.
// Lifecycle: call SendInitAwaitACK first; only then start the Drainer.
type Drainer struct {
	s  *Sender
	ch chan<- groovy.ACK
}

func NewDrainer(s *Sender, ch chan<- groovy.ACK) *Drainer {
	return &Drainer{s: s, ch: ch}
}

func (d *Drainer) Run() {
	buf := make([]byte, groovy.ACKPacketSize*2)
	conn := d.s.Conn()
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n != groovy.ACKPacketSize {
			continue
		}
		ack, err := groovy.ParseACK(buf[:n])
		if err != nil {
			continue
		}
		select {
		case d.ch <- ack:
		default:
			slog.Debug("ack channel full, dropping")
		}
	}
}
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(groovynet): non-blocking ACK drainer goroutine"
```

---

### Task 4.4: First end-to-end integration test (sender → fake)

**Files:**
- Create: `tests/integration/helper_test.go`
- Create: `tests/integration/basic_test.go`

- [ ] **Step 1: Integration helper**

```go
//go:build integration

package integration

import (
	"net"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

type Harness struct {
	Listener *fakemister.Listener
	Sender   *groovynet.Sender
	Recorder *fakemister.Recorder
	Events   chan fakemister.Command
}

func NewHarness(t *testing.T) *Harness {
	t.Helper()
	l, err := fakemister.NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan fakemister.Command, 256)
	rec := fakemister.NewRecorder()
	go func() {
		for c := range events {
			rec.Record(c)
		}
	}()
	go l.Run(events)

	addr := l.Addr().(*net.UDPAddr)
	s, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		l.Close()
		close(events)
	})
	return &Harness{Listener: l, Sender: s, Recorder: rec, Events: events}
}
```

- [ ] **Step 2: Basic scenario test**

```go
//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func TestBasic_InitSwitchresClose(t *testing.T) {
	h := NewHarness(t)
	// Integration smoke: drive the fake via the unguarded Send (NOT
	// SendInitAwaitACK) because the fake listener never replies with an ACK
	// in this basic scenario. Real Plane.Run uses SendInitAwaitACK.
	h.Sender.Send(groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRate48000, 2, groovy.RGBMode888))
	h.Sender.Send(groovy.BuildSwitchres(groovy.NTSC480i60))
	h.Sender.Send(groovy.BuildClose())
	time.Sleep(100 * time.Millisecond)

	snap := h.Recorder.Snapshot()
	if snap.Counts[groovy.CmdInit] != 1 {
		t.Errorf("init count = %d", snap.Counts[groovy.CmdInit])
	}
	if snap.Counts[groovy.CmdSwitchres] != 1 {
		t.Errorf("switchres count = %d", snap.Counts[groovy.CmdSwitchres])
	}
	if snap.Counts[groovy.CmdClose] != 1 {
		t.Errorf("close count = %d", snap.Counts[groovy.CmdClose])
	}
}
```

- [ ] **Step 3: Run**

Run: `go test -tags=integration ./tests/integration/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git commit -am "test(integration): basic INIT/SWITCHRES/CLOSE flow against fake-mister"
```

---

### ✅ Phase 4 Review Checkpoint

**Report to user:**
- Groovy UDP sender works end-to-end with fake-mister.
- First green integration test: commands round-trip through UDP correctly.
- From here on, every protocol-level change has automated feedback in seconds.

---

## Phase 5: FFmpeg Pipeline

### Task 5.1: ffprobe wrapper

**Files:**
- Create: `internal/ffmpeg/probe.go`
- Create: `internal/ffmpeg/probe_test.go`

- [ ] **Step 1: Define probe output struct and implement using `exec.Command("ffprobe", ...)`**

```go
package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type ProbeResult struct {
	Width      int
	Height     int
	FrameRate  float64
	Interlaced bool
	AudioRate  int
	Duration   float64
}

type ffprobeOutput struct {
	Streams []struct {
		CodecType     string `json:"codec_type"`
		Width         int    `json:"width"`
		Height        int    `json:"height"`
		FieldOrder    string `json:"field_order"`
		RFrameRate    string `json:"r_frame_rate"`
		SampleRate    string `json:"sample_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func Probe(ctx context.Context, url string) (*ProbeResult, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_streams", "-show_format",
		url,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	var raw ffprobeOutput
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse ffprobe: %w", err)
	}
	r := &ProbeResult{}
	for _, s := range raw.Streams {
		if s.CodecType == "video" {
			r.Width = s.Width
			r.Height = s.Height
			r.FrameRate = parseFrameRate(s.RFrameRate)
			r.Interlaced = s.FieldOrder == "tt" || s.FieldOrder == "bb" || s.FieldOrder == "tb" || s.FieldOrder == "bt"
		} else if s.CodecType == "audio" && r.AudioRate == 0 {
			fmt.Sscan(s.SampleRate, &r.AudioRate)
		}
	}
	fmt.Sscan(raw.Format.Duration, &r.Duration)
	return r, nil
}

func parseFrameRate(s string) float64 {
	var num, den float64
	if _, err := fmt.Sscanf(s, "%f/%f", &num, &den); err == nil && den != 0 {
		return num / den
	}
	return 0
}
```

- [ ] **Step 2: Smoke test** against a small local video file if CI provides one; otherwise skip with a build tag.

- [ ] **Step 3: Commit**

```bash
git commit -am "feat(ffmpeg): ffprobe wrapper returning structured stream info"
```

---

### Task 5.2: FFmpeg filter graph and invocation builder

The hardest part. Must construct the right filter chain based on source properties.

**Files:**
- Create: `internal/ffmpeg/pipeline.go`
- Create: `internal/ffmpeg/pipeline_test.go`

- [ ] **Step 1: Define `PipelineSpec`**

```go
package ffmpeg

type PipelineSpec struct {
	InputURL        string
	InputHeaders    map[string]string // for Plex transcode URL tokens
	SeekSeconds     float64
	UseSSSeek       bool // true on direct-play (pass -ss); false on transcode (offset is in URL)
	SourceProbe     *ProbeResult
	OutputWidth     int
	OutputHeight    int
	FieldOrder      string // "tff" | "bff"
	AspectMode      string // "letterbox" | "zoom" | "auto"
	CropRect        *CropRect // set by auto-probe pass when AspectMode == "auto"
	SubtitleURL     string    // empty = no subs
	SubtitleIndex   int
	AudioSampleRate int
	AudioChannels   int
	VideoPipePath   string // named pipe or - for stdout
	AudioPipePath   string // named pipe or - for stdout
}
```

**Pipeline output contract:** this task produces one 720×240 RGB888 **field** per pipe read at exactly 59.94 fields/sec. The data plane reads `hActive*vActive*3` bytes per tick and sends one BLIT_FIELD_VSYNC alternating `field=0`/`field=1`. Key choices:

1. **Frame-rate target is 59.94 fps of _fields_, not frames.** Use `telecine` (23.976→29.97i) or `fps=30000/1001` (for 25/30p) to get to 29.97 frame-per-second, then `interlace=scan=...:lowpass=0` to build 29.97i (720×480), then `separatefields` to emit 59.94 fields/sec (720×240 each). `separatefields` doubles the frame rate and halves the height — exactly what we need.
2. **Subtitle burn-in comes BEFORE `interlace`** so captions composite onto the progressive raster.
3. **Cropdetect-auto requires a probe pass** (first ~2s) to capture the crop rect, then a real pipeline using that rect. See Task 5.2b.

- [ ] **Step 2: Test filter-graph construction (pure string assembly)**

```go
func TestBuildFilterChain_Progressive24p(t *testing.T) {
	spec := PipelineSpec{
		SourceProbe: &ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976, Interlaced: false},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "letterbox",
	}
	chain := buildFilterChain(spec)
	for _, need := range []string{"telecine", "interlace=scan=tff", "separatefields", "pad="} {
		if !strings.Contains(chain, need) {
			t.Errorf("chain missing %q: %s", need, chain)
		}
	}
}

func TestBuildFilterChain_Interlaced30i(t *testing.T) {
	spec := PipelineSpec{
		SourceProbe: &ProbeResult{Width: 720, Height: 480, FrameRate: 29.97, Interlaced: true},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "letterbox",
	}
	chain := buildFilterChain(spec)
	if !strings.Contains(chain, "yadif") {
		t.Error("expected yadif for interlaced source")
	}
	if !strings.Contains(chain, "separatefields") {
		t.Error("expected separatefields to produce 59.94 field output")
	}
}

func TestBuildFilterChain_BffScan(t *testing.T) {
	spec := PipelineSpec{
		SourceProbe: &ProbeResult{Width: 720, Height: 480, FrameRate: 29.97},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "bff", AspectMode: "letterbox",
	}
	chain := buildFilterChain(spec)
	if !strings.Contains(chain, "interlace=scan=bff") {
		t.Error("expected bff scan")
	}
}

func TestBuildFilterChain_SubtitleBeforeInterlace(t *testing.T) {
	spec := PipelineSpec{
		SourceProbe: &ProbeResult{Width: 1920, Height: 1080, FrameRate: 24, Interlaced: false},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "letterbox",
		SubtitleURL: "http://pms/subtitle.srt", SubtitleIndex: 0,
	}
	chain := buildFilterChain(spec)
	subIdx := strings.Index(chain, "subtitles=")
	intIdx := strings.Index(chain, "interlace=")
	if subIdx < 0 || intIdx < 0 || subIdx >= intIdx {
		t.Errorf("subtitles must precede interlace: %s", chain)
	}
}

func TestBuildFilterChain_AutoCropUsesLockedRect(t *testing.T) {
	spec := PipelineSpec{
		SourceProbe: &ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976},
		OutputWidth: 720, OutputHeight: 480,
		FieldOrder: "tff", AspectMode: "auto",
		CropRect: &CropRect{W: 1920, H: 800, X: 0, Y: 140}, // from probe
	}
	chain := buildFilterChain(spec)
	if !strings.Contains(chain, "crop=1920:800:0:140") {
		t.Errorf("expected locked crop, got %s", chain)
	}
	if strings.Contains(chain, "cropdetect") {
		t.Errorf("main chain must NOT include cropdetect (that runs in the probe pass)")
	}
}
```

- [ ] **Step 3: Implement**

```go
package ffmpeg

import (
	"fmt"
	"strings"
)

// CropRect is a locked crop window produced by Task 5.2b's probe pass.
// When non-nil (auto mode) it replaces the default pad-to-fit behaviour.
type CropRect struct {
	W, H, X, Y int
}

func buildFilterChain(s PipelineSpec) string {
	var filters []string

	// 1. Deinterlace source if interlaced. yadif=send_frame → one output frame
	//    per input frame (output rate = input rate).
	if s.SourceProbe != nil && s.SourceProbe.Interlaced {
		filters = append(filters, "yadif=mode=send_frame")
	}

	// 2. Normalise to 29.97 progressive frames/sec. The final separatefields
	//    step doubles this to 59.94 fields/sec.
	if s.SourceProbe != nil {
		fr := s.SourceProbe.FrameRate
		switch {
		case fr >= 23.0 && fr < 24.0:
			// 23.976 → 2:3 pulldown → 29.97. `telecine` default pattern is 2:3.
			filters = append(filters, "telecine=pattern=23")
		default:
			// 25/29.97/30/50/60 → normalise to 29.97 for the interlace step.
			filters = append(filters, "fps=30000/1001")
		}
	}

	// 3. Aspect / crop.
	switch {
	case s.AspectMode == "auto" && s.CropRect != nil:
		r := s.CropRect
		filters = append(filters,
			fmt.Sprintf("crop=%d:%d:%d:%d", r.W, r.H, r.X, r.Y),
			fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease", s.OutputWidth, s.OutputHeight),
			fmt.Sprintf("pad=w=%d:h=%d:x=(ow-iw)/2:y=(oh-ih)/2:color=black", s.OutputWidth, s.OutputHeight),
		)
	case s.AspectMode == "zoom":
		filters = append(filters,
			fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=increase", s.OutputWidth, s.OutputHeight),
			fmt.Sprintf("crop=%d:%d", s.OutputWidth, s.OutputHeight),
		)
	default: // letterbox, or auto with no probed rect yet
		filters = append(filters,
			fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease", s.OutputWidth, s.OutputHeight),
			fmt.Sprintf("pad=w=%d:h=%d:x=(ow-iw)/2:y=(oh-ih)/2:color=black", s.OutputWidth, s.OutputHeight),
		)
	}

	// 4. Subtitle burn-in BEFORE interlacing, so captions composite onto the
	//    progressive raster (avoids subtitle rows splitting across fields).
	if s.SubtitleURL != "" {
		filters = append(filters,
			fmt.Sprintf("subtitles=filename='%s':si=%d", s.SubtitleURL, s.SubtitleIndex))
	}

	// 5. Build interlaced frame (720×480 at 29.97i) and then split into fields.
	scan := "tff"
	if s.FieldOrder == "bff" {
		scan = "bff"
	}
	filters = append(filters,
		fmt.Sprintf("interlace=scan=%s:lowpass=0", scan),
		// separatefields doubles the frame rate (29.97i → 59.94p) and halves
		// the height (480 → 240). Output frame 0 is the top field, frame 1
		// the bottom field, alternating.
		"separatefields",
	)

	return strings.Join(filters, ",")
}

// BuildCommand returns a ready-to-run exec.Cmd (minus pipe wiring, which
// the caller attaches via ExtraFiles). The filter chain always ends with
// `separatefields`, so the video pipe yields one 720×240 RGB24 field per
// read at 59.94 Hz.
//
// Seeking:
//   - Transcode path: the transcode URL encodes `offset=` server-side; we do
//     NOT pass `-ss`. Set UseSSSeek=false.
//   - Direct-play path: pass `-ss SeekSeconds` before `-i` so FFmpeg fast-seeks
//     the container. Set UseSSSeek=true, leave URL offset alone.
func BuildCommand(ctx context.Context, s PipelineSpec) *exec.Cmd {
	args := []string{
		"-loglevel", "warning",
		"-fflags", "+genpts",
	}
	if s.UseSSSeek && s.SeekSeconds > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", s.SeekSeconds))
	}
	// Input headers. FFmpeg's `-headers` takes a single string with all
	// headers concatenated; passing multiple `-headers` overwrites the
	// previous value. See https://ffmpeg.org/ffmpeg-protocols.html#http
	if len(s.InputHeaders) > 0 {
		var sb strings.Builder
		for k, v := range s.InputHeaders {
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(v)
			sb.WriteString("\r\n")
		}
		args = append(args, "-headers", sb.String())
	}
	args = append(args, "-i", s.InputURL)

	// Video output: raw rgb24 to pipe.
	args = append(args,
		"-map", "0:v:0",
		"-vf", buildFilterChain(s),
		"-pix_fmt", "rgb24",
		"-f", "rawvideo",
		s.VideoPipePath,
	)

	// Audio output: s16le stereo 48k PCM to pipe.
	args = append(args,
		"-map", "0:a:0",
		"-ar", fmt.Sprintf("%d", s.AudioSampleRate),
		"-ac", fmt.Sprintf("%d", s.AudioChannels),
		"-f", "s16le",
		s.AudioPipePath,
	)

	return exec.CommandContext(ctx, "ffmpeg", args...)
}
```

Imports: `context`, `os/exec`, plus `fmt` and `strings`.

- [ ] **Step 4-5: Verify tests pass, commit**

```bash
git commit -am "feat(ffmpeg): pipeline spec + filter-chain builder + command assembler"
```

---

### Task 5.3: Process supervisor

**Files:**
- Create: `internal/ffmpeg/process.go`
- Create: `internal/ffmpeg/process_test.go`

- [ ] **Step 1: Test with a fake ffmpeg (echo-based)**

Use `exec.LookPath` to skip if ffmpeg unavailable, or use `os/exec` with `cat` as a stand-in for simpler unit coverage.

- [ ] **Step 2: Implement**

```go
package ffmpeg

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"sync"
)

type Process struct {
	cmd       *exec.Cmd
	videoPipe io.ReadCloser
	audioPipe io.ReadCloser
	wg        sync.WaitGroup
	stopped   chan struct{}
}

func Spawn(ctx context.Context, spec PipelineSpec) (*Process, error) {
	// Use os.Pipe for video/audio to avoid filesystem named pipes on Windows.
	videoR, videoW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	audioR, audioW, err := os.Pipe()
	if err != nil {
		videoR.Close()
		videoW.Close()
		return nil, err
	}

	spec.VideoPipePath = "pipe:3"
	spec.AudioPipePath = "pipe:4"
	cmd := BuildCommand(ctx, spec)
	cmd.ExtraFiles = []*os.File{videoW, audioW} // fd 3 and 4
	cmd.Stderr = os.Stderr // ffmpeg logs

	if err := cmd.Start(); err != nil {
		videoR.Close(); videoW.Close()
		audioR.Close(); audioW.Close()
		return nil, err
	}

	// The write-ends of the pipes are in the child now; close our copies so
	// EOF propagates correctly on the read side.
	videoW.Close()
	audioW.Close()

	p := &Process{cmd: cmd, videoPipe: videoR, audioPipe: audioR, stopped: make(chan struct{})}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		if err := cmd.Wait(); err != nil {
			slog.Warn("ffmpeg exited", "err", err)
		}
		close(p.stopped)
	}()
	return p, nil
}

func (p *Process) VideoPipe() io.Reader { return p.videoPipe }
func (p *Process) AudioPipe() io.Reader { return p.audioPipe }

func (p *Process) Stop() {
	_ = p.cmd.Process.Kill()
	p.wg.Wait()
	p.videoPipe.Close()
	p.audioPipe.Close()
}

func (p *Process) Done() <-chan struct{} { return p.stopped }
```

Imports: `os`.

- [ ] **Step 3-4: Run, commit**

```bash
git commit -am "feat(ffmpeg): Process supervisor spawning with fd3/4 pipes"
```

---

### Task 5.4: Auto-crop probe (for AspectMode="auto")

**Why:** `cropdetect` is a metadata-only filter — it emits crop candidates via `av_log` but does not crop the video. A real auto-crop needs a short probe pass that runs `cropdetect` for ~2 s against the source, parses the logged `crop=W:H:X:Y` line, and caches the rect. The main pipeline then uses that rect as a fixed `crop=` filter (see `buildFilterChain` auto branch).

**Files:**
- Create: `internal/ffmpeg/cropprobe.go`
- Create: `internal/ffmpeg/cropprobe_test.go`

- [ ] **Step 1: Test with a synthetic letterboxed file**

```go
func TestProbeCrop_FindsLetterbox(t *testing.T) {
	// Generate a 2s letterboxed test clip (720x480 with 60px black bars top/bottom)
	// using ffmpeg itself as the source. Skip if ffmpeg unavailable.
	// ...
	rect, err := ProbeCrop(context.Background(), testSrcURL, nil, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rect == nil || rect.Y < 50 || rect.Y > 70 {
		t.Errorf("expected Y~60, got %+v", rect)
	}
}
```

- [ ] **Step 2: Implement**

```go
package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

var cropRegex = regexp.MustCompile(`crop=(\d+):(\d+):(\d+):(\d+)`)

// ProbeCrop runs a short cropdetect pass and returns the last-stable rect.
// Returns nil if no detection (e.g. content with no letterbox).
func ProbeCrop(ctx context.Context, inputURL string, headers map[string]string, duration time.Duration) (*CropRect, error) {
	probeCtx, cancel := context.WithTimeout(ctx, duration+5*time.Second)
	defer cancel()

	args := []string{"-loglevel", "info", "-t", fmt.Sprintf("%.1f", duration.Seconds())}
	if len(headers) > 0 {
		var sb strings.Builder
		for k, v := range headers {
			sb.WriteString(k + ": " + v + "\r\n")
		}
		args = append(args, "-headers", sb.String())
	}
	args = append(args,
		"-i", inputURL,
		"-vf", "cropdetect=limit=24:round=2:reset=0",
		"-f", "null", "-",
	)
	cmd := exec.CommandContext(probeCtx, "ffmpeg", args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var last *CropRect
	scan := bufio.NewScanner(stderr)
	for scan.Scan() {
		m := cropRegex.FindStringSubmatch(scan.Text())
		if m == nil {
			continue
		}
		w, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		x, _ := strconv.Atoi(m[3])
		y, _ := strconv.Atoi(m[4])
		last = &CropRect{W: w, H: h, X: x, Y: y}
	}
	_ = cmd.Wait() // ffmpeg exits cleanly when `-t` elapses
	return last, nil
}
```

- [ ] **Step 3-5: Commit**

```bash
git commit -am "feat(ffmpeg): cropdetect probe pass returning a locked CropRect"
```

---

### Task 5.5: Direct-play detection helper

Spec §9 risk 2: seek has two code paths. The control plane decides which based on whether the SessionRequest came with a direct media URL or with a transcode-universal URL. The adapter (Plex) knows which path applies at session-creation time and sets `SessionRequest.DirectPlay` accordingly (see core.types updates in Task 10.0).

**Files:**
- Modify: `internal/core/types.go` (add `DirectPlay bool` field)
- Modify: `internal/core/manager.go` (translate `DirectPlay` → `PipelineSpec.UseSSSeek`)
- Modify: `internal/adapters/plex/companion.go` (choose direct-play when PMS returns `directPlay=1`)

- [ ] **Step 1: Add field + translation**

```go
// In internal/core/types.go:
type SessionRequest struct {
	// ... existing fields ...
	DirectPlay bool // true → FFmpeg seeks via -ss; false → URL-encoded offset (transcode)
}

// In internal/core/manager.go StartSession:
spec.UseSSSeek = req.DirectPlay
spec.SeekSeconds = float64(req.SeekOffsetMs) / 1000.0
```

- [ ] **Step 2: Plex adapter side** — v1 forces transcode via profile extras, so `DirectPlay` is always `false` in practice. Still wire the field so the v2 direct-play path has a landing zone.

- [ ] **Step 3-5: Commit**

```bash
git commit -am "feat(core): DirectPlay flag selects FFmpeg -ss vs URL-encoded offset"
```

---

## Phase 6: Data Plane Orchestrator

### Task 6.1: Frame-timer clock

**Files:**
- Create: `internal/dataplane/clock.go`
- Create: `internal/dataplane/clock_test.go`

- [ ] **Step 1: Test tick cadence**

```go
package dataplane

import (
	"context"
	"testing"
	"time"
)

func TestClock_TicksAtExpectedRate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticks := make(chan time.Time, 256)
	go RunFieldTimer(ctx, 59.94, ticks)

	var count int
	deadline := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case <-ticks:
			count++
		case <-deadline:
			break loop
		}
	}
	// 500ms * 59.94 = 29.97 ticks expected; tolerate ±20%.
	if count < 24 || count > 36 {
		t.Errorf("expected ~30 ticks in 500ms, got %d", count)
	}
}
```

- [ ] **Step 2: Implement**

```go
package dataplane

import (
	"context"
	"time"
)

func RunFieldTimer(ctx context.Context, fieldsPerSec float64, out chan<- time.Time) {
	period := time.Duration(float64(time.Second) / fieldsPerSec)
	tick := time.NewTicker(period)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-tick.C:
			select {
			case out <- t:
			default:
				// Drop if consumer behind — deadline pressure is real.
			}
		}
	}
}
```

- [ ] **Step 3-5: Verify, commit**

```bash
git commit -am "feat(dataplane): 59.94 Hz field timer with backpressure drop"
```

---

### Task 6.2: Video pipe reader

Reads raw RGB888 from FFmpeg stdout pipe, emits one field per 720×240×3 bytes.

**Files:**
- Create: `internal/dataplane/videopipe.go`
- Create: `internal/dataplane/videopipe_test.go`

- [ ] **Step 1: Test**

```go
func TestVideoPipeReader_EmitsFields(t *testing.T) {
	fieldSize := 720 * 240 * 3
	buf := &bytes.Buffer{}
	// Write 3 fields of incrementing byte patterns.
	for i := 0; i < 3; i++ {
		field := make([]byte, fieldSize)
		for j := range field {
			field[j] = byte(i)
		}
		buf.Write(field)
	}
	ch := make(chan []byte, 4)
	go ReadFieldsFromPipe(buf, 720, 240, 3, ch)
	for i := 0; i < 3; i++ {
		select {
		case f := <-ch:
			if len(f) != fieldSize {
				t.Errorf("field %d size = %d", i, len(f))
			}
			if f[0] != byte(i) {
				t.Errorf("field %d first byte = %d", i, f[0])
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout on field %d", i)
		}
	}
}
```

- [ ] **Step 2: Implement**

```go
package dataplane

import (
	"io"
)

// ReadFieldsFromPipe reads fixed-size fields from r and sends each one on out.
// Closes out on EOF or error.
func ReadFieldsFromPipe(r io.Reader, width, height, bytesPerPixel int, out chan<- []byte) {
	defer close(out)
	size := width * height * bytesPerPixel
	for {
		buf := make([]byte, size)
		_, err := io.ReadFull(r, buf)
		if err != nil {
			return
		}
		out <- buf
	}
}
```

- [ ] **Step 3-5: Commit**

```bash
git commit -am "feat(dataplane): video pipe reader emitting raw RGB fields"
```

---

### Task 6.3: Audio pipe reader

Similar shape to 6.2 but emits PCM chunks sized to match the field cadence (so one audio packet per video field, roughly).

**Files:**
- Create: `internal/dataplane/audiopipe.go`
- Create: `internal/dataplane/audiopipe_test.go`

- [ ] **Step 1-5:** Same pattern — test with an `io.Reader` producing known bytes, assert chunk sizes. One chunk = `sampleRate * channels * 2 / 59.94` bytes ≈ `48000*2*2/60 = 3200` bytes per field. Commit.

```bash
git commit -am "feat(dataplane): audio pipe reader emitting per-field PCM chunks"
```

---

### Task 6.4: Data plane orchestrator (the Plane)

Glues together: FFmpeg process, video reader, audio reader, field timer, LZ4 compressor, sender. Runs one "session" until its context is cancelled.

**Files:**
- Create: `internal/dataplane/plane.go`
- Create: `internal/dataplane/plane_test.go`

- [ ] **Step 1: Define Plane struct and Run method**

```go
package dataplane

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

type PlaneConfig struct {
	Sender        *groovynet.Sender
	SpawnSpec     ffmpeg.PipelineSpec
	Modeline      groovy.Modeline // SWITCHRES modeline
	FieldWidth    int             // hActive
	FieldHeight   int             // per-field vActive (e.g. 240 for 480i)
	BytesPerPixel int
	RGBMode       byte   // groovy.RGBMode888 etc.
	LZ4Enabled    bool
	AudioRate     int    // Go-side integer (48000)
	AudioChans    int    // 2 for stereo
	SeekOffsetMs  int    // reported as session start position
}

// Plane streams one FFmpeg session to the MiSTer. One BLIT_FIELD_VSYNC per
// 59.94 Hz tick, audio gated on the latest ACK's audio-ready bit, and a
// playback-position atomic exposed via Position() for the timeline.
type Plane struct {
	cfg         PlaneConfig
	proc        *ffmpeg.Process
	positionMs  atomic.Int64
	audioReady  atomic.Bool
	fpgaFrame   atomic.Uint32
	done        chan struct{}
}

func NewPlane(cfg PlaneConfig) *Plane {
	return &Plane{cfg: cfg, done: make(chan struct{})}
}

// Position returns the current playback offset since start (never decreases
// unless Stop() is called). The timeline broadcaster queries this every second.
func (p *Plane) Position() time.Duration {
	return time.Duration(p.positionMs.Load()) * time.Millisecond
}

// Done returns a channel closed when Run exits (EOF, ctx cancel, or error).
func (p *Plane) Done() <-chan struct{} { return p.done }

func (p *Plane) Run(ctx context.Context) error {
	defer close(p.done)

	proc, err := ffmpeg.Spawn(ctx, p.cfg.SpawnSpec)
	if err != nil {
		return fmt.Errorf("ffmpeg spawn: %w", err)
	}
	p.proc = proc
	defer proc.Stop()

	// 1. INIT handshake (ACK-gated; 60ms timeout). This must happen BEFORE
	//    the Drainer goroutine starts reading from the socket.
	soundRate := rateCodeForHz(p.cfg.AudioRate)
	lz4Mode := groovy.LZ4ModeOff
	if p.cfg.LZ4Enabled {
		lz4Mode = groovy.LZ4ModeDefault
	}
	initPkt := groovy.BuildInit(lz4Mode, soundRate, byte(p.cfg.AudioChans), p.cfg.RGBMode)
	ack, err := p.cfg.Sender.SendInitAwaitACK(initPkt, 60*time.Millisecond)
	if err != nil {
		return fmt.Errorf("init handshake: %w", err)
	}
	p.audioReady.Store(ack.AudioReady())

	// 2. SWITCHRES (fire-and-forget).
	if err := p.cfg.Sender.Send(groovy.BuildSwitchres(p.cfg.Modeline)); err != nil {
		return fmt.Errorf("switchres: %w", err)
	}

	// 3. Start drainer for subsequent ACKs (frame echo, audio-ready updates).
	ackCh := make(chan groovy.ACK, 32)
	drainer := groovynet.NewDrainer(p.cfg.Sender, ackCh)
	go drainer.Run()

	// 4. Readers + timer.
	videoCh := make(chan []byte, 4)
	audioCh := make(chan []byte, 16)
	ticks := make(chan time.Time, 4)
	go ReadFieldsFromPipe(proc.VideoPipe(), p.cfg.FieldWidth, p.cfg.FieldHeight, p.cfg.BytesPerPixel, videoCh)
	go ReadAudioFromPipe(proc.AudioPipe(), p.cfg.AudioRate, p.cfg.AudioChans, audioCh)
	go RunFieldTimer(ctx, 59.94, ticks)

	// 5. Position bookkeeping — one tick = 1/59.94 s ≈ 16.683 ms.
	const fieldPeriodMs = int64(16) // integer approximation, good enough for timeline
	startMs := int64(p.cfg.SeekOffsetMs)
	p.positionMs.Store(startMs)

	var (
		frameNum uint32 // increments once per interlaced frame (every 2 fields)
		nextField uint8 // 0 or 1
	)

	for {
		select {
		case <-ctx.Done():
			_ = p.cfg.Sender.Send(groovy.BuildClose())
			return ctx.Err()
		case <-proc.Done():
			_ = p.cfg.Sender.Send(groovy.BuildClose())
			return nil
		case a := <-ackCh:
			p.audioReady.Store(a.AudioReady())
			p.fpgaFrame.Store(a.FPGAFrame)
		case <-ticks:
			// 1 field per tick. Frame number increments on the top-field
			// boundary (field==0).
			select {
			case field, ok := <-videoCh:
				if !ok {
					_ = p.cfg.Sender.Send(groovy.BuildClose())
					return nil
				}
				p.sendField(frameNum, nextField, field)
				if nextField == 1 {
					frameNum++
				}
				nextField ^= 1
			default:
				// Under-run — send a duplicate field to hold the raster.
				p.sendDuplicate(frameNum, nextField)
				if nextField == 1 {
					frameNum++
				}
				nextField ^= 1
			}
			// Audio: only send when fpga.audio bit is set AND we have PCM ready.
			if p.audioReady.Load() {
				select {
				case pcm, ok := <-audioCh:
					if ok && len(pcm) > 0 {
						p.sendAudio(pcm)
					}
				default:
				}
			}
			// Advance reported position by one field period.
			p.positionMs.Add(fieldPeriodMs)
		}
	}
}

func (p *Plane) sendField(frame uint32, field uint8, raw []byte) {
	opts := groovy.BlitOpts{Frame: frame, Field: field}
	payload := raw
	if p.cfg.LZ4Enabled {
		compressed, err := groovy.LZ4Compress(raw)
		if err != nil {
			slog.Warn("lz4 compress failed; sending raw", "err", err)
		} else {
			payload = compressed
			opts.Compressed = true
			opts.CompressedSize = uint32(len(compressed))
		}
	}
	p.cfg.Sender.WaitForCongestion()
	if err := p.cfg.Sender.Send(groovy.BuildBlitHeader(opts)); err != nil {
		slog.Warn("blit header send", "err", err)
		return
	}
	if err := p.cfg.Sender.SendPayload(payload); err != nil {
		slog.Warn("blit payload send", "err", err)
		return
	}
	p.cfg.Sender.MarkBlitSent(len(payload))
}

func (p *Plane) sendDuplicate(frame uint32, field uint8) {
	opts := groovy.BlitOpts{Frame: frame, Field: field, Duplicate: true}
	_ = p.cfg.Sender.Send(groovy.BuildBlitHeader(opts))
	p.cfg.Sender.MarkBlitSent(0) // no payload, no congestion hit
}

func (p *Plane) sendAudio(pcm []byte) {
	if len(pcm) > int(^uint16(0)) {
		pcm = pcm[:int(^uint16(0))] // soundSize is uint16 on the wire
	}
	if err := p.cfg.Sender.Send(groovy.BuildAudioHeader(uint16(len(pcm)))); err != nil {
		slog.Warn("audio header send", "err", err)
		return
	}
	if err := p.cfg.Sender.SendPayload(pcm); err != nil {
		slog.Warn("audio payload send", "err", err)
	}
}

func rateCodeForHz(hz int) byte {
	switch hz {
	case 22050:
		return groovy.AudioRate22050
	case 44100:
		return groovy.AudioRate44100
	case 48000:
		return groovy.AudioRate48000
	}
	return groovy.AudioRateOff
}
```

- [ ] **Step 2: Integration test using fake-mister**

```go
//go:build integration

func TestPlane_StreamsFieldsToFake(t *testing.T) {
	h := NewHarness(t)
	// Point plane at a short test video.
	plane := dataplane.NewPlane(dataplane.PlaneConfig{
		Sender: h.Sender,
		SpawnSpec: ffmpeg.PipelineSpec{
			InputURL: "testdata/5s.mp4",
			OutputWidth: 720, OutputHeight: 480,
			FieldOrder: "tff", AspectMode: "letterbox",
			AudioSampleRate: 48000, AudioChannels: 2,
			SourceProbe: &ffmpeg.ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976},
		},
		FieldWidth: 720, FieldHeight: 240, BytesPerPixel: 3,
		LZ4Enabled: true, AudioRate: 48000, AudioChans: 2,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	plane.Run(ctx)

	snap := h.Recorder.Snapshot()
	// 5 seconds × ~59.94 fields/sec = ~300 fields; tolerate ±10%.
	got := snap.Counts[groovy.CmdBlitFieldVSync]
	if got < 270 || got > 330 {
		t.Errorf("expected ~300 blits, got %d", got)
	}
}
```

Requires `tests/integration/testdata/5s.mp4` — a 5-second sample video. Check one in.

- [ ] **Step 3-5: Commit**

```bash
git commit -am "feat(dataplane): Plane orchestrator tying FFmpeg → Groovy sender"
```

---

### ✅ Phase 6 Review Checkpoint

**Report to user:**
- Full data-plane pipeline works end-to-end against fake-mister.
- A 5-second test video streams ~300 fields with audio.
- PNG dumps prove pixel content is right (spot-check visually).
- No Plex Companion yet — everything downstream of "here's a URL" works.

---

## Phase 7: Plex Adapter — Companion HTTP

> **Package location:** all files in this phase live under `internal/adapters/plex/`. The Go package name stays `plex`. The adapter depends on `internal/core/` (types + manager) and translates Plex Companion HTTP requests into `core.SessionRequest` / status queries.

### Task 7.1: HTTP server scaffolding and middleware

**Files:**
- Create: `internal/adapters/plex/companion.go`
- Create: `internal/adapters/plex/companion_test.go`

- [ ] **Step 1: Test that the server mounts routes**

```go
func TestCompanion_RootReturns200(t *testing.T) {
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "abc-123"})
	ts := httptest.NewServer(c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/resources")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Implement scaffolding**

```go
package plex

import (
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type CompanionConfig struct {
	DeviceName string
	DeviceUUID string
	Version    string
}

type Companion struct {
	cfg      CompanionConfig
	core     SessionManager // adapter-agnostic core.Manager
	timeline *TimelineBroker
}

// SessionManager is the adapter's narrow view of core.Manager. Declared as
// an interface here (rather than importing core.Manager concretely) to keep
// tests in this package mockable without spinning up a real core.
type SessionManager interface {
	StartSession(core.SessionRequest) error
	Pause() error
	Play() error
	Stop() error
	SeekTo(offsetMs int) error
	Status() core.SessionStatus
}

func NewCompanion(cfg CompanionConfig, core SessionManager) *Companion {
	return &Companion{cfg: cfg, core: core}
}

func (c *Companion) SetTimeline(t *TimelineBroker) { c.timeline = t }

func (c *Companion) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/resources", c.handleResources)
	mux.HandleFunc("/player/playback/playMedia", c.handlePlayMedia)
	mux.HandleFunc("/player/playback/pause", c.handlePause)
	mux.HandleFunc("/player/playback/play", c.handlePlay)
	mux.HandleFunc("/player/playback/stop", c.handleStop)
	mux.HandleFunc("/player/playback/seekTo", c.handleSeekTo)
	mux.HandleFunc("/player/playback/setParameters", c.handleSetParameters)
	mux.HandleFunc("/player/playback/setStreams", c.handleSetStreams)
	mux.HandleFunc("/player/timeline/subscribe", c.handleTimelineSubscribe)
	mux.HandleFunc("/player/timeline/unsubscribe", c.handleTimelineUnsubscribe)
	mux.HandleFunc("/player/timeline/poll", c.handleTimelinePoll)
	mux.HandleFunc("/player/mirror/details", c.handleMirrorDetails)
	return withHeaders(mux)
}

// withHeaders injects the X-Plex-* headers that identify us to controllers.
func withHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "X-Plex-Token, X-Plex-Client-Identifier, X-Plex-Device-Name, X-Plex-Product, X-Plex-Version, X-Plex-Platform, X-Plex-Platform-Version, X-Plex-Provides, X-Plex-Protocol, X-Plex-Target-Client-Identifier, Content-Type, Accept")
		w.Header().Set("Content-Type", "text/xml")
		h.ServeHTTP(w, r)
	})
}

// handleResources returns our advertised capabilities.
func (c *Companion) handleResources(w http.ResponseWriter, r *http.Request) {
	type MediaContainer struct {
		XMLName  xml.Name `xml:"MediaContainer"`
		Size     int      `xml:"size,attr"`
		Player   struct {
			Title                string `xml:"title,attr"`
			MachineIdentifier    string `xml:"machineIdentifier,attr"`
			ProtocolVersion      string `xml:"protocolVersion,attr"`
			ProtocolCapabilities string `xml:"protocolCapabilities,attr"`
			DeviceClass          string `xml:"deviceClass,attr"`
			Product              string `xml:"product,attr"`
			Platform             string `xml:"platform,attr"`
			PlatformVersion      string `xml:"platformVersion,attr"`
		} `xml:"Player"`
	}
	var mc MediaContainer
	mc.Size = 1
	mc.Player.Title = c.cfg.DeviceName
	mc.Player.MachineIdentifier = c.cfg.DeviceUUID
	mc.Player.ProtocolVersion = "1"
	mc.Player.ProtocolCapabilities = "timeline,playback,playqueues"
	mc.Player.DeviceClass = "stb"
	mc.Player.Product = "MiSTer_GroovyRelay"
	mc.Player.Platform = "Linux"
	mc.Player.PlatformVersion = c.cfg.Version
	w.WriteHeader(200)
	xml.NewEncoder(w).Encode(mc)
}

// Other handlers in later tasks.
func (c *Companion) handlePlayMedia(w http.ResponseWriter, r *http.Request)     { http.Error(w, "not implemented", 501) }
func (c *Companion) handlePause(w http.ResponseWriter, r *http.Request)          { http.Error(w, "not implemented", 501) }
func (c *Companion) handlePlay(w http.ResponseWriter, r *http.Request)           { http.Error(w, "not implemented", 501) }
func (c *Companion) handleStop(w http.ResponseWriter, r *http.Request)           { http.Error(w, "not implemented", 501) }
func (c *Companion) handleSeekTo(w http.ResponseWriter, r *http.Request)         { http.Error(w, "not implemented", 501) }
func (c *Companion) handleSetParameters(w http.ResponseWriter, r *http.Request)  { http.Error(w, "not implemented", 501) }
func (c *Companion) handleSetStreams(w http.ResponseWriter, r *http.Request)     { http.Error(w, "not implemented", 501) }
func (c *Companion) handleTimelineSubscribe(w http.ResponseWriter, r *http.Request)   { http.Error(w, "not implemented", 501) }
func (c *Companion) handleTimelineUnsubscribe(w http.ResponseWriter, r *http.Request) { http.Error(w, "not implemented", 501) }
func (c *Companion) handleTimelinePoll(w http.ResponseWriter, r *http.Request)        { http.Error(w, "not implemented", 501) }
func (c *Companion) handleMirrorDetails(w http.ResponseWriter, r *http.Request)       { http.Error(w, "not implemented", 501) }
```

- [ ] **Step 3-5: Verify and commit**

```bash
git commit -am "feat(plex): Companion HTTP scaffolding with /resources + route stubs"
```

---

### Task 7.2: Device capability profile

**Files:**
- Create: `internal/adapters/plex/profile.go`
- Create: `internal/adapters/plex/profile_test.go`

Reference: `docs/references/plex-mpv-shim.md` §"Device Capability Profile". We inject `X-Plex-Client-Profile-Extra` on stream URL requests to force transcode to 480p H.264. The profile name we advertise is configurable.

- [ ] **Step 1: Test**

```go
func TestProfileExtra_Forces480pH264(t *testing.T) {
	extra := BuildProfileExtra()
	if !strings.Contains(extra, "video-resolution-match=match(videoResolution,\"480\")") && !strings.Contains(extra, "resolution=720x480") {
		t.Error("profile extra should constrain resolution to 480")
	}
	if !strings.Contains(extra, "codec=h264") {
		t.Error("profile extra should force H.264")
	}
}
```

- [ ] **Step 2: Implement**

```go
package plex

// BuildProfileExtra returns the X-Plex-Client-Profile-Extra string that
// overrides the server-side profile lookup. Structured as semicolon-separated
// conditions. See docs/references/plex-mpv-shim.md for exact syntax.
func BuildProfileExtra() string {
	// This is a conservative profile that forces PMS to transcode everything
	// to H.264 Main@L3.1 at ≤720x480 progressive, AAC 2-channel.
	return "" +
		"add-transcode-target(type=videoProfile&protocol=http&container=mp4&videoCodec=h264&audioCodec=aac);" +
		"add-transcode-target-settings(type=videoProfile&context=streaming&CopyInternalSubs=true&BurnSubtitles=true);" +
		"add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.width&value=720&isRequired=true);" +
		"add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.height&value=480&isRequired=true);" +
		"add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.framerate&value=30&isRequired=true);" +
		"add-limitation(scope=audioCodec&scopeName=aac&type=upperBound&name=audio.channels&value=2)"
}

func BuildClientCapabilities() string {
	return "protocols=http-video,http-mp4-video,http-hls,http-streaming-video,http-streaming-video-720p;videoDecoders=h264{profile:baseline,main,high;resolution:480;level:41};audioDecoders=aac"
}
```

- [ ] **Step 3-5: Commit**

```bash
git commit -am "feat(plex): device capability profile extras (force 480p H.264 transcode)"
```

---

### Task 7.3: Transcode URL construction

**Files:**
- Create: `internal/adapters/plex/transcode.go`
- Create: `internal/adapters/plex/transcode_test.go`

Reference: `docs/references/plex-mpv-shim.md` §"Plex Companion HTTP Endpoints" and `docs/references/mister_plex.md` §"Media URL Construction".

- [ ] **Step 1: Test URL shape**

```go
func TestBuildTranscodeURL_ContainsExpectedParams(t *testing.T) {
	req := TranscodeRequest{
		PlexServerURL: "http://192.168.1.10:32400",
		MediaPath:     "/library/metadata/42",
		Token:         "xyz",
		OffsetMs:      0,
		OutputWidth:   720,
		OutputHeight:  480,
		ClientID:      "client-id-abc",
	}
	u := BuildTranscodeURL(req)
	for _, substr := range []string{
		"directPlay=0", "directStream=0", "copyts=1",
		"videoResolution=720x480", "X-Plex-Token=xyz",
	} {
		if !strings.Contains(u, substr) {
			t.Errorf("url missing %q: %s", substr, u)
		}
	}
}
```

- [ ] **Step 2-4: Implement**

```go
package plex

import (
	"fmt"
	"net/url"
)

type TranscodeRequest struct {
	PlexServerURL string
	MediaPath     string
	Token         string
	OffsetMs      int
	OutputWidth   int
	OutputHeight  int
	SessionID     string
	ClientID      string
	MaxBitrate    int
}

func BuildTranscodeURL(r TranscodeRequest) string {
	if r.MaxBitrate == 0 {
		r.MaxBitrate = 2000
	}
	q := url.Values{}
	q.Set("path", r.MediaPath)
	q.Set("mediaIndex", "0")
	q.Set("partIndex", "0")
	q.Set("protocol", "http")
	q.Set("fastSeek", "1")
	q.Set("directPlay", "0")
	q.Set("directStream", "0")
	q.Set("copyts", "1")
	q.Set("videoResolution", fmt.Sprintf("%dx%d", r.OutputWidth, r.OutputHeight))
	q.Set("maxVideoBitrate", fmt.Sprintf("%d", r.MaxBitrate))
	q.Set("offset", fmt.Sprintf("%d", r.OffsetMs/1000))
	q.Set("X-Plex-Session-Identifier", r.SessionID)
	q.Set("X-Plex-Client-Identifier", r.ClientID)
	q.Set("X-Plex-Client-Profile-Extra", BuildProfileExtra())
	q.Set("X-Plex-Token", r.Token)
	return r.PlexServerURL + "/video/:/transcode/universal/start.m3u8?" + q.Encode()
}
```

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(plex): transcode URL builder with force-transcode profile"
```

---

### Task 7.4: playMedia / pause / play / stop / seekTo handlers

Each handler parses query parameters, translates into a `core.SessionRequest` (for playMedia) or calls the corresponding core method (Pause/Play/Stop/SeekTo), returns a small XML response. Errors map to 4xx.

**Files:**
- Modify: `internal/adapters/plex/companion.go`
- Modify: `internal/adapters/plex/companion_test.go`

- [ ] **Step 1: Test playMedia happy path with a fake controller**

```go
type fakeCore struct {
	lastReq core.SessionRequest
	paused  bool
}

func (f *fakeCore) StartSession(r core.SessionRequest) error { f.lastReq = r; return nil }
func (f *fakeCore) Pause() error                              { f.paused = true; return nil }
func (f *fakeCore) Play() error                               { return nil }
func (f *fakeCore) Stop() error                               { return nil }
func (f *fakeCore) SeekTo(int) error                          { return nil }
func (f *fakeCore) Status() core.SessionStatus                { return core.SessionStatus{} }

func TestPlayMedia_ParsesFields(t *testing.T) {
	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer"}, fc)
	ts := httptest.NewServer(c.Handler())
	defer ts.Close()

	url := ts.URL + "/player/playback/playMedia?" +
		"address=192.168.1.10&port=32400&protocol=http&" +
		"key=%2Flibrary%2Fmetadata%2F42&offset=0&" +
		"X-Plex-Client-Identifier=client-1&X-Plex-Token=tok"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-Plex-Target-Client-Identifier", "our-uuid")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	// Adapter should have constructed a stream URL and called core.StartSession.
	if fc.lastReq.StreamURL == "" {
		t.Error("adapter did not construct a stream URL")
	}
	if fc.lastReq.AdapterRef != "/library/metadata/42" {
		t.Errorf("AdapterRef = %q, want /library/metadata/42", fc.lastReq.AdapterRef)
	}
	// The raw token is opaque to core but should be embedded in the stream URL.
	if !strings.Contains(fc.lastReq.StreamURL, "X-Plex-Token=tok") {
		t.Errorf("stream URL missing plex token: %s", fc.lastReq.StreamURL)
	}
}
```

- [ ] **Step 2: Define `PlayMediaRequest` and implement all five handlers**

Add to `companion.go`:

```go
type PlayMediaRequest struct {
	PlexServerAddress string
	PlexServerPort    string
	PlexServerScheme  string
	MediaKey          string
	OffsetMs          int
	SessionID         string
	ClientID          string
	PlexToken         string
	SubtitleStreamID  string
	AudioStreamID     string
	CommandID         string
}

// Status is reported via core.SessionStatus; the Plex adapter uses
// core.SessionStatus.AdapterRef to carry the media key back for timeline XML.

func (c *Companion) handlePlayMedia(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	p := PlayMediaRequest{
		PlexServerAddress: q.Get("address"),
		PlexServerPort:    q.Get("port"),
		PlexServerScheme:  q.Get("protocol"),
		MediaKey:          q.Get("key"),
		OffsetMs:          offset,
		SessionID:         q.Get("X-Plex-Session-Identifier"),
		ClientID:          q.Get("X-Plex-Client-Identifier"),
		PlexToken:         q.Get("X-Plex-Token"),
		SubtitleStreamID:  q.Get("subtitleStreamID"),
		AudioStreamID:     q.Get("audioStreamID"),
		CommandID:         q.Get("commandID"),
	}

	// Translate Plex Companion request → generic core.SessionRequest.
	serverURL := fmt.Sprintf("%s://%s:%s", p.PlexServerScheme, p.PlexServerAddress, p.PlexServerPort)
	streamURL := BuildTranscodeURL(TranscodeRequest{
		PlexServerURL: serverURL,
		MediaPath:     p.MediaKey,
		Token:         p.PlexToken,
		OffsetMs:      p.OffsetMs,
		OutputWidth:   720,
		OutputHeight:  480,
		SessionID:     p.SessionID,
		ClientID:      p.ClientID,
	})
	req := core.SessionRequest{
		StreamURL:    streamURL,
		SeekOffsetMs: p.OffsetMs,
		AdapterRef:   p.MediaKey,
		Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
	}
	if p.SubtitleStreamID != "" {
		req.SubtitleURL = subtitleURLFor(serverURL, p.SubtitleStreamID, p.PlexToken)
	}

	if err := c.core.StartSession(req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.rememberPlaySession(p) // adapter-local context for timeline
	writeOKResponse(w)
}

func (c *Companion) handlePause(w http.ResponseWriter, r *http.Request) {
	if err := c.core.Pause(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeOKResponse(w)
}

func (c *Companion) handlePlay(w http.ResponseWriter, r *http.Request) {
	if err := c.core.Play(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeOKResponse(w)
}

func (c *Companion) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := c.core.Stop(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeOKResponse(w)
}

func (c *Companion) handleSeekTo(w http.ResponseWriter, r *http.Request) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if err := c.core.SeekTo(offset); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeOKResponse(w)
}

// subtitleURLFor builds a PMS subtitle track URL for burn-in.
// Implemented as a real function in transcode.go (Task 7.4b); the declaration
// here is for reference. Signature takes the PMS base URL, the media key
// (Plex "ratingKey" path like "/library/metadata/42"), the subtitle stream
// ID from the playMedia query, and the X-Plex-Token.
//
//   func SubtitleURLFor(serverURL, mediaKey, streamID, token string) (string, error)

// rememberPlaySession stores adapter-local context (media key, client ID,
// etc) so the timeline broker can attribute status updates to the right
// Plex media entity. core does not know these Plex-specific details.
func (c *Companion) rememberPlaySession(p PlayMediaRequest) {
	// Implementation holds a mutex-protected pointer to the last PlayMediaRequest.
}

func writeOKResponse(w http.ResponseWriter) {
	w.WriteHeader(200)
	w.Write([]byte(`<?xml version="1.0"?><Response code="200" status="OK"/>`))
}
```

Imports: `strconv`.

- [ ] **Step 3-5: Verify, commit**

```bash
git commit -am "feat(plex): playMedia/pause/play/stop/seekTo handlers with session delegation"
```

---

### Task 7.4b: PMS subtitle-stream lookup (SubtitleURLFor)

Spec §2 requires subtitle burn-in for the `subtitleStreamID` Plex Companion sends with playMedia. The adapter queries PMS for the media's metadata, finds the Stream element matching the requested stream ID, and returns a URL FFmpeg can fetch with the X-Plex-Token.

**Files:**
- Modify: `internal/adapters/plex/transcode.go`
- Modify: `internal/adapters/plex/transcode_test.go`

- [ ] **Step 1: Test against a stubbed PMS metadata endpoint**

```go
func TestSubtitleURLFor_FindsMatchingStream(t *testing.T) {
	xmlBody := `<?xml version="1.0"?>
<MediaContainer>
	<Video ratingKey="42">
		<Media>
			<Part key="/library/parts/99/file.mkv">
				<Stream id="201" streamType="3" key="/library/streams/201" codec="srt"/>
				<Stream id="202" streamType="3" key="/library/streams/202" codec="subrip"/>
			</Part>
		</Media>
	</Video>
</MediaContainer>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/metadata/42" {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(xmlBody))
	}))
	defer ts.Close()

	url, err := SubtitleURLFor(ts.URL, "/library/metadata/42", "202", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(url, "/library/streams/202") {
		t.Errorf("got %q, want containing /library/streams/202", url)
	}
	if !strings.Contains(url, "X-Plex-Token=tok") {
		t.Error("subtitle URL must carry token for FFmpeg")
	}
}

func TestSubtitleURLFor_NoMatch(t *testing.T) {
	xmlBody := `<MediaContainer><Video><Media><Part/></Media></Video></MediaContainer>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(xmlBody))
	}))
	defer ts.Close()
	_, err := SubtitleURLFor(ts.URL, "/library/metadata/42", "999", "tok")
	if err == nil {
		t.Error("expected error for missing stream id")
	}
}
```

- [ ] **Step 2-4: Implement**

```go
package plex

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type pmsMediaContainer struct {
	Video []struct {
		Media []struct {
			Part []struct {
				Stream []struct {
					ID         string `xml:"id,attr"`
					StreamType string `xml:"streamType,attr"`
					Key        string `xml:"key,attr"`
				} `xml:"Stream"`
			} `xml:"Part"`
		} `xml:"Media"`
	} `xml:"Video"`
}

// SubtitleURLFor queries PMS metadata for mediaKey and returns a URL to the
// subtitle stream whose id matches streamID, token-appended so FFmpeg can
// fetch it directly.
//
// streamType=3 is the Plex convention for subtitle streams.
func SubtitleURLFor(serverURL, mediaKey, streamID, token string) (string, error) {
	u := fmt.Sprintf("%s%s?X-Plex-Token=%s", strings.TrimRight(serverURL, "/"),
		mediaKey, url.QueryEscape(token))
	resp, err := http.Get(u)
	if err != nil {
		return "", fmt.Errorf("metadata fetch: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var mc pmsMediaContainer
	if err := xml.Unmarshal(body, &mc); err != nil {
		return "", fmt.Errorf("parse metadata: %w", err)
	}
	for _, v := range mc.Video {
		for _, media := range v.Media {
			for _, part := range media.Part {
				for _, s := range part.Stream {
					if s.ID == streamID && s.Key != "" {
						return fmt.Sprintf("%s%s?X-Plex-Token=%s",
							strings.TrimRight(serverURL, "/"), s.Key,
							url.QueryEscape(token)), nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("subtitle stream %q not found under %s", streamID, mediaKey)
}
```

- [ ] **Step 5: Wire into handlePlayMedia**

Replace the previous stub call:

```go
if p.SubtitleStreamID != "" {
	subURL, err := SubtitleURLFor(serverURL, p.MediaKey, p.SubtitleStreamID, p.PlexToken)
	if err != nil {
		slog.Warn("subtitle lookup failed; continuing without burn-in",
			"streamID", p.SubtitleStreamID, "err", err)
	} else {
		req.SubtitleURL = subURL
	}
}
```

Commit:

```bash
git commit -am "feat(plex): SubtitleURLFor queries PMS metadata for burn-in target"
```

---

### Task 7.5: timeline/subscribe + timeline/poll + broadcaster

**Files:**
- Create: `internal/adapters/plex/timeline.go`
- Create: `internal/adapters/plex/timeline_test.go`

Reference: `docs/references/plex-mpv-shim.md` §"Timeline Subscribe Mechanics" and `docs/references/plexdlnaplayer.md`.

- [ ] **Step 1: Define TimelineBroker**

```go
package plex

import (
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// TimelineConfig carries the X-Plex-* identity headers the broker sends on
// every timeline push.
type TimelineConfig struct {
	DeviceUUID string
	DeviceName string
}

type subscriber struct {
	clientID  string
	host      string // already stripped of :port by handleTimelineSubscribe
	port      string
	commandID int
	protocol  string
	lastSeen  time.Time
}

type TimelineBroker struct {
	cfg         TimelineConfig
	mu          sync.Mutex
	subscribers map[string]*subscriber
	status      func() core.SessionStatus
	stop        chan struct{}
}

func NewTimelineBroker(cfg TimelineConfig, statusFn func() core.SessionStatus) *TimelineBroker {
	return &TimelineBroker{
		cfg:         cfg,
		subscribers: make(map[string]*subscriber),
		status:      statusFn,
		stop:        make(chan struct{}),
	}
}

func (t *TimelineBroker) RunBroadcastLoop() {
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-tick.C:
			t.broadcastOnce()
		}
	}
}

func (t *TimelineBroker) Stop() { close(t.stop) }

func (t *TimelineBroker) broadcastOnce() {
	t.mu.Lock()
	subs := make([]*subscriber, 0, len(t.subscribers))
	for id, s := range t.subscribers {
		if time.Since(s.lastSeen) > 90*time.Second {
			delete(t.subscribers, id)
			continue
		}
		subs = append(subs, s)
	}
	cfg := t.cfg
	t.mu.Unlock()

	st := t.status()
	xmlBody := t.buildTimelineXML(st)
	client := &http.Client{Timeout: 1 * time.Second}

	for _, s := range subs {
		protocol := s.protocol
		if protocol == "" {
			protocol = "http"
		}
		url := fmt.Sprintf("%s://%s:%s/:/timeline", protocol, s.host, s.port)
		req, err := http.NewRequestWithContext(context.Background(), "POST", url,
			strings.NewReader(xmlBody))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("X-Plex-Protocol", "1.0")
		req.Header.Set("X-Plex-Client-Identifier", cfg.DeviceUUID)
		req.Header.Set("X-Plex-Device-Name", cfg.DeviceName)
		req.Header.Set("X-Plex-Target-Client-Identifier", s.clientID)
		resp, err := client.Do(req)
		if err != nil {
			slog.Debug("timeline push failed", "sub", s.clientID, "err", err)
			continue
		}
		resp.Body.Close()
	}
}

func (t *TimelineBroker) buildTimelineXML(s core.SessionStatus) string {
	type Timeline struct {
		XMLName  xml.Name `xml:"Timeline"`
		Type     string   `xml:"type,attr"`
		State    string   `xml:"state,attr"`
		Time     int64    `xml:"time,attr"`
		Duration int64    `xml:"duration,attr"`
	}
	type MediaContainer struct {
		XMLName   xml.Name   `xml:"MediaContainer"`
		Timelines []Timeline `xml:"Timeline"`
	}
	// Map core.State → Plex timeline state strings.
	plexState := "stopped"
	switch s.State {
	case core.StatePlaying:
		plexState = "playing"
	case core.StatePaused:
		plexState = "paused"
	}
	mc := MediaContainer{
		Timelines: []Timeline{
			{Type: "music", State: "stopped"},
			{Type: "photo", State: "stopped"},
			{
				Type:     "video",
				State:    plexState,
				Time:     s.Position.Milliseconds(),
				Duration: s.Duration.Milliseconds(),
			},
		},
	}
	out, _ := xml.Marshal(mc)
	return string(out)
}

func (t *TimelineBroker) Subscribe(clientID, host, port, protocol string, commandID int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.subscribers[clientID] = &subscriber{
		clientID:  clientID,
		host:      host,
		port:      port,
		protocol:  protocol,
		commandID: commandID,
		lastSeen:  time.Now(),
	}
}

func (t *TimelineBroker) Unsubscribe(clientID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.subscribers, clientID)
}

func (t *TimelineBroker) TouchSubscriber(clientID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.subscribers[clientID]; ok {
		s.lastSeen = time.Now()
	}
}
```

- [ ] **Step 2: Wire handlers in companion.go**

```go
func (c *Companion) handleTimelineSubscribe(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// r.RemoteAddr is "ip:port" — strip the port. On IPv6 it's "[::1]:port".
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	c.timeline.Subscribe(
		q.Get("X-Plex-Client-Identifier"),
		host,
		q.Get("port"),
		q.Get("protocol"),
		atoiDefault(q.Get("commandID"), 0),
	)
	writeOKResponse(w)
}
func (c *Companion) handleTimelineUnsubscribe(w http.ResponseWriter, r *http.Request) {
	c.timeline.Unsubscribe(r.URL.Query().Get("X-Plex-Client-Identifier"))
	writeOKResponse(w)
}
func (c *Companion) handleTimelinePoll(w http.ResponseWriter, r *http.Request) {
	// Long-poll fallback; Plexamp uses this. Block up to N seconds waiting for
	// a state change, then return current timeline.
	st := c.core.Status()
	w.WriteHeader(200)
	w.Write([]byte(c.timeline.buildTimelineXML(st)))
}
func atoiDefault(s string, d int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return d
	}
	return n
}
```

- [ ] **Step 3-4: Test and verify**

- [ ] **Step 5: Commit**

```bash
git commit -am "feat(plex): timeline subscribe/poll/broadcast with 90s subscriber TTL"
```

---

## Phase 8: Plex Adapter — GDM Discovery

> All files in this phase live under `internal/adapters/plex/`. GDM is Plex-specific; future adapters will have their own discovery mechanisms (mDNS for AirPlay, SSDP for DLNA, proprietary for HDHomeRun, etc).

### Task 8.1: GDM multicast listener and M-SEARCH responder

**Files:**
- Create: `internal/adapters/plex/discovery.go`
- Create: `internal/adapters/plex/discovery_test.go`

Reference: `docs/references/plexdlnaplayer.md` — UDP 32412 with group `239.0.0.250`; send `HELLO * HTTP/1.0` on startup; reply `HTTP/1.0 200 OK` to `M-SEARCH`.

- [ ] **Step 1-5:** Implement and commit.

```go
package plex

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/ipv4" // if needed for multicast on some platforms
)

type DiscoveryConfig struct {
	DeviceName string
	DeviceUUID string
	HTTPPort   int
}

type Discovery struct {
	cfg DiscoveryConfig
	conn *net.UDPConn
}

func NewDiscovery(cfg DiscoveryConfig) (*Discovery, error) {
	group := &net.UDPAddr{IP: net.ParseIP("239.0.0.250"), Port: 32412}
	conn, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return nil, err
	}
	d := &Discovery{cfg: cfg, conn: conn}
	if err := d.sendHello(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *Discovery) sendHello() error {
	dst := &net.UDPAddr{IP: net.ParseIP("239.0.0.250"), Port: 32413}
	_, err := d.conn.WriteToUDP([]byte("HELLO * HTTP/1.0\r\n\r\n"), dst)
	return err
}

func (d *Discovery) Run() {
	buf := make([]byte, 4096)
	for {
		n, src, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req := string(buf[:n])
		if strings.HasPrefix(req, "M-SEARCH") {
			d.respondToMSearch(src)
		}
	}
}

func (d *Discovery) respondToMSearch(dst *net.UDPAddr) {
	body := fmt.Sprintf("HTTP/1.0 200 OK\r\n"+
		"Name: %s\r\n"+
		"Port: %d\r\n"+
		"Resource-Identifier: %s\r\n"+
		"Product: MiSTer_GroovyRelay\r\n"+
		"Version: 1.0\r\n"+
		"Content-Type: plex/media-player\r\n"+
		"Protocol-Capabilities: timeline,playback,playqueues\r\n"+
		"Device-Class: stb\r\n"+
		"Protocol-Version: 1\r\n\r\n",
		d.cfg.DeviceName, d.cfg.HTTPPort, d.cfg.DeviceUUID)
	d.conn.WriteToUDP([]byte(body), dst)
}

func (d *Discovery) Close() error { return d.conn.Close() }
```

Commit:

```bash
git commit -am "feat(plex): GDM multicast discovery with HELLO and M-SEARCH responder"
```

---

## Phase 9: Plex Adapter — plex.tv Account Linking

> All files live under `internal/adapters/plex/`. plex.tv account linking is Plex-specific; Jellyfin has its own API-key flow, and non-cast-target adapters (URL-input, IPTV) skip this stage entirely.

### Task 9.1: PIN request + polling

**Files:**
- Create: `internal/adapters/plex/linking.go`
- Create: `internal/adapters/plex/linking_test.go`

Reference: `docs/references/plexdlnaplayer.md` §"plex.tv link flow".

- [ ] **Step 1: Implement PIN request**

```go
package plex

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type PinResponse struct {
	ID        int    `json:"id"`
	Code      string `json:"code"`
	AuthToken string `json:"authToken"`
}

func RequestPIN(clientID, deviceName string) (*PinResponse, error) {
	form := url.Values{}
	form.Set("strong", "true")
	form.Set("X-Plex-Client-Identifier", clientID)
	form.Set("X-Plex-Device-Name", deviceName)
	form.Set("X-Plex-Product", "MiSTer_GroovyRelay")
	form.Set("X-Plex-Version", "1.0")

	req, _ := http.NewRequest("POST", "https://plex.tv/api/v2/pins", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("pin request failed: %d", resp.StatusCode)
	}
	var pr PinResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

func PollPIN(id int, clientID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET",
			fmt.Sprintf("https://plex.tv/api/v2/pins/%d?X-Plex-Client-Identifier=%s", id, clientID),
			nil)
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var pr PinResponse
		json.NewDecoder(resp.Body).Decode(&pr)
		resp.Body.Close()
		if pr.AuthToken != "" {
			return pr.AuthToken, nil
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("pin expired without auth token")
}
```

- [ ] **Step 2-5:** Tests (against a mock HTTP server), commit.

```bash
git commit -am "feat(plex): plex.tv PIN request and polling"
```

---

### Task 9.2: Device registration with refresh loop

**Files:**
- Modify: `internal/adapters/plex/linking.go`

- [ ] **Step 1-5:** Implement `RegisterDevice` that `PUT`s to `https://plex.tv/devices/{uuid}` with `Connection[][uri]=http://HOST_IP:HTTP_PORT`, and a goroutine that refreshes every 60s.

```go
func RegisterDevice(uuid, token, hostIP string, httpPort int) error {
	form := url.Values{}
	form.Set("Connection[][uri]", fmt.Sprintf("http://%s:%d", hostIP, httpPort))
	req, _ := http.NewRequest("PUT",
		fmt.Sprintf("https://plex.tv/devices/%s?X-Plex-Token=%s", uuid, token),
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func RunRegistrationLoop(ctx context.Context, uuid, token, hostIP string, httpPort int) {
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	// Fire once immediately.
	RegisterDevice(uuid, token, hostIP, httpPort)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := RegisterDevice(uuid, token, hostIP, httpPort); err != nil {
				slog.Warn("plex.tv register failed", "err", err)
			}
		}
	}
}
```

Commit:

```bash
git commit -am "feat(plex): plex.tv device registration with 60s refresh loop"
```

---

### Task 9.3: Token persistence

**Files:**
- Modify: `internal/adapters/plex/linking.go`
- Add new: `internal/adapters/plex/tokenstore.go`

- [ ] **Step 1: Test round-trip**

```go
func TestStoredData_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &StoredData{DeviceUUID: "abc-123", AuthToken: "secret"}
	if err := SaveStoredData(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadStoredData(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out.DeviceUUID != in.DeviceUUID || out.AuthToken != in.AuthToken {
		t.Errorf("round-trip mismatch: %+v vs %+v", out, in)
	}
}

func TestLoadStoredData_Missing(t *testing.T) {
	dir := t.TempDir()
	out, err := LoadStoredData(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Missing file returns zero-value struct, not error.
	if out.DeviceUUID != "" || out.AuthToken != "" {
		t.Errorf("expected zero-value, got %+v", out)
	}
}
```

- [ ] **Step 2: Implement**

```go
package plex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type StoredData struct {
	DeviceUUID string `json:"device_uuid"`
	AuthToken  string `json:"auth_token"`
}

const storedDataFilename = "data.json"

func LoadStoredData(dataDir string) (*StoredData, error) {
	path := filepath.Join(dataDir, storedDataFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &StoredData{}, nil
		}
		return nil, fmt.Errorf("read stored data: %w", err)
	}
	var sd StoredData
	if err := json.Unmarshal(data, &sd); err != nil {
		return nil, fmt.Errorf("parse stored data: %w", err)
	}
	return &sd, nil
}

func SaveStoredData(dataDir string, d *StoredData) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, storedDataFilename)
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal stored data: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write stored data: %w", err)
	}
	return nil
}
```

- [ ] **Step 3: Verify**

Run: `go test ./internal/adapters/plex/... -run TestStoredData -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git commit -am "feat(plex): JSON token persistence for plex.tv auth (0600 perms)"
```

---

### ✅ Phase 9 Review Checkpoint

**Report to user:**
- Bridge is discoverable on LAN via GDM.
- Bridge can be linked to a Plex account via PIN.
- Bridge registers itself with plex.tv every 60s, making it discoverable from Plex iOS / web / Plexamp out-of-LAN.

**Manual validation step:**
- Start the bridge.
- Open a Plex client. Look for "MiSTer" in the cast target list.
- Should appear within a few seconds both on LAN and (after linking) from a mobile data connection.

---

## Phase 10: Core — Session Orchestration (adapter-agnostic)

> All files in this phase live under `internal/core/`. The package name is `core`. This is the adapter-agnostic control-plane root. It imports no adapter packages. Future adapters (URL-input, Jellyfin, etc) will all call into `core.Manager.StartSession(core.SessionRequest)`.

### Task 10.0: Define core.SessionRequest and core.SessionStatus

**Files:**
- Create: `internal/core/types.go`
- Create: `internal/core/types_test.go`

- [ ] **Step 1: Define the generic types**

```go
package core

import "time"

// SessionRequest is the adapter-agnostic input to StartSession. Every adapter
// (Plex, and future: URL-input, Jellyfin, DLNA, ...) translates its
// protocol-specific request into one of these before calling the manager.
type SessionRequest struct {
	// StreamURL is a URL FFmpeg can consume (HLS manifest, direct file URL,
	// RTSP, etc). The adapter is responsible for constructing any
	// protocol-specific URL (e.g. Plex transcode URL with token).
	StreamURL string

	// InputHeaders are passed as FFmpeg -headers (e.g. Plex tokens).
	InputHeaders map[string]string

	// SeekOffsetMs is where to start playback (0 = beginning).
	SeekOffsetMs int

	// SubtitleURL is a URL to an external subtitle track to burn in.
	// Empty = no subtitles.
	SubtitleURL   string
	SubtitleIndex int

	// Capabilities describe what the adapter's control surface supports.
	// Used by the manager to decide whether Pause/Seek calls are valid.
	Capabilities Capabilities

	// AdapterRef is an opaque handle the adapter can use to correlate
	// status updates back to its own session context (e.g., a Plex media
	// key or a URL-input session ID). Never inspected by core.
	AdapterRef string
}

type Capabilities struct {
	CanSeek  bool
	CanPause bool
}

// SessionStatus is the adapter-agnostic view of what's currently playing.
// Adapters subscribe to this for their timeline reporting.
type SessionStatus struct {
	State      State
	Position   time.Duration
	Duration   time.Duration
	AdapterRef string
	StartedAt  time.Time
}
```

- [ ] **Step 2: Write tests** covering zero-value behavior and capability combinations.

- [ ] **Step 3: Commit**

```bash
git commit -am "feat(core): define SessionRequest and SessionStatus generic types"
```

---

### Task 10.1: Session state machine

**Files:**
- Create: `internal/core/state.go`
- Create: `internal/core/state_test.go`

- [ ] **Step 1: Test transitions**

```go
package core

import "testing"

func TestState_IdleToPlaying(t *testing.T) {
	s := New()
	if err := s.Transition(EvPlayMedia); err != nil {
		t.Fatal(err)
	}
	if s.State() != StatePlaying {
		t.Errorf("state = %s", s.State())
	}
}

func TestState_PlayingToPausedToPlaying(t *testing.T) {
	s := New()
	s.Transition(EvPlayMedia)
	s.Transition(EvPause)
	if s.State() != StatePaused {
		t.Errorf("state = %s after pause", s.State())
	}
	s.Transition(EvPlay)
	if s.State() != StatePlaying {
		t.Errorf("state = %s after play", s.State())
	}
}

func TestState_Stop(t *testing.T) {
	s := New()
	s.Transition(EvPlayMedia)
	s.Transition(EvStop)
	if s.State() != StateIdle {
		t.Errorf("state = %s", s.State())
	}
}

func TestState_PreemptFromPlaying(t *testing.T) {
	s := New()
	s.Transition(EvPlayMedia)
	// A second playMedia should succeed (preempt semantics).
	if err := s.Transition(EvPlayMedia); err != nil {
		t.Errorf("preempt playMedia failed: %v", err)
	}
	if s.State() != StatePlaying {
		t.Errorf("state after preempt = %s", s.State())
	}
}
```

- [ ] **Step 2: Implement**

```go
package core

import (
	"fmt"
	"sync"
)

type State string

const (
	StateIdle    State = "idle"
	StatePlaying State = "playing"
	StatePaused  State = "paused"
)

type Event string

const (
	EvPlayMedia Event = "playMedia"
	EvPause     Event = "pause"
	EvPlay      Event = "play"
	EvStop      Event = "stop"
	EvSeek      Event = "seek"
	EvEOF       Event = "eof"
)

type StateMachine struct {
	mu    sync.Mutex
	state State
}

func New() *StateMachine { return &StateMachine{state: StateIdle} }

func (s *StateMachine) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *StateMachine) Transition(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e {
	case EvPlayMedia:
		// Always allowed (preempt from any state).
		s.state = StatePlaying
	case EvPause:
		if s.state != StatePlaying {
			return fmt.Errorf("cannot pause from %s", s.state)
		}
		s.state = StatePaused
	case EvPlay:
		if s.state != StatePaused {
			return fmt.Errorf("cannot play from %s", s.state)
		}
		s.state = StatePlaying
	case EvStop, EvEOF:
		s.state = StateIdle
	case EvSeek:
		// Seek from playing or paused, stays in same state conceptually
		// (data plane is torn down and respawned — state doesn't change)
		if s.state == StateIdle {
			return fmt.Errorf("cannot seek from idle")
		}
	}
	return nil
}
```

- [ ] **Step 3-5: Commit**

```bash
git commit -am "feat(core): state machine with preempt-on-playMedia semantics"
```

---

### Task 10.2: Session manager (control plane root)

**Files:**
- Create: `internal/core/manager.go`
- Create: `internal/core/manager_test.go`

- [ ] **Step 1: Define Manager**

```go
package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

type Manager struct {
	cfg    *config.Config
	sender *groovynet.Sender
	fsm    *StateMachine

	mu       sync.Mutex
	cancelFn context.CancelFunc
	plane    *dataplane.Plane // nil when idle
	active   *activeSession
}

type activeSession struct {
	req            SessionRequest
	startedAt      time.Time
	baseOffsetMs   int       // offset the plane was spawned with
	pausedPosition time.Duration // snapshot from plane at Pause
	duration       time.Duration
}

func NewManager(cfg *config.Config, sender *groovynet.Sender) *Manager {
	return &Manager{cfg: cfg, sender: sender, fsm: New()}
}

// startPlaneLocked spawns a new data plane. Caller MUST hold m.mu. Preempts
// any existing plane and waits for its goroutine to exit before returning,
// ensuring the Sender is never shared between two planes.
func (m *Manager) startPlaneLocked(req SessionRequest, offsetMs int) error {
	// 1. Preempt and await prior plane.
	if m.cancelFn != nil {
		prev := m.plane
		m.cancelFn()
		m.cancelFn = nil
		if prev != nil {
			// Release the lock so the goroutine that calls Transition(EvEOF)
			// doesn't deadlock against us.
			m.mu.Unlock()
			<-prev.Done()
			m.mu.Lock()
		}
	}

	probeURL := req.StreamURL
	probe, err := ffmpeg.Probe(context.Background(), probeURL)
	if err != nil {
		return fmt.Errorf("probe source: %w", err)
	}

	// Resolve the SWITCHRES modeline from config (falls back to NTSC 480i60).
	modeline, err := resolveModeline(m.cfg.Modeline)
	if err != nil {
		return err
	}
	rgbMode, err := resolveRGBMode(m.cfg.RGBMode)
	if err != nil {
		return err
	}

	// Per-field vActive for interlaced modes; full height otherwise.
	fieldH := int(modeline.VActive)
	bpp := bytesPerPixel(rgbMode)

	// Optional auto-crop probe (runs once per session when aspect_mode=auto).
	var cropRect *ffmpeg.CropRect
	if m.cfg.AspectMode == "auto" {
		cropRect, _ = ffmpeg.ProbeCrop(context.Background(), probeURL, req.InputHeaders, 2*time.Second)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel

	spec := ffmpeg.PipelineSpec{
		InputURL:        req.StreamURL,
		InputHeaders:    req.InputHeaders,
		SeekSeconds:     float64(offsetMs) / 1000.0,
		UseSSSeek:       req.DirectPlay,
		SourceProbe:     probe,
		OutputWidth:     int(modeline.HActive),
		OutputHeight:    fieldH * 2,
		FieldOrder:      m.cfg.InterlaceFieldOrder,
		AspectMode:      m.cfg.AspectMode,
		CropRect:        cropRect,
		SubtitleURL:     req.SubtitleURL,
		SubtitleIndex:   req.SubtitleIndex,
		AudioSampleRate: m.cfg.AudioSampleRate,
		AudioChannels:   m.cfg.AudioChannels,
	}

	plane := dataplane.NewPlane(dataplane.PlaneConfig{
		Sender:        m.sender,
		SpawnSpec:     spec,
		Modeline:      modeline,
		FieldWidth:    int(modeline.HActive),
		FieldHeight:   fieldH,
		BytesPerPixel: bpp,
		RGBMode:       rgbMode,
		LZ4Enabled:    m.cfg.LZ4Enabled,
		AudioRate:     m.cfg.AudioSampleRate,
		AudioChans:    m.cfg.AudioChannels,
		SeekOffsetMs:  offsetMs,
	})
	m.plane = plane
	m.active = &activeSession{req: req, startedAt: time.Now(), baseOffsetMs: offsetMs}

	go func() {
		err := plane.Run(ctx)
		if err != nil && err != context.Canceled {
			slog.Warn("data plane exited", "err", err)
		}
		m.mu.Lock()
		// Only clear the plane pointer if it's still the one we spawned.
		if m.plane == plane {
			m.plane = nil
			m.fsm.Transition(EvEOF)
		}
		m.mu.Unlock()
	}()
	return nil
}

// StartSession is the adapter-agnostic entry point. Adapters translate their
// protocol-specific requests into a SessionRequest and call this. Any
// existing session is preempted and the prior goroutine awaited.
func (m *Manager) StartSession(req SessionRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startPlaneLocked(req, req.SeekOffsetMs); err != nil {
		return err
	}
	return m.fsm.Transition(EvPlayMedia)
}

func (m *Manager) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return fmt.Errorf("no session to pause")
	}
	if !m.active.req.Capabilities.CanPause {
		return fmt.Errorf("adapter does not support pause")
	}
	// Snapshot current plane position so Play() can resume from it.
	if m.plane != nil {
		m.active.pausedPosition = m.plane.Position()
	}
	if m.cancelFn != nil {
		prev := m.plane
		m.cancelFn()
		m.cancelFn = nil
		if prev != nil {
			m.mu.Unlock()
			<-prev.Done()
			m.mu.Lock()
		}
	}
	return m.fsm.Transition(EvPause)
}

func (m *Manager) Play() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.active
	if a == nil {
		return fmt.Errorf("no session to resume")
	}
	resumeMs := int(a.pausedPosition / time.Millisecond)
	if resumeMs <= 0 {
		resumeMs = a.baseOffsetMs
	}
	if err := m.startPlaneLocked(a.req, resumeMs); err != nil {
		return err
	}
	return m.fsm.Transition(EvPlay)
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancelFn != nil {
		prev := m.plane
		m.cancelFn()
		m.cancelFn = nil
		if prev != nil {
			m.mu.Unlock()
			<-prev.Done()
			m.mu.Lock()
		}
	}
	m.active = nil
	return m.fsm.Transition(EvStop)
}

func (m *Manager) SeekTo(offsetMs int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.active
	if a == nil {
		return fmt.Errorf("no session")
	}
	if !a.req.Capabilities.CanSeek {
		return fmt.Errorf("adapter does not support seek")
	}
	if err := m.startPlaneLocked(a.req, offsetMs); err != nil {
		return err
	}
	// Seek keeps state=playing; FSM's Seek event is a no-op transition.
	return nil
}

// Status returns the live session status, including the running plane's
// current playback position (for timeline).
func (m *Manager) Status() SessionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := SessionStatus{State: m.fsm.State()}
	if m.active != nil {
		st.AdapterRef = m.active.req.AdapterRef
		st.StartedAt = m.active.startedAt
		if m.plane != nil {
			st.Position = m.plane.Position()
		} else {
			st.Position = m.active.pausedPosition
		}
	}
	return st
}

// resolveModeline maps config's `modeline` string to a groovy.Modeline.
// v1 supports "NTSC_480i" only; future values extend this switch.
func resolveModeline(name string) (groovy.Modeline, error) {
	switch name {
	case "", "NTSC_480i":
		return groovy.NTSC480i60, nil
	}
	return groovy.Modeline{}, fmt.Errorf("unknown modeline %q (v1 supports NTSC_480i)", name)
}

func resolveRGBMode(name string) (byte, error) {
	switch name {
	case "", "rgb888":
		return groovy.RGBMode888, nil
	case "rgba8888":
		return groovy.RGBMode8888, nil
	case "rgb565":
		return groovy.RGBMode565, nil
	}
	return 0, fmt.Errorf("unknown rgb_mode %q", name)
}

func bytesPerPixel(rgbMode byte) int {
	switch rgbMode {
	case groovy.RGBMode8888:
		return 4
	case groovy.RGBMode565:
		return 2
	}
	return 3
}
```

**Adapter translation note:** In the Plex adapter, `handlePlayMedia` now does the Plex-specific work (parse query → build transcode URL via `plex.BuildTranscodeURL`) and then builds a `core.SessionRequest`:

```go
// Inside internal/adapters/plex/companion.go
func (c *Companion) handlePlayMedia(w http.ResponseWriter, r *http.Request) {
	p := parsePlayMedia(r)
	serverURL := fmt.Sprintf("%s://%s:%s", p.PlexServerScheme, p.PlexServerAddress, p.PlexServerPort)
	streamURL := BuildTranscodeURL(TranscodeRequest{
		PlexServerURL: serverURL, MediaPath: p.MediaKey,
		Token: p.PlexToken, OffsetMs: p.OffsetMs,
		OutputWidth: 720, OutputHeight: 480,
		SessionID: p.SessionID, ClientID: p.ClientID,
	})
	req := core.SessionRequest{
		StreamURL:    streamURL,
		SeekOffsetMs: p.OffsetMs,
		AdapterRef:   p.MediaKey,
		Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
	}
	if p.SubtitleStreamID != "" {
		req.SubtitleURL = subtitleURLFor(serverURL, p.SubtitleStreamID, p.PlexToken)
	}
	if err := c.core.StartSession(req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.rememberPlaySession(p) // adapter-local context for timeline reporting
	writeOKResponse(w)
}
```

The Plex adapter keeps its adapter-specific context (media key, client ID, subscribers) in its own state; core only sees the generic `SessionRequest`.

Imports: `log/slog`.

- [ ] **Step 2: Test**

Use a fake ffmpeg (maybe the fake-mister approach in reverse) to verify manager calls run end-to-end without errors. Integration-level coverage.

- [ ] **Step 3-5: Commit**

```bash
git commit -am "feat(core): adapter-agnostic Manager with SessionRequest + preempt"
```

---

## Phase 11: Main Binary Assembly

### Task 11.1: Wire everything in `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Replace placeholder**

```go
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/plex"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/logging"
)

func main() {
	cfgPath := flag.String("config", "/config/config.toml", "path to config.toml")
	logLevel := flag.String("log-level", "info", "debug|info|warn|error")
	linkFlag := flag.Bool("link", false, "run plex.tv PIN linking and exit")
	flag.Parse()

	slog.SetDefault(logging.New(*logLevel))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	// Load or create device UUID + auth token (stored per the Plex adapter's
	// token store; lives in the adapter package because v1 only has one
	// adapter that needs persistent auth).
	store, err := plex.LoadStoredData(cfg.DataDir)
	if err != nil || store.DeviceUUID == "" {
		store = &plex.StoredData{DeviceUUID: newUUID()}
		plex.SaveStoredData(cfg.DataDir, store)
	}
	cfg.DeviceUUID = store.DeviceUUID

	if *linkFlag {
		runLinkFlow(cfg, store)
		return
	}

	sender, err := groovynet.NewSender(cfg.MisterHost, cfg.MisterPort, cfg.SourcePort)
	if err != nil {
		slog.Error("sender init", "err", err)
		os.Exit(1)
	}
	defer sender.Close()

	// Core: adapter-agnostic session manager. Imports no adapters.
	coreMgr := core.NewManager(cfg, sender)

	// Plex adapter: wraps Companion HTTP + GDM discovery + plex.tv linking +
	// timeline broadcaster + HTTP server lifecycle. Takes core.Manager as its
	// session backend. Future adapters (URL-input, Jellyfin) plug in the same
	// way — see spec §4.5.
	plexAdapter, err := plex.NewAdapter(plex.AdapterConfig{
		Cfg:        cfg,
		Core:       coreMgr,
		TokenStore: store,
		HostIP:     outboundIP(),
	})
	if err != nil {
		slog.Error("plex adapter init", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := plexAdapter.Start(ctx); err != nil {
		slog.Error("plex adapter start", "err", err)
		os.Exit(1)
	}

	<-ctx.Done()
	slog.Info("shutting down")
	plexAdapter.Stop()
}

func newUUID() string {
	// crypto/rand-based UUID v4
	// ...implementation...
	return "stub"
}

func outboundIP() string {
	// best-effort: dial 8.8.8.8:53, read LocalAddr
	// ...implementation...
	return "0.0.0.0"
}

func runLinkFlow(cfg *config.Config, store *plex.StoredData) {
	// See Task 11.2. Calls into plex package PIN request + poll, saves token.
	// Kept here so the main binary exposes `--link` as a top-level verb.
}
```

> **Implementation note:** the consolidation of Companion + Discovery + Linking + TimelineBroker + HTTP server into one `plex.Adapter` struct is a task to do as part of this step. Add `internal/adapters/plex/adapter.go` with `AdapterConfig`, `NewAdapter`, `Start(ctx)`, `Stop()` methods. The adapter holds references to Companion, Discovery, TimelineBroker, and the HTTP server, and owns their lifecycles. This is the surface future adapters (URL-input, Jellyfin) will match conceptually — but do not extract an interface yet; see spec §4.5.

- [ ] **Step 2: Run** `go build ./...` — expect clean build.

- [ ] **Step 3: Commit**

```bash
git commit -am "feat: wire main binary with config + sender + session + plex + discovery"
```

---

### Task 11.2: Implement runLinkFlow

The `--link` flag is already declared and handled in Task 11.1's `main.go`. This task implements the `runLinkFlow` function that calls into the Plex adapter's PIN request / poll API and persists the token.

- [ ] **Step 1: Replace the runLinkFlow stub in `cmd/mister-groovy-relay/main.go`**

```go
func runLinkFlow(cfg *config.Config, store *plex.StoredData) {
	pin, err := plex.RequestPIN(cfg.DeviceUUID, cfg.DeviceName)
	if err != nil {
		slog.Error("pin request", "err", err)
		os.Exit(1)
	}
	fmt.Printf("Open https://plex.tv/link and enter this code: %s\n", pin.Code)
	token, err := plex.PollPIN(pin.ID, cfg.DeviceUUID, 5*time.Minute)
	if err != nil {
		slog.Error("pin poll", "err", err)
		os.Exit(1)
	}
	store.AuthToken = token
	if err := plex.SaveStoredData(cfg.DataDir, store); err != nil {
		slog.Error("save token", "err", err)
		os.Exit(1)
	}
	fmt.Println("Linked successfully.")
}
```

Don't forget to add `"fmt"` and `"time"` to the imports.

- [ ] **Step 2: Commit**

```bash
git commit -am "feat(cli): runLinkFlow wires --link to plex.tv PIN pairing"
```

---

### ✅ Phase 11 Review Checkpoint

**Report to user:**
- `mister-groovy-relay` builds and starts.
- Local discovery works (appears in Plex app on LAN).
- After `--link`, appears on Plex iOS / web out-of-LAN.
- Can receive a `playMedia` and stream to fake-mister.
- **Manual validation step:** run locally with `mister_host = "127.0.0.1"` and `fake-mister` listening on 32100; cast from Plex; observe PNG dumps from fake-mister.

---

## Phase 12: Integration Test Suite

### Task 12.1: Scripted scenarios

**Files:**
- Create: `tests/integration/scenarios_test.go`

- [ ] **Step 1: Write scenarios** covering:
- Cast a 10-second clip; assert ~600 fields received, audio byte count correct
- Seek forward 2 seconds mid-playback; assert data plane respawned with new offset (look for a second INIT in command sequence)
- Pause; assert no fields received during pause; resume; assert fields resume
- Preempt: cast clip A; mid-playback cast clip B; assert clip A ended, clip B started
- Stop: cast clip; stop mid-playback; assert CLOSE received

Each scenario uses the `Harness` from `helper_test.go` plus a local mock PMS (see 12.2).

- [ ] **Step 2: Mock PMS**

Create a minimal HTTP server that serves a known test media file at a known URL and issues redirects to `ffmpeg` via `m3u8`. Simpler: let ffmpeg read a local file directly and bypass the transcode URL for these tests (add a `DirectInputURL` field on `PlayMediaRequest` used only in tests).

- [ ] **Step 3-5: Commit**

```bash
git commit -am "test(integration): scripted cast/seek/pause/preempt/stop scenarios"
```

---

### Task 12.2: Timing and pixel-variance assertions

- [ ] **Step 1: Add assertions** to scenarios_test.go:

```go
func assertPixelVariance(t *testing.T, pngPath string) {
	t.Helper()
	f, err := os.Open(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	bounds := img.Bounds()
	var sum, sumSq uint64
	var n uint64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, _, _, _ := img.At(x, y).RGBA()
			sum += uint64(r >> 8)
			sumSq += uint64(r>>8) * uint64(r>>8)
			n++
		}
	}
	mean := float64(sum) / float64(n)
	variance := float64(sumSq)/float64(n) - mean*mean
	if variance < 100 {
		t.Errorf("pixel variance too low (%f) — image may be uniform black/solid", variance)
	}
}

func assertInterFieldTiming(t *testing.T, fieldTimestamps []time.Time) {
	t.Helper()
	var gaps []time.Duration
	for i := 1; i < len(fieldTimestamps); i++ {
		gaps = append(gaps, fieldTimestamps[i].Sub(fieldTimestamps[i-1]))
	}
	// Expected gap ~16.68ms (1/59.94).
	for i, g := range gaps {
		if g < 10*time.Millisecond || g > 30*time.Millisecond {
			t.Errorf("field %d gap = %v, expected ~17ms", i, g)
		}
	}
}
```

- [ ] **Step 2-5: Commit**

```bash
git commit -am "test(integration): pixel variance and inter-field timing assertions"
```

---

## Phase 13: Docker + CI

### Task 13.1: Dockerfile

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`

- [ ] **Step 1: Multi-stage build**

```dockerfile
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/mister-groovy-relay ./cmd/mister-groovy-relay

FROM alpine:3.20
RUN apk add --no-cache ffmpeg ca-certificates tzdata
COPY --from=build /out/mister-groovy-relay /usr/local/bin/mister-groovy-relay
COPY config.example.toml /config/config.example.toml
VOLUME /config
EXPOSE 32500/tcp
EXPOSE 32412/udp
ENTRYPOINT ["/usr/local/bin/mister-groovy-relay", "--config", "/config/config.toml"]
```

- [ ] **Step 2: .dockerignore**

```
.git
.github
tests
docs
/mister-groovy-relay
/fake-mister
*.test
```

- [ ] **Step 3: Build locally**

Run: `docker build -t mister-groovy-relay:dev .`
Expected: image built without errors.

- [ ] **Step 4: Commit**

```bash
git commit -am "feat(docker): multi-stage Dockerfile with ffmpeg runtime"
```

---

### Task 13.2: GitHub Actions CI

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write workflow**

```yaml
name: CI

on:
  push:
    branches: [main]
    tags: ['v*']
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22' }
      - name: Install ffmpeg
        run: sudo apt-get update && sudo apt-get install -y ffmpeg
      - run: go vet ./...
      - run: go test ./...
      - run: go test -tags=integration ./tests/integration/...

  build-image:
    runs-on: ubuntu-latest
    needs: test
    if: startsWith(github.ref, 'refs/tags/v')
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          username: ${{ secrets.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}
      - uses: docker/build-push-action@v5
        with:
          context: .
          push: true
          tags: |
            idiosync000/mister-groovy-relay:${{ github.ref_name }}
            idiosync000/mister-groovy-relay:latest
```

- [ ] **Step 2: Commit**

```bash
git commit -am "ci: GitHub Actions for test + integration + docker push on tag"
```

---

### ✅ Phase 13 Review Checkpoint

**Report to user:**
- `docker build` succeeds locally.
- CI runs unit + integration tests on every PR.
- On tagged release (e.g., `v0.1.0`), image pushes to Docker Hub.
- **User step:** configure `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` secrets in the GitHub repo settings.

---

## Phase 14: Documentation and First Manual End-to-End

### Task 14.1: README

**Files:**
- Create: `README.md`

Structure:
- What it is (one paragraph)
- Hardware requirements (MiSTer + 15 kHz CRT + PMS on LAN)
- Quick start: `docker run` with volume mount for config
- Configuration reference (table of TOML keys + defaults + meaning, pulled from config.example.toml)
- First-time setup walkthrough: install, configure, link, first cast
- Troubleshooting: discovery not working, no video on CRT, audio drift
- License (GPL-3)
- References to `docs/specs/`, `docs/references/`

- [ ] **Steps 1-2: Write and commit**

```bash
git commit -am "docs: README with quick start, config reference, troubleshooting"
```

---

### Task 14.2: First manual end-to-end validation

**This task is user-facing, not code.**

- [ ] Start the real MiSTer, load the Groovy_MiSTer core, connect the CRT.
- [ ] `docker run` the bridge on Unraid with `--network=host`. Configure `mister_host` to the MiSTer's IP.
- [ ] Run `--link` once, enter the PIN at `plex.tv/link`.
- [ ] Verify "MiSTer" appears in Plex's cast list on: Plex web (LAN) and Plex iOS (mobile data off-LAN).
- [ ] Cast a 24p movie. Validate: picture visible on CRT, motion smooth (no field-order shimmer), audio synced, letterboxing present.
- [ ] Cast a 4:3 TV episode. Validate: auto-crop correctly removes pillarboxing, image fills screen.
- [ ] Seek mid-playback. Validate: ~1-2s black, picture resumes at new position.
- [ ] Pause, wait 30s, resume. Validate: resumes at same position.
- [ ] Cast from Plex iOS on mobile data (off-LAN). Validate: discovery works, cast succeeds.
- [ ] Watch a 60+ minute movie. Validate: no visible A/V drift at end.

If field-order shimmer is observed, flip `interlace_field_order = "bff"` in config and restart.

---

### ✅ Final Review Checkpoint

**Report to user:**
- v1 complete per spec.
- All phase checkpoints passed.
- Manual validation on real hardware complete.
- Known residual items: PAL sources not handled (v2), per-content aspect override not supported (v2), mid-playback subtitle track change not supported (v2), Jellyfin not supported (v2).

---

## Plan Self-Review

Checked this plan against the spec at `docs/specs/2026-04-19-mister-groovy-relay-design.md` **after the 2026-04-19 audit pass** (see audit log in session history):

### Spec coverage
- ✅ All §2 in-scope items covered: Plex Companion (Phase 7), discovery (Phase 8), plex.tv linking (Phase 9), 480i output (Phases 2+5+6), subtitle burn-in (Task 7.4b + Task 5.2 filter chain), Docker (Phase 13), fake-MiSTer + integration tests (Phases 3+4+12).
- ✅ §4 architecture: control plane (Phases 7-10) and data plane (Phases 5-6) separated. `core.SessionRequest` / `core.SessionStatus` is the narrow interface.
- ✅ §4.1.1 / §4.1.2 core/adapter split preserved. Core imports no adapter packages; Plex adapter imports core.
- ✅ §4.5 adapter expansion: no universal `SourceAdapter` interface; future adapters plug into core directly.
- ✅ §5.2 FFmpeg one-process-two-streams via `ExtraFiles` fd 3/4 (Task 5.3).
- ✅ §5.2 filter chain now ends with `interlace → separatefields` producing 720×240 fields at 59.94 fps (Task 5.2, corrected from audit).
- ✅ §5.3 seek: transcode path (URL offset, no `-ss`) and direct-play path (`-ss` with `UseSSSeek=true`) both present (Task 5.5).
- ✅ §6 Groovy protocol: all commands built byte-for-byte against `docs/references/groovy_mister.md` and `docs/references/mistercast.md`. Command IDs, INIT layout (5 bytes, lz4/rate/chan/rgb), SWITCHRES cumulative-offset modeline, BLIT field-byte-at-[5] / vSync-at-[6:8], AUDIO 3-byte-header-plus-payload-stream all corrected.
- ✅ §6 INIT ACK handshake (60 ms timeout) gates startup (Task 4.1b). Drainer starts AFTER handshake.
- ✅ §6.1 push-driven clock: 59.94 Hz field timer (Task 6.1). Field byte alternates 0/1. Frame counter increments on top-field boundary.
- ✅ §6.1 audio-enable gating: Plane reads ACK bit 6 (`audioReady`) and only sends CMD_AUDIO when set.
- ✅ §6.2 stable source port: `NewSender` + `SO_REUSEADDR` + `IP_MTU_DISCOVER=PMTUDISC_DO` (Linux) for IP_DONTFRAGMENT semantics (Task 4.1).
- ✅ §7 config: every key in spec §7 loaded, validated, AND consumed downstream. `modeline` → `resolveModeline` → SWITCHRES; `rgb_mode` → `resolveRGBMode` → INIT byte[4] + per-pixel byte math.
- ✅ §8 three testing tiers: Phase 2 (unit — now with byte-for-byte fixtures), Phase 3-4 (fake MiSTer + first integration), Phase 12 (scenarios), Task 14.2 (manual e2e).
- ✅ §9 risk register: congestion back-off (Task 4.2), cropdetect as a **probe pass** that locks a real `crop=` filter (Task 5.4), plex.tv register refresh (Task 9.2), stable source port (Task 4.1).

### Cross-cutting design decisions (post-audit)
- **Preempt discipline:** `Manager.startPlaneLocked` awaits the prior plane goroutine before spawning the next, so the Sender (and its stable source port) is never shared between two planes. Pause snapshots `plane.Position()` so resume starts from the right offset.
- **Playback position channel:** `Plane.Position()` is an atomic int64 of milliseconds-played, incremented per tick. `Manager.Status()` reads it; `TimelineBroker.broadcastOnce` queries Status every second. This is the single control-plane ↔ data-plane signal plex-mpv-shim.md:142 flagged as critical.
- **Timeline push shape:** POST to `{protocol}://{host}:{port}/:/timeline` with XML body + `X-Plex-Protocol`, `X-Plex-Client-Identifier`, `X-Plex-Device-Name`, `X-Plex-Target-Client-Identifier` headers. `handleTimelineSubscribe` strips port from `r.RemoteAddr`.
- **Subtitle burn-in:** `SubtitleURLFor` queries PMS metadata, finds the stream matching `subtitleStreamID`, returns a token-appended URL for FFmpeg (Task 7.4b). Burn-in filter runs BEFORE interlacing (Task 5.2).

### Remaining accepted weaknesses
- `newUUID` and `outboundIP` stubs in Task 11.1. Implementer fills in `crypto/rand` UUID-v4 and a multi-NIC-aware outbound-IP helper; on Unraid the recommendation is to make `host_ip` a required config key and bypass auto-detection.
- Integration test media: Task 12.1 requires either a checked-in small test clip in `tests/integration/testdata/` or a generated solid-color+tone clip at test-setup time via FFmpeg.
- SWITCHRES per-field vertical porches for NTSC 480i (v1: 3/3/19 → vTotal 265) are derived from the canonical modeline but not verified against a working GroovyMAME pcap. Manual validation at Task 14.2 is the final gate; if the CRT shimmers, capture a pcap and adjust the preset.
- Multi-NIC Unraid host IP selection and Docker cgroup CPU throttling are flagged but not automated — documented in README as known operational concerns.

---

## Execution Handoff

Plan complete and saved to `docs/plans/2026-04-19-mister-groovy-relay-v1.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Good fit here given the plan size: 40+ tasks, each with focused scope, lets subagents run without losing coherence.

**2. Inline Execution** — Execute tasks in this session using `superpowers:executing-plans`, batch execution with checkpoints for review. Simpler but long session.

Which approach?
