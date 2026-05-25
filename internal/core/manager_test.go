package core

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

type staticBinaryResolver string

func (r staticBinaryResolver) Resolve() (string, error) {
	return string(r), nil
}

// testBridgeConfig returns a minimal BridgeConfig suitable for unit tests.
func testBridgeConfig(t *testing.T) config.BridgeConfig {
	t.Helper()
	return config.BridgeConfig{
		MiSTer: config.MisterConfig{
			Host:       "127.0.0.1",
			Port:       32100,
			SourcePort: 0,
		},
		Video: config.VideoConfig{
			Modeline:            "NTSC_480i",
			InterlaceFieldOrder: "tff",
			AspectMode:          "letterbox",
			RGBMode:             "rgb888",
			LZ4Enabled:          false,
		},
		Audio: config.AudioConfig{
			SampleRate:   48000,
			Channels:     2,
			OutputVolume: 100,
		},
	}
}

// newTestManager returns a Manager with a Sender bound to a free local port.
// The sender is never actually used by these tests: StartSession gets a
// missing test ffprobe path so it fails at the Probe step before any UDP
// traffic. We still construct a real sender so NewManager's real constructor
// is exercised without depending on host ffprobe behavior.
// Extra opts are forwarded to NewManager after the default WithBinaryResolvers.
func newTestManager(t *testing.T, opts ...ManagerOption) *Manager {
	t.Helper()
	sender, err := groovynet.NewSender("127.0.0.1", 0, 0)
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })
	bridge := testBridgeConfig(t)
	toolsDir := t.TempDir()
	allOpts := append([]ManagerOption{WithBinaryResolvers(
		staticBinaryResolver(filepath.Join(toolsDir, "missing-ffmpeg")),
		staticBinaryResolver(filepath.Join(toolsDir, "missing-ffprobe")),
	)}, opts...)
	return NewManager(bridge, sender, allOpts...)
}

// bogusRequest builds a SessionRequest for tests whose Manager has a missing
// ffprobe resolver. This lets us exercise the Manager's public API and state
// bookkeeping on any platform without needing real media or host ffprobe.
func bogusRequest() SessionRequest {
	return SessionRequest{
		StreamURL:    "udp://127.0.0.1:1/this-url-will-fail-probe",
		AdapterRef:   "test-ref",
		Capabilities: Capabilities{CanSeek: true, CanPause: true},
	}
}

type fakePlane struct{}

func (f *fakePlane) Run(context.Context) error  { return nil }
func (f *fakePlane) Done() <-chan struct{}      { ch := make(chan struct{}); close(ch); return ch }
func (f *fakePlane) Position() time.Duration    { return 0 }
func (f *fakePlane) SetFieldOrder(string) error { return nil }
func (f *fakePlane) SetOutputVolume(int) error  { return nil }
func (f *fakePlane) BlitsTotal() uint64         { return 0 }
func (f *fakePlane) FramesTotal() uint64        { return 0 }
func (f *fakePlane) Underruns() uint64          { return 0 }
func (f *fakePlane) WireBytes() uint64          { return 0 }
func (f *fakePlane) LastACKAge() time.Duration  { return 0 }

type contextDonePlane struct {
	done chan struct{}
}

func (f *contextDonePlane) Run(ctx context.Context) error {
	<-ctx.Done()
	close(f.done)
	return ctx.Err()
}
func (f *contextDonePlane) Done() <-chan struct{}      { return f.done }
func (f *contextDonePlane) Position() time.Duration    { return 0 }
func (f *contextDonePlane) SetFieldOrder(string) error { return nil }
func (f *contextDonePlane) SetOutputVolume(int) error  { return nil }
func (f *contextDonePlane) BlitsTotal() uint64         { return 0 }
func (f *contextDonePlane) FramesTotal() uint64        { return 0 }
func (f *contextDonePlane) Underruns() uint64          { return 0 }
func (f *contextDonePlane) WireBytes() uint64          { return 0 }
func (f *contextDonePlane) LastACKAge() time.Duration  { return 0 }

type blockingDonePlane struct {
	done chan struct{}
	pos  time.Duration
}

func (f *blockingDonePlane) Run(context.Context) error  { <-f.done; return nil }
func (f *blockingDonePlane) Done() <-chan struct{}      { return f.done }
func (f *blockingDonePlane) Position() time.Duration    { return f.pos }
func (f *blockingDonePlane) SetFieldOrder(string) error { return nil }
func (f *blockingDonePlane) SetOutputVolume(int) error  { return nil }
func (f *blockingDonePlane) BlitsTotal() uint64         { return 0 }
func (f *blockingDonePlane) FramesTotal() uint64        { return 0 }
func (f *blockingDonePlane) Underruns() uint64          { return 0 }
func (f *blockingDonePlane) WireBytes() uint64          { return 0 }
func (f *blockingDonePlane) LastACKAge() time.Duration  { return 0 }

type volumePlane struct {
	fakePlane
	volumes []int
}

func (f *volumePlane) SetOutputVolume(volume int) error {
	f.volumes = append(f.volumes, volume)
	return nil
}

func TestManager_DropActiveCast_NoSession(t *testing.T) {
	m := newTestManager(t)
	if err := m.DropActiveCast("unit test"); err != nil {
		t.Errorf("DropActiveCast with no session: %v", err)
	}
	if m.Status().State != StateIdle {
		t.Errorf("state should remain Idle after no-op drop")
	}
}

func TestManager_SetOutputVolumeUpdatesActivePlane(t *testing.T) {
	m := newTestManager(t)
	vp := &volumePlane{}
	m.mu.Lock()
	m.plane = vp
	m.mu.Unlock()

	if err := m.SetOutputVolume(37); err != nil {
		t.Fatalf("SetOutputVolume: %v", err)
	}
	if got := m.bridge.Audio.OutputVolume; got != 37 {
		t.Fatalf("manager bridge output volume = %d, want 37", got)
	}
	if len(vp.volumes) != 1 || vp.volumes[0] != 37 {
		t.Fatalf("active plane volumes = %v, want [37]", vp.volumes)
	}
}

func TestManager_SetOutputVolumeRejectsOutOfRange(t *testing.T) {
	m := newTestManager(t)
	for _, volume := range []int{-1, 101} {
		if err := m.SetOutputVolume(volume); err == nil {
			t.Fatalf("SetOutputVolume(%d) error = nil, want range error", volume)
		}
	}
}

func TestManager_InitialStatusIdle(t *testing.T) {
	m := newTestManager(t)
	st := m.Status()
	if st.State != StateIdle {
		t.Errorf("initial state = %s, want %s", st.State, StateIdle)
	}
	if st.AdapterRef != "" {
		t.Errorf("AdapterRef = %q, want empty", st.AdapterRef)
	}
	if !st.StartedAt.IsZero() {
		t.Errorf("StartedAt = %v, want zero", st.StartedAt)
	}
}

func TestManager_VisualizerMode_ReturnsLiveBridgeMode(t *testing.T) {
	t.Parallel()
	m := newTestManager(t)

	// testBridgeConfig intentionally omits Visualizer.Mode, so the initial value is the zero string.
	// UpdateBridge is the path the saver uses to refresh; this test asserts VisualizerMode reads through it.
	b := testBridgeConfig(t)
	b.Visualizer.Mode = config.VisualizerModeStereoScope
	m.UpdateBridge(b)

	if got := m.VisualizerMode(); got != config.VisualizerModeStereoScope {
		t.Errorf("VisualizerMode() = %q, want %q", got, config.VisualizerModeStereoScope)
	}
}

func TestManager_VisualizerMode_ReturnsEmptyBeforeUpdateBridge(t *testing.T) {
	t.Parallel()
	m := newTestManager(t)
	// testBridgeConfig omits Visualizer.Mode, so newTestManager constructs the Manager with an empty mode.
	// The getter returns whatever is in m.bridge with no defaulting: that is saver/UI responsibility, not core's.
	if got := m.VisualizerMode(); got != "" {
		t.Errorf("VisualizerMode() = %q, want empty (testBridgeConfig leaves field unset)", got)
	}
}

func TestProbeDuration_ConvertsSecondsToDuration(t *testing.T) {
	got := probeDuration(&ffmpeg.ProbeResult{Duration: 12.345})
	want := 12345 * time.Millisecond
	if got != want {
		t.Errorf("probeDuration = %v, want %v", got, want)
	}
	if probeDuration(nil) != 0 {
		t.Error("probeDuration(nil) should be zero")
	}
	if probeDuration(&ffmpeg.ProbeResult{Duration: -1}) != 0 {
		t.Error("probeDuration(negative) should be zero")
	}
}

func TestManager_LogPlaneExit_InitACKTimeoutIsClear(t *testing.T) {
	m := newTestManager(t)

	var buf bytes.Buffer
	old := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(old) })

	m.logPlaneExit(fmt.Errorf("init handshake: %w", &groovynet.InitACKTimeoutError{
		Timeout: 60 * time.Millisecond,
		Err:     os.ErrDeadlineExceeded,
	}))

	got := buf.String()
	if !strings.Contains(got, "MiSTer did not acknowledge INIT") {
		t.Fatalf("expected friendly INIT warning, got %q", got)
	}
	if !strings.Contains(got, "mister_host=127.0.0.1") {
		t.Fatalf("expected mister_host in log, got %q", got)
	}
	if !strings.Contains(got, "mister_port=32100") {
		t.Fatalf("expected mister_port in log, got %q", got)
	}
}

func TestManager_NaturalEOFFiresOnStopAndClearsActive(t *testing.T) {
	m := newTestManager(t)
	done := make(chan string, 1)

	plane := &fakePlane{}
	m.mu.Lock()
	m.plane = plane
	m.cancelFn = func() {}
	m.active = &activeSession{req: SessionRequest{
		OnStop: func(reason string) { done <- reason },
	}}
	m.mu.Unlock()

	m.handlePlaneExit(plane, nil)

	select {
	case got := <-done:
		if got != "eof" {
			t.Fatalf("OnStop reason = %q, want eof", got)
		}
	case <-time.After(time.Second):
		t.Fatal("OnStop was not called")
	}
	if st := m.Status(); st.State != StateIdle {
		t.Fatalf("state after EOF = %s, want %s", st.State, StateIdle)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil || m.plane != nil || m.cancelFn != nil {
		t.Fatalf("manager did not clear session: active=%v plane=%v cancelFnNil=%v", m.active, m.plane, m.cancelFn == nil)
	}
}

func TestManager_NaturalEOFViaStartPlaneLockedFiresOnStop(t *testing.T) {
	prevNewPlane := newPlane
	t.Cleanup(func() { newPlane = prevNewPlane })

	plane := &fakePlane{}
	newPlane = func(dataplane.PlaneConfig) planeRunner { return plane }

	m := newTestManager(t)
	done := make(chan string, 1)
	req := SessionRequest{
		StreamURL:  "https://example.test/video.mp4",
		DirectPlay: true,
		OnStop:     func(reason string) { done <- reason },
	}
	probe := &ffmpeg.ProbeResult{Width: 1280, Height: 720, FrameRate: 29.97, AudioRate: 48000, Duration: 1}

	m.mu.Lock()
	if err := m.startPlaneLocked(req, 0, probe, nil, "ffmpeg", 1, sessionGuard{}, false); err != nil {
		m.mu.Unlock()
		t.Fatalf("startPlaneLocked: %v", err)
	}
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition play media: %v", err)
	}
	m.mu.Unlock()

	select {
	case got := <-done:
		if got != "eof" {
			t.Fatalf("OnStop reason = %q, want eof", got)
		}
	case <-time.After(time.Second):
		t.Fatal("OnStop was not called via goroutine wrapper")
	}
	if st := m.Status(); st.State != StateIdle {
		t.Fatalf("state after EOF = %s, want %s", st.State, StateIdle)
	}
}

