package plex

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// TestLinkUnlink_CallsRevokeBeforeLocalCleanup verifies Unlink hits
// plex.tv with the snapshotted (uuid, token) pair before clearing local
// state. Pinning order matters: if we cleared the in-memory token first the
// revoke call would fire with an empty token and produce a 401.
func TestLinkUnlink_CallsRevokeBeforeLocalCleanup(t *testing.T) {
	dir := t.TempDir()
	store := &StoredData{DeviceUUID: "uuid-revoke", AuthToken: "tok-revoke"}
	if err := SaveStoredData(dir, store); err != nil {
		t.Fatal(err)
	}

	var calls int32
	var gotMethod, gotPath, gotToken string
	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotToken = r.URL.Query().Get("X-Plex-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	restore := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = restore })

	a := &Adapter{
		cfg: AdapterConfig{
			TokenStore: store,
			Bridge:     config.BridgeConfig{DataDir: dir},
		},
	}
	a.plexCfg = DefaultConfig()

	snap, err := a.Unlink(context.Background())
	if err != nil {
		t.Fatalf("Unlink err = %v", err)
	}
	if snap.Phase != adapters.LinkPhaseUnlinked {
		t.Fatalf("Phase = %q, want unlinked", snap.Phase)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected exactly 1 plex.tv revoke call, got %d", got)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	if gotPath != "/devices/uuid-revoke.xml" {
		t.Errorf("expected /devices/uuid-revoke.xml, got %s", gotPath)
	}
	if gotToken != "tok-revoke" {
		t.Errorf("expected snapshotted token tok-revoke, got %q", gotToken)
	}
	if a.cfg.TokenStore.AuthToken != "" {
		t.Error("AuthToken should be cleared after Unlink returns")
	}
}

// TestLinkUnlink_RevokeFailureStillCleansUpLocally guards the best-effort
// contract: a plex.tv 5xx (or any failure) must not block local cleanup.
// The operator clicked Unlink — the bridge is unlinked regardless of what
// plex.tv says.
func TestLinkUnlink_RevokeFailureStillCleansUpLocally(t *testing.T) {
	dir := t.TempDir()
	store := &StoredData{DeviceUUID: "uuid-fail", AuthToken: "tok-fail"}
	if err := SaveStoredData(dir, store); err != nil {
		t.Fatal(err)
	}

	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	restore := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = restore })

	a := &Adapter{
		cfg: AdapterConfig{
			TokenStore: store,
			Bridge:     config.BridgeConfig{DataDir: dir},
		},
	}
	a.plexCfg = DefaultConfig()

	snap, err := a.Unlink(context.Background())
	if err != nil {
		t.Fatalf("revoke failure must not propagate; err = %v", err)
	}
	if snap.Phase != adapters.LinkPhaseUnlinked {
		t.Fatalf("Phase = %q, want unlinked", snap.Phase)
	}
	if a.cfg.TokenStore.AuthToken != "" {
		t.Error("local AuthToken must still be cleared even when revoke fails")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".data.json.unlinked-") {
			return
		}
	}
	t.Error("rename target (.data.json.unlinked-*) not found after revoke failure")
}

// TestLinkUnlink_ClearsTokenAndRenames verifies the on-disk token file is
// rotated aside (not deleted outright) so a botched unlink is recoverable.
func TestLinkUnlink_ClearsTokenAndRenames(t *testing.T) {
	dir := t.TempDir()
	store := &StoredData{DeviceUUID: "uuid", AuthToken: "tok"}
	if err := SaveStoredData(dir, store); err != nil {
		t.Fatal(err)
	}

	// Unlink fires RevokeDevice before local cleanup. Stub plex.tv so this
	// test never escapes to the real network on CI offline runners.
	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	restore := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = restore })

	a := &Adapter{
		cfg: AdapterConfig{
			TokenStore: store,
			Bridge:     config.BridgeConfig{DataDir: dir},
		},
	}
	a.plexCfg = DefaultConfig()

	if _, err := a.Unlink(context.Background()); err != nil {
		t.Fatalf("Unlink err = %v", err)
	}
	if a.cfg.TokenStore.AuthToken != "" {
		t.Error("AuthToken should be cleared")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".data.json.unlinked-") {
			return // success
		}
	}
	t.Error("rename target (.data.json.unlinked-*) not found in data_dir")
}
