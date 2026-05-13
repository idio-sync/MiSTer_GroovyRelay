# Toonami Aftermath Streams Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Toonami Aftermath to the existing Streams page as one bundled provider with East, West, Movies, and Radio direct HLS channels.

**Architecture:** Introduce `direct-streams` as a bundled-only Streams catalog type. It builds one direct `StreamItem` per channel from trusted bundled definitions, bypasses remote manifest/catalog fetching, skips yt-dlp during playback, and starts core sessions with a constrained HLS `MediaInputPolicy`. Remote/cached manifests remain limited to remote-eligible catalog providers and cannot add, remove, or override the bundled direct-stream provider.

**Tech Stack:** Go 1.26.2, stdlib `net/url`, `strings`, `time`, existing `internal/adapters/streams`, `internal/core.MediaInputPolicy`, htmx-rendered Streams panel, Go unit tests via `cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test`.

**Spec:** [docs/superpowers/specs/2026-05-13-toonami-aftermath-streams-design.md](../specs/2026-05-13-toonami-aftermath-streams-design.md)

---

## Files

**Create:**
- `internal/adapters/streams/provider_direct_streams.go` — bundled-only direct HLS catalog builder, Toonami URL validator, direct HLS input policy helper.
- `internal/adapters/streams/provider_direct_streams_test.go` — direct catalog, URL validation, and non-direct URL-field regression tests.

**Modify:**
- `internal/adapters/streams/provider.go` — add `ChannelDefinition.URL` and `StreamItem.Direct`.
- `internal/adapters/streams/assets.go` — add `direct-streams` constant and `bundledToonamiAftermathDefinition()`.
- `internal/adapters/streams/manifest.go` — split remote-eligible provider validation from bundled provider support; protect bundled direct-stream providers from remote/cached overlays.
- `internal/adapters/streams/manifest_test.go` — hosted manifest expectations, remote/cached direct-stream rejection, bundled provider preservation tests.
- `internal/adapters/streams/refresh.go` — dispatch direct-stream catalog builds without `fetchProviderPlaylist`, seed files, or catalog cache writes.
- `internal/adapters/streams/refresh_test.go` — startup and manual refresh coverage for Toonami; no remote fetch for direct-stream catalogs.
- `internal/adapters/streams/queue.go` — keep one-item direct queues in `loopNone`.
- `internal/adapters/streams/queue_test.go` — direct one-item queue capability behavior.
- `internal/adapters/streams/playback.go` — skip yt-dlp for `StreamItem.Direct`, set direct HLS policy, no pause support.
- `internal/adapters/streams/playback_test.go` — direct playback request, resolver bypass, policy, headers, replay reconnect.
- `internal/adapters/streams/ui.go` — avoid advertising Pause for direct live queues.
- `internal/adapters/streams/ui_test.go` — provider/channel display tests.
- `internal/adapters/streams/routes_test.go` — provider count, filter, status, and capability JSON tests.
- `README.md` — mention Toonami Aftermath wherever built-in Streams sources are listed.

**Do not modify for provider data:**
- `docs/streams/providers.json` — keep Toonami absent because this file is the remote manifest surface. Update tests only.

---

## Task 1: Direct Provider Model And Bundled Toonami Catalog

**Files:**
- Modify: `internal/adapters/streams/provider.go`
- Modify: `internal/adapters/streams/assets.go`
- Create: `internal/adapters/streams/provider_direct_streams.go`
- Create: `internal/adapters/streams/provider_direct_streams_test.go`

- [ ] **Step 1: Write failing direct catalog tests**

Create `internal/adapters/streams/provider_direct_streams_test.go`:

```go
package streams

import "testing"

func TestBuildDirectStreamsCatalogToonami(t *testing.T) {
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	if cat.ProviderID != "toonami-aftermath" || cat.Name != "Toonami Aftermath" {
		t.Fatalf("catalog identity = %#v", cat)
	}
	if len(cat.Groups) != 1 || cat.Groups[0].ID != "live" || cat.Groups[0].Name != "Live Channels" {
		t.Fatalf("groups = %#v", cat.Groups)
	}
	if len(cat.Channels) != 4 {
		t.Fatalf("channels = %d, want 4", len(cat.Channels))
	}
	want := map[string]string{
		"east":   "http://api.toonamiaftermath.com:3000/est/playlist.m3u8",
		"west":   "http://api.toonamiaftermath.com:3000/pst/playlist.m3u8",
		"movies": "http://api.toonamiaftermath.com:3000/movies/playlist.m3u8",
		"radio":  "http://api.toonamiaftermath.com:3000/radio/playlist.m3u8",
	}
	for _, ch := range cat.Channels {
		if ch.GroupID != "live" {
			t.Fatalf("channel %q group = %q, want live", ch.ID, ch.GroupID)
		}
		if len(ch.Items) != 1 {
			t.Fatalf("channel %q items = %d, want 1", ch.ID, len(ch.Items))
		}
		item := ch.Items[0]
		if item.ID != ch.ID || item.SourceID != ch.ID || item.Title != ch.Name {
			t.Fatalf("channel %q item identity = %#v", ch.ID, item)
		}
		if item.URL != want[ch.ID] {
			t.Fatalf("channel %q URL = %q, want %q", ch.ID, item.URL, want[ch.ID])
		}
		if !item.Direct {
			t.Fatalf("channel %q item Direct = false, want true", ch.ID)
		}
	}
}

func TestBuildDirectStreamsCatalogRejectsBadURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "blank", url: ""},
		{name: "userinfo", url: "http://user@api.toonamiaftermath.com:3000/est/playlist.m3u8"},
		{name: "ftp", url: "ftp://api.toonamiaftermath.com:3000/est/playlist.m3u8"},
		{name: "missing host", url: "http:///playlist.m3u8"},
		{name: "wrong host", url: "http://example.com/est/playlist.m3u8"},
		{name: "wrong path", url: "http://api.toonamiaftermath.com:3000/evil/playlist.m3u8"},
		{name: "wrong port", url: "http://api.toonamiaftermath.com/est/playlist.m3u8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := bundledToonamiAftermathDefinition()
			def.Channels[0].URL = tt.url
			if _, err := buildDirectStreamsCatalog(def); err == nil {
				t.Fatal("bad direct stream URL accepted")
			}
		})
	}
}

func TestYouTubeChannelCatalogIgnoresChannelDefinitionURL(t *testing.T) {
	def := bundledMTVDefinition()
	def.Channels = []ChannelDefinition{{
		ID:       "metal",
		Name:     "Metal",
		URL:      "http://api.toonamiaftermath.com:3000/est/playlist.m3u8",
		PlayMode: PlayShuffle,
	}}
	cat, err := buildYouTubeChannelCatalog(def, []byte(`{"metal":["dQw4w9WgXcQ"]}`), DefaultConfig())
	if err != nil {
		t.Fatalf("buildYouTubeChannelCatalog: %v", err)
	}
	item := cat.Channel("metal").Items[0]
	if item.URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("item URL = %q, want YouTube watch URL", item.URL)
	}
	if item.Direct {
		t.Fatal("YouTube item Direct = true, want false")
	}
}
```

