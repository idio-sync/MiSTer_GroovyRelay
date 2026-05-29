package chassis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/launchcore"
)

// fakeBridgeSettingsSaver is a compile-time conformance fixture for the
// chassis-owned BridgeSettingsSaver interface. If the interface shape
// changes, this fails to build, alerting the changeset reviewer.
type fakeBridgeSettingsSaver struct {
	cur    config.BridgeConfig
	saveFn func(config.BridgeConfig) (adapters.ApplyScope, error)
}

func (f fakeBridgeSettingsSaver) Current() config.BridgeConfig { return f.cur }
func (f fakeBridgeSettingsSaver) Save(c config.BridgeConfig) (adapters.ApplyScope, error) {
	if f.saveFn != nil {
		return f.saveFn(c)
	}
	return adapters.ScopeHotSwap, nil
}

// SaveTouched mirrors the production *uiserver.BridgeSaver.SaveTouched
// semantics for tests: it applies the closure to a copy of f.cur and
// forwards the resulting BridgeConfig to saveFn (so existing tests that
// inspect the saved config via saveFn continue to work).
func (f fakeBridgeSettingsSaver) SaveTouched(apply func(*config.BridgeConfig)) (adapters.ApplyScope, error) {
	next := f.cur
	apply(&next)
	if f.saveFn != nil {
		return f.saveFn(next)
	}
	return adapters.ScopeHotSwap, nil
}

// fakeProber is a compile-time conformance fixture for the chassis-owned
// Prober interface.
type fakeProber struct {
	res ProbeResult
	err error
}

func (f fakeProber) ProbeMister(ctx context.Context, b config.BridgeConfig) (ProbeResult, error) {
	return f.res, f.err
}

// fakeSettingsChipError exercises the structural settingsChipError match.
type fakeSettingsChipError struct {
	status int
	chip   string
	cause  error
}

func (f *fakeSettingsChipError) Error() string  { return f.chip }
func (f *fakeSettingsChipError) StatusCode() int { return f.status }
func (f *fakeSettingsChipError) Chip() string   { return f.chip }
func (f *fakeSettingsChipError) Unwrap() error  { return f.cause }

func TestChassisSettingsInterfaces_StructuralConformance(t *testing.T) {
	t.Parallel()
	var s BridgeSettingsSaver = fakeBridgeSettingsSaver{}
	if got := s.Current().DataDir; got != "" {
		t.Errorf("Current().DataDir = %q, want empty", got)
	}
	var p Prober = fakeProber{}
	if _, err := p.ProbeMister(context.Background(), config.BridgeConfig{}); err != nil {
		t.Errorf("ProbeMister err = %v, want nil", err)
	}
	var ce settingsChipError
	src := &fakeSettingsChipError{status: 409, chip: "PORT IN USE"}
	if !errors.As(src, &ce) {
		t.Fatalf("errors.As(src, &settingsChipError) = false, want true")
	}
	if ce.StatusCode() != 409 || ce.Chip() != "PORT IN USE" {
		t.Errorf("ce = (%d, %q), want (409, \"PORT IN USE\")", ce.StatusCode(), ce.Chip())
	}
}

func TestDecodeMisterHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want, errSub string
	}{
		{"192.168.1.42", "192.168.1.42", ""},
		{"mister.local", "mister.local", ""},
		{"  192.168.1.42  ", "192.168.1.42", ""},
		{"", "", "is required"},
		{"!not a host!", "", "not a valid IPv4 or hostname"},
		{"::1", "", "not a valid IPv4 or hostname"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			v, err := decodeMisterHost(tc.in)
			if tc.errSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSub) {
					t.Fatalf("err = %v, want substring %q", err, tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if v != tc.want {
				t.Errorf("v = %q, want %q", v, tc.want)
			}
		})
	}
}

func TestDecodePort_Range(t *testing.T) {
	t.Parallel()
	if _, err := decodePort("32100"); err != nil {
		t.Errorf("32100 err = %v, want nil", err)
	}
	if _, err := decodePort("65535"); err != nil {
		t.Errorf("65535 err = %v, want nil", err)
	}
	for _, in := range []string{"0", "-1", "65536", "abc", ""} {
		if _, err := decodePort(in); err == nil {
			t.Errorf("%q err = nil, want non-nil", in)
		}
	}
}

func TestDecodeOptionalIPv4(t *testing.T) {
	t.Parallel()
	v, err := decodeOptionalIPv4("")
	if err != nil || v != "" {
		t.Errorf("empty: (%q, %v), want (\"\", nil)", v, err)
	}
	v, err = decodeOptionalIPv4("192.168.1.1")
	if err != nil || v != "192.168.1.1" {
		t.Errorf("valid: (%q, %v)", v, err)
	}
	if _, err := decodeOptionalIPv4("not-an-ip"); err == nil {
		t.Errorf("invalid: err = nil, want non-nil")
	}
}

func TestDecodeOptionalAbsPath(t *testing.T) {
	t.Parallel()
	v, err := decodeOptionalAbsPath("")
	if err != nil || v != "" {
		t.Errorf("empty: (%q, %v), want (\"\", nil)", v, err)
	}
	// Use a platform-appropriate absolute path so the test passes on both
	// Windows (requires drive letter, e.g. C:\...) and Unix (/var/lib/relay).
	absPath := "/var/lib/relay"
	if runtime.GOOS == "windows" {
		absPath = `C:\var\lib\relay`
	}
	v, err = decodeOptionalAbsPath(absPath)
	if err != nil || v != absPath {
		t.Errorf("absolute: (%q, %v)", v, err)
	}
	if _, err := decodeOptionalAbsPath("relative/path"); err == nil {
		t.Errorf("relative: err = nil, want non-nil")
	}
}

func TestDecodeOptionalExecutablePath(t *testing.T) {
	t.Parallel()
	tool := tempExecutable(t, "ffmpeg")
	v, err := decodeOptionalExecutablePath(tool)
	if err != nil || v != tool {
		t.Fatalf("valid executable: (%q, %v), want (%q, nil)", v, err, tool)
	}
	if _, err := decodeOptionalExecutablePath("relative/tool"); err == nil {
		t.Errorf("relative executable path: err = nil, want non-nil")
	}
	missingName := "ffmpeg"
	if runtime.GOOS == "windows" {
		missingName += ".exe"
	}
	if _, err := decodeOptionalExecutablePath(filepath.Join(t.TempDir(), missingName)); err == nil {
		t.Errorf("missing executable path: err = nil, want non-nil")
	}
}

