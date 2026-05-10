//go:build integration

package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/dlna"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// dlnaStubCore is a no-op SessionManager for the Phase-1 HTTP-surface
// smoke. SetAVTransportURI / Play / Stop still return Stub-501 in Phase
// 2.1, so no method is actually invoked here; the stub exists only to
// satisfy dlna.AdapterConfig.Core != nil.
type dlnaStubCore struct{}

func (*dlnaStubCore) StartSession(core.SessionRequest) error { return nil }
func (*dlnaStubCore) Status() core.SessionStatus             { return core.SessionStatus{} }
func (*dlnaStubCore) Pause() error                           { return nil }
func (*dlnaStubCore) Play() error                            { return nil }
func (*dlnaStubCore) Stop() error                            { return nil }
func (*dlnaStubCore) SeekTo(int) error                       { return nil }

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
