# Streams Provider Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a generic `streams` adapter that plays catalog-backed YouTube-ID channel providers, with bundled MTV Rewind and Cartoon Rewind support plus native URL handoff.

**Architecture:** Implement `internal/adapters/streams` as a peer adapter that owns provider catalogs, queue state, playback controls, and UI routes. Remote provider data is data-only and passes strict manifest, cache, URL, redirect, DNS, and byte-limit validation before becoming active. Playback reuses the existing yt-dlp resolver and `core.Manager`, with a small EOF lifecycle fix in core so queues can advance naturally.

**Tech Stack:** Go 1.26.2, BurntSushi/toml, stdlib `net/http`, `html/template`, `httptest`, `embed`, `encoding/json`, `net/netip`, `golang.org/x/net/idna`, `internal/adapters/url/ytdlp`, htmx fragments through the existing adapter route mount.

**Spec:** [docs/superpowers/specs/2026-05-09-streams-provider-adapter-design.md](../specs/2026-05-09-streams-provider-adapter-design.md)

---

## Files

**Create:**
- `internal/adapters/streams/adapter.go` — adapter lifecycle, state, dependencies, `adapters.Adapter` implementation.
- `internal/adapters/streams/config.go` — TOML config, defaults, validation, apply-scope mapping.
- `internal/adapters/streams/provider.go` — normalized catalog structs, manifest structs, provider interface, validation helpers.
- `internal/adapters/streams/manifest.go` — bundled/cached/remote manifest merge and source tracking.
- `internal/adapters/streams/cache.go` — atomic cache read/write and cache metadata checksum validation.
- `internal/adapters/streams/fetch.go` — bounded HTTP fetcher, redirect checks, DNS/IP SSRF guard.
- `internal/adapters/streams/provider_youtube_channel_json.go` — playlist JSON parser and YouTube item synthesis.
- `internal/adapters/streams/url_resolver.go` — typed URL rule matching and extraction.
- `internal/adapters/streams/queue.go` — queue construction, play modes, ownership tokens, next/previous/replay/stop state.
- `internal/adapters/streams/playback.go` — yt-dlp resolution and `core.SessionRequest` construction.
- `internal/adapters/streams/routes.go` — adapter-owned route table and handlers.
- `internal/adapters/streams/ui.go` — `ExtraPanelHTML`, htmx fragments, JSON status model.
- `internal/adapters/streams/assets.go` — embedded seed manifest/catalogs.
- `internal/adapters/streams/test_helpers_test.go` — fake core, fake resolver, fake clock/RNG helpers.
- `internal/adapters/streams/*_test.go` — focused unit/adapter/fake-server tests.
- `internal/adapters/streams/testdata/mtv-playlists.seed.json` — small checked-in MTV seed catalog.
- `internal/adapters/streams/testdata/cartoon-playlists.seed.json` — small checked-in Cartoon seed catalog.
- `internal/adapters/streamhandoff/handoff.go` — neutral URL-to-streams handoff contract shared by URL and streams.

**Modify:**
- `internal/core/manager.go` — fire `OnStop("eof")` on natural EOF with existing identity guards.
- `internal/core/manager_test.go` — natural EOF, pause, error, stop, preempt regression coverage.
- `internal/adapters/url/adapter.go` — add optional stream resolver dependency.
- `internal/adapters/url/play.go` — consult streams before direct/yt-dlp URL dispatch.
- `internal/adapters/url/play_test.go` — handoff behavior and disabled/error propagation.
- `cmd/mister-groovy-relay/main.go` — construct/register streams adapter and inject it into URL adapter.
- `README.md` — document streams adapter config and example provider links.

## Conventions

- Keep every new streams file focused; avoid one large adapter file.
- Run package tests after each task and commit each task independently.
- Use `cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ...` on this machine when WSL Go is not the repo's configured toolchain.
- Do not fetch real MTV/Cartoon URLs in unit tests. Use `httptest.Server` and embedded seed JSON.
- Do not execute remote JavaScript. Provider behavior comes from compiled Go provider types plus data-only manifests.
- Do not change unrelated dirty files already present in the worktree.

---

## Task 1: Core EOF Lifecycle Fix

**Files:**
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`

**Why:** Streams queues need `SessionRequest.OnStop("eof")` when a plane exits normally. The current `runErr == nil` branch transitions `EvEOF` but never captures `OnStop`, clears `m.active`, clears `m.cancelFn`, removes subtitles, or calls `notifySessionStop`.

- [ ] **Step 1: Write failing EOF regression test with a hermetic plane seam**

Add this test near the existing manager lifecycle tests in `internal/core/manager_test.go`:

```go
func TestManager_NaturalEOFFiresOnStopAndClearsActive(t *testing.T) {
	m := newTestManager(t)
	done := make(chan string, 1)

	plane := &fakePlane{}
	m.mu.Lock()
	m.plane = plane
	m.cancelFn = func() {}
	m.active = &activeSession{req: SessionRequest{
		OnStop: func(reason string) { done <- reason },
	}}
	m.mu.Unlock()

	m.handlePlaneExit(plane, nil)

	select {
	case got := <-done:
		if got != "eof" {
			t.Fatalf("OnStop reason = %q, want eof", got)
		}
	case <-time.After(time.Second):
		t.Fatal("OnStop was not called")
	}
	if st := m.Status(); st.State != StateIdle {
		t.Fatalf("state after EOF = %s, want %s", st.State, StateIdle)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil || m.plane != nil || m.cancelFn != nil {
		t.Fatalf("manager did not clear session: active=%v plane=%v cancelFnNil=%v", m.active, m.plane, m.cancelFn == nil)
	}
}
```

Add this fake plane to the same test file:

```go
type fakePlane struct{}

func (f *fakePlane) Run(context.Context) error        { return nil }
func (f *fakePlane) Done() <-chan struct{}            { ch := make(chan struct{}); close(ch); return ch }
func (f *fakePlane) Position() time.Duration          { return 0 }
func (f *fakePlane) SetFieldOrder(string) error       { return nil }
```

Add `context` and `github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane` to the existing `internal/core/manager_test.go` import block. The test file already imports `internal/ffmpeg`.

This direct `handlePlaneExit` test asserts handler logic without going through the goroutine wrapper. It does not call `StartSession` and does not use `bogusRequest()`: the current helpers exercise the probe failure path, not natural EOF.

The wrapper itself is covered by a second test that drives EOF through the real goroutine path with an injected `newPlane` factory. Use `startPlaneLocked` directly so the test stays hermetic and does not need a fake ffprobe seam:

```go
func TestManager_NaturalEOFViaStartPlaneLockedFiresOnStop(t *testing.T) {
	prevNewPlane := newPlane
	t.Cleanup(func() { newPlane = prevNewPlane })

	plane := &fakePlane{}
	newPlane = func(dataplane.PlaneConfig) planeRunner { return plane }

	m := newTestManager(t)
	done := make(chan string, 1)
	req := SessionRequest{
		StreamURL:  "https://example.test/video.mp4",
		DirectPlay: true,
		OnStop:     func(reason string) { done <- reason },
	}
	probe := &ffmpeg.ProbeResult{Width: 1280, Height: 720, FrameRate: 29.97, AudioRate: 48000, Duration: 1}

	m.mu.Lock()
	if err := m.startPlaneLocked(req, 0, probe, nil, "ffmpeg"); err != nil {
		m.mu.Unlock()
		t.Fatalf("startPlaneLocked: %v", err)
	}
	if err := m.fsm.Transition(EvPlayMedia); err != nil {
		m.mu.Unlock()
		t.Fatalf("transition play media: %v", err)
	}
	m.mu.Unlock()

	select {
	case got := <-done:
		if got != "eof" {
			t.Fatalf("OnStop reason = %q, want eof", got)
		}
	case <-time.After(time.Second):
		t.Fatal("OnStop was not called via goroutine wrapper")
	}
	if st := m.Status(); st.State != StateIdle {
		t.Fatalf("state after EOF = %s, want %s", st.State, StateIdle)
	}
}
```

This test ensures that a future refactor which drops `m.handlePlaneExit(plane, runErr)` from the goroutine body fails CI rather than silently regressing.

- [ ] **Step 2: Run the failing tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/core -run "TestManager_NaturalEOF" -count=1
```

Expected: FAIL with `cannot use plane as *dataplane.Plane`, `m.handlePlaneExit undefined`, or `newPlane undefined`. The failures prove the test seam, EOF exit path, and `newPlane` injection point do not exist yet.

- [ ] **Step 3: Implement EOF handling with existing guards**

In `internal/core/manager.go`, introduce a narrow plane interface and move the goroutine body into a helper so tests can drive the EOF branch without ffmpeg/ffprobe:

```go
type planeRunner interface {
	Run(context.Context) error
	Done() <-chan struct{}
	Position() time.Duration
	SetFieldOrder(string) error
}

var newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
	return dataplane.NewPlane(cfg)
}
```

Change `Manager.plane` from `*dataplane.Plane` to `planeRunner`, change `plane := dataplane.NewPlane(...)` to `plane := newPlane(...)`, and add:

```go
func (m *Manager) handlePlaneExit(plane planeRunner, runErr error) {
	m.logPlaneExit(runErr)

	var onStop func(string)
	var subtitlePath string
	reason := "error"

	m.mu.Lock()
	if m.plane != plane {
		m.mu.Unlock()
		return
	}
	m.plane = nil
	switch {
	case runErr == nil:
		reason = "eof"
		_ = m.fsm.Transition(EvEOF)
		if m.active != nil {
			onStop = m.active.req.OnStop
			subtitlePath = m.active.req.SubtitlePath
			m.active = nil
			m.cancelFn = nil
		}
	case errors.Is(runErr, context.Canceled):
		reason = ""
	default:
		_ = m.fsm.Transition(EvError)
		if m.active != nil {
			onStop = m.active.req.OnStop
			subtitlePath = m.active.req.SubtitlePath
			m.active = nil
			m.cancelFn = nil
		}
	}
	m.mu.Unlock()

	removeSubtitleFile(subtitlePath)
	if reason != "" {
		notifySessionStop(onStop, reason)
	}
}
```

Then replace the goroutine body with:

```go
go func() {
	runErr := plane.Run(ctx)
	m.handlePlaneExit(plane, runErr)
}()
```

Rationale on ordering: `m.plane = nil` runs unconditionally before the `switch` on `runErr` so that a Pause (`context.Canceled`) leaves `m.active` intact for `Play()` to reattach a fresh plane, while EOF and Error tear `m.active` down inside the matching switch arms. This mirrors the current code's behavior — `m.plane = nil` is already global to all exit paths in the existing implementation; only `m.active` is per-branch.

Keep the existing `m.plane != plane` guard. Keep pause cancellation silent by leaving `reason == ""`.

- [ ] **Step 4: Run lifecycle tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/core -run "TestManager_(NaturalEOF|Pause|Error|Stop|Preempt)" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/manager.go internal/core/manager_test.go
git commit -m "fix(core): fire eof stop callbacks"
```

---

## Task 2: Streams Adapter Skeleton And Config

**Files:**
- Create: `internal/adapters/streams/adapter.go`
- Create: `internal/adapters/streams/config.go`
- Create: `internal/adapters/streams/provider.go`
- Create: `internal/adapters/streams/queue.go`
- Create: `internal/adapters/streams/refresh.go`
- Create: `internal/adapters/streams/adapter_test.go`
- Create: `internal/adapters/streams/config_test.go`

**Why:** Establish the adapter boundary, TOML defaults, validation, current values, field scopes, and route-provider conformance before any provider logic exists.

- [ ] **Step 1: Write config and interface tests**

Create `internal/adapters/streams/config_test.go`:

```go
package streams

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Enabled {
		t.Fatal("streams must default disabled")
	}
	if cfg.MaxManifestBytes != 1048576 {
		t.Fatalf("MaxManifestBytes = %d", cfg.MaxManifestBytes)
	}
	if cfg.YoutubeFormat == "" {
		t.Fatal("YoutubeFormat must have an SD-biased default")
	}
}

