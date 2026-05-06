package main

import (
	"bytes"
	"strings"
	"testing"
)

// Banner tests exercise greetingFor (pure, no I/O) and printGreetingTo
// (writer-parameterized). The full *adapters.Registry path is covered
// only indirectly via the live bridge integration test in Task 11 —
// here we test the flat []adapterStatus shape directly.

func TestGreetingFor_IncludesURLAndAdapters(t *testing.T) {
	out := greetingFor("1.2.3", "192.168.1.20", 32500, []adapterStatus{
		{display: "Plex adapter", enabled: true},
		{display: "Jellyfin adapter", enabled: false},
		{display: "URL adapter", enabled: true},
	})
	for _, want := range []string{
		"MiSTer GroovyRelay",
		"1.2.3",
		"http://192.168.1.20:32500",
		"http://localhost:32500",
		"Plex adapter",
		"enabled",
		"Jellyfin adapter",
		"disabled",
		"Press Ctrl-C to quit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("greeting missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// TestGreetingFor_LocalhostOnlyWhenNoHostIP asserts the LAN-IP line is
// dropped (offline host with no default route — the existing fallback
// path in main.go) but localhost is still printed so the user has a
// reachable URL.
func TestGreetingFor_LocalhostOnlyWhenNoHostIP(t *testing.T) {
	out := greetingFor("1.0.0", "", 32500, nil)
	if strings.Contains(out, "http://:32500") {
		t.Errorf("malformed URL leaked in: %q", out)
	}
	if !strings.Contains(out, "http://localhost:32500") {
		t.Errorf("expected localhost line; got %q", out)
	}
}

// TestPrintGreeting_SuppressedByEnv asserts MISTER_GROOVY_NO_BANNER=1
// short-circuits printGreeting even when mode is text.
func TestPrintGreeting_SuppressedByEnv(t *testing.T) {
	t.Setenv("MISTER_GROOVY_NO_BANNER", "1")
	var buf bytes.Buffer
	printGreetingTo(&buf, true /*text mode*/, "1", "1.2.3.4", 32500, nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output with NO_BANNER; got %q", buf.String())
	}
}

// TestPrintGreeting_SuppressedInJSONMode asserts the greeter does not
// print when mode is json (banner would otherwise pollute the JSON
// stream that aggregators tail).
func TestPrintGreeting_SuppressedInJSONMode(t *testing.T) {
	t.Setenv("MISTER_GROOVY_NO_BANNER", "")
	t.Setenv("MISTER_GROOVY_BANNER", "")
	var buf bytes.Buffer
	printGreetingTo(&buf, false /*text mode*/, "1", "1.2.3.4", 32500, nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output in json mode; got %q", buf.String())
	}
}

// TestPrintGreeting_ForcedInJSONMode asserts MISTER_GROOVY_BANNER=1
// overrides the json-mode suppression for operators who want both.
func TestPrintGreeting_ForcedInJSONMode(t *testing.T) {
	t.Setenv("MISTER_GROOVY_NO_BANNER", "")
	t.Setenv("MISTER_GROOVY_BANNER", "1")
	var buf bytes.Buffer
	printGreetingTo(&buf, false, "1", "1.2.3.4", 32500, nil)
	if buf.Len() == 0 {
		t.Errorf("expected output with MISTER_GROOVY_BANNER=1")
	}
}

func TestFormatAttrSuffix(t *testing.T) {
	if got := formatAttrSuffix(nil); got != "" {
		t.Errorf("nil attrs: got %q, want empty", got)
	}
	if got := formatAttrSuffix([]any{"name", "plex"}); got != " (name=plex)" {
		t.Errorf("single attr: got %q", got)
	}
	if got := formatAttrSuffix([]any{"name", "plex", "version", 2}); got != " (name=plex, version=2)" {
		t.Errorf("two attrs: got %q", got)
	}
	if got := formatAttrSuffix([]any{"odd"}); got != "" {
		t.Errorf("malformed odd-length attrs should return empty; got %q", got)
	}
}
