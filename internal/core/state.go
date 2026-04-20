package core

import (
	"fmt"
	"sync"
)

// Session lifecycle states. Values are strings so they appear human-readable
// in SessionStatus JSON and log output.
const (
	StateIdle    State = "idle"
	StatePlaying State = "playing"
	StatePaused  State = "paused"
)

// Event is the set of inputs the state machine accepts.
type Event string

const (
	EvPlayMedia Event = "playMedia"
	EvPause     Event = "pause"
	EvPlay      Event = "play"
	EvStop      Event = "stop"
	EvSeek      Event = "seek"
	EvEOF       Event = "eof"
)

// StateMachine is the session FSM. Transitions are thread-safe. The manager
// holds one instance and drives it in lockstep with the data plane lifecycle.
//
// Transition table:
//
//	Idle    → Playing   on EvPlayMedia
//	Playing → Playing   on EvPlayMedia   (preempt)
//	Paused  → Playing   on EvPlayMedia   (preempt)
//	Playing → Paused    on EvPause
//	Paused  → Playing   on EvPlay
//	*       → Idle      on EvStop | EvEOF
//	Playing → Playing   on EvSeek        (plane is respawned; FSM unchanged)
//	Paused  → Paused    on EvSeek
//	Idle    → error     on EvSeek
type StateMachine struct {
	mu    sync.Mutex
	state State
}

// New returns a StateMachine initialized to StateIdle.
func New() *StateMachine { return &StateMachine{state: StateIdle} }

// State returns the current state under the internal lock.
func (s *StateMachine) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Transition applies an event. Returns an error for transitions the table
// rejects (EvPause from non-Playing, EvPlay from non-Paused, EvSeek from Idle).
func (s *StateMachine) Transition(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e {
	case EvPlayMedia:
		// Always allowed (preempt from any state).
		s.state = StatePlaying
	case EvPause:
		if s.state != StatePlaying {
			return fmt.Errorf("cannot pause from %s", s.state)
		}
		s.state = StatePaused
	case EvPlay:
		if s.state != StatePaused {
			return fmt.Errorf("cannot play from %s", s.state)
		}
		s.state = StatePlaying
	case EvStop, EvEOF:
		s.state = StateIdle
	case EvSeek:
		// Seek from playing or paused, stays in same state conceptually
		// (data plane is torn down and respawned — state doesn't change).
		if s.state == StateIdle {
			return fmt.Errorf("cannot seek from idle")
		}
	default:
		return fmt.Errorf("unknown event %q", e)
	}
	return nil
}
