package dlna

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// pause_test.go covers the P3.1 Pause SOAP handler + the
// disabled-adapter gate + concurrent-Play guard + the live-edge resume
// branch + the refined GetCurrentTransportActions advertisement.
//
// All tests reuse captureSessionManager from play_test.go so the fake
// surface area stays consolidated.

// avtSendPause invokes handleAVTransportSOAP with a Pause envelope and
// the given args fragment.
func avtSendPause(t *testing.T, a *Adapter, argsXML string) *httptest.ResponseRecorder {
	t.Helper()
	req, rr := avtSOAPRequest(t, "Pause", argsXML)
	a.handleAVTransportSOAP(rr, req)
	return rr
}

// pausePrimedAdapter constructs an adapter with a captureSessionManager
// already in PLAYING state and wires Status() to report ownership of
// our ref. Most Pause tests want this shape because handlePause's
// happy path requires currentRef != "" + transportState == PLAYING.
// Returns the adapter, fake, and the active ref.
func pausePrimedAdapter(t *testing.T) (*Adapter, *captureSessionManager, string) {
	t.Helper()
	a, fake := avtPlayAdapter(t)
	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("priming Play status = %d, want 200", rr.Code)
	}
	a.mu.Lock()
	ref := a.currentRef
	a.mu.Unlock()
	if ref == "" {
		t.Fatal("priming Play left empty currentRef")
	}
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref}
	}
	return a, fake, ref
}

// ---- Pause handler ----

func TestPause_BadInstanceID(t *testing.T) {
	a, fake, _ := pausePrimedAdapter(t)
	rr := avtSendPause(t, a, "<InstanceID>1</InstanceID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>718</errorCode>") {
		t.Errorf("body missing errorCode 718: %s", rr.Body.String())
	}
	if fake.snapshotPauseCalls() != 0 {
		t.Errorf("core.Pause called %d times on bad InstanceID; want 0", fake.snapshotPauseCalls())
	}
}

func TestPause_NoSession_Returns701(t *testing.T) {
	// currentRef == "" — no DLNA session, no foreign session. Spec
	// §Pause line 376 requires the ownership guard, and "owned == ''"
	// means there's nothing to pause.
	fake := &captureSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)

	rr := avtSendPause(t, a, "<InstanceID>0</InstanceID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotPauseCalls() != 0 {
		t.Errorf("core.Pause called %d times on no-session; want 0", fake.snapshotPauseCalls())
	}
}

func TestPause_AlreadyStopped_Returns701(t *testing.T) {
	// Adapter has a ref but transportState is STOPPED — the FSM would
	// reject EvPause from idle/stopped, so handlePause short-circuits.
	a, fake := avtPlayAdapter(t)
	a.mu.Lock()
	a.currentRef = "dlna:abcd1234"
	a.transportState = transportStateStopped
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: "dlna:abcd1234"}
	}

	rr := avtSendPause(t, a, "<InstanceID>0</InstanceID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotPauseCalls() != 0 {
		t.Errorf("core.Pause called %d times when STOPPED; want 0", fake.snapshotPauseCalls())
	}
}

func TestPause_AlreadyPaused_Returns701(t *testing.T) {
	// Repeat-Pause is a no-op-via-fault: the FSM rejects EvPause from
	// Paused, and the controller-visible 701 ("transition not
	// available") matches that intent.
	a, fake := avtPlayAdapter(t)
	a.mu.Lock()
	a.currentRef = "dlna:efgh5678"
	a.transportState = transportStatePausedPlayback
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: "dlna:efgh5678"}
	}

	rr := avtSendPause(t, a, "<InstanceID>0</InstanceID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotPauseCalls() != 0 {
		t.Errorf("core.Pause called %d times when already PAUSED; want 0", fake.snapshotPauseCalls())
	}
}

func TestPause_ForeignSession_Returns701(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	// Adapter has its own state set to PLAYING with a ref — but core's
	// Status reports a different (foreign) AdapterRef. Spec §Common
	// Action Rules line 294: foreign sessions must remain untouched.
	a.mu.Lock()
	a.currentRef = "dlna:ours"
	a.transportState = transportStatePlaying
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: "plex:/library/metadata/1234"}
	}

	rr := avtSendPause(t, a, "<InstanceID>0</InstanceID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotPauseCalls() != 0 {
		t.Errorf("core.Pause called %d times on foreign session; want 0", fake.snapshotPauseCalls())
	}
}

