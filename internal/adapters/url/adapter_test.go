package url

import (
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