func tempExecutable(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write temp executable: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatalf("chmod temp executable: %v", err)
		}
	}
	return path
}

func TestBridgeFieldDecoders_HasEntryForEveryNetworkField(t *testing.T) {
	t.Parallel()
	want := []string{
		"mister_host", "mister_port", "mister_source_port",
		"ui_http_port", "host_ip", "data_dir",
		"ffmpeg_path", "ffprobe_path", "ytdlp_path",
	}
	for _, name := range want {
		if _, ok := bridgeFieldDecoders[name]; !ok {
			t.Errorf("bridgeFieldDecoders missing entry %q", name)
		}
	}
}

func TestBridgeFieldOverlays_HostWritesToMisterHost(t *testing.T) {
	t.Parallel()
	cfg := config.BridgeConfig{}
	bridgeFieldOverlays["mister_host"](&cfg, "192.168.1.42")
	if cfg.MiSTer.Host != "192.168.1.42" {
		t.Errorf("MiSTer.Host = %q, want 192.168.1.42", cfg.MiSTer.Host)
	}
}

func TestBridgeFieldOverlays_AllNetworkFieldsPresent(t *testing.T) {
	t.Parallel()
	// The decoder table is the authoritative supported-fields set.
	for name := range bridgeFieldDecoders {
		if _, ok := bridgeFieldOverlays[name]; !ok {
			t.Errorf("bridgeFieldOverlays missing entry for decoder %q", name)
		}
	}
	for name := range bridgeFieldOverlays {
		if _, ok := bridgeFieldDecoders[name]; !ok {
			t.Errorf("bridgeFieldOverlays has orphan entry %q (no decoder)", name)
		}
	}
}

func TestBridgeFieldOverlays_EveryFieldRoundTrips(t *testing.T) {
	t.Parallel()
	cases := map[string]any{
		"mister_host":        "10.0.0.1",
		"mister_port":        32100,
		"mister_source_port": 32101,
		"ui_http_port":       32500,
		"host_ip":            "10.0.0.2",
		"data_dir":           "/tmp/data",
		"ffmpeg_path":        "/usr/bin/ffmpeg",
		"ffprobe_path":       "/usr/bin/ffprobe",
		"ytdlp_path":         "/usr/bin/yt-dlp",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := config.BridgeConfig{}
			bridgeFieldOverlays[name](&cfg, value)
			// Round-trip: assert the field is non-zero on the right path.
			// We assert via reflective compare rather than per-path so the
			// test stays compact.
			zero := config.BridgeConfig{}
			if reflect.DeepEqual(cfg, zero) {
				t.Errorf("overlay %q wrote nothing to BridgeConfig", name)
			}
		})
	}
}

func TestScopeLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   adapters.ApplyScope
		want string
	}{
		{adapters.ScopeHotSwap, "hot"},
		{adapters.ScopeNextCast, "next"},
		{adapters.ScopeRestartCast, "recast"},
		{adapters.ScopeRestartBridge, "reboot"},
	}
	for _, tc := range cases {
		got, ok := scopeLabel(tc.in)
		if !ok {
			t.Errorf("scopeLabel(%v) ok = false, want true", tc.in)
		}
		if got != tc.want {
			t.Errorf("scopeLabel(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, ok := scopeLabel(adapters.ApplyScope(99)); ok {
		t.Errorf("scopeLabel(unknown) ok = true, want false")
	}
}

func TestBridgeFieldScopes_AllNetworkFieldsPresent(t *testing.T) {
	t.Parallel()
	for name := range bridgeFieldDecoders {
		if _, ok := bridgeFieldScopes[name]; !ok {
			t.Errorf("bridgeFieldScopes missing entry for decoder %q", name)
		}
	}
}

func TestBridgeFieldScopes_NetworkPaneIsRebootOrHot(t *testing.T) {
	t.Parallel()
	want := map[string]adapters.ApplyScope{
		"mister_host":        adapters.ScopeRestartBridge,
		"mister_port":        adapters.ScopeRestartBridge,
		"mister_source_port": adapters.ScopeRestartBridge,
		"ui_http_port":       adapters.ScopeRestartBridge,
		"host_ip":            adapters.ScopeRestartBridge,
		"data_dir":           adapters.ScopeRestartBridge,
		"ffmpeg_path":        adapters.ScopeHotSwap,
		"ffprobe_path":       adapters.ScopeHotSwap,
		"ytdlp_path":         adapters.ScopeHotSwap,
	}
	for name, wantScope := range want {
		if got := bridgeFieldScopes[name]; got != wantScope {
			t.Errorf("scope[%s] = %v, want %v", name, got, wantScope)
		}
	}
}

func newTestServerWithSaver(t *testing.T, saver BridgeSettingsSaver) *Server {
	t.Helper()
	return &Server{cfg: Config{
		BridgeSaver: saver,
		Registry:    adapters.NewRegistry(),
		Bridge:      config.BridgeConfig{MiSTer: config.MisterConfig{Host: "old", Port: 32100, SourcePort: 32101}},
	}}
}

func postBridge(t *testing.T, srv *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/bridge",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleSettingsBridgePost(rec, req)
	return rec
}

func TestHandleSettingsBridgePost_HotSwapSuccess(t *testing.T) {
	t.Parallel()
	var called bool
	toolPath := tempExecutable(t, "ffmpeg")
	saver := fakeBridgeSettingsSaver{
		saveFn: func(c config.BridgeConfig) (adapters.ApplyScope, error) {
			called = true
			if c.FFmpegPath != toolPath {
				t.Errorf("FFmpegPath = %q, want %q", c.FFmpegPath, toolPath)
			}
			return adapters.ScopeHotSwap, nil
		},
	}
	srv := newTestServerWithSaver(t, saver)
	rec := postBridge(t, srv, url.Values{"ffmpeg_path": {toolPath}})
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatalf("saver.Save was not called")
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ok"] != true || body["scope"] != "hot" {
		t.Errorf("body = %+v, want {ok:true, scope:\"hot\"}", body)
	}
}

