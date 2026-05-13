# Streams Focused Guide Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Streams adapter's provider tables with a wide, TV Guide-inspired focused guide that supports future providers, provider artwork, explicit selection state, and one-click channel playback.

**Architecture:** Keep Streams server-rendered and htmx-driven. Extend provider manifest/status metadata for optional artwork, introduce a small server-side selection model for provider/group focus, render a single selected provider/category slice, and preserve that selection through polling and POST responses.

**Tech Stack:** Go `net/http`, `html/template` escaping helpers, htmx fragments, embedded UI static assets, CSS in `internal/ui/static/app.css`, focused Go tests under `internal/adapters/streams`.

---

## Visual And Interaction Thesis

**Visual thesis:** A restrained CRT-era channel guide: wide, dense, amber-accented, scanline-light, and readable inside the existing GroovyRelay dark system.

**Content plan:** Keep the normal adapter header/status/settings surfaces, then let only `.adapter-extras` widen into the Streams guide. The guide contains the Now Playing/Idle strip, provider tabs, category rail, channel grid, and refresh metadata.

**Interaction thesis:** Provider/category changes are ordinary htmx GETs; playback and transport controls are forms/buttons that carry hidden selection fields; channel cells lift subtly on hover without resizing.

## File Structure

- Modify `internal/adapters/streams/provider.go`: add optional artwork fields to `ProviderDefinition`.
- Modify `internal/adapters/streams/assets.go`: populate bundled MTV Rewind and Cartoon Rewind artwork metadata.
- Modify `docs/streams/providers.json`: publish the same artwork fields in the hosted manifest.
- Modify `internal/adapters/streams/manifest.go`: add one artwork URL validator that reuses the Streams URL/IP validation path.
- Modify `internal/adapters/streams/manifest_test.go`: pin artwork validation and hosted manifest parity.
- Modify `internal/adapters/streams/ui.go`: add artwork fields to `ProviderStatusView`, preserve zero-channel providers, add selection helpers, and render the focused guide.
- Modify `internal/adapters/streams/ui_test.go`: cover focused guide rendering, provider counts, artwork shell, empty providers, escaping, and active state.
- Modify `internal/adapters/streams/routes.go`: parse and preserve `provider_id`/`group_id` for panel, refresh, play, previous, next, replay, stop, and error responses.
- Modify `internal/adapters/streams/routes_test.go`: cover selection round-tripping across polling and POST route responses.
- Create `internal/ui/static/streams-artwork.js`: hide failed provider artwork images so the fallback wordmark is visible.
- Modify `internal/ui/templates/shell.html`: load the artwork script as same-origin static JavaScript.
- Modify `internal/ui/static/app.css`: widen only Streams extras and style the focused guide.

## Task 1: Provider Artwork Metadata And Validation

**Files:**
- Modify: `internal/adapters/streams/provider.go`
- Modify: `internal/adapters/streams/assets.go`
- Modify: `docs/streams/providers.json`
- Modify: `internal/adapters/streams/manifest.go`
- Modify: `internal/adapters/streams/manifest_test.go`

- [ ] **Step 1: Write failing manifest validation tests**

Append these tests to `internal/adapters/streams/manifest_test.go`:

```go
func TestValidateManifestValidatesProviderArtworkURL(t *testing.T) {
	useManifestValidationResolver(t, staticResolver{
		"wantmymtv.vercel.app": []string{"93.184.216.34"},
		"private.example":     []string{"192.168.1.10"},
	})

	cases := []struct {
		name string
		logo string
	}{
		{name: "http", logo: "http://wantmymtv.vercel.app/logo.png"},
		{name: "ip literal", logo: "https://93.184.216.34/logo.png"},
		{name: "loopback", logo: "https://127.0.0.1/logo.png"},
		{name: "private resolved host", logo: "https://private.example/logo.png"},
		{name: "userinfo", logo: "https://user@wantmymtv.vercel.app/logo.png"},
		{name: "raw length", logo: "https://wantmymtv.vercel.app/" + strings.Repeat("a", 2049)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifestForTest()
			m.Providers[0].LogoURL = tc.logo
			cfg := DefaultConfig()
			cfg.AllowLocalManifestURLs = true
			if err := validateManifest(t.Context(), m, cfg); err == nil {
				t.Fatalf("artwork URL %q accepted", tc.logo)
			}
		})
	}

	t.Run("valid public https", func(t *testing.T) {
		m := validManifestForTest()
		m.Providers[0].LogoURL = "https://wantmymtv.vercel.app/public/images/rewindlogo.png"
		if err := validateManifest(t.Context(), m, DefaultConfig()); err != nil {
			t.Fatalf("valid artwork URL rejected: %v", err)
		}
	})
}

func TestHostedProviderManifestIncludesArtworkMetadata(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "streams", "providers.json"))
	if err != nil {
		t.Fatalf("read hosted manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse hosted manifest: %v", err)
	}

	mtv, ok := manifest.Provider("mtv-rewind")
	if !ok {
		t.Fatal("hosted manifest missing mtv-rewind")
	}
	if mtv.LogoURL != "https://wantmymtv.vercel.app/public/images/rewindlogo.png" ||
		mtv.LogoAlt != "MTV Rewind logo" ||
		mtv.FallbackLabel != "MTV REWIND" {
		t.Fatalf("mtv artwork metadata = %+v", mtv)
	}

	cartoon, ok := manifest.Provider("cartoon-rewind")
	if !ok {
		t.Fatal("hosted manifest missing cartoon-rewind")
	}
	if cartoon.LogoURL != "https://cartoonrewind.tv/social.png" ||
		cartoon.LogoAlt != "Cartoon Rewind logo" ||
		cartoon.FallbackLabel != "CARTOON REWIND" {
		t.Fatalf("cartoon artwork metadata = %+v", cartoon)
	}
}
```

Add `strings` to the `manifest_test.go` import list.

- [ ] **Step 2: Run the tests and verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestValidateManifestValidatesProviderArtworkURL|TestHostedProviderManifestIncludesArtworkMetadata"
```

Expected: FAIL because `ProviderDefinition` has no `LogoURL`, `LogoAlt`, or `FallbackLabel` fields.

- [ ] **Step 3: Add provider artwork fields**

In `internal/adapters/streams/provider.go`, extend `ProviderDefinition`:

```go
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
	LogoURL             string              `json:"logo_url,omitempty"`
	LogoAlt             string              `json:"logo_alt,omitempty"`
	FallbackLabel       string              `json:"fallback_label,omitempty"`
	Groups              []GroupDefinition   `json:"groups"`
	Channels            []ChannelDefinition `json:"channels"`
}
```

- [ ] **Step 4: Add artwork validation**

In `internal/adapters/streams/manifest.go`, add a raw length constant:

```go
const (
	reservedAdhocID      = "adhoc"
	maxManifestProviders = 128
	maxManifestGroups    = 256
	maxManifestChannels  = 1024
	maxManifestURLRules  = 128
	maxArtworkURLBytes   = 2048
)
```

Still in `manifest.go`, extend the manifest validation function signatures so remote manifest validation can perform DNS checks while cached/hosted syntax validation stays network-free:

```go
type remoteDataURLValidator func(context.Context, string, Config) error
type artworkURLValidator func(context.Context, string, Config) error

