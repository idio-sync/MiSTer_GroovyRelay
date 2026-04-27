package jellyfin

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
