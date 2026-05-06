//go:build !windows

package logging

// enableWindowsVT is a no-op on non-Windows platforms. The Windows
// implementation lives in logging_vt_windows.go.
func enableWindowsVT() {}
