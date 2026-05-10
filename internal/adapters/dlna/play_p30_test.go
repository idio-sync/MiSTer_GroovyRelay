package dlna

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// play_p30_test.go covers the P3.0 acceptance criteria: handlePlay's
// startFreshSession branch must fault-map StartSession failures via
// errors.Is on core.ErrProbeUnreachable. A wrapped sentinel produces
// 716 (Resource not found); anything else produces 501 (Action Failed).
//
// Spec: docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md
// SOAP fault table line 321.

// TestPlay_StartSession_ProbeUnreachable_Returns716 is the positive case
// for the new sentinel switch. The fake SessionManager wraps a synthetic
// inner error with core.ErrProbeUnreachable; the handler must emit 716.
func TestPlay_StartSession_ProbeUnreachable_Returns716(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	// Mirror the chain Manager.probeForStart produces (errors.Join of
	// the underlying ffprobe error + the sentinel) so the test exercises
	// the actual production wrap path, not just a single-level wrap.
	fake.startErr = fmt.Errorf("probe source: %w: %w",
		errors.New("dial tcp: connection refused"),
		core.ErrProbeUnreachable)

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>716</errorCode>") {
		t.Errorf("body missing errorCode 716 (Resource not found): %s", rr.Body.String())
	}
	// Rollback discipline must still hold under the new mapping.
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRef != "" {
		t.Errorf("currentRef = %q after probe-unreachable failure; want empty (rollback)",
			a.currentRef)
	}
	if a.transportState != transportStateStopped {
		t.Errorf("transportState = %q; want STOPPED", a.transportState)
	}
}

// TestPlay_StartSession_GenericFailure_Returns501 is the negative case:
// a StartSession error that does NOT wrap core.ErrProbeUnreachable
// (e.g. ErrPlaneError from a corrupted modeline, or any unwrapped
// internal failure) must map to 501 Action Failed, not 716.
func TestPlay_StartSession_GenericFailure_Returns501(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	// ErrPlaneError chain — distinct sentinel that must NOT match the
	// ErrProbeUnreachable switch. Production wraps modeline / RGB-mode
	// resolve failures this way.
	fake.startErr = fmt.Errorf("%w: unknown modeline", core.ErrPlaneError)

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>501</errorCode>") {
		t.Errorf("body missing errorCode 501 (Action Failed): %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "<errorCode>716</errorCode>") {
		t.Errorf("body has errorCode 716 for non-probe failure: %s", rr.Body.String())
	}
}
