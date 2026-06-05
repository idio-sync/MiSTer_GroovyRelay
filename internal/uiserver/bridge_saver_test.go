package uiserver

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

// TestDiffBridgeConfig_SSHFields confirms ssh_user and ssh_password
// edits surface as changed keys so scopeForBridgeField gets a chance
// to dispatch them.
func TestDiffBridgeConfig_SSHFields(t *testing.T) {
	old := config.BridgeConfig{
		MiSTer: config.MisterConfig{
			Host: "192.168.1.42", Port: 32100, SourcePort: 32101,
			SSHUser: "root", SSHPassword: "",
		},
	}
	newCfg := old
	newCfg.MiSTer.SSHUser = "alice"
	newCfg.MiSTer.SSHPassword = "hunter2"

	keys := diffBridgeConfig(old, newCfg)
	if !containsStr(keys, "mister.ssh_user") {
		t.Errorf("expected mister.ssh_user in diff keys, got %v", keys)
	}
	if !containsStr(keys, "mister.ssh_password") {
		t.Errorf("expected mister.ssh_password in diff keys, got %v", keys)
	}
}

// TestScopeForBridgeField_SSHFieldsHotSwap confirms the new SSH keys
// are explicitly hot-swap, not the default ScopeRestartBridge.
func TestScopeForBridgeField_SSHFieldsHotSwap(t *testing.T) {
	for _, k := range []string{"mister.ssh_user", "mister.ssh_password"} {
		t.Run(k, func(t *testing.T) {
			got := scopeForBridgeField(k)
			if got != adapters.ScopeHotSwap {
				t.Errorf("scopeForBridgeField(%q) = %v, want ScopeHotSwap", k, got)
			}
		})
	}
}

// TestDiffBridgeConfig_LoggingDebug pins the diff for the new logging
// toggle so a flip surfaces as a changed key and reaches the apply
// switch.
func TestDiffBridgeConfig_LoggingDebug(t *testing.T) {
	old := config.BridgeConfig{
		Logging: config.LoggingConfig{Debug: false},
	}
	newCfg := old
	newCfg.Logging.Debug = true

	keys := diffBridgeConfig(old, newCfg)
	if !containsStr(keys, "logging.debug") {
		t.Errorf("expected logging.debug in diff keys, got %v", keys)
	}
}

func TestDiffBridgeConfig_OutputVolume(t *testing.T) {
	old := config.BridgeConfig{
		Audio: config.AudioConfig{OutputVolume: 100},
	}
	newCfg := old
	newCfg.Audio.OutputVolume = 25

	keys := diffBridgeConfig(old, newCfg)
	if !containsStr(keys, "audio.output_volume") {
		t.Errorf("expected audio.output_volume in diff keys, got %v", keys)
	}
	if got := scopeForBridgeField("audio.output_volume"); got != adapters.ScopeHotSwap {
		t.Errorf("scopeForBridgeField(audio.output_volume) = %v, want ScopeHotSwap", got)
	}
}

func TestDiffBridgeConfig_DeltaLZ4(t *testing.T) {
	old := config.BridgeConfig{
		Video: config.VideoConfig{DeltaLZ4Enabled: false},
	}
	newCfg := old
	newCfg.Video.DeltaLZ4Enabled = true

	keys := diffBridgeConfig(old, newCfg)
	if !containsStr(keys, "video.delta_lz4_enabled") {
		t.Errorf("expected video.delta_lz4_enabled in diff keys, got %v", keys)
	}
}

func TestDiffBridgeConfig_VisualizerMode(t *testing.T) {
	old := config.BridgeConfig{
		Visualizer: config.VisualizerConfig{Mode: config.VisualizerModeRetroAnalyzer},
	}
	newCfg := old
	newCfg.Visualizer.Mode = config.VisualizerModeStereoScope

	keys := diffBridgeConfig(old, newCfg)
	if !containsStr(keys, "visualizer.mode") {
		t.Errorf("expected visualizer.mode in diff keys, got %v", keys)
	}
	if got := scopeForBridgeField("visualizer.mode"); got != adapters.ScopeNextCast {
		t.Errorf("scopeForBridgeField(visualizer.mode) = %v, want ScopeNextCast", got)
	}
}

func TestScopeForBridgeField_DeltaLZ4RestartCast(t *testing.T) {
	got := scopeForBridgeField("video.delta_lz4_enabled")
	if got != adapters.ScopeRestartCast {
		t.Errorf("scopeForBridgeField(video.delta_lz4_enabled) = %v, want ScopeRestartCast", got)
	}
}

