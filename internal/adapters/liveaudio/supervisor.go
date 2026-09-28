package liveaudio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// HelperSpec is one helper process invocation.
type HelperSpec struct {
	Path string   // binary; resolved with exec.LookPath
	Args []string // excluding argv[0]
	Env  []string // appended to the bridge's environment
}

// SupervisorConfig wires a Supervisor.
type SupervisorConfig struct {
	Name   string    // helper name for logs, e.g. "librespot"
	Stdout io.Writer // the helper's PCM, normally a *Relay
	// OnExit, when set, is called after every helper exit with the exit
	// error and whether the exit was requested (Restart/Stop). It runs on
	// the supervisor goroutine and must not call back into the Supervisor.
	OnExit func(err error, requested bool)

	// Zero values take the production defaults.
	BaseBackoff  time.Duration // first retry delay (1 s)
	MaxBackoff   time.Duration // retry delay cap (30 s)
	StopGrace    time.Duration // SIGTERM → kill (3 s)
	FailWindow   time.Duration // failures counted inside this window (60 s)
	FailureLimit int           // failures inside FailWindow before Health reports an error (3)
}

// Health is a supervisor's view of its helper.
type Health struct {
	Running bool   // a helper process is up (or being started)
	Err     string // set once the helper keeps failing; cleared by a healthy run
}

// Supervisor runs one helper process, restarting it with backoff when it
// exits, until Stop.
type Supervisor struct {
	cfg SupervisorConfig

	mu       sync.Mutex
	cmd      *exec.Cmd
	running  bool
	failures []time.Time
	lastErr  string
	stderr   *lastLineWriter
	restart  bool // the current exit was requested by Restart
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	if cfg.StopGrace <= 0 {
		cfg.StopGrace = 3 * time.Second
	}
	if cfg.FailWindow <= 0 {
		cfg.FailWindow = time.Minute
	}
	if cfg.FailureLimit <= 0 {
		cfg.FailureLimit = 3
	}
	if cfg.Stdout == nil {
		cfg.Stdout = io.Discard
	}
	return &Supervisor{cfg: cfg}
}

// Start resolves the helper binary and begins supervising it. A missing
// binary is reported here rather than retried. Start on a running
// supervisor is an error; Stop first.
func (s *Supervisor) Start(spec HelperSpec) error {
	path, err := exec.LookPath(spec.Path)
	if err != nil {
		return fmt.Errorf("%s not found (set binary_path or install it on PATH): %w", s.cfg.Name, err)
	}
	spec.Path = path
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done != nil {
		return fmt.Errorf("%s supervisor already started", s.cfg.Name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	s.failures = nil
	s.lastErr = ""
	go s.loop(ctx, spec, s.done)
	return nil
}

// Restart ends the current helper process; the supervisor respawns it at
// once, without backoff. Used to drop the phone's connection cleanly.
func (s *Supervisor) Restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.restart = true
	// While the helper is still starting there is no process yet; runOnce
	// ends it as soon as Start returns.
	if s.cmd != nil && s.cmd.Process != nil {
		_ = terminate(s.cmd.Process)
	}
}

// Stop terminates the helper and waits for supervision to end.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// Health reports whether the helper is up and whether it keeps failing.
func (s *Supervisor) Health() Health {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Health{Running: s.running, Err: s.lastErr}
}

func (s *Supervisor) loop(ctx context.Context, spec HelperSpec, done chan struct{}) {
	defer close(done)
	backoff := s.cfg.BaseBackoff
	for {
		started := time.Now()
		err := s.runOnce(ctx, spec)
		ranFor := time.Since(started)

		s.mu.Lock()
		requested := s.restart || ctx.Err() != nil
		s.restart = false
		s.mu.Unlock()
		if s.cfg.OnExit != nil {
			s.cfg.OnExit(err, requested)
		}
		if ctx.Err() != nil {
			return
		}
		if requested {
			backoff = s.cfg.BaseBackoff
			continue
		}

		slog.Warn("live audio helper exited", "helper", s.cfg.Name, "err", err, "ran_for", ranFor.Round(time.Millisecond))
		if ranFor >= s.cfg.FailWindow {
			// A long healthy run: start the failure accounting over.
			backoff = s.cfg.BaseBackoff
			s.mu.Lock()
			s.failures = nil
			s.lastErr = ""
			s.mu.Unlock()
		}
		s.recordFailure(err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, s.cfg.MaxBackoff)
	}
}

func (s *Supervisor) runOnce(ctx context.Context, spec HelperSpec) error {
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Stdout = s.cfg.Stdout
	stderr := &lastLineWriter{name: s.cfg.Name}
	cmd.Stderr = stderr
	cmd.Cancel = func() error { return terminate(cmd.Process) }
	cmd.WaitDelay = s.cfg.StopGrace
	// Running before Start: exec begins copying the helper's stdout inside
	// Start, so its first PCM can arrive before Start returns.
	s.mu.Lock()
	s.stderr = stderr
	s.running = true
	s.mu.Unlock()
	if err := cmd.Start(); err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return fmt.Errorf("start %s: %w", s.cfg.Name, err)
	}
	s.mu.Lock()
	s.cmd = cmd
	if s.restart { // Restart arrived while the helper was starting
		_ = terminate(cmd.Process)
	}
	s.mu.Unlock()

	err := cmd.Wait()

	s.mu.Lock()
	s.cmd = nil
	s.running = false
	s.mu.Unlock()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil // exited; only the stdout copy was cut short
	}
	if err == nil {
		err = fmt.Errorf("%s exited", s.cfg.Name)
	}
	if line := stderr.Last(); line != "" {
		err = fmt.Errorf("%w: %s", err, line)
	}
	return err
}

// recordFailure notes an unrequested exit and, once FailureLimit exits
// land inside FailWindow, exposes it through Health.
func (s *Supervisor) recordFailure(err error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.failures[:0]
	for _, t := range s.failures {
		if now.Sub(t) < s.cfg.FailWindow {
			kept = append(kept, t)
		}
	}
	s.failures = append(kept, now)
	if len(s.failures) >= s.cfg.FailureLimit {
		msg := err.Error()
		if s.stderr != nil {
			if line := s.stderr.Last(); line != "" {
				msg = line
			}
		}
		s.lastErr = msg
	}
}

// lastLineWriter logs a helper's stderr line by line and remembers the
// last non-empty line for error reporting.
type lastLineWriter struct {
	name string
	mu   sync.Mutex
	buf  []byte
	last string
}

func (w *lastLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if line == "" {
			continue
		}
		w.last = line
		slog.Debug("live audio helper", "helper", w.name, "line", line)
	}
	if len(w.buf) > 4096 { // a runaway line without a newline
		w.last = strings.TrimSpace(string(w.buf))
		w.buf = w.buf[:0]
	}
	return len(p), nil
}

func (w *lastLineWriter) Last() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if tail := strings.TrimSpace(string(w.buf)); tail != "" {
		return tail
	}
	return w.last
}
