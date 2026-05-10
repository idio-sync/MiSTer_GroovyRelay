//go:build integration

package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/dlna"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// dlna_controls_test.go drives the DLNA SOAP control plane against a
// REAL core.Manager (not the stub from dlna_smoke_test.go). The Phase 3
// task plan asks for end-to-end coverage of:
//
//   1. Full sequence — SetAVTransportURI → Play → Pause → Resume →
//      Seek → Stop, asserting the FSM state at each transition.
//   2. Foreign-session ownership-guard rejection — a non-DLNA session
//      active in core, DLNA Pause/Stop/Seek must all return 701 and
//      leave the foreign session intact.
//   3. Concurrent Play race — two parallel Play SOAP requests, exactly
//      one wins and the other lands on a benign no-op or 701 inFlight
//      guard. The race detector must stay clean.
//   4. SetAVTransportURI redirect to a cloud-metadata endpoint —
//      validator's redirect re-classification must reject and emit 716,
//      no session started.
//
// All four exercise the real Manager + fake-mister harness. The MP4
// fixture is tests/integration/testdata/url/tiny.mp4 (1 s, h264+aac);
// 5s.mp4 lives at the same depth and is used for the seek test where
// we need a longer window.

// dlnaIntegrationFixture bundles the stable plumbing every DLNA control
// integration test needs: harness, manager, adapter, mux + httptest
// server, and a media-file origin server. Tests build this once via
// newDLNAIntegrationFixture, override the fields they want, and pull
// the cleanups in via t.Cleanup automatically (NewHarness already
// registers its own).
type dlnaIntegrationFixture struct {
	harness *Harness
	mgr     *core.Manager
	adapter *dlna.Adapter
	mux     *http.ServeMux
	dlnaSrv *httptest.Server
	origin  *httptest.Server
	mp4Path string
}

// newDLNAIntegrationFixture wires the integration harness for DLNA
// control flow tests. The static-IP DNS resolver override is the same
// trick dlna_smoke_test.go uses so the loopback origin server is
// classified as a private LAN target rather than rejected outright.
func newDLNAIntegrationFixture(t *testing.T, mp4Fixture string) *dlnaIntegrationFixture {
	t.Helper()

	mp4Path := filepath.Join("testdata", mp4Fixture)
	if _, err := os.Stat(mp4Path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}

	h := NewHarness(t)
	h.Listener.EnableACKs(true)
	mgr := core.NewManager(urlBridgeConfig(t), h.Sender)

	// Dial DNS to a stable private IP so the adapter's URL validator
	// accepts the loopback URL the httptest.Server below produces.
	restore := dlna.SetDNSResolverForTesting(dlna.StaticIPResolver("192.168.99.1"))
	t.Cleanup(restore)

	// Origin server that the SetAVTransportURI URL points at. HEAD has
	// to return 200 OK for the validator to accept the URL; GET must
	// stream the MP4 bytes so ffprobe sees a real media file.
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		f, err := os.Open(mp4Path)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.Copy(w, f)
	}))
	t.Cleanup(origin.Close)

	a, err := dlna.New(dlna.AdapterConfig{
		DeviceUUID: "33333333-4444-5555-6666-777777777777",
		HostIP:     "127.0.0.1",
		HTTPPort:   32500,
		Core:       mgr,
	})
	if err != nil {
		t.Fatalf("dlna.New: %v", err)
	}
	a.SetEnabled(true)

	mux := http.NewServeMux()
	a.MountPublicRoutes(mux)
	dlnaSrv := httptest.NewServer(mux)
	t.Cleanup(dlnaSrv.Close)
	t.Cleanup(func() { _ = mgr.Stop() })

	return &dlnaIntegrationFixture{
		harness: h,
		mgr:     mgr,
		adapter: a,
		mux:     mux,
		dlnaSrv: dlnaSrv,
		origin:  origin,
		mp4Path: mp4Path,
	}
}

