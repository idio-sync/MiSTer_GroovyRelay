# Companion Extension Mini Remote Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the reviewed companion extension mini remote: a gated bridge JSON API under `/ui/companion/*`, URL adapter companion methods, and a PR2-aligned browser-extension popup.

**Architecture:** Land in three independently testable slices. First add the companion read surface and stable URL history ids. Second add mutating companion routes by factoring existing URL adapter behavior into JSON-safe methods. Third migrate the extension client, popup, options test, context menus, local fonts, and styling onto the companion API.

**Tech Stack:** Go 1.26, `net/http` Go 1.22 method-aware mux, existing `internal/ui` HTML server patterns, existing URL adapter controls/history, plain browser-extension HTML/CSS/JS, Vitest/MSW/jsdom.

---

## Baseline

Created isolated worktree:

```bash
git worktree add .worktrees/companion-mini-remote -b feat/companion-mini-remote
```

Fresh focused Go baseline in the worktree:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui ./internal/adapters/url ./internal/core
```

Expected baseline result:

```text
ok  	github.com/idio-sync/MiSTer_GroovyRelay/internal/ui
ok  	github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url
ok  	github.com/idio-sync/MiSTer_GroovyRelay/internal/core
```

Extension baseline requires dependencies inside this worktree:

```bash
cd extension/firefox
cmd.exe /c npm install
cmd.exe /c npm test
```

## File Structure

- Modify `internal/ui/server.go`: add companion dependencies to `Config`, mount companion routes with a gate-only helper.
- Create `internal/ui/companion.go`: companion interfaces, JSON response structs, gate, route helpers, status and mutating handlers.
- Create `internal/ui/companion_test.go`: gate tests, status route tests, mutating route tests.
- Modify `internal/adapters/url/history.go`: stable opaque ids, backfill save-on-load, id lookup/delete helpers.
- Create `internal/adapters/url/companion.go`: `CompanionURLSource` and `CompanionDisplayProvider` methods for the URL adapter.
- Modify `internal/adapters/url/history_test.go`: stable id generation, persistence, backfill, id lookup/delete, reorder safety.
- Modify `internal/adapters/url/controls_test.go` and `internal/adapters/url/play_test.go`: companion method coverage for play/control/history responses.
- Modify `cmd/mister-groovy-relay/main.go`: wire `CompanionSession`, `CompanionURL`, and `CompanionDisplay`.
- Modify `extension/firefox/src/lib/bridge.js`: companion API client and shared error formatter.
- Modify `extension/firefox/src/popup/popup.html`: adaptive shell markup.
- Modify `extension/firefox/src/popup/popup.css`: PR2-aligned popup-local tokens, fonts, motion, controls.
- Modify `extension/firefox/src/popup/popup.js`: popup state machine, polling, command handlers, stale-snapshot handling.
- Modify `extension/firefox/src/background.js`: context menus call companion play and shared error formatter.
- Modify `extension/firefox/src/options/options.js`: connection test calls companion status.
- Modify `extension/firefox/test/*.test.js`: bridge client, popup, background, options, manifest/release coverage.
- Copy font files from `internal/ui/static/fonts/` to `extension/firefox/src/fonts/`.
- Modify `THIRD_PARTY_NOTICES.md` if font license coverage is not already documented for the extension package.

---

### Task 1: Companion Interfaces, Gate, History IDs, And GET Status

**Files:**
- Create: `internal/ui/companion.go`
- Create: `internal/ui/companion_test.go`
- Create: `internal/adapters/url/companion.go`
- Modify: `internal/ui/server.go`
- Modify: `internal/adapters/url/history.go`
- Modify: `internal/adapters/url/history_test.go`
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Write failing history id tests**

Add these tests to `internal/adapters/url/history_test.go`:

```go
func TestHistory_AddOrBumpAssignsStableOpaqueID(t *testing.T) {
	h := LoadHistory("")
	h.AddOrBump("https://example.com/a")
	first := h.List()[0]
	if !strings.HasPrefix(first.ID, "h_") {
		t.Fatalf("ID = %q, want h_ prefix", first.ID)
	}
	if len(first.ID) != 34 {
		t.Fatalf("ID length = %d, want 34", len(first.ID))
	}
	h.AddOrBump("https://example.com/a")
	second := h.List()[0]
	if second.ID != first.ID {
		t.Fatalf("bump changed ID: before=%q after=%q", first.ID, second.ID)
	}
}

func TestHistory_LoadBackfillsIDsAndSavesOnce(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "h.json")
	raw := `{"version":1,"entries":[{"url":"https://example.com/a","last_played_at":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(tmp, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	h := LoadHistory(tmp)
	list := h.List()
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if !strings.HasPrefix(list[0].ID, "h_") {
		t.Fatalf("backfilled ID = %q, want h_ prefix", list[0].ID)
	}

	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var hf historyFile
	if err := json.Unmarshal(data, &hf); err != nil {
		t.Fatal(err)
	}
	if hf.Entries[0].ID != list[0].ID {
		t.Fatalf("saved ID = %q, want %q", hf.Entries[0].ID, list[0].ID)
	}
}

func TestHistory_GetAndRemoveByIDSurviveReorder(t *testing.T) {
	h := LoadHistory("")
	h.AddOrBump("https://example.com/a")
	h.AddOrBump("https://example.com/b")
	older := h.List()[1]
	h.AddOrBump("https://example.com/a")

	got, ok := h.GetByID(older.ID)
	if !ok {
		t.Fatalf("GetByID(%q) returned false", older.ID)
	}
	if got.URL != "https://example.com/a" {
		t.Fatalf("GetByID URL = %q, want older entry URL", got.URL)
	}
	if !h.RemoveByID(older.ID) {
		t.Fatalf("RemoveByID(%q) returned false", older.ID)
	}
	for _, e := range h.List() {
		if e.ID == older.ID {
			t.Fatalf("removed ID still present in history: %+v", e)
		}
	}
}
```

Update the import block in `history_test.go` to include `strings`.

- [ ] **Step 2: Run history tests and confirm they fail**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url -run "TestHistory_(AddOrBumpAssignsStableOpaqueID|LoadBackfillsIDsAndSavesOnce|GetAndRemoveByIDSurviveReorder)"
```

Expected: fail because `HistoryEntry.ID`, `GetByID`, and `RemoveByID` do not exist.

- [ ] **Step 3: Implement stable history ids**

In `internal/adapters/url/history.go`, extend `HistoryEntry`:

```go
type HistoryEntry struct {
	ID           string    `json:"id,omitempty"`
	URL          string    `json:"url"`
	LastPlayedAt time.Time `json:"last_played_at"`
	Title        string    `json:"title,omitempty"`
}
```

Add helpers near the history constants:

```go
func newHistoryID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("url history id entropy unavailable: " + err.Error())
	}
	return "h_" + hex.EncodeToString(b[:])
}

func validHistoryID(id string) bool {
	if len(id) != 34 || !strings.HasPrefix(id, "h_") {
		return false
	}
	_, err := hex.DecodeString(id[2:])
	return err == nil
}
```

Add imports:

```go
import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	stdurl "net/url"
	"os"
	"strings"
	"sync"
	"time"
)
```

Inside `LoadHistory`, after dedupe/drop/truncate and before assigning `h.entries`, backfill IDs and save while the history is not yet reachable by readers:

```go
	backfilled := false
	for i := range deduped {
		if !validHistoryID(deduped[i].ID) {
			deduped[i].ID = newHistoryID()
			backfilled = true
		}
	}
	h.mu.Lock()
	h.entries = deduped
	if backfilled {
		h.saveLocked()
	}
	h.mu.Unlock()
	return h
```

In `AddOrBump`, carry the existing ID across reorders and create one for new rows:

```go
	var carriedID string
	var carriedTitle string
	for i, e := range h.entries {
		if dedupeKey(e.URL) == key {
			carriedID = e.ID
			carriedTitle = e.Title
			h.entries = append(h.entries[:i], h.entries[i+1:]...)
			break
		}
	}
	if !validHistoryID(carriedID) {
		carriedID = newHistoryID()
	}

	h.entries = append([]HistoryEntry{{
		ID:           carriedID,
		URL:          rawURL,
		LastPlayedAt: now,
		Title:        carriedTitle,
	}}, h.entries...)
```

Add id helpers below `Get`:

```go
func (h *History) GetByID(id string) (HistoryEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.entries {
		if e.ID == id {
			return e, true
		}
	}
	return HistoryEntry{}, false
}

func (h *History) RemoveByID(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, e := range h.entries {
		if e.ID == id {
			h.entries = append(h.entries[:i], h.entries[i+1:]...)
			h.saveLocked()
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run history tests and commit history id slice**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/adapters/url/history.go internal/adapters/url/history_test.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url -run "TestHistory_"
```

Expected: pass.

Commit:

```bash
git add internal/adapters/url/history.go internal/adapters/url/history_test.go
git commit -m "feat(url): add stable history ids"
```

- [ ] **Step 5: Write failing companion gate and status tests**

First extend `uiStubAdapter` in `internal/ui/sidebar_test.go` so companion tests can control display and enabled state while existing tests keep their current behavior:

```go
type uiStubAdapter struct {
	name        string
	displayName string
	enabled     bool
	enabledSet  bool
	state       adapters.State
}

func (a *uiStubAdapter) Name() string { return a.name }
func (a *uiStubAdapter) DisplayName() string {
	if a.displayName != "" {
		return a.displayName
	}
	return a.name
}
func (a *uiStubAdapter) IsEnabled() bool {
	if a.enabledSet {
		return a.enabled
	}
	return true
}
```

Leave its existing `Fields`, `DecodeConfig`, `Start`, `Stop`, `Status`, and `ApplyConfig` methods in place.

Create `internal/ui/companion_test.go` with test fakes that satisfy the new companion interfaces:

```go
type fakeCompanionSession struct {
	status core.SessionStatus
}

func (f fakeCompanionSession) Status() core.SessionStatus { return f.status }

type fakeCompanionURL struct {
	history     []CompanionHistoryEntry
	lastDisplay string
}

func (f fakeCompanionURL) CompanionPlay(context.Context, string, string) (CompanionPlayResult, error) {
	return CompanionPlayResult{}, errors.New("unused")
}
func (f fakeCompanionURL) CompanionPause(context.Context) error { return errors.New("unused") }
func (f fakeCompanionURL) CompanionResume(context.Context) error { return errors.New("unused") }
func (f fakeCompanionURL) CompanionStop(context.Context) error { return errors.New("unused") }
func (f fakeCompanionURL) CompanionReplay(context.Context) (CompanionPlayResult, error) {
	return CompanionPlayResult{}, errors.New("unused")
}
func (f fakeCompanionURL) CompanionSeek(context.Context, int) error { return errors.New("unused") }
func (f fakeCompanionURL) CompanionHistory() []CompanionHistoryEntry { return f.history }
func (f fakeCompanionURL) CompanionHistoryPlay(context.Context, string) (CompanionPlayResult, error) {
	return CompanionPlayResult{}, errors.New("unused")
}
func (f fakeCompanionURL) CompanionHistoryDelete(context.Context, string) error { return errors.New("unused") }
func (f fakeCompanionURL) CompanionLastURLDisplay() string { return f.lastDisplay }

type fakeCompanionDisplay struct {
	display CompanionSessionDisplay
}

func (f fakeCompanionDisplay) CompanionDisplay(string) CompanionSessionDisplay { return f.display }
```

Add tests:

```go
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
				ID: "h_7f4c9e2b8a1d4c0aa9d3e6f124b8c2d1",
				Title: "Example",
				URLDisplay: "example.com/video.mp4",
				LastPlayed: time.Date(2026, 5, 9, 1, 2, 3, 0, time.UTC),
			}},
		},
		CompanionDisplay: fakeCompanionDisplay{display: CompanionSessionDisplay{
			AdapterName: "URL",
			Title: "Example",
			SourceDisplay: "example.com/video.mp4",
			ResolvedVia: "direct",
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
	caps := session["capabilities"].(map[string]any)
	if caps["can_pause"] != true || caps["can_seek"] != true || caps["can_resume"] != false {
		t.Fatalf("capabilities = %#v", caps)
	}
	history := got["history"].([]any)
	first := history[0].(map[string]any)
	if first["id"] != "h_7f4c9e2b8a1d4c0aa9d3e6f124b8c2d1" {
		t.Fatalf("history id = %v", first["id"])
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
			State: core.StatePlaying,
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
	caps := got["session"].(map[string]any)["capabilities"].(map[string]any)
	for k, v := range caps {
		if v != false {
			t.Fatalf("foreign capability %s = %v, want false", k, v)
		}
	}
}
```

- [ ] **Step 6: Run companion UI tests and confirm they fail**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -run "TestCompanion"
```

Expected: fail because `Companion*` types, `companionExtensionGate`, and route registration do not exist.

- [ ] **Step 7: Implement companion interfaces, gate, and status route**

Create `internal/ui/companion.go` with:

```go
package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type CompanionSessionProvider interface {
	Status() core.SessionStatus
}

type CompanionPlayResult struct {
	AdapterRef  string
	ResolvedVia string
}

type CompanionError interface {
	error
	HTTPStatus() int
}

type CompanionHistoryEntry struct {
	ID         string
	Title      string
	URLDisplay string
	LastPlayed time.Time
}

type CompanionSessionDisplay struct {
	AdapterName    string
	Title          string
	SourceDisplay  string
	ResolvedVia    string
}

type CompanionURLSource interface {
	CompanionPlay(ctx context.Context, rawURL, mode string) (CompanionPlayResult, error)
	CompanionPause(ctx context.Context) error
	CompanionResume(ctx context.Context) error
	CompanionStop(ctx context.Context) error
	CompanionReplay(ctx context.Context) (CompanionPlayResult, error)
	CompanionSeek(ctx context.Context, offsetMs int) error
	CompanionHistory() []CompanionHistoryEntry
	CompanionHistoryPlay(ctx context.Context, id string) (CompanionPlayResult, error)
	CompanionHistoryDelete(ctx context.Context, id string) error
	CompanionLastURLDisplay() string
}

type CompanionDisplayProvider interface {
	CompanionDisplay(adapterRef string) CompanionSessionDisplay
}
```

Add JSON response structs in the same file:

```go
type companionStatusResponse struct {
	Configured bool                       `json:"configured"`
	BridgeURL  string                     `json:"bridge_url,omitempty"`
	Session    *companionSessionResponse  `json:"session,omitempty"`
	Health     companionHealthResponse    `json:"health"`
	History    []companionHistoryResponse `json:"history,omitempty"`
}

type companionSessionResponse struct {
	State         core.State                      `json:"state"`
	AdapterRef    string                          `json:"adapter_ref,omitempty"`
	AdapterName   string                          `json:"adapter_name,omitempty"`
	Title         string                          `json:"title,omitempty"`
	SourceDisplay string                          `json:"source_display,omitempty"`
	ResolvedVia   string                          `json:"resolved_via,omitempty"`
	PositionMS    int64                           `json:"position_ms,omitempty"`
	DurationMS    int64                           `json:"duration_ms,omitempty"`
	StartedAt     string                          `json:"started_at,omitempty"`
	Capabilities  companionCapabilitiesResponse   `json:"capabilities"`
}

type companionCapabilitiesResponse struct {
	CanPause  bool `json:"can_pause"`
	CanResume bool `json:"can_resume"`
	CanStop   bool `json:"can_stop"`
	CanReplay bool `json:"can_replay"`
	CanSeek   bool `json:"can_seek"`
}

type companionHealthResponse struct {
	Bridge     string `json:"bridge"`
	Mister     string `json:"mister"`
	URLAdapter string `json:"url_adapter"`
}

type companionHistoryResponse struct {
	ID         string `json:"id"`
	Title      string `json:"title,omitempty"`
	URLDisplay string `json:"url_display"`
	LastPlayed string `json:"last_played"`
}
```

Add gate and JSON helpers:

```go
func companionExtensionGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			handleExtensionCORSPreflight(w, r)
			return
		}
		setExtensionCORSHeaders(w, r)
		if !isExtensionOrigin(r.Header.Get("Origin")) || r.Header.Get("X-Bridge-Extension") != "1" {
			writeCompanionError(w, http.StatusForbidden, "companion extension origin and header required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeCompanionJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeCompanionError(w http.ResponseWriter, status int, msg string) {
	writeCompanionJSON(w, status, map[string]any{"ok": false, "error": msg})
}

func companionHTTPStatus(err error) int {
	var ce CompanionError
	if errors.As(err, &ce) {
		return ce.HTTPStatus()
	}
	return http.StatusInternalServerError
}
```

Add route mounting:

```go
func (s *Server) mountCompanion(mux *http.ServeMux, method, pattern string, handler http.HandlerFunc) {
	mux.Handle(method+" "+pattern, companionExtensionGate(handler))
}
```

In `Server.Mount`, before the shell catch-all `/ui/` route, add:

```go
	mux.Handle("OPTIONS /ui/companion/", companionExtensionGate(http.NotFoundHandler()))
	s.mountCompanion(mux, "GET", "/ui/companion/status", s.handleCompanionStatus)
```

Add status builder:

```go
func (s *Server) handleCompanionStatus(w http.ResponseWriter, r *http.Request) {
	resp := companionStatusResponse{
		Configured: true,
		BridgeURL:  companionBridgeURL(r),
		Health: companionHealthResponse{
			Bridge:     "online",
			Mister:     "unknown",
			URLAdapter: s.companionAdapterHealth("url"),
		},
	}
	if s.cfg.CompanionURL != nil {
		for _, e := range s.cfg.CompanionURL.CompanionHistory() {
			resp.History = append(resp.History, companionHistoryResponse{
				ID:         e.ID,
				Title:      e.Title,
				URLDisplay: e.URLDisplay,
				LastPlayed: e.LastPlayed.UTC().Format(time.RFC3339),
			})
		}
	}
	if s.cfg.CompanionSession != nil {
		st := s.cfg.CompanionSession.Status()
		resp.Session = s.companionSession(st)
	}
	writeCompanionJSON(w, http.StatusOK, resp)
}

func companionBridgeURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if r.Host == "" {
		return ""
	}
	return scheme + "://" + r.Host
}

func (s *Server) companionSession(st core.SessionStatus) *companionSessionResponse {
	if st.State == "" {
		st.State = core.StateIdle
	}
	out := &companionSessionResponse{State: st.State}
	if st.AdapterRef != "" {
		out.AdapterRef = st.AdapterRef
		out.AdapterName = s.companionAdapterName(st.AdapterRef)
	}
	if st.Position > 0 {
		out.PositionMS = int64(st.Position / time.Millisecond)
	}
	if st.Duration > 0 {
		out.DurationMS = int64(st.Duration / time.Millisecond)
	}
	if !st.StartedAt.IsZero() {
		out.StartedAt = st.StartedAt.UTC().Format(time.RFC3339)
	}
	if s.cfg.CompanionDisplay != nil && st.AdapterRef != "" {
		d := s.cfg.CompanionDisplay.CompanionDisplay(st.AdapterRef)
		if d.AdapterName != "" {
			out.AdapterName = d.AdapterName
		}
		out.Title = d.Title
		out.SourceDisplay = d.SourceDisplay
		out.ResolvedVia = d.ResolvedVia
	}
	out.Capabilities = s.companionCapabilities(st)
	return out
}
```

Add capability/health helpers:

```go
func (s *Server) companionCapabilities(st core.SessionStatus) companionCapabilitiesResponse {
	if !strings.HasPrefix(st.AdapterRef, "url:") || s.cfg.CompanionURL == nil {
		return companionCapabilitiesResponse{}
	}
	return companionCapabilitiesResponse{
		CanPause:  st.State == core.StatePlaying,
		CanResume: st.State == core.StatePaused,
		CanStop:   st.State == core.StatePlaying || st.State == core.StatePaused,
		CanReplay: s.cfg.CompanionURL.CompanionLastURLDisplay() != "",
		CanSeek:   st.Duration > 0 && (st.State == core.StatePlaying || st.State == core.StatePaused),
	}
}

func (s *Server) companionAdapterName(adapterRef string) string {
	name := adapterRef
	if i := strings.Index(adapterRef, ":"); i >= 0 {
		name = adapterRef[:i]
	}
	if a, ok := s.cfg.Registry.Get(name); ok {
		return a.DisplayName()
	}
	return name
}

func (s *Server) companionAdapterHealth(name string) string {
	a, ok := s.cfg.Registry.Get(name)
	if !ok {
		return "unknown"
	}
	if !a.IsEnabled() {
		return "disabled"
	}
	switch a.Status().State {
	case adapters.StateRunning:
		return "enabled"
	case adapters.StateStarting:
		return "starting"
	case adapters.StateError:
		return "error"
	default:
		return "disabled"
	}
}
```

- [ ] **Step 8: Extend `ui.Config`**

In `internal/ui/server.go`, add fields:

```go
type Config struct {
	Registry          *adapters.Registry
	BridgeSaver       BridgeSaver
	AdapterSaver      AdapterSaver
	MisterLauncher    MisterLauncher
	CompanionSession  CompanionSessionProvider
	CompanionURL      CompanionURLSource
	CompanionDisplay  CompanionDisplayProvider
}
```

- [ ] **Step 9: Implement URL adapter read-only companion methods**

Create `internal/adapters/url/companion.go`:

```go
package url

import (
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

func (a *Adapter) CompanionHistory() []ui.CompanionHistoryEntry {
	list := a.history.List()
	out := make([]ui.CompanionHistoryEntry, 0, len(list))
	for _, e := range list {
		out = append(out, ui.CompanionHistoryEntry{
			ID:         e.ID,
			Title:      e.Title,
			URLDisplay: redactURL(e.URL),
			LastPlayed: e.LastPlayedAt,
		})
	}
	return out
}

func (a *Adapter) CompanionLastURLDisplay() string {
	last := a.snapshotLastURL()
	if last == "" {
		return ""
	}
	return redactURL(last)
}

func (a *Adapter) CompanionDisplay(adapterRef string) ui.CompanionSessionDisplay {
	if !strings.HasPrefix(adapterRef, "url:") {
		return ui.CompanionSessionDisplay{}
	}
	last := a.snapshotLastURL()
	display := ui.CompanionSessionDisplay{
		AdapterName:   "URL",
		SourceDisplay: redactURL(last),
	}
	for _, e := range a.history.List() {
		if dedupeKey(e.URL) == dedupeKey(last) {
			display.Title = e.Title
			break
		}
	}
	return display
}
```

- [ ] **Step 10: Wire production dependencies**

In `cmd/mister-groovy-relay/main.go`, update `ui.New`:

```go
	uiSrv, err := ui.New(ui.Config{
		Registry:         reg,
		BridgeSaver:      saver,
		AdapterSaver:     adapterSaver,
		MisterLauncher:   misterLauncher,
		CompanionSession: coreMgr,
		CompanionURL:     urlAdapter,
		CompanionDisplay: urlAdapter,
	})
```

- [ ] **Step 11: Verify Task 1**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/ui/server.go internal/ui/companion.go internal/ui/companion_test.go internal/adapters/url/companion.go cmd/mister-groovy-relay/main.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui ./internal/adapters/url ./cmd/mister-groovy-relay
```

Expected: pass.

Commit:

```bash
git add internal/ui/server.go internal/ui/companion.go internal/ui/companion_test.go internal/adapters/url/companion.go cmd/mister-groovy-relay/main.go
git commit -m "feat(ui): add companion status endpoint"
```

---

### Task 2: URL Companion Methods And Mutating JSON Routes

**Files:**
- Modify: `internal/adapters/url/companion.go`
- Modify: `internal/adapters/url/controls.go`
- Modify: `internal/adapters/url/controls_test.go`
- Modify: `internal/ui/companion.go`
- Modify: `internal/ui/companion_test.go`

- [ ] **Step 1: Write failing URL companion method tests**

Add to `internal/adapters/url/controls_test.go`:

```go
func TestCompanionPause_URLSessionCallsPause(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc"})
	a := newTestAdapter(t, fc)
	if err := a.CompanionPause(context.Background()); err != nil {
		t.Fatalf("CompanionPause error = %v", err)
	}
	if !fc.pauseCalled {
		t.Fatal("Pause was not called")
	}
}

func TestCompanionPause_ForeignSessionReturns409(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StatePlaying, AdapterRef: "plex:abc"})
	a := newTestAdapter(t, fc)
	err := a.CompanionPause(context.Background())
	var ce interface{ HTTPStatus() int }
	if !errors.As(err, &ce) || ce.HTTPStatus() != http.StatusConflict {
		t.Fatalf("error = %v, want companion 409", err)
	}
	if fc.pauseCalled {
		t.Fatal("Pause called for foreign session")
	}
}

func TestCompanionSeekAbsoluteClampsAndCallsSeekTo(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State: core.StatePlaying,
		AdapterRef: "url:abc",
		Duration: time.Minute,
	})
	a := newTestAdapter(t, fc)
	if err := a.CompanionSeek(context.Background(), 90_000); err != nil {
		t.Fatalf("CompanionSeek error = %v", err)
	}
	if fc.seekOffsetMs != 60_000 {
		t.Fatalf("seek offset = %d, want duration clamp 60000", fc.seekOffsetMs)
	}
}

func TestCompanionHistoryPlayUsesID(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	a.history.AddOrBump("https://example.com/a")
	id := a.history.List()[0].ID
	res, err := a.CompanionHistoryPlay(context.Background(), id)
	if err != nil {
		t.Fatalf("CompanionHistoryPlay error = %v", err)
	}
	if res.AdapterRef == "" || res.ResolvedVia != "direct" {
		t.Fatalf("result = %+v", res)
	}
	if fc.lastReq.StreamURL != "https://example.com/a" {
		t.Fatalf("StreamURL = %q", fc.lastReq.StreamURL)
	}
}

func TestCompanionHistoryDeleteUnknownIDReturns404(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	err := a.CompanionHistoryDelete(context.Background(), "h_00000000000000000000000000000000")
	var ce interface{ HTTPStatus() int }
	if !errors.As(err, &ce) || ce.HTTPStatus() != http.StatusNotFound {
		t.Fatalf("error = %v, want companion 404", err)
	}
}
```

Add imports `context`, `net/http`, and `errors` if missing.

- [ ] **Step 2: Run URL companion tests and confirm they fail**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url -run "TestCompanion"
```

Expected: fail because companion mutating methods do not exist.

- [ ] **Step 3: Implement URL companion error contract and methods**

In `internal/adapters/url/companion.go`, add:

```go
type companionHTTPError struct {
	status int
	msg    string
}

func (e companionHTTPError) Error() string { return e.msg }
func (e companionHTTPError) HTTPStatus() int { return e.status }

func companionErr(status int, err error) error {
	if err == nil {
		return nil
	}
	return companionHTTPError{status: status, msg: err.Error()}
}

func companionMsg(status int, msg string) error {
	return companionHTTPError{status: status, msg: msg}
}

func foreignSession(st core.SessionStatus) bool {
	return st.AdapterRef != "" && !strings.HasPrefix(st.AdapterRef, "url:")
}
```

Add imports `context`, `fmt`, `net/http`, `time`, and `github.com/idio-sync/MiSTer_GroovyRelay/internal/core`.

Add methods:

```go
func (a *Adapter) CompanionPlay(ctx context.Context, rawURL, mode string) (ui.CompanionPlayResult, error) {
	ref, resolvedVia, status, err := a.castURL(ctx, rawURL, mode)
	if err != nil {
		return ui.CompanionPlayResult{}, companionErr(status, err)
	}
	return ui.CompanionPlayResult{AdapterRef: ref, ResolvedVia: resolvedVia}, nil
}

func (a *Adapter) CompanionPause(ctx context.Context) error {
	st := a.core.Status()
	if foreignSession(st) {
		return companionMsg(http.StatusConflict, "active session belongs to another adapter")
	}
	if st.State == core.StatePaused {
		return nil
	}
	if err := a.core.Pause(); err != nil {
		return companionErr(http.StatusConflict, err)
	}
	return nil
}

func (a *Adapter) CompanionResume(ctx context.Context) error {
	st := a.core.Status()
	if foreignSession(st) {
		return companionMsg(http.StatusConflict, "active session belongs to another adapter")
	}
	if st.State == core.StatePlaying {
		return nil
	}
	if st.Duration > 0 {
		if err := a.core.Play(); err != nil {
			return companionMsg(http.StatusConflict, redactErr(err, a.snapshotLastURL()))
		}
		return nil
	}
	lastURL := a.snapshotLastURL()
	if lastURL == "" {
		return companionMsg(http.StatusBadRequest, "no URL to resume")
	}
	_, _, status, err := a.castURL(ctx, lastURL, "auto")
	if err != nil {
		return companionErr(status, err)
	}
	return nil
}

func (a *Adapter) CompanionStop(ctx context.Context) error {
	st := a.core.Status()
	if foreignSession(st) {
		return companionMsg(http.StatusConflict, "active session belongs to another adapter")
	}
	if err := a.core.Stop(); err != nil {
		return companionErr(http.StatusConflict, err)
	}
	return nil
}

func (a *Adapter) CompanionReplay(ctx context.Context) (ui.CompanionPlayResult, error) {
	st := a.core.Status()
	if foreignSession(st) {
		return ui.CompanionPlayResult{}, companionMsg(http.StatusConflict, "active session belongs to another adapter")
	}
	lastURL := a.snapshotLastURL()
	if lastURL == "" {
		return ui.CompanionPlayResult{}, companionMsg(http.StatusBadRequest, "no URL to replay")
	}
	ref, resolvedVia, status, err := a.castURL(ctx, lastURL, "auto")
	if err != nil {
		return ui.CompanionPlayResult{}, companionErr(status, err)
	}
	return ui.CompanionPlayResult{AdapterRef: ref, ResolvedVia: resolvedVia}, nil
}

func (a *Adapter) CompanionSeek(ctx context.Context, offsetMs int) error {
	st := a.core.Status()
	if foreignSession(st) {
		return companionMsg(http.StatusConflict, "active session belongs to another adapter")
	}
	if st.Duration <= 0 {
		return companionMsg(http.StatusConflict, "source not seekable")
	}
	durMs := int(st.Duration / time.Millisecond)
	if offsetMs < 0 {
		offsetMs = 0
	}
	if offsetMs > durMs {
		offsetMs = durMs
	}
	if err := a.core.SeekTo(offsetMs); err != nil {
		return companionMsg(http.StatusConflict, redactErr(err, a.snapshotLastURL()))
	}
	return nil
}

func (a *Adapter) CompanionHistoryPlay(ctx context.Context, id string) (ui.CompanionPlayResult, error) {
	entry, ok := a.history.GetByID(id)
	if !ok {
		return ui.CompanionPlayResult{}, companionMsg(http.StatusNotFound, "history entry no longer exists")
	}
	ref, resolvedVia, status, err := a.castURL(ctx, entry.URL, "auto")
	if err != nil {
		return ui.CompanionPlayResult{}, companionErr(status, err)
	}
	return ui.CompanionPlayResult{AdapterRef: ref, ResolvedVia: resolvedVia}, nil
}

func (a *Adapter) CompanionHistoryDelete(ctx context.Context, id string) error {
	if !a.history.RemoveByID(id) {
		return companionMsg(http.StatusNotFound, "history entry no longer exists")
	}
	return nil
}
```

Silence unused `ctx` warnings in methods that do not pass context by assigning `_ = ctx` at method start only where needed.

- [ ] **Step 4: Verify URL companion methods**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/adapters/url/companion.go internal/adapters/url/controls_test.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url -run "TestCompanion|TestPause|TestResume|TestStop|TestReplay|TestSeek|TestHistory"
```

Expected: pass.

Commit:

```bash
git add internal/adapters/url/companion.go internal/adapters/url/controls_test.go
git commit -m "feat(url): expose companion controls"
```

- [ ] **Step 5: Write failing companion route tests**

In `internal/ui/companion_test.go`, extend `fakeCompanionURL` with function fields:

```go
playFn          func(context.Context, string, string) (CompanionPlayResult, error)
pauseFn         func(context.Context) error
resumeFn        func(context.Context) error
stopFn          func(context.Context) error
replayFn        func(context.Context) (CompanionPlayResult, error)
seekFn          func(context.Context, int) error
historyPlayFn   func(context.Context, string) (CompanionPlayResult, error)
historyDeleteFn func(context.Context, string) error
```

Have each method call its function field when non-nil and otherwise return the existing unused error.

Add tests:

```go
func TestCompanionPlayRouteReturns202AndPlaying(t *testing.T) {
	var gotURL, gotMode string
	s, mux := newCompanionRouteServer(t, fakeCompanionURL{
		playFn: func(ctx context.Context, rawURL, mode string) (CompanionPlayResult, error) {
			gotURL, gotMode = rawURL, mode
			return CompanionPlayResult{AdapterRef: "url:abc", ResolvedVia: "direct"}, nil
		},
	})
	_ = s
	rw := companionJSONRequest(t, mux, http.MethodPost, "/ui/companion/play", `{"url":"https://example.com/v.mp4","mode":"auto"}`)
	if rw.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rw.Code, rw.Body.String())
	}
	if gotURL != "https://example.com/v.mp4" || gotMode != "auto" {
		t.Fatalf("called with url=%q mode=%q", gotURL, gotMode)
	}
	if strings.Contains(rw.Body.String(), `"url"`) {
		t.Fatalf("companion play response leaked url: %s", rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), `"state":"playing"`) {
		t.Fatalf("response missing playing state: %s", rw.Body.String())
	}
}

func TestCompanionPlayRouteRequiresJSONContentType(t *testing.T) {
	_, mux := newCompanionRouteServer(t, fakeCompanionURL{})
	req := httptest.NewRequest(http.MethodPost, "/ui/companion/play", strings.NewReader(`{"url":"https://example.com"}`))
	req.Header.Set("Origin", "moz-extension://abc")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rw.Code)
	}
}

func TestCompanionControlSeekDispatchesAbsoluteOffset(t *testing.T) {
	var gotOffset int
	_, mux := newCompanionRouteServer(t, fakeCompanionURL{
		seekFn: func(ctx context.Context, offsetMs int) error {
			gotOffset = offsetMs
			return nil
		},
	})
	rw := companionJSONRequest(t, mux, http.MethodPost, "/ui/companion/control", `{"action":"seek","offset_ms":90000}`)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	if gotOffset != 90000 {
		t.Fatalf("offset = %d, want absolute 90000", gotOffset)
	}
}

func TestCompanionHistoryPlayMissingIDReturns404(t *testing.T) {
	_, mux := newCompanionRouteServer(t, fakeCompanionURL{
		historyPlayFn: func(ctx context.Context, id string) (CompanionPlayResult, error) {
			return CompanionPlayResult{}, fakeCompanionHTTPError{status: http.StatusNotFound, msg: "history entry no longer exists"}
		},
	})
	rw := companionJSONRequest(t, mux, http.MethodPost, "/ui/companion/history/play", `{"id":"h_00000000000000000000000000000000"}`)
	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rw.Code)
	}
}

func TestCompanionLaunchEmptyBodyNoContentTypeAccepted(t *testing.T) {
	launcher := &fakeMisterLauncher{}
	_, mux := newCompanionRouteServerWithLauncher(t, fakeCompanionURL{}, launcher)
	req := httptest.NewRequest(http.MethodPost, "/ui/companion/launch", nil)
	req.Header.Set("Origin", "moz-extension://abc")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	if !launcher.called {
		t.Fatalf("launcher was not called")
	}
}
```

Add helper types:

```go
type fakeCompanionHTTPError struct {
	status int
	msg    string
}

func (e fakeCompanionHTTPError) Error() string { return e.msg }
func (e fakeCompanionHTTPError) HTTPStatus() int { return e.status }
```

Add `newCompanionRouteServer`, `newCompanionRouteServerWithLauncher`, and `companionJSONRequest` helpers that create a registry, server, mux, extension-origin request, and JSON content type.

- [ ] **Step 6: Run route tests and confirm they fail**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -run "TestCompanion.*Route|TestCompanion.*Launch|TestCompanion.*Control|TestCompanion.*History"
```

Expected: fail because POST companion routes are not mounted.

- [ ] **Step 7: Implement JSON body/content-type helpers and mutating routes**

In `internal/ui/companion.go`, add:

```go
func requireCompanionJSON(w http.ResponseWriter, r *http.Request) bool {
	ct := strings.TrimSpace(r.Header.Get("Content-Type"))
	if ct == "" {
		if r.ContentLength == 0 {
			return true
		}
		writeCompanionError(w, http.StatusUnsupportedMediaType, "Content-Type application/json required")
		return false
	}
	if mt := strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0])); mt != "application/json" {
		writeCompanionError(w, http.StatusUnsupportedMediaType, "Content-Type application/json required")
		return false
	}
	return true
}

func decodeCompanionJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !requireCompanionJSON(w, r) {
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeCompanionError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func (s *Server) companionURLRequired(w http.ResponseWriter) (CompanionURLSource, bool) {
	if s.cfg.CompanionURL == nil {
		writeCompanionError(w, http.StatusInternalServerError, "companion URL source not wired")
		return nil, false
	}
	return s.cfg.CompanionURL, true
}
```

Mount POST routes in `Server.Mount`:

```go
	s.mountCompanion(mux, "POST", "/ui/companion/play", s.handleCompanionPlay)
	s.mountCompanion(mux, "POST", "/ui/companion/control", s.handleCompanionControl)
	s.mountCompanion(mux, "POST", "/ui/companion/history/play", s.handleCompanionHistoryPlay)
	s.mountCompanion(mux, "POST", "/ui/companion/history/delete", s.handleCompanionHistoryDelete)
	s.mountCompanion(mux, "POST", "/ui/companion/launch", s.handleCompanionLaunch)
```

Add handlers:

```go
func (s *Server) handleCompanionPlay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  string `json:"url"`
		Mode string `json:"mode"`
	}
	if !decodeCompanionJSON(w, r, &req) {
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	if req.URL == "" {
		writeCompanionError(w, http.StatusBadRequest, "url is required")
		return
	}
	if req.Mode == "" {
		req.Mode = "auto"
	}
	src, ok := s.companionURLRequired(w)
	if !ok {
		return
	}
	res, err := src.CompanionPlay(r.Context(), req.URL, req.Mode)
	if err != nil {
		writeCompanionError(w, companionHTTPStatus(err), err.Error())
		return
	}
	writeCompanionJSON(w, http.StatusAccepted, map[string]any{
		"ok": true, "adapter_ref": res.AdapterRef, "state": core.StatePlaying, "resolved_via": res.ResolvedVia,
	})
}

func (s *Server) handleCompanionControl(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action   string `json:"action"`
		OffsetMS int    `json:"offset_ms"`
	}
	if !decodeCompanionJSON(w, r, &req) {
		return
	}
	src, ok := s.companionURLRequired(w)
	if !ok {
		return
	}
	var err error
	status := http.StatusOK
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "pause":
		err = src.CompanionPause(r.Context())
	case "resume":
		err = src.CompanionResume(r.Context())
	case "stop":
		err = src.CompanionStop(r.Context())
	case "replay":
		var res CompanionPlayResult
		res, err = src.CompanionReplay(r.Context())
		if err == nil {
			writeCompanionJSON(w, http.StatusAccepted, map[string]any{
				"ok": true, "adapter_ref": res.AdapterRef, "state": core.StatePlaying, "resolved_via": res.ResolvedVia,
			})
			return
		}
	case "seek":
		err = src.CompanionSeek(r.Context(), req.OffsetMS)
	default:
		writeCompanionError(w, http.StatusBadRequest, "unsupported action")
		return
	}
	if err != nil {
		writeCompanionError(w, companionHTTPStatus(err), err.Error())
		return
	}
	state := core.StateIdle
	if s.cfg.CompanionSession != nil {
		state = s.cfg.CompanionSession.Status().State
	}
	writeCompanionJSON(w, status, map[string]any{"ok": true, "state": state})
}
```

Add history and launch handlers with the same JSON envelope:

```go
func (s *Server) handleCompanionHistoryPlay(w http.ResponseWriter, r *http.Request) {
	var req struct{ ID string `json:"id"` }
	if !decodeCompanionJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeCompanionError(w, http.StatusBadRequest, "id is required")
		return
	}
	src, ok := s.companionURLRequired(w)
	if !ok {
		return
	}
	res, err := src.CompanionHistoryPlay(r.Context(), req.ID)
	if err != nil {
		writeCompanionError(w, companionHTTPStatus(err), err.Error())
		return
	}
	writeCompanionJSON(w, http.StatusAccepted, map[string]any{
		"ok": true, "adapter_ref": res.AdapterRef, "state": core.StatePlaying, "resolved_via": res.ResolvedVia,
	})
}

func (s *Server) handleCompanionHistoryDelete(w http.ResponseWriter, r *http.Request) {
	var req struct{ ID string `json:"id"` }
	if !decodeCompanionJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeCompanionError(w, http.StatusBadRequest, "id is required")
		return
	}
	src, ok := s.companionURLRequired(w)
	if !ok {
		return
	}
	if err := src.CompanionHistoryDelete(r.Context(), req.ID); err != nil {
		writeCompanionError(w, companionHTTPStatus(err), err.Error())
		return
	}
	writeCompanionJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCompanionLaunch(w http.ResponseWriter, r *http.Request) {
	if !requireCompanionJSON(w, r) {
		return
	}
	if s.cfg.MisterLauncher == nil {
		writeCompanionError(w, http.StatusInternalServerError, "mister launcher not wired")
		return
	}
	if err := s.cfg.MisterLauncher.Launch(r.Context()); err != nil {
		writeCompanionError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeCompanionJSON(w, http.StatusOK, map[string]any{"ok": true})
}
```

- [ ] **Step 8: Verify Task 2**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/ui/companion.go internal/ui/companion_test.go internal/adapters/url/companion.go internal/adapters/url/controls_test.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui ./internal/adapters/url
```

Expected: pass.

Commit:

```bash
git add internal/ui/companion.go internal/ui/companion_test.go internal/adapters/url/companion.go internal/adapters/url/controls_test.go
git commit -m "feat(ui): add companion control routes"
```

---

### Task 3: Extension Mini Remote, Fonts, And Context Menu Migration

**Files:**
- Modify: `extension/firefox/src/lib/bridge.js`
- Modify: `extension/firefox/src/popup/popup.html`
- Modify: `extension/firefox/src/popup/popup.css`
- Modify: `extension/firefox/src/popup/popup.js`
- Modify: `extension/firefox/src/background.js`
- Modify: `extension/firefox/src/options/options.js`
- Modify: `extension/firefox/test/bridge.test.js`
- Modify: `extension/firefox/test/popup.test.js`
- Modify: `extension/firefox/test/options.test.js`
- Modify: `extension/firefox/test/manifest.test.js`
- Modify: `extension/firefox/test/release-workflow.test.js`
- Create: `extension/firefox/src/fonts/InterTight-400.woff2`
- Create: `extension/firefox/src/fonts/InterTight-500.woff2`
- Create: `extension/firefox/src/fonts/JetBrainsMono-400.woff2`
- Create: `extension/firefox/src/fonts/SpaceGrotesk-600.woff2`
- Modify: `THIRD_PARTY_NOTICES.md` if font notices are not already sufficient

- [ ] **Step 1: Copy popup font assets**

Run:

```bash
mkdir -p extension/firefox/src/fonts
cp internal/ui/static/fonts/InterTight-400.woff2 extension/firefox/src/fonts/InterTight-400.woff2
cp internal/ui/static/fonts/InterTight-500.woff2 extension/firefox/src/fonts/InterTight-500.woff2
cp internal/ui/static/fonts/JetBrainsMono-400.woff2 extension/firefox/src/fonts/JetBrainsMono-400.woff2
cp internal/ui/static/fonts/SpaceGrotesk-600.woff2 extension/firefox/src/fonts/SpaceGrotesk-600.woff2
```

If `THIRD_PARTY_NOTICES.md` does not already cover these font files, append:

```markdown
## Bundled UI Fonts

The bridge UI and companion extension bundle WOFF2 font assets for Space
Grotesk, Inter Tight, and JetBrains Mono. These fonts are distributed under
the SIL Open Font License 1.1 by their upstream projects. The bundled files
are used locally by the web UI and extension popup and are not fetched from a
remote font service at runtime.
```

- [ ] **Step 2: Write failing bridge client tests**

Update `extension/firefox/test/bridge.test.js` so `play()` posts to `/ui/companion/play` and the response does not expect a raw URL. Add tests for:

```js
it("GETs companion status with the extension header", async () => {
  let captured;
  server.use(
    http.get("http://192.168.1.50:32500/ui/companion/status", ({ request }) => {
      captured = { headers: Object.fromEntries(request.headers) };
      return HttpResponse.json({ configured: true, health: { bridge: "online", mister: "unknown", url_adapter: "enabled" } });
    })
  );
  await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
  const result = await getStatus();
  expect(result.ok).toBe(true);
  expect(captured.headers["x-bridge-extension"]).toBe("1");
});

it("POSTs control actions to the companion control route", async () => {
  let captured;
  server.use(
    http.post("http://192.168.1.50:32500/ui/companion/control", async ({ request }) => {
      captured = await request.json();
      return HttpResponse.json({ ok: true, state: "paused" });
    })
  );
  await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
  const result = await control("seek", { offset_ms: 90000 });
  expect(result).toEqual({ ok: true, state: "paused" });
  expect(captured).toEqual({ action: "seek", offset_ms: 90000 });
});

it("POSTs launch to the companion launch route", async () => {
  let captured;
  server.use(
    http.post("http://192.168.1.50:32500/ui/companion/launch", ({ request }) => {
      captured = { headers: Object.fromEntries(request.headers) };
      return HttpResponse.json({ ok: true });
    })
  );
  await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
  const result = await launchGroovyMister();
  expect(result).toEqual({ ok: true });
  expect(captured.headers["x-bridge-extension"]).toBe("1");
});
```

- [ ] **Step 3: Implement companion API client**

In `extension/firefox/src/lib/bridge.js`, replace endpoint-specific fetch duplication with a helper:

```js
async function companionFetch(path, options = {}) {
  const bridgeURL = await getBridgeURL();
  if (!bridgeURL) return { ok: false, error: "Bridge not configured" };

  const permission = await ensureBridgeHostPermission(bridgeURL);
  if (!permission.ok) return permission;

  const ctrl = new AbortController();
  const timeout = setTimeout(() => ctrl.abort(), timeoutMs);
  const headers = {
    "X-Bridge-Extension": "1",
    ...(options.body ? { "Content-Type": "application/json" } : {}),
    ...(options.headers || {}),
  };

  try {
    const res = await fetch(`${bridgeURL}${path}`, {
      method: options.method || "GET",
      headers,
      body: options.body ? JSON.stringify(options.body) : undefined,
      signal: ctrl.signal,
    });
    clearTimeout(timeout);
    const text = await res.text().catch(() => "");
    let data = {};
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = {};
      }
    }
    if (res.ok) return { ok: true, ...data };
    return { ok: false, status: res.status, error: data.error || `HTTP ${res.status}` };
  } catch (e) {
    clearTimeout(timeout);
    if (e.name === "AbortError") return { ok: false, error: "Bridge timed out" };
    return { ok: false, error: `Bridge unreachable: ${e.message}` };
  }
}
```

Export:

```js
export async function getStatus() {
  return companionFetch("/ui/companion/status");
}

export async function play(url, mode = "auto") {
  return companionFetch("/ui/companion/play", {
    method: "POST",
    body: { url, mode },
  });
}

export async function control(action, extra = {}) {
  return companionFetch("/ui/companion/control", {
    method: "POST",
    body: { action, ...extra },
  });
}

export async function historyPlay(id) {
  return companionFetch("/ui/companion/history/play", {
    method: "POST",
    body: { id },
  });
}

export async function historyDelete(id) {
  return companionFetch("/ui/companion/history/delete", {
    method: "POST",
    body: { id },
  });
}

export async function launchGroovyMister() {
  return companionFetch("/ui/companion/launch", { method: "POST" });
}
```

Keep `formatPlayError(result)` but make it call a shared formatter:

```js
export function formatBridgeError(result, fallback = "Command failed") {
  if (!result || result.ok) return "";
  if (!result.status || result.error === `HTTP ${result.status}`) {
    return result.error || fallback;
  }
  if (result.status >= 400 && result.status < 500) {
    return `Bridge rejected: ${result.error}`;
  }
  if (result.status >= 500) {
    return `${fallback}: ${result.error}`;
  }
  return result.error || `HTTP ${result.status}`;
}

export function formatPlayError(result) {
  return formatBridgeError(result, "Cast failed");
}
```

- [ ] **Step 4: Verify bridge client**

Run:

```bash
cd extension/firefox
cmd.exe /c npm test -- bridge.test.js
```

Expected: pass.

Commit:

```bash
git add extension/firefox/src/lib/bridge.js extension/firefox/test/bridge.test.js
git commit -m "feat(extension): use companion bridge client"
```

- [ ] **Step 5: Replace popup markup**

In `extension/firefox/src/popup/popup.html`, keep the same script tags and replace the popup body with stable ids:

```html
<div id="popup" class="popup-shell" data-state="loading">
  <section id="unconfigured" class="view" hidden>
    <div class="brand-row">
      <span class="brand">GroovyRelay</span>
      <span class="chip">SETUP</span>
    </div>
    <h1>Configure bridge</h1>
    <p class="empty">Bridge URL is not set.</p>
    <button type="button" id="configure" class="primary">Configure</button>
  </section>

  <section id="configured" class="view" hidden>
    <div class="brand-row">
      <span class="brand">GroovyRelay</span>
      <span id="state-chip" class="chip">LOADING</span>
    </div>

    <div id="active-view" class="remote-view" hidden>
      <p class="eyebrow">Now playing</p>
      <h1 id="media-title">Unknown source</h1>
      <p id="source-line" class="source-line"></p>
      <div id="progress-wrap" class="progress-wrap" hidden>
        <div class="time-row">
          <span id="position-label">0:00</span>
          <span id="duration-label">0:00</span>
        </div>
        <input id="seek" type="range" min="0" value="0" step="1000">
      </div>
      <div class="control-grid">
        <button type="button" id="pause">Pause</button>
        <button type="button" id="resume">Resume</button>
        <button type="button" id="stop">Stop</button>
        <button type="button" id="replay">Replay</button>
      </div>
    </div>

    <div id="idle-view" class="remote-view" hidden>
      <p class="eyebrow">Cast this tab</p>
      <div id="tab-url" class="tab-url"></div>
      <button type="button" id="cast" class="primary">Cast tab</button>
      <div class="health-grid">
        <div><span>Bridge</span><strong id="health-bridge">--</strong></div>
        <div><span>MiSTer</span><strong id="health-mister">--</strong></div>
        <div><span>URL</span><strong id="health-url">--</strong></div>
      </div>
    </div>

    <div id="history" class="history-list"></div>

    <div class="actions">
      <button type="button" id="launch-groovy">Launch GroovyMiSTer</button>
      <button type="button" id="open-webui">Open Web UI</button>
      <button type="button" id="open-options">Settings</button>
    </div>
    <div id="status" class="status" role="status" aria-live="polite"></div>
  </section>
</div>
```

- [ ] **Step 6: Implement popup state machine**

In `extension/firefox/src/popup/popup.js`, import new client functions:

```js
import {
  play,
  getBridgeURL,
  getStatus,
  control,
  historyPlay,
  historyDelete,
  launchGroovyMister,
  formatBridgeError,
  formatPlayError,
} from "../lib/bridge.js";
```

Use a state object:

```js
const POLL_MS = 2000;

function createState() {
  return {
    bridgeURL: "",
    activeTabURL: "",
    lastBridgeHost: "",
    snapshot: null,
    stale: true,
    commanding: false,
    pollTimer: null,
  };
}
```

Implement the open flow:

```js
export async function initPopup(doc = document) {
  const state = createState();
  state.bridgeURL = await getBridgeURL();
  bindStaticActions(doc, state);

  if (!state.bridgeURL) {
    showUnconfigured(doc);
    return;
  }

  showConfigured(doc);
  const [tabs] = await Promise.all([
    browser.tabs.query({ active: true, currentWindow: true }),
    refreshStatus(doc, state),
  ]);
  state.activeTabURL = tabs[0]?.url || "";
  render(doc, state);
  state.pollTimer = setInterval(() => {
    if (!state.commanding) refreshStatus(doc, state);
  }, POLL_MS);
}
```

Use snapshot freshness rules:

```js
async function refreshStatus(doc, state) {
  const result = await getStatus();
  if (!result.ok) {
    state.stale = true;
    setStatus(doc, "err", formatBridgeError(result, "Status failed"));
    render(doc, state);
    return;
  }
  const host = hostOf(result.bridge_url || state.bridgeURL);
  if (state.lastBridgeHost && host && host !== state.lastBridgeHost) {
    state.stale = true;
  } else {
    state.stale = false;
  }
  if (host) state.lastBridgeHost = host;
  state.snapshot = result;
  render(doc, state);
}

function hostOf(url) {
  try {
    return new URL(url).host;
  } catch {
    return "";
  }
}
```

Implement `runCommand(doc, state, fn, fallback)` so commands pause polling, disable controls, run, refresh status once, then restore:

```js
async function runCommand(doc, state, fn, fallback) {
  state.commanding = true;
  render(doc, state);
  const result = await fn();
  if (!result.ok) setStatus(doc, "err", formatBridgeError(result, fallback));
  await refreshStatus(doc, state);
  state.commanding = false;
  render(doc, state);
}
```

Render active vs idle based on `state.snapshot.session.state === "playing" || state.snapshot.session.state === "paused"`. Controls are disabled whenever `state.stale || state.commanding || capabilityFlag === false`. The seek input uses server-provided `position_ms` and `duration_ms` without extrapolating between polls.

Render history rows as buttons with ids from status:

```js
function renderHistory(doc, state) {
  const root = doc.getElementById("history");
  root.textContent = "";
  for (const item of state.snapshot?.history || []) {
    const row = doc.createElement("div");
    row.className = "history-row";
    const playBtn = doc.createElement("button");
    playBtn.type = "button";
    playBtn.textContent = item.title || item.url_display;
    playBtn.disabled = state.stale || state.commanding;
    playBtn.addEventListener("click", () => runCommand(doc, state, () => historyPlay(item.id), "History play failed"));
    const delBtn = doc.createElement("button");
    delBtn.type = "button";
    delBtn.textContent = "Delete";
    delBtn.disabled = state.stale || state.commanding;
    delBtn.addEventListener("click", () => runCommand(doc, state, () => historyDelete(item.id), "History delete failed"));
    row.append(playBtn, delBtn);
    root.append(row);
  }
}
```

- [ ] **Step 7: Write popup tests**

Replace the popup fixture in `extension/firefox/test/popup.test.js` with the new markup. Add fake timers for polling tests:

```js
it("renders active remote first when status is playing", async () => {
  vi.spyOn(bridge, "getStatus").mockResolvedValue({
    ok: true,
    configured: true,
    session: {
      state: "playing",
      title: "Night",
      source_display: "archive.org/night",
      position_ms: 1000,
      duration_ms: 10000,
      capabilities: { can_pause: true, can_resume: false, can_stop: true, can_replay: true, can_seek: true },
    },
    health: { bridge: "online", mister: "unknown", url_adapter: "enabled" },
    history: [],
  });
  await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
  await initPopup(document);
  expect(document.getElementById("active-view").hidden).toBe(false);
  expect(document.getElementById("idle-view").hidden).toBe(true);
  expect(document.getElementById("media-title").textContent).toBe("Night");
});

it("disables stale controls after a failed poll until the next successful poll", async () => {
  vi.useFakeTimers();
  vi.spyOn(bridge, "getStatus")
    .mockResolvedValueOnce({
      ok: true,
      bridge_url: "http://192.168.1.50:32500",
      session: { state: "playing", capabilities: { can_pause: true, can_stop: true, can_replay: true, can_seek: false } },
      health: { bridge: "online", mister: "unknown", url_adapter: "enabled" },
      history: [],
    })
    .mockResolvedValueOnce({ ok: false, status: 500, error: "bridge restarted" })
    .mockResolvedValueOnce({
      ok: true,
      bridge_url: "http://192.168.1.50:32500",
      session: { state: "playing", capabilities: { can_pause: true, can_stop: true, can_replay: true, can_seek: false } },
      health: { bridge: "online", mister: "unknown", url_adapter: "enabled" },
      history: [],
    });
  await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
  await initPopup(document);
  await vi.advanceTimersByTimeAsync(2000);
  expect(document.getElementById("pause").disabled).toBe(true);
  await vi.advanceTimersByTimeAsync(2000);
  expect(document.getElementById("pause").disabled).toBe(false);
  vi.useRealTimers();
});
```

Also add tests for idle cast-first state, hidden seek when duration is missing, history play/delete, and two popup instances with independent fake timers.

- [ ] **Step 8: Implement PR2 popup CSS**

Replace `extension/firefox/src/popup/popup.css` with popup-local tokens and stable control sizing:

```css
@font-face { font-family: "Space Grotesk"; src: url("../fonts/SpaceGrotesk-600.woff2") format("woff2"); font-weight: 600; font-display: swap; }
@font-face { font-family: "Inter Tight"; src: url("../fonts/InterTight-400.woff2") format("woff2"); font-weight: 400; font-display: swap; }
@font-face { font-family: "Inter Tight"; src: url("../fonts/InterTight-500.woff2") format("woff2"); font-weight: 500; font-display: swap; }
@font-face { font-family: "JetBrains Mono"; src: url("../fonts/JetBrainsMono-400.woff2") format("woff2"); font-weight: 400; font-display: swap; }

:root {
  --gr-bg: oklch(0.20 0.008 80);
  --gr-surface: oklch(0.25 0.010 80);
  --gr-surface-2: oklch(0.29 0.012 80);
  --gr-border: oklch(0.36 0.015 80);
  --gr-text: oklch(0.94 0.012 85);
  --gr-dim: oklch(0.65 0.012 80);
  --gr-amber: oklch(0.78 0.16 80);
  --gr-ok: oklch(0.72 0.14 150);
  --gr-err: oklch(0.65 0.22 25);
}

body {
  margin: 0;
  width: 360px;
  color: var(--gr-text);
  background: var(--gr-bg);
  font-family: "Inter Tight", sans-serif;
  letter-spacing: 0;
}

.popup-shell {
  min-height: 360px;
  padding: 14px;
  box-sizing: border-box;
}

.brand-row, .time-row, .actions, .control-grid, .health-grid, .history-row {
  display: grid;
  gap: 8px;
}

.brand-row {
  grid-template-columns: 1fr auto;
  align-items: center;
  margin-bottom: 12px;
}

.brand, h1 {
  font-family: "Space Grotesk", sans-serif;
  font-weight: 600;
}

h1 {
  margin: 0 0 8px;
  font-size: 22px;
  line-height: 1.1;
}

.chip {
  min-width: 58px;
  padding: 4px 7px;
  border: 1px solid var(--gr-border);
  color: var(--gr-amber);
  font-family: "JetBrains Mono", monospace;
  font-size: 11px;
  text-align: center;
}

.tab-url, .source-line, .time-row, .health-grid strong {
  font-family: "JetBrains Mono", monospace;
}

button {
  min-height: 34px;
  border: 1px solid var(--gr-border);
  background: var(--gr-surface);
  color: var(--gr-text);
  font: 500 13px "Inter Tight", sans-serif;
  cursor: pointer;
}

button.primary {
  border-color: var(--gr-amber);
  color: var(--gr-bg);
  background: var(--gr-amber);
}

button:disabled {
  opacity: 0.46;
  cursor: default;
}

.control-grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.health-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.health-grid div, .history-row {
  border: 1px solid var(--gr-border);
  background: var(--gr-surface);
  padding: 8px;
}

.history-row {
  grid-template-columns: 1fr auto;
}

.status {
  min-height: 20px;
  margin-top: 10px;
  color: var(--gr-dim);
}

.status.ok { color: var(--gr-ok); }
.status.err { color: var(--gr-err); }

@media (prefers-reduced-motion: no-preference) {
  .view { animation: popup-in 120ms ease-out; }
  .chip.live { animation: live-pulse 1200ms ease-in-out infinite; }
}

@keyframes popup-in {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
}

@keyframes live-pulse {
  50% { border-color: var(--gr-amber); }
}
```

- [ ] **Step 9: Migrate background and options**

In `extension/firefox/src/background.js`, import `formatBridgeError` and keep context menus on `play(url, "auto")`; the endpoint changes through `bridge.js`.

In `extension/firefox/src/options/options.js`, test connection through companion status:

```js
const res = await fetch(`${bridgeURL}/ui/companion/status`, {
  method: "GET",
  headers: { "X-Bridge-Extension": "1" },
  signal: ctrl.signal,
});
```

Update `options.test.js` from `/ui/adapter/url/panel` to `/ui/companion/status`, and assert the header is sent.

- [ ] **Step 10: Manifest/release tests**

Update tests so packaging checks include font assets:

```js
expect(files).toContain("src/fonts/InterTight-400.woff2");
expect(files).toContain("src/fonts/InterTight-500.woff2");
expect(files).toContain("src/fonts/JetBrainsMono-400.woff2");
expect(files).toContain("src/fonts/SpaceGrotesk-600.woff2");
```

Bump `extension/firefox/package.json` and `extension/firefox/manifest.json` in sync, for example from `0.1.5` to `0.2.0`, and keep `scripts/check-versions.mjs` passing.

- [ ] **Step 11: Verify Task 3**

Run:

```bash
cd extension/firefox
cmd.exe /c npm install
cmd.exe /c npm test
cmd.exe /c npm run lint
cmd.exe /c npm run build
cmd.exe /c npm run check:versions
```

From repo root, run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui ./internal/adapters/url ./cmd/mister-groovy-relay
```

Expected: all commands pass.

Commit:

```bash
git add extension/firefox THIRD_PARTY_NOTICES.md
git commit -m "feat(extension): add companion mini remote"
```

---

## Final Verification

Run from repo root:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/ui/companion.go internal/ui/companion_test.go internal/ui/server.go internal/adapters/url/companion.go internal/adapters/url/history.go internal/adapters/url/history_test.go internal/adapters/url/controls_test.go cmd/mister-groovy-relay/main.go
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui ./internal/adapters/url ./internal/core ./cmd/mister-groovy-relay
cd extension/firefox
cmd.exe /c npm test
cmd.exe /c npm run lint
cmd.exe /c npm run build
cmd.exe /c npm run check:versions
```

Manual verification:

```text
1. Load the unpacked extension from extension/firefox in Firefox.
2. Configure bridge URL in extension options.
3. Open popup while bridge is idle; confirm cast-first layout and history.
4. Cast a direct MP4 URL; confirm active remote layout.
5. Pause, resume, seek, stop, and replay from popup.
6. Recast and delete one history row.
7. Start a Plex or Jellyfin session; confirm status appears and controls are disabled.
8. Confirm a credentialed URL displays without password in popup, server JSON errors, and logs.
```

## Self-Review Checklist

- Spec coverage:
  - Companion gate and GET status: Task 1.
  - Stable opaque history ids and backfill save: Task 1.
  - Mutating route status codes, JSON envelopes, and content-type rules: Task 2.
  - URL adapter error contract and redaction-preserving play route: Task 2.
  - Absolute seek semantics: Task 2.
  - Gate-only mount helper instead of `mountPOST`: Task 1 and Task 2.
  - Popup stale snapshot and multi-popup polling semantics: Task 3.
  - PR2-aligned extension visual design and font packaging: Task 3.
- Placeholder scan: clean for banned planning markers.
- Type consistency:
  - `CompanionPlayResult`, `CompanionHistoryEntry`, and `CompanionSessionDisplay` are defined in `internal/ui/companion.go` before URL adapter methods use them.
  - Mutation response `state` values use `core.StatePlaying`, current `core.SessionStatus.State`, or `core.StateIdle`.
  - `offset_ms` is absolute in Go route tests and popup seek handling.