func validateManifest(ctx context.Context, m Manifest, cfg Config) error {
	return validateManifestWithURLValidator(ctx, m, cfg, validateRemoteDataURL, validateProviderArtworkURL)
}

func validateCachedManifest(ctx context.Context, m Manifest, cfg Config) error {
	return validateManifestWithURLValidator(ctx, m, cfg, validateRemoteDataURLSyntax, validateProviderArtworkURLSyntax)
}

func validateManifestWithURLValidator(ctx context.Context, m Manifest, cfg Config, validateURL remoteDataURLValidator, validateArtwork artworkURLValidator) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if m.Version != 1 {
		return fmt.Errorf("unsupported manifest version %d", m.Version)
	}
	if len(m.Providers) > maxManifestProviders {
		return fmt.Errorf("manifest has %d providers, max %d", len(m.Providers), maxManifestProviders)
	}

	providerIDs := map[string]struct{}{}
	for _, provider := range m.Providers {
		if isUnsupportedProviderType(provider.Type) {
			continue
		}
		if err := validateProviderDefinition(ctx, provider, cfg, validateURL, validateArtwork); err != nil {
			return err
		}
		if _, ok := providerIDs[provider.ID]; ok {
			return fmt.Errorf("duplicate provider id %q", provider.ID)
		}
		providerIDs[provider.ID] = struct{}{}
	}
	return nil
}
```

Change `validateProviderDefinition` to accept the artwork validator:

```go
func validateProviderDefinition(ctx context.Context, provider ProviderDefinition, cfg Config, validateURL remoteDataURLValidator, validateArtwork artworkURLValidator) error {
```

Add these helpers near `validateRemoteDataURL`:

```go
func validateProviderArtworkURL(ctx context.Context, raw string, cfg Config) error {
	_ = cfg
	if raw == "" {
		return nil
	}
	if len(raw) > maxArtworkURLBytes {
		return fmt.Errorf("artwork URL exceeds %d bytes", maxArtworkURLBytes)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.User != nil {
		return fmt.Errorf("userinfo is not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("host is required")
	}
	if strings.ToLower(u.Scheme) != "https" {
		return fmt.Errorf("scheme %q is not allowed", strings.ToLower(u.Scheme))
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return fmt.Errorf("IP literal hosts are not allowed")
	}
	if _, err := resolveValidatedIP(ctx, manifestValidationResolver, host, false); err != nil {
		return err
	}
	return nil
}

func validateProviderArtworkURLSyntax(ctx context.Context, raw string, cfg Config) error {
	_ = ctx
	_ = cfg
	if raw == "" {
		return nil
	}
	if len(raw) > maxArtworkURLBytes {
		return fmt.Errorf("artwork URL exceeds %d bytes", maxArtworkURLBytes)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.User != nil {
		return fmt.Errorf("userinfo is not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("host is required")
	}
	if strings.ToLower(u.Scheme) != "https" {
		return fmt.Errorf("scheme %q is not allowed", strings.ToLower(u.Scheme))
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return fmt.Errorf("IP literal hosts are not allowed")
	}
	if _, err := normalizeConfigHost(host); err != nil {
		return err
	}
	return nil
}
```

Then call it from `validateProviderDefinition` after optional `BaseURL` validation:

```go
	if strings.TrimSpace(provider.LogoURL) != "" {
		if err := validateArtwork(ctx, provider.LogoURL, cfg); err != nil {
			return fmt.Errorf("provider %q logo_url: %w", provider.ID, err)
		}
	}
```

- [ ] **Step 5: Populate bundled and hosted artwork metadata**

In `internal/adapters/streams/assets.go`, add these fields to `bundledMTVDefinition()`:

```go
		LogoURL:       "https://wantmymtv.vercel.app/public/images/rewindlogo.png",
		LogoAlt:       "MTV Rewind logo",
		FallbackLabel: "MTV REWIND",
```

Add these fields to `bundledCartoonDefinition()`:

```go
		LogoURL:       "https://cartoonrewind.tv/social.png",
		LogoAlt:       "Cartoon Rewind logo",
		FallbackLabel: "CARTOON REWIND",
```

In `docs/streams/providers.json`, add the matching keys to both provider objects immediately after `catalog_refresh_hours` if present, otherwise after `default_play_mode`:

```json
"logo_url": "https://wantmymtv.vercel.app/public/images/rewindlogo.png",
"logo_alt": "MTV Rewind logo",
"fallback_label": "MTV REWIND",
```

```json
"logo_url": "https://cartoonrewind.tv/social.png",
"logo_alt": "Cartoon Rewind logo",
"fallback_label": "CARTOON REWIND",
```

- [ ] **Step 6: Run tests and verify GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestValidateManifestValidatesProviderArtworkURL|TestHostedProviderManifestIncludesArtworkMetadata|TestHostedProviderManifestFileValidates"
```

Expected: PASS.

- [ ] **Step 7: Commit Task 1**

```bash
git add internal/adapters/streams/provider.go internal/adapters/streams/assets.go internal/adapters/streams/manifest.go internal/adapters/streams/manifest_test.go docs/streams/providers.json
git commit -m "feat(streams): add provider artwork metadata"
```

## Task 2: Status View Artwork And Empty Providers

**Files:**
- Modify: `internal/adapters/streams/ui.go`
- Modify: `internal/adapters/streams/ui_test.go`

- [ ] **Step 1: Write failing status/render tests**

Append these tests to `internal/adapters/streams/ui_test.go`:

```go
func TestStatusViewIncludesArtworkMetadataAndEmptyEnabledProviders(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels: []Channel{{
			ID:       "metal",
			Name:     "Metal",
			PlayMode: PlayShuffle,
			Items:    nil,
		}},
	}, {
		ProviderID: "cartoon-rewind",
		Name:       "Cartoon Rewind",
		Channels: []Channel{{
			ID:       "heman",
			Name:     "He-Man",
			PlayMode: PlayShuffle,
			Items:    []StreamItem{{ID: "9bZkp7q19f0"}},
		}},
	}})

	view := a.statusView()
	if len(view.Providers) != 2 {
		t.Fatalf("providers = %d, want empty enabled provider retained", len(view.Providers))
	}
	if view.Providers[0].ID != "cartoon-rewind" || view.Providers[1].ID != "mtv-rewind" {
		t.Fatalf("provider order = %+v", view.Providers)
	}
	var mtv ProviderStatusView
	for _, provider := range view.Providers {
		if provider.ID == "mtv-rewind" {
			mtv = provider
		}
	}
	if len(mtv.Channels) != 0 {
		t.Fatalf("mtv channels = %d, want 0 post-filter playable channels", len(mtv.Channels))
	}
	if mtv.LogoURL != "https://wantmymtv.vercel.app/public/images/rewindlogo.png" ||
		mtv.LogoAlt != "MTV Rewind logo" ||
		mtv.FallbackLabel != "MTV REWIND" {
		t.Fatalf("mtv artwork = %+v", mtv)
	}
}

func TestExtraPanelHTMLRendersProviderArtworkFallbackWithoutEmptyImage(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.replaceDefinitionsForTest([]ProviderDefinition{{
		ID:              "mtv-rewind",
		Type:            youtubeChannelJSONProviderType,
		DisplayName:     "MTV Rewind",
		BaseURL:         "https://wantmymtv.vercel.app",
		PlaylistURL:     "https://wantmymtv.vercel.app/public/mtv-playlists.json",
		DefaultChannel:  "metal",
		DefaultPlayMode: PlayShuffle,
		FallbackLabel:   `M<TV "REWIND"`,
		URLRules: []URLRule{{
			ID:         "mtv-player-channel",
			Schemes:    []string{"https"},
			Hosts:      []string{"wantmymtv.vercel.app"},
			Path:       "/player.html",
			Target:     "channel",
			QueryParam: "channel",
		}},
	}})
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels:   []Channel{{ID: "metal", Name: "Metal", Items: []StreamItem{{ID: "dQw4w9WgXcQ"}}}},
	}})

	html := string(a.ExtraPanelHTML())
	if strings.Contains(html, `<img class="streams-provider-art"`) || strings.Contains(html, `src=""`) {
		t.Fatalf("empty logo URL should not render img src: %s", html)
	}
	if !strings.Contains(html, `class="streams-provider-art-shell"`) ||
		!strings.Contains(html, `role="img"`) ||
		!strings.Contains(html, `aria-label="MTV Rewind"`) ||
		!strings.Contains(html, `M&lt;TV &#34;REWIND&#34;`) {
		t.Fatalf("fallback artwork shell missing or unescaped: %s", html)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestStatusViewIncludesArtworkMetadataAndEmptyEnabledProviders|TestExtraPanelHTMLRendersProviderArtworkFallbackWithoutEmptyImage"
```

Expected: FAIL because `ProviderStatusView` does not expose artwork fields and `statusView` drops zero-channel providers.

- [ ] **Step 3: Extend `ProviderStatusView` and preserve empty enabled providers**

In `internal/adapters/streams/ui.go`, update the struct:

```go
type ProviderStatusView struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Groups        []ChannelGroupView  `json:"groups,omitempty"`
	Channels      []ChannelStatusView `json:"channels"`
	UpdatedAt     time.Time           `json:"updated_at,omitempty"`
	LogoURL       string              `json:"logo_url,omitempty"`
	LogoAlt       string              `json:"logo_alt,omitempty"`
	FallbackLabel string              `json:"fallback_label,omitempty"`
}
```

In `statusView`, after `p.Name` is resolved, add:

```go
		def := a.definitions[id]
		p.LogoURL = strings.TrimSpace(def.LogoURL)
		p.LogoAlt = strings.TrimSpace(def.LogoAlt)
		p.FallbackLabel = strings.TrimSpace(def.FallbackLabel)
		if p.FallbackLabel == "" {
			p.FallbackLabel = strings.ToUpper(p.Name)
		}
```

Remove this provider-dropping block:

```go
		if len(p.Channels) == 0 {
			continue
		}
```

- [ ] **Step 4: Add the artwork shell renderer**

In `internal/adapters/streams/ui.go`, add this helper near `renderProvidersFromView`:

```go
func renderProviderArtwork(b *strings.Builder, provider ProviderStatusView) {
	label := strings.TrimSpace(provider.LogoAlt)
	if label == "" {
		label = provider.Name
	}
	fallback := strings.TrimSpace(provider.FallbackLabel)
	if fallback == "" {
		fallback = provider.Name
	}
	fmt.Fprintf(b, `<div class="streams-provider-art-shell" role="img" aria-label="%s">`, escAttr(label))
	if strings.TrimSpace(provider.LogoURL) != "" {
		fmt.Fprintf(b,
			`<img class="streams-provider-art" src="%s" alt="" loading="lazy" decoding="async" data-streams-artwork>`,
			escAttr(provider.LogoURL))
	}
	fmt.Fprintf(b, `<span class="streams-provider-wordmark">%s</span></div>`, esc(fallback))
}
```

Temporarily call `renderProviderArtwork(&b, p)` inside `renderProvidersFromView` immediately after each provider `<h4>` so the RED test can go GREEN before the full guide renderer replaces the table layout.

- [ ] **Step 5: Run tests and verify GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestStatusViewIncludesArtworkMetadataAndEmptyEnabledProviders|TestExtraPanelHTMLRendersProviderArtworkFallbackWithoutEmptyImage|TestStatusJSONIncludesCompanionFields"
```

Expected: PASS.

- [ ] **Step 6: Commit Task 2**

```bash
git add internal/adapters/streams/ui.go internal/adapters/streams/ui_test.go
git commit -m "feat(streams): expose provider artwork in status view"
```

## Task 3: Server-Side Selection Contract

**Files:**
- Modify: `internal/adapters/streams/ui.go`
- Modify: `internal/adapters/streams/routes.go`
- Modify: `internal/adapters/streams/ui_test.go`
- Modify: `internal/adapters/streams/routes_test.go`

- [ ] **Step 1: Write failing selection tests**

Append these tests to `internal/adapters/streams/ui_test.go`:

```go
func TestRenderPanelPreservesSelectedProviderAndGroupInPollingURL(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Groups: []ChannelGroup{
			{ID: "shows", Name: "MTV Shows", Order: 10},
			{ID: "genres", Name: "Genres", Order: 20},
		},
		Channels: []Channel{
			{ID: "120minutes", Name: "120 Minutes", GroupID: "shows", Items: []StreamItem{{ID: "AAAAAAAAAAA"}}},
			{ID: "metal", Name: "Headbangers Ball", GroupID: "genres", Items: []StreamItem{{ID: "BBBBBBBBBBB"}}},
		},
	}})

	html := a.renderPanel(panelSelectionRequest{ProviderID: "mtv-rewind", GroupID: "genres", Explicit: true})
	if !strings.Contains(html, `hx-get="/ui/adapter/streams/panel?provider_id=mtv-rewind&amp;group_id=genres"`) {
		t.Fatalf("polling URL did not preserve selection: %s", html)
	}
	if !strings.Contains(html, `name="group_id" value="genres"`) {
		t.Fatalf("forms did not preserve group_id: %s", html)
	}
}

func TestRenderPanelUnknownSelectionFallsBackDeterministically(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	html := a.renderPanel(panelSelectionRequest{ProviderID: "bogus", GroupID: "missing", Explicit: true})
	if !strings.Contains(html, `hx-get="/ui/adapter/streams/panel?provider_id=cartoon-rewind`) {
		t.Fatalf("bogus provider should fall back to first provider by status order: %s", html)
	}
}
```

Append this route test to `internal/adapters/streams/routes_test.go`:

```go
func TestHandlePanelReadsSelectionQuery(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/adapter/streams/panel?provider_id=mtv-rewind&group_id=genres", nil)
	rr := httptest.NewRecorder()
	a.handlePanel(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `hx-get="/ui/adapter/streams/panel?provider_id=mtv-rewind&amp;group_id=`) {
		t.Fatalf("panel did not read provider selection: %s", body)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestRenderPanelPreservesSelectedProviderAndGroupInPollingURL|TestRenderPanelUnknownSelectionFallsBackDeterministically|TestHandlePanelReadsSelectionQuery"
```

Expected: FAIL because `renderPanel` does not accept selection and `handlePanel` ignores query params.

- [ ] **Step 3: Add selection types and helpers**

In `internal/adapters/streams/ui.go`, add these types after `ControlCapabilities`:

```go
type panelSelectionRequest struct {
	ProviderID string
	GroupID    string
	Explicit   bool
}

type resolvedPanelSelection struct {
	ProviderID string
	GroupID    string
}
```

Add helpers near `groupedChannels`:

```go
func selectionFromRequest(r *http.Request) panelSelectionRequest {
	if r == nil {
		return panelSelectionRequest{}
	}
	providerID := strings.TrimSpace(r.FormValue("provider_id"))
	groupID := strings.TrimSpace(r.FormValue("group_id"))
	return panelSelectionRequest{
		ProviderID: providerID,
		GroupID:    groupID,
		Explicit:   providerID != "" || groupID != "",
	}
}

func resolvePanelSelection(view StatusView, req panelSelectionRequest) resolvedPanelSelection {
	if len(view.Providers) == 0 {
		return resolvedPanelSelection{}
	}
	providerIdx := -1
	if req.ProviderID != "" {
		for i, provider := range view.Providers {
			if provider.ID == req.ProviderID {
				providerIdx = i
				break
			}
		}
	}
	if providerIdx < 0 && !req.Explicit && view.Active != nil {
		for i, provider := range view.Providers {
			if provider.ID == view.Active.ProviderID {
				providerIdx = i
				break
			}
		}
	}
	if providerIdx < 0 {
		providerIdx = 0
	}

	provider := view.Providers[providerIdx]
	groups := groupedChannels(provider)
	groupID := ""
	if req.GroupID != "" {
		for _, group := range groups {
			if group.ID == req.GroupID {
				groupID = group.ID
				break
			}
		}
	}
	if groupID == "" && !req.Explicit && view.Active != nil && view.Active.ProviderID == provider.ID {
		for _, channel := range provider.Channels {
			if channel.ID == view.Active.ChannelID {
				groupID = channel.GroupID
				if groupID == "" {
					groupID = ungroupedGroupID
				}
				break
			}
		}
	}
	if groupID == "" && len(groups) > 0 {
		groupID = groups[0].ID
	}
	return resolvedPanelSelection{ProviderID: provider.ID, GroupID: groupID}
}

func panelURL(selection resolvedPanelSelection) string {
	parts := make([]string, 0, 2)
	if selection.ProviderID != "" {
		parts = append(parts, "provider_id="+url.QueryEscape(selection.ProviderID))
	}
	if selection.GroupID != "" {
		parts = append(parts, "group_id="+url.QueryEscape(selection.GroupID))
	}
	if len(parts) != 0 {
		return "/ui/adapter/streams/panel?" + strings.Join(parts, "&")
	}
	return "/ui/adapter/streams/panel"
}
```

Add `net/url` to the `ui.go` imports.

- [ ] **Step 4: Thread selection through panel rendering and responses**

Change these functions in `internal/adapters/streams/ui.go`:

```go
func (a *Adapter) ExtraPanelHTML() template.HTML {
	return template.HTML(a.renderPanel(panelSelectionRequest{}))
}
```

```go
func (a *Adapter) renderPanel(req panelSelectionRequest) string {
	view := a.statusView()
	selection := resolvePanelSelection(view, req)
	var b strings.Builder
	trigger := "every 5s"
	if view.Active != nil {
		trigger = "every 1s"
	}
	fmt.Fprintf(&b, `<section class="streams-panel" id="streams-panel" hx-get="%s" hx-trigger="%s" hx-swap="outerHTML">`,
		escAttr(panelURL(selection)), trigger)
```

Update `respondPanel`:

```go
func (a *Adapter) respondPanel(w http.ResponseWriter, r *http.Request, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(a.renderPanel(selectionFromRequest(r))))
}
```

- [ ] **Step 5: Update route calls**

In `internal/adapters/streams/routes.go`, change `handlePanel`:

```go
func (a *Adapter) handlePanel(w http.ResponseWriter, r *http.Request) {
	a.respondPanel(w, r, http.StatusOK)
}
```

Replace every HTML `a.respondPanel(w, http.StatusOK)` call with:

```go
a.respondPanel(w, r, http.StatusOK)
```

In `respondRouteError`, preserve selection for htmx HTML responses by rendering the panel with the same request and the error status:

```go
func (a *Adapter) respondRouteError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if wantsJSON(r) {
		respondJSON(w, status, routeErrorResponse{Error: msg})
		return
	}
	a.respondPanel(w, r, status)
}
```

- [ ] **Step 6: Run tests and verify GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestRenderPanelPreservesSelectedProviderAndGroupInPollingURL|TestRenderPanelUnknownSelectionFallsBackDeterministically|TestHandlePanelReadsSelectionQuery"
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

```bash
git add internal/adapters/streams/ui.go internal/adapters/streams/routes.go internal/adapters/streams/ui_test.go internal/adapters/streams/routes_test.go
git commit -m "feat(streams): preserve guide selection state"
```

## Task 4: Focused Guide Renderer

**Files:**
- Modify: `internal/adapters/streams/ui.go`
- Modify: `internal/adapters/streams/ui_test.go`

- [ ] **Step 1: Replace old table expectations with focused guide tests**

Update `TestExtraPanelHTMLGroupsChannelsByCategory` in `internal/adapters/streams/ui_test.go` to:

```go
func TestExtraPanelHTMLRendersFocusedGuideForSelectedCategory(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Groups: []ChannelGroup{
			{ID: "shows", Name: "MTV Shows", Order: 10},
			{ID: "decades", Name: "By Decade", Order: 20},
		},
		Channels: []Channel{
			{ID: "120minutes", Name: "120 Minutes", GroupID: "shows", Order: 10, Items: []StreamItem{{ID: "AAAAAAAAAAA"}}},
			{ID: "80s", Name: "80s", GroupID: "decades", Order: 20, Items: []StreamItem{{ID: "BBBBBBBBBBB"}}},
		},
	}})

	html := a.renderPanel(panelSelectionRequest{ProviderID: "mtv-rewind", GroupID: "decades", Explicit: true})
	for _, want := range []string{
		`class="streams-guide"`,
		`class="streams-provider-tabs"`,
		`class="streams-category-rail"`,
		`class="streams-channel-grid"`,
		`aria-current="true"`,
		`By Decade`,
		`80s`,
		`name="provider_id" value="mtv-rewind"`,
		`name="group_id" value="decades"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("panel missing %q: %s", want, html)
		}
	}
	for _, old := range []string{`streams-channel-table`, `<table`, `120 Minutes`} {
		if strings.Contains(html, old) {
			t.Fatalf("focused guide should not render old/unselected content %q: %s", old, html)
		}
	}
}
```

Append:

```go
func TestExtraPanelHTMLRendersDenseMTVCategoryAsGrid(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	channels := make([]Channel, 0, 12)
	for i := 0; i < 12; i++ {
		channels = append(channels, Channel{
			ID:      fmt.Sprintf("label-%02d", i),
			Name:    fmt.Sprintf("Label %02d", i),
			GroupID: "labels",
			Items:   []StreamItem{{ID: fmt.Sprintf("item-%02d", i)}},
		})
	}
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Groups:     []ChannelGroup{{ID: "labels", Name: "Labels & Scenes", Order: 50}},
		Channels:   channels,
	}})

	html := a.renderPanel(panelSelectionRequest{ProviderID: "mtv-rewind", GroupID: "labels", Explicit: true})
	if strings.Count(html, `class="streams-channel-card`) != 12 {
		t.Fatalf("channel card count mismatch: %s", html)
	}
	if strings.Contains(html, `<table`) {
		t.Fatalf("dense category should not render a table: %s", html)
	}
}

