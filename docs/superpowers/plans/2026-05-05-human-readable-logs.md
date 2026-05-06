# Human-Readable Logs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a TTY-aware text slog handler, a startup banner advertising the Web UI URL, and a Windows console-window pause gate, so binary users (especially Windows double-clickers) get readable output and discover the Web UI — while Docker / journald / piped output keeps its current strict-JSON shape.

**Architecture:** A custom `slog.Handler` selected at `logging.New()` time based on `MISTER_GROOVY_LOG_FORMAT` env + `term.IsTerminal(stdout)`. The existing `slog.JSONHandler` stays the default for non-terminal output. A separate banner subsystem in `cmd/mister-groovy-relay/banner.go` prints the Web UI URL via `fmt.Fprintln` (not slog) and only when the resolved log mode is `text`. Eleven `os.Exit(1)` sites in `main.go` get wrapped in a `dieFriendly` helper. `net.Listen` runs synchronously on the main goroutine before the banner prints, eliminating the listener-readiness race.

**Tech Stack:** Go 1.26.2, `log/slog`, `golang.org/x/term` (already direct dep), `golang.org/x/sys/windows` (already direct dep, used only by build-tagged Windows VT helper).

**Spec:** [docs/superpowers/specs/2026-05-05-human-readable-logs-design.md](../specs/2026-05-05-human-readable-logs-design.md)

---

## Files

**Create:**
- `internal/logging/text_handler.go` — TextHandler implementing `slog.Handler`
- `internal/logging/text_handler_test.go` — golden-string + behavior tests
- `internal/logging/logging_vt_windows.go` — `enableWindowsVT()` calling `SetConsoleMode(VT_PROCESSING)`
- `internal/logging/logging_vt_other.go` — no-op `enableWindowsVT()` for non-Windows
- `cmd/mister-groovy-relay/banner.go` — `printGreeting`, `firstRunMessage`, `dieFriendly`, `waitForEnterOnWindows`
- `cmd/mister-groovy-relay/banner_test.go` — banner content + suppression tests
- `tests/integration/strict_json_test.go` — regression test piping bridge stdout through `json.Decoder`

**Modify:**
- `internal/logging/logging.go` — add mode/color resolution, `pickHandler`
- `cmd/mister-groovy-relay/main.go` — listen-then-serve pattern, `dieFriendly` at 11 sites, greeter call, first-run banner
- `README.md` — env var documentation

---

## Conventions for this plan

- **Working directory:** `c:\Users\Jake\Git\MiSTer_GroovyRelay`. All paths below are relative to it.
- **Shell:** PowerShell (commands shown in PowerShell-friendly form). Bash also works.
- **Test invocation:** Use `go test` from repo root. Race detector via `-race`. Single test selection via `-run TestName`.
- **`t.Setenv`:** Used to set env vars in tests; Go automatically restores on cleanup. Don't mix with `os.Setenv`.
- **Existing test pattern:** `internal/logging/logging_test.go` swaps `newHandlerWriter` to a `bytes.Buffer` to capture handler output. Reuse that pattern.
- **Commit style:** Use the conventional-commits-ish prefix this repo uses (`feat`, `fix`, `docs`, `test`, `refactor`). Recent examples: `docs(dlna): tighten...`, `feat(extension): add popup...`. Keep subject under 70 chars.
- **No emojis in source files.**
- **`replace_all` in Edit:** Don't use it unless you need to rename a symbol everywhere — usually you want a single targeted edit.

---

## Task 1: TextHandler skeleton — emits one line per record

**Files:**
- Create: `internal/logging/text_handler.go`
- Create: `internal/logging/text_handler_test.go`

This task gives us a TextHandler that emits a single line per record with a fixed-width level tag, second-precision time, message, and flat key=value attrs. No color yet, no rewrite table yet, no group support yet. Just the skeleton.

- [ ] **Step 1: Write the failing test**

Create `internal/logging/text_handler_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/logging/ -run TestTextHandler -v`
Expected: FAIL — `newTextHandler`, `textOptions` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/logging/text_handler.go`:

```go
// Package logging — TextHandler implements a human-readable slog.Handler
// for interactive terminal use. It produces one line per record:
//
//	HH:MM:SS LVL  Message  key=value key=value
//
// Selection between TextHandler and the stdlib JSONHandler happens in
// logging.New based on MISTER_GROOVY_LOG_FORMAT and isatty(stdout). See
// docs/superpowers/specs/2026-05-05-human-readable-logs-design.md.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

// textOptions configures TextHandler at construction time. Fields are
// resolved once in pickHandler and not mutated thereafter.
type textOptions struct {
	color bool
}

// TextHandler implements slog.Handler with a single-line human format.
// Concurrent Handle calls serialize through mu so two records can't
// interleave bytes on stdout.
type TextHandler struct {
	w     io.Writer
	level *slog.LevelVar
	opts  textOptions
	mu    *sync.Mutex
}

func newTextHandler(w io.Writer, level *slog.LevelVar, opts textOptions) *TextHandler {
	return &TextHandler{w: w, level: level, opts: opts, mu: &sync.Mutex{}}
}

func (h *TextHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *TextHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Time.Format("15:04:05"))
	b.WriteString(" ")
	b.WriteString(levelTag(r.Level))
	b.WriteString("  ")
	b.WriteString(humanizeMessage(r.Message))

	r.Attrs(func(a slog.Attr) bool {
		b.WriteString("  ")
		b.WriteString(a.Key)
		b.WriteString("=")
		b.WriteString(a.Value.String())
		return true
	})

	b.WriteString("\n")

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := fmt.Fprint(h.w, b.String())
	return err
}

// WithAttrs and WithGroup are filled in by Task 3.
func (h *TextHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *TextHandler) WithGroup(name string) slog.Handler       { return h }

// levelTag returns a fixed 4-char tag per slog level.
func levelTag(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return " ERR"
	case l >= slog.LevelWarn:
		return "WARN"
	case l >= slog.LevelInfo:
		return " ok "
	default:
		return "dbg "
	}
}

