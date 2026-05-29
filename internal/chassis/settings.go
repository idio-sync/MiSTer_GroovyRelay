// Package chassis settings.go defines the chassis-owned interfaces and
// handlers for Phase 4A: the settings drawer + Network pane.
//
// internal/chassis intentionally does NOT import internal/uiserver. The
// production *uiserver.BridgeSaver satisfies BridgeSettingsSaver from
// outside; the typed settingsError wrapper that uiserver returns
// satisfies the settingsChipError interface structurally, matched via
// errors.As against the interface (Go 1.21+).
package chassis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/launchcore"
)

// BridgeSettingsSaver is the narrow chassis-side interface for bridge
// settings persistence and snapshot. Production passes
// *uiserver.BridgeSaver, but internal/chassis does not import
// internal/uiserver — the wiring lives in cmd/mister-groovy-relay.
type BridgeSettingsSaver interface {
	// Current returns the live in-memory bridge config snapshot. The
	// chassis settings drawer uses this for first render.
	Current() config.BridgeConfig

	// Save persists a fully-composed patch to disk and dispatches in-memory
	// side effects. The returned ApplyScope is the max-wins scope across all
	// changed fields; the chassis maps it via scopeLabel before emitting to
	// the wire.
	//
	// The chassis settings handler prefers SaveTouched to avoid the
	// read-Current-then-Save TOCTOU window; Save remains on the interface
	// for callers that already hold a full patch.
	Save(config.BridgeConfig) (adapters.ApplyScope, error)

	// SaveTouched applies the given mutation against the latest in-memory
	// bridge snapshot under the saver's lock, then persists. This closes
	// the TOCTOU window between Current() and Save() for fast multi-field
	// auto-saves: parallel writers each see and write the most recent
	// snapshot. The apply closure runs under the saver's lock and MUST NOT
	// call back into any BridgeSettingsSaver method.
	SaveTouched(apply func(*config.BridgeConfig)) (adapters.ApplyScope, error)
}

// Prober is the narrow chassis-side interface the probe-mister action
// uses. Production passes a thin wrapper around the existing
// bridgeMisterProber in cmd/mister-groovy-relay/launcher.go, which uses
// CMD_GET_STATUS over an ephemeral source port (NOT the live sender's
// bound source port).
type Prober interface {
	ProbeMister(ctx context.Context, bridge config.BridgeConfig) (ProbeResult, error)
}

// ProbeResult is the structured success payload from a probe attempt.
// LatencyMs is the wall-clock round-trip in milliseconds (e.g. 4.2).
type ProbeResult struct {
	LatencyMs float64
	Host      string
	Port      int
}

// CoreLauncher is the chassis-side interface the launch-core action
// invokes. Production passes the bridgeMisterLauncher from
// cmd/mister-groovy-relay, which wraps internal/misterctl.LaunchGroovy
// with credentials snapshotted from BridgeSaver.Current() on each call.
// internal/chassis does NOT import internal/misterctl — the forbidden
// imports test enforces this (see import_check_test.go).
type CoreLauncher interface {
	// Launch dials the configured MiSTer over SSH and runs the canonical
	// load_core command. The chassis handler wraps the call in a 6s
	// timeout matching the legacy /ui/* path. Implementations must
	// snapshot host/credentials at call time (not at construction) so
	// HOT-scope SSH credential edits apply without a restart.
	Launch(ctx context.Context) error
}

// settingsChipError is matched structurally so saver-layer typed errors
// can carry HTTP/chip details across the interface boundary without a
// uiserver import. The chassis handler uses errors.As against the
// interface (Go 1.21+).
type settingsChipError interface {
	error
	StatusCode() int
	Chip() string
}

// CatalogSettingsManager is the chassis-side interface for Catalog-pane
// state mutation. Production passes a thin wrapper around *streams.Adapter
// from cmd/mister-groovy-relay; internal/chassis does NOT import
// internal/adapters/streams.
type CatalogSettingsManager interface {
	// Providers returns the renderable Catalog-pane state. Stable order
	// matches StreamsCatalogViewer.Catalog() so the two surfaces agree
	// on ID/order. Safe to call before adapter Start.
	Providers() []CatalogProviderState

	// UpdateProvider applies the patch's non-nil flags to providers.<id>
	// in a single snapshot/save/apply cycle. Either pointer may be nil
	// (means "do not change that field"); both nil is rejected by the
	// chassis handler before invoking the interface. Returns the
	// aggregated ApplyScope (with the Catalog-side declared-scope floor
	// already applied by the production wrapper).
	UpdateProvider(id string, patch CatalogProviderPatch) (adapters.ApplyScope, error)

	// SetDirectStreamHLSBuffer flips providers.<id>.hls_buffer_disabled
	// for every provider where Live == true in one save. Returns the
	// max-wins scope (RECAST after the declared-scope floor).
	SetDirectStreamHLSBuffer(disabled bool) (adapters.ApplyScope, error)
}

