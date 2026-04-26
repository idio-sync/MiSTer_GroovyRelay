package url

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	stdurl "net/url"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// handlePlay is the POST /play endpoint. It accepts form-encoded or JSON
// bodies, validates the URL (scheme must be http or https, must be
// well-formed), builds a fire-and-forget core.SessionRequest, and calls
// core.Manager.StartSession. Response shape switches on HX-Request.
func (a *Adapter) handlePlay(w http.ResponseWriter, r *http.Request) {
	rawURL, err := extractURL(r)
	if err != nil {
		a.respondError(w, r, http.StatusBadRequest, err.Error(), "url")
		return
	}

	parsed, err := stdurl.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		a.respondError(w, r, http.StatusBadRequest, "not a valid URL", "url")
		return
	}
	switch parsed.Scheme {
	case "http", "https":
		// ok
	default:
		a.respondError(w, r, http.StatusBadRequest,
			fmt.Sprintf("scheme not supported in v1: %s (only http and https)", parsed.Scheme),
			"url")
		return
	}

	ref := newAdapterRef()
	req := core.SessionRequest{
		StreamURL:    rawURL,
		Capabilities: core.Capabilities{CanSeek: false, CanPause: false},
		AdapterRef:   ref,
		DirectPlay:   false, // always false in v1; spec §"Known limitations"
		OnStop:       a.handleOnStop,
	}

	if a.core == nil {
		a.respondError(w, r, http.StatusInternalServerError, "core not wired", "")
		return
	}
	if err := a.core.StartSession(req); err != nil {
		// Redact the URL in stored / returned messages — ffprobe and
		// similar errors echo the raw input URL, which may contain
		// user:password credentials. The slog line below also redacts.
		safeMsg := strings.ReplaceAll(err.Error(), rawURL, redactURL(rawURL))
		a.setState(adapters.StateError, safeMsg)
		slog.Warn("url cast failed", "url", redactURL(rawURL), "err", err)
		a.respondError(w, r, http.StatusInternalServerError, safeMsg, "")
		return
	}

	a.markRunning(rawURL)
	slog.Info("url cast started", "url", redactURL(rawURL), "ref", ref)
	a.respondStarted(w, r, ref, rawURL)
}

// extractURL pulls the "url" field from either a form-encoded body or a
// JSON body, distinguished by Content-Type.
func extractURL(r *http.Request) (string, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 4096))
		if err != nil {
			return "", fmt.Errorf("read body: %w", err)
		}
		var payload struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", fmt.Errorf("invalid JSON: %w", err)
		}
		if payload.URL == "" {
			return "", fmt.Errorf("url is required")
		}
		return strings.TrimSpace(payload.URL), nil
	}
	if err := r.ParseForm(); err != nil {
		return "", fmt.Errorf("parse form: %w", err)
	}
	v := strings.TrimSpace(r.Form.Get("url"))
	if v == "" {
		return "", fmt.Errorf("url is required")
	}
	return v, nil
}

// handleOnStop is the closure handed to core.Manager via SessionRequest.OnStop.
// Reasons "eof", "preempted", "stopped" (literal Manager.Stop reason at
// manager.go:382), and the empty string (treated as "eof") all transition
// to StateStopped and clear lastError. Any other non-empty reason is an
// error path: state -> StateError, lastError -> reason.
func (a *Adapter) handleOnStop(reason string) {
	switch reason {
	case "eof", "preempted", "stopped", "":
		a.setState(adapters.StateStopped, "")
	default:
		a.setState(adapters.StateError, reason)
	}
	slog.Debug("url session ended", "reason", reason)
}

// markRunning records the active URL and transitions to StateRunning.
func (a *Adapter) markRunning(url string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = adapters.StateRunning
	a.lastErr = ""
	a.lastURL = url
	a.stateSince = time.Now()
}

// respondError writes a 4xx/5xx response. HX-Request = HTML fragment;
// otherwise JSON.
func (a *Adapter) respondError(w http.ResponseWriter, r *http.Request, code int, msg, field string) {
	if isHTMXRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		fmt.Fprintf(w, `<div class="url-panel error" id="url-panel"><p class="err">%s</p></div>`, template.HTMLEscapeString(msg))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	payload := map[string]string{"error": msg}
	if field != "" {
		payload["field"] = field
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// respondStarted writes the 202 success response.
//
// The HTMX branch redacts credentials (user:pass@) from the URL before
// echoing it to the browser — the panel could otherwise display a
// password to anyone shoulder-surfing the operator's screen. The JSON
// branch echoes the URL verbatim because the API caller submitted it
// and already possesses any credentials within.
func (a *Adapter) respondStarted(w http.ResponseWriter, r *http.Request, ref, url string) {
	if isHTMXRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w,
			`<div class="url-panel" id="url-panel"><p>Playing: <code>%s</code></p></div>`,
			template.HTMLEscapeString(redactURL(url)))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"adapter_ref": ref,
		"state":       "running",
		"url":         url,
	})
}

// isHTMXRequest mirrors internal/ui/server.go's helper. Local copy so the
// adapter doesn't take a UI-package dependency.
func isHTMXRequest(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// newAdapterRef returns "url:<8 hex>". 4 random bytes is plenty of entropy
// for a single-active-session adapter; collisions are inconsequential
// since AdapterRef is opaque to core.
func newAdapterRef() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "url:" + hex.EncodeToString(b[:])
}

// redactURL returns the URL with any user:password authority component
// stripped. Uses url.URL.Redacted() under the hood; on parse failure
// returns "<unparseable url>" rather than echoing arbitrary user input.
func redactURL(raw string) string {
	u, err := stdurl.Parse(raw)
	if err != nil || u == nil {
		return "<unparseable url>"
	}
	return u.Redacted()
}