// humanizeMessage capitalizes the first letter of the supplied message.
// Task 5 extends this with the rewrite table.
func humanizeMessage(msg string) string {
	if msg == "" {
		return msg
	}
	return strings.ToUpper(msg[:1]) + msg[1:]
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/logging/ -run TestTextHandler -v`
Expected: PASS — three tests.

- [ ] **Step 5: Run full logging package + vet**

Run: `go vet ./internal/logging/... ; go test ./internal/logging/... -race`
Expected: PASS — both existing JSON tests and new text tests.

- [ ] **Step 6: Commit**

```powershell
git add internal/logging/text_handler.go internal/logging/text_handler_test.go
git commit -m "feat(logging): add TextHandler skeleton"
```

---

## Task 2: TextHandler — color support

**Files:**
- Modify: `internal/logging/text_handler.go`
- Modify: `internal/logging/text_handler_test.go`

ANSI color escapes around level tag, message, and `err=` values, gated on `textOptions.color`. No state machine, just inline escape strings.

- [ ] **Step 1: Write the failing test**

Append to `internal/logging/text_handler_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/logging/ -run TestTextHandler_Color -v`
Expected: FAIL — color test sees no escapes.

- [ ] **Step 3: Implement color helpers and apply them**

Replace the existing `Handle` method and add helpers in `internal/logging/text_handler.go`:

```go
// ANSI escape constants. We use bare strings instead of importing a
// terminal-styling library so the binary stays dependency-light. The
// stylize helper short-circuits when color is disabled.
const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiYellw = "\x1b[33m"
)

// stylize wraps s in code...reset when on is true; otherwise returns s.
func stylize(on bool, code, s string) string {
	if !on || s == "" {
		return s
	}
	return code + s + ansiReset
}

func levelColor(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return ansiRed
	case l >= slog.LevelWarn:
		return ansiYellw
	case l >= slog.LevelInfo:
		return ansiGreen
	default:
		return ansiDim
	}
}