- [ ] **Step 2: Run direct catalog tests and verify they fail**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "TestBuildDirectStreamsCatalog|TestYouTubeChannelCatalogIgnoresChannelDefinitionURL" -count=1
```

Expected: FAIL with undefined `bundledToonamiAftermathDefinition`, undefined `buildDirectStreamsCatalog`, and missing `URL`/`Direct` fields.

- [ ] **Step 3: Add model fields**

Modify `internal/adapters/streams/provider.go`:

```go
type ChannelDefinition struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	GroupID     string   `json:"group_id,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	URL         string   `json:"url,omitempty"`
	PlayMode    PlayMode `json:"play_mode,omitempty"`
	Order       int      `json:"order"`
}
```

```go
type StreamItem struct {
	ID       string
	Title    string
	URL      string
	SourceID string
	Direct   bool
}
```

- [ ] **Step 4: Add bundled Toonami provider definition**

Modify `internal/adapters/streams/assets.go`:

```go
const (
	youtubeChannelJSONProviderType = "youtube-channel-json"
	directStreamsProviderType      = "direct-streams"
)
```

Add `bundledToonamiAftermathDefinition()` to `bundledManifest().Providers` after Cartoon Rewind:

```go
func bundledManifest() Manifest {
	return Manifest{
		Version: 1,
		Providers: []ProviderDefinition{
			bundledMTVDefinition(),
			bundledCartoonDefinition(),
			bundledToonamiAftermathDefinition(),
		},
	}
}
```

Add the provider definition:

```go
func bundledToonamiAftermathDefinition() ProviderDefinition {
	return ProviderDefinition{
		ID:              "toonami-aftermath",
		Type:            directStreamsProviderType,
		DisplayName:     "Toonami Aftermath",
		BaseURL:         "https://www.toonamiaftermath.com",
		DefaultChannel:  "east",
		DefaultPlayMode: PlaySequential,
		Groups: []GroupDefinition{
			{ID: "live", Name: "Live Channels", Order: 10},
		},
		Channels: []ChannelDefinition{
			{ID: "east", Name: "East", GroupID: "live", URL: "http://api.toonamiaftermath.com:3000/est/playlist.m3u8", PlayMode: PlaySequential, Order: 10},
			{ID: "west", Name: "West", GroupID: "live", URL: "http://api.toonamiaftermath.com:3000/pst/playlist.m3u8", PlayMode: PlaySequential, Order: 20},
			{ID: "movies", Name: "Movies", GroupID: "live", URL: "http://api.toonamiaftermath.com:3000/movies/playlist.m3u8", PlayMode: PlaySequential, Order: 30},
			{ID: "radio", Name: "Radio", GroupID: "live", URL: "http://api.toonamiaftermath.com:3000/radio/playlist.m3u8", PlayMode: PlaySequential, Order: 40},
		},
	}
}
```

- [ ] **Step 5: Add direct catalog builder**

Create `internal/adapters/streams/provider_direct_streams.go`:

