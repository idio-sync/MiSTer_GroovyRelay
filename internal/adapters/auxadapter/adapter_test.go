package aux

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

func TestAdapterInterfaces(t *testing.T) {
	var _ adapters.Adapter = (*Adapter)(nil)
	var _ adapters.Validator = (*Adapter)(nil)
	var _ ui.ValueProvider = (*Adapter)(nil)
	var _ ui.EnableSetter = (*Adapter)(nil)
}

func TestNewRequiresCore(t *testing.T) {
	if _, err := New(AdapterConfig{}); err == nil {
		t.Fatal("New without Core: want error")
	}
}

func TestNewDefaults(t *testing.T) {
	a := newTestAdapter(t)
	if a.Name() != "aux" {
		t.Errorf("Name = %q, want aux", a.Name())
	}
	if a.DisplayName() != "AUX" {
		t.Errorf("DisplayName = %q, want AUX", a.DisplayName())
	}
	if a.IsEnabled() {
		t.Fatal("new adapter should be disabled")
	}
	st := a.Status()
	if st.State != adapters.StateStopped {
		t.Errorf("initial state = %v, want stopped", st.State)
	}
	if st.Since.IsZero() {
		t.Fatal("state Since should be initialized")
	}
}

func TestFieldsUseDottedInputKeysAndHotSwapScopes(t *testing.T) {
	a := newTestAdapter(t)
	fields := a.Fields()
	want := []struct {
		key      string
		kind     adapters.FieldKind
		def      any
		required bool
		enum     []string
	}{
		{key: "enabled", kind: adapters.KindBool, def: false},
		{key: "input.id", kind: adapters.KindText, def: "aux", required: true},
		{key: "input.name", kind: adapters.KindText, def: "AUX", required: true},
		{key: "input.mode", kind: adapters.KindEnum, def: ModeStreamURL, required: true, enum: []string{ModeStreamURL, ModeLocalCapture}},
		{key: "input.audio_output", kind: adapters.KindEnum, def: AudioOutputVisualOnly, enum: []string{AudioOutputVisualOnly, AudioOutputMonitor}},
		{key: "input.url", kind: adapters.KindText},
		{key: "input.format", kind: adapters.KindText},
		{key: "input.device", kind: adapters.KindText},
		{key: "input.sample_rate", kind: adapters.KindInt, def: 44100},
		{key: "input.channels", kind: adapters.KindInt, def: 1},
		{key: "input.thread_queue_size", kind: adapters.KindInt, def: 64},
		{key: "input.analyze_duration_ms", kind: adapters.KindInt, def: 100},
		{key: "input.probe_size", kind: adapters.KindInt, def: 32768},
	}
	if len(fields) != len(want) {
		t.Fatalf("Fields returned %d entries, want %d: %+v", len(fields), len(want), fields)
	}
	for i, expected := range want {
		got := fields[i]
		if got.Key != expected.key {
			t.Errorf("field[%d].Key = %q, want %q", i, got.Key, expected.key)
		}
		if got.Kind != expected.kind {
			t.Errorf("%s Kind = %v, want %v", expected.key, got.Kind, expected.kind)
		}
		if got.Default != expected.def {
			t.Errorf("%s Default = %#v, want %#v", expected.key, got.Default, expected.def)
		}
		if got.Required != expected.required {
			t.Errorf("%s Required = %v, want %v", expected.key, got.Required, expected.required)
		}
		if got.ApplyScope != adapters.ScopeHotSwap {
			t.Errorf("%s ApplyScope = %v, want HotSwap", expected.key, got.ApplyScope)
		}
		if len(expected.enum) > 0 {
			if len(got.Enum) != len(expected.enum) {
				t.Fatalf("%s Enum = %#v, want %#v", expected.key, got.Enum, expected.enum)
			}
			for j := range expected.enum {
				if got.Enum[j] != expected.enum[j] {
					t.Fatalf("%s Enum = %#v, want %#v", expected.key, got.Enum, expected.enum)
				}
			}
		}
	}
}

func TestDecodeConfigOverlaysDefaultsAndCurrentValues(t *testing.T) {
	raw, meta := decodeAUXSection(t, `
[adapters.aux]
enabled = true

[adapters.aux.input]
url = "http://127.0.0.1:8080/aux.wav"
sample_rate = 48000
channels = 2
`)
	a := newTestAdapter(t)
	if err := a.DecodeConfig(raw, meta); err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if !a.IsEnabled() {
		t.Fatal("DecodeConfig did not set Enabled")
	}
	values := a.CurrentValues()
	want := map[string]any{
		"enabled":                   true,
		"input.id":                  "aux",
		"input.name":                "AUX",
		"input.mode":                ModeStreamURL,
		"input.audio_output":        AudioOutputVisualOnly,
		"input.url":                 "http://127.0.0.1:8080/aux.wav",
		"input.format":              "",
		"input.device":              "",
		"input.sample_rate":         48000,
		"input.channels":            2,
		"input.thread_queue_size":   64,
		"input.analyze_duration_ms": 100,
		"input.probe_size":          32768,
	}
	if len(values) != len(want) {
		t.Fatalf("CurrentValues returned %d keys, want %d: %#v", len(values), len(want), values)
	}
	for key, expected := range want {
		if values[key] != expected {
			t.Errorf("CurrentValues[%q] = %#v, want %#v", key, values[key], expected)
		}
	}
}