func (h *TextHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(stylize(h.opts.color, ansiDim, r.Time.Format("15:04:05")))
	b.WriteString(" ")
	b.WriteString(stylize(h.opts.color, levelColor(r.Level), levelTag(r.Level)))
	b.WriteString("  ")
	b.WriteString(stylize(h.opts.color, ansiBold, humanizeMessage(r.Message)))

	r.Attrs(func(a slog.Attr) bool {
		b.WriteString("  ")
		b.WriteString(stylize(h.opts.color, ansiDim, a.Key))
		b.WriteString("=")
		val := a.Value.String()
		if a.Key == "err" {
			val = stylize(h.opts.color, ansiRed, val)
		}
		b.WriteString(val)
		return true
	})

	b.WriteString("\n")

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := fmt.Fprint(h.w, b.String())
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/logging/ -run TestTextHandler -v`
Expected: PASS — all five Text tests.

- [ ] **Step 5: Commit**

```powershell
git add internal/logging/text_handler.go internal/logging/text_handler_test.go
git commit -m "feat(logging): add ANSI color to TextHandler"
```

---

## Task 3: TextHandler — WithAttrs / WithGroup parity

**Files:**
- Modify: `internal/logging/text_handler.go`
- Modify: `internal/logging/text_handler_test.go`

`slog.Logger.With` calls `Handler.WithAttrs`; `slog.Logger.WithGroup` calls `WithGroup`. Most call sites in this repo pass flat attrs, but this gives parity with the JSON handler so future refactors don't surprise anyone.

- [ ] **Step 1: Write the failing test**

Append to `internal/logging/text_handler_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/logging/ -run "TestTextHandler_With" -v`
Expected: FAIL — current `WithAttrs` returns receiver unchanged.

- [ ] **Step 3: Implement attrs/group propagation**

In `text_handler.go`, add the `presetAttr` type and replace the existing `TextHandler` struct + constructor + `WithAttrs` + `WithGroup`:

```go
// presetAttr is an attr accumulated via With/WithGroup. Stored
// already-prefixed (i.e. group dots applied at With-time) so the hot
// Handle path doesn't have to rebuild strings per record.
type presetAttr struct {
	key   string
	value slog.Value
}

type TextHandler struct {
	w      io.Writer
	level  *slog.LevelVar
	opts   textOptions
	mu     *sync.Mutex
	preset []presetAttr
	prefix string // dot-joined group path, e.g. "net." or "net.tcp."
}

func newTextHandler(w io.Writer, level *slog.LevelVar, opts textOptions) *TextHandler {
	return &TextHandler{w: w, level: level, opts: opts, mu: &sync.Mutex{}}
}

func (h *TextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	clone := *h
	clone.preset = append([]presetAttr{}, h.preset...)
	for _, a := range attrs {
		clone.preset = append(clone.preset, presetAttr{key: h.prefix + a.Key, value: a.Value})
	}
	return &clone
}

func (h *TextHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.prefix = h.prefix + name + "."
	return &clone
}
```

And update the `Handle` method to render preset attrs first. Replace the per-record attrs block in `Handle` with:

```go
	for _, a := range h.preset {
		b.WriteString("  ")
		b.WriteString(stylize(h.opts.color, ansiDim, a.key))
		b.WriteString("=")
		val := a.value.String()
		if a.key == "err" || strings.HasSuffix(a.key, ".err") {
			val = stylize(h.opts.color, ansiRed, val)
		}
		b.WriteString(val)
	}

	r.Attrs(func(a slog.Attr) bool {
		key := h.prefix + a.Key
		b.WriteString("  ")
		b.WriteString(stylize(h.opts.color, ansiDim, key))
		b.WriteString("=")
		val := a.Value.String()
		if a.Key == "err" || strings.HasSuffix(key, ".err") {
			val = stylize(h.opts.color, ansiRed, val)
		}
		b.WriteString(val)
		return true
	})
```

Also delete the helper struct `TextHandlerState` block (it was scaffolding only).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/logging/ -race -v`
Expected: PASS — all logging tests.

- [ ] **Step 5: Commit**

```powershell
git add internal/logging/text_handler.go internal/logging/text_handler_test.go
git commit -m "feat(logging): add WithAttrs/WithGroup parity to TextHandler"
```

---

## Task 4: TextHandler — multi-line value safety

**Files:**
- Modify: `internal/logging/text_handler.go`
- Modify: `internal/logging/text_handler_test.go`

A value that contains newlines must not break the one-record-per-line invariant. Replace `\n` and `\r` in values with literal `\n` / `\r` escape strings.

- [ ] **Step 1: Write the failing test**

Append to `internal/logging/text_handler_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/logging/ -run TestTextHandler_NoNewlines -v`
Expected: FAIL — raw newlines pass through.

- [ ] **Step 3: Implement value sanitization**

Add to `text_handler.go`:

```go
// safeValue replaces newlines and carriage returns with literal escape
// strings so a multi-line value can't break the one-record-per-line
// invariant. Other control bytes are passed through — slog values are
// already string-formatted by the time we see them.
func safeValue(s string) string {
	if !strings.ContainsAny(s, "\n\r") {
		return s
	}
	r := strings.NewReplacer("\n", `\n`, "\r", `\r`)
	return r.Replace(s)
}
```

In `Handle`, replace both occurrences of `val := a.value.String()` / `val := a.Value.String()` with sanitized variants:

For preset attrs:
```go
		val := safeValue(a.value.String())
```

For per-record attrs:
```go
		val := safeValue(a.Value.String())
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/logging/ -race -v`
Expected: PASS — all logging tests.

- [ ] **Step 5: Commit**

```powershell
git add internal/logging/text_handler.go internal/logging/text_handler_test.go
git commit -m "feat(logging): escape newlines in TextHandler values"
```

---

## Task 5: TextHandler — message rewrite table

**Files:**
- Modify: `internal/logging/text_handler.go`
- Modify: `internal/logging/text_handler_test.go`

A small lookup table maps frequently-emitted internal messages to friendlier copy. Anything not in the table falls through to the existing capitalize-first-letter behavior. Spec table from the design doc, verbatim.

- [ ] **Step 1: Write the failing test**

Append to `internal/logging/text_handler_test.go`:

```go
// TestTextHandler_MessageRewrite_Table asserts the seed entries in the
// rewrite table render the friendly copy instead of the raw internal
// message. Verifies the path that fires on every cast.
func TestTextHandler_MessageRewrite_Table(t *testing.T) {
	cases := []struct {
		raw, friendly string
	}{
		{"listening", "Web UI ready"},
		{"shutting down", "Shutting down..."},
		{"adapter disabled", "Adapter disabled"},
		{"preempting prior session for new request", "Switching to new cast"},
		{"dataplane session started", "Cast started"},
		{"dataplane session ended", "Cast ended"},
		{"GDM discovery active", "Plex discovery active"},
		{"plex.tv device registration loop started", "Plex registration active"},
		{"plex.tv registration skipped (no auth token; run with --link)", "Plex not linked yet — open the Web UI to link"},
		{"host_ip not set; auto-detected via default route — override in config for multi-NIC hosts", "Auto-detected LAN IP"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		h := newTextHandler(&buf, &slog.LevelVar{}, textOptions{color: false})
		rec := slog.NewRecord(fixedTime(), slog.LevelInfo, c.raw, 0)
		if err := h.Handle(t.Context(), rec); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		if !strings.Contains(buf.String(), c.friendly) {
			t.Errorf("rewrite missing for %q -> %q; got %q", c.raw, c.friendly, buf.String())
		}
	}
}

// TestTextHandler_MessageRewrite_Fallback asserts an untable message
// falls through to capitalize-first-letter.
func TestTextHandler_MessageRewrite_Fallback(t *testing.T) {
	var buf bytes.Buffer
	h := newTextHandler(&buf, &slog.LevelVar{}, textOptions{color: false})
	rec := slog.NewRecord(fixedTime(), slog.LevelInfo, "ffmpeg started", 0)
	if err := h.Handle(t.Context(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(buf.String(), "Ffmpeg started") {
		t.Errorf("expected capitalized fallback; got %q", buf.String())
	}
}

// TestJSONHandler_MessageNotRewritten asserts the rewrite table does
// not affect the stdlib JSONHandler — JSON consumers depend on the raw
// "msg" field staying stable for grep / aggregation.
func TestJSONHandler_MessageNotRewritten(t *testing.T) {
	var buf bytes.Buffer
	original := newHandlerWriter
	t.Cleanup(func() { newHandlerWriter = original })
	newHandlerWriter = &buf

	t.Setenv("MISTER_GROOVY_LOG_FORMAT", "json")
	logger := New("info")
	logger.Info("listening", "addr", ":32500")

	if !strings.Contains(buf.String(), `"msg":"listening"`) {
		t.Errorf("JSON msg field should preserve raw text; got %q", buf.String())
	}
	if strings.Contains(buf.String(), "Web UI ready") {
		t.Errorf("rewrite leaked into JSON output; got %q", buf.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/logging/ -run "TestTextHandler_MessageRewrite|TestJSONHandler_MessageNotRewritten" -v`
Expected: FAIL on the two TextHandler tests (table lookups undefined). The `TestJSONHandler_MessageNotRewritten` test will *pass* even at this point because `New("info")` currently returns the JSON handler unconditionally, and the JSON handler never invokes the rewrite table — that test is forward-compatible with the Task 6 mode resolution. Leave it active.

- [ ] **Step 3: Implement the rewrite table**

In `text_handler.go`, replace `humanizeMessage` with:

```go
// messageRewrites maps internal slog message strings to the friendlier
// text emitted by TextHandler. Applies only to text output; the JSON
// handler keeps the raw msg field unchanged so log aggregation pipelines
// that grep msg keep working. Grow additively over time — each entry
// here is a quiet contract with downstream operators reading the
// console, not a code dependency.
var messageRewrites = map[string]string{
	"listening":                                                "Web UI ready",
	"shutting down":                                            "Shutting down...",
	"adapter disabled":                                         "Adapter disabled",
	"preempting prior session for new request":                 "Switching to new cast",
	"dataplane session started":                                "Cast started",
	"dataplane session ended":                                  "Cast ended",
	"GDM discovery active":                                     "Plex discovery active",
	"plex.tv device registration loop started":                 "Plex registration active",
	"plex.tv registration skipped (no auth token; run with --link)": "Plex not linked yet — open the Web UI to link",
	"host_ip not set; auto-detected via default route — override in config for multi-NIC hosts": "Auto-detected LAN IP",
}

// humanizeMessage looks up msg in messageRewrites; on miss it returns
// msg with the first letter uppercased. Empty msg passes through.
func humanizeMessage(msg string) string {
	if msg == "" {
		return msg
	}
	if friendly, ok := messageRewrites[msg]; ok {
		return friendly
	}
	return strings.ToUpper(msg[:1]) + msg[1:]
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/logging/ -run TestTextHandler_MessageRewrite -v`
Expected: PASS — table + fallback tests.

- [ ] **Step 5: Run full logging package**

Run: `go test ./internal/logging/... -race`
Expected: PASS — all tests.

- [ ] **Step 6: Commit**

```powershell
git add internal/logging/text_handler.go internal/logging/text_handler_test.go
git commit -m "feat(logging): add message rewrite table to TextHandler"
```

---

## Task 6: Mode/color resolution + handler selection in `logging.New`

**Files:**
- Modify: `internal/logging/logging.go`
- Modify: `internal/logging/text_handler_test.go`
- Modify: `internal/logging/logging_test.go` (add tests)

Replace `New` with logic that resolves a `mode` (text|json) and `color` (bool) from `MISTER_GROOVY_LOG_FORMAT`, `NO_COLOR`, and `term.IsTerminal(stdout)`, then constructs the matching handler. JSON path stays default for non-terminals — every existing test continues to see JSON.

- [ ] **Step 1: Add resolution tests to `internal/logging/logging_test.go`**

Append to `internal/logging/logging_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/logging/ -run "TestResolveMode|TestJSONHandler_MessageNotRewritten" -v`
Expected: FAIL — `resolveMode`, `logModeText`, `logModeJSON` undefined.

- [ ] **Step 3: Replace `logging.go` with mode-resolving version**

Replace the contents of `internal/logging/logging.go` with:

```go
// Package logging owns the bridge's slog handler and its mutable level.
// The level lives in a package-level slog.LevelVar so callers can flip
// the threshold at runtime — specifically, the bridge UI's "Debug
// Logging" checkbox calls SetLevel without re-creating the default
// logger.
//
// New picks one of two handlers at startup:
//   - TextHandler (this package) when stdout is a terminal or
//     MISTER_GROOVY_LOG_FORMAT requests text/text-plain.
//   - slog.JSONHandler otherwise (Docker, journald, redirected stdout,
//     CI). JSON mode is the safe default — existing log aggregation
//     pipelines keep working unchanged.
//
// See docs/superpowers/specs/2026-05-05-human-readable-logs-design.md.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"golang.org/x/term"
)

// logMode is the resolved output mode.
type logMode int

const (
	logModeText logMode = iota
	logModeJSON
)

// levelVar is the single source of truth for the active log threshold.
var levelVar slog.LevelVar

// newHandlerWriter is the io.Writer the handler pipes records into.
// Production points at os.Stdout; tests overwrite this with a
// bytes.Buffer to assert what reaches the handler at a given level.
var newHandlerWriter io.Writer = os.Stdout

// stdoutIsTerminal is the function that probes whether the output
// stream is a terminal. Production wires it to term.IsTerminal on
// os.Stdout's fd; tests can override.
var stdoutIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// New constructs the bridge's slog.Logger. mode and color are resolved
// from env + isatty(stdout); see resolveMode for the rules.
func New(level string) *slog.Logger {
	levelVar.Set(parseLevel(level))
	mode, color := resolveMode(stdoutIsTerminal())

	if mode == logModeText {
		// On Windows, enable VT processing so ANSI escapes render
		// correctly in legacy cmd.exe spawned by double-clicking the
		// .exe. No-op on other platforms.
		enableWindowsVT()
	}

	h := pickHandler(newHandlerWriter, mode, color)
	return slog.New(h)
}

// pickHandler constructs the configured handler. Split out so tests
// can exercise it with a buffer.
func pickHandler(w io.Writer, mode logMode, color bool) slog.Handler {
	if mode == logModeText {
		return newTextHandler(w, &levelVar, textOptions{color: color})
	}
	return slog.NewJSONHandler(w, &slog.HandlerOptions{Level: &levelVar})
}

// resolveMode returns the (mode, color) pair from env + isTerminal.
// Rules (in priority order):
//  1. MISTER_GROOVY_LOG_FORMAT explicit value: json | text | text-plain
//     all win regardless of TTY. auto + non-TTY -> json. auto + TTY ->
//     text. text-plain is text with color off.
//  2. color starts true if mode resolves to text. Forced off when
//     mode is json, NO_COLOR is set (any non-empty), text-plain is
//     selected, or stdout is not a terminal.
func resolveMode(isTerminal bool) (logMode, bool) {
	format := strings.ToLower(strings.TrimSpace(os.Getenv("MISTER_GROOVY_LOG_FORMAT")))
	if format == "" {
		format = "auto"
	}

	var mode logMode
	color := false
	switch format {
	case "json":
		mode = logModeJSON
	case "text":
		mode = logModeText
		color = isTerminal && os.Getenv("NO_COLOR") == ""
	case "text-plain":
		mode = logModeText
		color = false
	default: // auto + anything unrecognized
		if isTerminal {
			mode = logModeText
			color = os.Getenv("NO_COLOR") == ""
		} else {
			mode = logModeJSON
		}
	}
	return mode, color
}

// SetLevel mutates the active logging threshold. Unknown strings map
// to slog.LevelInfo so a typo can't silently leave Debug on.
func SetLevel(level string) {
	levelVar.Set(parseLevel(level))
}

// parseLevel maps the bridge's string vocabulary to slog.Level. Default
// is Info — a misspelled level name should err on the quieter side.
func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
```

- [ ] **Step 4: Add a stub `enableWindowsVT` so the package compiles**

Create `internal/logging/logging_vt_other.go`:

```go
//go:build !windows

package logging

// enableWindowsVT is a no-op on non-Windows platforms. The Windows
// implementation lives in logging_vt_windows.go.
func enableWindowsVT() {}
```

Create `internal/logging/logging_vt_windows.go` (real impl comes in Task 7; for now keep it a no-op so the build stays green on Windows too):

```go
//go:build windows

package logging

// enableWindowsVT is a no-op stub. Real implementation in Task 7.
func enableWindowsVT() {}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/logging/... -race -v`
Expected: PASS — all logging tests, including resolveMode + JSONHandler-not-rewritten.

- [ ] **Step 6: Run vet on the whole repo**

Run: `go vet ./...`
Expected: PASS — no diagnostics.

- [ ] **Step 7: Commit**

```powershell
git add internal/logging/logging.go internal/logging/logging_test.go internal/logging/text_handler_test.go internal/logging/logging_vt_windows.go internal/logging/logging_vt_other.go
git commit -m "feat(logging): add mode/color resolution and handler selection"
```

---

## Task 7: Windows VT processing enablement

**Files:**
- Modify: `internal/logging/logging_vt_windows.go`

`SetConsoleMode(stdout, current | ENABLE_VIRTUAL_TERMINAL_PROCESSING)` so that legacy `cmd.exe` (the console window Windows users get when double-clicking the .exe) renders ANSI escapes correctly. Failures are silently ignored — the worst case is the user sees raw escapes, fixable with `NO_COLOR=1`.

- [ ] **Step 1: Replace the stub with the real implementation**

Replace contents of `internal/logging/logging_vt_windows.go`:

```go
//go:build windows

package logging

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableWindowsVT enables ENABLE_VIRTUAL_TERMINAL_PROCESSING on stdout
// so ANSI escapes render in legacy cmd.exe spawned by double-clicking
// the binary. Modern terminals (Windows Terminal, recent PowerShell)
// inherit it; legacy hosts do not until the process opts in.
//
// Failures are intentionally swallowed: if SetConsoleMode rejects the
// flag (very old Windows, redirected handle, non-console writer), the
// worst outcome is the user sees raw escape bytes — recoverable with
// NO_COLOR=1 or MISTER_GROOVY_LOG_FORMAT=text-plain.
func enableWindowsVT() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return
	}
	_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}
```

- [ ] **Step 2: Verify it builds on Windows**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 3: Run logging tests**

Run: `go test ./internal/logging/... -race`
Expected: PASS — `enableWindowsVT` is invoked indirectly by `New("info")` calls during tests (no assertions about VT, but the syscall must not panic).

- [ ] **Step 4: Manual smoke (optional but recommended)**

Run: `go run ./cmd/mister-groovy-relay --help` from a fresh `cmd.exe` window. The flag-parsing usage should print without raw `\x1b[` bytes visible.
Expected: clean help text.

- [ ] **Step 5: Commit**

```powershell
git add internal/logging/logging_vt_windows.go
git commit -m "feat(logging): enable VT processing on Windows console"
```

---

## Task 8: Greeter — `printGreeting`

**Files:**
- Create: `cmd/mister-groovy-relay/banner.go`
- Create: `cmd/mister-groovy-relay/banner_test.go`

`printGreeting` constructs the multi-line banner with the resolved Web UI URL(s) and adapter status. Suppression rules (text mode default on, JSON mode default off, env overrides) live here.

- [ ] **Step 1: Write the failing test**

Create `cmd/mister-groovy-relay/banner_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/mister-groovy-relay/ -v`
Expected: FAIL — symbols undefined.

- [ ] **Step 3: Implement the greeter**

Create `cmd/mister-groovy-relay/banner.go`:

```go
// Console banner + first-run / fatal-exit helpers. See
// docs/superpowers/specs/2026-05-05-human-readable-logs-design.md.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// adapterStatus is the flat shape printGreeting needs from each adapter.
// Building this slice in main lets banner.go stay small and testable.
type adapterStatus struct {
	display string
	enabled bool
}

// printGreeting writes the startup banner to os.Stdout when appropriate.
// textMode is the resolved log mode from internal/logging. Suppression
// rules (in priority order):
//  1. MISTER_GROOVY_NO_BANNER=1: never print.
//  2. textMode=false (JSON mode): print only if MISTER_GROOVY_BANNER=1.
//  3. textMode=true: always print (subject to rule 1).
func printGreeting(textMode bool, version, hostIP string, port int, reg *adapters.Registry) {
	statuses := make([]adapterStatus, 0)
	if reg != nil {
		for _, a := range reg.List() {
			statuses = append(statuses, adapterStatus{
				display: a.DisplayName() + " adapter",
				enabled: a.IsEnabled(),
			})
		}
	}
	printGreetingTo(os.Stdout, textMode, version, hostIP, port, statuses)
}

// printGreetingTo is the testable form: writer + already-flattened
// adapter list, no Registry coupling.
func printGreetingTo(w io.Writer, textMode bool, version, hostIP string, port int, statuses []adapterStatus) {
	if os.Getenv("MISTER_GROOVY_NO_BANNER") == "1" {
		return
	}
	if !textMode && os.Getenv("MISTER_GROOVY_BANNER") != "1" {
		return
	}
	fmt.Fprint(w, greetingFor(version, hostIP, port, statuses))
}

// greetingFor renders the banner string. Pure function — no I/O.
func greetingFor(version, hostIP string, port int, statuses []adapterStatus) string {
	var b strings.Builder
	const rule = "================================================================"
	const thin = "----------------------------------------------------------------"

	b.WriteString(rule + "\n")
	b.WriteString("  MiSTer GroovyRelay  v" + version + "\n")
	b.WriteString(rule + "\n\n")

	b.WriteString("  Web UI:  ")
	if hostIP != "" {
		fmt.Fprintf(&b, "http://%s:%d\n", hostIP, port)
		fmt.Fprintf(&b, "           http://localhost:%d   (this machine)\n", port)
	} else {
		fmt.Fprintf(&b, "http://localhost:%d\n", port)
	}
	b.WriteString("\n")

	if len(statuses) > 0 {
		b.WriteString("  Status:")
		first := true
		for _, s := range statuses {
			state := "enabled"
			if !s.enabled {
				state = "disabled"
			}
			n := 18 - len(s.display)
			if n < 2 {
				n = 2
			}
			pad := strings.Repeat(" ", n)
			if first {
				fmt.Fprintf(&b, "  %s%s%s\n", s.display, pad, state)
				first = false
			} else {
				fmt.Fprintf(&b, "           %s%s%s\n", s.display, pad, state)
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("  Next:    Open the Web UI in your browser to link Plex/Jellyfin\n")
	b.WriteString("           and confirm your MiSTer host is reachable.\n\n")

	b.WriteString("  Logs:    Detailed activity will appear below.\n")
	b.WriteString("           Press Ctrl-C to quit.\n\n")

	b.WriteString(thin + "\n")
	return b.String()
}

// dieFriendly wraps a fatal startup error with a friendly console
// message and (on Windows) a "Press Enter to close" pause so the user
// can read it before the console window slams shut. Always exits with
// code 1.
//
// Wired in main() at the 11 sites listed in the design spec — every
// os.Exit(1) reachable before httpSrv.Serve(ln). Extra structured
// attrs follow the slog convention (key, value, key, value, ...) and
// are emitted with the slog.Error record so the JSON stream keeps the
// per-site context (e.g. adapter name) the prior code carried.
func dieFriendly(title string, err error, attrs ...any) {
	args := append([]any{"err", err}, attrs...)
	slog.Error(title, args...)
	fmt.Fprintf(os.Stderr, "\nError: %s.\n  %v\n", title, err)
	waitForEnterOnWindows()
	os.Exit(1)
}

// firstRunMessage prints the friendly first-run banner when the bridge
// auto-creates a default config. Replaces the existing one-line stderr
// message in main.go.
func firstRunMessage(path string) {
	fmt.Fprintln(os.Stderr, "================================================================")
	fmt.Fprintln(os.Stderr, "  MiSTer GroovyRelay  --  First-run setup")
	fmt.Fprintln(os.Stderr, "================================================================")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  A default config was written to:")
	fmt.Fprintf(os.Stderr, "    %s\n", path)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  Next steps:")
	fmt.Fprintln(os.Stderr, "    1. Open that file in a text editor.")
	fmt.Fprintln(os.Stderr, "    2. Set bridge.mister.host to your MiSTer's IP address.")
	fmt.Fprintln(os.Stderr, "    3. Re-launch this app.")
	fmt.Fprintln(os.Stderr)
}

// waitForEnterOnWindows is filled in by Task 9 (build-tag split).
```

- [ ] **Step 4: Add stub `waitForEnterOnWindows` so the package compiles**

Create `cmd/mister-groovy-relay/banner_pause_other.go`:

```go
//go:build !windows

package main

// waitForEnterOnWindows is a no-op on non-Windows platforms.
func waitForEnterOnWindows() {}
```

Create `cmd/mister-groovy-relay/banner_pause_windows.go`:

```go
//go:build windows

package main

// waitForEnterOnWindows is a stub; real implementation lands in Task 9.
func waitForEnterOnWindows() {}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/mister-groovy-relay/ -v`
Expected: PASS — all five greeter tests.

- [ ] **Step 6: Commit**

```powershell
git add cmd/mister-groovy-relay/banner.go cmd/mister-groovy-relay/banner_test.go cmd/mister-groovy-relay/banner_pause_windows.go cmd/mister-groovy-relay/banner_pause_other.go
git commit -m "feat(cli): add startup banner with Web UI URL"
```

---

## Task 9: Windows pause gate

**Files:**
- Modify: `cmd/mister-groovy-relay/banner_pause_windows.go`

Real implementation: print "Press Enter to close this window." and read a line from stdin, but only when stdin AND stdout are both terminals AND `MISTER_GROOVY_NO_PAUSE` is not set.

- [ ] **Step 1: Replace the stub**

Replace `cmd/mister-groovy-relay/banner_pause_windows.go`:

```go
//go:build windows

package main

import (
	"bufio"
	"fmt"
	"os"

	"golang.org/x/term"
)

// waitForEnterOnWindows blocks until the user presses Enter. Used on
// fatal-error paths so the console window stays visible long enough to
// read the message when the user double-clicked the binary. No-op when:
//   - MISTER_GROOVY_NO_PAUSE=1 (operator opt-out).
//   - stdin is not a terminal (Docker, CI, headless service, redirected
//     stdin — would deadlock or read garbage otherwise).
//   - stdout is not a terminal (the message went to a file the user is
//     reading separately; no console to keep open).
func waitForEnterOnWindows() {
	if os.Getenv("MISTER_GROOVY_NO_PAUSE") == "1" {
		return
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}
	fmt.Fprintln(os.Stderr, "  Press Enter to close this window.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
```

- [ ] **Step 2: Build verification**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 3: Test the existing tests still pass**

Run: `go test ./cmd/mister-groovy-relay/ -v`
Expected: PASS — banner tests don't invoke the pause gate (they call `printGreeting`, not `dieFriendly`).

Note: We don't unit-test `waitForEnterOnWindows` directly. The opt-out conditions all return early without I/O, and the actually-blocks-on-stdin path is intentionally interactive — a manual smoke at deploy time covers it.

- [ ] **Step 4: Commit**

```powershell
git add cmd/mister-groovy-relay/banner_pause_windows.go
git commit -m "feat(cli): add Windows console pause gate for fatal errors"
```

---

## Task 10: Wire main.go — listen-then-serve, dieFriendly, greeter, first-run

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`

Replace `ListenAndServe` in goroutine with `net.Listen` on main + `Serve` in goroutine. Wrap the 11 listed `slog.Error; os.Exit(1)` blocks with `dieFriendly()`. Replace the first-run stderr line with `firstRunMessage()`. Call `printGreeting()` after the listener is bound.

- [ ] **Step 1: Read main.go to confirm line numbers haven't drifted**

Read `cmd/mister-groovy-relay/main.go`. Locate the 11 sites by their `slog.Error("<title>", ...)` call rather than by line number, since prior commits may have shifted line numbers. Use the title strings listed in Step 3 (`load config`, `data_dir preflight`, etc.) to find each call site.

- [ ] **Step 2: Apply the first-run message replacement**

In `cmd/mister-groovy-relay/main.go`, find the existing block:

```go
		var created *config.ErrConfigCreated
		if errors.As(err, &created) {
			fmt.Fprintf(os.Stderr,
				"No config found. Wrote defaults to %s.\nEdit it (set bridge.mister.host) and restart.\n",
				created.Path)
			os.Exit(2)
		}
```

Replace with:

```go
		var created *config.ErrConfigCreated
		if errors.As(err, &created) {
			firstRunMessage(created.Path)
			waitForEnterOnWindows()
			os.Exit(2)
		}
```

- [ ] **Step 3: Replace each fatal-exit site with `dieFriendly()`**

For each of the 11 sites, replace the two-line `slog.Error(title, ...); os.Exit(1)` with `dieFriendly(title, err, extraAttrs...)`. The 11 sites:

| Title | Extra attrs |
| --- | --- |
| `load config` | none |
| `data_dir preflight` | none |
| `save stored data` | none |
| `sender init` | none |
| `plex adapter init` | none |
| `registry register plex` | none |
| `url adapter init` | none |
| `registry register url` | none |
| `registry register jellyfin` | none |
| `adapter DecodeConfig` | `"name", a.Name()` |
| `ui init` | none |

Substitution for sites with only `err`:
```go
		// before:
		slog.Error("title", "err", err)
		os.Exit(1)
		// after:
		dieFriendly("title", err)
```

Substitution for the `adapter DecodeConfig` site (extra `name` attr):
```go
		// before:
		if err := a.DecodeConfig(raw, sec.MetaData()); err != nil {
			slog.Error("adapter DecodeConfig", "name", a.Name(), "err", err)
			os.Exit(1)
		}
		// after:
		if err := a.DecodeConfig(raw, sec.MetaData()); err != nil {
			dieFriendly("adapter DecodeConfig", err, "name", a.Name())
		}
```

`dieFriendly` is variadic — extra attrs become the trailing key/value pairs on the single `slog.Error` record, so JSON output preserves the `name` field with no double-logging.

- [ ] **Step 4: Add `IsTextMode` to logging.go (caches mode at New() time)**

The greeter needs to know whether the resolved log mode is text. We cache the resolution in a package-level var at `New()` time so `IsTextMode` returns a stable answer even if env changes after startup (a latent footgun otherwise).

In `internal/logging/logging.go`, add a package-level var below `levelVar`:

```go
// resolvedMode captures the mode picked by the most recent New() call.
// IsTextMode reads it. Cached (rather than re-resolving from env) so
// the banner gate can't drift from the actual handler choice if env
// changes between startup and greeter invocation.
var resolvedMode logMode
```

Update `New` to set it:

```go
func New(level string) *slog.Logger {
	levelVar.Set(parseLevel(level))
	mode, color := resolveMode(stdoutIsTerminal())
	resolvedMode = mode

	if mode == logModeText {
		enableWindowsVT()
	}

	h := pickHandler(newHandlerWriter, mode, color)
	return slog.New(h)
}
```

Append `IsTextMode`:

```go
// IsTextMode reports whether the most recent New() call resolved to
// text output. Used by the greeter in cmd/mister-groovy-relay to gate
// banner printing without re-implementing the env/TTY rules.
func IsTextMode() bool {
	return resolvedMode == logModeText
}
```

- [ ] **Step 5: Replace ListenAndServe with listen-then-serve and call greeter**

In `cmd/mister-groovy-relay/main.go`, find:

```go
	addr := fmt.Sprintf(":%d", sec.Bridge.UI.HTTPPort)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("listening", "addr", addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http listener", "err", err)
		}
	}()
