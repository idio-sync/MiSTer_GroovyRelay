package dlna

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// Fields returns the four-field UI schema. Per-field ApplyScope is
// the authoritative spec §Config table (lines 540-548 of
// docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md).
//
// device_name is ScopeRestartBridge because SSDP advertisements are
// baked at startup and controllers cache friendlyName against the
// stable UDN — renaming mid-run leaves stale entries on the LAN.
// Everything else is ScopeHotSwap (read at request-evaluation time,
// not at SSDP-init time).
//
// Defaults are pulled from DefaultConfig() so a single source of
// truth drives both the TOML side and the UI prefill.
func (a *Adapter) Fields() []adapters.FieldDef {
	defaults := DefaultConfig()
	return []adapters.FieldDef{
		{
			Key:        "enabled",
			Label:      "Enabled",
			Help:       "Enable SSDP discovery and UPnP control on the trusted LAN. Uses UDP 1900; if the renderer does not appear, check README troubleshooting for bind conflicts.",
			Kind:       adapters.KindBool,
			Default:    defaults.Enabled,
			ApplyScope: adapters.ScopeHotSwap,
		},
		{
			Key:        "device_name",
			Label:      "Device Name",
			Help:       "Friendly name shown in DLNA control points. Restart-bridge: controllers cache friendlyName and SSDP advertisements by device UUID.",
			Kind:       adapters.KindText,
			Default:    defaults.DeviceName,
			Required:   true,
			ApplyScope: adapters.ScopeRestartBridge,
		},
		{
			Key:        "autoplay_on_set_uri",
			Label:      "Autoplay on SetAVTransportURI",
			Help:       "Compatibility mode for controllers that send SetAVTransportURI without Play. Leave off for controllers that send explicit Play commands.",
			Kind:       adapters.KindBool,
			Default:    defaults.AutoplayOnSetURI,
			ApplyScope: adapters.ScopeHotSwap,
		},
		{
			Key:        "allow_public_source_urls",
			Label:      "Allow Public Source URLs",
			Help:       "Allow media URLs that resolve to public-internet addresses. SSRF risk; this still does not allow loopback or link-local targets.",
			Kind:       adapters.KindBool,
			Default:    defaults.AllowPublicSourceURLs,
			ApplyScope: adapters.ScopeHotSwap,
		},
	}
}

// CurrentValues implements ui.ValueProvider via duck-typing — surfaces
// the current cfg values to the UI for form prefill. Keys must match
// the FieldDef.Key values returned from Fields() above.
func (a *Adapter) CurrentValues() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{
		"enabled":                  a.cfg.Enabled,
		"device_name":              a.cfg.DeviceName,
		"autoplay_on_set_uri":      a.cfg.AutoplayOnSetURI,
		"allow_public_source_urls": a.cfg.AllowPublicSourceURLs,
	}
}
