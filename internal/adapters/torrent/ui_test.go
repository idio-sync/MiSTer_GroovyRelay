package torrent

import (
	"strings"
	"testing"
)

func TestExtraPanelHTMLContainsConsentAndUpload(t *testing.T) {
	a := &Adapter{cfg: DefaultConfig(), sessions: map[string]*Session{}}
	html := string(a.ExtraPanelHTML())
	for _, want := range []string{"torrent-panel", `name="magnet"`, `name="torrent_file"`, "traffic_acknowledged"} {
		if !strings.Contains(html, want) {
			t.Fatalf("panel missing %q: %s", want, html)
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
