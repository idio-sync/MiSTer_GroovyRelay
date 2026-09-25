//go:build windows

package liveaudio

import "os"

// terminate kills the helper: Windows has no SIGTERM for child processes.
func terminate(p *os.Process) error {
	return p.Kill()
}
