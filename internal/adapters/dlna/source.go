package dlna

// SourceID identifies this adapter to the chassis source-cluster.
func (a *Adapter) SourceID() string { return "dlna" }

// Configured returns whether the dlna adapter is ready to serve.
// SSDP discovery is passive — there is no link state — so configured
// means "operator enabled it in the bridge config." The lamp's
// .casting state is derived from transport.AdapterRef elsewhere.
func (a *Adapter) Configured() bool { return a.IsEnabled() }
