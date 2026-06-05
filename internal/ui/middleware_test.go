package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeFirstRun struct{ first bool }

func (f *fakeFirstRun) IsFirstRun() bool       { return f.first }
func (f *fakeFirstRun) DismissFirstRun() error { f.first = false; return nil }

func newGuardedHandler(saver FirstRunAware) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("inner"))
	})
	return firstRunGuard(saver)(inner)
}

func TestFirstRunGuard_NilSaver_PassesThrough(t *testing.T) {
	h := newGuardedHandler(nil)
	r := httptest.NewRequest("GET", "/old_ui/bridge", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("nil saver: got %d", w.Code)
	}
}

func TestFirstRunGuard_FlagCleared_PassesThrough(t *testing.T) {
	h := newGuardedHandler(&fakeFirstRun{first: false})
	r := httptest.NewRequest("GET", "/old_ui/bridge", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("flag cleared: got %d", w.Code)
	}
}

func TestFirstRunGuard_FlagSet_GET_RedirectsToSetup(t *testing.T) {
	h := newGuardedHandler(&fakeFirstRun{first: true})
	r := httptest.NewRequest("GET", "/old_ui/bridge", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 302 {
		t.Errorf("got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/old_ui/setup" {
		t.Errorf("Location: %q", loc)
	}
}

func TestFirstRunGuard_FlagSet_POST_Returns409(t *testing.T) {
	h := newGuardedHandler(&fakeFirstRun{first: true})
	r := httptest.NewRequest("POST", "/old_ui/bridge/save", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 409 {
		t.Errorf("got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "first-run not complete") {
		t.Errorf("body: %s", w.Body.String())
	}
}

func TestFirstRunGuard_FlagSet_SetupBypass(t *testing.T) {
	h := newGuardedHandler(&fakeFirstRun{first: true})
	for _, p := range []string{"/old_ui/setup", "/old_ui/setup/step/bridge", "/old_ui/setup/done"} {
		r := httptest.NewRequest("GET", p, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("%s: got %d", p, w.Code)
		}
	}
}

func TestFirstRunGuard_FlagSet_StaticBypass(t *testing.T) {
	h := newGuardedHandler(&fakeFirstRun{first: true})
	for _, p := range []string{"/old_ui/static/app.css", "/old_ui/static/fonts/inter.woff2"} {
		r := httptest.NewRequest("GET", p, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("%s: got %d", p, w.Code)
		}
	}
}

func TestFirstRunGuard_FlagSet_WizardAdapterRoutesBypass(t *testing.T) {
	h := newGuardedHandler(&fakeFirstRun{first: true})
	cases := []struct{ method, path string }{
		{"POST", "/old_ui/adapter/plex/link/start"},
		{"POST", "/old_ui/adapter/plex/link/cancel"},
		{"GET", "/old_ui/adapter/plex/link/status"},
		{"POST", "/old_ui/adapter/jellyfin/link/start"},
		{"POST", "/old_ui/adapter/plex/save"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("%s %s: got %d", tc.method, tc.path, w.Code)
		}
	}
}

func TestFirstRunGuard_FlagSet_NonWizardAdapterRoutesBlocked(t *testing.T) {
	h := newGuardedHandler(&fakeFirstRun{first: true})
	r := httptest.NewRequest("POST", "/old_ui/adapter/plex/toggle", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 409 {
		t.Errorf("toggle: got %d", w.Code)
	}
}