```go
package streams

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

var toonamiAftermathPaths = map[string]struct{}{
	"/est/playlist.m3u8":    {},
	"/pst/playlist.m3u8":    {},
	"/movies/playlist.m3u8": {},
	"/radio/playlist.m3u8":  {},
}

func buildDirectStreamsCatalog(def ProviderDefinition) (ProviderCatalog, error) {
	if def.Type != directStreamsProviderType {
		return ProviderCatalog{}, fmt.Errorf("provider %q type %q is unsupported", def.ID, def.Type)
	}
	groupByID := make(map[string]ChannelGroup, len(def.Groups))
	groups := make([]ChannelGroup, 0, len(def.Groups))
	for _, group := range def.Groups {
		g := ChannelGroup{ID: group.ID, Name: group.Name, Order: group.Order}
		groupByID[group.ID] = g
		groups = append(groups, g)
	}
	channels := make([]Channel, 0, len(def.Channels))
	for _, chDef := range def.Channels {
		if err := validateDirectStreamChannelURL(def.ID, chDef.URL); err != nil {
			return ProviderCatalog{}, fmt.Errorf("provider %q channel %q url: %w", def.ID, chDef.ID, err)
		}
		ch := channelFromDefinition(chDef.ID, chDef, true, def)
		ch.Items = []StreamItem{{
			ID:       ch.ID,
			Title:    ch.Name,
			URL:      chDef.URL,
			SourceID: ch.ID,
			Direct:   true,
		}}
		if ch.GroupID != "" {
			if _, ok := groupByID[ch.GroupID]; !ok {
				return ProviderCatalog{}, fmt.Errorf("provider %q channel %q references unknown group %q", def.ID, ch.ID, ch.GroupID)
			}
		}
		channels = append(channels, ch)
	}
	sortChannelGroups(groups)
	sortChannels(channels)
	return ProviderCatalog{
		ProviderID: def.ID,
		Name:       def.DisplayName,
		Groups:     groups,
		Channels:   channels,
		UpdatedAt:  time.Now(),
	}, nil
}

func validateDirectStreamChannelURL(providerID, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.User != nil {
		return fmt.Errorf("userinfo is not allowed")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("host is required")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("scheme %q is not allowed", scheme)
	}
	if providerID == "toonami-aftermath" {
		host := strings.ToLower(u.Host)
		if host != "api.toonamiaftermath.com:3000" {
			return fmt.Errorf("host %q is not allowed", host)
		}
		if _, ok := toonamiAftermathPaths[u.EscapedPath()]; !ok {
			return fmt.Errorf("path %q is not allowed", u.EscapedPath())
		}
	}
	return nil
}
```

- [ ] **Step 6: Run direct catalog tests and verify they pass**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "TestBuildDirectStreamsCatalog|TestYouTubeChannelCatalogIgnoresChannelDefinitionURL" -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 1**

```bash
git add internal/adapters/streams/provider.go internal/adapters/streams/assets.go internal/adapters/streams/provider_direct_streams.go internal/adapters/streams/provider_direct_streams_test.go
git commit -m "feat(streams): add bundled toonami direct catalog"
```

---

## Task 2: Manifest Safety For Bundled-Only Direct Streams

**Files:**
- Modify: `internal/adapters/streams/manifest.go`
- Modify: `internal/adapters/streams/manifest_test.go`

- [ ] **Step 1: Write failing manifest safety tests**

Append these tests to `internal/adapters/streams/manifest_test.go`:

```go
func TestHostedProviderManifestOmitsBundledOnlyDirectStreams(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "streams", "providers.json"))
	if err != nil {
		t.Fatalf("read hosted manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse hosted manifest: %v", err)
	}
	if _, ok := manifest.Provider("toonami-aftermath"); ok {
		t.Fatal("hosted remote manifest must not include bundled-only direct streams provider")
	}
	for _, want := range bundledManifest().Providers {
		if want.Type == directStreamsProviderType {
			continue
		}
		if _, ok := manifest.Provider(want.ID); !ok {
			t.Fatalf("hosted manifest missing remote-eligible bundled provider %q", want.ID)
		}
	}
}

func TestValidateManifestIgnoresRemoteDirectStreamsBeforePlaylistValidation(t *testing.T) {
	m := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID:          "remote-direct",
		Type:        directStreamsProviderType,
		DisplayName: "Remote Direct",
		Channels: []ChannelDefinition{{
			ID:   "x",
			Name: "X",
			URL:  "http://api.toonamiaftermath.com:3000/est/playlist.m3u8",
		}},
	}}}
	if err := validateManifest(t.Context(), m, DefaultConfig()); err != nil {
		t.Fatalf("remote direct-streams should be ignored before playlist validation: %v", err)
	}
}

func TestMergeManifestsRejectsRemoteDirectStreamsOverlay(t *testing.T) {
	bundled := bundledManifest()
	remote := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID:              "toonami-aftermath",
		Type:            directStreamsProviderType,
		DisplayName:     "Changed",
		DefaultChannel:  "east",
		DefaultPlayMode: PlaySequential,
		Channels: []ChannelDefinition{{
			ID:   "east",
			Name: "East",
			URL:  "http://api.toonamiaftermath.com:3000/pst/playlist.m3u8",
		}},
	}}}
	got := mergeManifests(DefaultConfig(), bundled, nil, &remote, remoteProviderFactories())
	provider, ok := got.Provider("toonami-aftermath")
	if !ok {
		t.Fatal("bundled Toonami provider missing")
	}
	if provider.DisplayName != "Toonami Aftermath" {
		t.Fatalf("display name = %q, want bundled value", provider.DisplayName)
	}
	if len(provider.Channels) != 4 || provider.Channels[0].URL != "http://api.toonamiaftermath.com:3000/est/playlist.m3u8" {
		t.Fatalf("remote overlay changed Toonami channels: %+v", provider.Channels)
	}
}

func TestMergeManifestsRejectsCachedDirectStreamsOverlay(t *testing.T) {
	bundled := bundledManifest()
	cached := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID:              "toonami-aftermath",
		Type:            directStreamsProviderType,
		DisplayName:     "Cached Changed",
		DefaultChannel:  "east",
		DefaultPlayMode: PlaySequential,
		Channels: []ChannelDefinition{{
			ID:   "east",
			Name: "East",
			URL:  "http://api.toonamiaftermath.com:3000/pst/playlist.m3u8",
		}},
	}}}
	got := mergeManifests(DefaultConfig(), bundled, &cached, nil, remoteProviderFactories())
	provider, ok := got.Provider("toonami-aftermath")
	if !ok {
		t.Fatal("bundled Toonami provider missing")
	}
	if provider.DisplayName != "Toonami Aftermath" || len(provider.Channels) != 4 {
		t.Fatalf("cached overlay changed bundled provider: %+v", provider)
	}
}

func TestMergeManifestsRejectsRemoteTypeChangeToDirectStreams(t *testing.T) {
	bundled := bundledManifest()
	remote := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID:              "mtv-rewind",
		Type:            directStreamsProviderType,
		DisplayName:     "MTV As Direct",
		DefaultChannel:  "east",
		DefaultPlayMode: PlaySequential,
	}}}
	got := mergeManifests(DefaultConfig(), bundled, nil, &remote, remoteProviderFactories())
	provider, ok := got.Provider("mtv-rewind")
	if !ok {
		t.Fatal("bundled MTV provider missing")
	}
	if provider.Type != youtubeChannelJSONProviderType {
		t.Fatalf("provider type = %q, want %q", provider.Type, youtubeChannelJSONProviderType)
	}
}
```

