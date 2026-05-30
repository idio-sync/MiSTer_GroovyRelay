package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
	// HostIP is the LAN IP address the bridge advertises in /resources and
	// plex.tv RegisterDevice. If empty, the bridge falls back to a route-based
	// auto-detection which routes a UDP packet to 8.8.8.8 and reads the
	// local address. On multi-NIC hosts (Unraid with both LAN and WireGuard
	// interfaces is the common case), the auto-detected IP may be the WG
	// interface, not the LAN — and the Plex controller cannot reach the
	// WG-only address. Set host_ip explicitly when the default route is not
	// the Plex-facing NIC. See README "Multi-NIC Unraid hosts".
	HostIP string `toml:"host_ip"`

	// Video output
	Modeline            string `toml:"modeline"`
	InterlaceFieldOrder string `toml:"interlace_field_order"` // "tff" | "bff"
	AspectMode          string `toml:"aspect_mode"`           // "letterbox" | "zoom" | "auto"
	RGBMode             string `toml:"rgb_mode"`              // v1: "rgb888" only (rgba8888 / rgb565 reserved for v2)
	LZ4Enabled          bool   `toml:"lz4_enabled"`
	DeltaLZ4Enabled     bool   `toml:"delta_lz4_enabled"`

	// Audio
	AudioSampleRate   int `toml:"audio_sample_rate"`
	AudioChannels     int `toml:"audio_channels"`
	AudioOutputVolume int `toml:"audio_output_volume"`

	// Plex
	PlexProfileName string `toml:"plex_profile_name"`
	PlexServerURL   string `toml:"plex_server_url"`

	// Paths
	DataDir string `toml:"data_dir"`
}

const (
	VisualizerModeRetroAnalyzer     = "retro_analyzer"
	VisualizerModeOscilloscopeWave  = "oscilloscope_wave"
	VisualizerModeStereoScope       = "stereo_scope"
	VisualizerModeVUCabinet         = "vu_cabinet"
	VisualizerModeSpectrumWaterfall = "spectrum_waterfall"
	VisualizerModeRasterPulse       = "raster_pulse"
	VisualizerModeCoverVU           = "cover_vu"
	VisualizerModeCoverSpectrum     = "cover_spectrum"
)

var supportedVisualizerModes = []string{
	VisualizerModeRetroAnalyzer,
	VisualizerModeOscilloscopeWave,
	VisualizerModeStereoScope,
	VisualizerModeVUCabinet,
	VisualizerModeSpectrumWaterfall,
	VisualizerModeRasterPulse,
	VisualizerModeCoverVU,
	VisualizerModeCoverSpectrum,
}

func NormalizeVisualizerMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return VisualizerModeRetroAnalyzer
	}
	return mode
}

func SupportedVisualizerModes() []string {
	out := make([]string, len(supportedVisualizerModes))
	copy(out, supportedVisualizerModes)
	return out
}

func defaults() *Config {
	return &Config{
		DeviceName:          "MiSTer",
		MisterPort:          32100,
		SourcePort:          32101,
		HTTPPort:            32500,
		Modeline:            "NTSC_480i",
		InterlaceFieldOrder: "bff",
		AspectMode:          "auto",
		RGBMode:             "rgb888",
		LZ4Enabled:          true,
		DeltaLZ4Enabled:     true,
		AudioSampleRate:     48000,
		AudioChannels:       2,
		AudioOutputVolume:   100,
		PlexProfileName:     "Plex Home Theater",
		DataDir:             "",
	}
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
	// v1 scope: only rgb888 is wired through the FFmpeg pipeline. The Groovy
	// protocol supports rgba8888 and rgb565 and the constants exist in
	// internal/groovy and internal/core for future use, but the FFmpeg
	// command in internal/ffmpeg/pipeline.go hardcodes -pix_fmt bgr24.
	// Selecting a non-rgb888 mode before those wires are complete produces
	// a torn raster. Revisit when v2+ extends the pipeline.
	if c.RGBMode != "rgb888" {
		return fmt.Errorf("rgb_mode: only rgb888 is supported in v1 (got %q; rgba8888/rgb565 reserved for future work)", c.RGBMode)
	}
	switch c.AudioSampleRate {
	case 22050, 44100, 48000:
	default:
		return fmt.Errorf("audio_sample_rate must be 22050, 44100, or 48000, got %d", c.AudioSampleRate)
	}
	switch c.AudioChannels {
	case 1, 2:
	default:
		return fmt.Errorf("audio_channels must be 1 or 2, got %d", c.AudioChannels)
	}
	if c.AudioOutputVolume < 0 || c.AudioOutputVolume > 100 {
		return fmt.Errorf("audio_output_volume must be in 0..100, got %d", c.AudioOutputVolume)
	}
	// host_ip is optional (empty → auto-detect in main.go). When set, it must
	// parse as a valid IP address. Catches fat-fingered CIDR (/24 suffix),
	// URL-style values ("http://..."), and outright typos before the bridge
	// silently fails at the first plex.tv registration tick.
	if c.HostIP != "" && net.ParseIP(c.HostIP) == nil {
		return fmt.Errorf("host_ip must be a valid IP address, got %q", c.HostIP)
	}
	return nil
}

