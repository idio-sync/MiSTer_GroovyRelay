package ui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
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
	RefreshRate             string
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

	view := core.StatusHomeView{}
	if s.cfg.StatusViewer != nil {
		view = s.cfg.StatusViewer.StatusHomeView()
		d.State = string(view.State)
		d.Title = view.Title
		d.AdapterRef = view.AdapterRef
		d.Modeline = view.Modeline
		d.RefreshRate = formatRefreshRate(view.Modeline)
		d.AdapterDisplayName = displayNameForSource(s.cfg.Registry, view.Source, view.AdapterRef)
		switch view.State {
		case core.StatePlaying:
			d.FPS = formatFPS(view.BlitsTotal, time.Since(view.StartedAt))
			d.ThroughputMBps = formatMBps(view.WireBytes, time.Since(view.StartedAt))
			d.Elapsed = formatElapsed(time.Since(view.StartedAt))
		case core.StatePaused:
			d.FPS = "0"
			d.ThroughputMBps = "0.0"
			d.Elapsed = formatElapsed(view.Position)
		}
	} else {
		d.AdapterListeningSummary = "no live data"
	}
	if summary := adapterListeningSummary(s.cfg.Registry); summary != "" {
		d.AdapterListeningSummary = summary
	}

	if s.cfg.BridgeSaver != nil {
		bc := s.cfg.BridgeSaver.Current()
		d.HostPort = fmt.Sprintf("%s:%d", bc.MiSTer.Host, bc.MiSTer.Port)
		d.FieldOrder = bc.Video.InterlaceFieldOrder
	}

	d.Tiles = buildStatusTiles(s.cfg, d, view)

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

func buildStatusTiles(cfg Config, d statusPanelData, view core.StatusHomeView) []statusTile {
	if d.State == string(core.StatePlaying) || d.State == string(core.StatePaused) {
		return buildLiveStatusTiles(cfg, d, view)
	}
	return buildIdleStatusTiles(cfg, d)
}

func buildIdleStatusTiles(cfg Config, d statusPanelData) []statusTile {
	tiles := make([]statusTile, 0, 4)

	bridgeTile := statusTile{
		Num: "01", Name: "Bridge",
		BadgeClass: "run", BadgeGlyph: "●", BadgeLabel: "online",
	}
	if cfg.BridgeSaver != nil {
		bc := cfg.BridgeSaver.Current()
		bridgeTile.Rows = []tileRow{
			{"host", bc.MiSTer.Host},
			{"src port", fmt.Sprintf("%d", bc.MiSTer.SourcePort)},
			{"uptime", d.Uptime},
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
				BadgeLabel: adapterBadgeLabel(a, st.State),
				Rows: []tileRow{
					{"mode", adapterModeLabel(a)},
					{"state", adapterBadgeLabel(a, st.State)},
					{"since", formatSince(st.Since)},
					{"source", a.Name()},
				},
			}
			if !a.IsEnabled() {
				tile.BadgeClass = "off"
				tile.BadgeGlyph = "○"
			}
			tiles = append(tiles, tile)
		}
	}
	return tiles
}

