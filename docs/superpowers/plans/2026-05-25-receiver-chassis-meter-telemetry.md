# Receiver Chassis Meter Telemetry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **Scope guard:** This plan implements only [Spec 5A](../specs/2026-05-24-receiver-chassis-meter-telemetry-design.md). Preserve the already-shipping `source` SSE event; add `meter` after `transport`.
> **Import hygiene:** When snippets show imports for an existing Go file, merge names into the existing import block and run `gofmt`; do not create a second `import` declaration.

**Goal:** Make the receiver chassis meter screen show real low-rate telemetry from core session facts, data-plane counters, and URL/Streams HLS buffer overlays, while leaving audio-analysis scopes quiet for Spec 5B.

**Architecture:** Core owns neutral session and pipeline facts on `StatusHomeView.Meter`; adapters optionally expose allowlisted HLS overlays through a structural `adapters.MeterOverlayProvider`; chassis owns formatting, sampling, histories, and the `meter` SSE payload from the existing snapshot cache. The single snapshot refresher remains the only path that samples core state, so additional browser tabs do not increase `StatusHomeView()` calls.

**Tech Stack:** Go 1.26 stdlib (`context`, `encoding/json`, `log/slog`, `math`, `net/http`, `strings`, `sync`, `time`), existing internal packages, vanilla browser JavaScript, existing `EventSource`.

**Spec:** [docs/superpowers/specs/2026-05-24-receiver-chassis-meter-telemetry-design.md](../specs/2026-05-24-receiver-chassis-meter-telemetry-design.md)

---

## File Structure

**New files:**

| Path | Responsibility |
|---|---|
| `internal/core/meter.go` | Core-owned meter view construction from probe/crop/modeline/bridge config facts. |
| `internal/adapters/meter.go` | `MeterOverlayProvider`, `MeterOverlay`, `HLSMeterOverlay`, and safe HLS stats allowlist mapper. |
| `internal/chassis/meter.go` | Chassis formatting, `meterSampler`, overlay discovery/recovery, and `meterEnvelope` conversion. |
| `internal/chassis/meter_test.go` | Meter formatter, sampler, overlay recovery, payload, and security tests. |
| `internal/chassis/static/meter.js` | Client updater for `data-meter-*` hooks, HLS lamps, throughput canvas, ACK canvas, and quiet audio scopes. |

**Files modified:**

| Path | Change |
|---|---|
| `internal/ffmpeg/probe.go` | Capture codec, channels, aspect ratios, and bitrate fields from ffprobe JSON. |
| `internal/ffmpeg/probe_test.go` | Parser tests for meter metadata and missing-value zero behavior. |
| `internal/core/types.go` | Add `MeterHomeView` and nested meter view structs to `StatusHomeView`. |
| `internal/core/manager.go` | Store stable meter facts on `activeSession`; copy runtime counters into `StatusHomeView.Meter.Runtime`. |
| `internal/core/manager_test.go` | Core meter idle/live/runtime/hot-swap tests. |
| `internal/hlsbuffer/config.go` | Export normalized config and make `Stats.Cached*` mean current cache occupancy. |
| `internal/hlsbuffer/session.go` | Track current cache occupancy after warm and refresh eviction. |
| `internal/hlsbuffer/session_test.go` | Current occupancy vs lifetime segment-download tests. |
| `internal/adapters/url/adapter.go` | Store the active URL HLS overlay handle under adapter lock. |
| `internal/adapters/url/play.go` | Install/clear overlay after successful core start; clear before URL state mutation and HLS close. |
| `internal/adapters/url/play_test.go` | URL overlay ownership, max-segment capture, cleanup order, and leak tests. |
| `internal/adapters/streams/adapter.go` | Store the active Streams HLS overlay handle under adapter lock. |
| `internal/adapters/streams/playback.go` | Install/clear overlay after successful core start; use core generation, not queue generation. |
| `internal/adapters/streams/playback_test.go` | Streams overlay ownership, generation, max-segment capture, cleanup order, and leak tests. |
| `internal/chassis/data.go` | Extend `MeterData` with raw numeric fields and make idle standard lamps both dim. |
| `internal/chassis/session.go` | Route snapshot building through one core status read plus meter sampling. |
| `internal/chassis/server.go` | Own one `meterSampler`; seed and refresh cache through `Server.buildSnapshot`. |
| `internal/chassis/events.go` | Emit initial `meter` from cache; emit live meter at sampler/structural boundaries. |
| `internal/chassis/events_test.go` | Initial order, cached burst, 2 Hz cadence, gating, panic recovery, and fan-out tests. |
| `internal/chassis/templates/meter.html` | Add explicit `data-meter-*` hooks and pending audio-scope markers. |
| `internal/chassis/templates/shell.html` | Serve `meter.js` after the shared SSE script path is available. |
| `internal/chassis/static/vfd-live.js` | Add `window.Chassis.events.subscribe(eventName, handler)` with reconnect dedupe and unsubscribe. |
| `internal/chassis/chassis_test.go` | Template/static asset contract tests for hooks, script order, and no demo generators. |
| `internal/chassis/import_check_test.go` | Extend forbidden import rules from the spec. |
| `cmd/mister-groovy-relay/main.go` | No production behavior change expected; chassis already receives the registry and session manager. Verify after compile. |

**Files intentionally unchanged:**

| Path | Reason |
|---|---|
| `internal/ui/*` | Spec 5A is additive under `/receiver/*`; `/ui/*` response shape stays unchanged. |
| `internal/playback/*` | Meter overlays are separate from playback controls. |
| `internal/chassis/static/transport.js` | Existing raw EventSource pattern remains until the follow-up migration. |
| `internal/chassis/static/visualizer-bank.js` | Existing raw EventSource pattern remains until the follow-up migration. |

---

## Task 1: ffprobe Metadata For Meter Facts

**Files:**
- Modify: `internal/ffmpeg/probe.go`
- Modify: `internal/ffmpeg/probe_test.go`

- [ ] **Step 1: Write the failing parser test**

Append this test to `internal/ffmpeg/probe_test.go`:

```go
func TestParseProbeOutput_MeterMetadata(t *testing.T) {
	raw := []byte(`{
		"streams": [
			{
				"codec_type": "video",
				"codec_name": "h264",
				"width": 720,
				"height": 480,
				"field_order": "tt",
				"r_frame_rate": "30000/1001",
				"sample_aspect_ratio": "8:9",
				"display_aspect_ratio": "4:3",
				"bit_rate": "1800000"
			},
			{
				"codec_type": "audio",
				"codec_name": "aac",
				"sample_rate": "48000",
				"channels": 2,
				"bit_rate": "128000"
			}
		],
		"format": {
			"duration": "60.25",
			"bit_rate": "2100000"
		}
	}`)
	got, err := parseProbeOutput(raw)
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if got.VideoCodec != "h264" || got.AudioCodec != "aac" {
		t.Fatalf("codecs = video %q audio %q", got.VideoCodec, got.AudioCodec)
	}
	if got.AudioChannels != 2 {
		t.Fatalf("AudioChannels = %d, want 2", got.AudioChannels)
	}
	if got.SampleAspectRatioNum != 8 || got.SampleAspectRatioDen != 9 {
		t.Fatalf("sample aspect = %d:%d, want 8:9", got.SampleAspectRatioNum, got.SampleAspectRatioDen)
	}
	if got.DisplayAspectRatioNum != 4 || got.DisplayAspectRatioDen != 3 {
		t.Fatalf("display aspect = %d:%d, want 4:3", got.DisplayAspectRatioNum, got.DisplayAspectRatioDen)
	}
	if got.VideoBitrateBPS != 1800000 || got.AudioBitrateBPS != 128000 || got.FormatBitrateBPS != 2100000 {
		t.Fatalf("bitrates = video %d audio %d format %d", got.VideoBitrateBPS, got.AudioBitrateBPS, got.FormatBitrateBPS)
	}
}

func TestParseProbeOutput_MissingMeterMetadataStaysZero(t *testing.T) {
	raw := []byte(`{"streams":[{"codec_type":"video","width":640,"height":360}],"format":{}}`)
	got, err := parseProbeOutput(raw)
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if got.VideoCodec != "" || got.AudioCodec != "" || got.AudioChannels != 0 {
		t.Fatalf("unexpected non-zero metadata: %+v", got)
	}
	if got.SampleAspectRatioNum != 0 || got.DisplayAspectRatioNum != 0 {
		t.Fatalf("unexpected aspect metadata: %+v", got)
	}
	if got.VideoBitrateBPS != 0 || got.AudioBitrateBPS != 0 || got.FormatBitrateBPS != 0 {
		t.Fatalf("unexpected bitrate metadata: %+v", got)
	}
}
```

- [ ] **Step 2: Run the focused failing tests**

Run:

```bash
go test ./internal/ffmpeg -run 'TestParseProbeOutput_MeterMetadata|TestParseProbeOutput_MissingMeterMetadataStaysZero'
```

Expected: FAIL with missing fields on `ProbeResult`.

- [ ] **Step 3: Extend `ProbeResult` and parser**

Modify `internal/ffmpeg/probe.go`:

```go
type ProbeResult struct {
	Width                 int
	Height                int
	FrameRate             float64
	Interlaced            bool
	AudioRate             int
	Duration              float64
	VideoCodec            string
	AudioCodec            string
	AudioChannels         int
	SampleAspectRatioNum  int
	SampleAspectRatioDen  int
	DisplayAspectRatioNum int
	DisplayAspectRatioDen int
	VideoBitrateBPS       int64
	AudioBitrateBPS       int64
	FormatBitrateBPS      int64
}

type ffprobeOutput struct {
	Streams []struct {
		CodecType          string `json:"codec_type"`
		CodecName          string `json:"codec_name"`
		Width              int    `json:"width"`
		Height             int    `json:"height"`
		FieldOrder         string `json:"field_order"`
		RFrameRate         string `json:"r_frame_rate"`
		SampleRate         string `json:"sample_rate"`
		Channels           int    `json:"channels"`
		SampleAspectRatio  string `json:"sample_aspect_ratio"`
		DisplayAspectRatio string `json:"display_aspect_ratio"`
		BitRate            string `json:"bit_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

func parseProbeOutput(raw []byte) (*ProbeResult, error) {
	var p ffprobeOutput
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse ffprobe: %w", err)
	}
	r := &ProbeResult{}
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			if r.Width == 0 {
				r.Width = s.Width
				r.Height = s.Height
				r.FrameRate = parseFrameRate(s.RFrameRate)
				r.Interlaced = s.FieldOrder == "tt" || s.FieldOrder == "bb" ||
					s.FieldOrder == "tb" || s.FieldOrder == "bt"
				r.VideoCodec = s.CodecName
				r.SampleAspectRatioNum, r.SampleAspectRatioDen = parseAspectRatio(s.SampleAspectRatio)
				r.DisplayAspectRatioNum, r.DisplayAspectRatioDen = parseAspectRatio(s.DisplayAspectRatio)
				r.VideoBitrateBPS = parseInt64(s.BitRate)
			}
		case "audio":
			if r.AudioRate == 0 {
				fmt.Sscan(s.SampleRate, &r.AudioRate)
				r.AudioCodec = s.CodecName
				r.AudioChannels = s.Channels
				r.AudioBitrateBPS = parseInt64(s.BitRate)
			}
		}
	}
	fmt.Sscan(p.Format.Duration, &r.Duration)
	r.FormatBitrateBPS = parseInt64(p.Format.BitRate)
	return r, nil
}

func parseAspectRatio(s string) (int, int) {
	var n, d int
	if _, err := fmt.Sscanf(s, "%d:%d", &n, &d); err == nil && n > 0 && d > 0 {
		return n, d
	}
	return 0, 0
}

func parseInt64(s string) int64 {
	var v int64
	if _, err := fmt.Sscan(s, &v); err != nil || v < 0 {
		return 0
	}
	return v
}
```

- [ ] **Step 4: Run ffmpeg tests**

Run:

```bash
go test ./internal/ffmpeg
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ffmpeg/probe.go internal/ffmpeg/probe_test.go
git commit -m "feat(ffmpeg): expose probe metadata for receiver meter"
```

---

## Task 2: Core Meter View And Stable Session Facts

**Files:**
- Create: `internal/core/meter.go`
- Modify: `internal/core/types.go`
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`
- Modify: `internal/core/types_test.go`

- [ ] **Step 1: Write failing core status tests**

Append these focused tests and the meter-specific fake plane to `internal/core/manager_test.go`.

