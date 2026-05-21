package chassis

import "mime"

// Register woff2/woff content types at package init. http.FileServer
// falls back to mime.TypeByExtension when serving embedded assets, and
// minimal Linux containers (Alpine, scratch) plus some Windows hosts
// return "" for these extensions, yielding application/octet-stream
// and tripping strict-CSP deployments. Registering once at init keeps
// the static handler deterministic across host environments.
func init() {
	mime.AddExtensionType(".woff2", "font/woff2")
	mime.AddExtensionType(".woff", "font/woff")
}
