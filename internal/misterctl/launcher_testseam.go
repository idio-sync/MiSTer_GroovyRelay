package misterctl

import "context"

// SwapDialForTesting replaces dialAndRun with fn and returns the
// previous value. Production code never calls this; it exists so
// callers in other packages (the cmd-package closure-seam test) can
// inject a capture without standing up a real ssh.Server.
//
// Callers MUST restore the previous value (typically via t.Cleanup).
// The package-level var is not goroutine-safe under swap, so tests
// using SwapDialForTesting must NOT call t.Parallel().
func SwapDialForTesting(fn func(context.Context, Params) error) func(context.Context, Params) error {
	prev := dialAndRun
	dialAndRun = fn
	return prev
}