```

Replace with:

```go
	addr := fmt.Sprintf(":%d", sec.Bridge.UI.HTTPPort)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		dieFriendly("http listener bind", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http listener", "err", err)
		}
	}()

	slog.Info("listening", "addr", addr)
```

Then, after the adapter Start loop and before `<-ctx.Done()`, insert the greeter call:

```go
	// Greeter prints once after the listener is bound and adapters are
	// started. Suppressed in JSON mode by default (see banner.go) so
	// log aggregators receive a clean strict-JSON stream.
	printGreeting(logging.IsTextMode(), version, hostIP, sec.Bridge.UI.HTTPPort, reg)
```

The `logging` import is already present in main.go.

- [ ] **Step 6: Run vet + tests**

Run: `go vet ./... ; go build ./... ; go test ./... -race`
Expected: PASS.

- [ ] **Step 7: Manual smoke**

Run: `go run ./cmd/mister-groovy-relay --help`
Expected: usage prints; no panic.

(Full bridge requires `bridge.mister.host` set, so deeper smoke needs a config; sufficient that it compiles and `--help` works.)

- [ ] **Step 8: Commit**

```powershell
git add cmd/mister-groovy-relay/main.go internal/logging/logging.go
git commit -m "feat(cli): wire human-readable logs into bridge startup"
```

---

## Task 11: Strict-JSON stdout regression test

**Files:**
- Create: `tests/integration/strict_json_test.go`

Spawn the bridge subprocess with `MISTER_GROOVY_LOG_FORMAT=json`, capture ~1s of stdout, and assert every newline-terminated line decodes via `json.Decoder`. Catches a class of future regressions where someone routes a banner or `fmt.Println` through stdout in JSON mode.

This is an integration test (build tag `integration`), matching the existing `tests/integration/` pattern.

- [ ] **Step 1: Read existing integration test for boilerplate**

Read: `tests/integration/basic_test.go` (first 60 lines).

Note the build tag (`//go:build integration`) and the helper functions for spawning subprocesses. Reuse the convention.

