// Package uiserver wires the production ui.BridgeSaver / ui.AdapterSaver
// implementations used by cmd/mister-groovy-relay and exercised directly
// by integration tests. Keeping these types in a dedicated package (not
// the cmd binary) lets tests drive the same save path the operator hits,
// eliminating the "integration test reimplements main.go" drift that the
// review flagged as C3.
package uiserver

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/logging"
)

// FirstRunMarker is the dot-prefixed sentinel file placed in data_dir
// once the operator dismisses the first-run banner. Missing = banner
// still shows; present = banner hidden. Filesystem-based so dismissal
// survives process restart without mutating config.toml.
const FirstRunMarker = ".first-run-complete"

// Core is the subset of core.Manager the saver needs. Declared as an
// interface so tests can inject a fake without pulling in the full
// ffmpeg/dataplane stack and so the package avoids a circular import
// via core -> config -> uiserver.
type Core interface {
	UpdateBridge(b config.BridgeConfig)
	SetInterlaceFieldOrder(order string) error
	DropActiveCast(reason string) error
}

// BridgeSaver implements ui.BridgeSaver + ui.FirstRunAware with real
// per-field scope dispatch (design §9). Current() returns the live
// in-memory bridge for prefill; Save() diffs old vs new, runs pre-flight
// probes for bindable restart-bridge fields, persists to disk via a
// bridge-only rewrite (preserves adapter sections), and dispatches the
// runtime side effects.
//
// Ordering contract: pre-flight -> disk write -> in-memory / core update.
// If any step fails, in-memory state is unchanged so callers can retry
// without the on-disk file and the live Manager drifting apart.
type BridgeSaver struct {
	path     string
	sec      *config.Sectioned
	core     Core
	registry *adapters.Registry
	mu       sync.Mutex
}

// NewBridgeSaver constructs a BridgeSaver bound to the given config
// path, in-memory sectioned config, and running core.Manager. The
// registry is accepted for future multi-adapter coordination (field
// probes that cross adapter boundaries); unused today.
func NewBridgeSaver(path string, sec *config.Sectioned, core Core, registry *adapters.Registry) *BridgeSaver {
	return &BridgeSaver{path: path, sec: sec, core: core, registry: registry}
}

// Mu exposes the shared serialization mutex so AdapterSaver can
// coordinate with bridge saves on the same file. Both paths read-modify-
// write the same config.toml, so concurrent saves must serialize.
func (r *BridgeSaver) Mu() *sync.Mutex { return &r.mu }

// Current returns a snapshot of the live bridge config for UI prefill.
func (r *BridgeSaver) Current() config.BridgeConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sec.Bridge
}

// IsFirstRun implements ui.FirstRunAware: true until DismissFirstRun
// runs once. Re-reads the sentinel every call so container restarts or
// external tooling that touches the marker file are picked up.
func (r *BridgeSaver) IsFirstRun() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := os.Stat(filepath.Join(r.sec.Bridge.DataDir, FirstRunMarker))
	return os.IsNotExist(err)
}

// DismissFirstRun implements ui.FirstRunAware: writes the sentinel
// file so subsequent page loads skip the quick-start banner.
func (r *BridgeSaver) DismissFirstRun() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	path := filepath.Join(r.sec.Bridge.DataDir, FirstRunMarker)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return f.Close()
}

