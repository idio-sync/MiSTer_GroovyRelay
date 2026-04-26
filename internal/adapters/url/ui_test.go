package url

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestUIRoutes_HasPlayAndPanel(t *testing.T) {
	a := New(&fakeCore{})
	routes := a.UIRoutes()
	if len(routes) != 2 {
		t.Fatalf("UIRoutes count = %d, want 2", len(routes))
	}
	have := map[string]string{}
	for _, r := range routes {
		have[r.Method+" "+r.Path] = "ok"
	}
	if _, ok := have["POST play"]; !ok {
		t.Errorf("missing POST play route: %v", have)
	}
	if _, ok := have["GET panel"]; !ok {
		t.Errorf("missing GET panel route: %v", have)
	}
}

func TestPanel_RendersIdle(t *testing.T) {
	a := New(&fakeCore{})
	req := httptest.NewRequest(http.MethodGet, "/panel", nil)
	w := httptest.NewRecorder()
	a.handlePanel(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Idle") {
		t.Errorf("idle panel missing 'Idle' text: %s", body)
	}
	if !strings.Contains(body, `hx-post="/ui/adapter/url/play"`) {
		t.Errorf("panel form should hx-post to /ui/adapter/url/play: %s", body)
	}
}

func TestPanel_RendersPlaying(t *testing.T) {
	a := New(&fakeCore{})
	a.markRunning("https://example.com/video.mp4")
	req := httptest.NewRequest(http.MethodGet, "/panel", nil)
	w := httptest.NewRecorder()
	a.handlePanel(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Playing") {
		t.Errorf("playing panel missing 'Playing' text: %s", body)
	}
	if !strings.Contains(body, "example.com/video.mp4") {
		t.Errorf("playing panel missing URL: %s", body)
	}
}

func TestPanel_RendersError(t *testing.T) {
	a := New(&fakeCore{})
	a.setState(adapters.StateError, "probe failed: connection refused")
	req := httptest.NewRequest(http.MethodGet, "/panel", nil)
	w := httptest.NewRecorder()
	a.handlePanel(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "probe failed") {
		t.Errorf("error panel missing error text: %s", body)
	}
}

func TestExtraPanelHTML_EmbedsPanel(t *testing.T) {
	a := New(&fakeCore{})
	html := string(a.ExtraPanelHTML())
	if !strings.Contains(html, "url-panel") {
		t.Errorf("ExtraPanelHTML should include the panel; got %s", html)
	}
}
