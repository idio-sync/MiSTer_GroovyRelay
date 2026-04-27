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

// handleLinkStart accepts a form-encoded {server_url, username,
// password} POST. On success, persists the token and renders a
// "linked-as" fragment. On failure, renders the link form with an
// error fragment underneath. Always returns 200 (htmx fragments are
// 200 + body; non-2xx triggers htmx error handling we don't want
// here).
func (a *Adapter) handleLinkStart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderLinkFragment(w, "Bad form")
		return
	}
	serverURL := strings.TrimSpace(r.FormValue("server_url"))
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	if serverURL == "" || username == "" || password == "" {
		a.link.SetError("server_url, username, and password are all required")
		a.renderLinkFragment(w, "All fields required")
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

// handleUnlink wipes the token file and resets link state. Does NOT
// stop a mid-cast session — that goes through the bridge-wide stop.
// In Phase 4, this is extended to also drop the WS connection.
func (a *Adapter) handleUnlink(w http.ResponseWriter, r *http.Request) {
	_ = WipeToken(a.tokenPath())
	a.link.SetIdle()
	a.renderLinkFragment(w, "")
}

// linkFragmentHTML returns the link section's inner content as an
// HTML string: a form when not-linked or in error, "Linking…" while
// awaiting auth, or "Linked as ..." with an Unlink button when linked.
// Must NOT include the outer <div id="jf-link"> wrapper — htmx swaps
// this fragment into the wrapper's innerHTML, so the wrapper has to
// survive across swaps. ExtraPanelHTML adds the wrapper for the
// initial server-rendered render.
func (a *Adapter) linkFragmentHTML(errMsg string) string {
	switch a.link.State() {
	case LinkLinked:
		user, sid := a.link.LinkedAs()
		return fmt.Sprintf(`<div class="jf-link-status">Linked as %s on %s. <button hx-post="/ui/adapter/jellyfin/unlink" hx-target="#jf-link">Unlink</button></div>`,
			html.EscapeString(user), html.EscapeString(sid))
	case LinkLinking:
		return `<div class="jf-link-status">Linking…</div>`
	default:
		// Idle or Error
		errBlock := ""
		if errMsg != "" {
			errBlock = fmt.Sprintf(`<div class="jf-link-error">%s</div>`, html.EscapeString(errMsg))
		}
		return fmt.Sprintf(`%s<form hx-post="/ui/adapter/jellyfin/link/start" hx-target="#jf-link">
<input type="text" name="server_url" placeholder="https://jellyfin.example.com" required>
<input type="text" name="username" placeholder="username" required>
<input type="password" name="password" placeholder="password" required>
<button type="submit">Link</button>
</form>`, errBlock)
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
