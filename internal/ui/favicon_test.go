package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServer_ShellLoadsFavicons(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/old_ui/", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	body := rw.Body.String()
	for _, want := range []string{
		`<link rel="icon" href="/old_ui/static/favicon.svg" type="image/svg+xml">`,
		`<link rel="icon" href="/old_ui/static/favicon-32.png" sizes="32x32" type="image/png">`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing favicon link %q: %s", want, body)
		}
	}
}

func TestServer_StaticFavicons(t *testing.T) {
	_, mux := newTestServer(t)

	for _, tc := range []struct {
		path      string
		wantHead  string
		wantBytes []byte
	}{
		{
			path:     "/old_ui/static/favicon.svg",
			wantHead: "<svg",
		},
		{
			path:      "/old_ui/static/favicon-32.png",
			wantBytes: []byte{0x89, 'P', 'N', 'G'},
		},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Fatalf("%s status = %d", tc.path, rw.Code)
		}
		if tc.wantHead != "" && !strings.Contains(rw.Body.String(), tc.wantHead) {
			t.Fatalf("%s missing %q: %s", tc.path, tc.wantHead, rw.Body.String())
		}
		if len(tc.wantBytes) > 0 {
			got := rw.Body.Bytes()
			if len(got) < len(tc.wantBytes) || string(got[:len(tc.wantBytes)]) != string(tc.wantBytes) {
				t.Fatalf("%s missing PNG header", tc.path)
			}
		}
	}
}