Update `TestHostedProviderManifestFileValidates` so the existing parity loop skips `directStreamsProviderType`:

```go
for _, want := range bundledManifest().Providers {
	if want.Type == directStreamsProviderType {
		continue
	}
	// existing parity assertions remain unchanged
}
```

- [ ] **Step 2: Run manifest tests and verify they fail**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "TestHostedProviderManifest|TestValidateManifestIgnoresRemoteDirectStreams|TestMergeManifestsRejects" -count=1
```

Expected: FAIL because `remoteProviderFactories` does not exist and manifest merge/validation still use one provider factory set.

- [ ] **Step 3: Split remote provider support from bundled catalog support**

Modify `internal/adapters/streams/manifest.go`:

```go
func isUnsupportedProviderType(providerType string) bool {
	providerType = strings.TrimSpace(providerType)
	if providerType == "" {
		return false
	}
	_, ok := remoteProviderFactories()[providerType]
	return !ok
}
```

```go
func remoteProviderFactories() map[string]ProviderFactory {
	return map[string]ProviderFactory{
		youtubeChannelJSONProviderType: func(ProviderDefinition) (Provider, error) { return struct{}{}, nil },
	}
}
```

Rename the existing `providerFactories()` function to `remoteProviderFactories()` and update its call site in `isUnsupportedProviderType`.

- [ ] **Step 4: Protect bundled providers in mergeManifests**

Modify `mergeManifests` in `internal/adapters/streams/manifest.go` so factory checks apply only to remote/cached providers and bundled direct-stream providers cannot be overlaid:

```go
addProvider := func(provider ProviderDefinition, remoteOverlay bool) {
	if provider.ID == "" || provider.ID == reservedAdhocID {
		return
	}
	if remoteOverlay {
		if factories != nil {
			if _, ok := factories[provider.Type]; !ok {
				return
			}
		}
		if bundledTypes[provider.ID] == directStreamsProviderType {
			return
		}
		if bundledTypes[provider.ID] != "" && bundledTypes[provider.ID] != provider.Type {
			return
		}
		if provider.Type == directStreamsProviderType {
			return
		}
	}
	if existingIndex, exists := index[provider.ID]; exists {
		out.Providers[existingIndex] = provider
		return
	}
	out.Providers = append(out.Providers, provider)
	index[provider.ID] = len(out.Providers) - 1
}
```

Retain the existing disabled-provider filter after merging.

- [ ] **Step 5: Update merge call sites**

Modify `internal/adapters/streams/refresh.go` to pass `remoteProviderFactories()`:

```go
manifest := mergeManifests(cfg, bundledManifest(), cached, nil, remoteProviderFactories())
```

```go
manifest := mergeManifests(cfg, bundledManifest(), nil, &remote, remoteProviderFactories())
```

Leave tests that pass explicit ad hoc factory maps unchanged; they intentionally exercise known non-standard provider types.

- [ ] **Step 6: Run manifest tests and verify they pass**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "TestHostedProviderManifest|TestValidateManifestIgnoresRemoteDirectStreams|TestMergeManifestsRejects|TestValidateManifestStillRejectsKnownProviderMissingPlaylistURL|TestMergeManifestsRemoteChangingBundledTypeIgnored" -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

```bash
git add internal/adapters/streams/manifest.go internal/adapters/streams/manifest_test.go internal/adapters/streams/refresh.go
git commit -m "fix(streams): keep direct providers bundled-only"
```

---

## Task 3: Direct Catalog Build And Refresh Integration

**Files:**
- Modify: `internal/adapters/streams/refresh.go`
- Modify: `internal/adapters/streams/refresh_test.go`

- [ ] **Step 1: Write failing startup and refresh tests**

Add these tests to `internal/adapters/streams/refresh_test.go`:

```go
func TestStartDisabledLoadsBundledToonamiCatalog(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	cat := a.catalogSnapshotForTest("toonami-aftermath")
	if cat.ProviderID != "toonami-aftermath" || len(cat.Channels) != 4 {
		t.Fatalf("Toonami catalog = %+v", cat)
	}
	if got := cat.Channel("east").Items[0].URL; got != "http://api.toonamiaftermath.com:3000/est/playlist.m3u8" {
		t.Fatalf("east URL = %q", got)
	}
}

