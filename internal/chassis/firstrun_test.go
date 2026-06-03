package chassis

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// fakeFirstRun embeds the existing BridgeSettingsSaver conformance fixture
// and adds the FirstRunController sentinel methods. *fakeFirstRun therefore
// satisfies BOTH BridgeSettingsSaver (promoted value methods) and
// FirstRunController (pointer methods).
type fakeFirstRun struct {
	fakeBridgeSettingsSaver
	firstRun   bool
	dismissErr error
	dismissed  int
}

func (f *fakeFirstRun) IsFirstRun() bool       { return f.firstRun }
func (f *fakeFirstRun) DismissFirstRun() error { f.dismissed++; return f.dismissErr }

// hostSaver builds a *fakeFirstRun with the given MiSTer host and first-run flag.
func hostSaver(host string, firstRun bool) *fakeFirstRun {
	return &fakeFirstRun{
		fakeBridgeSettingsSaver: fakeBridgeSettingsSaver{
			cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: host}},
		},
		firstRun: firstRun,
	}
}

// enabledRegistry returns a registry containing one enabled source adapter,
// so setupStatus reports SourceEnabled=true.
func enabledRegistry() *adapters.Registry {
	return adapters.NewRegistryWith(fakeNamedAdapter{name: "src"})
}

// newSetupServer builds a *Server via New (so firstRun resolves, templates
// parse, and Mount is safe) with the given saver and registry.
func newSetupServer(t *testing.T, saver BridgeSettingsSaver, reg *adapters.Registry) *Server {
	t.Helper()
	if reg == nil {
		reg = adapters.NewRegistry()
	}
	s, err := New(Config{Version: "test", StartedAt: time.Now(), BridgeSaver: saver, Registry: reg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestFirstRunActive(t *testing.T) {
	cases := []struct {
		name  string
		saver BridgeSettingsSaver
		want  bool
	}{
		{"nil controller", nil, false},
		{"wired, first-run", hostSaver("", true), true},
		{"wired, dismissed", hostSaver("", false), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{cfg: Config{BridgeSaver: tc.saver}}
			s.firstRun = resolveFirstRun(tc.saver)
			if got := s.firstRunActive(); got != tc.want {
				t.Fatalf("firstRunActive()=%v want %v", got, tc.want)
			}
		})
	}
}

func TestSetupStatus_NilSafe(t *testing.T) {
	s := &Server{cfg: Config{}} // nil BridgeSaver, nil Registry
	st := s.setupStatus()
	if st.HostSet || st.SourceEnabled {
		t.Fatalf("nil deps must yield false/false, got %+v", st)
	}
}

func TestRequireSetupComplete_Gate(t *testing.T) {
	saver := hostSaver("", true)
	s := &Server{cfg: Config{BridgeSaver: saver}}
	s.firstRun = resolveFirstRun(saver)

	reached := false
	h := s.requireSetupComplete(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ui/cast", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("gated POST: got %d want 409", rec.Code)
	}
	if reached {
		t.Fatal("inner handler must not run while setup active")
	}

	// Dismiss → passes through.
	saver.firstRun = false
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ui/cast", nil))
	if rec.Code != http.StatusOK || !reached {
		t.Fatalf("after dismiss: got %d reached=%v", rec.Code, reached)
	}
}

func TestMount_GatesCastRoutes(t *testing.T) {
	gated := []struct {
		method, path string
	}{
		{"POST", "/ui/cast"},
		{"POST", "/ui/preset/1/cast"},
		{"POST", "/ui/streams/cast"},
		{"POST", "/ui/localfiles/cast"},
		{"POST", "/ui/settings/adapter/localfiles/cast"},
		{"POST", "/ui/history/play"},
		{"POST", "/ui/aux/start"},
	}
	saver := hostSaver("", true)
	s := newSetupServer(t, saver, nil) // built via New → firstRun resolves, Mount safe

	mux := http.NewServeMux()
	s.Mount(mux)

	for _, g := range gated {
		req := httptest.NewRequest(g.method, g.path, nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin") // pass requireSameOrigin
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s %s: got %d want 409 (gated)", g.method, g.path, rec.Code)
		}
	}
}
