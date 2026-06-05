package streams

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
)

func TestUIRoutes(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	routes := a.UIRoutes()
	want := map[string]string{
		"GET panel":     "",
		"GET status":    "",
		"GET providers": "",
		"POST refresh":  "",
		"POST play":     "",
		"POST replay":   "",
		"POST next":     "",
		"POST previous": "",
		"POST stop":     "",
	}
	for _, r := range routes {
		delete(want, r.Method+" "+r.Path)
	}
	if len(want) != 0 {
		t.Fatalf("missing routes: %#v", want)
	}
}

func TestStatusJSONIncludesCompanionFields(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/old_ui/adapter/streams/status", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got StatusView
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Capabilities.CanNext != false || got.Capabilities.CanSeek != false {
		t.Fatalf("capabilities = %+v", got.Capabilities)
	}
	assertProviderIDs(t, got.Providers, "mtv-rewind", "cartoon-rewind", "toonami-aftermath")
}

func TestStatusJSONActiveQueueCapabilities(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:  "s1",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		ItemToken:  1,
		Items: []StreamItem{
			{ID: "a", URL: "https://youtu.be/a"},
			{ID: "b", URL: "https://youtu.be/b"},
		},
		loopMode: loopSequential,
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)
	req := httptest.NewRequest(http.MethodGet, "/old_ui/adapter/streams/status", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got StatusView
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Capabilities.CanNext || !got.Capabilities.CanReplay || !got.Capabilities.CanStop || !got.Capabilities.CanPause || got.Capabilities.CanSeek {
		t.Fatalf("active capabilities = %+v", got.Capabilities)
	}
}

func TestHandleProvidersJSONFiltersByProviderIDAndQuery(t *testing.T) {
	a := newTestAdapterWithCatalog(t)

	req := httptest.NewRequest(http.MethodGet, "/old_ui/adapter/streams/providers?provider_id=mtv-rewind", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleProviders(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("provider_id status = %d", rr.Code)
	}
	var byProvider []ProviderStatusView
	if err := json.NewDecoder(rr.Body).Decode(&byProvider); err != nil {
		t.Fatalf("decode provider_id: %v", err)
	}
	if len(byProvider) != 1 || byProvider[0].ID != "mtv-rewind" {
		t.Fatalf("provider_id filter = %+v, want only mtv-rewind", byProvider)
	}

	req = httptest.NewRequest(http.MethodGet, "/old_ui/adapter/streams/providers?q=he-man", nil)
	req.Header.Set("Accept", "application/json")
	rr = httptest.NewRecorder()
	a.handleProviders(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("q status = %d", rr.Code)
	}
	var byQuery []ProviderStatusView
	if err := json.NewDecoder(rr.Body).Decode(&byQuery); err != nil {
		t.Fatalf("decode q: %v", err)
	}
	if len(byQuery) != 1 || byQuery[0].ID != "cartoon-rewind" {
		t.Fatalf("q filter = %+v, want only cartoon-rewind", byQuery)
	}
}

func TestHandleProvidersHTMLFiltersByProviderIDAndQuery(t *testing.T) {
	a := newTestAdapterWithCatalog(t)

	req := httptest.NewRequest(http.MethodGet, "/old_ui/adapter/streams/providers?provider_id=mtv-rewind", nil)
	rr := httptest.NewRecorder()
	a.handleProviders(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("provider_id status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "MTV Rewind") || strings.Contains(body, "Cartoon Rewind") || strings.Contains(body, "Toonami Aftermath") {
		t.Fatalf("provider_id HTML body = %q, want only MTV provider", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/old_ui/adapter/streams/providers?q=he-man", nil)
	rr = httptest.NewRecorder()
	a.handleProviders(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("q status = %d", rr.Code)
	}
	body = rr.Body.String()
	if !strings.Contains(body, "Cartoon Rewind") || !strings.Contains(body, "He-Man") || strings.Contains(body, "MTV Rewind") || strings.Contains(body, "Toonami Aftermath") {
		t.Fatalf("q HTML body = %q, want only matching cartoon provider", body)
	}
}

func TestStatusJSONDisablesControlsForForeignOwner(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:  "s1",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		ItemToken:  1,
		Index:      1,
		Items: []StreamItem{
			{ID: "a", URL: "https://youtu.be/a"},
			{ID: "b", URL: "https://youtu.be/b"},
			{ID: "c", URL: "https://youtu.be/c"},
		},
		loopMode: loopSequential,
	}
	core.status.AdapterRef = "url:foreign"
	req := httptest.NewRequest(http.MethodGet, "/old_ui/adapter/streams/status", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got StatusView
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Capabilities.CanNext || got.Capabilities.CanPrevious || got.Capabilities.CanReplay || got.Capabilities.CanStop || got.Capabilities.CanPause {
		t.Fatalf("foreign-owner capabilities = %+v, want read-only controls", got.Capabilities)
	}
}

func TestStatusJSONDirectStreamDoesNotAdvertisePauseOrAdvance(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "toonami-aftermath", ChannelID: "east"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	c.status.AdapterRef = activeAdapterRef(a.active)

	req := httptest.NewRequest(http.MethodGet, "/old_ui/adapter/streams/status", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got StatusView
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got.Capabilities.CanPause || got.Capabilities.CanNext || got.Capabilities.CanPrevious {
		t.Fatalf("capabilities = %+v, want no pause/next/previous", got.Capabilities)
	}
	if !got.Capabilities.CanReplay || !got.Capabilities.CanStop {
		t.Fatalf("capabilities = %+v, want replay and stop", got.Capabilities)
	}
}

func TestStatusJSONDoesNotAdvertiseReplayForMissingCatalogChannel(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:  "s1",
		ProviderID: "mtv-rewind",
		ChannelID:  "missing",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "a", URL: "https://youtu.be/a"}},
		loopMode:   loopSequential,
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)
	req := httptest.NewRequest(http.MethodGet, "/old_ui/adapter/streams/status", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got StatusView
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Capabilities.CanReplay {
		t.Fatalf("capabilities = %+v, should not advertise replay for missing catalog channel", got.Capabilities)
	}
}

func TestStatusJSONDoesNotAdvertiseReplayForEmptyCatalogChannel(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:  "s1",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "a", URL: "https://youtu.be/a"}},
		loopMode:   loopSequential,
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels:   []Channel{{ID: "metal", Name: "Metal", PlayMode: PlaySequential}},
	}})
	req := httptest.NewRequest(http.MethodGet, "/old_ui/adapter/streams/status", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got StatusView
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Capabilities.CanReplay {
		t.Fatalf("capabilities = %+v, should not advertise replay for empty catalog channel", got.Capabilities)
	}
}

