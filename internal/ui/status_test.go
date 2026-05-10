package ui

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

type fakeStatusViewer struct{ v core.StatusHomeView }

func (f fakeStatusViewer) StatusHomeView() core.StatusHomeView { return f.v }

func TestStatusHome_Idle(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
		c.EventLog = eventlog.New(8)
	})

	r := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status: %d, body: %s", w.Code, body)
	}
	if !strings.Contains(body, "Idle") {
		t.Errorf("missing Idle hero: %s", body)
	}
	if !strings.Contains(body, "gr-tile") {
		t.Errorf("missing tile markup: %s", body)
	}
}

func TestStatusHome_Casting(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{
			State:      core.StatePlaying,
			Title:      "Game of Thrones · S01E03",
			AdapterRef: "plex/episode-481",
			Modeline:   "NTSC_480i",
			Position:   90 * time.Second,
			StartedAt:  time.Now().Add(-90 * time.Second),
			BlitsTotal: 5391,
			WireBytes:  11_400_000,
		}}
		c.EventLog = eventlog.New(8)
	})

	r := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "Game of Thrones · S01E03") {
		t.Errorf("missing title: %s", body)
	}
	if !strings.Contains(body, "Live") {
		t.Errorf("missing LIVE indicator: %s", body)
	}
	if !strings.Contains(body, "NTSC_480i") {
		t.Errorf("missing modeline: %s", body)
	}
}

func TestStatusHome_Paused(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{
			State:      core.StatePaused,
			Title:      "Paused Movie",
			AdapterRef: "plex/movie-1",
			Modeline:   "PAL_576i",
			Position:   42 * time.Second,
			StartedAt:  time.Now().Add(-2 * time.Minute),
		}}
		c.EventLog = eventlog.New(8)
	})

	r := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "Paused") {
		t.Errorf("missing paused hero: %s", body)
	}
	if !strings.Contains(body, "Paused Movie") {
		t.Errorf("missing paused title: %s", body)
	}
	if !strings.Contains(body, "PAL_576i") {
		t.Errorf("missing paused modeline: %s", body)
	}
	if !strings.Contains(body, "00:42") && !strings.Contains(body, "42s") {
		t.Errorf("missing frozen paused position: %s", body)
	}
}

func TestStatusContent_Partial(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
		c.EventLog = eventlog.New(8)
	})

	r := httptest.NewRequest("GET", "/ui/status/content", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status: %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "<aside") || strings.Contains(body, "<html") {
		t.Errorf("partial leaked shell markup: %s", body)
	}
}
