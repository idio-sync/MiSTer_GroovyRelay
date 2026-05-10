package dlna

import (
	"fmt"
	"net/http"
	"time"
)

// avtransport.go owns the AVTransport:1 SOAP control endpoint
// (POST /dlna/control/AVTransport). Disposition per spec
// §Service Action Surface lines 196-216:
//
//   Impl (return constant defaults / read adapter state):
//     GetMediaInfo, GetTransportInfo, GetPositionInfo,
//     GetDeviceCapabilities, GetTransportSettings,
//     GetCurrentTransportActions
//   Impl (no-op):
//     SetNextAVTransportURI
//   Impl (mode validation):
//     SetPlayMode (NORMAL ok; other modes → 712)
//   Impl (validate + store) — Phase 2 P2.3:
//     SetAVTransportURI
//   Stub (return 501 Action Failed) — flip to Impl in P2.4 / P3:
//     Play, Pause, Stop, Seek, Next, Previous

const avTransportServiceURN = "urn:schemas-upnp-org:service:AVTransport:1"

// avtStubActions is the set of action names whose handler still
// returns 501. Listed explicitly so the table is auditable and so the
// dispatcher can branch on the action name without a per-action stub
// function. SetAVTransportURI moved out of this set in P2.3 (real
// validate+store handler); Play/Stop will move in P2.4.
var avtStubActions = map[string]struct{}{
	"Play":     {},
	"Pause":    {},
	"Stop":     {},
	"Seek":     {},
	"Next":     {},
	"Previous": {},
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

	// SetAVTransportURI runs BEFORE the generic InstanceID gate because
	// the handler enforces InstanceID itself (it has admission checks
	// that should run regardless of InstanceID validity to keep the
	// handler self-contained).
	if action == "SetAVTransportURI" {
		a.handleSetAVTransportURI(w, r, extractSetAVTransportURIArgs(args))
		return
	}

	// Stub actions return 501 (will flip to Impl in P2.4 / P3).
	if _, isStub := avtStubActions[action]; isStub {
		writeSOAPFault(w, upnpErrActionFailed)
		return
	}

	// All remaining Impl actions take InstanceID=0; reject other values.
	if iid, ok := args["InstanceID"]; ok && iid != "" && iid != "0" {
		writeSOAPFault(w, upnpErrInvalidInstanceID)
		return
	}

	switch action {
	case "GetMediaInfo":
		// Read loaded URI/metadata under mu (CLAUDE.md: never hold mu
		// across HTTP I/O — the response write happens after release).
		a.mu.Lock()
		uri := a.loadedURI
		metaRaw := a.loadedMetaRaw
		duration := a.loadedMeta.Duration
		a.mu.Unlock()

		nrTracks := "0"
		mediaDuration := "00:00:00"
		if uri != "" {
			nrTracks = "1"
			if duration > 0 {
				mediaDuration = formatUPnPDuration(duration)
			}
		}
		writeSOAPResponse(w, avTransportServiceURN, action, []soapOutArg{
			{Name: "NrTracks", Value: nrTracks},
			{Name: "MediaDuration", Value: mediaDuration},
			{Name: "CurrentURI", Value: uri},
			{Name: "CurrentURIMetaData", Value: metaRaw},
			// NextURI / NextURIMetaData are always empty: the renderer
			// has no queue model. SetNextAVTransportURI is a no-op (spec
			// §Service Action Surface line 201).
			{Name: "NextURI", Value: ""},
			{Name: "NextURIMetaData", Value: ""},
			{Name: "PlayMedium", Value: "NONE"},
			{Name: "RecordMedium", Value: "NOT_IMPLEMENTED"},
			{Name: "WriteStatus", Value: "NOT_IMPLEMENTED"},
		})

	case "GetTransportInfo":
		// State is always STOPPED until P2.4 wires Play. TransportStatus
		// is OK by default and ERROR_OCCURRED when lastError is non-empty
		// (a prior failed SetAVTransportURI / probe). Spec §Common Action
		// Rules line 326: "On failure, set TransportStatus=ERROR_OCCURRED,
		// store a redacted last error..."
		a.mu.Lock()
		lastErr := a.lastError
		a.mu.Unlock()

		status := "OK"
		if lastErr != "" {
			status = "ERROR_OCCURRED"
		}
		writeSOAPResponse(w, avTransportServiceURN, action, []soapOutArg{
			{Name: "CurrentTransportState", Value: "STOPPED"},
			{Name: "CurrentTransportStatus", Value: status},
			{Name: "CurrentSpeed", Value: "1"},
		})

	case "GetPositionInfo":
		// Position/duration zeros until playback (P2.4); Track=1 only
		// when a URI is loaded. TrackURI mirrors loadedURI and
		// TrackMetaData mirrors loadedMetaRaw — controllers expect
		// these to round-trip.
		a.mu.Lock()
		uri := a.loadedURI
		metaRaw := a.loadedMetaRaw
		duration := a.loadedMeta.Duration
		a.mu.Unlock()

		track := "0"
		trackDuration := "00:00:00"
		if uri != "" {
			track = "1"
			if duration > 0 {
				trackDuration = formatUPnPDuration(duration)
			}
		}
		writeSOAPResponse(w, avTransportServiceURN, action, []soapOutArg{
			{Name: "Track", Value: track},
			{Name: "TrackDuration", Value: trackDuration},
			{Name: "TrackMetaData", Value: metaRaw},
			{Name: "TrackURI", Value: uri},
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
		// P2.3: no playback yet, so no transport actions are advertised
		// even when a URI is loaded. P2.4 will populate this with
		// {Play, Stop[, Pause, Seek]} based on stream capabilities.
		// Until then, reading from loadedURI alone would lie about what
		// the renderer can do.
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

// formatUPnPDuration renders a time.Duration as a UPnP duration string.
//
// UPnP AVTransport:1 specifies HH:MM:SS or HH:MM:SS.FFF (where FFF is
// milliseconds, zero-padded to three digits). We always emit the
// HH:MM:SS form for the renderer's GetMediaInfo / GetPositionInfo
// outputs because most controllers ignore the fractional component for
// display and parsing it back to compare against re-fetched
// CurrentURIMetaData would diverge if we round-tripped a millisecond
// value the controller never sent.
//
// Negative durations clamp to "00:00:00" (an unknown / invalid input
// shouldn't crash; spec line 369 treats zero as unknown). Durations
// over 24h are formatted as their literal hour count — UPnP does not
// require a leading zero on HH and controllers we care about parse
// "100:00:00" correctly. Movies at 9:56 are the realistic upper bound.
func formatUPnPDuration(d time.Duration) string {
	if d < 0 {
		return "00:00:00"
	}
	totalSec := int64(d / time.Second)
	h := totalSec / 3600
	m := (totalSec % 3600) / 60
	s := totalSec % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