// Save validates + persists a candidate bridge config and applies the
// per-field scope. Order of operations:
//
//  1. Diff old -> new, aggregate max scope.
//  2. Pre-flight probes for bindable restart-bridge fields.
//  3. Marshal + WriteAtomic (on-disk rewrite, adapter sections intact).
//  4. Update in-memory sec.Bridge + core.Manager's bridge copy.
//  5. Apply per-scope side effects (hot-swap interlace, drop cast).
//
// Step 4 comes AFTER step 3 on purpose (review fix I4): a failed write
// must not leave memory ahead of disk. If step 5 fails after step 4,
// disk + memory still agree — only the runtime side effect is missing,
// which the returned error signals to the caller.
func (r *BridgeSaver) Save(newCfg config.BridgeConfig) (adapters.ApplyScope, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	old := r.sec.Bridge
	changed := diffBridgeConfig(old, newCfg)

	scope := adapters.ScopeHotSwap
	for _, k := range changed {
		scope = adapters.MaxScope(scope, scopeForBridgeField(k))
	}

	// Pre-flight probes for bindable restart-bridge fields. Failing
	// here leaves disk + in-memory untouched, matching the bridge
	// panel's "validate before write" contract.
	if scope == adapters.ScopeRestartBridge {
		if containsStr(changed, "ui.http_port") && newCfg.UI.HTTPPort != old.UI.HTTPPort {
			if err := config.ProbeTCPPort(newCfg.UI.HTTPPort); err != nil {
				return 0, fmt.Errorf("ui.http_port pre-flight failed: %w", err)
			}
		}
		if containsStr(changed, "mister.source_port") && newCfg.MiSTer.SourcePort != old.MiSTer.SourcePort {
			if err := config.ProbeUDPPort(newCfg.MiSTer.SourcePort); err != nil {
				return 0, fmt.Errorf("mister.source_port pre-flight failed: %w", err)
			}
		}
		if containsStr(changed, "data_dir") && newCfg.DataDir != old.DataDir {
			if err := config.ProbeDirWritable(newCfg.DataDir); err != nil {
				return 0, fmt.Errorf("data_dir pre-flight failed: %w", err)
			}
		}
	}

	// Write-first so a disk failure can't leave memory + disk skewed.
	// We marshal against a local candidate Sectioned snapshot — the
	// on-disk rewrite (marshalBridgeSection) reads the existing file
	// to preserve adapter sections, then splices in the new [bridge*]
	// blocks from newCfg.
	buf, err := marshalBridgeSection(newCfg, r.path)
	if err != nil {
		return 0, fmt.Errorf("marshal: %w", err)
	}
	if err := config.WriteAtomic(r.path, buf); err != nil {
		return 0, fmt.Errorf("write: %w", err)
	}

	// Disk now reflects newCfg. Move in-memory state to match.
	r.sec.Bridge = newCfg
	r.core.UpdateBridge(newCfg)

	// Apply per scope.
	switch scope {
	case adapters.ScopeHotSwap:
		if containsStr(changed, "video.interlace_field_order") {
			if err := r.core.SetInterlaceFieldOrder(newCfg.Video.InterlaceFieldOrder); err != nil {
				return scope, fmt.Errorf("interlace hot-swap: %w", err)
			}
		}
		if containsStr(changed, "logging.debug") {
			if newCfg.Logging.Debug {
				logging.SetLevel("debug")
			} else {
				logging.SetLevel("info")
			}
		}

	case adapters.ScopeRestartCast:
		// UpdateBridge already ran above so the next session picks up
		// the new aspect_mode / audio settings. Now drop the active
		// cast so the pipeline rebuilds.
		if err := r.core.DropActiveCast("bridge config change"); err != nil {
			return scope, fmt.Errorf("drop-cast: %w", err)
		}

	case adapters.ScopeRestartBridge:
		// Nothing to do at runtime; file is persisted, UI flashes
		// restart-required toast with the docker command.
	}

	return scope, nil
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// marshalBridgeSection rewrites only the [bridge*] tables of the TOML
// file, preserving [adapters.*] sections intact. Avoids round-tripping
// toml.Primitive values through the encoder. Side effect: the new
// [bridge] block always lands at EOF regardless of where it was
// originally — TOML is order-insensitive so the bridge still reads it
// correctly, but users inspecting the file will see the re-ordering
// after first save.
func marshalBridgeSection(newCfg config.BridgeConfig, path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	without := stripBridgeSections(data)

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(struct {
		Bridge config.BridgeConfig `toml:"bridge"`
	}{newCfg}); err != nil {
		return nil, err
	}
	return append(append(without, []byte("\n")...), buf.Bytes()...), nil
}

// stripBridgeSections removes every line from a `[bridge` header
// through the next non-bridge header (or EOF). Matches `[bridge]` and
// any `[bridge.<sub>]` child table; does not match siblings like
// `[bridgehost]` because the prefix check requires either `]` or `.`
// to follow "bridge".
func stripBridgeSections(doc []byte) []byte {
	lines := strings.Split(string(doc), "\n")
	out := make([]string, 0, len(lines))
	skipping := false
	for _, ln := range lines {
		tr := strings.TrimSpace(ln)
		if strings.HasPrefix(tr, "[bridge]") || strings.HasPrefix(tr, "[bridge.") {
			skipping = true
			continue
		}
		if skipping && strings.HasPrefix(tr, "[") && strings.HasSuffix(tr, "]") {
			skipping = false
		}
		if !skipping {
			out = append(out, ln)
		}
	}
	return []byte(strings.Join(out, "\n"))
}

func diffBridgeConfig(oldCfg, newCfg config.BridgeConfig) []string {
	var keys []string
	if oldCfg.DataDir != newCfg.DataDir {
		keys = append(keys, "data_dir")
	}
	if oldCfg.HostIP != newCfg.HostIP {
		keys = append(keys, "host_ip")
	}
	if oldCfg.Video.Modeline != newCfg.Video.Modeline {
		keys = append(keys, "video.modeline")
	}
	if oldCfg.Video.InterlaceFieldOrder != newCfg.Video.InterlaceFieldOrder {
		keys = append(keys, "video.interlace_field_order")
	}
	if oldCfg.Video.AspectMode != newCfg.Video.AspectMode {
		keys = append(keys, "video.aspect_mode")
	}
	if oldCfg.Video.RGBMode != newCfg.Video.RGBMode {
		keys = append(keys, "video.rgb_mode")
	}
	if oldCfg.Video.LZ4Enabled != newCfg.Video.LZ4Enabled {
		keys = append(keys, "video.lz4_enabled")
	}
	if oldCfg.Audio.SampleRate != newCfg.Audio.SampleRate {
		keys = append(keys, "audio.sample_rate")
	}
	if oldCfg.Audio.Channels != newCfg.Audio.Channels {
		keys = append(keys, "audio.channels")
	}
	if oldCfg.MiSTer.Host != newCfg.MiSTer.Host {
		keys = append(keys, "mister.host")
	}
	if oldCfg.MiSTer.Port != newCfg.MiSTer.Port {
		keys = append(keys, "mister.port")
	}
	if oldCfg.MiSTer.SourcePort != newCfg.MiSTer.SourcePort {
		keys = append(keys, "mister.source_port")
	}
	if oldCfg.MiSTer.SSHUser != newCfg.MiSTer.SSHUser {
		keys = append(keys, "mister.ssh_user")
	}
	if oldCfg.MiSTer.SSHPassword != newCfg.MiSTer.SSHPassword {
		keys = append(keys, "mister.ssh_password")
	}
	if oldCfg.UI.HTTPPort != newCfg.UI.HTTPPort {
		keys = append(keys, "ui.http_port")
	}
	if oldCfg.Logging.Debug != newCfg.Logging.Debug {
		keys = append(keys, "logging.debug")
	}
	return keys
}

func scopeForBridgeField(key string) adapters.ApplyScope {
	switch key {
	case "video.interlace_field_order":
		return adapters.ScopeHotSwap
	case "mister.ssh_user", "mister.ssh_password":
		return adapters.ScopeHotSwap
	case "logging.debug":
		// Flipping the log level on an in-flight session is the
		// whole point of this checkbox — the operator wants Debug
		// records flowing now, not after a docker restart.
		return adapters.ScopeHotSwap
	case "video.modeline",
		"video.aspect_mode",
		"video.rgb_mode",
		"video.lz4_enabled",
		"audio.sample_rate",
		"audio.channels":
		return adapters.ScopeRestartCast
	default:
		// mister.host, mister.port, mister.source_port, host_ip,
		// data_dir, ui.http_port — all restart-bridge.
		return adapters.ScopeRestartBridge
	}
}
