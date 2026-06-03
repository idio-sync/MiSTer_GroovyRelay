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
	"net/url"
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

// AdapterSettingsSaver is the chassis-side mirror of BridgeSettingsSaver
// for adapter-section writes. Production binding wraps
// *uiserver.AdapterSaver + the adapter registry; the chassis does not
// import internal/uiserver or any concrete adapter package.
type AdapterSettingsSaver interface {
	// Current returns the adapter's current in-memory values, keyed by
	// FieldDef.Key. Returns (nil, false) for unknown adapter names.
	Current(name string) (map[string]any, bool)

	// Fields returns the 4D writable FieldDef surface. DLNA/Torrent return
	// their full FieldDef table; Streams returns top-level fields plus a
	// wildcard providers.*.catalog_refresh_hours allowlist entry. Template
	// rendering skips provider wildcard rows and renders provider overrides
	// from AdapterPaneData. Returns (nil, false) for unknown adapter names.
	Fields(name string) ([]adapters.FieldDef, bool)

	// SaveTouched applies the touched-keys subset to the adapter's
	// [adapters.<name>] TOML section, validates, writes atomically,
	// and dispatches the runtime apply. Returns the wire scope label
	// ("hot" / "next" / "recast" / "reboot") and a typed error
	// implementing settingsChipError on failure. Mirror of
	// BridgeSettingsSaver.SaveTouched.
	SaveTouched(name string, touched map[string]string) (string, error)
}

// StreamsRefresher is the chassis-side interface backing the
// /ui/settings/action/streams-refresh action. Production binding
// wraps *streams.Adapter.RefreshNow(ctx, "") — the canonical manifest-
// refresh entry point. The chassis does not import internal/adapters/streams.
type StreamsRefresher interface {
	// RefreshNow fetches the streams manifest (and ripples to provider
	// catalogs as a side effect). The returned result carries the
	// source label ("remote" / "cache") and a non-nil Err if the
	// refresh failed. The chassis handler wraps the call in a 30s
	// context.
	RefreshNow(ctx context.Context) (StreamsRefreshResult, error)
}

// StreamsRefreshResult is the scalar status returned by RefreshNow.
type StreamsRefreshResult struct {
	Source     string
	DurationMS int64
	Err        error
}

// AdapterLinker is the chassis-side interface backing the per-adapter
// /ui/settings/adapter/{name}/link/* routes. The production binding
// (cmd/mister-groovy-relay) wraps each adapter's adapters.LinkController
// and maps its LinkSnapshot onto LinkView. The chassis imports no adapter
// package.
type AdapterLinker interface {
	// LinkView returns the current render state for an adapter's Account
	// sub-section, or ok=false if the named adapter is not linkable.
	LinkView(name string) (LinkView, bool)
	// StartLink begins pairing. PIN adapters ignore params; credential
	// adapters read params["username"]/["password"].
	StartLink(ctx context.Context, name string, params map[string]string) (LinkView, error)
	// LinkStatus polls progress (PIN adapters); credential adapters return
	// the current view.
	LinkStatus(ctx context.Context, name string) (LinkView, error)
	// Unlink revokes/logs out and clears the token. Idempotent.
	Unlink(ctx context.Context, name string) (LinkView, error)
}

// LinkView is the JSON wire/render shape for the Account sub-section.
type LinkView struct {
	Kind           string      `json:"kind"`  // "pin" | "credential"
	Phase          string      `json:"phase"` // "unlinked"|"pending"|"linked"|"error"
	LinkedAs       string      `json:"linkedAs,omitempty"`
	Code           string      `json:"code,omitempty"`
	ExpiresInSec   int         `json:"expiresInSec,omitempty"`
	NeedsServerURL bool        `json:"needsServerURL,omitempty"`
	Error          string      `json:"error,omitempty"`
	Fields         []LinkField `json:"fields,omitempty"`
}

