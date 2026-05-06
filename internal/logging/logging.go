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

// resolvedMode captures the mode picked by the most recent New() call.
// IsTextMode reads it. Cached (rather than re-resolving from env) so
// the banner gate can't drift from the actual handler choice if env
// changes between startup and greeter invocation.
var resolvedMode logMode

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
	resolvedMode = mode

	if mode == logModeText {
		// On Windows, enable VT processing so ANSI escapes render
		// correctly in legacy cmd.exe spawned by double-clicking the
		// .exe. No-op on other platforms.
		enableWindowsVT()
	}

	h := pickHandler(newHandlerWriter, mode, color)
	return slog.New(h)
}

// IsTextMode reports whether the most recent New() call resolved to
// text output. Used by the greeter in cmd/mister-groovy-relay to gate
// banner printing without re-implementing the env/TTY rules.
func IsTextMode() bool {
	return resolvedMode == logModeText
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
