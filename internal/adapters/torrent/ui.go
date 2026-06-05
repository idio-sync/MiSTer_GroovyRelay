package torrent

import (
	"fmt"
	"html/template"
	"strings"
)

type statusView struct {
	Enabled             bool   `json:"enabled"`
	TrafficAcknowledged bool   `json:"traffic_acknowledged"`
	ActiveTitle         string `json:"active_title,omitempty"`
	ActiveToken         string `json:"active_token,omitempty"`
}

func (a *Adapter) statusView() statusView {
	a.mu.Lock()
	defer a.mu.Unlock()
	view := statusView{
		Enabled:             a.cfg.Enabled,
		TrafficAcknowledged: a.cfg.TrafficAcknowledged,
		ActiveToken:         a.activeToken,
	}
	if s := a.sessions[a.activeToken]; s != nil {
		view.ActiveTitle = s.Title
	}
	return view
}

func (a *Adapter) renderPanel() string {
	view := a.statusView()
	var b strings.Builder
	b.WriteString(`<section class="torrent-panel" id="torrent-panel">`)
	b.WriteString(`<h3>Torrent</h3>`)
	b.WriteString(renderLiveStatus(view))
	b.WriteString(`<p class="muted">BitTorrent traffic requires <code>traffic_acknowledged</code>.</p>`)
	b.WriteString(`</section>`)
	return b.String()
}

func (a *Adapter) renderLiveStatus() string {
	return renderLiveStatus(a.statusView())
}

func renderLiveStatus(view statusView) string {
	var b strings.Builder
	pollAttrs := ""
	if view.ActiveToken != "" {
		pollAttrs = ` hx-get="/old_ui/adapter/torrent/live" hx-trigger="every 5s" hx-swap="outerHTML"`
	}
	fmt.Fprintf(&b, `<div id="torrent-live"%s>`, pollAttrs)
	if view.ActiveTitle != "" {
		fmt.Fprintf(&b, `<p class="status run">Playing: <code>%s</code></p>`, template.HTMLEscapeString(view.ActiveTitle))
	} else if !view.Enabled {
		b.WriteString(`<p class="status">Disabled</p>`)
	} else if !view.TrafficAcknowledged {
		b.WriteString(`<p class="status err">BitTorrent traffic acknowledgement required</p>`)
	} else {
		b.WriteString(`<p class="status">Idle</p>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}
