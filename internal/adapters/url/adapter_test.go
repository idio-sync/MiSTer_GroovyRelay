package url

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
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

// TestApplyConfig_RebuildsResolverOnTimeoutChange pins the contract
// that ytdlp_resolve_timeout_seconds is genuinely HotSwap (per spec
// §"Config schema"): operators changing the timeout via TOML/UI must
// see the new value reflected in the next resolver invocation, not
// silently keep the Start-time value.
func TestApplyConfig_RebuildsResolverOnTimeoutChange(t *testing.T) {
	a, _ := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   nil,
	})
	a.cfg = DefaultConfig()
	a.probeFn = func() ytdlpProbe {
		return ytdlpProbe{
			Path:    "/usr/local/bin/yt-dlp",
			Version: "stub",
			OK:      true,
		}
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	startResolver := a.resolver
	if startResolver == nil {
		t.Fatal("Start should have wired a resolver")
	}

	// Build a TOML primitive with a different timeout (60s vs default 30s).
	const raw = `
[adapters.url]
enabled = true
ytdlp_resolve_timeout_seconds = 60
`
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(raw, &envelope)
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	scope, err := a.ApplyConfig(envelope.Adapters["url"], meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v, want HotSwap", scope)
	}

	// Resolver should have been rebuilt — different pointer, new timeout.
	if a.resolver == startResolver {
		t.Error("resolver was not rebuilt after timeout change (HotSwap silently failed)")
	}
	r, ok := a.resolver.(*ytdlp.Resolver)
	if !ok {
		t.Fatalf("resolver type = %T, want *ytdlp.Resolver", a.resolver)
	}
	if r.Timeout != 60*time.Second {
		t.Errorf("resolver.Timeout = %v, want 60s", r.Timeout)
	}
}

// TestApplyConfig_NoRebuildWhenTimeoutUnchanged verifies the rebuild
// only fires on actual changes — avoids needless allocation churn
// when an operator saves a panel without touching the timeout.
func TestApplyConfig_NoRebuildWhenTimeoutUnchanged(t *testing.T) {
	a, _ := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   nil,
	})
	a.cfg = DefaultConfig()
	a.probeFn = func() ytdlpProbe {
		return ytdlpProbe{Path: "/x", Version: "v", OK: true}
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	startResolver := a.resolver

	// Same timeout as default (30) — no rebuild expected.
	const raw = `
[adapters.url]
enabled = true
ytdlp_resolve_timeout_seconds = 30
`
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, _ := toml.Decode(raw, &envelope)
	if _, err := a.ApplyConfig(envelope.Adapters["url"], meta); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if a.resolver != startResolver {
		t.Error("resolver was rebuilt when timeout did not change — wasted allocation")
	}
}
