package dlna

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// play.go owns the Play and Stop SOAP action handlers, plus the
// MediaInputPolicy construction and OnStop closure plumbing they share.
//
// Spec: docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md
//   - §Play (lines 351-372): fresh-start vs. paused-resume vs. already-
//     playing branches; CanSeek=true for the manager capability gate;
//     OnStop must capture the minted ref by VALUE.
//   - §Pause, Stop, Seek (lines 374-379): Stop applies the ownership
//     guard, calls core.Stop, and "stopping while already stopped is
//     success."
//   - §Common Action Rules / Session Ref Lifecycle (lines 296-308): the
//     compare-and-clear discipline that lets a stale OnStop coexist
//     with a fresh markStartInFlight without corrupting state.
//
// Phase 2 scope:
//   - Play handles fresh-start, already-PLAYING short-circuit, and the
//     PAUSED_PLAYBACK → core.Play() resume branch (PAUSED_PLAYBACK is
//     not reachable in Phase 2 because Pause is still 501, but the
//     code path lives here so Phase 3 only needs to wire Pause).
//   - Stop calls core.Stop and lets OnStop drive the state transition
//     so the data plane's actual termination is the one source of
//     truth for STOPPED.
//
// Pause / Seek / Next / Previous still return 501 (Phase 3 / never).

// UPnP TransportState constants (spec §Query Actions, line 387-393).
// We expose these as package-private constants so handler code reads
// "transportStatePlaying" instead of a bare string literal — the
// canonical wire spelling lives in one place. PAUSED_PLAYBACK is
// reachable by external state callbacks today (pre-Phase-3 Pause is
// stub) but the handler code path that consumes it is already wired.
const (
	transportStateStopped        = "STOPPED"
	transportStatePlaying        = "PLAYING"
	transportStatePausedPlayback = "PAUSED_PLAYBACK"
	// transportStateTransitioning reserved for Phase 4 eventing — when
	// LastChange fires we'll briefly publish TRANSITIONING ahead of the
	// terminal state. Not used in Phase 2 (state changes are atomic from
	// the controller's perspective because eventing isn't wired).
	transportStateTransitioning = "TRANSITIONING"
)

// dlnaInputPolicy returns the MediaInputPolicy the DLNA adapter applies
// to every SessionRequest. Per spec §SetAVTransportURI lines 343-347:
//   - protocol whitelist: file/http/https/tcp/tls/crypto only
//   - reconnect disabled (the validator approved a specific URL — a
//     reconnect could reach a different IP after a server-side rebind)
//   - rw_timeout 5s so a stalled remote can't pin the data plane
//   - Referer header blocked (a redirect server should not learn
//     internal hostnames via a header echo)
//
// The adapter does NOT toggle this policy per-config: every accepted
// DLNA URL is "untrusted source we just validated" and gets the same
// hardening. Future operator switches (e.g., longer timeout) would
// extend this builder.
//
// Note the absence of "file" in the per-source validator's allowed
// scheme list (urlvalidator.go) and its presence here is deliberate:
// FFmpeg's protocol whitelist is a safety net against ffmpeg
// dereferencing schemes the validator never approved (e.g., a
// mis-encoded URL that the validator rejected at parse time but that
// ffmpeg might interpret differently). The validator is the
// load-bearing scheme gate; this whitelist is defense in depth.
func dlnaInputPolicy() core.MediaInputPolicy {
	return core.MediaInputPolicy{
		ProtocolWhitelist: []string{"file", "http", "https", "tcp", "tls", "crypto"},
		DisableReconnect:  true,
		RWTimeout:         5 * time.Second,
		BlockedHeaders:    []string{"Referer"},
	}
}

func dlnaInputPolicyForSource(canSeek bool) core.MediaInputPolicy {
	if canSeek {
		return dlnaInputPolicy()
	}
	return core.MediaInputPolicy{
		ProtocolWhitelist: []string{"file", "crypto"},
		DisableReconnect:  true,
		RWTimeout:         5 * time.Second,
		BlockedHeaders:    []string{"Referer"},
		DisablePlaylists:  true,
	}
}