func TestDiffBridgeConfig_HLSBufferFieldsRestartCast(t *testing.T) {
	old := config.BridgeConfig{HLSBuffer: config.HLSBufferConfig{
		Enabled:                true,
		LiveEdgeSegments:       3,
		StartSegments:          2,
		MaxCachedSegments:      6,
		MaxCacheBytes:          268435456,
		MaxPlaylistBytes:       1048576,
		MaxSegmentBytes:        52428800,
		SegmentTimeoutSeconds:  10,
		PlaylistTimeoutSeconds: 10,
		MaxVariantHeight:       720,
		StaleCacheReapHours:    24,
	}}
	mutations := map[string]func(*config.BridgeConfig){
		"hls_buffer.enabled":                  func(c *config.BridgeConfig) { c.HLSBuffer.Enabled = false },
		"hls_buffer.live_edge_segments":       func(c *config.BridgeConfig) { c.HLSBuffer.LiveEdgeSegments = 4 },
		"hls_buffer.start_segments":           func(c *config.BridgeConfig) { c.HLSBuffer.StartSegments = 3 },
		"hls_buffer.max_cached_segments":      func(c *config.BridgeConfig) { c.HLSBuffer.MaxCachedSegments = 7 },
		"hls_buffer.max_cache_bytes":          func(c *config.BridgeConfig) { c.HLSBuffer.MaxCacheBytes++ },
		"hls_buffer.max_playlist_bytes":       func(c *config.BridgeConfig) { c.HLSBuffer.MaxPlaylistBytes++ },
		"hls_buffer.max_segment_bytes":        func(c *config.BridgeConfig) { c.HLSBuffer.MaxSegmentBytes++ },
		"hls_buffer.segment_timeout_seconds":  func(c *config.BridgeConfig) { c.HLSBuffer.SegmentTimeoutSeconds++ },
		"hls_buffer.playlist_timeout_seconds": func(c *config.BridgeConfig) { c.HLSBuffer.PlaylistTimeoutSeconds++ },
		"hls_buffer.max_variant_height":       func(c *config.BridgeConfig) { c.HLSBuffer.MaxVariantHeight++ },
		"hls_buffer.stale_cache_reap_hours":   func(c *config.BridgeConfig) { c.HLSBuffer.StaleCacheReapHours++ },
	}
	for key, mutate := range mutations {
		t.Run(key, func(t *testing.T) {
			next := old
			mutate(&next)
			if !containsStr(diffBridgeConfig(old, next), key) {
				t.Fatalf("diffBridgeConfig missing %s", key)
			}
			if got := scopeForBridgeField(key); got != adapters.ScopeRestartCast {
				t.Fatalf("scopeForBridgeField(%q) = %v, want ScopeRestartCast", key, got)
			}
		})
	}
}

// TestScopeForBridgeField_LoggingDebugHotSwap pins the scope for the
// logging toggle: flipping it must NOT trigger a cast restart or
// container restart — the operator wants to enable diagnostic logs
// against an in-flight session, that's the entire point.
func TestScopeForBridgeField_LoggingDebugHotSwap(t *testing.T) {
	got := scopeForBridgeField("logging.debug")
	if got != adapters.ScopeHotSwap {
		t.Errorf("scopeForBridgeField(logging.debug) = %v, want ScopeHotSwap", got)
	}
}

func TestDiffBridgeConfig_ToolPathsHotSwap(t *testing.T) {
	old := config.BridgeConfig{}
	newCfg := old
	newCfg.FFmpegPath = "/bin/ffmpeg"
	newCfg.FFprobePath = "/bin/ffprobe"
	newCfg.YTDLPPath = "/bin/yt-dlp"

	keys := diffBridgeConfig(old, newCfg)
	for _, k := range []string{"ffmpeg_path", "ffprobe_path", "ytdlp_path"} {
		if !containsStr(keys, k) {
			t.Errorf("expected %s in diff keys, got %v", k, keys)
		}
		if got := scopeForBridgeField(k); got != adapters.ScopeHotSwap {
			t.Errorf("scopeForBridgeField(%q) = %v, want ScopeHotSwap", k, got)
		}
	}
}

