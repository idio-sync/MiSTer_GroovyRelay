# Live HLS Buffering V1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add default-on live HLS segment buffering for bundled Streams direct HLS and URL direct-pasted public `.m3u8` casts, while keeping v1 out of dataplane slate/audio-hold work.

**Architecture:** Bridge config owns shared HLS buffer defaults and UI apply scope. A new `internal/hlsbuffer` package owns playlist parsing, public/bundled URL validation, bounded rolling cache files, and local playlist publishing. Streams and URL adapters call that package before starting core and pass a local-only `MediaInputPolicy` to FFmpeg.

**Tech Stack:** Go, `net/http`, `net/url`, `net/netip`, `httptest`, BurntSushi TOML, existing `core.SessionRequest` and `ffmpeg.MediaInputPolicy`.

---

Baseline in isolated worktree:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./...
```

Expected: all packages pass. Verified once before this plan in `.worktrees/hls-buffering-v1`.

## File Structure

- Modify `internal/config/config.go`: add `HLSBufferConfig`, defaults, validation on `BridgeConfig`.
- Modify `internal/config/migration.go`: include default HLS buffer config in `defaultBridge`.
- Modify `internal/config/example.go` and `internal/config/example.toml`: document generated defaults.
- Modify `internal/ui/bridge_fields.go`: add Bridge UI fields for `[bridge.hls_buffer]`.
- Modify `internal/uiserver/bridge_saver.go`: diff `hls_buffer.*` fields and return `ScopeRestartCast`.
- Create `internal/hlsbuffer/config.go`: public `Config`, `TrustMode`, `SessionOptions`, `Stats`, defaults conversion from bridge config.
- Create `internal/hlsbuffer/playlist.go`: parse supported HLS subset, classify master/media playlists, reject unsupported tags.
- Create `internal/hlsbuffer/variant.go`: deterministic variant selection.
- Create `internal/hlsbuffer/validator.go`: generic public URL and bundled Toonami child URL validation.
- Create `internal/hlsbuffer/session.go`: open/warm/reload/publish/cleanup local buffered sessions.
- Create `internal/hlsbuffer/reaper.go`: stale session cache cleanup.
- Modify `internal/adapters/streams/config.go`: add provider/channel opt-out config.
- Modify `internal/adapters/streams/adapter.go`: carry `BridgeConfig` and construct HLS buffer options.
- Modify `internal/adapters/streams/playback.go`: buffer eligible direct HLS items before `StartSession`.
- Modify `internal/adapters/url/adapter.go`: carry `BridgeConfig` and HLS buffer opener seam.
- Modify `internal/adapters/url/play.go`: parse `hls_buffer` mode, buffer eligible direct `.m3u8`, cleanup on failure.
- Modify `internal/adapters/url/history.go`: persist per-entry HLS buffer mode for replay.
- Modify `internal/adapters/url/ui.go`: expose `auto` / `off` HLS buffering control.
- Modify `cmd/mister-groovy-relay/main.go`: pass bridge config and hlsbuffer dependencies into adapters.

## Task 1: Bridge HLS Buffer Config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/migration.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/ui/bridge_fields.go`
- Modify: `internal/ui/bridge_fields_test.go`
- Modify: `internal/uiserver/bridge_saver.go`
- Modify: `internal/uiserver/bridge_saver_test.go`
- Modify: `internal/config/example.go`
- Modify: `internal/config/example.toml`

- [ ] **Step 1: Write failing config tests**

Add these tests to `internal/config/config_test.go`:

