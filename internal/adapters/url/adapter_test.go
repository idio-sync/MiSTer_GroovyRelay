package url

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestNew_RequiresDataDir(t *testing.T) {
	_, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{
			DataDir: "", // empty
		},
		Core: nil,
	})
	if err == nil {
		t.Fatalf("New with empty DataDir: want error, got nil")
	}
}

func TestNew_AcceptsValidConfig(t *testing.T) {
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: "/tmp/test-data-dir"},
		Core:   nil, // permitted; play handler returns 500 instead of nil-deref
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a == nil {
		t.Fatal("New returned nil adapter")
	}
}

func TestAdapter_CookiesPath_DerivedFromDataDir(t *testing.T) {
	dataDir := filepath.FromSlash("/var/lib/bridge")
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: dataDir},
		Core:   nil,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := a.CookiesPath()
	want := filepath.Join(dataDir, "url_cookies.txt")
	if got != want {
		t.Fatalf("CookiesPath = %q, want %q", got, want)
	}
}

func TestStart_ProbesYtdlp_BinaryPresent(t *testing.T) {
	a, _ := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   nil,
	})
	a.cfg = DefaultConfig()
	a.cfg.Enabled = true

	// Inject a stub probe function (not the real one) that simulates
	// "found at /usr/local/bin/yt-dlp, version 2026.04.20".
	a.probeFn = func() ytdlpProbe {
		return ytdlpProbe{
			Path:    "/usr/local/bin/yt-dlp",
			Version: "2026.04.20",
			OK:      true,
		}
	}

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !a.ytdlpProbe.OK {
		t.Error("ytdlpProbe.OK should be true")
	}
	if a.resolver == nil {
		t.Error("resolver should be non-nil after successful probe")
	}
}

func TestStart_ProbesYtdlp_BinaryMissing(t *testing.T) {
	a, _ := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   nil,
	})
	a.cfg = DefaultConfig()
	a.probeFn = func() ytdlpProbe {
		return ytdlpProbe{OK: false}
	}

	if err := a.Start(context.Background()); err != nil {
		// Failed probe must not fail Start — adapter degrades.
		t.Fatalf("Start should not fail when yt-dlp is missing: %v", err)
	}
	if a.ytdlpProbe.OK {
		t.Error("ytdlpProbe.OK should be false")
	}
	if a.resolver != nil {
		t.Error("resolver should be nil when probe failed")
	}
}
