package dlna

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// seek.go owns the Seek SOAP action handler. Mirrors play.go / pause.go
// (args struct + extractor + handler) so the dispatcher in
// handleAVTransportSOAP looks symmetric across Play / Stop / Pause /
// Seek.
//
// Spec: docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md
//   §Pause, Stop, Seek lines 378-379:
//     "Seek supports REL_TIME first. Convert HH:MM:SS or HH:MM:SS.FFF
//      to milliseconds, reject negative targets, reject unknown-
//      duration sources, reject targets beyond known duration, and
//      call core.SeekTo only when the active dlna: session matches
//      currentRef."
//     "Unsupported seek units return 710 Seek mode not supported; bad
//      targets return 711 Illegal seek target."
//
//   §Play lines 366-370 (the seek-derivation block) is normative for
//   ordering — Seek acquires the adapter mutex, snapshots currentRef,
//   drops the mutex for core.Status(), then re-acquires the mutex and
//   re-checks ownership before dropping the mutex to call core.SeekTo.
//   The live duration check happens in that same window; if duration
//   is unknown, the ref no longer matches, or the target is outside
//   the known range, return 711 / 701 without mutating core.

// seekArgs is the IN-argument set for AVTransport:1 Seek.
type seekArgs struct {
	InstanceID string
	Unit       string
	Target     string
}

// extractSeekArgs pulls Seek arguments from the flat name→value map
// produced by parseSOAPRequest.
func extractSeekArgs(args map[string]string) seekArgs {
	return seekArgs{
		InstanceID: args["InstanceID"],
		Unit:       args["Unit"],
		Target:     args["Target"],
	}
}

