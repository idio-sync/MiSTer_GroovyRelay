// Package extbin resolves external helper binaries shipped beside the bridge
// or provided by the host environment.
package extbin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Resolver resolves one external binary name, honoring a mutable config
// override, a sidecar directory, then PATH.
type Resolver struct {
	name      string
	selfDir   string
	override  string
	cachePath string
	mu        sync.Mutex
}

// New constructs a resolver for name. selfDir may be empty to skip sidecar
// lookup; configOverride may be empty to fall through to sidecar/PATH.
func New(name, configOverride, selfDir string) *Resolver {
	return &Resolver{name: name, override: configOverride, selfDir: selfDir}
}

// Resolve returns the first usable binary path according to the configured
// override, sidecar, PATH lookup order.
func (r *Resolver) Resolve() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cachePath != "" {
		return r.cachePath, nil
	}

	if r.override != "" {
		if err := CheckExecutable(r.override); err != nil {
			return "", fmt.Errorf("%s override %q is not usable: %w", r.name, r.override, err)
		}
		r.cachePath = r.override
		return r.cachePath, nil
	}

	var tried []string
	if r.selfDir != "" {
		sidecar := filepath.Join(r.selfDir, executableName(r.name))
		tried = append(tried, sidecar)
		if err := CheckExecutable(sidecar); err == nil {
			r.cachePath = sidecar
			return r.cachePath, nil
		}
	}

	if path, err := exec.LookPath(r.name); err == nil {
		r.cachePath = path
		return r.cachePath, nil
	}
	tried = append(tried, "$PATH")

	if r.override == "" {
		tried = append(tried, fmt.Sprintf("bridge.%s_path is empty", configFieldName(r.name)))
	}
	return "", fmt.Errorf("%s not found: tried %s", r.name, strings.Join(tried, ", "))
}

// UpdateOverride changes the config override and clears any cached result.
func (r *Resolver) UpdateOverride(configOverride string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.override = configOverride
	r.cachePath = ""
}

// Invalidate clears the cached resolution without changing the override.
func (r *Resolver) Invalidate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cachePath = ""
}

// CheckExecutable verifies that path names an executable file for the current
// platform. On Windows, the shipped sidecars are .exe files; on Unix, the
// executable bit must be present.
func CheckExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}
	if runtime.GOOS == "windows" {
		if strings.EqualFold(filepath.Ext(path), ".exe") {
			return nil
		}
		return fmt.Errorf("does not have .exe extension")
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("not executable")
	}
	return nil
}

func executableName(name string) string {
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(name), ".exe") {
		return name + ".exe"
	}
	return name
}

func configFieldName(name string) string {
	if name == "yt-dlp" {
		return "ytdlp"
	}
	return name
}
