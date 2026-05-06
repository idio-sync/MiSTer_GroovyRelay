# Human-readable logs and Web UI guidance — design

**Date:** 2026-05-05
**Status:** Draft, pending implementation

## Problem

Native binary users — particularly Windows operators who double-click
`mister-groovy-relay.exe` and get a console window — have no on-ramp.
The bridge currently emits `slog.NewJSONHandler` records to stdout, so
the first thing they see is:

```
{"time":"2026-05-05T14:23:01.234Z","level":"INFO","msg":"listening","addr":":32500"}
```

That tells them nothing about how to reach the Web UI, what the bridge
is doing, or what to do next. On a config-not-found first run the
process exits immediately, often before the operator can read the
single-line stderr message — the console window just slams shut.

Operators running under Docker, journald, or any pipe / file redirect,
on the other hand, *want* the JSON shape to stay stable so log
aggregation and `grep` pipelines keep working.

## Goals

1. Replace JSON output with human-readable text **only when stdout is an
   interactive terminal**. Non-terminal output (Docker, journald,
   redirected files, CI) keeps the existing JSON handler.
2. Print a one-time startup banner naming the Web UI URL (LAN IP +
   `localhost`) so users running the binary directly know where to go.
3. On first-run config creation and on fatal startup errors on Windows,
   keep the console window open with a "Press Enter to close" prompt so
   the operator actually sees the message.
4. Lightly humanize the highest-frequency `slog` messages without
   touching the ~100 existing call sites.

## Non-goals

- Replacing `slog` with a third-party logger.
- Log rotation, log files, or any output destination other than stdout.
- Localization.
- Rewriting all ~100 existing `slog.Info/Warn/Error` call sites.
- Multi-line stack traces or pretty-printed errors. One record = one
  line in both modes.

## Approach

A small custom `slog.Handler` produces text output; the existing
`slog.JSONHandler` continues to be used otherwise. Selection happens
once at `logging.New()` time and resolves to one of two modes — `text`
or `json` — plus an independent `color` boolean:

1. `MISTER_GROOVY_LOG_FORMAT` env var — `text` | `text-plain` | `json`
   | `auto` (default). `text-plain` is `text` with `color=false`.
2. If `auto`: `golang.org/x/term.IsTerminal(int(os.Stdout.Fd()))` —
   terminal → `text`, otherwise → `json`.
3. `color` resolution: starts true if mode is `text`. Forced false if
   any of: mode is `text-plain`, `NO_COLOR` env is set (any non-empty
   value), or stdout is not a terminal. `NO_COLOR` is a no-op for
   `json` mode (JSON output never contains escapes).

Resolved combinations (the only four that matter):

| Resolution | Trigger |
| --- | --- |
| text + color | terminal stdout, no `NO_COLOR`, format `auto` or `text` |
| text + no color | `text-plain`, or `text` + `NO_COLOR` |
| json | non-terminal stdout, or explicit `json` |
| json | (`NO_COLOR` ignored) |

Docker and `tee file` are non-terminal, so they get JSON automatically.
Windows double-click and `mister-groovy-relay.exe` from a terminal get
text.

The startup banner is printed via `fmt.Fprintln(os.Stdout, ...)` — not
slog — and **only when the resolved mode is `text`**. JSON mode
suppresses the banner entirely so log aggregators receive a clean
strict-JSON stream. An operator who explicitly wants the banner in
JSON mode (rare) can set `MISTER_GROOVY_BANNER=1`. Conversely, a
text-mode operator who wants a quiet boot sets
`MISTER_GROOVY_NO_BANNER=1`. Banner content is constructed from
already-resolved values (host IP, port, adapter list).

## Architecture

### `internal/logging`

```
logging.go          — public API: New, SetLevel, parseLevel
text_handler.go     — new: TextHandler implementing slog.Handler
text_handler_test.go — new: golden-string tests
```

`New(level string)` becomes:

```go
func New(level string) *slog.Logger {
    levelVar.Set(parseLevel(level))
    h := pickHandler(newHandlerWriter, &levelVar)
    return slog.New(h)
}
```

`pickHandler` reads env, runs the isatty check on `os.Stdout`, and
returns either a `*slog.JSONHandler` (existing behavior) or a
`*TextHandler` (new). The handler choice is fixed at construction time;
no runtime swap. Operators changing the env var must restart, which is
fine — env-driven config is read once.

### `TextHandler`

Implements the four-method `slog.Handler` interface (`Enabled`,
`Handle`, `WithAttrs`, `WithGroup`). About 150 LOC.

Output format per record:

```
HH:MM:SS LVL  Message  key=value key=value
```

- `LVL` is a fixed-width 4-char tag rendered in color when enabled:
  - DEBUG → `dbg ` (dim gray)
  - INFO  → ` ok ` (green)
  - WARN  → `WARN` (yellow)
  - ERROR → ` ERR` (red)
- Message is bold; keys dim; values normal; `err=...` values red.
- Time uses local timezone, 24-hour, second precision.
- Group / `WithAttrs` flatten the same way `slog.JSONHandler` does:
  prepended attrs first, then per-record attrs, dotted group prefixes.
  Most call sites pass flat key/value pairs, so this is exercised
  primarily by group-aware adapters (none today, but we don't want to
  break them later).
- Multi-line values are not pretty-printed: newlines in values are
  replaced with `\n` literals to preserve the one-record-per-line rule.

Color disable wins from any one of: `NO_COLOR` set, `text-plain`
selected, or stdout not a terminal at the time `pickHandler` runs.

### Message rewrite table

Inside `text_handler.go`, a `var messageRewrites = map[string]string{}`
maps internal log message → friendlier copy. v1 entries (the messages
that fire on every startup or every cast):

| Internal message | Friendly text |
| --- | --- |
| `listening` | `Web UI ready` |
| `shutting down` | `Shutting down...` |
| `adapter disabled` | `Adapter disabled` |
| `preempting prior session for new request` | `Switching to new cast` |
| `dataplane session started` | `Cast started` |
| `dataplane session ended` | `Cast ended` |
| `GDM discovery active` | `Plex discovery active` |
| `plex.tv device registration loop started` | `Plex registration active` |
| `plex.tv registration skipped (no auth token; run with --link)` | `Plex not linked yet — open the Web UI to link` |
| `host_ip not set; auto-detected via default route — override in config for multi-NIC hosts` | `Auto-detected LAN IP` |

Anything not in the table renders with its first letter capitalized. The
table can grow over time without touching call sites.

The rewrite table applies **only to text output**. JSON records keep
their original `msg` field unchanged so log aggregation and grep
patterns are unaffected.

### `cmd/mister-groovy-relay`

```
main.go            — existing, modified to call greeter and dieFriendly
banner.go          — new: printGreeting, dieFriendly, firstRunMessage,
                          waitForEnterOnWindows
```

`main()` adds three new touchpoints:

1. **First-run config-not-found:** the existing
   `fmt.Fprintf(os.Stderr, "No config found...")` block becomes
   `firstRunMessage(created.Path); waitForEnterOnWindows(); os.Exit(2)`.
2. **Greeter:** call `printGreeting(version, hostIP,
   sec.Bridge.UI.HTTPPort, reg)` after the HTTP listener is *bound*
   (see "listener readiness" below) and after adapter `Start()` calls
   return, before `<-ctx.Done()`. The greeter is a no-op when the
   resolved log mode is `json` (unless `MISTER_GROOVY_BANNER=1`).