// CatalogProviderPatch is the optional-field patch consumed by
// UpdateProvider. Pointer-to-bool encodes the tri-state {unset, true,
// false} the chassis handler needs to distinguish "this form key was
// omitted" from "this form key was set to false."
type CatalogProviderPatch struct {
	Enabled           *bool
	HLSBufferDisabled *bool
}

// CatalogProviderState is the chassis-shaped per-provider state for
// rendering and mutation. All fields are populated by the production
// wrapper from streams.Config + adapters.CatalogProvider; the chassis
// renders directly from this struct.
type CatalogProviderState struct {
	ID                string
	DisplayName       string
	BadgeLabel        string
	BadgeClass        string
	Origin            string
	Kind              string
	DefaultChannel    string
	Live              bool
	ChannelCount      int
	Enabled           bool
	HLSBufferDisabled bool
}

// ConfigReset is the chassis-side interface for the restore-defaults
// action. Production passes a wrapper that calls config.WriteAtomic
// with the bundled defaults TOML, preserving the operator's data_dir.
// Scope is REBOOT (live process continues with old config; restart
// applies defaults).
type ConfigReset interface {
	// ResetToDefaults atomically rewrites the on-disk config.toml with
	// the bundled defaults (data_dir preserved from the live config).
	// MUST NOT touch data_dir contents, MUST NOT mutate in-memory
	// bridge/adapter state. Disk-write failures return a typed error
	// satisfying settingsChipError so the chassis can map to
	// {chip:"WRITE FAILED"} cleanly.
	ResetToDefaults() error
}

// bridgeFieldDecoder is the per-field validation entry. It takes a raw
// form string (already trimmed by the caller) and returns the decoded
// typed value (as an any so the overlay table can write it into the
// right BridgeConfig field) or a human-readable error.
type bridgeFieldDecoder func(raw string) (any, error)

