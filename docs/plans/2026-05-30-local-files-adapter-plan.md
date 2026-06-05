# Local Files Adapter Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a `localfiles` cast-source adapter that browses on-disk media (Docker bind-mounts or native OS paths) from the receiver-chassis UI and casts a single file to the MiSTer.

**Architecture:** A new `internal/adapters/localfiles` package implements the standard `adapters.Adapter` interface (disabled by default), owns named-library config, and exposes a path-jailed browse + single-file cast service. The cast builds a `core.SessionRequest` (`DirectPlay`, full caps, a restrictive `MediaInputPolicy`, and a `VisualizerRequest` for audio-only files) and calls `Manager.StartSession`. The UI is a receiver-chassis settings pane (library editor + a hidden browse drawer) wired through explicit `requireSameOrigin` POST routes — mirroring the URL adapter's host/cookies widget precedent.

**Tech Stack:** Go 1.26, `BurntSushi/toml`, `path/filepath`, stdlib `testing` (no testify), `internal/ffmpeg` (ProbeInput + MediaInputPolicy), `internal/core` (SessionRequest/Manager), vanilla JS + Go `html/template` for the chassis pane.

**Design reference:** [docs/plans/2026-05-30-local-files-adapter-design.md](2026-05-30-local-files-adapter-design.md) (passed three codebase-grounded review rounds).

> **Plan revised after a codebase-grounded review** (2026-05-30). Fixes: the ffprobe binary path is resolved via an injected `extbin.Resolver`, NOT `BridgeConfig.FFprobePath` (C1, Tasks 0/5/7); the integration test moved to `tests/integration/` so `make test-integration` actually runs it (C2, Task 9); `evalExistingPrefix` is now real code with its own test cases incl. an escaping leaf-symlink (I1, Task 3); the `joined`-vs-`real` jail return is documented as deliberate (I2); library array-of-tables persistence is split into its own TDD task that proves the round-trip before any wiring/JS (I3+granularity, Task 11a/11b); `errors.Is(err, io.EOF)` replaces a string compare (M1). The reviewer independently CONFIRMED ~30 codebase claims (interface surface, module path, `SessionRequest` fields, `MediaKind` is a string type, normalize-before-validate, registration site, chassis wiring, and that a plain `[]Library` decodes directly — no wire type needed).

---

## Conventions for every task

- **Tests:** stdlib `testing`, table-driven, `t.TempDir()` for filesystem fixtures. Mirror `internal/adapters/torrent/config_test.go` and `internal/adapters/streams/test_helpers_test.go`.
- **Run one package:** `go test ./internal/adapters/localfiles/ -run TestName -v`
- **Run all:** `make test` (`go test ./...`) and `make lint` (`go vet ./...`).
- **Commit cadence:** one commit per task (after its tests pass). Commit message footer:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- **Plan files are git-ignored;** force-add only the plan/tasks files (`git add -f`). Source files commit normally.

---

## Task 0: Package skeleton + interface assertion (compile-first)

**Files:**
- Create: `internal/adapters/localfiles/adapter.go`
- Create: `internal/adapters/localfiles/doc.go`
- Test: `internal/adapters/localfiles/adapter_test.go`

**Step 1 — Write the failing test** (`adapter_test.go`):

```go
package localfiles

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestAdapterImplementsInterfaces(t *testing.T) {
	var _ adapters.Adapter = (*Adapter)(nil)
	var _ adapters.Validator = (*Adapter)(nil)
}
```

**Step 2 — Run, expect FAIL** (`Adapter` undefined):
`go test ./internal/adapters/localfiles/ -run TestAdapterImplementsInterfaces -v`

**Step 3 — Minimal implementation** (`adapter.go`): declare the struct, a `SessionManager` interface (only what cast needs), `AdapterConfig`, `New`, and stub every interface method so it compiles. Use the streams adapter struct/mutex/state pattern.

```go
package localfiles

import (
	"context"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)
```

> The `probe` field references `ffmpeg.ProbeResult`/`ffmpeg.MediaInputPolicy` and `a.probeDefault` (added in Task 5). To keep Task 0 compiling on its own, add a temporary `func (a *Adapter) probeDefault(ctx context.Context, url string, policy ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) { return nil, nil }` stub at the bottom of `adapter.go`; Task 5 replaces it with the resolver-backed implementation.

// SessionManager is the subset of *core.Manager the localfiles cast path uses.
type SessionManager interface {
	StartSession(core.SessionRequest) error
}

// BinaryResolver resolves the ffprobe binary path. *extbin.Resolver satisfies
// it (see internal/extbin/resolver.go:33). MUST be injected — the ffprobe path
// is NOT readable off config.BridgeConfig.FFprobePath (that field is a usually-
// empty operator override; real resolution goes through extbin, which falls
// back to the bundled sidecar then PATH). main.go already builds
// `ffprobeResolver := extbin.New("ffprobe", sec.Bridge.FFprobePath, selfDir)`
// (main.go:125) and wires it into core (:194) and the chassis (:341); Task 7
// threads that same value in here.
type BinaryResolver interface {
	Resolve() (string, error)
}

type AdapterConfig struct {
	Bridge   config.BridgeConfig
	Core     SessionManager
	FFprobe  BinaryResolver
}

