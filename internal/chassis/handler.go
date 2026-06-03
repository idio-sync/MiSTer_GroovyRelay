package chassis

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"time"
)

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

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data := snapshotFromSession(s.cfg, s.session, s.visualizerViewer, s.volumeViewer, s.transportViewer, s.aux, time.Now())
	data.Version = s.assetVer
	data.SetupMode = s.firstRunActive()
	if data.SetupMode {
		data.SetupStatus = s.setupStatus()
	}
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "shell.html", data); err != nil {
		http.Error(w, "template execute failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if r.URL.Path == "/receiver/static/chassis.css" {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(s.cssBytes)
		return
	}

	staticSub, err := fs.Sub(chassisStaticFS, "static")
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	http.StripPrefix("/receiver/static/", http.FileServer(http.FS(staticSub))).ServeHTTP(w, r)
}
