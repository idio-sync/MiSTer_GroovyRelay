package ui

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

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
			Help:       "Video mode. v1 supports NTSC_480i only.",
			Kind:       adapters.KindEnum,
			Enum:       []string{"NTSC_480i"},
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
			Default:    "tff",
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
			Help:       "Where plex.json and other persistent state live.",
			Kind:       adapters.KindText,
			Default:    "/config",
			ApplyScope: adapters.ScopeRestartBridge,
			Section:    "Server",
		},
	}
}