// postSOAP issues a SOAP POST against the DLNA AVTransport control
// endpoint and returns the status + body string. action is the SOAP
// action name (Play/Pause/Stop/Seek/...); args is the argument fragment
// inside the action element.
func (f *dlnaIntegrationFixture) postSOAP(t *testing.T, action, args string) (int, string) {
	t.Helper()
	envelope := `<?xml version="1.0" encoding="utf-8"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body><u:` + action + ` xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` +
		args +
		`</u:` + action + `></s:Body></s:Envelope>`
	req, err := http.NewRequest(http.MethodPost,
		f.dlnaSrv.URL+"/dlna/control/AVTransport",
		strings.NewReader(envelope))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION",
		`"urn:schemas-upnp-org:service:AVTransport:1#`+action+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(body)
}

// waitForState polls core.Manager.Status() until State matches want or
// the deadline expires. Returns the last observed status for assertions.
func waitForState(t *testing.T, mgr *core.Manager, want core.State, timeout time.Duration) core.SessionStatus {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var st core.SessionStatus
	for time.Now().Before(deadline) {
		st = mgr.Status()
		if st.State == want {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitForState: want %v, got %v after %v", want, st.State, timeout)
	return st
}

// TestDLNAControls_FullSequence_SetURI_Play_Pause_Resume_Seek_Stop covers
// the canonical SOAP control flow end to end against a real core.Manager
// (no fakes): SetAVTransportURI → Play → Pause → Resume → Seek → Stop.
// Every transition is driven by an actual SOAP POST against the AVTransport
// control endpoint — no direct core.Manager calls — so this is the proof
// that the entire DLNA SOAP surface composes correctly with the core FSM
// + data plane.
//
// Originally this lived as two tests (Pause+Resume and Seek-only) split
// around a regression in core.startPlaneLocked: same-session replay
// (Play-resume, SeekTo) was unconditionally firing the active session's
// OnStop with reason="preempted", and the DLNA onStopForRef closure
// interpreted that as session-end and cleared currentRef. The P3.3 fix
// gates the OnStop fire on a genuine AdapterRef change, so same-session
// replay no longer clobbers adapter state — making this single
// end-to-end test possible.
func TestDLNAControls_FullSequence_SetURI_Play_Pause_Resume_Seek_Stop(t *testing.T) {
	f := newDLNAIntegrationFixture(t, "10s.mp4")

	mediaURL := f.origin.URL + "/movie.mp4"
	didl := `&lt;DIDL-Lite xmlns=&quot;urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/&quot;&gt;` +
		`&lt;item&gt;&lt;res duration=&quot;0:00:30.000&quot;&gt;` + mediaURL + `&lt;/res&gt;` +
		`&lt;/item&gt;&lt;/DIDL-Lite&gt;`

	// --- 1. SetAVTransportURI: real validator + DIDL parse + store.
	status, body := f.postSOAP(t, "SetAVTransportURI",
		"<InstanceID>0</InstanceID>"+
			"<CurrentURI>"+mediaURL+"</CurrentURI>"+
			"<CurrentURIMetaData>"+didl+"</CurrentURIMetaData>")
	if status != http.StatusOK {
		t.Fatalf("SetAVTransportURI status = %d, want 200; body=%s", status, snippet(body))
	}

	// --- 2. Play: probe + StartSession + plane spin-up.
	status, body = f.postSOAP(t, "Play", "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if status != http.StatusOK {
		t.Fatalf("Play status = %d, want 200; body=%s", status, snippet(body))
	}
	st := waitForState(t, f.mgr, core.StatePlaying, 10*time.Second)
	if !strings.HasPrefix(st.AdapterRef, "dlna:") {
		t.Errorf("StartSession.AdapterRef = %q, want dlna:* prefix", st.AdapterRef)
	}
	originalRef := st.AdapterRef

	// --- 3. GetPositionInfo: Track=1, TrackDuration parsed from the
	//        live probe (10s, formatted as 00:00:10).
	status, body = f.postSOAP(t, "GetPositionInfo", "<InstanceID>0</InstanceID>")
	if status != http.StatusOK {
		t.Fatalf("GetPositionInfo status = %d", status)
	}
	if !strings.Contains(body, "<Track>1</Track>") {
		t.Errorf("GetPositionInfo Track != 1: %s", snippet(body))
	}
	// Live probe surfaces 10s; metadata says 30s. Live wins (own session).
	if !strings.Contains(body, "<TrackDuration>00:00:10</TrackDuration>") {
		t.Errorf("GetPositionInfo TrackDuration != 00:00:10: %s", snippet(body))
	}

	// --- 4. Pause. Real core.Manager tears down the plane and FSM
	//        flips to Paused. core.Manager.Pause does NOT fire OnStop
	//        (intentional cancellation is treated as a paused state, not
	//        a session-end), so currentRef is preserved.
	status, body = f.postSOAP(t, "Pause", "<InstanceID>0</InstanceID>")
	if status != http.StatusOK {
		t.Fatalf("Pause status = %d, want 200; body=%s", status, snippet(body))
	}
	waitForState(t, f.mgr, core.StatePaused, 5*time.Second)

	status, body = f.postSOAP(t, "GetTransportInfo", "<InstanceID>0</InstanceID>")
	if status != http.StatusOK {
		t.Fatalf("GetTransportInfo status = %d", status)
	}
	if !strings.Contains(body, "<CurrentTransportState>PAUSED_PLAYBACK</CurrentTransportState>") {
		t.Errorf("GetTransportInfo state != PAUSED_PLAYBACK: %s", snippet(body))
	}

	// --- 5. Play (resume). FSM flips back to Playing. After the P3.3
	//        fix, same-session replay (Play with the same AdapterRef
	//        carried forward by m.active.req) does NOT fire OnStop —
	//        currentRef is preserved across the resume.
	status, body = f.postSOAP(t, "Play", "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if status != http.StatusOK {
		t.Fatalf("Play resume status = %d, want 200; body=%s", status, snippet(body))
	}
	st = waitForState(t, f.mgr, core.StatePlaying, 10*time.Second)
	if st.AdapterRef != originalRef {
		t.Errorf("after resume, AdapterRef = %q, want unchanged %q",
			st.AdapterRef, originalRef)
	}

	// --- 6. Seek to 00:00:02 via SOAP. core.SeekTo respawns the plane
	//        at the new offset; FSM stays Playing. After the P3.3 fix,
	//        SeekTo no longer fires OnStop("preempted") on the same-ref
	//        replay, so the adapter's currentRef survives the seek and
	//        subsequent SOAP actions still address our session.
	status, body = f.postSOAP(t, "Seek",
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:02</Target>")
	if status != http.StatusOK {
		t.Fatalf("Seek status = %d, want 200; body=%s", status, snippet(body))
	}
	st = waitForState(t, f.mgr, core.StatePlaying, 10*time.Second)
	if st.AdapterRef != originalRef {
		t.Errorf("after seek, AdapterRef = %q, want unchanged %q",
			st.AdapterRef, originalRef)
	}

	// --- 7. Stop via SOAP. Now possible end-to-end because steps 5–6
	//        no longer clear the adapter's currentRef. handleStop
	//        snapshots ownership, calls core.Stop, and the OnStop
	//        callback drives the transportState → STOPPED transition.
	status, body = f.postSOAP(t, "Stop", "<InstanceID>0</InstanceID>")
	if status != http.StatusOK {
		t.Fatalf("Stop status = %d, want 200; body=%s", status, snippet(body))
	}
	waitForState(t, f.mgr, core.StateIdle, 5*time.Second)
}

// TestDLNAControls_NoDLNASessionWithForeignActive_RejectsControlActions
// covers the narrow no-DLNA-session-loaded shape of the ownership guard:
// a foreign (e.g. plex:) session is started directly via
// core.Manager.StartSession without DLNA ever calling SetAVTransportURI,
// so the DLNA adapter's currentRef stays empty. SOAP Pause/Stop/Seek
// then short-circuit on the empty-currentRef branch and return 701
// without disturbing the foreign session.
//
// Note this does NOT exercise the "currentRef set AND foreign live core
// ref" branch of the guard. Constructing that state requires a real
// preempt sequence (DLNA Plays, then a foreign adapter calls
// StartSession to take core, then user Pause/Stop/Seek hits DLNA before
// DLNA's OnStop has cleared currentRef) which is racy to fixture and
// would be a fragile flake. The user-visible promise that matters here
// — a plex play cannot be paused by a DLNA controller — is fully
// validated by the empty-currentRef path below.
func TestDLNAControls_NoDLNASessionWithForeignActive_RejectsControlActions(t *testing.T) {
	f := newDLNAIntegrationFixture(t, "5s.mp4")

	// Foreign session — no DLNA involvement. plex: prefix is the
	// canonical foreign-adapter ref the spec calls out for the
	// ownership guard. Capabilities allow Pause+Seek so we can confirm
	// the foreign session is still pausable from its own adapter
	// (we only assert it's untouched here).
	foreignRef := "plex:test-session-1"
	plexReq := core.SessionRequest{
		StreamURL:    f.origin.URL + "/foreign.mp4",
		AdapterRef:   foreignRef,
		DirectPlay:   true,
		Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
	}
	if err := f.mgr.StartSession(plexReq); err != nil {
		t.Fatalf("foreign StartSession: %v", err)
	}
	waitForState(t, f.mgr, core.StatePlaying, 10*time.Second)

	// We never called SetAVTransportURI on the DLNA adapter, so its
	// loadedURI is empty. Pause/Stop/Seek must short-circuit on the
	// no-current-ref branch — that's still 701 (no DLNA session to
	// pause) per the spec ownership guard. To exercise the OWNED-but-
	// foreign-current-session path we'd need an in-flight DLNA ref AND
	// a different live core ref, which only happens in the brief race
	// window inside Play. The "no-DLNA-session, foreign live" shape
	// here covers the user-visible promise: a plex play does not let
	// a DLNA controller pause it.
	for _, action := range []struct {
		name string
		args string
	}{
		{"Pause", "<InstanceID>0</InstanceID>"},
		{"Stop", "<InstanceID>0</InstanceID>"},
		{"Seek", "<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:01</Target>"},
	} {
		status, body := f.postSOAP(t, action.name, action.args)
		if status != http.StatusInternalServerError {
			t.Errorf("%s status = %d, want 500 (UPnP fault); body=%s",
				action.name, status, snippet(body))
		}
		if !strings.Contains(body, "<errorCode>701</errorCode>") {
			t.Errorf("%s body missing errorCode 701 (Transition not available): %s",
				action.name, snippet(body))
		}
	}

	// Plex session is still live — ownership guard didn't touch core.
	st := f.mgr.Status()
	if st.AdapterRef != foreignRef {
		t.Errorf("after DLNA SOAP rejection, AdapterRef = %q, want %q",
			st.AdapterRef, foreignRef)
	}
	if st.State != core.StatePlaying {
		t.Errorf("after DLNA SOAP rejection, State = %v, want Playing", st.State)
	}
}

// TestDLNAControls_ConcurrentPlayRace fires two Play SOAP requests in
// parallel and asserts the system is well-formed afterwards: at most
// one core.Manager session, no orphan refs, no panic. Race detector
// must stay clean (-race is CI's default for this suite).
func TestDLNAControls_ConcurrentPlayRace(t *testing.T) {
	f := newDLNAIntegrationFixture(t, "5s.mp4")

	mediaURL := f.origin.URL + "/movie.mp4"
	status, body := f.postSOAP(t, "SetAVTransportURI",
		"<InstanceID>0</InstanceID>"+
			"<CurrentURI>"+mediaURL+"</CurrentURI>"+
			"<CurrentURIMetaData></CurrentURIMetaData>")
	if status != http.StatusOK {
		t.Fatalf("SetAVTransportURI status = %d, want 200: %s", status, snippet(body))
	}

	// Fire N concurrent Play requests. The exact number doesn't matter
	// — two is the minimum to provoke a race, but a handful gives a
	// wider window for the inFlight guard to catch the slower ones.
	const n = 4
	var wg sync.WaitGroup
	successes := atomic.Int32{}
	faults701 := atomic.Int32{}
	otherFaults := atomic.Int32{}

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			s, b := f.postSOAP(t, "Play", "<InstanceID>0</InstanceID><Speed>1</Speed>")
			switch {
			case s == http.StatusOK:
				successes.Add(1)
			case s == http.StatusInternalServerError && strings.Contains(b, "<errorCode>701</errorCode>"):
				faults701.Add(1)
			default:
				otherFaults.Add(1)
				t.Logf("unexpected response: status=%d body=%s", s, snippet(b))
			}
		}()
	}
	wg.Wait()

	total := successes.Load() + faults701.Load() + otherFaults.Load()
	if int(total) != n {
		t.Errorf("concurrent Play: %d responses recorded, expected %d", total, n)
	}
	if otherFaults.Load() != 0 {
		t.Errorf("concurrent Play: %d unexpected fault codes (not 200 or 701)", otherFaults.Load())
	}
	if successes.Load() < 1 {
		t.Errorf("concurrent Play: 0 successes; expected at least 1 winner")
	}

	// The system MUST settle with at most one active core session and
	// the FSM either Playing (winner spun up the plane) or Idle (a
	// race left things idle, which is also acceptable per the spec).
	waitForState(t, f.mgr, core.StatePlaying, 10*time.Second)
	st := f.mgr.Status()
	if !strings.HasPrefix(st.AdapterRef, "dlna:") {
		t.Errorf("post-race AdapterRef = %q, want dlna:* prefix", st.AdapterRef)
	}
}

// TestDLNAControls_RedirectToCloudMetadata_Returns716 stands up an HTTP
// server that 302's to the AWS cloud-metadata endpoint
// (169.254.169.254). The DLNA URL validator's redirect re-classification
// must reject the link-local destination and emit a 716 SOAP fault. No
// session must be started in core.Manager.
func TestDLNAControls_RedirectToCloudMetadata_Returns716(t *testing.T) {
	f := newDLNAIntegrationFixture(t, "5s.mp4")

	// Redirector — 302's to the metadata endpoint. The static-IP DNS
	// resolver override above maps the loopback host to a private LAN
	// IP, so the FIRST hop classifies as private-LAN. The redirect
	// target's literal IP (169.254.169.254) skips DNS and is checked
	// directly against the address policy, which rejects link-local.
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://169.254.169.254/latest/meta-data/")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(redir.Close)

	mediaURL := redir.URL + "/redirect-me.mp4"
	status, body := f.postSOAP(t, "SetAVTransportURI",
		"<InstanceID>0</InstanceID>"+
			"<CurrentURI>"+mediaURL+"</CurrentURI>"+
			"<CurrentURIMetaData></CurrentURIMetaData>")
	if status != http.StatusInternalServerError {
		t.Fatalf("SetAVTransportURI status = %d, want 500 (UPnP fault); body=%s",
			status, snippet(body))
	}
	if !strings.Contains(body, "<errorCode>716</errorCode>") {
		t.Errorf("body missing errorCode 716 (Resource not found):\n%s", snippet(body))
	}

	// Core never received the URL — Status reports idle.
	st := f.mgr.Status()
	if st.State != core.StateIdle {
		t.Errorf("after rejected SetAVTransportURI, State = %v, want Idle", st.State)
	}
	if st.AdapterRef != "" {
		t.Errorf("after rejected SetAVTransportURI, AdapterRef = %q, want empty", st.AdapterRef)
	}

	// And a follow-up Play with no URI loaded must still 701 (sanity:
	// the rejected SetAVTransportURI didn't accidentally store the URL).
	status, body = f.postSOAP(t, "Play", "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if status != http.StatusInternalServerError {
		t.Errorf("post-rejection Play status = %d, want 500", status)
	}
	if !strings.Contains(body, "<errorCode>701</errorCode>") {
		t.Errorf("post-rejection Play missing 701: %s", snippet(body))
	}
}

