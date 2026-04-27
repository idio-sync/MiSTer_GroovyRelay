package jellyfin

import (
	"sync"
	"time"
)

// LinkPhase is the link state machine's current phase. It is distinct
// from adapters.State (which is the adapter's runtime lifecycle); a
// JF adapter can be StateStopped & LinkLinked simultaneously.
type LinkPhase int

const (
	LinkIdle    LinkPhase = iota // never linked or unlinked
	LinkLinking                  // AuthenticateByName in flight
	LinkLinked                   // token persisted, ready
	LinkError                    // last link attempt failed
)

func (p LinkPhase) String() string {
	switch p {
	case LinkIdle:
		return "idle"
	case LinkLinking:
		return "linking"
	case LinkLinked:
		return "linked"
	case LinkError:
		return "error"
	default:
		return "unknown"
	}
}

// LinkState is the adapter-internal link FSM. All accessors are
// thread-safe. The struct is held inside Adapter and its mutex is
// orthogonal to Adapter.mu — link state can be inspected without
// taking the (busier) adapter mutex.
type LinkState struct {
	mu        sync.Mutex
	phase     LinkPhase
	user      string
	serverID  string
	lastErr   string
	updatedAt time.Time
}

func NewLinkState() *LinkState {
	return &LinkState{phase: LinkIdle, updatedAt: time.Now()}
}

func (s *LinkState) State() LinkPhase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}

func (s *LinkState) LinkedAs() (user, serverID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.user, s.serverID
}

func (s *LinkState) LastError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *LinkState) SetIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = LinkIdle
	s.user = ""
	s.serverID = ""
	s.lastErr = ""
	s.updatedAt = time.Now()
}

func (s *LinkState) SetLinking() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = LinkLinking
	s.lastErr = ""
	s.updatedAt = time.Now()
}

func (s *LinkState) SetLinked(user, serverID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = LinkLinked
	s.user = user
	s.serverID = serverID
	s.lastErr = ""
	s.updatedAt = time.Now()
}

func (s *LinkState) SetError(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = LinkError
	s.lastErr = msg
	s.updatedAt = time.Now()
}

// UpdatedAt returns when the state last changed. Used by the UI's
// status fragment to render "linking for 3s..." style hints.
func (s *LinkState) UpdatedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updatedAt
}
