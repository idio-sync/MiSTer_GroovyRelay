package ui

import (
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