```go
func TestDefaultBridge_HLSBufferDefaults(t *testing.T) {
	b := defaultBridge()
	want := HLSBufferConfig{
		Enabled:                true,
		LiveEdgeSegments:       3,
		StartSegments:          2,
		MaxCachedSegments:      6,
		MaxCacheBytes:          268435456,
		MaxPlaylistBytes:       1048576,
		MaxSegmentBytes:        52428800,
		SegmentTimeoutSeconds:  10,
		PlaylistTimeoutSeconds: 10,
		MaxVariantHeight:       720,
		StaleCacheReapHours:    24,
	}
	if b.HLSBuffer != want {
		t.Fatalf("HLSBuffer defaults = %+v, want %+v", b.HLSBuffer, want)
	}
}

func TestSectioned_Validate_HLSBuffer(t *testing.T) {
	valid := validSectioned()
	if err := valid.Validate(); err != nil {
		t.Fatalf("default HLSBuffer should validate: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*HLSBufferConfig)
		key  string
	}{
		{"live edge below start", func(c *HLSBufferConfig) { c.LiveEdgeSegments = 1; c.StartSegments = 2 }, "live_edge_segments"},
		{"start segments below one", func(c *HLSBufferConfig) { c.StartSegments = 0 }, "start_segments"},
		{"cached segments below start", func(c *HLSBufferConfig) { c.MaxCachedSegments = 1; c.StartSegments = 2 }, "max_cached_segments"},
		{"cache bytes too small", func(c *HLSBufferConfig) { c.MaxCacheBytes = 1 }, "max_cache_bytes"},
		{"playlist bytes too small", func(c *HLSBufferConfig) { c.MaxPlaylistBytes = 1 }, "max_playlist_bytes"},
		{"segment bytes too small", func(c *HLSBufferConfig) { c.MaxSegmentBytes = 1 }, "max_segment_bytes"},
		{"segment timeout too small", func(c *HLSBufferConfig) { c.SegmentTimeoutSeconds = 0 }, "segment_timeout_seconds"},
		{"playlist timeout too small", func(c *HLSBufferConfig) { c.PlaylistTimeoutSeconds = 0 }, "playlist_timeout_seconds"},
		{"variant height too small", func(c *HLSBufferConfig) { c.MaxVariantHeight = 1 }, "max_variant_height"},
		{"reap hours too small", func(c *HLSBufferConfig) { c.StaleCacheReapHours = 0 }, "stale_cache_reap_hours"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validSectioned()
			tc.mut(&s.Bridge.HLSBuffer)
			err := s.Validate()
			if err == nil {
				t.Fatalf("Validate() error = nil, want %s error", tc.key)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("Validate() error = %q, want mention %q", err, tc.key)
			}
		})
	}
}
```

- [ ] **Step 2: Run config tests to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/config
```

Expected: FAIL because `BridgeConfig.HLSBuffer` and `HLSBufferConfig` do not exist.

- [ ] **Step 3: Implement config struct/defaults/validation**

Add to `internal/config/config.go`:

```go
type HLSBufferConfig struct {
	Enabled                bool  `toml:"enabled"`
	LiveEdgeSegments       int   `toml:"live_edge_segments"`
	StartSegments          int   `toml:"start_segments"`
	MaxCachedSegments      int   `toml:"max_cached_segments"`
	MaxCacheBytes          int64 `toml:"max_cache_bytes"`
	MaxPlaylistBytes       int64 `toml:"max_playlist_bytes"`
	MaxSegmentBytes        int64 `toml:"max_segment_bytes"`
	SegmentTimeoutSeconds  int   `toml:"segment_timeout_seconds"`
	PlaylistTimeoutSeconds int   `toml:"playlist_timeout_seconds"`
	MaxVariantHeight       int   `toml:"max_variant_height"`
	StaleCacheReapHours    int   `toml:"stale_cache_reap_hours"`
}

func defaultHLSBufferConfig() HLSBufferConfig {
	return HLSBufferConfig{
		Enabled:                true,
		LiveEdgeSegments:       3,
		StartSegments:          2,
		MaxCachedSegments:      6,
		MaxCacheBytes:          268435456,
		MaxPlaylistBytes:       1048576,
		MaxSegmentBytes:        52428800,
		SegmentTimeoutSeconds:  10,
		PlaylistTimeoutSeconds: 10,
		MaxVariantHeight:       720,
		StaleCacheReapHours:    24,
	}
}

