//go:build integration

package integration

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// TestCrossAdapter_PlexPreemptsJF verifies that a JF cast in flight is
// torn down cleanly when a Plex session arrives, and the JF adapter's
// OnStop closure observes reason="preempted".
func TestCrossAdapter_PlexPreemptsJF(t *testing.T) {
	t.Skip("requires fakemister + core.Manager harness; flesh out with the existing integration harness pattern at tests/integration/<existing>.go")

	// Outline:
	// 1. Stand up fakemister on a localhost UDP port.
	// 2. Build a real core.Manager pointing at it (with -short-friendly
	//    SWITCHRES + RGB defaults).
	// 3. Construct a JF adapter and a stub-Plex StartSession caller.
	// 4. JF.HandlePlay against a tiny test stream URL → verify
	//    fakemister sees BLIT frames.
	// 5. var preemptReason atomic.Value
	//    Wire JF SessionRequest's OnStop to record the reason.
	//    (Done automatically via JF's makeOnStop; need to expose
	//    a hook on the JF adapter for the test, or assert via the
	//    reporter's emitted PlaybackStopped over a fake WS server.)
	// 6. Plex-style: build a SessionRequest manually and call
	//    coreMgr.StartSession.
	// 7. Verify JF's OnStop fired with "preempted".
	// 8. Verify fakemister sees the new (Plex) stream.
	_ = atomic.Value{}
	_ = context.Background
	_ = core.SessionRequest{}
	_ = time.Second
}

// TestCrossAdapter_PlaneErrorTriggersJFStopped verifies that an
// ffmpeg crash mid-cast triggers JF's reporter to emit
// PlaybackStopped {Failed: true} via the Phase-0 EvError path.
func TestCrossAdapter_PlaneErrorTriggersJFStopped(t *testing.T) {
	t.Skip("requires forced-failure ffmpeg URL + WS capture; outline below")

	// Outline:
	// 1. Build a JF adapter wired to a real core.Manager + fakemister.
	// 2. Stand up a fake JF (httptest WS server) that captures all
	//    outbound messages (PlaybackStart, Progress, Stopped).
	// 3. JF.HandlePlay with an ffmpeg-incompatible URL (e.g. an
	//    unreachable host — ffmpeg will fail INIT or read).
	// 4. Wait for the fake WS to see PlaybackStopped {Failed: true}
	//    within ~5 s (plane-error → EvError → reporter classifies
	//    as Idle → emits Stopped with errReason="error" → Failed=true).
	// 5. Verify FSM is back at StateIdle (post-Phase 0 transition).
}
