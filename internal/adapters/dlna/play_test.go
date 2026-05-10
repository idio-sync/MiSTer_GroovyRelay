package dlna

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// play_test.go covers the P2.4 Play and Stop SOAP handlers + the
// autoplay_on_set_uri branch in handleSetAVTransportURI. It uses a
// richer fake than session_lifecycle_test.go's fakeSessionManager —
// we capture the SessionRequest passed to StartSession and let tests
// inject errors per-call. The fake also remembers the OnStop closure
// so tests can reach into the lifecycle directly.

// captureSessionManager records every StartSession call along with the
// captured request, so tests can assert on StreamURL / Capabilities /
// AdapterRef / MediaInputPolicy etc. errFn lets a test return an
// error on demand to drive the rollback path.
type captureSessionManager struct {
	mu sync.Mutex

	statusFn func() core.SessionStatus

	// startReqs holds every SessionRequest passed to StartSession in
	// order. captured.OnStop is preserved verbatim so a test can
	// invoke it manually to drive the lifecycle (compare-and-clear,
	// stale-ref no-op, error-reason path).
	startReqs []core.SessionRequest

	// startErr lets a test inject a StartSession error to exercise the
	// rollback path. nil = success.
	startErr error

	// stopErr lets a test inject a Stop error to exercise the
	// 501-Action-Failed branch in handleStop.
	stopErr error

	// playErr lets a test inject a Play error to exercise the
	// PAUSED→Play branch's error path.
	playErr error

	// Method counters mirror fakeSessionManager — useful for assertions
	// like "core.Stop was called once."
	startCalls int
	stopCalls  int
	playCalls  int
	pauseCalls int
	seekCalls  int
}

func (c *captureSessionManager) StartSession(req core.SessionRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.startReqs = append(c.startReqs, req)
	c.startCalls++
	return c.startErr
}

func (c *captureSessionManager) Status() core.SessionStatus {
	if c.statusFn != nil {
		return c.statusFn()
	}
	return core.SessionStatus{}
}

func (c *captureSessionManager) Pause() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pauseCalls++
	return nil
}

func (c *captureSessionManager) Play() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.playCalls++
	return c.playErr
}

func (c *captureSessionManager) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopCalls++
	return c.stopErr
}

func (c *captureSessionManager) SeekTo(int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seekCalls++
	return nil
}

// lastReq returns the most-recent captured SessionRequest. Convenience
// helper for tests that just want to check the latest call.
func (c *captureSessionManager) lastReq() core.SessionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.startReqs) == 0 {
		return core.SessionRequest{}
	}
	return c.startReqs[len(c.startReqs)-1]
}

// snapshotStartCalls reads startCalls under the fake's mutex so tests
// don't race with a goroutine running OnStop.
func (c *captureSessionManager) snapshotStartCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startCalls
}

func (c *captureSessionManager) snapshotStopCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopCalls
}

func (c *captureSessionManager) snapshotPlayCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.playCalls
}

// avtPlayAdapter constructs an enabled adapter with a captureSessionManager
// already wired and a pre-loaded URI/metadata. Returns the adapter +
// the fake so tests can both invoke handlers and assert on captured
// state. Most Play tests want this shape because the Play handler's
// fresh-start branch only runs when a URI is loaded.
func avtPlayAdapter(t *testing.T) (*Adapter, *captureSessionManager) {
	t.Helper()
	fake := &captureSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	a.mu.Lock()
	a.loadedURI = "http://192.168.1.99/movie.mp4"
	a.mu.Unlock()
	return a, fake
}

// avtSendPlay invokes handleAVTransportSOAP with a Play envelope and
// the given args fragment.
func avtSendPlay(t *testing.T, a *Adapter, argsXML string) *httptest.ResponseRecorder {
	t.Helper()
	req, rr := avtSOAPRequest(t, "Play", argsXML)
	a.handleAVTransportSOAP(rr, req)
	return rr
}

// avtSendStop invokes handleAVTransportSOAP with a Stop envelope.
func avtSendStop(t *testing.T, a *Adapter, argsXML string) *httptest.ResponseRecorder {
	t.Helper()
	req, rr := avtSOAPRequest(t, "Stop", argsXML)
	a.handleAVTransportSOAP(rr, req)
	return rr
}

// ---- Play handler ----

