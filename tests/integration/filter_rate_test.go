//go:build integration

package integration

import (
	"runtime"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// TestFilterRate_FieldsPerSecondMatchesTarget runs the full FFmpeg pipeline
// against synthetic clips at 23.976p and 60p and asserts the sender emits the
// expected ~300 fields per 5-second clip regardless of source rate. This is
// the regression harness for C1 — before the fix, 60p input produced ~150
// fields (half rate).
func TestFilterRate_FieldsPerSecondMatchesTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg scenarios require Unix ExtraFiles; run on Linux/CI")
	}
	cases := []struct {
		name string
		rate string
	}{
		{"film_23.976p", "24000/1001"},
		{"sports_60p", "60"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sample := ensureSampleMP4Rate(t, tc.name+".mp4", 5, tc.rate)
			h := newScenarioHarness(t)
			req := core.SessionRequest{
				StreamURL:    sample,
				AdapterRef:   tc.name,
				DirectPlay:   true,
				Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
			}
			if err := h.Manager.StartSession(req); err != nil {
				t.Fatalf("start: %v", err)
			}
			waitIdle(t, h, 15*time.Second)
			time.Sleep(200 * time.Millisecond)

			snap := h.Recorder.Snapshot()
			blits := snap.Counts[groovy.CmdBlitFieldVSync]
			if blits < 255 || blits > 345 {
				t.Errorf("%s: expected ~300 blits, got %d", tc.name, blits)
			}
		})
	}
}
