package chassis

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestHandleCatalogProviderForm_ReturnsAuthoringJSON(t *testing.T) {
	t.Parallel()
	v := formViewer{form: adapters.UserProviderForm{
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
	s, err := New(Config{Version: "t", StartedAt: time.Now(), UserProviderViewer: formViewer{}})
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
	v := formViewer{form: adapters.UserProviderForm{ID: "user:f1-tv", DisplayName: "F1 TV", BadgeLabel: "F1", BadgeColor: "amber"}}
	s, err := New(Config{Version: "t", StartedAt: time.Now(), UserProviderViewer: v})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/catalog/provider/user:f1-tv", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/catalog/provider/user:f1-tv", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site status = %d, want 403", rec.Code)
	}
}

type formViewer struct {
	form adapters.UserProviderForm
}

func (v formViewer) UserProviderForm(id string) (adapters.UserProviderForm, bool) {
	if v.form.ID == id {
		return v.form, true
	}
	return adapters.UserProviderForm{}, false
}
func (formViewer) UserProviderStatuses() []adapters.UserProviderStatus { return nil }