func TestExtraPanelHTMLMarksActiveChannelInSelectedGrid(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.active = &ActiveQueue{
		ProviderID:   "mtv-rewind",
		ProviderName: "MTV Rewind",
		ChannelID:    "metal",
		ChannelName:  "Headbangers Ball",
		Items:        []StreamItem{{ID: "dQw4w9WgXcQ"}},
	}

	html := a.renderPanel(panelSelectionRequest{})
	if !strings.Contains(html, `streams-channel-card tuned`) ||
		!strings.Contains(html, `Now Playing`) {
		t.Fatalf("active channel treatment missing: %s", html)
	}
}
```

Add `fmt` to the `ui_test.go` imports.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestExtraPanelHTMLRendersFocusedGuideForSelectedCategory|TestExtraPanelHTMLRendersDenseMTVCategoryAsGrid|TestExtraPanelHTMLMarksActiveChannelInSelectedGrid"
```

Expected: FAIL because the renderer still emits provider sections and tables.

- [ ] **Step 3: Add guide render helpers**

In `internal/adapters/streams/ui.go`, replace `renderProvidersFromView` with these focused helpers:

```go
func renderFocusedGuide(b *strings.Builder, view StatusView, selection resolvedPanelSelection) {
	provider, ok := selectedProvider(view.Providers, selection.ProviderID)
	b.WriteString(`<div class="streams-guide">`)
	renderNowStrip(b, view, provider, selection)
	renderProviderTabs(b, view.Providers, selection)
	if !ok {
		b.WriteString(`<div class="streams-guide-empty">No stream providers are available.</div>`)
		b.WriteString(`</div>`)
		return
	}
	groups := groupedChannels(provider)
	if len(provider.Channels) == 0 {
		b.WriteString(`<div class="streams-provider-empty">No playable channels are available for this provider.</div>`)
		b.WriteString(`</div>`)
		return
	}
	renderGuideBody(b, provider, groups, selection, view.Active)
	b.WriteString(`</div>`)
}

func selectedProvider(providers []ProviderStatusView, providerID string) (ProviderStatusView, bool) {
	for _, provider := range providers {
		if provider.ID == providerID {
			return provider, true
		}
	}
	return ProviderStatusView{}, false
}

func renderProviderTabs(b *strings.Builder, providers []ProviderStatusView, selection resolvedPanelSelection) {
	b.WriteString(`<div class="streams-provider-tabs" role="navigation" aria-label="Stream providers">`)
	for _, provider := range providers {
		active := provider.ID == selection.ProviderID
		attrs := ""
		if active {
			attrs = ` aria-current="true"`
		}
		fmt.Fprintf(b,
			`<button type="button" class="streams-provider-tab%s" hx-get="%s" hx-target="#streams-panel" hx-swap="outerHTML"%s>`+
				`<span>%s</span><span class="streams-provider-count">%d</span></button>`,
			activeClass(active), escAttr(panelURL(resolvedPanelSelection{ProviderID: provider.ID})), attrs,
			esc(provider.Name), len(provider.Channels))
	}
	b.WriteString(`</div>`)
}

func renderGuideBody(b *strings.Builder, provider ProviderStatusView, groups []groupedChannelView, selection resolvedPanelSelection, active *QueueStatusView) {
	b.WriteString(`<div class="streams-guide-body">`)
	b.WriteString(`<div class="streams-category-rail" role="navigation" aria-label="Channel categories">`)
	for _, group := range groups {
		activeGroup := group.ID == selection.GroupID
		attrs := ""
		if activeGroup {
			attrs = ` aria-current="true"`
		}
		fmt.Fprintf(b,
			`<button type="button" class="streams-category-tab%s" hx-get="%s" hx-target="#streams-panel" hx-swap="outerHTML"%s>`+
				`<span>%s</span><span class="streams-category-count">%d</span></button>`,
			activeClass(activeGroup), escAttr(panelURL(resolvedPanelSelection{ProviderID: provider.ID, GroupID: group.ID})), attrs,
			esc(group.Name), len(group.Channels))
	}
	b.WriteString(`</div>`)
	b.WriteString(`<div class="streams-channel-grid">`)
	for _, group := range groups {
		if group.ID != selection.GroupID {
			continue
		}
		for _, ch := range group.Channels {
			renderChannelCard(b, provider, ch, selection, active)
		}
	}
	b.WriteString(`</div></div>`)
}

func activeClass(active bool) string {
	if active {
		return " active"
	}
	return ""
}
```

