package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSRF_GetAlwaysAllowed(t *testing.T) {
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Errorf("GET blocked: status = %d", rw.Code)
	}
}

func TestCSRF_PostSameOriginAllowed(t *testing.T) {
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/ui/bridge/save", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Errorf("same-origin POST blocked: status = %d", rw.Code)
	}
}

func TestCSRF_PostSecFetchSiteNoneAllowed(t *testing.T) {
	// "none" means user typed URL / used bookmark. Not a cross-origin
	// attack. Must be allowed.
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/ui/bridge/save", nil)
	req.Header.Set("Sec-Fetch-Site", "none")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Errorf("sec-fetch-site=none POST blocked: status = %d", rw.Code)
	}
}

func TestCSRF_PostCrossSiteRejected(t *testing.T) {
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/ui/bridge/save", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Errorf("cross-site POST status = %d, want 403", rw.Code)
	}
}

func TestCSRF_PostOriginMatchesHost(t *testing.T) {
	// No Sec-Fetch-Site: fall back to Origin check.
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/ui/bridge/save", nil)
	req.Host = "bridge.lan:32500"
	req.Header.Set("Origin", "http://bridge.lan:32500")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Errorf("matching Origin blocked: status = %d", rw.Code)
	}
}

func TestCSRF_PostOriginDiffersRejected(t *testing.T) {
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/ui/bridge/save", nil)
	req.Host = "bridge.lan:32500"
	req.Header.Set("Origin", "http://evil.example.com")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Errorf("mismatched Origin status = %d, want 403", rw.Code)
	}
}

func TestCSRF_PostNoHeadersRejected(t *testing.T) {
	// No Sec-Fetch-Site, no Origin — refuse by default. A curl user
	// who legitimately wants to POST from the same machine can set
	// Origin: http://localhost:32500 or pass -H "Sec-Fetch-Site: same-origin".
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/ui/bridge/save", nil)
	req.Host = "bridge.lan:32500"
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Errorf("no-header POST status = %d, want 403", rw.Code)
	}
}

func TestCSRF_ExtensionOrigin_MozWithHeader_Accepts(t *testing.T) {
	// First-pass bypass tier: moz-extension Origin + X-Bridge-Extension
	// header must be accepted even though Sec-Fetch-Site is "cross-site"
	// (which would otherwise reject). This demonstrates the bypass runs
	// BEFORE the Sec-Fetch-Site check.
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", nil)
	req.Host = "bridge.lan:32500"
	req.Header.Set("Origin", "moz-extension://abcd-1234-5678-9abc")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Errorf("moz-extension + header POST status = %d, want 200", rw.Code)
	}
}

func TestCSRF_ExtensionOrigin_ChromeWithHeader_Accepts(t *testing.T) {
	// Chrome / Edge / Brave / Opera / Vivaldi all use chrome-extension://
	// origins. One test covers them all.
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", nil)
	req.Host = "bridge.lan:32500"
	req.Header.Set("Origin", "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Errorf("chrome-extension + header POST status = %d, want 200", rw.Code)
	}
}

func TestCSRF_ExtensionOrigin_SafariWithHeader_Accepts(t *testing.T) {
	// Safari is excluded from the v1 manual-test matrix in the spec
	// (heavier signing pipeline), but the scheme is in the bypass
	// allowlist; this test pins the contract so a future scheme-list
	// edit can't silently drop Safari support.
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", nil)
	req.Host = "bridge.lan:32500"
	req.Header.Set("Origin", "safari-web-extension://AAAAAAAA-1234-5678-9abc-def012345678")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Errorf("safari-web-extension + header POST status = %d, want 200", rw.Code)
	}
}

func TestCSRF_ExtensionOrigin_NoHeader_Rejects(t *testing.T) {
	// An extension that forgot to set X-Bridge-Extension: 1 is just a
	// cross-origin caller from the middleware's point of view. Bypass
	// requires both signals; the header alone is what distinguishes
	// "the operator's companion extension" from "any extension that
	// happens to fetch us." Sec-Fetch-Site: cross-site reject fires.
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", nil)
	req.Host = "bridge.lan:32500"
	req.Header.Set("Origin", "moz-extension://abcd-1234")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	// no X-Bridge-Extension header
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Errorf("ext-origin without header status = %d, want 403", rw.Code)
	}
}

func TestCSRF_HeaderWithoutExtensionOrigin_Rejects(t *testing.T) {
	// Hostile webpage attempting to forge the bypass: sets the
	// X-Bridge-Extension header but the Origin is the page's actual
	// origin (which the browser fixes — no JS can override). Bypass
	// fails the isExtensionOrigin check; falls through to
	// Sec-Fetch-Site: cross-site reject.
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", nil)
	req.Host = "bridge.lan:32500"
	req.Header.Set("Origin", "https://attacker.example.com")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("X-Bridge-Extension", "1")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Errorf("header without ext-origin status = %d, want 403", rw.Code)
	}
}

func TestCSRF_HeaderWithEmptyOrigin_Rejects(t *testing.T) {
	// X-Bridge-Extension is set but Origin is missing entirely (no
	// browser would do this; misbehaving curl-based test could).
	// isExtensionOrigin("") is false, so the bypass tier doesn't fire.
	// No Sec-Fetch-Site either, so the request lands on the
	// origin-empty reject branch. Asserting on the response body string
	// pins which of the three pre-existing reject branches handled the
	// request, defending against future refactors silently re-ordering
	// the inner conditions.
	h := csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/ui/adapter/url/play", nil)
	req.Host = "bridge.lan:32500"
	req.Header.Set("X-Bridge-Extension", "1")
	// no Origin, no Sec-Fetch-Site
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Errorf("header with empty origin status = %d, want 403", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "CSRF: missing Origin") {
		t.Errorf("response body = %q, want substring %q", rw.Body.String(), "CSRF: missing Origin")
	}
}
