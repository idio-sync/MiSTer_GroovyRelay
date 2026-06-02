package streams

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestCatalog_ReturnsThreeBundledProviders(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	cat := a.Catalog()
	if len(cat) != 3 {
		t.Fatalf("len(Catalog) = %d, want 3", len(cat))
	}
	gotIDs := []string{cat[0].ID, cat[1].ID, cat[2].ID}
	wantIDs := []string{"mtv-rewind", "cartoon-rewind", "toonami-aftermath"}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Errorf("Catalog[%d].ID = %q, want %q", i, gotIDs[i], wantIDs[i])
		}
	}
}

func TestCatalog_BadgesMatchTable(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	for _, p := range a.Catalog() {
		want, ok := providerBadges[p.ID]
		if !ok {
			t.Errorf("provider %q missing from providerBadges", p.ID)
			continue
		}
		if p.BadgeLabel != want.Label || p.BadgeClass != want.Class {
			t.Errorf("Catalog[%q] badge = (%q, %q), want (%q, %q)",
				p.ID, p.BadgeLabel, p.BadgeClass, want.Label, want.Class)
		}
	}
}

func TestCatalog_ToonamiIsLiveAndChannelsLive(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	for _, p := range a.Catalog() {
		if p.ID != "toonami-aftermath" {
			continue
		}
		if !p.Live {
			t.Errorf("toonami-aftermath provider.Live = false, want true")
		}
		for _, g := range p.Groups {
			for _, c := range g.Channels {
				if !c.Live {
					t.Errorf("toonami-aftermath channel %q Live = false, want true (inherits provider.Live)", c.ID)
				}
			}
		}
	}
}

func TestCatalog_MTVNotLive(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	for _, p := range a.Catalog() {
		if p.ID != "mtv-rewind" {
			continue
		}
		if p.Live {
			t.Errorf("mtv-rewind provider.Live = true, want false (youtube-channel-json)")
		}
		for _, g := range p.Groups {
			for _, c := range g.Channels {
				if c.Live {
					t.Errorf("mtv-rewind channel %q Live = true, want false", c.ID)
				}
			}
		}
	}
}

func TestCatalog_PlayModeUppercased(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	for _, p := range a.Catalog() {
		for _, g := range p.Groups {
			for _, c := range g.Channels {
				if c.PlayMode == "" {
					continue
				}
				if c.PlayMode != strings.ToUpper(c.PlayMode) {
					t.Errorf("channel %q PlayMode = %q, want uppercased", c.ID, c.PlayMode)
				}
			}
		}
	}
}

func TestCatalog_BundledProviderListIntegrity(t *testing.T) {
	t.Parallel()
	if len(bundledChassisCatalogProviderIDs) != 3 {
		t.Fatalf("bundledChassisCatalogProviderIDs len = %d, want 3", len(bundledChassisCatalogProviderIDs))
	}
	manifest := bundledManifest()
	providers := map[string]bool{}
	for _, p := range manifest.Providers {
		providers[p.ID] = true
	}
	for _, id := range bundledChassisCatalogProviderIDs {
		if !providers[id] {
			t.Errorf("bundledChassisCatalogProviderIDs entry %q not in bundled manifest", id)
		}
		if _, ok := providerBadges[id]; !ok {
			t.Errorf("bundledChassisCatalogProviderIDs entry %q missing from providerBadges", id)
		}
	}
}

func TestCatalog_BeforeStartReturnsBundled(t *testing.T) {
	t.Parallel()
	// Adapter constructed with New but Start NOT called: Catalog must
	// still return the bundled providers (local-only bootstrap, no network).
	a, _ := newTestAdapter(t)
	cat := a.Catalog()
	if len(cat) != 3 {
		t.Fatalf("pre-Start Catalog len = %d, want 3", len(cat))
	}
}

func TestCastChannel_DisabledReturnsNotReady(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	a.SetEnabled(false)
	err := a.CastChannel(context.Background(), "mtv-rewind", "80s")
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusServiceUnavailable || qerr.Chip != "NOT READY" {
		t.Errorf("qerr = %+v, want Status=503 Chip=NOT READY", qerr)
	}
}

func TestCastChannel_UnknownReturnsNotFound(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	a.SetEnabled(true)
	err := a.CastChannel(context.Background(), "nonexistent", "x")
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusNotFound || qerr.Chip != "NOT FOUND" {
		t.Errorf("qerr = %+v, want Status=404 Chip=NOT FOUND", qerr)
	}
}

