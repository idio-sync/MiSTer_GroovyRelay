package ui

import (
	"fmt"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// bridgePanelData is the template root for the Bridge panel render.
// Toast is optional (nil = no toast); Sections is always populated
// in the order Network → Video → Audio → Server.
type bridgePanelData struct {
	Toast    *toastData
	Sections []bridgeSection
}

type bridgeSection struct {
	Name string
	Rows []bridgeRow
}

// bridgeRow is the flattened representation of one input control.
// The template renders against Kind (string) so the comparison is
// obvious; all value-shaping and type-conversion happens in Go
// (bridgeLookupString / bridgeLookupInt / bridgeLookupBool).
type bridgeRow struct {
	Key         string
	Label       string
	Help        string
	Kind        string // "text" | "int" | "bool" | "enum"
	InputType   string // for KindText/KindInt: "text" or "number"
	Enum        []string
	Placeholder string
	Required    bool
	StringValue string // for text/int/enum
	BoolValue   bool   // for bool
	Error       string // per-field error, empty when OK
}

// toastData is rendered by templates/toast.html. Class is "" for
// green/OK and "err" for red; Command is optional and shown inside
// a <pre> when a restart-bridge save wants to surface the exact
// docker command.
type toastData struct {
	Class   string
	Message string
	Command string
}

// handleBridgeGET renders the bridge panel with current values.
func (s *Server) handleBridgeGET(w http.ResponseWriter, r *http.Request) {
	if s.cfg.BridgeSaver == nil {
		http.Error(w, "bridge saver not wired", http.StatusInternalServerError)
		return
	}
	cur := s.cfg.BridgeSaver.Current()
	data := bridgePanelData{Sections: buildBridgeSections(cur, nil)}
	s.renderPanel(w, "bridge-panel", data)
}

// handleBridgePOST validates the form, persists via BridgeSaver, and
// re-renders. Parse errors → inline per-field rendering. Semantic
// validation errors (Sectioned.Validate) → form-wide error toast.
// Save errors → error toast with the underlying message. Success →
// scope-appropriate green toast and prefilled fields from the saver's
// updated Current().
func (s *Server) handleBridgePOST(w http.ResponseWriter, r *http.Request) {
	if s.cfg.BridgeSaver == nil {
		http.Error(w, "bridge saver not wired", http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	candidate, parseErr := parseBridgeForm(r.Form)
	if parseErr != nil {
		if fe, ok := parseErr.(FormErrors); ok {
			data := bridgePanelData{Sections: buildBridgeSections(candidate, fe)}
			s.renderPanel(w, "bridge-panel", data)
			return
		}
	}

	// Validate via Sectioned.Validate (covers ports, enum membership,
	// required-mister-host, etc.). Keeps the save path using the same
	// rules as boot-time validation.
	sec := &config.Sectioned{Bridge: candidate}
	if err := sec.Validate(); err != nil {
		data := bridgePanelData{
			Toast:    &toastData{Class: "err", Message: err.Error()},
			Sections: buildBridgeSections(candidate, nil),
		}
		s.renderPanel(w, "bridge-panel", data)
		return
	}

	scope, err := s.cfg.BridgeSaver.Save(candidate)
	if err != nil {
		data := bridgePanelData{
			Toast:    &toastData{Class: "err", Message: fmt.Sprintf("Save failed: %v", err)},
			Sections: buildBridgeSections(candidate, nil),
		}
		s.renderPanel(w, "bridge-panel", data)
		return
	}

	// Success — re-render with updated values + scope-appropriate toast.
	data := bridgePanelData{
		Toast:    scopeToast(scope),
		Sections: buildBridgeSections(s.cfg.BridgeSaver.Current(), nil),
	}
	s.renderPanel(w, "bridge-panel", data)
}

// scopeToast maps an ApplyScope onto the operator-facing toast copy.
// restart-bridge includes the docker command so the operator can
// copy it straight to a terminal without looking it up.
func scopeToast(scope adapters.ApplyScope) *toastData {
	switch scope {
	case adapters.ScopeHotSwap:
		return &toastData{Message: "Saved — applied live."}
	case adapters.ScopeRestartCast:
		return &toastData{Message: "Saved — cast restarted."}
	case adapters.ScopeRestartBridge:
		return &toastData{
			Message: "Saved. Restart the container to apply.",
			Command: "docker restart mister-groovy-relay",
		}
	}
	return &toastData{Message: "Saved."}
}

// renderPanel renders a template into a panel-fragment response.
// Content-Type is text/html so htmx swaps it verbatim into #panel.
func (s *Server) renderPanel(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildBridgeSections groups bridgeFields() by Section in render
// order, populating each row's current value from cur and overlaying
// per-field parse errors from errs.
func buildBridgeSections(cur config.BridgeConfig, errs FormErrors) []bridgeSection {
	byName := map[string]*bridgeSection{}
	order := []string{}
	for _, fd := range bridgeFields() {
		sec, ok := byName[fd.Section]
		if !ok {
			sec = &bridgeSection{Name: fd.Section}
			byName[fd.Section] = sec
			order = append(order, fd.Section)
		}
		sec.Rows = append(sec.Rows, rowFor(fd, cur, errs))
	}
	out := make([]bridgeSection, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out
}

// rowFor populates a bridgeRow from a FieldDef + the live BridgeConfig.
// Int and enum values serialize via strconv/fmt on the Go side so the
// template renders a single StringValue regardless of the underlying
// type.
func rowFor(fd adapters.FieldDef, cur config.BridgeConfig, errs FormErrors) bridgeRow {
	r := bridgeRow{
		Key:         fd.Key,
		Label:       fd.Label,
		Help:        fd.Help,
		Placeholder: fd.Placeholder,
		Required:    fd.Required,
		Enum:        fd.Enum,
		Error:       errs[fd.Key],
	}
	switch fd.Kind {
	case adapters.KindText:
		r.Kind = "text"
		r.InputType = "text"
		r.StringValue = bridgeLookupString(fd.Key, cur)
	case adapters.KindInt:
		r.Kind = "int"
		r.InputType = "number"
		r.StringValue = fmt.Sprintf("%d", bridgeLookupInt(fd.Key, cur))
	case adapters.KindBool:
		r.Kind = "bool"
		r.BoolValue = bridgeLookupBool(fd.Key, cur)
	case adapters.KindEnum:
		r.Kind = "enum"
		// int-valued enums (sample_rate, channels) still serialize
		// as strings on the wire — select/option values must match
		// the TOML-form strings.
		r.StringValue = bridgeLookupString(fd.Key, cur)
	}
	return r
}

// bridgeLookupString returns the current string value for a dotted
// key. Int-valued enums (audio.sample_rate, audio.channels) serialize
// via strconv so the <select> option comparison works against string
// values consistently.
func bridgeLookupString(key string, cur config.BridgeConfig) string {
	switch key {
	case "mister.host":
		return cur.MiSTer.Host
	case "host_ip":
		return cur.HostIP
	case "video.modeline":
		return cur.Video.Modeline
	case "video.interlace_field_order":
		return cur.Video.InterlaceFieldOrder
	case "video.aspect_mode":
		return cur.Video.AspectMode
	case "audio.sample_rate":
		return fmt.Sprintf("%d", cur.Audio.SampleRate)
	case "audio.channels":
		return fmt.Sprintf("%d", cur.Audio.Channels)
	case "data_dir":
		return cur.DataDir
	}
	return ""
}

// bridgeLookupInt returns the current int value for a dotted key.
// Only KindInt fields come through here; enum ints route through
// bridgeLookupString.
func bridgeLookupInt(key string, cur config.BridgeConfig) int {
	switch key {
	case "mister.port":
		return cur.MiSTer.Port
	case "mister.source_port":
		return cur.MiSTer.SourcePort
	case "ui.http_port":
		return cur.UI.HTTPPort
	}
	return 0
}

// bridgeLookupBool returns the current bool value for a dotted key.
func bridgeLookupBool(key string, cur config.BridgeConfig) bool {
	switch key {
	case "video.lz4_enabled":
		return cur.Video.LZ4Enabled
	}
	return false
}
