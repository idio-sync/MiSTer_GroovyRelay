package ui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// statusPanelData is the template context for /ui/ and /ui/status/content.
type statusPanelData struct {
	State                   string
	Title                   string
	AdapterRef              string
	AdapterDisplayName      string
	AdapterListeningSummary string
	Modeline                string
	FieldOrder              string
	FPS                     string
	ThroughputMBps          string
	Elapsed                 string
	HostPort                string
	Uptime                  string
	Empty                   bool

	Tiles []statusTile

	Events           []statusEvent
	ActivityShown    int
	ActivityTotal    int
	ActivityCapacity int
}

type statusTile struct {
	Num        string
	Name       string
	BadgeClass string
	BadgeGlyph string
	BadgeLabel string
	Rows       []tileRow
}

type tileRow struct{ Key, Value string }

type statusEvent struct {
	Time      string
	Severity  string
	SevClass  string
	Source    string
	Message   string
	Highlight bool
}

func (s *Server) handleStatusHome(w http.ResponseWriter, r *http.Request) {
	panelData := s.buildStatusData()
	if isHTMXRequest(r) {
		// Sidebar nav targets #panel with hx-swap=innerHTML; returning the
		// full shell would nest a second sidebar inside the panel. Emit the
		// panel plus an OOB sidebar refresh so active-link state follows
		// hx-pushed navigation without waiting for a full page reload.
		s.renderPanelWithSidebar(w, r, "status-panel.html", panelData)
		return
	}
	s.renderShellWithPanel(w, r, "status-panel.html", panelData)
}

func (s *Server) handleStatusContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "status-content.html", s.buildStatusData()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) buildStatusData() statusPanelData {
	d := statusPanelData{
		State:            "idle",
		ActivityCapacity: 256,
		Uptime:           formatElapsed(time.Since(s.cfg.StartedAt)),
	}

	if s.cfg.StatusViewer != nil {
		v := s.cfg.StatusViewer.StatusHomeView()
		d.State = string(v.State)
		d.Title = v.Title
		d.AdapterRef = v.AdapterRef
		d.Modeline = v.Modeline
		d.AdapterDisplayName = displayNameForSource(s.cfg.Registry, v.Source, v.AdapterRef)
		switch v.State {
		case core.StatePlaying:
			d.FPS = formatFPS(v.BlitsTotal, time.Since(v.StartedAt))
			d.ThroughputMBps = formatMBps(v.WireBytes, time.Since(v.StartedAt))
			d.Elapsed = formatElapsed(time.Since(v.StartedAt))
		case core.StatePaused:
			d.FPS = "0"
			d.ThroughputMBps = "0.0"
			d.Elapsed = formatElapsed(v.Position)
		}
	} else {
		d.AdapterListeningSummary = "no live data"
	}

	if s.cfg.BridgeSaver != nil {
		bc := s.cfg.BridgeSaver.Current()
		d.HostPort = fmt.Sprintf("%s:%d", bc.MiSTer.Host, bc.MiSTer.Port)
		d.FieldOrder = bc.Video.InterlaceFieldOrder
	}

	d.Tiles = buildStatusTiles(s.cfg, d.State)

	enabledCount := 0
	if s.cfg.Registry != nil {
		for _, a := range s.cfg.Registry.List() {
			if a.IsEnabled() {
				enabledCount++
			}
		}
	}
	d.Empty = (d.State == "idle") && enabledCount == 0

	if s.cfg.EventLog != nil {
		all := s.cfg.EventLog.Snapshot()
		d.ActivityTotal = len(all)
		const showN = 10
		start := 0
		if len(all) > showN {
			start = len(all) - showN
		}
		recent := all[start:]
		d.Events = make([]statusEvent, 0, len(recent))
		for i := len(recent) - 1; i >= 0; i-- {
			e := recent[i]
			d.Events = append(d.Events, statusEvent{
				Time:      e.Time.Format("15:04:05"),
				Severity:  e.Severity.String(),
				SevClass:  e.Severity.String(),
				Source:    e.Source,
				Message:   e.Message,
				Highlight: strings.HasPrefix(e.Message, "cast-started") && i == len(recent)-1,
			})
		}
		d.ActivityShown = len(d.Events)
	}

	return d
}

func buildStatusTiles(cfg Config, _ string) []statusTile {
	tiles := make([]statusTile, 0, 4)

	bridgeTile := statusTile{
		Num: "01", Name: "Bridge",
		BadgeClass: "run", BadgeGlyph: "●", BadgeLabel: "online",
	}
	if cfg.BridgeSaver != nil {
		bc := cfg.BridgeSaver.Current()
		bridgeTile.Rows = []tileRow{
			{"host", bc.MiSTer.Host},
			{"src port", fmt.Sprintf("%d", bc.MiSTer.Port)},
			{"fields", bc.Video.InterlaceFieldOrder},
		}
	}
	tiles = append(tiles, bridgeTile)

	if cfg.Registry != nil {
		for i, a := range cfg.Registry.List() {
			st := a.Status()
			tile := statusTile{
				Num:        fmt.Sprintf("%02d", i+2),
				Name:       a.DisplayName(),
				BadgeClass: dotClass(st.State),
				BadgeGlyph: statusBadgeGlyph(st.State),
				BadgeLabel: statusBadgeLabel(st.State),
			}
			tiles = append(tiles, tile)
		}
	}
	return tiles
}

func statusBadgeGlyph(s adapters.State) string {
	switch s {
	case adapters.StateRunning:
		return "●"
	case adapters.StateStarting:
		return "◐"
	case adapters.StateError:
		return "✕"
	default:
		return "○"
	}
}

func statusBadgeLabel(s adapters.State) string {
	switch s {
	case adapters.StateRunning:
		return "listening"
	case adapters.StateStarting:
		return "starting"
	case adapters.StateError:
		return "error"
	default:
		return "off"
	}
}

// displayNameForSource resolves the human-readable display name for the
// active session. Prefers the explicit Source field on SessionRequest
// (the contract added in PR2 follow-up #10); falls back to the
// AdapterRef's leading "name/" segment for sessions started before
// adapters were taught to populate Source.
func displayNameForSource(reg *adapters.Registry, source, ref string) string {
	if reg == nil {
		return source
	}
	if source != "" {
		if a, ok := reg.Get(source); ok {
			return a.DisplayName()
		}
		return source
	}
	if ref == "" {
		return ""
	}
	name, _, _ := strings.Cut(ref, "/")
	if a, ok := reg.Get(name); ok {
		return a.DisplayName()
	}
	return name
}

func formatFPS(blits uint64, elapsed time.Duration) string {
	if elapsed.Seconds() < 0.5 {
		return "—"
	}
	return fmt.Sprintf("%.2f", float64(blits)/elapsed.Seconds())
}

func formatMBps(bytes uint64, elapsed time.Duration) string {
	if elapsed.Seconds() < 0.5 {
		return "—"
	}
	return fmt.Sprintf("%.1f", float64(bytes)/elapsed.Seconds()/1_000_000)
}

func formatElapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}
