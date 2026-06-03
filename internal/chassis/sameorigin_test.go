package chassis

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequireSameOrigin_AllowsSameOrigin(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/ui/visualizer", strings.NewReader(""))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()

	requireSameOrigin(next).ServeHTTP(w, req)

	if !called {
		t.Fatal("next handler was not called")
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestRequireSameOrigin_AllowsSameSite(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/ui/visualizer", strings.NewReader(""))
	req.Header.Set("Sec-Fetch-Site", "same-site")
	w := httptest.NewRecorder()

	requireSameOrigin(next).ServeHTTP(w, req)

	if !called {
		t.Fatal("next handler was not called")
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestRequireSameOrigin_BlocksNone(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/ui/visualizer", strings.NewReader(""))
	req.Header.Set("Sec-Fetch-Site", "none")
	w := httptest.NewRecorder()

	requireSameOrigin(next).ServeHTTP(w, req)

	if called {
		t.Fatal("next handler was called")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestRequireSameOrigin_BlocksCrossSite(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/ui/visualizer", strings.NewReader(""))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()

	requireSameOrigin(next).ServeHTTP(w, req)

	if called {
		t.Fatal("next handler was called")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestRequireSameOrigin_BlocksMissingHeader(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/ui/visualizer", strings.NewReader(""))
	w := httptest.NewRecorder()

	requireSameOrigin(next).ServeHTTP(w, req)

	if called {
		t.Fatal("next handler was called")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestRequireSameOrigin_AllowsMissingFetchMetadataWithSameOriginOrigin(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "http://example.com/ui/cast", strings.NewReader(""))
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()

	requireSameOrigin(next).ServeHTTP(w, req)

	if !called {
		t.Fatal("next handler was not called")
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestRequireSameOrigin_AllowsMissingFetchMetadataWithSameOriginReferer(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "http://example.com/ui/volume", strings.NewReader(""))
	req.Header.Set("Referer", "http://example.com/ui")
	w := httptest.NewRecorder()

	requireSameOrigin(next).ServeHTTP(w, req)

	if !called {
		t.Fatal("next handler was not called")
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestRequireSameOrigin_BlocksMissingFetchMetadataWithCrossOriginFallback(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "http://example.com/ui/cast", strings.NewReader(""))
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Referer", "http://evil.example/ui")
	w := httptest.NewRecorder()

	requireSameOrigin(next).ServeHTTP(w, req)

	if called {
		t.Fatal("next handler was called")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestRequireSameOrigin_Returns403JSON(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/ui/visualizer", strings.NewReader(""))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()

	requireSameOrigin(next).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	if !strings.Contains(w.Body.String(), `"error":"cross-site request blocked"`) {
		t.Fatalf("body = %q, want JSON error", w.Body.String())
	}
}

func TestRequireSameOriginBlocksCrossSiteUnsafeMethods(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			called := false
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
			req := httptest.NewRequest(method, "/ui/catalog/provider/user:mix", nil)
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			w := httptest.NewRecorder()
			requireSameOrigin(next).ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", w.Code)
			}
			if called {
				t.Fatal("next handler called for cross-site unsafe method")
			}
		})
	}
}

func TestRequireSameOriginAllowsSameOriginUnsafeMethods(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			req := httptest.NewRequest(method, "/ui/catalog/provider/user:mix", nil)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			w := httptest.NewRecorder()
			requireSameOrigin(next).ServeHTTP(w, req)
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", w.Code)
			}
			if !called {
				t.Fatal("next handler not called for same-origin unsafe method")
			}
		})
	}
}