func TestPlay_FreshStart_StoresRefAndPlays(t *testing.T) {
	a, fake := avtPlayAdapter(t)

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	if fake.snapshotStartCalls() != 1 {
		t.Errorf("StartSession called %d times, want 1", fake.snapshotStartCalls())
	}

	req := fake.lastReq()
	if req.StreamURL != "http://192.168.1.99/movie.mp4" {
		t.Errorf("StreamURL = %q, want %q", req.StreamURL, "http://192.168.1.99/movie.mp4")
	}
	if !strings.HasPrefix(req.AdapterRef, "dlna:") {
		t.Errorf("AdapterRef = %q, want dlna: prefix", req.AdapterRef)
	}
	if !req.Capabilities.CanSeek {
		t.Error("Capabilities.CanSeek = false, want true (spec line 361 keeps manager gate permissive)")
	}
	if !req.Capabilities.CanPause {
		t.Error("Capabilities.CanPause = false, want true")
	}
	if !req.DirectPlay {
		t.Error("DirectPlay = false, want true (validated direct HTTP URL)")
	}
	if req.OnStop == nil {
		t.Error("OnStop = nil, want closure (lifecycle requires it)")
	}
	// MediaInputPolicy must carry the spec's path-A constraints.
	policy := req.MediaInputPolicy
	if !contains(policy.ProtocolWhitelist, "https") {
		t.Errorf("ProtocolWhitelist missing https: %v", policy.ProtocolWhitelist)
	}
	if !policy.DisableReconnect {
		t.Error("DisableReconnect = false, want true")
	}
	if policy.RWTimeout == 0 {
		t.Error("RWTimeout = 0, want >0 (5s per dlnaInputPolicy)")
	}
	if !contains(policy.BlockedHeaders, "Referer") {
		t.Errorf("BlockedHeaders missing Referer: %v", policy.BlockedHeaders)
	}

	// State and ref invariants.
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.transportState != transportStatePlaying {
		t.Errorf("transportState = %q, want %q", a.transportState, transportStatePlaying)
	}
	if a.currentRef == "" {
		t.Error("currentRef empty after success; want minted ref")
	}
	if a.startInFlight {
		t.Error("startInFlight = true after success; want false")
	}
	if a.lastError != "" {
		t.Errorf("lastError = %q after success; want empty", a.lastError)
	}
}

func TestPlay_NoURILoaded_ReturnsTransitionNotAvail(t *testing.T) {
	fake := &captureSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotStartCalls() != 0 {
		t.Errorf("StartSession called %d times; should not call when no URI",
			fake.snapshotStartCalls())
	}
}

func TestPlay_AlreadyPlayingOwnSession_NoOp(t *testing.T) {
	a, fake := avtPlayAdapter(t)

	// First Play kicks off the session.
	rr1 := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr1.Code != 200 {
		t.Fatalf("first Play status = %d, want 200", rr1.Code)
	}
	a.mu.Lock()
	ref := a.currentRef
	a.mu.Unlock()
	if ref == "" {
		t.Fatal("first Play left empty currentRef")
	}

	// Wire Status to report OUR session as active so the ownership
	// guard in the second Play recognizes it.
	fake.mu.Lock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref}
	}
	fake.mu.Unlock()

	// Second Play: state is PLAYING + own session → success no-op.
	rr2 := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr2.Code != 200 {
		t.Fatalf("second Play status = %d, want 200", rr2.Code)
	}
	if fake.snapshotStartCalls() != 1 {
		t.Errorf("StartSession called %d times after second Play, want 1 (no-op)",
			fake.snapshotStartCalls())
	}
}

func TestPlay_PausedOwnSession_CallsCorePlay(t *testing.T) {
	// Phase 2 has no Pause handler so this state isn't reachable from a
	// controller, but the code path still needs to exist for Phase 3.
	// Set up the state by directly mutating transportState to
	// PAUSED_PLAYBACK and pointing Status() at our ref.
	a, fake := avtPlayAdapter(t)

	ref := "dlna:abc123"
	a.mu.Lock()
	a.currentRef = ref
	a.transportState = transportStatePausedPlayback
	a.mu.Unlock()

	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref}
	}

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fake.snapshotPlayCalls() != 1 {
		t.Errorf("core.Play called %d times, want 1 (paused→resume branch)",
			fake.snapshotPlayCalls())
	}
	if fake.snapshotStartCalls() != 0 {
		t.Errorf("StartSession called %d times; paused→Play should NOT rebuild session",
			fake.snapshotStartCalls())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.transportState != transportStatePlaying {
		t.Errorf("transportState = %q, want %q after resume",
			a.transportState, transportStatePlaying)
	}
}

