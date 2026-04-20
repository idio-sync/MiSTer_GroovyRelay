//go:build integration

package integration

import (
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// scenarioHarness wires core.Manager into an in-process fake-mister so the
// scripted scenarios below can exercise the full control plane (StartSession
// / Pause / Play / SeekTo / Stop / preempt) against real UDP loopback.
//
// The fake-mister Listener is configured with EnableACKs(true) so the
// Plane's INIT handshake (SendInitAwaitACK, 60 ms timeout) completes and
// the pump loop reaches BLIT/AUDIO streaming. Integration tests that
// exercise the Plane directly (plane_test.go) use a one-shot ACK stub
// instead; EnableACKs is the scalable equivalent for scenarios that
// respawn the plane (Seek / Preempt / Play-after-Pause each re-INIT).
type scenarioHarness struct {
	Manager  *core.Manager
	Listener *fakemister.Listener
	Sender   *groovynet.Sender
	Recorder *fakemister.Recorder
	cleanup  func()
}

// newScenarioHarness stands up a fresh loopback pair per test. The Listener
// binds an ephemeral port; the Sender targets that port with SourcePort=0
// (OS-assigned — scenarios don't depend on a stable source port since they
// don't reuse the sender across test cases). ACKs are enabled with
// audioReady=true so the Plane's audio pump path runs.
func newScenarioHarness(t *testing.T) *scenarioHarness {
	t.Helper()
	l, err := fakemister.NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.EnableACKs(true)

	events := make(chan fakemister.Command, 4096)
	fieldsCh := make(chan fakemister.FieldEvent, 4096)
	audios := make(chan fakemister.AudioEvent, 4096)
	rec := fakemister.NewRecorder()
	recDone := make(chan struct{})
	runDone := make(chan struct{})
	drainDone := make(chan struct{})
	audioFanDone := make(chan struct{})

	go func() {
		for c := range events {
			rec.Record(c)
		}
		close(recDone)
	}()
	// Drain fields — scenario assertions only count BLIT headers (via Command records),
	// not reassembled field payloads.
	go func() {
		for range fieldsCh {
		}
		close(drainDone)
	}()
	// Fan AudioEvents into synthetic Commands with AudioPayload so Recorder.audioBytes increments.
	go func() {
		for ev := range audios {
			events <- fakemister.Command{
				Type:         groovy.CmdAudio,
				AudioPayload: &fakemister.AudioPayload{PCM: ev.PCM},
			}
		}
		close(audioFanDone)
	}()
	fieldSizeFn := func() uint32 { return 720 * 240 * 3 } // RAW BLIT fallback size
	go func() {
		l.RunWithFields(events, fieldsCh, audios, fieldSizeFn)
		close(runDone)
	}()

	addr := l.Addr().(*net.UDPAddr)
	sender, err := groovynet.NewSender("127.0.0.1", addr.Port, 0)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}

	cfg := &config.Config{
		MisterHost:          "127.0.0.1",
		MisterPort:          addr.Port,
		SourcePort:          0,
		Modeline:            "NTSC_480i",
		InterlaceFieldOrder: "tff",
		AspectMode:          "letterbox",
		RGBMode:             "rgb888",
		LZ4Enabled:          true,
		AudioSampleRate:     48000,
		AudioChannels:       2,
	}
	mgr := core.NewManager(cfg, sender)

	cleaned := false
	var cleanMu sync.Mutex
	cleanup := func() {
		cleanMu.Lock()
		defer cleanMu.Unlock()
		if cleaned {
			return
		}
		cleaned = true
		_ = mgr.Stop()
		sender.Close()
		l.Close()
		<-runDone
		// Listener has exited; close the downstream channels so the fan-in
		// and drain goroutines terminate, then close events and wait for
		// the recorder goroutine.
		close(fieldsCh)
		close(audios)
		<-drainDone
		<-audioFanDone
		close(events)
		<-recDone
	}
	t.Cleanup(cleanup)

	return &scenarioHarness{
		Manager:  mgr,
		Listener: l,
		Sender:   sender,
		Recorder: rec,
		cleanup:  cleanup,
	}
}

// defaultRequest is a SessionRequest with both Pause and Seek capabilities
// advertised — every scenario except Cast exercises those paths. AdapterRef
// is stamped per-scenario so preempt assertions can compare references.
func defaultRequest(streamURL, ref string) core.SessionRequest {
	return core.SessionRequest{
		StreamURL:    streamURL,
		AdapterRef:   ref,
		DirectPlay:   true,
		Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
	}
}

