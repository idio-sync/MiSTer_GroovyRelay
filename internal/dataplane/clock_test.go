package dataplane

import (
	"context"
	"sync"
	"testing"
	"time"
)

var timerSanity struct {
	once    sync.Once
	elapsed time.Duration
}

func requireReasonableTimers(t *testing.T) {
	t.Helper()
	timerSanity.once.Do(func() {
		start := time.Now()
		timer := time.NewTimer(20 * time.Millisecond)
		<-timer.C
		timerSanity.elapsed = time.Since(start)
	})
	if timerSanity.elapsed > 200*time.Millisecond {
		t.Skipf("runtime timers too slow for pacing assertions in this environment: 20ms timer took %v", timerSanity.elapsed)
	}
}

// TestClock_TicksAtExpectedRate verifies RunFieldTimer fires at the requested
// fields-per-second rate. 500 ms × 59.94 = ~29.97 ticks expected; Windows
// timer jitter (~15.6 ms) warrants a ±20% tolerance on unit-test hardware.
func TestClock_TicksAtExpectedRate(t *testing.T) {
	requireReasonableTimers(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticks := make(chan time.Time, 256)
	go RunFieldTimer(ctx, 59.94, ticks)

	var count int
	deadline := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case <-ticks:
			count++
		case <-deadline:
			break loop
		}
	}
	// 500ms * 59.94 = 29.97 ticks expected; tolerate ±20%.
	if count < 24 || count > 36 {
		t.Errorf("expected ~30 ticks in 500ms, got %d", count)
	}
}

// TestClock_StopsOnContextCancel verifies the goroutine exits promptly when
// ctx is cancelled, not stranded on the ticker channel.
func TestClock_StopsOnContextCancel(t *testing.T) {
	requireReasonableTimers(t)

	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 4)
	done := make(chan struct{})
	go func() {
		RunFieldTimer(ctx, 59.94, ticks)
		close(done)
	}()
	// Let at least one tick elapse, then cancel.
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunFieldTimer did not exit after ctx cancel")
	}
}

// TestClock_DropsWhenConsumerIsBehind verifies ticks are dropped on a full
// channel instead of blocking the timer loop — the data plane's deadline
// pressure is real.
func TestClock_DropsWhenConsumerIsBehind(t *testing.T) {
	requireReasonableTimers(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Unbuffered consumer — every tick after the first gets dropped.
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		RunFieldTimer(ctx, 59.94, ticks)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunFieldTimer stuck on full channel instead of dropping")
	}
}
