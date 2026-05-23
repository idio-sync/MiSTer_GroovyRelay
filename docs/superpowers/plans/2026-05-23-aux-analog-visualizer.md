# AUX Analog Visualizer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a manual `AUX` receiver source that can drive the existing CRT visualizer from an external analog audio signal, using either a relay-local FFmpeg capture input or a validated remote HTTP(S) audio stream.

**Architecture:** `internal/adapters/aux` owns AUX configuration, proxy tokens, URL validation, and core session construction. `internal/chassis` owns `/receiver/aux/*` same-origin routes and source-cluster UI through a narrow `AUXStarter` interface. `internal/core`, `internal/ffmpeg`, and `internal/dataplane` gain adapter-agnostic capture/probe/audio-output contracts so AUX can reuse the existing visualizer renderer without importing adapter packages across layers.

**Tech Stack:** Go standard library HTTP, BurntSushi TOML, existing adapter registry, existing chassis templates/static assets, existing FFmpeg command builder and Groovy data plane.

---

## Source Spec

- Design: `docs/superpowers/specs/2026-05-22-aux-analog-visualizer-design.md`
- Current core visualizer path: `internal/core/manager.go`, `internal/ffmpeg/pipeline.go`, `internal/dataplane/plane.go`
- Current chassis route/style pattern: `internal/chassis/visualizer.go`, `internal/chassis/server.go`, `internal/chassis/templates/source-cluster.html`, `internal/chassis/static/chassis.js`
- Current adapter pattern: `internal/adapters/url`, `internal/adapters/dlna`, `internal/adapters/adapter.go`

## File Structure

New files:

```text
internal/adapters/aux/adapter.go
internal/adapters/aux/config.go
internal/adapters/aux/proxy.go
internal/adapters/aux/session.go
internal/adapters/aux/url.go
internal/adapters/aux/adapter_test.go
internal/adapters/aux/config_test.go
internal/adapters/aux/proxy_test.go
internal/adapters/aux/session_test.go
internal/adapters/aux/url_test.go
internal/adapters/aux_contract.go
internal/chassis/aux.go
internal/chassis/aux_test.go
internal/ffmpeg/input.go
internal/ffmpeg/input_test.go
```

Modified files:

```text
cmd/mister-groovy-relay/main.go
internal/chassis/chassis_test.go
internal/chassis/css_scope_test.go
internal/chassis/data.go
internal/chassis/events.go
internal/chassis/events_test.go
internal/chassis/import_check_test.go
internal/chassis/server.go
internal/chassis/session.go
internal/chassis/static/chassis.css
internal/chassis/static/chassis.js
internal/chassis/static/vfd-live.js
internal/chassis/templates/source-cluster.html
internal/config/example.toml
internal/core/manager.go
internal/core/manager_test.go
internal/core/types.go
internal/core/types_test.go
internal/dataplane/plane.go
internal/dataplane/plane_test.go
internal/ffmpeg/pipeline.go
internal/ffmpeg/pipeline_test.go
internal/ffmpeg/probe.go
internal/ffmpeg/probe_test.go
README.md
```

## Worker Notes

- Before editing, run `git status --short`. At plan creation time `.gitignore` and `README.md` already had user changes. Do not overwrite them. When Task 12 edits `README.md`, inspect and preserve the current diff first.
- Use `apply_patch` for manual edits.
- Run `gofmt` after each Go task.
- Keep commits narrow. Suggested commit points are listed after tasks.
- Chassis must not import `internal/adapters/aux`; keep coupling through interfaces declared in `internal/chassis` and shared neutral contracts in `internal/adapters`.
- The AUX proxy path is adapter-owned and mounted through `adapters.PublicRouteProvider` at `/internal/aux-proxy/`. The chassis start/stop routes are chassis-owned at `/receiver/aux/*`.

## Task 1: Add Core Session Input Contract

- [ ] Add failing tests in `internal/core/types_test.go` and `internal/core/manager_test.go`.
- [ ] Add `AudioCaptureInput`, `AudioOutputMode`, `StreamProbeURL`, `AudioCapture`, and `AudioOutputMode` to `internal/core/types.go`.
- [ ] Extend session validation in `internal/core/manager.go`.
- [ ] Extend `redactURL` to redact `aux_token`.
- [ ] Run focused core tests.

Tests to add:

```go
func TestValidateSessionRequestRejectsInvalidInputShapes(t *testing.T) {
	tests := []struct {
		name string
		req  SessionRequest
		want string
	}{
		{
			name: "stream probe without stream url",
			req: SessionRequest{
				StreamProbeURL: "http://127.0.0.1:32500/internal/aux-proxy/?aux_token=probe",
			},
			want: "stream_probe_url requires stream_url",
		},
		{
			name: "stream and capture both set",
			req: SessionRequest{
				StreamURL: "http://127.0.0.1:32500/internal/aux-proxy/?aux_token=play",
				AudioCapture: AudioCaptureInput{
					Enabled:    true,
					Format:     "alsa",
					Device:     "hw:1,0",
					SampleRate: 48000,
					Channels:   2,
				},
			},
			want: "exactly one media input",
		},
		{
			name: "secondary audio stream and capture both set",
			req: SessionRequest{
				AudioStreamURL: "http://example.test/audio-only.m4a",
				AudioCapture: AudioCaptureInput{
					Enabled:    true,
					Format:     "alsa",
					Device:     "hw:1,0",
					SampleRate: 48000,
					Channels:   2,
				},
			},
			want: "exactly one media input",
		},
		{
			name: "capture missing format",
			req: SessionRequest{
				AudioCapture: AudioCaptureInput{
					Enabled:    true,
					Device:     "hw:1,0",
					SampleRate: 48000,
					Channels:   2,
				},
			},
			want: "audio_capture.format is required",
		},
		{
			name: "capture missing device",
			req: SessionRequest{
				AudioCapture: AudioCaptureInput{
					Enabled:    true,
					Format:     "alsa",
					SampleRate: 48000,
					Channels:   2,
				},
			},
			want: "audio_capture.device is required",
		},
		{
			name: "bad output mode",
			req: SessionRequest{
				StreamURL:       "http://example.test/a.wav",
				AudioOutputMode: AudioOutputMode("speaker"),
			},
			want: "audio_output_mode must be visual_only or monitor",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSessionRequest(tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateSessionRequest() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateSessionRequestAcceptsStreamAndCaptureShapes(t *testing.T) {
	streamReq := SessionRequest{
		StreamURL:       "http://127.0.0.1:32500/internal/aux-proxy/?aux_token=play",
		StreamProbeURL:  "http://127.0.0.1:32500/internal/aux-proxy/?aux_token=probe",
		AudioOutputMode: AudioOutputVisualOnly,
	}
	if err := validateSessionRequest(streamReq); err != nil {
		t.Fatalf("stream shape rejected: %v", err)
	}

	captureReq := SessionRequest{
		AudioCapture: AudioCaptureInput{
			Enabled:    true,
			Format:     "alsa",
			Device:     "hw:1,0",
			SampleRate: 48000,
			Channels:   2,
		},
		AudioOutputMode: AudioOutputMonitor,
	}
	if err := validateSessionRequest(captureReq); err != nil {
		t.Fatalf("capture shape rejected: %v", err)
	}
}

func TestRedactURLRedactsAuxToken(t *testing.T) {
	raw := "http://127.0.0.1:32500/internal/aux-proxy/?aux_token=secret&x=1"
	got := redactURL(raw)
	if strings.Contains(got, "secret") {
		t.Fatalf("redactURL leaked aux token: %s", got)
	}
	if !strings.Contains(got, "aux_token=REDACTED") {
		t.Fatalf("redactURL = %s, want aux_token redacted", got)
	}
}
```

