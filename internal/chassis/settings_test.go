package chassis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
