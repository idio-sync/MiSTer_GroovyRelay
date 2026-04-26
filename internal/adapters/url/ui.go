package url

import (
	"fmt"
	"html/template"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// handlePanel renders the htmx fragment shown inside the URL adapter
// card on the settings page. Auto-refreshes every 5s so the status
// line catches EOF / preemption without operator action.
func (a *Adapter) handlePanel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(a.renderPanel()))
}

// ExtraPanelHTML implements ui.ExtraHTMLProvider — the UI adapter
// template inserts whatever this returns below the standard form.
// Using it lets us embed the URL play form without changing the
// generic adapter-panel template.
func (a *Adapter) ExtraPanelHTML() template.HTML {
	return template.HTML(a.renderPanel())
}

// renderPanel produces the panel fragment.
//
// Sections:
//  1. Status line (Idle / Playing: <url> / Error: <msg>)
//  2. Mode radio (auto/ytdlp/direct) — hidden when YtdlpEnabled=false
//     OR ytdlpProbe.OK=false
//  3. URL input + Play button
//  4. "Auto-resolves: ..." line — comma-joined, truncated at ~70 chars
//     with "(N total)" suffix (review fix M2)
//  5. yt-dlp version/path line — or "yt-dlp not found" if probe failed
//  6. Cookies section (collapsed <details>) — status, textarea,
//     Save/Clear buttons. Textarea ships autocomplete=off,
//     spellcheck=false (review fix I4); never echoes file content
//     back to the browser.
func (a *Adapter) renderPanel() string {
	a.mu.Lock()
	state := a.state
	lastURL := a.lastURL
	lastErr := a.lastErr
	cfg := a.cfg
	probe := a.ytdlpProbe
	a.mu.Unlock()

	status := `<p class="status">Idle</p>`
	switch state {
	case adapters.StateRunning:
		if lastURL != "" {
			status = fmt.Sprintf(`<p class="status run">Playing: <code>%s</code></p>`,
				template.HTMLEscapeString(redactURL(lastURL)))
		} else {
			status = `<p class="status run">Running</p>`
		}
	case adapters.StateError:
		status = fmt.Sprintf(`<p class="status err">Error: %s</p>`,
			template.HTMLEscapeString(lastErr))
	}

	modeRadio := ""
	if cfg.YtdlpEnabled && probe.OK {
		modeRadio = `<fieldset class="url-mode">
  <legend>Mode</legend>
  <label><input type="radio" name="mode" value="auto" checked> Auto</label>
  <label><input type="radio" name="mode" value="ytdlp"> yt-dlp</label>
  <label><input type="radio" name="mode" value="direct"> Direct</label>
</fieldset>`
	}

	hostsLine := renderHostsLine(cfg.YtdlpHosts)
	versionLine := renderVersionLine(probe)
	cookiesSection := a.renderCookiesSection()

	return fmt.Sprintf(`<section class="url-panel" id="url-panel" hx-get="/ui/adapter/url/panel" hx-trigger="every 5s" hx-swap="outerHTML">
  <h3>Play URL</h3>
  %s
  <form hx-post="/ui/adapter/url/play" hx-target="#url-panel" hx-swap="outerHTML" autocomplete="off">
    %s
    <input type="url" name="url" placeholder="https://example.com/video.mp4 or https://youtu.be/..." required>
    <button type="submit">Play</button>
  </form>
  %s
  %s
  %s
</section>`, status, modeRadio, hostsLine, versionLine, cookiesSection)
}

// renderHostsLine produces the "Auto-resolves: ..." line. Truncated to
// fit within ~70 characters TOTAL (suffix included — review fix I6
// expanded review fix M2). Budget = 70 minus the worst-case suffix
// length so the rendered line stays at-or-under the visual budget on
// narrow panels.
func renderHostsLine(hosts []string) string {
	if len(hosts) == 0 {
		return `<p class="url-hosts muted">Auto-resolves: <em>(none)</em></p>`
	}
	const totalBudget = 70
	suffix := fmt.Sprintf("... (%d total)", len(hosts))
	hostBudget := totalBudget - len(suffix) // reserve room for the suffix
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

// renderCookiesSection produces the collapsed <details> block. Status
// shows file size + mtime; textarea is always empty on render
// (never echoes saved content). The status div MUST keep its
// id="url-cookies-status" — cookies POST/DELETE handlers in cookies.go
// emit replacement fragments targeted at that id (locked in by tests
// TestHandleCookiesPOST_HTMX_RendersStatusFragment / DELETE).
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