func TestRefreshNowDirectStreamsBypassesPlaylistFetchWhenRemoteDisabled(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	toonami := bundledToonamiAftermathDefinition()
	a.replaceDefinitionsForTest([]ProviderDefinition{bundledMTVDefinition(), bundledCartoonDefinition(), toonami})
	a.replaceCatalogsForTest(nil)
	a.mu.Lock()
	a.cfg.AllowRemoteManifest = false
	a.mu.Unlock()

	status := a.RefreshNow(t.Context(), "toonami-aftermath")
	if status.Err != nil {
		t.Fatalf("RefreshNow toonami: %v", status.Err)
	}
	cat := a.catalogSnapshotForTest("toonami-aftermath")
	if cat.ProviderID != "toonami-aftermath" || len(cat.Channels) != 4 {
		t.Fatalf("refreshed Toonami catalog = %+v", cat)
	}
}

func TestRefreshCatalogsDirectStreamsDoesNotFetchPlaylist(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	toonami := bundledToonamiAftermathDefinition()
	a.replaceDefinitionsForTest([]ProviderDefinition{toonami})
	a.replaceCatalogsForTest(nil)
	a.mu.Lock()
	a.cfg.AllowRemoteManifest = true
	a.mu.Unlock()

	status := a.refreshCatalogsDefault(t.Context(), []string{"toonami-aftermath"}, "manual")
	if status.Err != nil {
		t.Fatalf("refreshCatalogsDefault: %v", status.Err)
	}
	if !reflect.DeepEqual(status.refreshedProviderIDs, []string{"toonami-aftermath"}) {
		t.Fatalf("refreshed provider IDs = %#v", status.refreshedProviderIDs)
	}
	cat := a.catalogSnapshotForTest("toonami-aftermath")
	if cat.Channel("radio") == nil {
		t.Fatalf("radio channel missing from catalog: %+v", cat.Channels)
	}
}
```

- [ ] **Step 2: Run refresh tests and verify they fail**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "TestStartDisabledLoadsBundledToonamiCatalog|TestRefreshNowDirectStreams|TestRefreshCatalogsDirectStreams" -count=1
```

Expected: FAIL because startup has no direct seed path/build dispatch and refresh still calls `fetchProviderPlaylist`.

- [ ] **Step 3: Dispatch buildProviderCatalog by provider type**

Modify `internal/adapters/streams/refresh.go`:

```go
func buildProviderCatalog(def ProviderDefinition, raw []byte, cfg Config) (ProviderCatalog, error) {
	switch def.Type {
	case youtubeChannelJSONProviderType:
		return buildYouTubeChannelCatalog(def, raw, cfg)
	case directStreamsProviderType:
		return buildDirectStreamsCatalog(def)
	default:
		return ProviderCatalog{}, fmt.Errorf("provider %q type %q is unsupported", def.ID, def.Type)
	}
}
```

No fake `raw` body is required for `direct-streams`.

- [ ] **Step 4: Build direct catalogs during startup snapshot**

Modify `buildCachedOrSeedSnapshot` in `internal/adapters/streams/refresh.go`:

```go
for _, def := range defs {
	if def.Type == directStreamsProviderType {
		cat, err := buildProviderCatalog(def, nil, cfg)
		if err != nil {
			return nil, nil, err
		}
		catalogs = append(catalogs, cat)
		continue
	}
	// existing cache-or-seed path remains unchanged
}
```

- [ ] **Step 5: Bypass playlist fetch during catalog refresh**

Modify `refreshCatalogsDefault` in `internal/adapters/streams/refresh.go` so direct-stream providers are rebuilt locally before remote-fetch gating:

```go
func (a *Adapter) refreshCatalogsDefault(ctx context.Context, providerIDs []string, reason string) RefreshStatus {
	_ = reason
	cfg := a.configSnapshot()
	status := RefreshStatus{Source: "bundled", FetchedAt: time.Now().UTC()}
	if len(providerIDs) == 1 {
		status.ProviderID = providerIDs[0]
	}

	defs, err := a.definitionsForRefresh(providerIDs)
	if err != nil {
		a.recordRefreshFailure(err)
		status.Err = err
		return status
	}

	remoteAllowed := cfg.AllowRemoteManifest
	var errs []error
	for _, def := range defs {
		if def.Type == directStreamsProviderType {
			cat, err := buildProviderCatalog(def, nil, cfg)
			if err != nil {
				errs = append(errs, fmt.Errorf("provider %q build catalog: %w", def.ID, err))
				continue
			}
			a.mu.Lock()
			a.catalogs[cat.ProviderID] = cat
			if a.state != adapters.StateStopped {
				a.state = adapters.StateRunning
			}
			a.stateSince = time.Now()
			a.mu.Unlock()
			status.refreshedProviderIDs = append(status.refreshedProviderIDs, def.ID)
			continue
		}
		if !remoteAllowed {
			continue
		}
		raw, meta, err := fetchProviderPlaylist(ctx, def, cfg, a.cacheDir)
		// keep existing fetch/build/cache/install logic for non-direct providers
		_ = raw
		_ = meta
	}
	// keep existing status.Source, error joining, and lastErr clearing logic
}
```