Implementation shape:

```go
type AudioCaptureInput struct {
	Enabled         bool
	Format          string
	Device          string
	SampleRate      int
	Channels        int
	ThreadQueueSize int
	AnalyzeDuration time.Duration
	ProbeSize       int
}

type AudioOutputMode string

const (
	AudioOutputDefault    AudioOutputMode = ""
	AudioOutputVisualOnly AudioOutputMode = "visual_only"
	AudioOutputMonitor    AudioOutputMode = "monitor"
)
```

Validation rules in `validateSessionInputShape(req SessionRequest) error`:

```go
hasStream := req.StreamURL != "" || req.StreamProbeURL != "" || req.AudioStreamURL != ""
hasCapture := req.AudioCapture.Enabled
switch {
case req.StreamProbeURL != "" && req.StreamURL == "":
	return fmt.Errorf("stream_probe_url requires stream_url")
case hasStream && hasCapture:
	return fmt.Errorf("exactly one media input may be set")
case !hasStream && !hasCapture:
	return fmt.Errorf("stream_url or audio_capture is required")
}
if hasCapture {
	c := req.AudioCapture
	if strings.TrimSpace(c.Format) == "" {
		return fmt.Errorf("audio_capture.format is required")
	}
	if strings.TrimSpace(c.Device) == "" {
		return fmt.Errorf("audio_capture.device is required")
	}
	if c.SampleRate <= 0 {
		return fmt.Errorf("audio_capture.sample_rate must be positive")
	}
	if c.Channels != 1 && c.Channels != 2 {
		return fmt.Errorf("audio_capture.channels must be 1 or 2")
	}
}
switch req.AudioOutputMode {
case AudioOutputDefault, AudioOutputVisualOnly, AudioOutputMonitor:
	return nil
default:
	return fmt.Errorf("audio_output_mode must be visual_only or monitor, got %q", req.AudioOutputMode)
}
```

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\gofmt.exe -w internal/core/types.go internal/core/types_test.go internal/core/manager.go internal/core/manager_test.go
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/core
```

Commit:

```bash
git add internal/core/types.go internal/core/types_test.go internal/core/manager.go internal/core/manager_test.go
git commit -m "feat(aux): add core capture session contract"
```

## Task 2: Add FFmpeg Input-Shaped Probe And Capture Args

- [ ] Add `internal/ffmpeg/input.go` and tests.
- [ ] Add `CaptureInputSpec` and `ProbeInputSpec`.
- [ ] Make `Probe` delegate to `ProbeInput` for backward compatibility.
- [ ] Add capture input support to `PipelineSpec` and `BuildCommand`.
- [ ] Keep local capture arguments structured; do not build shell strings.

Tests to add:

```go
func TestBuildCommandCaptureInputArgsPrecedeInput(t *testing.T) {
	spec := PipelineSpec{
		CaptureInput: CaptureInputSpec{
			Enabled:         true,
			Format:          "alsa",
			Device:          "hw:1,0",
			SampleRate:      48000,
			Channels:        2,
			ThreadQueueSize: 64,
			AnalyzeDuration: 100 * time.Millisecond,
			ProbeSize:       32768,
		},
		Visualizer: VisualizerSpec{
			Enabled:                  true,
			Mode:                     VisualizerModeRetroAnalyzer,
			RequiredFiltersAvailable: true,
		},
		SourceProbe:     &ProbeResult{AudioRate: 48000},
		OutputWidth:     720,
		OutputHeight:    480,
		AudioSampleRate: 48000,
		AudioChannels:   2,
		VideoPipePath:   "pipe:3",
		AudioPipePath:   "pipe:4",
		FFmpegPath:      "ffmpeg",
	}
	args := BuildCommand(context.Background(), spec).Args
	wantOrder := []string{
		"-thread_queue_size", "64",
		"-f", "alsa",
		"-sample_rate", "48000",
		"-channels", "2",
		"-analyzeduration", "100000",
		"-probesize", "32768",
		"-i", "hw:1,0",
	}
	assertArgsContainSubsequence(t, args, wantOrder)
}

func TestProbeInputURLAppliesPolicyBeforeURL(t *testing.T) {
	cmd := probeCommand("ffprobe", ProbeInputSpec{
		URL: "http://127.0.0.1:32500/internal/aux-proxy/?aux_token=probe",
		Policy: MediaInputPolicy{
			ProtocolWhitelist: []string{"http", "tcp"},
			DisableReconnect: true,
			RWTimeout:        2 * time.Second,
		},
	})
	assertArgsContainSubsequence(t, cmd.Args, []string{
		"-protocol_whitelist", "http,tcp",
		"-reconnect", "0",
		"-reconnect_at_eof", "0",
		"-reconnect_streamed", "0",
		"-reconnect_on_network_error", "0",
		"-rw_timeout", "2000000",
		"http://127.0.0.1:32500/internal/aux-proxy/?aux_token=probe",
	})
}

func TestProbeInputCaptureUsesStructuredArgs(t *testing.T) {
	cmd := probeCommand("ffprobe", ProbeInputSpec{
		Capture: CaptureInputSpec{
			Enabled:         true,
			Format:          "dshow",
			Device:          `audio=Line In (USB Audio Device)`,
			SampleRate:      48000,
			Channels:        2,
			ThreadQueueSize: 64,
			AnalyzeDuration: 100 * time.Millisecond,
			ProbeSize:       32768,
		},
	})
	assertArgsContainSubsequence(t, cmd.Args, []string{
		"-thread_queue_size", "64",
		"-f", "dshow",
		"-sample_rate", "48000",
		"-channels", "2",
		"-analyzeduration", "100000",
		"-probesize", "32768",
		"-i", `audio=Line In (USB Audio Device)`,
	})
}
```

Implementation shape:

```go
type CaptureInputSpec struct {
	Enabled         bool
	Format          string
	Device          string
	SampleRate      int
	Channels        int
	ThreadQueueSize int
	AnalyzeDuration time.Duration
	ProbeSize       int
}

type ProbeInputSpec struct {
	URL     string
	Policy  MediaInputPolicy
	Capture CaptureInputSpec
}