// LinkField is one credential input a credential adapter wants rendered.
type LinkField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // "text" | "secret"
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
	ID                  string
	DisplayName         string
	BadgeLabel          string
	BadgeClass          string
	Origin              string
	Kind                string
	DefaultChannel      string
	Live                bool
	ChannelCount        int
	Enabled             bool
	HLSBufferDisabled   bool
	CatalogRefreshHours int // 4D — per-provider override; 0 = inherit the streams-global catalog_refresh_hours
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
	"mister_host":                 func(c *config.BridgeConfig, v any) { c.MiSTer.Host = v.(string) },
	"mister_port":                 func(c *config.BridgeConfig, v any) { c.MiSTer.Port = v.(int) },
	"mister_source_port":          func(c *config.BridgeConfig, v any) { c.MiSTer.SourcePort = v.(int) },
	"ui_http_port":                func(c *config.BridgeConfig, v any) { c.UI.HTTPPort = v.(int) },
	"host_ip":                     func(c *config.BridgeConfig, v any) { c.HostIP = v.(string) },
	"data_dir":                    func(c *config.BridgeConfig, v any) { c.DataDir = v.(string) },
	"ffmpeg_path":                 func(c *config.BridgeConfig, v any) { c.FFmpegPath = v.(string) },
	"ffprobe_path":                func(c *config.BridgeConfig, v any) { c.FFprobePath = v.(string) },
	"ytdlp_path":                  func(c *config.BridgeConfig, v any) { c.YTDLPPath = v.(string) },
	"video_modeline":              func(c *config.BridgeConfig, v any) { c.Video.Modeline = v.(string) },
	"video_interlace_field_order": func(c *config.BridgeConfig, v any) { c.Video.InterlaceFieldOrder = v.(string) },
	"video_aspect_mode":           func(c *config.BridgeConfig, v any) { c.Video.AspectMode = v.(string) },
	"video_lz4_enabled":           func(c *config.BridgeConfig, v any) { c.Video.LZ4Enabled = v.(bool) },
	"video_delta_lz4_enabled":     func(c *config.BridgeConfig, v any) { c.Video.DeltaLZ4Enabled = v.(bool) },
	"audio_sample_rate":           func(c *config.BridgeConfig, v any) { c.Audio.SampleRate = v.(int) },
	"audio_channels":              func(c *config.BridgeConfig, v any) { c.Audio.Channels = v.(int) },
	"mister_ssh_user":             func(c *config.BridgeConfig, v any) { c.MiSTer.SSHUser = v.(string) },
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
	"logging_debug":                func(c *config.BridgeConfig, v any) { c.Logging.Debug = v.(bool) },
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
	"mister_host":                  adapters.ScopeRestartBridge,
	"mister_port":                  adapters.ScopeRestartBridge,
	"mister_source_port":           adapters.ScopeRestartBridge,
	"ui_http_port":                 adapters.ScopeRestartBridge,
	"host_ip":                      adapters.ScopeRestartBridge,
	"data_dir":                     adapters.ScopeRestartBridge,
	"ffmpeg_path":                  adapters.ScopeHotSwap,
	"ffprobe_path":                 adapters.ScopeHotSwap,
	"ytdlp_path":                   adapters.ScopeHotSwap,
	"video_modeline":               adapters.ScopeRestartCast,
	"video_interlace_field_order":  adapters.ScopeHotSwap,
	"video_aspect_mode":            adapters.ScopeRestartCast,
	"video_lz4_enabled":            adapters.ScopeRestartCast,
	"video_delta_lz4_enabled":      adapters.ScopeRestartCast,
	"audio_sample_rate":            adapters.ScopeRestartCast,
	"audio_channels":               adapters.ScopeRestartCast,
	"mister_ssh_user":              adapters.ScopeHotSwap,
	"mister_ssh_password":          adapters.ScopeHotSwap,
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
	"logging_debug":                adapters.ScopeHotSwap,
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

