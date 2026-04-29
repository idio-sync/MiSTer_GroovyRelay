package ui

import (
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
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