func TestConfigValidateRejectsBadRanges(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxManifestBytes = 0
	cfg.CatalogRequestTimeoutSeconds = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate succeeded with invalid byte/timeout ranges")
	}
}

func TestScopeForField(t *testing.T) {
	cases := map[string]adapters.ApplyScope{
		"enabled":                                      adapters.ScopeHotSwap,
		"manifest_url":                                 adapters.ScopeHotSwap,
		"manifest_refresh_hours":                       adapters.ScopeHotSwap,
		"catalog_refresh_hours":                        adapters.ScopeHotSwap,
		"max_manifest_bytes":                           adapters.ScopeHotSwap,
		"max_catalog_bytes":                            adapters.ScopeHotSwap,
		"max_items_per_channel":                        adapters.ScopeHotSwap,
		"max_consecutive_failures":                     adapters.ScopeHotSwap,
		"manifest_request_timeout_seconds":             adapters.ScopeHotSwap,
		"catalog_request_timeout_seconds":              adapters.ScopeHotSwap,
		"youtube_format":                               adapters.ScopeRestartCast,
		"allow_remote_manifest":                        adapters.ScopeHotSwap,
		"allow_cached_remote_manifest":                 adapters.ScopeHotSwap,
		"allow_local_manifest_urls":                    adapters.ScopeHotSwap,
		"remote_provider_allowed_hosts":                adapters.ScopeHotSwap,
		"providers.mtv-rewind.disabled":                adapters.ScopeHotSwap,
		"providers.mtv-rewind.catalog_refresh_hours": adapters.ScopeHotSwap,
	}
	for key, want := range cases {
		got, ok := scopeForField(key)
		if !ok {
			t.Fatalf("scopeForField(%q) not found", key)
		}
		if got != want {
			t.Fatalf("scopeForField(%q) = %v, want %v", key, got, want)
		}
	}
	if _, ok := scopeForField("youtub_format"); ok {
		t.Fatal("unknown field should not receive an implicit scope")
	}
}
```

Create `internal/adapters/streams/adapter_test.go`:

```go
package streams