```go
func TestManager_StatusHomeViewIdleHasZeroMeter(t *testing.T) {
	m := newTestManager(t)
	got := m.StatusHomeView()
	if got.Meter != (MeterHomeView{}) {
		t.Fatalf("idle Meter = %+v, want zero value", got.Meter)
	}
}

func TestManager_StatusHomeViewIncludesMeterFacts(t *testing.T) {
	m := newTestManager(t)
	m.bridge.Video.Modeline = "PAL_576i"
	m.bridge.Video.InterlaceFieldOrder = "tff"
	m.bridge.Video.LZ4Enabled = true
	m.bridge.Video.DeltaLZ4Enabled = true
	m.bridge.Audio.SampleRate = 48000
	m.bridge.Audio.Channels = 2
	m.bridge.Audio.OutputVolume = 77

	probe := &ffmpeg.ProbeResult{
		Width:                 720,
		Height:                480,
		FrameRate:             29.97,
		Interlaced:            true,
		AudioRate:             48000,
		VideoCodec:            "h264",
		AudioCodec:            "aac",
		AudioChannels:         2,
		SampleAspectRatioNum:  8,
		SampleAspectRatioDen:  9,
		DisplayAspectRatioNum: 4,
		DisplayAspectRatioDen: 3,
		VideoBitrateBPS:       1800000,
		AudioBitrateBPS:       128000,
		FormatBitrateBPS:      2100000,
	}
	crop := &ffmpeg.CropRect{W: 704, H: 480, X: 8, Y: 0}
	req := SessionRequest{
		StreamURL:   "http://example.test/live.m3u8",
		AdapterRef: "url:test",
		Source:     "url",
		Title:      "Meter Test",
		DirectPlay: true,
	}

	oldNewPlane := newPlane
	newPlane = func(dataplane.PlaneConfig) planeRunner {
		return &fakePlane{}
	}
	t.Cleanup(func() { newPlane = oldNewPlane })

	m.mu.Lock()
	err := m.startPlaneLocked(req, 0, probe, crop, "ffmpeg", 42, sessionGuard{}, false)
	m.mu.Unlock()
	if err != nil {
		t.Fatalf("startPlaneLocked: %v", err)
	}
	got := m.StatusHomeView().Meter
	if got.Source.Width != 720 || got.Source.Height != 480 || got.Source.VideoCodec != "h264" {
		t.Fatalf("source meter = %+v", got.Source)
	}
	if got.Crop.Mode != "letterbox" || !got.Crop.Detected || got.Crop.W != 704 {
		t.Fatalf("crop meter = %+v", got.Crop)
	}
	if got.Pipeline.ModelineName != "PAL_576i" || got.Pipeline.Standard != "pal" {
		t.Fatalf("pipeline modeline/standard = %+v", got.Pipeline)
	}
	if !got.Pipeline.InterlacedOutput || got.Pipeline.FieldOrder != "tff" {
		t.Fatalf("pipeline interlace = %+v", got.Pipeline)
	}
	if got.Pipeline.AudioSampleRate != 48000 || got.Pipeline.AudioChannels != 2 || got.Pipeline.AudioOutputVolume != 77 {
		t.Fatalf("pipeline audio = %+v", got.Pipeline)
	}
}

func TestManager_StatusHomeViewMeterRuntimeAndHotSwapFacts(t *testing.T) {
	m := newTestManager(t)
	m.bridge.Video.InterlaceFieldOrder = "bff"
	m.bridge.Audio.OutputVolume = 44
	m.active = &activeSession{
		req:        SessionRequest{Title: "Runtime", AdapterRef: "url:runtime", Source: "url"},
		generation: 7,
		meter: MeterHomeView{
			Pipeline: PipelineMeterView{FieldOrder: "tff", AudioOutputVolume: 20},
		},
	}
	m.plane = &meterCountingPlane{
		blitsTotal:  120,
		framesTotal: 60,
		underruns:   3,
		wireBytes:   9000000,
		lastACKAge:  4 * time.Millisecond,
	}
	got := m.StatusHomeView().Meter
	if got.Pipeline.FieldOrder != "bff" {
		t.Fatalf("hot-swapped field order = %q, want bff", got.Pipeline.FieldOrder)
	}
	if got.Pipeline.AudioOutputVolume != 44 {
		t.Fatalf("hot-swapped volume = %d, want 44", got.Pipeline.AudioOutputVolume)
	}
	if got.Runtime.BlitsTotal != 120 || got.Runtime.Underruns != 3 || got.Runtime.WireBytes != 9000000 {
		t.Fatalf("runtime counters = %+v", got.Runtime)
	}
	if got.Runtime.LastACKAge != 4*time.Millisecond || got.Runtime.Generation != 7 {
		t.Fatalf("runtime ack/generation = %+v", got.Runtime)
	}
}

type meterCountingPlane struct {
	fakePlane
	blitsTotal  uint64
	framesTotal uint64
	underruns   uint64
	wireBytes   uint64
	lastACKAge  time.Duration
}

func (p *meterCountingPlane) BlitsTotal() uint64        { return p.blitsTotal }
func (p *meterCountingPlane) FramesTotal() uint64       { return p.framesTotal }
func (p *meterCountingPlane) Underruns() uint64         { return p.underruns }
func (p *meterCountingPlane) WireBytes() uint64         { return p.wireBytes }
func (p *meterCountingPlane) LastACKAge() time.Duration { return p.lastACKAge }
```

- [ ] **Step 2: Run the focused failing core tests**

Run:

```bash
go test ./internal/core -run 'TestManager_StatusHomeView.*Meter'
```

Expected: FAIL with missing `MeterHomeView` types and missing `StatusHomeView.Meter`.

- [ ] **Step 3: Add core meter types**

Add these types to `internal/core/types.go` near `StatusHomeView`:

```go
type MeterHomeView struct {
	Source   SourceMeterView
	Crop     CropMeterView
	Pipeline PipelineMeterView
	Runtime  RuntimeMeterView
}

type SourceMeterView struct {
	Width                 int
	Height                int
	FrameRate             float64
	Interlaced            bool
	SampleAspectRatioNum  int
	SampleAspectRatioDen  int
	DisplayAspectRatioNum int
	DisplayAspectRatioDen int
	VideoCodec            string
	AudioCodec            string
	AudioRate             int
	AudioChannels         int
	VideoBitrateBPS       int64
	AudioBitrateBPS       int64
	FormatBitrateBPS      int64
}

type CropMeterView struct {
	Mode     string
	Detected bool
	W        int
	H        int
	X        int
	Y        int
}

type PipelineMeterView struct {
	ModelineName        string
	OutputWidth         int
	OutputHeight        int
	FieldHeight         int
	FieldRateHz         float64
	HorizontalKHz       float64
	InterlacedOutput    bool
	Standard            string
	FieldOrder          string
	RGBMode             string
	LZ4Enabled          bool
	DeltaLZ4Enabled     bool
	AudioSampleRate     int
	AudioChannels       int
	AudioOutputVolume   int
	EffectiveAspectMode string
}

type RuntimeMeterView struct {
	BlitsTotal  uint64
	FramesTotal uint64
	Underruns   uint64
	WireBytes   uint64
	LastACKAge  time.Duration
	StartedAt   time.Time
	Generation  uint64
}
```

Add `Meter MeterHomeView` to `StatusHomeView`.

- [ ] **Step 4: Build meter facts in core**

Create `internal/core/meter.go`:

```go
package core

import (
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func buildMeterHomeView(req SessionRequest, probe *ffmpeg.ProbeResult, crop *ffmpeg.CropRect, aspectMode string, preset ModelinePreset, modeline groovy.Modeline, fieldHeight int, rgbMode string, audioRate int, audioChans int, bridge config.BridgeConfig) MeterHomeView {
	var source SourceMeterView
	if probe != nil {
		source = SourceMeterView{
			Width:                 probe.Width,
			Height:                probe.Height,
			FrameRate:             probe.FrameRate,
			Interlaced:            probe.Interlaced,
			SampleAspectRatioNum:  probe.SampleAspectRatioNum,
			SampleAspectRatioDen:  probe.SampleAspectRatioDen,
			DisplayAspectRatioNum: probe.DisplayAspectRatioNum,
			DisplayAspectRatioDen: probe.DisplayAspectRatioDen,
			VideoCodec:            probe.VideoCodec,
			AudioCodec:            probe.AudioCodec,
			AudioRate:             probe.AudioRate,
			AudioChannels:         probe.AudioChannels,
			VideoBitrateBPS:       probe.VideoBitrateBPS,
			AudioBitrateBPS:       probe.AudioBitrateBPS,
			FormatBitrateBPS:      probe.FormatBitrateBPS,
		}
	}

	var cropView CropMeterView
	cropView.Mode = aspectMode
	if crop != nil {
		cropView.Detected = true
		cropView.W = crop.W
		cropView.H = crop.H
		cropView.X = crop.X
		cropView.Y = crop.Y
	}

	return MeterHomeView{
		Source: source,
		Crop:   cropView,
		Pipeline: PipelineMeterView{
			ModelineName:        preset.Name,
			OutputWidth:         int(modeline.HActive),
			OutputHeight:        int(modeline.VActive),
			FieldHeight:         fieldHeight,
			FieldRateHz:         modeline.FieldRate(),
			HorizontalKHz:       horizontalKHz(modeline),
			InterlacedOutput:    modeline.Interlaced(),
			Standard:            standardForModeline(preset.Name),
			FieldOrder:          bridge.Video.InterlaceFieldOrder,
			RGBMode:             rgbMode,
			LZ4Enabled:          bridge.Video.LZ4Enabled,
			DeltaLZ4Enabled:     bridge.Video.DeltaLZ4Enabled,
			AudioSampleRate:     audioRate,
			AudioChannels:       audioChans,
			AudioOutputVolume:   bridge.Audio.OutputVolume,
			EffectiveAspectMode: aspectMode,
		},
	}
}

func horizontalKHz(m groovy.Modeline) float64 {
	if m.HTotal == 0 {
		return 0
	}
	return m.PClock * 1000 / float64(m.HTotal)
}

func standardForModeline(name string) string {
	switch {
	case strings.HasPrefix(name, "NTSC_"):
		return "ntsc"
	case strings.HasPrefix(name, "PAL_"):
		return "pal"
	default:
		return ""
	}
}
```

Modify `internal/core/manager.go`:

```go
type activeSession struct {
	req            SessionRequest
	startedAt      time.Time
	generation     uint64
	baseOffsetMs   int
	pausedPosition time.Duration
	duration       time.Duration
	meter          MeterHomeView
}
```

Inside `startPlaneLocked`, immediately before assigning `m.active`, build the meter:

```go
meter := buildMeterHomeView(req, probe, cropRect, aspectMode, preset, modeline, fieldH, rgbMode, audioRate, audioChans, m.bridge)
m.active = &activeSession{
	req:          req,
	startedAt:    time.Now(),
	generation:   generation,
	baseOffsetMs: offsetMs,
	duration:     visualizerDuration(req, probe),
	meter:        meter,
}
```

In `StatusHomeView`, copy stable facts and runtime facts:

```go
if m.active != nil {
	view.Meter = m.active.meter
	view.Meter.Pipeline.FieldOrder = m.bridge.Video.InterlaceFieldOrder
	view.Meter.Pipeline.AudioOutputVolume = m.bridge.Audio.OutputVolume
	view.Meter.Runtime.StartedAt = m.active.startedAt
	view.Meter.Runtime.Generation = m.active.generation
}
if m.plane != nil {
	view.Meter.Runtime.BlitsTotal = m.plane.BlitsTotal()
	view.Meter.Runtime.FramesTotal = m.plane.FramesTotal()
	view.Meter.Runtime.Underruns = m.plane.Underruns()
	view.Meter.Runtime.WireBytes = m.plane.WireBytes()
	view.Meter.Runtime.LastACKAge = m.plane.LastACKAge()
}
```

- [ ] **Step 5: Run core tests**

Run:

```bash
go test ./internal/core
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/core/types.go internal/core/meter.go internal/core/manager.go internal/core/manager_test.go internal/core/types_test.go
git commit -m "feat(core): expose receiver meter session facts"
```

---

## Task 3: HLS Buffer Current Occupancy Stats

**Files:**
- Modify: `internal/hlsbuffer/config.go`
- Modify: `internal/hlsbuffer/session.go`
- Modify: `internal/hlsbuffer/session_test.go`

- [ ] **Step 1: Write failing occupancy tests**

Append to `internal/hlsbuffer/session_test.go`:

```go
func TestSessionStats_CachedFieldsRepresentCurrentOccupancy(t *testing.T) {
	stats := &sessionStats{}
	stats.addSegment(2*time.Second, 100)
	stats.addSegment(3*time.Second, 200)
	stats.setCurrent([]cachedSegment{
		{segment: Segment{Duration: 3 * time.Second, Sequence: 2}, name: "segment-000002.ts", size: 200},
	}, 200)

	got := stats.snapshot()
	if got.CachedSegments != 1 {
		t.Fatalf("CachedSegments = %d, want current occupancy 1", got.CachedSegments)
	}
	if got.CachedMediaDuration != 3*time.Second {
		t.Fatalf("CachedMediaDuration = %s, want 3s", got.CachedMediaDuration)
	}
	if got.CacheBytes != 200 {
		t.Fatalf("CacheBytes = %d, want current bytes 200", got.CacheBytes)
	}
	if got.SegmentDownloadsTotal != 2 {
		t.Fatalf("SegmentDownloadsTotal = %d, want lifetime downloads 2", got.SegmentDownloadsTotal)
	}
}

func TestNormalizeConfig_ExportsSessionDefaults(t *testing.T) {
	got := NormalizeConfig(Config{})
	if got.MaxCachedSegments != 6 {
		t.Fatalf("MaxCachedSegments = %d, want 6", got.MaxCachedSegments)
	}
	if got.MaxCacheBytes <= 0 || got.MaxSegmentBytes <= 0 {
		t.Fatalf("expected byte limits to be defaulted: %+v", got)
	}
}
```

- [ ] **Step 2: Run the focused failing tests**

Run:

```bash
go test ./internal/hlsbuffer -run 'TestSessionStats_CachedFieldsRepresentCurrentOccupancy|TestNormalizeConfig_ExportsSessionDefaults'
```

Expected: FAIL because `setCurrent` and `NormalizeConfig` are missing and stats still use lifetime cached fields.

- [ ] **Step 3: Export normalized config and split current vs lifetime stats**

In `OpenSession`, replace the existing config normalization line with:

```go
cfg := NormalizeConfig(opts.Config)
```

Add the exported wrapper near `normalizeSessionConfig`:

```go
func NormalizeConfig(c Config) Config {
	return normalizeSessionConfig(c)
}
```

Update `sessionStats` in `internal/hlsbuffer/session.go`:

```go
type sessionStats struct {
	mu                    sync.Mutex
	currentSegments       int
	currentMediaDuration  time.Duration
	currentCacheBytes     int64
	playlistReloadsTotal  int64
	segmentDownloadsTotal int64
	selectedVariant       Variant
	failureReason         string
}

func (s *sessionStats) addSegment(time.Duration, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.segmentDownloadsTotal++
}

func (s *sessionStats) setCurrent(cached []cachedSegment, bytes int64) {
	var duration time.Duration
	for _, item := range cached {
		duration += item.segment.Duration
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentSegments = len(cached)
	s.currentMediaDuration = duration
	s.currentCacheBytes = bytes
}

func (s *sessionStats) snapshot() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		CachedSegments:        s.currentSegments,
		CachedMediaDuration:   s.currentMediaDuration,
		CacheBytes:            s.currentCacheBytes,
		PlaylistReloadsTotal:  s.playlistReloadsTotal,
		SegmentDownloadsTotal: s.segmentDownloadsTotal,
		SelectedVariant:       s.selectedVariant,
		FailureReason:         s.failureReason,
	}
}
```

After initial warm in `OpenSession`, set current occupancy:

```go
for _, item := range cached {
	stats.addSegment(item.segment.Duration, item.size)
}
stats.setCurrent(cached, cache.TotalBytes())
```

After refresh pruning in `refreshPlaylist`, set current occupancy:

```go
for _, item := range cached {
	state.cachedBySeq[item.segment.Sequence] = item
	state.stats.addSegment(item.segment.Duration, item.size)
}
pruneCachedByEntries(state.cachedBySeq, state.cache.Entries())
current := cachedWindow(state.cachedBySeq)
state.stats.setCurrent(current, state.cache.TotalBytes())
```

- [ ] **Step 4: Run hlsbuffer tests**

Run:

```bash
go test ./internal/hlsbuffer
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/hlsbuffer/config.go internal/hlsbuffer/session.go internal/hlsbuffer/session_test.go
git commit -m "fix(hlsbuffer): report current cache occupancy"
```

---

## Task 4: Adapter Meter Overlay Interface And Safe Mapper

**Files:**
- Create: `internal/adapters/meter.go`
- Create or modify: `internal/adapters/meter_test.go`
- Modify: `internal/chassis/import_check_test.go`

- [ ] **Step 1: Write failing adapter meter tests**

Create `internal/adapters/meter_test.go`:

```go
package adapters

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
)

func TestHLSMeterOverlayFromStats_AllowsOnlySafeFields(t *testing.T) {
	stats := hlsbuffer.Stats{
		CachedSegments:        4,
		CachedMediaDuration:   6 * time.Second,
		CacheBytes:            1234,
		PlaylistReloadsTotal:  9,
		SegmentDownloadsTotal: 10,
		SelectedVariant: hlsbuffer.Variant{
			URI:       "live.m3u8?token=secret",
			Width:     1280,
			Height:    720,
			Bandwidth: 2200000,
			Codecs:    "avc1.secret",
		},
		FailureReason: "fetch /live.m3u8?token=secret failed",
	}
	got := HLSMeterOverlayFromStats(stats, 12)
	if got.CachedSegments != 4 || got.MaxCachedSegments != 12 || got.CacheBytes != 1234 {
		t.Fatalf("cache fields = %+v", got)
	}
	if got.CachedMediaDurationMS != 6000 {
		t.Fatalf("CachedMediaDurationMS = %d, want 6000", got.CachedMediaDurationMS)
	}
	if got.SelectedVariantWidth != 1280 || got.SelectedVariantHeight != 720 || got.SelectedVariantBPS != 2200000 {
		t.Fatalf("variant fields = %+v", got)
	}
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leak := range []string{"http://", "https://", "://", "/live.m3u8", "token=", "secret", "Authorization", "avc1"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("serialized overlay leaked %q: %s", leak, body)
		}
	}
}

func TestHLSMeterOverlayFromStats_AllowsShortFailureEnums(t *testing.T) {
	got := HLSMeterOverlayFromStats(hlsbuffer.Stats{FailureReason: "playlist-timeout"}, 6)
	if got.FailureReason != "playlist-timeout" {
		t.Fatalf("FailureReason = %q, want playlist-timeout", got.FailureReason)
	}
}
```

- [ ] **Step 2: Run the focused failing tests**

Run:

```bash
go test ./internal/adapters -run 'TestHLSMeterOverlayFromStats'
```

Expected: FAIL because the mapper and overlay types are missing.

- [ ] **Step 3: Add the interface and safe mapper**

Create `internal/adapters/meter.go`:

```go
package adapters

import (
	"context"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
)

type MeterOverlayProvider interface {
	MeterOverlay(ctx context.Context, snap core.StatusHomeView) (MeterOverlay, bool)
}

type MeterOverlay struct {
	HLS *HLSMeterOverlay
}

type HLSMeterOverlay struct {
	CachedSegments        int
	MaxCachedSegments     int
	CachedMediaDurationMS int
	CacheBytes            int64
	PlaylistReloadsTotal  int64
	SegmentDownloadsTotal int64
	SelectedVariantWidth  int
	SelectedVariantHeight int
	SelectedVariantBPS    int64
	FailureReason         string
}

func HLSMeterOverlayFromStats(stats hlsbuffer.Stats, maxCachedSegments int) *HLSMeterOverlay {
	return &HLSMeterOverlay{
		CachedSegments:        stats.CachedSegments,
		MaxCachedSegments:     maxCachedSegments,
		CachedMediaDurationMS: int(stats.CachedMediaDuration.Milliseconds()),
		CacheBytes:            stats.CacheBytes,
		PlaylistReloadsTotal:  stats.PlaylistReloadsTotal,
		SegmentDownloadsTotal: stats.SegmentDownloadsTotal,
		SelectedVariantWidth:  stats.SelectedVariant.Width,
		SelectedVariantHeight: stats.SelectedVariant.Height,
		SelectedVariantBPS:    int64(stats.SelectedVariant.Bandwidth),
		FailureReason:         sanitizeHLSFailureReason(stats.FailureReason),
	}
}

func sanitizeHLSFailureReason(raw string) string {
	reason := strings.TrimSpace(raw)
	if reason == "" {
		return ""
	}
	lower := strings.ToLower(reason)
	blocked := []string{"://", "/", "\\", "token=", "sig=", "secret", "authorization", "cookie", ".m3u8", ".ts", ".m4s"}
	for _, needle := range blocked {
		if strings.Contains(lower, needle) {
			return "hls-error"
		}
	}
	if len(reason) > 64 {
		return "hls-error"
	}
	return reason
}
```

Update `internal/chassis/import_check_test.go` so production `internal/core` cannot import adapters/chassis/ui/uiserver, and production `internal/adapters` cannot import chassis/ui/uiserver:

```go
{
	fromPkg: modulePath + "/internal/core",
	fromDir: filepath.Join(repoRoot, "internal", "core"),
	forbidden: []string{
		modulePath + "/internal/adapters",
		modulePath + "/internal/chassis",
		modulePath + "/internal/playback",
		modulePath + "/internal/ui",
		modulePath + "/internal/uiserver",
	},
},
{
	fromPkg: modulePath + "/internal/adapters",
	fromDir: filepath.Join(repoRoot, "internal", "adapters"),
	forbidden: []string{
		modulePath + "/internal/chassis",
		modulePath + "/internal/playback",
		modulePath + "/internal/ui",
		modulePath + "/internal/uiserver",
	},
},
```

- [ ] **Step 4: Run adapter and import tests**

Run:

```bash
go test ./internal/adapters ./internal/chassis -run 'TestHLSMeterOverlayFromStats|TestProductionImports_NoCrossPackageCoupling'
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/meter.go internal/adapters/meter_test.go internal/chassis/import_check_test.go
git commit -m "feat(adapters): add receiver meter overlay contract"
```

---

## Task 5: URL HLS Meter Overlay

**Files:**
- Modify: `internal/adapters/url/adapter.go`
- Modify: `internal/adapters/url/play.go`
- Modify: `internal/adapters/url/play_test.go`

- [ ] **Step 1: Write failing URL overlay tests**

Add tests to `internal/adapters/url/play_test.go`:

```go
func TestURLMeterOverlayExposesHLSStatsForOwningSession(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	a.bridge.HLSBuffer.Enabled = true
	a.bridge.HLSBuffer.MaxCachedSegments = 8

	var openedMaxSegments int
	stats := hlsbuffer.Stats{
		CachedSegments:        3,
		CachedMediaDuration:   4500 * time.Millisecond,
		CacheBytes:            2048,
		PlaylistReloadsTotal:  2,
		SegmentDownloadsTotal: 3,
		SelectedVariant:       hlsbuffer.Variant{URI: "live.m3u8?token=secret", Width: 720, Height: 480, Bandwidth: 1500000, Codecs: "avc1.secret"},
		FailureReason:         "fetch /live.m3u8?token=secret failed",
	}
	a.hlsBufferOpen = func(ctx context.Context, opts hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		openedMaxSegments = opts.Config.MaxCachedSegments
		return &hlsbuffer.Session{
			PlaybackPath: filepath.Join(t.TempDir(), "playlist.m3u8"),
			Policy:       core.MediaInputPolicy{ProtocolWhitelist: []string{"file"}},
			Stats:        func() hlsbuffer.Stats { return stats },
			Close:        func() error { return nil },
		}, nil
	}
	fc.statusFn = func() core.SessionStatus {
		req := fc.snapshot()
		return core.SessionStatus{State: core.StatePlaying, AdapterRef: req.AdapterRef, Generation: 11}
	}

	ref, _, status, err := a.castURLWithHLSBuffer(context.Background(), "https://example.test/live.m3u8", "direct", "auto")
	if err != nil || status != http.StatusOK {
		t.Fatalf("castURLWithHLSBuffer status=%d err=%v", status, err)
	}
	if openedMaxSegments != 8 {
		t.Fatalf("opened MaxCachedSegments = %d, want normalized configured value 8", openedMaxSegments)
	}
	a.mu.Lock()
	a.bridge.HLSBuffer.MaxCachedSegments = 99
	a.mu.Unlock()

	snap := core.StatusHomeView{State: core.StatePlaying, AdapterRef: ref, Source: "url", Generation: 11}
	if snap.AdapterRef != ref {
		t.Fatalf("core AdapterRef = %q, want %q", snap.AdapterRef, ref)
	}
	overlay, ok := a.MeterOverlay(context.Background(), snap)
	if !ok || overlay.HLS == nil {
		t.Fatalf("MeterOverlay ok=%v overlay=%+v", ok, overlay)
	}
	if overlay.HLS.CachedSegments != 3 || overlay.HLS.MaxCachedSegments != openedMaxSegments {
		t.Fatalf("HLS overlay = %+v", overlay.HLS)
	}
	body, _ := json.Marshal(overlay)
	for _, leak := range []string{"https://example.test", "/live.m3u8", "token=", "secret", "avc1"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("overlay leaked %q: %s", leak, body)
		}
	}
}

func TestURLMeterOverlayClearsBeforeBaseOnStopAndClose(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	var order []string
	var baseSawOverlay bool
	a.hlsBufferOpen = func(ctx context.Context, opts hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		return &hlsbuffer.Session{
			PlaybackPath: filepath.Join(t.TempDir(), "playlist.m3u8"),
			Stats:        func() hlsbuffer.Stats { return hlsbuffer.Stats{CachedSegments: 1} },
			Close: func() error {
				return nil
			},
		}, nil
	}
	fc.statusFn = func() core.SessionStatus {
		req := fc.snapshot()
		return core.SessionStatus{State: core.StatePlaying, AdapterRef: req.AdapterRef, Generation: 12}
	}
	ref, _, status, err := a.castURLWithHLSBuffer(context.Background(), "https://example.test/live.m3u8", "direct", "auto")
	if err != nil || status != http.StatusOK {
		t.Fatalf("castURLWithHLSBuffer status=%d err=%v", status, err)
	}
	snap := core.StatusHomeView{State: core.StatePlaying, AdapterRef: ref, Source: "url", Generation: 12}
	if _, ok := a.MeterOverlay(context.Background(), snap); !ok {
		t.Fatal("overlay should be active before stop")
	}
	baseOnStop := func(reason string) {
		order = append(order, "base")
		_, baseSawOverlay = a.MeterOverlay(context.Background(), snap)
	}
	onStop := withHLSBufferCleanup(a.hlsMeterClearingOnStop(ref, baseOnStop), &hlsbuffer.Session{
		Close: func() error {
			order = append(order, "close")
			return nil
		},
	})
	onStop("stopped")
	if baseSawOverlay {
		t.Fatal("overlay should be cleared before base OnStop state mutation")
	}
	if _, ok := a.MeterOverlay(context.Background(), core.StatusHomeView{State: core.StateIdle}); ok {
		t.Fatal("overlay should be cleared after stop")
	}
	if len(order) != 2 || order[0] != "base" || order[1] != "close" {
		t.Fatalf("stop order = %#v, want base before close", order)
	}
	_ = ref
}
```

- [ ] **Step 2: Run the focused failing URL tests**

Run:

```bash
go test ./internal/adapters/url -run 'TestURLMeterOverlay'
```

Expected: FAIL because URL adapter does not implement `MeterOverlay`.

- [ ] **Step 3: Add active overlay state to URL adapter**

Modify `internal/adapters/url/adapter.go`. Add only the new overlay handle field to the existing `Adapter` struct; do not replace the full struct or remove existing fields:

```go
type hlsMeterHandle struct {
	ref               string
	generation        uint64
	stats             func() hlsbuffer.Stats
	maxCachedSegments int
}

type Adapter struct {
	core SessionManager
	bridge config.BridgeConfig
	activeOverlay *hlsMeterHandle
}
```

Add methods in `internal/adapters/url/play.go` or a new URL package file:

