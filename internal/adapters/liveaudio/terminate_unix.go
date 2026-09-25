//go:build !windows

package liveaudio

import (
	"os"
	"syscall"
)

// terminate asks a helper to exit; exec's WaitDelay kills it if it lingers.
func terminate(p *os.Process) error {
	return p.Signal(syscall.SIGTERM)
}
