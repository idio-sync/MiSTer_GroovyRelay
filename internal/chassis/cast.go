package chassis

import (
	"encoding/json"
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
