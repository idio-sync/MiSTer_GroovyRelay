package ui

import "embed"

// templatesFS holds the html/template files used to render both full
// pages and htmx-swappable fragments. Embedded at build so the Docker
// image is self-contained; no filesystem reads at runtime.
//
//go:embed templates/*.html
var templatesFS embed.FS

// staticFS holds vendored assets served under /ui/static/: htmx 2.0.3,
// the four woff2 font files, and app.css. Embedded wholesale so the
// paths on disk and on the wire line up 1:1.
//
//go:embed static
var staticFS embed.FS