func TestValidateRejectsWithoutMutatingConfig(t *testing.T) {
	a := newTestAdapter(t)
	raw, meta := decodeAUXSection(t, `
[adapters.aux]
enabled = true
`)
	if err := a.Validate(raw, meta); err == nil {
		t.Fatal("Validate enabled without configured input: want error")
	}
	if a.IsEnabled() {
		t.Fatal("Validate mutated adapter config")
	}
}

func TestApplyConfigHotSwapAndRejectedApplyDoesNotMutate(t *testing.T) {
	a := newTestAdapter(t)
	raw, meta := decodeAUXSection(t, `
[adapters.aux]
enabled = true

[adapters.aux.input]
id = "line-in"
name = "Line In"
mode = "local_capture"
audio_output = "monitor"
device = ":0"
sample_rate = 44100
channels = 1
thread_queue_size = 96
analyze_duration_ms = 300
probe_size = 65536
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Fatalf("scope = %v, want HotSwap", scope)
	}
	if a.cfg.Input.ID != "line-in" || a.cfg.Input.Device != ":0" {
		t.Fatalf("config was not applied: %+v", a.cfg.Input)
	}

	badRaw, badMeta := decodeAUXSection(t, `
[adapters.aux]
enabled = true
`)
	if _, err := a.ApplyConfig(badRaw, badMeta); err == nil {
		t.Fatal("invalid ApplyConfig: want error")
	}
	if a.cfg.Input.ID != "line-in" || a.cfg.Input.Device != ":0" {
		t.Fatalf("invalid ApplyConfig mutated config: %+v", a.cfg.Input)
	}
}

func TestSetEnabledAndStartStopLifecycle(t *testing.T) {
	fake := &fakeCore{}
	a := newTestAdapterWithCore(t, fake)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start disabled: %v", err)
	}
	if a.Status().State != adapters.StateStopped {
		t.Fatalf("disabled Start state = %v, want stopped", a.Status().State)
	}
	if fake.starts != 0 {
		t.Fatalf("Start should not start playback; starts = %d", fake.starts)
	}

	a.SetEnabled(true)
	if !a.IsEnabled() {
		t.Fatal("SetEnabled(true) did not update IsEnabled")
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start enabled: %v", err)
	}
	if a.Status().State != adapters.StateRunning {
		t.Fatalf("enabled Start state = %v, want running", a.Status().State)
	}
	if fake.starts != 0 {
		t.Fatalf("Start should not start playback; starts = %d", fake.starts)
	}
	beforeStop := a.Status().Since
	time.Sleep(time.Millisecond)
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	st := a.Status()
	if st.State != adapters.StateStopped {
		t.Fatalf("Stop state = %v, want stopped", st.State)
	}
	if !st.Since.After(beforeStop) {
		t.Fatalf("Stop did not refresh Since: before=%v after=%v", beforeStop, st.Since)
	}
}

func TestStopOnlyStopsActiveAUXRef(t *testing.T) {
	fake := &fakeCore{}
	a := newTestAdapterWithCore(t, fake)
	a.SetEnabled(true)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	a.activeRef = "aux:line-in"

	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if fake.stopRef != "aux:line-in" {
		t.Fatalf("StopIfAdapterRef called with %q, want aux:line-in", fake.stopRef)
	}
	if a.activeRef != "" {
		t.Fatalf("activeRef = %q, want cleared", a.activeRef)
	}
}

func TestStopPropagatesOwnershipStopError(t *testing.T) {
	wantErr := errors.New("core stop failed")
	fake := &fakeCore{stopErr: wantErr}
	a := newTestAdapterWithCore(t, fake)
	a.activeRef = "aux:line-in"

	if err := a.Stop(); !errors.Is(err, wantErr) {
		t.Fatalf("Stop error = %v, want %v", err, wantErr)
	}
}

func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	return newTestAdapterWithCore(t, &fakeCore{})
}

func newTestAdapterWithCore(t *testing.T, core SessionManager) *Adapter {
	t.Helper()
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{
			Audio: config.AudioConfig{
				SampleRate: 44100,
				Channels:   1,
			},
		},
		Core:     core,
		HTTPPort: 32500,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func decodeAUXSection(t *testing.T, raw string) (toml.Primitive, toml.MetaData) {
	t.Helper()
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(raw, &envelope)
	if err != nil {
		t.Fatal(err)
	}
	return envelope.Adapters["aux"], meta
}

type fakeCore struct {
	starts  int
	stopRef string
	stopErr error
	status  core.SessionStatus
}

func (f *fakeCore) StartSession(core.SessionRequest) error {
	f.starts++
	return nil
}

func (f *fakeCore) StopIfAdapterRef(ref string) (bool, error) {
	f.stopRef = ref
	return true, f.stopErr
}

func (f *fakeCore) Status() core.SessionStatus { return f.status }
