package streams

import (
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestExtraPanelHTMLContainsStreamsPanel(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	html := string(a.ExtraPanelHTML())
	for _, want := range []string{"streams-panel", "MTV Rewind", "Cartoon Rewind", `hx-post="/ui/adapter/streams/play"`, `hx-post="/ui/adapter/streams/refresh"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("panel missing %q: %s", want, html)
		}
	}
}

func TestExtraPanelHTMLGroupsChannelsByCategory(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Groups: []ChannelGroup{
			{ID: "shows", Name: "MTV Shows", Order: 10},
			{ID: "decades", Name: "By Decade", Order: 20},
		},
		Channels: []Channel{
			{ID: "120minutes", Name: "120 Minutes", GroupID: "shows", Order: 10, Items: []StreamItem{{ID: "AAAAAAAAAAA"}}},
			{ID: "80s", Name: "80s", GroupID: "decades", Order: 20, Items: []StreamItem{{ID: "BBBBBBBBBBB"}}},
		},
	}})
	html := string(a.ExtraPanelHTML())
	for _, want := range []string{
		`class="streams-channel-groups"`,
		`class="streams-channel-table"`,
		`<h5>MTV Shows</h5>`,
		`<h5>By Decade</h5>`,
		`120 Minutes`,
		`80s`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("panel missing %q: %s", want, html)
		}
	}
}

func TestExtraPanelHTMLRendersActiveTitleAndProgressUnderPlayingLine(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:    "s1",
		ProviderID:   "mtv-rewind",
		ProviderName: "MTV Rewind",
		ChannelID:    "metal",
		ChannelName:  "Metal",
		ItemToken:    1,
		Items: []StreamItem{{
			ID:    "a",
			Title: "Ace of Spades",
			URL:   "https://youtu.be/a",
		}},
		loopMode: loopSequential,
	}
	fc.status = core.SessionStatus{
		State:      core.StatePlaying,
		AdapterRef: queueAdapterRef(a.active, a.active.ItemToken),
		Position:   83 * time.Second,
		Duration:   45*time.Minute + 12*time.Second,
	}

	html := string(a.ExtraPanelHTML())
	statusIdx := strings.Index(html, `Playing: MTV Rewind / Metal`)
	titleIdx := strings.Index(html, `<p class="streams-now-title">Ace of Spades</p>`)
	progressIdx := strings.Index(html, `<p class="position">01:23 / 45:12</p>`)
	if statusIdx < 0 || titleIdx < 0 || progressIdx < 0 {
		t.Fatalf("active panel missing status/title/progress: %s", html)
	}
	if !(statusIdx < titleIdx && titleIdx < progressIdx) {
		t.Fatalf("active title/progress should render under Playing line: %s", html)
	}
}

func TestExtraPanelHTMLEscapesProviderAndChannelNames(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "evil",
		Name:       `<script>alert("p")</script>`,
		Channels: []Channel{{
			ID:    `bad" onclick="x`,
			Name:  `<img src=x onerror=alert(1)>`,
			Items: []StreamItem{{ID: "dQw4w9WgXcQ"}},
		}},
	}})
	html := string(a.ExtraPanelHTML())
	for _, bad := range []string{`<script>`, `<img`, `onclick="x`} {
		if strings.Contains(html, bad) {
			t.Fatalf("panel did not escape %q: %s", bad, html)
		}
	}
	if !strings.Contains(html, `&lt;script&gt;`) || !strings.Contains(html, `&lt;img`) {
		t.Fatalf("panel missing escaped provider/channel text: %s", html)
	}
}