var bridgeFieldDecoders = map[string]bridgeFieldDecoder{
	"mister_host": func(s string) (any, error) {
		v, err := decodeMisterHost(s)
		return v, err
	},
	"mister_port": func(s string) (any, error) {
		v, err := decodePort(s)
		return v, err
	},
	"mister_source_port": func(s string) (any, error) {
		v, err := decodePort(s)
		return v, err
	},
	"ui_http_port": func(s string) (any, error) {
		v, err := decodePort(s)
		return v, err
	},
	"host_ip": func(s string) (any, error) {
		v, err := decodeOptionalIPv4(s)
		return v, err
	},
	"data_dir": func(s string) (any, error) {
		v, err := decodeOptionalAbsPath(s)
		return v, err
	},
	"ffmpeg_path": func(s string) (any, error) {
		v, err := decodeOptionalExecutablePath(s)
		return v, err
	},
	"ffprobe_path": func(s string) (any, error) {
		v, err := decodeOptionalExecutablePath(s)
		return v, err
	},
	"ytdlp_path": func(s string) (any, error) {
		v, err := decodeOptionalExecutablePath(s)
		return v, err
	},
	"video_modeline": func(s string) (any, error) {
		v, err := decodeVideoModeline(s)
		return v, err
	},
	"video_interlace_field_order": func(s string) (any, error) {
		v, err := decodeInterlaceFieldOrder(s)
		return v, err
	},
	"video_aspect_mode": func(s string) (any, error) {
		v, err := decodeAspectMode(s)
		return v, err
	},
	"video_lz4_enabled": func(s string) (any, error) {
		v, err := decodeBool(s)
		return v, err
	},
	"video_delta_lz4_enabled": func(s string) (any, error) {
		v, err := decodeBool(s)
		return v, err
	},
	"audio_sample_rate": func(s string) (any, error) {
		v, err := decodeAudioSampleRate(s)
		return v, err
	},
	"audio_channels": func(s string) (any, error) {
		v, err := decodeAudioChannels(s)
		return v, err
	},
	"mister_ssh_user": func(s string) (any, error) {
		v, err := decodeMisterSSHUser(s)
		return v, err
	},
	"mister_ssh_password": func(s string) (any, error) {
		v, err := decodeMisterSSHPassword(s)
		return v, err
	},
	"hls_enabled": func(s string) (any, error) {
		v, err := decodeBool(s)
		return v, err
	},
	"hls_live_edge_segments": func(s string) (any, error) {
		v, err := decodeIntInRange(s, 1, 12)
		return v, err
	},
	"hls_start_segments": func(s string) (any, error) {
		v, err := decodeIntInRange(s, 1, 6)
		return v, err
	},
	"hls_max_cached_segments": func(s string) (any, error) {
		v, err := decodeIntInRange(s, 2, 24)
		return v, err
	},
	"hls_max_cache_bytes": func(s string) (any, error) {
		v, err := decodeInt64InRange(s, 16777216, 2147483648)
		return v, err
	},
	"hls_max_playlist_bytes": func(s string) (any, error) {
		v, err := decodeInt64InRange(s, 4096, 8388608)
		return v, err
	},
	"hls_max_segment_bytes": func(s string) (any, error) {
		v, err := decodeInt64InRange(s, 1048576, 536870912)
		return v, err
	},
	"hls_segment_timeout_seconds": func(s string) (any, error) {
		v, err := decodeIntInRange(s, 1, 60)
		return v, err
	},
	"hls_playlist_timeout_seconds": func(s string) (any, error) {
		v, err := decodeIntInRange(s, 1, 60)
		return v, err
	},
	"hls_max_variant_height": func(s string) (any, error) {
		v, err := decodeIntInRange(s, 240, 2160)
		return v, err
	},
	"hls_stale_cache_reap_hours": func(s string) (any, error) {
		v, err := decodeIntInRange(s, 1, 168)
		return v, err
	},
	"logging_debug": func(s string) (any, error) {
		v, err := decodeBool(s)
		return v, err
	},
}

// decodeMisterHost trims whitespace and accepts a non-empty IPv4 string
// or RFC-952 hostname. Empty -> "is required". Otherwise -> "not a
// valid IPv4 or hostname".
func decodeMisterHost(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("is required")
	}
	if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
		return s, nil
	}
	if isValidHostname(s) {
		return s, nil
	}
	return "", fmt.Errorf("not a valid IPv4 or hostname")
}

// decodePort accepts a numeric string in [1, 65535]. Empty or non-numeric
// -> "must be a whole number". Out of range -> "port out of range (1-65535)".
func decodePort(raw string) (int, error) {
	s := strings.TrimSpace(raw)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("must be a whole number")
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port out of range (1-65535)")
	}
	return n, nil
}

// decodeOptionalIPv4 returns "" for empty input (clears the field), or a
// valid IPv4 string. Anything else -> "not a valid IPv4 address".
func decodeOptionalIPv4(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("not a valid IPv4 address")
	}
	return s, nil
}

// decodeOptionalAbsPath returns "" for empty input, or an absolute path.
// Relative -> "must be an absolute path". No existence check.
func decodeOptionalAbsPath(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if !filepath.IsAbs(s) {
		return "", fmt.Errorf("must be an absolute path")
	}
	return s, nil
}

// decodeOptionalExecutablePath returns "" for empty input, or an absolute
// path to a usable executable file. This mirrors config.Sectioned.Validate's
// external-tool checks so the drawer can show inline field errors instead
// of a whole-form BAD INPUT chip.
func decodeOptionalExecutablePath(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if !filepath.IsAbs(s) {
		return "", fmt.Errorf("must be an absolute path")
	}
	info, err := os.Stat(s)
	if err != nil {
		return "", fmt.Errorf("not usable: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("not usable: is a directory")
	}
	if runtime.GOOS == "windows" {
		if !strings.EqualFold(filepath.Ext(s), ".exe") {
			return "", fmt.Errorf("not usable: does not have .exe extension")
		}
		return s, nil
	}
	if info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("not usable: not executable")
	}
	return s, nil
}

// decodeVideoModeline accepts one of the four supported modelines.
func decodeVideoModeline(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	switch s {
	case "NTSC_480i", "NTSC_240p", "PAL_576i", "PAL_288p":
		return s, nil
	}
	return "", fmt.Errorf("must be one of NTSC_480i, NTSC_240p, PAL_576i, PAL_288p")
}

