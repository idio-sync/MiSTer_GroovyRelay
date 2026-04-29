// Package logging owns the bridge's slog handler and its mutable level.
// The level lives in a package-level slog.LevelVar so callers can flip
// the threshold at runtime — specifically, the bridge UI's "Debug
// Logging" checkbox calls SetLevel without re-creating the default
// logger. New() returns a JSON handler bound to that LevelVar; every
// log emission re-reads the current level on each record, so changes
// take effect immediately for already-constructed loggers.
package logging

import (
	"io"
	"log/slog"
	"os"
)

// levelVar is the single source of truth for the active log threshold.
// New binds it to the constructed handler; SetLevel mutates it.
var levelVar slog.LevelVar

// newHandlerWriter is the io.Writer the JSON handler pipes records into.
// Production points at os.Stdout; tests overwrite this with a
// bytes.Buffer to assert what reaches the handler at a given level.
// Package-private — adapters and the rest of the bridge talk to slog,
// not to this writer.
var newHandlerWriter io.Writer = os.Stdout

// New constructs the bridge's slog.Logger with the supplied initial
// level (one of debug|info|warn|error). The level can be changed later
// via SetLevel without rebuilding the logger.
func New(level string) *slog.Logger {
	levelVar.Set(parseLevel(level))
	h := slog.NewJSONHandler(newHandlerWriter, &slog.HandlerOptions{Level: &levelVar})
	return slog.New(h)
}

// SetLevel mutates the active logging threshold. Unknown strings map
// to slog.LevelInfo so a typo can't silently leave Debug on. Callers
// driving this from the UI checkbox pass "debug" or "info"; broader
// values exist for the --log-level boot flag.
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