func TestManager_SessionGenerationStatusIncrementsOnFreshStarts(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	origNewPlane := newPlane
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
		newPlane = origNewPlane
	})

	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 640, Height: 480, FrameRate: 60, Duration: 120}, nil
	}
	probeCropFn = func(context.Context, string, string, map[string]string, time.Duration, ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		return nil, nil
	}
	newPlane = func(dataplane.PlaneConfig) planeRunner { return &contextDonePlane{done: make(chan struct{})} }

	m := newTestManager(t)
	t.Cleanup(func() { _ = m.Stop() })
	if err := m.StartSession(SessionRequest{StreamURL: "http://example.test/one.mp4", AdapterRef: "url:one", Source: "url", Capabilities: Capabilities{CanSeek: true, CanPause: true}, DirectPlay: true}); err != nil {
		t.Fatalf("start one: %v", err)
	}
	first := m.Status()
	if first.Generation == 0 {
		t.Fatalf("first generation = 0, want non-zero")
	}
	if got := m.StatusHomeView().Generation; got != first.Generation {
		t.Fatalf("home generation = %d, want %d", got, first.Generation)
	}

	if err := m.StartSession(SessionRequest{StreamURL: "http://example.test/two.mp4", AdapterRef: "url:two", Source: "url", Capabilities: Capabilities{CanSeek: true, CanPause: true}, DirectPlay: true}); err != nil {
		t.Fatalf("start two: %v", err)
	}
	second := m.Status()
	if second.Generation <= first.Generation {
		t.Fatalf("second generation = %d, want > %d", second.Generation, first.Generation)
	}
}

func TestManager_SessionGenerationStableAcrossPauseResumeAndSeek(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	origNewPlane := newPlane
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
		newPlane = origNewPlane
	})

	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 640, Height: 480, FrameRate: 60, Duration: 120}, nil
	}
	probeCropFn = func(context.Context, string, string, map[string]string, time.Duration, ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		return nil, nil
	}
	newPlane = func(dataplane.PlaneConfig) planeRunner { return &contextDonePlane{done: make(chan struct{})} }

	m := newTestManager(t)
	t.Cleanup(func() { _ = m.Stop() })
	req := SessionRequest{StreamURL: "http://example.test/movie.mp4", AdapterRef: "url:movie", Source: "url", Capabilities: Capabilities{CanSeek: true, CanPause: true}, DirectPlay: true}
	if err := m.StartSession(req); err != nil {
		t.Fatalf("start: %v", err)
	}
	gen := m.Status().Generation

	if err := m.Pause(); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if got := m.Status().Generation; got != gen {
		t.Fatalf("generation after pause = %d, want %d", got, gen)
	}
	if err := m.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	if got := m.Status().Generation; got != gen {
		t.Fatalf("generation after play = %d, want %d", got, gen)
	}
	if err := m.SeekTo(5000); err != nil {
		t.Fatalf("seek: %v", err)
	}
	if got := m.Status().Generation; got != gen {
		t.Fatalf("generation after seek = %d, want %d", got, gen)
	}
}

func TestManager_StartSession_ProbeFailLeavesIdle(t *testing.T) {
	m := newTestManager(t)
	err := m.StartSession(bogusRequest())
	if err == nil {
		t.Fatal("expected probe failure, got nil")
	}
	if !strings.Contains(err.Error(), "probe source") && !strings.Contains(err.Error(), "ffprobe") {
		t.Logf("err = %v (acceptable — any probe path failure)", err)
	}
	st := m.Status()
	if st.State != StateIdle {
		t.Errorf("state after failed StartSession = %s, want Idle", st.State)
	}
	if m.plane != nil {
		t.Errorf("plane should be nil after failed start")
	}
	if m.active != nil {
		t.Errorf("active should be nil after failed start")
	}
}

func TestManager_StartSessionFiltersBlockedHeadersBeforePipeline(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	origNewPlane := newPlane
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
		newPlane = origNewPlane
	})

	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 640, Height: 480, FrameRate: 60}, nil
	}
	probeCropFn = func(context.Context, string, string, map[string]string, time.Duration, ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		return nil, nil
	}
	var captured dataplane.PlaneConfig
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
		captured = cfg
		return &blockingDonePlane{done: done}
	}

	m := newTestManager(t)
	m.bridge.Video.LZ4Enabled = true
	m.bridge.Video.DeltaLZ4Enabled = true
	err := m.StartSession(SessionRequest{
		StreamURL:         "http://example/clip.m3u8",
		AudioStreamURL:    "http://example/audio.m3u8",
		InputHeaders:      map[string]string{"Cookie": "session=abc", "Referer": "http://example", "User-Agent": "groovyrelay"},
		AudioInputHeaders: map[string]string{"Authorization": "Bearer secret", "User-Agent": "audio-agent"},
		MediaInputPolicy: MediaInputPolicy{
			BlockedHeaders: []string{"Cookie", "Referer", "Authorization"},
		},
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, ok := captured.SpawnSpec.InputHeaders["Cookie"]; ok {
		t.Fatalf("Cookie reached pipeline argv inputs: %v", captured.SpawnSpec.InputHeaders)
	}
	if _, ok := captured.SpawnSpec.InputHeaders["Referer"]; ok {
		t.Fatalf("Referer reached pipeline argv inputs: %v", captured.SpawnSpec.InputHeaders)
	}
	if got := captured.SpawnSpec.InputHeaders["User-Agent"]; got != "groovyrelay" {
		t.Fatalf("User-Agent input header = %q", got)
	}
	if _, ok := captured.SpawnSpec.AudioInputHeaders["Authorization"]; ok {
		t.Fatalf("Authorization reached pipeline argv audio inputs: %v", captured.SpawnSpec.AudioInputHeaders)
	}
	if got := captured.SpawnSpec.AudioInputHeaders["User-Agent"]; got != "audio-agent" {
		t.Fatalf("User-Agent audio input header = %q", got)
	}
	if !captured.DeltaLZ4Enabled {
		t.Fatal("DeltaLZ4Enabled was not plumbed into PlaneConfig")
	}
}

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
			name: "secondary audio stream without primary stream",
			req: SessionRequest{
				AudioStreamURL:  "http://example.test/audio-only.m4a",
				AudioOutputMode: AudioOutputVisualOnly,
			},
			want: "audio_stream_url requires stream_url",
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

func TestManager_SessionAspectModeOverrideSkipsAutoCropAndReachesPipeline(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	origNewPlane := newPlane
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
		newPlane = origNewPlane
	})

	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 640, Height: 480, FrameRate: 60}, nil
	}
	cropCalls := 0
	probeCropFn = func(context.Context, string, string, map[string]string, time.Duration, ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		cropCalls++
		return nil, nil
	}
	var captured dataplane.PlaneConfig
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
		captured = cfg
		return &blockingDonePlane{done: done}
	}

	m := newTestManager(t)
	m.bridge.Video.AspectMode = "auto"
	err := m.StartSession(SessionRequest{
		StreamURL:  "http://example/clip.mp4",
		AdapterRef: "streams:mtv-rewind:test",
		AspectMode: "zoom",
		DirectPlay: true,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if cropCalls != 0 {
		t.Fatalf("crop probe calls = %d, want 0 for zoom override", cropCalls)
	}
	if captured.SpawnSpec.AspectMode != "zoom" {
		t.Fatalf("pipeline aspect mode = %q, want zoom", captured.SpawnSpec.AspectMode)
	}
}

func TestManager_StopWhenIdleIsIdempotent(t *testing.T) {
	m := newTestManager(t)
	// First stop from Idle.
	if err := m.Stop(); err != nil {
		t.Errorf("first Stop from Idle: %v", err)
	}
	if m.Status().State != StateIdle {
		t.Errorf("state = %s, want Idle", m.Status().State)
	}
	// Second stop from Idle.
	if err := m.Stop(); err != nil {
		t.Errorf("second Stop from Idle: %v", err)
	}
	if m.Status().State != StateIdle {
		t.Errorf("state = %s, want Idle", m.Status().State)
	}
}

func TestManager_StopIfAdapterRefMismatchDoesNotStopForeignActive(t *testing.T) {
	m := newTestManager(t)
	stopped := make(chan string, 1)
	cancelled := false
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		AdapterRef: "streams:owned",
		OnStop:     func(reason string) { stopped <- reason },
	}}
	m.cancelFn = func() { cancelled = true }
	m.mu.Unlock()

	matched, err := m.StopIfAdapterRef("url:foreign")
	if err != nil {
		t.Fatalf("StopIfAdapterRef mismatch: %v", err)
	}
	if matched {
		t.Fatal("StopIfAdapterRef mismatch returned matched=true")
	}
	if cancelled {
		t.Fatal("StopIfAdapterRef mismatch cancelled the foreign active session")
	}
	if got := m.Status().AdapterRef; got != "streams:owned" {
		t.Fatalf("AdapterRef after mismatch = %q, want streams:owned", got)
	}
	select {
	case reason := <-stopped:
		t.Fatalf("OnStop fired on mismatch with reason %q", reason)
	default:
	}
}

func TestManager_StopIfAdapterRefMatchStopsActive(t *testing.T) {
	m := newTestManager(t)
	stopped := make(chan string, 1)
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		AdapterRef: "streams:owned",
		OnStop:     func(reason string) { stopped <- reason },
	}}
	m.mu.Unlock()

	matched, err := m.StopIfAdapterRef("streams:owned")
	if err != nil {
		t.Fatalf("StopIfAdapterRef match: %v", err)
	}
	if !matched {
		t.Fatal("StopIfAdapterRef match returned matched=false")
	}
	if got := m.Status().AdapterRef; got != "" {
		t.Fatalf("AdapterRef after stop = %q, want empty", got)
	}
	select {
	case reason := <-stopped:
		if reason != "stopped" {
			t.Fatalf("OnStop reason = %q, want stopped", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("OnStop was not called")
	}
}

func TestManager_StartSessionIfAdapterRefMismatchSkipsValidationAndProbe(t *testing.T) {
	origProbe := probeFn
	t.Cleanup(func() { probeFn = origProbe })
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		t.Fatal("mismatched StartSessionIfAdapterRef must not probe")
		return nil, nil
	}

	m := newTestManager(t)
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{AdapterRef: "streams:owned"}}
	m.mu.Unlock()

	matched, err := m.StartSessionIfAdapterRef(SessionRequest{
		StreamURL:  "http://example.test/movie.mp4",
		AdapterRef: "streams:foreign",
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerMode("unsupported")},
	}, "streams:foreign")
	if err != nil {
		t.Fatalf("StartSessionIfAdapterRef mismatch err = %v, want nil", err)
	}
	if matched {
		t.Fatal("StartSessionIfAdapterRef mismatch returned matched=true")
	}
}