When applying this snippet, preserve the existing non-direct fetch/build/cache/install block instead of replacing it with `_ = raw`.

- [ ] **Step 6: Run refresh tests and verify they pass**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "TestStartDisabledLoadsBundled|TestRefreshNowDirectStreams|TestRefreshCatalogsDirectStreams|TestRefreshOnceFallsBackToBundledCatalogsWhenManifestFetchFails" -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

```bash
git add internal/adapters/streams/refresh.go internal/adapters/streams/refresh_test.go
git commit -m "fix(streams): refresh direct catalogs locally"
```

---

## Task 4: Direct HLS Playback, Queue Controls, And Policy

**Files:**
- Modify: `internal/adapters/streams/queue.go`
- Modify: `internal/adapters/streams/playback.go`
- Modify: `internal/adapters/streams/ui.go`
- Modify: `internal/adapters/streams/queue_test.go`
- Modify: `internal/adapters/streams/playback_test.go`
- Modify: `internal/adapters/streams/routes_test.go`

- [ ] **Step 1: Write failing queue and playback tests**

Add to `internal/adapters/streams/queue_test.go`:

```go
func TestBuildQueueDirectSingleItemUsesLoopNone(t *testing.T) {
	q, err := buildQueue("toonami-aftermath", Channel{
		ID:       "east",
		Name:     "East",
		PlayMode: PlaySequential,
		Items: []StreamItem{{
			ID:     "east",
			URL:    "http://api.toonamiaftermath.com:3000/est/playlist.m3u8",
			Direct: true,
		}},
	}, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatalf("buildQueue: %v", err)
	}
	if q.loopMode != loopNone {
		t.Fatalf("loopMode = %v, want loopNone", q.loopMode)
	}
	if q.canAdvanceNext() || q.canAdvancePrevious() {
		t.Fatal("direct single-item queue should not advance next/previous")
	}
}
```

Add to `internal/adapters/streams/playback_test.go`:

```go
func TestStartResolvedDirectStreamSkipsResolverAndSetsPolicy(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	resolver := &fakeResolver{res: &ytdlp.Resolution{
		URL:     "https://media.example/should-not-be-used.m3u8",
		Headers: map[string]string{"User-Agent": "yt-dlp"},
	}}
	a.resolver = resolver

	_, err = a.StartResolvedStream(t.Context(), streamhandoff.Resolution{
		ProviderID: "toonami-aftermath",
		ChannelID:  "east",
	})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls = %d, want 0", resolver.calls)
	}
	req := c.lastReq
	if req.StreamURL != "http://api.toonamiaftermath.com:3000/est/playlist.m3u8" {
		t.Fatalf("StreamURL = %q", req.StreamURL)
	}
	if len(req.InputHeaders) != 0 || len(req.AudioInputHeaders) != 0 {
		t.Fatalf("headers leaked into direct request: video=%v audio=%v", req.InputHeaders, req.AudioInputHeaders)
	}
	if req.Capabilities.CanPause || req.Capabilities.CanSeek {
		t.Fatalf("capabilities = %+v, want no pause/seek", req.Capabilities)
	}
	wantProtocols := "file,http,https,tcp,tls,crypto"
	if got := strings.Join(req.MediaInputPolicy.ProtocolWhitelist, ","); got != wantProtocols {
		t.Fatalf("ProtocolWhitelist = %q, want %q", got, wantProtocols)
	}
	if !req.MediaInputPolicy.DisableRedirects || !req.MediaInputPolicy.DisableReconnect {
		t.Fatalf("redirect/reconnect policy = %+v", req.MediaInputPolicy)
	}
	if req.MediaInputPolicy.RWTimeout != 5*time.Second {
		t.Fatalf("RWTimeout = %s, want 5s", req.MediaInputPolicy.RWTimeout)
	}
	if got := strings.Join(req.MediaInputPolicy.BlockedHeaders, ","); got != "Cookie,Authorization,Proxy-Authorization,Referer" {
		t.Fatalf("BlockedHeaders = %q", got)
	}
}

func TestReplayDirectStreamRebuildsFromCatalogItem(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})

	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "toonami-aftermath", ChannelID: "east"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if err := a.Replay(t.Context()); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if c.startCalls != 2 {
		t.Fatalf("StartSession calls = %d, want 2", c.startCalls)
	}
	if c.lastReq.StreamURL != "http://api.toonamiaftermath.com:3000/est/playlist.m3u8" {
		t.Fatalf("replay StreamURL = %q", c.lastReq.StreamURL)
	}
}
```

Add imports to `playback_test.go` as needed:

```go
import (
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
)
```

- [ ] **Step 2: Run queue/playback tests and verify they fail**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "TestBuildQueueDirectSingleItemUsesLoopNone|TestStartResolvedDirectStreamSkipsResolverAndSetsPolicy|TestReplayDirectStreamRebuildsFromCatalogItem" -count=1
```

Expected: FAIL because direct queues still use sequential looping and playback still resolves through yt-dlp.

- [ ] **Step 3: Keep one-item direct queues in loopNone**

Modify `buildQueue` in `internal/adapters/streams/queue.go` before the play-mode switch:

```go
if len(items) == 1 && items[0].Direct {
	return &ActiveQueue{
		ProviderID:     providerID,
		ChannelID:      ch.ID,
		ChannelName:    ch.Name,
		Items:          items,
		baseItems:      baseItems,
		loopMode:       loopNone,
		StartedAt:      time.Now(),
		Generation:     0,
		ItemToken:      0,
		LastResolvedAt: time.Time{},
		cancelResolve:  nil,
	}, nil
}
```

- [ ] **Step 4: Add direct HLS policy helper and playback branch**

Modify `internal/adapters/streams/playback.go` imports to include `time`.

Add helper functions:

```go
func directHLSInputPolicy() core.MediaInputPolicy {
	return core.MediaInputPolicy{
		ProtocolWhitelist: []string{"file", "http", "https", "tcp", "tls", "crypto"},
		DisableRedirects:  true,
		DisableReconnect:  true,
		RWTimeout:         5 * time.Second,
		BlockedHeaders:    []string{"Cookie", "Authorization", "Proxy-Authorization", "Referer"},
	}
}