func appendCaptureInputArgs(args []string, c CaptureInputSpec) []string {
	if c.ThreadQueueSize > 0 {
		args = append(args, "-thread_queue_size", fmt.Sprintf("%d", c.ThreadQueueSize))
	}
	if c.Format != "" {
		args = append(args, "-f", c.Format)
	}
	if c.SampleRate > 0 {
		args = append(args, "-sample_rate", fmt.Sprintf("%d", c.SampleRate))
	}
	if c.Channels > 0 {
		args = append(args, "-channels", fmt.Sprintf("%d", c.Channels))
	}
	if c.AnalyzeDuration > 0 {
		args = append(args, "-analyzeduration", fmt.Sprintf("%d", c.AnalyzeDuration.Microseconds()))
	}
	if c.ProbeSize > 0 {
		args = append(args, "-probesize", fmt.Sprintf("%d", c.ProbeSize))
	}
	return append(args, "-i", c.Device)
}
```

Add `probeCommand(ffprobePath string, input ProbeInputSpec) *exec.Cmd` so tests can inspect argv without spawning ffprobe. `ProbeInput` calls `probeCommand`, sets `WaitDelay`, runs `cmd.Output()`, and calls `parseProbeOutput`.

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\gofmt.exe -w internal/ffmpeg/input.go internal/ffmpeg/input_test.go internal/ffmpeg/probe.go internal/ffmpeg/probe_test.go internal/ffmpeg/pipeline.go internal/ffmpeg/pipeline_test.go
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/ffmpeg
```

Commit:

```bash
git add internal/ffmpeg/input.go internal/ffmpeg/input_test.go internal/ffmpeg/probe.go internal/ffmpeg/probe_test.go internal/ffmpeg/pipeline.go internal/ffmpeg/pipeline_test.go
git commit -m "feat(aux): support capture inputs in ffmpeg pipeline"
```

## Task 3: Wire Core Probe Selection And Audio Output Mode

- [ ] Replace the core probe indirection with an input-shaped probe indirection.
- [ ] Use `StreamProbeURL` for probe and `StreamURL` for playback.
- [ ] Relax the visualizer audio gate for configured local capture.
- [ ] Normalize local-capture monitor audio from capture config when probe reports zero audio rate.
- [ ] Set `SuppressAudioOutput` for visual-only sessions.

Core tests:

```go
func TestManagerProbeForStartUsesStreamProbeURLWhenPresent(t *testing.T) {
	var got ffmpeg.ProbeInputSpec
	restore := stubProbeInput(func(ctx context.Context, ffprobePath string, input ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		got = input
		return &ffmpeg.ProbeResult{AudioRate: 48000}, nil
	})
	defer restore()

	m := newTestManager(t)
	_, _, _, err := m.probeForStart(SessionRequest{
		StreamURL:      "http://127.0.0.1:32500/internal/aux-proxy/?aux_token=play",
		StreamProbeURL: "http://127.0.0.1:32500/internal/aux-proxy/?aux_token=probe",
		MediaKind:      MediaKindMusic,
		Visualizer:     VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
	})
	if err != nil {
		t.Fatalf("probeForStart: %v", err)
	}
	if got.URL == "" || !strings.Contains(got.URL, "aux_token=probe") {
		t.Fatalf("probe used URL %q, want probe token URL", got.URL)
	}
}

func TestManagerVisualizerCaptureAcceptsZeroProbeAudioRate(t *testing.T) {
	restore := stubProbeInput(func(ctx context.Context, ffprobePath string, input ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 0}, nil
	})
	defer restore()

	var got dataplane.PlaneConfig
	restorePlane := stubNewPlane(func(cfg dataplane.PlaneConfig) planeRunner {
		got = cfg
		return newNoopPlane()
	})
	defer restorePlane()

	m := newTestManager(t)
	err := m.StartSession(SessionRequest{
		MediaKind:       MediaKindMusic,
		AudioOutputMode: AudioOutputMonitor,
		AudioCapture: AudioCaptureInput{
			Enabled:    true,
			Format:     "alsa",
			Device:     "hw:1,0",
			SampleRate: 48000,
			Channels:   2,
		},
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if got.AudioRate != 48000 || got.AudioChans != 2 {
		t.Fatalf("PlaneConfig audio = %d/%d, want 48000/2", got.AudioRate, got.AudioChans)
	}
}

func TestManagerVisualOnlySuppressesFFmpegAndPlaneAudio(t *testing.T) {
	restore := stubProbeInput(func(ctx context.Context, ffprobePath string, input ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 48000}, nil
	})
	defer restore()

	var got dataplane.PlaneConfig
	restorePlane := stubNewPlane(func(cfg dataplane.PlaneConfig) planeRunner {
		got = cfg
		return newNoopPlane()
	})
	defer restorePlane()

	m := newTestManager(t)
	err := m.StartSession(SessionRequest{
		StreamURL:       "http://example.test/aux.wav",
		MediaKind:       MediaKindMusic,
		AudioOutputMode: AudioOutputVisualOnly,
		Visualizer:      VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if !got.SpawnSpec.SuppressAudioOutput {
		t.Fatal("SpawnSpec.SuppressAudioOutput = false, want true")
	}
	if !got.SuppressAudioOutput {
		t.Fatal("PlaneConfig.SuppressAudioOutput = false, want true")
	}
}
```

Implementation points:

- Change `probeFn` to:

```go
probeInputFn = func(ctx context.Context, ffprobePath string, input ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
	return ffmpeg.ProbeInput(ctx, ffprobePath, input)
}
```

- Build `ffmpeg.ProbeInputSpec` in `probeForStart`:

```go
probeURL := req.StreamURL
if req.StreamProbeURL != "" {
	probeURL = req.StreamProbeURL
}
probeInput := ffmpeg.ProbeInputSpec{
	URL:     probeURL,
	Policy:  req.MediaInputPolicy,
	Capture: ffmpegCaptureSpec(req.AudioCapture),
}
probe, err := probeInputFn(ctx, ffprobePath, probeInput)
```

- Visualizer audio gate:

```go
if req.Visualizer.Enabled && !req.AudioCapture.Enabled {
	if probe == nil || probe.AudioRate <= 0 {
		return nil, nil, "", fmt.Errorf("visualizer source has no audio")
	}
}
```

- Add helper for effective audio:

```go
func effectiveSessionAudio(req SessionRequest, bridge config.AudioConfig, probe *ffmpeg.ProbeResult) (rate, chans int, suppress bool, normalizedProbe *ffmpeg.ProbeResult) {
	if req.AudioOutputMode == AudioOutputVisualOnly {
		return 0, 0, true, probe
	}
	rate, chans = bridge.SampleRate, bridge.Channels
	if req.AudioCapture.Enabled {
		rate, chans = req.AudioCapture.SampleRate, req.AudioCapture.Channels
		if probe != nil && probe.AudioRate <= 0 {
			cp := *probe
			cp.AudioRate = rate
			probe = &cp
		}
	}
	return rate, chans, false, probe
}
```

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\gofmt.exe -w internal/core/manager.go internal/core/manager_test.go
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/core ./internal/ffmpeg
```

Commit:

```bash
git add internal/core/manager.go internal/core/manager_test.go
git commit -m "feat(aux): wire core capture probe and audio modes"
```

## Task 4: Disable Data-Plane Audio In Visual-Only Mode

- [ ] Add `SuppressAudioOutput bool` to `dataplane.PlaneConfig`.
- [ ] Make `effectiveAudioConfig` return `0,0` when suppressed.
- [ ] Add tests that visual-only skips audio reader and advertises `AudioRateOff`.
- [ ] Ensure monitor mode still starts PCM reader when capture probe audio was normalized.

Tests:

```go
func TestEffectiveAudioConfigSuppressAudioOutput(t *testing.T) {
	p := NewPlane(PlaneConfig{
		SpawnSpec: ffmpeg.PipelineSpec{SourceProbe: &ffmpeg.ProbeResult{AudioRate: 48000}},
		AudioRate: 48000,
		AudioChans: 2,
		SuppressAudioOutput: true,
	})
	rate, chans := p.effectiveAudioConfig()
	if rate != 0 || chans != 0 {
		t.Fatalf("effectiveAudioConfig() = %d/%d, want 0/0", rate, chans)
	}
}

func TestEffectiveAudioConfigCaptureMonitorWithNormalizedProbe(t *testing.T) {
	p := NewPlane(PlaneConfig{
		SpawnSpec: ffmpeg.PipelineSpec{
			CaptureInput: ffmpeg.CaptureInputSpec{Enabled: true},
			SourceProbe: &ffmpeg.ProbeResult{AudioRate: 48000},
		},
		AudioRate: 48000,
		AudioChans: 2,
	})
	rate, chans := p.effectiveAudioConfig()
	if rate != 48000 || chans != 2 {
		t.Fatalf("effectiveAudioConfig() = %d/%d, want 48000/2", rate, chans)
	}
}
```

Implementation:

- Add `SuppressAudioOutput bool` to `PlaneConfig` next to `AudioRate` and `AudioChans`.
- In `effectiveAudioConfig`, return `0, 0` before the existing `AudioRate` / `AudioChans` checks when `p.cfg.SuppressAudioOutput` is true.
- Leave the existing DASH dual-input exception intact.

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\gofmt.exe -w internal/dataplane/plane.go internal/dataplane/plane_test.go
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/dataplane
```

Commit:

```bash
git add internal/dataplane/plane.go internal/dataplane/plane_test.go
git commit -m "feat(aux): support visual-only dataplane audio"
```

## Task 5: Add AUX Config And Adapter Skeleton

- [ ] Create `internal/adapters/aux/config.go`.
- [ ] Create `internal/adapters/aux/adapter.go`.
- [ ] Add config/default/field/apply/status tests.
- [ ] Ensure `enabled=false` allows incomplete input config.
- [ ] Ensure `enabled=true` requires a configured input.
- [ ] Ensure fields use dotted keys compatible with `internal/ui/adapter.go`.

Config shape:

```go
package aux

type Config struct {
	Enabled bool     `toml:"enabled"`
	Input   AUXInput `toml:"input"`
}

type AUXInput struct {
	ID                    string `toml:"id"`
	Name                  string `toml:"name"`
	Mode                  string `toml:"mode"`
	AudioOutput           string `toml:"audio_output"`
	URL                   string `toml:"url"`
	Format                string `toml:"format"`
	Device                string `toml:"device"`
	SampleRate            int    `toml:"sample_rate"`
	Channels              int    `toml:"channels"`
	ThreadQueueSize       int    `toml:"thread_queue_size"`
	AnalyzeDurationMillis int    `toml:"analyze_duration_ms"`
	ProbeSize             int    `toml:"probe_size"`
}

const (
	ModeStreamURL    = "stream_url"
	ModeLocalCapture = "local_capture"

	AudioOutputVisualOnly = "visual_only"
	AudioOutputMonitor    = "monitor"
)
```

Defaults:

```go
func DefaultConfig() Config {
	return Config{
		Enabled: false,
		Input: AUXInput{
			ID:                    "aux",
			Name:                  "AUX",
			Mode:                  ModeStreamURL,
			AudioOutput:           AudioOutputVisualOnly,
			ThreadQueueSize:       64,
			AnalyzeDurationMillis: 100,
			ProbeSize:             32768,
		},
	}
}
```

Adapter skeleton:

```go
type SessionManager interface {
	StartSession(core.SessionRequest) error
	StopIfAdapterRef(string) (bool, error)
	Status() core.SessionStatus
}

type Adapter struct {
	core      SessionManager
	bridge    config.BridgeConfig
	httpPort  int
	eventLog  *eventlog.Log
	now       func() time.Time
	mu        sync.Mutex
	cfg       Config
	state     adapters.State
	lastErr   string
	stateSince time.Time
	activeRef string
}
```

Fields:

```go
func (a *Adapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{Key: "enabled", Label: "Enabled", Kind: adapters.KindBool, Default: false, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.id", Label: "Input ID", Kind: adapters.KindText, Default: "aux", Required: true, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.name", Label: "Name", Kind: adapters.KindText, Default: "AUX", Required: true, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.mode", Label: "Mode", Kind: adapters.KindEnum, Enum: []string{ModeStreamURL, ModeLocalCapture}, Default: ModeStreamURL, Required: true, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.audio_output", Label: "Audio Output", Kind: adapters.KindEnum, Enum: []string{AudioOutputVisualOnly, AudioOutputMonitor}, Default: AudioOutputVisualOnly, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.url", Label: "Stream URL", Kind: adapters.KindText, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.format", Label: "Capture Format", Kind: adapters.KindText, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.device", Label: "Capture Device", Kind: adapters.KindText, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.sample_rate", Label: "Sample Rate", Kind: adapters.KindInt, Default: a.bridge.Audio.SampleRate, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.channels", Label: "Channels", Kind: adapters.KindInt, Default: a.bridge.Audio.Channels, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.thread_queue_size", Label: "Thread Queue", Kind: adapters.KindInt, Default: 64, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.analyze_duration_ms", Label: "Analyze Duration", Kind: adapters.KindInt, Default: 100, ApplyScope: adapters.ScopeHotSwap},
		{Key: "input.probe_size", Label: "Probe Size", Kind: adapters.KindInt, Default: 32768, ApplyScope: adapters.ScopeHotSwap},
	}
}
```