- [ ] **Step 4: Render Now strip and channel cards**

Add these helpers to `internal/adapters/streams/ui.go`:

```go
func renderNowStrip(b *strings.Builder, view StatusView, provider ProviderStatusView, selection resolvedPanelSelection) {
	b.WriteString(`<div class="streams-now-strip">`)
	renderProviderArtwork(b, provider)
	b.WriteString(`<div class="streams-now-copy">`)
	if view.Active != nil {
		fmt.Fprintf(b, `<div class="streams-kicker">Now Playing</div><div class="streams-now-line">%s / %s</div>`,
			esc(view.Active.ProviderName), esc(view.Active.ChannelName))
		if view.Active.ItemTitle != "" {
			fmt.Fprintf(b, `<div class="streams-now-title">%s</div>`, esc(view.Active.ItemTitle))
		}
		if view.Active.DurationMS > 0 {
			position := time.Duration(view.Active.PositionMS) * time.Millisecond
			duration := time.Duration(view.Active.DurationMS) * time.Millisecond
			fmt.Fprintf(b, `<div class="streams-position">%s / %s</div>`,
				formatDuration(position), formatDuration(duration))
		}
	} else {
		b.WriteString(`<div class="streams-kicker">Idle</div><div class="streams-now-line">Choose a channel to start playback.</div>`)
	}
	b.WriteString(`</div><div class="streams-now-controls">`)
	button(b, "/ui/adapter/streams/refresh", "Refresh", false, selection)
	if view.Active != nil {
		button(b, "/ui/adapter/streams/previous", "Previous", !view.Capabilities.CanPrevious, selection)
		button(b, "/ui/adapter/streams/next", "Next", !view.Capabilities.CanNext, selection)
		button(b, "/ui/adapter/streams/replay", "Replay", !view.Capabilities.CanReplay, selection)
		button(b, "/ui/adapter/streams/stop", "Stop", !view.Capabilities.CanStop, selection)
	}
	b.WriteString(`</div></div>`)
}

func renderChannelCard(b *strings.Builder, provider ProviderStatusView, ch ChannelStatusView, selection resolvedPanelSelection, active *QueueStatusView) {
	tuned := active != nil && active.ProviderID == provider.ID && active.ChannelID == ch.ID
	fmt.Fprintf(b,
		`<form class="streams-channel-card%s" hx-post="/ui/adapter/streams/play" hx-target="#streams-panel" hx-swap="outerHTML">`,
		activeClass(tuned))
	if selection.GroupID != "" {
		fmt.Fprintf(b, `<input type="hidden" name="group_id" value="%s">`, escAttr(selection.GroupID))
	}
	fmt.Fprintf(b,
		`<input type="hidden" name="provider_id" value="%s">`+
			`<input type="hidden" name="channel_id" value="%s">`+
			`<button type="submit"><span class="streams-channel-name">%s</span><span class="streams-channel-meta">%d items</span></button></form>`,
		escAttr(provider.ID), escAttr(ch.ID), esc(ch.Name), ch.ItemCount)
}

func selectionInputs(b *strings.Builder, selection resolvedPanelSelection) {
	if selection.ProviderID != "" {
		fmt.Fprintf(b, `<input type="hidden" name="provider_id" value="%s">`, escAttr(selection.ProviderID))
	}
	if selection.GroupID != "" {
		fmt.Fprintf(b, `<input type="hidden" name="group_id" value="%s">`, escAttr(selection.GroupID))
	}
}
```