// ---- Sectioned schema (design §5.3) ----

// BridgeConfig groups adapter-agnostic fields: shared data-plane
// pipeline settings, MiSTer destination, bridge-level HTTP port,
// data directory. Every adapter shares these.
type BridgeConfig struct {
	DataDir     string           `toml:"data_dir"`
	FFmpegPath  string           `toml:"ffmpeg_path"`
	FFprobePath string           `toml:"ffprobe_path"`
	YTDLPPath   string           `toml:"ytdlp_path"`
	HostIP      string           `toml:"host_ip"`
	Video       VideoConfig      `toml:"video"`
	Audio       AudioConfig      `toml:"audio"`
	Visualizer  VisualizerConfig `toml:"visualizer"`
	MiSTer      MisterConfig     `toml:"mister"`
	UI          UIConfig         `toml:"ui"`
	HLSBuffer   HLSBufferConfig  `toml:"hls_buffer"`
	Logging     LoggingConfig    `toml:"logging"`
}

type VideoConfig struct {
	Modeline            string `toml:"modeline"`
	InterlaceFieldOrder string `toml:"interlace_field_order"`
	AspectMode          string `toml:"aspect_mode"`
	RGBMode             string `toml:"rgb_mode"`
	LZ4Enabled          bool   `toml:"lz4_enabled"`
	DeltaLZ4Enabled     bool   `toml:"delta_lz4_enabled"`
}

type AudioConfig struct {
	SampleRate   int      `toml:"sample_rate"`
	Channels     int      `toml:"channels"`
	OutputVolume int      `toml:"output_volume"`
	DSP          AudioDSP `toml:"dsp"`
}

// AudioDSP is the live tone/EQ chain configuration (spec §Config). dB
// fields are clamped to ±12 and Balance to ±100 by ValidateAudioDSP; EQ is
// the 10 ISO-octave band gains (31 Hz..16 kHz). A missing [bridge.audio.dsp]
// table inherits DefaultAudioDSP() via defaultBridge() seeding.
type AudioDSP struct {
	Enabled  bool             `toml:"enabled"`
	Mono     bool             `toml:"mono"`
	Subsonic bool             `toml:"subsonic"`
	Loudness bool             `toml:"loudness"`
	Bass     float64          `toml:"bass"`
	Mid      float64          `toml:"mid"`
	Treble   float64          `toml:"treble"`
	Balance  int              `toml:"balance"`
	EQ       []float64        `toml:"eq"`
	Memory   []AudioDSPMemory `toml:"memory"`
}

// AudioDSPMemory is one saved EQ voicing (M1..M3). Stored distinguishes a
// saved-flat memory from an empty slot.
type AudioDSPMemory struct {
	Slot   int       `toml:"slot"`
	Name   string    `toml:"name"`
	Stored bool      `toml:"stored"`
	Bass   float64   `toml:"bass"`
	Mid    float64   `toml:"mid"`
	Treble float64   `toml:"treble"`
	EQ     []float64 `toml:"eq"`
}

// DefaultAudioDSP returns the transparent default: enabled (defeat off),
// nothing engaged, a 10-band flat EQ.
func DefaultAudioDSP() AudioDSP {
	return AudioDSP{Enabled: true, EQ: make([]float64, 10)}
}

