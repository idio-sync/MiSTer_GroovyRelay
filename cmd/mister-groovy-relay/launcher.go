package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/launchcore"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/misterctl"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

// bridgeMisterLauncher is the closure adapter wiring ui.MisterLauncher
// to misterctl.LaunchGroovy. Snapshots host/user/password from the
// live BridgeSaver at each call so credential edits apply hot — no
// bridge restart needed.
//
// Owns the empty-host short-circuit: returns "MiSTer host not
// configured" before dialing if BridgeSaver.Current().MiSTer.Host is
// empty. (LaunchGroovy itself is policy-free; UI-layer "config not
// set" semantics belong here.)
type bridgeMisterLauncher struct {
	bridge  ui.BridgeSaver
	timeout time.Duration
}

func (b bridgeMisterLauncher) Launch(ctx context.Context) error {
	cur := b.bridge.Current()
	if cur.MiSTer.Host == "" {
		return errors.New(launchcore.EmptyHostMessage)
	}
	return misterctl.LaunchGroovy(ctx, misterctl.Params{
		Host:     cur.MiSTer.Host,
		User:     cur.MiSTer.SSHUser,
		Password: cur.MiSTer.SSHPassword,
		Timeout:  b.timeout,
	})
}

// bridgeMisterProber wires ui.MisterProber to a side-effect-light UDP status
// request. CMD_GET_STATUS is the protocol's ACK-only reachability check; unlike
// INIT, it does not start or reset a streaming session on the MiSTer.
type bridgeMisterProber struct {
	bridge  ui.BridgeSaver
	timeout time.Duration
}

func (b bridgeMisterProber) Probe(ctx context.Context) error {
	cur := b.bridge.Current()
	if cur.MiSTer.Host == "" {
		return errors.New("MiSTer host not configured (set bridge.mister.host)")
	}

	timeout := b.timeout
	if timeout <= 0 {
		timeout = time.Second
	}

	sender, err := groovynet.NewSender(cur.MiSTer.Host, cur.MiSTer.Port, 0)
	if err != nil {
		return fmt.Errorf("open UDP probe socket: %w", err)
	}
	defer sender.Close()

	if err := sender.Send([]byte{groovy.CmdGetStatus}); err != nil {
		return fmt.Errorf("send status probe: %w", err)
	}

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := sender.Conn().SetReadDeadline(deadline); err != nil {
		return fmt.Errorf("set probe deadline: %w", err)
	}
	defer sender.Conn().SetReadDeadline(time.Time{})

	buf := make([]byte, groovy.ACKPacketSize*2)
	n, _, err := sender.Conn().ReadFromUDP(buf)
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return fmt.Errorf("status ack timeout after %s: %w", timeout, err)
		}
		return fmt.Errorf("read status ack: %w", err)
	}
	if n != groovy.ACKPacketSize {
		return fmt.Errorf("status ack wrong size: %d", n)
	}
	if _, err := groovy.ParseACK(buf[:n]); err != nil {
		return fmt.Errorf("parse status ack: %w", err)
	}
	return nil
}
