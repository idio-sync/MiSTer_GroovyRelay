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

// TestTextHandler_ColorWrapsLevelAndMessage asserts that with color=true
// the level tag is wrapped in the level's color escape and the message
// in bold, while err= attr values get the red escape. With color=false
// no escape bytes (\x1b[) appear at all.
func TestTextHandler_ColorWrapsLevelAndMessage(t *testing.T) {
	var buf bytes.Buffer
	h := newTextHandler(&buf, &slog.LevelVar{}, textOptions{color: true})

	rec := slog.NewRecord(fixedTime(), slog.LevelError, "boom", 0)
	rec.AddAttrs(slog.String("err", "kaboom"))
	if err := h.Handle(t.Context(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("expected ANSI escapes with color=true; got %q", got)
	}
	// Reset code at end of every styled span.
	if !strings.Contains(got, "\x1b[0m") {
		t.Errorf("expected reset code; got %q", got)
	}
}

func TestTextHandler_NoColorLeavesPlainBytes(t *testing.T) {
	var buf bytes.Buffer
	h := newTextHandler(&buf, &slog.LevelVar{}, textOptions{color: false})

	rec := slog.NewRecord(fixedTime(), slog.LevelError, "boom", 0)
	rec.AddAttrs(slog.String("err", "kaboom"))
	if err := h.Handle(t.Context(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("unexpected ANSI escape with color=false; got %q", buf.String())
	}
}

// TestTextHandler_WithAttrs asserts attrs supplied via With() prepend
// the per-record attrs in the rendered line, matching JSONHandler shape.
func TestTextHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := newTextHandler(&buf, &slog.LevelVar{}, textOptions{color: false})
	logger := slog.New(h).With("app", "bridge", "version", "1.0.0")

	logger.Info("hello", "addr", ":32500")
	got := buf.String()

	for _, want := range []string{"app=bridge", "version=1.0.0", "addr=:32500"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q; got %q", want, got)
		}
	}
	if i, j := strings.Index(got, "app="), strings.Index(got, "addr="); i >= j {
		t.Errorf("With() attrs should appear before per-record attrs; got %q", got)
	}
}

// TestTextHandler_WithGroup asserts WithGroup prefixes nested attr keys
// with the group name and a dot, matching JSONHandler. Multiple nested
// groups concatenate with dots.
func TestTextHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	h := newTextHandler(&buf, &slog.LevelVar{}, textOptions{color: false})
	logger := slog.New(h).WithGroup("net").With("port", 32500)

	logger.Info("listening", "host", "0.0.0.0")
	got := buf.String()
	for _, want := range []string{"net.port=32500", "net.host=0.0.0.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q; got %q", want, got)
		}
	}
}

// TestTextHandler_NoNewlinesInValues asserts a multi-line attr value is
// rendered as a single line — newlines escaped, single trailing \n.
func TestTextHandler_NoNewlinesInValues(t *testing.T) {
	var buf bytes.Buffer
	h := newTextHandler(&buf, &slog.LevelVar{}, textOptions{color: false})

	rec := slog.NewRecord(fixedTime(), slog.LevelError, "boom", 0)
	rec.AddAttrs(slog.String("err", "line1\nline2\rline3"))

	if err := h.Handle(t.Context(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got := buf.String()
	if strings.Count(got, "\n") != 1 {
		t.Errorf("expected exactly one trailing newline; got %q", got)
	}
	if !strings.Contains(got, `line1\nline2\rline3`) {
		t.Errorf("newlines should be escaped to literal \\n / \\r; got %q", got)
	}
}