```go
func (a *Adapter) installHLSMeterOverlay(ref string, session *hlsbuffer.Session, cfg hlsbuffer.Config) {
	if session == nil || session.Stats == nil || a.core == nil {
		return
	}
	st := a.core.Status()
	a.mu.Lock()
	defer a.mu.Unlock()
	if st.AdapterRef == ref && st.Generation != 0 {
		a.activeOverlay = &hlsMeterHandle{
			ref:               ref,
			generation:        st.Generation,
			stats:             session.Stats,
			maxCachedSegments: cfg.MaxCachedSegments,
		}
	}
}

func (a *Adapter) clearHLSMeterOverlay(ref string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeOverlay != nil && a.activeOverlay.ref == ref {
		a.activeOverlay = nil
	}
}

func (a *Adapter) hlsMeterClearingOnStop(ref string, base func(string)) func(string) {
	return func(reason string) {
		a.clearHLSMeterOverlay(ref)
		if base != nil {
			base(reason)
		}
	}
}

func (a *Adapter) MeterOverlay(ctx context.Context, snap core.StatusHomeView) (adapters.MeterOverlay, bool) {
	a.mu.Lock()
	h := a.activeOverlay
	a.mu.Unlock()
	if h == nil || h.ref != snap.AdapterRef || h.generation != snap.Generation || h.stats == nil {
		return adapters.MeterOverlay{}, false
	}
	return adapters.MeterOverlay{HLS: adapters.HLSMeterOverlayFromStats(h.stats(), h.maxCachedSegments)}, true
}
```

- [ ] **Step 4: Install and clear overlay in URL play path**

Change the HLS open branch to capture normalized config:

```go
var hlsSession *hlsbuffer.Session
var hlsCfg hlsbuffer.Config

// in the HLS branch:
hlsCfg = hlsbuffer.NormalizeConfig(hlsConfigFromBridge(bridge.HLSBuffer))
hlsSession, berr = a.openURLHLSBufferWithConfig(ctx, rawURL, bridge, hlsCfg, hlsBufferOpen)
```

Replace `openURLHLSBuffer` with a config-explicit helper:

```go
func (a *Adapter) openURLHLSBufferWithConfig(ctx context.Context, rawURL string, bridge config.BridgeConfig, hlsCfg hlsbuffer.Config, open hlsBufferOpener) (*hlsbuffer.Session, error) {
	if bridge.DataDir == "" {
		return nil, fmt.Errorf("url hls buffer: bridge data_dir is required")
	}
	cacheRoot := filepath.Join(bridge.DataDir, "url", "hls")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return nil, fmt.Errorf("url hls buffer: create cache root: %w", err)
	}
	if open == nil {
		open = hlsbuffer.OpenSession
	}
	return open(ctx, hlsbuffer.SessionOptions{
		SourceURL:    rawURL,
		CacheRoot:    cacheRoot,
		Config:       hlsCfg,
		TrustMode:    hlsbuffer.TrustModeGenericPublic,
		OutputHeight: hlsOutputHeightFromBridge(bridge),
	})
}
```

Build `onStop` so clear happens before URL state mutation and HLS close:

```go
baseOnStop := a.makeOnStop(rawURL, resolvedTitle)
onStop := baseOnStop
if hlsSession != nil {
	stoppedRef := ref
	onStop = withHLSBufferCleanup(a.hlsMeterClearingOnStop(stoppedRef, baseOnStop), hlsSession)
}
```

After a successful matched core start:

```go
if hlsSession != nil {
	a.installHLSMeterOverlay(ref, hlsSession, hlsCfg)
}
```

Update `withHLSBufferCleanup` to close only after the supplied function:

```go
func withHLSBufferCleanup(base func(string), session *hlsbuffer.Session) func(string) {
	return func(reason string) {
		if base != nil {
			base(reason)
		}
		closeHLSSession(session)
	}
}
```

The clear-first behavior is now provided by the wrapped `base` function above.

- [ ] **Step 5: Run URL adapter tests**

Run:

```bash
go test ./internal/adapters/url
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/url/adapter.go internal/adapters/url/play.go internal/adapters/url/play_test.go
git commit -m "feat(url): expose hls meter overlay"
```

---

## Task 6: Streams HLS Meter Overlay

**Files:**
- Modify: `internal/adapters/streams/adapter.go`
- Modify: `internal/adapters/streams/playback.go`
- Modify: `internal/adapters/streams/playback_test.go`

- [ ] **Step 1: Write failing Streams overlay tests**

Add tests to `internal/adapters/streams/playback_test.go`:

```go
func TestStreamsMeterOverlayUsesCoreGeneration(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	enableBridgeHLSBufferForTest(a)
	a.bridge.HLSBuffer.MaxCachedSegments = 9
	fc.status = core.SessionStatus{State: core.StatePlaying, Generation: 21}
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})

	var openedMaxSegments int
	stats := hlsbuffer.Stats{
		CachedSegments:        2,
		CachedMediaDuration:   5 * time.Second,
		CacheBytes:            4096,
		SegmentDownloadsTotal: 2,
		SelectedVariant:       hlsbuffer.Variant{URI: "relative/live.m3u8?sig=secret", Width: 640, Height: 480, Bandwidth: 1200000, Codecs: "avc1.secret"},
	}
	a.hlsBufferOpen = func(ctx context.Context, opts hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		openedMaxSegments = opts.Config.MaxCachedSegments
		return &hlsbuffer.Session{
			PlaybackPath: filepath.Join(t.TempDir(), "playlist.m3u8"),
			Stats:        func() hlsbuffer.Stats { return stats },
			Close:        func() error { return nil },
		}, nil
	}

	started, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "toonami-aftermath", ChannelID: "east"})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if openedMaxSegments != 9 {
		t.Fatalf("opened MaxCachedSegments = %d, want normalized configured value 9", openedMaxSegments)
	}
	a.bridge.HLSBuffer.MaxCachedSegments = 99

	snap := core.StatusHomeView{State: core.StatePlaying, AdapterRef: started.AdapterRef, Source: "streams", Generation: 21}
	if snap.AdapterRef != started.AdapterRef || snap.Generation == 0 {
		t.Fatalf("core snapshot = %+v started=%+v", snap, started)
	}
	overlay, ok := a.MeterOverlay(context.Background(), snap)
	if !ok || overlay.HLS == nil {
		t.Fatalf("MeterOverlay ok=%v overlay=%+v", ok, overlay)
	}
	if overlay.HLS.CachedSegments != 2 || overlay.HLS.MaxCachedSegments != openedMaxSegments {
		t.Fatalf("HLS overlay = %+v", overlay.HLS)
	}
	stale := snap
	stale.Generation++
	if _, ok := a.MeterOverlay(context.Background(), stale); ok {
		t.Fatalf("stale core generation should not own overlay")
	}
	body, _ := json.Marshal(overlay)
	for _, leak := range []string{"/live.m3u8", "sig=", "secret", "avc1"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("overlay leaked %q: %s", leak, body)
		}
	}
}

func TestStreamsMeterOverlayClearsBeforeBaseOnStopAndClose(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	ref := "streams:test:channel:1"
	snap := core.StatusHomeView{State: core.StatePlaying, AdapterRef: ref, Source: "streams", Generation: 21}
	a.activeOverlay = &hlsMeterHandle{
		ref:               ref,
		generation:        21,
		stats:             func() hlsbuffer.Stats { return hlsbuffer.Stats{CachedSegments: 1} },
		maxCachedSegments: 6,
	}
	var order []string
	var baseSawOverlay bool
	baseOnStop := func(reason string) {
		order = append(order, "base")
		_, baseSawOverlay = a.MeterOverlay(context.Background(), snap)
	}
	onStop := withHLSBufferCleanup(a.hlsMeterClearingOnStop(ref, baseOnStop), &hlsbuffer.Session{
		Close: func() error {
			order = append(order, "close")
			return nil
		},
	})
	onStop("stopped")
	if baseSawOverlay {
		t.Fatal("overlay should be cleared before base OnStop state mutation")
	}
	if _, ok := a.MeterOverlay(context.Background(), core.StatusHomeView{State: core.StateIdle}); ok {
		t.Fatal("overlay should be cleared after stop")
	}
	if len(order) != 2 || order[0] != "base" || order[1] != "close" {
		t.Fatalf("stop order = %#v, want base before close", order)
	}
}
```

- [ ] **Step 2: Run the focused failing Streams tests**

Run:

```bash
go test ./internal/adapters/streams -run 'TestStreamsMeterOverlay'
```

Expected: FAIL because Streams adapter does not implement `MeterOverlay`.

- [ ] **Step 3: Add Streams overlay state and methods**

Modify `internal/adapters/streams/adapter.go`. Add only the new overlay handle field to the existing `Adapter` struct; do not replace the full struct or remove existing fields:

```go
type hlsMeterHandle struct {
	ref               string
	generation        uint64
	stats             func() hlsbuffer.Stats
	maxCachedSegments int
}

type Adapter struct {
	core          SessionManager
	bridge        config.BridgeConfig
	cookiesPath   string
	cacheDir      string
	ytdlpBinary   ytdlp.BinaryResolver
	resolver      streamResolver
	hlsBufferOpen hlsBufferOpener
	activeOverlay *hlsMeterHandle
}
```

Add Streams methods in `internal/adapters/streams/playback.go` or a new file in the same package:

```go
func (a *Adapter) installHLSMeterOverlay(ref string, session *hlsbuffer.Session, cfg hlsbuffer.Config) {
	if session == nil || session.Stats == nil || a.core == nil {
		return
	}
	st := a.core.Status()
	a.mu.Lock()
	defer a.mu.Unlock()
	if st.AdapterRef == ref && st.Generation != 0 {
		a.activeOverlay = &hlsMeterHandle{
			ref:               ref,
			generation:        st.Generation,
			stats:             session.Stats,
			maxCachedSegments: cfg.MaxCachedSegments,
		}
	}
}

func (a *Adapter) clearHLSMeterOverlay(ref string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeOverlay != nil && a.activeOverlay.ref == ref {
		a.activeOverlay = nil
	}
}

func (a *Adapter) hlsMeterClearingOnStop(ref string, base func(string)) func(string) {
	return func(reason string) {
		a.clearHLSMeterOverlay(ref)
		if base != nil {
			base(reason)
		}
	}
}

func (a *Adapter) MeterOverlay(ctx context.Context, snap core.StatusHomeView) (adapters.MeterOverlay, bool) {
	a.mu.Lock()
	h := a.activeOverlay
	a.mu.Unlock()
	if h == nil || h.ref != snap.AdapterRef || h.generation != snap.Generation || h.stats == nil {
		return adapters.MeterOverlay{}, false
	}
	return adapters.MeterOverlay{HLS: adapters.HLSMeterOverlayFromStats(h.stats(), h.maxCachedSegments)}, true
}
```

- [ ] **Step 4: Install and clear overlay in Streams direct-HLS path**

In `playCurrentWithStarter`, inside the `a.shouldBufferDirectHLS(q, item)` branch:

```go
hlsCfg := hlsbuffer.NormalizeConfig(hlsConfigFromBridge(a.bridge.HLSBuffer))
hlsSession, err = open(resolveCtx, hlsbuffer.SessionOptions{
	SourceURL:    pageURL,
	CacheRoot:    a.hlsBufferCacheRoot(),
	Config:       hlsCfg,
	TrustMode:    hlsbuffer.TrustModeBundledToonami,
	OutputHeight: a.hlsOutputHeight(),
})
```

Capture `stoppedRef := ref` before building `onStop` and clear before base state work:

```go
baseOnStop := a.makeOnStop(capture)
onStop := baseOnStop
if hlsSession != nil {
	stoppedRef := ref
	onStop = withHLSBufferCleanup(a.hlsMeterClearingOnStop(stoppedRef, baseOnStop), hlsSession)
}
```

After `starter(coreManager, req)` succeeds with `matched == true` and before releasing the playback path:

```go
if hlsSession != nil {
	a.installHLSMeterOverlay(ref, hlsSession, hlsCfg)
}
```

Use the core status generation returned by `a.core.Status()` inside `installHLSMeterOverlay`. Do not use `queueCapture.Generation`.

- [ ] **Step 5: Run Streams adapter tests**

Run:

```bash
go test ./internal/adapters/streams
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/streams/adapter.go internal/adapters/streams/playback.go internal/adapters/streams/playback_test.go
git commit -m "feat(streams): expose hls meter overlay"
```

---

## Task 7: Chassis Meter Data, Formatting, And Sampler

