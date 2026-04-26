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
	"os"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// handlePlay accepts a paste from the UI form or a JSON POST. Routes
// the URL to either the direct path (existing v1 behavior) or the
// yt-dlp resolver, based on mode + hostname allowlist. On success
// builds a fire-and-forget core.SessionRequest and calls Manager.
//
// mode form/JSON field values:
//   - "" or "auto" → host in cfg.YtdlpHosts → ytdlp; else direct
//   - "ytdlp" → forced ytdlp (400 if YtdlpEnabled=false)
//   - "direct" → forced direct (existing v1 behavior)
//
// Spec: docs/specs/2026-04-25-url-ytdlp-design.md §"HTTP surface".
func (a *Adapter) handlePlay(w http.ResponseWriter, r *http.Request) {
	rawURL, mode, err := extractURLAndMode(r)
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

	// Decide the route. Snapshot resolver under the same lock as cfg
	// and probe — Start writes a.resolver under a.mu (review fix I4),
	// so the read here must hold the lock to avoid a -race detector
	// flag in CI.
	a.mu.Lock()
	cfg := a.cfg
	probe := a.ytdlpProbe
	resolver := a.resolver
	a.mu.Unlock()

	useYtdlp, err := decideRoute(mode, parsed.Host, cfg, probe)
	if err != nil {
		a.respondError(w, r, http.StatusBadRequest, err.Error(), "mode")
		return
	}

	resolvedVia := "direct"
	streamURL := rawURL
	var headers map[string]string
	var resolvedTitle string

	if useYtdlp {
		if resolver == nil {
			a.respondError(w, r, http.StatusInternalServerError, "resolver not configured", "")
			return
		}
		// Pass r.Context() directly — Resolver.Resolve wraps it in
		// context.WithTimeout internally; an outer WithCancel here
		// would be redundant (review fix E1-MIN1 from r2 plan review).
		res, err := resolver.Resolve(r.Context(), rawURL,
			cfg.YtdlpFormat,
			cookiesPathIfPresent(a.cookiesPath))
		if err != nil {
			safeMsg := strings.ReplaceAll(err.Error(), rawURL, redactURL(rawURL))
			a.setState(adapters.StateError, safeMsg)
			slog.Warn("yt-dlp resolve failed", "url", redactURL(rawURL), "err", safeMsg)
			a.respondError(w, r, http.StatusInternalServerError, safeMsg, "")
			return
		}
		streamURL = res.URL
		headers = res.Headers
		resolvedTitle = res.Title
		resolvedVia = "ytdlp"
	}

	ref := newAdapterRef()
	req := core.SessionRequest{
		StreamURL:    streamURL,
		InputHeaders: headers,
		Capabilities: core.Capabilities{CanSeek: false, CanPause: false},
		AdapterRef:   ref,
		DirectPlay:   false,
		// OnStop captures rawURL + resolvedTitle at request-construction
		// time, NOT inside the closure body — by the time OnStop runs,
		// adapter state may have been overwritten by a preempting
		// session (review fix I3 / spec §"Lifecycle integration").
		OnStop: a.makeOnStop(rawURL, resolvedTitle),
	}

	if a.core == nil {
		a.respondError(w, r, http.StatusInternalServerError, "core not wired", "")
		return
	}
	if err := a.core.StartSession(req); err != nil {
		safeMsg := strings.ReplaceAll(err.Error(), rawURL, redactURL(rawURL))
		a.setState(adapters.StateError, safeMsg)
		slog.Warn("url cast failed", "url", redactURL(rawURL), "err", err)
		a.respondError(w, r, http.StatusInternalServerError, safeMsg, "")
		return
	}

	a.markRunning(rawURL)
	slog.Info("url cast started",
		"url", redactURL(rawURL),
		"ref", ref,
		"resolved_via", resolvedVia,
		"title", resolvedTitle)
	a.respondStarted(w, r, ref, rawURL, resolvedVia)
}

// extractURLAndMode parses both fields from form-encoded or JSON bodies.
// mode defaults to "auto" if absent.
func extractURLAndMode(r *http.Request) (rawURL, mode string, err error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 4096))
		if err != nil {
			return "", "", fmt.Errorf("read body: %w", err)
		}
		var payload struct {
			URL  string `json:"url"`
			Mode string `json:"mode"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", "", fmt.Errorf("invalid JSON: %w", err)
		}
		if payload.URL == "" {
			return "", "", fmt.Errorf("url is required")
		}
		m := payload.Mode
		if m == "" {
			m = "auto"
		}
		return strings.TrimSpace(payload.URL), m, nil
	}
	if err := r.ParseForm(); err != nil {
		return "", "", fmt.Errorf("parse form: %w", err)
	}
	v := strings.TrimSpace(r.Form.Get("url"))
	if v == "" {
		return "", "", fmt.Errorf("url is required")
	}
	m := r.Form.Get("mode")
	if m == "" {
		m = "auto"
	}
	return v, m, nil
}

// decideRoute is pure: returns whether to invoke yt-dlp, or an error
// for malformed mode values / forced-ytdlp-when-disabled.
func decideRoute(mode, host string, cfg Config, probe ytdlpProbe) (useYtdlp bool, err error) {
	switch mode {
	case "auto":
		if !cfg.YtdlpEnabled || !probe.OK {
			return false, nil
		}
		return ytdlp.Match(host, cfg.YtdlpHosts), nil
	case "ytdlp":
		if !cfg.YtdlpEnabled {
			return false, fmt.Errorf("yt-dlp resolver is disabled in adapter config")
		}
		if !probe.OK {
			return false, fmt.Errorf("yt-dlp binary not found at runtime")
		}
		return true, nil
	case "direct":
		return false, nil
	default:
		return false, fmt.Errorf("mode must be one of auto, ytdlp, direct (got %q)", mode)
	}
}

// cookiesPathIfPresent returns the cookies path if the file exists,
// or "" otherwise. The resolver passes "" → no --cookies flag.
func cookiesPathIfPresent(path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// makeOnStop captures rawURL + title at request-construction time so
// the closure body uses the captured values, not adapter mutable
// fields that may have been overwritten by a preempting session
// (review fix I3 / spec §"Lifecycle integration").
func (a *Adapter) makeOnStop(rawURL, title string) func(reason string) {
	return func(reason string) {
		switch reason {
		case "eof", "preempted", "stopped", "":
			a.setState(adapters.StateStopped, "")
		default:
			a.setState(adapters.StateError, reason)
		}
		slog.Debug("url session ended",
			"reason", reason,
			"url", redactURL(rawURL),
			"title", title)
	}
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
func (a *Adapter) respondStarted(w http.ResponseWriter, r *http.Request, ref, url, resolvedVia string) {
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
		"adapter_ref":  ref,
		"state":        "running",
		"url":          url,
		"resolved_via": resolvedVia,
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