func validateHLSBufferConfig(c HLSBufferConfig) error {
	if c.LiveEdgeSegments < 1 || c.LiveEdgeSegments > 12 || c.LiveEdgeSegments < c.StartSegments {
		return fmt.Errorf("bridge.hls_buffer.live_edge_segments must be in [1, 12] and >= start_segments")
	}
	if c.StartSegments < 1 || c.StartSegments > 6 {
		return fmt.Errorf("bridge.hls_buffer.start_segments must be in [1, 6]")
	}
	if c.MaxCachedSegments < 2 || c.MaxCachedSegments > 24 || c.MaxCachedSegments < c.StartSegments {
		return fmt.Errorf("bridge.hls_buffer.max_cached_segments must be in [2, 24] and >= start_segments")
	}
	if c.MaxCacheBytes < 16777216 || c.MaxCacheBytes > 2147483648 {
		return fmt.Errorf("bridge.hls_buffer.max_cache_bytes must be in [16777216, 2147483648]")
	}
	if c.MaxPlaylistBytes < 4096 || c.MaxPlaylistBytes > 8388608 {
		return fmt.Errorf("bridge.hls_buffer.max_playlist_bytes must be in [4096, 8388608]")
	}
	if c.MaxSegmentBytes < 1048576 || c.MaxSegmentBytes > 536870912 {
		return fmt.Errorf("bridge.hls_buffer.max_segment_bytes must be in [1048576, 536870912]")
	}
	if c.SegmentTimeoutSeconds < 1 || c.SegmentTimeoutSeconds > 60 {
		return fmt.Errorf("bridge.hls_buffer.segment_timeout_seconds must be in [1, 60]")
	}
	if c.PlaylistTimeoutSeconds < 1 || c.PlaylistTimeoutSeconds > 60 {
		return fmt.Errorf("bridge.hls_buffer.playlist_timeout_seconds must be in [1, 60]")
	}
	if c.MaxVariantHeight < 240 || c.MaxVariantHeight > 2160 {
		return fmt.Errorf("bridge.hls_buffer.max_variant_height must be in [240, 2160]")
	}
	if c.StaleCacheReapHours < 1 || c.StaleCacheReapHours > 168 {
		return fmt.Errorf("bridge.hls_buffer.stale_cache_reap_hours must be in [1, 168]")
	}
	return nil
}
```

Add `HLSBuffer HLSBufferConfig `toml:"hls_buffer"` to `BridgeConfig`, set it in `defaultBridge`, and call `validateHLSBufferConfig(b.HLSBuffer)` from `Sectioned.Validate`.

