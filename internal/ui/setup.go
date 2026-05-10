package ui

import (
	"context"
	"fmt"
	"html/template"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// stepperItem is one entry in the wizard progress bar rendered in shell.html
// when SetupMode=true.
type stepperItem struct {
	Number int
	Label  string
	Done   bool
	Active bool
	Final  bool // last step — uses ▸ instead of a number
}

// setupStepData is the template root for all wizard step templates.
type setupStepData struct {
	// Bridge step
	Bridge       config.BridgeConfig
	BridgeErrors FormErrors

	// Adapters picker step
	AdapterCards []setupAdapterCard

	// Per-adapter configure step
	AdapterName        string
	AdapterDisplayName string
	AdapterSections    []bridgeSection
	AdapterErrors      FormErrors
	ExtraHTML          template.HTML
	GateForm           bool // true when LinkAware && !IsLinked(); setup keeps config fields enabled before linking

	// Done step
	EnabledAdapters []string
}

// setupAdapterCard is one card in the adapters multiselect step.
type setupAdapterCard struct {
	Name        string
	DisplayName string
	Description string
	Picked      bool
}

// handleSetupRoot redirects to the first incomplete wizard step (or done).
// Re-entry: if ?step=<name> is in the URL, honour it directly.
func (s *Server) handleSetupRoot(w http.ResponseWriter, r *http.Request) {
	if step := r.URL.Query().Get("step"); step != "" {
		http.Redirect(w, r, "/ui/setup/step/"+step, http.StatusFound)
		return
	}
	target := s.firstIncompleteStep()
	http.Redirect(w, r, "/ui/setup/step/"+target, http.StatusFound)
}

// firstIncompleteStep walks bridge → adapters → done and returns the name
// of the first step that still needs attention.
//
//   - bridge: host is empty → must configure
//   - adapters: no adapters are enabled → must pick
//   - done: everything looks good
//
// Known limitation: if the operator picks multiple adapters in step 2 and
// configures only the first, this returns "done" as soon as that one
// adapter is enabled — the unconfigured-but-picked adapters are invisible
// to the walk because the picker step deliberately does NOT write
// enabled=true to disk before configure (see round-3 review fix C3).
// Multi-adapter setup currently requires re-entering /ui/setup?step=adapters
// after each configure; tracked as a follow-up.
func (s *Server) firstIncompleteStep() string {
	if s.cfg.BridgeSaver != nil {
		cur := s.cfg.BridgeSaver.Current()
		if cur.MiSTer.Host == "" {
			return "bridge"
		}
	}
	// Check if any adapters are enabled.
	anyEnabled := false
	for _, a := range s.cfg.Registry.List() {
		if a.IsEnabled() {
			anyEnabled = true
			break
		}
	}
	if !anyEnabled {
		return "adapters"
	}
	return "done"
}

// handleSetupStepGET renders the named wizard step inside the setup shell.
func (s *Server) handleSetupStepGET(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.renderSetupStep(w, r, name, setupStepData{})
}

// handleSetupStepPOST handles form submission for each wizard step.
func (s *Server) handleSetupStepPOST(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch name {
	case "bridge":
		s.handleSetupBridgePOST(w, r)
	case "adapters":
		s.handleSetupAdaptersPOST(w, r)
	default:
		// Per-adapter configure step: name is the adapter name.
		s.handleSetupAdapterConfigPOST(w, r, name)
	}
}

// handleSetupBridgePOST saves bridge basics and redirects to next step.
func (s *Server) handleSetupBridgePOST(w http.ResponseWriter, r *http.Request) {
	if s.cfg.BridgeSaver == nil {
		http.Error(w, "bridge saver not wired", http.StatusInternalServerError)
		return
	}

	candidate, parseErr := applyBridgeFromSetupForm(r.Form, s.cfg.BridgeSaver.Current())
	if parseErr != nil {
		data := setupStepData{Bridge: candidate}
		if fe, ok := parseErr.(FormErrors); ok {
			data.BridgeErrors = fe
		}
		s.renderSetupStep(w, r, "bridge", data)
		return
	}

	// Preserve unset fields from current config so we don't wipe non-wizard fields.
	cur := s.cfg.BridgeSaver.Current()
	if candidate.MiSTer.Port == 0 {
		candidate.MiSTer.Port = cur.MiSTer.Port
	}
	if candidate.MiSTer.SourcePort == 0 {
		candidate.MiSTer.SourcePort = cur.MiSTer.SourcePort
	}
	if candidate.Video.Modeline == "" {
		candidate.Video.Modeline = cur.Video.Modeline
	}
	if candidate.Video.InterlaceFieldOrder == "" {
		candidate.Video.InterlaceFieldOrder = cur.Video.InterlaceFieldOrder
	}
	if candidate.Video.AspectMode == "" {
		candidate.Video.AspectMode = cur.Video.AspectMode
	}
	if candidate.Video.RGBMode == "" {
		candidate.Video.RGBMode = cur.Video.RGBMode
	}
	if candidate.Audio.SampleRate == 0 {
		candidate.Audio.SampleRate = cur.Audio.SampleRate
	}
	if candidate.Audio.Channels == 0 {
		candidate.Audio.Channels = cur.Audio.Channels
	}
	if candidate.UI.HTTPPort == 0 {
		candidate.UI.HTTPPort = cur.UI.HTTPPort
	}
	if candidate.DataDir == "" {
		candidate.DataDir = cur.DataDir
	}

	if _, err := s.cfg.BridgeSaver.Save(candidate); err != nil {
		data := setupStepData{
			Bridge:       candidate,
			BridgeErrors: FormErrors{"": fmt.Sprintf("Save failed: %v", err)},
		}
		s.renderSetupStep(w, r, "bridge", data)
		return
	}

	// Bridge saved — go to adapters step.
	http.Redirect(w, r, "/ui/setup/step/adapters", http.StatusFound)
}

// handleSetupAdaptersPOST validates the adapter selection and redirects
// to the first selected adapter's configure step. Does NOT write enabled=true
// to disk — that happens in the per-adapter configure step Save.
func (s *Server) handleSetupAdaptersPOST(w http.ResponseWriter, r *http.Request) {
	picked := r.Form["adapters"]

	// Validate: every picked name must exist in registry.
	valid := make([]string, 0, len(picked))
	for _, name := range picked {
		if _, ok := s.cfg.Registry.Get(name); ok {
			valid = append(valid, name)
		}
	}

	// Skip: if nothing was picked, redirect to done.
	if len(valid) == 0 {
		http.Redirect(w, r, "/ui/setup/step/done", http.StatusFound)
		return
	}

	// Redirect to the first picked adapter's configure step.
	http.Redirect(w, r, "/ui/setup/step/"+valid[0], http.StatusFound)
}

// handleSetupAdapterConfigPOST saves an adapter's config (with enabled=true)
// and redirects back to /ui/setup (which routes to next incomplete step).
func (s *Server) handleSetupAdapterConfigPOST(w http.ResponseWriter, r *http.Request, adapterName string) {
	a, ok := s.cfg.Registry.Get(adapterName)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if s.cfg.AdapterSaver == nil {
		http.Error(w, "adapter saver not wired", http.StatusInternalServerError)
		return
	}

	lock := adapterLocks.forName(adapterName)
	lock.Lock()
	defer lock.Unlock()

	oldEnabled := a.IsEnabled()

	// Build TOML snippet from form, injecting enabled=true.
	tomlBytes, ferrs := formToAdapterTOML(r.Form, setupAdapterConfigFields(a.Fields()))
	if len(ferrs) > 0 {
		data := buildSetupAdapterConfigData(a, ferrs)
		s.renderSetupStep(w, r, adapterName, data)
		return
	}

	// Inject enabled = true so the adapter comes online after wizard.
	tomlBytes = append([]byte("enabled = true\n"), tomlBytes...)

	raw, meta, decodeErr := decodeAdapterSection(tomlBytes, adapterName)
	if decodeErr != nil {
		data := buildSetupAdapterConfigData(a, FormErrors{"": fmt.Sprintf("Internal decode error: %v", decodeErr)})
		s.renderSetupStep(w, r, adapterName, data)
		return
	}

	if v, ok := a.(adapters.Validator); ok {
		if err := v.Validate(raw, meta); err != nil {
			data := buildSetupAdapterConfigData(a, FormErrors{"": err.Error()})
			s.renderSetupStep(w, r, adapterName, data)
			return
		}
	}

	if err := s.cfg.AdapterSaver.Save(adapterName, tomlBytes); err != nil {
		data := buildSetupAdapterConfigData(a, FormErrors{"": fmt.Sprintf("Save failed: %v", err)})
		s.renderSetupStep(w, r, adapterName, data)
		return
	}

	// Apply config (enables the adapter in-memory). If apply fails, the
	// disk write has already happened, but the operator needs to see the
	// failure instead of being advanced through first-run with a stopped
	// source.
	if _, err := a.ApplyConfig(raw, meta); err != nil {
		data := buildSetupAdapterConfigData(a, FormErrors{"": fmt.Sprintf("Saved to disk but apply failed: %v", err)})
		s.renderSetupStep(w, r, adapterName, data)
		return
	}

	if setter, ok := a.(EnableSetter); ok {
		setter.SetEnabled(true)
	}

	if (!oldEnabled || a.IsEnabled()) && a.Status().State != adapters.StateRunning {
		if err := a.Start(context.Background()); err != nil {
			data := buildSetupAdapterConfigData(a, FormErrors{"": fmt.Sprintf("Saved but start failed: %v", err)})
			s.renderSetupStep(w, r, adapterName, data)
			return
		}
	}

	// Redirect back to /ui/setup — it will route to the next incomplete step.
	http.Redirect(w, r, "/ui/setup", http.StatusFound)
}

// handleSetupDone dismisses the first-run flag and redirects to the main UI.
func (s *Server) handleSetupDone(w http.ResponseWriter, r *http.Request) {
	if target := s.firstIncompleteStep(); target != "done" {
		http.Redirect(w, r, "/ui/setup/step/"+target, http.StatusFound)
		return
	}
	if fra, ok := s.cfg.BridgeSaver.(FirstRunAware); ok {
		_ = fra.DismissFirstRun()
	}
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

// renderSetupStep builds the setup shell and renders the named step template.
func (s *Server) renderSetupStep(w http.ResponseWriter, r *http.Request, stepName string, data setupStepData) {
	// Populate step-specific data if not already set.
	switch stepName {
	case "bridge":
		if s.cfg.BridgeSaver != nil && data.Bridge.MiSTer.Host == "" && len(data.BridgeErrors) == 0 {
			data.Bridge = s.cfg.BridgeSaver.Current()
		}
	case "adapters":
		if len(data.AdapterCards) == 0 {
			data = buildSetupAdaptersData(s.cfg.Registry)
		}
	case "done":
		data = buildSetupDoneData(s.cfg.Registry)
	default:
		// Per-adapter configure step.
		if a, ok := s.cfg.Registry.Get(stepName); ok {
			if len(data.AdapterSections) == 0 && len(data.AdapterErrors) == 0 {
				data = buildSetupAdapterConfigData(a, nil)
			}
		} else {
			http.NotFound(w, r)
			return
		}
	}

	panelTemplateName := setupTemplateName(stepName)
	panelHTML, err := s.renderTemplateHTML(panelTemplateName, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	shell := s.shellData()
	shell.SetupMode = true
	shell.Steps = stepperFor(stepName, s.cfg.Registry)
	shell.PanelHTML = panelHTML

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "shell.html", shell); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// setupTemplateName maps a step name to its template name.
func setupTemplateName(step string) string {
	switch step {
	case "bridge":
		return "setup-step-bridge"
	case "adapters":
		return "setup-step-adapters"
	case "done":
		return "setup-step-done"
	default:
		return "setup-step-adapter-config"
	}
}

// stepperFor builds the ordered stepper items for the wizard progress bar.
// The steps are: Bridge → Adapters → per-adapter configure steps → Done.
func stepperFor(activeStep string, reg *adapters.Registry) []stepperItem {
	type namedStep struct {
		name  string
		label string
	}

	steps := []namedStep{
		{"bridge", "Bridge basics"},
		{"adapters", "Pick sources"},
	}

	// Add enabled adapters as individual configure steps. While actively
	// configuring an adapter, include it even before enabled=true has been
	// saved so the stepper can highlight the current step.
	for _, a := range reg.List() {
		if a.IsEnabled() || a.Name() == activeStep {
			steps = append(steps, namedStep{a.Name(), a.DisplayName()})
		}
	}
	steps = append(steps, namedStep{"done", "Done"})

	// Determine which steps are "done" (come before active).
	activeIdx := -1
	for i, st := range steps {
		if st.name == activeStep {
			activeIdx = i
			break
		}
	}

	items := make([]stepperItem, 0, len(steps))
	for i, st := range steps {
		item := stepperItem{
			Number: i + 1,
			Label:  st.label,
			Done:   activeIdx >= 0 && i < activeIdx,
			Active: st.name == activeStep,
			Final:  i == len(steps)-1,
		}
		items = append(items, item)
	}
	return items
}

// applyBridgeFromSetupForm parses only the bridge wizard fields (host, port,
// source_port) from the setup form, overlaying them onto the current config
// so non-wizard fields are preserved.
func applyBridgeFromSetupForm(form interface{ Get(string) string }, cur config.BridgeConfig) (config.BridgeConfig, error) {
	errs := FormErrors{}
	out := cur // start from current so non-wizard fields are preserved

	out.MiSTer.Host = form.Get("mister.host")
	if out.MiSTer.Host == "" {
		errs["mister.host"] = "required"
	}

	if raw := form.Get("mister.port"); raw != "" {
		n := 0
		if _, err := fmt.Sscanf(raw, "%d", &n); err != nil || n <= 0 || n > 65535 {
			errs["mister.port"] = fmt.Sprintf("not a valid port: %q", raw)
		} else {
			out.MiSTer.Port = n
		}
	}

	if raw := form.Get("mister.source_port"); raw != "" {
		n := 0
		if _, err := fmt.Sscanf(raw, "%d", &n); err != nil || n <= 0 || n > 65535 {
			errs["mister.source_port"] = fmt.Sprintf("not a valid port: %q", raw)
		} else {
			out.MiSTer.SourcePort = n
		}
	}

	if len(errs) > 0 {
		return out, errs
	}
	return out, nil
}

// buildSetupAdaptersData builds the adapter cards for the picker step.
func buildSetupAdaptersData(reg *adapters.Registry) setupStepData {
	cards := make([]setupAdapterCard, 0)
	for _, a := range reg.List() {
		cards = append(cards, setupAdapterCard{
			Name:        a.Name(),
			DisplayName: a.DisplayName(),
			Description: adapterDescription(a.Name()),
			Picked:      a.IsEnabled(),
		})
	}
	return setupStepData{AdapterCards: cards}
}

// buildSetupAdapterConfigData builds the per-adapter configure step data.
func buildSetupAdapterConfigData(a adapters.Adapter, errs FormErrors) setupStepData {
	values := map[string]any{}
	if vp, ok := a.(ValueProvider); ok {
		values = vp.CurrentValues()
	}

	byName := map[string]*bridgeSection{}
	order := []string{}
	for _, fd := range setupAdapterConfigFields(a.Fields()) {
		section := fd.Section
		if section == "" {
			section = "Settings"
		}
		sec, ok := byName[section]
		if !ok {
			sec = &bridgeSection{Name: section}
			byName[section] = sec
			order = append(order, section)
		}
		sec.Rows = append(sec.Rows, adapterRowFor(fd, values, errs))
	}
	sections := make([]bridgeSection, 0, len(order))
	for _, n := range order {
		sections = append(sections, *byName[n])
	}

	var extraHTML template.HTML
	if ep, ok := a.(ExtraHTMLProvider); ok {
		extraHTML = ep.ExtraPanelHTML()
	}

	// GateForm: true if the adapter implements LinkAware and is not yet linked.
	// The setup template intentionally does not disable fields from this bit:
	// Jellyfin and similar adapters need their server URL saved before linking.
	gateForm := false
	if la, ok := a.(adapters.LinkAware); ok {
		gateForm = !la.IsLinked()
	}

	return setupStepData{
		AdapterName:        a.Name(),
		AdapterDisplayName: a.DisplayName(),
		AdapterSections:    sections,
		AdapterErrors:      errs,
		ExtraHTML:          extraHTML,
		GateForm:           gateForm,
	}
}

func setupAdapterConfigFields(fields []adapters.FieldDef) []adapters.FieldDef {
	out := make([]adapters.FieldDef, 0, len(fields))
	for _, fd := range fields {
		if fd.Kind == adapters.KindAction || fd.Key == "enabled" {
			continue
		}
		out = append(out, fd)
	}
	return out
}

// buildSetupDoneData builds the done step data.
func buildSetupDoneData(reg *adapters.Registry) setupStepData {
	var names []string
	for _, a := range reg.List() {
		if a.IsEnabled() {
			names = append(names, a.DisplayName())
		}
	}
	return setupStepData{EnabledAdapters: names}
}

// adapterDescription returns a short human-readable description of an adapter.
func adapterDescription(name string) string {
	switch name {
	case "plex":
		return "Cast from Plex Media Server to your CRT via MiSTer."
	case "jellyfin":
		return "Cast from Jellyfin to your CRT via MiSTer."
	case "dlna":
		return "Use any DLNA-compatible controller."
	case "url":
		return "Paste a direct URL and play it on the CRT."
	}
	return ""
}
