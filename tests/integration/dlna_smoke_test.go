//go:build integration

package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/dlna"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// dlnaStubCore is a configurable SessionManager for the DLNA smoke
// tests. The Phase-1 surface used a no-op stub, but Phase 2 needs to
// observe StartSession / Stop calls so the smoke can confirm Play /
// Stop SOAP actions actually drive the adapter→core wiring.
//
// startReqs records every SessionRequest passed to StartSession;
// status drives Status() returns; method counters confirm call shape.
// All fields are guarded by mu so a future test that exercises the
// OnStop goroutine can read the captured request without a race.
type dlnaStubCore struct {
	mu sync.Mutex

	statusFn func() core.SessionStatus

	startReqs  []core.SessionRequest
	startCalls int
	stopCalls  int
}

func (s *dlnaStubCore) StartSession(req core.SessionRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startReqs = append(s.startReqs, req)
	s.startCalls++
	return nil
}

func (s *dlnaStubCore) Status() core.SessionStatus {
	if s.statusFn != nil {
		return s.statusFn()
	}
	return core.SessionStatus{}
}

func (*dlnaStubCore) Pause() error { return nil }
func (*dlnaStubCore) Play() error  { return nil }

func (s *dlnaStubCore) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopCalls++
	return nil
}

func (*dlnaStubCore) SeekTo(int) error { return nil }

// snapshotStartCalls / snapshotStopCalls / lastStartReq are read
// helpers used by the Phase 2 smoke. Each takes the mutex so a
// goroutine running the OnStop callback (which the adapter fires
// asynchronously when core.Stop succeeds) can't race with a test's
// assertion read.
func (s *dlnaStubCore) snapshotStartCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startCalls
}

func (s *dlnaStubCore) snapshotStopCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopCalls
}

func (s *dlnaStubCore) lastStartReq() core.SessionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.startReqs) == 0 {
		return core.SessionRequest{}
	}
	return s.startReqs[len(s.startReqs)-1]
}