- [ ] **Step 2: Write the test**

Create `tests/integration/strict_json_test.go`:

```go
//go:build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestStrictJSONStdout starts the bridge with MISTER_GROOVY_LOG_FORMAT=json,
// captures ~1.5s of stdout, and asserts every line is valid JSON.
// Regression guard for the spec's "Docker / journald / piped output keeps
// strict JSON" promise — catches a future PR that accidentally
// fmt.Println's banner output to stdout regardless of mode.
func TestStrictJSONStdout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	// Build the bridge into a temp dir so we don't depend on a prior
	// `make build`. Append .exe on Windows so exec.Command can locate it.
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "mister-groovy-relay")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/mister-groovy-relay").CombinedOutput(); err != nil {
		t.Skipf("go build failed: %v\n%s", err, out)
	}

	// Pre-create a minimal valid config so the bridge boots to the
	// listener. mister.host = 127.0.0.1 + an arbitrary high port that
	// nothing listens on — the bridge doesn't validate MiSTer
	// reachability at startup. data_dir = the temp dir so no state
	// leaks into the user's home directory. http_port = 32599 because
	// internal/config.validPort rejects 0.
	dataDir := t.TempDir()
	cfgDir := t.TempDir()
	configPath := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(minimalConfig(dataDir)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "--config", configPath, "--log-level", "info")
	cmd.Env = append(os.Environ(), "MISTER_GROOVY_LOG_FORMAT=json")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// Read up to ~1.5s of output, then validate.
	var lines []string
	deadline := time.NewTimer(1500 * time.Millisecond)
	defer deadline.Stop()
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		s := bufio.NewScanner(stdout)
		for s.Scan() {
			lines = append(lines, s.Text())
		}
	}()
	select {
	case <-deadline.C:
	case <-doneCh:
	}

	if len(lines) == 0 {
		t.Fatal("no output captured from bridge stdout in 1.5s")
	}

	for i, line := range lines {
		// Skip blank lines defensively.
		if strings.TrimSpace(line) == "" {
			continue
		}
		var v map[string]any
		if err := json.NewDecoder(strings.NewReader(line)).Decode(&v); err != nil {
			t.Errorf("line %d not valid JSON: %v\nline: %q", i, err, line)
		}
	}
}

// minimalConfig returns the smallest config.toml that lets the bridge
// boot to the HTTP listener stage. mister.host can be unreachable;
// http_port must be a valid TCP port (validPort rejects 0).
func minimalConfig(dataDir string) string {
	return fmt.Sprintf(`
