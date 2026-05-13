package streams

import (
	"fmt"
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

func TestExtraPanelHTMLRendersFocusedGuideForSelectedCategory(t *testing.T) {
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

	html := a.renderPanel(panelSelectionRequest{ProviderID: "mtv-rewind", GroupID: "decades", ProviderExplicit: true, GroupExplicit: true})
	for _, want := range []string{
		`class="streams-guide"`,
		`class="streams-provider-tabs"`,
		`class="streams-category-rail"`,
		`class="streams-channel-grid"`,
		`aria-current="true"`,
		`By Decade`,
		`80s`,
		`name="provider_id" value="mtv-rewind"`,
		`name="guide_group_id" value="decades"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("panel missing %q: %s", want, html)
		}
	}
	for _, old := range []string{`streams-channel-table`, `<table`, `120 Minutes`} {
		if strings.Contains(html, old) {
			t.Fatalf("focused guide should not render old/unselected content %q: %s", old, html)
		}
	}
}

func TestExtraPanelHTMLRendersDenseMTVCategoryAsGrid(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	channels := make([]Channel, 0, 12)
	for i := 0; i < 12; i++ {
		channels = append(channels, Channel{
			ID:      fmt.Sprintf("label-%02d", i),
			Name:    fmt.Sprintf("Label %02d", i),
			GroupID: "labels",
			Items:   []StreamItem{{ID: fmt.Sprintf("item-%02d", i)}},
		})
	}
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Groups:     []ChannelGroup{{ID: "labels", Name: "Labels & Scenes", Order: 50}},
		Channels:   channels,
	}})

	html := a.renderPanel(panelSelectionRequest{ProviderID: "mtv-rewind", GroupID: "labels", ProviderExplicit: true, GroupExplicit: true})
	if strings.Count(html, `class="streams-channel-card`) != 12 {
		t.Fatalf("channel card count mismatch: %s", html)
	}
	if strings.Contains(html, `<table`) {
		t.Fatalf("dense category should not render a table: %s", html)
	}
}

func TestExtraPanelHTMLMarksActiveChannelInSelectedGrid(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.active = &ActiveQueue{
		ProviderID:   "mtv-rewind",
		ProviderName: "MTV Rewind",
		ChannelID:    "metal",
		ChannelName:  "Headbangers Ball",
		Items:        []StreamItem{{ID: "dQw4w9WgXcQ"}},
	}

	html := a.renderPanel(panelSelectionRequest{})
	if !strings.Contains(html, `streams-channel-card tuned`) ||
		!strings.Contains(html, `Now Playing`) {
		t.Fatalf("active channel treatment missing: %s", html)
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
	statusIdx := strings.Index(html, `Now Playing`)
	titleIdx := strings.Index(html, `<div class="streams-now-title">Ace of Spades</div>`)
	progressIdx := strings.Index(html, `<div class="streams-position">01:23 / 45:12</div>`)
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

func TestStatusViewIncludesArtworkMetadataAndEmptyEnabledProviders(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels: []Channel{{
			ID:       "metal",
			Name:     "Metal",
			PlayMode: PlayShuffle,
			Items:    nil,
		}},
	}, {
		ProviderID: "cartoon-rewind",
		Name:       "Cartoon Rewind",
		Channels: []Channel{{
			ID:       "heman",
			Name:     "He-Man",
			PlayMode: PlayShuffle,
			Items:    []StreamItem{{ID: "9bZkp7q19f0"}},
		}},
	}})

	view := a.statusView()
	if len(view.Providers) != 2 {
		t.Fatalf("providers = %d, want empty enabled provider retained", len(view.Providers))
	}
	// statusView currently sorts provider IDs alphabetically before rendering.
	if view.Providers[0].ID != "cartoon-rewind" || view.Providers[1].ID != "mtv-rewind" {
		t.Fatalf("provider order = %+v", view.Providers)
	}
	var mtv ProviderStatusView
	for _, provider := range view.Providers {
		if provider.ID == "mtv-rewind" {
			mtv = provider
		}
	}
	if len(mtv.Channels) != 0 {
		t.Fatalf("mtv channels = %d, want 0 post-filter playable channels", len(mtv.Channels))
	}
	if mtv.LogoURL != "https://wantmymtv.vercel.app/public/images/rewindlogo.png" ||
		mtv.LogoAlt != "MTV Rewind logo" ||
		mtv.FallbackLabel != "MTV REWIND" {
		t.Fatalf("mtv artwork = %+v", mtv)
	}
}