3. **Fatal startup errors → `dieFriendly`:** every `os.Exit(1)` site
   reachable *before* the HTTP listener boots gets wrapped. Concretely,
   the rule is: any `slog.Error(...); os.Exit(1)` between `flag.Parse`
   and `httpSrv.Serve(ln)` returning. As of HEAD, that includes (file
   `cmd/mister-groovy-relay/main.go`):
   - line 67 `load config`
   - line 71 `data_dir preflight`
   - line 97 `save stored data`
   - line 122 `sender init`
   - line 167 `plex adapter init`
   - line 171 `registry register plex`
   - line 183 `url adapter init`
   - line 187 `registry register url`
   - line 196 `registry register jellyfin`
   - line 203 `adapter DecodeConfig`
   - line 235 `ui init`

   The two `os.Exit(2)` config-flag-parse sites (lines 53–55, 102–104)
   keep their existing `fmt.Fprintln(os.Stderr, ...)` plus
   `waitForEnterOnWindows()` — they're argument errors, not config
   errors, so the user-friendly tone is different.

   Sites *inside* `runLinkFlow` are out of scope: `--link` is an
   interactive flow that already prints to stdout and the user is
   already attentive.

   `dieFriendly(slogger, title, err)` calls `slog.Error(title, "err",
   err)` (preserving today's structured log line for ops), then in text
   mode prints a short human message
   (`"\nError: <title> failed.\n  <err>\n"`), then
   `waitForEnterOnWindows()`, then `os.Exit(1)`.

### Listener readiness (eliminates banner race)

The current code calls `httpSrv.ListenAndServe()` inside a goroutine,
which means the banner can print before the OS-level `bind()` has
completed. Replace with the listen-then-serve pattern:

```go
ln, err := net.Listen("tcp", addr)
if err != nil {
    dieFriendly(slog.Default(), "http listener", err)
}
go func() {
    if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
        slog.Error("http listener", "err", err)
    }
}()
```

After `net.Listen` returns successfully, the port is bound and accepts
connections (the goroutine just dispatches them). The banner can
safely advertise the URL.

`waitForEnterOnWindows()`:

- No-op unless `runtime.GOOS == "windows"`.
- No-op if `term.IsTerminal(int(os.Stdin.Fd()))` is false (Docker, CI,
  headless service, redirected stdin).
- No-op if `term.IsTerminal(int(os.Stdout.Fd()))` is false (redirected
  stdout — see Risks).
- No-op if `MISTER_GROOVY_NO_PAUSE=1`.
- Otherwise: prints `Press Enter to close this window.` and reads a
  line from stdin.

Greeter format (text mode example, host_ip 192.168.1.20):

```
================================================================
  MiSTer GroovyRelay  v1.0.0
================================================================

  Web UI:  http://192.168.1.20:32500
           http://localhost:32500   (this machine)

  Status:  Plex adapter      enabled
           Jellyfin adapter  enabled
           URL adapter       enabled

  Next:    Open the Web UI in your browser to link Plex/Jellyfin
           and confirm your MiSTer host is reachable.

  Logs:    Detailed activity will appear below.
           Press Ctrl-C to quit.

----------------------------------------------------------------
```

Status lines pull `enabled`/`disabled` from `reg.List()` +
`a.IsEnabled()`. The localhost line is omitted if `hostIP` is empty
(offline host with no default route — the existing fallback path) and
the bridge is bound to all interfaces.

Greeter output is suppressed by `MISTER_GROOVY_NO_BANNER=1` for clean
Docker logs. Default is on — Docker users typically only care about the
absence of a TTY, not the banner itself, since the banner is short and
appears once at boot.

First-run message:

```
================================================================
  MiSTer GroovyRelay  --  First-run setup
================================================================

  A default config was written to:
    C:\Users\Jake\AppData\Roaming\mister-groovy-relay\config.toml

  Next steps:
    1. Open that file in a text editor.
    2. Set bridge.mister.host to your MiSTer's IP address.
    3. Re-launch this app.

  Press Enter to close this window.
```

The path comes straight from `created.Path`. The double-hyphen avoids
non-ASCII em-dash for Windows console safety.

## Detection details

### `term.IsTerminal`

We add `golang.org/x/term` as a direct dependency. It's the canonical
isatty wrapper, ~50 LOC of platform-specific syscalls, and trivial to
audit. Both stdout (for handler choice) and stdin (for the Windows
pause gate) are checked.

### `NO_COLOR`

Per <https://no-color.org>, presence of `NO_COLOR` (any non-empty
value) disables ANSI escapes. We follow that convention exactly.

