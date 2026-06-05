package jellyfin

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// linkVersion is the build version sent in the MediaBrowser auth
// header. Overridden in tests if needed; populated by main.go via
// SetVersion().
var linkVersion = "dev"

// SetVersion is called once at startup from main.go to thread the
// build version through to JF auth headers.
func (a *Adapter) SetVersion(v string) { linkVersion = v }

// handleLinkStart accepts a form-encoded {username, password} POST.
// Server URL is read from the saved adapter config (the same field
// edited in the Settings section above the link form), so operators
// don't have to type it in twice. On success, persists the token and
// renders a "linked-as" fragment. On failure, renders the link form
// with an error fragment underneath. Always returns 200 (htmx
// fragments are 200 + body; non-2xx triggers htmx error handling we
// don't want here).
func (a *Adapter) handleLinkStart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderLinkFragment(w, "Bad form")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	snap, _ := a.StartLink(ctx, map[string]string{
		"username": r.FormValue("username"),
		"password": r.FormValue("password"),
	})
	// Preserve legacy fragment behavior: error text under the form,
	// linked/linking callout otherwise.
	errMsg := ""
	if snap.Phase == adapters.LinkPhaseError || (snap.Phase == adapters.LinkPhaseLinked && snap.Error != "") {
		errMsg = snap.Error
	}
	a.renderLinkFragment(w, errMsg)
}

// handleLinkCancel resets a stuck Linking state to Idle. The browser
// has no way to abort an in-flight HTTP call from the bridge to JF,
// so this is a soft-cancel: the next link/start supersedes any
// in-flight one.
func (a *Adapter) handleLinkCancel(w http.ResponseWriter, r *http.Request) {
	if a.link.State() == LinkLinking {
		a.link.SetIdle()
	}
	a.renderLinkFragment(w, "")
}

// handleUnlink tells JF to log the device out, wipes the local token
// file, resets link state, and stops the running adapter so a stale
// runSession goroutine doesn't keep retrying with the now-invalid
// token. Does NOT call core.Manager.Stop — a mid-cast session goes
// through the bridge-wide stop path.
//
// The /Sessions/Logout call is best-effort: on failure (network down,
// server unreachable, token already rejected) we still proceed with
// local cleanup, since the device row will eventually expire on the
// server side. Bridge-side state must always converge to "unlinked"
// when the operator clicks Unlink, regardless of what JF says.
func (a *Adapter) handleUnlink(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	_, _ = a.Unlink(ctx)
	a.renderLinkFragment(w, "")
}

// configuredServerURL returns the saved Server URL from cfg under the
// adapter mutex. Used by the link form so the operator doesn't have to
// type the URL a second time below the Settings section that already
// owns it.
func (a *Adapter) configuredServerURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.TrimSpace(a.cfg.ServerURL)
}

// linkFragmentHTML returns the link section's inner content as an
// HTML string: a form when not-linked or in error, "Linking…" while
// awaiting auth, or "Linked as ..." with an Unlink button when linked.
// All states are wrapped in a `.section` block so the link UI sits
// alongside Status / Settings with the same heading + spacing.
//
// Must NOT include the outer <div id="jf-link"> wrapper — htmx swaps
// this fragment into the wrapper's innerHTML, so the wrapper has to
// survive across swaps. ExtraPanelHTML adds the wrapper for the
// initial server-rendered render.
func (a *Adapter) linkFragmentHTML(errMsg string) string {
	const sectionOpen = `<div class="section"><h3><span class="num">03 —</span> Account</h3>`
	const sectionClose = `</div>`

	switch a.link.State() {
	case LinkLinked:
		user, sid := a.link.LinkedAs()
		disabledHint := ""
		if !a.IsEnabled() {
			disabledHint = ` <span class="help">Enable Jellyfin in Settings and save to appear in Jellyfin cast menus.</span>`
		}
		return sectionOpen + fmt.Sprintf(
			`<div class="gr-callout ok">Linked as %s on %s.%s <button class="btn ghost" hx-post="/old_ui/adapter/jellyfin/unlink" hx-target="#jf-link">Unlink</button></div>`,
			html.EscapeString(user), html.EscapeString(sid), disabledHint,
		) + sectionClose
	case LinkLinking:
		return sectionOpen + `<div class="gr-callout">Linking…</div>` + sectionClose
	default:
		// Idle or Error.
		if a.configuredServerURL() == "" {
			return sectionOpen +
				`<div class="help">Set a Server URL in Settings above and click Save before linking.</div>` +
				sectionClose
		}
		errBlock := ""
		if errMsg != "" {
			errBlock = fmt.Sprintf(
				`<div class="field"><div></div><div class="err">%s</div></div>`,
				html.EscapeString(errMsg),
			)
		}
		return sectionOpen + `<form hx-post="/old_ui/adapter/jellyfin/link/start" hx-target="#jf-link">
<div class="field"><label for="jf-username">Username</label><div><input type="text" name="username" id="jf-username" required></div></div>
<div class="field"><label for="jf-password">Password</label><div><input type="password" name="password" id="jf-password" required></div></div>
` + errBlock + `<div style="margin-top: 16px; text-align: right;"><button type="submit" class="btn">Link ▸</button></div>
</form>` + sectionClose
	}
}

// renderLinkFragment writes the link fragment as a 200 + html body
// in response to htmx form posts. The body is the inner content only;
// htmx swaps it into the existing #jf-link wrapper rendered by
// ExtraPanelHTML.
func (a *Adapter) renderLinkFragment(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(a.linkFragmentHTML(errMsg)))
}
