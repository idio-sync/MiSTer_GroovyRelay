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

func (h *TextHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

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

	for _, a := range h.preset {
		b.WriteString("  ")
		b.WriteString(stylize(h.opts.color, ansiDim, a.key))
		b.WriteString("=")
		val := safeValue(a.value.String())
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
		val := safeValue(a.Value.String())
		if a.Key == "err" || strings.HasSuffix(key, ".err") {
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

// messageRewrites maps internal slog message strings to the friendlier
// text emitted by TextHandler. Applies only to text output; the JSON
// handler keeps the raw msg field unchanged so log aggregation pipelines
// that grep msg keep working. Grow additively over time — each entry
// here is a quiet contract with downstream operators reading the
// console, not a code dependency.
var messageRewrites = map[string]string{
	"listening":                                "Web UI ready",
	"shutting down":                            "Shutting down...",
	"adapter disabled":                         "Adapter disabled",
	"preempting prior session for new request": "Switching to new cast",
	"dataplane session started":                "Cast started",
	"dataplane session ended":                  "Cast ended",
	"GDM discovery active":                     "Plex discovery active",
	"plex.tv device registration loop started": "Plex registration active",
	"plex.tv registration skipped (no auth token; run with --link)":                              "Plex not linked yet — open the Web UI to link",
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