// WireLabelForScope returns the JSON wire label ("hot"/"next"/"recast"/"reboot")
// for the given ApplyScope. Exported for cross-package use (drift tests and the
// cmd-side adapter-save wrapper) to translate adapters.ApplyScope into the wire
// label.
func WireLabelForScope(s adapters.ApplyScope) (string, bool) { return scopeLabel(s) }

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

// handleSettingsBridgePost is the POST handler for /ui/settings/bridge.
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
// /ui/settings/action/probe-mister. Hard 1s server-side timeout;
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
// /ui/settings/action/launch-core. It SSH-sends the canonical
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

// writeJSON emits any JSON-serialisable body with the given status code.
// Used by handlers that need a custom envelope shape not covered by
// writeSettingsChip / writeSettingsSuccess / writeSettingsFieldErrors.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// handleSettingsCatalogProviderPost is the POST handler for
// /ui/settings/catalog/provider/{id}. Accepts any subset of the
// supported form keys (enabled, hls_buffer_disabled); missing keys mean
// "do not change that field." Returns 404 for an unknown id, 400 for bad
// bools or an empty body, 503 if the manager is unwired.
func (s *Server) handleSettingsCatalogProviderPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.CatalogManager == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	id := r.PathValue("id")
	known := s.cfg.CatalogManager.Providers()
	if !catalogContainsProvider(known, id) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"ok": false, "error": "unknown provider",
		})
		return
	}

	var patch CatalogProviderPatch
	errs := map[string]string{}
	if vals, ok := r.PostForm["enabled"]; ok && len(vals) > 0 {
		v, err := decodeBool(vals[0])
		if err != nil {
			errs["enabled"] = "must be true or false"
		} else {
			patch.Enabled = &v
		}
	}
	if vals, ok := r.PostForm["hls_buffer_disabled"]; ok && len(vals) > 0 {
		v, err := decodeBool(vals[0])
		if err != nil {
			errs["hls_buffer_disabled"] = "must be true or false"
		} else {
			patch.HLSBufferDisabled = &v
		}
	}
	if len(errs) > 0 {
		writeSettingsFieldErrors(w, http.StatusBadRequest, errs)
		return
	}
	if patch.Enabled == nil && patch.HLSBufferDisabled == nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}

	scope, err := s.cfg.CatalogManager.UpdateProvider(id, patch)
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

// catalogContainsProvider reports whether any element of providers has the
// given id. Used by handleSettingsCatalogProviderPost to gate unknown-id
// requests before invoking UpdateProvider.
func catalogContainsProvider(providers []CatalogProviderState, id string) bool {
	for _, p := range providers {
		if p.ID == id {
			return true
		}
	}
	return false
}

