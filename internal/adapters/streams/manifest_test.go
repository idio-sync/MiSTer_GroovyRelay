package streams

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestValidateManifestRejectsUnsupportedVersion(t *testing.T) {
	m := Manifest{Version: 2}
	if err := validateManifest(t.Context(), m, DefaultConfig()); err == nil {
		t.Fatal("unsupported version accepted")
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