type Adapter struct {
	core    SessionManager
	bridge  config.BridgeConfig
	ffprobe BinaryResolver

	mu         sync.Mutex
	cfg        Config
	state      adapters.State
	lastErr    string
	stateSince time.Time

	// probe is the injection seam for tests; defaults to a wrapper that
	// resolves the ffprobe path via a.ffprobe then calls ffmpeg.Probe.
	// Set in New (see Task 5).
	probe func(ctx context.Context, url string, policy ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error)
}

func New(cfg AdapterConfig) (*Adapter, error) {
	a := &Adapter{
		core:       cfg.Core,
		bridge:     cfg.Bridge,
		ffprobe:    cfg.FFprobe,
		cfg:        DefaultConfig(),
		state:      adapters.StateStopped,
		stateSince: time.Now(),
	}
	a.probe = a.probeDefault // implemented in Task 5
	return a, nil
}

func (a *Adapter) Name() string        { return "localfiles" }
func (a *Adapter) DisplayName() string { return "Local Files" }

func (a *Adapter) Fields() []adapters.FieldDef { return nil }                 // filled in Task 2
func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error { return nil } // Task 2
func (a *Adapter) Validate(raw toml.Primitive, meta toml.MetaData) error     { return nil } // Task 2
func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil // Task 2
}

func (a *Adapter) IsEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Enabled
}

func (a *Adapter) Start(ctx context.Context) error {
	a.mu.Lock()
	a.state = adapters.StateRunning
	a.lastErr = ""
	a.stateSince = time.Now()
	a.mu.Unlock()
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	a.state = adapters.StateStopped
	a.lastErr = ""
	a.stateSince = time.Now()
	a.mu.Unlock()
	return nil
}

func (a *Adapter) Status() adapters.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return adapters.Status{State: a.state, LastError: a.lastErr, Since: a.stateSince}
}
```

Add `doc.go` with a one-paragraph package comment pointing at the design doc.

> NOTE: `Config`/`DefaultConfig` don't exist yet — add a temporary `type Config struct{ Enabled bool }` and `func DefaultConfig() Config { return Config{} }` at the bottom of `adapter.go` so it compiles; Task 1 replaces them in `config.go`.

**Step 4 — Run, expect PASS.**

**Step 5 — `make lint` then commit:**
```bash
git add internal/adapters/localfiles/
git commit -m "feat(localfiles): adapter skeleton implementing adapters.Adapter"
```

---

## Task 1: Named-library config + validation

**Files:**
- Create: `internal/adapters/localfiles/config.go`
- Test: `internal/adapters/localfiles/config_test.go`
- Modify: `internal/adapters/localfiles/adapter.go` (remove the temporary Config stub)

A plain `[]Library` array-of-tables decodes directly via `meta.PrimitiveDecode` — no `UnmarshalTOML` wire type is needed (that ceremony in `streams/config.go` exists only for `hostList`'s string-or-array ambiguity, which we don't have).

**Step 1 — Failing tests** (`config_test.go`). Cover: default is disabled with no libraries; empty name rejected; duplicate names (case-folded) rejected; missing root rejected; non-directory root rejected; unreadable root rejected; valid config passes.

```go
package localfiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigDisabled(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled {
		t.Error("Enabled = true, want false")
	}
	if len(c.Libraries) != 0 {
		t.Errorf("Libraries = %d, want 0", len(c.Libraries))
	}
}

