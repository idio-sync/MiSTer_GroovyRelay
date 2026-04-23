package plex

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
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

// handleLinkStart asks plex.tv for a fresh PIN, stores a pendingLink,
// spawns a background poller, and returns the "pending" fragment with
// the 4-char code. Re-clicks abandon the prior flow first.
//
// Serialization (review fix I2): linkStartMu is held across RequestPIN
// so two rapid clicks can't interleave — the second call waits for the
// first to return before proceeding, and the stale goroutine's token
// write is rejected by the "pending-identity check" below because
// a.pending has moved on. mu is held only for short writes to pending
// and TokenStore.AuthToken.
func (a *Adapter) handleLinkStart(w http.ResponseWriter, r *http.Request) {
	a.linkStartMu.Lock()
	defer a.linkStartMu.Unlock()

	// Abandon any in-flight prior flow before starting a new one.
	if old := a.snapshotPending(); old != nil && !old.Done() {
		old.abandon()
	}

	deviceUUID := a.cfg.TokenStore.DeviceUUID // TokenStore non-nil (NewAdapter)
	deviceName := a.snapshotCfg().DeviceName

	pin, err := RequestPIN(deviceUUID, deviceName)
	if err != nil {
		http.Error(w, fmt.Sprintf("plex.tv unreachable: %v", err), http.StatusServiceUnavailable)
		return
	}

	// plex.tv PINs expire 15 minutes after creation.
	pl := newPendingLink(pin.Code, pin.ID, time.Now().Add(15*time.Minute))

	a.mu.Lock()
	a.pending = pl
	a.mu.Unlock()

	go a.pollPendingLink(pl, pin.ID, deviceUUID)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderPending(pl)))
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
		pl.complete("", err.Error())
		return
	}

	a.mu.Lock()
	if a.pending != pl {
		// Abandoned by a newer flow; don't clobber its state.
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
		pl.complete("", fmt.Sprintf("token received but save failed: %v", err))
		return
	}
	pl.complete(token, "")
}

// handleLinkStatus returns the Account-section fragment for the
// current state. Status codes let htmx triggers distinguish the
// terminal states: 200 = linked/unlinked (stop polling); 202 =
// pending (keep polling); 410 = expired (stop polling, show Try
// Again).
func (a *Adapter) handleLinkStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	token := a.snapshotToken()
	pending := a.snapshotPending()

	if token != "" {
		w.WriteHeader(http.StatusOK)
		_ = linkTemplate.ExecuteTemplate(w, "linked", struct{ TokenPath string }{
			TokenPath: tokenFilePath(a.cfg.Bridge.DataDir),
		})
		return
	}

	if pending == nil {
		w.WriteHeader(http.StatusOK)
		_ = linkTemplate.ExecuteTemplate(w, "unlinked", nil)
		return
	}
	if pending.Expired() {
		w.WriteHeader(http.StatusGone)
		_ = linkTemplate.ExecuteTemplate(w, "expired", nil)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(renderPending(pending)))
}

// handleUnlink rotates the on-disk token file aside and clears the
// in-memory token. Cancels the plex.tv registration loop if running
// — GDM + Companion continue to serve the LAN; the bridge just
// stops advertising to plex.tv's central index.
//
// Does NOT Stop+Start the adapter. The Task 3.4 review correction
// flagged that a sync.Once finalizeOnce makes such restarts unsafe
// (TimelineBroker.Stop is one-shot). Canceling just the registration
// loop is sufficient to represent "unlinked" state on the wire:
// plex.tv stops hearing from us; LAN discovery continues as an
// unlinked player.
func (a *Adapter) handleUnlink(w http.ResponseWriter, r *http.Request) {
	src := tokenFilePath(a.cfg.Bridge.DataDir)
	dst := filepath.Join(a.cfg.Bridge.DataDir,
		fmt.Sprintf(".%s.unlinked-%d", storedDataFilename, time.Now().Unix()))
	_ = os.Rename(src, dst) // best-effort; a missing file is fine

	// Clear in-memory token + steal regCancel under mu so concurrent
	// handleLinkStart / Stop / ExtraPanelHTML see a consistent view.
	a.mu.Lock()
	a.cfg.TokenStore.AuthToken = ""
	cancel := a.regCancel
	a.regCancel = nil
	a.mu.Unlock()

	// Stop the plex.tv registration loop. GDM + Companion keep running.
	if cancel != nil {
		cancel()
	}

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