func TestPause_HappyPath_TransitionsToPaused(t *testing.T) {
	a, fake, _ := pausePrimedAdapter(t)

	rr := avtSendPause(t, a, "<InstanceID>0</InstanceID>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fake.snapshotPauseCalls() != 1 {
		t.Errorf("core.Pause called %d times, want 1", fake.snapshotPauseCalls())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.transportState != transportStatePausedPlayback {
		t.Errorf("transportState = %q, want %q", a.transportState, transportStatePausedPlayback)
	}
	if a.lastError != "" {
		t.Errorf("lastError = %q after success; want empty", a.lastError)
	}
}

func TestPause_CoreFailure_Returns501AndSetsLastError(t *testing.T) {
	a, fake, _ := pausePrimedAdapter(t)
	fake.pauseErr = errors.New("simulated pause failure")

	rr := avtSendPause(t, a, "<InstanceID>0</InstanceID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>501</errorCode>") {
		t.Errorf("body missing errorCode 501: %s", rr.Body.String())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastError == "" {
		t.Error("lastError empty after Pause failure; want descriptive message")
	}
	// Redaction: raw err.Error() must NOT leak into lastError.
	if strings.Contains(a.lastError, "simulated pause failure") {
		t.Errorf("lastError = %q; raw err.Error() leaked into operator-visible field", a.lastError)
	}
	if a.transportState != transportStatePlaying {
		t.Errorf("transportState = %q after Pause failure; want PLAYING (no transition)",
			a.transportState)
	}
}

// ---- Concurrent-Play guard ----

func TestPlay_StartInFlight_Returns701(t *testing.T) {
	// Symmetric with handleSetAVTransportURI's busy-reject (spec line
	// 332). startInFlight=true means a sibling Play already minted a
	// ref and is awaiting StartSession — short-circuit instead of
	// allowing a parallel mint.
	a, fake := avtPlayAdapter(t)
	a.mu.Lock()
	a.startInFlight = true
	a.currentRef = "dlna:already-running"
	a.mu.Unlock()

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotStartCalls() != 0 {
		t.Errorf("StartSession called %d times during startInFlight; want 0",
			fake.snapshotStartCalls())
	}
	if fake.snapshotPlayCalls() != 0 {
		t.Errorf("core.Play called %d times during startInFlight; want 0",
			fake.snapshotPlayCalls())
	}
}

// ---- Disabled-adapter gate ----

func TestPlay_Disabled_Returns701(t *testing.T) {
	fake := &captureSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Default cfg.Enabled=false. Even with a URI loaded, control
	// actions on a disabled adapter must reject without touching core.
	a.mu.Lock()
	a.loadedURI = "http://192.168.1.99/movie.mp4"
	a.mu.Unlock()

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotStartCalls() != 0 {
		t.Errorf("StartSession called %d times on disabled adapter; want 0",
			fake.snapshotStartCalls())
	}
}

func TestPause_Disabled_Returns701(t *testing.T) {
	fake := &captureSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Even with a live ref, a disabled adapter must short-circuit
	// without consulting core (avoids preempting a foreign session via
	// the core call chain).
	a.mu.Lock()
	a.currentRef = "dlna:owned"
	a.transportState = transportStatePlaying
	a.mu.Unlock()

	rr := avtSendPause(t, a, "<InstanceID>0</InstanceID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotPauseCalls() != 0 {
		t.Errorf("core.Pause called %d times on disabled adapter; want 0",
			fake.snapshotPauseCalls())
	}
}

func TestStop_Disabled_Returns701(t *testing.T) {
	fake := &captureSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.mu.Lock()
	a.currentRef = "dlna:owned"
	a.transportState = transportStatePlaying
	a.mu.Unlock()

	rr := avtSendStop(t, a, "<InstanceID>0</InstanceID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotStopCalls() != 0 {
		t.Errorf("core.Stop called %d times on disabled adapter; want 0",
			fake.snapshotStopCalls())
	}
}

// ---- Resume branch: VOD vs live-edge ----

func TestPlay_PausedResume_VODKnownDuration_CallsCorePlay(t *testing.T) {
	// Spec §Play line 358: "For seekable sources with known duration,
	// call core.Play()." Status reports Duration > 0 → Play() resume
	// path, NOT a rebuild via StartSession.
	a, fake := avtPlayAdapter(t)
	ref := "dlna:vod-resume"
	a.mu.Lock()
	a.currentRef = ref
	a.transportState = transportStatePausedPlayback
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref, Duration: 2 * time.Hour}
	}

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fake.snapshotPlayCalls() != 1 {
		t.Errorf("core.Play called %d times, want 1 (VOD resume)", fake.snapshotPlayCalls())
	}
	if fake.snapshotStartCalls() != 0 {
		t.Errorf("StartSession called %d times; VOD resume must NOT rebuild session",
			fake.snapshotStartCalls())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.transportState != transportStatePlaying {
		t.Errorf("transportState = %q after VOD resume; want PLAYING", a.transportState)
	}
	// Ref preserved across VOD resume — no rebuild, no rotation.
	if a.currentRef != ref {
		t.Errorf("currentRef = %q after VOD resume; want %q (preserved)", a.currentRef, ref)
	}
}

func TestPlay_PausedResume_LiveUnknownDuration_RebuildsAtLiveEdge(t *testing.T) {
	// Spec §Play line 358: "For live or unknown-duration sources,
	// rebuild the same core.SessionRequest with SeekOffsetMs=0 and
	// call core.StartSession to reconnect from the live edge."
	a, fake := avtPlayAdapter(t)
	originalRef := "dlna:live-resume"
	a.mu.Lock()
	a.currentRef = originalRef
	a.transportState = transportStatePausedPlayback
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		// Duration=0 means the prior probe found no usable duration —
		// live or unknown.
		return core.SessionStatus{AdapterRef: originalRef, Duration: 0}
	}

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fake.snapshotStartCalls() != 1 {
		t.Errorf("StartSession called %d times, want 1 (live-edge rebuild)",
			fake.snapshotStartCalls())
	}
	if fake.snapshotPlayCalls() != 0 {
		t.Errorf("core.Play called %d times; live-edge resume must rebuild, not Play()",
			fake.snapshotPlayCalls())
	}
	req := fake.lastReq()
	if req.SeekOffsetMs != 0 {
		t.Errorf("rebuilt SessionRequest.SeekOffsetMs = %d, want 0 (live edge)",
			req.SeekOffsetMs)
	}
	if req.StreamURL != "http://192.168.1.99/movie.mp4" {
		t.Errorf("rebuilt StreamURL = %q, want preserved loaded URI", req.StreamURL)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRef == originalRef {
		t.Error("currentRef unchanged after live-edge rebuild; want fresh ref via markStartInFlight")
	}
	if a.currentRef == "" {
		t.Error("currentRef empty after live-edge rebuild; want fresh ref")
	}
	if a.transportState != transportStatePlaying {
		t.Errorf("transportState = %q after live-edge rebuild; want PLAYING", a.transportState)
	}
}

// TestPlay_PausedResume_LiveEdgeFailure_GenericError_Returns501 mirrors
// TestPlay_StartSessionFailure_RollsBackRef but enters the failure path
// from the live-edge resume caller (PAUSED_PLAYBACK → live-edge rebuild)
// instead of the fresh-start caller. P3.1 review issue 1: the
// buildAndStartSession failure block must force transportState=STOPPED
// unconditionally so both callers leave the same observable shape —
// otherwise the live-edge caller would strand the adapter at
// PAUSED_PLAYBACK with an empty currentRef, an "orphan PAUSED" state.
func TestPlay_PausedResume_LiveEdgeFailure_GenericError_Returns501(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	originalRef := "dlna:live-edge-fail"
	a.mu.Lock()
	a.currentRef = originalRef
	a.transportState = transportStatePausedPlayback
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		// Duration=0 + AdapterRef=ours selects the live-edge rebuild
		// branch in handlePlay's PAUSED_PLAYBACK sub-case.
		return core.SessionStatus{AdapterRef: originalRef, Duration: 0}
	}
	// Generic error (no ErrProbeUnreachable in chain) → 501 mapping.
	fake.startErr = errors.New("simulated rebuild failure")

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>501</errorCode>") {
		t.Errorf("body missing errorCode 501: %s", rr.Body.String())
	}
	if fake.snapshotStartCalls() != 1 {
		t.Errorf("StartSession called %d times, want 1 (live-edge rebuild attempt)",
			fake.snapshotStartCalls())
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// Rollback discipline: ref cleared, in-flight cleared.
	if a.currentRef != "" {
		t.Errorf("currentRef = %q after live-edge rebuild failure; want empty (rollback)",
			a.currentRef)
	}
	if a.startInFlight {
		t.Error("startInFlight = true after live-edge rebuild failure; want false")
	}
	// The fix from issue 1: transportState must land at STOPPED, NOT
	// stay at PAUSED_PLAYBACK. Without the unconditional set, the
	// live-edge caller would strand at PAUSED_PLAYBACK with currentRef
	// empty — the orphan-PAUSED bug.
	if a.transportState != transportStateStopped {
		t.Errorf("transportState = %q after live-edge rebuild failure; want STOPPED (orphan-PAUSED guard)",
			a.transportState)
	}
	if a.lastError == "" {
		t.Error("lastError empty after live-edge rebuild failure; want a redacted message")
	}
	// Redaction discipline (parallel to the fresh-start caller's
	// redaction guarantee in TestPlay_Resume_CoreFailure_RedactsLastError):
	// the raw err.Error() must NOT leak into lastError.
	if strings.Contains(a.lastError, "simulated rebuild failure") {
		t.Errorf("lastError = %q; raw err.Error() leaked into operator-visible field",
			a.lastError)
	}
}

