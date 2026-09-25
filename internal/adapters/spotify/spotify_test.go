package spotify

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/liveaudio"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// TestMain doubles as a fake librespot: launched with librespot's
// --backend flag, the test binary just idles until it is killed.
func TestMain(m *testing.M) {
	if slices.Contains(os.Args[1:], "--backend") {
		select {}
	}
	os.Exit(m.Run())
}

func decode(t *testing.T, doc string) (toml.Primitive, toml.MetaData) {
	t.Helper()
	var wrapper struct {
		Section toml.Primitive `toml:"spotify"`
	}
	meta, err := toml.Decode(doc, &wrapper)
	if err != nil {
		t.Fatal(err)
	}
	return wrapper.Section, meta
}

func TestConfigDefaultsAndDecode(t *testing.T) {
	cfg, err := decodeConfig(toml.Primitive{}, toml.MetaData{})
	if err != nil || cfg != DefaultConfig() {
		t.Fatalf("empty section = %+v, %v; want defaults", cfg, err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	prim, meta := decode(t, "[spotify]\nenabled = true\nname = \"Den CRT\"\nbitrate = 160\n")
	cfg, err = decodeConfig(prim, meta)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Name != "Den CRT" || cfg.Bitrate != 160 || cfg.PauseGraceSeconds != 30 {
		t.Fatalf("decoded = %+v", cfg)
	}
}

func TestConfigValidateReportsEachField(t *testing.T) {
	cfg := Config{
		Name:              strings.Repeat("x", 64),
		BinaryPath:        "a\nb",
		AudioOutput:       "loud",
		PauseGraceSeconds: 601,
		Bitrate:           128,
		ZeroconfPort:      70000,
	}
	err := cfg.Validate()
	var fe adapters.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("Validate = %v, want FieldErrors", err)
	}
	got := map[string]bool{}
	for _, e := range fe {
		got[e.Key] = true
	}
	for _, key := range []string{"name", "binary_path", "audio_output", "pause_grace_seconds", "bitrate", "zeroconf_port"} {
		if !got[key] {
			t.Errorf("no error for %s (got %v)", key, fe)
		}
	}
}

func TestEventFromForm(t *testing.T) {
	cases := []struct {
		event string
		kind  liveaudio.EventKind
		ok    bool
	}{
		{"playing", liveaudio.EventPlay, true},
		{"paused", liveaudio.EventPause, true},
		{"stopped", liveaudio.EventStop, true},
		{"session_disconnected", liveaudio.EventStop, true},
		{"track_changed", liveaudio.EventTrack, true},
		{"volume_changed", 0, false},
		{"loading", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		ev, ok := eventFromForm(url.Values{"PLAYER_EVENT": {tc.event}})
		if ok != tc.ok || ev.Kind != tc.kind {
			t.Errorf("%q → %v %v, want %v %v", tc.event, ev.Kind, ok, tc.kind, tc.ok)
		}
	}
}

func TestTrackFromForm(t *testing.T) {
	track := trackFromForm(url.Values{
		"NAME":        {"Age of Consent"},
		"ARTISTS":     {"New Order\nArthur Baker\n"},
		"ALBUM":       {"Power, Corruption & Lies"},
		"DURATION_MS": {"315000"},
		"COVERS":      {"\nhttps://i.scdn.co/image/large\nhttps://i.scdn.co/image/small"},
	})
	want := liveaudio.TrackMeta{
		Title: "Age of Consent", Artist: "New Order, Arthur Baker", Album: "Power, Corruption & Lies",
		Duration: 315 * time.Second, ArtworkURL: "https://i.scdn.co/image/large",
	}
	if track.Title != want.Title || track.Artist != want.Artist || track.Album != want.Album ||
		track.Duration != want.Duration || track.ArtworkURL != want.ArtworkURL {
		t.Fatalf("track = %+v, want %+v", track, want)
	}
	episode := trackFromForm(url.Values{"ITEM_TYPE": {"Episode"}, "NAME": {"Ep 1"}, "SHOW_NAME": {"The Show"}})
	if episode.Artist != "The Show" {
		t.Fatalf("episode artist = %q, want the show name", episode.Artist)
	}
}

func TestRunEventHookForwardsEvent(t *testing.T) {
	got := make(chan url.Values, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got <- r.PostForm
	}))
	defer srv.Close()
	env := map[string]string{
		eventURLEnv:    srv.URL + "/events/tok",
		"PLAYER_EVENT": "track_changed",
		"NAME":         "Blue Monday",
		"USER_NAME":    "not-forwarded",
	}
	if code := RunEventHook(func(k string) string { return env[k] }, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	form := <-got
	if form.Get("PLAYER_EVENT") != "track_changed" || form.Get("NAME") != "Blue Monday" {
		t.Fatalf("forwarded = %v", form)
	}
	if form.Has("USER_NAME") {
		t.Fatal("hook forwarded a variable outside its allowlist")
	}
}

func TestRunEventHookWithoutBridgeExitsZero(t *testing.T) {
	var stderr bytes.Buffer
	if code := RunEventHook(func(string) string { return "" }, &stderr); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), eventURLEnv) {
		t.Fatalf("stderr = %q, want a hint naming %s", stderr.String(), eventURLEnv)
	}
}