// TestScenario_Cast streams a ~5 s clip end-to-end and asserts the BLIT
// count lands in the expected band. Per the plan, 5 s × 59.94 fields/s ≈
// 300 fields; tolerate ±15 % to absorb startup lag and the natural ramp-up
// while ffmpeg's decoder pipeline primes.
//
// Audio byte count is also checked: 48 kHz × 2 ch × 2 B/sample × 5 s ≈
// 960 000 B, but the Plane only sends AUDIO while ACK bit 6 is set AND a
// PCM chunk is ready (default-select), so realistic throughput runs lower.
// Assert a lower bound only (>= 200 000 B) so this test remains stable
// across ffmpeg versions and CI load.
func TestScenario_Cast(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg scenarios require Unix ExtraFiles; run on Linux/CI")
	}

	sample := ensureSampleMP4(t, "5s.mp4", 5)
	h := newScenarioHarness(t)

	if err := h.Manager.StartSession(defaultRequest(sample, "cast-clip-1")); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for the plane to finish naturally. The Manager's exit goroutine
	// clears its plane pointer + fires EvEOF on clean exit, so we poll
	// Status until the FSM returns to Idle (bounded by a generous timeout).
	waitIdle(t, h, 10*time.Second)

	// Settling delay for trailing datagrams.
	time.Sleep(200 * time.Millisecond)

	snap := h.Recorder.Snapshot()
	blits := snap.Counts[groovy.CmdBlitFieldVSync]
	if blits < 255 || blits > 345 {
		t.Errorf("cast: expected ~300 blits, got %d", blits)
	}
	if snap.Counts[groovy.CmdInit] < 1 {
		t.Errorf("cast: expected at least 1 INIT, got %d", snap.Counts[groovy.CmdInit])
	}
	if snap.Counts[groovy.CmdSwitchres] < 1 {
		t.Errorf("cast: expected at least 1 SWITCHRES, got %d", snap.Counts[groovy.CmdSwitchres])
	}
	if snap.Counts[groovy.CmdClose] < 1 {
		t.Errorf("cast: expected at least 1 CLOSE, got %d", snap.Counts[groovy.CmdClose])
	}
	if snap.AudioBytes < 200_000 {
		t.Errorf("cast: audio byte count low (%d); expected >= 200_000", snap.AudioBytes)
	}

	// Inter-field timing: the Plane drives the pump at 59.94 Hz, so
	// consecutive BLIT arrivals on loopback should land roughly one field
	// period apart. See assertInterFieldTiming for the acceptance band.
	assertInterFieldTiming(t, snap.FieldTimestamps)
}

// TestScenario_Seek kicks off a long clip, waits for the plane to ramp up,
// then seeks mid-playback. The Manager's SeekTo respawns the plane, which
// re-runs the INIT handshake — so the recorder should see >= 2 INIT
// commands across the session.
func TestScenario_Seek(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg scenarios require Unix ExtraFiles; run on Linux/CI")
	}

	sample := ensureSampleMP4(t, "10s.mp4", 10)
	h := newScenarioHarness(t)

	if err := h.Manager.StartSession(defaultRequest(sample, "seek-clip")); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Let the first plane warm up and ship at least one INIT + SWITCHRES.
	time.Sleep(1 * time.Second)

	if err := h.Manager.SeekTo(2000); err != nil {
		t.Fatalf("seek: %v", err)
	}

	// Let the respawned plane complete.
	waitIdle(t, h, 15*time.Second)
	time.Sleep(200 * time.Millisecond)

	snap := h.Recorder.Snapshot()
	if got := snap.Counts[groovy.CmdInit]; got < 2 {
		t.Errorf("seek: expected >= 2 INITs (pre + post seek), got %d", got)
	}
	if got := snap.Counts[groovy.CmdSwitchres]; got < 2 {
		t.Errorf("seek: expected >= 2 SWITCHRES (pre + post seek), got %d", got)
	}
}