func TestHandleSettingsBridgePost_RebootScope(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{
		saveFn: func(_ config.BridgeConfig) (adapters.ApplyScope, error) {
			return adapters.ScopeRestartBridge, nil
		},
	}
	srv := newTestServerWithSaver(t, saver)
	rec := postBridge(t, srv, url.Values{"mister_host": {"192.168.1.42"}})
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["scope"] != "reboot" {
		t.Errorf("scope = %v, want reboot", body["scope"])
	}
}

func TestHandleSettingsBridgePost_FieldValidationError(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{
		saveFn: func(_ config.BridgeConfig) (adapters.ApplyScope, error) {
			t.Fatalf("saver must not be called on field-validation error")
			return 0, nil
		},
	}
	srv := newTestServerWithSaver(t, saver)
	rec := postBridge(t, srv, url.Values{"mister_host": {""}})
	if rec.Code != 400 {
		t.Fatalf("Code = %d, want 400", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errs, ok := body["errors"].(map[string]any)
	if !ok {
		t.Fatalf("errors not present in body: %s", rec.Body.String())
	}
	if msg, _ := errs["mister_host"].(string); !strings.Contains(msg, "is required") {
		t.Errorf("errors[mister_host] = %v, want substring 'is required'", errs["mister_host"])
	}
}

func TestHandleSettingsBridgePost_MultipleFieldErrors(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{}
	srv := newTestServerWithSaver(t, saver)
	rec := postBridge(t, srv, url.Values{
		"mister_host": {""},
		"mister_port": {"99999"},
	})
	if rec.Code != 400 {
		t.Fatalf("Code = %d, want 400", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errs := body["errors"].(map[string]any)
	if _, ok := errs["mister_host"]; !ok {
		t.Errorf("missing mister_host error")
	}
	if _, ok := errs["mister_port"]; !ok {
		t.Errorf("missing mister_port error")
	}
}

func TestHandleSettingsBridgePost_EmptyBodyReturns400BadInput(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithSaver(t, fakeBridgeSettingsSaver{})
	rec := postBridge(t, srv, url.Values{})
	if rec.Code != 400 {
		t.Fatalf("Code = %d, want 400", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "BAD INPUT" {
		t.Errorf("chip = %v, want BAD INPUT", body["chip"])
	}
}

func TestHandleSettingsBridgePost_NilSaverReturns503(t *testing.T) {
	t.Parallel()
	srv := &Server{cfg: Config{Registry: adapters.NewRegistry()}}
	rec := postBridge(t, srv, url.Values{"mister_host": {"1.2.3.4"}})
	if rec.Code != 503 {
		t.Fatalf("Code = %d, want 503", rec.Code)
	}
}

func TestHandleSettingsBridgePost_PreflightChipError(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{
		saveFn: func(_ config.BridgeConfig) (adapters.ApplyScope, error) {
			return 0, &fakeSettingsChipError{status: 409, chip: "PORT IN USE"}
		},
	}
	srv := newTestServerWithSaver(t, saver)
	rec := postBridge(t, srv, url.Values{"ui_http_port": {"32500"}})
	if rec.Code != 409 {
		t.Fatalf("Code = %d, want 409", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "PORT IN USE" {
		t.Errorf("chip = %v, want PORT IN USE", body["chip"])
	}
}

func TestHandleSettingsBridgePost_UnknownScopeReturns500(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{
		saveFn: func(_ config.BridgeConfig) (adapters.ApplyScope, error) {
			return adapters.ApplyScope(99), nil
		},
	}
	srv := newTestServerWithSaver(t, saver)
	rec := postBridge(t, srv, url.Values{"mister_host": {"1.2.3.4"}})
	if rec.Code != 500 {
		t.Fatalf("Code = %d, want 500", rec.Code)
	}
}

func postProbe(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/action/probe-mister", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleSettingsActionProbeMister(rec, req)
	return rec
}

func TestHandleSettingsActionProbeMister_Success(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithSaver(t, fakeBridgeSettingsSaver{
		cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: "192.168.1.42", Port: 32100}},
	})
	srv.cfg.Prober = fakeProber{res: ProbeResult{LatencyMs: 4.2, Host: "192.168.1.42", Port: 32100}}
	rec := postProbe(t, srv)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if got, _ := body["latency_ms"].(float64); got < 4.0 || got > 5.0 {
		t.Errorf("latency_ms = %v, want ~4.2", body["latency_ms"])
	}
	if body["host"] != "192.168.1.42" {
		t.Errorf("host = %v, want 192.168.1.42", body["host"])
	}
}

func TestHandleSettingsActionProbeMister_Timeout(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithSaver(t, fakeBridgeSettingsSaver{
		cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: "1.2.3.4", Port: 32100}},
	})
	srv.cfg.Prober = fakeProber{err: context.DeadlineExceeded}
	rec := postProbe(t, srv)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200 (timeout is operational, not transport)", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ok"] != false || body["error"] != "timeout" {
		t.Errorf("body = %+v, want {ok:false, error:\"timeout\"}", body)
	}
}

func TestHandleSettingsActionProbeMister_SocketError(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithSaver(t, fakeBridgeSettingsSaver{
		cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: "1.2.3.4", Port: 32100}},
	})
	srv.cfg.Prober = fakeProber{err: errors.New("socket: connection refused")}
	rec := postProbe(t, srv)
	if rec.Code != 500 {
		t.Fatalf("Code = %d, want 500", rec.Code)
	}
}

// TestHandleSettingsActionProbeMister_SocketErrorRedactsHosts verifies the
// JSON error body has dotted-quad IPv4:port tokens replaced with <host>,
// so a leaky upstream socket message can't echo internal addresses on
// the wire.
func TestHandleSettingsActionProbeMister_SocketErrorRedactsHosts(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithSaver(t, fakeBridgeSettingsSaver{
		cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: "1.2.3.4", Port: 32100}},
	})
	srv.cfg.Prober = fakeProber{err: errors.New("read udp 127.0.0.1:54321->192.168.1.42:32100: connection refused")}
	rec := postProbe(t, srv)
	if rec.Code != 500 {
		t.Fatalf("Code = %d, want 500", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errStr, _ := body["error"].(string)
	for _, leak := range []string{"127.0.0.1", "192.168.1.42", "54321", "32100"} {
		if strings.Contains(errStr, leak) {
			t.Errorf("response error %q contains %q (must be redacted)", errStr, leak)
		}
	}
	if !strings.Contains(errStr, "<host>") {
		t.Errorf("response error %q missing <host> redaction marker", errStr)
	}
	if !strings.Contains(errStr, "connection refused") {
		t.Errorf("response error %q lost the actionable suffix", errStr)
	}
}

func TestSanitizeProbeError_RedactsIPv4AndPort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "ip-with-port",
			in:   "read udp 127.0.0.1:54321->192.168.1.42:32100: connection refused",
			want: "read udp <host>-><host>: connection refused",
		},
		{
			name: "ip-without-port",
			in:   "dial 10.0.0.1: network unreachable",
			want: "dial <host>: network unreachable",
		},
		{
			name: "no-ip-passthrough",
			in:   "open UDP probe socket: bind: permission denied",
			want: "open UDP probe socket: bind: permission denied",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeProbeError(errors.New(tc.in))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeProbeError_CapsLengthAt200(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 250)
	got := sanitizeProbeError(errors.New(long))
	if len(got) != 200 {
		t.Errorf("len = %d, want 200", len(got))
	}
}

