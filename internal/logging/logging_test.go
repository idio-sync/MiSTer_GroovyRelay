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