**Files:**
- Create: `internal/chassis/meter.go`
- Create: `internal/chassis/meter_test.go`
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/session.go`
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing chassis meter tests**

Create `internal/chassis/meter_test.go`:

```go
package chassis

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestMeterSamplerFormatsLiveLowRateFields(t *testing.T) {
	sampler := newMeterSampler()
	now := time.Unix(100, 0)
	snap := core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Live",
		AdapterRef: "url:live",
		Source:     "url",
		Generation: 4,
		Position:   10 * time.Second,
		Duration:   time.Minute,
		Meter: core.MeterHomeView{
			Source: core.SourceMeterView{
				Width: 1280, Height: 720, FrameRate: 29.97,
				VideoCodec: "h264", AudioCodec: "aac", AudioRate: 48000, AudioChannels: 2,
				DisplayAspectRatioNum: 16, DisplayAspectRatioDen: 9,
				FormatBitrateBPS: 2400000,
			},
			Crop: core.CropMeterView{Mode: "letterbox"},
			Pipeline: core.PipelineMeterView{
				ModelineName: "NTSC_480i", OutputWidth: 720, OutputHeight: 480, FieldHeight: 240,
				FieldRateHz: 59.94, HorizontalKHz: 15.734, InterlacedOutput: true, Standard: "ntsc",
				FieldOrder: "tff", RGBMode: "bt601", LZ4Enabled: true, DeltaLZ4Enabled: true,
				AudioSampleRate: 48000, AudioChannels: 2,
			},
			Runtime: core.RuntimeMeterView{
				BlitsTotal: 100, Underruns: 0, WireBytes: 1_000_000, LastACKAge: 4 * time.Millisecond,
				Generation: 4,
			},
		},
	}
	first := sampler.Sample(snap, adapters.MeterOverlay{}, now)
	secondSnap := snap
	secondSnap.Meter.Runtime.BlitsTotal = 130
	secondSnap.Meter.Runtime.Underruns = 3
	secondSnap.Meter.Runtime.WireBytes = 8_000_000
	got := sampler.Sample(secondSnap, adapters.MeterOverlay{HLS: &adapters.HLSMeterOverlay{
		CachedSegments: 6, MaxCachedSegments: 12, CacheBytes: 1234, SelectedVariantBPS: 2600000,
	}}, now.Add(500*time.Millisecond))

	if first.SampleSeq == got.SampleSeq {
		t.Fatalf("SampleSeq did not advance: first=%d got=%d", first.SampleSeq, got.SampleSeq)
	}
	if got.SourceStrip.AudioIn != "AAC - STEREO" {
		t.Fatalf("AudioIn = %q", got.SourceStrip.AudioIn)
	}
	if got.SourceStrip.AudioOut != "S16LE - 48k - STEREO" {
		t.Fatalf("AudioOut = %q", got.SourceStrip.AudioOut)
	}
	if got.SourceStrip.HLSBuffer != "6 / 12 SEG" || got.SourceStrip.HLSCachedSegments != 6 {
		t.Fatalf("HLS fields = %+v", got.SourceStrip)
	}
	if got.SourceStrip.Drops != "10.0" || got.SourceStrip.DropsPercent != 10.0 {
		t.Fatalf("drops = %q %.1f", got.SourceStrip.Drops, got.SourceStrip.DropsPercent)
	}
	if got.MidRow.Standard != "ntsc" || !got.MidRow.StandardNTSC || got.MidRow.StandardPAL {
		t.Fatalf("standard lamps = %+v", got.MidRow)
	}
	if got.MidRow.ThroughputSampleMBs <= 0 || len(got.MidRow.ThroughputHistoryMBs) != 1 {
		t.Fatalf("throughput = %+v", got.MidRow)
	}
	if got.Readout.Pipe != "LZ4+D - TFF" || !strings.Contains(got.Readout.Link, "MiSTer") {
		t.Fatalf("readout = %+v", got.Readout)
	}
}

func TestMeterSamplerPausedFreezesHistoriesAndDisplayValues(t *testing.T) {
	sampler := newMeterSampler()
	now := time.Unix(200, 0)
	snap := core.StatusHomeView{
		State:      core.StatePlaying,
		AdapterRef: "url:live",
		Source:     "url",
		Generation: 9,
		Meter: core.MeterHomeView{
			Source: core.SourceMeterView{
				Width: 720, Height: 480, FrameRate: 29.97,
				VideoCodec: "h264", AudioCodec: "aac", AudioRate: 48000, AudioChannels: 2,
			},
			Pipeline: core.PipelineMeterView{
				FieldRateHz: 59.94, HorizontalKHz: 15.734, Standard: "ntsc", FieldOrder: "tff",
				AudioSampleRate: 48000, AudioChannels: 2,
			},
			Runtime: core.RuntimeMeterView{
				BlitsTotal: 10, WireBytes: 1_000_000, LastACKAge: 4 * time.Millisecond,
				Generation: 9,
			},
		},
	}
	sampler.Sample(snap, adapters.MeterOverlay{}, now)

	liveSnap := snap
	liveSnap.Meter.Runtime.BlitsTotal = 40
	liveSnap.Meter.Runtime.WireBytes = 4_000_000
	liveSnap.Meter.Runtime.LastACKAge = 6 * time.Millisecond
	live := sampler.Sample(liveSnap, adapters.MeterOverlay{}, now.Add(500*time.Millisecond))

	pausedSnap := liveSnap
	pausedSnap.State = core.StatePaused
	pausedSnap.Meter.Runtime.BlitsTotal = 400
	pausedSnap.Meter.Runtime.WireBytes = 80_000_000
	pausedSnap.Meter.Runtime.LastACKAge = 99 * time.Millisecond
	paused := sampler.Sample(pausedSnap, adapters.MeterOverlay{}, now.Add(time.Second))

	if !paused.Paused {
		t.Fatal("paused sample did not set Paused")
	}
	if paused.SampleSeq != live.SampleSeq {
		t.Fatalf("paused SampleSeq = %d, want frozen %d", paused.SampleSeq, live.SampleSeq)
	}
	if paused.MidRow.ThroughputMBs != live.MidRow.ThroughputMBs || paused.MidRow.MSAck != live.MidRow.MSAck || paused.Readout.Speed != live.Readout.Speed {
		t.Fatalf("paused live display changed: live=%+v paused=%+v", live, paused)
	}
	if len(paused.MidRow.ThroughputHistoryMBs) != len(live.MidRow.ThroughputHistoryMBs) ||
		len(paused.MidRow.AckHistoryMS) != len(live.MidRow.AckHistoryMS) ||
		len(paused.MidRow.ThroughputHistoryMBs) == 0 ||
		len(paused.MidRow.AckHistoryMS) == 0 ||
		paused.MidRow.ThroughputHistoryMBs[0] != live.MidRow.ThroughputHistoryMBs[0] ||
		paused.MidRow.AckHistoryMS[0] != live.MidRow.AckHistoryMS[0] {
		t.Fatalf("paused histories changed: live=%+v paused=%+v", live.MidRow, paused.MidRow)
	}
}

func TestMeterSamplerIdleDimsBothStandardLamps(t *testing.T) {
	got := newMeterSampler().Sample(core.StatusHomeView{State: core.StateIdle}, adapters.MeterOverlay{}, time.Unix(1, 0))
	if got.MidRow.StandardNTSC || got.MidRow.StandardPAL {
		t.Fatalf("idle standard lamps = NTSC %v PAL %v, want both false", got.MidRow.StandardNTSC, got.MidRow.StandardPAL)
	}
}

func TestMeterOverlayPanicRecoveryKeepsBaseFields(t *testing.T) {
	snap := core.StatusHomeView{State: core.StatePlaying, AdapterRef: "url:1", Source: "url", Generation: 1}
	providers := []namedMeterOverlayProvider{
		{name: "panic", provider: panicMeterOverlayProvider{}},
		{name: "ok", provider: staticMeterOverlayProvider{overlay: adapters.MeterOverlay{HLS: &adapters.HLSMeterOverlay{CachedSegments: 2, MaxCachedSegments: 6}}}},
	}
	got := collectMeterOverlays(context.Background(), providers, snap, newOverlayPanicLimiter())
	if got.HLS == nil || got.HLS.CachedSegments != 2 {
		t.Fatalf("overlay after panic recovery = %+v", got)
	}
}

func TestMeterEnvelopeDoesNotLeakHLSSecrets(t *testing.T) {
	m := idleSnapshot(nonZeroConfig(), time.Unix(1, 0)).Meter
	m.SourceStrip.HLSBuffer = "1 / 6 SEG"
	env := meterEnvelopeFrom(m)
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leak := range []string{"http://", "https://", "://", "/live.m3u8", "token=", "sig=", "secret", "Authorization"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("meter envelope leaked %q: %s", leak, body)
		}
	}
}

type panicMeterOverlayProvider struct{}

func (panicMeterOverlayProvider) MeterOverlay(context.Context, core.StatusHomeView) (adapters.MeterOverlay, bool) {
	panic("boom")
}

type staticMeterOverlayProvider struct{ overlay adapters.MeterOverlay }

func (p staticMeterOverlayProvider) MeterOverlay(context.Context, core.StatusHomeView) (adapters.MeterOverlay, bool) {
	return p.overlay, true
}
```

- [ ] **Step 2: Run the focused failing tests**

Run:

```bash
go test ./internal/chassis -run 'TestMeterSampler|TestMeterOverlay|TestMeterEnvelope'
```

Expected: FAIL because meter sampler and extended `MeterData` fields are missing.

- [ ] **Step 3: Extend `MeterData`**

Modify `internal/chassis/data.go`:

```go
type MeterData struct {
	State       string
	Paused      bool
	Generation  uint64
	SampleSeq   uint64
	SourceStrip SourceStripIdleData
	MidRow      MidRowIdleData
	Readout     ReadoutIdleData
	AudioScopes AudioScopesData
}

type SourceStripIdleData struct {
	AudioIn             string
	AudioOut            string
	Src                 string
	Crop                string
	HLSBuffer           string
	HLSCachedSegments   int
	HLSMaxSegments      int
	HLSCacheBytes       int64
	Drops               string
	DropsPercent        float64
	BlitsTotal          uint64
	UnderrunsTotal      uint64
}

type MidRowIdleData struct {
	BitrateMbps           string
	FreqKHz               string
	Mode                  string
	Standard              string
	StandardNTSC          bool
	StandardPAL           bool
	FieldOrder            string
	FieldRateHz           float64
	InterlacedOutput      bool
	FieldLock             string
	FieldFlip             string
	ThroughputMBs         string
	ThroughputSampleMBs   float64
	ThroughputHistoryMBs  []float64
	MSAck                 string
	AckSampleMS           float64
	AckHistoryMS          []float64
}

type ReadoutIdleData struct {
	LRBars     int
	PhaseNeedle string
	LUFS       string
	Output     string
	Aspect     string
	Pipe       string
	Speed      string
	SpeedRatio float64
	Link       string
}

type AudioScopesData struct {
	Status string
}
```

Update `idleSnapshot` meter defaults:

```go
MidRow: MidRowIdleData{
	BitrateMbps:   "---",
	FreqKHz:       "---",
	Mode:          "---",
	Standard:      "",
	StandardNTSC:  false,
	StandardPAL:   false,
	FieldFlip:     "idle",
	FieldLock:     "idle",
	ThroughputMBs: "0.0",
	MSAck:         "--",
},
AudioScopes: AudioScopesData{Status: "pending"},
```

- [ ] **Step 4: Add meter sampler and formatting**

Create `internal/chassis/meter.go` with these public-to-package entry points:

```go
package chassis

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type meterSampler struct {
	prevGeneration uint64
	prevWireBytes  uint64
	prevBlits      uint64
	prevUnderruns  uint64
	prevSampleTime time.Time
	sampleSeq      uint64
	throughput     []float64
	ack            []float64
	lastLive        MeterData
}

func newMeterSampler() *meterSampler {
	return &meterSampler{}
}

func (s *meterSampler) Sample(snap core.StatusHomeView, overlay adapters.MeterOverlay, now time.Time) MeterData {
	if snap.State != core.StatePlaying && snap.State != core.StatePaused {
		s.reset()
		return idleMeterData()
	}
	if snap.Generation != s.prevGeneration {
		s.reset()
		s.prevGeneration = snap.Generation
		s.prevWireBytes = snap.Meter.Runtime.WireBytes
		s.prevBlits = snap.Meter.Runtime.BlitsTotal
		s.prevUnderruns = snap.Meter.Runtime.Underruns
		s.prevSampleTime = now
	}
	current := meterDataFromSnapshot(snap, overlay, s.throughput, s.ack)
	current.State = string(StateLive)
	current.Paused = snap.State == core.StatePaused
	current.Generation = snap.Generation
	current.SampleSeq = s.sampleSeq
	if current.Paused {
		if s.lastLive.Generation == snap.Generation && s.lastLive.State != "" {
			paused := current
			paused.MidRow.ThroughputSampleMBs = s.lastLive.MidRow.ThroughputSampleMBs
			paused.MidRow.ThroughputMBs = s.lastLive.MidRow.ThroughputMBs
			paused.MidRow.AckSampleMS = s.lastLive.MidRow.AckSampleMS
			paused.MidRow.MSAck = s.lastLive.MidRow.MSAck
			paused.MidRow.ThroughputHistoryMBs = append([]float64(nil), s.lastLive.MidRow.ThroughputHistoryMBs...)
			paused.MidRow.AckHistoryMS = append([]float64(nil), s.lastLive.MidRow.AckHistoryMS...)
			paused.SourceStrip.DropsPercent = s.lastLive.SourceStrip.DropsPercent
			paused.SourceStrip.Drops = s.lastLive.SourceStrip.Drops
			paused.Readout.SpeedRatio = s.lastLive.Readout.SpeedRatio
			paused.Readout.Speed = s.lastLive.Readout.Speed
			paused.SampleSeq = s.lastLive.SampleSeq
			s.lastLive = paused
			return paused
		}
		s.lastLive = current
		return current
	}
	out := current
	if s.prevSampleTime.IsZero() || now.Sub(s.prevSampleTime) >= 500*time.Millisecond {
		elapsed := now.Sub(s.prevSampleTime).Seconds()
		if elapsed > 0 {
			wireDelta := deltaUint64(snap.Meter.Runtime.WireBytes, s.prevWireBytes)
			blitDelta := deltaUint64(snap.Meter.Runtime.BlitsTotal, s.prevBlits)
			underrunDelta := deltaUint64(snap.Meter.Runtime.Underruns, s.prevUnderruns)
			out.MidRow.ThroughputSampleMBs = float64(wireDelta) / elapsed / 1000000
			out.MidRow.ThroughputMBs = formatOneDecimal(out.MidRow.ThroughputSampleMBs)
			out.SourceStrip.DropsPercent = dropPercent(blitDelta, underrunDelta)
			out.SourceStrip.Drops = formatOneDecimal(out.SourceStrip.DropsPercent)
			out.Readout.SpeedRatio = speedRatioForDelta(blitDelta, elapsed, snap.Meter.Pipeline.FieldRateHz)
			out.Readout.Speed = formatSpeed(out.Readout.SpeedRatio)
			if snap.Meter.Runtime.LastACKAge > 0 {
				out.MidRow.AckSampleMS = float64(snap.Meter.Runtime.LastACKAge) / float64(time.Millisecond)
				out.MidRow.MSAck = formatAckMS(out.MidRow.AckSampleMS)
			}
			s.throughput = appendBoundedFloat(s.throughput, out.MidRow.ThroughputSampleMBs, 60)
			if out.MidRow.AckSampleMS > 0 {
				s.ack = appendBoundedFloat(s.ack, out.MidRow.AckSampleMS, 128)
			}
			out.MidRow.ThroughputHistoryMBs = append([]float64(nil), s.throughput...)
			out.MidRow.AckHistoryMS = append([]float64(nil), s.ack...)
			s.sampleSeq++
			out.SampleSeq = s.sampleSeq
		}
		s.prevWireBytes = snap.Meter.Runtime.WireBytes
		s.prevBlits = snap.Meter.Runtime.BlitsTotal
		s.prevUnderruns = snap.Meter.Runtime.Underruns
		s.prevSampleTime = now
	}
	s.lastLive = out
	return out
}