// probeErrorHostPortRe matches dotted-quad IPv4 with an optional :port
// suffix, e.g. "192.168.1.42" or "10.0.0.1:32100". The chassis only
// validates IPv4 host_ip / mister_host shapes that the regex can catch;
// hostnames pass through unchanged because the prober's host text mirrors
// the operator's own bridge.mister.host value (no information leak beyond
// what they typed). Pre-compiled at package init to avoid re-parsing per
// probe.
// handleSettingsCatalogDirectStreamHLSBufferPost handles
// POST /ui/settings/catalog/direct-stream-hls-buffer.
// It reads a single form field "disabled" (strict "true"/"false") and
// calls CatalogSettingsManager.SetDirectStreamHLSBuffer, which flips
// hls_buffer_disabled on every Live provider in one atomic save.
func (s *Server) handleSettingsCatalogDirectStreamHLSBufferPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.CatalogManager == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	vals, ok := r.PostForm["disabled"]
	if !ok || len(vals) == 0 {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	v, err := decodeBool(vals[0])
	if err != nil {
		writeSettingsFieldErrors(w, http.StatusBadRequest, map[string]string{
			"disabled": "must be true or false",
		})
		return
	}
	scope, err := s.cfg.CatalogManager.SetDirectStreamHLSBuffer(v)
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

// handleSettingsActionRestoreDefaults is the POST handler for
// /ui/settings/action/restore-defaults. Empty body. Returns
// success with scope:"reboot" so the client toasts the dedicated
// "Defaults restored — restart container to apply" message via the
// 4A REBOOT toast helper.
func (s *Server) handleSettingsActionRestoreDefaults(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ConfigReset == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	if err := s.cfg.ConfigReset.ResetToDefaults(); err != nil {
		var ce settingsChipError
		if errors.As(err, &ce) {
			writeSettingsChip(w, ce.StatusCode(), ce.Chip())
			return
		}
		writeSettingsChip(w, http.StatusInternalServerError, "WRITE FAILED")
		return
	}
	writeSettingsSuccess(w, "reboot")
}

// handleSettingsAdapterPost is the POST handler for
// /ui/settings/adapter/{name}. Validates the adapter name against the
// saver's Fields table, rejects unknown form keys (BAD INPUT chip), then
// delegates to AdapterSettingsSaver.SaveTouched. Error shapes are unwrapped
// via emitSaveError.
func (s *Server) handleSettingsAdapterPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdapterSettingsSaver == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	fields, ok := s.cfg.AdapterSettingsSaver.Fields(name)
	if !ok {
		writeSettingsChip(w, http.StatusNotFound, "UNKNOWN ADAPTER")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	touched, ferrs := touchedFromForm(r.PostForm, fields)
	if len(ferrs) > 0 {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	if !atLeastOneKey(touched) {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	scope, err := s.cfg.AdapterSettingsSaver.SaveTouched(name, touched)
	if err != nil {
		emitSaveError(w, err)
		return
	}
	writeSettingsSuccess(w, scope)
}

// touchedFromForm extracts form keys that match the adapter's FieldDef
// schema. Unknown keys accumulate into ferrs; on any unknown key the
// whole map is rejected (BAD INPUT). Returns (touched, nil) on clean
// input, (nil, ferrs) if any key was unrecognised.
func touchedFromForm(form url.Values, fields []adapters.FieldDef) (map[string]string, map[string]string) {
	touched := map[string]string{}
	ferrs := map[string]string{}
	for key, values := range form {
		if len(values) == 0 {
			continue
		}
		if !keyMatchesSchema(key, fields) {
			ferrs[key] = "unknown field"
			continue
		}
		touched[key] = values[0]
	}
	if len(ferrs) > 0 {
		return nil, ferrs
	}
	return touched, nil
}

// keyMatchesSchema reports whether key matches any FieldDef in fields.
// Exact matches take priority; wildcard patterns (e.g. "providers.*.foo")
// are matched via dottedKeyMatchesPattern.
func keyMatchesSchema(key string, fields []adapters.FieldDef) bool {
	for _, fd := range fields {
		if fd.Key == key {
			return true
		}
		if strings.Contains(fd.Key, "*") && dottedKeyMatchesPattern(key, fd.Key) {
			return true
		}
	}
	return false
}

// dottedKeyMatchesPattern performs segment-wise matching of a dotted key
// against a dotted pattern where "*" matches any single segment.
// "providers.foo.bar" matches "providers.*.bar"; length mismatch is a
// fast-reject. Used for the Streams wildcard allowlist entry
// "providers.*.catalog_refresh_hours".
func dottedKeyMatchesPattern(key, pattern string) bool {
	keyParts := strings.Split(key, ".")
	patParts := strings.Split(pattern, ".")
	if len(keyParts) != len(patParts) {
		return false
	}
	for i, p := range patParts {
		if p == "*" {
			continue
		}
		if p != keyParts[i] {
			return false
		}
	}
	return true
}

// atLeastOneKey reports whether m has at least one entry. Named for
// clarity at the call site; inlined by the compiler in release builds.
func atLeastOneKey(m map[string]string) bool {
	for range m {
		return true
	}
	return false
}

// fieldErrorBearerForChassis is the named interface emitSaveError
// unwraps adapter field errors against. cmd's *cmdAdapterFieldErrors
// (declared in Task 32) satisfies this structurally — no concrete-type
// coupling between layers. Declared at package scope because Go's
// errors.As requires a named target type; anonymous interface targets
// do not compile.
type fieldErrorBearerForChassis interface {
	error
	FieldErrors() []adapters.FieldError
}

// emitSaveError unwraps typed saver errors into the appropriate JSON
// envelope. settingsChipError carries chip + status; fieldErrorBearerForChassis
// becomes the errors map. Falls back to a generic WRITE FAILED chip.
func emitSaveError(w http.ResponseWriter, err error) {
	var chipErr settingsChipError
	if errors.As(err, &chipErr) {
		writeSettingsChip(w, chipErr.StatusCode(), chipErr.Chip())
		return
	}
	var feb fieldErrorBearerForChassis
	if errors.As(err, &feb) {
		ferrs := map[string]string{}
		for _, fe := range feb.FieldErrors() {
			ferrs[fe.Key] = fe.Msg
		}
		writeSettingsFieldErrors(w, http.StatusBadRequest, ferrs)
		return
	}
	writeSettingsChip(w, http.StatusInternalServerError, "WRITE FAILED")
}

const streamsRefreshTimeout = 30 * time.Second

// handleSettingsActionStreamsRefresh is the POST handler for
// /ui/settings/action/streams-refresh. Single-flight via
// s.streamsRefreshGate.TryLock (second concurrent click → 409 BUSY).
// The 30s timeout chains off r.Context(). Refresh failures return
// HTTP 200 with {ok:false, error} — the action ran cleanly; the
// refresh itself failed.
func (s *Server) handleSettingsActionStreamsRefresh(w http.ResponseWriter, r *http.Request) {
	if s.cfg.StreamsRefresher == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	if !s.streamsRefreshGate.TryLock() {
		writeSettingsChip(w, http.StatusConflict, "BUSY")
		return
	}
	defer s.streamsRefreshGate.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), streamsRefreshTimeout)
	defer cancel()
	start := time.Now()
	result, err := s.cfg.StreamsRefresher.RefreshNow(ctx)
	elapsed := time.Since(start)
	if err != nil {
		writeStreamsRefreshError(w, sanitizeRefreshError(err))
		return
	}
	if result.Err != nil {
		writeStreamsRefreshError(w, sanitizeRefreshError(result.Err))
		return
	}
	durationMS := result.DurationMS
	if durationMS == 0 {
		durationMS = elapsed.Milliseconds()
	}
	writeStreamsRefreshSuccess(w, result.Source, durationMS)
}

func writeStreamsRefreshSuccess(w http.ResponseWriter, source string, durationMS int64) {
	w.Header().Set("Content-Type", "application/json")
	body := map[string]any{
		"ok":          true,
		"summary":     fmt.Sprintf("Manifest refreshed from %s in %dms", source, durationMS),
		"source":      source,
		"duration_ms": durationMS,
	}
	_ = json.NewEncoder(w).Encode(body)
}

func writeStreamsRefreshError(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	body := map[string]any{
		"ok":    false,
		"error": fmt.Sprintf("manifest refresh failed: %s", reason),
	}
	_ = json.NewEncoder(w).Encode(body)
}

// sanitizeRefreshError trims the upstream error message to 200 chars
// (matching 4A's sanitizeProbeError cap) so the .action-result slot
// has predictable size.
func sanitizeRefreshError(err error) string {
	const cap = 200
	msg := strings.TrimSpace(err.Error())
	if len(msg) > cap {
		msg = msg[:cap-3] + "..."
	}
	return msg
}

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

// link action timeouts (chained off r.Context()).
const (
	linkStartTimeout  = 15 * time.Second
	linkStatusTimeout = 5 * time.Second
	linkUnlinkTimeout = 5 * time.Second
)

// resolveLinkable returns (name, ok) after the nil-linker + linkability
// guards, writing the appropriate chip on failure. ok=false means a
// response was already written.
func (s *Server) resolveLinkable(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.cfg.AdapterLinker == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return "", false
	}
	name := r.PathValue("name")
	if name == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return "", false
	}
	if _, ok := s.cfg.AdapterLinker.LinkView(name); !ok {
		writeSettingsChip(w, http.StatusNotFound, "UNKNOWN ADAPTER")
		return "", false
	}
	return name, true
}