// dlnaDisplayMetadata builds the core.DisplayMetadata for a DLNA session
// from the DIDL-Lite title. The title is trimmed so whitespace-only
// strings produce an empty Primary row rather than a blank VFD line.
func dlnaDisplayMetadata(title string) core.DisplayMetadata {
	return core.DisplayMetadata{Primary: strings.TrimSpace(title)}
}

var playBeforeStartAdmissionHook func()

// playArgs is the IN-argument set for AVTransport:1 Play. Speed is the
// decimal-string speed value; UPnP defines this as A_ARG_TYPE_TransportPlaySpeed
// and the only value we honor is "1".
type playArgs struct {
	InstanceID string
	Speed      string
}

// stopArgs is the IN-argument set for AVTransport:1 Stop. Stop only
// takes InstanceID.
type stopArgs struct {
	InstanceID string
}

// extractPlayArgs pulls Play arguments from the flat name→value map
// produced by parseSOAPRequest. Mirrors extractSetAVTransportURIArgs.
func extractPlayArgs(args map[string]string) playArgs {
	return playArgs{
		InstanceID: args["InstanceID"],
		Speed:      args["Speed"],
	}
}

// extractStopArgs pulls Stop arguments from the flat name→value map.
func extractStopArgs(args map[string]string) stopArgs {
	return stopArgs{
		InstanceID: args["InstanceID"],
	}
}

