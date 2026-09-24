package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestStart_NoToken_Errors(t *testing.T) {
	a := New(nil, t.TempDir(), "device-1", "", nil)
	a.cfg = Config{ServerURL: "https://jf.example.com", MaxVideoBitrateKbps: 4000, Enabled: true}

	err := a.Start(t.Context())
	if err == nil {
		t.Fatal("Start without token returned nil, want error")
	}
}

func TestStart_TokenProbe401_WipesAndError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	a := New(nil, t.TempDir(), "device-1", "", nil)
	a.cfg = Config{ServerURL: srv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "stale", ServerURL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	if err := a.Start(t.Context()); err == nil {
		t.Fatal("Start with rejected token returned nil, want error")
	}

	tok, _ := LoadToken(a.tokenPath())
	if tok != (Token{}) {
		t.Errorf("token should have been wiped, got %+v", tok)
	}
	if a.Status().State != adapters.StateError {
		t.Errorf("state = %v, want StateError", a.Status().State)
	}
}

func TestStart_HappyPath_DialsAndPostsCapabilities(t *testing.T) {
	var (
		mu        sync.Mutex
		capPosted int
		dialed    int
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/System/Info", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	mux.HandleFunc("/Sessions/Capabilities/Full", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capPosted++
		mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("/Sessions", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/socket", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		dialed++
		mu.Unlock()
		<-r.Context().Done()
		_ = conn.Close(websocket.StatusNormalClosure, "")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := New(nil, t.TempDir(), "device-1", "", nil)
	a.cfg = Config{ServerURL: srv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", ServerURL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := dialed >= 1 && capPosted >= 1
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if dialed < 1 {
		t.Errorf("dialed = %d, want >= 1", dialed)
	}
	if capPosted < 1 {
		t.Errorf("capPosted = %d, want >= 1", capPosted)
	}

	if got := a.Status().State; got != adapters.StateRunning {
		t.Errorf("state = %v, want StateRunning", got)
	}

	_ = a.Stop()
}

func TestStart_ServerURLDriftForcesUnlink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	a := New(nil, t.TempDir(), "device-1", "", nil)
	a.cfg = Config{ServerURL: srv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	// Token says we linked against a DIFFERENT server.
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", ServerURL: "https://old.example.com"}); err != nil {
		t.Fatal(err)
	}

	err := a.Start(t.Context())
	if err == nil {
		t.Fatal("Start with URL-drift returned nil, want error")
	}
	tok, _ := LoadToken(a.tokenPath())
	if tok != (Token{}) {
		t.Errorf("token should have been wiped on URL drift, got %+v", tok)
	}
}

// jfFlakyProbeServer answers /System/Info with probeCodes in order (the
// last code repeats) and serves the happy-path session endpoints, counting
// Capabilities posts and websocket dials.
func jfFlakyProbeServer(t *testing.T, probeCodes ...int) (srv *httptest.Server, capPosted, dialed func() int) {
	t.Helper()
	var (
		mu               sync.Mutex
		probes, caps, ws int
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/System/Info", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		code := probeCodes[min(probes, len(probeCodes)-1)]
		probes++
		mu.Unlock()
		w.WriteHeader(code)
	})
	mux.HandleFunc("/Sessions/Capabilities/Full", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		caps++
		mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("/Sessions", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/socket", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		ws++
		mu.Unlock()
		<-r.Context().Done()
		_ = conn.Close(websocket.StatusNormalClosure, "")
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	read := func(p *int) func() int {
		return func() int { mu.Lock(); defer mu.Unlock(); return *p }
	}
	return srv, read(&caps), read(&ws)
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A server that isn't up yet when the relay starts (compose/Unraid boot
// race) must not park the adapter in StateError forever: Start keeps the
// token and the session loop re-probes until the server answers.
func TestStart_ServerUnavailableAtBoot_RetriesUntilUp(t *testing.T) {
	// Start's probe and the loop's first re-probe fail; the second succeeds.
	srv, capPosted, dialed := jfFlakyProbeServer(t, 503, 503, 200)

	a := New(nil, t.TempDir(), "device-1", "", nil)
	a.cfg = Config{ServerURL: srv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", ServerURL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start with unreachable server returned %v, want nil (retry in background)", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	if got := a.Status().State; got != adapters.StateError {
		t.Errorf("state right after Start = %v, want StateError while unreachable", got)
	}

	waitUntil(t, "capabilities + websocket after server recovers", func() bool {
		return capPosted() >= 1 && dialed() >= 1
	})
	waitUntil(t, "StateRunning", func() bool { return a.Status().State == adapters.StateRunning })
	if tok, _ := LoadToken(a.tokenPath()); tok.AccessToken != "tok" {
		t.Errorf("token = %+v, want preserved", tok)
	}
}

// If the server comes back and rejects the token, the background re-probe
// treats it exactly like a 401 in Start: wipe the token, stop retrying.
func TestStart_ServerUnavailableThen401_WipesToken(t *testing.T) {
	srv, capPosted, _ := jfFlakyProbeServer(t, 503, 401)

	a := New(nil, t.TempDir(), "device-1", "", nil)
	a.cfg = Config{ServerURL: srv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "stale", ServerURL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })

	// Wait on status rather than polling the token file: on Windows a
	// concurrent read makes WipeToken's os.Remove fail with a sharing
	// violation. LastError is set after the wipe returns.
	waitUntil(t, "token-rejected status", func() bool {
		st := a.Status()
		return st.State == adapters.StateError && strings.Contains(st.LastError, "token rejected")
	})
	if tok, _ := LoadToken(a.tokenPath()); tok != (Token{}) {
		t.Errorf("token should have been wiped, got %+v", tok)
	}
	if n := capPosted(); n != 0 {
		t.Errorf("capabilities posted %d times with a rejected token, want 0", n)
	}
}
