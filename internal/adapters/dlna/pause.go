package dlna

import (
	"log/slog"
	"net/http"
)

// pause.go owns the Pause SOAP action handler. Mirrors play.go's
// decomposition (args struct + extractor + handler) so the dispatcher
// in handleAVTransportSOAP looks symmetric across Play / Stop / Pause.
//
// Spec: docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md
//   §Pause, Stop, Seek line 376: "Pause applies the ownership guard,
//   calls core.Pause(), and sets UPnP state to PAUSED_PLAYBACK only
//   after success."

// pauseArgs is the IN-argument set for AVTransport:1 Pause. UPnP
// defines only InstanceID for this action.
type pauseArgs struct {
	InstanceID string
}

// extractPauseArgs pulls Pause arguments from the flat name→value map
// produced by parseSOAPRequest. Mirrors extractStopArgs.
func extractPauseArgs(args map[string]string) pauseArgs {
	return pauseArgs{
		InstanceID: args["InstanceID"],
	}
}

// handlePause implements the Pause SOAP action.
//
// Decision tree:
//
//   1. InstanceID == 0 (else 718).
//   2. Snapshot enabled + currentRef + transportState + lastError under
//      mu, drop. Disabled adapter rejects with 701.
//   3. core.Status() enforces the ownership guard: foreign session
//      rejects with 701.
//   4. No active DLNA session (currentRef == "") rejects with 701 —
//      there is nothing to pause.
//   5. Already STOPPED or already PAUSED_PLAYBACK rejects with 701.
//      The FSM rejects EvPause from those states; surfacing the
//      controller-visible "transition not available" matches the
//      core's view of the world.
//   6. core.Pause() under lock-released conditions; on success,
//      compare-and-clear the transportState flip (same discipline
//      buildAndStartSession uses for currentRef).
//
// Locking discipline: a.mu is never held across the core.Status() or
// core.Pause() call (CLAUDE.md). All snapshots are brief read/release.
func (a *Adapter) handlePause(w http.ResponseWriter, args pauseArgs) {
	if args.InstanceID != "" && args.InstanceID != "0" {
		writeSOAPFault(w, upnpErrInvalidInstanceID)
		return
	}

	a.mu.Lock()
	enabled := a.cfg.Enabled
	owned := a.currentRef
	state := a.transportState
	a.mu.Unlock()

	// Disabled adapter: reject with 701. Same rationale as
	// handleSetAVTransportURI's disabled-gate.
	if !enabled {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// core.Status() enforces the ownership guard. A foreign session
	// (non-empty AdapterRef that doesn't match ours) must remain
	// untouched; a missing DLNA session (owned == "") means there is
	// nothing to pause. Both map to 701 — the spec's "transition not
	// available" framing covers both shapes.
	st := a.core.Status()
	if st.AdapterRef != "" && st.AdapterRef != owned {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}
	if owned == "" {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// Reject EvPause-equivalent transitions from STOPPED or already-
	// PAUSED. The core FSM rejects these too; short-circuiting here
	// avoids a wasted core mutex acquisition and lets the controller
	// see the canonical "transition not available" without burning a
	// log line on every benign repeat-Pause.
	if state == transportStateStopped || state == transportStatePausedPlayback {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	matched, err := a.core.PauseIfAdapterRef(owned)
	if !matched {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}
	if err != nil {
		// Don't echo err.Error() into lastError — a wrapped
		// ffmpeg/dataplane error could surface container paths or
		// internal hostnames into a SOAP GetTransportInfo response
		// (TransportStatus=ERROR_OCCURRED) visible to any DLNA control
		// point. Match the redaction discipline used at
		// buildAndStartSession's StartSession failure path.
		slog.Default().Warn("dlna: core.Pause failed", "err", err, "ref", owned)
		a.setLastError("Pause failed (see bridge logs)")
		writeSOAPFault(w, upnpErrActionFailed)
		return
	}

	// Compare-and-clear the state flip: only set PAUSED_PLAYBACK if
	// our ref is still the active one. If the data plane raced ahead
	// and an OnStop fired while core.Pause was in flight, currentRef
	// has been cleared and we must not clobber the STOPPED state.
	a.mu.Lock()
	if a.currentRef == owned {
		a.transportState = transportStatePausedPlayback
	}
	a.mu.Unlock()
	writeSOAPResponse(w, avTransportServiceURN, "Pause", nil)
}
