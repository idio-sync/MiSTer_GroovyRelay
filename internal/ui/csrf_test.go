package ui

import (
	"net/http"
	"net/http/httptest"
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