### Windows ANSI

Windows 10 build 1511+ added support for ANSI escape sequences via the
`ENABLE_VIRTUAL_TERMINAL_PROCESSING` console-mode flag, but the flag
must be set per-process. Modern terminals (Windows Terminal, recent
PowerShell) inherit it; legacy `cmd.exe` launched by double-clicking
the .exe historically does not.

We enable it explicitly. On Windows, `logging.New()` calls a small
`enableWindowsVT()` helper that wraps
`SetConsoleMode(stdout, current | ENABLE_VIRTUAL_TERMINAL_PROCESSING)`
(via `golang.org/x/sys/windows`, already a transitive dep). On other
platforms the helper is a no-op (build-tag split:
`logging_vt_windows.go` / `logging_vt_other.go`). Failures (older
console host, redirected handle) are silently ignored — the worst case
is the user sees raw escapes, fixable with `NO_COLOR=1`.

## Configuration surface

New environment variables (none in `config.toml` — these are
operator-runtime concerns, not persistent config):

| Var | Values | Default | Effect |
| --- | --- | --- | --- |
| `MISTER_GROOVY_LOG_FORMAT` | `auto` \| `text` \| `text-plain` \| `json` | `auto` | Force handler choice; `text-plain` = text without color. |
| `NO_COLOR` | any non-empty | unset | Disable ANSI in text mode. |
| `MISTER_GROOVY_NO_BANNER` | `1` | unset | Suppress startup banner in text mode. |
| `MISTER_GROOVY_BANNER` | `1` | unset | Force banner in JSON mode (rare). |
| `MISTER_GROOVY_NO_PAUSE` | `1` | unset | Skip the Windows "Press Enter" pause on first-run / fatal errors. |

`MISTER_GROOVY_CONFIG` already exists and is unchanged.

## Test plan

### Unit (`internal/logging`)

Reuse the existing `newHandlerWriter` swap pattern. New tests:

- `TestTextHandler_Levels` — golden strings for one record at each of
  DEBUG/INFO/WARN/ERROR in both color and no-color modes.
- `TestTextHandler_WithAttrs` — preset attrs render before per-record
  attrs.
- `TestTextHandler_WithGroup` — dotted group prefixes match
  `slog.JSONHandler`.
- `TestTextHandler_MessageRewrite` — table entries rewrite, untable
  messages capitalize.
- `TestJSONHandler_MessageNotRewritten` — verify the rewrite table is
  not applied to JSON records; the original `msg` is preserved.
- `TestTextHandler_NoNewlinesInValues` — multi-line value is escaped to
  one line.
- `TestPickHandler_RespectsEnv` — `MISTER_GROOVY_LOG_FORMAT=json`
  always picks JSON regardless of TTY state; `text` always picks text.
- Existing JSON tests: stay green because `auto` + non-terminal (test
  buffers are not terminals) → JSON.

### Unit (`cmd/mister-groovy-relay`)

- `TestPrintGreeting_Format` — the rendered greeter contains the
  resolved `hostIP`, the configured port, and a line for each adapter.
- `TestPrintGreeting_Suppressed` — `MISTER_GROOVY_NO_BANNER=1` produces
  empty output.
- `TestPrintGreeting_SuppressedInJSONMode` — when the resolved log
  mode is `json`, the greeter produces no output unless
  `MISTER_GROOVY_BANNER=1` is set.
- `TestStdoutIsStrictJSON` — start the bridge with
  `MISTER_GROOVY_LOG_FORMAT=json`, capture ~1s of stdout, assert every
  newline-terminated line decodes via `json.Decoder` without error.
  Catches future regressions where someone routes a banner or
  `fmt.Println` through stdout in JSON mode.

### Manual