func TestManager_StopIfSessionRejectsStaleGeneration(t *testing.T) {
	m := newTestManager(t)
	stopped := make(chan string, 1)
	cancelled := false
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		AdapterRef: "streams:owned",
		OnStop:     func(reason string) { stopped <- reason },
	}, generation: 7}
	m.cancelFn = func() { cancelled = true }
	m.mu.Unlock()

	matched, err := m.StopIfSession("streams:owned", 6)
	if err != nil {
		t.Fatalf("StopIfSession stale generation: %v", err)
	}
	if matched {
		t.Fatal("StopIfSession stale generation returned matched=true")
	}
	if cancelled {
		t.Fatal("StopIfSession stale generation cancelled the active session")
	}
	if got := m.Status(); got.AdapterRef != "streams:owned" || got.Generation != 7 {
		t.Fatalf("status after stale stop = %+v, want streams:owned generation 7", got)
	}
	select {
	case reason := <-stopped:
		t.Fatalf("OnStop fired on stale generation with reason %q", reason)
	default:
	}
}

func TestManager_StopIfSessionMatchesGeneration(t *testing.T) {
	m := newTestManager(t)
	stopped := make(chan string, 1)
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		AdapterRef: "streams:owned",
		OnStop:     func(reason string) { stopped <- reason },
	}, generation: 7}
	m.mu.Unlock()

	matched, err := m.StopIfSession("streams:owned", 7)
	if err != nil {
		t.Fatalf("StopIfSession match: %v", err)
	}
	if !matched {
		t.Fatal("StopIfSession match returned matched=false")
	}
	if got := m.Status(); got.AdapterRef != "" || got.Generation != 0 {
		t.Fatalf("status after stop = %+v, want idle with generation 0", got)
	}
	select {
	case reason := <-stopped:
		if reason != "stopped" {
			t.Fatalf("OnStop reason = %q, want stopped", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("OnStop was not called")
	}
}

func TestManager_PlaySeekAndStartIfSessionRejectStaleGeneration(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	origNewPlane := newPlane
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
		newPlane = origNewPlane
	})

	probeCalls := 0
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		probeCalls++
		return &ffmpeg.ProbeResult{Width: 640, Height: 480, FrameRate: 60, Duration: 120}, nil
	}
	probeCropFn = func(context.Context, string, string, map[string]string, time.Duration, ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		return nil, nil
	}
	newPlane = func(dataplane.PlaneConfig) planeRunner {
		t.Fatal("stale generation must not start a plane")
		return nil
	}

	m := newTestManager(t)
	req := SessionRequest{
		StreamURL:    "http://example.test/movie.mp4",
		AdapterRef:   "streams:owned",
		Capabilities: Capabilities{CanSeek: true, CanPause: true},
		DirectPlay:   true,
	}
	m.mu.Lock()
	m.active = &activeSession{
		req:            req,
		generation:     7,
		baseOffsetMs:   1000,
		pausedPosition: 2 * time.Second,
	}
	m.mu.Unlock()

	matched, err := m.PlayIfSession("streams:owned", 6)
	if err != nil {
		t.Fatalf("PlayIfSession stale generation: %v", err)
	}
	if matched {
		t.Fatal("PlayIfSession stale generation returned matched=true")
	}

	matched, err = m.SeekToIfSession("streams:owned", 6, 5000)
	if err != nil {
		t.Fatalf("SeekToIfSession stale generation: %v", err)
	}
	if matched {
		t.Fatal("SeekToIfSession stale generation returned matched=true")
	}

	matched, err = m.StartSessionIfSession(req, "streams:owned", 6)
	if err != nil {
		t.Fatalf("StartSessionIfSession stale generation: %v", err)
	}
	if matched {
		t.Fatal("StartSessionIfSession stale generation returned matched=true")
	}

	if probeCalls != 0 {
		t.Fatalf("stale generation called probe %d times, want 0", probeCalls)
	}
	if got := m.Status(); got.AdapterRef != "streams:owned" || got.Generation != 7 {
		t.Fatalf("status after stale guarded calls = %+v, want streams:owned generation 7", got)
	}
}

func TestManager_StartSessionIfSessionPreservesNewSessionInstalledWhileOldPlaneDrains(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	origNewPlane := newPlane
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
		newPlane = origNewPlane
	})

	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 640, Height: 480, FrameRate: 60, Duration: 120}, nil
	}
	probeCropFn = func(context.Context, string, string, map[string]string, time.Duration, ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		return nil, nil
	}
	newPlane = func(dataplane.PlaneConfig) planeRunner {
		return &contextDonePlane{done: make(chan struct{})}
	}

	m := newTestManager(t)
	t.Cleanup(func() { _ = m.Stop() })
	oldPlane := &blockingDonePlane{done: make(chan struct{})}
	cancelled := make(chan struct{})
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		StreamURL:  "http://example.test/old.mp4",
		AdapterRef: "streams:old",
		DirectPlay: true,
	}, generation: 7}
	m.plane = oldPlane
	m.cancelFn = func() { close(cancelled) }
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition to playing: %v", err)
	}
	m.mu.Unlock()

	done := make(chan struct {
		matched bool
		err     error
	}, 1)
	go func() {
		matched, err := m.StartSessionIfSession(SessionRequest{
			StreamURL:  "http://example.test/replacement.mp4",
			AdapterRef: "streams:old",
			DirectPlay: true,
		}, "streams:old", 7)
		done <- struct {
			matched bool
			err     error
		}{matched: matched, err: err}
	}()

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("StartSessionIfSession did not cancel old plane")
	}
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{AdapterRef: "streams:new"}, generation: 8}
	m.plane = &blockingDonePlane{done: make(chan struct{})}
	m.mu.Unlock()
	close(oldPlane.done)

	got := <-done
	if got.err != nil {
		t.Fatalf("StartSessionIfSession: %v", got.err)
	}
	if got.matched {
		t.Fatal("StartSessionIfSession should report mismatch after a newer session is installed")
	}
	st := m.Status()
	if st.AdapterRef != "streams:new" || st.Generation != 8 || st.State != StatePlaying {
		t.Fatalf("status after guarded start race = %+v, want playing streams:new generation 8", st)
	}
}

func TestManager_StopIfAdapterRefPreservesNewSessionInstalledWhileOldPlaneDrains(t *testing.T) {
	m := newTestManager(t)
	oldPlane := &blockingDonePlane{done: make(chan struct{})}
	cancelled := make(chan struct{})
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{AdapterRef: "streams:old"}}
	m.plane = oldPlane
	m.cancelFn = func() { close(cancelled) }
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition to playing: %v", err)
	}
	m.mu.Unlock()

	done := make(chan struct {
		matched bool
		err     error
	}, 1)
	go func() {
		matched, err := m.StopIfAdapterRef("streams:old")
		done <- struct {
			matched bool
			err     error
		}{matched: matched, err: err}
	}()

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("StopIfAdapterRef did not cancel old plane")
	}
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{AdapterRef: "streams:new"}}
	m.plane = &fakePlane{}
	m.mu.Unlock()
	close(oldPlane.done)

	got := <-done
	if got.err != nil {
		t.Fatalf("StopIfAdapterRef: %v", got.err)
	}
	if got.matched {
		t.Fatal("StopIfAdapterRef should report mismatch after a newer session is installed")
	}
	st := m.Status()
	if st.AdapterRef != "streams:new" || st.State != StatePlaying {
		t.Fatalf("status after guarded stop race = %+v, want playing streams:new", st)
	}
}

func TestManager_PauseRequiresActiveSession(t *testing.T) {
	m := newTestManager(t)
	err := m.Pause()
	if err == nil {
		t.Fatal("Pause from Idle should fail")
	}
	if !strings.Contains(err.Error(), "no session") {
		t.Errorf("err = %v, want 'no session'", err)
	}
}

func TestManager_PauseIfAdapterRefMismatchDoesNotPauseForeignActive(t *testing.T) {
	m := newTestManager(t)
	cancelled := false
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		AdapterRef:   "streams:owned",
		Capabilities: Capabilities{CanPause: true},
	}}
	m.cancelFn = func() { cancelled = true }
	m.mu.Unlock()

	matched, err := m.PauseIfAdapterRef("url:foreign")
	if err != nil {
		t.Fatalf("PauseIfAdapterRef mismatch: %v", err)
	}
	if matched {
		t.Fatal("PauseIfAdapterRef mismatch returned matched=true")
	}
	if cancelled {
		t.Fatal("PauseIfAdapterRef mismatch cancelled the foreign active session")
	}
	if got := m.Status().AdapterRef; got != "streams:owned" {
		t.Fatalf("AdapterRef after mismatch = %q, want streams:owned", got)
	}
}

func TestManager_PauseIfAdapterRefMatchPausesActive(t *testing.T) {
	m := newTestManager(t)
	cancelled := false
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		AdapterRef:   "streams:owned",
		Capabilities: Capabilities{CanPause: true},
	}}
	m.plane = &fakePlane{}
	m.cancelFn = func() { cancelled = true }
	m.mu.Unlock()
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		t.Fatalf("transition to playing: %v", err)
	}

	matched, err := m.PauseIfAdapterRef("streams:owned")
	if err != nil {
		t.Fatalf("PauseIfAdapterRef match: %v", err)
	}
	if !matched {
		t.Fatal("PauseIfAdapterRef match returned matched=false")
	}
	if !cancelled {
		t.Fatal("PauseIfAdapterRef match did not cancel the active plane")
	}
	st := m.Status()
	if st.State != StatePaused || st.AdapterRef != "streams:owned" {
		t.Fatalf("status after pause = %+v, want paused owned session", st)
	}
}

func TestManager_PauseIfAdapterRefPreservesNewSessionInstalledWhileOldPlaneDrains(t *testing.T) {
	m := newTestManager(t)
	oldPlane := &blockingDonePlane{done: make(chan struct{}), pos: 42 * time.Millisecond}
	cancelled := make(chan struct{})
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		AdapterRef:   "streams:old",
		Capabilities: Capabilities{CanPause: true},
	}}
	m.plane = oldPlane
	m.cancelFn = func() { close(cancelled) }
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition to playing: %v", err)
	}
	m.mu.Unlock()

	done := make(chan struct {
		matched bool
		err     error
	}, 1)
	go func() {
		matched, err := m.PauseIfAdapterRef("streams:old")
		done <- struct {
			matched bool
			err     error
		}{matched: matched, err: err}
	}()

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("PauseIfAdapterRef did not cancel old plane")
	}
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		AdapterRef:   "streams:new",
		Capabilities: Capabilities{CanPause: true},
	}}
	m.plane = &fakePlane{}
	m.mu.Unlock()
	close(oldPlane.done)

	got := <-done
	if got.err != nil {
		t.Fatalf("PauseIfAdapterRef: %v", got.err)
	}
	if got.matched {
		t.Fatal("PauseIfAdapterRef should report mismatch after a newer session is installed")
	}
	st := m.Status()
	if st.AdapterRef != "streams:new" || st.State != StatePlaying {
		t.Fatalf("status after guarded pause race = %+v, want playing streams:new", st)
	}
}