// handlePlay implements the Play SOAP action.
//
// Decision tree (spec §Play, lines 353-372):
//
//  1. InstanceID == 0 (else 718).
//  2. Speed == "1" (else 717). Empty Speed defaults to "1" — some
//     controllers omit it on the assumption the renderer treats
//     missing-Speed as 1, which matches AVTransport:1's default.
//  3. Snapshot enabled + currentRef + transportState + startInFlight
//     under mu, drop, call core.Status(), enforce ownership.
//  4. Branch:
//     a. Already PLAYING with own session → success no-op.
//     b. PAUSED_PLAYBACK with own session → either core.Play() (VOD
//     with known duration) or rebuild + StartSession with
//     SeekOffsetMs=0 for live/unknown-duration (live-edge resume
//     per spec §Play line 358).
//     c. No active DLNA session AND a URI is loaded → fresh start
//     via startFreshSession.
//     d. No active DLNA session AND no URI loaded → 701.
//
// The mutex is never held across core.* calls (CLAUDE.md discipline):
// every snapshot is a brief lock/read/release, and every state
// mutation that follows a successful core.* call re-acquires the
// lock for the write only.
func (a *Adapter) handlePlay(w http.ResponseWriter, args playArgs) {
	if args.InstanceID != "" && args.InstanceID != "0" {
		writeSOAPFault(w, upnpErrInvalidInstanceID)
		return
	}
	// Speed validation. The spec only requires "1"; a missing value
	// is treated as "1" because UPnP defines that as the default and
	// some controllers omit the argument.
	if args.Speed != "" && args.Speed != "1" {
		writeSOAPFault(w, upnpErrPlaySpeedNotSupp)
		return
	}

	// Snapshot adapter-side ownership state under mu. enabled and
	// startInFlight are read here so the disabled-adapter gate (mirrors
	// handleSetAVTransportURI's 701) and the concurrent-Play guard
	// (symmetric with SetAVTransportURI's busy-reject) can short-circuit
	// before any core.* call.
	a.mu.Lock()
	enabled := a.cfg.Enabled
	owned := a.currentRef
	state := a.transportState
	hasURI := a.loadedURI != ""
	inFlight := a.startInFlight
	a.mu.Unlock()

	// Disabled adapter: reject with 701. Same rationale as
	// handleSetAVTransportURI (line 102-105) — 401 would imply the action
	// is not in the SCPD, which is wrong; 701 communicates "this
	// renderer is currently unavailable for transport control."
	if !enabled {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// Concurrent-Play guard. handleSetAVTransportURI already rejects on
	// startInFlight (spec line 332); applying the same rule here keeps
	// near-simultaneous Play calls from each minting their own ref. The
	// compare-and-clear discipline would still preserve correctness, but
	// short-circuiting is cleaner and matches the spec's symmetric
	// treatment of the two admission gates.
	if inFlight {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// core.Status() enforces the ownership guard. Empty AdapterRef →
	// no foreign session; matching ref → our session; otherwise 701.
	st := a.core.Status()
	foreignActive := st.AdapterRef != "" && st.AdapterRef != owned

	// Sub-case (a): already playing OUR session — success no-op. Spec
	// line 371 ("If already playing, return success and refresh
	// state"). The FSM rejects EvPlay from Playing in core, so calling
	// core.Play() here would error; short-circuit instead.
	if state == transportStatePlaying && owned != "" && st.AdapterRef == owned {
		writeSOAPResponse(w, avTransportServiceURN, "Play", nil)
		return
	}

	// Sub-case (b): paused our session.
	if state == transportStatePausedPlayback && owned != "" && st.AdapterRef == owned {
		// Spec §Play line 358: "For seekable sources with known
		// duration, call core.Play(). For live or unknown-duration
		// sources, rebuild the same core.SessionRequest with
		// SeekOffsetMs=0 and call core.StartSession to reconnect from
		// the live edge."
		if st.Duration > 0 {
			matched, err := a.core.PlayIfAdapterRef(owned)
			if !matched {
				writeSOAPFault(w, upnpErrTransitionNotAvail)
				return
			}
			if err != nil {
				// Don't echo err.Error() into lastError — a wrapped
				// ffmpeg/dataplane error could surface container paths
				// or internal hostnames into a SOAP GetTransportInfo
				// response (TransportStatus=ERROR_OCCURRED) visible to
				// any DLNA control point. Match the redaction
				// discipline used at buildAndStartSession's StartSession
				// failure path. Operators have bridge logs (slog below)
				// for the underlying cause.
				slog.Default().Warn("dlna: core.Play resume failed", "err", err, "ref", owned)
				a.setLastError("Play resume failed (see bridge logs)")
				a.publishAVTransportLastChange()
				writeSOAPFault(w, upnpErrActionFailed)
				return
			}
			a.setTransportState(transportStatePlaying)
			a.publishAVTransportLastChange()
			writeSOAPResponse(w, avTransportServiceURN, "Play", nil)
			return
		}

		// Live / unknown-duration resume: rebuild the same SessionRequest
		// at the live edge while preserving the existing AdapterRef. If
		// probe/start fails before core replaces the plane, the paused
		// session remains owned by DLNA and visible to future actions.
		if code := a.buildAndStartSession(0, owned); code != 0 {
			writeSOAPFault(w, code)
			return
		}
		writeSOAPResponse(w, avTransportServiceURN, "Play", nil)
		return
	}

	// Foreign-session reject. We get here when state was STOPPED but
	// core has a non-DLNA active session — the ownership guard (spec
	// §Common Action Rules line 294) refuses to mutate it.
	if foreignActive {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// Sub-case (c) / (d): no active DLNA session.
	if !hasURI {
		// Spec §Play: no active session AND no URI loaded → 701. This
		// is "Transition not available" because the request is well-
		// formed but the renderer has nothing to play.
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// Sub-case (c): fresh start.
	if code := a.startFreshSession(); code != 0 {
		writeSOAPFault(w, code)
		return
	}
	writeSOAPResponse(w, avTransportServiceURN, "Play", nil)
}

// startFreshSession is the helper handlePlay's sub-case 4(c) and the
// autoplay_on_set_uri branch in handleSetAVTransportURI both call.
// Builds a SessionRequest from the loaded URI/metadata, mints a fresh
// ref + OnStop closure, and calls core.StartSession at SeekOffsetMs=0.
// Returns 0 on success or a UPnP fault code on failure.
//
// Thin wrapper over buildAndStartSession that encodes the "fresh start"
// intent: SeekOffsetMs=0 from a STOPPED state with no expected active core
// ref. The PAUSED→live-edge reconnect branch passes its current ref so the
// same DLNA session remains visible if reconnect fails before core replaces
// the plane.
func (a *Adapter) startFreshSession() (faultCode upnpErrorCode) {
	return a.buildAndStartSession(0, "")
}

// buildAndStartSession is the shared core of startFreshSession and the
// PAUSED→live-edge reconnect branch in handlePlay. Fresh starts mint a
// ref; live-edge reconnects preserve the existing ref. Both paths build
// the SessionRequest from the currently loaded URI and call core at the
// given SeekOffsetMs. Returns
// 0 on success or a UPnP fault code on failure.
//
// Locking discipline (spec §Session Ref Lifecycle steps 1-6):
//
//  1. Snapshot the loaded URI under mu.
//  2. markStartInFlightFor under mu (sets startInFlight and either
//     mints currentRef or preserves the expected currentRef).
//  3. Build the OnStop closure that captures the session ref
//     by VALUE — so a late OnStop from a superseded session no-ops
//     on its own captured ref instead of clobbering ours.
//  4. Drop mu before calling core.StartSessionIfAdapterRef.
//  5. On success: clearStartInFlight(ref, true) keeps currentRef and
//     flips transportState to PLAYING.
//  6. On failure: fresh starts roll back the minted ref; live-edge
//     reconnects keep the paused ref/state. Both record lastError and
//     map to 716 (probe unreachable) or 501 (anything else) per the
//     spec's fault mapping table.
//
// Why a shared helper: the live-edge reconnect path in handlePlay
// rebuilds the same SessionRequest the fresh-start path builds, while
// preserving the prior ref. Centralizing here keeps the ref/OnStop
// capture, the SessionRequest shape, and the rollback discipline in one
// place.
func (a *Adapter) buildAndStartSession(seekOffsetMs int, expectedCoreRef string) (faultCode upnpErrorCode) {
	a.mu.Lock()
	hasLoadedURI := a.loadedURI != ""
	a.mu.Unlock()
	if !hasLoadedURI {
		// Defensive guard. Callers already validated hasURI before
		// reaching here; if we somehow got here without a URI, return
		// 701 rather than calling core with an empty StreamURL.
		return upnpErrTransitionNotAvail
	}
	if playBeforeStartAdmissionHook != nil {
		playBeforeStartAdmissionHook()
	}

	// Admit this StartSession attempt atomically. Fresh starts mint a
	// new ref; live-edge resumes preserve the paused session's existing
	// ref. Either way, only one start may be in flight at a time.
	ref, admitted := a.markStartInFlightFor(expectedCoreRef)
	if !admitted {
		return upnpErrTransitionNotAvail
	}
	rollbackAdmission := func() {
		if expectedCoreRef == "" {
			a.clearStartInFlight(ref, false)
		} else {
			a.clearStartInFlightPreserveRef(ref)
		}
	}

	// Snapshot cleanup-capable playback state only after admission. While
	// currentRef/startInFlight is set, SetAVTransportURI rejects and
	// Adapter.Stop leaves loaded HLS caches alone, so the cache we hand to
	// core cannot be deleted before this session's OnStop owns cleanup.
	a.mu.Lock()
	loadedURI := a.loadedURI
	playbackURI := a.loadedPlaybackURI
	canSeek := a.loadedCanSeek
	allowPublic := a.cfg.AllowPublicSourceURLs
	a.mu.Unlock()
	if loadedURI == "" {
		rollbackAdmission()
		return upnpErrTransitionNotAvail
	}
	if playbackURI == "" && !canSeek {
		policy := PolicyPrivateOnly
		if allowPublic {
			policy = PolicyAllowPublic
		}
		ctx, cancel := context.WithTimeout(context.Background(), hlsFetchTimeout)
		hls, err := prepareHLSPlaybackForAdapter(ctx, loadedURI, policy)
		cancel()
		if err != nil {
			rollbackAdmission()
			a.setLastError("Play rejected: HLS validation failed")
			a.publishAVTransportLastChange()
			return upnpErrResourceNotFound
		}
		if !a.replaceLoadedPlaybackURIIfCurrent(loadedURI, hls.PlaybackURI, hls.Cleanup, false) {
			rollbackAdmission()
			_ = hls.Cleanup()
			return upnpErrTransitionNotAvail
		}
		playbackURI = hls.PlaybackURI
	}
	if playbackURI == "" {
		playbackURI = loadedURI
	}

	var sessionCleanup func() error
	if !canSeek {
		a.mu.Lock()
		if a.loadedPlaybackURI == playbackURI {
			sessionCleanup = a.loadedHLSCleanup
		}
		a.mu.Unlock()
	}
	onStop := a.onStopForRef(ref, playbackURI, sessionCleanup)

	a.mu.Lock()
	didlTitle := a.loadedMeta.Title
	a.mu.Unlock()

	req := core.SessionRequest{
		StreamURL:        playbackURI,
		Capabilities:     core.Capabilities{CanSeek: canSeek, CanPause: true},
		AdapterRef:       ref,
		DirectPlay:       canSeek,
		SeekOffsetMs:     seekOffsetMs,
		OnStop:           onStop,
		MediaInputPolicy: dlnaInputPolicyForSource(canSeek),
		Title:            didlTitle,
		DisplayMetadata:  dlnaDisplayMetadata(didlTitle),
	}

	matched, err := a.core.StartSessionIfAdapterRef(req, expectedCoreRef)
	if !matched {
		if expectedCoreRef == "" {
			a.clearStartInFlight(ref, false)
		} else {
			a.clearStartInFlightPreserveRef(ref)
		}
		return upnpErrTransitionNotAvail
	}
	if err != nil {
		// Roll back the ref and record a redacted last error. Spec table
		// at line 321: "Backend probe/playback failure → 716 when the
		// resource cannot be reached, otherwise 501 Action Failed."
		// core.Manager wraps probe failures with ErrProbeUnreachable
		// (P3.0); anything else (plane setup, FSM transitions, future
		// policy rejection) maps to 501. The match here is errors.Is
		// rather than direct equality so the joined/wrapped chain
		// works.
		if expectedCoreRef == "" {
			a.clearStartInFlight(ref, false)
		} else {
			a.clearStartInFlightPreserveRef(ref)
		}
		// Don't include err.Error() verbatim — ffprobe stderr can
		// echo internal hosts or path fragments. Just record that
		// StartSession failed; redactURL gives a scheme+host snapshot
		// for operator context.
		a.setLastError("StartSession failed for " + redactURL(loadedURI))
		if expectedCoreRef == "" {
			// Fresh-start failure rolls back to STOPPED/no ref. Live-edge
			// resume failure preserves the paused ref/state because core
			// may still own that session.
			a.mu.Lock()
			if a.currentRef == "" {
				a.transportState = transportStateStopped
			}
			a.mu.Unlock()
		}
		a.publishAVTransportLastChange()
		if errors.Is(err, core.ErrProbeUnreachable) {
			return upnpErrResourceNotFound
		}
		return upnpErrActionFailed
	}

	// Success path. clearStartInFlight(ref, true) keeps currentRef =
	// ref and clears the in-flight flag. The compare-and-clear is
	// safe: if a faster OnStop already won the race (e.g., the data
	// plane immediately failed and fired our own OnStop before
	// StartSession returned), currentRef is already cleared and this
	// is a no-op.
	a.clearStartInFlight(ref, true)
	// Reach into mu only when our ref is still the active one — same
	// compare-and-clear discipline. Otherwise we'd risk overwriting
	// STOPPED that an OnStop just set.
	a.mu.Lock()
	if a.currentRef == ref {
		a.transportState = transportStatePlaying
		a.lastError = ""
	}
	a.mu.Unlock()
	a.publishAVTransportLastChange()
	return 0
}

// onStopForRef returns the OnStop closure for a session minted with
// `ref`. The closure captures `ref` by VALUE so a late callback from
// a superseded session no-ops on the compare-and-clear (spec
// §Session Ref Lifecycle step 4).
//
// On a matching ref, the closure clears currentRef + startInFlight,
// flips transportState to STOPPED, and records non-routine reasons
// as lastError. "stopped" is the controller-initiated routine
// teardown (or the test fake's default reason) — that one is silent.
// Other reasons (probe error, plane error, preempted) become operator-
// visible via lastError so query actions surface ERROR_OCCURRED.
//
// Phase 4 will add LastChange firing here once eventing lands.
func (a *Adapter) onStopForRef(ref string, playbackURI string, cleanup func() error) func(string) {
	return func(reason string) {
		a.mu.Lock()
		// Compare-and-clear: only act on equality (spec line 303).
		if a.currentRef != ref {
			a.mu.Unlock()
			return
		}
		if cleanup != nil && a.loadedPlaybackURI == playbackURI {
			a.loadedPlaybackURI = ""
			a.loadedHLSCleanup = nil
			a.loadedCanSeek = false
		}
		a.currentRef = ""
		a.startInFlight = false
		a.transportState = transportStateStopped
		// "stopped" / "" are routine — no error surface. Anything else
		// (preempted, eof, probe error, plane error) is operator-
		// observable via TransportStatus=ERROR_OCCURRED on the next
		// GetTransportInfo poll.
		if reason != "" && reason != "stopped" {
			a.lastError = "playback ended: " + reason
		}
		a.mu.Unlock()
		if cleanup != nil {
			_ = cleanup()
		}
		a.publishAVTransportLastChange()
	}
}

// setTransportState atomically updates transportState. Used by handlers
// that already validated all preconditions and need a one-line state
// flip without juggling lock scope. Callers must NOT hold mu.
func (a *Adapter) setTransportState(s string) {
	a.mu.Lock()
	a.transportState = s
	a.mu.Unlock()
}

// handleStop implements the Stop SOAP action.
//
// Spec §Pause, Stop, Seek line 376-377: Stop applies the ownership
// guard, calls core.Stop(), and "stopping while already stopped is
// success." The transportState transition to STOPPED is driven by
// the OnStop callback registered in handlePlay's startFreshSession,
// NOT by handleStop directly — OnStop is the single source of truth
// for state-on-end so the transition reflects actual data-plane
// termination ordering.
//
// Locking: snapshot ownership under mu, drop, call core.Stop(),
// return. The OnStop closure mutates state from a goroutine.
func (a *Adapter) handleStop(w http.ResponseWriter, args stopArgs) {
	if args.InstanceID != "" && args.InstanceID != "0" {
		writeSOAPFault(w, upnpErrInvalidInstanceID)
		return
	}

	a.mu.Lock()
	enabled := a.cfg.Enabled
	owned := a.currentRef
	a.mu.Unlock()

	// Disabled adapter: reject with 701. Same rationale as
	// handleSetAVTransportURI's disabled-gate (control actions on a
	// stale URI must fail cleanly rather than potentially preempt a
	// valid Plex/Jellyfin session via core).
	if !enabled {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	st := a.core.Status()
	if st.AdapterRef != "" && st.AdapterRef != owned {
		// Foreign session — leave it alone. Spec §Common Action Rules
		// line 294.
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// "Stopping while already stopped is success" (spec line 377). We
	// detect this via currentRef == "" — the adapter has no active
	// session ref, so there is nothing to stop. core.Manager would
	// also accept the call as a no-op when idle, but short-circuiting
	// here avoids waking the core mutex for a no-op.
	if owned == "" {
		writeSOAPResponse(w, avTransportServiceURN, "Stop", nil)
		return
	}

	matched, err := a.core.StopIfAdapterRef(owned)
	if !matched {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}
	if err != nil {
		// Don't echo err.Error() into lastError — a wrapped
		// ffmpeg/dataplane shutdown error could surface container paths
		// or internal hostnames into a SOAP GetTransportInfo response
		// (TransportStatus=ERROR_OCCURRED) visible to any DLNA control
		// point. Match the redaction discipline used at
		// startFreshSession's StartSession failure path. Operators have
		// bridge logs (slog below) for the underlying cause.
		slog.Default().Warn("dlna: core.Stop failed", "err", err, "ref", owned)
		a.setLastError("Stop failed (see bridge logs)")
		a.publishAVTransportLastChange()
		writeSOAPFault(w, upnpErrActionFailed)
		return
	}
	// The OnStop callback registered in startFreshSession will run
	// from a goroutine and update transportState / currentRef. We
	// return success without waiting; controllers poll
	// GetTransportInfo for the actual STOPPED reflection. If the data
	// plane's teardown is fast enough to fire OnStop before we
	// respond here, that's also fine — the response payload doesn't
	// depend on transportState.
	writeSOAPResponse(w, avTransportServiceURN, "Stop", nil)
}
