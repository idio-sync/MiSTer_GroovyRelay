package streams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestValidateManifestRejectsUnsupportedVersion(t *testing.T) {
	m := Manifest{Version: 2}
	if err := validateManifest(t.Context(), m, DefaultConfig()); err == nil {
		t.Fatal("unsupported version accepted")
	}
}

func TestHostedProviderManifestFileValidates(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "streams", "providers.json"))
	if err != nil {
		t.Fatalf("read hosted manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse hosted manifest: %v", err)
	}
	if err := validateCachedManifest(t.Context(), manifest, DefaultConfig()); err != nil {
		t.Fatalf("validate hosted manifest: %v", err)
	}

	for _, want := range bundledManifest().Providers {
		if want.Type == directStreamsProviderType {
			continue
		}
		got, ok := manifest.Provider(want.ID)
		if !ok {
			t.Fatalf("hosted manifest missing bundled provider %q", want.ID)
		}
		if got.Type != want.Type || got.BaseURL != want.BaseURL || got.PlaylistURL != want.PlaylistURL || got.DefaultChannel != want.DefaultChannel {
			t.Fatalf("hosted provider %q does not match bundled definition: %+v", want.ID, got)
		}
		if !reflect.DeepEqual(got.Groups, want.Groups) {
			t.Fatalf("hosted provider %q groups do not match bundled definition", want.ID)
		}
		if !reflect.DeepEqual(got.Channels, want.Channels) {
			t.Fatalf("hosted provider %q channels do not match bundled definition", want.ID)
		}
	}
}

func TestValidateManifestRejectsReservedAdhocChannel(t *testing.T) {
	useManifestValidationResolver(t, staticResolver{
		"wantmymtv.vercel.app": []string{"93.184.216.34"},
	})
	m := validManifestForTest()
	m.Providers[0].Channels = []ChannelDefinition{{ID: "adhoc", Name: "Bad"}}
	if err := validateManifest(t.Context(), m, DefaultConfig()); err == nil {
		t.Fatal("reserved channel ID accepted")
	}
}

func TestValidateManifestRejectsReservedAdhocProviderID(t *testing.T) {
	// "adhoc" is the synthetic channel ID used for ?v=<id> single-item
	// queues; reserving it as a provider ID too prevents stable-key
	// collisions in the queue model.
	m := validManifestForTest()
	m.Providers[0].ID = "adhoc"
	if err := validateManifest(t.Context(), m, DefaultConfig()); err == nil {
		t.Fatal("reserved provider ID accepted")
	}
}

func TestValidateManifestRejectsPlaylistURLResolvingPrivateIP(t *testing.T) {
	useManifestValidationResolver(t, staticResolver{
		"private.example": []string{"127.0.0.1"},
	})
	m := validManifestForTest()
	m.Providers[0].BaseURL = ""
	m.Providers[0].PlaylistURL = "https://private.example/catalog.json"
	if err := validateManifest(t.Context(), m, DefaultConfig()); err == nil {
		t.Fatal("playlist URL resolving to private IP accepted")
	}
}

