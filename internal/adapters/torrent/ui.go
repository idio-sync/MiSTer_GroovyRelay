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
	b.WriteString(`<p class="muted">Enable <code>traffic_acknowledged</code> before starting magnet links or torrent uploads.</p>`)
	b.WriteString(`<form hx-post="/ui/adapter/torrent/play" hx-target="#torrent-panel" hx-swap="outerHTML" autocomplete="off">`)
	b.WriteString(`<label>Magnet <input type="text" name="magnet" required></label>`)
	b.WriteString(`<button type="submit">Play Magnet</button>`)
	b.WriteString(`</form>`)
	b.WriteString(`<form hx-post="/ui/adapter/torrent/upload" hx-target="#torrent-panel" hx-swap="outerHTML" enctype="multipart/form-data">`)
	b.WriteString(`<input type="file" name="torrent_file" accept=".torrent" required>`)
	b.WriteString(`<button type="submit">Upload Torrent</button>`)
	b.WriteString(`</form>`)
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
		pollAttrs = ` hx-get="/ui/adapter/torrent/live" hx-trigger="every 5s" hx-swap="outerHTML"`
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
