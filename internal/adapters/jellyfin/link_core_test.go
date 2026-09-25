package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// newSnapshotTestAdapter constructs a minimal Adapter for linkSnapshot tests.
// tokenPath() derives from a.dataDir via filepath.Join(a.dataDir, "jellyfin", "token.json"),
// so we pass t.TempDir() to New() for per-test isolation.
func newSnapshotTestAdapter(t *testing.T, serverURL string) *Adapter {
	t.Helper()
	a := New(nil, t.TempDir(), "dev-1", "", nil)
	a.cfg.ServerURL = serverURL
	return a
}

// writeRawToken writes raw bytes directly to the token file path,
// creating the parent directory as needed. Used to inject corrupt JSON
// that SaveToken's atomicity would prevent.
func writeRawToken(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o600)
}

func TestJFSnapshot_NoToken(t *testing.T) {
	a := newSnapshotTestAdapter(t, "")
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseUnlinked {
		t.Errorf("Phase = %q, want unlinked", got.Phase)
	}
	if !got.NeedsServerURL {
		t.Errorf("NeedsServerURL = false, want true (blank server_url)")
	}
}

func TestJFSnapshot_NoTokenWithURL(t *testing.T) {
	a := newSnapshotTestAdapter(t, "http://jf.local:8096")
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseUnlinked || got.NeedsServerURL {
		t.Errorf("got %+v, want unlinked + NeedsServerURL=false", got)
	}
}