`Start(ctx)` only sets runtime state to running when enabled; it must not start playback. `Stop()` must set state stopped and ownership-stop only the active AUX session through `StopIfAdapterRef`.

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\gofmt.exe -w internal/adapters/aux/adapter.go internal/adapters/aux/config.go internal/adapters/aux/adapter_test.go internal/adapters/aux/config_test.go
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/aux
```

Commit:

```bash
git add internal/adapters/aux
git commit -m "feat(aux): add analog input adapter config"
```

## Task 6: Add AUX Stream URL Validator And Proxy

- [ ] Create `internal/adapters/aux/url.go`.
- [ ] Create `internal/adapters/aux/proxy.go`.
- [ ] Implement `adapters.PublicRouteProvider`.
- [ ] Mint distinct single-use probe/play tokens.
- [ ] Reject non-loopback clients before token lookup.
- [ ] Reject redirects and non-2xx upstream responses.
- [ ] Use no operator-configurable headers.

URL validation tests:

```go
func TestValidateStreamURLRejectsUnsafeShapes(t *testing.T) {
	for _, raw := range []string{
		"",
		"file:///tmp/a.wav",
		"udp://239.0.0.1:5004",
		"http://user:pass@example.test/a.wav",
		"http:///missing-host",
		"http://example.test/a.wav#frag",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := validateStreamURL(raw); err == nil {
				t.Fatalf("validateStreamURL(%q) succeeded, want error", raw)
			}
		})
	}
}

func TestValidateStreamURLAcceptsHTTPAndHTTPS(t *testing.T) {
	for _, raw := range []string{"http://capture-host:8090/aux.wav", "https://capture.example/aux.wav"} {
		if _, err := validateStreamURL(raw); err != nil {
			t.Fatalf("validateStreamURL(%q): %v", raw, err)
		}
	}
}
```

Proxy contracts:

```go
type proxyTokenKind string

const (
	proxyTokenProbe proxyTokenKind = "probe"
	proxyTokenPlay  proxyTokenKind = "play"
)

type proxyToken struct {
	token     string
	kind      proxyTokenKind
	upstream  string
	expiresAt time.Time
	used      bool
	cancel    context.CancelFunc
}

type proxyStore struct {
	mu     sync.Mutex
	tokens []proxyToken
	now    func() time.Time
}
```

Use slice lookup so token comparison can be constant-time:

```go
func (s *proxyStore) consume(raw string, kind proxyTokenKind) (proxyToken, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for i := range s.tokens {
		tok := &s.tokens[i]
		if subtle.ConstantTimeCompare([]byte(tok.token), []byte(raw)) != 1 {
			continue
		}
		if tok.used || tok.kind != kind || now.After(tok.expiresAt) {
			return proxyToken{}, false
		}
		tok.used = true
		return *tok, true
	}
	return proxyToken{}, false
}
```

Handler rules:

```go
func (a *Adapter) MountPublicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /internal/aux-proxy/", a.handleProxy)
}