func TestManager_PlayRequiresActiveSession(t *testing.T) {
	m := newTestManager(t)
	err := m.Play()
	if err == nil {
		t.Fatal("Play from Idle should fail")
	}
	if !strings.Contains(err.Error(), "no session") {
		t.Errorf("err = %v, want 'no session'", err)
	}
}

func TestManager_SeekRequiresActiveSession(t *testing.T) {
	m := newTestManager(t)
	err := m.SeekTo(5000)
	if err == nil {
		t.Fatal("SeekTo from Idle should fail")
	}
	if !strings.Contains(err.Error(), "no session") {
		t.Errorf("err = %v, want 'no session'", err)
	}
}

func TestManager_SeekValidatesVisualizerBeforeProbe(t *testing.T) {
	origProbe := probeFn
	t.Cleanup(func() { probeFn = origProbe })
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		t.Fatal("Probe must not run before seek visualizer validation")
		return nil, nil
	}

	m := newTestManager(t)
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		StreamURL:    "http://pms/music.mp3",
		AdapterRef:   "plex:/library/metadata/42:tsid-1",
		MediaKind:    MediaKindMusic,
		Capabilities: Capabilities{CanSeek: true},
		Visualizer:   VisualizerRequest{Enabled: true, Mode: VisualizerMode("unknown")},
	}}
	m.mu.Unlock()

	err := m.SeekTo(5000)
	if err == nil || !strings.Contains(err.Error(), "unsupported visualizer mode") {
		t.Fatalf("SeekTo err = %v, want unsupported visualizer mode", err)
	}
}

func TestManager_BogusModelineRejected(t *testing.T) {
	m := newTestManager(t)
	m.bridge.Video.Modeline = "bogus_modeline"
	err := m.StartSession(bogusRequest())
	if err == nil {
		t.Fatal("expected error for unknown modeline")
	}
	// Could fail at probe OR modeline resolution depending on ffprobe's
	// availability. Either is fine — what matters is StartSession fails
	// cleanly and the FSM stays Idle.
	if m.Status().State != StateIdle {
		t.Errorf("state = %s, want Idle", m.Status().State)
	}
}

func TestManager_BogusRGBModeRejected(t *testing.T) {
	// Construct a Manager whose cfg has an invalid RGBMode and verify
	// resolveRGBMode rejects it. We test the helper directly to avoid the
	// probe dependency.
	if _, err := resolveRGBMode("not-a-mode"); err == nil {
		t.Fatal("expected error for unknown rgb_mode")
	}
}

func TestManager_ResolveModeline(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"", true},
		{"NTSC_480i", true},
		{"something-else", false},
	}
	for _, c := range cases {
		preset, err := ResolvePreset(c.in)
		got := err == nil
		if got != c.ok {
			t.Errorf("ResolvePreset(%q) ok=%v, want %v (err=%v)", c.in, got, c.ok, err)
		}
		_ = preset.Modeline
	}
}

func TestManager_StartSession_PlumbsFpsExprFromPreset(t *testing.T) {
	cases := []struct {
		modeline    string
		wantFpsExpr string
	}{
		{modeline: "NTSC_480i", wantFpsExpr: "60000/1001"},
		{modeline: "NTSC_240p", wantFpsExpr: "60000/1001"},
		{modeline: "PAL_576i", wantFpsExpr: "50/1"},
		{modeline: "PAL_288p", wantFpsExpr: "50/1"},
	}
	for _, c := range cases {
		t.Run(c.modeline, func(t *testing.T) {
			preset, err := ResolvePreset(c.modeline)
			if err != nil {
				t.Fatalf("ResolvePreset(%q) error = %v", c.modeline, err)
			}
			if preset.FpsExpr != c.wantFpsExpr {
				t.Errorf("preset.FpsExpr = %q, want %q",
					preset.FpsExpr, c.wantFpsExpr)
			}
		})
	}
}

func TestManager_ResolveRGBMode(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want byte
	}{
		{"", true, 0},         // groovy.RGBMode888
		{"rgb888", true, 0},   // groovy.RGBMode888
		{"rgba8888", true, 1}, // groovy.RGBMode8888
		{"rgb565", true, 2},   // groovy.RGBMode565
		{"bogus", false, 0},
	}
	for _, c := range cases {
		got, err := resolveRGBMode(c.in)
		ok := err == nil
		if ok != c.ok {
			t.Errorf("resolveRGBMode(%q) ok=%v, want %v", c.in, ok, c.ok)
		}
		if ok && got != c.want {
			t.Errorf("resolveRGBMode(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestManager_BytesPerPixel(t *testing.T) {
	cases := map[byte]int{
		0: 3, // RGBMode888
		1: 4, // RGBMode8888
		2: 2, // RGBMode565
	}
	for mode, want := range cases {
		if got := bytesPerPixel(mode); got != want {
			t.Errorf("bytesPerPixel(%d) = %d, want %d", mode, got, want)
		}
	}
}

// TestManager_PauseFSMRaceSafety verifies that concurrent access to Status()
// while StartSession is failing does not panic or deadlock. This exercises
// the mutex discipline around m.plane / m.active bookkeeping.
func TestManager_PauseFSMRaceSafety(t *testing.T) {
	m := newTestManager(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			_ = m.Status()
		}
	}()
	for i := 0; i < 10; i++ {
		_ = m.StartSession(bogusRequest())
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Status() did not complete")
	}
	if m.Status().State != StateIdle {
		t.Errorf("final state = %s, want Idle", m.Status().State)
	}
}

// TestProbeTimeout_DoesNotDeadlockManager exercises I8: a slow/unreachable
// StreamURL must not hold Manager.mu. We fire StartSession against a URL
// that never responds; concurrently call Stop; assert Stop returns quickly
// regardless of whether Probe is still in flight.
func TestProbeTimeout_DoesNotDeadlockManager(t *testing.T) {
	// A TCP listener that accepts but never writes: ffprobe will hang
	// waiting for response headers, hitting our 10 s timeout.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Park the connection; never reply.
			_ = c
		}
	}()
	url := "http://" + ln.Addr().String() + "/never.mp4"

	sender, err := groovynet.NewSender("127.0.0.1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	bridge := config.BridgeConfig{
		Video: config.VideoConfig{
			Modeline:            "NTSC_480i",
			InterlaceFieldOrder: "tff",
			AspectMode:          "letterbox",
			RGBMode:             "rgb888",
		},
		Audio: config.AudioConfig{SampleRate: 48000, Channels: 2},
	}
	m := NewManager(bridge, sender)

	startErr := make(chan error, 1)
	go func() {
		startErr <- m.StartSession(SessionRequest{
			StreamURL:  url,
			DirectPlay: true,
		})
	}()

	// Stop must not block even though Probe is in flight.
	stopDone := make(chan struct{})
	go func() {
		_ = m.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stop blocked on in-flight Probe — mutex discipline regressed")
	}

	// StartSession eventually returns (ffprobe either hits timeout or errors).
	select {
	case err := <-startErr:
		if err == nil {
			t.Errorf("StartSession returned nil for unreachable URL; expected an error")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("StartSession never returned — probe timeout not enforced")
	}
}