Update `button` to a form-posting helper:

```go
func button(b *strings.Builder, path, label string, disabled bool, selection resolvedPanelSelection) {
	dis := ""
	if disabled {
		dis = " disabled"
	}
	fmt.Fprintf(b, `<form class="streams-control-form" hx-post="%s" hx-target="#streams-panel" hx-swap="outerHTML">`, escAttr(path))
	selectionInputs(b, selection)
	fmt.Fprintf(b, `<button type="submit"%s>%s</button></form>`, dis, esc(label))
}
```

In `renderPanel`, remove the old `<h3>Streams</h3>`, `.controls`, old status paragraphs, and `renderProvidersFromView` call. After opening the section, call:

```go
renderFocusedGuide(&b, view, selection)
```

- [ ] **Step 5: Keep providers endpoint compatible**

Because `/ui/adapter/streams/providers` still calls `renderProvidersFromView`, keep this simple compatibility renderer:

```go
func renderProvidersFromView(providers []ProviderStatusView) string {
	var b strings.Builder
	b.WriteString(`<div class="streams-providers">`)
	for _, p := range providers {
		fmt.Fprintf(&b, `<section class="streams-provider"><h4>%s</h4>`, esc(p.Name))
		if len(p.Channels) == 0 {
			b.WriteString(`<p class="muted">No channels</p>`)
		}
		groups := groupedChannels(p)
		for _, group := range groups {
			fmt.Fprintf(&b, `<section class="streams-channel-group"><h5>%s</h5>`, esc(group.Name))
			for _, ch := range group.Channels {
				fmt.Fprintf(&b, `<p class="streams-provider-channel">%s <span class="muted">%d items</span></p>`,
					esc(ch.Name), ch.ItemCount)
			}
			b.WriteString(`</section>`)
		}
		b.WriteString(`</section>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}
