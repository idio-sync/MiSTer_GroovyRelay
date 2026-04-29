package jellyfin

import (
	"encoding/base64"
	_ "embed"
)

// iconPNG is the MiSTer logo embedded at build time. Mirrors
// images/mister_logo.png at the repo root (kept in sync manually
// since go:embed paths cannot escape the package directory).
//
//go:embed icon.png
var iconPNG []byte

// iconDataURL is the icon as a base64-encoded data URL, sent in
// /Sessions/Capabilities/Full so the bridge appears with a real
// logo in JF clients' cast-target menus instead of a generic
// placeholder.
//
// A data URL is preferred over hosting the icon on the bridge's
// HTTP listener because (a) it sidesteps host_ip/port plumbing
// from main.go into this adapter, and (b) it reaches JF clients
// reliably even in --network=host Docker setups or behind
// segmented LANs where the bridge's UI port may not be directly
// reachable from every client device.
//
// Computed once at package init; the ~30KB inflation per cap-POST
// is amortized by clients caching the icon after first render.
var iconDataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(iconPNG)