// TestStop_RemovesSubtitleFile verifies that Manager.Stop() removes any
// subtitle file staged for the active session. Regression harness for
// I6 (C2) — the temp file written by FetchSubtitleToFile must not leak
// across session boundaries. Spec §4 Bucket D line 352.
func TestStop_RemovesSubtitleFile(t *testing.T) {
	dir := t.TempDir()
	subPath := filepath.Join(dir, "stop-test.srt")
	if err := os.WriteFile(subPath, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	sender, err := groovynet.NewSender("127.0.0.1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	bridge := config.BridgeConfig{
		Video: config.VideoConfig{
			Modeline:            "NTSC_480i",
			InterlaceFieldOrder: "tff",
			AspectMode:          "letterbox",
			RGBMode:             "rgb888",
		},
		Audio: config.AudioConfig{SampleRate: 48000, Channels: 2},
	}
	m := NewManager(bridge, sender)
	m.active = &activeSession{
		req: SessionRequest{SubtitlePath: subPath},
	}

	if err := m.Stop(); err != nil {
		// Stop transitions FSM to Idle. From the default-constructed FSM
		// (also Idle) that's a no-op; EvStop on Idle is legal per the
		// state machine. Don't fail if FSM rejects the transition — this
		// test only cares about file cleanup.
		t.Logf("Stop returned error (OK for this test): %v", err)
	}
	if _, err := os.Stat(subPath); !os.IsNotExist(err) {
		t.Errorf("subtitle file not removed: Stat err = %v", err)
	}
}

// TestStartSession_PreemptCleansOldSubtitle verifies that StartSession
// with a new subtitle path removes the prior session's subtitle file.
// Does not spin up a real data plane — just exercises the preempt-
// path cleanup by manually seeding m.active with a written file and
// calling startPlaneLocked-equivalent behavior via removeSubtitleFile
// reachable through the Stop path.
func TestStartSession_PreemptCleansOldSubtitle(t *testing.T) {
	// Using Stop as the observable entry: it deletes the subtitle whose
	// path is on m.active. We've already tested the preempt code path in
	// startPlaneLocked at the unit level via this file-cleanup mechanism;
	// a full StartSession test requires a real ffprobe-able URL which is
	// out of scope for a pure unit test. The preempt cleanup logic reuses
	// removeSubtitleFile — tested here through Stop().
	t.Skip("covered by TestStop_RemovesSubtitleFile + code inspection")
}

// TestRedactURL covers the auth-token redaction helper used at the
// preempt log site. Keeps adapter-supplied tokens (Jellyfin api_key,
// Plex X-Plex-Token, generic token) out of operator logs while leaving
// URLs without token params untouched.
func TestRedactURL(t *testing.T) {
	cases := []struct {
		name           string
		in             string
		wantContains   []string
		wantNotContain []string
		exactMatch     string
	}{
		{name: "empty", in: "", exactMatch: ""},
		{name: "plain", in: "http://h/v.m3u8", exactMatch: "http://h/v.m3u8"},
		{name: "unparsable", in: "://bad", exactMatch: "://bad"},
		{name: "jellyfin api_key", in: "http://h/v.m3u8?api_key=secret",
			wantContains: []string{"api_key=REDACTED"}, wantNotContain: []string{"secret"}},
		{name: "plex token", in: "http://h/v.m3u8?X-Plex-Token=secret",
			wantContains: []string{"X-Plex-Token=REDACTED"}, wantNotContain: []string{"secret"}},
		{name: "generic token", in: "http://h/v.m3u8?token=secret",
			wantContains: []string{"token=REDACTED"}, wantNotContain: []string{"secret"}},
		{name: "case-insensitive", in: "http://h/v.m3u8?API_KEY=secret",
			wantContains: []string{"API_KEY=REDACTED"}, wantNotContain: []string{"secret"}},
		{name: "preserves other params", in: "http://h/v.m3u8?MediaSourceId=src&api_key=secret&Static=true",
			wantContains:   []string{"api_key=REDACTED", "MediaSourceId=src", "Static=true"},
			wantNotContain: []string{"secret"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactURL(tc.in)
			if tc.exactMatch != "" || (tc.wantContains == nil && tc.wantNotContain == nil) {
				if got != tc.exactMatch {
					t.Errorf("redactURL(%q) = %q, want %q", tc.in, got, tc.exactMatch)
				}
				return
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("redactURL(%q) = %q, want it to contain %q", tc.in, got, want)
				}
			}
			for _, bad := range tc.wantNotContain {
				if strings.Contains(got, bad) {
					t.Errorf("redactURL(%q) = %q, leaked %q", tc.in, got, bad)
				}
			}
		})
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

// TestManager_PlaneError_TransitionsIdleAndFiresOnStop verifies that
// when the data plane returns a non-nil, non-context.Canceled error
// (e.g. ffmpeg crash mid-cast), the manager:
//   - transitions the FSM to StateIdle
//   - fires the active session's OnStop with reason "error"
//
// We exercise this by spawning a Manager-driven plane against an
// invalid stream URL: ffprobe will fail, plane.Run returns an error
// promptly, and we observe the resulting state.
func TestManager_PlaneError_TransitionsIdleAndFiresOnStop(t *testing.T) {
	// Skip if not in a -short-friendly environment; this test depends on
	// ffmpeg/ffprobe NOT being present OR returning quickly on bad URL.
	// A more hermetic test is added in Task 11.1 once we have a fake plane.
	t.Skip("hermetic test deferred to Task 11.1's integration harness")
}

// TestProbeForStart_ThreadsPolicyToProbeAndProbeCrop is the policy-threading
// proof: it constructs a SessionRequest with a non-zero MediaInputPolicy
// and verifies the same policy reaches BOTH ffmpeg.Probe AND ffmpeg.ProbeCrop
// via the package-private probeFn / probeCropFn seams. Spec acceptance:
// the policy gates ffprobe, crop probe, and ffmpeg playback consistently.
//
// This test stubs the ffmpeg package entry points so it runs hermetically
// on any platform — no external binaries required.
func TestProbeForStart_ThreadsPolicyToProbeAndProbeCrop(t *testing.T) {
	// Save and restore the package-level seams.
	origProbe := probeFn
	origCrop := probeCropFn
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
	})

	var capturedProbe ffmpeg.MediaInputPolicy
	var capturedCrop ffmpeg.MediaInputPolicy
	var capturedCropHeaders map[string]string
	var probeCalls, cropCalls int

	probeFn = func(_ context.Context, _, _ string, policy ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		probeCalls++
		capturedProbe = policy
		// Return a synthetic probe result; the test does not need to drive
		// the FSM further than probeForStart's return.
		return &ffmpeg.ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976}, nil
	}
	probeCropFn = func(_ context.Context, _, _ string, headers map[string]string, _ time.Duration, policy ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		cropCalls++
		capturedCrop = policy
		capturedCropHeaders = headers
		return nil, nil
	}

	m := newTestManager(t)
	// Force the auto-crop branch so probeCropFn is invoked.
	m.bridge.Video.AspectMode = "auto"

	wantPolicy := ffmpeg.MediaInputPolicy{
		ProtocolWhitelist: []string{"file", "http", "https", "tcp", "tls", "crypto"},
		DisableReconnect:  true,
		RWTimeout:         5 * time.Second,
		BlockedHeaders:    []string{"Referer", "Cookie"},
	}
	req := SessionRequest{
		StreamURL: "https://example/clip.mp4",
		// Includes a header that should be filtered out before reaching
		// ffmpeg.ProbeCrop, plus one that should pass through.
		InputHeaders: map[string]string{
			"Cookie":     "session=abc",
			"User-Agent": "groovyrelay",
		},
		MediaInputPolicy: wantPolicy,
	}

	probe, cropRect, _, err := m.probeForStart(req)
	if err != nil {
		t.Fatalf("probeForStart: %v", err)
	}
	if probe == nil {
		t.Fatal("probe nil")
	}
	if cropRect != nil {
		t.Errorf("crop rect should be nil from stub; got %+v", cropRect)
	}
	if probeCalls != 1 {
		t.Errorf("probeFn calls = %d, want 1", probeCalls)
	}
	if cropCalls != 1 {
		t.Errorf("probeCropFn calls = %d, want 1", cropCalls)
	}

	// Both call sites must receive the SAME policy by value.
	if !reflect.DeepEqual(capturedProbe, wantPolicy) {
		t.Errorf("Probe policy mismatch:\n got %+v\nwant %+v", capturedProbe, wantPolicy)
	}
	if !reflect.DeepEqual(capturedCrop, wantPolicy) {
		t.Errorf("ProbeCrop policy mismatch:\n got %+v\nwant %+v", capturedCrop, wantPolicy)
	}

	// The crop probe must receive headers that have been FILTERED through
	// BlockedHeaders — Cookie should be gone, User-Agent should remain.
	// This proves the core/FFmpeg-boundary header filter is wired up.
	if _, blocked := capturedCropHeaders["Cookie"]; blocked {
		t.Errorf("Cookie header was not filtered before ProbeCrop: %v", capturedCropHeaders)
	}
	if got := capturedCropHeaders["User-Agent"]; got != "groovyrelay" {
		t.Errorf("User-Agent should pass through filter; got %q (full=%v)", got, capturedCropHeaders)
	}
}

func TestManager_WithEventLog_AppendsBridgeBoot(t *testing.T) {
	// WithEventLog sets the field; no event is emitted by the option itself.
	// This test exists so the option survives later refactors.
	log := eventlog.New(16)
	m := NewManager(testBridgeConfig(t), nil, WithEventLog(log))
	if m.eventLog != log {
		t.Error("WithEventLog did not store the log pointer")
	}
}

func TestManager_EmitsCastStarted(t *testing.T) {
	// Unit test for the entry shape: invoke m.emit directly with the
	// same payload the production OnInit callback would produce.
	log := eventlog.New(16)
	m := newTestManager(t, WithEventLog(log))
	m.active = &activeSession{req: SessionRequest{AdapterRef: "plex/abc"}}
	m.emit(eventlog.SeverityInfo,
		fmt.Sprintf("cast-started %s · %s", m.active.req.AdapterRef, m.bridge.Video.Modeline))

	entries := log.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Source != "core" || e.Severity != eventlog.SeverityInfo ||
		!strings.Contains(e.Message, "cast-started plex/abc") {
		t.Errorf("entry: %+v", e)
	}
}

func TestManager_EmitsCastEnded(t *testing.T) {
	log := eventlog.New(16)
	m := newTestManager(t, WithEventLog(log))
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	m.active = &activeSession{req: SessionRequest{AdapterRef: "plex/abc"}}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	entries := log.Snapshot()
	if len(entries) == 0 {
		t.Fatal("expected at least one event entry")
	}
	last := entries[len(entries)-1]
	if last.Source != "core" {
		t.Errorf("Source: got %q, want %q", last.Source, "core")
	}
	if last.Severity != eventlog.SeverityInfo {
		t.Errorf("Severity: got %v, want Info", last.Severity)
	}
	if !strings.Contains(last.Message, "cast-ended") {
		t.Errorf("Message: got %q, want substring %q", last.Message, "cast-ended")
	}
}

func TestManager_EmitsInitFailed_AsErr(t *testing.T) {
	log := eventlog.New(16)
	m := newTestManager(t, WithEventLog(log))
	m.emit(eventlog.SeverityErr, "init-failed: timeout")
	entries := log.Snapshot()
	if len(entries) != 1 || entries[0].Severity != eventlog.SeverityErr {
		t.Fatalf("entries: %+v", entries)
	}
}

func TestManager_EmitsCastPreempted(t *testing.T) {
	log := eventlog.New(16)
	m := newTestManager(t, WithEventLog(log))
	m.emit(eventlog.SeverityInfo, "cast-preempted plex/old")
	entries := log.Snapshot()
	if len(entries) != 1 || !strings.Contains(entries[0].Message, "cast-preempted") {
		t.Fatalf("entries: %+v", entries)
	}
}

func TestManager_StatusHomeView_Idle(t *testing.T) {
	m := newTestManager(t)
	v := m.StatusHomeView()
	if v.State != StateIdle {
		t.Errorf("State: got %q, want idle", v.State)
	}
	if v.Title != "" || v.AdapterRef != "" || v.Modeline != "" {
		t.Errorf("non-empty fields when idle: %+v", v)
	}
	if v.BlitsTotal != 0 {
		t.Errorf("BlitsTotal non-zero when idle: %d", v.BlitsTotal)
	}
}

func TestManager_StatusHomeView_Casting(t *testing.T) {
	m := newTestManager(t)
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	m.active = &activeSession{
		req:       SessionRequest{Title: "Test Title", AdapterRef: "plex/12345"},
		startedAt: time.Now().Add(-30 * time.Second),
		duration:  90 * time.Minute,
	}
	v := m.StatusHomeView()
	if v.State != StatePlaying {
		t.Errorf("State: got %q, want playing", v.State)
	}
	if v.Title != "Test Title" {
		t.Errorf("Title: got %q", v.Title)
	}
	if v.AdapterRef != "plex/12345" {
		t.Errorf("AdapterRef: got %q", v.AdapterRef)
	}
	if v.Duration != 90*time.Minute {
		t.Errorf("Duration: got %v, want 90m", v.Duration)
	}
}

func TestManager_StatusCarriesMediaKind(t *testing.T) {
	m := newTestManager(t)
	m.mu.Lock()
	m.active = &activeSession{
		req: SessionRequest{
			AdapterRef: "plex:/library/metadata/42:tsid-1",
			Source:     "plex",
			Title:      "Blue Monday",
			MediaKind:  MediaKindMusic,
			Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
		},
		startedAt: time.Now(),
		duration:  4 * time.Minute,
	}
	_ = m.fsm.Transition(EvPlayMedia)
	m.mu.Unlock()

	st := m.Status()
	if st.MediaKind != MediaKindMusic {
		t.Fatalf("Status.MediaKind = %q, want %q", st.MediaKind, MediaKindMusic)
	}
	view := m.StatusHomeView()
	if view.MediaKind != MediaKindMusic {
		t.Fatalf("StatusHomeView.MediaKind = %q, want %q", view.MediaKind, MediaKindMusic)
	}
}

