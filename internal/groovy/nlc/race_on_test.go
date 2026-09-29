//go:build race

package nlc

// raceEnabled skips the zero-allocation assertion under the race detector,
// whose instrumentation may allocate on goroutine start.
const raceEnabled = true
