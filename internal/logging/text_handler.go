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
