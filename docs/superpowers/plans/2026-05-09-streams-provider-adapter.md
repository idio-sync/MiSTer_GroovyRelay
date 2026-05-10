# Streams Provider Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a generic `streams` adapter that plays catalog-backed YouTube-ID channel providers, with bundled MTV Rewind and Cartoon Rewind support plus native URL handoff.

**Architecture:** Implement `internal/adapters/streams` as a peer adapter that owns provider catalogs, queue state, playback controls, and UI routes. Remote provider data is data-only and passes strict manifest, cache, URL, redirect, DNS, and byte-limit validation before becoming active. Playback reuses the existing yt-dlp resolver and `core.Manager`, with a small EOF lifecycle fix in core so queues can advance naturally.

**Tech Stack:** Go 1.26, BurntSushi/toml, stdlib `net/http`, `html/template`, `httptest`, `embed`, `encoding/json`, `net/netip`, `internal/adapters/url/ytdlp`, htmx fragments through the existing adapter route mount.

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

- [ ] **Step 1: Write failing EOF regression test**

Add this test near the existing manager lifecycle tests in `internal/core/manager_test.go`:

```go
func TestManager_NaturalEOFFiresOnStopAndClearsActive(t *testing.T) {
	m := newTestManager(t)
	done := make(chan string, 1)
	req := bogusRequest()
	req.OnStop = func(reason string) { done <- reason }

	if err := m.StartSession(req); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	waitForPlaneExit(t, m)

	select {
	case got := <-done:
		if got != "eof" {
			t.Fatalf("OnStop reason = %q, want eof", got)
		}
	case <-time.After(time.Second):
		t.Fatal("OnStop was not called")
	}
	if err := m.Stop(); err == nil {
		t.Fatal("Stop after EOF should report no active session")
	}
}
```

If `newTestManager` or `waitForPlaneExit` do not already exist with this exact shape, add small helpers in the same test file:

```go
func waitForPlaneExit(t *testing.T, m *Manager) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		m.mu.Lock()
		plane := m.plane
		m.mu.Unlock()
		if plane == nil {
			return
		}
		select {
		case <-deadline:
			t.Fatal("plane did not exit")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/core -run TestManager_NaturalEOFFiresOnStopAndClearsActive -count=1
```

Expected: FAIL because `OnStop` is not called with `"eof"` or `Stop()` still sees an active session.

- [ ] **Step 3: Implement EOF handling with existing guards**

In `internal/core/manager.go`, update the goroutine inside `startPlaneLocked` so the `runErr == nil` branch mirrors the safe parts of the error branch:

```go
m.mu.Lock()
if m.plane != plane {
	m.mu.Unlock()
	return
}
m.plane = nil
reason := "error"
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
```

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
		"enabled":                         adapters.ScopeHotSwap,
		"manifest_url":                    adapters.ScopeHotSwap,
		"max_manifest_bytes":              adapters.ScopeHotSwap,
		"youtube_format":                  adapters.ScopeRestartCast,
		"providers.mtv-rewind.disabled":   adapters.ScopeHotSwap,
		"providers.mtv-rewind.catalog_refresh_hours": adapters.ScopeHotSwap,
	}
	for key, want := range cases {
		if got := scopeForField(key); got != want {
			t.Fatalf("scopeForField(%q) = %v, want %v", key, got, want)
		}
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
	for host := range normalizeHostSet(c.RemoteProviderAllowedHosts) {
		if strings.ContainsAny(host, " \t\r\n/:?#@*") {
			errs = append(errs, adapters.FieldError{Key: "remote_provider_allowed_hosts", Msg: fmt.Sprintf("invalid hostname %q", host)})
		}
	}
	return errs.Err()
}

func scopeForField(key string) adapters.ApplyScope {
	if key == "youtube_format" {
		return adapters.ScopeRestartCast
	}
	return adapters.ScopeHotSwap
}

func normalizeHostSet(in []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, h := range in {
		h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
		if h != "" {
			out[h] = struct{}{}
		}
	}
	return out
}
```

Create `internal/adapters/streams/adapter.go` with the adapter shell, fields, `DecodeConfig`, `Validate`, `ApplyConfig`, `CurrentValues`, `SetEnabled`, `Start`, `Stop`, and `Status`. Keep `Start` as state-only in this task:

```go
type AdapterConfig struct {
	Bridge        config.BridgeConfig
	Core          SessionManager
	YTDLPResolver ytdlp.BinaryResolver
}