import (
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

func TestAdapterInterfaces(t *testing.T) {
	var _ adapters.Adapter = (*Adapter)(nil)
	var _ adapters.Validator = (*Adapter)(nil)
	var _ adapters.RouteProvider = (*Adapter)(nil)
	var _ ui.ValueProvider = (*Adapter)(nil)
	var _ ui.ExtraHTMLProvider = (*Adapter)(nil)
}

func TestDecodeConfigDefaults(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var raw toml.Primitive
	var meta toml.MetaData
	if err := a.DecodeConfig(raw, meta); err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if a.IsEnabled() {
		t.Fatal("decoded default should be disabled")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -count=1
```

Expected: FAIL because package `streams` does not exist.

- [ ] **Step 3: Implement config and adapter skeleton**

Create `internal/adapters/streams/config.go` with these public shapes:

```go
package streams

import (
	"fmt"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type Config struct {
	Enabled                       bool                      `toml:"enabled"`
	ManifestURL                   string                    `toml:"manifest_url"`
	ManifestRefreshHours          int                       `toml:"manifest_refresh_hours"`
	CatalogRefreshHours           int                       `toml:"catalog_refresh_hours"`
	MaxManifestBytes              int64                     `toml:"max_manifest_bytes"`
	MaxCatalogBytes               int64                     `toml:"max_catalog_bytes"`
	MaxItemsPerChannel            int                       `toml:"max_items_per_channel"`
	MaxConsecutiveFailures        int                       `toml:"max_consecutive_failures"`
	ManifestRequestTimeoutSeconds int                       `toml:"manifest_request_timeout_seconds"`
	CatalogRequestTimeoutSeconds  int                       `toml:"catalog_request_timeout_seconds"`
	YoutubeFormat                 string                    `toml:"youtube_format"`
	AllowRemoteManifest           bool                      `toml:"allow_remote_manifest"`
	AllowCachedRemoteManifest     bool                      `toml:"allow_cached_remote_manifest"`
	AllowLocalManifestURLs        bool                      `toml:"allow_local_manifest_urls"`
	RemoteProviderAllowedHosts    []string                  `toml:"remote_provider_allowed_hosts"`
	Providers                     map[string]ProviderConfig `toml:"providers"`
}

type ProviderConfig struct {
	Disabled            bool `toml:"disabled"`
	CatalogRefreshHours int  `toml:"catalog_refresh_hours"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:                       false,
		ManifestURL:                   "https://raw.githubusercontent.com/idio-sync/MiSTer_GroovyRelay/main/docs/streams/providers.json",
		ManifestRefreshHours:          24,
		CatalogRefreshHours:           12,
		MaxManifestBytes:              1048576,
		MaxCatalogBytes:               10485760,
		MaxItemsPerChannel:            5000,
		MaxConsecutiveFailures:        5,
		ManifestRequestTimeoutSeconds: 10,
		CatalogRequestTimeoutSeconds:  20,
		YoutubeFormat:                 "bv*[height<=480]+ba/b[height<=480]/bv*+ba/b",
		AllowRemoteManifest:           true,
		AllowCachedRemoteManifest:     false,
		AllowLocalManifestURLs:        false,
		RemoteProviderAllowedHosts:    nil,
		Providers:                     map[string]ProviderConfig{},
	}
}

func (c *Config) Validate() error {
	var errs adapters.FieldErrors
	if c.ManifestRefreshHours < 1 || c.ManifestRefreshHours > 168 {
		errs = append(errs, adapters.FieldError{Key: "manifest_refresh_hours", Msg: "must be in [1, 168]"})
	}
	if c.CatalogRefreshHours < 1 || c.CatalogRefreshHours > 168 {
		errs = append(errs, adapters.FieldError{Key: "catalog_refresh_hours", Msg: "must be in [1, 168]"})
	}
	if c.MaxManifestBytes < 1024 || c.MaxManifestBytes > 8*1024*1024 {
		errs = append(errs, adapters.FieldError{Key: "max_manifest_bytes", Msg: "must be in [1024, 8388608]"})
	}
	if c.MaxCatalogBytes < 1024 || c.MaxCatalogBytes > 64*1024*1024 {
		errs = append(errs, adapters.FieldError{Key: "max_catalog_bytes", Msg: "must be in [1024, 67108864]"})
	}
	if c.MaxItemsPerChannel < 1 || c.MaxItemsPerChannel > 50000 {
		errs = append(errs, adapters.FieldError{Key: "max_items_per_channel", Msg: "must be in [1, 50000]"})
	}
	if c.MaxConsecutiveFailures < 1 || c.MaxConsecutiveFailures > 100 {
		errs = append(errs, adapters.FieldError{Key: "max_consecutive_failures", Msg: "must be in [1, 100]"})
	}
	if c.ManifestRequestTimeoutSeconds < 1 || c.ManifestRequestTimeoutSeconds > 60 {
		errs = append(errs, adapters.FieldError{Key: "manifest_request_timeout_seconds", Msg: "must be in [1, 60]"})
	}
	if c.CatalogRequestTimeoutSeconds < 1 || c.CatalogRequestTimeoutSeconds > 120 {
		errs = append(errs, adapters.FieldError{Key: "catalog_request_timeout_seconds", Msg: "must be in [1, 120]"})
	}
	if strings.TrimSpace(c.YoutubeFormat) == "" {
		errs = append(errs, adapters.FieldError{Key: "youtube_format", Msg: "must not be empty"})
	}
	if _, err := normalizeHostSet(c.RemoteProviderAllowedHosts); err != nil {
		errs = append(errs, adapters.FieldError{
			Key: "remote_provider_allowed_hosts",
			Msg: err.Error(),
		})
	}
	return errs.Err()
}

var fieldScopes = map[string]adapters.ApplyScope{
	"enabled":                         adapters.ScopeHotSwap,
	"manifest_url":                    adapters.ScopeHotSwap,
	"manifest_refresh_hours":          adapters.ScopeHotSwap,
	"catalog_refresh_hours":           adapters.ScopeHotSwap,
	"max_manifest_bytes":              adapters.ScopeHotSwap,
	"max_catalog_bytes":               adapters.ScopeHotSwap,
	"max_items_per_channel":           adapters.ScopeHotSwap,
	"max_consecutive_failures":        adapters.ScopeHotSwap,
	"manifest_request_timeout_seconds": adapters.ScopeHotSwap,
	"catalog_request_timeout_seconds":  adapters.ScopeHotSwap,
	"youtube_format":                  adapters.ScopeRestartCast,
	"allow_remote_manifest":           adapters.ScopeHotSwap,
	"allow_cached_remote_manifest":    adapters.ScopeHotSwap,
	"allow_local_manifest_urls":       adapters.ScopeHotSwap,
	"remote_provider_allowed_hosts":   adapters.ScopeHotSwap,
}

func scopeForField(key string) (adapters.ApplyScope, bool) {
	if scope, ok := fieldScopes[key]; ok {
		return scope, true
	}
	if strings.HasPrefix(key, "providers.") &&
		(strings.HasSuffix(key, ".disabled") || strings.HasSuffix(key, ".catalog_refresh_hours")) {
		return adapters.ScopeHotSwap, true
	}
	return 0, false
}

func normalizeHostSet(in []string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	for _, h := range in {
		normalized, err := normalizeConfigHost(h)
		if err != nil {
			return nil, fmt.Errorf("invalid hostname %q: %w", h, err)
		}
		if normalized != "" {
			out[normalized] = struct{}{}
		}
	}
	return out, nil
}
```

Add `normalizeConfigHost` tests before implementing it:

```go
func TestNormalizeConfigHost(t *testing.T) {
	cases := map[string]string{
		"Example.COM.":           "example.com",
		"https://Example.COM:443/path": "example.com",
		"bücher.example":         "xn--bcher-kva.example",
	}
	for in, want := range cases {
		got, err := normalizeConfigHost(in)
		if err != nil {
			t.Fatalf("normalizeConfigHost(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("normalizeConfigHost(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"*.example.com", "http://user@example.com", "127.0.0.1"} {
		if got, err := normalizeConfigHost(bad); err == nil {
			t.Fatalf("normalizeConfigHost(%q) = %q, want error", bad, got)
		}
	}
}
```

Implement `normalizeConfigHost` with `net/url`, `net/netip`, and `golang.org/x/net/idna`: accept either a bare hostname or URL-like value, strip default `:443` for HTTPS and `:80` for HTTP, reject userinfo, wildcard hosts, empty hosts, and IP literals, lower-case, trim a trailing dot, and convert to ASCII with `idna.Lookup.ToASCII`.

Create `internal/adapters/streams/adapter.go` with the adapter shell, fields, `DecodeConfig`, `Validate`, `ApplyConfig`, `CurrentValues`, `SetEnabled`, `Start`, `Stop`, and `Status`. Keep `Start` lightweight in this task: no network/catalog refresh, just state and resolver construction:

```go
type AdapterConfig struct {
	Bridge        config.BridgeConfig
	Core          SessionManager
	YTDLPResolver ytdlp.BinaryResolver // binary-path resolver, mirrors url.AdapterConfig
}

type SessionManager interface {
	StartSession(core.SessionRequest) error
	Stop() error
	Status() core.SessionStatus
}

// streamResolver is the adapter's narrow view of *ytdlp.Resolver. Lets
// playback_test.go inject a stub without spinning up exec. Mirrors the
// resolverIface pattern in internal/adapters/url/adapter.go.
type streamResolver interface {
	Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*ytdlp.Resolution, error)
}

const defaultYTDLPResolveTimeout = 30 * time.Second
```

Declare the `Adapter` struct now with every private field that later tasks reference, so each task can compile independently:

```go
type Adapter struct {
	core        SessionManager
	cookiesPath string
	ytdlpBinary ytdlp.BinaryResolver
	// Assigned in Start() from ytdlpBinary; tests overwrite directly.
	resolver streamResolver
	// Injectable in refresh tests.
	fetchManifest func(context.Context) (Manifest, CacheMetadata, error)
	// Injectable in refresh tests.
	refreshOnce func(ctx context.Context, reason string) RefreshStatus

	mu          sync.Mutex
	cfg         Config
	state       adapters.State
	lastErr     string
	stateSince  time.Time
	definitions map[string]ProviderDefinition
	catalogs    map[string]ProviderCatalog
	active      *ActiveQueue

	loopCtx    context.Context
	loopCancel context.CancelFunc
	loopDone   chan struct{}
}
```

Create `internal/adapters/streams/provider.go` with the minimal compile-time shells that `Adapter` references in this task. Later tasks expand these same definitions as their failing tests require more fields:

```go
package streams

type Manifest struct{}
type ProviderDefinition struct{}
type ProviderCatalog struct{}
type CacheMetadata struct{}
```

Create `internal/adapters/streams/queue.go` with the active queue shell used by the adapter:

```go
package streams

type ActiveQueue struct {
	ProviderID string
	ChannelID  string
}
```

Create `internal/adapters/streams/refresh.go` with the refresh status shell used by the injectable `refreshOnce` field:

```go
package streams

import "time"

type RefreshStatus struct {
	ProviderID string
	Source     string
	FetchedAt  time.Time
	Err        error
}
```

In `New`, store the `AdapterConfig.YTDLPResolver` value on `a.ytdlpBinary`. In `Start()`, when `a.ytdlpBinary != nil`, build this resolver and assign it to `a.resolver`:

```go
a.resolver = &ytdlp.Resolver{
	BinaryResolver: a.ytdlpBinary,
	Timeout:        defaultYTDLPResolveTimeout,
	Runner:         ytdlp.OSRunner{},
}
```

This mirrors the construction pattern in `internal/adapters/url/adapter.go:206` and `:310`, with a fixed 30 second timeout because the accepted streams spec does not define a separate yt-dlp timeout field.

- [ ] **Step 4: Run tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams
git commit -m "feat(streams): add adapter config skeleton"
```

---

## Task 3: Manifest, Cache, Fetch Security

**Files:**
- Modify: `internal/adapters/streams/provider.go`
- Create: `internal/adapters/streams/manifest.go`
- Create: `internal/adapters/streams/cache.go`
- Create: `internal/adapters/streams/fetch.go`
- Create: `internal/adapters/streams/manifest_test.go`
- Create: `internal/adapters/streams/cache_test.go`
- Create: `internal/adapters/streams/fetch_test.go`

**Why:** Lock down the dangerous part first: remote manifests and catalogs can influence network fetches, so schema validation and SSRF defenses must exist before playback.

- [ ] **Step 1: Write manifest validation tests**

Create `internal/adapters/streams/manifest_test.go`:

```go
func TestValidateManifestRejectsUnsupportedVersion(t *testing.T) {
	m := Manifest{Version: 2}
	if err := validateManifest(m, DefaultConfig()); err == nil {
		t.Fatal("unsupported version accepted")
	}
}

func TestValidateManifestRejectsReservedAdhocChannel(t *testing.T) {
	m := validManifestForTest()
	m.Providers[0].Channels = []ChannelDefinition{{ID: "adhoc", Name: "Bad"}}
	if err := validateManifest(m, DefaultConfig()); err == nil {
		t.Fatal("reserved channel ID accepted")
	}
}

func TestValidateManifestRejectsReservedAdhocProviderID(t *testing.T) {
	// "adhoc" is the synthetic channel ID used for ?v=<id> single-item
	// queues; reserving it as a provider ID too prevents stable-key
	// collisions in the queue model.
	m := validManifestForTest()
	m.Providers[0].ID = "adhoc"
	if err := validateManifest(m, DefaultConfig()); err == nil {
		t.Fatal("reserved provider ID accepted")
	}
}

func TestMergeManifestsRemoteUnknownTypeIgnored(t *testing.T) {
	bundled := validManifestForTest()
	remote := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID: "remote-x", Type: "unknown", DisplayName: "Nope",
	}}}
	got := mergeManifests(DefaultConfig(), bundled, nil, &remote, map[string]ProviderFactory{
		"youtube-channel-json": nil,
	})
	if _, ok := got.Provider("remote-x"); ok {
		t.Fatal("unknown remote provider type should be ignored")
	}
}
```

Add these helpers to the same file so the tests are self-contained:

```go
func validManifestForTest() Manifest {
	return Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID:              "mtv-rewind",
		Type:            "youtube-channel-json",
		DisplayName:     "MTV Rewind",
		BaseURL:         "https://wantmymtv.vercel.app",
		PlaylistURL:     "https://wantmymtv.vercel.app/public/mtv-playlists.json",
		DefaultChannel:  "metal",
		DefaultPlayMode: PlayShuffle,
		URLRules: []URLRule{{
			ID:         "mtv-player-channel",
			Schemes:    []string{"https"},
			Hosts:      []string{"wantmymtv.vercel.app"},
			Path:       "/player.html",
			Target:     "channel",
			QueryParam: "channel",
		}},
		Channels: []ChannelDefinition{{ID: "metal", Name: "Metal", PlayMode: PlayShuffle}},
	}}}
}
```

- [ ] **Step 2: Write cache correctness tests**

Create `internal/adapters/streams/cache_test.go`:

```go
func TestCacheReadRejectsChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := writeCacheFile(dir, "manifest", []byte(`{"version":1}`), CacheMetadata{
		Schema:    1,
		SourceURL: "https://example.test/providers.json",
		SHA256:    "not-the-real-digest",
	}); err != nil {
		t.Fatalf("writeCacheFile: %v", err)
	}
	if _, _, err := readCacheFile(dir, "manifest"); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
}

func TestCacheReadRejectsCorruptMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.meta.json"), []byte(`{bad json`), 0o600); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if _, _, err := readCacheFile(dir, "manifest"); err == nil {
		t.Fatal("corrupt metadata accepted")
	}
}

func TestCacheWriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	if err := writeCacheFile(dir, "catalog-mtv-rewind", []byte(`{"metal":["dQw4w9WgXcQ"]}`), CacheMetadata{
		Schema:    1,
		SourceURL: "https://wantmymtv.vercel.app/public/mtv-playlists.json",
	}); err != nil {
		t.Fatalf("writeCacheFile: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v", matches)
	}
}

func TestCachedRemoteManifestIgnoredWhenDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowRemoteManifest = false
	cfg.AllowCachedRemoteManifest = false
	bundled := validManifestForTest()
	cached := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID: "cached-provider", Type: "youtube-channel-json", DisplayName: "Cached",
	}}}
	got := mergeManifests(cfg, bundled, &cached, nil, map[string]ProviderFactory{
		"youtube-channel-json": nil,
	})
	if _, ok := got.Provider("cached-provider"); ok {
		t.Fatal("cached remote manifest should be ignored")
	}
}
```

- [ ] **Step 3: Write fetch security tests**

Create `internal/adapters/streams/fetch_test.go`:

```go
func TestFetchRejectsLoopbackByDefault(t *testing.T) {
	f := secureFetcher{client: http.DefaultClient, resolver: staticResolver{"example.test": []string{"127.0.0.1"}}}
	_, err := f.Fetch(t.Context(), "https://example.test/catalog.json", fetchLimits{MaxBytes: 1024})
	if err == nil {
		t.Fatal("loopback target accepted")
	}
}

func TestFetchRejectsIPv6PrivateRanges(t *testing.T) {
	for _, addr := range []string{"::1", "::", "fc00::1", "fe80::1", "ff00::1", "::ffff:192.168.1.2"} {
		t.Run(addr, func(t *testing.T) {
			if isPublicIP(netip.MustParseAddr(addr)) {
				t.Fatalf("%s classified public", addr)
			}
		})
	}
}

func TestFetchRejectsRedirectToPrivateIP(t *testing.T) {
	f := secureFetcher{
		resolver: sequenceResolver{
			"public.example":  {[]string{"203.0.113.10"}},
			"private.example": {[]string{"192.168.1.10"}},
		},
		transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Hostname() != "public.example" {
				t.Fatalf("unexpected first request host %s", req.URL.Host)
			}
			return redirectResponse(req, "https://private.example/catalog.json"), nil
		}),
	}
	_, err := f.Fetch(t.Context(), "https://public.example/catalog.json", fetchLimits{MaxBytes: 1024})
	if err == nil {
		t.Fatal("redirect revalidation did not reject private target")
	}
}

func TestFetchResolvesOnceAndDialsValidatedIP(t *testing.T) {
	resolver := &rebindResolver{
		first:  []string{"203.0.113.10"},
		second: []string{"192.168.1.20"},
	}
	dialer := &recordingDialer{}
	f := secureFetcher{resolver: resolver, dialContext: dialer.DialContext}
	_, _ = f.Fetch(t.Context(), "https://media.example/catalog.json", fetchLimits{MaxBytes: 1024})
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
	if !strings.Contains(dialer.addr, "203.0.113.10") {
		t.Fatalf("dialed %q, want validated IP 203.0.113.10", dialer.addr)
	}
}

func TestFetchRemoteProviderRespectsHostAllowlistAfterRedirect(t *testing.T) {
	// Spec §"RemoteProviderAllowedHosts": when the allowlist is non-empty,
	// remote-added providers may fetch catalogs only from those hosts after
	// redirect resolution. Allowlist must re-apply on each redirect hop.
	allow, err := normalizeHostSet([]string{"trusted.example"})
	if err != nil {
		t.Fatalf("normalizeHostSet: %v", err)
	}
	f := secureFetcher{
		resolver: staticResolver{
			"trusted.example":   []string{"203.0.113.10"},
			"untrusted.example": []string{"203.0.113.20"},
		},
		transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Hostname() == "trusted.example" {
				return redirectResponse(req, "https://untrusted.example/catalog.json"), nil
			}
			t.Fatalf("untrusted host should never be fetched, got %s", req.URL.Host)
			return nil, nil
		}),
	}
	_, err = f.Fetch(t.Context(), "https://trusted.example/catalog.json", fetchLimits{
		MaxBytes:     1024,
		AllowedHosts: allow,
	})
	if err == nil {
		t.Fatal("redirect to non-allowlisted host should be rejected")
	}
}
```

Add these helpers to `internal/adapters/streams/fetch_test.go`:

```go
type staticResolver map[string][]string