func TestCastChannel_ValidForwardsToFakeCore(t *testing.T) {
	t.Parallel()
	// newTestAdapterWithFakeCore exists from 3A's preset_test.go suite.
	a, core := newTestAdapterWithFakeCore(t)
	if err := a.CastChannel(context.Background(), "cartoon-rewind", "heman"); err != nil {
		t.Fatalf("CastChannel err = %v", err)
	}
	if core.lastReq.Source != "streams" {
		t.Errorf("lastReq.Source = %q, want streams", core.lastReq.Source)
	}
	if !strings.HasPrefix(core.lastReq.AdapterRef, "streams:cartoon-rewind:heman:") {
		t.Errorf("lastReq.AdapterRef = %q, want prefix streams:cartoon-rewind:heman:", core.lastReq.AdapterRef)
	}
}

func userExposureTestDef() ProviderDefinition {
	return ProviderDefinition{
		ID:          "user:mix",
		Type:        userProviderType,
		DisplayName: "Mix",
		BadgeLabel:  "MX",
		BadgeColor:  "teal",
		// no groups → channels are ungrouped (flat)
		Channels: []ChannelDefinition{
			{ID: "live", Name: "Live", Kind: kindDirect, URL: "https://cdn.example.com/live.m3u8"},
			{ID: "vid", Name: "Single", Kind: kindSingle, URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		},
	}
}

func TestBuildChassisCatalogProvider_UserBadgeLiveAndUngrouped(t *testing.T) {
	t.Parallel()
	p := buildChassisCatalogProvider(userExposureTestDef())
	if p.BadgeLabel != "MX" {
		t.Errorf("BadgeLabel = %q, want MX", p.BadgeLabel)
	}
	if p.BadgeClass != "u-teal" {
		t.Errorf("BadgeClass = %q, want u-teal", p.BadgeClass)
	}
	if p.Live {
		t.Errorf("user provider.Live = true, want false (only direct-streams providers are provider-level live)")
	}
	// ungrouped channels must still appear (synthetic group with empty ID).
	var live, vid *adapters.CatalogChannel
	for gi := range p.Groups {
		for ci := range p.Groups[gi].Channels {
			c := &p.Groups[gi].Channels[ci]
			switch c.ID {
			case "live":
				live = c
			case "vid":
				vid = c
			}
		}
	}
	if live == nil || vid == nil {
		t.Fatalf("ungrouped channels missing: live=%v vid=%v", live, vid)
	}
	if !live.Live {
		t.Errorf("direct channel Live = false, want true")
	}
	if vid.Live {
		t.Errorf("single channel Live = true, want false")
	}
}

func TestCatalog_EmitsBundledThenUser(t *testing.T) {
	t.Parallel()
	a := newTestAdapterWithCatalog(t)
	// Install bundled defs + a user def (replaceDefinitionsForTest preserves order).
	a.replaceDefinitionsForTest([]ProviderDefinition{
		bundledMTVDefinition(),
		bundledCartoonDefinition(),
		bundledToonamiAftermathDefinition(),
		userExposureTestDef(),
	})
	cat := a.Catalog()
	if len(cat) != 4 {
		t.Fatalf("len(Catalog) = %d, want 4 (3 bundled + 1 user)", len(cat))
	}
	wantIDs := []string{"mtv-rewind", "cartoon-rewind", "toonami-aftermath", "user:mix"}
	for i, want := range wantIDs {
		if cat[i].ID != want {
			t.Errorf("Catalog[%d].ID = %q, want %q", i, cat[i].ID, want)
		}
	}
}

func TestCatalog_OmitsBundledProviderAbsentFromDefinitions(t *testing.T) {
	t.Parallel()
	// mergeManifests filters disabled bundled providers out of the installed
	// definitions (catalog.go doc comment), so a disabled bundled provider is
	// simply absent from definitions. Simulate that by installing only two of
	// the three bundled defs plus a user def: Catalog must skip the missing
	// bundled entry and still emit the rest in order.
	a := newTestAdapterWithCatalog(t)
	a.replaceDefinitionsForTest([]ProviderDefinition{
		bundledMTVDefinition(),
		bundledCartoonDefinition(),
		// toonami-aftermath intentionally omitted (stand-in for disabled→filtered)
		userExposureTestDef(),
	})
	cat := a.Catalog()
	wantIDs := []string{"mtv-rewind", "cartoon-rewind", "user:mix"}
	if len(cat) != len(wantIDs) {
		t.Fatalf("len(Catalog) = %d, want %d", len(cat), len(wantIDs))
	}
	for i, want := range wantIDs {
		if cat[i].ID != want {
			t.Errorf("Catalog[%d].ID = %q, want %q", i, cat[i].ID, want)
		}
	}
}
