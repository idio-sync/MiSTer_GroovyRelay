package dataplane

import (
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func TestRasterLinePeriod_NTSC480iReasonable(t *testing.T) {
	got := rasterLinePeriod(groovy.NTSC480i60)
	if got < 60*time.Microsecond || got > 70*time.Microsecond {
		t.Fatalf("rasterLinePeriod(NTSC480i60) = %v, want about 64us", got)
	}
}

func TestRasterCorrection_UsesFreshEchoOnly(t *testing.T) {
	linePeriod := 64 * time.Microsecond
	fieldPeriod := 16683 * time.Microsecond
	ack := groovy.ACK{
		FrameEcho:  10,
		VCountEcho: 40,
		FPGAFrame:  10,
		FPGAVCount: 44,
	}

	if _, ok := rasterCorrection(ack, groovy.NTSC480i60, linePeriod, fieldPeriod, 10); ok {
		t.Fatal("expected no correction for repeated frame echo")
	}
	if got, ok := rasterCorrection(ack, groovy.NTSC480i60, linePeriod, fieldPeriod, 9); !ok {
		t.Fatal("expected correction for fresh frame echo")
	} else if got >= 0 {
		t.Fatalf("expected negative correction when FPGA raster is ahead, got %v", got)
	}
}

func TestFieldClock_DelayClampsNegativeToZero(t *testing.T) {
	t0 := time.Unix(1000, 0)
	c := newFieldClock(t0, 16*time.Millisecond)
	if got := c.delay(t0.Add(20 * time.Millisecond)); got != 0 {
		t.Fatalf("delay overshoot = %v, want 0", got)
	}
}

func TestFieldClock_SendWorkDoesNotStretchTick(t *testing.T) {
	period := 16 * time.Millisecond
	t0 := time.Unix(1000, 0)
	c := newFieldClock(t0, period)

	c.fired(t0.Add(period))
	// 8 ms of send work after the tick must shorten the wait, not add to it.
	if got, want := c.delay(t0.Add(period+8*time.Millisecond)), 8*time.Millisecond; got != want {
		t.Fatalf("delay after 8ms of send work = %v, want %v", got, want)
	}
}

func TestFieldClock_WakeLatencyDoesNotAccumulate(t *testing.T) {
	period := 16 * time.Millisecond
	t0 := time.Unix(1000, 0)
	c := newFieldClock(t0, period)

	// Every wake is 1 ms late; the schedule must stay on the ideal grid.
	for i := 1; i <= 100; i++ {
		c.fired(t0.Add(time.Duration(i)*period + time.Millisecond))
	}
	if got, want := c.next, t0.Add(101*period); !got.Equal(want) {
		t.Fatalf("next tick after 100 late wakes = %v, want %v (drift %v)", got, want, got.Sub(want))
	}
}

func TestFieldClock_CorrectionPersistsAndLatestWins(t *testing.T) {
	period := 16 * time.Millisecond
	t0 := time.Unix(1000, 0)
	c := newFieldClock(t0, period)

	c.fired(t0.Add(period))
	c.correct(-2 * time.Millisecond)
	c.correct(-1 * time.Millisecond) // a second fresh echo in the same slot replaces the first
	if got, want := c.next, t0.Add(2*period-time.Millisecond); !got.Equal(want) {
		t.Fatalf("corrected next = %v, want %v", got, want)
	}
	// The phase shift carries into subsequent slots.
	c.fired(c.next)
	if got, want := c.next, t0.Add(3*period-time.Millisecond); !got.Equal(want) {
		t.Fatalf("next after corrected slot = %v, want %v", got, want)
	}
}

func TestFieldClock_LateLessThanPeriodCatchesUp(t *testing.T) {
	period := 16 * time.Millisecond
	t0 := time.Unix(1000, 0)
	c := newFieldClock(t0, period)

	wake := t0.Add(period + 10*time.Millisecond)
	c.fired(wake)
	if got, want := c.delay(wake), 6*time.Millisecond; got != want {
		t.Fatalf("delay after 10ms-late wake = %v, want %v", got, want)
	}
}

func TestFieldClock_StallBeyondPeriodRebasesInsteadOfBursting(t *testing.T) {
	period := 16 * time.Millisecond
	t0 := time.Unix(1000, 0)
	c := newFieldClock(t0, period)

	// A 100 ms stall: the missed slots are gone (the FPGA already repeated
	// fields); catching up would burst several BLITs back-to-back.
	wake := t0.Add(period + 100*time.Millisecond)
	c.fired(wake)
	if got, want := c.delay(wake), period; got != want {
		t.Fatalf("delay after stall = %v, want %v", got, want)
	}
}

func TestResetTimer_ResetsCleanly(t *testing.T) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	resetTimer(timer, 5*time.Millisecond)

	select {
	case <-timer.C:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timer did not fire after reset")
	}
}
