package chassis

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
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

// presetEditBody is the JSON envelope shared by the three preset edit
// helpers. omitempty rules:
//   - Success: Ok=true, Chip stays empty (omitted).
//   - Error: Ok=false, Chip set, Starred/Slot/Cleared stay zero
//     (omitted by *bool / omitempty rules below).
//   - Star success: Ok=true, Starred=*true, Slot in 1..12 (omitted when 0).
//   - Move success: Ok=true; all other fields zero/nil and omitted.
//
// Starred is a *bool so the unstarred-success path emits "starred":false
// (a value bool would be omitempty-dropped when false).
type presetEditBody struct {
	Ok      bool   `json:"ok"`
	Chip    string `json:"chip,omitempty"`
	Starred *bool  `json:"starred,omitempty"`
	Slot    int    `json:"slot,omitempty"`
	Cleared []int  `json:"cleared,omitempty"`
}

// writePresetStarSuccess emits {"ok":true,"starred":<starred>,...} with
// Slot populated on the starred path and Cleared populated on the
// unstarred path. Callers pass zero values for the inapplicable fields.
func writePresetStarSuccess(w http.ResponseWriter, starred bool, slot int, cleared []int) {
	body := presetEditBody{Ok: true, Starred: &starred}
	if starred {
		body.Slot = slot
		// Cleared MUST be nil on the starred path; the field's omitempty
		// rule will drop it. Caller passing a non-nil cleared is a bug
		// caught by the unit tests.
	} else {
		body.Cleared = cleared
		// Slot must be zero on the unstarred path; omitempty drops it.
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

// writePresetMoveSuccess emits the minimal {"ok":true} success envelope
// for a successful POST /receiver/preset/move (including the from==to
// no-op).
func writePresetMoveSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(presetEditBody{Ok: true})
}

// writePresetEditError emits {"ok":false,"chip":<chip>} with no slot
// or cleared fields. Status drives the HTTP code.
func writePresetEditError(w http.ResponseWriter, status int, chip string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(presetEditBody{Ok: false, Chip: chip})
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
	// Iterate maps in sorted-key order so the first-failure error message
	// is deterministic; otherwise the test that asserts a specific kind
	// in the message races against Go's randomized map iteration.
	for _, kind := range sortedKeys(castKindToTab) {
		tabID := castKindToTab[kind]
		if _, ok := tabs[tabID]; !ok {
			return fmt.Errorf("castKindToTab[%q] = %q: tab not registered", kind, tabID)
		}
	}
	for _, kind := range sortedKeys(valuesKeyForTab) {
		fieldName := valuesKeyForTab[kind]
		tabID, ok := castKindToTab[kind]
		if !ok {
			return fmt.Errorf("valuesKeyForTab[%q] has no castKindToTab entry", kind)
		}
		idx := tabs[tabID]
		if _, ok := idx.fields[fieldName]; !ok {
			return fmt.Errorf("valuesKeyForTab[%q] = %q: field not present on tab %q", kind, fieldName, tabID)
		}
	}
	for _, kind := range sortedKeys(fileFieldForTab) {
		fieldName := fileFieldForTab[kind]
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

// sortedKeys returns the keys of m in lexicographic order. Used to
// stabilize iteration order for first-failure error messages.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
