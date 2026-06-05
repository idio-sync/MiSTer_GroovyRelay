// Note: these tests swap misterctl.dialAndRun via SwapDialForTesting
// and MUST NOT call t.Parallel().

package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/launchcore"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/misterctl"
)

// fakeBridgeSaver is the minimal BridgeSaver used by the closure-seam
// test. Implements only Current(); Save() is unused here.
type fakeBridgeSaver struct {
	cur config.BridgeConfig
}

func (f *fakeBridgeSaver) Current() config.BridgeConfig { return f.cur }
func (f *fakeBridgeSaver) Save(_ config.BridgeConfig) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

func TestBridgeMisterLauncher_PassesParams(t *testing.T) {
	var got misterctl.Params
	prev := misterctl.SwapDialForTesting(func(_ context.Context, p misterctl.Params) error {
		got = p
		return nil
	})
	t.Cleanup(func() { misterctl.SwapDialForTesting(prev) })

	saver := &fakeBridgeSaver{cur: config.BridgeConfig{
		MiSTer: config.MisterConfig{
			Host: "192.168.1.42", Port: 32100, SourcePort: 32101,
			SSHUser: "alice", SSHPassword: "hunter2",
		},
	}}
	launcher := bridgeMisterLauncher{bridge: saver, timeout: 5 * time.Second}

	if err := launcher.Launch(context.Background()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	want := misterctl.Params{
		Host:     "192.168.1.42",
		User:     "alice",
		Password: "hunter2",
		Timeout:  5 * time.Second,
	}
	if got != want {
		t.Errorf("misterctl.LaunchGroovy got %+v, want %+v", got, want)
	}
}

func TestBridgeMisterLauncher_EmptyHostShortCircuits(t *testing.T) {
	dialed := false
	prev := misterctl.SwapDialForTesting(func(_ context.Context, _ misterctl.Params) error {
		dialed = true
		return nil
	})
	t.Cleanup(func() { misterctl.SwapDialForTesting(prev) })

	saver := &fakeBridgeSaver{cur: config.BridgeConfig{
		MiSTer: config.MisterConfig{Host: ""}, // empty
	}}
	launcher := bridgeMisterLauncher{bridge: saver, timeout: 5 * time.Second}

	err := launcher.Launch(context.Background())
	if err == nil {
		t.Fatal("expected empty-host error, got nil")
	}
	if !strings.Contains(err.Error(), "MiSTer host not configured") {
		t.Errorf("err = %q, want 'MiSTer host not configured'", err)
	}
	if dialed {
		t.Error("dialAndRun called on empty-host short-circuit")
	}
}

func TestBridgeMisterProber_SendsStatusProbe(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer conn.Close()

	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 32)
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		got <- append([]byte(nil), buf[:n]...)
		ack := make([]byte, groovy.ACKPacketSize)
		ack[12] = 1
		_, _ = conn.WriteToUDP(ack, addr)
	}()

	port := conn.LocalAddr().(*net.UDPAddr).Port
	saver := &fakeBridgeSaver{cur: config.BridgeConfig{
		MiSTer: config.MisterConfig{Host: "127.0.0.1", Port: port},
	}}
	prober := bridgeMisterProber{bridge: saver, timeout: time.Second}

	if err := prober.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	select {
	case pkt := <-got:
		if len(pkt) != 1 || pkt[0] != groovy.CmdGetStatus {
			t.Fatalf("probe packet = %v, want [%d]", pkt, groovy.CmdGetStatus)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive status probe")
	}
}

func TestBridgeMisterProber_EmptyHostShortCircuits(t *testing.T) {
	saver := &fakeBridgeSaver{cur: config.BridgeConfig{
		MiSTer: config.MisterConfig{Host: ""},
	}}
	prober := bridgeMisterProber{bridge: saver, timeout: time.Second}

	err := prober.Probe(context.Background())
	if err == nil {
		t.Fatal("expected empty-host error, got nil")
	}
	if err.Error() != launchcore.EmptyHostMessage {
		t.Errorf("err = %q, want %q", err.Error(), launchcore.EmptyHostMessage)
	}
}

func TestBridgeMisterLauncher_EmptyHostMessageStable(t *testing.T) {
	saver := &fakeBridgeSaver{cur: config.BridgeConfig{
		MiSTer: config.MisterConfig{Host: ""},
	}}
	launcher := bridgeMisterLauncher{bridge: saver, timeout: 5 * time.Second}
	err := launcher.Launch(context.Background())
	if err == nil {
		t.Fatalf("Launch err = nil, want non-nil for empty host")
	}
	if err.Error() != launchcore.EmptyHostMessage {
		t.Errorf("Launch err = %q, want %q", err.Error(), launchcore.EmptyHostMessage)
	}
}
