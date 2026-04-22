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
	// command in internal/ffmpeg/pipeline.go hardcodes -pix_fmt rgb24.
	// Selecting a non-rgb888 mode before those wires are complete produces
	// a torn raster. Revisit when v2+ extends the pipeline.
	if c.RGBMode != "rgb888" {
		return fmt.Errorf("rgb_mode: only rgb888 is supported in v1 (got %q; rgba8888/rgb565 reserved for future work)", c.RGBMode)
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
	Host       string `toml:"host"`
	Port       int    `toml:"port"`
	SourcePort int    `toml:"source_port"`
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

// ToLegacy flattens a Sectioned config into the pre-UI flat Config
// shape. Exists only as a Phase-1 transitional shim so main.go can
// keep driving core.Manager + plex.NewAdapter against the legacy
// struct while the adapter interface is under construction. Phase 2
// (adapter refactor) removes this method.
func (s *Sectioned) ToLegacy() *Config {
	c := defaults()
	c.DataDir = s.Bridge.DataDir
	c.HostIP = s.Bridge.HostIP
	c.Modeline = s.Bridge.Video.Modeline
	c.InterlaceFieldOrder = s.Bridge.Video.InterlaceFieldOrder
	c.AspectMode = s.Bridge.Video.AspectMode
	c.RGBMode = s.Bridge.Video.RGBMode
	c.LZ4Enabled = s.Bridge.Video.LZ4Enabled
	c.AudioSampleRate = s.Bridge.Audio.SampleRate
	c.AudioChannels = s.Bridge.Audio.Channels
	c.MisterHost = s.Bridge.MiSTer.Host
	c.MisterPort = s.Bridge.MiSTer.Port
	c.SourcePort = s.Bridge.MiSTer.SourcePort
	c.HTTPPort = s.Bridge.UI.HTTPPort

	// Decode the Plex adapter section (if present) so device_name /
	// profile_name / server_url flow through to the legacy struct.
	// PrimitiveDecode is a no-op on a zero Primitive, but we gate on
	// map presence to avoid clobbering defaults when the adapter
	// section is absent entirely.
	if raw, ok := s.Adapters["plex"]; ok {
		var plexRaw struct {
			DeviceName  string `toml:"device_name"`
			DeviceUUID  string `toml:"device_uuid"`
			ProfileName string `toml:"profile_name"`
			ServerURL   string `toml:"server_url"`
		}
		_ = s.meta.PrimitiveDecode(raw, &plexRaw)
		if plexRaw.DeviceName != "" {
			c.DeviceName = plexRaw.DeviceName
		}
		c.DeviceUUID = plexRaw.DeviceUUID
		if plexRaw.ProfileName != "" {
			c.PlexProfileName = plexRaw.ProfileName
		}
		c.PlexServerURL = plexRaw.ServerURL
	}
	return c
}
