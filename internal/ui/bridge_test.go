package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// fakeBridgeSaver implements BridgeSaver for tests.
type fakeBridgeSaver struct {
	got     *config.BridgeConfig
	failErr error
}

func (f *fakeBridgeSaver) Current() config.BridgeConfig {
	return config.BridgeConfig{
		DataDir: "/config",
		Video: config.VideoConfig{
			Modeline:            "NTSC_480i",
			InterlaceFieldOrder: "tff",
			AspectMode:          "auto",
			RGBMode:             "rgb888",
			LZ4Enabled:          true,
		},
		Audio: config.AudioConfig{SampleRate: 48000, Channels: 2},
		MiSTer: config.MisterConfig{
			Host: "192.168.1.42", Port: 32100, SourcePort: 32101,
			SSHUser: "alice", SSHPassword: "hunter2",
		},
		UI: config.UIConfig{HTTPPort: 32500},
	}
}

func (f *fakeBridgeSaver) Save(newCfg config.BridgeConfig) (adapters.ApplyScope, error) {
	if f.failErr != nil {
		return 0, f.failErr
	}
	f.got = &newCfg
	return adapters.ScopeHotSwap, nil
}

func newBridgeTestServer(t *testing.T, saver *fakeBridgeSaver) *http.ServeMux {
	t.Helper()
	reg := adapters.NewRegistry()
	s, err := New(Config{Registry: reg, BridgeSaver: saver})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	return mux
}

func TestHandleBridge_GET_RendersAllFields(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != 200 {
		t.Fatalf("status = %d", rw.Code)
	}
	body := rw.Body.String()
	wantSnippets := []string{
		`name="mister.host"`,
		`name="mister.port"`,
		`name="video.interlace_field_order"`,
		`name="audio.sample_rate"`,
		`name="ui.http_port"`,
		"Save Bridge",
	}
	for _, w := range wantSnippets {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in body", w)
		}
	}
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("direct /ui/bridge load should render the full shell document")
	}
	if !strings.Contains(body, "htmx.min.js") {
		t.Error("direct /ui/bridge load should include shell assets")
	}
}

func TestHandleBridge_GET_CurrentValuesPrefill(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()
	// MisterHost from Current() is 192.168.1.42 — must appear prefilled.
	if !strings.Contains(body, `value="192.168.1.42"`) {
		t.Error("mister.host value not prefilled")
	}
	// interlace_field_order "tff" must be the <option selected>.
	if !strings.Contains(body, `<option value="tff" selected`) {
		t.Error("interlace tff option not marked selected")
	}
}

func TestHandleBridge_GET_HTMXReturnsFragment(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	req.Header.Set("HX-Request", "true")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("htmx bridge request should return a panel fragment, not a full document")
	}
	if !strings.Contains(body, "<h1>Bridge</h1>") {
		t.Error("bridge fragment missing heading")
	}
}

func TestHandleBridge_POST_Success(t *testing.T) {
	saver := &fakeBridgeSaver{}
	mux := newBridgeTestServer(t, saver)

	body := strings.NewReader(
		"mister.host=192.168.1.99" +
			"&mister.port=32100" +
			"&mister.source_port=32101" +
			"&mister.ssh_user=root" +
			"&mister.ssh_password=" +
			"&host_ip=" +
			"&video.modeline=NTSC_480i" +
			"&video.interlace_field_order=bff" +
			"&video.aspect_mode=auto" +
			"&video.lz4_enabled=true" +
			"&audio.sample_rate=48000" +
			"&audio.channels=2" +
			"&ui.http_port=32500" +
			"&data_dir=/config")

	req := httptest.NewRequest("POST", "/ui/bridge/save", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
	}
	if saver.got == nil {
		t.Fatal("saver.Save not called")
	}
	if saver.got.MiSTer.Host != "192.168.1.99" {
		t.Errorf("saved host = %q", saver.got.MiSTer.Host)
	}
	if saver.got.Video.InterlaceFieldOrder != "bff" {
		t.Errorf("saved interlace = %q", saver.got.Video.InterlaceFieldOrder)
	}
	if !strings.Contains(rw.Body.String(), "applied live") {
		t.Error("expected hot-swap toast message")
	}
	if saver.got.MiSTer.SSHPassword != "hunter2" {
		t.Errorf("expected preserve-on-empty to retain prior password, got %q", saver.got.MiSTer.SSHPassword)
	}
}

func TestHandleBridge_POST_ValidationError(t *testing.T) {
	saver := &fakeBridgeSaver{}
	mux := newBridgeTestServer(t, saver)

	body := strings.NewReader(
		"mister.host=" + // empty → validation fails
			"&mister.port=32100" +
			"&mister.source_port=32101" +
			"&mister.ssh_user=root" +
			"&mister.ssh_password=" +
			"&host_ip=" +
			"&video.modeline=NTSC_480i" +
			"&video.interlace_field_order=tff" +
			"&video.aspect_mode=auto" +
			"&audio.sample_rate=48000" +
			"&audio.channels=2" +
			"&ui.http_port=32500" +
			"&data_dir=/config")

	req := httptest.NewRequest("POST", "/ui/bridge/save", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d", rw.Code)
	}
	if saver.got != nil {
		t.Error("saver.Save should NOT have been called on validation error")
	}
	if !strings.Contains(rw.Body.String(), "mister.host") {
		t.Errorf("expected host validation message, body = %s", rw.Body)
	}
}

func TestHandleBridge_POST_CSRFRejected(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("POST", "/ui/bridge/save", strings.NewReader(""))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rw.Code)
	}
}

// firstRunSaver embeds fakeBridgeSaver + FirstRunAware methods.
type firstRunSaver struct {
	fakeBridgeSaver
	firstRun bool
}

