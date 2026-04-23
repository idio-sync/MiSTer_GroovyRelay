package plex

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestLinkStatus_PendingFragment(t *testing.T) {
	a := &Adapter{}
	a.pending = newPendingLink("ABCD", 12345, time.Now().Add(14*time.Minute+47*time.Second))

	req := httptest.NewRequest("GET", "/ui/adapter/plex/link/status", nil)
	rw := httptest.NewRecorder()
	a.handleLinkStatus(rw, req)

	if rw.Code != http.StatusAccepted {
		t.Errorf("pending status code = %d, want 202", rw.Code)
	}
	body := rw.Body.String()
	if !strings.Contains(body, "ABCD") {
		t.Errorf("body missing PIN code: %s", body)
	}
	if !strings.Contains(body, "hx-trigger") {
		t.Errorf("body missing htmx polling attribute: %s", body)
	}
}

func TestLinkStatus_LinkedFragment(t *testing.T) {
	a := &Adapter{
		cfg: AdapterConfig{
			TokenStore: &StoredData{DeviceUUID: "uuid", AuthToken: "the-token"},
			Bridge:     config.BridgeConfig{DataDir: "/tmp"},
		},
	}
	a.plexCfg = DefaultConfig()

	req := httptest.NewRequest("GET", "/ui/adapter/plex/link/status", nil)
	rw := httptest.NewRecorder()
	a.handleLinkStatus(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("linked status code = %d, want 200", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "linked") {
		t.Errorf("body missing 'linked': %s", rw.Body.String())
	}
}

func TestLinkStatus_ExpiredFragment(t *testing.T) {
	a := &Adapter{}
	a.pending = newPendingLink("ABCD", 12345, time.Now().Add(-1*time.Second))

	req := httptest.NewRequest("GET", "/ui/adapter/plex/link/status", nil)
	rw := httptest.NewRecorder()
	a.handleLinkStatus(rw, req)

	if rw.Code != http.StatusGone {
		t.Errorf("expired status code = %d, want 410", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "Try Again") {
		t.Errorf("body missing try-again CTA: %s", rw.Body.String())
	}
}

func TestLinkUnlink_ClearsTokenAndRenames(t *testing.T) {
	dir := t.TempDir()
	store := &StoredData{DeviceUUID: "uuid", AuthToken: "tok"}
	if err := SaveStoredData(dir, store); err != nil {
		t.Fatal(err)
	}

	a := &Adapter{
		cfg: AdapterConfig{
			TokenStore: store,
			Bridge:     config.BridgeConfig{DataDir: dir},
		},
	}
	a.plexCfg = DefaultConfig()

	req := httptest.NewRequest("POST", "/ui/adapter/plex/unlink", strings.NewReader(""))
	rw := httptest.NewRecorder()
	a.handleUnlink(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
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