// writeLinkView emits {ok:true, view:<LinkView>} with HTTP 200.
func writeLinkView(w http.ResponseWriter, view LinkView) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "view": view})
}

// handleSettingsAdapterLinkStart is the POST handler for
// /ui/settings/adapter/{name}/link/start. Single-flight via
// linkStartGate(name).TryLock — a second concurrent click returns 409
// BUSY. Chains the 15s timeout off r.Context(). On success emits
// {ok:true, view:<LinkView>}.
func (s *Server) handleSettingsAdapterLinkStart(w http.ResponseWriter, r *http.Request) {
	name, ok := s.resolveLinkable(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	params := map[string]string{}
	for _, k := range []string{"username", "password"} {
		if v := r.PostForm.Get(k); v != "" {
			params[k] = v
		}
	}
	gate := s.linkStartGate(name)
	if !gate.TryLock() {
		writeSettingsChip(w, http.StatusConflict, "BUSY")
		return
	}
	defer gate.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), linkStartTimeout)
	defer cancel()
	view, err := s.cfg.AdapterLinker.StartLink(ctx, name, params)
	if err != nil {
		writeSettingsChip(w, http.StatusInternalServerError, "LINK FAILED")
		return
	}
	writeLinkView(w, view)
}

// handleSettingsAdapterLinkStatus is the GET handler for
// /ui/settings/adapter/{name}/link/status. Chains the 5s timeout
// off r.Context(). PIN adapters report current pending/linked state;
// credential adapters return the persisted snapshot.
func (s *Server) handleSettingsAdapterLinkStatus(w http.ResponseWriter, r *http.Request) {
	name, ok := s.resolveLinkable(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), linkStatusTimeout)
	defer cancel()
	view, err := s.cfg.AdapterLinker.LinkStatus(ctx, name)
	if err != nil {
		writeSettingsChip(w, http.StatusInternalServerError, "LINK FAILED")
		return
	}
	writeLinkView(w, view)
}

