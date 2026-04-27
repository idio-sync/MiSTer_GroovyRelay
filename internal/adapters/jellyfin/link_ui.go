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

// renderLinkFragment writes either a form (when not linked) or a
// "linked-as" fragment (when linked) into w. errMsg is shown above
// the form on error; empty string suppresses it. No template engine
// here — the fragment is small enough to inline, and tests can match
// on substrings without parsing HTML.
func (a *Adapter) renderLinkFragment(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	switch a.link.State() {
	case LinkLinked:
		user, sid := a.link.LinkedAs()
		fmt.Fprintf(w, `<div class="jf-link-status">Linked as %s on %s. <button hx-post="/ui/adapter/jellyfin/unlink" hx-target="#jf-link">Unlink</button></div>`,
			html.EscapeString(user), html.EscapeString(sid))
	case LinkLinking:
		fmt.Fprint(w, `<div class="jf-link-status">Linking…</div>`)
	default:
		// Idle or Error
		errBlock := ""
		if errMsg != "" {
			errBlock = fmt.Sprintf(`<div class="jf-link-error">%s</div>`, html.EscapeString(errMsg))
		}
		fmt.Fprintf(w, `%s<form hx-post="/ui/adapter/jellyfin/link/start" hx-target="#jf-link">
<input type="text" name="server_url" placeholder="https://jellyfin.example.com" required>
<input type="text" name="username" placeholder="username" required>
<input type="password" name="password" placeholder="password" required>
<button type="submit">Link</button>
</form>`, errBlock)
	}
}
