package torrent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

const maxTorrentUploadBytes = 4 * 1024 * 1024
const maxTorrentMultipartOverheadBytes = 64 * 1024

func (a *Adapter) UIRoutes() []adapters.Route {
	return []adapters.Route{
		{Method: http.MethodGet, Path: "panel", Handler: a.handlePanel},
		{Method: http.MethodGet, Path: "status", Handler: a.handleStatus},
		{Method: http.MethodPost, Path: "play", Handler: a.handlePlay},
		{Method: http.MethodPost, Path: "upload", Handler: a.handleUpload},
		{Method: http.MethodPost, Path: "stop", Handler: a.handleStop},
	}
}

func (a *Adapter) handlePanel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(a.renderPanel()))
}

func (a *Adapter) handleStatus(w http.ResponseWriter, r *http.Request) {
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, a.statusView())
		return
	}
	a.handlePanel(w, r)
}

func (a *Adapter) handlePlay(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.respondRouteError(w, r, http.StatusBadRequest, "parse form: "+err.Error())
		return
	}
	raw := strings.TrimSpace(r.Form.Get("magnet"))
	if raw == "" {
		a.respondRouteError(w, r, http.StatusBadRequest, "magnet is required")
		return
	}
	started, err := a.startMagnet(r.Context(), raw)
	if err != nil {
		a.respondRouteError(w, r, torrentErrorStatus(err), err.Error())
		return
	}
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, started)
		return
	}
	a.handlePanel(w, r)
}

func (a *Adapter) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTorrentUploadBytes+maxTorrentMultipartOverheadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		a.respondRouteError(w, r, http.StatusBadRequest, "parse upload: "+err.Error())
		return
	}
	var body []byte
	found := false
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				a.respondRouteError(w, r, http.StatusRequestEntityTooLarge, "torrent upload exceeds 4 MiB")
				return
			}
			a.respondRouteError(w, r, http.StatusBadRequest, "read multipart: "+err.Error())
			return
		}
		if part.FormName() != "torrent_file" {
			_ = part.Close()
			continue
		}
		found = true
		body, err = io.ReadAll(io.LimitReader(part, maxTorrentUploadBytes+1))
		_ = part.Close()
		if err != nil {
			a.respondRouteError(w, r, http.StatusBadRequest, "read torrent_file: "+err.Error())
			return
		}
		break
	}
	if !found {
		a.respondRouteError(w, r, http.StatusBadRequest, "torrent_file is required")
		return
	}
	if len(body) > maxTorrentUploadBytes {
		a.respondRouteError(w, r, http.StatusRequestEntityTooLarge, "torrent file exceeds 4 MiB")
		return
	}
	started, err := a.startTorrentBytes(r.Context(), body)
	if err != nil {
		a.respondRouteError(w, r, torrentErrorStatus(err), err.Error())
		return
	}
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, started)
		return
	}
	a.handlePanel(w, r)
}

func (a *Adapter) handleStop(w http.ResponseWriter, r *http.Request) {
	if a.core == nil {
		a.respondRouteError(w, r, http.StatusInternalServerError, "core not wired")
		return
	}
	if err := a.core.Stop(); err != nil {
		a.respondRouteError(w, r, http.StatusConflict, err.Error())
		return
	}
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, a.statusView())
		return
	}
	a.handlePanel(w, r)
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *Adapter) respondRouteError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	a.setState(adapters.StateError, msg)
	if wantsJSON(r) {
		respondJSON(w, status, map[string]string{"error": msg})
		return
	}
	http.Error(w, msg, status)
}
