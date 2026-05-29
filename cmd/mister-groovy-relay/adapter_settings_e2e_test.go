//go:build integration
// +build integration

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/dlna"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

// dlnaTestSessionManager is a no-op dlna.SessionManager for use in
// tests that only exercise DecodeConfig / ApplyConfig / CurrentValues
// and do not drive the DLNA session lifecycle. It satisfies the
// dlna.SessionManager interface via structural typing — the DLNA
// adapter package does not export the interface, so we must implement
// every method signature explicitly.
type dlnaTestSessionManager struct{}

func (*dlnaTestSessionManager) StartSession(core.SessionRequest) error { return nil }
func (*dlnaTestSessionManager) StartSessionIfAdapterRef(core.SessionRequest, string) (bool, error) {
	return false, nil
}
func (*dlnaTestSessionManager) Status() core.SessionStatus { return core.SessionStatus{} }
func (*dlnaTestSessionManager) Pause() error               { return nil }
func (*dlnaTestSessionManager) PauseIfAdapterRef(string) (bool, error) {
	return false, nil
}
func (*dlnaTestSessionManager) Play() error { return nil }
func (*dlnaTestSessionManager) PlayIfAdapterRef(string) (bool, error) {
	return false, nil
}
func (*dlnaTestSessionManager) Stop() error { return nil }
func (*dlnaTestSessionManager) StopIfAdapterRef(string) (bool, error) {
	return false, nil
}
func (*dlnaTestSessionManager) SeekTo(int) error { return nil }
func (*dlnaTestSessionManager) SeekToIfAdapterRef(string, int) (bool, error) {
	return false, nil
}

func TestE2E_DLNA_SaveEnabledToggle(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	// Minimal sectioned TOML. defaultBridge() fills in video, audio,
	// mister.port, mister.source_port, and ui.http_port defaults so we
	// only need to supply the required mister.host and a concrete data_dir
	// (so ResolveDataDir doesn't fall back to a platform dir the test
	// can't write to). The [adapters.dlna] section starts with
	// enabled = false so the POST toggle is a true before→after change.
	//
	// Use forward slashes in the TOML data_dir value: TOML string
	// literals use Go/C escape rules, so Windows backslashes would need
	// double-escaping. Forward slashes are valid path separators on
	// Windows and sidestep the TOML parser escape issue entirely.
	dataDirFwd := strings.ReplaceAll(dir, `\`, `/`)
	body := `[bridge]
mister.host = "127.0.0.1"
data_dir = "` + dataDirFwd + `"

[adapters.dlna]
enabled = false
device_name = "MiSTer"
autoplay_on_set_uri = false
allow_public_source_urls = false
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Step A: construct the DLNA adapter exactly as main.go does.
	// dlna.New requires DeviceUUID (non-empty), HTTPPort (1-65535), and
	// Core (non-nil SessionManager). HostIP is optional at construction;
	// Version is optional. The stub Core satisfies the SessionManager
	// interface; this test only exercises DecodeConfig/ApplyConfig/
	// CurrentValues so the stub never gets called.
	a, err := dlna.New(dlna.AdapterConfig{
		DeviceUUID: "test-uuid-0000-0000-0000-000000000001",
		HostIP:     "",
		HTTPPort:   32500,
		Version:    "test",
		Core:       &dlnaTestSessionManager{},
	})
	if err != nil {
		t.Fatalf("dlna.New: %v", err)
	}

	// Step B: seed in-memory state from the on-disk [adapters.dlna]
	// section via config.LoadSectioned — the REAL loader main.go uses.
	// The loader returns a *Sectioned whose Adapters map holds the raw
	// toml.Primitive for each adapter section, and whose MetaData() lets
	// us call toml.PrimitiveDecode inside DecodeConfig.
	if err := decodeAdapterSectionFromFile(t, cfgPath, "dlna", a); err != nil {
		t.Fatalf("decodeAdapterSectionFromFile: %v", err)
	}

	// Step C: wire a chassis Server with just this adapter registered.
	mu := &sync.Mutex{}
	adapterSaver := uiserver.NewAdapterSaver(cfgPath, mu)
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"dlna": a}}
	wrapper := newBridgeAdapterSettingsSaver(adapterSaver, reg)

	srv, err := chassis.New(chassis.Config{
		Version:              "test",
		StartedAt:            time.Now(),
		AdapterSettingsSaver: wrapper,
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Step D: POST the enabled toggle.
	// requireSameOrigin checks Sec-Fetch-Site: same-origin (confirmed in
	// Task 38). Use http.NewRequest so we can set that header; the plain
	// http.Post helper cannot.
	req, err := http.NewRequest("POST", ts.URL+"/receiver/settings/adapter/dlna",
		strings.NewReader("enabled=true"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(res.Body)
		t.Fatalf("Status = %d; body = %s", res.StatusCode, out)
	}

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response JSON: %v", err)
	}
	if scope, _ := payload["scope"].(string); scope != "hot" {
		t.Errorf("scope = %q, want hot", scope)
	}

	// Disk side: config.toml must contain enabled = true.
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile config.toml: %v", err)
	}
	if !strings.Contains(string(got), `enabled = true`) {
		t.Errorf("config.toml missing enabled = true:\n%s", got)
	}

	// In-memory side: adapter's CurrentValues must reflect the change.
	// a is *dlna.Adapter which exposes CurrentValues() directly; use the
	// adapters.Adapter interface (assigned to adapterIface) to satisfy
	// the type-assertion pattern demanded by the task spec, and confirm
	// the method is reachable via the interface too.
	var adapterIface interface{} = a
	cv, ok := adapterIface.(interface{ CurrentValues() map[string]any })
	if !ok {
		t.Fatalf("dlna adapter does not expose CurrentValues")
	}
	if v, _ := cv.CurrentValues()["enabled"].(bool); !v {
		t.Errorf("CurrentValues()[\"enabled\"] = false; want true")
	}
}

// decodeAdapterSectionFromFile loads the sectioned config at path via
// config.LoadSectioned (the real loader main.go uses at line 86), extracts
// the toml.Primitive for adapters[name], and calls a.DecodeConfig with the
// real MetaData. This mirrors main.go's adapter-decode loop
// (cmd/mister-groovy-relay/main.go:290-295).
func decodeAdapterSectionFromFile(t *testing.T, path, name string, a adapters.Adapter) error {
	t.Helper()
	sec, err := config.LoadSectioned(path)
	if err != nil {
		return err
	}
	prim, ok := sec.Adapters[name]
	if !ok {
		// No section for this adapter in the file — nothing to decode;
		// adapter stays at DefaultConfig(). Not an error (main.go skips
		// adapters with no TOML section too).
		return nil
	}
	return a.DecodeConfig(prim, sec.MetaData())
}
