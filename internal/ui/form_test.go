package ui

import (
	"net/url"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestParseBridgeForm_HappyPath(t *testing.T) {
	form := url.Values{}
	form.Set("mister.host", "192.168.1.42")
	form.Set("mister.port", "32100")
	form.Set("mister.source_port", "32101")
	form.Set("host_ip", "")
	form.Set("video.modeline", "NTSC_480i")
	form.Set("video.interlace_field_order", "bff")
	form.Set("video.aspect_mode", "auto")
	form.Set("video.lz4_enabled", "true")
	form.Set("video.delta_lz4_enabled", "true")
	form.Set("audio.sample_rate", "48000")
	form.Set("audio.channels", "2")
	form.Set("ui.http_port", "32500")
	form.Set("data_dir", "/config")
	form.Set("ffmpeg_path", "/tools/ffmpeg")
	form.Set("ffprobe_path", "/tools/ffprobe")
	form.Set("ytdlp_path", "/tools/yt-dlp")
	addHLSBufferFormDefaults(form)

	got, err := parseBridgeForm(form)
	if err != nil {
		t.Fatalf("parseBridgeForm: %v", err)
	}
	if got.MiSTer.Host != "192.168.1.42" {
		t.Errorf("Host = %q", got.MiSTer.Host)
	}
	if got.MiSTer.Port != 32100 {
		t.Errorf("Port = %d", got.MiSTer.Port)
	}
	if got.Video.InterlaceFieldOrder != "bff" {
		t.Errorf("InterlaceFieldOrder = %q", got.Video.InterlaceFieldOrder)
	}
	if !got.Video.LZ4Enabled {
		t.Error("LZ4Enabled should be true")
	}
	if !got.Video.DeltaLZ4Enabled {
		t.Error("DeltaLZ4Enabled should be true")
	}
	if got.Audio.Channels != 2 {
		t.Errorf("Channels = %d", got.Audio.Channels)
	}
	if got.FFmpegPath != "/tools/ffmpeg" || got.FFprobePath != "/tools/ffprobe" || got.YTDLPPath != "/tools/yt-dlp" {
		t.Errorf("tool paths not parsed: %+v", got)
	}
}

func TestParseBridgeForm_VisualizerMode(t *testing.T) {
	form := url.Values{}
	form.Set("mister.host", "192.168.1.42")
	form.Set("mister.port", "32100")
	form.Set("mister.source_port", "32101")
	form.Set("video.modeline", "NTSC_480i")
	form.Set("video.interlace_field_order", "bff")
	form.Set("video.aspect_mode", "auto")
	form.Set("audio.sample_rate", "48000")
	form.Set("audio.channels", "2")
	form.Set("ui.http_port", "32500")
	form.Set("data_dir", "/config")
	form.Set("visualizer.mode", config.VisualizerModeOscilloscopeWave)
	addHLSBufferFormDefaults(form)

	got, err := parseBridgeForm(form)
	if err != nil {
		t.Fatalf("parseBridgeForm: %v", err)
	}
	if got.Visualizer.Mode != config.VisualizerModeOscilloscopeWave {
		t.Errorf("Visualizer.Mode = %q, want %q", got.Visualizer.Mode, config.VisualizerModeOscilloscopeWave)
	}
}

func TestParseBridgeForm_BadInt(t *testing.T) {
	form := url.Values{}
	form.Set("mister.host", "192.168.1.42")
	form.Set("mister.port", "not-a-number")
	form.Set("mister.source_port", "32101")
	form.Set("video.modeline", "NTSC_480i")
	form.Set("video.interlace_field_order", "tff")
	form.Set("video.aspect_mode", "auto")
	form.Set("audio.sample_rate", "48000")
	form.Set("audio.channels", "2")
	form.Set("ui.http_port", "32500")
	form.Set("data_dir", "/config")
	addHLSBufferFormDefaults(form)

	_, err := parseBridgeForm(form)
	if err == nil {
		t.Fatal("want error on bad int")
	}
	fe, ok := err.(FormErrors)
	if !ok {
		t.Fatalf("want FormErrors, got %T", err)
	}
	if _, seen := fe["mister.port"]; !seen {
		t.Errorf("want mister.port error, got %v", fe)
	}
}

func TestParseBridgeForm_BoolFalse(t *testing.T) {
	// HTML checkboxes omit the field when unchecked — our parser
	// must treat a missing bool key as false, not a validation error.
	form := url.Values{}
	form.Set("mister.host", "192.168.1.42")
	form.Set("mister.port", "32100")
	form.Set("mister.source_port", "32101")
	form.Set("video.modeline", "NTSC_480i")
	form.Set("video.interlace_field_order", "tff")
	form.Set("video.aspect_mode", "auto")
	// no video.lz4_enabled → should default to false
	form.Set("audio.sample_rate", "48000")
	form.Set("audio.channels", "2")
	form.Set("ui.http_port", "32500")
	form.Set("data_dir", "/config")
	addHLSBufferFormDefaults(form)

	got, err := parseBridgeForm(form)
	if err != nil {
		t.Fatalf("parseBridgeForm: %v", err)
	}
	if got.Video.LZ4Enabled {
		t.Error("missing checkbox should parse as false, got true")
	}
	if got.Video.DeltaLZ4Enabled {
		t.Error("missing delta checkbox should parse as false, got true")
	}
	_ = got
	_ = config.BridgeConfig{} // ensure import
}

// TestParseBridgeForm_LoggingDebug pins the round-trip for the new
// "Debug Logging" checkbox: present → true, missing → false. Same
// shape as the LZ4Enabled pair of tests above.
func TestParseBridgeForm_LoggingDebug(t *testing.T) {
	base := url.Values{}
	base.Set("mister.host", "192.168.1.42")
	base.Set("mister.port", "32100")
	base.Set("mister.source_port", "32101")
	base.Set("video.modeline", "NTSC_480i")
	base.Set("video.interlace_field_order", "bff")
	base.Set("video.aspect_mode", "auto")
	base.Set("audio.sample_rate", "48000")
	base.Set("audio.channels", "2")
	base.Set("ui.http_port", "32500")
	base.Set("data_dir", "/config")
	addHLSBufferFormDefaults(base)

	t.Run("checked", func(t *testing.T) {
		form := cloneValues(base)
		form.Set("logging.debug", "true")
		got, err := parseBridgeForm(form)
		if err != nil {
			t.Fatalf("parseBridgeForm: %v", err)
		}
		if !got.Logging.Debug {
			t.Error("Logging.Debug should be true when checkbox present")
		}
	})

	t.Run("unchecked-missing-key", func(t *testing.T) {
		form := cloneValues(base)
		got, err := parseBridgeForm(form)
		if err != nil {
			t.Fatalf("parseBridgeForm: %v", err)
		}
		if got.Logging.Debug {
			t.Error("Logging.Debug should be false when checkbox absent")
		}
	})
}

func TestParseBridgeForm_HLSBufferFields(t *testing.T) {
	form := url.Values{}
	form.Set("mister.host", "192.168.1.42")
	form.Set("mister.port", "32100")
	form.Set("mister.source_port", "32101")
	form.Set("video.modeline", "NTSC_480i")
	form.Set("video.interlace_field_order", "bff")
	form.Set("video.aspect_mode", "auto")
	form.Set("audio.sample_rate", "48000")
	form.Set("audio.channels", "2")
	form.Set("ui.http_port", "32500")
	form.Set("data_dir", "/config")
	form.Set("hls_buffer.enabled", "true")
	form.Set("hls_buffer.live_edge_segments", "4")
	form.Set("hls_buffer.start_segments", "3")
	form.Set("hls_buffer.max_cached_segments", "8")
	form.Set("hls_buffer.max_cache_bytes", "536870912")
	form.Set("hls_buffer.max_playlist_bytes", "2097152")
	form.Set("hls_buffer.max_segment_bytes", "104857600")
	form.Set("hls_buffer.segment_timeout_seconds", "11")
	form.Set("hls_buffer.playlist_timeout_seconds", "12")
	form.Set("hls_buffer.max_variant_height", "1080")
	form.Set("hls_buffer.stale_cache_reap_hours", "48")

	got, err := parseBridgeForm(form)
	if err != nil {
		t.Fatalf("parseBridgeForm: %v", err)
	}
	want := config.HLSBufferConfig{
		Enabled:                true,
		LiveEdgeSegments:       4,
		StartSegments:          3,
		MaxCachedSegments:      8,
		MaxCacheBytes:          536870912,
		MaxPlaylistBytes:       2097152,
		MaxSegmentBytes:        104857600,
		SegmentTimeoutSeconds:  11,
		PlaylistTimeoutSeconds: 12,
		MaxVariantHeight:       1080,
		StaleCacheReapHours:    48,
	}
	if got.HLSBuffer != want {
		t.Fatalf("HLSBuffer = %+v, want %+v", got.HLSBuffer, want)
	}
}

func TestEnsureHLSBufferFormFieldsPreservesCurrentWhenSectionAbsent(t *testing.T) {
	form := url.Values{}
	cur := config.HLSBufferConfig{
		Enabled:                false,
		LiveEdgeSegments:       4,
		StartSegments:          3,
		MaxCachedSegments:      8,
		MaxCacheBytes:          536870912,
		MaxPlaylistBytes:       2097152,
		MaxSegmentBytes:        104857600,
		SegmentTimeoutSeconds:  11,
		PlaylistTimeoutSeconds: 12,
		MaxVariantHeight:       1080,
		StaleCacheReapHours:    48,
	}
	ensureHLSBufferFormFields(form, cur)
	if got := form.Get("hls_buffer.enabled"); got != "false" {
		t.Fatalf("hls_buffer.enabled = %q, want false", got)
	}
	if got := form.Get("hls_buffer.max_cache_bytes"); got != "536870912" {
		t.Fatalf("hls_buffer.max_cache_bytes = %q, want current value", got)
	}

	form.Set("hls_buffer.live_edge_segments", "9")
	ensureHLSBufferFormFields(form, cur)
	if got := form.Get("hls_buffer.live_edge_segments"); got != "9" {
		t.Fatalf("present hls_buffer field should not be overwritten, got %q", got)
	}
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func addHLSBufferFormDefaults(form url.Values) {
	form.Set("hls_buffer.enabled", "true")
	form.Set("hls_buffer.live_edge_segments", "3")
	form.Set("hls_buffer.start_segments", "2")
	form.Set("hls_buffer.max_cached_segments", "6")
	form.Set("hls_buffer.max_cache_bytes", "268435456")
	form.Set("hls_buffer.max_playlist_bytes", "1048576")
	form.Set("hls_buffer.max_segment_bytes", "52428800")
	form.Set("hls_buffer.segment_timeout_seconds", "10")
	form.Set("hls_buffer.playlist_timeout_seconds", "10")
	form.Set("hls_buffer.max_variant_height", "720")
	form.Set("hls_buffer.stale_cache_reap_hours", "24")
}

func TestStripExperimentalSuffix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"NTSC_480i", "NTSC_480i"},
		{"NTSC_240p", "NTSC_240p"},
		{"PAL_576i (experimental)", "PAL_576i"},
		{"PAL_288p (experimental)", "PAL_288p"},
		{"random_value", "random_value"},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := stripExperimentalSuffix(c.in)
			if got != c.want {
				t.Errorf("stripExperimentalSuffix(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestParseBridgeForm_StripsExperimentalSuffix(t *testing.T) {
	form := url.Values{}
	form.Set("video.modeline", "PAL_576i (experimental)")
	// Mirror the existing TestParseBridgeForm_HappyPath fixture (form_test.go:11-23)
	// so parseBridgeForm doesn't fail on missing required fields.
	form.Set("mister.host", "192.168.1.42")
	form.Set("mister.port", "32100")
	form.Set("mister.source_port", "32101")
	form.Set("host_ip", "")
	form.Set("video.interlace_field_order", "bff")
	form.Set("video.aspect_mode", "auto")
	form.Set("video.lz4_enabled", "true")
	form.Set("video.delta_lz4_enabled", "true")
	form.Set("audio.sample_rate", "48000")
	form.Set("audio.channels", "2")
	form.Set("ui.http_port", "32500")
	form.Set("data_dir", "/config")
	addHLSBufferFormDefaults(form)

	cfg, err := parseBridgeForm(form)
	if err != nil {
		t.Fatalf("parseBridgeForm: %v", err)
	}
	if cfg.Video.Modeline != "PAL_576i" {
		t.Errorf("modeline = %q, want %q (suffix should be stripped)",
			cfg.Video.Modeline, "PAL_576i")
	}
}

// TestParseBridgeForm_SSHFields confirms ssh_user / ssh_password
// round-trip through parseBridgeForm into BridgeConfig.MiSTer.
func TestParseBridgeForm_SSHFields(t *testing.T) {
	form := url.Values{}
	form.Set("mister.host", "192.168.1.42")
	form.Set("mister.port", "32100")
	form.Set("mister.source_port", "32101")
	form.Set("mister.ssh_user", "alice")
	form.Set("mister.ssh_password", "hunter2")
	form.Set("video.modeline", "NTSC_480i")
	form.Set("video.interlace_field_order", "tff")
	form.Set("video.aspect_mode", "auto")
	form.Set("audio.sample_rate", "48000")
	form.Set("audio.channels", "2")
	form.Set("ui.http_port", "32500")
	form.Set("data_dir", "/config")
	addHLSBufferFormDefaults(form)

	got, err := parseBridgeForm(form)
	if err != nil {
		t.Fatalf("parseBridgeForm: %v", err)
	}
	if got.MiSTer.SSHUser != "alice" {
		t.Errorf("SSHUser = %q, want alice", got.MiSTer.SSHUser)
	}
	if got.MiSTer.SSHPassword != "hunter2" {
		t.Errorf("SSHPassword = %q, want hunter2", got.MiSTer.SSHPassword)
	}
}