func (r staticResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if ips, ok := r[host]; ok {
		return ips, nil
	}
	return nil, fmt.Errorf("unexpected lookup for %s", host)
}

type sequenceResolver map[string][][]string

func (r sequenceResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	seq := r[host]
	if len(seq) == 0 {
		return nil, fmt.Errorf("unexpected lookup for %s", host)
	}
	out := seq[0]
	r[host] = seq[1:]
	return out, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func redirectResponse(req *http.Request, loc string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{loc}},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}
}

type rebindResolver struct {
	first  []string
	second []string
	calls  int
}

func (r *rebindResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	r.calls++
	if r.calls == 1 {
		return r.first, nil
	}
	return r.second, nil
}

type recordingDialer struct {
	addr string
}

func (d *recordingDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d.addr = addr
	return nil, errors.New("stop after recording dial address")
}
```

- [ ] **Step 4: Implement schema, merge, cache, and fetch**

Implement these exported/internal types in `provider.go`:

```go
type Manifest struct {
	Version   int                  `json:"version"`
	Providers []ProviderDefinition `json:"providers"`
}

type ProviderDefinition struct {
	ID                  string              `json:"id"`
	Type                string              `json:"type"`
	DisplayName         string              `json:"display_name"`
	BaseURL             string              `json:"base_url"`
	PlaylistURL         string              `json:"playlist_url"`
	URLRules            []URLRule           `json:"url_rules"`
	DefaultChannel      string              `json:"default_channel"`
	DefaultPlayMode     PlayMode            `json:"default_play_mode"`
	CatalogRefreshHours *int                `json:"catalog_refresh_hours,omitempty"`
	Groups              []GroupDefinition   `json:"groups"`
	Channels            []ChannelDefinition `json:"channels"`
}

type URLRule struct {
	ID         string   `json:"id"`
	Schemes    []string `json:"schemes"`
	Hosts      []string `json:"hosts"`
	Path       string   `json:"path,omitempty"`
	PathPrefix string   `json:"path_prefix,omitempty"`
	Target     string   `json:"target"`
	QueryParam string   `json:"query_param,omitempty"`
}
```

Implement fetch security in `fetch.go` with:

```go
var deniedIPv4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),  // CGNAT (RFC 6598)
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"),
}

var deniedIPv6 = []netip.Prefix{
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
	netip.MustParsePrefix("::ffff:0:0/96"),
}
```

The fetcher must resolve once, dial the validated IP, preserve host for SNI/Host, cap redirects at three, reject HTTPS-to-HTTP downgrade unless local URLs are allowed, and wrap response bodies with `http.MaxBytesReader` or `io.LimitReader` plus an over-limit check. Give `secureFetcher` injectable `resolver`, `transport`, and `dialContext` fields so DNS rebinding and redirect behavior are testable without real external network.

- [ ] **Step 5: Run manifest/cache/fetch tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "Test(ValidateManifest|MergeManifests|Fetch|Cache)" -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/streams
git commit -m "feat(streams): secure provider manifests"
```

---

## Task 4: YouTube Channel JSON Provider And Bundled Seeds

**Files:**
- Create: `internal/adapters/streams/provider_youtube_channel_json.go`
- Create: `internal/adapters/streams/assets.go`
- Create: `internal/adapters/streams/provider_youtube_channel_json_test.go`
- Create: `internal/adapters/streams/testdata/mtv-playlists.seed.json`
- Create: `internal/adapters/streams/testdata/cartoon-playlists.seed.json`

**Why:** Prove the provider model without URL handoff or playback. This task converts playlist JSON into normalized channels/items using bundled metadata and seed catalogs.

- [ ] **Step 1: Add seed catalogs**

Create `internal/adapters/streams/testdata/mtv-playlists.seed.json`:

```json
{
  "metal": ["dQw4w9WgXcQ", "9bZkp7q19f0"],
  "1stday": ["3JZ_D3ELwOQ"],
  "all": ["dQw4w9WgXcQ", "9bZkp7q19f0", "3JZ_D3ELwOQ"]
}
```

Create `internal/adapters/streams/testdata/cartoon-playlists.seed.json`:

```json
{
  "heman": ["dQw4w9WgXcQ", "9bZkp7q19f0"],
  "all": ["dQw4w9WgXcQ", "9bZkp7q19f0"],
  "commercials": ["3JZ_D3ELwOQ"]
}
```

- [ ] **Step 2: Write provider tests**

Create `internal/adapters/streams/provider_youtube_channel_json_test.go`:

```go
func TestYouTubeChannelJSONBuildsCatalog(t *testing.T) {
	def := bundledMTVDefinition()
	raw := []byte(`{"metal":["dQw4w9WgXcQ"],"unknown":["9bZkp7q19f0"]}`)
	cat, err := buildYouTubeChannelCatalog(def, raw, DefaultConfig())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if cat.ProviderID != "mtv-rewind" {
		t.Fatalf("ProviderID = %q", cat.ProviderID)
	}
	if got := cat.Channel("metal").Items[0].URL; got != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("item URL = %q", got)
	}
	if cat.Channel("unknown").GroupID != "ungrouped" {
		t.Fatal("unknown channel should move to ungrouped")
	}
}

func TestYouTubeChannelJSONSkipsMalformedIDs(t *testing.T) {
	def := bundledMTVDefinition()
	raw := []byte(`{"metal":["not a youtube id","dQw4w9WgXcQ"]}`)
	cat, err := buildYouTubeChannelCatalog(def, raw, DefaultConfig())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := len(cat.Channel("metal").Items); got != 1 {
		t.Fatalf("items = %d, want 1", got)
	}
}

func TestCartoonCommercialsExcludedFromAll(t *testing.T) {
	def := bundledCartoonDefinition()
	raw := []byte(`{"all":["dQw4w9WgXcQ"],"commercials":["9bZkp7q19f0"]}`)
	cat, err := buildYouTubeChannelCatalog(def, raw, DefaultConfig())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if cat.Channel("commercials") != nil {
		t.Fatal("commercials should not be exposed as a normal v1 channel")
	}
}
```

- [ ] **Step 3: Implement bundled definitions and parser**

In `assets.go`, embed seed files:

```go
//go:embed testdata/*.seed.json
var seedFS embed.FS
```

Add `bundledManifest()` returning MTV and Cartoon provider definitions with URL rules from the spec. In `provider_youtube_channel_json.go`, parse `map[string][]string`, validate YouTube IDs with:

```go
var youtubeIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
```

Build `ProviderCatalog` with deterministic group/channel ordering by `Order`, then `Name`, then `ID`.

- [ ] **Step 4: Run provider tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "TestYouTube|TestCartoon" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams
git commit -m "feat(streams): add youtube channel provider"
```

---

## Task 5: URL Rule Resolution And URL Adapter Handoff

**Files:**
- Create: `internal/adapters/streamhandoff/handoff.go`
- Create: `internal/adapters/streams/errors.go`
- Create: `internal/adapters/streams/url_resolver.go`
- Create: `internal/adapters/streams/url_resolver_test.go`
- Modify: `internal/adapters/url/adapter.go`
- Modify: `internal/adapters/url/play.go`
- Modify: `internal/adapters/url/play_test.go`

**Why:** The highest-priority user workflow is source compatibility: `https://wantmymtv.vercel.app/player.html?channel=metal` must route to streams before the generic URL adapter tries direct or yt-dlp playback.

- [ ] **Step 1: Write resolver tests**

Create `internal/adapters/streams/url_resolver_test.go`:

```go
func TestResolveStreamURL_MTVChannel(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	res, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=metal")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if !matched || res.ProviderID != "mtv-rewind" || res.ChannelID != "metal" {
		t.Fatalf("resolution = %+v matched=%v", res, matched)
	}
}

func TestResolveStreamURL_MTVShortVideo(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	res, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/s/dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if !matched || res.ItemID != "dQw4w9WgXcQ" {
		t.Fatalf("resolution = %+v matched=%v", res, matched)
	}
}

func TestResolveStreamURLRejectsRepeatedQueryParam(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	_, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=metal&channel=80s")
	if !matched || err == nil {
		t.Fatal("repeated channel param should match provider and return validation error")
	}
	// Spec §"URL rule grammar": match-but-invalid must be a typed streams
	// error so the URL adapter (Task 5) can distinguish "matched but
	// extraction failed" from "no match" and refuse to fall through to
	// generic URL casting. Plain errors.New(...) is not enough.
	var streamsErr *StreamsError
	if !errors.As(err, &streamsErr) {
		t.Fatalf("error type = %T, want *StreamsError", err)
	}
}
```

