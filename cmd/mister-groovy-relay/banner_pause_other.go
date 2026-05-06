//go:build !windows

package main

// waitForEnterOnWindows is a no-op on non-Windows platforms.
func waitForEnterOnWindows() {}
