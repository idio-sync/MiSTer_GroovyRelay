package dlna

import (
	"net/http"
	"strings"
)

// connection_manager.go owns the ConnectionManager:1 SOAP control
// endpoint (POST /dlna/control/ConnectionManager). Phase 1 disposition
// per spec §Service Action Surface lines 241-254: all 5 actions Impl.
//
//   GetProtocolInfo         — return Source/Sink protocol strings
//   GetCurrentConnectionIDs — always "0" (single virtual connection)
//   GetCurrentConnectionInfo — describe the virtual connection 0
//   PrepareForConnection    — no-op returning IDs all 0
//   ConnectionComplete      — no-op for ConnectionID=0; non-0 → 701

const connectionManagerServiceURN = "urn:schemas-upnp-org:service:ConnectionManager:1"

// sinkProtocolInfoEntries enumerates the eight HTTP MIME entries the
// renderer accepts. Spec §ConnectionManager (lines 414-423). HLS and
// M3U8 are deliberately omitted in v1 — they require playlist-child
// URL validation (spec §ConnectionManager line 425) which lands in
// Phase 5.
var sinkProtocolInfoEntries = []string{
	"http-get:*:video/mp4:*",
	"http-get:*:video/x-matroska:*",
	"http-get:*:video/mpeg:*",
	"http-get:*:video/vnd.dlna.mpeg-tts:*",
	"http-get:*:audio/mpeg:*",
	"http-get:*:audio/mp4:*",
	"http-get:*:audio/flac:*",
	"http-get:*:audio/x-flac:*",
}

// sinkProtocolInfo is the comma-joined SinkProtocolInfo string. Built
// once at init from sinkProtocolInfoEntries so the wire format and
// the unit-test entry list stay in sync without string-splitting at
// every fetch.
var sinkProtocolInfo = strings.Join(sinkProtocolInfoEntries, ",")

// handleConnectionManagerSOAP dispatches the ConnectionManager:1 SOAP
// action.
func (a *Adapter) handleConnectionManagerSOAP(w http.ResponseWriter, r *http.Request) {
	action, ok := extractSOAPAction(r.Header.Get("SOAPACTION"))
	if !ok {
		writeSOAPFault(w, upnpErrInvalidAction)
		return
	}

	_, args, err := parseSOAPRequest(w, r)
	if err != nil {
		// All parse failures (oversized body, empty body, missing
		// action element, malformed XML) map to InvalidArgs — the
		// controller sent a malformed envelope. MaxBytesReader sets
		// Connection: close on the response and stops the read with
		// *http.MaxBytesError on the next Read; we still emit a SOAP
		// fault as the response body, then the connection closes.
		writeSOAPFault(w, upnpErrInvalidArgs)
		return
	}

	switch action {
	case "GetProtocolInfo":
		// Source is empty (we don't source content); Sink is the v1 list.
		writeSOAPResponse(w, connectionManagerServiceURN, action, []soapOutArg{
			{Name: "Source", Value: ""},
			{Name: "Sink", Value: sinkProtocolInfo},
		})

	case "GetCurrentConnectionIDs":
		// Always "0" — spec §ConnectionManager line 433: "models the
		// renderer's single virtual input connection and avoids control
		// points seeing an empty connection list as 'not ready.'"
		writeSOAPResponse(w, connectionManagerServiceURN, action, []soapOutArg{
			{Name: "ConnectionIDs", Value: "0"},
		})

	case "GetCurrentConnectionInfo":
		// ConnectionID must be 0 — that's the only virtual connection
		// the renderer maintains. Spec §ConnectionManager line 434:
		// "return connection details for ID 0".
		cid := args["ConnectionID"]
		if cid != "" && cid != "0" {
			writeSOAPFault(w, upnpErrTransitionNotAvail)
			return
		}
		writeSOAPResponse(w, connectionManagerServiceURN, action, []soapOutArg{
			{Name: "RcsID", Value: "0"},
			{Name: "AVTransportID", Value: "0"},
			// ProtocolInfo for the sink connection is reported as empty
			// — it's not bound to a specific resource until playback,
			// and Phase 1 has no playback.
			{Name: "ProtocolInfo", Value: ""},
			{Name: "PeerConnectionManager", Value: ""},
			// PeerConnectionID=-1 per UPnP CM:1 spec when there is no
			// peer (the renderer is the input endpoint of a virtual
			// connection that has no remote peer yet).
			{Name: "PeerConnectionID", Value: "-1"},
			{Name: "Direction", Value: "Input"},
			{Name: "Status", Value: "OK"},
		})

	case "PrepareForConnection":
		// Spec §Service Action Surface line 246: "No-op returning
		// ConnectionID=0/AVTransportID=0/RcsID=0."
		writeSOAPResponse(w, connectionManagerServiceURN, action, []soapOutArg{
			{Name: "ConnectionID", Value: "0"},
			{Name: "AVTransportID", Value: "0"},
			{Name: "RcsID", Value: "0"},
		})

	case "ConnectionComplete":
		// Spec §Service Action Surface line 247: "No-op for ConnectionID=0".
		// Non-zero IDs name a connection that doesn't exist — return 701.
		cid := args["ConnectionID"]
		if cid != "" && cid != "0" {
			writeSOAPFault(w, upnpErrTransitionNotAvail)
			return
		}
		writeSOAPResponse(w, connectionManagerServiceURN, action, nil)

	default:
		writeSOAPFault(w, upnpErrInvalidAction)
	}
}