func TestHandlePlayRejectsMalformedRequests(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	cases := []struct {
		name string
		form string
	}{
		{name: "both channel and item", form: "provider_id=mtv-rewind&channel_id=metal&item_id=dQw4w9WgXcQ"},
		{name: "bad item", form: "provider_id=mtv-rewind&item_id=bad"},
		{name: "unknown provider", form: "provider_id=missing&channel_id=metal"},
		{name: "unknown channel", form: "provider_id=mtv-rewind&channel_id=missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/old_ui/adapter/streams/play", strings.NewReader(tc.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			a.handlePlay(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", rr.Code, http.StatusOK, rr.Body.String())
			}
			if body := rr.Body.String(); !strings.Contains(body, `class="gr-callout err streams-error"`) {
				t.Fatalf("HTML error response missing swappable callout: %s", body)
			}
		})
	}
}

func TestHandleRefreshRejectsUnknownProvider(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	calls := 0
	a.refreshOnce = func(ctx context.Context, reason string) RefreshStatus {
		calls++
		return RefreshStatus{}
	}
	req := httptest.NewRequest(http.MethodPost, "/old_ui/adapter/streams/refresh", strings.NewReader("provider_id=missing"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleRefresh(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if calls != 0 {
		t.Fatalf("refresh calls = %d, want 0", calls)
	}
	var got struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Error == "" {
		t.Fatal("JSON error response should include error")
	}
}

func TestHandlePlayJSONReturnsStartResult(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.SetEnabled(true)
	req := httptest.NewRequest(http.MethodPost, "/old_ui/adapter/streams/play", strings.NewReader("provider_id=mtv-rewind&channel_id=metal"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handlePlay(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", ct)
	}
	var got struct {
		AdapterRef string `json:"adapter_ref"`
		ProviderID string `json:"provider_id"`
		ChannelID  string `json:"channel_id"`
		ItemID     string `json:"item_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AdapterRef == "" || got.ProviderID != "mtv-rewind" || got.ChannelID != "metal" || got.ItemID != "" {
		t.Fatalf("start result = %+v", got)
	}
}

func TestHandleNextJSONReturnsStatus(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:  "s1",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		ItemToken:  1,
		Items: []StreamItem{
			{ID: "a", URL: "https://youtu.be/a"},
			{ID: "b", URL: "https://youtu.be/b"},
		},
		loopMode: loopSequential,
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)
	req := httptest.NewRequest(http.MethodPost, "/old_ui/adapter/streams/next", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleNext(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", ct)
	}
	var got StatusView
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Active == nil || got.Active.ItemID != "b" {
		t.Fatalf("status active = %+v", got.Active)
	}
}

func TestHandleProvidersJSON(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/old_ui/adapter/streams/providers", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleProviders(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got []ProviderStatusView
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertProviderIDs(t, got, "mtv-rewind", "cartoon-rewind", "toonami-aftermath")
}

func assertProviderIDs(t *testing.T, got []ProviderStatusView, want ...string) {
	t.Helper()
	seen := make(map[string]int, len(got))
	for _, provider := range got {
		seen[provider.ID]++
	}
	if len(got) != len(want) {
		t.Fatalf("providers = %v, want exactly %v", seen, want)
	}
	for _, id := range want {
		if seen[id] != 1 {
			t.Fatalf("providers = %v, want exactly %v", seen, want)
		}
	}
}

func TestHandlePanelReadsSelectionQuery(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/old_ui/adapter/streams/panel?provider_id=mtv-rewind&group_id=genres", nil)
	rr := httptest.NewRecorder()
	a.handlePanel(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `hx-get="/old_ui/adapter/streams/panel?provider_id=mtv-rewind&amp;group_id=`) {
		t.Fatalf("panel did not read provider selection: %s", body)
	}
}

func assertPanelSelectionPreserved(t *testing.T, body, providerID, groupID string) {
	t.Helper()
	// panelURL emits provider_id before group_id; escAttr renders the
	// query separator as &amp; in HTML attributes.
	want := `hx-get="/old_ui/adapter/streams/panel?provider_id=` + providerID + `&amp;group_id=` + groupID + `"`
	if !strings.Contains(body, want) {
		t.Fatalf("selection polling URL missing %q: %s", want, body)
	}
}

func TestHTMLRouteResponsesPreserveGuideSelection(t *testing.T) {
	newActive := func(t *testing.T) (*Adapter, *fakeCore) {
		t.Helper()
		a, core := newTestAdapterWithFakeCore(t)
		a.replaceCatalogsForTest([]ProviderCatalog{{
			ProviderID: "mtv-rewind",
			Name:       "MTV Rewind",
			Groups:     []ChannelGroup{{ID: "genres", Name: "Genres"}},
			Channels:   []Channel{{ID: "metal", Name: "Metal", GroupID: "genres", Items: []StreamItem{{ID: "a", URL: "https://youtu.be/a"}}}},
		}})
		a.active = &ActiveQueue{
			SessionID:    "s1",
			ProviderID:   "mtv-rewind",
			ProviderName: "MTV Rewind",
			ChannelID:    "metal",
			ChannelName:  "Metal",
			ItemToken:    1,
			Items: []StreamItem{
				{ID: "a", URL: "https://youtu.be/a"},
				{ID: "b", URL: "https://youtu.be/b"},
			},
			loopMode: loopSequential,
		}
		core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)
		return a, core
	}

	cases := []struct {
		name    string
		handler func(*Adapter, http.ResponseWriter, *http.Request)
		path    string
		form    string
	}{
		{name: "refresh", handler: (*Adapter).handleRefresh, path: "/old_ui/adapter/streams/refresh", form: "guide_provider_id=mtv-rewind&guide_group_id=genres"},
		{name: "play", handler: (*Adapter).handlePlay, path: "/old_ui/adapter/streams/play", form: "provider_id=mtv-rewind&guide_provider_id=mtv-rewind&guide_group_id=genres&channel_id=metal"},
		{name: "previous", handler: (*Adapter).handlePrevious, path: "/old_ui/adapter/streams/previous", form: "guide_provider_id=mtv-rewind&guide_group_id=genres"},
		{name: "next", handler: (*Adapter).handleNext, path: "/old_ui/adapter/streams/next", form: "guide_provider_id=mtv-rewind&guide_group_id=genres"},
		{name: "replay", handler: (*Adapter).handleReplay, path: "/old_ui/adapter/streams/replay", form: "guide_provider_id=mtv-rewind&guide_group_id=genres"},
		{name: "stop", handler: (*Adapter).handleStop, path: "/old_ui/adapter/streams/stop", form: "guide_provider_id=mtv-rewind&guide_group_id=genres"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newActive(t)
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			tc.handler(a, rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
			}
			assertPanelSelectionPreserved(t, rr.Body.String(), "mtv-rewind", "genres")
		})
	}
}

func TestHTMLRouteErrorsRenderSwappablePanelWithSelectionAndMessage(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	cases := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		path    string
		form    string
		want    string
	}{
		{
			name:    "bad play",
			handler: a.handlePlay,
			path:    "/old_ui/adapter/streams/play",
			form:    "provider_id=mtv-rewind&guide_provider_id=mtv-rewind&guide_group_id=genres",
			want:    "channel_id or item_id required",
		},
		{
			name:    "unknown refresh target",
			handler: a.handleRefresh,
			path:    "/old_ui/adapter/streams/refresh",
			form:    "provider_id=missing&guide_provider_id=mtv-rewind&guide_group_id=genres",
			want:    "provider is not cataloged",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			tc.handler(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("HTML errors must be swappable 200 responses, got %d body=%s", rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			if !strings.Contains(body, `class="gr-callout err streams-error"`) || !strings.Contains(body, tc.want) {
				t.Fatalf("error callout missing %q: %s", tc.want, body)
			}
			assertPanelSelectionPreserved(t, body, "mtv-rewind", "genres")
		})
	}
}

func TestHandlePlayWithoutGuideFieldsFallsBackToPlaybackProviderSelection(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	req := httptest.NewRequest(http.MethodPost, "/old_ui/adapter/streams/play", strings.NewReader("provider_id=mtv-rewind&channel_id=metal"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	a.handlePlay(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	assertPanelSelectionPreserved(t, rr.Body.String(), "mtv-rewind", ungroupedGroupID)
}
