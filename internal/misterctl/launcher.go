// Package misterctl runs ad-hoc remote commands against the MiSTer
// over SSH. v1 has exactly one operation: LaunchGroovy, which writes
// `load_core /media/fat/_Utility/Groovy_20240928.rbf` into the
// MiSTer's /dev/MiSTer_cmd FIFO so the FPGA loads the Groovy core.
// This is the only currently-supported way to put the MiSTer into
// the right core for the bridge to stream into.
//
// The package is package-pure: no globals beyond the dialAndRun
// injection seam, no goroutines beyond the in-flight SSH session,
// no logging. Logging is the caller's responsibility. The password
// is never logged here — by construction, no log call exists in the
// package.
//
// Host-key verification: ssh.InsecureIgnoreHostKey. The bridge is a
// LAN tool and the MiSTer regenerates host keys on reflash; pinning
// keys would surface as a confusing "Host key changed" error after
// every firmware update without adding meaningful protection on a
// trusted residential LAN. If this assumption breaks (mixed LAN,
// public-network deployment), revisit.
package misterctl

import (
	"context"
	"time"
)

// Params bundles the inputs LaunchGroovy needs. Construct fresh on
// each call; the struct holds no shared state.
type Params struct {
	Host     string        // bridge.mister.host, no port suffix
	User     string        // bridge.mister.ssh_user
	Password string        // bridge.mister.ssh_password
	Timeout  time.Duration // dial+exec total budget; 5s in main.go
}

// launchCommand is the literal shell command written to /dev/MiSTer_cmd.
// Hard-coded per the upstream Groovy_MiSTer install layout: cores live
// in /media/fat/_Utility and ship with their release date in the
// filename (Groovy_YYYYMMDD.rbf). 2024-09-28 is the latest upstream
// release as of this writing; upstream is unmaintained after the
// author's passing, so this filename is likely stable. If a future
// release renames the core, bump this string or graduate the path to
// a config field (see spec "Out-of-scope follow-ups").
const launchCommand = `echo "load_core /media/fat/_Utility/Groovy_20240928.rbf" > /dev/MiSTer_cmd`

// dialAndRun is the SSH dial + session.Run sequence; var so tests can
// inject a fake without standing up a real ssh.Server. Production
// value is realDialAndRun (assigned in launcher_ssh.go's init).
var dialAndRun func(ctx context.Context, p Params) error

// LaunchGroovy dials the MiSTer over SSH, runs the canonical
// load-core command, and returns nil on exec success or a wrapped
// error on auth/dial/exec failure. The password is never logged.
//
// LaunchGroovy itself does NOT validate Host == ""; empty-host
// short-circuiting belongs in the UI-layer caller (which has the
// BridgeSaver context to surface a meaningful "MiSTer host not
// configured" message). Keeping LaunchGroovy pure makes it easier
// to reuse from a future CLI flag or alternate caller without
// inheriting that policy.
func LaunchGroovy(ctx context.Context, p Params) error {
	return dialAndRun(ctx, p)
}
