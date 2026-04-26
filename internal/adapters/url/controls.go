package url

// Handlers in this file mirror handlePlay's HTML-output patterns
// (respondError / respondStarted in play.go) but emit HTML-only
// responses. The play endpoint keeps its JSON branch for v1 API
// compatibility; v1.5 control endpoints are panel-only by design
// (spec §"HTTP surface").
//
// All handlers (Pause / Resume / Stop / Replay / Seek) enforce a
// cross-adapter ownership guard FIRST: if Status().AdapterRef is
// non-empty and not "url:"-prefixed, the active session belongs to
// another adapter and the handler refuses with 409. Plex declares
// Capabilities{CanPause: true, CanSeek: true} too, so without this
// guard the URL panel could pause / seek a foreign Plex session.
//
// handleResume's ordering is deliberate: the ownership guard fires
// BEFORE the already-Playing short-circuit so a foreign-Playing
// session returns 409 instead of a misleading silent 200 (IM-2 from
// the prior branch's pre-merge review).
//
// handlePause's check order: ownership guard → already-Paused →
// core.Pause(). The already-Paused short-circuit avoids an FSM-reject
// round-trip (state.go:67-69 rejects EvPause from Paused).
//
// Resume's live-reconnect branch and Replay both call castURL so
// they re-resolve via yt-dlp on each invocation — yt-dlp's resolved
// URLs are short-lived and replaying a cached resolution would fail
// once the token expires.

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// handlePause is POST /ui/adapter/url/pause. Short-circuits to a 200
// + current panel if the manager FSM is already Paused (FSM rejects
// EvPause from Paused; spec state.go:67-69). Otherwise calls
// core.Pause(); errors come back as 409 + error fragment.
func (a *Adapter) handlePause(w http.ResponseWriter, r *http.Request) {
	st := a.core.Status()

	// Cross-adapter ownership guard. Plex also declares
	// Capabilities{CanPause: true}, so Manager.Pause() would otherwise
	// happily pause a foreign session. Mirrors the guard in
	// handleStop / handleResume / handleReplay / handleSeek.
	if st.AdapterRef != "" && !strings.HasPrefix(st.AdapterRef, "url:") {
		a.respondControlError(w, http.StatusConflict, "active session belongs to another adapter")
		return
	}

	// Already-Paused short-circuit (FSM rejects EvPause from Paused).
	if st.State == core.StatePaused {
		a.respondPanel(w, http.StatusOK)
		return
	}
	if err := a.core.Pause(); err != nil {
		a.respondControlError(w, http.StatusConflict, err.Error())
		return
	}
	a.respondPanel(w, http.StatusOK)
}

// handleResume is POST /ui/adapter/url/resume. Branches on the cached
// probe duration to avoid bad ffmpeg -ss seeks against live/HLS:
//
//   Duration > 0  → core.Play() resumes from pausedPosition.
//   Duration == 0 → castURL(lastURL) reconnects from the live edge,
//                   re-resolving via yt-dlp if the host is allowlisted.
//
// Rejects when the active session belongs to another adapter (e.g.,
// Plex preempted between Pause and Resume) so we never accidentally
// route the click into a foreign session. The ownership guard fires
// BEFORE the Playing short-circuit so a foreign-Playing session gets
// 409 instead of a misleading silent 200 (IM-2 from the prior
// branch's pre-merge review). Spec §"Resume branching on Duration".
func (a *Adapter) handleResume(w http.ResponseWriter, r *http.Request) {
	st := a.core.Status()

	// Cross-adapter ownership guard. Runs before the Playing short-
	// circuit so a foreign-Playing session returns 409, consistent
	// with handleStop / handleReplay.
	if st.AdapterRef != "" && !strings.HasPrefix(st.AdapterRef, "url:") {
		a.respondControlError(w, http.StatusConflict, "active session belongs to another adapter")
		return
	}

	// Already-Playing short-circuit (FSM rejects EvPlay from Playing).
	if st.State == core.StatePlaying {
		a.respondPanel(w, http.StatusOK)
		return
	}

	// VOD: resume in place.
	if st.Duration > 0 {
		if err := a.core.Play(); err != nil {
			a.respondControlError(w, http.StatusConflict, redactErr(err, a.snapshotLastURL()))
			return
		}
		a.respondPanel(w, http.StatusOK)
		return
	}

	// Live / unknown duration: clean reconnect from the live edge via
	// castURL — re-resolves through yt-dlp when applicable so an
	// expired resolved-URL token doesn't kill the resume.
	lastURL := a.snapshotLastURL()
	if lastURL == "" {
		a.respondControlError(w, http.StatusBadRequest, "no URL to resume")
		return
	}
	_, _, status, err := a.castURL(r.Context(), lastURL, "auto")
	if err != nil {
		// castURL already redacts the URL inside its error message.
		a.respondControlError(w, status, err.Error())
		return
	}
	a.respondPanel(w, http.StatusOK)
}

