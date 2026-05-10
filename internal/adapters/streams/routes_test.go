package streams

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	req := httptest.NewRequest(http.MethodGet, "/ui/adapter/streams/status", nil)
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
	if len(got.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(got.Providers))
	}
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
	req := httptest.NewRequest(http.MethodGet, "/ui/adapter/streams/status", nil)
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
	if !got.Capabilities.CanNext || !got.Capabilities.CanReplay || !got.Capabilities.CanStop || got.Capabilities.CanSeek || got.Capabilities.CanPause {
		t.Fatalf("active capabilities = %+v", got.Capabilities)
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
	req := httptest.NewRequest(http.MethodGet, "/ui/adapter/streams/status", nil)
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
	if got.Capabilities.CanNext || got.Capabilities.CanPrevious || got.Capabilities.CanReplay || got.Capabilities.CanStop {
		t.Fatalf("foreign-owner capabilities = %+v, want read-only controls", got.Capabilities)
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
	req := httptest.NewRequest(http.MethodGet, "/ui/adapter/streams/status", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui/adapter/streams/status", nil)
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
			req := httptest.NewRequest(http.MethodPost, "/ui/adapter/streams/play", strings.NewReader(tc.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			a.handlePlay(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
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
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/streams/refresh", strings.NewReader("provider_id=missing"))
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
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/streams/play", strings.NewReader("provider_id=mtv-rewind&channel_id=metal"))
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
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/streams/next", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/ui/adapter/streams/providers", nil)
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
	if len(got) != 2 {
		t.Fatalf("providers = %d, want 2", len(got))
	}
}