// decodeInterlaceFieldOrder accepts "tff" or "bff" (case-sensitive,
// matching config.Sectioned.Validate).
func decodeInterlaceFieldOrder(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "tff" || s == "bff" {
		return s, nil
	}
	return "", fmt.Errorf("must be tff or bff")
}

// decodeAspectMode accepts "auto", "letterbox", or "zoom".
func decodeAspectMode(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	switch s {
	case "auto", "letterbox", "zoom":
		return s, nil
	}
	return "", fmt.Errorf("must be auto, letterbox, or zoom")
}

// decodeBool accepts exactly "true" or "false". Used by switch fields.
// Strict matching catches form-data drift early; the legacy strconv.ParseBool
// would accept "0"/"1"/"TRUE" which the chassis JS contract does not emit.
func decodeBool(raw string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("must be true or false")
}

// decodeAudioSampleRate accepts 22050, 44100, or 48000 as a numeric string.
func decodeAudioSampleRate(raw string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err == nil {
		switch n {
		case 22050, 44100, 48000:
			return n, nil
		}
	}
	return 0, fmt.Errorf("must be 22050, 44100, or 48000")
}

// decodeAudioChannels accepts 1 or 2 as a numeric string.
func decodeAudioChannels(raw string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err == nil {
		switch n {
		case 1, 2:
			return n, nil
		}
	}
	return 0, fmt.Errorf("must be 1 or 2")
}

// bridgeFieldOverlay writes the decoded value into the right path of a
// BridgeConfig. Type asserts to the decoder's return type; a type
// mismatch is a programmer bug and panics rather than silently failing.
type bridgeFieldOverlay func(cfg *config.BridgeConfig, value any)

var bridgeFieldOverlays = map[string]bridgeFieldOverlay{
	"mister_host":        func(c *config.BridgeConfig, v any) { c.MiSTer.Host = v.(string) },
	"mister_port":        func(c *config.BridgeConfig, v any) { c.MiSTer.Port = v.(int) },
	"mister_source_port": func(c *config.BridgeConfig, v any) { c.MiSTer.SourcePort = v.(int) },
	"ui_http_port":       func(c *config.BridgeConfig, v any) { c.UI.HTTPPort = v.(int) },
	"host_ip":            func(c *config.BridgeConfig, v any) { c.HostIP = v.(string) },
	"data_dir":           func(c *config.BridgeConfig, v any) { c.DataDir = v.(string) },
	"ffmpeg_path":        func(c *config.BridgeConfig, v any) { c.FFmpegPath = v.(string) },
	"ffprobe_path":       func(c *config.BridgeConfig, v any) { c.FFprobePath = v.(string) },
	"ytdlp_path":         func(c *config.BridgeConfig, v any) { c.YTDLPPath = v.(string) },
	"video_modeline":              func(c *config.BridgeConfig, v any) { c.Video.Modeline = v.(string) },
	"video_interlace_field_order": func(c *config.BridgeConfig, v any) { c.Video.InterlaceFieldOrder = v.(string) },
	"video_aspect_mode":           func(c *config.BridgeConfig, v any) { c.Video.AspectMode = v.(string) },
	"video_lz4_enabled":           func(c *config.BridgeConfig, v any) { c.Video.LZ4Enabled = v.(bool) },
	"video_delta_lz4_enabled":     func(c *config.BridgeConfig, v any) { c.Video.DeltaLZ4Enabled = v.(bool) },
	"audio_sample_rate": func(c *config.BridgeConfig, v any) { c.Audio.SampleRate = v.(int) },
	"audio_channels":    func(c *config.BridgeConfig, v any) { c.Audio.Channels = v.(int) },
	"mister_ssh_user":   func(c *config.BridgeConfig, v any) { c.MiSTer.SSHUser = v.(string) },
	"mister_ssh_password": func(c *config.BridgeConfig, v any) {
		s, _ := v.(string)
		if s == "" {
			return // preserve stored password — see Phase 4B spec, SSH password autosave skip
		}
		c.MiSTer.SSHPassword = s
	},
	"hls_enabled":                  func(c *config.BridgeConfig, v any) { c.HLSBuffer.Enabled = v.(bool) },
	"hls_live_edge_segments":       func(c *config.BridgeConfig, v any) { c.HLSBuffer.LiveEdgeSegments = v.(int) },
	"hls_start_segments":           func(c *config.BridgeConfig, v any) { c.HLSBuffer.StartSegments = v.(int) },
	"hls_max_cached_segments":      func(c *config.BridgeConfig, v any) { c.HLSBuffer.MaxCachedSegments = v.(int) },
	"hls_max_cache_bytes":          func(c *config.BridgeConfig, v any) { c.HLSBuffer.MaxCacheBytes = v.(int64) },
	"hls_max_playlist_bytes":       func(c *config.BridgeConfig, v any) { c.HLSBuffer.MaxPlaylistBytes = v.(int64) },
	"hls_max_segment_bytes":        func(c *config.BridgeConfig, v any) { c.HLSBuffer.MaxSegmentBytes = v.(int64) },
	"hls_segment_timeout_seconds":  func(c *config.BridgeConfig, v any) { c.HLSBuffer.SegmentTimeoutSeconds = v.(int) },
	"hls_playlist_timeout_seconds": func(c *config.BridgeConfig, v any) { c.HLSBuffer.PlaylistTimeoutSeconds = v.(int) },
	"hls_max_variant_height":       func(c *config.BridgeConfig, v any) { c.HLSBuffer.MaxVariantHeight = v.(int) },
	"hls_stale_cache_reap_hours":   func(c *config.BridgeConfig, v any) { c.HLSBuffer.StaleCacheReapHours = v.(int) },
	"logging_debug": func(c *config.BridgeConfig, v any) { c.Logging.Debug = v.(bool) },
}