func TestPlay_BadInstanceID(t *testing.T) {
	a, _ := avtPlayAdapter(t)
	rr := avtSendPlay(t, a, "<InstanceID>1</InstanceID><Speed>1</Speed>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>718</errorCode>") {
		t.Errorf("body missing errorCode 718: %s", rr.Body.String())
	}
}

func TestPlay_BadSpeed(t *testing.T) {
	a, _ := avtPlayAdapter(t)
	for _, speed := range []string{"2", "0.5", "-1"} {
		rr := avtSendPlay(t, a,
			fmt.Sprintf("<InstanceID>0</InstanceID><Speed>%s</Speed>", speed))
		if rr.Code != 500 {
			t.Errorf("speed=%s status = %d, want 500", speed, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "<errorCode>717</errorCode>") {
			t.Errorf("speed=%s body missing errorCode 717: %s", speed, rr.Body.String())
		}
	}
}

func TestPlay_EmptySpeedAcceptedAsOne(t *testing.T) {
	// Some controllers omit Speed on the assumption the renderer
	// treats missing-Speed as 1. We honor that interpretation.
	a, _ := avtPlayAdapter(t)
	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID>")
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestPlay_ForeignSession_ReturnsTransitionNotAvail(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	fake.statusFn = func() core.SessionStatus {
		// Plex / Jellyfin / URL adapter holds the active session.
		// Our currentRef is still empty (no DLNA Play has succeeded
		// yet). Spec §Common Action Rules line 294: the foreign
		// session must remain untouched.
		return core.SessionStatus{AdapterRef: "plex:/library/metadata/1234"}
	}

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotStartCalls() != 0 {
		t.Errorf("StartSession called %d times; foreign reject should NOT preempt",
			fake.snapshotStartCalls())
	}
}

func TestPlay_StartSessionFailure_RollsBackRef(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	fake.startErr = errors.New("simulated probe failure")

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	// Spec line 321: a generic backend failure (no ErrProbeUnreachable
	// sentinel in the chain) maps to 501 Action Failed. The 716
	// "Resource not found" path is exercised by
	// TestPlay_StartSession_ProbeUnreachable_Returns716 below.
	if !strings.Contains(rr.Body.String(), "<errorCode>501</errorCode>") {
		t.Errorf("body missing errorCode 501: %s", rr.Body.String())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRef != "" {
		t.Errorf("currentRef = %q after StartSession failure; want empty (rollback)",
			a.currentRef)
	}
	if a.startInFlight {
		t.Error("startInFlight = true after StartSession failure; want false")
	}
	if a.transportState != transportStateStopped {
		t.Errorf("transportState = %q after failure; want STOPPED (no PLAYING transition)",
			a.transportState)
	}
	if a.lastError == "" {
		t.Error("lastError empty after failure; want a redacted message")
	}
	// Loaded URI must persist so an explicit retry (Play, or another
	// SetAVTransportURI replacing it) can proceed.
	if a.loadedURI == "" {
		t.Error("loadedURI cleared after failure; want preserved")
	}
}

func TestPlay_OnStopCallback_ResetsState(t *testing.T) {
	a, fake := avtPlayAdapter(t)

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("Play status = %d, want 200", rr.Code)
	}

	// Pull the OnStop closure off the captured request and invoke it
	// with a non-routine reason to exercise the lastError branch.
	onStop := fake.lastReq().OnStop
	if onStop == nil {
		t.Fatal("captured OnStop nil; cannot exercise callback")
	}
	onStop("plane error: ffmpeg exited 1")

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.transportState != transportStateStopped {
		t.Errorf("transportState = %q after OnStop; want STOPPED",
			a.transportState)
	}
	if a.currentRef != "" {
		t.Errorf("currentRef = %q after OnStop; want empty", a.currentRef)
	}
	if a.startInFlight {
		t.Error("startInFlight = true after OnStop; want false")
	}
	if !strings.Contains(a.lastError, "plane error") {
		t.Errorf("lastError = %q; want to contain reason text", a.lastError)
	}
}

func TestPlay_OnStopRoutineReason_NoLastError(t *testing.T) {
	// "stopped" is the routine controller-initiated teardown reason.
	// It MUST NOT populate lastError — that field flips
	// TransportStatus to ERROR_OCCURRED on subsequent polls.
	a, fake := avtPlayAdapter(t)

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("Play status = %d, want 200", rr.Code)
	}
	onStop := fake.lastReq().OnStop
	onStop("stopped")

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastError != "" {
		t.Errorf("lastError = %q after routine OnStop reason; want empty",
			a.lastError)
	}
	if a.transportState != transportStateStopped {
		t.Errorf("transportState = %q; want STOPPED", a.transportState)
	}
}

func TestPlay_OnStopStaleRef_NoOp(t *testing.T) {
	// Spec §Session Ref Lifecycle step 4 / step 7. A late OnStop from
	// a superseded session carries its captured ref by VALUE and
	// must no-op when currentRef has moved on.
	a, fake := avtPlayAdapter(t)

	// First fresh start. Capture its OnStop.
	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("first Play status = %d, want 200", rr.Code)
	}
	staleOnStop := fake.lastReq().OnStop
	a.mu.Lock()
	staleRef := a.currentRef
	a.mu.Unlock()

	// Simulate a fresh markStartInFlight as if a new session was
	// minted (e.g., via a new SetAVTransportURI + Play, or the data
	// plane preempted). currentRef advances to a new ref; the stale
	// OnStop's captured ref no longer matches.
	winnerRef := a.markStartInFlight()
	if staleRef == winnerRef {
		t.Fatal("test setup: two markStartInFlight calls produced same ref")
	}

	// Invoke the stale OnStop. It should compare-and-clear: ref mismatches → no-op.
	staleOnStop("plane error")

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRef != winnerRef {
		t.Errorf("stale OnStop corrupted currentRef: got %q, want winner %q",
			a.currentRef, winnerRef)
	}
	if !a.startInFlight {
		t.Error("stale OnStop cleared startInFlight; winner is still in-flight")
	}
	if a.lastError != "" {
		t.Errorf("stale OnStop wrote lastError = %q; should have no-op'd",
			a.lastError)
	}
}

