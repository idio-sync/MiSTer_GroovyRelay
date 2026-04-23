package ui

import (
	"context"
	"fmt"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// ValueProvider is an optional interface an adapter implements to
// expose current field values for UI prefill. Kept off the core
// Adapter interface so adapters without UI support don't need to
// implement it — the UI falls back to empty strings when Current-
// Values is absent.
type ValueProvider interface {
	CurrentValues() map[string]any
}

// ExtraHTMLProvider is an optional interface an adapter implements
// to append adapter-specific markup below the standard form. Used by
// Plex to render the linking section (Phase 6).
type ExtraHTMLProvider interface {
	ExtraPanelHTML() string
}

// adapterPanelData is the template root for the Adapter panel.
// Sections reuses bridgeSection so the two templates can share the
// same row-rendering shape.
type adapterPanelData struct {
	Name         string
	DisplayName  string
	Subtitle     string
	StatusCode   string // "RUN" / "ERR" / "OFF" / "---"
	StatusClass  string // "run" / "err" / "off" / "starting"
	StatusDetail string // "since 14:22:07" / last error / ""
	Sections     []bridgeSection
	Toast        *toastData
	ExtraHTML    string
}

// handleAdapterGET renders the named adapter's panel. Unknown names
// return 404 — the sidebar only links to registered adapters, but a
// hand-typed /ui/adapter/xxx from a bookmarked URL should fail fast.
func (s *Server) handleAdapterGET(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	a, ok := s.cfg.Registry.Get(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	data := s.buildAdapterPanelData(a, nil, nil)
	s.renderPanel(w, "adapter-panel", data)
}

// buildAdapterPanelData assembles the template data: status line,
// section-grouped rows built from Fields() with values sourced via
// the optional ValueProvider, per-field error overlay from errs, and
// optional adapter-specific extra HTML.
func (s *Server) buildAdapterPanelData(a adapters.Adapter, toast *toastData, errs FormErrors) adapterPanelData {
	st := a.Status()

	data := adapterPanelData{
		Name:        a.Name(),
		DisplayName: a.DisplayName(),
		Subtitle:    adapterSubtitle(a.Name()),
		StatusCode:  st.State.String(),
		StatusClass: dotClass(st.State),
		Toast:       toast,
	}
	switch st.State {
	case adapters.StateRunning:
		data.StatusDetail = "since " + st.Since.Format("15:04:05")
	case adapters.StateError:
		data.StatusDetail = st.LastError
	}

	values := map[string]any{}
	if vp, ok := a.(ValueProvider); ok {
		values = vp.CurrentValues()
	}

	// Group fields by Section. Fields without a section fall into a
	// default "Settings" bucket so they render under some heading
	// rather than hanging at the form root.
	byName := map[string]*bridgeSection{}
	order := []string{}
	for _, fd := range a.Fields() {
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
	for _, n := range order {
		data.Sections = append(data.Sections, *byName[n])
	}

	if extra, ok := a.(ExtraHTMLProvider); ok {
		data.ExtraHTML = extra.ExtraPanelHTML()
	}
	return data
}

// adapterRowFor populates a bridgeRow (reused shape) from a FieldDef
// plus the adapter's current values. Secrets render as password
// inputs with a "leave empty to keep existing" placeholder so the
// template never echoes the stored value back to the browser.
func adapterRowFor(fd adapters.FieldDef, vals map[string]any, errs FormErrors) bridgeRow {
	r := bridgeRow{
		Key:         fd.Key,
		Label:       fd.Label,
		Help:        fd.Help,
		Placeholder: fd.Placeholder,
		Required:    fd.Required,
		Enum:        fd.Enum,
		Error:       errs[fd.Key],
	}
	v, have := vals[fd.Key]
	switch fd.Kind {
	case adapters.KindText:
		r.Kind = "text"
		r.InputType = "text"
		if have {
			r.StringValue = fmt.Sprintf("%v", v)
		}
	case adapters.KindInt:
		r.Kind = "int"
		r.InputType = "number"
		if have {
			r.StringValue = fmt.Sprintf("%v", v)
		}
	case adapters.KindBool:
		r.Kind = "bool"
		if have {
			if b, ok := v.(bool); ok {
				r.BoolValue = b
			}
		}
	case adapters.KindEnum:
		r.Kind = "enum"
		if have {
			r.StringValue = fmt.Sprintf("%v", v)
		}
	case adapters.KindSecret:
		r.Kind = "text"
		r.InputType = "password"
		r.Placeholder = "Leave empty to keep existing"
	}
	return r
}

// EnableSetter is the adapter-side mutator for the enabled flag.
// The toggle handler type-asserts for this and calls it in sync with
// Start/Stop so the in-memory enabled bit tracks the runtime state.
// Task 5.4 will extend the toggle path to also persist the new value
// to disk via AdapterSaver so the toggle survives process restart.
type EnableSetter interface {
	SetEnabled(bool)
}

// handleAdapterToggle flips the enabled flag + starts or stops the
// adapter as needed. Re-renders the panel with a success/error toast.
//
// Uses context.Background() for the Start call rather than
// r.Context() — the Start goroutines must outlive the HTTP request
// that triggered the toggle. The registration loop and other
// background workers belong to the adapter's own lifetime, not the
// handler's.
//
// Caveat: Phase 5.3 only mutates in-memory state; a process restart
// reverts the toggle to whatever the config file says. Phase 5.4
// (AdapterSaver) closes this persistence gap.
func (s *Server) handleAdapterToggle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	a, ok := s.cfg.Registry.Get(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	want := parseBoolField(r.Form, "enabled")

	setter, canSet := a.(EnableSetter)
	if !canSet {
		http.Error(w, "adapter does not implement EnableSetter", http.StatusInternalServerError)
		return
	}
	setter.SetEnabled(want)

	var toast *toastData
	if want && a.Status().State != adapters.StateRunning {
		if err := a.Start(context.Background()); err != nil {
			toast = &toastData{Class: "err", Message: fmt.Sprintf("Start failed: %v", err)}
		} else {
			toast = &toastData{Message: "Adapter enabled."}
		}
	} else if !want && a.Status().State == adapters.StateRunning {
		if err := a.Stop(); err != nil {
			toast = &toastData{Class: "err", Message: fmt.Sprintf("Stop failed: %v", err)}
		} else {
			toast = &toastData{Message: "Adapter disabled."}
		}
	}

	data := s.buildAdapterPanelData(a, toast, nil)
	s.renderPanel(w, "adapter-panel", data)
}

// handleAdapterStatus returns just the status-line fragment used by
// the panel header's own hx-poll when the adapter's panel is open.
// The sidebar polls /ui/sidebar/status instead.
func (s *Server) handleAdapterStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	a, ok := s.cfg.Registry.Get(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	st := a.Status()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	detail := ""
	switch st.State {
	case adapters.StateRunning:
		detail = " · since " + st.Since.Format("15:04:05")
	case adapters.StateError:
		detail = " · " + st.LastError
	}
	fmt.Fprintf(w, `<div class="status-line %s">%s%s</div>`,
		dotClass(st.State), st.State.String(), detail)
}

// adapterSubtitle returns a short descriptor shown under the heading.
// Adapter-specific copy lives here so the template stays generic —
// adapters that haven't registered a subtitle render with an empty
// line (better than a per-adapter template indirection in v1).
func adapterSubtitle(name string) string {
	switch name {
	case "plex":
		return "A Plex cast target advertised on your LAN."
	case "jellyfin":
		return "A Jellyfin cast target."
	case "dlna":
		return "A DLNA MediaRenderer endpoint."
	case "url":
		return "Direct-URL casting (paste a URL, play it on the CRT)."
	}
	return ""
}
