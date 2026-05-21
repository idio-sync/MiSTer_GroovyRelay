package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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

func TestValidate_RejectsBadAudioSampleRate(t *testing.T) {
	for _, rate := range []int{0, 16000, 96000} {
		t.Run(fmt.Sprintf("%d", rate), func(t *testing.T) {
			c := defaults()
			c.AudioSampleRate = rate
			err := c.Validate()
			if err == nil {
				t.Fatalf("audio_sample_rate=%d: expected validation error, got nil", rate)
			}
			if !strings.Contains(err.Error(), "audio_sample_rate") {
				t.Errorf("audio_sample_rate=%d: error %q should mention audio_sample_rate", rate, err)
			}
		})
	}
}

func TestValidate_AcceptsSupportedAudioSampleRates(t *testing.T) {
	for _, rate := range []int{22050, 44100, 48000} {
		t.Run(fmt.Sprintf("%d", rate), func(t *testing.T) {
			c := defaults()
			c.AudioSampleRate = rate
			if err := c.Validate(); err != nil {
				t.Errorf("audio_sample_rate=%d: expected OK, got %v", rate, err)
			}
		})
	}
}

func TestValidate_RejectsBadAudioChannels(t *testing.T) {
	for _, chans := range []int{0, 3, 99} {
		t.Run(fmt.Sprintf("%d", chans), func(t *testing.T) {
			c := defaults()
			c.AudioChannels = chans
			err := c.Validate()
			if err == nil {
				t.Fatalf("audio_channels=%d: expected validation error, got nil", chans)
			}
			if !strings.Contains(err.Error(), "audio_channels") {
				t.Errorf("audio_channels=%d: error %q should mention audio_channels", chans, err)
			}
		})
	}
}

func TestValidate_AcceptsSupportedAudioChannels(t *testing.T) {
	for _, chans := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d", chans), func(t *testing.T) {
			c := defaults()
			c.AudioChannels = chans
			if err := c.Validate(); err != nil {
				t.Errorf("audio_channels=%d: expected OK, got %v", chans, err)
			}
		})
	}
}

func TestValidate_RejectsNonRGB888(t *testing.T) {
	for _, mode := range []string{"rgba8888", "rgb565", "rgb16"} {
		c := defaults()
		c.RGBMode = mode
		err := c.Validate()
		if err == nil {
			t.Errorf("rgb_mode=%q: expected validation error, got nil", mode)
			continue
		}
		if !strings.Contains(err.Error(), "rgb888") {
			t.Errorf("rgb_mode=%q: error %q should mention 'rgb888'", mode, err)
		}
	}
}

func TestValidate_AcceptsRGB888(t *testing.T) {
	c := defaults()
	c.RGBMode = "rgb888"
	if err := c.Validate(); err != nil {
		t.Errorf("rgb_mode=rgb888: expected OK, got %v", err)
	}
}

func TestValidate_RejectsMalformedHostIP(t *testing.T) {
	bad := []string{
		"not-an-ip",
		"192.168.1.20/24",     // CIDR typo
		"http://192.168.1.20", // URL typo
		"192.168.1",           // truncated
		"256.0.0.1",           // invalid octet
		"192.168.1.20:32500",  // with port
	}
	for _, v := range bad {
		t.Run(v, func(t *testing.T) {
			c := defaults()
			c.HostIP = v
			err := c.Validate()
			if err == nil {
				t.Errorf("host_ip=%q: expected validation error, got nil", v)
				return
			}
			if !strings.Contains(err.Error(), "host_ip") {
				t.Errorf("error should mention host_ip: %v", err)
			}
		})
	}
}

func TestValidate_AcceptsValidHostIP(t *testing.T) {
	for _, v := range []string{"192.168.1.20", "10.0.0.1", "::1", "fe80::1"} {
		t.Run(v, func(t *testing.T) {
			c := defaults()
			c.HostIP = v
			if err := c.Validate(); err != nil {
				t.Errorf("host_ip=%q: expected OK, got %v", v, err)
			}
		})
	}
}