// handleSettingsAdapterLinkUnlink is the POST handler for
// /ui/settings/adapter/{name}/link/unlink. Best-effort revoke/
// logout under a 5s timeout. Idempotent — always returns the unlinked
// LinkView on success.
func (s *Server) handleSettingsAdapterLinkUnlink(w http.ResponseWriter, r *http.Request) {
	name, ok := s.resolveLinkable(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), linkUnlinkTimeout)
	defer cancel()
	view, err := s.cfg.AdapterLinker.Unlink(ctx, name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeLinkView(w, view)
}

// AdapterHostEditor edits an adapter's host allowlist (the URL adapter's
// ytdlp_hosts). Chassis-owned; satisfied by a cmd wrapper. Kept separate
// from AdapterSettingsSaver because the host list is a []string with no
// FieldDef and is persisted via uiserver.AdapterSaver.SaveValues.
type AdapterHostEditor interface {
	// Hosts returns the adapter's current host list for paint, ok=false
	// for unknown / non-host-editing adapters.
	Hosts(name string) (hosts []string, ok bool)
	// SetHosts validates+normalizes the whole list, persists it atomically,
	// and returns the wire scope ("hot") plus the normalized list.
	SetHosts(name string, hosts []string) (scope string, normalized []string, err error)
}

// AdapterCookieStore manages an adapter's file-backed cookie store (the
// URL adapter's url_cookies.txt). Chassis-owned; satisfied by a cmd
// wrapper. Cookies are a file, never TOML, so they bypass the saver.
type AdapterCookieStore interface {
	CookieStatus(name string) (CookieStatusView, bool)
	SaveCookies(name, raw string) (CookieStatusView, error)
	ClearCookies(name string) (CookieStatusView, error)
}

