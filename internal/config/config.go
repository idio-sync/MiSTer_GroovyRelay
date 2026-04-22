package config

import (
	"errors"
	"fmt"
	"net"
	"os"

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

// Load reads the TOML at path on top of defaults() and validates the
// result. If the file is missing, Load writes the embedded example to
// that path and returns *ErrConfigCreated — this turns first-run into
// "start once, edit, start again" instead of "hand-copy an example."
func Load(path string) (*Config, error) {
	cfg := defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if wErr := writeDefaultConfig(path); wErr != nil {
				return nil, wErr
			}
			return nil, &ErrConfigCreated{Path: path}
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if len(data) > 0 {
		if err := toml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
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