Create `internal/adapters/streams/errors.go` with:

```go
package streams

type ErrorKind string

const (
	ErrKindNoMatch           ErrorKind = "no_match"
	ErrKindInvalidExtraction ErrorKind = "invalid_extraction"
	ErrKindProviderDisabled  ErrorKind = "provider_disabled"
)

type StreamsError struct {
	Kind    ErrorKind
	Message string
}

func (e *StreamsError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}
```

The URL adapter (Task 5 Step 3) checks the discriminator to decide whether to fall through.

Add this helper to `internal/adapters/streams/test_helpers_test.go`:

```go
func newTestAdapterWithCatalog(t *testing.T) *Adapter {
	t.Helper()
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mtvDef := bundledMTVDefinition()
	cartoonDef := bundledCartoonDefinition()
	mtvCat := ProviderCatalog{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels: []Channel{{
			ID:       "metal",
			Name:     "Metal",
			PlayMode: PlayShuffle,
			Items: []StreamItem{{
				ID:       "dQw4w9WgXcQ",
				SourceID: "dQw4w9WgXcQ",
				URL:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			}},
		}},
	}
	cartoonCat := ProviderCatalog{
		ProviderID: "cartoon-rewind",
		Name:       "Cartoon Rewind",
		Channels: []Channel{{
			ID:       "heman",
			Name:     "He-Man",
			PlayMode: PlayShuffle,
			Items: []StreamItem{{
				ID:       "9bZkp7q19f0",
				SourceID: "9bZkp7q19f0",
				URL:      "https://www.youtube.com/watch?v=9bZkp7q19f0",
			}},
		}},
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{mtvDef, cartoonDef})
	a.replaceCatalogsForTest([]ProviderCatalog{mtvCat, cartoonCat})
	return a
}
```

Implement `replaceCatalogsForTest` as a test-only method in `test_helpers_test.go` if the production catalog store is private:

```go
func (a *Adapter) replaceCatalogsForTest(cats []ProviderCatalog) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.catalogs = map[string]ProviderCatalog{}
	for _, cat := range cats {
		a.catalogs[cat.ProviderID] = cat
	}
}
```

Also add:

```go
func (a *Adapter) replaceDefinitionsForTest(defs []ProviderDefinition) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definitions = map[string]ProviderDefinition{}
	for _, def := range defs {
		a.definitions[def.ID] = def
	}
}
```

- [ ] **Step 2: Write URL adapter handoff tests**

In `internal/adapters/url/play_test.go`, add a fake stream resolver and tests:

```go
type fakeStreamResolver struct {
	matched bool
	res     streamhandoff.Resolution
	err     error
	starts  int
}

func (f *fakeStreamResolver) ResolveStreamURL(ctx context.Context, rawURL string) (streamhandoff.Resolution, bool, error) {
	return f.res, f.matched, f.err
}

func (f *fakeStreamResolver) StartResolvedStream(ctx context.Context, res streamhandoff.Resolution) (streamhandoff.StartResult, error) {
	f.starts++
	return streamhandoff.StartResult{AdapterRef: res.AdapterRef, ProviderID: res.ProviderID, ChannelID: res.ChannelID, ItemID: res.ItemID}, nil
}

func TestCastURL_StreamsHandoffBeforeYTDLP(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	f := &fakeStreamResolver{matched: true, res: streamhandoff.Resolution{AdapterRef: "streams:1", ProviderID: "mtv-rewind", ChannelID: "metal"}}
	a.SetStreamResolver(f)
	ref, via, status, err := a.castURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=metal", "auto")
	if err != nil {
		t.Fatalf("castURL: %v", err)
	}
	if ref != "streams:1" || via != "streams" || status != http.StatusOK || f.starts != 1 {
		t.Fatalf("ref=%q via=%q status=%d starts=%d", ref, via, status, f.starts)
	}
}
```

- [ ] **Step 3: Implement neutral handoff interfaces**

Create `internal/adapters/streamhandoff/handoff.go`:

```go
package streamhandoff

import "context"

type Resolver interface {
	ResolveStreamURL(ctx context.Context, rawURL string) (Resolution, bool, error)
	StartResolvedStream(ctx context.Context, res Resolution) (StartResult, error)
}

type Resolution struct {
	AdapterRef string
	ProviderID string
	ChannelID  string
	ItemID     string
}

type StartResult struct {
	AdapterRef string `json:"adapter_ref"`
	ProviderID string `json:"provider_id"`
	ChannelID  string `json:"channel_id,omitempty"`
	ItemID     string `json:"item_id,omitempty"`
}
```

Add `streamResolver streamhandoff.Resolver` to `url.Adapter`, import the neutral package, and add:

```go
func (a *Adapter) SetStreamResolver(r streamhandoff.Resolver) {
	a.mu.Lock()
	a.streamResolver = r
	a.mu.Unlock()
}
```

At the start of `castURL`, after URL parse/scheme validation and history add, snapshot `streamResolver` and call it before `decideRoute`.

The streams package must return `streamhandoff.Resolution` and `streamhandoff.StartResult`; it must not import `internal/adapters/url`.

- [ ] **Step 4: Run tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams ./internal/adapters/url -run "TestResolveStreamURL|TestCastURL_Streams" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams internal/adapters/url
git commit -m "feat(streams): route provider urls natively"
```

---

## Task 6: Queue Playback And Controls

**Files:**
- Modify: `internal/adapters/streams/queue.go`
- Create: `internal/adapters/streams/playback.go`
- Create: `internal/adapters/streams/queue_test.go`
- Create: `internal/adapters/streams/playback_test.go`

**Why:** Once a provider URL resolves, streams must build a stable queue snapshot, resolve each YouTube item via yt-dlp, start core sessions, and advance on EOF without stale callback races.

- [ ] **Step 1: Write queue behavior tests**

Create `internal/adapters/streams/queue_test.go`:

```go
func TestBuildQueueFirstThenShuffleSingleItem(t *testing.T) {
	ch := Channel{ID: "intro", PlayMode: PlayFirstThenShuffle, Items: []StreamItem{{ID: "a"}}}
	q, err := buildQueue("mtv-rewind", ch, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatalf("buildQueue: %v", err)
	}
	if q.Items[0].ID != "a" || q.loopMode != loopSequential {
		t.Fatalf("queue = %+v", q)
	}
}

func TestStaleOnStopDoesNotAdvanceNewQueue(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{ProviderID: "mtv-rewind", ChannelID: "metal", Generation: 2, ItemToken: 9, SessionID: "new", Items: []StreamItem{{ID: "new"}}}
	cb := a.makeOnStop(queueCapture{Generation: 1, ItemToken: 8, SessionID: "old", ItemID: "old"})
	cb("eof")
	if a.active.SessionID != "new" {
		t.Fatalf("stale callback mutated active queue: %+v", a.active)
	}
}

func TestManualControlsIncrementGenerationAndCancelResolve(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	cancelled := false
	a.active = &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		Generation: 4,
		Items:      []StreamItem{{ID: "a"}, {ID: "b"}},
		cancelResolve: func() { cancelled = true },
	}
	if err := a.Next(t.Context()); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !cancelled {
		t.Fatal("Next did not cancel in-flight resolution")
	}
	if a.active.Generation != 5 {
		t.Fatalf("generation = %d, want 5", a.active.Generation)
	}
}

func TestQueueSnapshotSurvivesCatalogRefresh(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		Items:      []StreamItem{{ID: "old"}},
	}
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Channels: []Channel{{ID: "metal", Items: []StreamItem{{ID: "new"}}}},
	}})
	if got := a.active.Items[0].ID; got != "old" {
		t.Fatalf("active queue item = %q, want old snapshot", got)
	}
}

func TestAdhocSingleItemStopsOnEOF(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "adhoc",
		SessionID:  "s1",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "dQw4w9WgXcQ"}},
	}
	cb := a.makeOnStop(queueCapture{Generation: 0, ItemToken: 1, SessionID: "s1", ItemID: "dQw4w9WgXcQ"})
	cb("eof")
	if a.active != nil {
		t.Fatalf("adhoc EOF should clear queue, got %+v", a.active)
	}
}