// ---- Stop handler ----

func TestStop_OwnSession_CallsCoreStop(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("Play status = %d, want 200", rr.Code)
	}
	a.mu.Lock()
	ref := a.currentRef
	a.mu.Unlock()

	// Wire Status to report own session — Stop's ownership guard checks
	// AdapterRef against currentRef.
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref}
	}

	rr2 := avtSendStop(t, a, "<InstanceID>0</InstanceID>")
	if rr2.Code != 200 {
		t.Fatalf("Stop status = %d, want 200; body=%s", rr2.Code, rr2.Body.String())
	}
	if fake.snapshotStopCalls() != 1 {
		t.Errorf("core.Stop called %d times, want 1", fake.snapshotStopCalls())
	}
}

func TestStop_AlreadyStopped_NoOpSuccess(t *testing.T) {
	fake := &captureSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	// currentRef = "" (no session), transportState = STOPPED. Stop
	// should short-circuit to success without calling core.Stop.
	rr := avtSendStop(t, a, "<InstanceID>0</InstanceID>")
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fake.snapshotStopCalls() != 0 {
		t.Errorf("core.Stop called %d times; should short-circuit when already stopped",
			fake.snapshotStopCalls())
	}
}

func TestStop_BadInstanceID(t *testing.T) {
	a, _ := avtPlayAdapter(t)
	rr := avtSendStop(t, a, "<InstanceID>1</InstanceID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>718</errorCode>") {
		t.Errorf("body missing errorCode 718: %s", rr.Body.String())
	}
}

func TestStop_ForeignSession_ReturnsTransitionNotAvail(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: "plex:/library/metadata/1234"}
	}

	rr := avtSendStop(t, a, "<InstanceID>0</InstanceID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotStopCalls() != 0 {
		t.Errorf("core.Stop called %d times; foreign reject should NOT call core",
			fake.snapshotStopCalls())
	}
}

