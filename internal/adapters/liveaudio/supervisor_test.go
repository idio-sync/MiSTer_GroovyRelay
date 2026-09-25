package liveaudio

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeHelperEnv = "LIVEAUDIO_FAKE_HELPER"

// TestMain doubles as the fake helper: when re-executed with
// LIVEAUDIO_FAKE_HELPER set, the test binary behaves like a helper.
func TestMain(m *testing.M) {
	switch os.Getenv(fakeHelperEnv) {
	case "":
		os.Exit(m.Run())
	case "pcm":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte{0x7f}, 4096))
		idleForever()
	case "crash":
		fmt.Fprintln(os.Stderr, "starting")
		fmt.Fprintln(os.Stderr, "boom: bad credentials")
		os.Exit(3)
	case "sleep":
		idleForever()
	}
}

// idleForever blocks a fake helper until it is killed. Not `select {}`:
// with no other goroutines the runtime declares that a deadlock and exits
// the helper moments after it starts.
func idleForever() {
	for {
		time.Sleep(time.Hour)
	}
}

func fakeHelper(t *testing.T, mode string) HelperSpec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return HelperSpec{Path: exe, Env: []string{fakeHelperEnv + "=" + mode}}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSupervisorStreamsStdoutAndStops(t *testing.T) {
	out := &syncBuffer{}
	s := NewSupervisor(SupervisorConfig{Name: "fake", Stdout: out, StopGrace: 200 * time.Millisecond})
	if err := s.Start(fakeHelper(t, "pcm")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "helper PCM", func() bool { return out.Len() == 4096 })
	if !s.Health().Running {
		t.Fatal("Health().Running = false while the helper streams")
	}
	stopped := make(chan struct{})
	go func() {
		s.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not end the helper")
	}
	if s.Health().Running {
		t.Fatal("Health().Running = true after Stop")
	}
	s.Stop() // idempotent
}

func TestSupervisorReportsRepeatedFailures(t *testing.T) {
	var mu sync.Mutex
	var exits []bool
	s := NewSupervisor(SupervisorConfig{
		Name:        "fake",
		BaseBackoff: time.Millisecond,
		MaxBackoff:  5 * time.Millisecond,
		OnExit: func(err error, requested bool) {
			mu.Lock()
			exits = append(exits, requested)
			mu.Unlock()
		},
	})
	if err := s.Start(fakeHelper(t, "crash")); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	waitFor(t, "failure report", func() bool { return s.Health().Err != "" })
	if got := s.Health().Err; got != "boom: bad credentials" {
		t.Fatalf("Health().Err = %q, want the helper's last stderr line", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(exits) < 3 {
		t.Fatalf("OnExit calls = %d, want >= 3 before an error is reported", len(exits))
	}
	for i, requested := range exits {
		if requested {
			t.Fatalf("exit %d reported as requested", i)
		}
	}
}

func TestSupervisorRestartRespawnsWithoutBackoff(t *testing.T) {
	requestedExits := make(chan bool, 4)
	s := NewSupervisor(SupervisorConfig{
		Name:        "fake",
		BaseBackoff: time.Hour, // any backoff would hang the test
		OnExit:      func(err error, requested bool) { requestedExits <- requested },
	})
	if err := s.Start(fakeHelper(t, "sleep")); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	waitFor(t, "helper start", func() bool { return s.Health().Running })
	s.Restart()
	select {
	case requested := <-requestedExits:
		if !requested {
			t.Fatal("Restart exit reported as unrequested")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Restart did not end the helper")
	}
	waitFor(t, "helper respawn", func() bool { return s.Health().Running })
	if err := s.Health().Err; err != "" {
		t.Fatalf("Restart counted as a failure: %q", err)
	}
}

func TestSupervisorStartMissingBinary(t *testing.T) {
	s := NewSupervisor(SupervisorConfig{Name: "librespot"})
	err := s.Start(HelperSpec{Path: "definitely-not-a-live-audio-helper"})
	if err == nil || !strings.Contains(err.Error(), "librespot not found") {
		t.Fatalf("Start error = %v, want a not-found error naming the helper", err)
	}
	s.Stop() // no-op
}

func TestSupervisorStartTwiceFails(t *testing.T) {
	s := NewSupervisor(SupervisorConfig{Name: "fake"})
	if err := s.Start(fakeHelper(t, "sleep")); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if err := s.Start(fakeHelper(t, "sleep")); err == nil {
		t.Fatal("second Start succeeded")
	}
}

func TestLastLineWriter(t *testing.T) {
	w := &lastLineWriter{name: "fake"}
	_, _ = w.Write([]byte("first\n\n  second  \npart"))
	if got := w.Last(); got != "part" {
		t.Fatalf("Last() = %q, want the unterminated tail", got)
	}
	_, _ = w.Write([]byte("ial\n"))
	if got := w.Last(); got != "partial" {
		t.Fatalf("Last() = %q, want partial", got)
	}
}
