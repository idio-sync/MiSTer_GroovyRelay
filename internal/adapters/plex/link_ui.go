package plex

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

// linkTemplate holds the four account-section fragments: unlinked,
// pending (with PIN code + countdown), linked (with unlink button),
// and expired. Parsed at package init so template errors fail build
// rather than at first render.
var linkTemplate = template.Must(template.New("link").Parse(`
{{define "unlinked"}}
<div class="field" id="plex-link-slot">
	<label>Account</label>
	<div>
		<div class="status-line off">OFF · not linked</div>
		<div class="help">To receive casts, link this bridge to your Plex account.</div>
		<button class="btn ghost" hx-post="/ui/adapter/plex/link/start"
			hx-target="#plex-link-slot" hx-swap="outerHTML"
			hx-headers='{"Sec-Fetch-Site":"same-origin"}'>
			Link Plex Account
		</button>
	</div>
</div>
{{end}}

{{define "pending"}}
<div class="field" id="plex-link-slot"
	hx-get="/ui/adapter/plex/link/status"
	hx-trigger="every 2s"
	hx-target="#plex-link-slot"
	hx-swap="outerHTML">
	<label>Account</label>
	<div>
		<div class="status-line starting">PEND · waiting for plex.tv</div>
		<div class="help">
			Open <a href="https://plex.tv/link" target="_blank">plex.tv/link</a> and enter this code:
		</div>
		<pre style="font-size: 28px; letter-spacing: 0.3em; padding: 8px 0;">{{.Code}}</pre>
		<div class="help">Code expires in {{.CountdownMin}}:{{printf "%02d" .CountdownSec}}</div>
	</div>
</div>
{{end}}

{{define "linked"}}
<div class="field" id="plex-link-slot">
	<label>Account</label>
	<div>
		<div class="status-line run">RUN · linked</div>
		<div class="help">Token persists in {{.TokenPath}}.</div>
		<button class="btn ghost" hx-post="/ui/adapter/plex/unlink"
			hx-target="#plex-link-slot" hx-swap="outerHTML"
			hx-headers='{"Sec-Fetch-Site":"same-origin"}'>
			Unlink
		</button>
	</div>
</div>
{{end}}

{{define "expired"}}
<div class="field" id="plex-link-slot">
	<label>Account</label>
	<div>
		<div class="status-line err">ERR · link code expired</div>
		<div class="help">The 4-character code was not entered at plex.tv within 15 minutes.</div>
		<button class="btn ghost" hx-post="/ui/adapter/plex/link/start"
			hx-target="#plex-link-slot" hx-swap="outerHTML"
			hx-headers='{"Sec-Fetch-Site":"same-origin"}'>
			Try Again
		</button>
	</div>
</div>
{{end}}
`))

// tokenFilePath returns the on-disk path to the persisted token/UUID
// file. Single source of truth so unlink, UI copy, and tokenstore
// agree on the filename (data.json today; kept behind this helper
// so a future rename doesn't drift across files).
func tokenFilePath(dataDir string) string {
	return filepath.Join(dataDir, storedDataFilename)
}

// ExtraPanelHTML is called by the UI when rendering the Plex adapter
// panel. Returns the current linking section HTML as a template.HTML
// so the adapter-panel template renders it as markup, not escaped
// text (review fix C1). Satisfies ui.ExtraHTMLProvider via duck-typing.
//
// Reads TokenStore.AuthToken + pending under a.mu so concurrent
// handleLinkStart / handleUnlink goroutines don't race (review fix C2).
func (a *Adapter) ExtraPanelHTML() template.HTML {
	token := a.snapshotToken()
	pending := a.snapshotPending()

	if token != "" {
		var buf strings.Builder
		_ = linkTemplate.ExecuteTemplate(&buf, "linked", struct {
			TokenPath string
		}{TokenPath: tokenFilePath(a.cfg.Bridge.DataDir)})
		return template.HTML(buf.String())
	}
	if pending != nil && !pending.Done() && !pending.Expired() {
		return template.HTML(renderPending(pending))
	}
	var buf strings.Builder
	_ = linkTemplate.ExecuteTemplate(&buf, "unlinked", nil)
	return template.HTML(buf.String())
}

func renderPending(p *pendingLink) string {
	tl := p.TimeLeft()
	min := int(tl / time.Minute)
	sec := int((tl % time.Minute) / time.Second)
	var buf strings.Builder
	_ = linkTemplate.ExecuteTemplate(&buf, "pending", struct {
		Code         string
		CountdownMin int
		CountdownSec int
	}{p.Code(), min, sec})
	return buf.String()
}

