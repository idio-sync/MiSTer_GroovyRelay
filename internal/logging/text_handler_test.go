package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// fixedTime returns a constant time used in golden-string assertions so
// tests are deterministic regardless of when they run. The handler
// reads each record's Time field, so we set it via slog.NewRecord.
func fixedTime() time.Time {
	return time.Date(2026, 5, 5, 14, 23, 1, 0, time.Local)
}

// TestTextHandler_BasicLine asserts the four-piece line shape:
// "HH:MM:SS LVL  message  k=v" — single line, no color, no rewrite.
func TestTextHandler_BasicLine(t *testing.T) {
	var buf bytes.Buffer
	h := newTextHandler(&buf, &slog.LevelVar{}, textOptions{color: false})

	rec := slog.NewRecord(fixedTime(), slog.LevelInfo, "hello", 0)
	rec.AddAttrs(slog.String("addr", ":32500"))

	if err := h.Handle(t.Context(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got := buf.String()
	if !strings.HasPrefix(got, "14:23:01 ") {
		t.Errorf("missing time prefix; got %q", got)
	}
	if !strings.Contains(got, " ok ") {
		t.Errorf("missing INFO level tag; got %q", got)
	}
	if !strings.Contains(got, "Hello") {
		t.Errorf("missing capitalized message; got %q", got)
	}
	if !strings.Contains(got, "addr=:32500") {
		t.Errorf("missing attr; got %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("missing trailing newline; got %q", got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("expected exactly one newline; got %q", got)
	}
}

// TestTextHandler_Levels asserts the level-tag mapping for all four
// slog levels. Tag width is fixed at 4 chars to keep columns aligned.
func TestTextHandler_Levels(t *testing.T) {
	cases := []struct {
		level slog.Level
		tag   string
	}{
		{slog.LevelDebug, "dbg "},
		{slog.LevelInfo, " ok "},
		{slog.LevelWarn, "WARN"},
		{slog.LevelError, " ERR"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		h := newTextHandler(&buf, &slog.LevelVar{}, textOptions{color: false})
		rec := slog.NewRecord(fixedTime(), c.level, "x", 0)
		if err := h.Handle(t.Context(), rec); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		if !strings.Contains(buf.String(), c.tag) {
			t.Errorf("level %v: tag %q not in %q", c.level, c.tag, buf.String())
		}
	}
}

// TestTextHandler_RespectsLevelVar asserts the level threshold gates
// emission the same way the JSON handler does, since the package's
// LevelVar is the single source of truth for runtime level changes.
func TestTextHandler_RespectsLevelVar(t *testing.T) {
	var lv slog.LevelVar
	lv.Set(slog.LevelInfo)

	var buf bytes.Buffer
	h := newTextHandler(&buf, &lv, textOptions{color: false})

	if h.Enabled(t.Context(), slog.LevelDebug) {
		t.Error("Debug enabled at level=Info")
	}
	if !h.Enabled(t.Context(), slog.LevelInfo) {
		t.Error("Info disabled at level=Info")
	}

	lv.Set(slog.LevelDebug)
	if !h.Enabled(t.Context(), slog.LevelDebug) {
		t.Error("Debug still disabled after SetLevel(debug)")
	}
}