func TestValidate_AcceptsEmptyHostIP(t *testing.T) {
	c := defaults()
	c.HostIP = ""
	if err := c.Validate(); err != nil {
		t.Errorf("empty host_ip (auto-detect fallback): expected OK, got %v", err)
	}
}

// TestSectioned_Validate_Modeline checks that Sectioned.Validate accepts all
// known modeline values (including the empty default) and rejects unknown ones
// at config-load time, before they can cause a silent failure at first cast.
func TestSectioned_Validate_Modeline(t *testing.T) {
	valid := []string{"", "NTSC_480i", "NTSC_240p", "PAL_576i", "PAL_288p"}
	for _, v := range valid {
		t.Run("valid/"+v, func(t *testing.T) {
			s := validSectioned()
			s.Bridge.Video.Modeline = v
			if err := s.Validate(); err != nil {
				t.Errorf("modeline=%q: expected OK, got %v", v, err)
			}
		})
	}

	invalid := []string{"ntsc_240p", "BOGUS", "NTSC480i", "PAL_576i (experimental)"}
	for _, v := range invalid {
		t.Run("invalid/"+v, func(t *testing.T) {
			s := validSectioned()
			s.Bridge.Video.Modeline = v
			err := s.Validate()
			if err == nil {
				t.Fatalf("modeline=%q: expected validation error, got nil", v)
			}
			if !strings.Contains(err.Error(), "modeline") {
				t.Errorf("modeline=%q: error %q should mention 'modeline'", v, err)
			}
		})
	}
}

// validSectioned returns a Sectioned with all required fields populated so
// individual tests can change just the field under test.
func validSectioned() *Sectioned {
	b := defaultBridge()
	b.MiSTer.Host = "192.168.1.50"
	return &Sectioned{Bridge: b}
}

func TestDefaultBridge_HLSBufferDefaults(t *testing.T) {
	b := defaultBridge()
	want := HLSBufferConfig{
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
	}
	if b.HLSBuffer != want {
		t.Fatalf("HLSBuffer defaults = %+v, want %+v", b.HLSBuffer, want)
	}
}

func TestLoadSectioned_MissingHLSBufferGetsDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := fmt.Sprintf(`
[bridge]
data_dir = %q

[bridge.video]
modeline = "NTSC_480i"
interlace_field_order = "tff"
aspect_mode = "auto"
rgb_mode = "rgb888"
lz4_enabled = true

[bridge.audio]
sample_rate = 48000
channels = 2

[bridge.mister]
host = "127.0.0.1"
port = 32100
source_port = 32101

[bridge.ui]
http_port = 32500
`, dir)
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sec, err := LoadSectioned(cfgPath)
	if err != nil {
		t.Fatalf("LoadSectioned: %v", err)
	}
	if got, want := sec.Bridge.HLSBuffer, defaultHLSBufferConfig(); got != want {
		t.Fatalf("HLSBuffer = %+v, want defaults %+v", got, want)
	}
}

func TestSectioned_Validate_HLSBuffer(t *testing.T) {
	valid := validSectioned()
	if err := valid.Validate(); err != nil {
		t.Fatalf("default HLSBuffer should validate: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*HLSBufferConfig)
		key  string
	}{
		{"live edge below start", func(c *HLSBufferConfig) { c.LiveEdgeSegments = 1; c.StartSegments = 2 }, "live_edge_segments"},
		{"start segments below one", func(c *HLSBufferConfig) { c.StartSegments = 0 }, "start_segments"},
		{"cached segments below start", func(c *HLSBufferConfig) { c.MaxCachedSegments = 1; c.StartSegments = 2 }, "max_cached_segments"},
		{"cache bytes too small", func(c *HLSBufferConfig) { c.MaxCacheBytes = 1 }, "max_cache_bytes"},
		{"playlist bytes too small", func(c *HLSBufferConfig) { c.MaxPlaylistBytes = 1 }, "max_playlist_bytes"},
		{"segment bytes too small", func(c *HLSBufferConfig) { c.MaxSegmentBytes = 1 }, "max_segment_bytes"},
		{"segment timeout too small", func(c *HLSBufferConfig) { c.SegmentTimeoutSeconds = 0 }, "segment_timeout_seconds"},
		{"playlist timeout too small", func(c *HLSBufferConfig) { c.PlaylistTimeoutSeconds = 0 }, "playlist_timeout_seconds"},
		{"variant height too small", func(c *HLSBufferConfig) { c.MaxVariantHeight = 1 }, "max_variant_height"},
		{"reap hours too small", func(c *HLSBufferConfig) { c.StaleCacheReapHours = 0 }, "stale_cache_reap_hours"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validSectioned()
			tc.mut(&s.Bridge.HLSBuffer)
			err := s.Validate()
			if err == nil {
				t.Fatalf("Validate() error = nil, want %s error", tc.key)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("Validate() error = %q, want mention %q", err, tc.key)
			}
		})
	}
}