// handleSeek implements the Seek SOAP action.
//
// Decision tree:
//
//   1. InstanceID == 0 (else 718).
//   2. Unit must be REL_TIME or empty (UPnP default for non-recording
//      renderers); other values → 710. Anything we don't support
//      explicitly (ABS_TIME, REL_COUNT, ABS_COUNT, TRACK_NR, CHAPTER,
//      FRAME) is rejected — pretending to support them and silently
//      coercing to REL_TIME would fool a controller into thinking its
//      command worked.
//   3. Target parses via parseUPnPDuration. Empty / unparseable /
//      negative → 711.
//   4. Disabled-adapter gate: read cfg.Enabled under mu; if false → 701.
//   5. Per-spec ordering (lines 366-370):
//      - Snapshot currentRef under mu, drop.
//      - core.Status() outside mu enforces the foreign-session check
//        (701) and surfaces live Duration.
//      - Validate target against Duration: unknown duration → 711;
//        target beyond duration → 711.
//      - Re-acquire mu and re-check ownership: if currentRef changed
//        OR startInFlight became true (a session-replacement won the
//        race), return 701. Drop mu before calling core.SeekTo.
//   6. core.SeekTo outside mu. On success, transportState is left
//      unchanged (Seek does not transition the FSM — PLAYING stays
//      PLAYING, PAUSED stays PAUSED). On failure: redacted setLastError
//      and 501.
//
// Locking discipline: a.mu is never held across core.Status() or
// core.SeekTo() calls (CLAUDE.md).
func (a *Adapter) handleSeek(w http.ResponseWriter, args seekArgs) {
	if args.InstanceID != "" && args.InstanceID != "0" {
		writeSOAPFault(w, upnpErrInvalidInstanceID)
		return
	}

	// Unit defaults to REL_TIME when omitted. UPnP defines REL_TIME as
	// the default for non-recording renderers, and some controllers
	// rely on that default. Trim whitespace before comparing — a
	// controller emitting "REL_TIME " with trailing space should be
	// honored.
	unit := strings.TrimSpace(args.Unit)
	if unit != "" && unit != "REL_TIME" {
		writeSOAPFault(w, upnpErrSeekModeNotSupp)
		return
	}

	target, err := parseUPnPDuration(args.Target)
	if err != nil {
		writeSOAPFault(w, upnpErrIllegalSeekTarget)
		return
	}

	a.mu.Lock()
	enabled := a.cfg.Enabled
	owned := a.currentRef
	a.mu.Unlock()

	// Disabled adapter: reject with 701. Same rationale as
	// handleSetAVTransportURI's disabled-gate.
	if !enabled {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// No active DLNA session — nothing to seek.
	if owned == "" {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// core.Status() enforces the ownership guard and surfaces the live
	// Duration. A foreign session (non-empty AdapterRef that doesn't
	// match ours) must remain untouched.
	st := a.core.Status()
	if st.AdapterRef != "" && st.AdapterRef != owned {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// Unknown duration (live or pre-probe) → 711. Spec line 378
	// explicitly rejects unknown-duration sources for Seek. The
	// transport-actions advertisement does not list Seek for these,
	// but a controller could still try.
	if st.Duration <= 0 {
		writeSOAPFault(w, upnpErrIllegalSeekTarget)
		return
	}

	// Target beyond known duration → 711. Negative targets are caught
	// in parseUPnPDuration. We allow target == 0 (seek to start) and
	// target == duration (effectively EOF — core.SeekTo handles the
	// edge).
	if target > st.Duration {
		writeSOAPFault(w, upnpErrIllegalSeekTarget)
		return
	}

	// Re-check ownership inside mu. A session-replacement (new
	// SetAVTransportURI minting a fresh ref via markStartInFlight, or
	// a late OnStop clearing currentRef) between our snapshot and
	// here invalidates the seek — bailing with 701 is safer than
	// calling core.SeekTo on a session we no longer own.
	a.mu.Lock()
	stillOwned := a.currentRef == owned && !a.startInFlight
	a.mu.Unlock()
	if !stillOwned {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// Convert to milliseconds for core.SeekTo. target is non-negative
	// and bounded by st.Duration (which is in-range time.Duration), so
	// the int conversion is safe.
	offsetMs := int(target / time.Millisecond)
	if err := a.core.SeekTo(offsetMs); err != nil {
		// Don't echo err.Error() into lastError — a wrapped
		// ffmpeg/dataplane seek error could surface container paths or
		// internal hostnames into a SOAP GetTransportInfo response
		// (TransportStatus=ERROR_OCCURRED) visible to any DLNA control
		// point. Match the redaction discipline used at handlePause /
		// handleStop. Operators have bridge logs (slog below) for the
		// underlying cause.
		slog.Default().Warn("dlna: core.SeekTo failed",
			"err", err, "ref", owned, "offsetMs", offsetMs)
		a.setLastError("Seek failed (see bridge logs)")
		writeSOAPFault(w, upnpErrActionFailed)
		return
	}

	// Seek leaves transportState unchanged. core.Manager.SeekTo keeps
	// the FSM in its current state (PLAYING stays PLAYING, PAUSED
	// stays PAUSED). The data plane re-seeks ffmpeg under the hood.
	writeSOAPResponse(w, avTransportServiceURN, "Seek", nil)
}

// parseUPnPDuration parses a UPnP REL_TIME target string. Two shapes
// are accepted:
//
//   "HH:MM:SS"          — integer seconds
//   "HH:MM:SS.FFF"      — fractional seconds, milliseconds precision
//   "HH:MM:SS,FFF"      — same, with comma decimal (locale-affected
//                         controllers — see metadata.go parseDIDLDuration
//                         comment)
//
// Distinct from parseDIDLDuration: this returns an error for
// unparseable input rather than degrading to 0, because the Seek SOAP
// fault (711) requires the handler to distinguish "unparseable
// target" (error) from "valid target of zero" (success seek to start).
// parseDIDLDuration's lenient zero-on-error semantics suit DIDL
// metadata (where a missing duration just suppresses Seek
// advertisement) but would conflate "00:00:00" with garbage here.
//
// Returns a non-nil error for: empty input, leading sign (negative),
// wrong separator count, non-numeric components, or out-of-range
// minute / second values (>= 60).
func parseUPnPDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errInvalidUPnPDuration
	}
	if strings.HasPrefix(s, "-") {
		return 0, errInvalidUPnPDuration
	}

	dotIdx := strings.IndexAny(s, ".,")
	mainPart := s
	fracPart := ""
	if dotIdx >= 0 {
		mainPart = s[:dotIdx]
		fracPart = s[dotIdx+1:]
	}

	parts := strings.Split(mainPart, ":")
	if len(parts) != 3 {
		return 0, errInvalidUPnPDuration
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 {
		return 0, errInvalidUPnPDuration
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m >= 60 {
		return 0, errInvalidUPnPDuration
	}
	sec, err := strconv.Atoi(parts[2])
	if err != nil || sec < 0 || sec >= 60 {
		return 0, errInvalidUPnPDuration
	}
	total := time.Duration(h)*time.Hour +
		time.Duration(m)*time.Minute +
		time.Duration(sec)*time.Second

	if fracPart != "" {
		// Pad or truncate to 3 digits (milliseconds). UPnP says
		// milliseconds explicitly; accept fewer or more digits
		// gracefully so a controller sending "00:00:30.5" parses as
		// 500ms rather than failing.
		switch {
		case len(fracPart) > 3:
			fracPart = fracPart[:3]
		case len(fracPart) < 3:
			fracPart = fracPart + strings.Repeat("0", 3-len(fracPart))
		}
		ms, err := strconv.Atoi(fracPart)
		if err != nil || ms < 0 {
			return 0, errInvalidUPnPDuration
		}
		total += time.Duration(ms) * time.Millisecond
	}

	return total, nil
}

// errInvalidUPnPDuration is the sentinel parseUPnPDuration returns on
// any parse failure. handleSeek inspects only the (err != nil) shape;
// the value is package-private and not threaded through the SOAP
// envelope.
var errInvalidUPnPDuration = errSeekParse{}

// errSeekParse is the concrete error type for parseUPnPDuration. A
// dedicated type (rather than errors.New on a string) keeps the
// sentinel allocation-free at construction time and makes the package
// boundary clean — callers get a non-nil error and don't need the
// message.
type errSeekParse struct{}

func (errSeekParse) Error() string { return "dlna: invalid UPnP duration" }
