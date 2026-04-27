package jellyfin

import "testing"

func TestLinkState_InitialIsIdle(t *testing.T) {
	s := NewLinkState()
	if s.State() != LinkIdle {
		t.Errorf("initial = %v, want LinkIdle", s.State())
	}
}

func TestLinkState_StartLinking(t *testing.T) {
	s := NewLinkState()
	s.SetLinking()
	if s.State() != LinkLinking {
		t.Errorf("after SetLinking = %v, want LinkLinking", s.State())
	}
}

func TestLinkState_LinkSuccess(t *testing.T) {
	s := NewLinkState()
	s.SetLinking()
	s.SetLinked("alice", "server-abc")
	if s.State() != LinkLinked {
		t.Errorf("after SetLinked = %v, want LinkLinked", s.State())
	}
	user, server := s.LinkedAs()
	if user != "alice" || server != "server-abc" {
		t.Errorf("LinkedAs = (%q, %q), want (alice, server-abc)", user, server)
	}
}

func TestLinkState_LinkFailure(t *testing.T) {
	s := NewLinkState()
	s.SetLinking()
	s.SetError("invalid credentials")
	if s.State() != LinkError {
		t.Errorf("after SetError = %v, want LinkError", s.State())
	}
	if got := s.LastError(); got != "invalid credentials" {
		t.Errorf("LastError = %q", got)
	}
}

func TestLinkState_Unlink(t *testing.T) {
	s := NewLinkState()
	s.SetLinked("alice", "server-abc")
	s.SetIdle()
	if s.State() != LinkIdle {
		t.Errorf("after SetIdle = %v, want LinkIdle", s.State())
	}
	user, _ := s.LinkedAs()
	if user != "" {
		t.Errorf("LinkedAs.user after SetIdle = %q, want empty", user)
	}
}

func TestLinkState_StringForms(t *testing.T) {
	cases := map[LinkPhase]string{
		LinkIdle:    "idle",
		LinkLinking: "linking",
		LinkLinked:  "linked",
		LinkError:   "error",
	}
	for phase, want := range cases {
		if got := phase.String(); got != want {
			t.Errorf("LinkPhase(%d).String() = %q, want %q", int(phase), got, want)
		}
	}
}
