package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestStart_NoToken_Errors(t *testing.T) {
	a := New(nil, t.TempDir(), "device-1")
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

	a := New(nil, t.TempDir(), "device-1")
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

	a := New(nil, t.TempDir(), "device-1")
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

	a := New(nil, t.TempDir(), "device-1")
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
