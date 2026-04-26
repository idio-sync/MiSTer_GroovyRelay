package misterctl

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"golang.org/x/crypto/ssh"
)

// sshPort is the port the MiSTer's sshd listens on. Hard-coded —
// MiSTers ship with stock sshd config and we don't expose an
// override field. If a custom MiSTer setup ever needs a different
// port, it's a spec change, not a runtime knob.
const sshPort = 22

// realDialAndRun is the production implementation of dialAndRun.
// Wired in init() so tests retain the ability to swap dialAndRun
// without overwriting this variable.
func realDialAndRun(ctx context.Context, p Params) error {
	cfg := &ssh.ClientConfig{
		User:            p.User,
		Auth:            []ssh.AuthMethod{ssh.Password(p.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // intentional; see package doc
		Timeout:         p.Timeout,
	}

	addr := net.JoinHostPort(p.Host, strconv.Itoa(sshPort))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	// Run the launchCommand on a goroutine so we can honor ctx
	// cancellation. session.Run does not take a context; closing the
	// client interrupts an in-flight Run.
	done := make(chan error, 1)
	go func() { done <- session.Run(launchCommand) }()

	select {
	case <-ctx.Done():
		// Force the session to close so the goroutine doesn't leak.
		_ = client.Close()
		<-done // drain
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("exec: %w", err)
		}
		return nil
	}
}

func init() {
	dialAndRun = realDialAndRun
}
