package jellyfin

import "html/template"

// ExtraPanelHTML implements ui.ExtraHTMLProvider. The settings panel
// template renders the returned markup in the {{.ExtraHTML}} slot.
//
// We wrap the link fragment in <div id="jf-link"> so that subsequent
// htmx form posts (which target #jf-link) swap into the wrapper's
// innerHTML, leaving the wrapper itself intact across swaps. Without
// this, the htmx target would be missing on first page load and the
// link form would be unreachable from the browser — operators would
// have to use --link-jellyfin headlessly.
//
// Returned content is trusted markup: linkFragmentHTML escapes its
// per-value interpolations via html.EscapeString.
func (a *Adapter) ExtraPanelHTML() template.HTML {
	return template.HTML(`<div id="jf-link">` + a.linkFragmentHTML("") + `</div>`)
}

// LinkSummary is what the UI's panel template displays in the link
// section. Built from LinkState; safe to inspect concurrently.
type LinkSummary struct {
	Phase     string // "idle" | "linking" | "linked" | "error"
	UserName  string // populated when Phase == "linked"
	ServerID  string // populated when Phase == "linked"
	LastError string // populated when Phase == "error"
}

// LinkSummary returns a snapshot of the adapter's link state. Used by
// the UI's panel template via the standard CurrentValues / template
// rendering path. Tests verify the surface is stable.
func (a *Adapter) LinkSummary() LinkSummary {
	user, sid := a.link.LinkedAs()
	return LinkSummary{
		Phase:     a.link.State().String(),
		UserName:  user,
		ServerID:  sid,
		LastError: a.link.LastError(),
	}
}
