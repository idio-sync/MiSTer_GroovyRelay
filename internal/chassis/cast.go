package chassis

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// castKindToTab maps the chassis input row's detected kind to the
// QuickCastTab.ID it submits against. VerifyCastTabBindings asserts
// every value resolves to a real tab in the registered adapters at
// startup time.
var castKindToTab = map[string]string{
	"url":    "url",
	"magnet": "torrent-magnet",
	"file":   "torrent-file",
}

// valuesKeyForTab maps each chassis cast kind to the QuickCastField.Name
// the adapter reads from QuickCastRequest.Values. Only used for non-file
// kinds; file uploads use fileFieldForTab.
var valuesKeyForTab = map[string]string{
	"url":    "url",
	"magnet": "magnet",
}

// fileFieldForTab maps each chassis cast kind to the QuickCastField.Name
// the adapter expects for multipart file uploads. The chassis populates
// QuickCastRequest.File.FieldName to match.
var fileFieldForTab = map[string]string{
	"file": "torrent_file",
}

// detectCastKind classifies a chassis input-row payload by URL scheme.
// hasFile takes precedence — when a torrent file is queued, the chip
// renders "TORRENT FILE" regardless of the paste box contents. Empty
// strings and non-supported schemes return "" (chassis renders BAD INPUT).
func detectCastKind(payload string, hasFile bool) string {
	if hasFile {
		return "file"
	}
	parsed, err := url.Parse(strings.TrimSpace(payload))
	if err != nil || parsed.Scheme == "" {
		return ""
	}
	switch strings.ToLower(parsed.Scheme) {
	case "magnet":
		return "magnet"
	case "http", "https":
		return "url"
	}
	return ""
}

// writeCastJSON emits the {"ok": bool, "chip": string?} shape used by
// both /receiver/cast and /receiver/preset/{slot}/cast. Sets
// Content-Type: application/json. When ok is true, chip is omitted
// from the body; when ok is false, chip is required.
func writeCastJSON(w http.ResponseWriter, status int, ok bool, chip string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"ok": ok}
	if !ok {
		body["chip"] = chip
	}
	_ = json.NewEncoder(w).Encode(body)
}

const castKindFormField = "kind"
const castPayloadFormField = "payload"

func (s *Server) handleCastPost(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	var (
		payload  string
		file     *adapters.QuickCastFile
		formKind string
	)
	switch {
	case strings.HasPrefix(contentType, "multipart/form-data"):
		r.Body = http.MaxBytesReader(w, r.Body, adapters.MaxQuickCastBytes)
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeCastJSON(w, http.StatusRequestEntityTooLarge, false, "FILE TOO BIG")
				return
			}
			writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
			return
		}
		if r.MultipartForm == nil {
			writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
			return
		}
		defer r.MultipartForm.RemoveAll()
		formKind = strings.TrimSpace(r.FormValue(castKindFormField))
		payload = strings.TrimSpace(r.FormValue(castPayloadFormField))
		files := r.MultipartForm.File["torrent_file"]
		if len(files) != 1 {
			writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
			return
		}
		for fieldName, headers := range r.MultipartForm.File {
			if fieldName != "torrent_file" && len(headers) > 0 {
				writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
				return
			}
		}
		file = &adapters.QuickCastFile{FieldName: "torrent_file", Header: files[0]}
	default:
		if err := r.ParseForm(); err != nil {
			writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
			return
		}
		formKind = strings.TrimSpace(r.PostFormValue(castKindFormField))
		payload = strings.TrimSpace(r.PostFormValue(castPayloadFormField))
	}
	_ = formKind // client hint only; server re-detects.

	kind := detectCastKind(payload, file != nil)
	if kind == "" {
		writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
		return
	}
	tabID, ok := castKindToTab[kind]
	if !ok {
		writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
		return
	}

	provider, _, ok := s.quickCastProviderForTab(tabID)
	if !ok {
		writeCastJSON(w, http.StatusNotFound, false, "NOT FOUND")
		return
	}

	req := adapters.QuickCastRequest{TabID: tabID, Values: map[string]string{}}
	if file != nil {
		expectedFieldName, ok := fileFieldForTab[kind]
		if !ok {
			writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
			return
		}
		if file.FieldName != expectedFieldName {
			writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
			return
		}
		req.File = file
	} else {
		valuesKey, ok := valuesKeyForTab[kind]
		if !ok {
			writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
			return
		}
		req.Values[valuesKey] = payload
	}

	_, err := provider.HandleQuickCast(r.Context(), req)
	if err != nil {
		var qerr *adapters.QuickCastError
		if errors.As(err, &qerr) {
			writeCastJSON(w, qerr.Status, false, qerr.Chip)
			return
		}
		writeCastJSON(w, http.StatusInternalServerError, false, "CAST FAILED")
		return
	}
	writeCastJSON(w, http.StatusOK, true, "")
}

// quickCastProviderForTab finds the QuickCastProvider that advertises
// the given tab ID. Mirror of internal/ui/playback.go:338. Returns
// (provider, tab, ok).
func (s *Server) quickCastProviderForTab(tabID string) (adapters.QuickCastProvider, adapters.QuickCastTab, bool) {
	if s.cfg.Registry == nil {
		return nil, adapters.QuickCastTab{}, false
	}
	for _, a := range s.cfg.Registry.List() {
		p, ok := a.(adapters.QuickCastProvider)
		if !ok {
			continue
		}
		for _, t := range p.QuickCastTabs() {
			if t.ID == tabID {
				return p, t, true
			}
		}
	}
	return nil, adapters.QuickCastTab{}, false
}

// VerifyCastTabBindings walks the registry's QuickCastProvider adapters
// and asserts every (kind, tabID) and (kind, fieldName) pair in
// castKindToTab/valuesKeyForTab/fileFieldForTab resolves to a real tab
// + field. Called from main.go at startup so adapter renames fail loud
// instead of producing 404s at request time.
func VerifyCastTabBindings(reg *adapters.Registry) error {
	type tabIndex struct {
		tab    adapters.QuickCastTab
		fields map[string]adapters.QuickCastField
	}
	tabs := map[string]tabIndex{}
	for _, a := range reg.List() {
		p, ok := a.(adapters.QuickCastProvider)
		if !ok {
			continue
		}
		for _, t := range p.QuickCastTabs() {
			idx := tabIndex{tab: t, fields: map[string]adapters.QuickCastField{}}
			for _, f := range t.Fields {
				idx.fields[f.Name] = f
			}
			tabs[t.ID] = idx
		}
	}
	for kind, tabID := range castKindToTab {
		if _, ok := tabs[tabID]; !ok {
			return fmt.Errorf("castKindToTab[%q] = %q: tab not registered", kind, tabID)
		}
	}
	for kind, fieldName := range valuesKeyForTab {
		tabID, ok := castKindToTab[kind]
		if !ok {
			return fmt.Errorf("valuesKeyForTab[%q] has no castKindToTab entry", kind)
		}
		idx := tabs[tabID]
		if _, ok := idx.fields[fieldName]; !ok {
			return fmt.Errorf("valuesKeyForTab[%q] = %q: field not present on tab %q", kind, fieldName, tabID)
		}
	}
	for kind, fieldName := range fileFieldForTab {
		tabID, ok := castKindToTab[kind]
		if !ok {
			return fmt.Errorf("fileFieldForTab[%q] has no castKindToTab entry", kind)
		}
		idx := tabs[tabID]
		if _, ok := idx.fields[fieldName]; !ok {
			return fmt.Errorf("fileFieldForTab[%q] = %q: field not present on tab %q", kind, fieldName, tabID)
		}
	}
	return nil
}
