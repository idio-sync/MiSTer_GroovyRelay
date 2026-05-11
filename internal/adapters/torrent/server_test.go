package torrent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsLoopbackRemoteAcceptsIPv4AndIPv6Loopback(t *testing.T) {
	for _, remote := range []string{"127.0.0.1:1234", "127.42.0.9:1234", "[::1]:1234"} {
		if !isLoopbackRemote(remote) {
			t.Fatalf("isLoopbackRemote(%q) = false, want true", remote)
		}
	}
}

func TestIsLoopbackRemoteRejectsLANAndHostnames(t *testing.T) {
	for _, remote := range []string{"192.168.1.5:1234", "10.0.0.5:1234", "example.com:1234"} {
		if isLoopbackRemote(remote) {
			t.Fatalf("isLoopbackRemote(%q) = true, want false", remote)
		}
	}
}

func TestMediaRouteRejectsNonLoopbackBeforeOpeningTorrent(t *testing.T) {
	torrent := &fakeTorrent{hash: "01234567", files: []FileCandidate{{DisplayPath: "movie.mkv", Length: 5, Index: 0}}}
	a := &Adapter{sessions: map[string]*Session{
		"tok": {Token: "tok", Torrent: torrent, FileIndex: 0, Title: "movie.mkv"},
	}}
	req := httptest.NewRequest(http.MethodGet, "/torrent/session/tok/media", nil)
	req.RemoteAddr = "192.168.1.5:1234"
	rr := httptest.NewRecorder()
	a.handleMedia(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestMediaRouteMissingToken(t *testing.T) {
	a := &Adapter{sessions: map[string]*Session{}}
	req := httptest.NewRequest(http.MethodGet, "/torrent/session/missing/media", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	a.handleMedia(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestMountPublicRoutesRegistersTokenizedMedia(t *testing.T) {
	torrent := &fakeTorrent{hash: "01234567", files: []FileCandidate{{DisplayPath: "movie.mkv", Length: 5, Index: 0}}}
	a := &Adapter{sessions: map[string]*Session{
		"tok": {Token: "tok", Torrent: torrent, FileIndex: 0, Title: "movie.mkv"},
	}}
	mux := http.NewServeMux()
	a.MountPublicRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/torrent/session/tok/media", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Range", "bytes=1-3")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 body=%q", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != "ide" {
		t.Fatalf("body = %q, want ide", got)
	}
}

func TestMediaRouteServesRange(t *testing.T) {
	torrent := &fakeTorrent{hash: "01234567", files: []FileCandidate{{DisplayPath: "movie.mkv", Length: 5, Index: 0}}}
	a := &Adapter{sessions: map[string]*Session{
		"tok": {Token: "tok", Torrent: torrent, FileIndex: 0, Title: "movie.mkv"},
	}}
	req := httptest.NewRequest(http.MethodGet, "/torrent/session/tok/media", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Range", "bytes=1-3")
	rr := httptest.NewRecorder()
	a.handleMedia(rr, req)
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 body=%q", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != "ide" {
		t.Fatalf("body = %q, want ide", got)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "video") && ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
}
