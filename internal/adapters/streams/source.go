package streams

// SourceID identifies this adapter to the chassis source-cluster.
// The chassis maps "streams" to the STREAMS lamp slot.
func (a *Adapter) SourceID() string { return "streams" }

// Configured returns whether the streams adapter is ready to receive
// casts. Streams is bundled, so configuration means "operator has
// enabled it in the bridge config"; remote-manifest readiness is
// orthogonal — disabled-but-ready and enabled-but-empty are both
// possible, but the chassis lamp distinguishes only dark vs amber.
func (a *Adapter) Configured() bool { return a.IsEnabled() }