```

- [ ] **Step 6: Run tests and verify GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestExtraPanelHTMLContainsStreamsPanel|TestExtraPanelHTMLRendersFocusedGuideForSelectedCategory|TestExtraPanelHTMLRendersDenseMTVCategoryAsGrid|TestExtraPanelHTMLMarksActiveChannelInSelectedGrid|TestExtraPanelHTMLEscapesProviderAndChannelNames|TestHandleProvidersHTMLFiltersByProviderIDAndQuery"
```

Expected: PASS.

- [ ] **Step 7: Commit Task 4**

```bash
git add internal/adapters/streams/ui.go internal/adapters/streams/ui_test.go
git commit -m "feat(streams): render focused guide layout"
```

## Task 5: Route Selection Preservation

**Files:**
- Modify: `internal/adapters/streams/routes_test.go`
- Modify: `internal/adapters/streams/routes.go`
- Modify: `internal/adapters/streams/ui.go`

- [ ] **Step 1: Write failing route preservation tests**

Append this helper and test to `internal/adapters/streams/routes_test.go`:

```go
func assertPanelSelectionPreserved(t *testing.T, body, providerID, groupID string) {
	t.Helper()
	want := `hx-get="/ui/adapter/streams/panel?provider_id=` + providerID + `&amp;group_id=` + groupID + `"`
	if !strings.Contains(body, want) {
		t.Fatalf("selection polling URL missing %q: %s", want, body)
	}
}

func TestHTMLRouteResponsesPreserveGuideSelection(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:    "s1",
		ProviderID:   "mtv-rewind",
		ProviderName: "MTV Rewind",
		ChannelID:    "metal",
		ChannelName:  "Metal",
		ItemToken:    1,
		Items: []StreamItem{
			{ID: "a", URL: "https://youtu.be/a"},
			{ID: "b", URL: "https://youtu.be/b"},
		},
		loopMode: loopSequential,
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)

	cases := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		path    string
		form    string
	}{
		{name: "refresh", handler: a.handleRefresh, path: "/ui/adapter/streams/refresh", form: "provider_id=mtv-rewind&group_id=genres"},
		{name: "play", handler: a.handlePlay, path: "/ui/adapter/streams/play", form: "provider_id=mtv-rewind&group_id=genres&channel_id=metal"},
		{name: "previous", handler: a.handlePrevious, path: "/ui/adapter/streams/previous", form: "provider_id=mtv-rewind&group_id=genres"},
		{name: "next", handler: a.handleNext, path: "/ui/adapter/streams/next", form: "provider_id=mtv-rewind&group_id=genres"},
		{name: "replay", handler: a.handleReplay, path: "/ui/adapter/streams/replay", form: "provider_id=mtv-rewind&group_id=genres"},
		{name: "stop", handler: a.handleStop, path: "/ui/adapter/streams/stop", form: "provider_id=mtv-rewind&group_id=genres"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			tc.handler(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
			}
			assertPanelSelectionPreserved(t, rr.Body.String(), "mtv-rewind", "genres")
		})
	}
}
```

