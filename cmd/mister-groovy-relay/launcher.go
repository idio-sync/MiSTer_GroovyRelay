package main

import (
	"context"
	"errors"
	"time"

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
		return errors.New("MiSTer host not configured (set bridge.mister.host)")
	}
	return misterctl.LaunchGroovy(ctx, misterctl.Params{
		Host:     cur.MiSTer.Host,
		User:     cur.MiSTer.SSHUser,
		Password: cur.MiSTer.SSHPassword,
		Timeout:  b.timeout,
	})
}