func buildLiveStatusTiles(cfg Config, d statusPanelData, view core.StatusHomeView) []statusTile {
	bc, hasBridge := currentBridgeConfig(cfg)
	sourceName := d.AdapterDisplayName
	if sourceName == "" {
		sourceName = "Source"
	}
	command := "play"
	bridgeLabel := "casting"
	if d.State == string(core.StatePaused) {
		command = "pause"
		bridgeLabel = "paused"
	}

	tiles := []statusTile{
		{
			Num: "01", Name: "Bridge",
			BadgeClass: "run", BadgeGlyph: "●", BadgeLabel: bridgeLabel,
			Rows: []tileRow{
				{"blits", formatCount(view.BlitsTotal)},
				{"frames", formatCount(view.FramesTotal)},
				{"under-runs", formatCount(view.Underruns)},
				{"ack age", formatAge(view.LastACKAge)},
			},
		},
		{
			Num: "02", Name: sourceName,
			BadgeClass: "live", BadgeGlyph: "▸", BadgeLabel: "active",
			Rows: []tileRow{
				{"command", command},
				{"title", firstNonEmpty(d.Title, d.AdapterRef, "—")},
				{"position", formatClock(firstNonZeroDuration(view.Position, time.Since(view.StartedAt)))},
				{"timeline", "source ✓"},
			},
		},
		{
			Num: "03", Name: "MiSTer",
			BadgeClass: "run", BadgeGlyph: "●", BadgeLabel: ackBadgeLabel(view.LastACKAge),
			Rows: []tileRow{
				{"switchres", switchresValue(d.Modeline)},
				{"tx port", bridgeSourcePort(bc, hasBridge)},
				{"last ack", formatAge(view.LastACKAge)},
				{"echo stall", "none"},
			},
		},
		{
			Num: "04", Name: "Pipeline",
			BadgeClass: "run", BadgeGlyph: "●", BadgeLabel: "nominal",
			Rows: []tileRow{
				{"ffmpeg", "running"},
				{"video", pipelineVideoValue(bc, hasBridge, d.Modeline)},
				{"audio", pipelineAudioValue(bc, hasBridge)},
				{"lz4", lz4Value(bc, hasBridge)},
			},
		},
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

func adapterListeningSummary(reg *adapters.Registry) string {
	if reg == nil {
		return ""
	}
	parts := make([]string, 0)
	for _, a := range reg.List() {
		parts = append(parts, fmt.Sprintf("%s %s", a.DisplayName(), adapterBadgeLabel(a, a.Status().State)))
	}
	if len(parts) == 0 {
		return "no source configured"
	}
	return strings.Join(parts, " · ")
}

func adapterBadgeLabel(a adapters.Adapter, s adapters.State) string {
	if !a.IsEnabled() {
		return "disabled"
	}
	return statusBadgeLabel(s)
}

func adapterModeLabel(a adapters.Adapter) string {
	if !a.IsEnabled() {
		return "disabled"
	}
	return "enabled"
}

func currentBridgeConfig(cfg Config) (bc config.BridgeConfig, ok bool) {
	if cfg.BridgeSaver == nil {
		return bc, false
	}
	return cfg.BridgeSaver.Current(), true
}

func bridgeSourcePort(bc config.BridgeConfig, ok bool) string {
	if !ok {
		return "—"
	}
	return fmt.Sprintf("%d", bc.MiSTer.SourcePort)
}

func ackBadgeLabel(d time.Duration) string {
	if d > 0 {
		return "acked"
	}
	return "online"
}

func switchresValue(modeline string) string {
	if modeline == "" {
		return "—"
	}
	return modeline + " ✓"
}

func pipelineVideoValue(bc config.BridgeConfig, ok bool, modeline string) string {
	if !ok {
		return firstNonEmpty(modeline, "—")
	}
	pixFmt := bc.Video.RGBMode
	if pixFmt == "rgb888" {
		pixFmt = "rgb24"
	}
	return strings.TrimSpace(firstNonEmpty(pixFmt, "video") + " " + firstNonEmpty(modeline, bc.Video.Modeline))
}

func pipelineAudioValue(bc config.BridgeConfig, ok bool) string {
	if !ok {
		return "—"
	}
	return fmt.Sprintf("s16le %d · %dch", bc.Audio.SampleRate, bc.Audio.Channels)
}

func lz4Value(bc config.BridgeConfig, ok bool) string {
	if !ok {
		return "—"
	}
	if bc.Video.LZ4Enabled {
		return "enabled"
	}
	return "disabled"
}

func formatCount(n uint64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	prefix := len(s) % 3
	if prefix == 0 {
		prefix = 3
	}
	b.WriteString(s[:prefix])
	for i := prefix; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func formatSince(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("15:04:05")
}

func formatAge(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms ago", d.Milliseconds())
	}
	return formatElapsed(d) + " ago"
}

func formatClock(d time.Duration) string {
	if d <= 0 {
		return "00:00"
	}
	total := int(d.Seconds())
	h := total / 3600
	m := (total / 60) % 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func firstNonZeroDuration(vals ...time.Duration) time.Duration {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func formatRefreshRate(modeline string) string {
	if modeline == "" {
		return ""
	}
	preset, err := core.ResolvePreset(modeline)
	if err != nil {
		return ""
	}
	switch preset.FpsExpr {
	case "60000/1001":
		return "59.94 Hz"
	case "50/1":
		return "50.00 Hz"
	default:
		return preset.FpsExpr + " Hz"
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
