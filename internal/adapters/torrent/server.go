package torrent

import (
	"net"
	"net/http"
	"path"
	"strings"
	"time"
)

func (a *Adapter) MountPublicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /torrent/session/{token}/media", a.handleMedia)
}

func (a *Adapter) handleMedia(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		http.Error(w, "loopback required", http.StatusForbidden)
		return
	}
	token := r.PathValue("token")
	if token == "" {
		token = tokenFromPath(r.URL.Path)
	}
	a.mu.Lock()
	s := a.sessions[token]
	a.mu.Unlock()
	if s == nil {
		http.Error(w, "media token not found", http.StatusNotFound)
		return
	}
	reader, err := s.Torrent.Open(r.Context(), s.FileIndex)
	if err != nil {
		http.Error(w, "open torrent media", http.StatusInternalServerError)
		return
	}
	defer reader.Close()
	http.ServeContent(w, r, s.Title, time.Time{}, reader)
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func tokenFromPath(p string) string {
	clean := path.Clean(p)
	parts := strings.Split(clean, "/")
	if len(parts) >= 4 && parts[1] == "torrent" && parts[2] == "session" {
		return parts[3]
	}
	return ""
}