- [ ] **Step 4: Verify config tests GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/config
```

Expected: PASS.

- [ ] **Step 5: Write failing UI/scope tests**

Add to `internal/ui/bridge_fields_test.go`:

```go
func TestBridgeFields_HLSBufferFieldsRestartCast(t *testing.T) {
	want := map[string]bool{
		"hls_buffer.enabled":                  false,
		"hls_buffer.live_edge_segments":       false,
		"hls_buffer.start_segments":           false,
		"hls_buffer.max_cached_segments":      false,
		"hls_buffer.max_cache_bytes":          false,
		"hls_buffer.max_playlist_bytes":       false,
		"hls_buffer.max_segment_bytes":        false,
		"hls_buffer.segment_timeout_seconds":  false,
		"hls_buffer.playlist_timeout_seconds": false,
		"hls_buffer.max_variant_height":       false,
		"hls_buffer.stale_cache_reap_hours":   false,
	}
	for _, f := range bridgeFields() {
		seen, ok := want[f.Key]
		if !ok {
			continue
		}
		if seen {
			t.Fatalf("duplicate bridge field %q", f.Key)
		}
		if f.Section != "HLS Buffer" {
			t.Errorf("%s section = %q, want HLS Buffer", f.Key, f.Section)
		}
		if f.ApplyScope != adapters.ScopeRestartCast {
			t.Errorf("%s scope = %v, want ScopeRestartCast", f.Key, f.ApplyScope)
		}
		want[f.Key] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("%s not found in bridgeFields()", k)
		}
	}
}
```

Add to `internal/uiserver/bridge_saver_test.go`:

```go
func TestDiffBridgeConfig_HLSBufferFieldsRestartCast(t *testing.T) {
	old := config.BridgeConfig{HLSBuffer: config.HLSBufferConfig{
		Enabled:                true,
		LiveEdgeSegments:       3,
		StartSegments:          2,
		MaxCachedSegments:      6,
		MaxCacheBytes:          268435456,
		MaxPlaylistBytes:       1048576,
		MaxSegmentBytes:        52428800,
		SegmentTimeoutSeconds:  10,
		PlaylistTimeoutSeconds: 10,
		MaxVariantHeight:       720,
		StaleCacheReapHours:    24,
	}}
	mutations := map[string]func(*config.BridgeConfig){
		"hls_buffer.enabled":                  func(c *config.BridgeConfig) { c.HLSBuffer.Enabled = false },
		"hls_buffer.live_edge_segments":       func(c *config.BridgeConfig) { c.HLSBuffer.LiveEdgeSegments = 4 },
		"hls_buffer.start_segments":           func(c *config.BridgeConfig) { c.HLSBuffer.StartSegments = 3 },
		"hls_buffer.max_cached_segments":      func(c *config.BridgeConfig) { c.HLSBuffer.MaxCachedSegments = 7 },
		"hls_buffer.max_cache_bytes":          func(c *config.BridgeConfig) { c.HLSBuffer.MaxCacheBytes++ },
		"hls_buffer.max_playlist_bytes":       func(c *config.BridgeConfig) { c.HLSBuffer.MaxPlaylistBytes++ },
		"hls_buffer.max_segment_bytes":        func(c *config.BridgeConfig) { c.HLSBuffer.MaxSegmentBytes++ },
		"hls_buffer.segment_timeout_seconds":  func(c *config.BridgeConfig) { c.HLSBuffer.SegmentTimeoutSeconds++ },
		"hls_buffer.playlist_timeout_seconds": func(c *config.BridgeConfig) { c.HLSBuffer.PlaylistTimeoutSeconds++ },
		"hls_buffer.max_variant_height":       func(c *config.BridgeConfig) { c.HLSBuffer.MaxVariantHeight++ },
		"hls_buffer.stale_cache_reap_hours":   func(c *config.BridgeConfig) { c.HLSBuffer.StaleCacheReapHours++ },
	}
	for key, mutate := range mutations {
		t.Run(key, func(t *testing.T) {
			next := old
			mutate(&next)
			if !containsStr(diffBridgeConfig(old, next), key) {
				t.Fatalf("diffBridgeConfig missing %s", key)
			}
			if got := scopeForBridgeField(key); got != adapters.ScopeRestartCast {
				t.Fatalf("scopeForBridgeField(%q) = %v, want ScopeRestartCast", key, got)
			}
		})
	}
}
```

- [ ] **Step 6: Run UI/scope tests to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui ./internal/uiserver
```

Expected: FAIL because HLS buffer fields are not in the UI table or bridge diff.

- [ ] **Step 7: Implement UI fields and bridge diff/scope**

Add HLS fields to `bridgeFields()` under section `"HLS Buffer"` with bool/int kinds, defaults from `config.HLSBufferConfig`, and `ApplyScope: adapters.ScopeRestartCast`. Add every `HLSBuffer` field to `diffBridgeConfig` and add an explicit `case` in `scopeForBridgeField` returning `ScopeRestartCast` for every `hls_buffer.*` key.

