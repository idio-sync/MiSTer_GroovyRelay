package chassis

import (
	"bytes"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestOSDSettingsTableEntries(t *testing.T) {
	t.Parallel()
	fields := map[string]func(config.OSDConfig) bool{
		"osd_enabled":   func(c config.OSDConfig) bool { return c.Enabled },
		"osd_clock":     func(c config.OSDConfig) bool { return c.Clock },
		"osd_clock_24h": func(c config.OSDConfig) bool { return c.Clock24h },
	}
	for name, get := range fields {
		decode, ok := bridgeFieldDecoders[name]
		if !ok {
			t.Errorf("missing decoder for %s", name)
			continue
		}
		v, err := decode("true")
		if err != nil || v != true {
			t.Errorf("%s decode(true) = %v, %v; want true, nil", name, v, err)
		}
		overlay, ok := bridgeFieldOverlays[name]
		if !ok {
			t.Errorf("missing overlay for %s", name)
			continue
		}
		c := &config.BridgeConfig{}
		overlay(c, true)
		if !get(c.OSD) {
			t.Errorf("%s overlay(true) left OSD = %+v", name, c.OSD)
		}
		overlay(c, false)
		if get(c.OSD) {
			t.Errorf("%s overlay(false) left OSD = %+v", name, c.OSD)
		}
		if got := bridgeFieldScopes[name]; got != adapters.ScopeHotSwap {
			t.Errorf("scope for %s = %v, want ScopeHotSwap", name, got)
		}
	}
}

// switchTag returns the <button …> tag carrying data-field="name".
func switchTag(t *testing.T, html, name string) string {
	t.Helper()
	i := strings.Index(html, `data-field="`+name+`"`)
	if i < 0 {
		t.Fatalf("no switch with data-field=%q in:\n%s", name, html)
	}
	start := strings.LastIndex(html[:i], "<button")
	end := strings.Index(html[i:], ">")
	if start < 0 || end < 0 {
		t.Fatalf("data-field=%q is not inside a <button> tag", name)
	}
	return html[start : i+end+1]
}

func TestSettingsAV_RendersOSDSwitches(t *testing.T) {
	t.Parallel()
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	data := SettingsData{
		Bridge: config.BridgeConfig{OSD: config.OSDConfig{Enabled: true, Clock: false, Clock24h: true}},
		Errors: map[string]string{},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-av", data); err != nil {
		t.Fatalf("ExecuteTemplate(settings-av): %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "<h4>On-screen display</h4>") {
		t.Errorf("settings-av has no On-screen display section:\n%s", html)
	}
	for name, on := range map[string]bool{"osd_enabled": true, "osd_clock": false, "osd_clock_24h": true} {
		tag := switchTag(t, html, name)
		want := `aria-pressed="false"`
		if on {
			want = `aria-pressed="true"`
		}
		if !strings.Contains(tag, want) {
			t.Errorf("%s switch = %s, want %s", name, tag, want)
		}
	}
}