// TestScenario_Pause casts a clip, pauses 1 s in, asserts the BLIT count
// stops growing, then resumes and asserts it grows again. The acceptance
// band is generous: the pump loop emits at ~60 Hz, so a 500 ms window
// should catch a delta of ~30 BLITs when playing and 0 when paused.
func TestScenario_Pause(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg scenarios require Unix ExtraFiles; run on Linux/CI")
	}

	sample := ensureSampleMP4(t, "10s.mp4", 10)
	h := newScenarioHarness(t)

	if err := h.Manager.StartSession(defaultRequest(sample, "pause-clip")); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(1 * time.Second)

	if err := h.Manager.Pause(); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// Settle: allow in-flight packets to land, then take the reference
	// count. 200 ms is enough for UDP loopback + recorder goroutine.
	time.Sleep(200 * time.Millisecond)
	beforeResume := h.Recorder.Snapshot().Counts[groovy.CmdBlitFieldVSync]

	// Hold the pause for 500 ms and verify the recorder did not grow
	// meaningfully — the Plane is down, so no BLITs should arrive. Allow
	// a small slack (<= 2 BLITs) for any packet that was in-flight at
	// pause time.
	time.Sleep(500 * time.Millisecond)
	duringPause := h.Recorder.Snapshot().Counts[groovy.CmdBlitFieldVSync]
	if duringPause-beforeResume > 2 {
		t.Errorf("pause: BLIT count grew by %d during pause window; plane should be down",
			duringPause-beforeResume)
	}

	if err := h.Manager.Play(); err != nil {
		t.Fatalf("play: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	afterResume := h.Recorder.Snapshot().Counts[groovy.CmdBlitFieldVSync]
	if afterResume-duringPause < 10 {
		t.Errorf("resume: expected BLIT count to grow post-Play, delta=%d",
			afterResume-duringPause)
	}
}

// TestScenario_Preempt starts clip A, and mid-playback starts clip B on the
// same Manager. StartSession preempts the running plane, cancels its ctx,
// and respawns — so the recorder should see two distinct INIT/SWITCHRES
// pairs. The Manager's internal plane-pointer swap guarantees clip A's
// goroutine exits before clip B's INIT is sent.
func TestScenario_Preempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg scenarios require Unix ExtraFiles; run on Linux/CI")
	}

	sampleA := ensureSampleMP4(t, "10s.mp4", 10)
	sampleB := ensureSampleMP4(t, "5s.mp4", 5)
	h := newScenarioHarness(t)

	if err := h.Manager.StartSession(defaultRequest(sampleA, "clip-A")); err != nil {
		t.Fatalf("start A: %v", err)
	}
	time.Sleep(1 * time.Second)

	if err := h.Manager.StartSession(defaultRequest(sampleB, "clip-B")); err != nil {
		t.Fatalf("start B: %v", err)
	}

	waitIdle(t, h, 15*time.Second)
	time.Sleep(200 * time.Millisecond)

	snap := h.Recorder.Snapshot()
	if got := snap.Counts[groovy.CmdInit]; got < 2 {
		t.Errorf("preempt: expected >= 2 INITs (A + B), got %d", got)
	}
	if got := snap.Counts[groovy.CmdClose]; got < 1 {
		t.Errorf("preempt: expected >= 1 CLOSE, got %d", got)
	}
	// The AdapterRef of the currently-active (or most-recent) session
	// should be clip-B.
	st := h.Manager.Status()
	if st.AdapterRef != "" && st.AdapterRef != "clip-B" {
		t.Errorf("preempt: status.AdapterRef = %q, want clip-B or empty", st.AdapterRef)
	}
}

// TestScenario_Stop casts a clip and stops it mid-playback. The recorder
// must see at least one CLOSE (emitted by the plane's ctx.Done branch).
func TestScenario_Stop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live FFmpeg scenarios require Unix ExtraFiles; run on Linux/CI")
	}

	sample := ensureSampleMP4(t, "10s.mp4", 10)
	h := newScenarioHarness(t)

	if err := h.Manager.StartSession(defaultRequest(sample, "stop-clip")); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(1 * time.Second)

	if err := h.Manager.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	snap := h.Recorder.Snapshot()
	if snap.Counts[groovy.CmdClose] < 1 {
		t.Errorf("stop: expected >= 1 CLOSE, got %d", snap.Counts[groovy.CmdClose])
	}
	st := h.Manager.Status()
	if st.State != core.StateIdle {
		t.Errorf("stop: FSM state = %q, want %q", st.State, core.StateIdle)
	}
}

// waitIdle polls Manager.Status() until the FSM returns to Idle or the
// timeout elapses. Used by scenarios that let the plane exit naturally
// (Cast, Seek, Preempt) rather than calling Stop().
func waitIdle(t *testing.T, h *scenarioHarness, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if h.Manager.Status().State == core.StateIdle {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("plane did not return to Idle within %v (state=%q)",
		timeout, h.Manager.Status().State)
}