- [ ] **Step 8: Verify Task 1 package tests GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/config ./internal/ui ./internal/uiserver
```

Expected: PASS.

- [ ] **Step 9: Commit Task 1**

Run:

```bash
git add internal/config/config.go internal/config/migration.go internal/config/config_test.go internal/config/example.go internal/config/example.toml internal/ui/bridge_fields.go internal/ui/bridge_fields_test.go internal/uiserver/bridge_saver.go internal/uiserver/bridge_saver_test.go
git commit -m "feat(config): add hls buffer bridge settings"
```

## Task 2: HLS Playlist Parsing And Variant Selection

**Files:**
- Create: `internal/hlsbuffer/config.go`
- Create: `internal/hlsbuffer/playlist.go`
- Create: `internal/hlsbuffer/variant.go`
- Create: `internal/hlsbuffer/playlist_test.go`
- Create: `internal/hlsbuffer/variant_test.go`

- [ ] **Step 1: Write failing parser and variant tests**

Tests must cover:

```go
func TestParseMediaPlaylistAcceptsLiveSegments(t *testing.T)
func TestParseMediaPlaylistRejectsUnsupportedTags(t *testing.T)
func TestParseMediaPlaylistRejectsAudioOnly(t *testing.T)
func TestParseMasterPlaylistSelectsVariantByHeightThenBandwidth(t *testing.T)
func TestParseMasterPlaylistFallsBackWhenResolutionMissing(t *testing.T)
func TestVariantChangeRequiresRestart(t *testing.T)
```

Each parser test should pass an inline playlist string and assert exact segment URIs, durations, unsupported-tag errors, and selected variant URI.

- [ ] **Step 2: Run parser tests to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/hlsbuffer
```

Expected: FAIL because `internal/hlsbuffer` does not exist.

- [ ] **Step 3: Implement minimal parser/selector**

Create types:

```go
type PlaylistKind int

const (
	PlaylistMedia PlaylistKind = iota + 1
	PlaylistMaster
)

type Segment struct {
	URI      string
	Duration time.Duration
	Sequence int64
}

type Variant struct {
	URI       string
	Bandwidth int
	Width     int
	Height    int
	Codecs    string
}

type Playlist struct {
	Kind        PlaylistKind
	Target      time.Duration
	MediaSeq    int64
	Segments    []Segment
	Variants    []Variant
	Unsupported string
}
```

Implement `ParsePlaylist(body []byte) (Playlist, error)`, `SelectVariant(variants []Variant, outputHeight, maxVariantHeight int) (Variant, error)`, and `VariantCompatible(old, next Variant) bool`.

- [ ] **Step 4: Verify parser tests GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/hlsbuffer
```

Expected: PASS.

- [ ] **Step 5: Commit Task 2**

Run:

```bash
git add internal/hlsbuffer
git commit -m "feat(hls): parse playlists and select variants"
```

## Task 3: HLS URL Validation, Cache Paths, And Reaper

**Files:**
- Create: `internal/hlsbuffer/validator.go`
- Create: `internal/hlsbuffer/validator_test.go`
- Create: `internal/hlsbuffer/cache.go`
- Create: `internal/hlsbuffer/cache_test.go`
- Create: `internal/hlsbuffer/reaper.go`
- Create: `internal/hlsbuffer/reaper_test.go`

- [ ] **Step 1: Write failing validation and cache tests**

Tests must cover:

```go
func TestGenericPublicValidationRejectsLocalTargets(t *testing.T)
func TestGenericPublicValidationRejectsRedirectToLocalTarget(t *testing.T)
func TestBundledToonamiValidationRejectsWrongHostOrPath(t *testing.T)
func TestCacheFilenameCannotEscapeRoot(t *testing.T)
func TestCacheLimitsEnforceCountAndBytes(t *testing.T)
func TestReapStaleSessionsKeepsActiveLock(t *testing.T)
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/hlsbuffer
```

Expected: FAIL because validators/cache/reaper are missing.

- [ ] **Step 3: Implement validators and cache helpers**

Add `TrustModeGenericPublic` and `TrustModeBundledToonami`. Generic mode accepts only HTTP(S), rejects userinfo, loopback, link-local, private, metadata, `file:`, and validates every redirect target. Bundled Toonami mode requires host `api.toonamiaftermath.com:3000` and the expected channel path prefix.

- [ ] **Step 4: Verify validation/cache tests GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/hlsbuffer
```

