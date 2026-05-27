package jellyfin

// SourceID identifies this adapter to the chassis source-cluster.
func (a *Adapter) SourceID() string { return "jellyfin" }

// Configured returns whether the jellyfin adapter is ready to receive
// casts. Requires both the enabled config flag AND a successful
// server-link operation (IsLinked() reflects credentialed-server
// presence). Per-server visibility is out of scope for the 3B lamp.
func (a *Adapter) Configured() bool { return a.IsEnabled() && a.IsLinked() }