func TestHandleSettingsActionProbeMister_NilProberReturns503(t *testing.T) {
	t.Parallel()
	srv := newTestServerWithSaver(t, fakeBridgeSettingsSaver{})
	// Prober is nil
	rec := postProbe(t, srv)
	if rec.Code != 503 {
		t.Fatalf("Code = %d, want 503", rec.Code)
	}
}

func TestHandleSettingsActionProbeMister_NilSaverReturns503(t *testing.T) {
	t.Parallel()
	srv := &Server{cfg: Config{
		Prober:   fakeProber{},
		Registry: adapters.NewRegistry(),
	}}
	rec := postProbe(t, srv)
	if rec.Code != 503 {
		t.Fatalf("Code = %d, want 503", rec.Code)
	}
}

// fakeCoreLauncher is the test fixture for the chassis-owned CoreLauncher interface.
type fakeCoreLauncher struct {
	calls int
	err   error
}

func (f *fakeCoreLauncher) Launch(ctx context.Context) error {
	f.calls++
	return f.err
}

func TestCoreLauncher_StructuralConformance(t *testing.T) {
	t.Parallel()
	var l CoreLauncher = &fakeCoreLauncher{}
	if err := l.Launch(context.Background()); err != nil {
		t.Errorf("Launch err = %v, want nil", err)
	}
}

func TestDecodeVideoModeline(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want, errSub string
	}{
		{"NTSC_480i", "NTSC_480i", ""},
		{"NTSC_240p", "NTSC_240p", ""},
		{"PAL_576i", "PAL_576i", ""},
		{"PAL_288p", "PAL_288p", ""},
		{"", "", "must be one of"},
		{"720p", "", "must be one of"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			v, err := decodeVideoModeline(tc.in)
			if tc.errSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSub) {
					t.Fatalf("err = %v, want substring %q", err, tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if v != tc.want {
				t.Errorf("v = %q, want %q", v, tc.want)
			}
		})
	}
}

func TestDecodeInterlaceFieldOrder(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"tff", "bff"} {
		v, err := decodeInterlaceFieldOrder(ok)
		if err != nil || v != ok {
			t.Errorf("decodeInterlaceFieldOrder(%q) = (%q, %v)", ok, v, err)
		}
	}
	for _, bad := range []string{"", "xyz", "TFF"} {
		if _, err := decodeInterlaceFieldOrder(bad); err == nil ||
			!strings.Contains(err.Error(), "must be tff or bff") {
			t.Errorf("decodeInterlaceFieldOrder(%q) err = %v, want substring", bad, err)
		}
	}
}

func TestDecodeAspectMode(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"auto", "letterbox", "zoom"} {
		if v, err := decodeAspectMode(ok); err != nil || v != ok {
			t.Errorf("decodeAspectMode(%q) = (%q, %v)", ok, v, err)
		}
	}
	if _, err := decodeAspectMode("stretch"); err == nil ||
		!strings.Contains(err.Error(), "must be auto, letterbox, or zoom") {
		t.Errorf("decodeAspectMode(stretch) err = %v, want substring", err)
	}
}

func TestDecodeBool(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"true", "false"} {
		v, err := decodeBool(in)
		if err != nil {
			t.Errorf("decodeBool(%q) err = %v", in, err)
		}
		want := in == "true"
		if v != want {
			t.Errorf("decodeBool(%q) = %v, want %v", in, v, want)
		}
	}
	for _, bad := range []string{"", "yes", "TRUE", "1"} {
		if _, err := decodeBool(bad); err == nil ||
			!strings.Contains(err.Error(), "must be true or false") {
			t.Errorf("decodeBool(%q) err = %v, want substring", bad, err)
		}
	}
}

func TestDecodeAudioSampleRate(t *testing.T) {
	t.Parallel()
	for _, ok := range []int{22050, 44100, 48000} {
		raw := strconv.Itoa(ok)
		v, err := decodeAudioSampleRate(raw)
		if err != nil || v != ok {
			t.Errorf("decodeAudioSampleRate(%q) = (%d, %v)", raw, v, err)
		}
	}
	for _, bad := range []string{"", "96000", "abc"} {
		if _, err := decodeAudioSampleRate(bad); err == nil ||
			!strings.Contains(err.Error(), "must be 22050, 44100, or 48000") {
			t.Errorf("decodeAudioSampleRate(%q) err = %v, want substring", bad, err)
		}
	}
}

func TestDecodeAudioChannels(t *testing.T) {
	t.Parallel()
	for _, ok := range []int{1, 2} {
		raw := strconv.Itoa(ok)
		v, err := decodeAudioChannels(raw)
		if err != nil || v != ok {
			t.Errorf("decodeAudioChannels(%q) = (%d, %v)", raw, v, err)
		}
	}
	for _, bad := range []string{"", "0", "3", "abc"} {
		if _, err := decodeAudioChannels(bad); err == nil ||
			!strings.Contains(err.Error(), "must be 1 or 2") {
			t.Errorf("decodeAudioChannels(%q) err = %v, want substring", bad, err)
		}
	}
}

