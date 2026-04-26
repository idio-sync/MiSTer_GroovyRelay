package config

import (
	"fmt"
	"net"

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
	DataDir string       `toml:"data_dir"`
	HostIP  string       `toml:"host_ip"`
	Video   VideoConfig  `toml:"video"`
	Audio   AudioConfig  `toml:"audio"`
	MiSTer  MisterConfig `toml:"mister"`
	UI      UIConfig     `toml:"ui"`
}

type VideoConfig struct {
	Modeline            string `toml:"modeline"`
	InterlaceFieldOrder string `toml:"interlace_field_order"`
	AspectMode          string `toml:"aspect_mode"`
	RGBMode             string `toml:"rgb_mode"`
	LZ4Enabled          bool   `toml:"lz4_enabled"`
}

type AudioConfig struct {
	SampleRate int `toml:"sample_rate"`
	Channels   int `toml:"channels"`
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
	if b.HostIP != "" && net.ParseIP(b.HostIP) == nil {
		return fmt.Errorf("bridge.host_ip must be a valid IP address, got %q", b.HostIP)
	}
	return nil
}

func validPort(p int, label string) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("%s must be in 1..65535, got %d", label, p)
	}
	return nil
}

// NOTE: The legacy flat `Config` type + `defaults()` remain inside
// this package as the migration source shape (migration.go decodes
// legacy flat TOML into a *Config before reshaping to Sectioned).
// Nothing outside internal/config reads them — the registry-based
// lifecycle hands BridgeConfig to core.Manager and per-adapter
// toml.Primitive sections to each adapter's DecodeConfig.