func TestStop_CoreStopFailure_Returns501(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("Play status = %d, want 200", rr.Code)
	}
	a.mu.Lock()
	ref := a.currentRef
	a.mu.Unlock()

	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref}
	}
	fake.stopErr = errors.New("simulated stop failure")

	rr2 := avtSendStop(t, a, "<InstanceID>0</InstanceID>")
	if rr2.Code != 500 {
		t.Errorf("status = %d, want 500", rr2.Code)
	}
	if !strings.Contains(rr2.Body.String(), "<errorCode>501</errorCode>") {
		t.Errorf("body missing errorCode 501: %s", rr2.Body.String())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastError == "" {
		t.Error("lastError empty after Stop failure; want descriptive message")
	}
	// Redaction discipline: the wrapped err.Error() must NOT leak into
	// lastError. A wrapped ffmpeg/dataplane error could surface
	// container paths or internal hostnames into a SOAP
	// GetTransportInfo response.
	if strings.Contains(a.lastError, "simulated stop failure") {
		t.Errorf("lastError = %q; raw err.Error() leaked into operator-visible field",
			a.lastError)
	}
}

// TestStop_LastError_DoesNotLeakRawError pins the redaction discipline:
// even a wrapped error that includes a path-like substring (the kind of
// thing an ffmpeg/dataplane teardown error tends to carry) must not be
// echoed verbatim into the operator-visible lastError. lastError is
// round-tripped to DLNA control points via GetTransportInfo's
// TransportStatus=ERROR_OCCURRED so any leak is observable to anyone on
// the LAN.
func TestStop_LastError_DoesNotLeakRawError(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("Play status = %d, want 200", rr.Code)
	}
	a.mu.Lock()
	ref := a.currentRef
	a.mu.Unlock()

	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref}
	}
	// A realistic worst-case error: wrapped ffmpeg stderr that echoes a
	// container-internal path. If lastError contained err.Error() this
	// substring would be visible to any DLNA control point.
	leakPayload := "/var/lib/groovy/socket: connection refused on 10.0.0.42:32100"
	fake.stopErr = errors.New("dataplane shutdown: " + leakPayload)

	rr2 := avtSendStop(t, a, "<InstanceID>0</InstanceID>")
	if rr2.Code != 500 {
		t.Fatalf("status = %d, want 500", rr2.Code)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.Contains(a.lastError, leakPayload) {
		t.Errorf("lastError = %q; leaked path/host fragment from wrapped error",
			a.lastError)
	}
	if !strings.HasPrefix(a.lastError, "Stop failed") {
		t.Errorf("lastError = %q; want it to start with the generic %q prefix",
			a.lastError, "Stop failed")
	}
}

// TestPlay_Resume_CoreFailure_RedactsLastError mirrors the Stop redaction
// guarantee for the PAUSED_PLAYBACK→PLAYING resume branch in handlePlay.
// Same leak vector (lastError → GetTransportInfo), same discipline.
func TestPlay_Resume_CoreFailure_RedactsLastError(t *testing.T) {
	a, fake := avtPlayAdapter(t)

	ref := "dlna:abc123"
	a.mu.Lock()
	a.currentRef = ref
	a.transportState = transportStatePausedPlayback
	a.mu.Unlock()

	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref}
	}
	leakPayload := "/var/lib/groovy/socket: connection refused on 10.0.0.42:32100"
	fake.playErr = errors.New("dataplane resume: " + leakPayload)

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 500 {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>501</errorCode>") {
		t.Errorf("body missing errorCode 501: %s", rr.Body.String())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastError == "" {
		t.Error("lastError empty after Play resume failure; want descriptive message")
	}
	if strings.Contains(a.lastError, leakPayload) {
		t.Errorf("lastError = %q; leaked path/host fragment from wrapped error",
			a.lastError)
	}
	if !strings.HasPrefix(a.lastError, "Play resume failed") {
		t.Errorf("lastError = %q; want it to start with %q prefix",
			a.lastError, "Play resume failed")
	}
}

// ---- autoplay ----