- [ ] **Step 2: Run tests and verify RED or existing GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run TestHTMLRouteResponsesPreserveGuideSelection
```

Expected: PASS if Task 3 and Task 4 already preserved selection everywhere. If this fails, the failure should show which route drops `group_id`.

- [ ] **Step 3: Fix any route that drops selection**

If the test fails, ensure every non-JSON route calls:

```go
a.respondPanel(w, r, http.StatusOK)
```

Ensure every HTML error route calls:

```go
a.respondPanel(w, r, status)
```

No JSON response shape should change.

- [ ] **Step 4: Run tests and verify GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams -run "TestHTMLRouteResponsesPreserveGuideSelection|TestHandlePlayJSONReturnsStartResult|TestHandleNextJSONReturnsStatus|TestHandleRefreshRejectsUnknownProvider"
```

Expected: PASS.

- [ ] **Step 5: Commit Task 5**

```bash
git add internal/adapters/streams/routes.go internal/adapters/streams/routes_test.go internal/adapters/streams/ui.go
git commit -m "test(streams): pin guide selection preservation"
```

## Task 6: Artwork Failure Handler

**Files:**
- Create: `internal/ui/static/streams-artwork.js`
- Modify: `internal/ui/templates/shell.html`
- Modify: `internal/ui/server_test.go`
- Modify: `internal/adapters/streams/ui_test.go`

- [ ] **Step 1: Write failing static asset and shell tests**

Append to `internal/ui/server_test.go`:

```go
func TestShellLoadsStreamsArtworkScript(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest("GET", "/ui/", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	body := rw.Body.String()
	if !strings.Contains(body, `<script src="/ui/static/streams-artwork.js" defer></script>`) {
		t.Fatalf("shell missing streams artwork script: %s", body)
	}
}

func TestStaticStreamsArtworkScriptServed(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/static/streams-artwork.js", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "data-streams-artwork") ||
		!strings.Contains(rr.Body.String(), "streams-artwork-failed") {
		t.Fatalf("unexpected artwork script body: %s", rr.Body.String())
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -run "TestShellLoadsStreamsArtworkScript|TestStaticStreamsArtworkScriptServed"
```

Expected: FAIL because the script file and shell tag do not exist.

- [ ] **Step 3: Create the artwork script**

Create `internal/ui/static/streams-artwork.js`:

```javascript
(function () {
  function markFailed(img) {
    if (!img || img.dataset.streamsArtworkHandled === "1") {
      return;
    }
    img.dataset.streamsArtworkHandled = "1";
    img.classList.add("streams-artwork-failed");
    img.setAttribute("aria-hidden", "true");
  }

  function bind(root) {
    var scope = root || document;
    var images = scope.querySelectorAll("img[data-streams-artwork]");
    images.forEach(function (img) {
      if (img.complete && img.naturalWidth === 0) {
        markFailed(img);
        return;
      }
      img.addEventListener("error", function () {
        markFailed(img);
      }, { once: true });
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    bind(document);
  });
  document.body.addEventListener("htmx:afterSwap", function (event) {
    bind(event.target);
  });
})();
```

- [ ] **Step 4: Load the script**

In `internal/ui/templates/shell.html`, add the script after `clipboard.js`:

```html
	<script src="/ui/static/streams-artwork.js" defer></script>
```

