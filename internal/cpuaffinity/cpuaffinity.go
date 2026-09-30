// Package cpuaffinity detects CPU pinning that starves the relay of cores.
//
// Linux never load-balances threads across CPUs listed in isolcpus (they sit
// outside every scheduler domain). A container pinned only to isolated CPUs
// therefore runs every relay and ffmpeg thread on whichever single CPU each
// thread started on, however many CPUs the pinning allows. On Unraid, where
// users isolate cores for VMs and pin containers from the same screen, that
// showed up as the MiSTer's lower screen half flashing: field sends paused
// behind ffmpeg on one shared core while the others sat idle.
//
// Check reports that situation (and plain single-core pinning) as a
// human-readable warning so the operator can fix the pinning. It changes
// nothing about scheduling.
package cpuaffinity

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ParseCPUList parses the kernel's CPU list format ("0,2-3,6-15"), as found
// in /sys/devices/system/cpu/isolated, into a sorted, de-duplicated slice.
// Blank input yields nil.
func ParseCPUList(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	seen := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		first, err := strconv.Atoi(lo)
		if err != nil || first < 0 {
			return nil, fmt.Errorf("cpu list %q: bad entry %q", s, part)
		}
		last := first
		if isRange {
			if last, err = strconv.Atoi(hi); err != nil || last < first {
				return nil, fmt.Errorf("cpu list %q: bad range %q", s, part)
			}
		}
		for c := first; c <= last; c++ {
			seen[c] = true
		}
	}
	out := make([]int, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Ints(out)
	return out, nil
}

// Evaluate returns a warning when the relay's allowed CPUs cannot run its
// threads in parallel, or "" when they can:
//
//   - pinned to a single CPU on a multi-CPU host;
//   - pinned to two or more CPUs that are all isolated, so the kernel keeps
//     every thread on the CPU it started on.
//
// allowed is the process affinity mask, isolated the kernel's isolcpus set
// and online the host's online CPU count. An empty allowed set means the
// mask is unknown and never warns.
func Evaluate(allowed, isolated []int, online int) string {
	const fix = "Remove the container's CPU pinning, or pin it to CPUs that are not isolated."
	switch {
	case len(allowed) == 0:
		return ""
	case len(allowed) == 1:
		if online <= 1 {
			return ""
		}
		return fmt.Sprintf("CPU pinning limits the relay to CPU %d, so ffmpeg and field sending share one core "+
			"and video can tear or flash on the CRT. %s", allowed[0], fix)
	}
	iso := make(map[int]bool, len(isolated))
	for _, c := range isolated {
		iso[c] = true
	}
	for _, c := range allowed {
		if !iso[c] {
			return ""
		}
	}
	return fmt.Sprintf("CPU pinning allows CPUs %s, but all of them are isolated (isolcpus), so the kernel "+
		"never spreads the relay's threads across them: ffmpeg and field sending share one core "+
		"and video can tear or flash on the CRT. %s", formatCPUs(allowed), fix)
}

const (
	isolatedPath = "/sys/devices/system/cpu/isolated"
	onlinePath   = "/sys/devices/system/cpu/online"
)

// checkSysfs evaluates allowed against the kernel's isolated and online CPU
// lists, read through readFile. An unreadable or malformed isolated list
// counts as "nothing isolated"; an unreadable online list leaves the host
// size unknown, which suppresses the single-CPU warning.
func checkSysfs(readFile func(string) ([]byte, error), allowed []int) string {
	var isolated []int
	if b, err := readFile(isolatedPath); err == nil {
		isolated, _ = ParseCPUList(string(b))
	}
	online := 0
	if b, err := readFile(onlinePath); err == nil {
		if cpus, err := ParseCPUList(string(b)); err == nil {
			online = len(cpus)
		}
	}
	return Evaluate(allowed, isolated, online)
}

func formatCPUs(cpus []int) string {
	parts := make([]string, len(cpus))
	for i, c := range cpus {
		parts[i] = strconv.Itoa(c)
	}
	return strings.Join(parts, ",")
}