[bridge]
data_dir = %q

[bridge.mister]
host = "127.0.0.1"
port = 32100
source_port = 32101

[bridge.ui]
http_port = 32599

[adapters.plex]
enabled = false

[adapters.url]
enabled = false

[adapters.jellyfin]
enabled = false
`, dataDir)
}
```

- [ ] **Step 3: Run the integration test**

Run: `go test -tags=integration ./tests/integration/ -run TestStrictJSONStdout -v`
Expected: PASS.

If it fails because `http_port = 0` doesn't satisfy the config parser, change `http_port = 0` to `http_port = 32599` (a high unlikely-to-conflict port). If it fails because the bridge needs additional fields, run the bridge once with no config and copy whichever defaults the auto-created file uses, into `minimalConfig`.

- [ ] **Step 4: Run the rest of the integration suite to ensure no regression**

Run: `go test -tags=integration ./tests/integration/...`
Expected: PASS (or whatever the prior baseline is — this task did not modify any code path the other tests exercise).

- [ ] **Step 5: Commit**

```powershell
git add tests/integration/strict_json_test.go
git commit -m "test(integration): assert json mode produces strict-JSON stdout"
```

---

## Task 12: README documentation

**Files:**
- Modify: `README.md`

Add a short paragraph documenting `MISTER_GROOVY_LOG_FORMAT`, `NO_COLOR`, `MISTER_GROOVY_NO_BANNER`, `MISTER_GROOVY_BANNER`, and `MISTER_GROOVY_NO_PAUSE` near the existing config / troubleshooting sections.