// bridgeFieldScopes is the chassis-side mirror of which ApplyScope each
// field dispatches to. It is validated at startup (see the cross-table
// tests in settings_test.go) against the decoder + overlay tables, and
// against the existing /ui/* saver's per-field dispatch logic.
//
// The HTTP handler's "max-wins scope" output uses the ApplyScope value
// returned by BridgeSaver.Save() (which is authoritative). This table
// is used only by the chassis when it wants to know a single field's
// scope ahead of time (e.g., for badge rendering — but Network pane
// scopes are already encoded directly in the template).
var bridgeFieldScopes = map[string]adapters.ApplyScope{
	"mister_host":        adapters.ScopeRestartBridge,
	"mister_port":        adapters.ScopeRestartBridge,
	"mister_source_port": adapters.ScopeRestartBridge,
	"ui_http_port":       adapters.ScopeRestartBridge,
	"host_ip":            adapters.ScopeRestartBridge,
	"data_dir":           adapters.ScopeRestartBridge,
	"ffmpeg_path":        adapters.ScopeHotSwap,
	"ffprobe_path":       adapters.ScopeHotSwap,
	"ytdlp_path":         adapters.ScopeHotSwap,
	"video_modeline":              adapters.ScopeRestartCast,
	"video_interlace_field_order": adapters.ScopeHotSwap,
	"video_aspect_mode":           adapters.ScopeRestartCast,
	"video_lz4_enabled":           adapters.ScopeRestartCast,
	"video_delta_lz4_enabled":     adapters.ScopeRestartCast,
	"audio_sample_rate":    adapters.ScopeRestartCast,
	"audio_channels":       adapters.ScopeRestartCast,
	"mister_ssh_user":      adapters.ScopeHotSwap,
	"mister_ssh_password":  adapters.ScopeHotSwap,
	"hls_enabled":                  adapters.ScopeRestartCast,
	"hls_live_edge_segments":       adapters.ScopeRestartCast,
	"hls_start_segments":           adapters.ScopeRestartCast,
	"hls_max_cached_segments":      adapters.ScopeRestartCast,
	"hls_max_cache_bytes":          adapters.ScopeRestartCast,
	"hls_max_playlist_bytes":       adapters.ScopeRestartCast,
	"hls_max_segment_bytes":        adapters.ScopeRestartCast,
	"hls_segment_timeout_seconds":  adapters.ScopeRestartCast,
	"hls_playlist_timeout_seconds": adapters.ScopeRestartCast,
	"hls_max_variant_height":       adapters.ScopeRestartCast,
	"hls_stale_cache_reap_hours":   adapters.ScopeRestartCast,
	"logging_debug": adapters.ScopeHotSwap,
}