func TestBridgeSaver_UpdateToolResolvers(t *testing.T) {
	ffmpeg := &fakeOverrideUpdater{}
	ffprobe := &fakeOverrideUpdater{}
	ytdlp := &fakeOverrideUpdater{}
	s := &BridgeSaver{tools: ToolResolvers{FFmpeg: ffmpeg, FFprobe: ffprobe, YTDLP: ytdlp}}
	cfg := config.BridgeConfig{
		FFmpegPath:  "/tools/ffmpeg",
		FFprobePath: "/tools/ffprobe",
		YTDLPPath:   "/tools/yt-dlp",
	}
	s.updateToolResolvers([]string{"ffmpeg_path", "ffprobe_path", "ytdlp_path"}, cfg)

	if ffmpeg.got != cfg.FFmpegPath {
		t.Errorf("ffmpeg override = %q", ffmpeg.got)
	}
	if ffprobe.got != cfg.FFprobePath {
		t.Errorf("ffprobe override = %q", ffprobe.got)
	}
	if ytdlp.got != cfg.YTDLPPath {
		t.Errorf("ytdlp override = %q", ytdlp.got)
	}
}

func TestBridgeSaver_ModelineSaveDropsAndNotifiesSubscribers(t *testing.T) {
	core := &fakeBridgeCore{}
	sub := &fakeVideoConfigSubscriber{name: "fake"}
	reg := adapters.NewRegistry()
	if err := reg.Register(sub); err != nil {
		t.Fatal(err)
	}
	old := testBridgeConfig(t, "NTSC_480i")
	s := NewBridgeSaver(testConfigPath(t), &config.Sectioned{Bridge: old}, core, reg)

	next := old
	next.Video.Modeline = "PAL_576i"
	scope, err := s.Save(next)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Fatalf("scope = %v, want restart-cast", scope)
	}
	if core.drops != 1 {
		t.Fatalf("DropActiveCast calls = %d, want 1", core.drops)
	}
	if core.updated.Video.Modeline != "PAL_576i" {
		t.Errorf("core updated modeline = %q", core.updated.Video.Modeline)
	}
	if sub.got != "PAL_576i" {
		t.Errorf("subscriber modeline = %q, want PAL_576i", sub.got)
	}
}

func TestBridgeSaver_ModelineMixedRestartBridgeStillDropsAndNotifies(t *testing.T) {
	core := &fakeBridgeCore{}
	sub := &fakeVideoConfigSubscriber{name: "fake"}
	reg := adapters.NewRegistry()
	if err := reg.Register(sub); err != nil {
		t.Fatal(err)
	}
	old := testBridgeConfig(t, "NTSC_480i")
	s := NewBridgeSaver(testConfigPath(t), &config.Sectioned{Bridge: old}, core, reg)

	next := old
	next.Video.Modeline = "PAL_288p"
	next.HostIP = "192.0.2.55"
	scope, err := s.Save(next)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if scope != adapters.ScopeRestartBridge {
		t.Fatalf("scope = %v, want restart-bridge", scope)
	}
	if core.drops != 1 {
		t.Fatalf("DropActiveCast calls = %d, want 1", core.drops)
	}
	if sub.got != "PAL_288p" {
		t.Errorf("subscriber modeline = %q, want PAL_288p", sub.got)
	}
}

func TestBridgeSaver_ModelineDropErrorStillNotifiesSubscribers(t *testing.T) {
	dropErr := errors.New("drop failed")
	core := &fakeBridgeCore{dropErr: dropErr}
	sub := &fakeVideoConfigSubscriber{name: "fake"}
	reg := adapters.NewRegistry()
	if err := reg.Register(sub); err != nil {
		t.Fatal(err)
	}
	old := testBridgeConfig(t, "NTSC_480i")
	s := NewBridgeSaver(testConfigPath(t), &config.Sectioned{Bridge: old}, core, reg)

	next := old
	next.Video.Modeline = "PAL_576i"
	scope, err := s.Save(next)
	if !errors.Is(err, dropErr) {
		t.Fatalf("Save error = %v, want wrapping %v", err, dropErr)
	}
	if scope != adapters.ScopeRestartCast {
		t.Fatalf("scope = %v, want restart-cast", scope)
	}
	if sub.got != "PAL_576i" {
		t.Errorf("subscriber modeline = %q, want PAL_576i", sub.got)
	}
}

