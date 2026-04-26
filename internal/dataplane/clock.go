// Package dataplane owns the field pump (NTSC 59.94 Hz, PAL 50 Hz): timer, pipe readers, and
// the Plane orchestrator that stitches FFmpeg output to the Groovy sender.
//
// The clock is a push-driven free-run at the source cadence (no pull from
// the MiSTer — see docs/references/groovy_mister.md §"Clock Discipline").
// Ticks are dropped on a full consumer channel because the data plane has
// hard deadlines: stalling the timer produces frame-pacing drift that is
// worse than the occasional dropped tick.
package dataplane

import (
	"context"
	"time"
)

// RunFieldTimer emits one tick per 1/fieldsPerSec seconds onto out until ctx
// is cancelled. If the consumer is behind (out channel full) the tick is
// dropped rather than blocking the timer loop.
//
// Intended to run as a goroutine; exits on ctx.Done().
func RunFieldTimer(ctx context.Context, fieldsPerSec float64, out chan<- time.Time) {
	period := time.Duration(float64(time.Second) / fieldsPerSec)
	tick := time.NewTicker(period)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-tick.C:
			select {
			case out <- t:
			default:
				// Drop if consumer behind — deadline pressure is real.
			}
		}
	}
}
