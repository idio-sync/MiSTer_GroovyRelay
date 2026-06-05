//go:build integration

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/jellyfin"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/plex"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// e2eSessionManager satisfies both plex.SessionManager and
// jellyfin.SessionManager with no-op implementations. The e2e test
// only drives link/status routes — no playback paths are exercised —
// so none of these methods need real behaviour.
type e2eSessionManager struct{}

func (*e2eSessionManager) StartSession(_ core.SessionRequest) error { return nil }
func (*e2eSessionManager) StartSessionIfIdle(_ core.SessionRequest) (bool, error) {
	return true, nil
}
func (*e2eSessionManager) StartSessionIfIdleSnapshot(_ core.SessionRequest) (core.SessionStatus, bool, error) {
	return core.SessionStatus{State: core.StatePlaying, AdapterRef: "test", Generation: 1}, true, nil
}
func (*e2eSessionManager) Pause() error { return nil }
func (*e2eSessionManager) Play() error  { return nil }
func (*e2eSessionManager) Stop() error  { return nil }
func (*e2eSessionManager) StopIfAdapterRef(string) (bool, error) {
	return false, nil
}
func (*e2eSessionManager) StopIfSession(string, uint64) (bool, error) {
	return false, nil
}
func (*e2eSessionManager) SeekTo(_ int) error            { return nil }
func (*e2eSessionManager) Status() core.SessionStatus    { return core.SessionStatus{} }
func (*e2eSessionManager) VisualizerMode() string        { return "" }
func (*e2eSessionManager) DropActiveCast(_ string) error { return nil }

// buildTestRegistry constructs a registry with the real plex and jellyfin
// adapters, each pointed at an isolated t.TempDir() so no token exists on
// disk. Both adapters therefore start in the unlinked phase without any
// network or ffmpeg calls. This mirrors main.go's construction order;
// DecodeConfig is skipped because we only need the default config (the
// adapters start enabled=false by default, which is fine for link/status).
func buildTestRegistry(t *testing.T) *adapters.Registry {
	t.Helper()

	dataDir := t.TempDir()
	core := &e2eSessionManager{}

	// Plex: NewAdapter requires a non-nil Core and non-nil TokenStore.
	// LoadStoredData on a fresh temp dir returns an empty StoredData (no
	// auth token), which is exactly the unlinked state we want.
	store, err := plex.LoadStoredData(dataDir)
	if err != nil {
		t.Fatalf("plex.LoadStoredData: %v", err)
	}
	plexAdapter, err := plex.NewAdapter(plex.AdapterConfig{
		Bridge:     config.BridgeConfig{DataDir: dataDir},
		Core:       core,
		TokenStore: store,
		Version:    "test",
	})
	if err != nil {
		t.Fatalf("plex.NewAdapter: %v", err)
	}

	// Jellyfin: core may be nil for tests that don't exercise StartSession.
	// dataDir is the token root; a fresh dir has no token file → unlinked.
	jfAdapter := jellyfin.New(nil, dataDir, "test-device-uuid", "", nil)

	reg := adapters.NewRegistry()
	if err := reg.Register(plexAdapter); err != nil {
		t.Fatalf("register plex: %v", err)
	}
	if err := reg.Register(jfAdapter); err != nil {
		t.Fatalf("register jellyfin: %v", err)
	}
	return reg
}

// TestE2E_LinkStatusContract drives the chassis link routes through the
// production adapterLinker binding against a real registry, asserting the
// unlinked JSON contract end to end. Auth that needs a live Jellyfin /
// plex.tv server is out of scope; this pins the wiring:
//
//	route → requireSameOrigin → handler → adapterLinker → adapter.Snapshot
//
// which is where 4E's integration risk lives.
func TestE2E_LinkStatusContract(t *testing.T) {
	reg := buildTestRegistry(t)

	s, err := chassis.New(chassis.Config{
		Version:       "test",
		StartedAt:     time.Now(),
		Bridge:        config.BridgeConfig{MiSTer: config.MisterConfig{Host: "127.0.0.1"}},
		AdapterLinker: newAdapterLinker(reg),
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	mux := http.NewServeMux()
	s.Mount(mux)

	// Jellyfin link/status — fresh data_dir, no server_url → unlinked credential view.
	req := httptest.NewRequest("GET", "/ui/settings/adapter/jellyfin/link/status", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("jellyfin link/status: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"kind":"credential"`) || !strings.Contains(body, `"phase":"unlinked"`) {
		t.Errorf("jellyfin link/status body = %s, want credential/unlinked view", body)
	}

	// Plex link/status — no auth token on disk → unlinked PIN view.
	req2 := httptest.NewRequest("GET", "/ui/settings/adapter/plex/link/status", nil)
	req2.Header.Set("Sec-Fetch-Site", "same-origin")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("plex link/status: status = %d, want 200; body = %s", rec2.Code, rec2.Body.String())
	}
	body2 := rec2.Body.String()
	if !strings.Contains(body2, `"kind":"pin"`) || !strings.Contains(body2, `"phase":"unlinked"`) {
		t.Errorf("plex link/status body = %s, want pin/unlinked view", body2)
	}
}