func TestSetAVTransportURI_AutoplayInvokesFreshStart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	fake := &captureSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	a.mu.Lock()
	a.cfg.AutoplayOnSetURI = true
	a.mu.Unlock()

	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/v.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
	))
	if rr.Code != 200 {
		t.Fatalf("SetAVTransportURI status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fake.snapshotStartCalls() != 1 {
		t.Errorf("StartSession called %d times after autoplay-enabled SetAVTransportURI, want 1",
			fake.snapshotStartCalls())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.transportState != transportStatePlaying {
		t.Errorf("transportState = %q after autoplay; want PLAYING", a.transportState)
	}
}

func TestSetAVTransportURI_AutoplayDisabled_DoesNotCallStartSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	fake := &captureSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	// AutoplayOnSetURI defaults to false; do not change it.

	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/v.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
	))
	if rr.Code != 200 {
		t.Fatalf("SetAVTransportURI status = %d, want 200", rr.Code)
	}
	if fake.snapshotStartCalls() != 0 {
		t.Errorf("StartSession called %d times when autoplay disabled, want 0",
			fake.snapshotStartCalls())
	}
}

func TestSetAVTransportURI_AutoplayFailure_StoresURIAndError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	fake := &captureSessionManager{
		startErr: errors.New("simulated probe failure"),
	}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	a.mu.Lock()
	a.cfg.AutoplayOnSetURI = true
	a.mu.Unlock()

	// SetAVTransportURI itself returns SOAP success even if autoplay
	// fails (best-effort semantic).
	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/v.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
	))
	if rr.Code != 200 {
		t.Fatalf("SetAVTransportURI status = %d, want 200 (autoplay failure must not fault Set)",
			rr.Code)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loadedURI == "" {
		t.Error("loadedURI cleared after autoplay failure; want preserved (next Play retries)")
	}
	if a.lastError == "" {
		t.Error("lastError empty after autoplay failure; want StartSession message")
	}
	if a.transportState != transportStateStopped {
		t.Errorf("transportState = %q after autoplay failure; want STOPPED",
			a.transportState)
	}
	if a.currentRef != "" {
		t.Errorf("currentRef = %q after autoplay failure; want empty (rollback)",
			a.currentRef)
	}
}

// ---- GetCurrentTransportActions ----

func TestGetCurrentTransportActions_NoURI_Empty(t *testing.T) {
	rr := runAVT(t, "GetCurrentTransportActions", "<InstanceID>0</InstanceID>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<Actions></Actions>") {
		t.Errorf("body missing empty Actions; got: %s", rr.Body.String())
	}
}

func TestGetCurrentTransportActions_StoppedWithURI_Play(t *testing.T) {
	a, _ := avtPlayAdapter(t) // pre-loads loadedURI; transportState defaults to STOPPED.
	req, rr := avtSOAPRequest(t, "GetCurrentTransportActions", "<InstanceID>0</InstanceID>")
	a.handleAVTransportSOAP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<Actions>Play</Actions>") {
		t.Errorf("body missing <Actions>Play</Actions>; got: %s", rr.Body.String())
	}
}

func TestGetCurrentTransportActions_Playing_Stop(t *testing.T) {
	a, _ := avtPlayAdapter(t)
	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("Play status = %d", rr.Code)
	}
	req2, rr2 := avtSOAPRequest(t, "GetCurrentTransportActions", "<InstanceID>0</InstanceID>")
	a.handleAVTransportSOAP(rr2, req2)
	if rr2.Code != 200 {
		t.Fatalf("status = %d, want 200", rr2.Code)
	}
	if !strings.Contains(rr2.Body.String(), "<Actions>Stop</Actions>") {
		t.Errorf("body missing <Actions>Stop</Actions>; got: %s", rr2.Body.String())
	}
}

// ---- GetTransportInfo reflects transportState ----

func TestGetTransportInfo_PlayingReflectsState(t *testing.T) {
	a, _ := avtPlayAdapter(t)
	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("Play status = %d", rr.Code)
	}
	req2, rr2 := avtSOAPRequest(t, "GetTransportInfo", "<InstanceID>0</InstanceID>")
	a.handleAVTransportSOAP(rr2, req2)
	if !strings.Contains(rr2.Body.String(), "<CurrentTransportState>PLAYING</CurrentTransportState>") {
		t.Errorf("body missing PLAYING state: %s", rr2.Body.String())
	}
}

// ---- helpers ----

// contains is a convenience for the policy assertions above. Avoids
// pulling in slices.Contains and keeps the test self-contained.
func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