// TestPlay_PausedResume_LiveEdgeFailure_ProbeUnreachable_Returns716 is
// the parallel test for the ErrProbeUnreachable mapping on the live-edge
// caller — matches TestPlay_StartSession_ProbeUnreachable_Returns716's
// shape but enters from PAUSED_PLAYBACK.
func TestPlay_PausedResume_LiveEdgeFailure_ProbeUnreachable_Returns716(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	originalRef := "dlna:live-edge-probe-fail"
	a.mu.Lock()
	a.currentRef = originalRef
	a.transportState = transportStatePausedPlayback
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: originalRef, Duration: 0}
	}
	// Mirror the wrap chain Manager.probeForStart produces so the test
	// exercises the actual production errors.Is path.
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

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRef != "" {
		t.Errorf("currentRef = %q after live-edge probe-unreachable failure; want empty (rollback)",
			a.currentRef)
	}
	if a.startInFlight {
		t.Error("startInFlight = true after live-edge probe-unreachable failure; want false")
	}
	if a.transportState != transportStateStopped {
		t.Errorf("transportState = %q after live-edge probe-unreachable failure; want STOPPED",
			a.transportState)
	}
	if a.lastError == "" {
		t.Error("lastError empty after live-edge probe-unreachable failure; want a redacted message")
	}
}

