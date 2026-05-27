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
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// BridgeSettingsSaver is the narrow chassis-side interface for bridge
// settings persistence and snapshot. Production passes
// *uiserver.BridgeSaver, but internal/chassis does not import
// internal/uiserver — the wiring lives in cmd/mister-groovy-relay.
type BridgeSettingsSaver interface {
	// Current returns the live in-memory bridge config snapshot. The
	// chassis settings drawer uses this for first render and for
	// composing patches (current + touched-field overlay) on each save.
	Current() config.BridgeConfig

	// Save persists the patch to disk and dispatches in-memory side
	// effects. The returned ApplyScope is the max-wins scope across all
	// changed fields; the chassis maps it via scopeLabel before emitting
	// to the wire.
	Save(config.BridgeConfig) (adapters.ApplyScope, error)
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

// settingsChipError is matched structurally so saver-layer typed errors
// can carry HTTP/chip details across the interface boundary without a
// uiserver import. The chassis handler uses errors.As against the
// interface (Go 1.21+).
type settingsChipError interface {
	error
	StatusCode() int
	Chip() string
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