// Engaged reports whether shaping is active (drives the status-bar EQ LED).
// Independent of mono/balance; false under defeat (Enabled == false).
func (a AudioDSP) Engaged() bool {
	if !a.Enabled {
		return false
	}
	if a.Subsonic || a.Loudness || a.Bass != 0 || a.Mid != 0 || a.Treble != 0 {
		return true
	}
	for _, g := range a.EQ {
		if g != 0 {
			return true
		}
	}
	return false
}

type VisualizerConfig struct {
	Mode string `toml:"mode"`
}

type MisterConfig struct {
	Host        string `toml:"host"`
	Port        int    `toml:"port"`
	SourcePort  int    `toml:"source_port"`
	SSHUser     string `toml:"ssh_user"`
	SSHPassword string `toml:"ssh_password"`
}

type UIConfig struct {
	HTTPPort int `toml:"http_port"`
}

type HLSBufferConfig struct {
	Enabled                bool  `toml:"enabled"`
	LiveEdgeSegments       int   `toml:"live_edge_segments"`
	StartSegments          int   `toml:"start_segments"`
	MaxCachedSegments      int   `toml:"max_cached_segments"`
	MaxCacheBytes          int64 `toml:"max_cache_bytes"`
	MaxPlaylistBytes       int64 `toml:"max_playlist_bytes"`
	MaxSegmentBytes        int64 `toml:"max_segment_bytes"`
	SegmentTimeoutSeconds  int   `toml:"segment_timeout_seconds"`
	PlaylistTimeoutSeconds int   `toml:"playlist_timeout_seconds"`
	MaxVariantHeight       int   `toml:"max_variant_height"`
	StaleCacheReapHours    int   `toml:"stale_cache_reap_hours"`
}

