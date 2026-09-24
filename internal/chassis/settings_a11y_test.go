package chassis

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// The settings drawer follows the WAI-ARIA tabs pattern: one tablist,
// each tab names a tabpanel via aria-controls, the panel points back via
// aria-labelledby, and exactly one tab is selected (and in the tab order).
func TestSettingsDrawer_TabsFollowARIATabsPattern(t *testing.T) {
	t.Parallel()
	s := renderDrawer(t, SettingsData{Errors: map[string]string{}})

	if got := strings.Count(s, `role="tablist"`); got != 1 {
		t.Fatalf("role=tablist count = %d, want 1", got)
	}
	tabRe := regexp.MustCompile(`<button class="settings-tab[^"]*" data-tab="([a-z]+)"[^>]*>`)
	tabs := tabRe.FindAllStringSubmatch(s, -1)
	if len(tabs) != 5 {
		t.Fatalf("found %d settings tabs, want 5", len(tabs))
	}
	selected := 0
	for _, m := range tabs {
		tag, id := m[0], m[1]
		for _, want := range []string{
			`role="tab"`,
			`id="settings-tab-` + id + `"`,
			`aria-controls="settings-pane-` + id + `"`,
		} {
			if !strings.Contains(tag, want) {
				t.Errorf("tab %q missing %s: %s", id, want, tag)
			}
		}
		if strings.Contains(tag, `aria-selected="true"`) {
			selected++
			if strings.Contains(tag, `tabindex="-1"`) {
				t.Errorf("selected tab %q must stay in the tab order: %s", id, tag)
			}
		} else if !strings.Contains(tag, `tabindex="-1"`) {
			t.Errorf("unselected tab %q should use roving tabindex=-1: %s", id, tag)
		}
		panel := `role="tabpanel" id="settings-pane-` + id + `" aria-labelledby="settings-tab-` + id + `"`
		if !strings.Contains(s, panel) {
			t.Errorf("missing tabpanel for %q (%s)", id, panel)
		}
	}
	if selected != 1 {
		t.Errorf("selected tabs = %d, want exactly 1", selected)
	}
	if strings.Contains(s, `class="settings-legend" aria-hidden`) {
		t.Error("scope legend must stay readable to assistive tech")
	}
}

func TestTransport_GearAnnouncesSettingsDrawerState(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "transport", TransportData{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	s := buf.String()
	for _, want := range []string{`aria-controls="settings-panel"`, `aria-expanded="false"`} {
		if !strings.Contains(s, want) {
			t.Errorf("gear button missing %s", want)
		}
	}
	drawer := renderDrawer(t, SettingsData{Errors: map[string]string{}})
	if !strings.Contains(drawer, `class="settings-panel" id="settings-panel"`) {
		t.Error("settings panel must carry the id the gear's aria-controls names")
	}
}

// Host tags were a tabindex span (no focus style) plus a ✕ span that
// keyboard users could not reach; both are real buttons now, and adding a
// host uses an inline field instead of window.prompt.
func TestSettingsURLHostEditor_UsesButtonsAndInlineAdd(t *testing.T) {
	t.Parallel()
	data := SettingsData{
		Errors: map[string]string{},
		Adapters: []AdapterPaneData{{
			Name:          "url",
			Fields:        []adapters.FieldDef{{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap}},
			Values:        map[string]any{"enabled": true},
			HasHostEditor: true,
			Hosts:         []string{"youtube.com"},
		}},
	}
	s := renderDrawer(t, data)
	for _, want := range []string{
		`<button type="button" class="x" data-remove-host="youtube.com" aria-label="Remove youtube.com">`,
		`<button type="button" class="tag add" data-add-host>`,
		`data-host-add-input`,
		`aria-label="New yt-dlp host"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("host editor missing %q", want)
		}
	}
	if strings.Contains(s, `<span class="tag add"`) {
		t.Error("add-host control must be a <button>, not a tabindex span")
	}

	js, err := chassisStaticFS.ReadFile("static/settings-drawer.js")
	if err != nil {
		t.Fatalf("read settings-drawer.js: %v", err)
	}
	// A prompt("...") call, not the word in a comment.
	if regexp.MustCompile(`(^|[^.\w])prompt\(\s*['"]`).Match(js) {
		t.Error("settings-drawer.js must not use window.prompt for adding hosts")
	}
}

func TestSettingsSwitches_HaveAccessibleNames(t *testing.T) {
	t.Parallel()
	data := SettingsData{
		Errors: map[string]string{},
		CatalogProviders: []CatalogProviderState{
			{ID: "mtv-rewind", DisplayName: "MTV Rewind", BadgeLabel: "MTV", ChannelCount: 1, Enabled: true},
		},
		Adapters: []AdapterPaneData{{
			Name:   "dlna",
			Fields: []adapters.FieldDef{{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap}},
			Values: map[string]any{"enabled": false},
		}},
	}
	s := renderDrawer(t, data)
	for _, want := range []string{
		`aria-label="Enable MTV Rewind"`,
		`aria-label="Skip the HLS buffer for direct-stream providers"`,
		`aria-label="Enable DLNA"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("switch missing accessible name %q", want)
		}
	}
}