// TestDLNA_Smoke_Phase1HTTPSurface exercises the full Phase 1 HTTP
// surface end-to-end through a real httptest.Server: the four
// descriptor GETs, two query-only SOAP POSTs (one per service used in
// this assertion), and an event-path SUBSCRIBE that should still
// return 503 (eventing lands in Phase 4).
//
// SSDP / multicast is intentionally out of scope — that needs a
// reachable network, root on POSIX, and is exercised by the SSDP unit
// tests under internal/adapters/dlna. This smoke confirms only that
// the adapter is wired into the shared mux correctly: routes register,
// descriptors render, SOAP dispatch dispatches, and the Phase-1 stub
// for events stays a stub.
func TestDLNA_Smoke_Phase1HTTPSurface(t *testing.T) {
	const (
		fakeUUID = "11111111-2222-3333-4444-555555555555"
		fakeIP   = "127.0.0.1"
		fakePort = 32500
	)

	a, err := dlna.New(dlna.AdapterConfig{
		DeviceUUID: fakeUUID,
		HostIP:     fakeIP,
		HTTPPort:   fakePort,
		Core:       &dlnaStubCore{},
	})
	if err != nil {
		t.Fatalf("dlna.New: %v", err)
	}

	mux := http.NewServeMux()
	a.MountPublicRoutes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// --- Descriptor GETs ---
	descriptorTests := []struct {
		path        string
		bodyMustHas []string // at least one substring expected in the body
	}{
		{
			path: "/dlna/device.xml",
			// DefaultConfig() seeds DeviceName="MiSTer" — the descriptor
			// must surface that as friendlyName.
			bodyMustHas: []string{"MiSTer"},
		},
		// SCPD bodies don't embed the service name verbatim — they use
		// the generic schema URN. Pick a distinctive action name per
		// service as the discriminator instead.
		{path: "/dlna/AVTransport.xml", bodyMustHas: []string{"<scpd", "SetAVTransportURI"}},
		{path: "/dlna/ConnectionManager.xml", bodyMustHas: []string{"<scpd", "GetProtocolInfo"}},
		{path: "/dlna/RenderingControl.xml", bodyMustHas: []string{"<scpd", "GetVolume"}},
	}
	for _, tc := range descriptorTests {
		tc := tc
		t.Run("GET "+tc.path, func(t *testing.T) {
			resp, err := http.Get(srv.URL + tc.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: status = %d, want 200", tc.path, resp.StatusCode)
			}
			ct := resp.Header.Get("Content-Type")
			if !strings.Contains(ct, "text/xml") {
				t.Fatalf("GET %s: Content-Type = %q, want text/xml prefix", tc.path, ct)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("GET %s: read body: %v", tc.path, err)
			}
			s := string(body)
			// All descriptors must declare some XML.
			if !strings.Contains(s, "<?xml") {
				t.Fatalf("GET %s: body missing <?xml prologue:\n%s", tc.path, snippet(s))
			}
			for _, want := range tc.bodyMustHas {
				if !strings.Contains(s, want) {
					t.Fatalf("GET %s: body missing %q:\n%s", tc.path, want, snippet(s))
				}
			}
		})
	}

	// --- ConnectionManager:GetProtocolInfo SOAP ---
	t.Run("SOAP ConnectionManager GetProtocolInfo", func(t *testing.T) {
		envelope := `<?xml version="1.0" encoding="utf-8"?>` +
			`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
			`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body>` +
			`<u:GetProtocolInfo xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1"/>` +
			`</s:Body></s:Envelope>`

		req, err := http.NewRequest(http.MethodPost,
			srv.URL+"/dlna/control/ConnectionManager",
			strings.NewReader(envelope))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
		req.Header.Set("SOAPACTION",
			`"urn:schemas-upnp-org:service:ConnectionManager:1#GetProtocolInfo"`)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		s := string(body)
		// Both Source and Sink out-args must appear, and at least one of
		// the eight Sink protocolInfo entries must be present.
		for _, want := range []string{"Source", "Sink", "http-get:*:video/mp4:*"} {
			if !strings.Contains(s, want) {
				t.Fatalf("body missing %q:\n%s", want, snippet(s))
			}
		}
	})

	// --- AVTransport:GetTransportInfo SOAP ---
	t.Run("SOAP AVTransport GetTransportInfo", func(t *testing.T) {
		envelope := `<?xml version="1.0" encoding="utf-8"?>` +
			`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
			`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body>` +
			`<u:GetTransportInfo xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` +
			`<InstanceID>0</InstanceID>` +
			`</u:GetTransportInfo>` +
			`</s:Body></s:Envelope>`

		req, err := http.NewRequest(http.MethodPost,
			srv.URL+"/dlna/control/AVTransport",
			strings.NewReader(envelope))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
		req.Header.Set("SOAPACTION",
			`"urn:schemas-upnp-org:service:AVTransport:1#GetTransportInfo"`)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		s := string(body)
		// Phase 1 always reports STOPPED.
		if !strings.Contains(s, "STOPPED") {
			t.Fatalf("body missing %q:\n%s", "STOPPED", snippet(s))
		}
	})

	// --- Event SUBSCRIBE still 503 (Phase 4 work) ---
	t.Run("SUBSCRIBE event/AVTransport returns 503", func(t *testing.T) {
		// http.Client lower-cases its method, but SUBSCRIBE rides through
		// the client untouched. We construct the request manually so
		// Method stays exactly "SUBSCRIBE" — same shape a UPnP control
		// point sends.
		req, err := http.NewRequest("SUBSCRIBE",
			srv.URL+"/dlna/event/AVTransport", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("CALLBACK", "<http://127.0.0.1:9999/cb>")
		req.Header.Set("NT", "upnp:event")
		req.Header.Set("TIMEOUT", "Second-1800")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (Phase 4 stub)", resp.StatusCode)
		}
	})
}

// TestDLNA_Smoke_Phase2Playback exercises the SetAVTransportURI →
// Play → Stop SOAP flow end-to-end through the same shared mux as the
// Phase 1 smoke. The dlnaStubCore captures every StartSession /
// Stop call so the smoke confirms the adapter actually drives core
// with the right SessionRequest. The full data plane (real ffmpeg +
// fakemister) is out of scope here — that lives in a follow-up
// integration test.
//
// The dlna adapter's URL validator runs DNS classification on the
// SetAVTransportURI hostname; we override it to return a private LAN
// IP so the loopback-bound httptest.Server is accepted (the validator
// would otherwise reject 127.0.0.1 outright). The actual TCP dial
// the validator's HEAD request makes still goes to loopback because
// the URL string carries the loopback host:port — only the IP
// classification is fooled.
func TestDLNA_Smoke_Phase2Playback(t *testing.T) {
	const (
		fakeUUID = "22222222-3333-4444-5555-666666666666"
		fakeIP   = "127.0.0.1"
		fakePort = 32500
	)

	// Origin server that the SetAVTransportURI URL points at. A 200
	// response on HEAD is enough for the validator to accept the URL.
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer origin.Close()

	// Override the DNS resolver so the loopback host is classified as
	// private. Without this the validator rejects 127.0.0.1 with 716
	// before the URL ever reaches StartSession. See dlna/testhooks.go.
	restore := dlna.SetDNSResolverForTesting(dlna.StaticIPResolver("192.168.99.1"))
	defer restore()

	stub := &dlnaStubCore{}
	a, err := dlna.New(dlna.AdapterConfig{
		DeviceUUID: fakeUUID,
		HostIP:     fakeIP,
		HTTPPort:   fakePort,
		Core:       stub,
	})
	if err != nil {
		t.Fatalf("dlna.New: %v", err)
	}
	// The adapter rejects SetAVTransportURI when disabled, so flip on.
	a.SetEnabled(true)

	mux := http.NewServeMux()
	a.MountPublicRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Helper to POST a SOAP envelope and return the response body
	// string. Fails the test on transport error.
	postSOAP := func(t *testing.T, action, args string) (int, string) {
		t.Helper()
		envelope := `<?xml version="1.0" encoding="utf-8"?>` +
			`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
			`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
			`<s:Body><u:` + action + ` xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` +
			args +
			`</u:` + action + `></s:Body></s:Envelope>`
		req, err := http.NewRequest(http.MethodPost,
			srv.URL+"/dlna/control/AVTransport",
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

	// --- 1. GetCurrentTransportActions before any URI: empty.
	status, body := postSOAP(t, "GetCurrentTransportActions",
		"<InstanceID>0</InstanceID>")
	if status != http.StatusOK {
		t.Fatalf("pre-SetURI GetCurrentTransportActions status = %d, want 200; body=%s",
			status, snippet(body))
	}
	if !strings.Contains(body, "<Actions></Actions>") {
		t.Fatalf("pre-SetURI Actions not empty:\n%s", snippet(body))
	}

	// --- 2. SetAVTransportURI with a private-LAN URL (loopback as
	//        seen via the resolver override).
	mediaURL := origin.URL + "/movie.mp4"
	status, body = postSOAP(t, "SetAVTransportURI",
		"<InstanceID>0</InstanceID>"+
			"<CurrentURI>"+mediaURL+"</CurrentURI>"+
			"<CurrentURIMetaData></CurrentURIMetaData>")
	if status != http.StatusOK {
		t.Fatalf("SetAVTransportURI status = %d, want 200; body=%s",
			status, snippet(body))
	}
	// The Phase 1 stub's startCalls counter should still be zero —
	// SetAVTransportURI alone (autoplay disabled in DefaultConfig) must
	// not spawn a session.
	if got := stub.snapshotStartCalls(); got != 0 {
		t.Fatalf("StartSession called %d time(s) after SetAVTransportURI; want 0 (autoplay off)",
			got)
	}

	// --- 3. GetCurrentTransportActions after SetURI: "Play".
	status, body = postSOAP(t, "GetCurrentTransportActions",
		"<InstanceID>0</InstanceID>")
	if status != http.StatusOK {
		t.Fatalf("post-SetURI GetCurrentTransportActions status = %d", status)
	}
	if !strings.Contains(body, "<Actions>Play</Actions>") {
		t.Fatalf("post-SetURI Actions != Play:\n%s", snippet(body))
	}

	// --- 4. Play. StartSession must be called with the validator's
	//        FinalURL (== mediaURL since no redirect happened).
	status, body = postSOAP(t, "Play",
		"<InstanceID>0</InstanceID><Speed>1</Speed>")
	if status != http.StatusOK {
		t.Fatalf("Play status = %d, want 200; body=%s", status, snippet(body))
	}
	if got := stub.snapshotStartCalls(); got != 1 {
		t.Fatalf("StartSession called %d time(s) after Play; want 1", got)
	}
	req := stub.lastStartReq()
	if req.StreamURL != mediaURL {
		t.Errorf("StartSession.StreamURL = %q, want %q", req.StreamURL, mediaURL)
	}
	if req.AdapterRef == "" || !strings.HasPrefix(req.AdapterRef, "dlna:") {
		t.Errorf("StartSession.AdapterRef = %q, want dlna:* prefix", req.AdapterRef)
	}
	if !req.DirectPlay {
		t.Error("StartSession.DirectPlay = false, want true")
	}
	if req.OnStop == nil {
		t.Error("StartSession.OnStop = nil, want closure")
	}

	// --- 5. GetTransportInfo after Play: PLAYING.
	status, body = postSOAP(t, "GetTransportInfo",
		"<InstanceID>0</InstanceID>")
	if status != http.StatusOK {
		t.Fatalf("GetTransportInfo status = %d", status)
	}
	if !strings.Contains(body, "<CurrentTransportState>PLAYING</CurrentTransportState>") {
		t.Fatalf("post-Play state != PLAYING:\n%s", snippet(body))
	}

	// --- 6. GetCurrentTransportActions after Play: "Stop".
	status, body = postSOAP(t, "GetCurrentTransportActions",
		"<InstanceID>0</InstanceID>")
	if status != http.StatusOK {
		t.Fatalf("playing GetCurrentTransportActions status = %d", status)
	}
	if !strings.Contains(body, "<Actions>Stop</Actions>") {
		t.Fatalf("playing Actions != Stop:\n%s", snippet(body))
	}

	// --- 7. Stop. core.Stop must be called once.
	status, body = postSOAP(t, "Stop", "<InstanceID>0</InstanceID>")
	if status != http.StatusOK {
		t.Fatalf("Stop status = %d, want 200; body=%s", status, snippet(body))
	}
	if got := stub.snapshotStopCalls(); got != 1 {
		t.Fatalf("core.Stop called %d time(s); want 1", got)
	}

	// Note: the OnStop callback fires via core.Manager in production;
	// the stub doesn't fire it here. transportState therefore remains
	// PLAYING from the adapter's perspective (the spec's "OnStop is
	// the source of truth" semantic, not "Stop sets state"). That's
	// the design — assertion stops at the core call boundary, which
	// is what this smoke is meant to prove.
}

// snippet returns up to the first 512 bytes of s for failure messages.
// Avoids dumping a multi-KB SCPD into test logs while still giving
// enough context to diagnose a missing-substring failure.
func snippet(s string) string {
	const max = 512
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