// TestSectioned_RoundTripSSHFields confirms the new SSH credential
// fields decode + re-encode through BurntSushi/toml without loss.
// Catches a forgotten struct tag or a missed migration helper if
// either drifts in a future refactor.
func TestSectioned_RoundTripSSHFields(t *testing.T) {
	const input = `
[bridge]
[bridge.mister]
host = "192.168.1.42"
port = 32100
source_port = 32101
ssh_user = "alice"
ssh_password = "hunter2"
`
	s, _, err := loadSectionedFromBytes([]byte(input))
	if err != nil {
		t.Fatalf("loadSectionedFromBytes: %v", err)
	}
	if s.Bridge.MiSTer.SSHUser != "alice" {
		t.Errorf("SSHUser = %q, want alice", s.Bridge.MiSTer.SSHUser)
	}
	if s.Bridge.MiSTer.SSHPassword != "hunter2" {
		t.Errorf("SSHPassword = %q, want hunter2", s.Bridge.MiSTer.SSHPassword)
	}
}

// TestDefaultBridge_SSHCredentials pins the default SSH user and
// password so a future refactor of defaultBridge can't silently
// change them. Defaults match MiSTer's stock credentials (root / 1)
// so first-run installs work without operator intervention. Anyone
// running a hardened MiSTer (changed password) types their value in
// the Settings UI and the preserve-on-empty conditional in
// handleBridgePOST keeps it set across saves.
func TestDefaultBridge_SSHCredentials(t *testing.T) {
	b := defaultBridge()
	if b.MiSTer.SSHUser != "root" {
		t.Errorf("default SSHUser = %q, want root", b.MiSTer.SSHUser)
	}
	if b.MiSTer.SSHPassword != "1" {
		t.Errorf("default SSHPassword = %q, want 1", b.MiSTer.SSHPassword)
	}
}

func TestDefaultBridge_DeltaLZ4Enabled(t *testing.T) {
	b := defaultBridge()
	if !b.Video.DeltaLZ4Enabled {
		t.Error("default DeltaLZ4Enabled = false, want true")
	}
}

func TestDefaultBridge_VisualizerMode(t *testing.T) {
	b := defaultBridge()
	if b.Visualizer.Mode != VisualizerModeRetroAnalyzer {
		t.Errorf("default visualizer mode = %q, want %q", b.Visualizer.Mode, VisualizerModeRetroAnalyzer)
	}
}

func TestSectioned_RoundTripDeltaLZ4Enabled(t *testing.T) {
	const input = `
[bridge.video]
delta_lz4_enabled = false
`
	s, _, err := loadSectionedFromBytes([]byte(input))
	if err != nil {
		t.Fatalf("loadSectionedFromBytes: %v", err)
	}
	if s.Bridge.Video.DeltaLZ4Enabled {
		t.Error("DeltaLZ4Enabled = true, want decoded false")
	}
}