- [ ] **Step 1: Find the right insertion point**

Read `README.md` and locate a section near "Native builds" or "Troubleshooting" / "Quick start" where deployment-style env vars belong. Likely after the "Native builds" section or near the bottom under a new "Console output" subsection.

- [ ] **Step 2: Insert the documentation**

Add a new section after the last config-related subsection (use exact heading style matching the rest of the README):

```markdown
## Console output

When you launch the binary directly (Windows double-click, macOS Terminal,
Linux shell), the bridge prints a friendly startup banner with the Web UI
URL and emits human-readable log lines. When stdout is piped to a file,
journald, or Docker's logging driver, the bridge instead emits structured
JSON — the same shape it has always used — so existing log-aggregation
pipelines keep working.

Override via environment variables:

| Variable | Effect |
| --- | --- |
| `MISTER_GROOVY_LOG_FORMAT` | `auto` (default), `text`, `text-plain` (text without color), or `json`. |
| `NO_COLOR` | If set, disables ANSI color in text mode. |
| `MISTER_GROOVY_NO_BANNER` | If `1`, suppresses the startup banner. |
| `MISTER_GROOVY_BANNER` | If `1`, forces the banner in JSON mode (rare). |
| `MISTER_GROOVY_NO_PAUSE` | If `1`, disables the Windows "Press Enter to close" pause on first-run / fatal-error exits. |
```

