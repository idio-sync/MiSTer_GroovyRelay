package chassis

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestCatalogDrawer_RendersUserAffordances(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	data := CatalogData{
		ActiveProviderID: "user:mix",
		Providers: []CatalogProviderTab{
			{ID: "mtv-rewind", DisplayName: "MTV Rewind", BadgeLabel: "MTV", BadgeClass: "mtv"},
			{ID: "user:mix", DisplayName: "Mix", BadgeLabel: "MX", BadgeClass: "u-teal",
				Groups: []CatalogGroupTab{{ID: "", Name: "", Channels: []CatalogChannelCard{{ID: "live", Name: "Live"}}}}},
		},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "catalog-drawer", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `data-edit-provider="user:mix"`) {
		t.Error("user provider tab missing edit affordance")
	}
	if strings.Contains(out, `data-edit-provider="mtv-rewind"`) {
		t.Error("bundled provider tab must NOT have an edit affordance")
	}
	if !strings.Contains(out, `<button class="cf-pencil" type="button" data-edit-provider="user:mix"`) {
		t.Error("edit affordance must render as a sibling button")
	}
	providerAt := strings.Index(out, `data-provider="user:mix"`)
	if providerAt < 0 {
		t.Fatal("user provider tab missing")
	}
	closeAt := strings.Index(out[providerAt:], `</button>`)
	if closeAt < 0 {
		t.Fatal("user provider tab closing button missing")
	}
	editAt := strings.Index(out[providerAt:], `data-edit-provider="user:mix"`)
	if editAt < 0 {
		t.Fatal("user provider edit affordance missing")
	}
	if editAt < closeAt {
		t.Error("edit affordance must be outside the provider tab button")
	}
	if !strings.Contains(out, "catalog-provider-new") {
		t.Error("missing New provider tab")
	}
	if !strings.Contains(out, `id="catalog-form"`) {
		t.Error("missing authoring form container")
	}
}