// fakeCore is a minimal liveaudio.Core.
type fakeCore struct {
	mu     sync.Mutex
	active *core.SessionRequest
	gen    uint64
	starts int
}

func (f *fakeCore) StartSession(req core.SessionRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active = &req
	f.gen++
	f.starts++
	return nil
}

func (f *fakeCore) StartSessionIfSession(req core.SessionRequest, ref string, gen uint64) (bool, error) {
	return false, nil
}

func (f *fakeCore) StopIfSession(ref string, gen uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active == nil || f.active.AdapterRef != ref {
		return false, nil
	}
	f.active = nil
	return true, nil
}

func (f *fakeCore) Status() core.SessionStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active == nil {
		return core.SessionStatus{}
	}
	return core.SessionStatus{AdapterRef: f.active.AdapterRef, Generation: f.gen}
}

func (f *fakeCore) UpdateNowPlayingIfSession(string, uint64, string, core.DisplayMetadata) bool {
	return true
}
func (f *fakeCore) VisualizerMode() string { return "retro_analyzer" }

func (f *fakeCore) activeRef() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active == nil {
		return ""
	}
	return f.active.AdapterRef
}

func testExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func newTestAdapter(t *testing.T, fc *fakeCore, exe string) *Adapter {
	t.Helper()
	a, err := New(AdapterConfig{
		Core: fc, HTTPPort: 32500, DataDir: t.TempDir(),
		Executable: func() (string, error) { return exe, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	return a
}

func TestHelperSpecCommandLine(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "relay")
	a := newTestAdapter(t, &fakeCore{}, exe)
	cfg := DefaultConfig()
	cfg.Name = " Den CRT "
	cfg.ZeroconfPort = 5354
	spec, err := a.helperSpec(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Path != "librespot" {
		t.Fatalf("path = %q, want a PATH lookup of librespot", spec.Path)
	}
	args := strings.Join(spec.Args, " ")
	for _, want := range []string{
		"--name Den CRT", "--backend pipe", "--format S16", "--bitrate 320",
		"--disable-audio-cache", "--zeroconf-port 5354",
		"--cache " + filepath.Join(a.dataDir, "librespot"),
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q:\n%s", want, args)
		}
	}
	// One argv element: librespot splits it on whitespace itself.
	if i := slices.Index(spec.Args, "--onevent"); i < 0 || spec.Args[i+1] != exe+" "+HookFlag {
		t.Errorf("--onevent value wrong: %q", spec.Args)
	}
	if slices.Contains(spec.Args, "--device") {
		t.Error("--device set: PCM must go to stdout")
	}
	wantEnv := eventURLEnv + "=http://127.0.0.1:32500" + eventRoute + a.eventToken
	if len(spec.Env) != 1 || spec.Env[0] != wantEnv {
		t.Fatalf("env = %v, want [%s]", spec.Env, wantEnv)
	}
}

func TestStartMissingLibrespotReportsError(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{}, testExe(t))
	a.cfg.Enabled = true
	a.cfg.BinaryPath = filepath.Join(t.TempDir(), "no-librespot-here")
	if err := a.Start(t.Context()); err == nil {
		t.Fatal("Start succeeded without librespot")
	}
	st := a.Status()
	if st.State != adapters.StateError || !strings.Contains(st.LastError, "librespot not found") {
		t.Fatalf("status = %+v", st)
	}
	_ = a.Stop()
	if st := a.Status(); st.State != adapters.StateStopped {
		t.Fatalf("status after Stop = %+v, want stopped", st)
	}
}

func postEvent(t *testing.T, mux *http.ServeMux, remote, token string, form url.Values) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, eventRoute+token, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code
}