func (a *Adapter) handleProxy(w http.ResponseWriter, r *http.Request) {
	if !remoteAddrIsLoopback(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	rawToken := r.URL.Query().Get("aux_token")
	kind := proxyTokenKind(r.URL.Query().Get("kind"))
	if kind == "" {
		kind = proxyTokenPlay
	}
	tok, ok := a.proxy.consume(rawToken, kind)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tok.upstream, nil)
	if err != nil {
		http.Error(w, "bad upstream", http.StatusBadGateway)
		return
	}
	resp, err := a.proxyHTTP.Do(req)
	if err != nil {
		http.Error(w, "AUX input unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		http.Error(w, "AUX input redirected", http.StatusBadGateway)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Error(w, "AUX input returned non-2xx status", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	_, _ = io.Copy(w, resp.Body)
}
```

The generated local URLs must be:

```go
fmt.Sprintf("http://127.0.0.1:%d/internal/aux-proxy/?kind=%s&aux_token=%s", a.httpPort, kind, url.QueryEscape(token))
```

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\gofmt.exe -w internal/adapters/aux/proxy.go internal/adapters/aux/proxy_test.go internal/adapters/aux/url.go internal/adapters/aux/url_test.go
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/aux
```

Commit:

```bash
git add internal/adapters/aux
git commit -m "feat(aux): add validated stream proxy"
```

## Task 7: Build AUX Session Start/Stop

- [ ] Add `internal/adapters/aux/session.go`.
- [ ] Implement `AUXStatus`, `StartAUX`, and `StopAUX` using `adapters.AUXStatus`.
- [ ] Build stream-url sessions with separate probe/play proxy URLs.
- [ ] Build local-capture sessions with structured capture input.
- [ ] Normalize sample rate/channels from bridge defaults when AUX fields are zero.
- [ ] Record clear adapter status errors on start failures.
- [ ] Release proxy tokens when `StartSession` fails and from the session `OnStop` path when the AUX session ends.

Public interface methods:

```go
func (a *Adapter) AUXStatus(ctx context.Context) adapters.AUXStatus
func (a *Adapter) StartAUX(ctx context.Context, inputID string) (string, error)
func (a *Adapter) StopAUX(ctx context.Context, inputID string) (bool, error)
```

Session construction tests:

```go
func TestStartAUXStreamURLBuildsProbeAndPlayURLs(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	a.mustApplyConfig(t, Config{
		Enabled: true,
		Input: AUXInput{
			ID: "aux", Name: "Analog In", Mode: ModeStreamURL,
			AudioOutput: AudioOutputVisualOnly,
			URL: "http://capture-host:8090/aux.wav",
			ThreadQueueSize: 64,
			AnalyzeDurationMillis: 100,
			ProbeSize: 32768,
		},
	})

	ref, err := a.StartAUX(context.Background(), "")
	if err != nil {
		t.Fatalf("StartAUX: %v", err)
	}
	req := fc.lastRequest
	if ref != "aux:aux" || req.AdapterRef != "aux:aux" || req.Source != "aux" {
		t.Fatalf("bad ref/source: ref=%q req=%+v", ref, req)
	}
	if req.StreamProbeURL == "" || req.StreamURL == "" || req.StreamProbeURL == req.StreamURL {
		t.Fatalf("probe/play URLs not distinct: probe=%q play=%q", req.StreamProbeURL, req.StreamURL)
	}
	if req.AudioOutputMode != core.AudioOutputVisualOnly {
		t.Fatalf("AudioOutputMode = %q", req.AudioOutputMode)
	}
	if !req.Visualizer.Enabled || req.MediaKind != core.MediaKindMusic {
		t.Fatalf("not a music visualizer request: %+v", req)
	}
}

func TestStartAUXLocalCaptureBuildsCaptureRequest(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	a.mustApplyConfig(t, Config{
		Enabled: true,
		Input: AUXInput{
			ID: "aux", Name: "Line In", Mode: ModeLocalCapture,
			AudioOutput: AudioOutputMonitor,
			Format: "alsa", Device: "hw:1,0",
			SampleRate: 48000, Channels: 2,
			ThreadQueueSize: 64, AnalyzeDurationMillis: 100, ProbeSize: 32768,
		},
	})

	_, err := a.StartAUX(context.Background(), "aux")
	if err != nil {
		t.Fatalf("StartAUX: %v", err)
	}
	req := fc.lastRequest
	if req.StreamURL != "" || req.StreamProbeURL != "" {
		t.Fatalf("local capture set stream URLs: %+v", req)
	}
	if !req.AudioCapture.Enabled || req.AudioCapture.Format != "alsa" || req.AudioCapture.Device != "hw:1,0" {
		t.Fatalf("bad capture request: %+v", req.AudioCapture)
	}
	if req.AudioOutputMode != core.AudioOutputMonitor {
		t.Fatalf("AudioOutputMode = %q", req.AudioOutputMode)
	}
}

func TestStopAUXDoesNotStopForeignSession(t *testing.T) {
	fc := &fakeCore{status: core.SessionStatus{AdapterRef: "plex:/library/metadata/42"}}
	a := newTestAdapter(t, fc)
	a.mustApplyConfig(t, DefaultConfig())
	matched, err := a.StopAUX(context.Background(), "")
	if err != nil {
		t.Fatalf("StopAUX: %v", err)
	}
	if matched || fc.stopCalls != 0 {
		t.Fatalf("foreign stop matched=%v calls=%d", matched, fc.stopCalls)
	}
}
```

Request construction:

```go
func (a *Adapter) buildSessionRequestLocked(ctx context.Context, input AUXInput) (core.SessionRequest, func(), error) {
	ref := "aux:" + input.ID
	cleanup := func() {}
	req := core.SessionRequest{
		AdapterRef: ref,
		Source: "aux",
		Title: input.Name,
		MediaKind: core.MediaKindMusic,
		Capabilities: core.Capabilities{CanPause: false, CanSeek: false},
		Visualizer: core.VisualizerRequest{
			Enabled: true,
			Metadata: core.VisualizerMetadata{Title: input.Name, Artist: "AUX"},
		},
		AudioOutputMode: coreAudioOutputMode(input.AudioOutput),
	}
	switch input.Mode {
	case ModeStreamURL:
		probeURL, playURL, release, err := a.mintProxyPairLocked(input.URL)
		if err != nil {
			return core.SessionRequest{}, nil, err
		}
		req.StreamProbeURL = probeURL
		req.StreamURL = playURL
		req.MediaInputPolicy = auxLoopbackPolicy()
		cleanup = release
	case ModeLocalCapture:
		req.AudioCapture = core.AudioCaptureInput{
			Enabled: true,
			Format: input.Format,
			Device: input.Device,
			SampleRate: effectiveSampleRate(input.SampleRate, a.bridge.Audio.SampleRate),
			Channels: effectiveChannels(input.Channels, a.bridge.Audio.Channels),
			ThreadQueueSize: input.ThreadQueueSize,
			AnalyzeDuration: time.Duration(input.AnalyzeDurationMillis) * time.Millisecond,
			ProbeSize: input.ProbeSize,
		}
	default:
		return core.SessionRequest{}, nil, fmt.Errorf("unsupported AUX input mode %q", input.Mode)
	}
	req.OnStop = func(reason string) {
		cleanup()
		a.onStopForRef(ref)(reason)
	}
	return req, cleanup, nil
}
```

`StartAUX` must call `cleanup()` if `a.core.StartSession(req)` returns an error. When `StartSession` succeeds, `req.OnStop` owns cleanup for EOF, stop, preempt, and error exits.

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\gofmt.exe -w internal/adapters/aux/session.go internal/adapters/aux/session_test.go internal/adapters/aux/adapter.go internal/adapters/aux/adapter_test.go
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/aux
```

Commit:

```bash
git add internal/adapters/aux
git commit -m "feat(aux): start analog visualizer sessions"
```

## Task 8: Add Shared AUX Contracts And Wire Adapter In Main

- [ ] Add `internal/adapters/aux_contract.go` with `AUXStatus` and `ErrSourceUnavailable`.
- [ ] Add `internal/chassis/aux.go` with the `AUXStarter` interface returning `adapters.AUXStatus`.
- [ ] Add `AUX AUXStarter` to `chassis.Config` and `Server` before wiring main.
- [ ] Import `internal/adapters/aux` in `cmd/mister-groovy-relay/main.go`.
- [ ] Construct the adapter after `coreMgr` and before the registry decode loop.
- [ ] Register it in a stable source order.
- [ ] Pass the AUX adapter to chassis config.
- [ ] Verify `PublicRouteProvider` registry walk mounts the proxy without explicit route calls.

Shared neutral contract:

```go
package adapters

import "errors"

var ErrSourceUnavailable = errors.New("source unavailable")

type AUXStatus struct {
	Enabled      bool
	Configured   bool
	Active       bool
	InputID      string
	DisplayName  string
	AdapterRef   string
	ErrorMessage string
}
```

Chassis interface:

```go
package chassis

import (
	"context"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type AUXStarter interface {
	AUXStatus(context.Context) adapters.AUXStatus
	StartAUX(context.Context, string) (adapterRef string, err error)
	StopAUX(context.Context, string) (matched bool, err error)
}
```

Implementation shape:

```go
auxAdapter, err := aux.New(aux.AdapterConfig{
	Bridge:   sec.Bridge,
	Core:     coreMgr,
	HTTPPort: sec.Bridge.UI.HTTPPort,
	EventLog: elog,
})
if err != nil {
	dieFriendly("aux adapter init", err)
}
if err := reg.Register(auxAdapter); err != nil {
	dieFriendly("registry register aux", err)
}
```

Chassis config: add `AUX AUXStarter` to `chassis.Config`, store it on `Server`, and add `AUX: auxAdapter,` to the existing `chassis.Config` literal in `cmd/mister-groovy-relay/main.go`, adjacent to `VisualizerSaver`. Keep all current fields unchanged.

Add a main-level compile test if an existing one covers imports; otherwise the package compile is sufficient.

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\gofmt.exe -w internal/adapters/aux_contract.go internal/chassis/aux.go internal/chassis/server.go cmd/mister-groovy-relay/main.go
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters ./internal/chassis ./cmd/mister-groovy-relay ./internal/adapters/aux
```

Commit:

```bash
git add internal/adapters/aux_contract.go internal/chassis/aux.go internal/chassis/server.go cmd/mister-groovy-relay/main.go
git commit -m "feat(aux): register analog source adapter"
```

## Task 9: Add Chassis AUX Routes

- [ ] Mount `POST /receiver/aux/start` and `POST /receiver/aux/stop` with same-origin protection.
- [ ] Return JSON errors with clear status codes.
- [ ] Keep foreign-session stop as success/no-op.
- [ ] Use `errors.Is(err, adapters.ErrSourceUnavailable)` for 422 errors so chassis does not import `internal/adapters/aux`.

Route handlers:

```go
func (s *Server) handleAUXStartPost(w http.ResponseWriter, r *http.Request) {
	if s.aux == nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "AUX input unavailable")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed AUX request")
		return
	}
	inputID := strings.TrimSpace(r.Form.Get("input_id"))
	if _, err := s.aux.StartAUX(r.Context(), inputID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, adapters.ErrSourceUnavailable) {
			status = http.StatusUnprocessableEntity
		}
		writeJSONError(w, status, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAUXStopPost(w http.ResponseWriter, r *http.Request) {
	if s.aux == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed AUX request")
		return
	}
	_, err := s.aux.StopAUX(r.Context(), strings.TrimSpace(r.Form.Get("input_id")))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Tests:

- `TestAUXStartRouteStartsConfiguredInput`: fake `AUXStarter` records `input_id`, handler returns 204, and `StartAUX` is called once.
- `TestAUXStartRouteRejectsWrongInputID`: fake returns `adapters.ErrSourceUnavailable`, handler returns 422 JSON containing `AUX input unavailable`.
- `TestAUXStartRouteUsesSameOriginProtection`: mount through `Server.Mount`, send a cross-origin POST, and assert 403 without calling `StartAUX`.
- `TestAUXStopRouteNoopsWithoutAUX`: server with nil AUX returns 204 for `/receiver/aux/stop`.
- `TestAUXStopRouteDoesNotRequireForeignStopMatch`: fake returns `matched=false`, handler returns 204, and no error body is emitted.
- `TestProductionImports_NoChassisAuxAdapterImport`: extend the existing import check to fail if any `internal/chassis` file imports `github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/aux`.

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\gofmt.exe -w internal/chassis/aux.go internal/chassis/aux_test.go internal/chassis/server.go internal/chassis/import_check_test.go
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/chassis
```

Commit:

```bash
git add internal/chassis/aux.go internal/chassis/aux_test.go internal/chassis/server.go internal/chassis/import_check_test.go
git commit -m "feat(aux): add receiver aux routes"
```

## Task 10: Add AUX Source Button UI

- [ ] Add AUX status data to `ReceiverPageData`.
- [ ] Add a fifth `AUX` source button.
- [ ] Add button action metadata instead of hardcoding click behavior by label.
- [ ] Add JavaScript that posts to `/receiver/aux/start`.
- [ ] Add a `source` SSE event and client handler so AUX active/unavailable state updates in already-open tabs.
- [ ] Update CSS responsive tests for five buttons.

Data changes:

```go
type SourceButton struct {
	Label       string
	Active      bool
	Lit         bool
	Unavailable bool
	Action      string
	InputID     string
}
```

Snapshot rule in `snapshotFromSession`:

```go
func applyAUXSourceState(base *ReceiverPageData, aux AUXStarter, ctx context.Context) {
	if aux == nil {
		return
	}
	st := aux.AUXStatus(ctx)
	if st.Active {
		for i := range base.Source.Buttons {
			base.Source.Buttons[i].Active = false
		}
	}
	for i := range base.Source.Buttons {
		if base.Source.Buttons[i].Label != "AUX" {
			continue
		}
		base.Source.Buttons[i].Unavailable = !st.Enabled || !st.Configured
		base.Source.Buttons[i].Lit = st.Active
		base.Source.Buttons[i].InputID = st.InputID
		if st.Active {
			base.Source.Buttons[i].Active = true
		}
	}
}
```

Template shape:

```html
{{range .Buttons}}<button
  class="hw-btn{{if .Active}} active{{end}}{{if .Lit}} lit{{end}}"
  type="button"
  role="radio"
  aria-checked="{{if .Active}}true{{else}}false{{end}}"
  aria-disabled="{{if .Unavailable}}true{{else}}false{{end}}"
  data-source-action="{{.Action}}"
  data-input-id="{{.InputID}}"
  {{if .Unavailable}}disabled{{end}}
  aria-label="{{.Label}}{{if .Active}} selected{{end}}{{if .Lit}} casting{{end}}"
  title="{{.Label}}{{if .Active}} selected{{end}}{{if .Lit}} casting{{end}}">{{.Label}}</button>{{end}}
```

JavaScript:

```js
function installSourceActions() {
  document.querySelectorAll('[data-source-action="aux-start"]').forEach((button) => {
    button.addEventListener('click', async () => {
      if (button.disabled || button.getAttribute('aria-disabled') === 'true') {
        return;
      }
      const body = new URLSearchParams();
      const inputID = button.dataset.inputId || '';
      if (inputID) {
        body.set('input_id', inputID);
      }
      const response = await fetch('/receiver/aux/start', {
        method: 'POST',
        headers: {'Content-Type': 'application/x-www-form-urlencoded'},
        body,
      });
      if (response.ok) {
        document.querySelectorAll('.source-cluster .hw-btn').forEach((candidate) => {
          candidate.classList.remove('active');
          candidate.setAttribute('aria-checked', 'false');
        });
        button.classList.add('lit', 'active');
        button.setAttribute('aria-checked', 'true');
      }
    });
  });
}
```

SSE source event:

```go
type sourceEnvelope struct {
	Buttons []sourceButtonEnvelope `json:"buttons"`
}

type sourceButtonEnvelope struct {
	Label       string `json:"label"`
	Active      bool   `json:"active"`
	Lit         bool   `json:"lit"`
	Unavailable bool   `json:"unavailable"`
	InputID     string `json:"inputId"`
}
```

`handleEvents` must emit an initial `source` event after `vfd`, compare source-button envelopes on each tick, and emit `source` when AUX active/unavailable state changes. `internal/chassis/static/vfd-live.js` must listen for `source`, find buttons by their label or `data-source-action`, and update `active`, `lit`, `aria-checked`, `aria-disabled`, `disabled`, and `data-input-id` without a full page reload.

Tests:

- `TestIdleSnapshotRendersFiveSourceButtonsIncludingAUX`: render `idleSnapshot` with a configured fake AUX and assert source labels are `STREAMS`, `PLEX`, `JELLYFIN`, `DLNA`, `AUX`.
- `TestSourceClusterAUXButtonCarriesStartAction`: render `source-cluster` and assert the AUX button has `data-source-action="aux-start"` and `data-input-id="aux"`.
- `TestSourceClusterAUXDisabledWhenUnavailable`: fake AUX status with `Enabled=false`, render page, and assert the AUX button is disabled with `aria-disabled="true"`.
- `TestChassisJSInstallsAUXSourceAction`: extend the existing static JS contract test to require `data-source-action="aux-start"` and `/receiver/aux/start`.
- `TestEventsEmitSourceWhenAUXStateChanges`: fake AUX status flips from idle to active, `/receiver/events` emits `event: source`, and the payload has only AUX active.
- `TestVFDLiveJSHandlesSourceEvents`: extend the static JS contract test to require `addEventListener('source'` and class/ARIA updates for source buttons.
- `TestSourceClusterResponsiveFiveButtonLayout`: extend `css_scope_test.go` to assert the desktop and mobile source cluster selectors define stable five-button grid behavior.

CSS target:

- Desktop: five equal-width buttons in one row.
- Around the existing 1180px breakpoint: 3x2 or 2x3 grid with stable button dimensions.
- Mobile: horizontal row or grid that does not overflow source row tests.

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\gofmt.exe -w internal/chassis/data.go internal/chassis/session.go internal/chassis/events.go internal/chassis/events_test.go internal/chassis/chassis_test.go internal/chassis/css_scope_test.go
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/chassis
```

Commit:

```bash
git add internal/chassis/data.go internal/chassis/session.go internal/chassis/events.go internal/chassis/events_test.go internal/chassis/templates/source-cluster.html internal/chassis/static/chassis.js internal/chassis/static/vfd-live.js internal/chassis/static/chassis.css internal/chassis/chassis_test.go internal/chassis/css_scope_test.go
git commit -m "feat(aux): add receiver source control"
```

## Task 11: Add End-To-End AUX Behavior Tests

- [ ] Add a fake-MiSTer/integration-style test for AUX visual-only video fields.
- [ ] Add a start-preempts-prior-session test.
- [ ] Add stream proxy two-GET behavior coverage using `httptest.Server`.
- [ ] Add local-capture command integration at the command-builder boundary rather than requiring a real device.

Recommended test targets:

- `TestAUXStreamURLConsumesProbeAndPlayTokensSeparately`: start AUX against an `httptest.Server`, consume the probe token, then the play token, and assert the upstream saw exactly two GETs.
- `TestAUXStreamURLProbeFailureDoesNotPreemptActiveCast`: make probe token consumption return a 502, call `StartAUX`, and assert the fake core's active foreign session was not stopped or replaced.
- `TestAUXStreamURLPlayFailureReportsAfterPreempt`: allow probe to pass, make the play token return a 502 during plane spawn, and assert AUX status records a clear error after the fake prior session was preempted.
- `TestAUXVisualOnlyProducesVideoWithoutAudioPipe`: run the fake-MiSTer path with `SuppressAudioOutput=true` and assert video fields are emitted while no audio chunks are read.
- `TestAUXStartPreemptsPriorSessionAfterSuccessfulProbe`: fake an active Plex session, start AUX after successful probe, and assert the final status carries an `aux:` adapter ref.

The fake upstream should count requests:

```go
var gets atomic.Int32
upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	gets.Add(1)
	w.Header().Set("Content-Type", "audio/wav")
	_, _ = w.Write(minimalWAVHeaderAndSilence())
}))
```

Expected result: one GET during `ProbeInput`, a second GET during FFmpeg command startup/play token use. Reusing either token returns 401.

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\gofmt.exe -w internal/adapters/aux/proxy_test.go internal/adapters/aux/session_test.go internal/core/manager_test.go internal/dataplane/plane_test.go
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/aux ./internal/core ./internal/dataplane ./internal/ffmpeg ./internal/chassis
```

