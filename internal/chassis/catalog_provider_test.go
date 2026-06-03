package chassis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestBuildCatalogProviderEnvelope_Shape(t *testing.T) {
	t.Parallel()
	p := adapters.CatalogProvider{
		ID: "user:mix", DisplayName: "Mix", BadgeLabel: "MX", BadgeClass: "u-teal",
		Groups: []adapters.CatalogGroup{{ID: "g1", Name: "Group", Channels: []adapters.CatalogChannel{
			{ID: "a", Name: "A", PlayMode: "SHUFFLE", Live: false},
		}}},
	}
	env := buildCatalogProviderEnvelope(p)
	if env.ID != "user:mix" || env.BadgeClass != "u-teal" {
		t.Fatalf("envelope identity = %+v", env)
	}
	if len(env.Groups) != 1 || len(env.Groups[0].Channels) != 1 || env.Groups[0].Channels[0].ID != "a" {
		t.Fatalf("envelope groups = %+v", env.Groups)
	}
}

func TestProviderStatusEnvelope_AutoEnabledField(t *testing.T) {
	t.Parallel()
	env := providerStatusEnvelope{Provider: "user:mix", AutoEnabledStreams: "on"}
	if env.AutoEnabledStreams != "on" {
		t.Fatal("autoEnabledStreams not carried")
	}
}

type fakeUserProviderEditor struct {
	createRes     adapters.UserProviderResult
	createErr     error
	updateRes     adapters.UserProviderResult
	deleteRes     adapters.UserProviderResult
	verifyRes     adapters.VerifyChannelResult
	reorderErr    error
	lastUpdateID  string
	lastDeleteID  string
	lastReorderID string
	lastCreate    adapters.UserProviderForm
}

func (f *fakeUserProviderEditor) CreateUserProvider(_ context.Context, form adapters.UserProviderForm) (adapters.UserProviderResult, error) {
	f.lastCreate = form
	return f.createRes, f.createErr
}
func (f *fakeUserProviderEditor) UpdateUserProvider(_ context.Context, id string, form adapters.UserProviderForm) (adapters.UserProviderResult, error) {
	f.lastUpdateID = id
	return f.updateRes, nil
}
func (f *fakeUserProviderEditor) DeleteUserProvider(_ context.Context, id string) (adapters.UserProviderResult, error) {
	f.lastDeleteID = id
	return f.deleteRes, nil
}
func (f *fakeUserProviderEditor) ReorderUserProvider(_ context.Context, id string, _ adapters.ReorderRequest) error {
	f.lastReorderID = id
	return f.reorderErr
}
func (f *fakeUserProviderEditor) VerifyChannel(context.Context, adapters.VerifyChannelRequest) (adapters.VerifyChannelResult, error) {
	return f.verifyRes, nil
}

func newCatalogTestServer(t *testing.T, ed adapters.UserProviderEditor, ensure func(string) error) *Server {
	t.Helper()
	cfg := nonZeroConfig()
	cfg.UserProviderEditor = ed
	cfg.EnsureAdapterStarted = ensure
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func catalogRoute(t *testing.T, s *Server) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	s.Mount(mux)
	return mux
}

func postJSON(t *testing.T, h http.HandlerFunc, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}

