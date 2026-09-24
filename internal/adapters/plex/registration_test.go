package plex

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// registerCall is one PUT /devices/{uuid} observed by fakePlexTV.
type registerCall struct {
	token string
	uri   string
}

// fakePlexTV serves the two plex.tv endpoints the link + registration
// paths hit: the PIN poll (returns pinToken) and the device PUT (recorded
// on the returned channel). Points PlexAPIBase at itself for the test.
func fakePlexTV(t *testing.T, pinToken string) <-chan registerCall {
	t.Helper()
	calls := make(chan registerCall, 16)
	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v2/pins/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":7,"code":"ABCD","authToken":"` + pinToken + `"}`))
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/devices/"):
			_ = r.ParseForm()
			select {
			case calls <- registerCall{token: r.URL.Query().Get("X-Plex-Token"), uri: r.PostForm.Get("Connection[][uri]")}:
			default:
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	restore := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = restore })
	return calls
}

// newRegistrationAdapter builds an adapter with GDM stubbed out so Start
// binds no multicast sockets.
func newRegistrationAdapter(t *testing.T, cfg AdapterConfig) *Adapter {
	t.Helper()
	prev := newDiscovery
	newDiscovery = func(DiscoveryConfig) (*Discovery, error) {
		return nil, errors.New("test fake: discovery disabled")
	}
	t.Cleanup(func() { newDiscovery = prev })

	cfg.Bridge = config.BridgeConfig{
		DataDir: t.TempDir(),
		UI:      config.UIConfig{HTTPPort: 32500},
	}
	cfg.Core = &fakeCore{}
	cfg.Version = "test"
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a.plexCfg = DefaultConfig()
	a.plexCfg.Enabled = true
	return a
}

func waitRegister(t *testing.T, calls <-chan registerCall) registerCall {
	t.Helper()
	select {
	case c := <-calls:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("no plex.tv device registration observed")
		return registerCall{}
	}
}

// A link completed through the UI while the adapter is running must
// start plex.tv registration immediately — previously the loop was only
// launched by Start, so a UI link stayed invisible to plex.tv until the
// container restarted.
func TestAdapter_UILinkStartsRegistration(t *testing.T) {
	calls := fakePlexTV(t, "tok-new")
	a := newRegistrationAdapter(t, AdapterConfig{
		TokenStore: &StoredData{DeviceUUID: "uuid-link"},
		HostIP:     "10.0.0.5",
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })

	pl := newPendingLink("ABCD", 7, time.Now().Add(time.Minute))
	a.mu.Lock()
	a.pending = pl
	a.mu.Unlock()
	a.pollPendingLink(pl, 7, "uuid-link")

	got := waitRegister(t, calls)
	if got.token != "tok-new" {
		t.Errorf("registered with token %q; want tok-new", got.token)
	}
	if got.uri != "http://10.0.0.5:32500" {
		t.Errorf("registered uri %q; want http://10.0.0.5:32500", got.uri)
	}
}

// Re-linking while a loop is already running replaces it so plex.tv sees
// the new token rather than the superseded one.
func TestAdapter_UIRelinkReplacesRegistrationToken(t *testing.T) {
	calls := fakePlexTV(t, "tok-new")
	a := newRegistrationAdapter(t, AdapterConfig{
		TokenStore: &StoredData{DeviceUUID: "uuid-relink", AuthToken: "tok-old"},
		HostIP:     "10.0.0.5",
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	if got := waitRegister(t, calls); got.token != "tok-old" {
		t.Fatalf("boot registration token %q; want tok-old", got.token)
	}

	pl := newPendingLink("ABCD", 7, time.Now().Add(time.Minute))
	a.mu.Lock()
	a.pending = pl
	a.mu.Unlock()
	a.pollPendingLink(pl, 7, "uuid-relink")

	if got := waitRegister(t, calls); got.token != "tok-new" {
		t.Errorf("post-relink registration token %q; want tok-new", got.token)
	}
}

// A link that lands after Stop must not resurrect the registration loop.
func TestAdapter_UILinkAfterStopDoesNotRegister(t *testing.T) {
	calls := fakePlexTV(t, "tok-late")
	a := newRegistrationAdapter(t, AdapterConfig{
		TokenStore: &StoredData{DeviceUUID: "uuid-stopped"},
		HostIP:     "10.0.0.5",
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	pl := newPendingLink("ABCD", 7, time.Now().Add(time.Minute))
	a.mu.Lock()
	a.pending = pl
	a.mu.Unlock()
	a.pollPendingLink(pl, 7, "uuid-stopped")

	select {
	case c := <-calls:
		t.Fatalf("unexpected registration after Stop: %+v", c)
	case <-time.After(200 * time.Millisecond):
	}
}

// When boot-time host IP detection failed, the adapter keeps polling the
// resolver and registers once an address appears instead of skipping
// registration for the life of the process.
func TestAdapter_RegistrationRetriesHostIPResolver(t *testing.T) {
	restore := hostIPRetryInterval
	hostIPRetryInterval = 20 * time.Millisecond
	t.Cleanup(func() { hostIPRetryInterval = restore })

	calls := fakePlexTV(t, "")
	var resolves int32
	a := newRegistrationAdapter(t, AdapterConfig{
		TokenStore: &StoredData{DeviceUUID: "uuid-late-net", AuthToken: "tok"},
		ResolveHostIP: func() string {
			if atomic.AddInt32(&resolves, 1) < 3 {
				return ""
			}
			return "10.0.0.9"
		},
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })

	if got := waitRegister(t, calls); got.uri != "http://10.0.0.9:32500" {
		t.Errorf("registered uri %q; want http://10.0.0.9:32500", got.uri)
	}
}

func TestWaitForHostIP(t *testing.T) {
	restore := hostIPRetryInterval
	hostIPRetryInterval = 10 * time.Millisecond
	t.Cleanup(func() { hostIPRetryInterval = restore })

	t.Run("static wins without resolving", func(t *testing.T) {
		ip, ok := waitForHostIP(context.Background(), "10.1.1.1", func() string {
			t.Error("resolver called despite static address")
			return ""
		})
		if !ok || ip != "10.1.1.1" {
			t.Errorf("got (%q, %v); want (10.1.1.1, true)", ip, ok)
		}
	})
	t.Run("nil resolver gives up", func(t *testing.T) {
		if ip, ok := waitForHostIP(context.Background(), "", nil); ok || ip != "" {
			t.Errorf("got (%q, %v); want (\"\", false)", ip, ok)
		}
	})
	t.Run("ctx cancel stops polling", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if ip, ok := waitForHostIP(ctx, "", func() string { return "" }); ok || ip != "" {
			t.Errorf("got (%q, %v); want (\"\", false)", ip, ok)
		}
	})
}

// Stop→Start (the chassis enable toggle) must bring registration and the
// timeline loop back, not leave a disabled-then-enabled adapter dark.
func TestAdapter_RestartResumesRegistrationAndTimeline(t *testing.T) {
	calls := fakePlexTV(t, "")
	a := newRegistrationAdapter(t, AdapterConfig{
		TokenStore: &StoredData{DeviceUUID: "uuid-restart", AuthToken: "tok"},
		HostIP:     "10.0.0.5",
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitRegister(t, calls)
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	waitRegister(t, calls)

	a.timeline.mu.Lock()
	running := a.timeline.stop != nil
	a.timeline.mu.Unlock()
	if !running {
		t.Error("timeline broadcast loop not running after restart")
	}
}