// CookieStatusView is the paint-time + response view of the cookies file.
type CookieStatusView struct {
	Loaded bool   // false → "not loaded"
	Bytes  int64  // file size when loaded
	SetAt  string // "2006-01-02 15:04:05Z" (UTC); "" when absent
}

// LocalFilesService exposes local-file browsing and single-file casts to the
// chassis without importing the adapter package.
type LocalFilesService interface {
	Browse(ctx context.Context, lib, path string) ([]LocalFileEntry, error)
	Cast(ctx context.Context, lib, path string) error
}

type LocalFileEntry struct {
	Name      string  `json:"name"`
	Rel       string  `json:"rel"`
	IsDir     bool    `json:"is_dir"`
	Playable  bool    `json:"playable"`
	DurationS float64 `json:"duration_s"`
	AudioOnly bool    `json:"audio_only"`
}

// LocalFilesLibraryEditor manages the adapter's named library list.
type LocalFilesLibraryEditor interface {
	Libraries() []LocalFileLibraryRow
	SetLibraries([]LocalFileLibraryRow) (scope string, normalized []LocalFileLibraryRow, err error)
}

type LocalFileLibraryRow struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

// handleSettingsAdapterHostsPost handles POST /ui/settings/adapter/{name}/hosts.
// Body: {"hosts":[...]}. Mirrors handleSettingsAdapterPost's error envelope.
func (s *Server) handleSettingsAdapterHostsPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdapterHostEditor == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	var payload struct {
		Hosts []string `json:"hosts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	scope, normalized, err := s.cfg.AdapterHostEditor.SetHosts(name, payload.Hosts)
	if err != nil {
		emitSaveError(w, err)
		return
	}
	writeSettingsHosts(w, scope, normalized)
}

func writeSettingsHosts(w http.ResponseWriter, scope string, hosts []string) {
	if hosts == nil {
		hosts = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "scope": scope, "hosts": hosts})
}

const maxCookiesBody = 1 << 20 // 1 MiB; mirrors the URL adapter's legacy cap.

type cookieFieldError string

func (e cookieFieldError) Error() string { return string(e) }
func (e cookieFieldError) FieldErrors() []adapters.FieldError {
	return []adapters.FieldError{{Key: "cookies", Msg: string(e)}}
}

func cookiesReadError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return cookieFieldError("cookies file must be 1 MiB or smaller")
	}
	return cookieFieldError("invalid cookies payload")
}

// readCookiesField extracts the cookies payload from a form-encoded or
// JSON body under a 1 MiB cap. Chassis-local (it must not import the URL
// adapter); mirrors the legacy adapter parser shape.
func readCookiesField(r *http.Request) (string, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxCookiesBody+1)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var payload struct {
			Cookies string `json:"cookies"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return "", cookiesReadError(err)
		}
		return payload.Cookies, nil
	}
	if err := r.ParseForm(); err != nil {
		return "", cookiesReadError(err)
	}
	return r.PostForm.Get("cookies"), nil
}

// handleSettingsAdapterCookiesPost handles POST /ui/settings/adapter/{name}/cookies.
func (s *Server) handleSettingsAdapterCookiesPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdapterCookieStore == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	raw, err := readCookiesField(r)
	if err != nil {
		emitSaveError(w, err)
		return
	}
	view, err := s.cfg.AdapterCookieStore.SaveCookies(name, raw)
	if err != nil {
		emitSaveError(w, err)
		return
	}
	writeSettingsCookie(w, view)
}

// handleSettingsAdapterCookiesClear handles POST /ui/settings/adapter/{name}/cookies/clear.
func (s *Server) handleSettingsAdapterCookiesClear(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdapterCookieStore == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	view, err := s.cfg.AdapterCookieStore.ClearCookies(name)
	if err != nil {
		emitSaveError(w, err)
		return
	}
	writeSettingsCookie(w, view)
}

