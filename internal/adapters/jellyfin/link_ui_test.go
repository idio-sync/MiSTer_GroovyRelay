package jellyfin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func newLinkTestAdapter(t *testing.T, version string) *Adapter {
	t.Helper()
	return New(nil, t.TempDir(), "device-uuid")
}

func TestLinkUI_StartSuccess_PersistsTokenAndReturnsLinkedFragment(t *testing.T) {
	jfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"AccessToken":"tok-1","User":{"Id":"uid-1","Name":"alice"},"ServerId":"sid-1"}`))
	}))
	defer jfSrv.Close()

	a := newLinkTestAdapter(t, "0.1.0")
	a.cfg.ServerURL = jfSrv.URL

	form := url.Values{}
	form.Set("username", "alice")
	form.Set("password", "s3cret")
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/link/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	a.handleLinkStart(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "alice") {
		t.Errorf("body missing 'alice': %s", rr.Body.String())
	}
	if a.link.State() != LinkLinked {
		t.Errorf("link state = %v, want LinkLinked", a.link.State())
	}
	tok, err := LoadToken(a.tokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "tok-1" {
		t.Errorf("persisted token = %+v", tok)
	}
}

func TestLinkUI_StartBadCredentials_NoDiskWrite(t *testing.T) {
	jfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer jfSrv.Close()

	a := newLinkTestAdapter(t, "0.1.0")
	a.cfg.ServerURL = jfSrv.URL

	form := url.Values{}
	form.Set("username", "alice")
	form.Set("password", "wrong")
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/link/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	a.handleLinkStart(rr, req)

	if a.link.State() != LinkError {
		t.Errorf("link state = %v, want LinkError", a.link.State())
	}
	tok, _ := LoadToken(a.tokenPath())
	if tok != (Token{}) {
		t.Errorf("token persisted on auth failure: %+v", tok)
	}
}

func TestLinkUI_StartRejectsMissingServerURL(t *testing.T) {
	a := newLinkTestAdapter(t, "0.1.0")
	// cfg.ServerURL intentionally left empty: link should refuse without
	// touching the network.
	form := url.Values{}
	form.Set("username", "alice")
	form.Set("password", "s3cret")
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/link/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	a.handleLinkStart(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (form errors render as fragment)", rr.Code)
	}
	if a.link.State() != LinkError {
		t.Errorf("link state = %v, want LinkError on empty server_url", a.link.State())
	}
}

func TestLinkUI_StartRejectsEmptyCredentials(t *testing.T) {
	a := newLinkTestAdapter(t, "0.1.0")
	a.cfg.ServerURL = "https://jf.example.com"
	form := url.Values{}
	form.Set("username", "")
	form.Set("password", "")
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/link/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	a.handleLinkStart(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (form errors render as fragment)", rr.Code)
	}
	if a.link.State() != LinkError {
		t.Errorf("link state = %v, want LinkError on empty creds", a.link.State())
	}
}

func TestLinkUI_Unlink_DeletesToken(t *testing.T) {
	a := newLinkTestAdapter(t, "0.1.0")
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	a.link.SetLinked("alice", "sid-1")

	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/unlink", nil)
	rr := httptest.NewRecorder()
	a.handleUnlink(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if a.link.State() != LinkIdle {
		t.Errorf("link state after unlink = %v, want LinkIdle", a.link.State())
	}
	tok, _ := LoadToken(a.tokenPath())
	if tok != (Token{}) {
		t.Errorf("token still present after unlink: %+v", tok)
	}
}

// TestLinkUI_Unlink_StopsAdapter exercises the lifecycle fix: an
// adapter sitting in StateRunning (because a prior Start succeeded)
// must transition to StateStopped after unlink. Without this, a
// runSession goroutine holding the now-wiped token in its closure
// keeps pounding JF with 401s — visible in the JF server logs as
// "Invalid token" challenges every reconnect tick.
func TestLinkUI_Unlink_StopsAdapter(t *testing.T) {
	a := newLinkTestAdapter(t, "0.1.0")
	a.setState(adapters.StateRunning, "")

	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/unlink", nil)
	rr := httptest.NewRecorder()
	a.handleUnlink(rr, req)

	if got := a.Status().State; got != adapters.StateStopped {
		t.Errorf("adapter state after unlink = %v, want StateStopped", got)
	}
}

// jfMockServer is the minimum JF surface a relink test exercises:
// AuthenticateByName + System/Info + the bookkeeping endpoints
// runSession touches. /socket accepts the upgrade and parks until
// ctx.Done() so the bridge sits in steady-state Running rather than
// thrashing through the reconnect backoff.
func jfMockServer(t *testing.T) (*httptest.Server, *int, *sync.Mutex) {
	t.Helper()
	var (
		mu          sync.Mutex
		probeCount  int
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"AccessToken":"fresh-tok","User":{"Id":"uid","Name":"alice"},"ServerId":"sid"}`))
	})
	mux.HandleFunc("/System/Info", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		probeCount++
		mu.Unlock()
		w.WriteHeader(200)
	})
	mux.HandleFunc("/Sessions/Capabilities/Full", func(w http.ResponseWriter, r *http.Request) {
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
		<-r.Context().Done()
		_ = conn.Close(websocket.StatusNormalClosure, "")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &probeCount, &mu
}

// TestLinkUI_StartSuccess_Enabled_StartsAdapter is the relink-side
// half of the lifecycle fix: a successful link with cfg.Enabled=true
// must transition the adapter to StateRunning so the new token
// actually drives the runSession goroutine. Verifies via the System/
// Info probe-count: Start runs the probe with the freshly-minted
// token before spawning the WS goroutine.
func TestLinkUI_StartSuccess_Enabled_StartsAdapter(t *testing.T) {
	srv, probeCount, mu := jfMockServer(t)

	a := newLinkTestAdapter(t, "0.1.0")
	a.cfg.ServerURL = srv.URL
	a.cfg.Enabled = true
	a.cfg.MaxVideoBitrateKbps = 4000
	t.Cleanup(func() { _ = a.Stop() })

	form := url.Values{}
	form.Set("username", "alice")
	form.Set("password", "s3cret")
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/link/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	a.handleLinkStart(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if a.link.State() != LinkLinked {
		t.Fatalf("link state = %v, want LinkLinked", a.link.State())
	}
	if got := a.Status().State; got != adapters.StateRunning {
		t.Fatalf("adapter state = %v, want StateRunning", got)
	}
	// Adapter.Start runs the probe synchronously before returning, so
	// by the time handleLinkStart returns we should already have one
	// probe hit. A second probe never fires unless something restarts
	// the adapter — exactly what we want to assert.
	mu.Lock()
	defer mu.Unlock()
	if *probeCount < 1 {
		t.Errorf("probeCount = %d, want >= 1 (Start should have probed with fresh token)", *probeCount)
	}
}

// TestLinkUI_StartSuccess_Disabled_DoesNotAutoStart guards against
// auto-starting an adapter the operator hasn't enabled. Without this
// gate, every link would silently bring the adapter online — making
// the Enabled toggle meaningless on first link.
func TestLinkUI_StartSuccess_Disabled_DoesNotAutoStart(t *testing.T) {
	srv, probeCount, mu := jfMockServer(t)

	a := newLinkTestAdapter(t, "0.1.0")
	a.cfg.ServerURL = srv.URL
	a.cfg.Enabled = false
	a.cfg.MaxVideoBitrateKbps = 4000

	form := url.Values{}
	form.Set("username", "alice")
	form.Set("password", "s3cret")
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/link/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	a.handleLinkStart(rr, req)

	if a.link.State() != LinkLinked {
		t.Fatalf("link state = %v, want LinkLinked", a.link.State())
	}
	if got := a.Status().State; got != adapters.StateStopped {
		t.Errorf("adapter state = %v, want StateStopped (Enabled=false should not auto-start)", got)
	}
	// Brief settle in case Start was kicked off in a goroutine — it
	// shouldn't be, but assert the negative directly.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if *probeCount != 0 {
		t.Errorf("probeCount = %d, want 0 (no probe when Enabled=false)", *probeCount)
	}
}