func (s *meterSampler) reset() {
	s.prevGeneration = 0
	s.prevWireBytes = 0
	s.prevBlits = 0
	s.prevUnderruns = 0
	s.prevSampleTime = time.Time{}
	s.throughput = nil
	s.ack = nil
	s.sampleSeq++
	s.lastLive = MeterData{}
}
```

Add helpers in the same file:

```go
func idleMeterData() MeterData {
	return idleSnapshot(Config{Version: "meter", StartedAt: time.Unix(0, 0)}, time.Unix(0, 0)).Meter
}

func meterDataFromSnapshot(snap core.StatusHomeView, overlay adapters.MeterOverlay, throughput []float64, ack []float64) MeterData {
	base := idleMeterData()
	src := snap.Meter.Source
	pipe := snap.Meter.Pipeline
	base.SourceStrip.AudioIn = formatAudioIn(src)
	base.SourceStrip.AudioOut = formatAudioOut(pipe.AudioSampleRate, pipe.AudioChannels)
	base.SourceStrip.Src = formatSource(src)
	base.SourceStrip.Crop = formatCrop(src, snap.Meter.Crop)
	base.SourceStrip.BlitsTotal = snap.Meter.Runtime.BlitsTotal
	base.SourceStrip.UnderrunsTotal = snap.Meter.Runtime.Underruns
	base.MidRow.BitrateMbps = formatBitrate(src, overlay)
	base.MidRow.FreqKHz = formatKHzFrequency(pipe.HorizontalKHz)
	base.MidRow.Mode = formatMode(pipe)
	base.MidRow.Standard = pipe.Standard
	base.MidRow.StandardNTSC = pipe.Standard == "ntsc"
	base.MidRow.StandardPAL = pipe.Standard == "pal"
	base.MidRow.FieldOrder = pipe.FieldOrder
	base.MidRow.FieldRateHz = pipe.FieldRateHz
	base.MidRow.InterlacedOutput = pipe.InterlacedOutput
	base.MidRow.FieldLock = formatFieldLock(pipe)
	base.MidRow.FieldFlip = base.MidRow.FieldLock
	base.MidRow.ThroughputHistoryMBs = append([]float64(nil), throughput...)
	base.MidRow.AckHistoryMS = append([]float64(nil), ack...)
	base.Readout.Output = formatOutput(pipe)
	base.Readout.Aspect = formatAspect(src, snap.Meter.Crop)
	base.Readout.Pipe = formatPipe(pipe)
	base.Readout.SpeedRatio = 1.0
	base.Readout.Speed = formatSpeed(base.Readout.SpeedRatio)
	base.Readout.Link = formatLink(snap.Meter.Runtime.LastACKAge)
	base.AudioScopes.Status = "pending"
	if overlay.HLS != nil {
		h := overlay.HLS
		base.SourceStrip.HLSCachedSegments = h.CachedSegments
		base.SourceStrip.HLSMaxSegments = h.MaxCachedSegments
		base.SourceStrip.HLSCacheBytes = h.CacheBytes
		base.SourceStrip.HLSBuffer = fmt.Sprintf("%d / %d SEG", h.CachedSegments, h.MaxCachedSegments)
	}
	return base
}

func deltaUint64(now, prev uint64) uint64 {
	if now < prev {
		return 0
	}
	return now - prev
}

func dropPercent(deltaBlits, deltaUnderruns uint64) float64 {
	if deltaBlits == 0 {
		return 0
	}
	return 100 * float64(deltaUnderruns) / float64(deltaBlits)
}

func appendBoundedFloat(values []float64, v float64, max int) []float64 {
	values = append(values, v)
	if len(values) > max {
		values = values[len(values)-max:]
	}
	return values
}
```

Add formatter helpers. Keep them package-private and test through `meterSampler`:

```go
func formatOneDecimal(v float64) string {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return "0.0"
	}
	return fmt.Sprintf("%.1f", v)
}

func formatKHzFrequency(v float64) string {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return "---"
	}
	return fmt.Sprintf("%.1f", v)
}

func formatAckMS(v float64) string {
	if v <= 0 {
		return "--"
	}
	return fmt.Sprintf("%04.1f", v)
}

func formatAudioIn(src core.SourceMeterView) string {
	if src.AudioCodec == "" && src.AudioChannels == 0 {
		return "---"
	}
	return strings.TrimSpace(formatCodec(src.AudioCodec) + " - " + formatChannels(src.AudioChannels))
}

func formatAudioOut(rate, channels int) string {
	if rate <= 0 || channels <= 0 {
		return "---"
	}
	return fmt.Sprintf("S16LE - %s - %s", formatKHz(rate), formatChannels(channels))
}

func formatCodec(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "h264":
		return "H.264"
	case "aac":
		return "AAC"
	case "aac_lc":
		return "AAC LC"
	case "mp3":
		return "MP3"
	case "opus":
		return "OPUS"
	default:
		if codec == "" {
			return "---"
		}
		return strings.ToUpper(codec)
	}
}

func formatChannels(ch int) string {
	switch ch {
	case 1:
		return "MONO"
	case 2:
		return "STEREO"
	default:
		return "---"
	}
}

func formatKHz(rate int) string {
	if rate%1000 == 0 {
		return fmt.Sprintf("%dk", rate/1000)
	}
	return fmt.Sprintf("%.1fk", float64(rate)/1000)
}

func formatSource(src core.SourceMeterView) string {
	if src.Width <= 0 || src.Height <= 0 {
		return "---"
	}
	codec := formatCodec(src.VideoCodec)
	if codec == "---" {
		codec = "VIDEO"
	}
	return fmt.Sprintf("%dx%d@%s - %s", src.Width, src.Height, formatFrameRate(src.FrameRate), codec)
}

func formatFrameRate(rate float64) string {
	if rate <= 0 {
		return "--"
	}
	if math.Abs(rate-math.Round(rate)) < 0.01 {
		return fmt.Sprintf("%.0f", math.Round(rate))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", rate), "0"), ".")
}

func formatCrop(src core.SourceMeterView, crop core.CropMeterView) string {
	if src.Width <= 0 || src.Height <= 0 {
		return "---"
	}
	aspect := aspectLabel(src)
	mode := strings.ToUpper(strings.TrimSpace(crop.Mode))
	if mode == "" {
		mode = "NATIVE"
	}
	if !crop.Detected {
		return fmt.Sprintf("NONE - %s %s", aspect, mode)
	}
	return fmt.Sprintf("%dx%d+%d+%d - %s %s", crop.W, crop.H, crop.X, crop.Y, aspect, mode)
}

func formatAspect(src core.SourceMeterView, crop core.CropMeterView) string {
	if src.Width <= 0 || src.Height <= 0 {
		return "---"
	}
	mode := strings.ToUpper(strings.TrimSpace(crop.Mode))
	if mode == "" {
		mode = "NATIVE"
	}
	return fmt.Sprintf("%s %s", aspectLabel(src), mode)
}

func aspectLabel(src core.SourceMeterView) string {
	n, d := src.DisplayAspectRatioNum, src.DisplayAspectRatioDen
	if n == 0 || d == 0 {
		n, d = src.SampleAspectRatioNum*src.Width, src.SampleAspectRatioDen*src.Height
	}
	if n == 0 || d == 0 {
		n, d = src.Width, src.Height
	}
	ratio := float64(n) / float64(d)
	switch {
	case math.Abs(ratio-(4.0/3.0)) < 0.04:
		return "4:3"
	case math.Abs(ratio-(16.0/9.0)) < 0.04:
		return "16:9"
	default:
		return fmt.Sprintf("%d:%d", n, d)
	}
}

func formatBitrate(src core.SourceMeterView, overlay adapters.MeterOverlay) string {
	bps := src.FormatBitrateBPS
	if bps == 0 {
		bps = src.VideoBitrateBPS + src.AudioBitrateBPS
	}
	if overlay.HLS != nil && overlay.HLS.SelectedVariantBPS > 0 {
		bps = overlay.HLS.SelectedVariantBPS
	}
	if bps <= 0 {
		return "---"
	}
	return fmt.Sprintf("%.1f", float64(bps)/1000000)
}

func formatMode(pipe core.PipelineMeterView) string {
	if pipe.OutputWidth == 720 && (pipe.OutputHeight == 480 || pipe.OutputHeight == 576) {
		return "704"
	}
	if pipe.OutputWidth > 0 {
		return fmt.Sprintf("%d", pipe.OutputWidth)
	}
	return "---"
}

func formatFieldLock(pipe core.PipelineMeterView) string {
	if !pipe.InterlacedOutput {
		return "PROG"
	}
	if pipe.FieldOrder == "" {
		return "LOCK"
	}
	return strings.ToUpper(pipe.FieldOrder) + " LOCK"
}

func formatOutput(pipe core.PipelineMeterView) string {
	if pipe.OutputHeight <= 0 {
		return "---"
	}
	if pipe.InterlacedOutput {
		return fmt.Sprintf("INTERLACE %di - BT.601", pipe.OutputHeight)
	}
	return fmt.Sprintf("PROGRESSIVE %dp - BT.601", pipe.OutputHeight)
}

func formatPipe(pipe core.PipelineMeterView) string {
	codec := "RAW"
	switch {
	case pipe.LZ4Enabled && pipe.DeltaLZ4Enabled:
		codec = "LZ4+D"
	case pipe.LZ4Enabled:
		codec = "LZ4"
	}
	order := strings.ToUpper(pipe.FieldOrder)
	if order == "" {
		order = "PROG"
	}
	return codec + " - " + order
}

func speedRatioForDelta(deltaBlits uint64, elapsedSeconds float64, expectedFieldRate float64) float64 {
	if elapsedSeconds <= 0 || expectedFieldRate <= 0 {
		return 1
	}
	return (float64(deltaBlits) / elapsedSeconds) / expectedFieldRate
}

func formatSpeed(ratio float64) string {
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return "---"
	}
	state := "LOCK"
	if ratio < 0.98 {
		state = "SLOW"
	}
	if ratio > 1.02 {
		state = "FAST"
	}
	return fmt.Sprintf("%.2fx %s", ratio, state)
}

func formatLink(ack time.Duration) string {
	if ack <= 0 {
		return "MiSTer - --"
	}
	return fmt.Sprintf("MiSTer - %.0fms", float64(ack)/float64(time.Millisecond))
}
```

- [ ] **Step 5: Route server snapshots through one sampler**

Modify `internal/chassis/server.go`. Add only the `meter` field to the existing `Server` struct; do not replace the full struct or remove existing fields:

```go
type Server struct {
	cfg      Config
	session  SessionViewer
	tmpl     *template.Template
	cssBytes []byte
	meter    *meterSampler
	transportViewer     TransportViewer
	transportController TransportController
	visualizerViewer    VisualizerViewer
	visualizerSaver     VisualizerSaver
	aux                 AUXStarter
}
```

Initialize it in `New`:

```go
s := &Server{
	cfg:                 cfg,
	session:             cfg.Session,
	tmpl:                tmpl,
	cssBytes:            cssBytes,
	meter:               newMeterSampler(),
	transportViewer:     cfg.TransportViewer,
	transportController: cfg.TransportController,
	visualizerViewer:    cfg.VisualizerViewer,
	visualizerSaver:     cfg.VisualizerSaver,
	aux:                 cfg.AUX,
}
s.cache.Set(s.buildSnapshot(time.Now()))
```

Add:

```go
func (s *Server) buildSnapshot(now time.Time) ReceiverPageData {
	if s.session == nil {
		base := idleSnapshot(s.cfg, now)
		base.Meter = s.meter.Sample(core.StatusHomeView{State: core.StateIdle}, adapters.MeterOverlay{}, now)
		applyAUXSourceState(&base, s.aux)
		base.Visualizer.ActiveMode = liveVisualizerMode(s.cfg, s.visualizerViewer)
		return base
	}
	view := s.session.StatusHomeView()
	base := snapshotFromStatusView(s.cfg, view, s.visualizerViewer, s.transportViewer, s.aux, now)
	overlay := s.collectMeterOverlay(context.Background(), view)
	base.Meter = s.meter.Sample(view, overlay, now)
	return base
}
```

Refactor `snapshotFromSession` in `internal/chassis/session.go` so tests keep working:

```go
func snapshotFromSession(cfg Config, sv SessionViewer, vv VisualizerViewer, tv TransportViewer, aux AUXStarter, now time.Time) ReceiverPageData {
	if sv == nil {
		base := idleSnapshot(cfg, now)
		applyAUXSourceState(&base, aux)
		base.Visualizer.ActiveMode = liveVisualizerMode(cfg, vv)
		return base
	}
	return snapshotFromStatusView(cfg, sv.StatusHomeView(), vv, tv, aux, now)
}