Expected: PASS.

- [ ] **Step 5: Commit Task 3**

Run:

```bash
git add internal/hlsbuffer
git commit -m "feat(hls): validate urls and bound cache files"
```

## Task 4: HLS Buffered Session Lifecycle

**Files:**
- Create: `internal/hlsbuffer/session.go`
- Create: `internal/hlsbuffer/session_test.go`
- Modify: `internal/hlsbuffer/config.go`

- [ ] **Step 1: Write failing session tests**

Tests must cover:

```go
func TestOpenSessionWarmsStartSegmentsAndPublishesLocalPlaylist(t *testing.T)
func TestOpenSessionUsesLocalOnlyMediaPolicy(t *testing.T)
func TestOpenSessionCleansUpOnClose(t *testing.T)
func TestSlowSegmentServerIsSmoothedByPrefetch(t *testing.T)
func TestUnsupportedPlaylistFailsClearly(t *testing.T)
func TestSessionStatsReportUnits(t *testing.T)
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/hlsbuffer
```

Expected: FAIL because `OpenSession` does not exist.

- [ ] **Step 3: Implement session API**

Expose:

```go
type Session struct {
	PlaybackPath string
	Policy       core.MediaInputPolicy
	Stats        func() Stats
	Close        func() error
}

func OpenSession(ctx context.Context, opts SessionOptions) (*Session, error)
```

`OpenSession` fetches the playlist, selects a variant, caches at least `StartSegments` when possible, writes a local playlist using atomic replace, keeps reloading in a goroutine, and returns a cleanup function. The returned policy whitelists local file access only.

- [ ] **Step 4: Verify session tests GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/hlsbuffer
```

Expected: PASS.

- [ ] **Step 5: Commit Task 4**

Run:

```bash
git add internal/hlsbuffer
git commit -m "feat(hls): add buffered session lifecycle"
```

## Task 5: Streams Adapter Integration

**Files:**
- Modify: `internal/adapters/streams/config.go`
- Modify: `internal/adapters/streams/config_test.go`
- Modify: `internal/adapters/streams/adapter.go`
- Modify: `internal/adapters/streams/playback.go`
- Modify: `internal/adapters/streams/playback_test.go`

- [ ] **Step 1: Write failing Streams tests**

Tests must cover:

```go
func TestStreamsDirectHLSUsesBufferByDefault(t *testing.T)
func TestStreamsDirectHLSOptOutUsesDirectPath(t *testing.T)
func TestStreamsDirectHLSCleansBufferWhenCoreStartFails(t *testing.T)
func TestStreamsBufferedDirectHLSKeepsVideoMediaKind(t *testing.T)
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams
```

Expected: FAIL because Streams does not call `hlsbuffer`.

- [ ] **Step 3: Implement Streams wiring**

Add provider/channel opt-out config, store bridge config on `Adapter`, and inject a test seam:

```go
type hlsBufferOpener func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error)
```

When direct item is eligible and not disabled, call the opener, use `Session.PlaybackPath` and `Session.Policy`, attach cleanup into `OnStop`, and clean up immediately if core start fails or guarded start does not match.

- [ ] **Step 4: Verify Streams tests GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams
```

Expected: PASS.

- [ ] **Step 5: Commit Task 5**

Run:

```bash
git add internal/adapters/streams
git commit -m "feat(streams): buffer bundled direct hls"
```

## Task 6: URL Adapter Integration