type SessionManager interface {
	StartSession(core.SessionRequest) error
	Stop() error
	Status() core.SessionStatus
}
```

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
- Create: `internal/adapters/streams/provider.go`
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

- [ ] **Step 2: Write fetch security tests**

Create `internal/adapters/streams/fetch_test.go`:

```go
func TestFetchRejectsLoopbackByDefault(t *testing.T) {
	f := secureFetcher{client: http.DefaultClient, resolver: staticResolver{"example.test": {"127.0.0.1"}}}
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
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))
	defer private.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, private.URL, http.StatusFound)
	}))
	defer public.Close()

	f := testFetcherAllowingServerHosts(t, public, private)
	_, err := f.Fetch(t.Context(), public.URL, fetchLimits{MaxBytes: 1024})
	if err == nil {
		t.Fatal("redirect revalidation did not reject private target")
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

func testFetcherAllowingServerHosts(t *testing.T, servers ...*httptest.Server) secureFetcher {
	t.Helper()
	lookup := staticResolver{}
	for _, srv := range servers {
		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("parse server URL: %v", err)
		}
		host, _, err := net.SplitHostPort(u.Host)
		if err != nil {
			t.Fatalf("split host: %v", err)
		}
		lookup[u.Hostname()] = []string{host}
	}
	return secureFetcher{client: http.DefaultClient, resolver: lookup}
}
```

- [ ] **Step 3: Implement schema, merge, cache, and fetch**

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
}
```

The fetcher must resolve once, dial the validated IP, preserve host for SNI/Host, cap redirects at three, reject HTTPS-to-HTTP downgrade unless local URLs are allowed, and wrap response bodies with `http.MaxBytesReader` or `io.LimitReader` plus an over-limit check.