func snapshotFromStatusView(cfg Config, view core.StatusHomeView, vv VisualizerViewer, tv TransportViewer, aux AUXStarter, now time.Time) ReceiverPageData {
	base := idleSnapshot(cfg, now)
	switch view.State {
	case core.StatePlaying, core.StatePaused:
		base.State = StateLive
		base.VFD.State = string(StateLive)
		base.VFD.Title = view.Title
		base.VFD.Marquee = formatLiveMarquee(view)
		base.Transport = buildTransportData(view, tv, context.Background())
	default:
	}
	applyAUXSourceState(&base, aux)
	base.Visualizer.ActiveMode = liveVisualizerMode(cfg, vv)
	return base
}
```

Update `startSnapshotRefresher`:

```go
s.cache.Set(s.buildSnapshot(time.Now()))
```

- [ ] **Step 6: Add overlay discovery and panic recovery**

Add to `internal/chassis/meter.go`:

```go
type namedMeterOverlayProvider struct {
	name     string
	provider adapters.MeterOverlayProvider
}

type overlayPanicLimiter struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func newOverlayPanicLimiter() *overlayPanicLimiter {
	return &overlayPanicLimiter{seen: map[string]struct{}{}}
}

func (l *overlayPanicLimiter) log(name string, generation uint64, err any) {
	key := fmt.Sprintf("%s/%d", name, generation)
	l.mu.Lock()
	_, seen := l.seen[key]
	if !seen {
		l.seen[key] = struct{}{}
	}
	l.mu.Unlock()
	if !seen {
		slog.Warn("chassis: meter overlay panic", "provider", name, "err", err)
	}
}

func (s *Server) meterOverlayProviders() []namedMeterOverlayProvider {
	if s.cfg.Registry == nil {
		return nil
	}
	var out []namedMeterOverlayProvider
	for _, a := range s.cfg.Registry.List() {
		if p, ok := a.(adapters.MeterOverlayProvider); ok {
			out = append(out, namedMeterOverlayProvider{name: a.Name(), provider: p})
		}
	}
	return out
}

func (s *Server) collectMeterOverlay(ctx context.Context, snap core.StatusHomeView) adapters.MeterOverlay {
	return collectMeterOverlays(ctx, s.meterOverlayProviders(), snap, s.overlayPanics)
}

func collectMeterOverlays(ctx context.Context, providers []namedMeterOverlayProvider, snap core.StatusHomeView, limiter *overlayPanicLimiter) adapters.MeterOverlay {
	var out adapters.MeterOverlay
	for _, item := range providers {
		overlay, ok := callMeterOverlayProvider(ctx, item, snap, limiter)
		if !ok {
			continue
		}
		if overlay.HLS != nil && out.HLS == nil {
			out.HLS = overlay.HLS
		}
	}
	return out
}

func callMeterOverlayProvider(ctx context.Context, item namedMeterOverlayProvider, snap core.StatusHomeView, limiter *overlayPanicLimiter) (overlay adapters.MeterOverlay, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if limiter != nil {
				limiter.log(item.name, snap.Generation, r)
			}
			overlay = adapters.MeterOverlay{}
			ok = false
		}
	}()
	return item.provider.MeterOverlay(ctx, snap)
}
```

Add `overlayPanics *overlayPanicLimiter` to `Server` and initialize it in `New`.

- [ ] **Step 7: Run chassis meter tests**

Run:

```bash
go test ./internal/chassis -run 'TestMeter|TestSnapshotFromSession|TestIdleSnapshot'
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/chassis/meter.go internal/chassis/meter_test.go internal/chassis/data.go internal/chassis/session.go internal/chassis/server.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): format receiver meter snapshots"
```

---

## Task 8: Meter SSE Event And Cached Initial Burst

**Files:**
- Modify: `internal/chassis/events.go`
- Modify: `internal/chassis/events_test.go`

- [ ] **Step 1: Write failing SSE tests**

Add or update tests in `internal/chassis/events_test.go`:

```go
func TestHandleEvents_InitialBurstIncludesMeterAfterTransportFromCache(t *testing.T) {
	cfg := nonZeroConfig()
	sv := &countingViewer{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Cached Meter",
		AdapterRef: "url:cached",
		Source:     "url",
		Generation: 2,
	}}
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before := sv.Calls()
	body := readInitialSSE(t, s)
	after := sv.Calls()
	if after != before {
		t.Fatalf("initial SSE called StatusHomeView %d extra times; want cached burst only", after-before)
	}
	transportIdx := strings.Index(body, "event: transport\n")
	meterIdx := strings.Index(body, "event: meter\n")
	if transportIdx < 0 || meterIdx < 0 || meterIdx <= transportIdx {
		t.Fatalf("meter must be emitted after transport; body:\n%s", body)
	}
	if !strings.Contains(body, `"generation":2`) {
		t.Fatalf("meter payload missing generation: body:\n%s", body)
	}
}

