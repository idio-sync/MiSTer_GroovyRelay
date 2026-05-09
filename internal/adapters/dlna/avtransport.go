package dlna

import (
	"net/http"
)

// avtransport.go owns the AVTransport:1 SOAP control endpoint
// (POST /dlna/control/AVTransport). Phase 1 disposition per spec
// §Service Action Surface lines 196-216:
//
//   Impl (return constant defaults):
//     GetMediaInfo, GetTransportInfo, GetPositionInfo,
//     GetDeviceCapabilities, GetTransportSettings,
//     GetCurrentTransportActions
//   Impl (no-op):
//     SetNextAVTransportURI
//   Impl (mode validation):
//     SetPlayMode (NORMAL ok; other modes → 712)
//   Stub (return 501 Action Failed):
//     SetAVTransportURI, Play, Pause, Stop, Seek, Next, Previous
//
// Real Stop/Play/Pause/Seek/SetAVTransportURI implementations land in
// Phase 2/3.

const avTransportServiceURN = "urn:schemas-upnp-org:service:AVTransport:1"

// avtPhase1StubActions is the set of action names whose handler
// returns 501 in Phase 1. Listed explicitly so the table is auditable
// and so the dispatcher can branch on the action name without a
// per-action stub function.
var avtPhase1StubActions = map[string]struct{}{
	"SetAVTransportURI": {},
	"Play":              {},
	"Pause":             {},
	"Stop":              {},
	"Seek":              {},
	"Next":              {},
	"Previous":          {},
}

// handleAVTransportSOAP dispatches the AVTransport:1 SOAP action.
//
// Spec §Common Action Rules (line 288): all actions operate on
// InstanceID=0; invalid InstanceID returns 718. We enforce this at
// the dispatcher only for actions that take InstanceID — Phase 1
// stub actions return 501 before InstanceID is validated.
func (a *Adapter) handleAVTransportSOAP(w http.ResponseWriter, r *http.Request) {
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

	// Phase 1 stub: seven mutating actions return 501. They are
	// declared in the SCPD because their argument lists are needed
	// for controllers to bind, but the actual transitions land in
	// Phase 2/3.
	if _, isStub := avtPhase1StubActions[action]; isStub {
		writeSOAPFault(w, upnpErrActionFailed)
		return
	}

	// All Impl actions take InstanceID=0; reject other values.
	if iid, ok := args["InstanceID"]; ok && iid != "" && iid != "0" {
		writeSOAPFault(w, upnpErrInvalidInstanceID)
		return
	}

	switch action {
	case "GetMediaInfo":
		// Phase 1 returns the empty-media defaults: NrTracks=0, no URI.
		// Spec §Query Actions: NrTracks=1 when URI is loaded; in Phase 1
		// no URI is ever loaded (SetAVTransportURI is a stub).
		writeSOAPResponse(w, avTransportServiceURN, action, []soapOutArg{
			{Name: "NrTracks", Value: "0"},
			{Name: "MediaDuration", Value: "00:00:00"},
			{Name: "CurrentURI", Value: ""},
			{Name: "CurrentURIMetaData", Value: ""},
			{Name: "NextURI", Value: ""},
			{Name: "NextURIMetaData", Value: ""},
			{Name: "PlayMedium", Value: "NONE"},
			{Name: "RecordMedium", Value: "NOT_IMPLEMENTED"},
			{Name: "WriteStatus", Value: "NOT_IMPLEMENTED"},
		})

	case "GetTransportInfo":
		// Phase 1 always reports STOPPED with no error.
		writeSOAPResponse(w, avTransportServiceURN, action, []soapOutArg{
			{Name: "CurrentTransportState", Value: "STOPPED"},
			{Name: "CurrentTransportStatus", Value: "OK"},
			{Name: "CurrentSpeed", Value: "1"},
		})

	case "GetPositionInfo":
		// Phase 1: no track loaded. Track=0 per spec §Query Actions
		// "Track=0 otherwise".
		writeSOAPResponse(w, avTransportServiceURN, action, []soapOutArg{
			{Name: "Track", Value: "0"},
			{Name: "TrackDuration", Value: "00:00:00"},
			{Name: "TrackMetaData", Value: ""},
			{Name: "TrackURI", Value: ""},
			{Name: "RelTime", Value: "00:00:00"},
			{Name: "AbsTime", Value: "00:00:00"},
			{Name: "RelCount", Value: "0"},
			{Name: "AbsCount", Value: "0"},
		})

	case "GetDeviceCapabilities":
		// Spec §Query Actions: PlayMedia=NETWORK; recording NOT_IMPLEMENTED.
		writeSOAPResponse(w, avTransportServiceURN, action, []soapOutArg{
			{Name: "PlayMedia", Value: "NETWORK"},
			{Name: "RecMedia", Value: "NOT_IMPLEMENTED"},
			{Name: "RecQualityModes", Value: "NOT_IMPLEMENTED"},
		})

	case "GetTransportSettings":
		// Spec §Query Actions: PlayMode=NORMAL; RecQualityMode NOT_IMPLEMENTED.
		writeSOAPResponse(w, avTransportServiceURN, action, []soapOutArg{
			{Name: "PlayMode", Value: "NORMAL"},
			{Name: "RecQualityMode", Value: "NOT_IMPLEMENTED"},
		})

	case "GetCurrentTransportActions":
		// Phase 1: no URI loaded → no transport actions advertised.
		// Spec §Query Actions "No URI loaded: empty."
		writeSOAPResponse(w, avTransportServiceURN, action, []soapOutArg{
			{Name: "Actions", Value: ""},
		})

	case "SetNextAVTransportURI":
		// Spec §Service Action Surface line 201: accepts valid arguments,
		// stores no queue state, and returns success. v1 always reports
		// NextURI="" from GetMediaInfo.
		writeSOAPResponse(w, avTransportServiceURN, action, nil)

	case "SetPlayMode":
		// Spec §Service Action Surface line 213: NORMAL is a no-op;
		// other modes return 712.
		mode := args["NewPlayMode"]
		if mode != "NORMAL" {
			writeSOAPFault(w, upnpErrPlayModeNotSupp)
			return
		}
		writeSOAPResponse(w, avTransportServiceURN, action, nil)

	default:
		// Unknown action — including any RC/CM action sent to the wrong
		// endpoint, or any v3+ action a permissive controller might try.
		writeSOAPFault(w, upnpErrInvalidAction)
	}
}