func TestDecodeMisterSSHUser(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want, errSub string
	}{
		{"root", "root", ""},
		{"  root  ", "root", ""},
		{"alice", "alice", ""},
		{"", "", "is required"},
		{"root:bar", "", "contains an illegal character"},
		{"root bar", "", "contains an illegal character"},
		{"line1\nline2", "", "contains an illegal character"},
		{"with\x00nul", "", "contains an illegal character"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			v, err := decodeMisterSSHUser(tc.in)
			if tc.errSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSub) {
					t.Fatalf("err = %v, want substring %q", err, tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if v != tc.want {
				t.Errorf("v = %q, want %q", v, tc.want)
			}
		})
	}
}

func TestDecodeMisterSSHPassword(t *testing.T) {
	t.Parallel()
	// Decoder is permissive: accepts any string verbatim including empty.
	// The overlay handles preserve-on-empty.
	cases := []string{"", "hunter2", "  trimmed  ", "p@ssw0rd!"}
	for _, in := range cases {
		v, err := decodeMisterSSHPassword(in)
		if err != nil {
			t.Errorf("decodeMisterSSHPassword(%q) err = %v", in, err)
		}
		if v != in {
			t.Errorf("decodeMisterSSHPassword(%q) = %q, want %q (no trim/transform)", in, v, in)
		}
	}
}

func TestMisterSSHPassword_OverlayPreservesOnEmpty(t *testing.T) {
	t.Parallel()
	overlay := bridgeFieldOverlays["mister_ssh_password"]
	if overlay == nil {
		t.Fatalf("bridgeFieldOverlays missing mister_ssh_password entry")
	}
	c := &config.BridgeConfig{MiSTer: config.MisterConfig{SSHPassword: "stored"}}

	// Empty value must NOT change the stored password.
	overlay(c, "")
	if c.MiSTer.SSHPassword != "stored" {
		t.Errorf("after overlay(empty) password = %q, want \"stored\"", c.MiSTer.SSHPassword)
	}

	// Non-empty value replaces.
	overlay(c, "newpass")
	if c.MiSTer.SSHPassword != "newpass" {
		t.Errorf("after overlay(newpass) password = %q, want \"newpass\"", c.MiSTer.SSHPassword)
	}
}

func TestDecodeIntInRange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in            string
		lo, hi, want  int
		errSub        string
	}{
		{"1", 1, 12, 1, ""},
		{"12", 1, 12, 12, ""},
		{"6", 1, 12, 6, ""},
		{"0", 1, 12, 0, "must be in [1, 12]"},
		{"13", 1, 12, 0, "must be in [1, 12]"},
		{"", 1, 12, 0, "must be a whole number"},
		{"abc", 1, 12, 0, "must be a whole number"},
	}
	for _, tc := range cases {
		v, err := decodeIntInRange(tc.in, tc.lo, tc.hi)
		if tc.errSub != "" {
			if err == nil || !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("decodeIntInRange(%q,%d,%d) err = %v, want substring %q",
					tc.in, tc.lo, tc.hi, err, tc.errSub)
			}
			continue
		}
		if err != nil || v != tc.want {
			t.Errorf("decodeIntInRange(%q,%d,%d) = (%d, %v), want (%d, nil)",
				tc.in, tc.lo, tc.hi, v, err, tc.want)
		}
	}
}

func TestDecodeInt64InRange(t *testing.T) {
	t.Parallel()
	// Uses humanizeBytes-style labels in the error message.
	const (
		lo = int64(16777216)
		hi = int64(2147483648)
	)
	cases := []struct {
		in     string
		want   int64
		errSub string
	}{
		{"16777216", lo, ""},
		{"2147483648", hi, ""},
		{"268435456", 268435456, ""},
		{"16777215", 0, "must be in [16 MB, 2 GB]"},
		{"2147483649", 0, "must be in [16 MB, 2 GB]"},
		{"", 0, "must be a whole number"},
	}
	for _, tc := range cases {
		v, err := decodeInt64InRange(tc.in, lo, hi)
		if tc.errSub != "" {
			if err == nil || !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("decodeInt64InRange(%q) err = %v, want substring %q",
					tc.in, err, tc.errSub)
			}
			continue
		}
		if err != nil || v != tc.want {
			t.Errorf("decodeInt64InRange(%q) = (%d, %v), want (%d, nil)",
				tc.in, v, err, tc.want)
		}
	}
}

func TestLoggingDebugTableEntries(t *testing.T) {
	t.Parallel()
	if _, ok := bridgeFieldDecoders["logging_debug"]; !ok {
		t.Error("missing decoder for logging_debug")
	}
	overlay, ok := bridgeFieldOverlays["logging_debug"]
	if !ok {
		t.Fatal("missing overlay for logging_debug")
	}
	c := &config.BridgeConfig{}
	overlay(c, true)
	if !c.Logging.Debug {
		t.Errorf("after overlay(true) Logging.Debug = false, want true")
	}
	overlay(c, false)
	if c.Logging.Debug {
		t.Errorf("after overlay(false) Logging.Debug = true, want false")
	}
	if got := bridgeFieldScopes["logging_debug"]; got != adapters.ScopeHotSwap {
		t.Errorf("scope for logging_debug = %v, want ScopeHotSwap", got)
	}
}

func TestHLSDecodersTableEntries(t *testing.T) {
	t.Parallel()
	// Smoke test: every HLS form key has decoder + overlay + scope entries
	// and they're all RECAST except hls_enabled (also RECAST).
	keys := []string{
		"hls_enabled",
		"hls_live_edge_segments",
		"hls_start_segments",
		"hls_max_cached_segments",
		"hls_max_cache_bytes",
		"hls_max_playlist_bytes",
		"hls_max_segment_bytes",
		"hls_segment_timeout_seconds",
		"hls_playlist_timeout_seconds",
		"hls_max_variant_height",
		"hls_stale_cache_reap_hours",
	}
	for _, k := range keys {
		if _, ok := bridgeFieldDecoders[k]; !ok {
			t.Errorf("missing decoder for %s", k)
		}
		if _, ok := bridgeFieldOverlays[k]; !ok {
			t.Errorf("missing overlay for %s", k)
		}
		got, ok := bridgeFieldScopes[k]
		if !ok {
			t.Errorf("missing scope for %s", k)
			continue
		}
		if got != adapters.ScopeRestartCast {
			t.Errorf("scope for %s = %v, want ScopeRestartCast", k, got)
		}
	}
}

