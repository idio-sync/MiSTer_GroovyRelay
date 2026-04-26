package url

import (
	"context"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestAdapter_ConformsToInterface(t *testing.T) {
	var _ adapters.Adapter = (*Adapter)(nil)
}

func TestAdapter_ConformsToValidator(t *testing.T) {
	var _ adapters.Validator = (*Adapter)(nil)
}

func TestAdapter_ConformsToRouteProvider(t *testing.T) {
	var _ adapters.RouteProvider = (*Adapter)(nil)
}

func TestAdapter_Name(t *testing.T) {
	a := newTestAdapter(t, nil)
	if a.Name() != "url" {
		t.Errorf("Name = %q", a.Name())
	}
}

func TestAdapter_DisplayName(t *testing.T) {
	a := newTestAdapter(t, nil)
	if a.DisplayName() != "URL" {
		t.Errorf("DisplayName = %q", a.DisplayName())
	}
}

func TestAdapter_Fields_HasEnabled(t *testing.T) {
	a := newTestAdapter(t, nil)
	fields := a.Fields()
	if len(fields) != 1 || fields[0].Key != "enabled" {
		t.Errorf("Fields = %+v, want single 'enabled' field", fields)
	}
	if fields[0].Kind != adapters.KindBool {
		t.Errorf("Fields[0].Kind = %v, want KindBool", fields[0].Kind)
	}
	if fields[0].ApplyScope != adapters.ScopeHotSwap {
		t.Errorf("Fields[0].ApplyScope = %v, want ScopeHotSwap", fields[0].ApplyScope)
	}
}

func TestAdapter_StatusInitial_Stopped(t *testing.T) {
	a := newTestAdapter(t, nil)
	if a.Status().State != adapters.StateStopped {
		t.Errorf("initial Status.State = %v, want StateStopped", a.Status().State)
	}
}

func TestAdapter_StartSetsRunning_StopSetsStopped(t *testing.T) {
	a := newTestAdapter(t, nil)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if a.Status().State != adapters.StateRunning {
		t.Errorf("after Start, State = %v, want StateRunning", a.Status().State)
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if a.Status().State != adapters.StateStopped {
		t.Errorf("after Stop, State = %v, want StateStopped", a.Status().State)
	}
}

func TestAdapter_SetEnabled_TogglesIsEnabled(t *testing.T) {
	a := newTestAdapter(t, nil)
	a.SetEnabled(true)
	if !a.IsEnabled() {
		t.Error("after SetEnabled(true), IsEnabled = false")
	}
	a.SetEnabled(false)
	if a.IsEnabled() {
		t.Error("after SetEnabled(false), IsEnabled = true")
	}
}

func TestAdapter_DecodeConfig_SetsEnabled(t *testing.T) {
	raw := `
[adapters.url]
enabled = true
`
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, _ := toml.Decode(raw, &envelope)
	a := newTestAdapter(t, nil)
	if err := a.DecodeConfig(envelope.Adapters["url"], meta); err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if !a.IsEnabled() {
		t.Error("Enabled not propagated")
	}
}

func TestAdapter_ApplyConfig_HotSwap(t *testing.T) {
	raw := `
[adapters.url]
enabled = true
`
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, _ := toml.Decode(raw, &envelope)
	a := newTestAdapter(t, nil)
	scope, err := a.ApplyConfig(envelope.Adapters["url"], meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v, want ScopeHotSwap", scope)
	}
}
