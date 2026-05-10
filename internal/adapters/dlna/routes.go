package dlna

import "net/http"

// MountPublicRoutes mounts the 13 protocol-side HTTP routes on the
// shared bridge mux. Called by main.go via the
// adapters.PublicRouteProvider interface BEFORE the UI server mounts
// /ui/*; the paths under /dlna/* are deliberately disjoint from the
// settings UI tree so DLNA control points (which cannot send the
// origin headers the /ui/* CSRF middleware requires) reach the
// adapter without going through that middleware.
//
// **T4: Phase 1 routing surface.** Descriptor and SOAP control
// handlers are real (descriptors.go / soap.go / avtransport.go /
// rendering_control.go / connection_manager.go). Event SUBSCRIBE/
// UNSUBSCRIBE handlers stay 503 — eventing lands in Phase 4.
//
// **Non-standard HTTP methods.** UPnP eventing uses SUBSCRIBE and
// UNSUBSCRIBE, which Go's http.ServeMux does NOT dispatch on. Even
// the Go 1.22+ method-prefixed pattern syntax ("GET /path") only
// recognizes standard methods. To accept SUBSCRIBE/UNSUBSCRIBE we
// register a single path-only pattern for each event endpoint and
// switch on r.Method inside the handler. Phase 4 will tighten dispatch
// and reject unknown methods with 405.
func (a *Adapter) MountPublicRoutes(mux *http.ServeMux) {
	// Descriptors: device + per-service SCPD documents (GET only).
	// Method-prefixed pattern syntax enforces 405 on other methods.
	mux.HandleFunc("GET /dlna/device.xml", a.handleDeviceDescriptor)
	mux.HandleFunc("GET /dlna/AVTransport.xml", handleSCPDAVTransport)
	mux.HandleFunc("GET /dlna/ConnectionManager.xml", handleSCPDConnectionManager)
	mux.HandleFunc("GET /dlna/RenderingControl.xml", handleSCPDRenderingControl)

	// SOAP control endpoints: POST only. Each service has its own
	// dispatcher that branches on the action name from the SOAPACTION
	// header.
	mux.HandleFunc("POST /dlna/control/AVTransport", a.handleAVTransportSOAP)
	mux.HandleFunc("POST /dlna/control/ConnectionManager", a.handleConnectionManagerSOAP)
	mux.HandleFunc("POST /dlna/control/RenderingControl", a.handleRenderingControlSOAP)

	// Eventing endpoints: SUBSCRIBE/UNSUBSCRIBE are non-standard HTTP
	// methods — register path-only and dispatch in the handler. Phase 1
	// stubs return 503 for all event traffic; Phase 4 implements the
	// SUBSCRIBE/UNSUBSCRIBE/NOTIFY surface.
	mux.HandleFunc("/dlna/event/AVTransport", eventStubHandler)
	mux.HandleFunc("/dlna/event/RenderingControl", eventStubHandler)
	mux.HandleFunc("/dlna/event/ConnectionManager", eventStubHandler)
}

// handleDeviceDescriptor returns the root device descriptor with
// friendlyName populated from the live config. Reads cfg.DeviceName
// under mu, then writes the response after releasing the mutex
// (Adapter mu is never held across HTTP I/O).
func (a *Adapter) handleDeviceDescriptor(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	friendlyName := a.cfg.DeviceName
	a.mu.Unlock()

	body, err := deviceXML(a.deviceUUID, friendlyName)
	if err != nil {
		// deviceXML only fails if encoding/xml fails on a struct it
		// fully controls, which shouldn't happen — guard with a 500
		// and a SOAP-style fault body so observability tooling can
		// still parse the response.
		http.Error(w, "device descriptor encoding error", http.StatusInternalServerError)
		return
	}
	writeXMLDescriptor(w, body)
}

// handleSCPDAVTransport returns the AVTransport:1 service description.
// Constant body — no adapter state needed.
func handleSCPDAVTransport(w http.ResponseWriter, r *http.Request) {
	writeXMLDescriptor(w, []byte(avTransportSCPD))
}

// handleSCPDConnectionManager returns the ConnectionManager:1 service
// description. Constant body — no adapter state needed.
func handleSCPDConnectionManager(w http.ResponseWriter, r *http.Request) {
	writeXMLDescriptor(w, []byte(connectionManagerSCPD))
}

// handleSCPDRenderingControl returns the RenderingControl:1 service
// description. Constant body — no adapter state needed.
func handleSCPDRenderingControl(w http.ResponseWriter, r *http.Request) {
	writeXMLDescriptor(w, []byte(renderingControlSCPD))
}

// writeXMLDescriptor writes a UPnP descriptor document with the
// canonical Content-Type header and HTTP 200 status. Centralized so
// every descriptor handler emits identical headers — control points
// are picky about the charset attribute being present.
func writeXMLDescriptor(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// eventStubHandler returns 503 for all event-route requests in Phase 1.
// Phase 4 replaces this with a SUBSCRIBE/UNSUBSCRIBE-aware handler.
const eventPhase1Body = "DLNA eventing arrives in Phase 4"

func eventStubHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(eventPhase1Body))
}
