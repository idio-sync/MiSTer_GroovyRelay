package ui

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// bridgePanelData is the template root for the Bridge panel render.
// Toast is optional (nil = no toast); Sections is always populated
// in the order Network → Video → Audio → Server. FirstRun drives
// the quick-start banner when the bridge hasn't been configured yet.
type bridgePanelData struct {
	Toast    *toastData
	Sections []bridgeSection
	FirstRun bool
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
// docker command. NewURL is populated on http_port changes so the
// operator knows where to reconnect after restart.
type toastData struct {
	Class   string
	Message string
	Command string
	NewURL  string
}

// handleBridgeGET renders the bridge panel with current values.
func (s *Server) handleBridgeGET(w http.ResponseWriter, r *http.Request) {
	if s.cfg.BridgeSaver == nil {
		http.Error(w, "bridge saver not wired", http.StatusInternalServerError)
		return
	}
	cur := s.cfg.BridgeSaver.Current()
	data := bridgePanelData{Sections: buildBridgeSections(cur, nil)}
	if fra, ok := s.cfg.BridgeSaver.(FirstRunAware); ok {
		data.FirstRun = fra.IsFirstRun()
	}
	if isHTMXRequest(r) {
		s.renderPanel(w, "bridge-panel", data)
		return
	}
	s.renderShellWithPanel(w, "bridge-panel", data)
}

// handleBridgeDismissFirstRun persists the first-run dismissal and
// re-renders the panel. 501 if the saver doesn't implement
// FirstRunAware — shouldn't happen in production (main.go wires it)
// but keeps tests that use a bare BridgeSaver stable.
func (s *Server) handleBridgeDismissFirstRun(w http.ResponseWriter, r *http.Request) {
	fra, ok := s.cfg.BridgeSaver.(FirstRunAware)
	if !ok {
		http.Error(w, "first-run not supported", http.StatusNotImplemented)
		return
	}
	if err := fra.DismissFirstRun(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleBridgeGET(w, r)
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

	// Preserve the stored ssh_password when the operator submits with
	// the field empty. Mirrors the "Leave empty to keep existing"
	// placeholder shown in rowFor's KindSecret case. Without this, any
	// save touching an unrelated field would silently clear the
	// password every time.
	if candidate.MiSTer.SSHPassword == "" {
		candidate.MiSTer.SSHPassword = s.cfg.BridgeSaver.Current().MiSTer.SSHPassword
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

	old := s.cfg.BridgeSaver.Current()
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
	toast := scopeToast(scope)
	if scope == adapters.ScopeRestartBridge && candidate.UI.HTTPPort != old.UI.HTTPPort {
		// Spell out the reconnect URL so the operator doesn't have to
		// guess which port to hit after the container restart.
		host := r.Host
		if idx := strings.Index(host, ":"); idx >= 0 {
			host = host[:idx]
		}
		toast.NewURL = fmt.Sprintf("http://%s:%d/", host, candidate.UI.HTTPPort)
	}
	data := bridgePanelData{
		Toast:    toast,
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
//
// Render order: ascending by SectionOrder (lowest first), with ties
// broken by the order each section's first field appears in the
// bridgeFields() slice. Sections whose fields all have SectionOrder=0
// retain "first-field-wins" registration order — back-compatible
// with the pre-§8.1 contract.
func buildBridgeSections(cur config.BridgeConfig, errs FormErrors) []bridgeSection {
	type secMeta struct {
		section *bridgeSection
		order   int
		regIdx  int
	}
	byName := map[string]*secMeta{}
	regCounter := 0

	for _, fd := range bridgeFields() {
		meta, ok := byName[fd.Section]
		if !ok {
			meta = &secMeta{
				section: &bridgeSection{Name: fd.Section},
				order:   fd.SectionOrder,
				regIdx:  regCounter,
			}
			byName[fd.Section] = meta
			regCounter++
		} else if fd.SectionOrder != 0 && (meta.order == 0 || fd.SectionOrder < meta.order) {
			// Lowest non-zero SectionOrder wins.
			meta.order = fd.SectionOrder
		}
		meta.section.Rows = append(meta.section.Rows, rowFor(fd, cur, errs))
	}

	all := make([]*secMeta, 0, len(byName))
	for _, m := range byName {
		all = append(all, m)
	}
	// Stable sort: order ascending, then registration index ascending.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].order != all[j].order {
			return all[i].order < all[j].order
		}
		return all[i].regIdx < all[j].regIdx
	})

	out := make([]bridgeSection, 0, len(all))
	for _, m := range all {
		out = append(out, *m.section)
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
	case adapters.KindSecret:
		// Mirrors internal/ui/adapter.go:167–171.
		r.Kind = "text"
		r.InputType = "password"
		r.Placeholder = "Leave empty to keep existing"
		// StringValue stays empty: never echo a stored password into HTML.
		// The preserve-on-empty conditional in handleBridgePOST recovers
		// the prior value when the operator submits without retyping.
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
	case "mister.ssh_user":
		return cur.MiSTer.SSHUser
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

// launchResultData is the template root for the launch-result
// fragment. Class = "run" for green / "err" for red; Message holds
// the operator-facing copy (success: "Sent — <command> delivered to
// <host>", error: "SSH failed: <error>").
//
// Spec note: the design doc shows this as `{Success bool, Message string}`
// with branch logic in the template. This implementation moves the
// branch into Go (cleaner separation, identical rendered HTML — both
// produce <div class="status-line run|err">). Functionally equivalent.
type launchResultData struct {
	Class   string
	Message string
}

// handleBridgeMisterLaunch invokes MisterLauncher.Launch (with a
// 6-second context budget — 1s slack on top of the 5s SSH dial
// timeout) and renders the result fragment swapped into the launch
// section's slot. The handler is the only place SSH errors surface
// to the operator; spec §"Failure modes" enumerates the cases.
//
// CSRF wrapping is automatic via mountPOST (server.go::Mount).
func (s *Server) handleBridgeMisterLaunch(w http.ResponseWriter, r *http.Request) {
	if s.cfg.MisterLauncher == nil {
		http.Error(w, "launcher not wired", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	err := s.cfg.MisterLauncher.Launch(ctx)
	data := launchResultData{}
	if err != nil {
		data.Class = "err"
		data.Message = fmt.Sprintf("SSH failed: %v", err)
	} else {
		host := s.cfg.BridgeSaver.Current().MiSTer.Host
		data.Class = "run"
		data.Message = fmt.Sprintf("Sent — load_core /media/fat/_Utility/Groovy_20240928.rbf delivered to %s", host)
	}
	s.renderPanel(w, "mister-launch-result", data)
}