// TestHLSDecoders_BoundsSubsetOfValidator asserts every chassis-accepted
// HLS boundary value passes config.Sectioned.Validate() when placed into
// a fixture with compatible companion fields. Catches drift between
// chassis single-field bounds and config.Sectioned.Validate's
// single-field + cross-field invariants.
func TestHLSDecoders_BoundsSubsetOfValidator(t *testing.T) {
	t.Parallel()

	// Build a baseline Sectioned with a valid HLS config, then perturb
	// one field at a time to its low and high boundaries.
	baseline := func() config.Sectioned {
		return config.Sectioned{
			Bridge: config.BridgeConfig{
				MiSTer:     config.MisterConfig{Host: "127.0.0.1", Port: 32100, SourcePort: 32101},
				UI:         config.UIConfig{HTTPPort: 32500},
				Video:      config.VideoConfig{Modeline: "NTSC_480i", InterlaceFieldOrder: "bff", AspectMode: "auto", RGBMode: "rgb888", LZ4Enabled: true, DeltaLZ4Enabled: true},
				Audio:      config.AudioConfig{SampleRate: 48000, Channels: 2, OutputVolume: 100},
				Visualizer: config.VisualizerConfig{Mode: "retro_analyzer"},
				HLSBuffer: config.HLSBufferConfig{
					Enabled:                true,
					LiveEdgeSegments:       3,
					StartSegments:          2,
					MaxCachedSegments:      6,
					MaxCacheBytes:          268435456,
					MaxPlaylistBytes:       1048576,
					MaxSegmentBytes:        52428800,
					SegmentTimeoutSeconds:  10,
					PlaylistTimeoutSeconds: 10,
					MaxVariantHeight:       720,
					StaleCacheReapHours:    24,
				},
			},
		}
	}

	type tc struct {
		field string
		lo    int64 // use int64 to cover both int and int64 fields uniformly
		hi    int64
		apply func(c *config.BridgeConfig, n int64)
		// Companion adjustments for cross-field invariants.
		companion func(c *config.BridgeConfig, n int64)
	}
	cases := []tc{
		{"live_edge_segments", 1, 12,
			func(c *config.BridgeConfig, n int64) { c.HLSBuffer.LiveEdgeSegments = int(n) },
			func(c *config.BridgeConfig, n int64) {
				// live_edge_segments must be >= start_segments
				if n < int64(c.HLSBuffer.StartSegments) {
					c.HLSBuffer.StartSegments = int(n)
				}
			}},
		{"start_segments", 1, 6,
			func(c *config.BridgeConfig, n int64) { c.HLSBuffer.StartSegments = int(n) },
			func(c *config.BridgeConfig, n int64) {
				// live_edge_segments must be >= start_segments
				if c.HLSBuffer.LiveEdgeSegments < int(n) {
					c.HLSBuffer.LiveEdgeSegments = int(n)
				}
				// max_cached_segments must be >= start_segments
				if c.HLSBuffer.MaxCachedSegments < int(n) {
					c.HLSBuffer.MaxCachedSegments = int(n)
				}
			}},
		{"max_cached_segments", 2, 24,
			func(c *config.BridgeConfig, n int64) { c.HLSBuffer.MaxCachedSegments = int(n) },
			func(c *config.BridgeConfig, n int64) {
				if int64(c.HLSBuffer.StartSegments) > n {
					c.HLSBuffer.StartSegments = int(n)
				}
			}},
		{"max_cache_bytes", 16777216, 2147483648,
			func(c *config.BridgeConfig, n int64) { c.HLSBuffer.MaxCacheBytes = n },
			nil},
		{"max_playlist_bytes", 4096, 8388608,
			func(c *config.BridgeConfig, n int64) { c.HLSBuffer.MaxPlaylistBytes = n },
			nil},
		{"max_segment_bytes", 1048576, 536870912,
			func(c *config.BridgeConfig, n int64) { c.HLSBuffer.MaxSegmentBytes = n },
			nil},
		{"segment_timeout_seconds", 1, 60,
			func(c *config.BridgeConfig, n int64) { c.HLSBuffer.SegmentTimeoutSeconds = int(n) },
			nil},
		{"playlist_timeout_seconds", 1, 60,
			func(c *config.BridgeConfig, n int64) { c.HLSBuffer.PlaylistTimeoutSeconds = int(n) },
			nil},
		{"max_variant_height", 240, 2160,
			func(c *config.BridgeConfig, n int64) { c.HLSBuffer.MaxVariantHeight = int(n) },
			nil},
		{"stale_cache_reap_hours", 1, 168,
			func(c *config.BridgeConfig, n int64) { c.HLSBuffer.StaleCacheReapHours = int(n) },
			nil},
	}

	for _, c := range cases {
		for _, n := range []int64{c.lo, c.hi} {
			sec := baseline()
			c.apply(&sec.Bridge, n)
			if c.companion != nil {
				c.companion(&sec.Bridge, n)
			}
			if err := sec.Validate(); err != nil {
				t.Errorf("%s = %d: Sectioned.Validate err = %v", c.field, n, err)
			}
		}
	}
}

func newTestServerForLaunchCore(saver fakeBridgeSettingsSaver, launcher CoreLauncher) *Server {
	return &Server{
		cfg: Config{
			Version:      "test",
			StartedAt:    time.Unix(0, 0),
			BridgeSaver:  saver,
			CoreLauncher: launcher,
		},
	}
}

func postLaunchCore(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/receiver/settings/action/launch-core", nil)
	rec := httptest.NewRecorder()
	s.handleSettingsActionLaunchCore(rec, req)
	return rec
}

func TestLaunchCore_Success(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: "192.168.1.42", Port: 32100}}}
	launcher := &fakeCoreLauncher{}
	s := newTestServerForLaunchCore(saver, launcher)
	rec := postLaunchCore(t, s)
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if launcher.calls != 1 {
		t.Errorf("launcher.calls = %d, want 1", launcher.calls)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("body.ok = %v, want true", body["ok"])
	}
	if host, _ := body["host"].(string); host != "192.168.1.42" {
		t.Errorf("body.host = %q, want 192.168.1.42", host)
	}
}

