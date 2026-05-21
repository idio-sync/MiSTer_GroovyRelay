package chassis

import (
	"mime"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// nonZeroConfig returns a Config valid enough for New(). Tests that
// want to assert error paths shadow individual fields with zero values.
func nonZeroConfig() Config {
	return Config{
		Bridge:    config.BridgeConfig{},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "test-1.0.0",
		StartedAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		HostIP:    "10.0.0.5",
	}
}

func TestNew_ReturnsServerWithValidConfig(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s == nil {
		t.Fatal("New returned nil Server with no error")
	}
}

func TestNew_RejectsZeroStartedAt(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.StartedAt = time.Time{}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for zero StartedAt, got nil")
	}
}

func TestNew_RejectsEmptyVersion(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.Version = ""
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for empty Version, got nil")
	}
}

func TestNew_AllowsEmptyHostIP(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.HostIP = ""
	_, err := New(cfg)
	if err != nil {
		t.Fatalf("New should accept empty HostIP, got: %v", err)
	}
}

func TestInit_RegistersWoff2MIME(t *testing.T) {
	t.Parallel()
	// Package init must register font/woff2 so http.FileServer's
	// content-type lookup succeeds on hosts where the system MIME
	// database doesn't know woff2 (Alpine, scratch containers).
	got := mime.TypeByExtension(".woff2")
	if got != "font/woff2" {
		t.Fatalf(`TypeByExtension(".woff2") = %q, want "font/woff2"`, got)
	}
}

func TestInit_RegistersWoffMIME(t *testing.T) {
	t.Parallel()
	got := mime.TypeByExtension(".woff")
	if got != "font/woff" {
		t.Fatalf(`TypeByExtension(".woff") = %q, want "font/woff"`, got)
	}
}
