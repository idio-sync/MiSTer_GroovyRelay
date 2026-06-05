package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	uipkg "github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

// TestCutover_DualMount asserts the chassis (/ui), legacy UI (/old_ui), and
// Companion API (/ui/companion) coexist on one mux without a duplicate-pattern
// panic, and that the swapped routes resolve to the right surface.
func TestCutover_DualMount(t *testing.T) {
	reg := adapters.NewRegistry()

	ui, err := uipkg.New(uipkg.Config{Registry: reg})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	ch, err := chassis.New(chassis.Config{Version: "test", StartedAt: time.Now(), Registry: reg})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	t.Cleanup(func() { _ = ch.Close() })

	mux := http.NewServeMux()
	// Must not panic on duplicate patterns — mirrors main.go mount order.
	ui.Mount(mux)
	ch.Mount(mux)

	check := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: code=%d want %d; body=%q", method, path, rec.Code, want, rec.Body.String())
		}
		return rec
	}

	checkBodyContains := func(rec *httptest.ResponseRecorder, needle string) {
		t.Helper()
		if !strings.Contains(rec.Body.String(), needle) {
			t.Fatalf("body missing %q; body=%q", needle, rec.Body.String())
		}
	}

	checkLocation := func(rec *httptest.ResponseRecorder, want string) {
		t.Helper()
		if got := rec.Header().Get("Location"); got != want {
			t.Fatalf("Location=%q want %q", got, want)
		}
	}

	checkCompanionPreflight := func(path string) {
		t.Helper()
		const origin = "moz-extension://groovy-relay"
		req := httptest.NewRequest(http.MethodOptions, path, nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type, x-bridge-extension")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("OPTIONS %s: code=%d want %d", path, rec.Code, http.StatusNoContent)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("OPTIONS %s: Access-Control-Allow-Origin=%q want %q", path, got, origin)
		}
	}

	checkLocation(check("GET", "/", "", http.StatusFound), "/ui/")
	checkBodyContains(check("GET", "/ui", "", http.StatusOK), "/ui/static/chassis.css")
	checkBodyContains(check("GET", "/ui/", "", http.StatusOK), "/ui/static/chassis.css")
	checkBodyContains(check("GET", "/old_ui/", "", http.StatusOK), "/old_ui/static/app.css")
	checkLocation(check("GET", "/old_ui/setup", "", http.StatusFound), "/old_ui/setup/step/adapters")
	check("POST", "/old_ui/setup/step/bridge", "mister.host=192.0.2.10", http.StatusInternalServerError)

	// Companion status requires the browser-extension origin+header gate.
	compStatusRec := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/ui/companion/status", nil)
		req.Header.Set("Origin", "moz-extension://groovy-relay")
		req.Header.Set("X-Bridge-Extension", "1")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}()
	if compStatusRec.Code != http.StatusOK {
		t.Fatalf("GET /ui/companion/status: code=%d want 200; body=%q", compStatusRec.Code, compStatusRec.Body.String())
	}
	checkBodyContains(compStatusRec, `"ok":true`)
	checkCompanionPreflight("/ui/companion/status")
	checkCompanionPreflight("/ui/companion/history/delete")

	// Chassis action route is registered at /ui and reaches the handler. With
	// an empty registry this returns the handler's JSON 404, not mux NotFound.
	checkBodyContains(check("POST", "/ui/cast", "kind=url&payload=https%3A%2F%2Fexample.com%2Fvideo.mp4", http.StatusNotFound), "NOT FOUND")
	// The legacy broad OPTIONS /ui/ preflight must not survive and catch chassis routes.
	check("OPTIONS", "/ui/cast", "", http.StatusMethodNotAllowed)

	check("GET", "/receiver", "", http.StatusNotFound)
	check("GET", "/receiver/static/chassis.css", "", http.StatusNotFound)
}
