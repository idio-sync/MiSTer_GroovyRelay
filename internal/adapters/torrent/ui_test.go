package torrent

import (
	"strings"
	"testing"
)

func TestExtraPanelHTMLContainsConsentWithoutLaunchForms(t *testing.T) {
	a := &Adapter{cfg: DefaultConfig(), sessions: map[string]*Session{}}
	html := string(a.ExtraPanelHTML())
	for _, want := range []string{"torrent-panel", "traffic_acknowledged"} {
		if !strings.Contains(html, want) {
			t.Fatalf("panel missing %q: %s", want, html)
		}
	}
	for _, forbidden := range []string{`name="magnet"`, `name="torrent_file"`, "/old_ui/adapter/torrent/play", "/old_ui/adapter/torrent/upload", "Play Magnet", "Upload Torrent"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("torrent panel still renders launch control %q: %s", forbidden, html)
		}
	}
}

func TestRenderPanelEscapesActiveTitle(t *testing.T) {
	a := &Adapter{
		cfg:         DefaultConfig(),
		sessions:    map[string]*Session{"tok": {Token: "tok", Title: `<script>alert(1)</script>`}},
		activeToken: "tok",
	}
	html := a.renderPanel()
	if strings.Contains(html, "<script>alert") {
		t.Fatalf("panel did not escape title: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("panel missing escaped title: %s", html)
	}
}

func TestRenderPanelDoesNotPollWhenIdleToPreserveUploadSelection(t *testing.T) {
	a := &Adapter{cfg: DefaultConfig(), sessions: map[string]*Session{}}
	html := a.renderPanel()
	if strings.Contains(html, `hx-trigger="every`) {
		t.Fatalf("idle torrent panel must not periodically swap the file input away: %s", html)
	}
}

func TestRenderPanelPollsOnlyLiveStatusWhenActive(t *testing.T) {
	a := &Adapter{
		cfg:         DefaultConfig(),
		sessions:    map[string]*Session{"tok": {Token: "tok", Title: "movie.mkv"}},
		activeToken: "tok",
	}
	html := a.renderPanel()
	if strings.Contains(html, `<section class="torrent-panel" id="torrent-panel" hx-get=`) {
		t.Fatalf("active torrent panel must not poll the whole input panel: %s", html)
	}
	if !strings.Contains(html, `<div id="torrent-live" hx-get="/old_ui/adapter/torrent/live" hx-trigger="every 5s" hx-swap="outerHTML">`) {
		t.Fatalf("active torrent panel should poll only the live status block: %s", html)
	}
}

func TestTorrentPanelDoesNotRenderActiveStop(t *testing.T) {
	html := renderLiveStatus(statusView{Enabled: true, TrafficAcknowledged: true, ActiveTitle: "Movie", ActiveToken: "tok-1"})
	if strings.Contains(html, "/old_ui/adapter/torrent/stop") || strings.Contains(html, ">Stop<") {
		t.Fatalf("torrent live status still renders Stop: %s", html)
	}
	if !strings.Contains(html, "Playing") {
		t.Fatalf("torrent live status lost active text: %s", html)
	}
}
