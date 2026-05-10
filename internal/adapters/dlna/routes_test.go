package dlna

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// allDLNARoutes is the set of (method, path) pairs the spec
// §HTTP Surface (lines 152-164) declares. Descriptor and SOAP routes
// have real handlers; event SUBSCRIBE/UNSUBSCRIBE requests reach the
// service-aware GENA handlers and reject malformed empty requests.
var allDLNARoutes = []struct {
	method     string
	path       string
	wantStatus int // 200 for descriptors, 400 for empty event requests, "varies" for SOAP control
}{
	{"GET", "/dlna/device.xml", http.StatusOK},
	{"GET", "/dlna/AVTransport.xml", http.StatusOK},
	{"GET", "/dlna/ConnectionManager.xml", http.StatusOK},
	{"GET", "/dlna/RenderingControl.xml", http.StatusOK},
	// SOAP control endpoints expect a SOAPACTION header + envelope.
	// Without those they 500 (UPnP fault) — covered specifically below.
	{"SUBSCRIBE", "/dlna/event/AVTransport", http.StatusBadRequest},
	{"UNSUBSCRIBE", "/dlna/event/AVTransport", http.StatusBadRequest},
	{"SUBSCRIBE", "/dlna/event/RenderingControl", http.StatusBadRequest},
	{"UNSUBSCRIBE", "/dlna/event/RenderingControl", http.StatusBadRequest},
	{"SUBSCRIBE", "/dlna/event/ConnectionManager", http.StatusBadRequest},
	{"UNSUBSCRIBE", "/dlna/event/ConnectionManager", http.StatusBadRequest},
}

// newTestAdapterForRoutes constructs a default adapter and mounts its
// public routes on a fresh ServeMux. Returned mux is ready to dispatch.
func newTestAdapterForRoutes(t *testing.T) (*Adapter, *http.ServeMux) {
	t.Helper()
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	a.MountPublicRoutes(mux)
	return a, mux
}

func TestMountPublicRoutes_AllPathsRegistered(t *testing.T) {
	_, mux := newTestAdapterForRoutes(t)

	for _, r := range allDLNARoutes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code == http.StatusNotFound {
				t.Fatalf("%s %s returned 404 — route not registered", r.method, r.path)
			}
			if rr.Code != r.wantStatus {
				t.Errorf("%s %s status = %d, want %d", r.method, r.path, rr.Code, r.wantStatus)
			}
		})
	}
}

// TestMountPublicRoutes_HandlesNonStandardMethods proves SUBSCRIBE
// reaches the registered handler. http.ServeMux refuses to dispatch on
// non-standard methods via the "METHOD /path" pattern syntax — we
// register path-only and dispatch in the handler instead. If this test
// regresses, the SCPD eventing in Phase 4 will silently fail.
func TestMountPublicRoutes_HandlesNonStandardMethods(t *testing.T) {
	_, mux := newTestAdapterForRoutes(t)

	for _, method := range []string{"SUBSCRIBE", "UNSUBSCRIBE"} {
		for _, path := range []string{
			"/dlna/event/AVTransport",
			"/dlna/event/RenderingControl",
			"/dlna/event/ConnectionManager",
		} {
			t.Run(method+" "+path, func(t *testing.T) {
				req := httptest.NewRequest(method, path, nil)
				rr := httptest.NewRecorder()
				mux.ServeHTTP(rr, req)
				if rr.Code == http.StatusNotFound {
					t.Fatalf("%s %s returned 404 — non-standard method not dispatched",
						method, path)
				}
				if rr.Code == http.StatusMethodNotAllowed {
					t.Fatalf("%s %s returned 405 — mux is rejecting non-standard methods; "+
						"event endpoints must accept SUBSCRIBE/UNSUBSCRIBE",
						method, path)
				}
				if rr.Code != http.StatusBadRequest {
					t.Errorf("%s %s status = %d, want %d", method, path, rr.Code, http.StatusBadRequest)
				}
			})
		}
	}
}