// scopeLabel maps an ApplyScope to the chassis JSON wire label. Returns
// (_, false) for unknown scopes; the chassis handler treats unknown as
// a server bug and responds 500 WRITE FAILED. Do NOT use
// ApplyScope.String() directly: it returns "hot-swap" / "next-cast" /
// "restart-cast" / "restart-bridge", which is the wrong wire shape.
func scopeLabel(s adapters.ApplyScope) (string, bool) {
	switch s {
	case adapters.ScopeHotSwap:
		return "hot", true
	case adapters.ScopeNextCast:
		return "next", true
	case adapters.ScopeRestartCast:
		return "recast", true
	case adapters.ScopeRestartBridge:
		return "reboot", true
	default:
		return "", false
	}
}

// isValidHostname is a permissive RFC-952/1123-ish check: 1..253 chars
// total, label chars in [a-z0-9-], labels non-empty, no leading/trailing
// hyphen, dot-separated.
func isValidHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			ok := (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') ||
				r == '-'
			if !ok {
				return false
			}
		}
	}
	return true
}

// decodeMisterSSHUser trims whitespace and rejects empty / SSH-illegal
// characters (colon, NUL, whitespace including newlines). The conservative
// character set catches typos before they confuse the SSH layer.
func decodeMisterSSHUser(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("is required")
	}
	for _, r := range s {
		switch {
		case r == ':' || r == 0 || r == '\n' || r == '\r' || r == ' ' || r == '\t':
			return "", fmt.Errorf("contains an illegal character")
		}
	}
	return s, nil
}

// decodeMisterSSHPassword returns the raw input verbatim (NOT trimmed —
// trailing whitespace may be intentional). Empty is allowed at decoder
// level; the overlay applies preserve-on-empty semantics so empty submits
// don't clobber the stored password.
func decodeMisterSSHPassword(raw string) (string, error) {
	return raw, nil
}

// decodeIntInRange parses an int from raw and asserts lo <= n <= hi.
// Used by HLS-segment-count fields and other int-typed bounded numerics.
func decodeIntInRange(raw string, lo, hi int) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("must be a whole number")
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("must be in [%d, %d]", lo, hi)
	}
	return n, nil
}

// decodeInt64InRange parses an int64 from raw and asserts lo <= n <= hi.
// Used by HLS byte-ceiling fields (max_cache_bytes etc.). The error message
// renders the bounds via humanizeBytes for operator readability — e.g.
// "must be in [16 MB, 2 GB]" rather than "[16777216, 2147483648]".
func decodeInt64InRange(raw string, lo, hi int64) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("must be a whole number")
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("must be in [%s, %s]", humanizeBytes(lo), humanizeBytes(hi))
	}
	return n, nil
}

// handleSettingsBridgePost is the POST handler for /receiver/settings/bridge.
// Accepts any subset of the supported form fields; missing keys mean "do
// not change that field." See the spec's Wire Contract for the response
// envelope.
func (s *Server) handleSettingsBridgePost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.BridgeSaver == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	touched := map[string]string{}
	for name := range bridgeFieldDecoders {
		if vals, ok := r.PostForm[name]; ok && len(vals) > 0 {
			touched[name] = vals[0]
		}
	}
	if len(touched) == 0 {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}

	// Decode all touched fields; collect all errors.
	decoded := map[string]any{}
	errs := map[string]string{}
	for name, raw := range touched {
		dec := bridgeFieldDecoders[name]
		v, err := dec(raw)
		if err != nil {
			errs[name] = err.Error()
			continue
		}
		decoded[name] = v
	}
	if len(errs) > 0 {
		writeSettingsFieldErrors(w, http.StatusBadRequest, errs)
		return
	}

	// Apply touched-field overlays under the saver's lock via SaveTouched
	// to close the TOCTOU window between read and write — two parallel
	// auto-saves on different fields would otherwise each snapshot the
	// same pre-mutation Bridge and the second writer would clobber the
	// first's field.
	scope, err := s.cfg.BridgeSaver.SaveTouched(func(patch *config.BridgeConfig) {
		for name, value := range decoded {
			bridgeFieldOverlays[name](patch, value)
		}
	})
	if err != nil {
		var ce settingsChipError
		if errors.As(err, &ce) {
			writeSettingsChip(w, ce.StatusCode(), ce.Chip())
			return
		}
		writeSettingsChip(w, http.StatusInternalServerError, "WRITE FAILED")
		return
	}

	label, ok := scopeLabel(scope)
	if !ok {
		writeSettingsChip(w, http.StatusInternalServerError, "WRITE FAILED")
		return
	}
	writeSettingsSuccess(w, label)
}