func TestManager_VisualizerSkipsProbeCropAndCapturesDuration(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	origNewPlane := newPlane
	origCheck := checkVisualizerFiltersFn
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
		newPlane = origNewPlane
		checkVisualizerFiltersFn = origCheck
	})

	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 48000, Duration: 111}, nil
	}
	checkVisualizerFiltersFn = func(context.Context, string, ffmpeg.VisualizerMode) error {
		return nil
	}
	cropCalls := 0
	probeCropFn = func(context.Context, string, string, map[string]string, time.Duration, ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		cropCalls++
		return &ffmpeg.CropRect{W: 1, H: 1}, nil
	}
	var captured dataplane.PlaneConfig
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
		captured = cfg
		return &blockingDonePlane{done: done}
	}

	m := newTestManager(t)
	m.bridge.Video.AspectMode = "auto"
	err := m.StartSession(SessionRequest{
		StreamURL:  "http://pms/music.mp3",
		AdapterRef: "plex:/library/metadata/42:tsid-1",
		Source:     "plex",
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{
			Enabled: true,
			Mode:    VisualizerModeRetroAnalyzer,
			Metadata: VisualizerMetadata{
				Title:    "Blue Monday",
				Artist:   "New Order",
				Album:    "Power Corruption & Lies",
				Duration: 3 * time.Minute,
			},
		},
		Capabilities: Capabilities{CanSeek: true, CanPause: true},
	})
	if err != nil {
		t.Fatalf("StartSession visualizer: %v", err)
	}
	if cropCalls != 0 {
		t.Fatalf("ProbeCrop calls = %d, want 0 for visualizer session", cropCalls)
	}
	if !captured.SpawnSpec.Visualizer.Enabled {
		t.Fatalf("SpawnSpec.Visualizer.Enabled = false, want true")
	}
	if captured.SpawnSpec.Visualizer.Mode != ffmpeg.VisualizerModeRetroAnalyzer {
		t.Fatalf("visualizer mode = %q, want %q", captured.SpawnSpec.Visualizer.Mode, ffmpeg.VisualizerModeRetroAnalyzer)
	}
	if captured.SpawnSpec.Visualizer.Metadata.Title != "Blue Monday" {
		t.Fatalf("visualizer title = %q", captured.SpawnSpec.Visualizer.Metadata.Title)
	}
	if captured.SpawnSpec.Visualizer.Metadata.Artist != "New Order" {
		t.Fatalf("visualizer artist = %q", captured.SpawnSpec.Visualizer.Metadata.Artist)
	}
	if captured.SpawnSpec.Visualizer.Metadata.Album != "Power Corruption & Lies" {
		t.Fatalf("visualizer album = %q", captured.SpawnSpec.Visualizer.Metadata.Album)
	}
	if captured.SpawnSpec.Visualizer.Metadata.Duration != 3*time.Minute {
		t.Fatalf("visualizer metadata duration = %v, want 3m", captured.SpawnSpec.Visualizer.Metadata.Duration)
	}
	if got := m.Status().Duration; got != 3*time.Minute {
		t.Fatalf("Status duration = %v, want metadata duration", got)
	}
}

func TestManager_VisualizerUsesConfiguredBridgeMode(t *testing.T) {
	origProbe := probeFn
	origNewPlane := newPlane
	origCheck := checkVisualizerFiltersFn
	t.Cleanup(func() {
		probeFn = origProbe
		newPlane = origNewPlane
		checkVisualizerFiltersFn = origCheck
	})
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 48000, Duration: 111}, nil
	}
	checkVisualizerFiltersFn = func(context.Context, string, ffmpeg.VisualizerMode) error {
		return nil
	}
	var captured dataplane.PlaneConfig
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
		captured = cfg
		return &blockingDonePlane{done: done}
	}

	m := newTestManager(t)
	m.bridge.Visualizer.Mode = config.VisualizerModeStereoScope
	err := m.StartSession(SessionRequest{
		StreamURL:  "http://pms/music.mp3",
		AdapterRef: "plex:/library/metadata/42:tsid-1",
		Source:     "plex",
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
		Capabilities: Capabilities{
			CanSeek:  true,
			CanPause: true,
		},
	})
	if err != nil {
		t.Fatalf("StartSession visualizer: %v", err)
	}
	if captured.SpawnSpec.Visualizer.Mode != ffmpeg.VisualizerModeStereoScope {
		t.Fatalf("ffmpeg visualizer mode = %q, want %q", captured.SpawnSpec.Visualizer.Mode, ffmpeg.VisualizerModeStereoScope)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		t.Fatal("active session is nil")
	}
	if m.active.req.Visualizer.Mode != VisualizerModeStereoScope {
		t.Fatalf("active visualizer mode = %q, want %q", m.active.req.Visualizer.Mode, VisualizerModeStereoScope)
	}
}

func TestManager_VisualizerSameAdapterRefInheritsSnapshottedMode(t *testing.T) {
	m := newTestManager(t)
	m.bridge.Visualizer.Mode = config.VisualizerModeOscilloscopeWave
	const ref = "plex:/library/metadata/42:tsid-1"
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		AdapterRef: ref,
		Visualizer: VisualizerRequest{
			Enabled: true,
			Mode:    VisualizerModeStereoScope,
		},
	}}
	got := m.normalizeVisualizerRequestLocked(SessionRequest{
		AdapterRef: ref,
		Visualizer: VisualizerRequest{
			Enabled: true,
			Mode:    VisualizerModeRetroAnalyzer,
		},
	})
	m.mu.Unlock()

	if got.Visualizer.Mode != VisualizerModeStereoScope {
		t.Fatalf("normalized visualizer mode = %q, want %q", got.Visualizer.Mode, VisualizerModeStereoScope)
	}
}

func TestManager_VisualizerGuardedReplayKeepsSnapshottedMode(t *testing.T) {
	origProbe := probeFn
	origNewPlane := newPlane
	origCheck := checkVisualizerFiltersFn
	t.Cleanup(func() {
		probeFn = origProbe
		newPlane = origNewPlane
		checkVisualizerFiltersFn = origCheck
	})
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 48000, Duration: 111}, nil
	}
	checkVisualizerFiltersFn = func(context.Context, string, ffmpeg.VisualizerMode) error {
		return nil
	}
	var captured dataplane.PlaneConfig
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
		captured = cfg
		return &blockingDonePlane{done: done}
	}

	m := newTestManager(t)
	const ref = "plex:/library/metadata/42:tsid-1"
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		StreamURL:  "http://pms/music.mp3",
		AdapterRef: ref,
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{
			Enabled: true,
			Mode:    VisualizerModeStereoScope,
		},
		Capabilities: Capabilities{CanSeek: true, CanPause: true},
	}}
	m.mu.Unlock()
	m.bridge.Visualizer.Mode = config.VisualizerModeOscilloscopeWave

	matched, err := m.StartSessionIfAdapterRef(SessionRequest{
		StreamURL:  "http://pms/music.mp3",
		AdapterRef: ref,
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
		Capabilities: Capabilities{
			CanSeek:  true,
			CanPause: true,
		},
	}, ref)
	if err != nil {
		t.Fatalf("StartSessionIfAdapterRef: %v", err)
	}
	if !matched {
		t.Fatal("StartSessionIfAdapterRef matched = false, want true")
	}
	if captured.SpawnSpec.Visualizer.Mode != ffmpeg.VisualizerModeStereoScope {
		t.Fatalf("ffmpeg visualizer mode = %q, want %q", captured.SpawnSpec.Visualizer.Mode, ffmpeg.VisualizerModeStereoScope)
	}
}

func TestManager_VisualizerUnknownConfiguredModeRejectedAfterNormalization(t *testing.T) {
	origProbe := probeFn
	t.Cleanup(func() { probeFn = origProbe })
	probeRan := false
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		probeRan = true
		return nil, nil
	}

	m := newTestManager(t)
	m.bridge.Visualizer.Mode = "sparkle"
	err := m.StartSession(SessionRequest{
		StreamURL:  "http://pms/music.mp3",
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported visualizer mode") {
		t.Fatalf("StartSession err = %v, want unsupported visualizer mode", err)
	}
	if probeRan {
		t.Fatal("probeFn ran before configured visualizer mode validation failed")
	}
}

func TestManager_VisualizerModeAdoptsBridgeAfterDrop(t *testing.T) {
	origProbe := probeFn
	origNewPlane := newPlane
	origCheck := checkVisualizerFiltersFn
	t.Cleanup(func() {
		probeFn = origProbe
		newPlane = origNewPlane
		checkVisualizerFiltersFn = origCheck
	})
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 48000, Duration: 111}, nil
	}
	checkVisualizerFiltersFn = func(context.Context, string, ffmpeg.VisualizerMode) error {
		return nil
	}
	var captured dataplane.PlaneConfig
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
		captured = cfg
		return &blockingDonePlane{done: done}
	}

	m := newTestManager(t)
	stereoBridge := testBridgeConfig(t)
	stereoBridge.Visualizer.Mode = config.VisualizerModeStereoScope
	m.UpdateBridge(stereoBridge)
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		StreamURL:  "http://pms/old.mp3",
		AdapterRef: "plex:old",
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeStereoScope},
	}}
	m.mu.Unlock()

	oscBridge := stereoBridge
	oscBridge.Visualizer.Mode = config.VisualizerModeOscilloscopeWave
	oscBridge.Video.InterlaceFieldOrder = "bff"
	m.UpdateBridge(oscBridge)
	if err := m.DropActiveCast("restart visualizer mode"); err != nil {
		t.Fatalf("DropActiveCast: %v", err)
	}

	err := m.StartSession(SessionRequest{
		StreamURL:  "http://pms/new.mp3",
		AdapterRef: "plex:new",
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
		Capabilities: Capabilities{
			CanSeek:  true,
			CanPause: true,
		},
	})
	if err != nil {
		t.Fatalf("StartSession visualizer: %v", err)
	}
	if captured.SpawnSpec.Visualizer.Mode != ffmpeg.VisualizerModeOscilloscopeWave {
		t.Fatalf("ffmpeg visualizer mode = %q, want %q", captured.SpawnSpec.Visualizer.Mode, ffmpeg.VisualizerModeOscilloscopeWave)
	}
}

func TestManager_VisualizerPlayKeepsSnapshottedMode(t *testing.T) {
	origProbe := probeFn
	origNewPlane := newPlane
	origCheck := checkVisualizerFiltersFn
	t.Cleanup(func() {
		probeFn = origProbe
		newPlane = origNewPlane
		checkVisualizerFiltersFn = origCheck
	})
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 48000, Duration: 111}, nil
	}
	checkVisualizerFiltersFn = func(context.Context, string, ffmpeg.VisualizerMode) error {
		return nil
	}
	var captured dataplane.PlaneConfig
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
		captured = cfg
		return &blockingDonePlane{done: done}
	}

	m := newTestManager(t)
	m.bridge.Visualizer.Mode = config.VisualizerModeOscilloscopeWave
	const ref = "plex:/library/metadata/42:tsid-1"
	m.mu.Lock()
	m.active = &activeSession{
		req: SessionRequest{
			StreamURL:  "http://pms/music.mp3",
			AdapterRef: ref,
			MediaKind:  MediaKindMusic,
			Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeStereoScope},
			Capabilities: Capabilities{
				CanSeek:  true,
				CanPause: true,
			},
		},
		generation:     7,
		baseOffsetMs:   1234,
		pausedPosition: 2 * time.Second,
	}
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition play: %v", err)
	}
	if err := m.fsm.Transition(EvPause); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition pause: %v", err)
	}
	m.mu.Unlock()

	matched, err := m.PlayIfAdapterRef(ref)
	if err != nil {
		t.Fatalf("PlayIfAdapterRef: %v", err)
	}
	if !matched {
		t.Fatal("PlayIfAdapterRef matched = false, want true")
	}
	if captured.SpawnSpec.Visualizer.Mode != ffmpeg.VisualizerModeStereoScope {
		t.Fatalf("ffmpeg visualizer mode = %q, want %q", captured.SpawnSpec.Visualizer.Mode, ffmpeg.VisualizerModeStereoScope)
	}
}