func TestAdhocPauseAfterEOFIsNoOp(t *testing.T) {
	// Lifecycle interaction: Capabilities{CanPause:true} on an adhoc
	// queue means a Pause request can arrive after the EOF callback has
	// already cleared a.active. The streams Pause handler must check
	// a.active for nil before dereferencing — otherwise Task 1's EOF
	// teardown plus this code path is a nil deref.
	a, _ := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "adhoc",
		SessionID:  "s1",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "dQw4w9WgXcQ"}},
	}
	cb := a.makeOnStop(queueCapture{Generation: 0, ItemToken: 1, SessionID: "s1", ItemID: "dQw4w9WgXcQ"})
	cb("eof")
	if a.active != nil {
		t.Fatal("precondition: EOF should clear adhoc queue")
	}
	// Pause must not panic on a cleared queue and must report a clear error.
	if err := a.Pause(t.Context()); err == nil {
		t.Fatal("Pause on cleared adhoc queue should report no-active-session error")
	}
}

func TestForeignPreemptClearsOnlyMatchingQueue(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{ProviderID: "mtv-rewind", ChannelID: "metal", Generation: 2, ItemToken: 3, SessionID: "current", Items: []StreamItem{{ID: "x"}}}
	stale := a.makeOnStop(queueCapture{Generation: 1, ItemToken: 3, SessionID: "old", ItemID: "x"})
	stale("preempted")
	if a.active == nil {
		t.Fatal("stale preempt cleared current queue")
	}
	matching := a.makeOnStop(queueCapture{Generation: 2, ItemToken: 3, SessionID: "current", ItemID: "x"})
	matching("preempted")
	if a.active != nil {
		t.Fatal("matching preempt should clear queue")
	}
}
```

- [ ] **Step 2: Write playback tests**

Create `internal/adapters/streams/playback_test.go`:

```go
func TestStartResolvedStreamStartsCoreSession(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.resolver = fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4", Title: "Clip"}}
	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	req := core.lastReq
	if req.StreamURL != "https://media.example/video.mp4" || req.AdapterRef == "" || !req.DirectPlay {
		t.Fatalf("SessionRequest = %+v", req)
	}
	if !req.Capabilities.CanPause {
		t.Fatal("streams sessions should be pausable")
	}
}

func TestOutOfCatalogItemBuildsAdhocQueue(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.resolver = fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4"}}
	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ItemID: "dQw4w9WgXcQ"})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if a.active.ChannelID != "adhoc" || a.active.ChannelName != "MTV Rewind Link" {
		t.Fatalf("active queue = %+v", a.active)
	}
}
```

Add these helpers to `internal/adapters/streams/test_helpers_test.go`:

```go
type fakeCore struct {
	lastReq core.SessionRequest
	startErr error
	stopCalls int
}

func (f *fakeCore) StartSession(req core.SessionRequest) error {
	f.lastReq = req
	return f.startErr
}

func (f *fakeCore) Stop() error {
	f.stopCalls++
	return nil
}

func (f *fakeCore) Status() core.SessionStatus {
	return core.SessionStatus{}
}

type fakeResolver struct {
	res *ytdlp.Resolution
	err error
}

func (f fakeResolver) Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*ytdlp.Resolution, error) {
	return f.res, f.err
}

func newTestAdapterWithFakeCore(t *testing.T) (*Adapter, *fakeCore) {
	t.Helper()
	core := &fakeCore{}
	a := newTestAdapterWithCatalog(t)
	a.core = core
	return a, core
}
```

- [ ] **Step 3: Implement queue and playback**

`ActiveQueue` must include the concurrency identifiers from the spec:

```go
type ActiveQueue struct {
	SessionID     string
	ProviderID    string
	ProviderName  string
	ChannelID     string
	ChannelName   string
	Items         []StreamItem
	Index         int
	Generation    uint64
	ItemToken     uint64
	Failures      []ItemFailure
	StartedAt     time.Time
	LastResolvedAt time.Time
	cancelResolve context.CancelFunc
}
```

`playCurrentLocked` must increment `ItemToken`, create `AdapterRef` as `streams:<provider>:<channel>:<token>`, resolve with yt-dlp using `cfg.YoutubeFormat`, and build:

```go
req := core.SessionRequest{
	StreamURL:         res.URL,
	InputHeaders:      res.Headers,
	AudioStreamURL:    res.AudioURL,
	AudioInputHeaders: res.AudioHeaders,
	AdapterRef:        ref,
	DirectPlay:        true,
	Capabilities:      core.Capabilities{CanPause: true, CanSeek: false},
	OnStop:            a.makeOnStop(capture),
}
```

Keep `CanSeek=false` until the implementation has reliable seekability/duration metadata for streams.

- [ ] **Step 4: Run queue/playback tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "Test(BuildQueue|StaleOnStop|StartResolvedStream|OutOfCatalog)" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams
git commit -m "feat(streams): play provider queues"
```

---

## Task 7: Routes, UI Panel, JSON Status

**Files:**
- Create: `internal/adapters/streams/routes.go`
- Create: `internal/adapters/streams/ui.go`
- Create: `internal/adapters/streams/routes_test.go`
- Create: `internal/adapters/streams/ui_test.go`

**Why:** Operators need a browser panel, htmx controls, JSON status for future companion use, manual refresh, play, replay, next, previous, and stop.

- [ ] **Step 1: Write route tests**

Create `internal/adapters/streams/routes_test.go`:

```go
func TestUIRoutes(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	routes := a.UIRoutes()
	want := map[string]string{
		"GET panel":       "",
		"GET status":      "",
		"GET providers":   "",
		"POST refresh":    "",
		"POST play":       "",
		"POST replay":     "",
		"POST next":       "",
		"POST previous":   "",
		"POST stop":       "",
	}
	for _, r := range routes {
		delete(want, r.Method+" "+r.Path)
	}
	if len(want) != 0 {
		t.Fatalf("missing routes: %#v", want)
	}
}

func TestStatusJSONIncludesCompanionFields(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/adapter/streams/status", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	a.handleStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got StatusView
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Capabilities.CanNext != true || got.Capabilities.CanSeek != false {
		t.Fatalf("capabilities = %+v", got.Capabilities)
	}
}
```

- [ ] **Step 2: Write UI smoke test**

Create `internal/adapters/streams/ui_test.go`:

```go
func TestExtraPanelHTMLContainsStreamsPanel(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	html := string(a.ExtraPanelHTML())
	// Match the existing URL adapter UI convention: route-table paths are
	// relative, but rendered htmx URLs are absolute mounted paths because
	// htmx resolves relative URLs against the current browser URL, not the
	// fetched fragment URL.
	for _, want := range []string{"streams-panel", "MTV Rewind", "Cartoon Rewind", `hx-post="/ui/adapter/streams/play"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("panel missing %q: %s", want, html)
		}
	}
}
```

- [ ] **Step 3: Implement routes and UI**

Route `Path` values are **relative** to the UI server's mount, which is `/ui/adapter/streams/` (managed by the registry; do not hard-code or duplicate the prefix in `UIRoutes`). Mirror the `Path: "panel"` style used by `internal/adapters/url/routes.go`. Rendered htmx `hx-*` attributes should use absolute mounted paths such as `/ui/adapter/streams/play`, matching `internal/adapters/url/ui.go`, because htmx resolves relative URLs against the current browser URL.

`UIRoutes()` must return:

```go
[]adapters.Route{
	{Method: http.MethodGet, Path: "panel", Handler: a.handlePanel},
	{Method: http.MethodGet, Path: "status", Handler: a.handleStatus},
	{Method: http.MethodGet, Path: "providers", Handler: a.handleProviders},
	{Method: http.MethodPost, Path: "refresh", Handler: a.handleRefresh},
	{Method: http.MethodPost, Path: "play", Handler: a.handlePlay},
	{Method: http.MethodPost, Path: "replay", Handler: a.handleReplay},
	{Method: http.MethodPost, Path: "next", Handler: a.handleNext},
	{Method: http.MethodPost, Path: "previous", Handler: a.handlePrevious},
	{Method: http.MethodPost, Path: "stop", Handler: a.handleStop},
}
```

Define JSON status as:

```go
type StatusView struct {
	Providers    []ProviderStatusView `json:"providers"`
	Active       *QueueStatusView     `json:"active,omitempty"`
	Capabilities ControlCapabilities  `json:"capabilities"`
}

type ControlCapabilities struct {
	CanStop     bool `json:"can_stop"`
	CanReplay   bool `json:"can_replay"`
	CanNext     bool `json:"can_next"`
	CanPrevious bool `json:"can_previous"`
	CanPause    bool `json:"can_pause"`
	CanSeek     bool `json:"can_seek"`
}
```

Escape every dynamic HTML value with `html/template` or `template.HTMLEscapeString`.

- [ ] **Step 4: Run route/UI tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "Test(UIRoutes|StatusJSON|ExtraPanel)" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams
git commit -m "feat(streams): add provider ui routes"
```

---

## Task 8: Main Wiring, Refresh Loop, README