// handleSeek is POST /ui/adapter/url/seek. Body: offset_ms=N (form-
// encoded). Clamps to [0, Duration] and refuses on Duration == 0
// (defense in depth — the slider isn't rendered in that state).
// Spec §"Error handling" rows for Seek.
func (a *Adapter) handleSeek(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.respondControlError(w, http.StatusBadRequest, "parse form: "+err.Error())
		return
	}
	raw := strings.TrimSpace(r.Form.Get("offset_ms"))
	if raw == "" {
		a.respondControlError(w, http.StatusBadRequest, "offset_ms required")
		return
	}
	offsetMs, err := strconv.Atoi(raw)
	if err != nil {
		a.respondControlError(w, http.StatusBadRequest, "offset_ms not an integer: "+raw)
		return
	}
	st := a.core.Status()

	// Cross-adapter ownership guard. Plex also declares
	// Capabilities{CanSeek: true}, so Manager.SeekTo() would otherwise
	// seek a foreign stream from URL panel input.
	if st.AdapterRef != "" && !strings.HasPrefix(st.AdapterRef, "url:") {
		a.respondControlError(w, http.StatusConflict, "active session belongs to another adapter")
		return
	}

	if st.Duration <= 0 {
		a.respondControlError(w, http.StatusConflict, "source not seekable")
		return
	}
	durMs := int(st.Duration / time.Millisecond)
	if offsetMs < 0 {
		offsetMs = 0
	}
	if offsetMs > durMs {
		offsetMs = durMs
	}
	if err := a.core.SeekTo(offsetMs); err != nil {
		a.respondControlError(w, http.StatusConflict, redactErr(err, a.snapshotLastURL()))
		return
	}
	a.respondPanel(w, http.StatusOK)
}

// handleStop is POST /ui/adapter/url/stop. Refuses to stop a session
// owned by another adapter; otherwise calls core.Stop (idempotent at
// the manager level — Stop-when-idle returns nil).
func (a *Adapter) handleStop(w http.ResponseWriter, r *http.Request) {
	st := a.core.Status()
	if st.AdapterRef != "" && !strings.HasPrefix(st.AdapterRef, "url:") {
		a.respondControlError(w, http.StatusConflict, "active session belongs to another adapter")
		return
	}
	if err := a.core.Stop(); err != nil {
		a.respondControlError(w, http.StatusConflict, err.Error())
		return
	}
	a.respondPanel(w, http.StatusOK)
}

// handleReplay is POST /ui/adapter/url/replay. Re-casts a.lastURL
// from offset 0 via castURL — re-resolves through yt-dlp when
// applicable so a long-cached resolved URL with an expired token
// doesn't break replay. Same ownership guard as Stop / Resume: if
// the active session belongs to another adapter, do not preempt it.
func (a *Adapter) handleReplay(w http.ResponseWriter, r *http.Request) {
	st := a.core.Status()
	if st.AdapterRef != "" && !strings.HasPrefix(st.AdapterRef, "url:") {
		a.respondControlError(w, http.StatusConflict, "active session belongs to another adapter")
		return
	}
	lastURL := a.snapshotLastURL()
	if lastURL == "" {
		a.respondControlError(w, http.StatusBadRequest, "no URL to replay")
		return
	}
	_, _, status, err := a.castURL(r.Context(), lastURL, "auto")
	if err != nil {
		// castURL already redacts the URL inside its error message.
		a.respondControlError(w, status, err.Error())
		return
	}
	a.respondPanel(w, http.StatusOK)
}

// respondPanel writes the current rendered panel as the response body.
// Used by every successful control handler so the operator's browser
// gets the up-to-date state.
func (a *Adapter) respondPanel(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(a.renderPanel()))
}

// respondControlError writes a small error fragment with the given
// status. Mirrors handlePlay's HTMX error path (play.go's respondError)
// but without the JSON branch — control endpoints are HTML-only in v1.5.
func (a *Adapter) respondControlError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w,
		`<div class="url-panel error" id="url-panel"><p class="err">%s</p></div>`,
		template.HTMLEscapeString(msg))
}

// snapshotLastURL returns a.lastURL under a.mu. Used by handlers that
// need the most-recent URL for credential redaction in error paths
// or for spawning a fresh cast (Resume/Replay).
func (a *Adapter) snapshotLastURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastURL
}

// redactErr returns err.Error() with any occurrence of lastURL replaced
// by its redacted form. Used to scrub raw URLs (with potential
// user:password@ credentials) from error messages echoed to the
// operator's browser. ffprobe stderr can include the URL verbatim;
// core.Play() / SeekTo() can surface that error string.
//
// castURL errors are already redacted internally — handlers that
// receive a castURL error should NOT call redactErr again (it would
// be a no-op but adds noise).
func redactErr(err error, lastURL string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if lastURL == "" {
		return msg
	}
	return strings.ReplaceAll(msg, lastURL, redactURL(lastURL))
}