func defaultHLSBufferConfig() HLSBufferConfig {
	return HLSBufferConfig{
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
}

// LoggingConfig is the bridge's runtime logging knob set. v1 has one
// field — a debug toggle exposed in the settings UI as a checkbox so
// the operator can enable verbose timeline / request logs without a
// container restart. New() in internal/logging owns the slog.LevelVar
// the toggle drives.
type LoggingConfig struct {
	Debug bool `toml:"debug"`
}

// Sectioned is the post-migration config envelope. Adapter sections
// live as toml.Primitive so each adapter can decode its own subtree
// with preserved TOML-native types (dates, times, etc.). The meta
// field carries toml.MetaData needed by toml.PrimitiveDecode.
type Sectioned struct {
	Bridge   BridgeConfig              `toml:"bridge"`
	Adapters map[string]toml.Primitive `toml:"adapters"`

	meta toml.MetaData
}

// MetaData exposes the decoder metadata captured at Load time.
// Adapters pass this to toml.PrimitiveDecode to hydrate their
// Primitive section.
func (s *Sectioned) MetaData() toml.MetaData { return s.meta }

// Validate checks bridge-level fields. Adapter sections validate
// themselves inside each adapter's DecodeConfig. Returns the first
// error found; callers expecting UI-surface multi-error output use
// the FieldError taxonomy in internal/adapters.
func (s *Sectioned) Validate() error {
	b := &s.Bridge

	if b.MiSTer.Host == "" {
		return fmt.Errorf("bridge.mister.host is required")
	}
	if err := validPort(b.MiSTer.Port, "bridge.mister.port"); err != nil {
		return err
	}
	if err := validPort(b.MiSTer.SourcePort, "bridge.mister.source_port"); err != nil {
		return err
	}
	if err := validPort(b.UI.HTTPPort, "bridge.ui.http_port"); err != nil {
		return err
	}

	switch b.Video.Modeline {
	case "", "NTSC_480i", "NTSC_240p", "PAL_576i", "PAL_288p":
	default:
		return fmt.Errorf("bridge.video.modeline must be one of NTSC_480i, NTSC_240p, PAL_576i, PAL_288p (or empty for default), got %q", b.Video.Modeline)
	}
	switch b.Video.InterlaceFieldOrder {
	case "tff", "bff":
	default:
		return fmt.Errorf("bridge.video.interlace_field_order must be tff or bff, got %q", b.Video.InterlaceFieldOrder)
	}
	switch b.Video.AspectMode {
	case "letterbox", "zoom", "auto":
	default:
		return fmt.Errorf("bridge.video.aspect_mode must be letterbox, zoom, or auto, got %q", b.Video.AspectMode)
	}
	if b.Video.RGBMode != "rgb888" {
		return fmt.Errorf("bridge.video.rgb_mode: only rgb888 is supported (got %q)", b.Video.RGBMode)
	}
	switch b.Audio.SampleRate {
	case 22050, 44100, 48000:
	default:
		return fmt.Errorf("bridge.audio.sample_rate must be 22050, 44100, or 48000, got %d", b.Audio.SampleRate)
	}
	if b.Audio.Channels != 1 && b.Audio.Channels != 2 {
		return fmt.Errorf("bridge.audio.channels must be 1 or 2, got %d", b.Audio.Channels)
	}
	if b.Audio.OutputVolume < 0 || b.Audio.OutputVolume > 100 {
		return fmt.Errorf("bridge.audio.output_volume must be in 0..100, got %d", b.Audio.OutputVolume)
	}
	if err := ValidateAudioDSP(b.Audio.DSP); err != nil {
		return err
	}
	b.Visualizer.Mode = NormalizeVisualizerMode(b.Visualizer.Mode)
	switch b.Visualizer.Mode {
	case VisualizerModeRetroAnalyzer,
		VisualizerModeOscilloscopeWave,
		VisualizerModeStereoScope,
		VisualizerModeVUCabinet,
		VisualizerModeSpectrumWaterfall,
		VisualizerModeRasterPulse,
		VisualizerModeCoverVU,
		VisualizerModeCoverSpectrum:
	default:
		return fmt.Errorf("bridge.visualizer.mode must be one of %s, got %q", strings.Join(SupportedVisualizerModes(), ", "), b.Visualizer.Mode)
	}
	if b.HostIP != "" && net.ParseIP(b.HostIP) == nil {
		return fmt.Errorf("bridge.host_ip must be a valid IP address, got %q", b.HostIP)
	}
	if err := validateHLSBufferConfig(b.HLSBuffer); err != nil {
		return err
	}
	if err := validateExternalToolPath("bridge.ffmpeg_path", b.FFmpegPath); err != nil {
		return err
	}
	if err := validateExternalToolPath("bridge.ffprobe_path", b.FFprobePath); err != nil {
		return err
	}
	if err := validateExternalToolPath("bridge.ytdlp_path", b.YTDLPPath); err != nil {
		return err
	}
	return nil
}

func validateHLSBufferConfig(c HLSBufferConfig) error {
	if c.LiveEdgeSegments < 1 || c.LiveEdgeSegments > 12 || c.LiveEdgeSegments < c.StartSegments {
		return fmt.Errorf("bridge.hls_buffer.live_edge_segments must be in [1, 12] and >= start_segments")
	}
	if c.StartSegments < 1 || c.StartSegments > 6 {
		return fmt.Errorf("bridge.hls_buffer.start_segments must be in [1, 6]")
	}
	if c.MaxCachedSegments < 2 || c.MaxCachedSegments > 24 || c.MaxCachedSegments < c.StartSegments {
		return fmt.Errorf("bridge.hls_buffer.max_cached_segments must be in [2, 24] and >= start_segments")
	}
	if c.MaxCacheBytes < 16777216 || c.MaxCacheBytes > 2147483648 {
		return fmt.Errorf("bridge.hls_buffer.max_cache_bytes must be in [16777216, 2147483648]")
	}
	if c.MaxPlaylistBytes < 4096 || c.MaxPlaylistBytes > 8388608 {
		return fmt.Errorf("bridge.hls_buffer.max_playlist_bytes must be in [4096, 8388608]")
	}
	if c.MaxSegmentBytes < 1048576 || c.MaxSegmentBytes > 536870912 {
		return fmt.Errorf("bridge.hls_buffer.max_segment_bytes must be in [1048576, 536870912]")
	}
	if c.SegmentTimeoutSeconds < 1 || c.SegmentTimeoutSeconds > 60 {
		return fmt.Errorf("bridge.hls_buffer.segment_timeout_seconds must be in [1, 60]")
	}
	if c.PlaylistTimeoutSeconds < 1 || c.PlaylistTimeoutSeconds > 60 {
		return fmt.Errorf("bridge.hls_buffer.playlist_timeout_seconds must be in [1, 60]")
	}
	if c.MaxVariantHeight < 240 || c.MaxVariantHeight > 2160 {
		return fmt.Errorf("bridge.hls_buffer.max_variant_height must be in [240, 2160]")
	}
	if c.StaleCacheReapHours < 1 || c.StaleCacheReapHours > 168 {
		return fmt.Errorf("bridge.hls_buffer.stale_cache_reap_hours must be in [1, 168]")
	}
	return nil
}

func validateExternalToolPath(label, path string) error {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s %q is not usable: %w", label, path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s %q is not usable: is a directory", label, path)
	}
	if runtime.GOOS == "windows" {
		if strings.EqualFold(filepath.Ext(path), ".exe") {
			return nil
		}
		return fmt.Errorf("%s %q is not usable: does not have .exe extension", label, path)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("%s %q is not usable: not executable", label, path)
	}
	return nil
}