func TestValidateLibraries(t *testing.T) {
	dir := t.TempDir()                        // a real, readable directory
	file := filepath.Join(dir, "f.txt")       // a non-directory
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		libs    []Library
		wantErr bool
	}{
		{"valid", []Library{{Name: "Movies", Root: dir}}, false},
		{"empty name", []Library{{Name: "  ", Root: dir}}, true},
		{"empty root", []Library{{Name: "Movies", Root: ""}}, true},
		{"missing root", []Library{{Name: "Movies", Root: filepath.Join(dir, "nope")}}, true},
		{"root is file", []Library{{Name: "Movies", Root: file}}, true},
		{"dup name casefold", []Library{{Name: "Movies", Root: dir}, {Name: "movies", Root: dir}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Enabled: true, Libraries: tc.libs}
			err := cfg.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
```

**Step 2 — Run, expect FAIL.**

**Step 3 — Implement** (`config.go`): use `adapters.FieldErrors` for per-field errors. The readable check uses `os.Open` + `Readdirnames(1)` (NOT `stat` — the contract is "this process can browse it").

```go
package localfiles

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type Library struct {
	Name string `toml:"name"`
	Root string `toml:"root"`
}

type Config struct {
	Enabled   bool      `toml:"enabled"`
	Libraries []Library `toml:"library"` // [[adapters.localfiles.library]]
}

func DefaultConfig() Config { return Config{} }

func decodeConfig(raw toml.Primitive, meta toml.MetaData) (Config, error) {
	cfg := DefaultConfig()
	if isZeroPrimitive(raw) {
		return cfg, nil
	}
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	var errs adapters.FieldErrors
	seen := map[string]struct{}{}
	for i, lib := range c.Libraries {
		name := strings.TrimSpace(lib.Name)
		key := "library." + itoa(i)
		if name == "" {
			errs = append(errs, adapters.FieldError{Key: key + ".name", Msg: "must not be empty"})
		} else {
			fold := strings.ToLower(name)
			if _, dup := seen[fold]; dup {
				errs = append(errs, adapters.FieldError{Key: key + ".name", Msg: "duplicate library name"})
			}
			seen[fold] = struct{}{}
		}
		if msg := validateRoot(lib.Root); msg != "" {
			errs = append(errs, adapters.FieldError{Key: key + ".root", Msg: msg})
		}
	}
	return errs.Err()
}

// validateRoot returns "" when root is a readable directory this process can
// browse, else a deployment-aware error message. Uses os.Open + Readdirnames
// (not stat) because a dir can stat fine yet be unreadable.
func validateRoot(root string) string {
	if strings.TrimSpace(root) == "" {
		return "must not be empty"
	}
	info, err := os.Stat(root)
	if err != nil {
		return "path not found — in Docker this must be a path you've mounted into the container"
	}
	if !info.IsDir() {
		return "must be a directory"
	}
	f, err := os.Open(root)
	if err != nil {
		return "exists but is not readable by the bridge — check filesystem permissions / container UID"
	}
	defer f.Close()
	if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		// io.EOF = empty but readable, which is fine.
		return "exists but is not readable by the bridge — check filesystem permissions / container UID"
	}
	return ""
}
```

Add small helpers `itoa` (wrap `strconv.Itoa`) and `isZeroPrimitive` (mirror streams' `reflect.ValueOf(raw).IsZero()`). Remove the temporary `Config`/`DefaultConfig` stub from `adapter.go`.

**Step 4 — Run, expect PASS.** **Step 5 — commit:**
```bash
git add internal/adapters/localfiles/
git commit -m "feat(localfiles): named-library config + readable-root validation"
```

---

## Task 2: Wire config into the adapter (Fields/DecodeConfig/Validate/ApplyConfig)

**Files:** Modify `internal/adapters/localfiles/adapter.go`; Test: `internal/adapters/localfiles/adapter_test.go`.

**Step 1 — Failing tests:** `DecodeConfig` with a TOML blob containing two libraries populates `a.cfg`; `Validate` on a bad blob returns error without mutating `a.cfg`; `IsEnabled` reflects decoded value; `Fields()` returns at least the `enabled` bool field.

Use this helper to build a `toml.Primitive` in tests (mirror existing adapter tests):
```go
func decodePrimitive(t *testing.T, body string) (toml.Primitive, toml.MetaData) {
	t.Helper()
	var doc struct {
		P toml.Primitive `toml:"localfiles"`
	}
	meta, err := toml.Decode(body, &doc)
	if err != nil { t.Fatalf("decode: %v", err) }
	return doc.P, meta
}
// body example:
// [localfiles]
// enabled = true
// [[localfiles.library]]
// name = "Movies"
// root = "<t.TempDir()>"
```

**Step 3 — Implement** the four methods using the streams pattern (DecodeConfig → decode → Validate → store under lock; ApplyConfig returns `ScopeHotSwap`). `Fields()` returns the `enabled` field only — the library list is a custom widget (Task 7), not a stock `FieldDef`:

```go
func (a *Adapter) Fields() []adapters.FieldDef {
	d := DefaultConfig()
	return []adapters.FieldDef{
		{Key: "enabled", Label: "Enabled", Kind: adapters.KindBool, Default: d.Enabled, ApplyScope: adapters.ScopeHotSwap},
	}
}

func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg, err := decodeConfig(raw, meta)
	if err != nil {
		return fmt.Errorf("localfiles: decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	return nil
}

func (a *Adapter) Validate(raw toml.Primitive, meta toml.MetaData) error {
	cfg, err := decodeConfig(raw, meta)
	if err != nil {
		return fmt.Errorf("localfiles: decode config: %w", err)
	}
	return cfg.Validate()
}

func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	cfg, err := decodeConfig(raw, meta)
	if err != nil {
		return 0, fmt.Errorf("localfiles: decode apply config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return 0, err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	return adapters.ScopeHotSwap, nil
}
```

**Step 4 — PASS. Step 5 — commit:** `feat(localfiles): wire config through DecodeConfig/Validate/ApplyConfig`.

---

## Task 3: Path-jail helper (the security core)

**Files:** Create `internal/adapters/localfiles/jail.go`; Test: `internal/adapters/localfiles/jail_test.go`.

Reuse the torrent `filepath.Rel` + `..`-prefix containment pattern ([internal/adapters/torrent/cache.go:83-106](../../internal/adapters/torrent/cache.go#L83-L106)). Add symlink resolution, case-folding for case-insensitive filesystems, and a non-existent-leaf path that jails the parent.

**Step 1 — Failing tests** (table-driven). Build a temp tree: `root/ok.mkv`, `root/sub/inner.mp4`, an escaping symlink `root/escape -> <outside>`, and a sibling `root-secret/`. Assert:
- `resolveInLibrary(root, "ok.mkv")` → ok, absolute path inside root.
- `"sub/inner.mp4"` → ok.
- `"../etc/passwd"` → error.
- absolute input `"/etc/passwd"` → error.
- `"escape"` (symlink leaving root) → error.
- a path that resolves into `root-secret` (boundary collision) → error.
- non-existent leaf `"new.mkv"` → ok if parent is in-jail (used by cast pre-create? for browse it's listing only — assert the parent-jail behavior returns the joined path without error and a `exists=false`).

**Step 3 — Implement:**

```go
package localfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// resolveInLibrary joins rel onto root, rejects traversal/absolute/symlink
// escapes, and returns the cleaned absolute path. The boolean reports whether
// the leaf exists (callers listing a dir require true; cast requires a file).
func resolveInLibrary(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	if hasDotDot(rel) {
		return "", fmt.Errorf("path traversal is not allowed")
	}
	rootReal, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("library root unavailable: %w", err)
	}
	joined := filepath.Join(rootReal, filepath.Clean("/"+rel)) // leading / neutralizes any residual ..
	// Resolve symlinks on the deepest existing ancestor, then re-check containment.
	real, err := evalExistingPrefix(joined)
	if err != nil {
		return "", err
	}
	if !withinRoot(rootReal, real) {
		return "", fmt.Errorf("path escapes library root")
	}
	return joined, nil
}

func withinRoot(rootReal, target string) bool {
	if sameFold(rootReal, target) {
		return true
	}
	rel, err := filepath.Rel(rootReal, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !(rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func hasDotDot(p string) bool {
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == os.PathSeparator }) {
		if seg == ".." {
			return true
		}
	}
	return false
}

// sameFold compares paths case-insensitively on case-insensitive platforms.
func sameFold(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
```

Make `withinRoot`'s `Rel` comparison case-fold on case-insensitive FS (compare `strings.ToLower` of both when on a `sameFold` platform).

**`evalExistingPrefix` — code it, don't paraphrase it (this is the security core).** The subtle trap: if the *leaf itself* exists and is a symlink (`root/escape -> /outside`), it MUST be resolved and caught — so the walk has to start at the full path, not the parent. Resolve the deepest existing prefix (which includes an existing leaf), then rejoin only the genuinely-missing tail:

```go
// evalExistingPrefix resolves symlinks on the longest existing prefix of p
// (including the leaf if it exists), then rejoins any non-existent tail
// segments unresolved. This catches an existing leaf symlink that escapes the
// jail while still allowing a not-yet-existing leaf (browse never needs that;
// kept for cast-path symmetry).
func evalExistingPrefix(p string) (string, error) {
	p = filepath.Clean(p)
	missing := []string{}
	cur := p
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(append([]string{real}, reverse(missing)...)...), nil
		}
		if !os.IsNotExist(err) {
			return "", err // permission error, etc. — fail closed
		}
		parent := filepath.Dir(cur)
		if parent == cur { // reached root and nothing resolved
			return "", err
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}
// reverse returns a new slice with elements in reverse order.
```

**Its own test cases (Task 3 table must include these):** existing file leaf (resolves, stays in jail → ok); existing **symlink leaf escaping** root (`root/escape -> /tmp/outside` → rejected by `withinRoot`); missing single-segment leaf under a real dir (ok, returns joined-with-tail); missing multi-segment tail (`root/sub/none/x.mkv` → resolves `root/sub`, rejoins `none/x.mkv`); permission-denied ancestor (fails closed, not treated as "missing").

**Return-value note (I2 — make this explicit in a code comment so a future reviewer doesn't "fix" it):** `resolveInLibrary` checks containment on the **resolved** path (`real` from `evalExistingPrefix`) but **returns `joined` (the cleaned, un-resolved path)**. This is deliberate: for a non-existent leaf there is no resolved path to return, and for the cast path the design explicitly accepts the TOCTOU race and re-jails rather than threading an fd (there is no fd field on `SessionRequest`). Containment is validated on `real`; `joined` is what ffmpeg consumes. Do not change the return to `real` — it breaks the missing-leaf case.

**TOCTOU comment per design:** browse handlers operate on the opened dir/file; the cast path re-jails and accepts the documented race (no fd to thread into `SessionRequest`).

**Step 4 — PASS (run the full jail table). Step 5 — commit:** `feat(localfiles): path-jail helper with symlink + case-fold + boundary checks`.

---

## Task 4: Direct-media allowlist + local-file MediaInputPolicy

**Files:** Create `internal/adapters/localfiles/media.go`; Test: `internal/adapters/localfiles/media_test.go`.

**Step 1 — Failing tests:**
- `isPlayable("x.mkv")`, `.mp4`, `.flac`, `.mp3` → true; `.m3u`, `.m3u8`, `.pls`, `.xspf`, `.strm`, `.url`, `.sdp`, `.smil`, `.ffconcat`, `.cue`, `.edl`, `.srt`, `.ass`, `.txt` → false; case-insensitive (`.MKV` true).
- `localFilePolicy()` returns `ffmpeg.MediaInputPolicy{ProtocolWhitelist: []string{"file"}, DisableRedirects: true, DisablePlaylists: true}` and `DisableReconnect == false` (the hlsbuffer Alpine-FFmpeg trap — do NOT set it).

**Step 3 — Implement:**

```go
package localfiles

import (
	"path/filepath"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

// playableExts is a fixed allow-list of direct audio/video containers. Anything
// that can reference child/external resources (playlists, manifests, links,
// concat/cue/edl, sidecar subtitles) is rejected: ProtocolWhitelist=["file"]
// still permits nested file: reads, so the extension gate is load-bearing.
var playableExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".mov": true, ".avi": true,
	".webm": true, ".ts": true, ".mpg": true, ".mpeg": true, ".wmv": true,
	".flac": true, ".mp3": true, ".m4a": true, ".aac": true, ".ogg": true,
	".opus": true, ".wav": true,
}

func isPlayable(name string) bool {
	return playableExts[strings.ToLower(filepath.Ext(name))]
}

func localFilePolicy() ffmpeg.MediaInputPolicy {
	return ffmpeg.MediaInputPolicy{
		ProtocolWhitelist: []string{"file"},
		DisableRedirects:  true, // contract marker (no argv); harmless for file inputs
		DisablePlaylists:  true, // contract marker (no argv); allow-list is the real gate
		// DisableReconnect intentionally false: Alpine FFmpeg 6.x rejects
		// -reconnect* under ProtocolWhitelist=["file"] (see hlsbuffer/session.go:96-103).
	}
}
```

> The policy test asserts struct fields for the two markers and asserts only `ProtocolWhitelist` would emit argv — do not assert ffmpeg output for the markers.

**Step 4 — PASS. Step 5 — commit:** `feat(localfiles): direct-media allow-list + restrictive file MediaInputPolicy`.

---

## Task 5: Browse service (jailed listing + bounded ffprobe)

**Files:** Create `internal/adapters/localfiles/browse.go`; Test: `internal/adapters/localfiles/browse_test.go`.

This is the in-memory service the chassis route (Task 8) calls. It does NOT touch HTTP yet.

**Step 1 — Failing tests:** build a library tree, then:
- `Browse(lib, "")` returns folders + playable files only (hidden/dotfiles and non-playable extensions filtered), sorted folders-first then name.
- Each returned entry's path is re-jailed: a child symlink pointing outside the root is dropped from results (not just the parent jailed).
- Unknown library name → error. Traversal path → error (delegates to jail).
- ffprobe is injected (`a.probe` func field) so tests run without a real binary; a probe that errors yields an entry with empty duration, never failing the listing.

**Step 3 — Implement.** Add a `probe` function field to `Adapter` (default wraps `ffmpeg.Probe` with the bridge's resolved ffprobe path; injectable for tests):

```go
type browseEntry struct {
	Name      string // display name
	Rel       string // path relative to library root (for next browse/cast)
	IsDir     bool
	Playable  bool
	DurationS float64 // 0 when unknown / dir
	AudioOnly bool
}

func (a *Adapter) Browse(libName, rel string) ([]browseEntry, error) {
	root, ok := a.libraryRoot(libName)
	if !ok {
		return nil, fmt.Errorf("unknown library %q", libName)
	}
	dirAbs, err := resolveInLibrary(root, rel)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(dirAbs)
	if err != nil {
		return nil, err
	}
	out := make([]browseEntry, 0, len(ents))
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // skip dotfiles
		}
		childRel := filepath.Join(rel, name)
		// Re-jail every child (symlink-inside-valid-folder defense).
		if _, err := resolveInLibrary(root, childRel); err != nil {
			continue
		}
		if e.IsDir() {
			out = append(out, browseEntry{Name: name, Rel: childRel, IsDir: true})
			continue
		}
		if !isPlayable(name) {
			continue
		}
		out = append(out, a.describeFile(root, childRel, name))
	}
	sortEntries(out) // dirs first, then case-insensitive name
	return out, nil
}
```

`describeFile` runs a **bounded** probe (short per-file timeout via `context.WithTimeout`, e.g. 800ms) and degrades to empty metadata on error. Add a small worker-pool cap (concurrency 4) in a `BrowseProbed` variant or document that v1 probes lazily per entry within the listing deadline. Keep the deadline behavior: return entries with empty metadata rather than blocking.

`libraryRoot(name)` does a **case-folded** lookup matching the validation fold.

**Also implement `probeDefault`** (replaces the Task 0 stub) — this is the real ffprobe seam. It resolves the binary via the injected `BinaryResolver` (NOT off `bridge.FFprobePath`) and calls `ffmpeg.Probe`:

```go
func (a *Adapter) probeDefault(ctx context.Context, url string, policy ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
	if a.ffprobe == nil {
		return nil, fmt.Errorf("localfiles: no ffprobe resolver configured")
	}
	bin, err := a.ffprobe.Resolve()
	if err != nil {
		return nil, fmt.Errorf("localfiles: resolve ffprobe: %w", err)
	}
	return ffmpeg.Probe(ctx, bin, url, policy)
}
```

Add a unit test that injects a fake `BinaryResolver` returning a path and asserts `probeDefault` forwards to a stubbed probe — but note the *real* binary path is exercised only by Task 9's integration test. All other tests in Tasks 5/6 inject `a.probe` directly and never call `probeDefault`.

**Step 4 — PASS. Step 5 — commit:** `feat(localfiles): jailed browse service with child re-jail + bounded probe`.

---

## Task 6: Cast service (SessionRequest + visualizer for audio)

**Files:** Create `internal/adapters/localfiles/cast.go`; Test: `internal/adapters/localfiles/cast_test.go`.

**Step 1 — Failing tests** using a `fakeCore` capturing the last `SessionRequest` (mirror `streams/test_helpers_test.go` `fakeCore`):
- Cast a video file → `StartSession` called once with `StreamURL` = the jailed absolute path, `Source == "localfiles"`, `DirectPlay == true`, `Capabilities{CanSeek:true,CanPause:true}`, `MediaInputPolicy == localFilePolicy()`, `Visualizer.Enabled == false`, `AdapterRef` non-empty and prefixed `localfiles:`.
- Cast an audio-only file (probe stub reports `AudioRate>0, Width==0`) → `MediaKind == core.MediaKindMusic`, `Visualizer.Enabled == true`, `Visualizer.Mode == core.VisualizerModeRetroAnalyzer` (placeholder; core normalizes to the live global mode before validate).
- Cast a non-playable extension → error, `StartSession` NOT called.
- Cast a traversal path → error (jail), `StartSession` NOT called.

**Step 3 — Implement:**

```go
func (a *Adapter) Cast(ctx context.Context, libName, rel string) error {
	root, ok := a.libraryRoot(libName)
	if !ok {
		return fmt.Errorf("unknown library %q", libName)
	}
	abs, err := resolveInLibrary(root, rel)
	if err != nil {
		return err
	}
	if !isPlayable(filepath.Base(abs)) {
		return fmt.Errorf("not a playable media file")
	}
	pr, _ := a.probe(ctx, abs, localFilePolicy()) // best-effort; nil tolerated
	title := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))

	req := core.SessionRequest{
		StreamURL:        abs,
		Source:           a.Name(),
		AdapterRef:       "localfiles:" + randHex8(),
		DirectPlay:       true,
		Capabilities:     core.Capabilities{CanSeek: true, CanPause: true},
		MediaInputPolicy: localFilePolicy(),
		MediaKind:        core.MediaKindVideo,
		Title:            title,
		DisplayMetadata:  core.DisplayMetadata{Primary: title, Secondary: libName},
	}
	if pr != nil && pr.AudioRate > 0 && pr.Width == 0 {
		req.MediaKind = core.MediaKindMusic
		req.Visualizer = core.VisualizerRequest{
			Enabled: true,
			Mode:    core.VisualizerModeRetroAnalyzer, // placeholder; core normalizes to global
		}
		// ArtworkPath stays empty in v1 (embedded-art extraction is out of scope).
	}
	return a.core.StartSession(req)
}
```

Add `randHex8()` (crypto/rand → 8 hex chars, mirror url adapter's session-id helper).

**Step 4 — PASS. Step 5 — commit:** `feat(localfiles): single-file cast building SessionRequest + audio visualizer`.

---

## Task 7: Register the adapter in main.go

**Files:** Modify `cmd/mister-groovy-relay/main.go`.

**Step 1 — Implementation** (no unit test; verified by build + a smoke check). Construct and register alongside torrent (after the torrent block, ~line 284), passing `Bridge`, `Core`, and the **already-constructed `ffprobeResolver`** (built at main.go:125 as `extbin.New("ffprobe", sec.Bridge.FFprobePath, selfDir)` — reuse that same variable; do NOT build a new one and do NOT pass `sec.Bridge.FFprobePath` as a path):

```go
localFilesAdapter, err := localfiles.New(localfiles.AdapterConfig{
	Bridge:  sec.Bridge,
	Core:    coreMgr,
	FFprobe: ffprobeResolver, // *extbin.Resolver, same value wired into core/chassis
})
if err != nil {
	dieFriendly("localfiles adapter init", err)
}
if err := reg.Register(localFilesAdapter); err != nil {
	dieFriendly("registry register localfiles", err)
}
```

Add the import `"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/localfiles"`. The existing `DecodeConfig` loop (main.go:307-312) picks it up automatically.

**Step 2 — Verify build + registration:**
```bash
make build-bridge
./mister-groovy-relay --help 2>&1 | head        # must not panic
```
Expected: builds clean; bridge starts far enough to register adapters (it's disabled-by-default so it stays OFF).

**Step 3 — commit:** `feat(cmd): register localfiles adapter in the bridge`.

---

## Task 8: Chassis browse + cast HTTP routes

**Files:**
- Modify: `internal/chassis/server.go` (route registration, ~after line 319)
- Modify: `internal/chassis/settings.go` (handlers + response writers)
- Modify: `internal/chassis/handler.go` or wherever the chassis `Config` struct lives (add a `LocalFiles` service interface field)
- Modify: `cmd/mister-groovy-relay/main.go` (wire the adapter as the chassis `LocalFiles` impl — mirror `bridgeAdapterHostEditor`)
- Test: `internal/chassis/settings_test.go` (handler tests)

**Step 1 — Define the chassis-facing interface** (in the chassis package, next to `AdapterHostEditor`):

```go
type LocalFilesService interface {
	Browse(lib, path string) ([]LocalFileEntry, error)
	Cast(ctx context.Context, lib, path string) error
}
type LocalFileEntry struct {
	Name string; Rel string; IsDir bool; Playable bool; DurationS float64; AudioOnly bool
}
```

Add `LocalFiles LocalFilesService` to the chassis `Config`. In `cmd`, write a thin `bridgeLocalFiles` wrapper (like `bridgeAdapterHostEditor`) that adapts `*localfiles.Adapter` to this interface and maps `browseEntry` → `LocalFileEntry`.

**Step 2 — Failing handler tests** (`settings_test.go`, follow existing chassis handler test style):
- `POST /receiver/settings/adapter/localfiles/browse` with `lib=Movies&path=` returns 200 JSON `{ok:true, entries:[...]}`.
- Missing `Sec-Fetch-Site` and cross-site `Origin` → 403 (the `requireSameOrigin` wrapper).
- Nil service → 503 chip.
- `POST .../cast` with a valid file → 200 `{ok:true}`; jail error → 4xx with error envelope (reuse `emitSaveError`).

**Step 3 — Register routes** (`server.go`, after line 319):

```go
mux.Handle("POST /receiver/settings/adapter/localfiles/browse",
	requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterLocalfilesBrowse)))