Commit:

```bash
git add internal/adapters/aux internal/core/manager_test.go internal/dataplane/plane_test.go internal/ffmpeg internal/chassis
git commit -m "test(aux): cover analog visualizer lifecycle"
```

## Task 12: Document AUX Config And Producer Commands

- [ ] Update `internal/config/example.toml`.
- [ ] Update `README.md`, preserving current user edits.
- [ ] Add operator notes for native capture and Unraid/remote streaming.
- [ ] Document the two sequential GET requirement.
- [ ] Document `visual_only` vs `monitor`.

`internal/config/example.toml` addition:

```toml
[adapters.aux]
enabled = false

[adapters.aux.input]
id = "aux"
name = "AUX"
mode = "stream_url"              # "stream_url" or "local_capture"
audio_output = "visual_only"     # "visual_only" or "monitor"
url = "http://capture-host:8090/aux.wav"
format = ""                      # local_capture example: "alsa", "dshow", "avfoundation"
device = ""                      # local_capture example: "hw:1,0" or "audio=Line In (...)"
sample_rate = 48000
channels = 2
thread_queue_size = 64
analyze_duration_ms = 100
probe_size = 32768
```

README content to include:

````markdown
### AUX analog visualizer

The receiver page can expose an `AUX` source that drives the CRT visualizer from a line-in or USB audio interface.