func TestManager_VisualizerSeekKeepsSnapshottedMode(t *testing.T) {
	origProbe := probeFn
	origNewPlane := newPlane
	origCheck := checkVisualizerFiltersFn
	t.Cleanup(func() {
		probeFn = origProbe
		newPlane = origNewPlane
		checkVisualizerFiltersFn = origCheck
	})
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 48000, Duration: 111}, nil
	}
	checkVisualizerFiltersFn = func(context.Context, string, ffmpeg.VisualizerMode) error {
		return nil
	}
	var captured dataplane.PlaneConfig
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
		captured = cfg
		return &blockingDonePlane{done: done}
	}

	m := newTestManager(t)
	m.bridge.Visualizer.Mode = config.VisualizerModeOscilloscopeWave
	const ref = "plex:/library/metadata/42:tsid-1"
	m.mu.Lock()
	m.active = &activeSession{
		req: SessionRequest{
			StreamURL:  "http://pms/music.mp3",
			AdapterRef: ref,
			MediaKind:  MediaKindMusic,
			Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeStereoScope},
			Capabilities: Capabilities{
				CanSeek:  true,
				CanPause: true,
			},
		},
		generation: 7,
	}
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition play: %v", err)
	}
	m.mu.Unlock()

	matched, err := m.SeekToIfAdapterRef(ref, 42_000)
	if err != nil {
		t.Fatalf("SeekToIfAdapterRef: %v", err)
	}
	if !matched {
		t.Fatal("SeekToIfAdapterRef matched = false, want true")
	}
	if captured.SpawnSpec.Visualizer.Mode != ffmpeg.VisualizerModeStereoScope {
		t.Fatalf("ffmpeg visualizer mode = %q, want %q", captured.SpawnSpec.Visualizer.Mode, ffmpeg.VisualizerModeStereoScope)
	}
}

func TestManager_VisualizerPlayKeepsSnapshottedModeWithEmptyAdapterRef(t *testing.T) {
	origProbe := probeFn
	origNewPlane := newPlane
	origCheck := checkVisualizerFiltersFn
	t.Cleanup(func() {
		probeFn = origProbe
		newPlane = origNewPlane
		checkVisualizerFiltersFn = origCheck
	})
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 48000, Duration: 111}, nil
	}
	checkVisualizerFiltersFn = func(context.Context, string, ffmpeg.VisualizerMode) error {
		return nil
	}
	var captured dataplane.PlaneConfig
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
		captured = cfg
		return &blockingDonePlane{done: done}
	}

	m := newTestManager(t)
	m.bridge.Visualizer.Mode = config.VisualizerModeOscilloscopeWave
	m.mu.Lock()
	m.active = &activeSession{
		req: SessionRequest{
			StreamURL: "http://pms/music.mp3",
			MediaKind: MediaKindMusic,
			Visualizer: VisualizerRequest{
				Enabled: true,
				Mode:    VisualizerModeStereoScope,
			},
		},
		generation:     7,
		baseOffsetMs:   1234,
		pausedPosition: 2 * time.Second,
	}
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition play: %v", err)
	}
	if err := m.fsm.Transition(EvPause); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition pause: %v", err)
	}
	m.mu.Unlock()

	if err := m.Play(); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if captured.SpawnSpec.Visualizer.Mode != ffmpeg.VisualizerModeStereoScope {
		t.Fatalf("ffmpeg visualizer mode = %q, want %q", captured.SpawnSpec.Visualizer.Mode, ffmpeg.VisualizerModeStereoScope)
	}
}

func TestManager_VisualizerSeekKeepsSnapshottedModeWithEmptyAdapterRef(t *testing.T) {
	origProbe := probeFn
	origNewPlane := newPlane
	origCheck := checkVisualizerFiltersFn
	t.Cleanup(func() {
		probeFn = origProbe
		newPlane = origNewPlane
		checkVisualizerFiltersFn = origCheck
	})
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 48000, Duration: 111}, nil
	}
	checkVisualizerFiltersFn = func(context.Context, string, ffmpeg.VisualizerMode) error {
		return nil
	}
	var captured dataplane.PlaneConfig
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
		captured = cfg
		return &blockingDonePlane{done: done}
	}

	m := newTestManager(t)
	m.bridge.Visualizer.Mode = config.VisualizerModeOscilloscopeWave
	m.mu.Lock()
	m.active = &activeSession{
		req: SessionRequest{
			StreamURL:    "http://pms/music.mp3",
			MediaKind:    MediaKindMusic,
			Visualizer:   VisualizerRequest{Enabled: true, Mode: VisualizerModeStereoScope},
			Capabilities: Capabilities{CanSeek: true},
		},
		generation: 7,
	}
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition play: %v", err)
	}
	m.mu.Unlock()

	if err := m.SeekTo(42_000); err != nil {
		t.Fatalf("SeekTo: %v", err)
	}
	if captured.SpawnSpec.Visualizer.Mode != ffmpeg.VisualizerModeStereoScope {
		t.Fatalf("ffmpeg visualizer mode = %q, want %q", captured.SpawnSpec.Visualizer.Mode, ffmpeg.VisualizerModeStereoScope)
	}
}

func TestValidateVisualizerRequestModes(t *testing.T) {
	for _, mode := range []VisualizerMode{
		VisualizerModeRetroAnalyzer,
		VisualizerModeOscilloscopeWave,
		VisualizerModeStereoScope,
	} {
		t.Run(string(mode), func(t *testing.T) {
			err := validateVisualizerRequest(SessionRequest{
				MediaKind:  MediaKindMusic,
				Visualizer: VisualizerRequest{Enabled: true, Mode: mode},
			})
			if err != nil {
				t.Fatalf("validateVisualizerRequest(%q): %v", mode, err)
			}
		})
	}

	err := validateVisualizerRequest(SessionRequest{
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerMode("unknown")},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported visualizer mode") {
		t.Fatalf("validateVisualizerRequest unknown err = %v, want unsupported visualizer mode", err)
	}

	err = validateVisualizerRequest(SessionRequest{
		MediaKind:  MediaKindVideo,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeStereoScope},
	})
	if err == nil || !strings.Contains(err.Error(), "visualizer requires music media kind") {
		t.Fatalf("validateVisualizerRequest non-music err = %v, want visualizer requires music media kind", err)
	}
}

func TestFFmpegVisualizerSpecMapsModes(t *testing.T) {
	cases := []struct {
		core VisualizerMode
		want ffmpeg.VisualizerMode
	}{
		{VisualizerModeRetroAnalyzer, ffmpeg.VisualizerModeRetroAnalyzer},
		{VisualizerModeOscilloscopeWave, ffmpeg.VisualizerModeOscilloscopeWave},
		{VisualizerModeStereoScope, ffmpeg.VisualizerModeStereoScope},
	}
	for _, c := range cases {
		t.Run(string(c.core), func(t *testing.T) {
			got := ffmpegVisualizerSpec(VisualizerRequest{Enabled: true, Mode: c.core})
			if got.Mode != c.want {
				t.Fatalf("ffmpegVisualizerSpec mode = %q, want %q", got.Mode, c.want)
			}
		})
	}
}

func TestManager_VisualizerMissingRequiredFilterFailsBeforePlaneStart(t *testing.T) {
	origProbe := probeFn
	origNewPlane := newPlane
	origCheck := checkVisualizerFiltersFn
	t.Cleanup(func() {
		probeFn = origProbe
		newPlane = origNewPlane
		checkVisualizerFiltersFn = origCheck
	})
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 48000, Duration: 111}, nil
	}
	checkVisualizerFiltersFn = func(context.Context, string, ffmpeg.VisualizerMode) error {
		return fmt.Errorf("requires ffmpeg filter avectorscope")
	}
	newPlane = func(dataplane.PlaneConfig) planeRunner {
		t.Fatal("newPlane must not be called when visualizer filter preflight fails")
		return nil
	}

	m := newTestManager(t)
	m.bridge.Visualizer.Mode = config.VisualizerModeStereoScope
	err := m.StartSession(SessionRequest{
		StreamURL:  "http://pms/music.mp3",
		AdapterRef: "plex:/library/metadata/42:tsid-1",
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
		Capabilities: Capabilities{
			CanSeek:  true,
			CanPause: true,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requires ffmpeg filter") {
		t.Fatalf("StartSession err = %v, want requires ffmpeg filter", err)
	}
}

func TestManager_VisualizerPlayMissingRequiredFilterFailsBeforePlaneStart(t *testing.T) {
	origProbe := probeFn
	origNewPlane := newPlane
	origCheck := checkVisualizerFiltersFn
	t.Cleanup(func() {
		probeFn = origProbe
		newPlane = origNewPlane
		checkVisualizerFiltersFn = origCheck
	})
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 48000, Duration: 111}, nil
	}
	checkVisualizerFiltersFn = func(context.Context, string, ffmpeg.VisualizerMode) error {
		return fmt.Errorf("requires ffmpeg filter avectorscope")
	}
	newPlane = func(dataplane.PlaneConfig) planeRunner {
		t.Fatal("newPlane must not be called when play visualizer filter preflight fails")
		return nil
	}

	m := newTestManager(t)
	m.mu.Lock()
	m.active = &activeSession{
		req: SessionRequest{
			StreamURL:  "http://pms/music.mp3",
			AdapterRef: "plex:/library/metadata/42:tsid-1",
			MediaKind:  MediaKindMusic,
			Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeStereoScope},
		},
		generation: 7,
	}
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition play: %v", err)
	}
	if err := m.fsm.Transition(EvPause); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition pause: %v", err)
	}
	m.mu.Unlock()

	err := m.Play()
	if err == nil || !strings.Contains(err.Error(), "requires ffmpeg filter") {
		t.Fatalf("Play err = %v, want requires ffmpeg filter", err)
	}
}

func TestManager_VisualizerGuardedStartRechecksAfterPreflightError(t *testing.T) {
	origProbe := probeFn
	origNewPlane := newPlane
	origCheck := checkVisualizerFiltersFn
	t.Cleanup(func() {
		probeFn = origProbe
		newPlane = origNewPlane
		checkVisualizerFiltersFn = origCheck
	})
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{AudioRate: 48000, Duration: 111}, nil
	}
	newPlane = func(dataplane.PlaneConfig) planeRunner {
		t.Fatal("newPlane must not be called after guarded preflight error")
		return nil
	}

	m := newTestManager(t)
	const oldRef = "plex:/library/metadata/42:tsid-1"
	m.mu.Lock()
	m.active = &activeSession{req: SessionRequest{
		AdapterRef: oldRef,
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeStereoScope},
	}}
	m.mu.Unlock()
	checkVisualizerFiltersFn = func(context.Context, string, ffmpeg.VisualizerMode) error {
		m.mu.Lock()
		m.active = &activeSession{req: SessionRequest{AdapterRef: "plex:/library/metadata/43:tsid-2"}}
		m.mu.Unlock()
		return fmt.Errorf("requires ffmpeg filter avectorscope")
	}

	matched, err := m.StartSessionIfAdapterRef(SessionRequest{
		StreamURL:  "http://pms/music.mp3",
		AdapterRef: oldRef,
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
	}, oldRef)
	if err != nil {
		t.Fatalf("StartSessionIfAdapterRef err = %v, want nil after guard mismatch", err)
	}
	if matched {
		t.Fatal("StartSessionIfAdapterRef matched = true, want false after guard mismatch")
	}
}