func TestEventsDriveSessionThroughRunningAdapter(t *testing.T) {
	fc := &fakeCore{}
	exe := testExe(t)
	a := newTestAdapter(t, fc, exe)
	a.cfg.Enabled = true
	a.cfg.BinaryPath = exe // fake librespot
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if st := a.Status(); st.State != adapters.StateRunning {
		t.Fatalf("status = %+v", st)
	}
	mux := http.NewServeMux()
	a.MountPublicRoutes(mux)

	if code := postEvent(t, mux, "192.168.1.9:4000", a.eventToken, url.Values{"PLAYER_EVENT": {"playing"}}); code != http.StatusForbidden {
		t.Fatalf("LAN caller = %d, want 403", code)
	}
	if code := postEvent(t, mux, "127.0.0.1:4000", "wrong", url.Values{"PLAYER_EVENT": {"playing"}}); code != http.StatusNotFound {
		t.Fatalf("bad token = %d, want 404", code)
	}
	if code := postEvent(t, mux, "127.0.0.1:4000", a.eventToken, url.Values{"PLAYER_EVENT": {"playing"}}); code != http.StatusNoContent {
		t.Fatalf("event = %d, want 204", code)
	}
	waitUntil(t, "spotify session", func() bool { return fc.activeRef() == "spotify:1" })

	postEvent(t, mux, "127.0.0.1:4000", a.eventToken, url.Values{"PLAYER_EVENT": {"stopped"}})
	waitUntil(t, "session end", func() bool { return fc.activeRef() == "" })
}

func TestApplyConfigRestartsHelperOnlyForCommandLineChanges(t *testing.T) {
	exe := testExe(t)
	a := newTestAdapter(t, &fakeCore{}, exe)
	a.cfg.Enabled = true
	a.cfg.BinaryPath = exe
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	firstSup := a.sup

	prim, meta := decode(t, "[spotify]\nenabled = true\nbinary_path = '"+exe+"'\npause_grace_seconds = 5\n")
	scope, err := a.ApplyConfig(prim, meta)
	if err != nil || scope != adapters.ScopeHotSwap {
		t.Fatalf("grace change: scope %v err %v, want hot-swap", scope, err)
	}
	if a.sup != firstSup {
		t.Fatal("hot-swap change restarted the helper")
	}

	prim, meta = decode(t, "[spotify]\nenabled = true\nbinary_path = '"+exe+"'\nname = \"Renamed\"\n")
	scope, err = a.ApplyConfig(prim, meta)
	if err != nil || scope != adapters.ScopeRestartCast {
		t.Fatalf("rename: scope %v err %v, want restart-cast", scope, err)
	}
	if a.sup == firstSup || a.sup == nil {
		t.Fatal("rename did not restart the helper")
	}
	if st := a.Status(); st.State != adapters.StateRunning {
		t.Fatalf("status after helper restart = %+v", st)
	}
}

func TestScopeForChange(t *testing.T) {
	base := DefaultConfig()
	cases := []struct {
		name string
		mut  func(*Config)
		want adapters.ApplyScope
	}{
		{"no change", func(*Config) {}, adapters.ScopeHotSwap},
		{"grace", func(c *Config) { c.PauseGraceSeconds = 1 }, adapters.ScopeHotSwap},
		{"audio output", func(c *Config) { c.AudioOutput = AudioOutputVisualOnly }, adapters.ScopeNextCast},
		{"bitrate", func(c *Config) { c.Bitrate = 96 }, adapters.ScopeRestartCast},
		{"enable", func(c *Config) { c.Enabled = true }, adapters.ScopeRestartCast},
	}
	for _, tc := range cases {
		next := base
		tc.mut(&next)
		if got := scopeForChange(base, next); got != tc.want {
			t.Errorf("%s: scope %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHookProgramHandlesSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "relay.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := hookProgram(exe)
	if runtime.GOOS != "windows" {
		if err == nil {
			t.Fatalf("hookProgram(%q) = %q, want an error", exe, got)
		}
		return
	}
	// Windows: an 8.3 short path when the volume has them, else an error.
	if err == nil && strings.ContainsAny(got, " \t") {
		t.Fatalf("hookProgram returned a path with spaces: %q", got)
	}
	if plain, err := hookProgram(`C:\relay\relay.exe`); err != nil || plain != `C:\relay\relay.exe` {
		t.Fatalf("space-free path changed: %q %v", plain, err)
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
