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
		Audio:  config.AudioConfig{SampleRate: 48000, Channels: 2},
		MiSTer: config.MisterConfig{Host: "192.168.1.42", Port: 32100, SourcePort: 32101},
		UI:     config.UIConfig{HTTPPort: 32500},
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
