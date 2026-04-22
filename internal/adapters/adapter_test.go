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
