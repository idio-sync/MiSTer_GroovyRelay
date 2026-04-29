package jellyfin

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
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
	serverURL := a.configuredServerURL()
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	if serverURL == "" {
		a.link.SetError("set Server URL above and save before linking")
		a.renderLinkFragment(w, "Set a Server URL above and click Save before linking.")
		return
	}
	if username == "" || password == "" {
		a.link.SetError("username and password are required")
		a.renderLinkFragment(w, "Username and password are required.")
		return
	}

	a.link.SetLinking()

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	res, err := AuthenticateByName(ctx, AuthRequest{
		ServerURL: serverURL,
		Username:  username,
		Password:  password,
		DeviceID:  a.deviceID,
		Version:   linkVersion,
	})
	if err != nil {
		a.link.SetError(err.Error())
		a.renderLinkFragment(w, err.Error())
		return
	}

	tok := Token{
		AccessToken: res.AccessToken,
		UserID:      res.UserID,
		UserName:    res.UserName,
		ServerID:    res.ServerID,
		ServerURL:   serverURL,
	}
	if err := SaveToken(a.tokenPath(), tok); err != nil {
		a.link.SetError("link succeeded but persist failed: " + err.Error())
		a.renderLinkFragment(w, err.Error())
		return
	}

	a.link.SetLinked(res.UserName, res.ServerID)

	// Bring the adapter into sync with the freshly-minted token. JF
	// rotates the AccessToken when the same DeviceId re-authenticates,
	// so a runSession goroutine spawned against an earlier token will
	// 401 forever. Stop tears down the stale goroutine; Start spawns a
	// new one that re-reads the token from disk. Idempotent for both
	// adapters that were never started and adapters in StateError.
	a.mu.Lock()
	enabled := a.cfg.Enabled
	a.mu.Unlock()
	if enabled {
		_ = a.Stop()
		if startErr := a.Start(context.Background()); startErr != nil {
			// Link itself succeeded; the adapter restart didn't. Surface
			// the start error to the link fragment so the operator sees
			// it without having to hunt in logs. Fragment still reads as
			// linked because the on-disk token IS valid.
			a.renderLinkFragment(w, "Linked, but adapter restart failed: "+startErr.Error())
			return
		}
	}

	a.renderLinkFragment(w, "")
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

// handleUnlink wipes the token file, resets link state, and stops the
// running adapter so a stale runSession goroutine doesn't keep
// retrying with the now-invalid token. Does NOT call core.Manager.Stop
// — a mid-cast session goes through the bridge-wide stop path.
func (a *Adapter) handleUnlink(w http.ResponseWriter, r *http.Request) {
	_ = WipeToken(a.tokenPath())
	a.link.SetIdle()
	// Stop is idempotent: a no-op when the adapter was never started or
	// is already stopped. We always call it on unlink so any in-flight
	// runSession goroutine — which holds the now-wiped token in its
	// closure — exits instead of pounding JF with 401s.
	_ = a.Stop()
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
			`<div class="gr-callout ok">Linked as %s on %s.%s <button class="btn ghost" hx-post="/ui/adapter/jellyfin/unlink" hx-target="#jf-link">Unlink</button></div>`,
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
		return sectionOpen + `<form hx-post="/ui/adapter/jellyfin/link/start" hx-target="#jf-link">
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