func validPort(p int, label string) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("%s must be in 1..65535, got %d", label, p)
	}
	return nil
}

const (
	audioDSPMaxDB   = 12.0
	audioDSPMaxBal  = 100
	audioDSPBands   = 10
	audioDSPMaxSlot = 3
)

// ValidateAudioDSP enforces the spec's bounds. EQ must already be length 10
// (normalizeSectionedRuntimeDefaults pads omitted arrays before Validate).
func ValidateAudioDSP(a AudioDSP) error {
	if err := dspDBInRange("bass", a.Bass); err != nil {
		return err
	}
	if err := dspDBInRange("mid", a.Mid); err != nil {
		return err
	}
	if err := dspDBInRange("treble", a.Treble); err != nil {
		return err
	}
	if a.Balance < -audioDSPMaxBal || a.Balance > audioDSPMaxBal {
		return fmt.Errorf("bridge.audio.dsp.balance must be in -100..100, got %d", a.Balance)
	}
	if len(a.EQ) != audioDSPBands {
		return fmt.Errorf("bridge.audio.dsp.eq must have %d bands, got %d", audioDSPBands, len(a.EQ))
	}
	for i, g := range a.EQ {
		if err := dspDBInRange(fmt.Sprintf("eq[%d]", i), g); err != nil {
			return err
		}
	}
	seen := map[int]bool{}
	for _, m := range a.Memory {
		if m.Slot < 1 || m.Slot > audioDSPMaxSlot {
			return fmt.Errorf("bridge.audio.dsp.memory slot must be 1..%d, got %d", audioDSPMaxSlot, m.Slot)
		}
		if seen[m.Slot] {
			return fmt.Errorf("bridge.audio.dsp.memory has duplicate slot %d", m.Slot)
		}
		seen[m.Slot] = true
		if err := dspDBInRange("memory.bass", m.Bass); err != nil {
			return err
		}
		if err := dspDBInRange("memory.mid", m.Mid); err != nil {
			return err
		}
		if err := dspDBInRange("memory.treble", m.Treble); err != nil {
			return err
		}
		if len(m.EQ) != audioDSPBands {
			return fmt.Errorf("bridge.audio.dsp.memory[%d].eq must have %d bands, got %d", m.Slot, audioDSPBands, len(m.EQ))
		}
		for i, g := range m.EQ {
			if err := dspDBInRange(fmt.Sprintf("memory[%d].eq[%d]", m.Slot, i), g); err != nil {
				return err
			}
		}
	}
	return nil
}

func dspDBInRange(name string, v float64) error {
	if v < -audioDSPMaxDB || v > audioDSPMaxDB {
		return fmt.Errorf("bridge.audio.dsp.%s must be in -12..12 dB, got %g", name, v)
	}
	return nil
}

// NOTE: The legacy flat `Config` type + `defaults()` remain inside
// this package as the migration source shape (migration.go decodes
// legacy flat TOML into a *Config before reshaping to Sectioned).
// Nothing outside internal/config reads them — the registry-based
// lifecycle hands BridgeConfig to core.Manager and per-adapter
// toml.Primitive sections to each adapter's DecodeConfig.