- [ ] **Step 3: Verify the README renders cleanly**

Run: `git diff README.md`
Expected: clean diff, no encoding artifacts.

- [ ] **Step 4: Commit**

```powershell
git add README.md
git commit -m "docs: document log-format and banner env vars"
```

---

## Self-review checklist (run before handing off)

- [ ] **Spec coverage:** every section in `docs/superpowers/specs/2026-05-05-human-readable-logs-design.md` maps to at least one task above. Notably:
  - Mode/color resolution → Task 6
  - TextHandler core → Tasks 1–5
  - Windows VT → Task 7
  - Banner + suppression rules → Task 8
  - Pause gate → Task 9
  - dieFriendly + listen-then-serve + greeter wiring → Task 10
  - Strict-JSON regression test → Task 11
  - README → Task 12

- [ ] **No placeholders:** every code block in this plan is a complete, copy-pasteable change.

- [ ] **Type consistency:** `textOptions`, `logMode`, `logModeText`, `logModeJSON`, `resolvedMode`, `printGreeting`, `printGreetingTo`, `greetingFor`, `adapterStatus`, `dieFriendly`, `firstRunMessage`, `waitForEnterOnWindows`, `enableWindowsVT`, `IsTextMode` — all referenced consistently across tasks.

- [ ] **Final full-suite green:** after Task 12, run:
  ```powershell
  go vet ./...
  go test ./... -race
  go test -tags=integration ./tests/integration/...
  ```
  All three must pass.
