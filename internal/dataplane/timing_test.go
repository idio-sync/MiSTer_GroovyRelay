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

func TestNextTickDelay_ClampsNegativeToZero(t *testing.T) {
	lastTick := time.Now().Add(-20 * time.Millisecond)
	got := nextTickDelay(lastTick, 16*time.Millisecond, 0)
	if got != 0 {
		t.Fatalf("nextTickDelay overshoot = %v, want 0", got)
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