func TestValidateManifestUsesCanceledContextForURLResolution(t *testing.T) {
	useManifestValidationResolver(t, blockingResolver{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- validateManifest(ctx, validManifestForTest(), DefaultConfig())
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("validateManifest error = %v, want context.Canceled", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("validateManifest did not return after context cancellation")
	}
}

func TestValidateManifestRejectsTooManyProviders(t *testing.T) {
	useManifestValidationResolver(t, staticResolver{
		"wantmymtv.vercel.app": []string{"93.184.216.34"},
	})
	m := Manifest{Version: 1, Providers: make([]ProviderDefinition, maxManifestProviders+1)}
	for i := range m.Providers {
		provider := validManifestForTest().Providers[0]
		provider.ID = fmt.Sprintf("provider-%d", i)
		m.Providers[i] = provider
	}
	if err := validateManifest(t.Context(), m, DefaultConfig()); err == nil {
		t.Fatal("manifest with too many providers accepted")
	}
}

func TestValidateManifestRejectsTooManyGroups(t *testing.T) {
	useManifestValidationResolver(t, staticResolver{
		"wantmymtv.vercel.app": []string{"93.184.216.34"},
	})
	m := validManifestForTest()
	groups := make([]GroupDefinition, maxManifestGroups+1)
	for i := range groups {
		groups[i] = GroupDefinition{
			ID:   fmt.Sprintf("group-%d", i),
			Name: fmt.Sprintf("Group %d", i),
		}
	}
	m.Providers[0].Groups = groups
	if err := validateManifest(t.Context(), m, DefaultConfig()); err == nil {
		t.Fatal("manifest with too many groups accepted")
	}
}

func TestValidateManifestRejectsTooManyChannels(t *testing.T) {
	m := validManifestForTest()
	channels := make([]ChannelDefinition, 1025)
	for i := range channels {
		channels[i] = ChannelDefinition{
			ID:       fmt.Sprintf("channel-%d", i),
			Name:     fmt.Sprintf("Channel %d", i),
			PlayMode: PlayShuffle,
		}
	}
	m.Providers[0].Channels = channels
	if err := validateManifest(t.Context(), m, DefaultConfig()); err == nil {
		t.Fatal("manifest with too many channels accepted")
	}
}

func TestValidateManifestRejectsTooManyURLRules(t *testing.T) {
	useManifestValidationResolver(t, staticResolver{
		"wantmymtv.vercel.app": []string{"93.184.216.34"},
	})
	m := validManifestForTest()
	rules := make([]URLRule, maxManifestURLRules+1)
	for i := range rules {
		rules[i] = URLRule{
			ID:         fmt.Sprintf("rule-%d", i),
			Schemes:    []string{"https"},
			Hosts:      []string{"wantmymtv.vercel.app"},
			Path:       "/player.html",
			Target:     "channel",
			QueryParam: "channel",
		}
	}
	m.Providers[0].URLRules = rules
	if err := validateManifest(t.Context(), m, DefaultConfig()); err == nil {
		t.Fatal("manifest with too many URL rules accepted")
	}
}

func TestValidateManifestIgnoresUnknownProviderType(t *testing.T) {
	useManifestValidationResolver(t, blockingResolver{})
	m := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID:   "future-provider",
		Type: "future-provider-type",
	}}}
	if err := validateManifest(t.Context(), m, DefaultConfig()); err != nil {
		t.Fatalf("unknown provider type should be ignored during validation: %v", err)
	}
}

func TestValidateManifestStillRejectsKnownProviderMissingPlaylistURL(t *testing.T) {
	m := validManifestForTest()
	m.Providers[0].PlaylistURL = ""
	if err := validateManifest(t.Context(), m, DefaultConfig()); err == nil {
		t.Fatal("known provider missing playlist URL accepted")
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

func TestMergeManifestsRemoteChangingBundledTypeIgnored(t *testing.T) {
	bundled := validManifestForTest()
	remote := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID: "mtv-rewind", Type: "other-known", DisplayName: "Changed",
	}}}
	got := mergeManifests(DefaultConfig(), bundled, nil, &remote, map[string]ProviderFactory{
		"youtube-channel-json": nil,
		"other-known":          nil,
	})
	provider, ok := got.Provider("mtv-rewind")
	if !ok {
		t.Fatal("bundled provider missing after merge")
	}
	if provider.Type != "youtube-channel-json" {
		t.Fatalf("bundled provider type = %q, want youtube-channel-json", provider.Type)
	}
}

func TestMergeManifestsRemoteCanReplaceCachedOnlyProviderType(t *testing.T) {
	bundled := validManifestForTest()
	cached := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID: "remote-only", Type: "youtube-channel-json", DisplayName: "Cached",
	}}}
	remote := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID: "remote-only", Type: "other-known", DisplayName: "Remote",
	}}}
	got := mergeManifests(DefaultConfig(), bundled, &cached, &remote, map[string]ProviderFactory{
		"youtube-channel-json": nil,
		"other-known":          nil,
	})
	provider, ok := got.Provider("remote-only")
	if !ok {
		t.Fatal("remote-only provider missing after merge")
	}
	if provider.Type != "other-known" {
		t.Fatalf("remote-only provider type = %q, want other-known", provider.Type)
	}
}

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