func TestHandleCatalogProviderForm_ReturnsAuthoringJSON(t *testing.T) {
	t.Parallel()
	v := catalogProviderFormViewer{form: adapters.UserProviderForm{
		ID: "user:f1-tv", DisplayName: "F1 TV", BadgeLabel: "F1", BadgeColor: "amber",
		Groups: []adapters.UserGroupForm{{ID: "races", Name: "Races", Order: 0}},
		Channels: []adapters.UserChannelForm{
			{ID: "live", Name: "Live", URL: "https://cdn.example.com/live.m3u8", Kind: "direct", GroupID: "races", Order: 0},
		},
	}}
	s, err := New(Config{Version: "t", StartedAt: time.Now(), UserProviderViewer: v})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ui/catalog/provider/user:f1-tv", nil)
	req.SetPathValue("id", "user:f1-tv")
	rec := httptest.NewRecorder()
	s.handleCatalogProviderForm(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got catalogProviderFormResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.OK || got.ID != "user:f1-tv" || got.BadgeColor != "amber" {
		t.Fatalf("identity wrong: %+v", got)
	}
	if len(got.Channels) != 1 || got.Channels[0].URL != "https://cdn.example.com/live.m3u8" || got.Channels[0].Kind != "direct" {
		t.Fatalf("channels wrong: %+v", got.Channels)
	}
}

func TestHandleCatalogProviderForm_NotFound(t *testing.T) {
	t.Parallel()
	s, err := New(Config{Version: "t", StartedAt: time.Now(), UserProviderViewer: catalogProviderFormViewer{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ui/catalog/provider/user:ghost", nil)
	req.SetPathValue("id", "user:ghost")
	rec := httptest.NewRecorder()
	s.handleCatalogProviderForm(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCatalogProviderFormRoute_MountedAndReadGuarded(t *testing.T) {
	t.Parallel()
	v := catalogProviderFormViewer{form: adapters.UserProviderForm{ID: "user:f1-tv", DisplayName: "F1 TV", BadgeLabel: "F1", BadgeColor: "amber"}}
	s, err := New(Config{Version: "t", StartedAt: time.Now(), UserProviderViewer: v})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/ui/catalog/provider/user:f1-tv", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "http://example.com/ui/catalog/provider/user:f1-tv", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site status = %d, want 403", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "http://example.com/ui/catalog/provider/user:f1-tv", nil)
	req.Header.Set("Origin", "http://example.com")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback origin status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "http://example.com/ui/catalog/provider/user:f1-tv", nil)
	req.Header.Set("Referer", "http://example.com/ui/catalog")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback referer status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "http://example.com/ui/catalog/provider/user:f1-tv", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("fallback missing headers status = %d, want 403", rec.Code)
	}
}

func TestCatalogEnvelopeFromData_And_Changed(t *testing.T) {
	t.Parallel()
	data := CatalogData{Providers: []CatalogProviderTab{
		{ID: "user:mix", DisplayName: "Mix", BadgeLabel: "MX", BadgeClass: "u-teal", Live: false,
			Groups: []CatalogGroupTab{{ID: "", Name: "", Channels: []CatalogChannelCard{
				{ID: "live", Name: "Live", PlayMode: "", Live: true},
			}}}},
	}}
	env := catalogEnvelopeFromData(data)
	if len(env.Providers) != 1 || env.Providers[0].ID != "user:mix" || env.Providers[0].BadgeClass != "u-teal" {
		t.Fatalf("provider envelope wrong: %+v", env.Providers)
	}
	if len(env.Providers[0].Groups[0].Channels) != 1 || !env.Providers[0].Groups[0].Channels[0].Live {
		t.Fatalf("channel envelope wrong: %+v", env.Providers[0].Groups)
	}
	if catalogChanged(env, env) {
		t.Fatal("identical envelopes reported changed")
	}
	next := catalogEnvelopeFromData(data)
	next.Providers[0].DisplayName = "Mixed"
	if !catalogChanged(env, next) {
		t.Fatal("display-name change not detected")
	}
}

func TestProviderStatusEnvelopes_And_Fingerprint(t *testing.T) {
	t.Parallel()
	in := []adapters.UserProviderStatus{
		{
			ProviderID: "user:mix",
			Channels: []adapters.UserChannelStatus{
				{ChannelID: "lofi", State: "ready", ItemCount: 9},
				{ChannelID: "news", State: "pending"},
			},
		},
	}
	envs := providerStatusEnvelopesFrom(in)
	if len(envs) != 1 {
		t.Fatalf("len(envs) = %d, want 1", len(envs))
	}
	got := envs[0]
	if got.Provider != "user:mix" {
		t.Fatalf("Provider = %q, want user:mix", got.Provider)
	}
	if got.AutoEnabledStreams != "" {
		t.Fatalf("AutoEnabledStreams = %q, want empty", got.AutoEnabledStreams)
	}
	if len(got.Channels) != 2 {
		t.Fatalf("len(Channels) = %d, want 2", len(got.Channels))
	}
	if got.Channels[0].Channel != "lofi" || got.Channels[0].State != "ready" || got.Channels[0].ItemCount != 9 {
		t.Fatalf("Channels[0] = %+v, want lofi ready count 9", got.Channels[0])
	}
	if got.Channels[1].Channel != "news" || got.Channels[1].State != "pending" || got.Channels[1].ItemCount != 0 {
		t.Fatalf("Channels[1] = %+v, want news pending count 0", got.Channels[1])
	}

	fp := providerStatusFingerprint(got)
	again := providerStatusFingerprint(providerStatusEnvelopesFrom(in)[0])
	if fp != again {
		t.Fatalf("fingerprint unstable: %q then %q", fp, again)
	}
	next := providerStatusEnvelopesFrom(in)[0]
	next.Channels[1].State = "ready"
	if fp == providerStatusFingerprint(next) {
		t.Fatalf("fingerprint did not change after channel state change: %q", fp)
	}
}

type catalogProviderFormViewer struct {
	form adapters.UserProviderForm
}

func (v catalogProviderFormViewer) UserProviderForm(id string) (adapters.UserProviderForm, bool) {
	if v.form.ID == id {
		return v.form, true
	}
	return adapters.UserProviderForm{}, false
}
func (catalogProviderFormViewer) UserProviderStatuses() []adapters.UserProviderStatus { return nil }
