//go:build !linux

package cpuaffinity

// Check always returns "" off Linux: Windows and macOS balance threads across
// every CPU a process may use, so pinning cannot strand them on one core.
func Check() string { return "" }
