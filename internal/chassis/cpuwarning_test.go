package chassis

import (
	"bytes"
	"strings"
	"testing"
)

func TestSettingsSystemPane_CPUWarning(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	const msg = "CPU pinning allows CPUs 6,7 but all of them are isolated & more"

	var buf bytes.Buffer
	data := SettingsData{Errors: map[string]string{}, CPUWarning: msg}
	if err := tmpl.ExecuteTemplate(&buf, "settings-system", data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	s := buf.String()
	if !strings.Contains(s, `class="settings-warn"`) {
		t.Errorf("missing settings-warn notice")
	}
	if !strings.Contains(s, "all of them are isolated &amp; more") {
		t.Errorf("warning text not rendered (escaped)")
	}

	buf.Reset()
	data.CPUWarning = ""
	if err := tmpl.ExecuteTemplate(&buf, "settings-system", data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(buf.String(), "settings-warn") {
		t.Errorf("notice rendered without a warning")
	}
}

func TestSettingsDataFromConfig_CPUWarning(t *testing.T) {
	t.Parallel()
	got := settingsDataFromConfig(Config{CPUWarning: "pinned"})
	if got.CPUWarning != "pinned" {
		t.Errorf("CPUWarning = %q, want %q", got.CPUWarning, "pinned")
	}
}