// writeSettingsSuccess emits {"ok":true,"scope":"<label>"} with status 200.
func writeSettingsSuccess(w http.ResponseWriter, scope string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "scope": scope})
}

// writeSettingsChip emits {"ok":false,"chip":"<chip>"} with the given status.
func writeSettingsChip(w http.ResponseWriter, status int, chip string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "chip": chip})
}

// writeSettingsFieldErrors emits {"ok":false,"errors":{...}} with the given status.
func writeSettingsFieldErrors(w http.ResponseWriter, status int, errs map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "errors": errs})
}

// handleSettingsActionProbeMister is the POST handler for
// /receiver/settings/action/probe-mister. Hard 1s server-side timeout;
// uses the currently-saved BridgeConfig (NOT form values). Returns 200
// for both success and timeout (the probe ran cleanly in both cases);
// 500 for socket/transport failures; 503 if dependencies aren't wired.
func (s *Server) handleSettingsActionProbeMister(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Prober == nil || s.cfg.BridgeSaver == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	bridge := s.cfg.BridgeSaver.Current()
	ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
	defer cancel()

	start := time.Now()
	res, err := s.cfg.Prober.ProbeMister(ctx, bridge)
	elapsed := time.Since(start)

	w.Header().Set("Content-Type", "application/json")
	if err == nil {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"latency_ms": res.LatencyMs,
			"host":       res.Host,
			"port":       res.Port,
		})
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         false,
			"error":      "timeout",
			"elapsed_ms": float64(elapsed) / float64(time.Millisecond),
		})
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":    false,
		"error": sanitizeProbeError(err),
	})
}

// handleSettingsActionLaunchCore is the POST handler for
// /receiver/settings/action/launch-core. It SSH-sends the canonical
// load_core command to the MiSTer using the saved credentials.
//
// Response policy:
//   - 503 NOT READY when CoreLauncher or BridgeSaver is unwired.
//   - 400 "MiSTer host not configured ..." when the saved host is empty.
//     The error string matches bridgeMisterLauncher.Launch's empty-host
//     short-circuit verbatim. Task 21 extracts this into a shared
//     internal/launchcore constant so chassis and cmd cannot drift.
//   - 200 {ok:true, host:"..."} on success.
//   - 500 {ok:false, error:"<redacted>"} on launcher failure; reuses 4A's
//     sanitizeProbeError to redact IPv4:port tokens.
//
// Context timeout: 6s (matches the legacy /ui/* timeout budget — 5s SSH
// dial + 1s slack).
func (s *Server) handleSettingsActionLaunchCore(w http.ResponseWriter, r *http.Request) {
	if s.cfg.CoreLauncher == nil || s.cfg.BridgeSaver == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	cur := s.cfg.BridgeSaver.Current()
	if cur.MiSTer.Host == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": launchcore.EmptyHostMessage,
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	if err := s.cfg.CoreLauncher.Launch(ctx); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": sanitizeProbeError(err),
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":   true,
		"host": cur.MiSTer.Host,
	})
}

// probeErrorHostPortRe matches dotted-quad IPv4 with an optional :port
// suffix, e.g. "192.168.1.42" or "10.0.0.1:32100". The chassis only
// validates IPv4 host_ip / mister_host shapes that the regex can catch;
// hostnames pass through unchanged because the prober's host text mirrors
// the operator's own bridge.mister.host value (no information leak beyond
// what they typed). Pre-compiled at package init to avoid re-parsing per
// probe.
var probeErrorHostPortRe = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(?::\d{1,5})?\b`)

// sanitizeProbeError redacts dotted-quad IPv4[:port] tokens from probe
// error messages and caps the length so a long upstream socket error
// doesn't blow up the JSON payload. The prober's output is already
// constrained from cmd/, so the regex covers the realistic leak shapes
// (e.g. "read udp 127.0.0.1:54321->192.168.1.42:32100: connection refused"
// → "read udp <host>-><host>: connection refused").
func sanitizeProbeError(err error) string {
	msg := probeErrorHostPortRe.ReplaceAllString(err.Error(), "<host>")
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}
