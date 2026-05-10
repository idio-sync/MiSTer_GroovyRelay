package ui

import (
	"context"
	"fmt"
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

	ProbeResult probeResultData

	BuildInfo []diagKV
}

// diagKV is a key/value pair for the Build info section.
type diagKV struct{ Key, Value string }

// probeResultData parameterizes the probe-result.html partial. Status
// is one of "" (initial render — button only), "ok", or "err".
type probeResultData struct {
	Status    string
	ElapsedMs int64
	ErrMsg    string
}

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
	data := probeResultData{ElapsedMs: time.Since(start).Milliseconds()}
	if probeErr != nil {
		data.Status = "err"
		data.ErrMsg = probeErr.Error()
	} else {
		data.Status = "ok"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "probe-result", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) buildDiagnosticsData() diagnosticsPanelData {
	d := diagnosticsPanelData{
		RingCapacity: 256,
		Uptime:       formatElapsed(time.Since(s.cfg.StartedAt)),
	}
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