- Windows double-click on a fresh checkout (no config) → friendly
  first-run banner → "Press Enter" → window stays open until keypress.
- Windows double-click with valid config → ok-tagged log lines, banner
  shows the LAN IP and `localhost`, Ctrl-C cleanly shuts down.
- `docker run` → JSON output unchanged.
- `mister-groovy-relay 2>&1 | tee log.txt` → JSON in the file (pipe is
  not a terminal).
- `MISTER_GROOVY_LOG_FORMAT=json mister-groovy-relay` from a terminal →
  JSON.

### CI

Existing `go vet`, `go test`, `go test -race`, integration tests stay
green. CI runs are non-terminal so the JSON path is exercised by all
existing assertions.

## Backward compatibility

- Docker / journald / redirected stdout: unchanged — non-terminal →
  JSON.
- CI: unchanged — non-terminal → JSON.
- Anyone scripting against the JSON shape from a terminal: opt back in
  with `MISTER_GROOVY_LOG_FORMAT=json`.
- All ~100 existing `slog.*` call sites: unchanged.

## Risks and open questions

- **Windows console encoding.** Some CJK locale configurations of
  `cmd.exe` use a code page that mishandles even ASCII underscores in
  pathological cases. We stay within plain ASCII (no em-dash, no box-
  drawing characters). Worst-case fallback is `NO_COLOR=1`.
- **`term.IsTerminal` on unusual stdouts.** A pseudo-terminal in
  Docker (`docker run -it`) reports as a terminal. That's correct — the
  operator wants text. A `winpty`-wrapped MSYS shell may misreport;
  override with the env var.
- **Banner timing.** Resolved by the listener-readiness pattern in
  `cmd/mister-groovy-relay`: `net.Listen` runs synchronously on the
  main goroutine before the banner prints, so the port is bound and
  accepting connections by the time the URL is advertised.

- **Redirected-stdout-from-a-terminal corner case.** Running
  `mister-groovy-relay > log.txt` from an interactive terminal puts
  stdout into a regular file (handler picks JSON; banner suppressed —
  good) but leaves stdin attached to the terminal. If a fatal startup
  error fires, the JSON `slog.Error` line goes to the file and the
  Windows pause prompt waits on stdin — the user sees an apparently
  stalled console. Mitigation: the pause gate also checks
  `term.IsTerminal(stdout)`. If stdout is redirected, no pause —
  consistent with the principle that the pause exists to keep visible
  messages visible, and a redirected stdout has no visible message to
  preserve.

## Out of scope (will not be addressed)

- Adapter-specific health checks in the greeter ("Plex linked? yes /
  no"). v1 only shows enabled/disabled. Future: extend the
  `adapters.Adapter` interface with a `Status() string` method and
  surface that.
- Promoting more existing log messages into the rewrite table — done
  additively over time, not in this PR.
- Color theming or theme env vars.
- A formal `--quiet` flag — `MISTER_GROOVY_NO_BANNER` covers the only
  use case raised so far.

## File summary

| File | Change | Approx LOC |
| --- | --- | --- |
| `internal/logging/logging.go` | Modified — `pickHandler`, mode/color resolution | +50 |
| `internal/logging/text_handler.go` | New | ~150 |
| `internal/logging/text_handler_test.go` | New | ~150 |
| `internal/logging/logging_vt_windows.go` | New — `enableWindowsVT` via `golang.org/x/sys/windows` | ~20 |
| `internal/logging/logging_vt_other.go` | New — no-op for non-Windows | ~10 |
| `cmd/mister-groovy-relay/main.go` | Modified — listen-then-serve, greeter, dieFriendly wrapping at the 11 listed sites, first-run banner | +30 |
| `cmd/mister-groovy-relay/banner.go` | New | ~120 |
| `cmd/mister-groovy-relay/banner_test.go` | New | ~80 |
| `go.mod`, `go.sum` | Modified — add `golang.org/x/term`, ensure `golang.org/x/sys/windows` is direct | n/a |
| `README.md` | Modified — env var note | +10 |