- [ ] **Step 4: Run manifest/cache/fetch tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "Test(ValidateManifest|MergeManifests|Fetch|Cache)" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

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
}
```

Add this helper to `internal/adapters/streams/test_helpers_test.go`:

```go
func newTestAdapterWithCatalog(t *testing.T) *Adapter {
	t.Helper()
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cat := ProviderCatalog{
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
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
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

- [ ] **Step 2: Write URL adapter handoff tests**

In `internal/adapters/url/play_test.go`, add a fake stream resolver and tests:

```go
type fakeStreamResolver struct {
	matched bool
	res     StreamURLResolution
	err     error
	starts  int
}

func (f *fakeStreamResolver) ResolveStreamURL(ctx context.Context, rawURL string) (StreamURLResolution, bool, error) {
	return f.res, f.matched, f.err
}

func (f *fakeStreamResolver) StartResolvedStream(ctx context.Context, res StreamURLResolution) (StreamStartResult, error) {
	f.starts++
	return StreamStartResult{AdapterRef: res.AdapterRef, ProviderID: res.ProviderID, ChannelID: res.ChannelID, ItemID: res.ItemID}, nil
}

func TestCastURL_StreamsHandoffBeforeYTDLP(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	f := &fakeStreamResolver{matched: true, res: StreamURLResolution{AdapterRef: "streams:1", ProviderID: "mtv-rewind", ChannelID: "metal"}}
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

- [ ] **Step 3: Implement handoff interfaces**

Define the shared handoff types in `internal/adapters/url/adapter.go` to avoid importing streams into URL:

```go
type StreamURLResolver interface {
	ResolveStreamURL(ctx context.Context, rawURL string) (StreamURLResolution, bool, error)
	StartResolvedStream(ctx context.Context, res StreamURLResolution) (StreamStartResult, error)
}

type StreamURLResolution struct {
	AdapterRef string
	ProviderID string
	ChannelID  string
	ItemID     string
}

type StreamStartResult struct {
	AdapterRef string `json:"adapter_ref"`
	ProviderID string `json:"provider_id"`
	ChannelID  string `json:"channel_id,omitempty"`
	ItemID     string `json:"item_id,omitempty"`
}
```

Add `streamResolver StreamURLResolver` to `url.Adapter`, plus:

```go
func (a *Adapter) SetStreamResolver(r StreamURLResolver) {
	a.mu.Lock()
	a.streamResolver = r
	a.mu.Unlock()
}
```

At the start of `castURL`, after URL parse/scheme validation and history add, snapshot `streamResolver` and call it before `decideRoute`.

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
- Create: `internal/adapters/streams/queue.go`
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
	a := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{ProviderID: "mtv-rewind", ChannelID: "metal", Generation: 2, ItemToken: 9, SessionID: "new", Items: []StreamItem{{ID: "new"}}}
	cb := a.makeOnStop(queueCapture{Generation: 1, ItemToken: 8, SessionID: "old", ItemID: "old"})
	cb("eof")
	if a.active.SessionID != "new" {
		t.Fatalf("stale callback mutated active queue: %+v", a.active)
	}
}
```

- [ ] **Step 2: Write playback tests**

Create `internal/adapters/streams/playback_test.go`:

```go
func TestStartResolvedStreamStartsCoreSession(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.resolver = fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4", Title: "Clip"}}
	_, err := a.StartResolvedStream(t.Context(), StreamURLResolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
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
	_, err := a.StartResolvedStream(t.Context(), StreamURLResolution{ProviderID: "mtv-rewind", ItemID: "dQw4w9WgXcQ"})
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
	for _, want := range []string{"streams-panel", "MTV Rewind", "Cartoon Rewind", "hx-post=\"/ui/adapter/streams/play\""} {
		if !strings.Contains(html, want) {
			t.Fatalf("panel missing %q: %s", want, html)
		}
	}
}
```

- [ ] **Step 3: Implement routes and UI**

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
- Modify: `cmd/mister-groovy-relay/main.go`
- Modify: `README.md`
- Create: `internal/adapters/streams/registry_test.go`
- Modify: `internal/adapters/url/play_test.go`

**Why:** The adapter must be registered in the real bridge, URL must receive the stream resolver, and operators need config and example links.

- [ ] **Step 1: Write registry/wiring tests**

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

- [ ] **Step 2: Wire main**

In `cmd/mister-groovy-relay/main.go`, construct streams after URL and before Jellyfin:

```go
streamsAdapter, err := streams.New(streams.AdapterConfig{
	Bridge:        sec.Bridge,
	Core:          coreMgr,
	YTDLPResolver: ytdlpResolver,
})
if err != nil {
	dieFriendly("streams adapter init", err)
}
urlAdapter.SetStreamResolver(streamsAdapter)
if err := reg.Register(streamsAdapter); err != nil {
	dieFriendly("registry register streams", err)
}
```

Import `github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams`.

- [ ] **Step 3: Document config and examples**

Add a README section with:

```markdown
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
- `https://wantmymtv.vercel.app/player.html?v=dQw4w9WgXcQ`
- `https://cartoonrewind.tv/player.html?channel=heman`
```

- [ ] **Step 4: Run focused integration tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams ./internal/adapters/url ./cmd/mister-groovy-relay -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/main.go internal/adapters/streams internal/adapters/url README.md
git commit -m "feat(streams): register provider adapter"
```

---

## Task 9: Final Verification And Review

**Files:**
- No new files unless a test reveals a defect.

- [ ] **Step 1: Run full Go tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./...
```

Expected: PASS.

- [ ] **Step 2: Run vet**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe vet ./...
```

Expected: PASS.

- [ ] **Step 3: Search for forbidden implementation drift**

Run:

```bash
rg -n "TO[D]O|TB[D]|execute remote|channels-config.js|url_patterns|CanSeek: true" internal/adapters/streams internal/adapters/url internal/core cmd/mister-groovy-relay README.md
```

Expected: no matches except explanatory documentation that explicitly says remote JavaScript is not executed.

- [ ] **Step 4: Request code review**

Use `superpowers:requesting-code-review` with:

- What was implemented: generic streams provider adapter with MTV/Cartoon bundled providers and URL handoff.
- Plan/reference: `docs/superpowers/plans/2026-05-09-streams-provider-adapter.md`
- Base SHA: commit before Task 1.
- Head SHA: current `HEAD`.

- [ ] **Step 5: Fix review feedback or document pushback**

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
- Placeholder scan: run `rg -n "TB[D]|TO[D]O|implement lat[e]r|fill i[n]|similar t[o]|appropriat[e]" docs/superpowers/plans/2026-05-09-streams-provider-adapter.md`.
- Type consistency:
  - URL handoff types are declared in URL adapter to avoid streams-to-URL runtime coupling.
  - Streams adapter implements URL's structural `StreamURLResolver`.
  - `ActiveQueue` generation/item token/session ID/item ID checks match the spec.
  - Route paths match `/ui/adapter/streams/<path>` from the UI server mount.
