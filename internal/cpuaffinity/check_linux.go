//go:build linux

package cpuaffinity

import (
	"os"

	"golang.org/x/sys/unix"
)

// Check returns a warning when this process's CPU affinity keeps its threads
// from running in parallel (see Evaluate), or "" when it doesn't or the
// affinity can't be read.
func Check() string {
	var set unix.CPUSet
	if err := unix.SchedGetaffinity(0, &set); err != nil {
		return ""
	}
	// CPUSet words are 32 or 64 bits depending on the arch, so walk until
	// every set bit has been found rather than to a fixed width.
	var allowed []int
	for c, n := 0, set.Count(); len(allowed) < n; c++ {
		if set.IsSet(c) {
			allowed = append(allowed, c)
		}
	}
	return checkSysfs(os.ReadFile, allowed)
}
