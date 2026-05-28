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

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
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