func TestJFSnapshot_Linked(t *testing.T) {
	a := newSnapshotTestAdapter(t, "http://jf.local:8096")
	if err := SaveToken(a.tokenPath(), Token{
		AccessToken: "tok", UserName: "jake", ServerID: "srv-9", ServerURL: "http://jf.local:8096",
	}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseLinked {
		t.Fatalf("Phase = %q, want linked", got.Phase)
	}
	if got.LinkedAs != "jake on srv-9" {
		t.Errorf("LinkedAs = %q, want 'jake on srv-9'", got.LinkedAs)
	}
}

func TestJFSnapshot_ParseError(t *testing.T) {
	a := newSnapshotTestAdapter(t, "http://jf.local:8096")
	// linkSnapshot reads the token file directly (not via LoadToken) so it can
	// distinguish a JSON parse failure from a missing file. LoadToken silently
	// swallows corrupt JSON; linkSnapshot must not.
	if err := writeRawToken(a.tokenPath(), "{not json"); err != nil {
		t.Fatalf("write corrupt token: %v", err)
	}
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseError {
		t.Errorf("Phase = %q, want error", got.Phase)
	}
}

func TestJFController_StartMissingURL(t *testing.T) {
	a := newSnapshotTestAdapter(t, "")
	got, _ := a.StartLink(contextTODO(), map[string]string{"username": "x", "password": "y"})
	if got.Phase != adapters.LinkPhaseError {
		t.Errorf("Phase = %q, want error (no server_url)", got.Phase)
	}
}

func TestJFController_StartBlankCreds(t *testing.T) {
	a := newSnapshotTestAdapter(t, "http://jf.local:8096")
	got, _ := a.StartLink(contextTODO(), map[string]string{"username": "", "password": ""})
	if got.Phase != adapters.LinkPhaseError {
		t.Errorf("Phase = %q, want error (blank creds)", got.Phase)
	}
}

func TestJFController_Conformance(t *testing.T) {
	var _ adapters.LinkController = (*Adapter)(nil)
}

func contextTODO() context.Context { return context.TODO() }

// TestJFController_StartSuccess_PersistsToken exercises StartLink's
// happy path directly (formerly covered via the HTTP handler that
// backed the removed htmx link form).
func TestJFController_StartSuccess_PersistsToken(t *testing.T) {
	jfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"AccessToken":"tok-1","User":{"Id":"uid-1","Name":"alice"},"ServerId":"sid-1"}`))
	}))
	defer jfSrv.Close()

	a := newSnapshotTestAdapter(t, jfSrv.URL)

	snap, err := a.StartLink(contextTODO(), map[string]string{"username": "alice", "password": "s3cret"})
	if err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	if snap.Phase != adapters.LinkPhaseLinked {
		t.Fatalf("Phase = %q, want linked: %+v", snap.Phase, snap)
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

// TestJFController_StartBadCredentials_NoDiskWrite ensures a failed
// auth attempt never leaves a token on disk.
func TestJFController_StartBadCredentials_NoDiskWrite(t *testing.T) {
	jfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer jfSrv.Close()

	a := newSnapshotTestAdapter(t, jfSrv.URL)

	_, err := a.StartLink(contextTODO(), map[string]string{"username": "alice", "password": "wrong"})
	if err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	if a.link.State() != LinkError {
		t.Errorf("link state = %v, want LinkError", a.link.State())
	}
	tok, _ := LoadToken(a.tokenPath())
	if tok != (Token{}) {
		t.Errorf("token persisted on auth failure: %+v", tok)
	}
}

// TestJFController_Unlink_DeletesToken covers the basic unlink path:
// token wiped, state back to idle.
func TestJFController_Unlink_DeletesToken(t *testing.T) {
	a := newSnapshotTestAdapter(t, "")
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	a.link.SetLinked("alice", "sid-1")

	if _, err := a.Unlink(contextTODO()); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if a.link.State() != LinkIdle {
		t.Errorf("link state after unlink = %v, want LinkIdle", a.link.State())
	}
	tok, _ := LoadToken(a.tokenPath())
	if tok != (Token{}) {
		t.Errorf("token still present after unlink: %+v", tok)
	}
}

// TestJFController_Unlink_CallsServerLogout asserts the bridge tells
// JF to invalidate the access token and remove the device row, so the
// JF admin's Devices list doesn't accumulate stale entries on every
// re-link cycle. Verifies the right endpoint, the right Authorization
// token, and that the call happens before WipeToken (otherwise the
// token would already be gone).
func TestJFController_Unlink_CallsServerLogout(t *testing.T) {
	logoutCh := make(chan string, 1) // captures the Authorization header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/Sessions/Logout" {
			http.NotFound(w, r)
			return
		}
		select {
		case logoutCh <- r.Header.Get("Authorization"):
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	a := newSnapshotTestAdapter(t, srv.URL)
	if err := SaveToken(a.tokenPath(), Token{
		AccessToken: "tok-to-revoke", UserID: "uid", ServerURL: srv.URL,
	}); err != nil {
		t.Fatal(err)
	}
	a.link.SetLinked("alice", "sid-1")

	if _, err := a.Unlink(contextTODO()); err != nil {
		t.Fatalf("Unlink: %v", err)
	}

	select {
	case auth := <-logoutCh:
		if !strings.Contains(auth, `Token="tok-to-revoke"`) {
			t.Errorf("logout auth header missing token: %q", auth)
		}
		if !strings.Contains(auth, `DeviceId="dev-1"`) {
			t.Errorf("logout auth header missing DeviceId: %q", auth)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("/Sessions/Logout never called")
	}

	// Local cleanup must still complete regardless.
	tok, _ := LoadToken(a.tokenPath())
	if tok != (Token{}) {
		t.Errorf("token not wiped after unlink: %+v", tok)
	}
}

// TestJFController_Unlink_LocalCleanupSurvivesServerError confirms the
// best-effort contract: even when the JF server is unreachable or
// returns 500, the local token gets wiped and link state goes Idle.
// Operators clicking Unlink expect the bridge to converge to
// "unlinked" no matter what JF says.
func TestJFController_Unlink_LocalCleanupSurvivesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	a := newSnapshotTestAdapter(t, srv.URL)
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", ServerURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	a.link.SetLinked("alice", "sid-1")

	if _, err := a.Unlink(contextTODO()); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if a.link.State() != LinkIdle {
		t.Errorf("link state = %v, want LinkIdle even after server 500", a.link.State())
	}
	tok, _ := LoadToken(a.tokenPath())
	if tok != (Token{}) {
		t.Errorf("token survived after server-side logout failed: %+v", tok)
	}
}

// TestJFController_Unlink_StopsAdapter exercises the lifecycle fix: an
// adapter sitting in StateRunning (because a prior Start succeeded)
// must transition to StateStopped after unlink. Without this, a
// runSession goroutine holding the now-wiped token in its closure
// keeps pounding JF with 401s — visible in the JF server logs as
// "Invalid token" challenges every reconnect tick.
func TestJFController_Unlink_StopsAdapter(t *testing.T) {
	a := newSnapshotTestAdapter(t, "")
	a.setState(adapters.StateRunning, "")

	if _, err := a.Unlink(contextTODO()); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
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
		mu         sync.Mutex
		probeCount int
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

// TestJFController_StartSuccess_Enabled_StartsAdapter is the relink-side
// half of the lifecycle fix: a successful link with cfg.Enabled=true
// must transition the adapter to StateRunning so the new token
// actually drives the runSession goroutine. Verifies via the System/
// Info probe-count: Start runs the probe with the freshly-minted
// token before spawning the WS goroutine.
func TestJFController_StartSuccess_Enabled_StartsAdapter(t *testing.T) {
	srv, probeCount, mu := jfMockServer(t)

	a := newSnapshotTestAdapter(t, srv.URL)
	a.cfg.Enabled = true
	a.cfg.MaxVideoBitrateKbps = 4000
	t.Cleanup(func() { _ = a.Stop() })

	snap, err := a.StartLink(contextTODO(), map[string]string{"username": "alice", "password": "s3cret"})
	if err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	if snap.Phase != adapters.LinkPhaseLinked {
		t.Fatalf("Phase = %q, want linked", snap.Phase)
	}
	if a.link.State() != LinkLinked {
		t.Fatalf("link state = %v, want LinkLinked", a.link.State())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && a.Status().State != adapters.StateRunning {
		time.Sleep(20 * time.Millisecond)
	}
	if got := a.Status().State; got != adapters.StateRunning {
		t.Fatalf("adapter state = %v, want StateRunning", got)
	}
	// Adapter.Start runs the probe synchronously before returning, so
	// by the time StartLink returns we should already have one probe
	// hit. A second probe never fires unless something restarts the
	// adapter — exactly what we want to assert.
	mu.Lock()
	defer mu.Unlock()
	if *probeCount < 1 {
		t.Errorf("probeCount = %d, want >= 1 (Start should have probed with fresh token)", *probeCount)
	}
}

// TestJFController_StartSuccess_Disabled_DoesNotAutoStart guards against
// auto-starting an adapter the operator hasn't enabled. Without this
// gate, every link would silently bring the adapter online — making
// the Enabled toggle meaningless on first link.
func TestJFController_StartSuccess_Disabled_DoesNotAutoStart(t *testing.T) {
	srv, probeCount, mu := jfMockServer(t)

	a := newSnapshotTestAdapter(t, srv.URL)
	a.cfg.Enabled = false
	a.cfg.MaxVideoBitrateKbps = 4000

	snap, err := a.StartLink(contextTODO(), map[string]string{"username": "alice", "password": "s3cret"})
	if err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	if snap.Phase != adapters.LinkPhaseLinked {
		t.Fatalf("Phase = %q, want linked", snap.Phase)
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
