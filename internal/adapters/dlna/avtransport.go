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
//   Impl (P2.4):
//     Play, Stop
//   Impl (P3.1):
//     Pause
//   Impl (P3.2):
//     Seek
//   Stub (return 501 Action Failed) — never:
//     Next, Previous

const avTransportServiceURN = "urn:schemas-upnp-org:service:AVTransport:1"

// avtStubActions is the set of action names whose handler still
// returns 501. Listed explicitly so the table is auditable and so the
// dispatcher can branch on the action name without a per-action stub
// function. SetAVTransportURI moved out of this set in P2.3 (real
// validate+store handler); Play/Stop moved out in P2.4; Pause moved
// out in P3.1; Seek moved out in P3.2. Next/Previous remain stubs —
// v1 has no queue model.
var avtStubActions = map[string]struct{}{
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
	// handler self-contained). Same rationale for Play/Stop: they
	// enforce their own InstanceID validation and have argument-shape
	// checks (Speed) that should be reachable without the generic
	// gate intercepting.
	if action == "SetAVTransportURI" {
		a.handleSetAVTransportURI(w, r, extractSetAVTransportURIArgs(args))
		return
	}
	if action == "Play" {
		a.handlePlay(w, extractPlayArgs(args))
		return
	}
	if action == "Stop" {
		a.handleStop(w, extractStopArgs(args))
		return
	}
	if action == "Pause" {
		a.handlePause(w, extractPauseArgs(args))
		return
	}
	if action == "Seek" {
		a.handleSeek(w, extractSeekArgs(args))
		return
	}

	// Stub actions return 501 (Seek will flip to Impl in P3.2;
	// Next/Previous remain stubs — v1 has no queue model).
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
		// State reflects the live transportState field (P2.4). For
		// Phase 2 the values cycle STOPPED ↔ PLAYING — Pause is still
		// 501 so PAUSED_PLAYBACK isn't reachable from a controller,
		// but the field can carry that value and we surface it
		// faithfully so Phase 3's Pause flip needs no change here.
		// TransportStatus is OK by default and ERROR_OCCURRED when
		// lastError is non-empty (a prior failed SetAVTransportURI,
		// probe, or playback). Spec §Common Action Rules line 326:
		// "On failure, set TransportStatus=ERROR_OCCURRED, store a
		// redacted last error..."
		a.mu.Lock()
		state := a.transportState
		lastErr := a.lastError
		a.mu.Unlock()

		status := "OK"
		if lastErr != "" {
			status = "ERROR_OCCURRED"
		}
		writeSOAPResponse(w, avTransportServiceURN, action, []soapOutArg{
			{Name: "CurrentTransportState", Value: state},
			{Name: "CurrentTransportStatus", Value: status},
			{Name: "CurrentSpeed", Value: "1"},
		})

	case "GetPositionInfo":
		// Spec §Query Actions line 407: Track=1 when a URI is loaded,
		// Track=0 otherwise; TrackURI is the stored URI; RelTime comes
		// from core.Status().Position only when the active session ref
		// matches the current dlna: ref. Foreign sessions never lend us
		// their position (we wouldn't claim them as ours), and the
		// no-active-session case mirrors the loaded-but-not-yet-playing
		// shape with zeros.
		//
		// Locking: snapshot adapter state under mu, drop, then call
		// core.Status() outside mu (CLAUDE.md: never hold a.mu across
		// core.Manager calls).
		a.mu.Lock()
		uri := a.loadedURI
		metaRaw := a.loadedMetaRaw
		metaDuration := a.loadedMeta.Duration
		owned := a.currentRef
		a.mu.Unlock()

		st := a.core.Status()
		ownSession := owned != "" && st.AdapterRef == owned

		track := "0"
		trackDuration := "00:00:00"
		relTime := "00:00:00"
		if uri != "" {
			track = "1"
			// Pick the duration source: live core.Status() wins when we
			// own the session AND its probe surfaced a positive duration;
			// otherwise fall back to the metadata's res@duration. Live
			// streams (Duration == 0 on both sides) stay "00:00:00".
			switch {
			case ownSession && st.Duration > 0:
				trackDuration = formatUPnPDuration(st.Duration)
			case metaDuration > 0:
				trackDuration = formatUPnPDuration(metaDuration)
			}
			// RelTime: only emit a non-zero value for our own session.
			// Live streams (Duration == 0) still produce a meaningful
			// elapsed-time figure — controllers treat that as a wall-clock
			// timer. Foreign / no-session: RelTime stays 00:00:00 because
			// we cannot claim someone else's position.
			if ownSession {
				relTime = formatUPnPDuration(st.Position)
			}
		}
		// AbsTime mirrors RelTime: UPnP convention when the renderer
		// doesn't track an absolute timeline separately is to alias the
		// two — most controllers either ignore AbsTime or accept this.
		writeSOAPResponse(w, avTransportServiceURN, action, []soapOutArg{
			{Name: "Track", Value: track},
			{Name: "TrackDuration", Value: trackDuration},
			{Name: "TrackMetaData", Value: metaRaw},
			{Name: "TrackURI", Value: uri},
			{Name: "RelTime", Value: relTime},
			{Name: "AbsTime", Value: relTime},
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
		// Spec §Query Actions / GetCurrentTransportActions (lines
		// 401-406). Reachable shapes after P3.1:
		//   - No URI: empty (controller knows there's nothing to do).
		//   - STOPPED with URI: "Play".
		//   - PLAYING own seekable VOD: "Pause,Stop,Seek".
		//   - PLAYING own live/unknown-duration: "Pause,Stop".
		//   - PAUSED own seekable VOD: "Play,Stop,Seek".
		//   - PAUSED own live/unknown-duration: "Play,Stop".
		// Seek advertisement is gated on core.Status().Duration > 0
		// AND own session — advertising Seek for a foreign session
		// would lie to the controller (we wouldn't accept the call).
		// Snapshot adapter state under mu, drop, call core.Status()
		// outside mu (CLAUDE.md: never hold mu across core.* calls).
		a.mu.Lock()
		state := a.transportState
		hasURI := a.loadedURI != ""
		owned := a.currentRef
		a.mu.Unlock()

		st := a.core.Status()
		// Seek can only honor an OWN session with known duration. The
		// spec at line 367-369 derives seekability from probe Duration;
		// foreign sessions are rejected by the ownership guard before
		// any core.SeekTo call would run, so advertising Seek for them
		// would be deceptive.
		ownSession := owned != "" && st.AdapterRef == owned
		canSeek := ownSession && st.Duration > 0

		actions := ""
		switch {
		case !hasURI:
			actions = ""
		case state == transportStatePlaying:
			actions = "Pause,Stop"
			if canSeek {
				actions = "Pause,Stop,Seek"
			}
		case state == transportStateStopped:
			actions = "Play"
		case state == transportStatePausedPlayback:
			actions = "Play,Stop"
			if canSeek {
				actions = "Play,Stop,Seek"
			}
		default:
			// TRANSITIONING — Phase 4 eventing. Not reachable today.
			actions = ""
		}
		writeSOAPResponse(w, avTransportServiceURN, action, []soapOutArg{
			{Name: "Actions", Value: actions},
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