**Files:**
- Modify: `internal/adapters/streams/refresh.go`
- Create: `internal/adapters/streams/refresh_test.go`
- Modify: `internal/adapters/streams/adapter.go`
- Modify: `cmd/mister-groovy-relay/main.go`
- Modify: `README.md`
- Create: `internal/adapters/streams/registry_test.go`
- Modify: `internal/adapters/url/play_test.go`

**Why:** The adapter must be registered in the real bridge, URL must receive the stream resolver, and operators need config and example links.

- [ ] **Step 1: Write refresh lifecycle tests**

Create `internal/adapters/streams/refresh_test.go`:

```go
func TestStartDisabledLoadsBundledAndDoesNotFetch(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	fetches := 0
	a.fetchManifest = func(context.Context) (Manifest, CacheMetadata, error) {
		fetches++
		return Manifest{}, CacheMetadata{}, nil
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if fetches != 0 {
		t.Fatalf("disabled Start fetched remote manifest %d times", fetches)
	}
}

func TestStartEnabledStartsRefreshLoopAndStopCancels(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.SetEnabled(true)
	started := make(chan struct{})
	a.refreshOnce = func(ctx context.Context, reason string) RefreshStatus {
		close(started)
		<-ctx.Done()
		return RefreshStatus{Err: ctx.Err()}
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh loop did not start")
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestManualRefreshKeepsLastGoodOnFailure(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	before := a.catalogSnapshotForTest("mtv-rewind")
	a.refreshOnce = func(ctx context.Context, reason string) RefreshStatus {
		return RefreshStatus{Err: errors.New("network down")}
	}
	status := a.RefreshNow(t.Context(), "mtv-rewind")
	if status.Err == nil {
		t.Fatal("manual refresh should report error")
	}
	after := a.catalogSnapshotForTest("mtv-rewind")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed refresh replaced last good catalog")
	}
}
```

Implement `catalogSnapshotForTest` in `test_helpers_test.go` if the catalog store is private.

- [ ] **Step 2: Implement refresh lifecycle**

Replace the Task 2 refresh shell in `internal/adapters/streams/refresh.go` with:

```go
type RefreshStatus struct {
	ProviderID string
	Source     string
	FetchedAt  time.Time
	ETag       string
	Err        error
}

func (a *Adapter) RefreshNow(ctx context.Context, providerID string) RefreshStatus {
	return a.refreshOnce(ctx, "manual")
}
```

In `Adapter.Start`, synchronously load bundled definitions, allowed cached remote manifest overlays, and cached catalogs without network. If `cfg.Enabled && cfg.AllowRemoteManifest`, start a background loop owned by an adapter context. The loop calls `refreshOnce`, backs off on failure with jitter, and exits on `Stop`. `Stop` must cancel the context and wait for the loop to exit.

The refresh path must validate a full manifest/catalog snapshot before swapping it into `a.definitions`/`a.catalogs`. Active queues keep their item snapshot.

- [ ] **Step 3: Write registry/wiring tests**

Create `internal/adapters/streams/registry_test.go`:

```go
func TestAdapterCanRegisterAndStartDisabled(t *testing.T) {
	reg := adapters.NewRegistry()
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got, ok := reg.Get("streams"); !ok || got.DisplayName() != "Streams" {
		t.Fatalf("registry lookup = %v, %v", got, ok)
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start disabled: %v", err)
	}
}
```

- [ ] **Step 4: Wire main**

In `cmd/mister-groovy-relay/main.go`, construct streams after URL and before Jellyfin. **Order matters**: `urlAdapter.SetStreamResolver(streamsAdapter)` must run before `streamsAdapter.Start(ctx)` so the URL adapter never observes a half-initialized streams adapter, and `reg.Register` happens last so the HTTP mux only exposes routes for a fully-wired adapter.

```go
streamsAdapter, err := streams.New(streams.AdapterConfig{
	Bridge:        sec.Bridge,
	Core:          coreMgr,
	YTDLPResolver: ytdlpResolver,
})
if err != nil {
	dieFriendly("streams adapter init", err)
}
urlAdapter.SetStreamResolver(streamsAdapter)       // wire dependency BEFORE Start.
if err := reg.Register(streamsAdapter); err != nil {
	dieFriendly("registry register streams", err)
}
// streamsAdapter.Start(ctx) is invoked by the registry's Start loop later,
// matching the existing per-adapter Start sequence in main.go.
```

Import `github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams`.

- [ ] **Step 5: Document config and examples**

Add a README section **after the URL adapter section and before the troubleshooting section** (mirroring the existing per-adapter ordering in `README.md`):

````markdown
### Streams adapter

The Streams adapter turns supported catalog sites into native relay queues. It is disabled by default.

```toml
[adapters.streams]
enabled = false
allow_remote_manifest = true
manifest_refresh_hours = 24
catalog_refresh_hours = 12
youtube_format = "bv*[height<=480]+ba/b[height<=480]/bv*+ba/b"
```

Example links handled natively when Streams is enabled:

- `https://wantmymtv.vercel.app/player.html?channel=metal`
- `https://wantmymtv.xyz/player.html?channel=metal`
- `https://wantmymtv.vercel.app/player.html?v=dQw4w9WgXcQ`
- `https://cartoonrewind.tv/player.html?channel=heman`
````

- [ ] **Step 6: Run focused integration tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams ./internal/adapters/url ./cmd/mister-groovy-relay -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/mister-groovy-relay/main.go internal/adapters/streams internal/adapters/url README.md
git commit -m "feat(streams): register provider adapter"
```

---

## Task 9: Final Verification And Review

**Files:**
- No new files unless a test reveals a defect.

All `cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe ...` invocations below assume this Windows machine's pinned Go toolchain. On any other host (CI, Linux dev, fresh checkout) where `go` is on PATH, drop the prefix and run `go test ./...` directly.

- [ ] **Step 1: Run full Go tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./...
```

Expected: PASS.

- [ ] **Step 2: Run race-detector tests**

CI runs `go test -race ./...` on every push (CLAUDE.md §Commands). The streams adapter introduces non-trivial new shared state — the queue mutex, the refresh background goroutine, and the `OnStop` closures captured into `core.SessionRequest`. Run the race detector locally before merge:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test -race ./internal/adapters/streams ./internal/adapters/url ./internal/core
```

Expected: PASS with no `DATA RACE` reports.

- [ ] **Step 3: Run vet**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe vet ./...
```

Expected: PASS.

- [ ] **Step 4: Search for forbidden implementation drift**

Run:

```bash
rg -n "TO[D]O|TB[D]|execute remote|channels-config.js|url_patterns|CanSeek: true" internal/adapters/streams internal/adapters/streamhandoff internal/adapters/url internal/core cmd/mister-groovy-relay README.md
```

Expected: no matches except explanatory documentation that explicitly says remote JavaScript is not executed.

- [ ] **Step 5: Request code review**

Use `superpowers:requesting-code-review` with:

- What was implemented: generic streams provider adapter with MTV/Cartoon bundled providers and URL handoff.
- Plan/reference: `docs/superpowers/plans/2026-05-09-streams-provider-adapter.md`
- Base SHA: commit before Task 1.
- Head SHA: current `HEAD`.

- [ ] **Step 6: Fix review feedback or document pushback**

Apply Critical and Important review items before merge. If a review item conflicts with the accepted spec, write the technical pushback in the final handoff and cite the spec section.

---

## Self-Review Checklist

- Spec coverage:
  - Core EOF lifecycle: Task 1.
  - Adapter/config/apply scopes: Task 2.
  - Manifest/cache/security/remote refresh boundaries: Task 3.
  - Bundled MTV and Cartoon providers: Task 4.
  - URL handoff for provider links: Task 5.
  - Queue/playback/EOF advancement: Task 6.
  - UI/API/companion status shape: Task 7.
  - Main wiring and docs: Task 8.
  - Final verification and code review: Task 9.
- Placeholder scan: run `rg -n "TB[D]|TO[D]O|implement lat[e]r|fill i[n]|similar t[o]|appropriat[e]" docs/superpowers/plans/2026-05-09-streams-provider-adapter.md internal/adapters/streams internal/adapters/streamhandoff` so any placeholders left in newly written code are caught alongside any left in the plan itself.
- Type consistency:
  - URL handoff types are declared in `internal/adapters/streamhandoff` to avoid streams-to-URL runtime coupling.
  - Streams adapter implements `streamhandoff.Resolver`.
  - `ActiveQueue` generation/item token/session ID/item ID checks match the spec.
  - Route paths match `/ui/adapter/streams/<path>` from the UI server mount.
