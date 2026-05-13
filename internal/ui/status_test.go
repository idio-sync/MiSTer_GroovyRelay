package ui

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
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

func TestStatusHome_RendersProcessUptime(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.StartedAt = time.Now().Add(-65 * time.Minute)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})

	r := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status: %d, body: %s", w.Code, body)
	}
	if !strings.Contains(body, "uptime 1h 05m") {
		t.Errorf("missing process uptime: %s", body)
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

func TestStatusDashboard_PollingKeepsPreviewSpacingAndStopsReplayAnimations(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
		c.EventLog = eventlog.New(8)
	})

	r := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	if !strings.Contains(body, `id="status-content"`) ||
		!strings.Contains(body, `hx-get="/ui/status/content"`) ||
		!strings.Contains(body, `hx-trigger="every 3s"`) {
		t.Fatalf("status dashboard missing polling content wrapper: %s", body)
	}

	r = httptest.NewRequest("GET", "/ui/static/app.css", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	css := w.Body.String()

	for _, want := range []string{
		"#panel:has(#status-content)",
		"#status-content",
		"data-status-refresh",
		".gr-hero::before",
		".gr-activity-body .gr-activity-entry",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css missing status dashboard rule marker %q", want)
		}
	}

	r = httptest.NewRequest("GET", "/ui/static/clipboard.js", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	js := w.Body.String()

	for _, want := range []string{"htmx:beforeSwap", "status-content", "data-status-refresh"} {
		if !strings.Contains(js, want) {
			t.Errorf("clipboard.js missing status refresh marker %q", want)
		}
	}
}

func TestStatusHome_IdleMatchesPreviewSummaryAndTileDensity(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(
			&uiStubAdapter{name: "plex", displayName: "Plex", enabled: true, enabledSet: true, state: adapters.StateRunning},
			&uiStubAdapter{name: "jellyfin", displayName: "Jellyfin", enabled: true, enabledSet: true, state: adapters.StateRunning},
			&uiStubAdapter{name: "url", displayName: "URL", enabled: false, enabledSet: true, state: adapters.StateStopped},
		)
		c.BridgeSaver = &fakeBridgeSaver{}
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
		c.StartedAt = time.Now().Add(-65 * time.Second)
		c.EventLog = eventlog.New(8)
	})

	r := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	for _, want := range []string{
		"Plex listening",
		"Jellyfin listening",
		"URL disabled",
		"<dt>uptime</dt>",
		"<dt>mode</dt>",
		"<dt>state</dt>",
		"<dt>since</dt>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("idle status preview marker %q missing from body: %s", want, body)
		}
	}
}

func TestStatusHome_CastingMatchesPreviewTileModel(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(
			&uiStubAdapter{name: "plex", displayName: "Plex", enabled: true, enabledSet: true, state: adapters.StateRunning},
			&uiStubAdapter{name: "jellyfin", displayName: "Jellyfin", enabled: true, enabledSet: true, state: adapters.StateRunning},
		)
		c.BridgeSaver = &fakeBridgeSaver{}
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{
			State:       core.StatePlaying,
			Source:      "plex",
			Title:       "Game of Thrones · S01E03",
			AdapterRef:  "plex/episode-481",
			Modeline:    "NTSC_480i",
			Position:    24*time.Minute + 32*time.Second,
			StartedAt:   time.Now().Add(-(24*time.Minute + 32*time.Second)),
			BlitsTotal:  88143,
			FramesTotal: 88201,
			Underruns:   0,
			WireBytes:   11_400_000,
			LastACKAge:  16 * time.Millisecond,
		}}
		c.EventLog = eventlog.New(8)
	})

	r := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	for _, want := range []string{
		"casting",
		"<dt>blits</dt>",
		"<dt>frames</dt>",
		"<dt>under-runs</dt>",
		"Plex",
		"active",
		"<dt>position</dt>",
		"MiSTer",
		"acked",
		"Pipeline",
		"nominal",
		"<dt>lz4</dt>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("casting status preview marker %q missing from body: %s", want, body)
		}
	}
}

func TestStatusHome_LiveSidebarMatchesPreview(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{
			State:     core.StatePlaying,
			StartedAt: time.Now().Add(-time.Minute),
		}}
	})

	r := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()

	statusLink := navLinkMarkup(t, body, `href="/ui/"`)
	if !strings.Contains(statusLink, `class="dot run"`) {
		t.Fatalf("live status sidebar link should use run dot; link=%q", statusLink)
	}
	if !strings.Contains(statusLink, `<span class="meta">live</span>`) {
		t.Fatalf("live status sidebar link should include live meta; link=%q", statusLink)
	}
}