func writeSettingsCookie(w http.ResponseWriter, v CookieStatusView) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
		"cookie": map[string]any{
			"loaded": v.Loaded,
			"bytes":  v.Bytes,
			"set_at": v.SetAt,
		},
	})
}

const maxLocalFilesWidgetBody = 64 << 10

func (s *Server) handleSettingsAdapterLocalfilesBrowse(w http.ResponseWriter, r *http.Request) {
	if s.cfg.LocalFiles == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	lib, rel, err := readLocalFileActionRequest(w, r)
	if err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	if lib == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	entries, err := s.cfg.LocalFiles.Browse(r.Context(), lib, rel)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "not found"})
		return
	}
	if entries == nil {
		entries = []LocalFileEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entries": entries})
}

func (s *Server) handleSettingsAdapterLocalfilesCast(w http.ResponseWriter, r *http.Request) {
	if s.cfg.LocalFiles == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	lib, rel, err := readLocalFileActionRequest(w, r)
	if err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	if lib == "" || strings.TrimSpace(rel) == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	if err := s.cfg.LocalFiles.Cast(r.Context(), lib, rel); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleReceiverLocalfilesBrowse(w http.ResponseWriter, r *http.Request) {
	s.handleSettingsAdapterLocalfilesBrowse(w, r)
}

func (s *Server) handleReceiverLocalfilesCast(w http.ResponseWriter, r *http.Request) {
	s.handleSettingsAdapterLocalfilesCast(w, r)
}

func (s *Server) handleSettingsAdapterLocalfilesLibraries(w http.ResponseWriter, r *http.Request) {
	if s.cfg.LocalFilesLibraryEditor == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	libs, err := readLocalFileLibraries(w, r)
	if err != nil {
		emitSaveError(w, err)
		return
	}
	scope, normalized, err := s.cfg.LocalFilesLibraryEditor.SetLibraries(libs)
	if err != nil {
		emitSaveError(w, err)
		return
	}
	writeLocalFileLibraries(w, scope, normalized)
}

func readLocalFileActionRequest(w http.ResponseWriter, r *http.Request) (lib string, rel string, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLocalFilesWidgetBody)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var payload struct {
			Lib  string `json:"lib"`
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return "", "", err
		}
		return strings.TrimSpace(payload.Lib), payload.Path, nil
	}
	if err := r.ParseForm(); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(r.PostForm.Get("lib")), r.PostForm.Get("path"), nil
}

func readLocalFileLibraries(w http.ResponseWriter, r *http.Request) ([]LocalFileLibraryRow, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLocalFilesWidgetBody)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var payload struct {
			Libraries []LocalFileLibraryRow `json:"libraries"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, &cmdStyleFieldErrors{errs: []adapters.FieldError{{Key: "libraries", Msg: "invalid library payload"}}}
		}
		return payload.Libraries, nil
	}
	if err := r.ParseForm(); err != nil {
		return nil, &cmdStyleFieldErrors{errs: []adapters.FieldError{{Key: "libraries", Msg: "invalid library payload"}}}
	}
	names := r.PostForm["name"]
	roots := r.PostForm["root"]
	if len(names) != len(roots) {
		return nil, &cmdStyleFieldErrors{errs: []adapters.FieldError{{Key: "libraries", Msg: "library names and roots must match"}}}
	}
	libs := make([]LocalFileLibraryRow, 0, len(names))
	for i := range names {
		libs = append(libs, LocalFileLibraryRow{Name: names[i], Root: roots[i]})
	}
	return libs, nil
}

type cmdStyleFieldErrors struct {
	errs []adapters.FieldError
}

func (e *cmdStyleFieldErrors) Error() string                      { return "localfiles field errors" }
func (e *cmdStyleFieldErrors) FieldErrors() []adapters.FieldError { return e.errs }

func writeLocalFileLibraries(w http.ResponseWriter, scope string, libs []LocalFileLibraryRow) {
	if libs == nil {
		libs = []LocalFileLibraryRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "scope": scope, "libraries": libs})
}
