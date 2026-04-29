package url

import (
	"context"
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
// builds a v1.5 SessionRequest (DirectPlay + Capabilities all true)
// and calls Manager.
//
// mode form/JSON field values:
//   - "" or "auto" → host in cfg.YtdlpHosts → ytdlp; else direct
//   - "ytdlp" → forced ytdlp (400 if YtdlpEnabled=false)
//   - "direct" → forced direct (existing v1 behavior)
//
// Spec: docs/specs/2026-04-25-url-ytdlp-design.md §"HTTP surface" +
// docs/specs/2026-04-25-url-adapter-controls-design.md §"Capability
// and DirectPlay flips".
func (a *Adapter) handlePlay(w http.ResponseWriter, r *http.Request) {
	rawURL, mode, err := extractURLAndMode(r)
	if err != nil {
		a.respondError(w, r, http.StatusBadRequest, err.Error(), "url")
		return
	}
	ref, resolvedVia, status, err := a.castURL(r.Context(), rawURL, mode)
	if err != nil {
		field := ""
		if status == http.StatusBadRequest {
			// 400 from castURL is either bad URL or bad mode value;
			// extractURLAndMode caught form-shape errors. We can't
			// always tell which, but "url" is the more common case.
			field = "url"
		}
		a.respondError(w, r, status, err.Error(), field)
		return
	}
	a.respondStarted(w, r, ref, rawURL, resolvedVia)
}

// castURL is the shared cast-spawning logic. It validates the URL,
// records into history (so failed casts surface for one-click retry),
// dispatches direct vs. yt-dlp per mode + cfg + probe, resolves if
// needed, builds the SessionRequest with v1.5 caps, and starts the
// session. Returns the AdapterRef, resolvedVia ("direct" or "ytdlp"),
// the HTTP status to use on error, and the error.
//
// Used by handlePlay, handleReplay, handleResume's live-reconnect
// branch, and handleHistoryPlay. Each of these re-resolves the URL
// (yt-dlp tokens expire), so they all funnel through here.
func (a *Adapter) castURL(ctx context.Context, rawURL, mode string) (ref, resolvedVia string, status int, err error) {
	parsed, perr := stdurl.Parse(rawURL)
	if perr != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", http.StatusBadRequest, fmt.Errorf("not a valid URL")
	}
	switch parsed.Scheme {
	case "http", "https":
		// ok
	default:
		return "", "", http.StatusBadRequest,
			fmt.Errorf("scheme not supported in v1: %s (only http and https)", parsed.Scheme)
	}

	// Record into history regardless of dispatch / cast outcome, so
	// the operator can re-try a typo URL with one click. Spec §"History
	// / Constraints". MUST come BEFORE the dispatch decision so a
	// resolver failure still records.
	a.history.AddOrBump(rawURL)

	// Decide the route. Snapshot resolver under the same lock as cfg
	// and probe — Start writes a.resolver under a.mu, so the read
	// here must hold the lock to avoid a -race detector flag in CI.
	a.mu.Lock()
	cfg := a.cfg
	probe := a.ytdlpProbe
	resolver := a.resolver
	a.mu.Unlock()

	// parsed.Hostname() strips any :port suffix.
	useYtdlp, derr := decideRoute(mode, parsed.Hostname(), cfg, probe)
	if derr != nil {
		return "", "", http.StatusBadRequest, derr
	}

	resolvedVia = "direct"
	streamURL := rawURL
	var headers map[string]string
	var resolvedTitle string

	if useYtdlp {
		if resolver == nil {
			return "", "", http.StatusInternalServerError, fmt.Errorf("resolver not configured")
		}
		res, rerr := resolver.Resolve(ctx, rawURL,
			cfg.YtdlpFormat,
			cookiesPathIfPresent(a.cookiesPath))
		if rerr != nil {
			safeMsg := strings.ReplaceAll(rerr.Error(), rawURL, redactURL(rawURL))
			a.setState(adapters.StateError, safeMsg)
			slog.Warn("yt-dlp resolve failed", "url", redactURL(rawURL), "err", safeMsg)
			return "", "", http.StatusInternalServerError, fmt.Errorf("%s", safeMsg)
		}
		streamURL = res.URL
		headers = res.Headers
		resolvedTitle = res.Title
		resolvedVia = "ytdlp"
		// Backfill the title onto the just-bumped history entry so the
		// panel shows "Big Buck Bunny" rather than just the youtu.be
		// shortlink. SetTitle no-ops on empty title and on missing
		// entries, so this is safe to call unconditionally here.
		a.history.SetTitle(rawURL, resolvedTitle)
	}

	ref = newAdapterRef()
	req := core.SessionRequest{
		StreamURL:    streamURL,
		InputHeaders: headers,
		// v1.5: unconditional caps + DirectPlay so the panel's controls
		// reach core.Manager. Per-source seekability is enforced by the
		// panel (Duration > 0 gating) and by the Resume handler's
		// Duration-based branching. Spec §"Capability and DirectPlay
		// flips".
		Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
		AdapterRef:   ref,
		DirectPlay:   true,
		// OnStop captures rawURL + resolvedTitle at request-construction
		// time, NOT inside the closure body — by the time OnStop runs,
		// adapter state may have been overwritten by a preempting
		// session.
		OnStop: a.makeOnStop(rawURL, resolvedTitle),
	}

	if a.core == nil {
		return "", "", http.StatusInternalServerError, fmt.Errorf("core not wired")
	}
	if serr := a.core.StartSession(req); serr != nil {
		safeMsg := strings.ReplaceAll(serr.Error(), rawURL, redactURL(rawURL))
		a.setState(adapters.StateError, safeMsg)
		slog.Warn("url cast failed", "url", redactURL(rawURL), "err", serr)
		return "", "", http.StatusInternalServerError, fmt.Errorf("%s", safeMsg)
	}

	a.markRunning(rawURL)
	slog.Info("url cast started",
		"url", redactURL(rawURL),
		"ref", ref,
		"resolved_via", resolvedVia)
	if resolvedTitle != "" {
		slog.Debug("url cast resolved", "ref", ref, "title", resolvedTitle)
	}
	return ref, resolvedVia, http.StatusOK, nil
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
		m := strings.ToLower(strings.TrimSpace(payload.Mode))
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
	m := strings.ToLower(strings.TrimSpace(r.Form.Get("mode")))
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
		fmt.Fprintf(w, `<div class="gr-callout err" id="url-panel"><p>%s</p></div>`, template.HTMLEscapeString(msg))
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
			`<div class="gr-callout ok" id="url-panel"><p>Playing: <code>%s</code></p></div>`,
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