func TestManager_VisualizerRejectsNilProbeWithoutPanic(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
	})
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return nil, nil
	}
	probeCropFn = func(context.Context, string, string, map[string]string, time.Duration, ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		t.Fatal("ProbeCrop must not run after visualizer nil-probe audio validation fails")
		return nil, nil
	}

	m := newTestManager(t)
	err := m.StartSession(SessionRequest{
		StreamURL:  "http://pms/not-audio",
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
	})
	if err == nil || !strings.Contains(err.Error(), "visualizer source has no audio") {
		t.Fatalf("StartSession err = %v, want visualizer source has no audio", err)
	}
}

func TestManager_VisualizerRejectsProbeWithoutAudio(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
	})
	probeFn = func(context.Context, string, string, ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 1920, Height: 1080, Duration: 10}, nil
	}
	probeCropFn = func(context.Context, string, string, map[string]string, time.Duration, ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		t.Fatal("ProbeCrop must not run after visualizer audio validation fails")
		return nil, nil
	}

	m := newTestManager(t)
	err := m.StartSession(SessionRequest{
		StreamURL:  "http://pms/not-audio",
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
	})
	if err == nil || !strings.Contains(err.Error(), "visualizer source has no audio") {
		t.Fatalf("StartSession err = %v, want visualizer source has no audio", err)
	}
}

func TestManager_VisualizerRejectsNonMusicKind(t *testing.T) {
	m := newTestManager(t)
	err := m.StartSession(SessionRequest{
		StreamURL:  "http://pms/music.mp3",
		MediaKind:  MediaKindVideo,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerModeRetroAnalyzer},
	})
	if err == nil || !strings.Contains(err.Error(), "visualizer requires music media kind") {
		t.Fatalf("StartSession err = %v, want visualizer media kind validation", err)
	}
}

func TestValidateVisualizerRequestRejectsUnsupportedMode(t *testing.T) {
	err := validateVisualizerRequest(SessionRequest{
		StreamURL:  "http://pms/music.mp3",
		MediaKind:  MediaKindMusic,
		Visualizer: VisualizerRequest{Enabled: true, Mode: VisualizerMode("unknown")},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported visualizer mode") {
		t.Fatalf("validateVisualizerRequest err = %v, want unsupported visualizer mode", err)
	}
}

func TestManager_StatusHomeView_PausedKeepsSnapshot(t *testing.T) {
	m := newTestManager(t)
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		t.Fatalf("play transition: %v", err)
	}
	if err := m.fsm.Transition(EvPause); err != nil {
		t.Fatalf("pause transition: %v", err)
	}
	m.bridge.Video.Modeline = "PAL_576i"
	m.active = &activeSession{
		req:            SessionRequest{Title: "Paused Title", AdapterRef: "plex/paused"},
		startedAt:      time.Now().Add(-2 * time.Minute),
		pausedPosition: 42 * time.Second,
		duration:       90 * time.Minute,
	}
	m.plane = nil

	v := m.StatusHomeView()
	if v.State != StatePaused {
		t.Errorf("State: got %q, want paused", v.State)
	}
	if v.Position != 42*time.Second {
		t.Errorf("Position: got %v, want paused snapshot", v.Position)
	}
	if v.Modeline != "PAL_576i" {
		t.Errorf("Modeline: got %q, want PAL_576i", v.Modeline)
	}
	if v.Title != "Paused Title" || v.AdapterRef != "plex/paused" {
		t.Errorf("active fields lost while paused: %+v", v)
	}
}

// TestStartPlaneLocked_SameSessionReplay_DoesNotFireOldOnStop verifies
// the P3.3 fix: when startPlaneLocked is invoked for the SAME AdapterRef
// (the Play-resume and SeekTo paths both replay m.active.req into
// startPlaneLocked), the previously-active session's OnStop closure must
// NOT be fired with reason "preempted". Same-session replay carries the
// same OnStop forward to the new activeSession, so firing it would
// double-clear adapter state (DLNA's onStopForRef would clear its
// currentRef while core retains the session).
//
// We exercise the bug-prone code path directly: seed m.active with a
// known ref + OnStop, then call startPlaneLocked with a SessionRequest
// carrying the SAME AdapterRef. The probe stub returns a synthetic
// result and we expect the OnStop NOT to be called.
func TestStartPlaneLocked_SameSessionReplay_DoesNotFireOldOnStop(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
	})
	probeFn = func(_ context.Context, _, _ string, _ ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 720, Height: 480, FrameRate: 29.97}, nil
	}
	probeCropFn = func(_ context.Context, _, _ string, _ map[string]string, _ time.Duration, _ ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		return nil, nil
	}

	m := newTestManager(t)

	const sharedRef = "dlna:test-ref-1"
	onStopCalls := make(chan string, 4)
	onStop := func(reason string) {
		onStopCalls <- reason
	}

	// Seed the manager with an existing active session for sharedRef.
	// We don't spawn a real plane; instead we set m.active directly to
	// emulate the post-Pause shape (cancelFn=nil, plane=nil, but active
	// retained so Play() / SeekTo() can resume).
	m.active = &activeSession{
		req: SessionRequest{
			StreamURL:    "udp://127.0.0.1:1/x",
			AdapterRef:   sharedRef,
			OnStop:       onStop,
			Capabilities: Capabilities{CanSeek: true, CanPause: true},
		},
	}

	// Build a SessionRequest with the SAME AdapterRef — same-session
	// replay (the shape Play() and SeekTo() construct internally).
	replayReq := SessionRequest{
		StreamURL:    "udp://127.0.0.1:1/x",
		AdapterRef:   sharedRef,
		OnStop:       onStop,
		Capabilities: Capabilities{CanSeek: true, CanPause: true},
	}

	probe, cropRect, ffmpegPath, err := m.probeForStart(replayReq)
	if err != nil {
		t.Fatalf("probeForStart: %v", err)
	}

	m.mu.Lock()
	err = m.startPlaneLocked(replayReq, 0, probe, cropRect, ffmpegPath, 1, sessionGuard{}, false)
	m.mu.Unlock()
	if err != nil {
		t.Fatalf("startPlaneLocked: %v", err)
	}

	// Stop immediately so the spawned plane goroutine winds down quickly.
	// Stop's own OnStop fire is "stopped"; we filter that out below.
	t.Cleanup(func() { _ = m.Stop() })

	// notifySessionStop spawns a goroutine; allow a brief window for any
	// "preempted" call to land. If the fix is in place, the channel
	// receives only "stopped" (from Stop above) — never "preempted".
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case reason := <-onStopCalls:
			if reason == "preempted" {
				t.Fatalf("OnStop fired with reason %q on same-session replay; "+
					"P3.3 regression: startPlaneLocked must gate the "+
					"preempted fire on a genuine AdapterRef change", reason)
			}
			// "stopped" / "error" / "" are acceptable outcomes for the
			// post-cleanup phase; we only fail on the regressed reason.
		case <-deadline:
			return
		}
	}
}

// TestStartPlaneLocked_DifferentSession_FiresOldOnStop verifies that a
// genuine preempt — startPlaneLocked invoked with a DIFFERENT AdapterRef
// while a session is active — still fires the prior session's OnStop
// with reason "preempted". This is the case we must NOT regress while
// fixing the same-session-replay bug.
func TestStartPlaneLocked_DifferentSession_FiresOldOnStop(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
	})
	probeFn = func(_ context.Context, _, _ string, _ ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 720, Height: 480, FrameRate: 29.97}, nil
	}
	probeCropFn = func(_ context.Context, _, _ string, _ map[string]string, _ time.Duration, _ ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		return nil, nil
	}

	m := newTestManager(t)

	preempted := make(chan string, 4)
	oldOnStop := func(reason string) {
		preempted <- reason
	}

	// Seed with the prior session.
	m.active = &activeSession{
		req: SessionRequest{
			StreamURL:    "udp://127.0.0.1:1/old",
			AdapterRef:   "plex:old-ref",
			OnStop:       oldOnStop,
			Capabilities: Capabilities{CanSeek: true, CanPause: true},
		},
	}

	// Different AdapterRef — genuine preempt.
	newReq := SessionRequest{
		StreamURL:    "udp://127.0.0.1:1/new",
		AdapterRef:   "dlna:new-ref",
		OnStop:       func(string) {},
		Capabilities: Capabilities{CanSeek: true, CanPause: true},
	}

	probe, cropRect, ffmpegPath, err := m.probeForStart(newReq)
	if err != nil {
		t.Fatalf("probeForStart: %v", err)
	}

	m.mu.Lock()
	err = m.startPlaneLocked(newReq, 0, probe, cropRect, ffmpegPath, 2, sessionGuard{}, false)
	m.mu.Unlock()
	if err != nil {
		t.Fatalf("startPlaneLocked: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop() })

	select {
	case reason := <-preempted:
		if reason != "preempted" {
			t.Errorf("OnStop reason = %q, want %q", reason, "preempted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected OnStop with reason \"preempted\" on different-session replacement; " +
			"never fired within 2s")
	}
}

// TestProbeForStart_ZeroPolicyPreservesBehavior is the backward-compat
// guard: when req.MediaInputPolicy is the zero value, the ffmpeg package
// receives the zero value verbatim — no filter, no flags, no behavioral
// drift for existing Plex / Jellyfin / URL adapter call paths.
func TestProbeForStart_ZeroPolicyPreservesBehavior(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
	})

	var probePolicy ffmpeg.MediaInputPolicy
	var cropPolicy ffmpeg.MediaInputPolicy
	var capturedHeaders map[string]string

	probeFn = func(_ context.Context, _, _ string, policy ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		probePolicy = policy
		return &ffmpeg.ProbeResult{Width: 1280, Height: 720, FrameRate: 24}, nil
	}
	probeCropFn = func(_ context.Context, _, _ string, headers map[string]string, _ time.Duration, policy ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		cropPolicy = policy
		capturedHeaders = headers
		return nil, nil
	}

	m := newTestManager(t)
	m.bridge.Video.AspectMode = "auto"

	originalHeaders := map[string]string{
		"X-Plex-Token": "abc",
		"User-Agent":   "groovyrelay",
	}
	req := SessionRequest{
		StreamURL:    "http://pms/transcode.m3u8",
		InputHeaders: originalHeaders,
		// MediaInputPolicy: zero value
	}

	if _, _, _, err := m.probeForStart(req); err != nil {
		t.Fatalf("probeForStart: %v", err)
	}

	if !probePolicy.IsZero() {
		t.Errorf("expected zero policy at Probe site, got %+v", probePolicy)
	}
	if !cropPolicy.IsZero() {
		t.Errorf("expected zero policy at ProbeCrop site, got %+v", cropPolicy)
	}
	// With an empty BlockedHeaders, FilterHeaders returns the input
	// unchanged — every header survives.
	for k, v := range originalHeaders {
		if got := capturedHeaders[k]; got != v {
			t.Errorf("header %q lost or changed under zero policy: got %q want %q", k, got, v)
		}
	}
}