// UIRoutes implements adapters.RouteProvider so the UI server mounts
// these under /ui/adapter/plex/<path>. Paths are relative; the UI
// prepends the adapter prefix + wraps POST handlers in csrfMiddleware.
func (a *Adapter) UIRoutes() []adapters.Route {
	return []adapters.Route{
		{Method: "POST", Path: "link/start", Handler: a.handleLinkStart},
		{Method: "GET", Path: "link/status", Handler: a.handleLinkStatus},
		{Method: "POST", Path: "unlink", Handler: a.handleUnlink},
	}
}

// handleLinkStart delegates to StartLink and returns the "pending" fragment.
func (a *Adapter) handleLinkStart(w http.ResponseWriter, r *http.Request) {
	snap, _ := a.StartLink(r.Context(), nil)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if snap.Phase == adapters.LinkPhaseError {
		http.Error(w, snap.Error, http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte(renderPending(a.snapshotPending())))
}

// pollPendingLink runs PollPIN for one in-flight link flow. On
// success it writes the token to TokenStore under a.mu AND checks
// that a.pending still points at this flow. If the user clicked
// "Link" again in the meantime (abandoning this pendingLink), we
// drop the token on the floor rather than persist a stale auth
// token — that's the I2 "rapid re-click" race fix.
func (a *Adapter) pollPendingLink(pl *pendingLink, pinID int, deviceUUID string) {
	token, err := pollForTokenCtx(pl.ctx, pinID, deviceUUID, 15*time.Minute)
	if err != nil {
		a.finishPendingLink(pl, "", err.Error())
		return
	}

	a.mu.Lock()
	if a.pending != pl {
		// Abandoned by a newer flow; don't clobber its state.
		// Do NOT call finishPendingLink here — abandoned flows must
		// not emit events (they are a race artifact, not a real outcome).
		a.mu.Unlock()
		pl.complete("", "abandoned")
		return
	}
	a.cfg.TokenStore.AuthToken = token
	dataDir := a.cfg.Bridge.DataDir
	store := a.cfg.TokenStore
	a.mu.Unlock()

	// SaveStoredData is disk I/O; run outside a.mu so sidebar polls
	// and other handlers don't block on fsync.
	if err := SaveStoredData(dataDir, store); err != nil {
		a.finishPendingLink(pl, "", fmt.Sprintf("token received but save failed: %v", err))
		return
	}
	a.finishPendingLink(pl, token, "")
}

// finishPendingLink marks pl as done and emits the appropriate lifecycle
// event. Exactly one of token or errMsg must be non-empty:
//
//   - token non-empty → success: emit adapter-linked (Info), complete with token.
//   - errMsg non-empty → failure: emit adapter-link-failed (Err), complete with error.
//
// The abandoned-flow branch in pollPendingLink calls pl.complete directly
// without going through here, so abandoned flows never emit.
func (a *Adapter) finishPendingLink(pl *pendingLink, token, errMsg string) {
	if token != "" {
		pl.complete(token, "")
		a.emit(eventlog.SeverityInfo, "adapter-linked")
		return
	}
	pl.complete("", errMsg)
	a.emit(eventlog.SeverityErr, fmt.Sprintf("adapter-link-failed: %s", errMsg))
}

// handleLinkStatus returns the Account-section fragment for the
// current state. Status codes let htmx triggers distinguish the
// terminal states: 200 = linked/unlinked (stop polling); 202 =
// pending (keep polling); 410 = expired/error (stop polling, show Try Again).
func (a *Adapter) handleLinkStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	snap := a.linkSnapshot()
	switch snap.Phase {
	case adapters.LinkPhaseLinked:
		w.WriteHeader(http.StatusOK)
		_ = linkTemplate.ExecuteTemplate(w, "linked", struct{ TokenPath string }{
			TokenPath: tokenFilePath(a.cfg.Bridge.DataDir),
		})
	case adapters.LinkPhasePending:
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(renderPending(a.snapshotPending())))
	case adapters.LinkPhaseError:
		w.WriteHeader(http.StatusGone)
		_ = linkTemplate.ExecuteTemplate(w, "expired", nil)
	default:
		w.WriteHeader(http.StatusOK)
		_ = linkTemplate.ExecuteTemplate(w, "unlinked", nil)
	}
}

// handleUnlink delegates to Unlink and returns the "unlinked" fragment.
func (a *Adapter) handleUnlink(w http.ResponseWriter, r *http.Request) {
	_, _ = a.Unlink(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = linkTemplate.ExecuteTemplate(w, "unlinked", nil)
}

// pollForTokenCtx wraps PollPIN with ctx cancellation so the
// handleLinkStart background poller can exit early when the
// pendingLink is abandoned (re-click, adapter stop).
func pollForTokenCtx(ctx context.Context, pinID int, uuid string, timeout time.Duration) (string, error) {
	type result struct {
		token string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		token, err := PollPIN(pinID, uuid, timeout)
		done <- result{token, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-done:
		return res.token, res.err
	}
}