func routeJSON(t *testing.T, h http.Handler, method, target string, body any, sameOrigin bool) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	if sameOrigin {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	} else {
		req.Header.Set("Sec-Fetch-Site", "cross-site")
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandleCatalogProviderCreate_AutoEnableInvokesEnsure(t *testing.T) {
	t.Parallel()
	ed := &fakeUserProviderEditor{createRes: adapters.UserProviderResult{
		Provider:         adapters.CatalogProvider{ID: "user:mix", DisplayName: "Mix", BadgeLabel: "MX", BadgeClass: "u-teal"},
		AutoEnableNeeded: true,
	}}
	called := false
	ensure := func(name string) error { called = (name == "streams"); return nil }
	s := newCatalogTestServer(t, ed, ensure)

	rr := postJSON(t, s.handleCatalogProviderCreate, http.MethodPost, "/ui/catalog/provider",
		map[string]any{"displayName": "Mix", "badgeLabel": "MX", "badgeColor": "teal",
			"channels": []map[string]any{{"name": "Live", "url": "https://cdn.example.com/live.m3u8"}}})

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !called {
		t.Fatal("EnsureAdapterStarted not called on first-provider create")
	}
	var resp struct {
		OK                 bool   `json:"ok"`
		Provider           struct{ ID string `json:"id"` } `json:"provider"`
		AutoEnabledStreams string `json:"autoEnabledStreams"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK || resp.Provider.ID != "user:mix" || resp.AutoEnabledStreams != "on" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestHandleCatalogProviderCreate_AutoEnableFailureReportsRestart(t *testing.T) {
	t.Parallel()
	ed := &fakeUserProviderEditor{createRes: adapters.UserProviderResult{
		Provider: adapters.CatalogProvider{ID: "user:mix"}, AutoEnableNeeded: true,
	}}
	ensure := func(string) error { return errors.New("yt-dlp missing") }
	s := newCatalogTestServer(t, ed, ensure)
	rr := postJSON(t, s.handleCatalogProviderCreate, http.MethodPost, "/ui/catalog/provider",
		map[string]any{"displayName": "Mix", "badgeLabel": "MX", "badgeColor": "teal"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (provider still saved), body = %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		AutoEnabledStreams string `json:"autoEnabledStreams"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.AutoEnabledStreams != "restart-required" {
		t.Fatalf("autoEnabledStreams = %q, want restart-required", resp.AutoEnabledStreams)
	}
}

func TestHandleCatalogChannelVerify_ReturnsResult(t *testing.T) {
	t.Parallel()
	ed := &fakeUserProviderEditor{verifyRes: adapters.VerifyChannelResult{OK: true, Kind: "playlist", ItemCount: 47}}
	s := newCatalogTestServer(t, ed, nil)
	rr := postJSON(t, s.handleCatalogChannelVerify, http.MethodPost, "/ui/catalog/channel/verify",
		map[string]any{"url": "https://www.youtube.com/playlist?list=PL1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var vr adapters.VerifyChannelResult
	if err := json.Unmarshal(rr.Body.Bytes(), &vr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !vr.OK || vr.ItemCount != 47 {
		t.Fatalf("verify resp = %+v", vr)
	}
}

func TestCatalogProviderRoutes_MountedMethodsCallEditor(t *testing.T) {
	t.Parallel()
	ed := &fakeUserProviderEditor{
		updateRes: adapters.UserProviderResult{
			Provider: adapters.CatalogProvider{ID: "user:mix", DisplayName: "Mix"},
		},
		deleteRes: adapters.UserProviderResult{ClearedSlots: []int{2}},
		verifyRes: adapters.VerifyChannelResult{OK: true, Kind: "direct"},
	}
	s := newCatalogTestServer(t, ed, nil)
	mux := catalogRoute(t, s)

	if rr := routeJSON(t, mux, http.MethodPut, "/ui/catalog/provider/user:mix", map[string]any{"displayName": "Mix"}, true); rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", rr.Code, rr.Body.String())
	}
	if ed.lastUpdateID != "user:mix" {
		t.Fatalf("lastUpdateID = %q", ed.lastUpdateID)
	}
	if rr := routeJSON(t, mux, http.MethodPost, "/ui/catalog/provider/user:mix/reorder", map[string]any{"channels": []map[string]any{{"id": "a", "order": 1}}}, true); rr.Code != http.StatusOK {
		t.Fatalf("reorder status = %d body=%s", rr.Code, rr.Body.String())
	}
	if ed.lastReorderID != "user:mix" {
		t.Fatalf("lastReorderID = %q", ed.lastReorderID)
	}
	if rr := routeJSON(t, mux, http.MethodDelete, "/ui/catalog/provider/user:mix", map[string]any{}, true); rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d body=%s", rr.Code, rr.Body.String())
	} else {
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal delete response: %v", err)
		}
		if _, ok := body["provider"]; ok {
			t.Fatalf("delete response unexpectedly included provider: %s", rr.Body.String())
		}
	}
	if ed.lastDeleteID != "user:mix" {
		t.Fatalf("lastDeleteID = %q", ed.lastDeleteID)
	}
	if rr := routeJSON(t, mux, http.MethodPost, "/ui/catalog/channel/verify", map[string]any{"url": "https://cdn.example.com/live.m3u8"}, true); rr.Code != http.StatusOK {
		t.Fatalf("verify status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCatalogProviderRoutes_BlockCrossSiteUnsafeMethods(t *testing.T) {
	t.Parallel()
	s := newCatalogTestServer(t, &fakeUserProviderEditor{}, nil)
	mux := catalogRoute(t, s)
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/ui/catalog/provider/user:mix"},
		{http.MethodDelete, "/ui/catalog/provider/user:mix"},
	}
	for _, tc := range cases {
		rr := routeJSON(t, mux, tc.method, tc.path, map[string]any{}, false)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d, want 403", tc.method, tc.path, rr.Code)
		}
	}
}

func TestHandleCatalogProviderCreate_ValidationErrorRendersChip(t *testing.T) {
	t.Parallel()
	ed := &fakeUserProviderEditor{createErr: &adapters.QuickCastError{Status: http.StatusBadRequest, Chip: "BAD INPUT", Message: "bad color"}}
	s := newCatalogTestServer(t, ed, nil)
	rr := postJSON(t, s.handleCatalogProviderCreate, http.MethodPost, "/ui/catalog/provider",
		map[string]any{"displayName": "Mix"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rr.Code, rr.Body.String())
	}
}
