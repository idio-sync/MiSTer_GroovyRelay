package ui

import (
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// modelineEnumOptions returns the dropdown values for video.modeline.
// Names are passed through verbatim so the saved config string matches
// the preset's Name field; experimental presets get a "(experimental)"
// suffix in a sibling display-label column once we have one — for v1.5
// the suffix is appended to the value string directly.
func modelineEnumOptions() []string {
	names := core.PresetNames()
	out := make([]string, 0, len(names))
	for _, n := range names {
		preset, err := core.ResolvePreset(n)
		if err != nil {
			continue // unreachable: PresetNames returns only registered names
		}
		if preset.Experimental {
			out = append(out, n+" (experimental)")
		} else {
			out = append(out, n)
		}
	}
	return out
}

// bridgeFields is the Bridge panel's form schema, rendered in order
// and grouped by Section. Field keys match the TOML path (dotted)
// so form-parse can reconstitute them into a BridgeConfig.
//
// The Default column is informational only — actual defaults come
// from config.defaultBridge(). ApplyScope maps each field to the
// three-tier apply model (design §9.2).
func bridgeFields() []adapters.FieldDef {
	return []adapters.FieldDef{
		// ---- Network ----
		{
			Key:        "mister.host",
			Label:      "MiSTer Host",
			Help:       "IP or hostname of your MiSTer on the LAN.",
			Kind:       adapters.KindText,
			Required:   true,
			ApplyScope: adapters.ScopeRestartBridge,
			Section:    "Network",
		},
		{
			Key:        "mister.port",
			Label:      "MiSTer Port",
			Help:       "UDP port the MiSTer's Groovy core listens on.",
			Kind:       adapters.KindInt,
			Default:    32100,
			ApplyScope: adapters.ScopeRestartBridge,
			Section:    "Network",
		},
		{
			Key:        "mister.source_port",
			Label:      "Source Port",
			Help:       "Our stable source UDP port. Must stay the same across restarts.",
			Kind:       adapters.KindInt,
			Default:    32101,
			ApplyScope: adapters.ScopeRestartBridge,
			Section:    "Network",
		},
		{
			Key:         "host_ip",
			Label:       "Host IP",
			Help:        "LAN IP this bridge advertises to Plex. Leave empty to auto-detect.",
			Kind:        adapters.KindText,
			ApplyScope:  adapters.ScopeRestartBridge,
			Placeholder: "auto-detect",
			Section:     "Network",
		},

		// ---- Video ----
		{
			Key:        "video.modeline",
			Label:      "Modeline",
			Help:       "CRT output resolution. PAL modes are verified against the Groovy wire protocol via fake-mister but have not been tested on real PAL CRT hardware. TFF/BFF setting below only affects interlaced modes (480i / 576i).",
			Kind:       adapters.KindEnum,
			Enum:       modelineEnumOptions(),
			Default:    "NTSC_480i",
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "Video",
		},
		{
			Key:        "video.interlace_field_order",
			Label:      "Interlace Order",
			Help:       "Flip if you see shimmer on the CRT.",
			Kind:       adapters.KindEnum,
			Enum:       []string{"tff", "bff"},
			Default:    "bff",
			ApplyScope: adapters.ScopeHotSwap,
			Section:    "Video",
		},
		{
			Key:        "video.aspect_mode",
			Label:      "Aspect Mode",
			Help:       "How the source is fit to 4:3 NTSC.",
			Kind:       adapters.KindEnum,
			Enum:       []string{"letterbox", "zoom", "auto"},
			Default:    "auto",
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "Video",
		},
		{
			Key:        "video.lz4_enabled",
			Label:      "LZ4 Compression",
			Help:       "Compress BLIT payloads. Strongly recommended.",
			Kind:       adapters.KindBool,
			Default:    true,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "Video",
		},
		{
			Key:        "video.delta_lz4_enabled",
			Label:      "Delta-LZ4",
			Help:       "Use adaptive delta-compressed BLITs when they beat full-field LZ4.",
			Kind:       adapters.KindBool,
			Default:    true,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "Video",
		},

		// ---- Audio ----
		{
			Key:        "audio.sample_rate",
			Label:      "Sample Rate",
			Help:       "PCM sample rate.",
			Kind:       adapters.KindEnum,
			Enum:       []string{"22050", "44100", "48000"},
			Default:    "48000",
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "Audio",
		},
		{
			Key:        "audio.channels",
			Label:      "Channels",
			Help:       "1 (mono) or 2 (stereo).",
			Kind:       adapters.KindEnum,
			Enum:       []string{"1", "2"},
			Default:    "2",
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "Audio",
		},
		{
			Key:        "audio.output_volume",
			Label:      "Output Volume",
			Help:       "Global software volume sent to the MiSTer. 0 mutes; 100 is unchanged.",
			Kind:       adapters.KindInt,
			Default:    100,
			ApplyScope: adapters.ScopeHotSwap,
			Section:    "Audio",
		},

		// ---- Visualizer ----
		{
			Key:        "visualizer.mode",
			Label:      "Mode",
			Help:       "Music visualizer mode for the next music cast.",
			Kind:       adapters.KindEnum,
			Enum:       config.SupportedVisualizerModes(),
			Default:    config.VisualizerModeRetroAnalyzer,
			ApplyScope: adapters.ScopeNextCast,
			Section:    "Visualizer",
		},

		// ---- Server ----
		{
			Key:        "ui.http_port",
			Label:      "HTTP Port",
			Help:       "Plex Companion HTTP + Settings UI (shared listener).",
			Kind:       adapters.KindInt,
			Default:    32500,
			ApplyScope: adapters.ScopeRestartBridge,
			Section:    "Server",
		},
		{
			Key:        "data_dir",
			Label:      "Data Directory",
			Help:       "Where plex.json and other persistent state live. Leave empty for the OS default.",
			Kind:       adapters.KindText,
			Default:    "",
			ApplyScope: adapters.ScopeRestartBridge,
			Section:    "Server",
		},

		// ---- External Tools ----
		{
			Key:         "ffmpeg_path",
			Label:       "FFmpeg Path",
			Help:        "Override the FFmpeg binary. Empty uses the bundled sidecar, then PATH.",
			Kind:        adapters.KindText,
			ApplyScope:  adapters.ScopeHotSwap,
			Placeholder: "auto",
			Section:     "External Tools",
		},
		{
			Key:         "ffprobe_path",
			Label:       "FFprobe Path",
			Help:        "Override the FFprobe binary. Empty uses the bundled sidecar, then PATH.",
			Kind:        adapters.KindText,
			ApplyScope:  adapters.ScopeHotSwap,
			Placeholder: "auto",
			Section:     "External Tools",
		},
		{
			Key:         "ytdlp_path",
			Label:       "yt-dlp Path",
			Help:        "Override the yt-dlp binary. Empty uses the bundled sidecar, then PATH.",
			Kind:        adapters.KindText,
			ApplyScope:  adapters.ScopeHotSwap,
			Placeholder: "auto",
			Section:     "External Tools",
		},

		// ---- MiSTer Control ----
		{
			Key:        "mister.ssh_user",
			Label:      "SSH User",
			Help:       "User to SSH into the MiSTer as. MiSTer's stock user is root.",
			Kind:       adapters.KindText,
			Default:    "root",
			ApplyScope: adapters.ScopeHotSwap,
			Section:    "MiSTer Control",
		},
		{
			Key:        "mister.ssh_password",
			Label:      "SSH Password",
			Help:       "MiSTer's stock password is 1. Stored plaintext in config.toml; the bridge does not verify the MiSTer's host key (LAN-only trust model).",
			Kind:       adapters.KindSecret,
			Default:    "",
			ApplyScope: adapters.ScopeHotSwap,
			Section:    "MiSTer Control",
		},

		// ---- Logging ----
		{
			Key:        "logging.debug",
			Label:      "Debug Logging",
			Help:       "Emit verbose slog records (request traces, timeline pushes, subscriber prunes). Takes effect immediately — no cast or container restart needed. Persisted across restarts.",
			Kind:       adapters.KindBool,
			Default:    false,
			ApplyScope: adapters.ScopeHotSwap,
			Section:    "Logging",
		},

		// ---- HLS Buffer ----
		{
			Key:        "hls_buffer.enabled",
			Label:      "Enabled",
			Help:       "Buffer eligible live .m3u8 casts through a local segment cache.",
			Kind:       adapters.KindBool,
			Default:    true,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "HLS Buffer",
		},
		{
			Key:        "hls_buffer.live_edge_segments",
			Label:      "Live Edge Segments",
			Help:       "Segments to stay behind the live edge.",
			Kind:       adapters.KindInt,
			Default:    3,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "HLS Buffer",
		},
		{
			Key:        "hls_buffer.start_segments",
			Label:      "Start Segments",
			Help:       "Segments to warm before handing the stream to FFmpeg.",
			Kind:       adapters.KindInt,
			Default:    2,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "HLS Buffer",
		},
		{
			Key:        "hls_buffer.max_cached_segments",
			Label:      "Max Cached Segments",
			Help:       "Rolling segment count retained per active cast.",
			Kind:       adapters.KindInt,
			Default:    6,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "HLS Buffer",
		},
		{
			Key:        "hls_buffer.max_cache_bytes",
			Label:      "Max Cache Bytes",
			Help:       "Per-session cache byte ceiling.",
			Kind:       adapters.KindInt,
			Default:    268435456,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "HLS Buffer",
		},
		{
			Key:        "hls_buffer.max_playlist_bytes",
			Label:      "Max Playlist Bytes",
			Help:       "Maximum playlist response size accepted from the origin.",
			Kind:       adapters.KindInt,
			Default:    1048576,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "HLS Buffer",
		},
		{
			Key:        "hls_buffer.max_segment_bytes",
			Label:      "Max Segment Bytes",
			Help:       "Maximum segment response size accepted from the origin.",
			Kind:       adapters.KindInt,
			Default:    52428800,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "HLS Buffer",
		},
		{
			Key:        "hls_buffer.segment_timeout_seconds",
			Label:      "Segment Timeout Seconds",
			Help:       "HTTP timeout for segment downloads.",
			Kind:       adapters.KindInt,
			Default:    10,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "HLS Buffer",
		},
		{
			Key:        "hls_buffer.playlist_timeout_seconds",
			Label:      "Playlist Timeout Seconds",
			Help:       "HTTP timeout for playlist refreshes.",
			Kind:       adapters.KindInt,
			Default:    10,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "HLS Buffer",
		},
		{
			Key:        "hls_buffer.max_variant_height",
			Label:      "Max Variant Height",
			Help:       "Highest master-playlist variant height eligible for buffering.",
			Kind:       adapters.KindInt,
			Default:    720,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "HLS Buffer",
		},
		{
			Key:        "hls_buffer.stale_cache_reap_hours",
			Label:      "Stale Cache Reap Hours",
			Help:       "Age after which abandoned HLS cache directories are removed on startup.",
			Kind:       adapters.KindInt,
			Default:    24,
			ApplyScope: adapters.ScopeRestartCast,
			Section:    "HLS Buffer",
		},

		// ---- Launch ----
		// Spec §6.2 / §8.1: Launch is a normal section rendered via a
		// single KindAction field. SectionOrder=60 places it after the
		// implicit-order sections (Network, Video, Audio, Server,
		// MiSTer Control, all SectionOrder=0). The button POSTs to
		// /ui/bridge/mister/launch — that handler is unchanged and
		// already registered in server.go.
		//
		// ApplyScope is intentionally omitted: per spec §8.1 it is
		// not consulted for KindAction. Other consumers that walk
		// bridgeFields() filter on Kind == KindAction first
		// (see hotSwapDiffKeys in bridge.go), so the zero value is safe.
		{
			Key:          "mister/launch",
			Label:        "Launch GroovyMiSTer",
			Help:         "Sends `load_core /media/fat/_Utility/Groovy_20240928.rbf` to /dev/MiSTer_cmd over SSH using the credentials in the MiSTer Control section.",
			Kind:         adapters.KindAction,
			Section:      "Launch",
			SectionOrder: 60,
		},
	}
}