mux.Handle("POST /receiver/settings/adapter/localfiles/cast",
	requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterLocalfilesCast)))
```

Implement the two handlers in `settings.go` mirroring `handleSettingsAdapterHostsPost`: nil-guard the service (503 chip), parse form (`lib`, `path`) with `http.MaxBytesReader`, call the service, and emit JSON. On browse/cast error use `emitSaveError` / a `writeJSONError`-style 4xx; never leak whether an out-of-jail path exists (generic "not found").

**Step 4 — PASS (`go test ./internal/chassis/ -run Localfiles -v`) + `make build`. Step 5 — commit:** `feat(chassis): localfiles browse/cast routes wired to the adapter`.

---

## Task 9: Real-ffprobe verification (de-risks the "free transport" claim)

**Files:** Create `tests/integration/localfiles_probe_test.go` (build-tagged `//go:build integration`).

> **Location matters.** `make test-integration` runs `go test -tags=integration ./tests/integration/...` ([Makefile:15](../../Makefile#L15)) — it ONLY walks `tests/integration/`. A `//go:build integration` file under `internal/adapters/localfiles/` is invisible to that target AND skipped by plain `make test` (no build tag), so it would never run anywhere. Put it under `tests/integration/` to match the existing convention. Resolve the bundled ffmpeg/ffprobe via `extbin.New(...)` (mirror how main.go:124-125 builds the resolvers) inside the test.

**Step 1 — Test:** generate a tiny fixture with the bundled ffmpeg (e.g. `ffmpeg -f lavfi -i sine -t 1 fixture.flac` into `t.TempDir()`), then call `ffmpeg.Probe(ctx, ffprobePath, fixtureAbsPath, localFilePolicy())` and assert `err == nil`, `Duration > 0`, and audio detected (`AudioRate > 0`). This proves a scheme-less local path is accepted under `ProtocolWhitelist=["file"]` — the assumption the whole "transport is free" claim rests on. (`localFilePolicy()` is unexported in `localfiles`; either duplicate the 3-field policy literal in the test or export a small `LocalFilePolicy()` helper — prefer duplicating in the test to avoid widening the adapter's API.)

**Step 2 — Run:** `make test-integration` (or `go test -tags=integration ./tests/integration/ -run TestLocalFilesProbeUnderPolicy -v`).
Expected: PASS. If it FAILS (Alpine FFmpeg rejects the bare path), the fix is to prefix `file:` on the URL in `cast.go`/`browse.go` and document it — do this before any UI polish.

**Step 3 — commit:** `test(localfiles): integration probe proves file-only policy accepts local paths`.

---

## Task 10: Receiver settings pane — data + template

**Files:**
- Modify: `internal/chassis/data.go` (add `"localfiles"` to the adapter slice at ~line 588; add pane fields + per-adapter block)
- Create: `internal/chassis/templates/settings-adapter-localfiles.html`
- Modify: `internal/chassis/templates/settings-adapters.html` (add the template invocation)
- Test: `internal/chassis/data_test.go` (pane appears when adapter present)

**Step 1 — Failing test:** `settingsDataFromConfig` with the localfiles adapter registered yields an `AdapterPaneData` named `localfiles` with `HasLibraryEditor == true` and `HasBrowseDrawer == true`.

**Step 3 — Implement:**
- `data.go`: extend the slice to include `"localfiles"`; add `HasLibraryEditor bool`, `Libraries []LocalFileLibraryRow`, `HasBrowseDrawer bool` to `AdapterPaneData`; in the loop add an `if name == "localfiles" { pane.HasLibraryEditor = true; pane.HasBrowseDrawer = true; pane.Libraries = ... }` block populated from the service/config.
- `settings-adapter-localfiles.html`: define `settings-adapter-localfiles` (enabled field + `{{ if .HasLibraryEditor }}{{ template "localfiles-library-editor" . }}{{ end }}` + `{{ if .HasBrowseDrawer }}{{ template "localfiles-browse-drawer" . }}{{ end }}`). Model the library editor on `url-host-editor` (a tag/row list with add/remove) and the drawer on a hidden modal. `ParseFS` auto-registers the file — no code change to `templates.go`.
- `settings-adapters.html`: add `{{ template "settings-adapter-localfiles" (adapterPane .Adapters "localfiles") }}`.

**Step 4 — PASS + `make build`. Step 5 — commit:** `feat(chassis): localfiles settings pane data + template`.

---

## Task 11a: Library-list persistence (the highest-risk plumbing — TDD, prove the round-trip FIRST)

**Files:**
- Modify: `internal/adapters/localfiles/` (add `SetLibraries`)
- Modify: `internal/chassis/settings.go` + `internal/chassis/server.go` (route + handler) and the chassis `Config` (a `LocalFilesLibraryEditor` interface)
- Modify: `cmd/mister-groovy-relay/` (a `bridgeLocalFiles` wrapper, mirroring `cmd/.../adapter_host_editor.go`)
- Test: `internal/adapters/localfiles/save_test.go` (end-to-end round-trip) + `internal/chassis/settings_test.go` (handler)

> **Why this is its own task and goes first:** persisting a variable-length `[[adapters.localfiles.library]]` **array-of-tables** is the single unproven assumption left in the plan. The URL host editor's `SaveValues` path ([internal/uiserver/adapter_saver.go:483](../../internal/uiserver/adapter_saver.go#L483)) is verified to work for `ytdlp_hosts`, but that is a **flat `[]string` scalar array** — NOT a table array. The `SaveValues` flow encodes the merged map → re-reads it as `map[string]any` → re-encodes (`encodeAdapterMap` → `readAdapterSectionMap` → `replaceAdapterSection`); the lossy spot for table arrays lives in that round-trip. **Do not assume it carries `[]Library`.**

**Step 1 — Failing round-trip test FIRST** (`save_test.go`): write a `config.toml` to `t.TempDir()` with `[adapters.localfiles]` and one library, call `SetLibraries([]Library{{ "Movies", dirA }, { "Music", dirB }})`, then re-read the file from disk (fresh `toml.Decode`) and assert both libraries survived with exact field equality and correct ordering. This proves array-of-tables persistence before any wiring.

**Step 2 — Run, expect FAIL.**

**Step 3 — Implement `SetLibraries`.** First try mirroring `SaveValues` (pass `values["library"] = []map[string]any{{"name":..,"root":..}, ...}`). **If the round-trip test shows `SaveValues` cannot carry the table array** (lossy/reordered/dropped), fall back to a dedicated path: have `SetLibraries` validate, then re-serialize the whole `[adapters.localfiles]` section itself via `toml`-marshal-and-replace (the adapter owns its section), returning `adapters.ApplyScope`. Reject invalid lists (empty/dup/missing-root) with `adapters.FieldErrors` before writing — never leave a partial file.

**Step 4 — Round-trip test PASS.** Then add the chassis route `POST /receiver/settings/adapter/localfiles/libraries` (wrapped in `requireSameOrigin`) + handler mirroring `handleSettingsAdapterHostsPost`, the `LocalFilesLibraryEditor` interface on chassis `Config`, and the `bridgeLocalFiles` cmd wrapper. Handler test: valid list 200s and persists; invalid returns field errors via `emitSaveError`.

**Step 5 — `make test` + commit:** `feat(localfiles): persist library list via SetLibraries + chassis route`.

---

## Task 11b: Library editor + browse drawer JS (manual-verified)

**Files:** Modify `internal/chassis/static/settings-drawer.js` (or a new `localfiles.js` if the build bundles it — check how `settings-drawer.js` is included first).

Follow the `urlHostEditor`/`putHosts` fetch pattern verbatim:
- **Library editor:** add/remove `{name, root}` rows; each change POSTs the full list to `/libraries` (whole-list replace, like hosts) and re-renders from the server's normalized response; field errors render inline.
- **Browse drawer:** an "Open" button reveals `[data-browse-modal]` (hidden by default → progressive disclosure); closing hides it. Browsing POSTs `lib`+`path` to `/browse`, renders folders/files (folders drill in, breadcrumb back), files show duration when present. "Cast" on a file POSTs to `/cast`; on `{ok:true}` show a notice chip and close the drawer; on error render the widget error.
- Use `fetch` with a form body; the browser sets `Sec-Fetch-Site` automatically (never set it manually).

**Manual verification** (record in the commit message / PR — JS has no unit harness here):
```bash
make build-bridge && ./mister-groovy-relay      # with a config.toml that has a localfiles library
# Open http://localhost:32500/receiver, Settings → Local Files:
#  - add a library, reload the page, confirm it persisted (exercises Task 11a end-to-end)
#  - open the browse drawer, navigate folders, cast a file, confirm playback starts on the CRT/fake-mister
```

**`make test` + `make lint` clean. Commit:** `feat(chassis): localfiles library editor + browse drawer UI`.

---

## Task 12: Docs

**Files:** Modify `README.md`; optionally add `docs/localfiles.md`.

- README "Adapters" table: add a `Local Files` row (Starts from: receiver settings drawer; Default: Off; Notes: browse + cast on-disk media).
- README "Video Cast Sources": add "Local media files (browse a mounted/host directory)".
- Add a short "Local Files" section: the Docker bind-mount example (`-v /mnt/user/media:/media:ro`), the container-vs-host path gotcha, the readable-by-container-UID permission note, and that native builds use real OS paths (incl. UNC).

**Verify:** `make test && make lint` clean. **Commit:** `docs: document the Local Files adapter (mounts, paths, permissions)`.

---

## Final verification (before handing off / PR)

```bash
make test          # go test ./...  — all green
make test-integration   # includes Task 9's probe test (needs bundled ffmpeg)
make lint          # go vet ./...
make build         # both binaries build
```

Then `superpowers:requesting-code-review` on the branch (the design itself already passed three review rounds; this verifies the implementation matches it).

## Out of scope (do NOT build — deferred per design)

Folder queues / auto-advance, subtitle burn-in, embedded cover-art extraction, multi-root search, a `QuickCastProvider` drawer tab, and a chassis source-cluster lamp (`SourceID`). Each is noted in the design's non-goals.