func TestLaunchCore_EmptyHost(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: ""}}}
	launcher := &fakeCoreLauncher{}
	s := newTestServerForLaunchCore(saver, launcher)
	rec := postLaunchCore(t, s)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Code = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if launcher.calls != 0 {
		t.Errorf("launcher.calls = %d, want 0 (must not dial on empty host)", launcher.calls)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if got, _ := body["error"].(string); got != launchcore.EmptyHostMessage {
		t.Errorf("body.error = %q, want %q", got, launchcore.EmptyHostMessage)
	}
}

func TestLaunchCore_LauncherError(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: "host"}}}
	launcher := &fakeCoreLauncher{err: errors.New("ssh: handshake failed")}
	s := newTestServerForLaunchCore(saver, launcher)
	rec := postLaunchCore(t, s)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("Code = %d, want 500", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if got, _ := body["error"].(string); got != "ssh: handshake failed" {
		t.Errorf("body.error = %q, want \"ssh: handshake failed\" (no IP token to redact)", got)
	}
}

func TestLaunchCore_LeakyErrorRedacted(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: "host"}}}
	launcher := &fakeCoreLauncher{err: errors.New("dial tcp 192.168.1.42:22: connection refused")}
	s := newTestServerForLaunchCore(saver, launcher)
	rec := postLaunchCore(t, s)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("Code = %d, want 500", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	got, _ := body["error"].(string)
	if strings.Contains(got, "192.168.1.42") || strings.Contains(got, "22") {
		t.Errorf("body.error %q leaked IP/port — should be redacted", got)
	}
	if !strings.Contains(got, "<host>") {
		t.Errorf("body.error %q missing <host> redaction marker", got)
	}
}

func TestLaunchCore_NilLauncher(t *testing.T) {
	t.Parallel()
	saver := fakeBridgeSettingsSaver{cur: config.BridgeConfig{MiSTer: config.MisterConfig{Host: "host"}}}
	s := newTestServerForLaunchCore(saver, nil)
	rec := postLaunchCore(t, s)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Code = %d, want 503", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if chip, _ := body["chip"].(string); chip != "NOT READY" {
		t.Errorf("body.chip = %q, want NOT READY", chip)
	}
}

func TestLaunchCore_NilSaver(t *testing.T) {
	t.Parallel()
	s := &Server{
		cfg: Config{
			Version:      "test",
			StartedAt:    time.Unix(0, 0),
			CoreLauncher: &fakeCoreLauncher{},
			// BridgeSaver intentionally nil
		},
	}
	rec := postLaunchCore(t, s)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Code = %d, want 503", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Catalog provider POST handler tests (Task 14)
// ---------------------------------------------------------------------------

// assertJSONField decodes b as JSON and asserts that the top-level key
// equals the expected string. Fails the test if b is not valid JSON or
// if the key is absent / wrong type.
func assertJSONField(t *testing.T, b []byte, key, want string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("assertJSONField: unmarshal: %v (body=%s)", err, b)
	}
	got, ok := m[key].(string)
	if !ok {
		t.Errorf("assertJSONField: key %q not a string in body %s", key, b)
		return
	}
	if got != want {
		t.Errorf("assertJSONField: %q = %q, want %q (body=%s)", key, got, want, b)
	}
}

// assertJSONFieldErrors decodes b as JSON and asserts that each entry in
// wantErrs appears verbatim under body["errors"][key]. Extra error keys in
// the body are silently ignored.
func assertJSONFieldErrors(t *testing.T, b []byte, wantErrs map[string]string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("assertJSONFieldErrors: unmarshal: %v (body=%s)", err, b)
	}
	errs, ok := m["errors"].(map[string]any)
	if !ok {
		t.Fatalf("assertJSONFieldErrors: body[\"errors\"] not a map (body=%s)", b)
	}
	for k, want := range wantErrs {
		got, _ := errs[k].(string)
		if got != want {
			t.Errorf("assertJSONFieldErrors: errors[%q] = %q, want %q (body=%s)", k, got, want, b)
		}
	}
}

// fakeCatalogManagerMutating is a test double for CatalogSettingsManager
// that records the arguments passed to UpdateProvider and returns a
// configurable scope.
type fakeCatalogManagerMutating struct {
	providers                []CatalogProviderState
	scope                    adapters.ApplyScope
	err                      error
	lastID                   string
	lastPatch                CatalogProviderPatch
	lastDirectStreamDisabled *bool
}

func (f *fakeCatalogManagerMutating) Providers() []CatalogProviderState { return f.providers }
func (f *fakeCatalogManagerMutating) UpdateProvider(id string, patch CatalogProviderPatch) (adapters.ApplyScope, error) {
	f.lastID = id
	f.lastPatch = patch
	return f.scope, f.err
}
func (f *fakeCatalogManagerMutating) SetDirectStreamHLSBuffer(disabled bool) (adapters.ApplyScope, error) {
	f.lastDirectStreamDisabled = &disabled
	return f.scope, f.err
}

// newCatalogFormReq builds a POST request with an
// application/x-www-form-urlencoded body.
func newCatalogFormReq(t *testing.T, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// newTestServerForCatalog constructs a Server with only CatalogManager
// wired, bypassing New() validation (Version/StartedAt are not required
// by the catalog handler). Matches the pattern used by newTestServerWithSaver.
func newTestServerForCatalog(t *testing.T, mgr CatalogSettingsManager) *Server {
	t.Helper()
	return &Server{cfg: Config{CatalogManager: mgr}}
}

func TestHandleSettingsCatalogProviderPost_EnabledOnly_HotScope(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{
		providers: []CatalogProviderState{{ID: "mtv-rewind"}},
		scope:     adapters.ScopeHotSwap,
	}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/mtv-rewind", "enabled=false")
	req.SetPathValue("id", "mtv-rewind")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "scope", "hot")
	if mgr.lastID != "mtv-rewind" {
		t.Errorf("UpdateProvider id = %q; want mtv-rewind", mgr.lastID)
	}
	if mgr.lastPatch.Enabled == nil || *mgr.lastPatch.Enabled != false {
		t.Errorf("patch.Enabled = %v; want &false", mgr.lastPatch.Enabled)
	}
	if mgr.lastPatch.HLSBufferDisabled != nil {
		t.Errorf("patch.HLSBufferDisabled = %v; want nil", mgr.lastPatch.HLSBufferDisabled)
	}
}

