package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestConfigReset_ResetToDefaults_WritesDefaultTOMLPreservingDataDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// Seed a non-default config.
	if err := os.WriteFile(path, []byte("# non-default content\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sec := &config.Sectioned{Bridge: config.BridgeConfig{DataDir: "/custom/data/dir"}}
	r := &configReset{path: path, mu: &sync.Mutex{}, sectioned: sec}

	if err := r.ResetToDefaults(); err != nil {
		t.Fatalf("ResetToDefaults: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	want, err := config.DefaultConfigTOML("/custom/data/dir")
	if err != nil {
		t.Fatalf("DefaultConfigTOML: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("disk content differs from DefaultConfigTOML(/custom/data/dir)")
	}
}

func TestConfigReset_ResetToDefaults_DataDirEmptyFallsThroughToPlatformDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("# initial\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sec := &config.Sectioned{Bridge: config.BridgeConfig{DataDir: ""}}
	r := &configReset{path: path, mu: &sync.Mutex{}, sectioned: sec}

	if err := r.ResetToDefaults(); err != nil {
		t.Fatalf("ResetToDefaults: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) == "# initial\n" {
		t.Errorf("disk was not overwritten")
	}
}

func TestConfigReset_ResetToDefaults_DiskFailureReturnsChipError(t *testing.T) {
	r := &configReset{
		path:      "/nonexistent/dir/config.toml",
		mu:        &sync.Mutex{},
		sectioned: &config.Sectioned{Bridge: config.BridgeConfig{DataDir: "/x"}},
	}

	err := r.ResetToDefaults()
	if err == nil {
		t.Fatalf("expected disk error; got nil")
	}
	if codeErr, ok := err.(interface{ StatusCode() int }); !ok {
		t.Errorf("error does not satisfy StatusCode() int; got %T", err)
	} else if codeErr.StatusCode() != http.StatusInternalServerError {
		t.Errorf("StatusCode() = %d; want 500", codeErr.StatusCode())
	}
	if chipErr, ok := err.(interface{ Chip() string }); !ok {
		t.Errorf("error does not satisfy Chip() string; got %T", err)
	} else if chipErr.Chip() != "WRITE FAILED" {
		t.Errorf("Chip() = %q; want \"WRITE FAILED\"", chipErr.Chip())
	}
}

func TestConfigReset_DoesNotTouchDataDirContents(t *testing.T) {
	dataDir := t.TempDir()
	sentinel := filepath.Join(dataDir, "device_uuid")
	if err := os.WriteFile(sentinel, []byte("uuid-from-before-reset"), 0o644); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte("# initial\n"), 0o644); err != nil {
		t.Fatalf("seed cfg: %v", err)
	}

	sec := &config.Sectioned{Bridge: config.BridgeConfig{DataDir: dataDir}}
	r := &configReset{path: cfgPath, mu: &sync.Mutex{}, sectioned: sec}
	if err := r.ResetToDefaults(); err != nil {
		t.Fatalf("ResetToDefaults: %v", err)
	}

	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(got) != "uuid-from-before-reset" {
		t.Errorf("data_dir sentinel was disturbed; got %q", string(got))
	}
}

func TestConfigReset_ResetToDefaults_CompletesWithoutSelfDeadlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("# initial\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r := &configReset{
		path:      path,
		mu:        &sync.Mutex{},
		sectioned: &config.Sectioned{Bridge: config.BridgeConfig{DataDir: "/x"}},
	}

	done := make(chan error, 1)
	go func() { done <- r.ResetToDefaults() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ResetToDefaults: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("ResetToDefaults timed out; possible self-deadlock on shared mutex")
	}
}