func readInitialSSE(t *testing.T, s *Server) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	w := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleEvents(w, req)
	}()
	deadline := time.After(300 * time.Millisecond)
	for {
		if strings.Contains(w.Body.String(), "event: meter\n") {
			cancel()
			<-done
			return w.Body.String()
		}
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("timed out waiting for initial meter event; body:\n%s", w.Body.String())
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestMeterChangedGating(t *testing.T) {
	base := idleSnapshot(nonZeroConfig(), time.Unix(1, 0)).Meter
	base.State = "live"
	base.Generation = 10
	base.SampleSeq = 1
	base.MidRow.Standard = "ntsc"
	base.MidRow.FieldOrder = "tff"
	base.MidRow.InterlacedOutput = true
	base.Readout.Output = "INTERLACE 480i - BT.601"
	base.Readout.Aspect = "4:3 LETTERBOX"
	base.Readout.Pipe = "LZ4+D - TFF"
	base.Readout.Link = "MiSTer - 4ms"
	cases := []struct {
		name string
		edit func(*MeterData)
		want bool
	}{
		{"sample boundary", func(m *MeterData) { m.SampleSeq++ }, true},
		{"pause flip", func(m *MeterData) { m.Paused = true }, true},
		{"generation", func(m *MeterData) { m.Generation++ }, true},
		{"structural field", func(m *MeterData) { m.MidRow.FieldOrder = "bff" }, true},
		{"idle clear", func(m *MeterData) { m.State = "idle" }, true},
		{"ack text jitter suppressed", func(m *MeterData) { m.MidRow.MSAck = "05.0" }, false},
	}
	for _, tc := range cases {
		next := base
		tc.edit(&next)
		if got := meterChanged(next, base); got != tc.want {
			t.Errorf("%s: meterChanged = %v, want %v", tc.name, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the focused failing SSE tests**

Run:

```bash
go test ./internal/chassis -run 'TestHandleEvents_InitialBurstIncludesMeter|TestMeterChangedGating'
```

Expected: FAIL because `meter` events and `meterChanged` do not exist.

- [ ] **Step 3: Add meter envelope and change gating**

In `internal/chassis/events.go`, add:

```go
type meterEnvelope struct {
	State       string                `json:"state"`
	Paused      bool                  `json:"paused"`
	Generation  uint64                `json:"generation"`
	SourceStrip meterSourceStripE     `json:"sourceStrip"`
	MidRow      meterMidRowE          `json:"midRow"`
	Readout     meterReadoutE         `json:"readout"`
	AudioScopes meterAudioScopesE     `json:"audioScopes"`
}

type meterSourceStripE struct {
	AudioIn           string  `json:"audioIn"`
	AudioOut          string  `json:"audioOut"`
	Src               string  `json:"src"`
	Crop              string  `json:"crop"`
	HLSBuffer         string  `json:"hlsBuffer"`
	HLSCachedSegments int     `json:"hlsCachedSegments"`
	HLSMaxSegments    int     `json:"hlsMaxSegments"`
	HLSCacheBytes     int64   `json:"hlsCacheBytes"`
	Drops             string  `json:"drops"`
	DropsPercent      float64 `json:"dropsPercent"`
	BlitsTotal        uint64  `json:"blitsTotal"`
	UnderrunsTotal    uint64  `json:"underrunsTotal"`
}

type meterMidRowE struct {
	BitrateMbps          string    `json:"bitrateMbps"`
	FreqKHz              string    `json:"freqKHz"`
	Mode                 string    `json:"mode"`
	Standard             string    `json:"standard"`
	FieldOrder           string    `json:"fieldOrder"`
	FieldRateHz          float64   `json:"fieldRateHz"`
	InterlacedOutput     bool      `json:"interlacedOutput"`
	FieldLock            string    `json:"fieldLock"`
	ThroughputMBs        string    `json:"throughputMBs"`
	ThroughputSampleMBs  float64   `json:"throughputSampleMBs"`
	ThroughputHistoryMBs []float64 `json:"throughputHistoryMBs"`
	AckMS                string    `json:"ackMS"`
	AckSampleMS          float64   `json:"ackSampleMS"`
	AckHistoryMS         []float64 `json:"ackHistoryMS"`
}

type meterReadoutE struct {
	Output     string  `json:"output"`
	Aspect     string  `json:"aspect"`
	Pipe       string  `json:"pipe"`
	Speed      string  `json:"speed"`
	SpeedRatio float64 `json:"speedRatio"`
	Link       string  `json:"link"`
}

type meterAudioScopesE struct {
	Status string `json:"status"`
}
```

Add `meterEnvelopeFrom(m MeterData) meterEnvelope` and `meterChanged(curr, last MeterData) bool`. The `meterChanged` implementation compares:

```go
return curr.State != last.State ||
	curr.Paused != last.Paused ||
	curr.Generation != last.Generation ||
	curr.SampleSeq != last.SampleSeq ||
	curr.MidRow.Standard != last.MidRow.Standard ||
	curr.MidRow.FieldOrder != last.MidRow.FieldOrder ||
	curr.MidRow.InterlacedOutput != last.MidRow.InterlacedOutput ||
	curr.Readout.Output != last.Readout.Output ||
	curr.Readout.Aspect != last.Readout.Aspect ||
	curr.Readout.Pipe != last.Readout.Pipe ||
	curr.Readout.Link != last.Readout.Link
```

- [ ] **Step 4: Emit initial and live meter events from cache**

In `handleEvents`, replace the initial direct recompute:

```go
last := s.cache.Get()
```

Keep the existing `source` event in place, then emit meter after transport:

```go
if err := emit(w, "transport", transportEnvelopeFrom(last.Transport)); err != nil {
	return
}
if err := emit(w, "meter", meterEnvelopeFrom(last.Meter)); err != nil {
	return
}
```

In the diff loop, after transport:

```go
if meterChanged(curr.Meter, last.Meter) {
	if err := emit(w, "meter", meterEnvelopeFrom(curr.Meter)); err != nil {
		return
	}
	last.Meter = curr.Meter
} else {
	s.logMeterEmitRefused("unchanged")
}
```

Add a rate-limited debug helper in `events.go`:

```go
func (s *Server) logMeterEmitRefused(reason string) {
	if s.meterRefusalLog == nil {
		return
	}
	if s.meterRefusalLog.Allow(time.Now()) {
		slog.Debug("chassis: meter emit refused", "reason", reason)
	}
}
```

Add a small limiter type in `meter.go`:

```go
type onePerSecondLimiter struct {
	mu   sync.Mutex
	last time.Time
}

func (l *onePerSecondLimiter) Allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last.IsZero() || now.Sub(l.last) >= time.Second {
		l.last = now
		return true
	}
	return false
}
```

Add `meterRefusalLog *onePerSecondLimiter` to `Server` and initialize it in `New`.

- [ ] **Step 5: Run SSE tests**

Run:

```bash
go test ./internal/chassis -run 'TestHandleEvents|TestMeterChanged|TestSnapshotCache'
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/events.go internal/chassis/events_test.go internal/chassis/meter.go internal/chassis/server.go
git commit -m "feat(chassis): stream receiver meter events"
```

---

## Task 9: Meter Template Hooks And Browser Updater

**Files:**
- Modify: `internal/chassis/templates/meter.html`
- Modify: `internal/chassis/templates/shell.html`
- Modify: `internal/chassis/static/vfd-live.js`
- Create: `internal/chassis/static/meter.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing template/static tests**

Append tests to `internal/chassis/chassis_test.go`:

```go
func TestMeterTemplateHasDataHooks(t *testing.T) {
	data := idleSnapshot(nonZeroConfig(), time.Unix(1, 0))
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "meter", data.Meter); err != nil {
		t.Fatalf("execute meter: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		"data-meter-audio-in",
		"data-meter-audio-out",
		"data-meter-src",
		"data-meter-crop",
		"data-meter-hls-buffer",
		"data-meter-drops",
		"data-meter-bitrate",
		"data-meter-freq-khz",
		"data-meter-mode",
		"data-meter-standard-ntsc",
		"data-meter-standard-pal",
		"data-meter-field-lock",
		"data-meter-throughput",
		"data-meter-ack",
		"data-meter-output",
		"data-meter-aspect",
		"data-meter-pipe",
		"data-meter-speed",
		"data-meter-link",
		"data-meter-audio-scopes-status",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("meter template missing %q; body:\n%s", want, body)
		}
	}
}

func TestMeterStaticUsesSubscribeAndNoDemoGenerators(t *testing.T) {
	js, err := chassisStaticFS.ReadFile("static/meter.js")
	if err != nil {
		t.Fatalf("ReadFile(static/meter.js): %v", err)
	}
	body := string(js)
	for _, want := range []string{
		"window.Chassis.events.subscribe('meter'",
		"data-meter-audio-in",
		"data-meter-hls-seg",
		"throughput-canvas",
		"ack-canvas",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("meter.js missing %q", want)
		}
	}
	for _, bad := range []string{"new EventSource", "Math.random", "setInterval(", "demo"} {
		if strings.Contains(body, bad) {
			t.Fatalf("meter.js contains forbidden generator %q", bad)
		}
	}
}

func TestVFDLiveSubscribeHelperContract(t *testing.T) {
	js, err := chassisStaticFS.ReadFile("static/vfd-live.js")
	if err != nil {
		t.Fatalf("ReadFile(static/vfd-live.js): %v", err)
	}
	body := string(js)
	for _, want := range []string{
		"subscribe(eventName, handler)",
		"subscriptions",
		"removeEventListener(eventName, handler)",
		"addEventListener(eventName, handler)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("vfd-live.js missing subscribe contract %q", want)
		}
	}
}
```

- [ ] **Step 2: Run failing static tests**

Run:

```bash
go test ./internal/chassis -run 'TestMeterTemplateHasDataHooks|TestMeterStaticUsesSubscribe|TestVFDLiveSubscribeHelperContract'
```

Expected: FAIL because hooks, `meter.js`, and `subscribe` are missing.

- [ ] **Step 3: Add template hooks**

Update `internal/chassis/templates/meter.html` by adding attributes without changing the visual structure. Representative replacements:

```html
<span class="val" data-meter-audio-in>{{.SourceStrip.AudioIn}}</span>
<span class="seg-text" data-meter-audio-out>{{.SourceStrip.AudioOut}}</span>
<span class="seg-text" data-meter-src>{{.SourceStrip.Src}}</span>
<span class="seg-text" data-meter-crop>{{.SourceStrip.Crop}}</span>
<span class="buf-bar" title="HLS segments cached" data-meter-hls-bar>
  {{range until 12}}<span class="seg" data-meter-hls-seg></span>{{end}}
</span>
<span class="seg-text" data-meter-hls-buffer>{{.SourceStrip.HLSBuffer}}</span>
<span class="seg-text" data-meter-drops>{{.SourceStrip.Drops}}</span>
<span class="seg-text" data-meter-bitrate>{{.MidRow.BitrateMbps}}</span>
<span class="seg-text" data-meter-freq-khz>{{.MidRow.FreqKHz}}</span>
<span class="seg-text" data-meter-mode>{{.MidRow.Mode}}</span>
<span class="std-ind{{if .MidRow.StandardNTSC}} active{{end}} seg-display" data-meter-standard-ntsc>
<span class="std-ind{{if .MidRow.StandardPAL}} active{{end}} seg-display" data-meter-standard-pal>
<span class="lock" data-meter-field-lock>{{.MidRow.FieldFlip}}</span>
<span class="seg-text" data-meter-throughput>{{.MidRow.ThroughputMBs}}</span>
<span class="seg-text" data-meter-ack>{{.MidRow.MSAck}}</span>
<span class="seg-text" data-meter-output>{{.Readout.Output}}</span>
<span class="seg-text" data-meter-aspect>{{.Readout.Aspect}}</span>
<span class="seg-text" data-meter-pipe>{{.Readout.Pipe}}</span>
<span class="seg-text" data-meter-speed>{{.Readout.Speed}}</span>
<span class="seg-text" data-meter-link>{{.Readout.Link}}</span>
<span data-meter-audio-scopes-status="{{.AudioScopes.Status}}"></span>
```

- [ ] **Step 4: Add `subscribe` to `vfd-live.js`**

Modify `internal/chassis/static/vfd-live.js`:

```js
  let source = null;
  const subscriptions = new Map();

  function subscribe(eventName, handler) {
    if (!subscriptions.has(eventName)) {
      subscriptions.set(eventName, new Set());
    }
    const handlers = subscriptions.get(eventName);
    handlers.add(handler);
    if (source) {
      source.removeEventListener(eventName, handler);
      source.addEventListener(eventName, handler);
    }
    return function unsubscribe() {
      handlers.delete(handler);
      if (source) {
        source.removeEventListener(eventName, handler);
      }
    };
  }

  function attachSubscriptions(nextSource) {
    subscriptions.forEach((handlers, eventName) => {
      handlers.forEach((handler) => {
        nextSource.removeEventListener(eventName, handler);
        nextSource.addEventListener(eventName, handler);
      });
    });
  }
```

Inside `connect()`, after built-in listeners:

```js
    attachSubscriptions(source);
```

Expose it:

```js
  Object.assign(window.Chassis.events, {
    subscribe,
    reconnect() {
      if (source) source.close();
      connect();
    },
  });
```

- [ ] **Step 5: Add `meter.js`**

Create `internal/chassis/static/meter.js`:

```js
// Receiver chassis meter telemetry. Spec 5A.
(() => {
  'use strict';

  if (!window.Chassis || !window.Chassis.events || !window.Chassis.events.subscribe) {
    console.warn('meter: subscribe helper missing');
    return;
  }

  let lastGeneration = 0;

  function setText(selector, value) {
    const el = document.querySelector(selector);
    if (el) el.textContent = value == null ? '' : String(value);
  }

  function setLamp(selector, active) {
    const el = document.querySelector(selector);
    if (el) el.classList.toggle('active', !!active);
  }

  function updateHLS(strip) {
    const cached = Math.max(0, Number(strip.hlsCachedSegments || 0));
    const max = Math.max(0, Number(strip.hlsMaxSegments || 0));
    Array.from(document.querySelectorAll('[data-meter-hls-seg]')).forEach((seg, idx) => {
      const threshold = max <= 0 ? 0 : Math.ceil((cached / max) * 12);
      seg.classList.toggle('on', idx < threshold);
    });
  }

  function drawLine(canvasId, values) {
    const canvas = document.getElementById(canvasId);
    if (!canvas || !canvas.getContext) return;
    const ctx = canvas.getContext('2d');
    const width = canvas.width;
    const height = canvas.height;
    ctx.clearRect(0, 0, width, height);
    if (!Array.isArray(values) || values.length === 0) return;
    const max = values.reduce((m, v) => Math.max(m, Number(v) || 0), 1);
    ctx.beginPath();
    values.forEach((v, idx) => {
      const x = values.length === 1 ? width : (idx / (values.length - 1)) * width;
      const y = height - ((Number(v) || 0) / max) * (height - 4) - 2;
      if (idx === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    });
    ctx.strokeStyle = '#7cffb2';
    ctx.lineWidth = 2;
    ctx.stroke();
  }

  function applyMeter(data) {
    const strip = data.sourceStrip || {};
    const mid = data.midRow || {};
    const readout = data.readout || {};
    if (data.generation !== lastGeneration || data.state === 'idle') {
      lastGeneration = data.generation || 0;
    }

    setText('[data-meter-audio-in]', strip.audioIn);
    setText('[data-meter-audio-out]', strip.audioOut);
    setText('[data-meter-src]', strip.src);
    setText('[data-meter-crop]', strip.crop);
    setText('[data-meter-hls-buffer]', strip.hlsBuffer);
    setText('[data-meter-drops]', strip.drops);
    setText('[data-meter-bitrate]', mid.bitrateMbps);
    setText('[data-meter-freq-khz]', mid.freqKHz);
    setText('[data-meter-mode]', mid.mode);
    setLamp('[data-meter-standard-ntsc]', mid.standard === 'ntsc');
    setLamp('[data-meter-standard-pal]', mid.standard === 'pal');
    setText('[data-meter-field-lock]', mid.fieldLock);
    setText('[data-meter-throughput]', mid.throughputMBs);
    setText('[data-meter-ack]', mid.ackMS);
    setText('[data-meter-output]', readout.output);
    setText('[data-meter-aspect]', readout.aspect);
    setText('[data-meter-pipe]', readout.pipe);
    setText('[data-meter-speed]', readout.speed);
    setText('[data-meter-link]', readout.link);
    updateHLS(strip);
    drawLine('throughput-canvas', mid.throughputHistoryMBs);
    drawLine('ack-canvas', mid.ackHistoryMS);
    const scopes = document.querySelector('[data-meter-audio-scopes-status]');
    if (scopes && data.audioScopes) {
      scopes.setAttribute('data-meter-audio-scopes-status', data.audioScopes.status || 'pending');
    }
  }

  window.Chassis.events.subscribe('meter', (ev) => {
    try {
      applyMeter(JSON.parse(ev.data));
    } catch (err) {
      console.warn('meter: bad meter payload', ev.data, err);
    }
  });
})();
```

- [ ] **Step 6: Serve `meter.js`**

Modify `internal/chassis/templates/shell.html`:

```html
  <script defer src="/receiver/static/vfd-live.js?v={{.Version}}"></script>
  <script defer src="/receiver/static/transport.js?v={{.Version}}"></script>
  <script defer src="/receiver/static/visualizer-bank.js?v={{.Version}}"></script>
  <script defer src="/receiver/static/meter.js?v={{.Version}}"></script>
```

- [ ] **Step 7: Run chassis static tests**

Run:

```bash
go test ./internal/chassis -run 'TestMeterTemplateHasDataHooks|TestMeterStaticUsesSubscribe|TestVFDLiveSubscribeHelperContract|TestHandleIndex'
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/chassis/templates/meter.html internal/chassis/templates/shell.html internal/chassis/static/vfd-live.js internal/chassis/static/meter.js internal/chassis/chassis_test.go
git commit -m "feat(chassis): update meter screen from sse"
```

---

## Task 10: Registration, Integration, And Full Verification

**Files:**
- Modify: `internal/adapters/url/adapter_interface_test.go`
- Modify: `internal/adapters/streams/playback_test.go`
- Modify: `internal/chassis/events_test.go`
- Verify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Add provider registration tests**

Append to `internal/adapters/url/adapter_interface_test.go`:

```go
func TestAdapterImplementsMeterOverlayProvider(t *testing.T) {
	var _ adapters.MeterOverlayProvider = (*Adapter)(nil)
}
```

Append to `internal/adapters/streams/playback_test.go`:

```go
func TestAdapterImplementsMeterOverlayProvider(t *testing.T) {
	var _ adapters.MeterOverlayProvider = (*Adapter)(nil)
}
```

- [ ] **Step 2: Add integration-style SSE payload test**

Add this to `internal/chassis/events_test.go`; it exercises the same SSE handler used by `/receiver/events` without needing MiSTer hardware:

```go
func TestReceiverEventsMeterPayloadIncludesLowRateFields(t *testing.T) {
	cfg := nonZeroConfig()
	sv := &mutableSessionViewer{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Integration Meter",
		AdapterRef: "url:meter",
		Source:     "url",
		Generation: 3,
		Meter: core.MeterHomeView{
			Source: core.SourceMeterView{Width: 720, Height: 480, VideoCodec: "h264", AudioCodec: "aac", AudioRate: 48000, AudioChannels: 2},
			Pipeline: core.PipelineMeterView{ModelineName: "NTSC_480i", Standard: "ntsc", FieldOrder: "tff", FieldRateHz: 59.94, HorizontalKHz: 15.7, InterlacedOutput: true, AudioSampleRate: 48000, AudioChannels: 2},
			Runtime: core.RuntimeMeterView{Generation: 3, BlitsTotal: 100, WireBytes: 1000000, LastACKAge: 4 * time.Millisecond},
		},
	}}
	s, err := New(Config{Version: cfg.Version, StartedAt: cfg.StartedAt, Bridge: cfg.Bridge, Session: sv})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := readInitialSSE(t, s)
	if !strings.Contains(body, "event: meter\n") {
		t.Fatalf("missing meter event:\n%s", body)
	}
	for _, want := range []string{`"audioIn":"AAC - STEREO"`, `"fieldRateHz":59.94`, `"interlacedOutput":true`, `"ackMS":"04.0"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("meter payload missing %s:\n%s", want, body)
		}
	}
}
```

- [ ] **Step 3: Run package tests**

Run:

```bash
go test ./internal/ffmpeg ./internal/core ./internal/hlsbuffer ./internal/adapters ./internal/adapters/url ./internal/adapters/streams ./internal/chassis
```

Expected: PASS.

- [ ] **Step 4: Run repository verification**

Run:

```bash
go test ./...
```

Expected: PASS.

Run:

```bash
go vet ./...
```

Expected: PASS.

- [ ] **Step 5: Optional race verification**

Run when the normal suite is green:

```bash
go test -race ./...
```

Expected: PASS. If this is too slow for the implementation session, record that it was not run and run the package-level race tests for touched packages:

```bash
go test -race ./internal/core ./internal/hlsbuffer ./internal/adapters/url ./internal/adapters/streams ./internal/chassis
```

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/url/adapter_interface_test.go internal/adapters/streams/playback_test.go internal/chassis/events_test.go
git commit -m "test(chassis): cover receiver meter telemetry flow"
```

---

## Final Review Checklist

- [ ] `git diff --name-only main...HEAD` contains only files listed in this plan.
- [ ] `rg -n "Math.random|setInterval\\(|new EventSource" internal/chassis/static/meter.js` returns no matches.
- [ ] `rg -n "http://|https://|://|token=|sig=|secret|Authorization" internal/adapters/*/*meter* internal/chassis/*meter*` finds only test fixtures and leak assertions, not serialized production payload fields.
- [ ] `go test ./internal/ffmpeg ./internal/core ./internal/hlsbuffer ./internal/adapters ./internal/adapters/url ./internal/adapters/streams ./internal/chassis` passes.
- [ ] `go test ./...` passes.
- [ ] `go vet ./...` passes.
- [ ] Manual browser check: `/receiver` during a live cast shows real source/HLS/network/ACK/readout fields within roughly 500 ms.
- [ ] Manual browser check: two `/receiver` tabs do not create extra server-side `StatusHomeView()` sampling beyond the shared refresher cadence.
- [ ] Manual browser check: stopping a cast clears HLS lamps, throughput/ACK histories, and low-rate text back to idle placeholders.

## Self-Review Notes

- Spec coverage: Tasks 1-2 cover core/probe facts; Tasks 3-6 cover safe HLS overlays; Tasks 7-8 cover chassis sampler/SSE/fan-out; Task 9 covers browser rendering and quiet audio scopes; Task 10 covers integration and route preservation.
- Type consistency: `core.MeterHomeView` feeds `chassis.meterSampler.Sample`; adapter overlays use `adapters.MeterOverlay`; SSE emits `meterEnvelope`.
- Scope: `/ui/*` code is not edited. Existing `source` SSE stays in the initial burst before `visualizer`; `meter` is appended after `transport`.
