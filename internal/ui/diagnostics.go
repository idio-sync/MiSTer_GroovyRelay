package ui

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"runtime"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

// diagnosticsPanelData is the template context for /ui/diagnostics.
type diagnosticsPanelData struct {
	RingCapacity  int
	Uptime        string
	EvictedMarker bool
	Counts        struct{ All, Info, Warn, Err int }
	Events        []statusEvent

	ProbeResult template.HTML

	BuildInfo []diagKV
}

// diagKV is a key/value pair for the Build info section.
type diagKV struct{ Key, Value string }

func (s *Server) handleDiagnosticsGET(w http.ResponseWriter, r *http.Request) {
	data := s.shellDataForPath(r.URL.Path)
	panelHTML, err := s.renderTemplateHTML("diagnostics-panel.html", s.buildDiagnosticsData())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.PanelHTML = panelHTML
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "shell.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleDiagnosticsProbe(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
	defer cancel()
	start := time.Now()
	var probeErr error
	if s.cfg.MisterProber != nil {
		probeErr = s.cfg.MisterProber.Probe(ctx)
	} else {
		probeErr = fmt.Errorf("no prober configured")
	}
	elapsed := time.Since(start)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if probeErr != nil {
		fmt.Fprintf(w,
			`<div class="gr-probe" id="probe-result"><button class="gr-btn primary" type="button" hx-post="/ui/diagnostics/probe" hx-target="#probe-result" hx-swap="outerHTML">Test MiSTer connectivity</button><div class="gr-callout err" style="flex:1;"><p><strong style="color:var(--gr-err);">probe failed</strong> · %s · %dms</p></div></div>`,
			template.HTMLEscapeString(probeErr.Error()), elapsed.Milliseconds())
		return
	}
	fmt.Fprintf(w,
		`<div class="gr-probe" id="probe-result"><button class="gr-btn primary" type="button" hx-post="/ui/diagnostics/probe" hx-target="#probe-result" hx-swap="outerHTML">Test MiSTer connectivity</button><div class="gr-callout ok" style="flex:1;"><p><strong style="color:var(--gr-ok);">ACK in %dms</strong></p></div></div>`,
		elapsed.Milliseconds())
}

func (s *Server) buildDiagnosticsData() diagnosticsPanelData {
	d := diagnosticsPanelData{RingCapacity: 256}
	d.BuildInfo = []diagKV{
		{"version", s.cfg.Version},
		{"go runtime", runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH},
	}

	if s.cfg.EventLog != nil {
		all := s.cfg.EventLog.Snapshot()
		d.Counts.All = len(all)
		for _, e := range all {
			switch e.Severity {
			case eventlog.SeverityInfo:
				d.Counts.Info++
			case eventlog.SeverityWarn:
				d.Counts.Warn++
			case eventlog.SeverityErr:
				d.Counts.Err++
			}
		}
		d.EvictedMarker = (len(all) == 256)
		d.Events = make([]statusEvent, 0, len(all))
		for i := len(all) - 1; i >= 0; i-- {
			e := all[i]
			d.Events = append(d.Events, statusEvent{
				Time:     e.Time.Format("15:04:05"),
				Severity: e.Severity.String(),
				SevClass: e.Severity.String(),
				Source:   e.Source,
				Message:  e.Message,
			})
		}
	}
	return d
}