func TestExtraPanelHTMLRendersProviderArtworkFallbackWithoutEmptyImage(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.replaceDefinitionsForTest([]ProviderDefinition{{
		ID:              "mtv-rewind",
		Type:            youtubeChannelJSONProviderType,
		DisplayName:     "MTV Rewind",
		BaseURL:         "https://wantmymtv.vercel.app",
		PlaylistURL:     "https://wantmymtv.vercel.app/public/mtv-playlists.json",
		DefaultChannel:  "metal",
		DefaultPlayMode: PlayShuffle,
		FallbackLabel:   `M<TV "REWIND"`,
		URLRules: []URLRule{{
			ID:         "mtv-player-channel",
			Schemes:    []string{"https"},
			Hosts:      []string{"wantmymtv.vercel.app"},
			Path:       "/player.html",
			Target:     "channel",
			QueryParam: "channel",
		}},
	}})
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels:   []Channel{{ID: "metal", Name: "Metal", Items: []StreamItem{{ID: "dQw4w9WgXcQ"}}}},
	}})

	html := string(a.ExtraPanelHTML())
	if strings.Contains(html, `<img class="streams-provider-art"`) || strings.Contains(html, `src=""`) {
		t.Fatalf("empty logo URL should not render img src: %s", html)
	}
	if !strings.Contains(html, `class="streams-provider-art-shell"`) ||
		!strings.Contains(html, `role="img"`) ||
		!strings.Contains(html, `aria-label="MTV Rewind"`) ||
		!strings.Contains(html, `M&lt;TV &#34;REWIND&#34;`) {
		t.Fatalf("fallback artwork shell missing or unescaped: %s", html)
	}
}

func TestRenderPanelPreservesSelectedProviderAndGroupInPollingURL(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Groups: []ChannelGroup{
			{ID: "shows", Name: "MTV Shows", Order: 10},
			{ID: "genres", Name: "Genres", Order: 20},
		},
		Channels: []Channel{
			{ID: "120minutes", Name: "120 Minutes", GroupID: "shows", Items: []StreamItem{{ID: "AAAAAAAAAAA"}}},
			{ID: "metal", Name: "Headbangers Ball", GroupID: "genres", Items: []StreamItem{{ID: "BBBBBBBBBBB"}}},
		},
	}})

	html := a.renderPanel(panelSelectionRequest{ProviderID: "mtv-rewind", GroupID: "genres", ProviderExplicit: true, GroupExplicit: true})
	if !strings.Contains(html, `hx-get="/ui/adapter/streams/panel?provider_id=mtv-rewind&amp;group_id=genres"`) {
		t.Fatalf("polling URL did not preserve selection: %s", html)
	}
	if !strings.Contains(html, `name="guide_group_id" value="genres"`) {
		t.Fatalf("forms did not preserve group_id: %s", html)
	}
}

func TestRenderPanelErrorPreservesExplicitUnresolvedGroup(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	html := a.renderPanel(panelSelectionRequest{
		ProviderID:       "mtv-rewind",
		GroupID:          "submitted-missing-group",
		ProviderExplicit: true,
		GroupExplicit:    true,
		ErrorMessage:     "provider is not cataloged",
	})
	if !strings.Contains(html, `hx-get="/ui/adapter/streams/panel?provider_id=mtv-rewind&amp;group_id=submitted-missing-group"`) {
		t.Fatalf("error panel should preserve explicit unresolved group: %s", html)
	}
}

func TestRenderPanelUnknownSelectionFallsBackDeterministically(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	html := a.renderPanel(panelSelectionRequest{ProviderID: "bogus", GroupID: "missing", ProviderExplicit: true, GroupExplicit: true})
	if !strings.Contains(html, `hx-get="/ui/adapter/streams/panel?provider_id=cartoon-rewind`) {
		t.Fatalf("bogus provider should fall back to first provider by status order: %s", html)
	}
}

func TestRenderPanelProviderOnlySelectionUsesActiveGroup(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Groups: []ChannelGroup{
			{ID: "shows", Name: "MTV Shows", Order: 10},
			{ID: "genres", Name: "Genres", Order: 20},
		},
		Channels: []Channel{
			{ID: "120minutes", Name: "120 Minutes", GroupID: "shows", Items: []StreamItem{{ID: "AAAAAAAAAAA"}}},
			{ID: "metal", Name: "Headbangers Ball", GroupID: "genres", Items: []StreamItem{{ID: "BBBBBBBBBBB"}}},
		},
	}})
	a.active = &ActiveQueue{ProviderID: "mtv-rewind", ChannelID: "metal", Items: []StreamItem{{ID: "BBBBBBBBBBB"}}}

	html := a.renderPanel(panelSelectionRequest{ProviderID: "mtv-rewind", ProviderExplicit: true})
	if !strings.Contains(html, `hx-get="/ui/adapter/streams/panel?provider_id=mtv-rewind&amp;group_id=genres"`) {
		t.Fatalf("provider-only selection should resolve active group: %s", html)
	}
}
