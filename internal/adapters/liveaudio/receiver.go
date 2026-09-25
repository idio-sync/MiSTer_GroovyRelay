package liveaudio

import (
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// ReceiverConfig wires a Receiver.
type ReceiverConfig struct {
	Source     string // core Source / AdapterRef prefix: "spotify", "airplay"
	Label      string // headline when the track has no title: "SPOTIFY"
	HelperName string // for logs and status: "librespot"
	Core       Core
	Relay      *Relay
	HTTPPort   int
	DataDir    string
}

// Receiver is the lifecycle a live audio adapter shares: one Session and
// one supervised helper, started and stopped together. The helper's exit
// ends the session (the phone's connection went with it), and a session
// the CRT ends on its own restarts the helper so the phone sees the
// disconnect. Adapters add protocol specifics: config, the helper command
// line, and turning helper events into Handle calls.
type Receiver struct {
	cfg ReceiverConfig

	mu       sync.Mutex
	session  *Session
	sup      *Supervisor
	running  bool
	startErr string
	since    time.Time
}

func NewReceiver(cfg ReceiverConfig) *Receiver {
	return &Receiver{cfg: cfg, since: time.Now()}
}

// Start brings the session and the helper up; a no-op when running.
// A failure (for example a missing helper binary) is kept for Status.
func (r *Receiver) Start(spec HelperSpec, opts Options) error {
	r.mu.Lock()
	running := r.running
	r.mu.Unlock()
	if running {
		return nil
	}
	var sup *Supervisor
	session := NewSession(SessionConfig{
		Source:         r.cfg.Source,
		Label:          r.cfg.Label,
		Core:           r.cfg.Core,
		Relay:          r.cfg.Relay,
		HTTPPort:       r.cfg.HTTPPort,
		DataDir:        r.cfg.DataDir,
		OnExternalStop: func(string) { sup.Restart() },
	}, opts)
	sup = NewSupervisor(SupervisorConfig{
		Name:   r.cfg.HelperName,
		Stdout: r.cfg.Relay,
		OnExit: func(error, bool) { session.Handle(Event{Kind: EventStop}) },
	})
	if err := sup.Start(spec); err != nil {
		session.Close()
		r.SetStartError(err)
		return err
	}
	r.mu.Lock()
	r.session, r.sup = session, sup
	r.running = true
	r.startErr = ""
	r.since = time.Now()
	r.mu.Unlock()
	return nil
}

// Stop tears the helper and session down and clears any start error.
func (r *Receiver) Stop() {
	r.mu.Lock()
	session, sup := r.session, r.sup
	r.session, r.sup = nil, nil
	wasRunning := r.running
	r.running = false
	r.startErr = ""
	if wasRunning {
		r.since = time.Now()
	}
	r.mu.Unlock()
	if sup != nil {
		sup.Stop()
	}
	if session != nil {
		session.Close()
	}
	r.cfg.Relay.Reset()
}

// Running reports whether the helper is supervised.
func (r *Receiver) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Handle forwards a helper event to the session; dropped when stopped.
func (r *Receiver) Handle(ev Event) {
	r.mu.Lock()
	session := r.session
	r.mu.Unlock()
	if session != nil {
		session.Handle(ev)
	}
}

// SetOptions updates the running session's options.
func (r *Receiver) SetOptions(opts Options) {
	r.mu.Lock()
	session := r.session
	r.mu.Unlock()
	if session != nil {
		session.SetOptions(opts)
	}
}

// SetStartError records a failure to start that happened before Start
// (for example an unsupported platform), for Status.
func (r *Receiver) SetStartError(err error) {
	r.mu.Lock()
	r.startErr = err.Error()
	r.since = time.Now()
	r.mu.Unlock()
}

// Status folds start errors, helper health, and session start failures
// into an adapter status.
func (r *Receiver) Status() adapters.Status {
	r.mu.Lock()
	running, startErr, since := r.running, r.startErr, r.since
	session, sup := r.session, r.sup
	r.mu.Unlock()
	switch {
	case startErr != "":
		return adapters.Status{State: adapters.StateError, LastError: startErr, Since: since}
	case !running:
		return adapters.Status{State: adapters.StateStopped, Since: since}
	}
	if h := sup.Health(); h.Err != "" {
		return adapters.Status{State: adapters.StateError, LastError: r.cfg.HelperName + ": " + h.Err, Since: since}
	}
	if msg := session.LastError(); msg != "" {
		return adapters.Status{State: adapters.StateError, LastError: msg, Since: since}
	}
	return adapters.Status{State: adapters.StateRunning, Since: since}
}