func isDirectStreamItem(item StreamItem) bool {
	return item.Direct
}
```

In `playCurrentGuarded`, after `pageURL`/`ref`/`capture` are prepared and before resolver checks, add:

```go
if isDirectStreamItem(item) {
	if coreManager == nil {
		cancel()
		a.clearResolveIfCurrent(capture)
		return streamhandoff.StartResult{}, playbackError(q.ProviderID, "core playback manager is not configured")
	}
	title := streamSessionTitle(item, "")
	req := core.SessionRequest{
		StreamURL:         pageURL,
		AdapterRef:        ref,
		DirectPlay:        true,
		Capabilities:      core.Capabilities{CanPause: false, CanSeek: false},
		MediaInputPolicy:  directHLSInputPolicy(),
		OnStop:            a.makeOnStop(capture),
		Source:            a.Name(),
		Title:             title,
	}
	cancel()
	a.playbackMu.Lock()
	if !a.captureStillActive(capture) {
		a.playbackMu.Unlock()
		return streamhandoff.StartResult{}, playbackError(q.ProviderID, "stream start was superseded")
	}
	if err := coreManager.StartSession(req); err != nil {
		a.playbackMu.Unlock()
		if next, ok := a.recordStartFailureAndAdvance(capture, "failed to start stream playback"); ok {
			a.runBeforeQueueContinuation()
			return a.playCurrentGuarded(ctx, next)
		}
		return streamhandoff.StartResult{}, playbackError(q.ProviderID, "failed to start stream playback")
	}
	a.playbackMu.Unlock()
	now := time.Now()
	a.mu.Lock()
	if capture.matches(a.active) {
		a.active.LastResolvedAt = now
		a.active.cancelResolve = nil
		setActiveItemTitleLocked(a.active, capture.ItemID, title)
	}
	a.mu.Unlock()
	return streamhandoff.StartResult{
		AdapterRef: ref,
		ProviderID: q.ProviderID,
		ChannelID:  q.ChannelID,
		ItemID:     itemIdentity(item),
	}, nil
}
```

Keep the existing yt-dlp resolver branch unchanged for non-direct items.

- [ ] **Step 5: Block direct pause and UI pause advertising**

Modify `Pause` in `internal/adapters/streams/playback.go`:

```go
a.mu.Lock()
q := a.active
ref := activeAdapterRef(q)
coreManager := a.core
if item, ok := q.currentItem(); ok && item.Direct {
	a.mu.Unlock()
	return playbackError(q.ProviderID, "direct live streams do not support pause")
}
a.mu.Unlock()
```

Retain the existing `ref == ""`, `coreManager == nil`, and `PauseIfAdapterRef` handling after this block.

Modify `statusView` in `internal/adapters/streams/ui.go` so pause follows the active item:

```go
if item, ok := q.currentItem(); ok {
	active.ItemID = itemIdentity(item)
	active.ItemTitle = item.Title
	caps.CanPause = !item.Direct
}
```

Then remove or avoid the later unconditional `caps.CanPause = true`.

- [ ] **Step 6: Add status capability test**

Add to `internal/adapters/streams/routes_test.go`:

```go
func TestStatusJSONDirectStreamDoesNotAdvertisePauseOrAdvance(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "toonami-aftermath", ChannelID: "east"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	c.status.AdapterRef = activeAdapterRef(a.active)

	req := httptest.NewRequest(http.MethodGet, "/ui/adapter/streams/status", nil)
	rr := httptest.NewRecorder()
	a.handleStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got StatusView
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got.Capabilities.CanPause || got.Capabilities.CanNext || got.Capabilities.CanPrevious {
		t.Fatalf("capabilities = %+v, want no pause/next/previous", got.Capabilities)
	}
	if !got.Capabilities.CanReplay || !got.Capabilities.CanStop {
		t.Fatalf("capabilities = %+v, want replay and stop", got.Capabilities)
	}
}
```

Add missing imports to `routes_test.go`:

```go
import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
```

- [ ] **Step 7: Run direct playback and capability tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "TestBuildQueueDirectSingleItemUsesLoopNone|TestStartResolvedDirectStreamSkipsResolverAndSetsPolicy|TestReplayDirectStreamRebuildsFromCatalogItem|TestStatusJSONDirectStreamDoesNotAdvertisePauseOrAdvance" -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 4**

```bash
git add internal/adapters/streams/queue.go internal/adapters/streams/playback.go internal/adapters/streams/ui.go internal/adapters/streams/queue_test.go internal/adapters/streams/playback_test.go internal/adapters/streams/routes_test.go
git commit -m "feat(streams): play direct HLS channels"
```

---

## Task 5: UI, README, And Provider List Polish

**Files:**
- Modify: `internal/adapters/streams/ui_test.go`
- Modify: `internal/adapters/streams/routes_test.go`
- Modify: `internal/adapters/streams/test_helpers_test.go`
- Modify: `README.md`

- [ ] **Step 1: Update UI test expectations**

Modify `internal/adapters/streams/ui_test.go` so `TestRenderPanelIncludesProvidersAndControls` expects Toonami:

```go
for _, want := range []string{
	"streams-panel",
	"MTV Rewind",
	"Cartoon Rewind",
	"Toonami Aftermath",
	"East",
	"West",
	"Movies",
	"Radio",
	`hx-post="/ui/adapter/streams/play"`,
	`hx-post="/ui/adapter/streams/refresh"`,
} {
	if !strings.Contains(html, want) {
		t.Fatalf("panel missing %q: %s", want, html)
	}
}
```

If the current test uses `newTestAdapterWithCatalog(t)`, update that helper or the test setup so it includes the bundled Toonami definition and catalog.

- [ ] **Step 2: Run UI test and verify it fails before helper update**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run TestRenderPanelIncludesProvidersAndControls -count=1
```

