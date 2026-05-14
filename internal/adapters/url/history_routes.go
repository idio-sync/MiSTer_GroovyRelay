package url

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

// respondPanel writes the current rendered panel as the response body.
// History routes still target the URL panel; active playback controls live in
// the global now-playing banner.
func (a *Adapter) respondPanel(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(a.renderPanel()))
}

func (a *Adapter) respondControlError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w,
		`<div class="gr-callout err" id="url-panel"><p>%s</p></div>`,
		template.HTMLEscapeString(msg))
}

// snapshotLastURL returns a.lastURL under a.mu. Used by banner and companion
// controls that need the most-recent URL for guarded replay/resume or for
// credential redaction in error paths.
func (a *Adapter) snapshotLastURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastURL
}

// redactErr returns err.Error() with any occurrence of lastURL replaced by its
// redacted form.
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

func (a *Adapter) handleHistoryPlay(w http.ResponseWriter, r *http.Request) {
	idx, err := parseFormIdx(r)
	if err != nil {
		a.respondControlError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, ok := a.history.Get(idx)
	if !ok {
		a.respondControlError(w, http.StatusBadRequest, fmt.Sprintf("idx %d out of range", idx))
		return
	}
	// Bump to position 0 before casting; the operator's intent is to use this
	// URL most recently. The bump persists even if castURL fails.
	a.history.AddOrBump(entry.URL)
	_, _, status, cerr := a.castURL(r.Context(), entry.URL, "auto")
	if cerr != nil {
		a.respondControlError(w, status, cerr.Error())
		return
	}
	a.respondPanel(w, http.StatusOK)
}

func (a *Adapter) handleHistoryDelete(w http.ResponseWriter, r *http.Request) {
	idx, err := parseFormIdx(r)
	if err != nil {
		a.respondControlError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.history.Remove(idx) {
		a.respondControlError(w, http.StatusBadRequest, fmt.Sprintf("idx %d out of range", idx))
		return
	}
	a.respondPanel(w, http.StatusOK)
}

func parseFormIdx(r *http.Request) (int, error) {
	if err := r.ParseForm(); err != nil {
		return 0, fmt.Errorf("parse form: %w", err)
	}
	raw := strings.TrimSpace(r.Form.Get("idx"))
	if raw == "" {
		return 0, fmt.Errorf("idx required")
	}
	idx, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("idx not an integer: %s", raw)
	}
	return idx, nil
}
