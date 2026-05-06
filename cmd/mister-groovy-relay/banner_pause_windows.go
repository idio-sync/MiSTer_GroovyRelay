//go:build windows

package main

import (
	"bufio"
	"fmt"
	"os"

	"golang.org/x/term"
)

// waitForEnterOnWindows blocks until the user presses Enter. Used on
// fatal-error paths so the console window stays visible long enough to
// read the message when the user double-clicked the binary. No-op when:
//   - MISTER_GROOVY_NO_PAUSE=1 (operator opt-out).
//   - stdin is not a terminal (Docker, CI, headless service, redirected
//     stdin — would deadlock or read garbage otherwise).
//   - stdout is not a terminal (the message went to a file the user is
//     reading separately; no console to keep open).
func waitForEnterOnWindows() {
	if os.Getenv("MISTER_GROOVY_NO_PAUSE") == "1" {
		return
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}
	fmt.Fprintln(os.Stderr, "  Press Enter to close this window.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