// ---- GetCurrentTransportActions ----

func TestGetCurrentTransportActions_PlayingOwnSeekable_PauseStopSeek(t *testing.T) {
	a, fake, ref := pausePrimedAdapter(t)
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref, Duration: 2 * time.Hour}
	}

	req, rr := avtSOAPRequest(t, "GetCurrentTransportActions", "<InstanceID>0</InstanceID>")
	a.handleAVTransportSOAP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<Actions>Pause,Stop,Seek</Actions>") {
		t.Errorf("body missing <Actions>Pause,Stop,Seek</Actions>; got: %s", rr.Body.String())
	}
}

func TestGetCurrentTransportActions_PlayingOwnLive_PauseStop(t *testing.T) {
	a, fake, ref := pausePrimedAdapter(t)
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref, Duration: 0}
	}

	req, rr := avtSOAPRequest(t, "GetCurrentTransportActions", "<InstanceID>0</InstanceID>")
	a.handleAVTransportSOAP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<Actions>Pause,Stop</Actions>") {
		t.Errorf("body missing <Actions>Pause,Stop</Actions>; got: %s", rr.Body.String())
	}
	// Defensive: Seek must not be advertised for live/unknown duration.
	if strings.Contains(rr.Body.String(), "Seek") {
		t.Errorf("body advertises Seek for unknown-duration session: %s", rr.Body.String())
	}
}

func TestGetCurrentTransportActions_PausedOwnSeekable_PlayStopSeek(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	ref := "dlna:paused-seekable"
	a.mu.Lock()
	a.currentRef = ref
	a.transportState = transportStatePausedPlayback
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref, Duration: 90 * time.Minute}
	}

	req, rr := avtSOAPRequest(t, "GetCurrentTransportActions", "<InstanceID>0</InstanceID>")
	a.handleAVTransportSOAP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<Actions>Play,Stop,Seek</Actions>") {
		t.Errorf("body missing <Actions>Play,Stop,Seek</Actions>; got: %s", rr.Body.String())
	}
}

func TestGetCurrentTransportActions_PausedOwnLive_PlayStop(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	ref := "dlna:paused-live"
	a.mu.Lock()
	a.currentRef = ref
	a.transportState = transportStatePausedPlayback
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref, Duration: 0}
	}

	req, rr := avtSOAPRequest(t, "GetCurrentTransportActions", "<InstanceID>0</InstanceID>")
	a.handleAVTransportSOAP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<Actions>Play,Stop</Actions>") {
		t.Errorf("body missing <Actions>Play,Stop</Actions>; got: %s", rr.Body.String())
	}
}
