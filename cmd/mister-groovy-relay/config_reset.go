package main

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// configReset satisfies chassis.ConfigReset from outside
// internal/chassis. It composes:
//   - path: the on-disk config.toml location
//   - mu:   BridgeSaver.Mu() — shared with bridge + adapter writes so a
//           reset cannot interleave with a partial save
//   - sectioned: a pointer to the live config snapshot, used to read
//                the operator's current data_dir for preservation
//                through the reset. Read under the already-held mu;
//                does NOT call bridgeSaver.Current() because Current()
//                acquires the same mutex internally and would deadlock.
type configReset struct {
	path      string
	mu        *sync.Mutex
	sectioned *config.Sectioned
}

func (r *configReset) ResetToDefaults() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Preserve the operator's current data_dir so persistent state
	// (device UUID, plex.tv token, streams cache, .first-run-complete)
	// stays chained to the post-reset config. Read sectioned.Bridge
	// directly while holding BridgeSaver.Mu(); do NOT call
	// BridgeSaver.Current() here, because Current() takes the same
	// mutex and would deadlock.
	dataDir := ""
	if r.sectioned != nil {
		dataDir = r.sectioned.Bridge.DataDir
	}
	rendered, err := config.DefaultConfigTOML(dataDir)
	if err != nil {
		return &configResetError{cause: fmt.Errorf("render defaults: %w", err)}
	}
	if err := config.WriteAtomic(r.path, rendered); err != nil {
		return &configResetError{cause: fmt.Errorf("write: %w", err)}
	}
	return nil
}

// configResetError satisfies chassis.settingsChipError so disk failures
// map to {chip:"WRITE FAILED"} via the existing chassis errors.As path.
type configResetError struct{ cause error }

func (e *configResetError) Error() string   { return e.cause.Error() }
func (e *configResetError) Unwrap() error   { return e.cause }
func (e *configResetError) StatusCode() int { return http.StatusInternalServerError }
func (e *configResetError) Chip() string    { return "WRITE FAILED" }