func (f *firstRunSaver) IsFirstRun() bool       { return f.firstRun }
func (f *firstRunSaver) DismissFirstRun() error { f.firstRun = false; return nil }

func TestHandleBridge_GET_FirstRunBannerShown(t *testing.T) {
	saver := &firstRunSaver{firstRun: true}
	reg := adapters.NewRegistry()
	s, _ := New(Config{Registry: reg, BridgeSaver: saver})
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if !strings.Contains(rw.Body.String(), "Quick start") {
		t.Error("first-run banner missing")
	}
}

func TestHandleBridge_GET_FirstRunBannerHidden(t *testing.T) {
	saver := &firstRunSaver{firstRun: false}
	reg := adapters.NewRegistry()
	s, _ := New(Config{Registry: reg, BridgeSaver: saver})
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if strings.Contains(rw.Body.String(), "Quick start") {
		t.Error("first-run banner should be hidden after dismissal")
	}
}

func TestHandleBridge_DismissFirstRun(t *testing.T) {
	saver := &firstRunSaver{firstRun: true}
	reg := adapters.NewRegistry()
	s, _ := New(Config{Registry: reg, BridgeSaver: saver})
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("POST", "/ui/bridge/dismiss-first-run", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d", rw.Code)
	}
	if saver.firstRun {
		t.Error("firstRun should be false after dismiss")
	}
}

// TestHandleBridge_GET_RendersSSHUserPrefilled confirms ssh_user
// renders as a normal text input prefilled from the saver.
func TestHandleBridge_GET_RendersSSHUserPrefilled(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()
	if !strings.Contains(body, `name="mister.ssh_user"`) {
		t.Error("ssh_user input not rendered")
	}
	if !strings.Contains(body, `value="alice"`) {
		t.Error("ssh_user value not prefilled (expected alice)")
	}
}

// TestHandleBridge_GET_DoesNotEchoSSHPassword guards the no-echo
// invariant: the stored password must NEVER appear in the rendered
// HTML, regardless of what's in the saver. The input is rendered
// as type=password with no value attribute.
func TestHandleBridge_GET_DoesNotEchoSSHPassword(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()
	if strings.Contains(body, "hunter2") {
		t.Error("stored password leaked into rendered HTML")
	}
	if !strings.Contains(body, `name="mister.ssh_password"`) {
		t.Error("ssh_password input not rendered")
	}
	if !strings.Contains(body, `type="password"`) {
		t.Error("ssh_password should render as type=password")
	}
	if !strings.Contains(body, "Leave empty to keep existing") {
		t.Error("ssh_password placeholder missing")
	}
}

// TestHandleBridge_POST_PreservesSSHPasswordOnEmpty verifies that an
// empty ssh_password in the form submission preserves the prior
// stored password (matching the placeholder "Leave empty to keep
// existing"). Without this, every save would silently clear the
// password whenever the operator edited an unrelated field.
func TestHandleBridge_POST_PreservesSSHPasswordOnEmpty(t *testing.T) {
	saver := &fakeBridgeSaver{}
	mux := newBridgeTestServer(t, saver)

	body := strings.NewReader(
		"mister.host=192.168.1.99" +
			"&mister.port=32100" +
			"&mister.source_port=32101" +
			"&mister.ssh_user=root" +
			"&mister.ssh_password=" + // intentionally empty
			"&host_ip=" +
			"&video.modeline=NTSC_480i" +
			"&video.interlace_field_order=tff" +
			"&video.aspect_mode=auto" +
			"&video.lz4_enabled=true" +
			"&audio.sample_rate=48000" +
			"&audio.channels=2" +
			"&ui.http_port=32500" +
			"&data_dir=/config")

	req := httptest.NewRequest("POST", "/ui/bridge/save", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
	}
	if saver.got == nil {
		t.Fatal("saver.Save not called")
	}
	// fakeBridgeSaver.Current() returns SSHPassword = "hunter2".
	// Empty-form submit should preserve it.
	if saver.got.MiSTer.SSHPassword != "hunter2" {
		t.Errorf("SSHPassword = %q, want preserved value 'hunter2'", saver.got.MiSTer.SSHPassword)
	}
}

// TestHandleBridge_POST_OverwritesSSHPasswordWhenProvided is a
// regression guard: it passes from green (Task 5's parseBridgeForm
// already does this) and locks in that the preserve-on-empty
// conditional in this task does NOT clamp the password to its
// previous value when the operator types a new one.
func TestHandleBridge_POST_OverwritesSSHPasswordWhenProvided(t *testing.T) {
	saver := &fakeBridgeSaver{}
	mux := newBridgeTestServer(t, saver)

	body := strings.NewReader(
		"mister.host=192.168.1.99" +
			"&mister.port=32100" +
			"&mister.source_port=32101" +
			"&mister.ssh_user=root" +
			"&mister.ssh_password=newsecret" +
			"&host_ip=" +
			"&video.modeline=NTSC_480i" +
			"&video.interlace_field_order=tff" +
			"&video.aspect_mode=auto" +
			"&video.lz4_enabled=true" +
			"&audio.sample_rate=48000" +
			"&audio.channels=2" +
			"&ui.http_port=32500" +
			"&data_dir=/config")

	req := httptest.NewRequest("POST", "/ui/bridge/save", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d", rw.Code)
	}
	if saver.got == nil {
		t.Fatal("saver.Save not called")
	}
	if saver.got.MiSTer.SSHPassword != "newsecret" {
		t.Errorf("SSHPassword = %q, want overwrite to 'newsecret'", saver.got.MiSTer.SSHPassword)
	}
}