Expected: FAIL until the test helper includes Toonami or startup loads the direct catalog.

- [ ] **Step 3: Update test helper to include Toonami**

Modify `newTestAdapterWithCatalog` in `internal/adapters/streams/test_helpers_test.go`:

```go
toonamiDef := bundledToonamiAftermathDefinition()
toonamiCat, err := buildDirectStreamsCatalog(toonamiDef)
if err != nil {
	t.Fatalf("buildDirectStreamsCatalog: %v", err)
}
a.replaceDefinitionsForTest([]ProviderDefinition{mtvDef, cartoonDef, toonamiDef})
a.replaceCatalogsForTest([]ProviderCatalog{mtvCat, cartoonCat, toonamiCat})
```

Remove the previous two-provider `replaceDefinitionsForTest` and `replaceCatalogsForTest` calls from the helper.

- [ ] **Step 4: Update route/provider count tests**

Modify `internal/adapters/streams/routes_test.go`.

In `TestStatusJSONIncludesCompanionFields`, change the provider count assertion:

```go
if len(got.Providers) != 3 {
	t.Fatalf("providers = %d, want 3", len(got.Providers))
}
```

In the JSON provider route test near the bottom of the file, change the provider count assertion:

```go
if len(got) != 3 {
	t.Fatalf("providers = %d, want 3", len(got))
}
```

Keep the existing provider-id and query filter assertions unchanged. The `provider_id=mtv-rewind` filter should still return only MTV, and the `q=he-man` filter should still return only Cartoon Rewind.

- [ ] **Step 5: Update README built-in Streams references**

Modify `README.md`:

```markdown
- Built-in catalog of streaming "channels" (Cartoon Rewind, MTV Rewind, Toonami Aftermath)
```

In the Streams adapter section, update the first paragraph:

```markdown
The Streams adapter turns supported catalog sites into native relay queues. Right now Cartoon Rewind, MTV Rewind, and Toonami Aftermath are supported, but more will be coming. I have initial "channel" buttons but the URL adapter now also supports links from these sites.
```

Add an example line:

```markdown
- Toonami Aftermath appears as four direct HLS channels: East, West, Movies, and Radio.
```

- [ ] **Step 6: Run UI and README-related tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -run "TestRenderPanelIncludesProvidersAndControls|TestStatusJSON" -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 5**

```bash
git add internal/adapters/streams/ui_test.go internal/adapters/streams/routes_test.go internal/adapters/streams/test_helpers_test.go README.md
git commit -m "docs(streams): surface toonami aftermath"
```

---

## Task 6: Full Verification And Final Review Prep

**Files:**
- Test-only task; no source edits expected unless verification finds a bug.

- [ ] **Step 1: Run package tests for Streams**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/streams -count=1
```

Expected: PASS.

- [ ] **Step 2: Run adjacent URL handoff tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/adapters/url -count=1
```

Expected: PASS. This guards the URL adapter's existing stream resolver handoff path.

- [ ] **Step 3: Run core policy tests**

Run:

```bash
cmd.exe /c C:\Users\Jake\sdk\go\bin\go.exe test ./internal/core ./internal/ffmpeg -run "MediaInputPolicy|Visualizer|PauseIfAdapterRef" -count=1
```

Expected: PASS. This confirms the policy and pause surfaces used by direct HLS still behave as expected.

- [ ] **Step 4: Run workspace status and diff checks**

Run:

```bash
git status --short
git diff --check HEAD~5..HEAD
```

Expected: status shows only intentional commits plus pre-existing unrelated dirty files; diff check reports no whitespace errors.

- [ ] **Step 5: Request code review**

Use `superpowers:requesting-code-review` on the implementation range:

```bash
BASE_SHA=$(git rev-parse HEAD~5)
HEAD_SHA=$(git rev-parse HEAD)
```

Review focus:
- direct-stream provider is bundled-only;
- remote/cached manifests cannot add, remove, or modify Toonami/direct-stream definitions;
- direct HLS skips yt-dlp and does not leak resolver headers;
- direct HLS uses the expected `MediaInputPolicy`;
- live direct queues do not advertise Pause, Next, or Previous;
- README and UI reflect Toonami Aftermath.

- [ ] **Step 6: Fix review findings or document deferrals**

For any Critical or Important findings, make a fix commit before proceeding. For Minor findings, either fix them immediately or list them in the final handoff with rationale.
