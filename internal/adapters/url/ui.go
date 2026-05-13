package url

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// handlePanel renders the htmx fragment shown inside the URL adapter
// card on the settings page.
func (a *Adapter) handlePanel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(a.renderPanel()))
}

// ExtraPanelHTML implements ui.ExtraHTMLProvider — the UI adapter
// template inserts whatever this returns below the standard form.
func (a *Adapter) ExtraPanelHTML() template.HTML {
	return template.HTML(a.renderPanel())
}

// renderPanel produces the panel fragment.
//
// The URL adapter's lifecycle state drives only the local status line
// ("Idle / Playing: <url> / Error: ..."). Active playback transport
// controls are rendered by the global now-playing banner.
//
// Layout order:
//  1. Status line (lifecycle)
//  2. URL form + optional mode-radio
//  3. Hosts line (yt-dlp auto-resolve list)
//  4. yt-dlp version line
//  5. Cookies section (collapsed details)
//  6. Recent history list (when >=1 entry)
func (a *Adapter) renderPanel() string {
	a.mu.Lock()
	lifecycle := a.state
	lastURL := a.lastURL
	lastErr := a.lastErr
	cfg := a.cfg
	probe := a.ytdlpProbe
	a.mu.Unlock()

	hist := a.history.List()

	var b strings.Builder

	fmt.Fprintf(&b,
		`<section class="url-panel" id="url-panel" hx-get="/ui/adapter/url/panel" hx-trigger="%s" hx-swap="outerHTML">`,
		"every 5s")
	b.WriteString(`<h3>Play URL</h3>`)

	// Lifecycle status line (driven by a.state).
	switch lifecycle {
	case adapters.StateRunning:
		if lastURL != "" {
			fmt.Fprintf(&b, `<p class="status run">Playing: <code>%s</code></p>`,
				template.HTMLEscapeString(redactURL(lastURL)))
		} else {
			b.WriteString(`<p class="status run">Running</p>`)
		}
	case adapters.StateError:
		fmt.Fprintf(&b, `<p class="status err">Error: %s</p>`,
			template.HTMLEscapeString(lastErr))
	default:
		b.WriteString(`<p class="status">Idle</p>`)
	}

	// URL form (with optional mode-radio for yt-dlp).
	modeRadio := ""
	if cfg.YtdlpEnabled && probe.OK {
		modeRadio = `<fieldset class="url-mode">
  <legend>Mode</legend>
  <label><input type="radio" name="mode" value="auto" checked> Auto</label>
  <label><input type="radio" name="mode" value="ytdlp"> yt-dlp</label>
  <label><input type="radio" name="mode" value="direct"> Direct</label>
</fieldset>`
	}
	fmt.Fprintf(&b, `<form hx-post="/ui/adapter/url/play" hx-target="#url-panel" hx-swap="outerHTML" autocomplete="off">
    %s
    <input type="url" name="url" placeholder="https://example.com/video.mp4 or https://youtu.be/..." required>
    <button type="submit">Play</button>
  </form>`, modeRadio)

	// Existing yt-dlp surface: hosts, version, cookies — preserved verbatim.
	b.WriteString(renderHostsLine(cfg.YtdlpHosts))
	b.WriteString(renderVersionLine(probe))
	b.WriteString(a.renderCookiesSection())

	// History list, if any. Entries that carry a resolver-provided
	// title (yt-dlp on YouTube/Vimeo/Archive.org/etc.) render the
	// title as the primary line with the URL muted underneath; bare
	// direct-mode URLs render as just the code line.
	if len(hist) > 0 {
		b.WriteString(`<div class="history"><h4>Recent:</h4><ul>`)
		for i, e := range hist {
			b.WriteString(`<li>`)
			if e.Title != "" {
				fmt.Fprintf(&b,
					`<div class="history-title">%s</div>`+
						`<code class="history-url muted">%s</code> `,
					template.HTMLEscapeString(e.Title),
					template.HTMLEscapeString(redactURL(e.URL)))
			} else {
				fmt.Fprintf(&b, `<code>%s</code> `,
					template.HTMLEscapeString(redactURL(e.URL)))
			}
			fmt.Fprintf(&b,
				`<button type="button" hx-post="/ui/adapter/url/history/play" hx-vals='{"idx":"%d"}' hx-target="#url-panel" hx-swap="outerHTML">Cast</button> `+
					`<button type="button" hx-post="/ui/adapter/url/history/delete" hx-vals='{"idx":"%d"}' hx-target="#url-panel" hx-swap="outerHTML">✕</button>`+
					`</li>`,
				i, i)
		}
		b.WriteString(`</ul></div>`)
	}

	b.WriteString(`</section>`)
	return b.String()
}

