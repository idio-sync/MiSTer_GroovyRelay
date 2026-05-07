package uiserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
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
		Audio:  config.AudioConfig{SampleRate: 48000, Channels: 2},
		MiSTer: config.MisterConfig{Port: 32100, SourcePort: 32101},
		UI:     config.UIConfig{HTTPPort: 32500},
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
	updated config.BridgeConfig
	drops   int
	dropErr error
}

func (f *fakeBridgeCore) UpdateBridge(b config.BridgeConfig) {
	f.updated = b
}

func (f *fakeBridgeCore) SetInterlaceFieldOrder(string) error {
	return nil
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