func TestHandleSettingsCatalogProviderPost_HLSOnly_RecastScope(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{
		providers: []CatalogProviderState{{ID: "toonami-aftermath"}},
		scope:     adapters.ScopeRestartCast,
	}
	srv := newTestServerForCatalog(t, mgr)
	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/toonami-aftermath", "hls_buffer_disabled=true")
	req.SetPathValue("id", "toonami-aftermath")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "scope", "recast")
}

func TestHandleSettingsCatalogProviderPost_BothFields_RecastMaxWins(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{
		providers: []CatalogProviderState{{ID: "toonami-aftermath"}},
		scope:     adapters.ScopeRestartCast,
	}
	srv := newTestServerForCatalog(t, mgr)
	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/toonami-aftermath", "enabled=true&hls_buffer_disabled=true")
	req.SetPathValue("id", "toonami-aftermath")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "scope", "recast")
}

func TestHandleSettingsCatalogProviderPost_UnknownProvider_404(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{providers: []CatalogProviderState{{ID: "mtv-rewind"}}}
	srv := newTestServerForCatalog(t, mgr)
	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/does-not-exist", "enabled=true")
	req.SetPathValue("id", "does-not-exist")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rr.Code)
	}
	if mgr.lastID != "" {
		t.Errorf("UpdateProvider should NOT be called; got id=%q", mgr.lastID)
	}
}

func TestHandleSettingsCatalogProviderPost_BadBool_400FieldError(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{providers: []CatalogProviderState{{ID: "mtv-rewind"}}}
	srv := newTestServerForCatalog(t, mgr)
	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/mtv-rewind", "enabled=maybe")
	req.SetPathValue("id", "mtv-rewind")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONFieldErrors(t, rr.Body.Bytes(), map[string]string{
		"enabled": "must be true or false",
	})
}

func TestHandleSettingsCatalogProviderPost_EmptyBody_BadInputChip(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{providers: []CatalogProviderState{{ID: "mtv-rewind"}}}
	srv := newTestServerForCatalog(t, mgr)
	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/mtv-rewind", "")
	req.SetPathValue("id", "mtv-rewind")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "chip", "BAD INPUT")
}

func TestHandleSettingsCatalogProviderPost_NilManager_NotReady503(t *testing.T) {
	srv := newTestServerForCatalog(t, nil)
	req := newCatalogFormReq(t, "/receiver/settings/catalog/provider/x", "enabled=true")
	req.SetPathValue("id", "x")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogProviderPost(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rr.Code)
	}
	assertJSONField(t, rr.Body.Bytes(), "chip", "NOT READY")
}

func TestHandleSettingsCatalogDirectStreamHLSBufferPost_Success(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{scope: adapters.ScopeRestartCast}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/direct-stream-hls-buffer", "disabled=true")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogDirectStreamHLSBufferPost(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "scope", "recast")
	if mgr.lastDirectStreamDisabled == nil || *mgr.lastDirectStreamDisabled != true {
		t.Errorf("manager.SetDirectStreamHLSBuffer arg = %v; want &true", mgr.lastDirectStreamDisabled)
	}
}

func TestHandleSettingsCatalogDirectStreamHLSBufferPost_BadBool_400(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/direct-stream-hls-buffer", "disabled=maybe")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogDirectStreamHLSBufferPost(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONFieldErrors(t, rr.Body.Bytes(), map[string]string{
		"disabled": "must be true or false",
	})
}

func TestHandleSettingsCatalogDirectStreamHLSBufferPost_EmptyBody_BadInput(t *testing.T) {
	mgr := &fakeCatalogManagerMutating{}
	srv := newTestServerForCatalog(t, mgr)

	req := newCatalogFormReq(t, "/receiver/settings/catalog/direct-stream-hls-buffer", "")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogDirectStreamHLSBufferPost(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rr.Code)
	}
	assertJSONField(t, rr.Body.Bytes(), "chip", "BAD INPUT")
}

func TestHandleSettingsCatalogDirectStreamHLSBufferPost_NilManager_503(t *testing.T) {
	srv := newTestServerForCatalog(t, nil)
	req := newCatalogFormReq(t, "/receiver/settings/catalog/direct-stream-hls-buffer", "disabled=false")
	rr := httptest.NewRecorder()
	srv.handleSettingsCatalogDirectStreamHLSBufferPost(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rr.Code)
	}
	assertJSONField(t, rr.Body.Bytes(), "chip", "NOT READY")
}

func TestHandleSettingsActionRestoreDefaults_Success(t *testing.T) {
	cr := &fakeConfigReset{}
	srv := newTestServerForReset(t, cr)

	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/action/restore-defaults", nil)
	rr := httptest.NewRecorder()
	srv.handleSettingsActionRestoreDefaults(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "scope", "reboot")
	if cr.calls != 1 {
		t.Errorf("ResetToDefaults called %d times; want 1", cr.calls)
	}
}

func TestHandleSettingsActionRestoreDefaults_NilReset_503(t *testing.T) {
	srv := newTestServerForReset(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/action/restore-defaults", nil)
	rr := httptest.NewRecorder()
	srv.handleSettingsActionRestoreDefaults(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rr.Code)
	}
	assertJSONField(t, rr.Body.Bytes(), "chip", "NOT READY")
}

func TestHandleSettingsActionRestoreDefaults_ChipError(t *testing.T) {
	cr := &fakeConfigReset{err: &fakeChipErr{status: 500, chip: "WRITE FAILED"}}
	srv := newTestServerForReset(t, cr)

	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/action/restore-defaults", nil)
	rr := httptest.NewRecorder()
	srv.handleSettingsActionRestoreDefaults(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", rr.Code)
	}
	assertJSONField(t, rr.Body.Bytes(), "chip", "WRITE FAILED")
}

type fakeConfigReset struct {
	calls int
	err   error
}

func (f *fakeConfigReset) ResetToDefaults() error {
	f.calls++
	return f.err
}

type fakeChipErr struct {
	status int
	chip   string
}

func (e *fakeChipErr) Error() string   { return e.chip }
func (e *fakeChipErr) StatusCode() int { return e.status }
func (e *fakeChipErr) Chip() string    { return e.chip }

// newTestServerForReset constructs a Server with only ConfigReset wired,
// bypassing New() validation. Matches the pattern used by newTestServerForCatalog.
func newTestServerForReset(t *testing.T, cr ConfigReset) *Server {
	t.Helper()
	return &Server{cfg: Config{ConfigReset: cr}}
}