// formatDuration formats a time.Duration as MM:SS for sources < 1h
// or HH:MM:SS otherwise. Negative durations render as 00:00.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d / time.Second)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

// renderHostsLine produces the "Auto-resolves: ..." line. Truncated to
// fit within ~70 characters TOTAL (suffix included). Budget = 70 minus
// the worst-case suffix length so the rendered line stays at-or-under
// the visual budget on narrow panels.
func renderHostsLine(hosts []string) string {
	if len(hosts) == 0 {
		return `<p class="url-hosts muted">Auto-resolves: <em>(none)</em></p>`
	}
	const totalBudget = 70
	suffix := fmt.Sprintf("... (%d total)", len(hosts))
	hostBudget := totalBudget - len(suffix)
	if hostBudget < 0 {
		hostBudget = 0
	}
	joined := ""
	count := 0
	for _, h := range hosts {
		next := h
		if count > 0 {
			next = ", " + h
		}
		if len(joined)+len(next) > hostBudget && count > 0 {
			joined += suffix
			return fmt.Sprintf(`<p class="url-hosts muted">Auto-resolves: %s</p>`,
				template.HTMLEscapeString(joined))
		}
		joined += next
		count++
	}
	return fmt.Sprintf(`<p class="url-hosts muted">Auto-resolves: %s</p>`,
		template.HTMLEscapeString(joined))
}

// renderVersionLine produces the yt-dlp version line, or the
// not-found notice. Read at adapter Start; not refreshed.
func renderVersionLine(probe ytdlpProbe) string {
	if !probe.OK {
		return `<p class="url-ytdlp-version muted">yt-dlp not found — auto-resolve disabled</p>`
	}
	return fmt.Sprintf(`<p class="url-ytdlp-version muted">yt-dlp %s at <code>%s</code></p>`,
		template.HTMLEscapeString(probe.Version),
		template.HTMLEscapeString(probe.Path))
}

// renderCookiesSection produces the collapsed <details> block. The
// status div MUST keep its id="url-cookies-status" — cookies POST/
// DELETE handlers in cookies.go emit replacement fragments targeted
// at that id.
func (a *Adapter) renderCookiesSection() string {
	stat, ok, _ := statCookies(a.cookiesPath)
	statusLine := `No cookies set`
	if ok {
		statusLine = fmt.Sprintf("Cookies stored (%d bytes, set %s)",
			stat.Size,
			stat.Mtime.UTC().Format("2006-01-02 15:04:05Z"))
	}
	return fmt.Sprintf(`<details class="url-cookies">
  <summary>Cookies for yt-dlp</summary>
  <div class="cookies-status" id="url-cookies-status">%s</div>
  <form hx-post="/ui/adapter/url/cookies" hx-target="#url-cookies-status" hx-swap="outerHTML" autocomplete="off">
    <textarea name="cookies" rows="6" placeholder="Paste Netscape cookies.txt content here..." autocomplete="off" spellcheck="false"></textarea>
    <button type="submit">Save Cookies</button>
    <button type="button" hx-delete="/ui/adapter/url/cookies" hx-target="#url-cookies-status" hx-swap="outerHTML">Clear</button>
  </form>
</details>`, template.HTMLEscapeString(statusLine))
}
