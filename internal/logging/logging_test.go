package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestSetLevel_LiveTogglesActiveThreshold asserts that calling SetLevel
// after logger construction changes whether subsequent emissions of a
// given level reach the handler. This is the contract the UI's
// "Debug Logging" checkbox depends on: the operator flips the box, the
// already-running logger immediately starts (or stops) emitting Debug
// records.
func TestSetLevel_LiveTogglesActiveThreshold(t *testing.T) {
	var buf bytes.Buffer
	// Replace the package handler's destination with our buffer for the
	// duration of the test, then restore. New() rebuilds the slog.Logger
	// using the package's mutable LevelVar; the level we pass becomes
	// the initial threshold.
	original := newHandlerWriter
	t.Cleanup(func() { newHandlerWriter = original })
	newHandlerWriter = &buf

	logger := New("info")

	logger.Debug("low-priority-pre")
	if strings.Contains(buf.String(), "low-priority-pre") {
		t.Fatalf("Debug emitted at level=info; output:\n%s", buf.String())
	}

	SetLevel("debug")

	logger.Debug("low-priority-post")
	if !strings.Contains(buf.String(), "low-priority-post") {
		t.Fatalf("Debug suppressed after SetLevel(debug); output:\n%s", buf.String())
	}

	SetLevel("info")

	pre := buf.Len()
	logger.Debug("low-priority-final")
	if buf.Len() != pre {
		t.Fatalf("Debug re-emitted after SetLevel(info); appended:\n%s", buf.String()[pre:])
	}
}

// TestSetLevel_UnknownStringMapsToInfo guards the parse path: bogus
// values must not silently activate Debug or otherwise alter the
// threshold to something outside the 4-level set.
func TestSetLevel_UnknownStringMapsToInfo(t *testing.T) {
	var buf bytes.Buffer
	original := newHandlerWriter
	t.Cleanup(func() { newHandlerWriter = original })
	newHandlerWriter = &buf

	_ = New("debug") // start at debug so we can verify the downgrade
	SetLevel("nonsense")

	if got, want := levelVar.Level(), slog.LevelInfo; got != want {
		t.Errorf("after SetLevel(nonsense): level=%v, want %v", got, want)
	}
}

// TestResolveMode_DefaultsToJSONForNonTerminal asserts that without env
// overrides, a non-terminal stdout (the test process is one) resolves
// to mode=json so existing log-aggregation pipelines stay unaffected.
func TestResolveMode_DefaultsToJSONForNonTerminal(t *testing.T) {
	t.Setenv("MISTER_GROOVY_LOG_FORMAT", "")
	t.Setenv("NO_COLOR", "")
	mode, color := resolveMode(false)
	if mode != logModeJSON {
		t.Errorf("expected json on non-terminal; got %v", mode)
	}
	if color {
		t.Error("expected no color in json mode")
	}
}

// TestResolveMode_AutoOnTerminal asserts that when stdout is a terminal,
// auto resolves to text+color (no NO_COLOR set).
func TestResolveMode_AutoOnTerminal(t *testing.T) {
	t.Setenv("MISTER_GROOVY_LOG_FORMAT", "auto")
	t.Setenv("NO_COLOR", "")
	mode, color := resolveMode(true)
	if mode != logModeText {
		t.Errorf("expected text on terminal; got %v", mode)
	}
	if !color {
		t.Error("expected color on terminal without NO_COLOR")
	}
}

// TestResolveMode_NoColor asserts NO_COLOR forces color off in text mode.
func TestResolveMode_NoColor(t *testing.T) {
	t.Setenv("MISTER_GROOVY_LOG_FORMAT", "text")
	t.Setenv("NO_COLOR", "1")
	mode, color := resolveMode(true)
	if mode != logModeText || color {
		t.Errorf("text+NO_COLOR should be text+nocolor; got %v color=%v", mode, color)
	}
}

// TestResolveMode_TextPlain asserts text-plain is text without color
// regardless of NO_COLOR / terminal state.
func TestResolveMode_TextPlain(t *testing.T) {
	t.Setenv("MISTER_GROOVY_LOG_FORMAT", "text-plain")
	t.Setenv("NO_COLOR", "")
	mode, color := resolveMode(true)
	if mode != logModeText || color {
		t.Errorf("text-plain should be text+nocolor; got %v color=%v", mode, color)
	}
}

// TestResolveMode_ExplicitJSON asserts explicit json wins regardless
// of TTY state.
func TestResolveMode_ExplicitJSON(t *testing.T) {
	t.Setenv("MISTER_GROOVY_LOG_FORMAT", "json")
	t.Setenv("NO_COLOR", "")
	mode, color := resolveMode(true)
	if mode != logModeJSON || color {
		t.Errorf("explicit json should be json+nocolor; got %v color=%v", mode, color)
	}
}

// TestResolveMode_ExplicitText asserts explicit text on a non-terminal
// still picks text (operator opt-in).
func TestResolveMode_ExplicitText(t *testing.T) {
	t.Setenv("MISTER_GROOVY_LOG_FORMAT", "text")
	t.Setenv("NO_COLOR", "")
	mode, color := resolveMode(false)
	if mode != logModeText {
		t.Errorf("explicit text should win; got %v", mode)
	}
	// color stays off because stdout is not a terminal — protects
	// redirected files from getting ANSI escapes burned in.
	if color {
		t.Error("color should be off on non-terminal even with explicit text")
	}
}