**Files:**
- Modify: `internal/adapters/url/config.go`
- Modify: `internal/adapters/url/history.go`
- Modify: `internal/adapters/url/play.go`
- Modify: `internal/adapters/url/ui.go`
- Modify: `internal/adapters/url/play_test.go`
- Modify: `internal/adapters/url/history_test.go`
- Modify: `internal/adapters/url/ui_test.go`
- Modify: `internal/adapters/url/controls_test.go`

- [ ] **Step 1: Write failing URL tests**

Tests must cover:

```go
func TestURLDirectM3U8UsesBufferByDefault(t *testing.T)
func TestURLDirectM3U8OffBypassesBuffer(t *testing.T)
func TestURLHistoryReplayPreservesHLSBufferMode(t *testing.T)
func TestURLBufferedM3U8CleansBufferOnStartFailure(t *testing.T)
func TestURLBufferedM3U8KeepsVideoMediaKind(t *testing.T)
func TestURLPanelRendersHLSBufferModeControl(t *testing.T)
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url
```

Expected: FAIL because URL does not parse or persist `hls_buffer` mode.

- [ ] **Step 3: Implement URL mode parsing/history/UI**

Extend URL parsing to return `rawURL`, `mode`, and `hlsBufferMode`. Add `HLSBufferMode string `json:"hls_buffer_mode,omitempty"` to `HistoryEntry`. Add `AddOrBumpWithHLSMode(rawURL, mode string)` and use it for play and history replay. Render a compact select in the URL panel:

```html
<select name="hls_buffer">
  <option value="auto" selected>HLS buffer: auto</option>
  <option value="off">HLS buffer: off</option>
</select>
```

- [ ] **Step 4: Implement URL buffering**

For direct `.m3u8` mode with global config enabled and `hls_buffer != "off"`, call `hlsbuffer.OpenSession` with `TrustModeGenericPublic`, start core with local path and local-only policy, and clean up on start failure or OnStop.

- [ ] **Step 5: Verify URL tests GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/url
```

Expected: PASS.

- [ ] **Step 6: Commit Task 6**

Run:

```bash
git add internal/adapters/url
git commit -m "feat(url): buffer direct m3u8 casts"
```

## Task 7: Production Wiring, Docs, And Full Verification

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`
- Modify: `README.md`
- Modify: `docs/operations.md`
- Modify: `docs/url-adapter.md`
- Modify: `docs/superpowers/specs/2026-05-14-hls-buffering-design.md`

- [ ] **Step 1: Wire production dependencies**

Pass `sec.Bridge` into adapters where needed and let production adapters use `hlsbuffer.OpenSession`.

- [ ] **Step 2: Document v1 behavior**

Document:

- default-on HLS buffering for eligible Streams and URL direct `.m3u8`;
- `GROOVY_HLS_BUFFER=0`;
- per-cast URL `hls_buffer=off`;
- live delay expectation;
- `BUFFERING...` is deferred to a dataplane follow-up.

- [ ] **Step 3: Run focused package tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/config ./internal/hlsbuffer ./internal/adapters/streams ./internal/adapters/url ./internal/ui ./internal/uiserver ./cmd/mister-groovy-relay
```

Expected: PASS.

- [ ] **Step 4: Run full test suite**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit Task 7**

Run:

```bash
git add cmd/mister-groovy-relay/main.go README.md docs/operations.md docs/url-adapter.md docs/superpowers/specs/2026-05-14-hls-buffering-design.md
git commit -m "docs: document hls buffer rollout"
```

## Self-Review Checklist

- Spec coverage: Tasks cover config, scope, cache byte/count limits, variant selection, public/bundled validation, stale reaping, Streams integration, URL integration, per-source/per-cast opt-out, MediaKind video, and metrics units.
- Deferred scope: No task implements dataplane slate rendering or audio hold; Task 7 documents the deferral.
- TDD: Every implementation task starts with failing tests and an explicit RED command.
- Dirty main checkout: Work happens in `.worktrees/hls-buffering-v1`; main checkout changes are not touched.
