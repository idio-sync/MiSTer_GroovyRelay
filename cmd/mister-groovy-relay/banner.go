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
// per-site context (e.g. adapter name) the prior code carried. Those
// attrs are also surfaced in the stderr message so the operator who
// double-clicked the .exe can see which adapter / which file failed.
func dieFriendly(title string, err error, attrs ...any) {
	args := append([]any{"err", err}, attrs...)
	slog.Error(title, args...)

	suffix := formatAttrSuffix(attrs)
	fmt.Fprintf(os.Stderr, "\nError: %s%s.\n  %v\n", title, suffix, err)
	waitForEnterOnWindows()
	os.Exit(1)
}

// formatAttrSuffix turns variadic slog attrs (key, value, ...) into
// " (key=value, key=value)" for inclusion in human-readable error
// messages. Returns "" if attrs is empty or malformed (odd length).
func formatAttrSuffix(attrs []any) string {
	if len(attrs) == 0 || len(attrs)%2 != 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(" (")
	for i := 0; i < len(attrs); i += 2 {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%v=%v", attrs[i], attrs[i+1])
	}
	b.WriteString(")")
	return b.String()
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