func TestMountPublicRoutes_RejectsUnsupportedEventMethod(t *testing.T) {
	_, mux := newTestAdapterForRoutes(t)

	req := httptest.NewRequest(http.MethodPost, "/dlna/event/AVTransport", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	if got, want := rr.Header().Get("Allow"), "SUBSCRIBE, UNSUBSCRIBE"; got != want {
		t.Fatalf("Allow = %q, want %q", got, want)
	}
}

// TestMountPublicRoutes_NoCSRFGate confirms /dlna/* routes are not
// wrapped in the settings-UI CSRF middleware. We never mount the UI
// middleware in this test (we hand the mux to the adapter directly),
// but we send a SOAP request without an Origin/Referer header — proving
// the route surface itself doesn't require those.
func TestMountPublicRoutes_NoCSRFGate(t *testing.T) {
	_, mux := newTestAdapterForRoutes(t)
	// Send a real (valid) SOAP envelope so the handler reaches the
	// dispatcher rather than failing on parse error. A GetProtocolInfo
	// is the simplest CM action and takes no arguments.
	body := `<?xml version="1.0"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">` +
		`<s:Body><u:GetProtocolInfo xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1"/></s:Body>` +
		`</s:Envelope>`
	req := httptest.NewRequest("POST", "/dlna/control/ConnectionManager", strings.NewReader(body))
	req.Header.Set("SOAPACTION", `"urn:schemas-upnp-org:service:ConnectionManager:1#GetProtocolInfo"`)
	// Deliberately no Origin / X-CSRF-Token header.
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (no CSRF gating expected on /dlna/*)", rr.Code)
	}
}

// TestDescriptorRoutes_ContentType verifies every descriptor route
// returns text/xml; charset=utf-8 — the canonical UPnP descriptor
// content-type. Some controllers (Windows DMR clients in particular)
// reject documents with a missing or non-UTF-8 charset attribute.
func TestDescriptorRoutes_ContentType(t *testing.T) {
	_, mux := newTestAdapterForRoutes(t)

	for _, path := range []string{
		"/dlna/device.xml",
		"/dlna/AVTransport.xml",
		"/dlna/ConnectionManager.xml",
		"/dlna/RenderingControl.xml",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("%s status = %d, want 200", path, rr.Code)
			}
			ct := rr.Header().Get("Content-Type")
			if ct != "text/xml; charset=utf-8" {
				t.Errorf("%s Content-Type = %q, want %q", path, ct, "text/xml; charset=utf-8")
			}
			if rr.Body.Len() == 0 {
				t.Errorf("%s body is empty", path)
			}
		})
	}
}

// TestDeviceDescriptor_FriendlyNameFromConfig pins that the device
// descriptor honours the operator's configured device_name. If a save
// path mutates a.cfg.DeviceName at runtime, future fetches must reflect
// the new name (modulo SSDP cache, which is not a route concern).
func TestDeviceDescriptor_FriendlyNameFromConfig(t *testing.T) {
	a, mux := newTestAdapterForRoutes(t)
	a.mu.Lock()
	a.cfg.DeviceName = "Lounge-CRT"
	a.mu.Unlock()

	req := httptest.NewRequest("GET", "/dlna/device.xml", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, "<friendlyName>Lounge-CRT</friendlyName>") {
		t.Errorf("device.xml does not contain configured friendlyName Lounge-CRT; body=\n%s", body)
	}
}

// TestSOAPControlRoute_DispatchesValidGet verifies a well-formed SOAP
// request reaches the per-service handler and produces a 200 response
// envelope. The dispatcher returning 200 (not 503) is the primary
// signal that T4 has activated.
func TestSOAPControlRoute_DispatchesValidGet(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		soapAction string
		envelope   string
		wantInBody string
	}{
		{
			name:       "AVTransport GetTransportInfo",
			path:       "/dlna/control/AVTransport",
			soapAction: `"urn:schemas-upnp-org:service:AVTransport:1#GetTransportInfo"`,
			envelope: `<?xml version="1.0"?>` +
				`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">` +
				`<s:Body><u:GetTransportInfo xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">` +
				`<InstanceID>0</InstanceID>` +
				`</u:GetTransportInfo></s:Body></s:Envelope>`,
			wantInBody: "STOPPED",
		},
		{
			name:       "RenderingControl GetVolume",
			path:       "/dlna/control/RenderingControl",
			soapAction: `"urn:schemas-upnp-org:service:RenderingControl:1#GetVolume"`,
			envelope: `<?xml version="1.0"?>` +
				`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">` +
				`<s:Body><u:GetVolume xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">` +
				`<InstanceID>0</InstanceID><Channel>Master</Channel>` +
				`</u:GetVolume></s:Body></s:Envelope>`,
			wantInBody: "<CurrentVolume>100</CurrentVolume>",
		},
		{
			name:       "ConnectionManager GetProtocolInfo",
			path:       "/dlna/control/ConnectionManager",
			soapAction: `"urn:schemas-upnp-org:service:ConnectionManager:1#GetProtocolInfo"`,
			envelope: `<?xml version="1.0"?>` +
				`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">` +
				`<s:Body><u:GetProtocolInfo xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1"/>` +
				`</s:Body></s:Envelope>`,
			wantInBody: "video/mp4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mux := newTestAdapterForRoutes(t)
			req := httptest.NewRequest("POST", tt.path, strings.NewReader(tt.envelope))
			req.Header.Set("SOAPACTION", tt.soapAction)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tt.wantInBody) {
				t.Errorf("body = %q, want substring %q", rr.Body.String(), tt.wantInBody)
			}
		})
	}
}
