package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type fakeCompanionSession struct {
	status core.SessionStatus
}

func (f fakeCompanionSession) Status() core.SessionStatus { return f.status }

type fakeCompanionURL struct {
	history     []CompanionHistoryEntry
	lastDisplay string
}

func (f fakeCompanionURL) CompanionHistory() []CompanionHistoryEntry {
	return f.history
}
func (f fakeCompanionURL) CompanionLastURLDisplay() string { return f.lastDisplay }

type fakeCompanionDisplay struct {
	display CompanionSessionDisplay
}

func (f fakeCompanionDisplay) CompanionDisplay(string) CompanionSessionDisplay {
	return f.display
}

func TestCompanionGateRequiresExtensionOriginAndHeader(t *testing.T) {
	h := companionExtensionGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	cases := []struct {
		name   string
		origin string
		header string
		want   int
	}{
		{"missing both", "", "", http.StatusForbidden},
		{"origin only", "moz-extension://abc", "", http.StatusForbidden},
		{"header only", "", "1", http.StatusForbidden},
		{"web origin", "https://evil.example", "1", http.StatusForbidden},
		{"extension pair", "moz-extension://abc", "1", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ui/companion/status", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.header != "" {
				req.Header.Set("X-Bridge-Extension", tc.header)
			}
			rw := httptest.NewRecorder()
			h.ServeHTTP(rw, req)
			if rw.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rw.Code, tc.want, rw.Body.String())
			}
			if tc.want == http.StatusForbidden && !strings.Contains(rw.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("forbidden content type = %q, want JSON", rw.Header().Get("Content-Type"))
			}
		})
	}
}

func TestCompanionGatePreflightUsesExtensionCORS(t *testing.T) {
	h := companionExtensionGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("preflight should not reach route")
	}))
	req := httptest.NewRequest(http.MethodOptions, "/ui/companion/status", nil)
	req.Header.Set("Origin", "chrome-extension://abc")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rw.Code)
	}
	if got := rw.Header().Get("Access-Control-Allow-Origin"); got != "chrome-extension://abc" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}

func TestCompanionStatusURLSessionIncludesCapabilitiesAndHistory(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "url", displayName: "URL", enabled: true, enabledSet: true, state: adapters.StateRunning}); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{
		Registry: reg,
		CompanionSession: fakeCompanionSession{status: core.SessionStatus{
			State:      core.StatePlaying,
			AdapterRef: "url:abc",
			Position:   90 * time.Second,
			Duration:   3 * time.Minute,
			StartedAt:  time.Date(2026, 5, 9, 1, 2, 3, 0, time.UTC),
		}},
		CompanionURL: fakeCompanionURL{
			lastDisplay: "example.com/video.mp4",
			history: []CompanionHistoryEntry{{
				ID:         "h_7f4c9e2b8a1d4c0aa9d3e6f124b8c2d1",
				Title:      "Example",
				URLDisplay: "example.com/video.mp4",
				LastPlayed: time.Date(2026, 5, 9, 1, 2, 3, 0, time.UTC),
			}},
		},
		CompanionDisplay: fakeCompanionDisplay{display: CompanionSessionDisplay{
			AdapterName:   "URL",
			Title:         "Example",
			SourceDisplay: "example.com/video.mp4",
			ResolvedVia:   "direct",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/companion/status", nil)
	req.Host = "bridge.lan:32500"
	req.Header.Set("Origin", "moz-extension://abc")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	session := got["session"].(map[string]any)
	if session["state"] != "playing" {
		t.Fatalf("state = %v, want playing", session["state"])
	}
	if session["adapter_name"] != "URL" || session["title"] != "Example" {
		t.Fatalf("session display = %#v", session)
	}
	if session["position_ms"] != float64(90000) || session["duration_ms"] != float64(180000) {
		t.Fatalf("session timing = %#v", session)
	}
	caps := session["capabilities"].(map[string]any)
	if caps["can_pause"] != true || caps["can_seek"] != true || caps["can_resume"] != false {
		t.Fatalf("capabilities = %#v", caps)
	}
	history := got["history"].([]any)
	first := history[0].(map[string]any)
	if first["id"] != "h_7f4c9e2b8a1d4c0aa9d3e6f124b8c2d1" {
		t.Fatalf("history id = %v", first["id"])
	}
	if first["url_display"] != "example.com/video.mp4" {
		t.Fatalf("history url_display = %v", first["url_display"])
	}
}

func TestCompanionStatusForeignSessionReadOnly(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "plex", displayName: "Plex", enabled: true, enabledSet: true, state: adapters.StateRunning}); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{
		Registry: reg,
		CompanionSession: fakeCompanionSession{status: core.SessionStatus{
			State:      core.StatePlaying,
			AdapterRef: "plex:machine",
		}},
		CompanionURL: fakeCompanionURL{},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/ui/companion/status", nil)
	req.Header.Set("Origin", "chrome-extension://abc")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	session := got["session"].(map[string]any)
	if session["adapter_name"] != "Plex" {
		t.Fatalf("adapter_name = %v, want Plex", session["adapter_name"])
	}
	caps := session["capabilities"].(map[string]any)
	for k, v := range caps {
		if v != false {
			t.Fatalf("foreign capability %s = %v, want false", k, v)
		}
	}
}
