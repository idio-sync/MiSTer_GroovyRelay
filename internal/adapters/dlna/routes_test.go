package dlna

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// allDLNARoutes is the set of (method, path) pairs the spec
// §HTTP Surface (lines 152-164) declares. Phase 1 stubs return 503
// for each — but the request must reach a handler, not the mux's
// default 404, or future-T4 controllers won't find the renderer at
// all.
var allDLNARoutes = []struct {
	method string
	path   string
}{
	{"GET", "/dlna/device.xml"},
	{"GET", "/dlna/AVTransport.xml"},
	{"GET", "/dlna/ConnectionManager.xml"},
	{"GET", "/dlna/RenderingControl.xml"},
	{"POST", "/dlna/control/AVTransport"},
	{"POST", "/dlna/control/ConnectionManager"},
	{"POST", "/dlna/control/RenderingControl"},
	{"SUBSCRIBE", "/dlna/event/AVTransport"},
	{"UNSUBSCRIBE", "/dlna/event/AVTransport"},
	{"SUBSCRIBE", "/dlna/event/RenderingControl"},
	{"UNSUBSCRIBE", "/dlna/event/RenderingControl"},
	{"SUBSCRIBE", "/dlna/event/ConnectionManager"},
	{"UNSUBSCRIBE", "/dlna/event/ConnectionManager"},
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

			// Phase 1: handler must respond with 503; if the mux didn't
			// match, the test sees 404 ("page not found") instead.
			if rr.Code == http.StatusNotFound {
				t.Fatalf("%s %s returned 404 — route not registered (phase 1 stub expected 503)",
					r.method, r.path)
			}
			if rr.Code != http.StatusServiceUnavailable {
				t.Errorf("%s %s status = %d, want %d (phase 1 stub)",
					r.method, r.path, rr.Code, http.StatusServiceUnavailable)
			}
			if !strings.Contains(rr.Body.String(), "Phase 1") {
				t.Errorf("%s %s body = %q, want a Phase 1 placeholder marker",
					r.method, r.path, rr.Body.String())
			}
		})
	}
}

// TestMountPublicRoutes_HandlesNonStandardMethods proves SUBSCRIBE
// reaches the registered handler. http.ServeMux refuses to dispatch on
// non-standard methods via the "METHOD /path" pattern syntax — we
// register path-only and dispatch in the handler instead. If this test
// regresses, the SCPD eventing in T4 will silently fail.
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
				if rr.Code != http.StatusServiceUnavailable {
					t.Errorf("%s %s status = %d, want %d", method, path, rr.Code, http.StatusServiceUnavailable)
				}
			})
		}
	}
}

// TestMountPublicRoutes_NoCSRFGate confirms /dlna/* routes are not
// wrapped in the settings-UI CSRF middleware. We never mount the UI
// middleware in this test (we hand the mux to the adapter directly),
// but we assert the handler responds without an Origin/Referer header
// — proving the route surface itself doesn't require those.
func TestMountPublicRoutes_NoCSRFGate(t *testing.T) {
	_, mux := newTestAdapterForRoutes(t)
	req := httptest.NewRequest("POST", "/dlna/control/AVTransport", strings.NewReader(""))
	// Deliberately no Origin / X-CSRF-Token header.
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (no CSRF gating expected on /dlna/*)", rr.Code)
	}
}
