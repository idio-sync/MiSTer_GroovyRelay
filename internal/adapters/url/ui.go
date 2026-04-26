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

// renderPanel produces the panel fragment. Includes:
//   - status line (Idle / Playing: <url> / Error)
//   - text input bound to POST /ui/adapter/url/play via htmx
//   - hx-trigger="every 5s" self-refresh on the outer container so the
//     status reflects EOF/preempt without operator action
//
// Markup is intentionally minimal — no CSS framework dependencies; it
// inherits the bridge's app.css naming.
func (a *Adapter) renderPanel() string {
	a.mu.Lock()
	state := a.state
	lastURL := a.lastURL
	lastErr := a.lastErr
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
		status = fmt.Sprintf(`<p class="status err">Error: %s</p>`, template.HTMLEscapeString(lastErr))
	}

	return fmt.Sprintf(`<section class="url-panel" id="url-panel" hx-get="/ui/adapter/url/panel" hx-trigger="every 5s" hx-swap="outerHTML">
  <h3>Play URL</h3>
  %s
  <form hx-post="/ui/adapter/url/play" hx-target="#url-panel" hx-swap="outerHTML">
    <input type="url" name="url" placeholder="https://example.com/video.mp4" required>
    <button type="submit">Play</button>
  </form>
</section>`, status)
}
