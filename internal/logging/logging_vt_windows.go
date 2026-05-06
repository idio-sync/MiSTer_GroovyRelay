//go:build windows

package logging

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableWindowsVT enables ENABLE_VIRTUAL_TERMINAL_PROCESSING on stdout
// so ANSI escapes render in legacy cmd.exe spawned by double-clicking
// the binary. Modern terminals (Windows Terminal, recent PowerShell)
// inherit it; legacy hosts do not until the process opts in.
//
// Failures are intentionally swallowed: if SetConsoleMode rejects the
// flag (very old Windows, redirected handle, non-console writer), the
// worst outcome is the user sees raw escape bytes — recoverable with
// NO_COLOR=1 or MISTER_GROOVY_LOG_FORMAT=text-plain.
func enableWindowsVT() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return
	}
	_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}
