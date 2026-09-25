package groovynet

import (
	"testing"
	"time"
)

// fakeClock models the pacer's view of time: every now() poll costs
// spinStep (a busy-wait iteration), and sleep(d) wakes d+overshoot later.
type fakeClock struct {
	t         time.Time
	spinStep  time.Duration
	overshoot time.Duration
	sleeps    []time.Duration
	polls     int
}

func (c *fakeClock) now() time.Time {
	c.polls++
	c.t = c.t.Add(c.spinStep)
	return c.t
}

func (c *fakeClock) sleep(d time.Duration) {
	c.sleeps = append(c.sleeps, d)
	c.t = c.t.Add(d + c.overshoot)
}

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestPacer(c *fakeClock) pacer {
	return pacer{
		interval: 20 * time.Microsecond,
		batch:    32,
		margin:   100 * time.Microsecond,
		now:      c.now,
		sleep:    c.sleep,
	}
}

var pacerT0 = time.Unix(1000, 0)

func TestPacer_NoWaitInsideABatch(t *testing.T) {
	c := &fakeClock{t: pacerT0, spinStep: time.Microsecond}
	p := newTestPacer(c)
	for sent := 1; sent < p.batch; sent++ {
		p.afterDatagram(pacerT0, sent, 1000)
	}
	if c.polls != 0 || len(c.sleeps) != 0 {
		t.Fatalf("mid-batch datagrams waited: polls=%d sleeps=%v", c.polls, c.sleeps)
	}
}

// At a batch boundary the pacer sleeps all but the margin of the wait, then
// spins to the cumulative deadline: same average rate as per-datagram
// pacing, a fraction of the CPU.
func TestPacer_BatchBoundarySleepsThenSpinsToDeadline(t *testing.T) {
	c := &fakeClock{t: pacerT0, spinStep: time.Microsecond}
	p := newTestPacer(c)
	c.advance(100 * time.Microsecond) // the burst's syscalls took 100 µs

	p.afterDatagram(pacerT0, 32, 1000)

	deadline := pacerT0.Add(32 * 20 * time.Microsecond)
	if len(c.sleeps) != 1 || c.sleeps[0] != 440*time.Microsecond-time.Microsecond {
		t.Fatalf("sleeps = %v, want one sleep of remaining-margin (~439µs)", c.sleeps)
	}
	if c.t.Before(deadline) {
		t.Fatalf("returned at %v before deadline %v", c.t.Sub(pacerT0), deadline.Sub(pacerT0))
	}
	if c.t.After(deadline.Add(2 * time.Microsecond)) {
		t.Fatalf("returned at %v, overshot deadline %v", c.t.Sub(pacerT0), deadline.Sub(pacerT0))
	}
}

func TestPacer_ShortWaitOnlySpins(t *testing.T) {
	c := &fakeClock{t: pacerT0, spinStep: time.Microsecond}
	p := newTestPacer(c)
	c.advance(600 * time.Microsecond) // 40 µs left: under the margin

	p.afterDatagram(pacerT0, 32, 1000)

	if len(c.sleeps) != 0 {
		t.Fatalf("slept %v for a wait under the margin", c.sleeps)
	}
	if c.t.Before(pacerT0.Add(640 * time.Microsecond)) {
		t.Fatalf("returned before the deadline")
	}
}

func TestPacer_PastDeadlineReturnsImmediately(t *testing.T) {
	c := &fakeClock{t: pacerT0, spinStep: time.Microsecond}
	p := newTestPacer(c)
	c.advance(900 * time.Microsecond)

	p.afterDatagram(pacerT0, 32, 1000)

	if c.polls != 1 || len(c.sleeps) != 0 {
		t.Fatalf("polls=%d sleeps=%v, want a single clock read", c.polls, c.sleeps)
	}
}

// An oversleep is absorbed by the next batch: deadlines are cumulative from
// the payload start, so the next wait shrinks instead of the send drifting.
func TestPacer_OversleepIsAbsorbedByNextBatch(t *testing.T) {
	c := &fakeClock{t: pacerT0, spinStep: time.Microsecond, overshoot: 300 * time.Microsecond}
	p := newTestPacer(c)
	c.advance(100 * time.Microsecond)
	p.afterDatagram(pacerT0, 32, 1000) // sleeps ~439 µs, wakes 300 µs late at ~840 µs

	c.advance(100 * time.Microsecond)  // second burst
	p.afterDatagram(pacerT0, 64, 1000) // deadline 1280 µs

	if len(c.sleeps) != 2 {
		t.Fatalf("sleeps = %v, want 2", c.sleeps)
	}
	if got := c.sleeps[1]; got >= 440*time.Microsecond {
		t.Fatalf("second sleep %v did not shrink to absorb the first oversleep", got)
	}
}

func TestPacer_ZeroIntervalNeverWaits(t *testing.T) {
	c := &fakeClock{t: pacerT0, spinStep: time.Microsecond}
	p := newTestPacer(c)
	p.interval = 0
	p.afterDatagram(pacerT0, 32, 1000)
	if c.polls != 0 || len(c.sleeps) != 0 {
		t.Fatalf("zero interval waited: polls=%d sleeps=%v", c.polls, c.sleeps)
	}
}

// The final batch has no later batch to absorb an oversleep, so its wait is
// spun: a late wake-up there would land directly on the send's end time
// (measured +0.5 ms per field under WSL2 before this rule).
func TestPacer_FinalBoundarySpinsInsteadOfSleeping(t *testing.T) {
	c := &fakeClock{t: pacerT0, spinStep: time.Microsecond, overshoot: 500 * time.Microsecond}
	p := newTestPacer(c)
	c.advance(100 * time.Microsecond)

	p.afterDatagram(pacerT0, 32, 40) // 8 datagrams left: the last batch

	if len(c.sleeps) != 0 {
		t.Fatalf("slept %v before the final batch", c.sleeps)
	}
	deadline := pacerT0.Add(640 * time.Microsecond)
	if c.t.Before(deadline) || c.t.After(deadline.Add(2*time.Microsecond)) {
		t.Fatalf("returned at %v, want the deadline %v", c.t.Sub(pacerT0), deadline.Sub(pacerT0))
	}
}
