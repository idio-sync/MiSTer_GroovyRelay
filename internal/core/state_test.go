package core

import "testing"

func TestState_InitialIdle(t *testing.T) {
	s := New()
	if s.State() != StateIdle {
		t.Errorf("initial state = %s, want %s", s.State(), StateIdle)
	}
}

func TestState_IdleToPlaying(t *testing.T) {
	s := New()
	if err := s.Transition(EvPlayMedia); err != nil {
		t.Fatal(err)
	}
	if s.State() != StatePlaying {
		t.Errorf("state = %s", s.State())
	}
}

func TestState_PlayingToPausedToPlaying(t *testing.T) {
	s := New()
	if err := s.Transition(EvPlayMedia); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(EvPause); err != nil {
		t.Fatal(err)
	}
	if s.State() != StatePaused {
		t.Errorf("state = %s after pause", s.State())
	}
	if err := s.Transition(EvPlay); err != nil {
		t.Fatal(err)
	}
	if s.State() != StatePlaying {
		t.Errorf("state = %s after play", s.State())
	}
}

func TestState_Stop(t *testing.T) {
	s := New()
	if err := s.Transition(EvPlayMedia); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(EvStop); err != nil {
		t.Fatal(err)
	}
	if s.State() != StateIdle {
		t.Errorf("state = %s", s.State())
	}
}

func TestState_EOFReturnsToIdle(t *testing.T) {
	s := New()
	_ = s.Transition(EvPlayMedia)
	if err := s.Transition(EvEOF); err != nil {
		t.Fatal(err)
	}
	if s.State() != StateIdle {
		t.Errorf("state after EOF = %s, want Idle", s.State())
	}
}

func TestState_PreemptFromPlaying(t *testing.T) {
	s := New()
	_ = s.Transition(EvPlayMedia)
	// A second playMedia should succeed (preempt semantics).
	if err := s.Transition(EvPlayMedia); err != nil {
		t.Errorf("preempt playMedia failed: %v", err)
	}
	if s.State() != StatePlaying {
		t.Errorf("state after preempt = %s", s.State())
	}
}

func TestState_PreemptFromPaused(t *testing.T) {
	s := New()
	_ = s.Transition(EvPlayMedia)
	_ = s.Transition(EvPause)
	if s.State() != StatePaused {
		t.Fatalf("setup: state = %s", s.State())
	}
	// playMedia from Paused is also a preempt — new content supersedes old.
	if err := s.Transition(EvPlayMedia); err != nil {
		t.Errorf("preempt from paused failed: %v", err)
	}
	if s.State() != StatePlaying {
		t.Errorf("state after preempt-from-paused = %s, want Playing", s.State())
	}
}

func TestState_PauseRejectedFromIdle(t *testing.T) {
	s := New()
	if err := s.Transition(EvPause); err == nil {
		t.Error("pause from idle should fail")
	}
	if s.State() != StateIdle {
		t.Errorf("state changed after failed pause: %s", s.State())
	}
}

func TestState_PauseRejectedFromPaused(t *testing.T) {
	s := New()
	_ = s.Transition(EvPlayMedia)
	_ = s.Transition(EvPause)
	if err := s.Transition(EvPause); err == nil {
		t.Error("pause from paused should fail")
	}
}

func TestState_PlayRejectedFromIdle(t *testing.T) {
	s := New()
	if err := s.Transition(EvPlay); err == nil {
		t.Error("play from idle should fail")
	}
}

func TestState_PlayRejectedFromPlaying(t *testing.T) {
	s := New()
	_ = s.Transition(EvPlayMedia)
	if err := s.Transition(EvPlay); err == nil {
		t.Error("play from playing should fail")
	}
}

func TestState_SeekRejectedFromIdle(t *testing.T) {
	s := New()
	if err := s.Transition(EvSeek); err == nil {
		t.Error("seek from idle should fail")
	}
}

func TestState_SeekFromPlayingKeepsState(t *testing.T) {
	s := New()
	_ = s.Transition(EvPlayMedia)
	if err := s.Transition(EvSeek); err != nil {
		t.Fatal(err)
	}
	if s.State() != StatePlaying {
		t.Errorf("seek changed state: %s", s.State())
	}
}

func TestState_SeekFromPausedKeepsState(t *testing.T) {
	s := New()
	_ = s.Transition(EvPlayMedia)
	_ = s.Transition(EvPause)
	if err := s.Transition(EvSeek); err != nil {
		t.Fatal(err)
	}
	if s.State() != StatePaused {
		t.Errorf("seek changed state: %s", s.State())
	}
}

func TestState_StopFromAnyState(t *testing.T) {
	// From Idle
	s := New()
	if err := s.Transition(EvStop); err != nil {
		t.Error(err)
	}
	if s.State() != StateIdle {
		t.Errorf("stop from idle => %s", s.State())
	}

	// From Playing
	s = New()
	_ = s.Transition(EvPlayMedia)
	if err := s.Transition(EvStop); err != nil {
		t.Error(err)
	}
	if s.State() != StateIdle {
		t.Errorf("stop from playing => %s", s.State())
	}

	// From Paused
	s = New()
	_ = s.Transition(EvPlayMedia)
	_ = s.Transition(EvPause)
	if err := s.Transition(EvStop); err != nil {
		t.Error(err)
	}
	if s.State() != StateIdle {
		t.Errorf("stop from paused => %s", s.State())
	}
}

func TestState_UnknownEvent(t *testing.T) {
	s := New()
	if err := s.Transition(Event("bogus")); err == nil {
		t.Error("unknown event should fail")
	}
}

func TestStateMachine_EvError_FromPlaying_GoesIdle(t *testing.T) {
	sm := New()
	if err := sm.Transition(EvPlayMedia); err != nil {
		t.Fatalf("EvPlayMedia from Idle: %v", err)
	}
	if err := sm.Transition(EvError); err != nil {
		t.Fatalf("EvError from Playing: %v", err)
	}
	if got := sm.State(); got != StateIdle {
		t.Errorf("state after EvError = %q, want %q", got, StateIdle)
	}
}

func TestStateMachine_EvError_FromPaused_GoesIdle(t *testing.T) {
	sm := New()
	_ = sm.Transition(EvPlayMedia)
	_ = sm.Transition(EvPause)
	if err := sm.Transition(EvError); err != nil {
		t.Fatalf("EvError from Paused: %v", err)
	}
	if got := sm.State(); got != StateIdle {
		t.Errorf("state after EvError = %q, want %q", got, StateIdle)
	}
}

func TestStateMachine_EvError_FromIdle_NoOp(t *testing.T) {
	sm := New()
	// Already StateIdle; EvError must accept it (the plane-exit goroutine
	// can race other transitions, so EvError must be idempotent on Idle).
	if err := sm.Transition(EvError); err != nil {
		t.Fatalf("EvError from Idle should be accepted: %v", err)
	}
	if got := sm.State(); got != StateIdle {
		t.Errorf("state after EvError-from-Idle = %q, want %q", got, StateIdle)
	}
}