For native binaries, configure `mode = "local_capture"` with the FFmpeg capture format and device for the host.

For Docker/Unraid, the recommended v1 path is `mode = "stream_url"` and a small FFmpeg producer on the machine that has the audio input:

```bash
while true; do
  ffmpeg -nostdin -f alsa -thread_queue_size 64 -sample_rate 48000 -channels 2 -i hw:1,0 \
    -vn -ac 2 -ar 48000 -f wav -listen 1 http://0.0.0.0:8090/aux.wav
  sleep 0.2
done
```

The loop is required because GroovyRelay opens the stream twice per AUX start: once for probe, then once for playback.
````

Run:

```bash
git diff --check
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/aux ./internal/chassis
```

Commit:

```bash
git add internal/config/example.toml README.md
git commit -m "docs(aux): document analog visualizer setup"
```

## Task 13: Full Verification

- [ ] Run format and test verification.
- [ ] Confirm no stale route names or wrong paths remain.
- [ ] Confirm no token leaks in logs/tests.
- [ ] Confirm working tree only contains intended changes before final commit or handoff.

Commands:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\gofmt.exe -w cmd/mister-groovy-relay/main.go internal/core internal/ffmpeg internal/dataplane internal/adapters/aux internal/chassis
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./...
git diff --check
rg -n "/receiver/visualizer/mode|aux_token=[A-Za-z0-9_-]{8,}|\\[\\[adapters\\.aux\\.input\\]\\]" .
rg -n "POST /receiver/aux/start|/internal/aux-proxy/|StreamProbeURL|SuppressAudioOutput|AudioCaptureInput" internal docs README.md
git status --short
```

Expected `rg` results:

- No `/receiver/visualizer/mode`.
- No `[[adapters.aux.input]]`.
- No hard-coded leaked `aux_token` values beyond redaction tests that intentionally use known dummy strings.
- Positive hits for `/receiver/aux/start`, `/internal/aux-proxy/`, `StreamProbeURL`, `SuppressAudioOutput`, and `AudioCaptureInput`.

Final commit if any verification-only fix was needed:

```bash
git add <fixed-files>
git commit -m "fix(aux): finish analog visualizer verification"
```

## Done When

- [ ] `AUX` appears in the receiver source cluster.
- [ ] `POST /receiver/aux/start` starts the configured input.
- [ ] `stream_url` mode uses two distinct loopback proxy URLs, one for probe and one for play.
- [ ] The proxy rejects non-loopback clients, redirects, non-2xx responses, unsafe URLs, expired tokens, and reused tokens.
- [ ] `local_capture` mode reaches FFmpeg through structured argv tokens.
- [ ] `visual_only` suppresses FFmpeg PCM output and advertises `AudioRateOff` / zero channels to the MiSTer.
- [ ] `monitor` keeps PCM output enabled.
- [ ] AUX start preempts an existing cast only after config validation and probe success.
- [ ] AUX stop cannot stop a foreign active session.
- [ ] Core/chassis imports preserve existing package boundaries.
- [ ] `cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./...` passes.
- [ ] `git diff --check` passes.