- [ ] **Step 5: Run tests and verify GREEN**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -run "TestShellLoadsStreamsArtworkScript|TestStaticStreamsArtworkScriptServed"
```

Expected: PASS.

- [ ] **Step 6: Commit Task 6**

```bash
git add internal/ui/static/streams-artwork.js internal/ui/templates/shell.html internal/ui/server_test.go
git commit -m "feat(ui): add streams artwork fallback handler"
```

## Task 7: Focused Guide CSS And Width Scoping

**Files:**
- Modify: `internal/ui/static/app.css`

- [ ] **Step 1: Add width-scoped adapter extras rules**

Add this CSS near the existing `.adapter-extras` rules:

```css
#panel:has(.streams-panel) {
	max-width: min(1440px, 100%);
}
#panel:has(.streams-panel) .gr-config-head,
#panel:has(.streams-panel) > .section,
#panel:has(.streams-panel) form {
	max-width: 860px;
}
#panel:has(.streams-panel) .adapter-extras {
	width: min(100%, 1320px);
	max-width: none;
}
```

- [ ] **Step 2: Add focused guide CSS**

Append this CSS before the existing delight/reduced-motion section:

```css
.streams-panel {
	width: 100%;
}
.streams-guide {
	position: relative;
	overflow: hidden;
	border: 1px solid var(--gr-border);
	border-radius: 6px;
	background:
		repeating-linear-gradient(180deg, transparent 0, transparent 3px, oklch(0.04 0 0 / 0.18) 3px, oklch(0.04 0 0 / 0.18) 4px),
		oklch(0.19 0.010 80);
}
.streams-now-strip {
	display: grid;
	grid-template-columns: minmax(150px, 220px) minmax(0, 1fr) auto;
	gap: 18px;
	align-items: center;
	padding: 18px;
	border-bottom: 1px solid var(--gr-border);
}
.streams-provider-art-shell {
	min-height: 94px;
	display: grid;
	place-items: center;
	border: 1px solid oklch(0.38 0.015 80);
	border-radius: 4px;
	background: oklch(0.15 0.008 80);
	overflow: hidden;
}
.streams-provider-art {
	max-width: 88%;
	max-height: 74px;
	object-fit: contain;
	grid-area: 1 / 1;
}
.streams-provider-art.streams-artwork-failed {
	display: none;
}
.streams-provider-wordmark {
	grid-area: 1 / 1;
	padding: 12px;
	text-align: center;
	font: 600 17px/1.15 var(--font-display);
	letter-spacing: 0.08em;
	color: var(--gr-amber);
}
.streams-kicker {
	font: 400 11px var(--font-mono);
	text-transform: uppercase;
	letter-spacing: 0.10em;
	color: var(--gr-dim);
}
.streams-now-line {
	margin-top: 4px;
	font: 600 20px/1.2 var(--font-display);
	color: var(--gr-text);
}
.streams-now-title,
.streams-position {
	margin-top: 4px;
	font: 400 13px var(--font-mono);
	color: var(--gr-dim);
}
.streams-now-controls {
	display: flex;
	flex-wrap: wrap;
	justify-content: flex-end;
	gap: 8px;
}
.streams-control-form {
	margin: 0;
}
.streams-control-form button,
.streams-provider-tab,
.streams-category-tab,
.streams-channel-card button {
	font: 500 13px var(--font-body);
	border: 1px solid var(--gr-border);
	background: transparent;
	color: var(--gr-text);
	cursor: pointer;
}
.streams-control-form button {
	min-height: 34px;
	padding: 7px 12px;
	border-color: oklch(0.46 0.035 80);
	color: var(--gr-amber);
	border-radius: 3px;
}
.streams-control-form button:hover:not(:disabled) {
	background: var(--gr-amber);
	color: var(--gr-bg);
}
.streams-control-form button:disabled {
	cursor: not-allowed;
	opacity: 0.45;
}
.streams-provider-tabs {
	display: flex;
	gap: 8px;
	overflow-x: auto;
	padding: 10px 12px;
	border-bottom: 1px solid var(--gr-border);
}
.streams-provider-tab {
	flex: 0 0 auto;
	display: inline-flex;
	align-items: center;
	gap: 10px;
	min-height: 36px;
	padding: 8px 12px;
	border-radius: 3px;
}
.streams-provider-tab.active,
.streams-provider-tab:hover {
	border-color: var(--gr-amber);
	color: var(--gr-amber);
	background: oklch(0.25 0.018 80);
}
.streams-provider-count,
.streams-category-count {
	font: 400 11px var(--font-mono);
	color: var(--gr-dim);
}
.streams-guide-body {
	display: grid;
	grid-template-columns: minmax(160px, 180px) minmax(0, 1fr);
	min-height: 360px;
}
.streams-category-rail {
	display: flex;
	flex-direction: column;
	gap: 6px;
	padding: 12px;
	border-right: 1px solid var(--gr-border);
	background: oklch(0.17 0.008 80);
}
.streams-category-tab {
	display: flex;
	justify-content: space-between;
	align-items: center;
	gap: 10px;
	min-height: 36px;
	padding: 8px 10px;
	text-align: left;
	border-radius: 3px;
}
.streams-category-tab.active,
.streams-category-tab:hover {
	border-color: var(--gr-amber);
	color: var(--gr-amber);
	background: oklch(0.24 0.015 80);
}
.streams-channel-grid {
	display: grid;
	grid-template-columns: repeat(4, minmax(0, 1fr));
	gap: 10px;
	align-content: start;
	padding: 14px;
}
.streams-channel-card {
	margin: 0;
	min-height: 86px;
}
.streams-channel-card button {
	width: 100%;
	min-height: 86px;
	padding: 12px;
	display: flex;
	flex-direction: column;
	justify-content: space-between;
	align-items: flex-start;
	gap: 8px;
	border-radius: 4px;
	text-align: left;
	transition: border-color 160ms ease, transform 160ms ease, background 160ms ease;
}
.streams-channel-card button:hover,
.streams-channel-card.tuned button {
	border-color: var(--gr-amber);
	background: oklch(0.24 0.016 80);
	transform: translateY(-1px);
}
.streams-channel-card.tuned button {
	box-shadow: inset 3px 0 0 var(--gr-amber);
}
.streams-channel-name {
	max-width: 100%;
	overflow: hidden;
	display: -webkit-box;
	-webkit-box-orient: vertical;
	-webkit-line-clamp: 2;
	line-clamp: 2;
}
.streams-channel-meta {
	font: 400 12px var(--font-mono);
	color: var(--gr-dim);
}
.streams-guide-empty,
.streams-provider-empty {
	padding: 28px;
	color: var(--gr-dim);
	font: 400 14px var(--font-body);
}
@media (min-width: 1280px) {
	.streams-channel-grid {
		grid-template-columns: repeat(5, minmax(0, 1fr));
	}
}
@media (max-width: 760px) {
	.streams-now-strip {
		grid-template-columns: 1fr;
	}
	.streams-now-controls {
		justify-content: flex-start;
	}
	.streams-guide-body {
		grid-template-columns: 1fr;
	}
	.streams-category-rail {
		border-right: 0;
		border-bottom: 1px solid var(--gr-border);
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(132px, 1fr));
	}
	.streams-channel-grid {
		grid-template-columns: 1fr;
	}
}
```

- [ ] **Step 3: Run CSS smoke checks**

Run:

```bash
grep -n "streams-channel-grid" internal/ui/static/app.css
grep -n "object-src" internal/ui/static/app.css
```

Expected: first command prints the grid selectors; second command exits 1 with no output because the CSS should not mention `object-src`.

- [ ] **Step 4: Commit Task 7**

```bash
git add internal/ui/static/app.css
git commit -m "style(streams): add focused guide layout"
```

## Task 8: Full Verification And Manual Preview

**Files:**
- Verify all modified files from Tasks 1-7.

- [ ] **Step 1: Format Go files**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/gofmt.exe -w internal/adapters/streams/provider.go internal/adapters/streams/assets.go internal/adapters/streams/manifest.go internal/adapters/streams/manifest_test.go internal/adapters/streams/ui.go internal/adapters/streams/ui_test.go internal/adapters/streams/routes.go internal/adapters/streams/routes_test.go internal/ui/server_test.go
```

Expected: command exits 0.

- [ ] **Step 2: Run focused tests**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams ./internal/ui
```

Expected: PASS.

- [ ] **Step 3: Run broader Go tests likely affected by embedded assets and templates**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/adapters/streams ./internal/ui ./internal/uiserver
```

Expected: PASS.

- [ ] **Step 4: Run a build check**

Run:

```bash
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./cmd/mister-groovy-relay
```

Expected: PASS.

- [ ] **Step 5: Manual browser verification**

Start or use the existing app instance, then open:

```text
http://192.168.50.138:32500/ui/adapter/streams
```

Verify:

- The header, status section, and settings form keep the normal readable width.
- The Streams guide fills the wider main column.
- Provider tabs scroll horizontally if there are more providers than fit.
- MTV Rewind `Labels & Scenes` renders as stable channel cards, not a table.
- Cartoon Rewind decade groups render with the selected group only.
- Channel buttons post to playback.
- Refresh, previous, next, replay, and stop preserve the selected provider/group after the htmx swap.
- Blocking `wantmymtv.vercel.app` in Chromium and Firefox leaves the wordmark visible.
- No text overlaps at desktop width and narrow/mobile width.

- [ ] **Step 6: Commit any verification-only fixes**

If formatting or visual verification required a small correction, commit only those files:

```bash
git add internal/adapters/streams internal/ui/static internal/ui/templates docs/streams/providers.json
git commit -m "fix(streams): polish focused guide verification"
```

If no correction was needed, do not create an empty commit.

## Self-Review Checklist

- [ ] Artwork metadata is in bundled providers, hosted manifest, manifest validation, and `ProviderStatusView`.
- [ ] Artwork URL validation reuses Streams validation primitives and ignores `AllowLocalManifestURLs`.
- [ ] Empty enabled providers remain visible with `Provider Name (0)` and provider-level empty state.
- [ ] `handlePanel` reads selection from query params.
- [ ] Root polling `hx-get` includes resolved `provider_id` and `group_id`.
- [ ] Provider tabs and category controls use htmx GET and `aria-current="true"` when selected.
- [ ] Play, refresh, previous, next, replay, and stop preserve selection via hidden fields.
- [ ] The focused guide renders one provider/category slice, not all providers.
- [ ] Dense MTV categories render as CSS grid channel cards, not tables.
- [ ] The guide width expansion is scoped to Streams `.adapter-extras`.
- [ ] The artwork fallback uses `<img>` plus same-origin JS, not `<object>` and not inline `onerror`.
- [ ] CSS uses existing GroovyRelay tokens and does not introduce provider-specific palettes.
- [ ] Full verification commands from Task 8 have been run before reporting completion.
