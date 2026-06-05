package plex

// SourceID identifies this adapter to the chassis source-cluster.
func (a *Adapter) SourceID() string { return "plex" }

// Configured returns whether the plex adapter is ready to receive
// casts. Plex requires both an enabled config flag AND a successful
// PIN-flow link to plex.tv (IsLinked() reflects token presence).
func (a *Adapter) Configured() bool { return a.IsEnabled() && a.IsLinked() }