func TestBridgeSaver_VisualizerModeNextCastDoesNotDrop(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	s := NewBridgeSaver(testConfigPath(t), &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	next := old
	next.Visualizer.Mode = config.VisualizerModeOscilloscopeWave
	scope, err := s.Save(next)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if scope != adapters.ScopeNextCast {
		t.Fatalf("scope = %v, want next-cast", scope)
	}
	if core.drops != 0 {
		t.Fatalf("DropActiveCast calls = %d, want 0", core.drops)
	}
	if core.updated.Visualizer.Mode != config.VisualizerModeOscilloscopeWave {
		t.Errorf("core updated visualizer mode = %q, want %q", core.updated.Visualizer.Mode, config.VisualizerModeOscilloscopeWave)
	}
	if s.Current().Visualizer.Mode != config.VisualizerModeOscilloscopeWave {
		t.Errorf("current visualizer mode = %q, want %q", s.Current().Visualizer.Mode, config.VisualizerModeOscilloscopeWave)
	}
}

func TestBridgeSaver_OutputVolumeHotSwapsNoDrop(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	old.Audio.OutputVolume = 100
	s := NewBridgeSaver(testConfigPath(t), &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	next := old
	next.Audio.OutputVolume = 33
	scope, err := s.Save(next)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Fatalf("scope = %v, want hot-swap", scope)
	}
	if core.drops != 0 {
		t.Fatalf("DropActiveCast calls = %d, want 0", core.drops)
	}
	if len(core.outputVolumes) != 1 || core.outputVolumes[0] != 33 {
		t.Fatalf("SetOutputVolume calls = %v, want [33]", core.outputVolumes)
	}
	if got := s.Current().Audio.OutputVolume; got != 33 {
		t.Fatalf("current output volume = %d, want 33", got)
	}
}

func TestBridgeSaver_SaveOutputVolumePreservesLatestBridgeFields(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	path := testConfigPath(t)
	s := NewBridgeSaver(path, &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	s.sec.Bridge.MiSTer.Host = "198.51.100.9"
	scope, err := s.SaveOutputVolume(44)
	if err != nil {
		t.Fatalf("SaveOutputVolume: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Fatalf("scope = %v, want hot-swap", scope)
	}
	if got := s.Current().MiSTer.Host; got != "198.51.100.9" {
		t.Fatalf("host = %q, want latest in-memory host", got)
	}
	if got := s.Current().Audio.OutputVolume; got != 44 {
		t.Fatalf("output volume = %d, want 44", got)
	}
	if core.updated.MiSTer.Host != "198.51.100.9" {
		t.Fatalf("core updated host = %q, want latest host", core.updated.MiSTer.Host)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `host = "198.51.100.9"`) {
		t.Fatalf("persisted config did not preserve latest host:\n%s", string(data))
	}
}

func TestBridgeSaver_SaveVisualizerMode_PersistsAndReturnsScope(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	path := testConfigPath(t)
	s := NewBridgeSaver(path, &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	scope, err := s.SaveVisualizerMode(config.VisualizerModeStereoScope)
	if err != nil {
		t.Fatalf("SaveVisualizerMode: %v", err)
	}
	if scope != adapters.ScopeNextCast {
		t.Errorf("scope = %v, want ScopeNextCast", scope)
	}

	if got := s.Current().Visualizer.Mode; got != config.VisualizerModeStereoScope {
		t.Errorf("Current().Visualizer.Mode = %q, want %q", got, config.VisualizerModeStereoScope)
	}
	if got := core.updated.Visualizer.Mode; got != config.VisualizerModeStereoScope {
		t.Errorf("core.updated.Visualizer.Mode = %q, want %q", got, config.VisualizerModeStereoScope)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(raw), `mode = "stereo_scope"`) {
		t.Errorf("config.toml does not contain new mode; content:\n%s", string(raw))
	}
}

func TestBridgeSaver_SaveVisualizerMode_PreservesLatestBridgeFields(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	path := testConfigPath(t)
	s := NewBridgeSaver(path, &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	s.sec.Bridge.MiSTer.Host = "198.51.100.9"

	if _, err := s.SaveVisualizerMode(config.VisualizerModeOscilloscopeWave); err != nil {
		t.Fatalf("SaveVisualizerMode: %v", err)
	}

	if got := s.Current().MiSTer.Host; got != "198.51.100.9" {
		t.Errorf("MiSTer.Host = %q, want preserved 198.51.100.9", got)
	}
	if got := s.Current().Visualizer.Mode; got != config.VisualizerModeOscilloscopeWave {
		t.Errorf("Visualizer.Mode = %q, want oscilloscope_wave", got)
	}
}

func TestBridgeSaver_SaveVisualizerMode_RejectsUnsupportedMode(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	path := testConfigPath(t)
	s := NewBridgeSaver(path, &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	_, err := s.SaveVisualizerMode("radial_spectrum")
	if err == nil {
		t.Fatal("expected error for unsupported mode, got nil")
	}
	if !strings.Contains(err.Error(), "bridge.visualizer.mode must be one of") {
		t.Errorf("error = %q, want it to contain validate-prose substring", err.Error())
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "radial_spectrum") {
		t.Errorf("config.toml unexpectedly contains rejected mode:\n%s", string(raw))
	}
}

func TestBridgeSaver_VisualizerModePersistsVisualizerTable(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	path := testConfigPath(t)
	s := NewBridgeSaver(path, &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	next := old
	next.Visualizer.Mode = config.VisualizerModeStereoScope
	if _, err := s.Save(next); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "[bridge.visualizer]") {
		t.Fatalf("rewritten TOML missing [bridge.visualizer]:\n%s", text)
	}
	if !strings.Contains(text, `mode = "stereo_scope"`) {
		t.Fatalf("rewritten TOML missing stereo visualizer mode:\n%s", text)
	}
}

func TestBridgeSaver_InvalidVisualizerModeRejectsBeforePersisting(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	path := testConfigPath(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read before config: %v", err)
	}
	s := NewBridgeSaver(path, &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	next := old
	next.Visualizer.Mode = "sparkle"
	_, err = s.Save(next)
	if err == nil {
		t.Fatal("Save error = nil, want invalid visualizer mode error")
	}
	if !strings.Contains(err.Error(), "bridge.visualizer.mode") {
		t.Fatalf("Save error = %v, want bridge.visualizer.mode", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after config: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("config file changed after rejected save:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if got := s.Current().Visualizer.Mode; got != old.Visualizer.Mode {
		t.Fatalf("current visualizer mode = %q, want unchanged %q", got, old.Visualizer.Mode)
	}
	if core.updates != 0 {
		t.Fatalf("UpdateBridge calls = %d, want 0", core.updates)
	}
}

func TestBridgeSaver_VisualizerModeMixedRestartCastDropsAndUpdates(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	s := NewBridgeSaver(testConfigPath(t), &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	next := old
	next.Visualizer.Mode = config.VisualizerModeStereoScope
	next.Video.AspectMode = "zoom"
	scope, err := s.Save(next)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Fatalf("scope = %v, want restart-cast", scope)
	}
	if core.drops != 1 {
		t.Fatalf("DropActiveCast calls = %d, want 1", core.drops)
	}
	if core.updated.Visualizer.Mode != config.VisualizerModeStereoScope {
		t.Errorf("core updated visualizer mode = %q, want %q", core.updated.Visualizer.Mode, config.VisualizerModeStereoScope)
	}
}

func TestBridgeSaver_VisualizerModeMixedHotSwapAppliesHotSwapNoDrop(t *testing.T) {
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	s := NewBridgeSaver(testConfigPath(t), &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())

	next := old
	next.Visualizer.Mode = config.VisualizerModeOscilloscopeWave
	next.Video.InterlaceFieldOrder = "tff"
	scope, err := s.Save(next)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if scope != adapters.ScopeNextCast {
		t.Fatalf("scope = %v, want next-cast", scope)
	}
	if core.drops != 0 {
		t.Fatalf("DropActiveCast calls = %d, want 0", core.drops)
	}
	if len(core.interlaceOrders) != 1 || core.interlaceOrders[0] != "tff" {
		t.Fatalf("SetInterlaceFieldOrder calls = %v, want [tff]", core.interlaceOrders)
	}
	if core.updated.Visualizer.Mode != config.VisualizerModeOscilloscopeWave {
		t.Errorf("core updated visualizer mode = %q, want %q", core.updated.Visualizer.Mode, config.VisualizerModeOscilloscopeWave)
	}
}

func testBridgeConfig(t *testing.T, modeline string) config.BridgeConfig {
	t.Helper()
	return config.BridgeConfig{
		DataDir: t.TempDir(),
		Video: config.VideoConfig{
			Modeline:            modeline,
			InterlaceFieldOrder: "bff",
			AspectMode:          "auto",
			RGBMode:             "rgb888",
			LZ4Enabled:          true,
		},
		Audio:      config.AudioConfig{SampleRate: 48000, Channels: 2, OutputVolume: 100, DSP: config.DefaultAudioDSP()},
		Visualizer: config.VisualizerConfig{Mode: config.VisualizerModeRetroAnalyzer},
		MiSTer:     config.MisterConfig{Host: "192.0.2.10", Port: 32100, SourcePort: 32101},
		UI:         config.UIConfig{HTTPPort: 32500},
		HLSBuffer: config.HLSBufferConfig{
			Enabled:                true,
			LiveEdgeSegments:       3,
			StartSegments:          2,
			MaxCachedSegments:      6,
			MaxCacheBytes:          268435456,
			MaxPlaylistBytes:       1048576,
			MaxSegmentBytes:        52428800,
			SegmentTimeoutSeconds:  10,
			PlaylistTimeoutSeconds: 10,
			MaxVariantHeight:       720,
			StaleCacheReapHours:    24,
		},
	}
}

func testConfigPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[adapters.fake]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type fakeBridgeCore struct {
	updated         config.BridgeConfig
	updates         int
	interlaceOrders []string
	outputVolumes   []int
	drops           int
	dropErr         error
	lastDSP         config.AudioDSP
	dspCalls        int
	dspErr          error
}

func (f *fakeBridgeCore) UpdateBridge(b config.BridgeConfig) {
	f.updates++
	f.updated = b
}

func (f *fakeBridgeCore) SetInterlaceFieldOrder(order string) error {
	f.interlaceOrders = append(f.interlaceOrders, order)
	return nil
}

func (f *fakeBridgeCore) SetOutputVolume(volume int) error {
	f.outputVolumes = append(f.outputVolumes, volume)
	return nil
}

func (f *fakeBridgeCore) SetAudioDSP(dsp config.AudioDSP) error {
	f.lastDSP = dsp
	f.dspCalls++
	return f.dspErr
}

func (f *fakeBridgeCore) DropActiveCast(string) error {
	f.drops++
	return f.dropErr
}

type fakeVideoConfigSubscriber struct {
	name string
	got  string
}

func (f *fakeVideoConfigSubscriber) Name() string                { return f.name }
func (f *fakeVideoConfigSubscriber) DisplayName() string         { return f.name }
func (f *fakeVideoConfigSubscriber) Fields() []adapters.FieldDef { return nil }
func (f *fakeVideoConfigSubscriber) DecodeConfig(toml.Primitive, toml.MetaData) error {
	return nil
}
func (f *fakeVideoConfigSubscriber) IsEnabled() bool { return true }
func (f *fakeVideoConfigSubscriber) Start(context.Context) error {
	return nil
}
func (f *fakeVideoConfigSubscriber) Stop() error { return nil }
func (f *fakeVideoConfigSubscriber) Status() adapters.Status {
	return adapters.Status{}
}
func (f *fakeVideoConfigSubscriber) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}
func (f *fakeVideoConfigSubscriber) OnVideoConfigChanged(modelineName string) {
	f.got = modelineName
}

type fakeOverrideUpdater struct {
	got string
}

func (f *fakeOverrideUpdater) UpdateOverride(v string) {
	f.got = v
}

func TestBridgeSaver_PortInUseReturnsTypedError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[bridge]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bind a UDP socket on a real ephemeral port so the preflight fails.
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	busyPort := conn.LocalAddr().(*net.UDPAddr).Port

	sec := &config.Sectioned{Bridge: testBridgeConfig(t, "NTSC_480i")}
	core := &fakeBridgeCore{}
	saver := NewBridgeSaver(cfgPath, sec, core, adapters.NewRegistry())
	newCfg := sec.Bridge
	newCfg.MiSTer.SourcePort = busyPort

	_, err = saver.Save(newCfg)
	if err == nil {
		t.Fatal("Save = nil, want PORT IN USE error")
	}
	var se *settingsError
	if !errors.As(err, &se) {
		t.Fatalf("err is not *settingsError: %v", err)
	}
	if se.StatusCode() != 409 || se.Chip() != "PORT IN USE" {
		t.Errorf("got (%d, %q), want (409, \"PORT IN USE\")", se.StatusCode(), se.Chip())
	}
}

func TestBridgeSaver_DataDirNotWritableReturnsTypedError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[bridge]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sec := &config.Sectioned{Bridge: testBridgeConfig(t, "NTSC_480i")}
	core := &fakeBridgeCore{}
	saver := NewBridgeSaver(cfgPath, sec, core, adapters.NewRegistry())

	// A relative path is rejected by the preflight (must be absolute or empty).
	// If the existing preflight uses ProbeDirWritable on an absolute non-existent
	// path that cannot be created, that's also a fail path — pick whichever
	// matches the saver's existing behavior. The assertion below holds either way.
	newCfg := sec.Bridge
	newCfg.DataDir = "relative/path/not/allowed"
	_, err := saver.Save(newCfg)
	if err == nil {
		t.Fatal("Save = nil, want PATH NOT WRITABLE error")
	}
	var se *settingsError
	if !errors.As(err, &se) {
		t.Fatalf("err is not *settingsError: %v", err)
	}
	if se.StatusCode() != 409 || se.Chip() != "PATH NOT WRITABLE" {
		t.Errorf("got (%d, %q), want (409, \"PATH NOT WRITABLE\")", se.StatusCode(), se.Chip())
	}
}

// minimalSectionedConfigTOML returns a TOML string in sectioned format
// (has a [bridge] section) with values that pass config validation.
func minimalSectionedConfigTOML(t *testing.T) string {
	t.Helper()
	return `[bridge]
data_dir = ""

[bridge.video]
modeline = "NTSC_480i"
interlace_field_order = "bff"
aspect_mode = "auto"
rgb_mode = "rgb888"
lz4_enabled = true

[bridge.audio]
sample_rate = 48000
channels = 2

[bridge.mister]
host = "192.168.1.50"
port = 32100
source_port = 32101

[bridge.ui]
http_port = 32500
`
}

// loadSectionedForTest loads a sectioned config from disk, failing the
// test immediately on any error.
func loadSectionedForTest(t *testing.T, path string) *config.Sectioned {
	t.Helper()
	sec, err := config.LoadSectioned(path)
	if err != nil {
		t.Fatalf("loadSectionedForTest: %v", err)
	}
	return sec
}

func TestBridgeSaver_EmitsConfigSaved(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(minimalSectionedConfigTOML(t)), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	sec := loadSectionedForTest(t, cfgPath)
	log := eventlog.New(16)
	saver := NewBridgeSaver(cfgPath, sec, &fakeBridgeCore{}, adapters.NewRegistry())
	saver.WithEventLog(log)

	current := saver.Current()
	current.UI.HTTPPort = 32600 // any change that passes validation
	if _, err := saver.Save(current); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries := log.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Source != "bridge" || e.Severity != eventlog.SeverityInfo {
		t.Errorf("Source/Severity: got %q/%v, want bridge/Info", e.Source, e.Severity)
	}
	if !strings.Contains(e.Message, "bridge-config-saved") {
		t.Errorf("Message: got %q", e.Message)
	}
}

func TestBridgeSaver_EmitsConfigSavedWhenPersistedEvenIfDropFails(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(minimalSectionedConfigTOML(t)), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	sec := loadSectionedForTest(t, cfgPath)
	log := eventlog.New(16)
	core := &fakeBridgeCore{dropErr: errors.New("drop failed after persist")}
	saver := NewBridgeSaver(cfgPath, sec, core, adapters.NewRegistry())
	saver.WithEventLog(log)

	current := saver.Current()
	current.Video.Modeline = "PAL_576i" // triggers notify + DropActiveCast after persist
	if _, err := saver.Save(current); err == nil {
		t.Fatal("Save returned nil; want drop-cast error after persisted config")
	}
	entries := log.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected persisted event despite drop error, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Message, "bridge-config-saved") {
		t.Errorf("Message: got %q", entries[0].Message)
	}
}

func TestSettingsError_StatusCodeAndChip(t *testing.T) {
	t.Parallel()
	cause := errors.New("bind: address in use")
	se := &settingsError{status: 409, chip: "PORT IN USE", cause: cause}
	if got := se.StatusCode(); got != 409 {
		t.Errorf("StatusCode = %d, want 409", got)
	}
	if got := se.Chip(); got != "PORT IN USE" {
		t.Errorf("Chip = %q, want PORT IN USE", got)
	}
	if got := se.Error(); got == "" {
		t.Errorf("Error() is empty, want non-empty")
	}
	if got := se.Unwrap(); got != cause {
		t.Errorf("Unwrap = %v, want %v", got, cause)
	}
	if !errors.Is(se, cause) {
		t.Fatalf("errors.Is(se, cause) = false, want true")
	}
	var shaped interface {
		error
		StatusCode() int
		Chip() string
	}
	if !errors.As(se, &shaped) {
		t.Fatalf("errors.As(se, &status/chip interface) = false, want true")
	}
}

// TestBridgeSaver_SaveTouchedSnapshotsUnderLock proves SaveTouched's apply
// closure observes the latest persisted state, not a stale snapshot. This
// is the regression guard for the TOCTOU window the chassis settings
// drawer would otherwise have between Current() and Save(): two parallel
// auto-saves on different fields would each see the same pre-mutation
// Bridge and the second save would clobber the first writer's field.
func TestBridgeSaver_SaveTouchedSnapshotsUnderLock(t *testing.T) {
	core := &fakeBridgeCore{}
	reg := adapters.NewRegistry()
	old := testBridgeConfig(t, "NTSC_480i")
	s := NewBridgeSaver(testConfigPath(t), &config.Sectioned{Bridge: old}, core, reg)

	// First save: set HostIP. (data_dir would need a writable preflight;
	// HostIP only needs Validate, which the testBridgeConfig fixture passes.)
	if _, err := s.SaveTouched(func(c *config.BridgeConfig) {
		c.HostIP = "10.0.0.1"
	}); err != nil {
		t.Fatalf("SaveTouched #1: %v", err)
	}

	// Second save: the apply closure should see HostIP=10.0.0.1 — the most
	// recent persisted value, not the pre-first-save zero value. If
	// SaveTouched ever drops the snapshot-under-lock invariant, this test
	// catches it.
	var observed string
	if _, err := s.SaveTouched(func(c *config.BridgeConfig) {
		observed = c.HostIP
		c.UI.HTTPPort = old.UI.HTTPPort // no-op to satisfy Validate
	}); err != nil {
		t.Fatalf("SaveTouched #2: %v", err)
	}
	if observed != "10.0.0.1" {
		t.Errorf("SaveTouched #2 saw HostIP = %q, want 10.0.0.1 (snapshot must include #1's write)", observed)
	}

	// Final state on disk: both writers' fields preserved.
	cur := s.Current()
	if cur.HostIP != "10.0.0.1" {
		t.Errorf("final HostIP = %q, want 10.0.0.1", cur.HostIP)
	}
}

func TestDiffBridgeConfig_DetectsDSP(t *testing.T) {
	t.Parallel()
	old := testBridgeConfig(t, "NTSC_480i")
	old.Audio.DSP = config.DefaultAudioDSP()

	next := old
	next.Audio.DSP.Bass = 3
	if !containsStr(diffBridgeConfig(old, next), "audio.dsp") {
		t.Error("bass change should surface audio.dsp")
	}
	next2 := old
	next2.Audio.DSP.EQ = append([]float64(nil), old.Audio.DSP.EQ...)
	next2.Audio.DSP.EQ[4] = -2
	if !containsStr(diffBridgeConfig(old, next2), "audio.dsp") {
		t.Error("EQ band change should surface audio.dsp")
	}
}

func TestScopeForBridgeField_DSPIsHotSwap(t *testing.T) {
	t.Parallel()
	if got := scopeForBridgeField("audio.dsp"); got != adapters.ScopeHotSwap {
		t.Errorf("scopeForBridgeField(audio.dsp) = %v, want ScopeHotSwap", got)
	}
}

// newBridgeSaverForTest constructs a BridgeSaver backed by a temp config file
// and a fakeBridgeCore so tests can inspect core.lastDSP / core.dspCalls.
func newBridgeSaverForTest(t *testing.T) (*BridgeSaver, *fakeBridgeCore) {
	t.Helper()
	core := &fakeBridgeCore{}
	old := testBridgeConfig(t, "NTSC_480i")
	r := NewBridgeSaver(testConfigPath(t), &config.Sectioned{Bridge: old}, core, adapters.NewRegistry())
	return r, core
}

func TestSaveAudioDSP_PersistsAndHotSwaps(t *testing.T) {
	t.Parallel()
	r, core := newBridgeSaverForTest(t)
	dsp := config.DefaultAudioDSP()
	dsp.Bass = 4
	scope, err := r.SaveAudioDSP(dsp)
	if err != nil {
		t.Fatalf("SaveAudioDSP: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v, want ScopeHotSwap", scope)
	}
	if r.Current().Audio.DSP.Bass != 4 {
		t.Error("in-memory bridge not updated")
	}
	if core.lastDSP.Bass != 4 || core.dspCalls != 1 {
		t.Errorf("core.SetAudioDSP not dispatched once with new params: %+v calls=%d", core.lastDSP, core.dspCalls)
	}
}

func TestSaveAudioDSPMemory_StoreAndRecall(t *testing.T) {
	t.Parallel()
	r, _ := newBridgeSaverForTest(t)
	store := config.DefaultAudioDSP()
	store.EQ[5] = 6
	if _, err := r.SaveAudioDSPMemory(2, "Rock", store); err != nil {
		t.Fatalf("store: %v", err)
	}
	mem := r.Current().Audio.DSP.Memory
	if len(mem) != 1 || mem[0].Slot != 2 || !mem[0].Stored || mem[0].EQ[5] != 6 {
		t.Fatalf("memory slot 2 not stored: %+v", mem)
	}
	// Recall returns the stored voicing for the chassis to apply.
	got, ok := r.RecallAudioDSPMemory(2)
	if !ok || got.EQ[5] != 6 {
		t.Errorf("recall slot 2 = %+v ok=%v", got, ok)
	}
	if _, ok := r.RecallAudioDSPMemory(3); ok {
		t.Error("empty slot 3 should not recall")
	}
}