func TestMergeManifestsRejectsRemoteOnlyDirectStreamsProvider(t *testing.T) {
	remote := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID:              "remote-direct",
		Type:            directStreamsProviderType,
		DisplayName:     "Remote Direct",
		DefaultChannel:  "x",
		DefaultPlayMode: PlaySequential,
		Channels: []ChannelDefinition{{
			ID:   "x",
			Name: "X",
			URL:  "http://api.toonamiaftermath.com:3000/est/playlist.m3u8",
		}},
	}}}
	got := mergeManifests(DefaultConfig(), bundledManifest(), nil, &remote, remoteProviderFactories())
	if _, ok := got.Provider("remote-direct"); ok {
		t.Fatal("remote-only direct-streams provider appeared in merged manifest")
	}
}

func TestMergeManifestsRejectsRemoteAndCachedBundledProviderRemoval(t *testing.T) {
	bundled := bundledManifest()
	empty := Manifest{Version: 1}
	for name, got := range map[string]Manifest{
		"remote": mergeManifests(DefaultConfig(), bundled, nil, &empty, remoteProviderFactories()),
		"cached": mergeManifests(DefaultConfig(), bundled, &empty, nil, remoteProviderFactories()),
	} {
		for _, want := range bundled.Providers {
			if _, ok := got.Provider(want.ID); !ok {
				t.Fatalf("%s manifest removed bundled provider %q", name, want.ID)
			}
		}
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

func TestMergeManifestsRejectsRemoteTypeChangeFromDirectStreams(t *testing.T) {
	bundled := bundledManifest()
	remote := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID:          "toonami-aftermath",
		Type:        youtubeChannelJSONProviderType,
		DisplayName: "Toonami As YouTube",
	}}}
	got := mergeManifests(DefaultConfig(), bundled, nil, &remote, remoteProviderFactories())
	provider, ok := got.Provider("toonami-aftermath")
	if !ok {
		t.Fatal("bundled Toonami provider missing")
	}
	if provider.Type != directStreamsProviderType || provider.DisplayName != "Toonami Aftermath" {
		t.Fatalf("remote type change from direct-streams was applied: %+v", provider)
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

func TestMergeManifestsRejectsCachedTypeChangesToOrFromDirectStreams(t *testing.T) {
	bundled := bundledManifest()
	cached := Manifest{Version: 1, Providers: []ProviderDefinition{
		{
			ID:          "mtv-rewind",
			Type:        directStreamsProviderType,
			DisplayName: "MTV As Direct",
		},
		{
			ID:          "toonami-aftermath",
			Type:        youtubeChannelJSONProviderType,
			DisplayName: "Toonami As YouTube",
		},
	}}
	got := mergeManifests(DefaultConfig(), bundled, &cached, nil, remoteProviderFactories())
	mtv, ok := got.Provider("mtv-rewind")
	if !ok {
		t.Fatal("bundled MTV provider missing")
	}
	if mtv.Type != youtubeChannelJSONProviderType {
		t.Fatalf("cached type change to direct-streams was applied: %+v", mtv)
	}
	toonami, ok := got.Provider("toonami-aftermath")
	if !ok {
		t.Fatal("bundled Toonami provider missing")
	}
	if toonami.Type != directStreamsProviderType || toonami.DisplayName != "Toonami Aftermath" {
		t.Fatalf("cached type change from direct-streams was applied: %+v", toonami)
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

func useManifestValidationResolver(t *testing.T, resolver hostResolver) {
	t.Helper()
	prev := manifestValidationResolver
	manifestValidationResolver = resolver
	t.Cleanup(func() {
		manifestValidationResolver = prev
	})
}

type blockingResolver struct{}

func (blockingResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