func TestNormalizeVisualizerMode(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", VisualizerModeRetroAnalyzer},
		{"   ", VisualizerModeRetroAnalyzer},
		{" retro_analyzer ", VisualizerModeRetroAnalyzer},
		{"oscilloscope_wave", VisualizerModeOscilloscopeWave},
		{"stereo_scope", VisualizerModeStereoScope},
	}
	for _, c := range cases {
		if got := NormalizeVisualizerMode(c.in); got != c.want {
			t.Errorf("NormalizeVisualizerMode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSupportedVisualizerModes_ReturnsDefensiveCopy(t *testing.T) {
	got := SupportedVisualizerModes()
	want := []string{
		VisualizerModeRetroAnalyzer,
		VisualizerModeOscilloscopeWave,
		VisualizerModeStereoScope,
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("SupportedVisualizerModes() = %#v, want %#v", got, want)
	}

	got[0] = "mutated"
	again := SupportedVisualizerModes()
	if again[0] != VisualizerModeRetroAnalyzer {
		t.Errorf("SupportedVisualizerModes returned shared backing array; got first value %q", again[0])
	}
}

func TestSectioned_Validate_VisualizerMode(t *testing.T) {
	valid := []string{
		"",
		VisualizerModeRetroAnalyzer,
		VisualizerModeOscilloscopeWave,
		VisualizerModeStereoScope,
	}
	for _, mode := range valid {
		t.Run("valid/"+mode, func(t *testing.T) {
			s := validSectioned()
			s.Bridge.Visualizer.Mode = mode
			if err := s.Validate(); err != nil {
				t.Fatalf("visualizer mode %q: expected OK, got %v", mode, err)
			}
			if s.Bridge.Visualizer.Mode != NormalizeVisualizerMode(mode) {
				t.Errorf("visualizer mode normalized to %q, want %q", s.Bridge.Visualizer.Mode, NormalizeVisualizerMode(mode))
			}
		})
	}

	invalid := []string{"retro", "milkdrop", "spectrogram", "RETRO_ANALYZER"}
	for _, mode := range invalid {
		t.Run("invalid/"+mode, func(t *testing.T) {
			s := validSectioned()
			s.Bridge.Visualizer.Mode = mode
			err := s.Validate()
			if err == nil {
				t.Fatalf("visualizer mode %q: expected validation error, got nil", mode)
			}
			msg := err.Error()
			if !strings.Contains(msg, "bridge.visualizer.mode") {
				t.Errorf("error %q should mention bridge.visualizer.mode", msg)
			}
			for _, supported := range SupportedVisualizerModes() {
				if !strings.Contains(msg, supported) {
					t.Errorf("error %q should mention supported value %q", msg, supported)
				}
			}
		})
	}
}

func TestExampleTOML_ContainsVisualizerAndValidates(t *testing.T) {
	data := ExampleTOML()
	text := string(data)
	if !strings.Contains(text, "[bridge.visualizer]") {
		t.Fatalf("example TOML missing [bridge.visualizer]:\n%s", text)
	}
	if !strings.Contains(text, `mode = "retro_analyzer"`) {
		t.Fatalf("example TOML missing explicit retro visualizer mode:\n%s", text)
	}

	s, _, err := loadSectionedFromBytes(data)
	if err != nil {
		t.Fatalf("load example TOML: %v", err)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("validate example TOML: %v", err)
	}
}

func TestSectioned_MissingVisualizerDefaultsToRetro(t *testing.T) {
	const input = `
[bridge.mister]
host = "192.168.1.42"
port = 32100
source_port = 32101
`
	s, _, err := loadSectionedFromBytes([]byte(input))
	if err != nil {
		t.Fatalf("loadSectionedFromBytes: %v", err)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if s.Bridge.Visualizer.Mode != VisualizerModeRetroAnalyzer {
		t.Errorf("visualizer mode = %q, want %q", s.Bridge.Visualizer.Mode, VisualizerModeRetroAnalyzer)
	}
}

func TestSectioned_Validate_ExternalToolPaths(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "ffmpeg")
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	if err := os.WriteFile(tool, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tool, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	s := validSectioned()
	s.Bridge.FFmpegPath = tool
	s.Bridge.FFprobePath = tool
	s.Bridge.YTDLPPath = tool
	if err := s.Validate(); err != nil {
		t.Fatalf("valid tool paths rejected: %v", err)
	}

	s.Bridge.FFmpegPath = filepath.Join(t.TempDir(), "missing")
	if err := s.Validate(); err == nil {
		t.Fatal("missing tool path should fail validation")
	}
}
