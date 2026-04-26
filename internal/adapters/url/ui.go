package url

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// handlePanel renders the htmx fragment shown inside the URL adapter
// card on the settings page. Polling cadence and visible controls
// depend on core.Manager state — read on every render.
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
// **Two distinct "states".** The URL adapter has its own a.state
// (adapters.State, lifecycle) and core.Manager has a separate FSM
// state via core.Status().State (StateIdle / StatePlaying /
// StatePaused). Conditional rules below use **manager state**;
// a.state only drives the lifecycle status line ("Idle / Playing:
// <url> / Error: …"). Spec §"Panel layout".
//
// Layout order:
//  1. Status line (lifecycle)
//  2. URL form + optional mode-radio
//  3. Position line + scrub bar (manager Playing/Paused, Duration > 0)
//  4. Control row Pause/Stop/Replay (manager Playing/Paused)
//  5. Hosts line (yt-dlp auto-resolve list)
//  6. yt-dlp version line
//  7. Cookies section (collapsed details)
//  8. Recent history list (when ≥1 entry)
//  9. Inline drag-protection script (when control row rendered)
func (a *Adapter) renderPanel() string {
	a.mu.Lock()
	lifecycle := a.state
	lastURL := a.lastURL
	lastErr := a.lastErr
	cfg := a.cfg
	probe := a.ytdlpProbe
	a.mu.Unlock()

	// Tests construct adapters with Core: nil (e.g. registry-boundary
	// + the older renderPanel tests that predate the manager FSM
	// integration). Treat absence of core as zero-value SessionStatus
	// so all conditional rules below collapse to the Idle layout.
	var st core.SessionStatus
	if a.core != nil {
		st = a.core.Status()
	}
	hist := a.history.List()

	var b strings.Builder

	// Outer section + dynamic poll cadence.
	trigger := "every 5s"
	if st.State == core.StatePlaying || st.State == core.StatePaused {
		trigger = "every 1s"
	}
	fmt.Fprintf(&b,
		`<section class="url-panel" id="url-panel" hx-get="/ui/adapter/url/panel" hx-trigger="%s" hx-swap="outerHTML">`,
		trigger)
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

	// Active-session UI: position, scrub, control row. State takes
	// precedence over Duration (state-precedence rule, spec §"Probe-
	// driven control gating") so an EOF that left m.active populated
	// doesn't render a stale frozen scrub bar.
	active := st.State == core.StatePlaying || st.State == core.StatePaused
	foreign := st.AdapterRef != "" && !strings.HasPrefix(st.AdapterRef, "url:")

	if active && st.Duration > 0 {
		fmt.Fprintf(&b, `<p class="position">%s / %s</p>`,
			formatDuration(st.Position), formatDuration(st.Duration))

		durMs := int(st.Duration / time.Millisecond)
		posMs := int(st.Position / time.Millisecond)
		fmt.Fprintf(&b,
			`<input type="range" class="scrub" min="0" max="%d" value="%d" `+
				`name="offset_ms" `+
				`hx-post="/ui/adapter/url/seek" hx-trigger="change" hx-target="#url-panel" hx-swap="outerHTML">`,
			durMs, posMs)
	}

	if active {
		// Control row: Pause/Resume + Stop + Replay. All three
		// disabled when ownership belongs to another adapter.
		disabled := ""
		if foreign {
			disabled = " disabled"
		}
		pauseLabel := "Pause"
		pausePath := "/ui/adapter/url/pause"
		if st.State == core.StatePaused {
			pauseLabel = "Resume"
			pausePath = "/ui/adapter/url/resume"
		}
		fmt.Fprintf(&b,
			`<div class="controls">`+
				`<button type="button" hx-post="%s" hx-target="#url-panel" hx-swap="outerHTML"%s>%s</button>`+
				`<button type="button" hx-post="/ui/adapter/url/stop" hx-target="#url-panel" hx-swap="outerHTML"%s>Stop</button>`+
				`<button type="button" hx-post="/ui/adapter/url/replay" hx-target="#url-panel" hx-swap="outerHTML"%s>Replay</button>`+
				`</div>`,
			pausePath, disabled, pauseLabel, disabled, disabled)
	}

	// Existing yt-dlp surface: hosts, version, cookies — preserved verbatim.
	b.WriteString(renderHostsLine(cfg.YtdlpHosts))
	b.WriteString(renderVersionLine(probe))
	b.WriteString(a.renderCookiesSection())

	// History list, if any.
	if len(hist) > 0 {
		b.WriteString(`<div class="history"><h4>Recent:</h4><ul>`)
		for i, e := range hist {
			fmt.Fprintf(&b,
				`<li><code>%s</code> `+
					`<button type="button" hx-post="/ui/adapter/url/history/play" hx-vals='{"idx":"%d"}' hx-target="#url-panel" hx-swap="outerHTML">Cast</button> `+
					`<button type="button" hx-post="/ui/adapter/url/history/delete" hx-vals='{"idx":"%d"}' hx-target="#url-panel" hx-swap="outerHTML">✕</button>`+
					`</li>`,
				template.HTMLEscapeString(redactURL(e.URL)), i, i)
		}
		b.WriteString(`</ul></div>`)
	}

	// Inline drag-protection script (only when control row + scrub are
	// rendered together, since we have a slider DOM element to guard).
	if active && st.Duration > 0 {
		b.WriteString(`<script>(function(){var p=document.getElementById('url-panel');if(!p||p.dataset.scrubGuard==='1')return;` +
			`var s=p.querySelector('input.scrub');if(!s)return;` +
			`p.dataset.scrubGuard='1';var paused=false;` +
			`s.addEventListener('pointerdown',function(){paused=true;});` +
			`s.addEventListener('pointerup',function(){paused=false;});` +
			`p.addEventListener('htmx:beforeSwap',function(e){if(paused)e.preventDefault();});` +
			`})();</script>`)
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
