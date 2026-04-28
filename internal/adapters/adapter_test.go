package adapters

import (
	"context"
	"net/http"
	"testing"

	"github.com/BurntSushi/toml"
)

type stubAdapter struct{ name string }

func (s *stubAdapter) Name() string        { return s.name }
func (s *stubAdapter) DisplayName() string { return s.name }
func (s *stubAdapter) Fields() []FieldDef  { return nil }
func (s *stubAdapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	return nil
}
func (s *stubAdapter) IsEnabled() bool                 { return true }
func (s *stubAdapter) Start(ctx context.Context) error { return nil }
func (s *stubAdapter) Stop() error                     { return nil }
func (s *stubAdapter) Status() Status                  { return Status{State: StateStopped} }
func (s *stubAdapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (ApplyScope, error) {
	return ScopeHotSwap, nil
}

func TestStubAdapter_Conforms(t *testing.T) {
	var _ Adapter = (*stubAdapter)(nil)
}

func TestApplyScope_MaxWins(t *testing.T) {
	cases := []struct{ a, b, want ApplyScope }{
		{ScopeHotSwap, ScopeHotSwap, ScopeHotSwap},
		{ScopeHotSwap, ScopeRestartCast, ScopeRestartCast},
		{ScopeRestartCast, ScopeHotSwap, ScopeRestartCast},
		{ScopeRestartCast, ScopeRestartBridge, ScopeRestartBridge},
		{ScopeRestartBridge, ScopeHotSwap, ScopeRestartBridge},
	}
	for _, c := range cases {
		if got := MaxScope(c.a, c.b); got != c.want {
			t.Errorf("MaxScope(%v,%v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestFieldErrors_Error(t *testing.T) {
	fe := FieldErrors{{Key: "host", Msg: "required"}, {Key: "port", Msg: "bad"}}
	if fe.Error() == "" {
		t.Error("empty error string")
	}
}

func TestState_String(t *testing.T) {
	if StateRunning.String() != "RUN" {
		t.Errorf("StateRunning.String = %q, want RUN", StateRunning.String())
	}
}

// Ensure Handler type is compatible with http.HandlerFunc.
func TestHandler_Compat(t *testing.T) {
	var h Handler = func(w http.ResponseWriter, r *http.Request) {}
	_ = h
}

func TestFieldDef_SectionOrderZeroValue(t *testing.T) {
	fd := FieldDef{Section: "Network"}
	if fd.SectionOrder != 0 {
		t.Errorf("zero value: got %d, want 0", fd.SectionOrder)
	}
}

func TestKindAction_Const(t *testing.T) {
	// KindAction must follow KindSecret in the iota sequence.
	// Existing kinds must keep their values.
	if KindText != 0 {
		t.Errorf("KindText: got %d, want 0", KindText)
	}
	if KindInt != 1 {
		t.Errorf("KindInt: got %d, want 1", KindInt)
	}
	if KindBool != 2 {
		t.Errorf("KindBool: got %d, want 2", KindBool)
	}
	if KindEnum != 3 {
		t.Errorf("KindEnum: got %d, want 3", KindEnum)
	}
	if KindSecret != 4 {
		t.Errorf("KindSecret: got %d, want 4", KindSecret)
	}
	if KindAction != 5 {
		t.Errorf("KindAction: got %d, want 5", KindAction)
	}
}
