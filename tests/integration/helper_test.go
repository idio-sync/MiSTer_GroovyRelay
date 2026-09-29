//go:build integration

// Package integration wires the fake-mister listener and the groovynet
// sender together over real UDP loopback for end-to-end scenario tests.
// Every file in this package is guarded by the `integration` build tag so
// the default `go test ./...` run stays fast; exercise the scenarios with
// `go test -tags=integration ./tests/integration/...`.
package integration

import (
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// Harness bundles the loopback pair and the accumulating Recorder a test
// needs to assert against. It's constructed per-test via NewHarness and
// registers its own t.Cleanup to tear everything down deterministically.
type Harness struct {
	Listener *fakemister.Listener
	Sender   *groovynet.Sender
	Recorder *fakemister.Recorder
	Events   chan fakemister.Command
}

// NewHarness binds a fakemister.Listener on 127.0.0.1:<ephemeral>, creates a
// Recorder, fans events into it on a background goroutine, and connects a
// fresh Sender to the listener's address. t.Cleanup closes both ends and
// drains the events channel so the recorder goroutine exits cleanly.
func NewHarness(t *testing.T) *Harness {
	t.Helper()
	l, err := fakemister.NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan fakemister.Command, 256)
	rec := fakemister.NewRecorder()
	recDone := make(chan struct{})
	runDone := make(chan struct{})
	go func() {
		for c := range events {
			rec.Record(c)
		}
		close(recDone)
	}()
	go func() {
		l.Run(events)
		close(runDone)
	}()

	addr := l.Addr().(*net.UDPAddr)
	s, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		l.Close()
		// Wait for the Listener's Run loop to return before closing the
		// shared events channel — otherwise a late datagram could race into
		// a closed channel and panic. Then the recorder drains and exits.
		<-runDone
		close(events)
		<-recDone
	})
	return &Harness{Listener: l, Sender: s, Recorder: rec, Events: events}
}

// answerInitHandshake serves the bridge's pre-INIT handshake on conn: it
// answers GET_VERSION with 1 (the original core), then ACKs the first INIT
// with status, and returns. It returns false on a 2 s read timeout.
func answerInitHandshake(conn *net.UDPConn, status byte) bool {
	defer conn.SetReadDeadline(time.Time{})
	buf := make([]byte, 64)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return false
		}
		switch {
		case n == 1 && buf[0] == groovy.CmdGetVersion:
			_, _ = conn.WriteToUDP([]byte{1}, src)
		case n > 0 && buf[0] == groovy.CmdInit:
			ack := make([]byte, groovy.ACKPacketSize)
			ack[12] = status
			_, _ = conn.WriteToUDP(ack, src)
			return true
		}
	}
}
