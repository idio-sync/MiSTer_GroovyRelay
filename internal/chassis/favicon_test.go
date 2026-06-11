package chassis

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleIndex_FaviconURLsIncludeStaticFingerprint(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rr := httptest.NewRecorder()

	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`/ui/static/favicon.svg?v=test-1.0.0-`,
		`/ui/static/favicon-32.png?v=test-1.0.0-`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing versioned favicon URL prefix %q: %s", want, body)
		}
	}
}

func TestHandleStatic_FaviconsServed(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	for _, tc := range []struct {
		path      string
		wantHead  string
		wantBytes []byte
	}{
		{
			path:     "/ui/static/favicon.svg",
			wantHead: "<svg",
		},
		{
			path:      "/ui/static/favicon-32.png",
			wantBytes: []byte{0x89, 'P', 'N', 'G'},
		},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", tc.path, rr.Code, http.StatusOK)
		}
		if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "max-age=31536000") {
			t.Fatalf("%s Cache-Control = %q, want immutable asset cache", tc.path, got)
		}
		if tc.wantHead != "" && !strings.Contains(rr.Body.String(), tc.wantHead) {
			t.Fatalf("%s missing %q: %s", tc.path, tc.wantHead, rr.Body.String())
		}
		if len(tc.wantBytes) > 0 {
			got := rr.Body.Bytes()
			if len(got) < len(tc.wantBytes) || string(got[:len(tc.wantBytes)]) != string(tc.wantBytes) {
				t.Fatalf("%s missing PNG header", tc.path)
			}
		}
	}
}
