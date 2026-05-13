package streams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestSanitizeManifestArtworkDropsInvalidOptionalLogoURLs(t *testing.T) {
	useManifestValidationResolver(t, staticResolver{
		"wantmymtv.vercel.app": []string{"93.184.216.34"},
		"private.example":      []string{"192.168.1.10"},
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
			m.Providers[0].LogoAlt = "MTV Rewind logo"
			m.Providers[0].FallbackLabel = "MTV REWIND"
			cfg := DefaultConfig()
			cfg.AllowLocalManifestURLs = true
			if err := validateManifest(t.Context(), m, cfg); err != nil {
				t.Fatalf("invalid optional artwork should not reject manifest: %v", err)
			}
			got := sanitizeManifestArtwork(t.Context(), m, cfg, validateProviderArtworkURL)
			if got.Providers[0].LogoURL != "" {
				t.Fatalf("invalid artwork URL %q survived as %q", tc.logo, got.Providers[0].LogoURL)
			}
			if got.Providers[0].FallbackLabel != "MTV REWIND" {
				t.Fatalf("fallback label was not preserved: %+v", got.Providers[0])
			}
		})
	}

	t.Run("valid public https", func(t *testing.T) {
		m := validManifestForTest()
		m.Providers[0].LogoURL = "https://wantmymtv.vercel.app/public/images/rewindlogo.png"
		if err := validateManifest(t.Context(), m, DefaultConfig()); err != nil {
			t.Fatalf("valid artwork URL rejected: %v", err)
		}
		got := sanitizeManifestArtwork(t.Context(), m, DefaultConfig(), validateProviderArtworkURL)
		if got.Providers[0].LogoURL != m.Providers[0].LogoURL {
			t.Fatalf("valid artwork URL was scrubbed: %+v", got.Providers[0])
		}
	})
}

func TestProviderArtworkURLValidatorsAllowEmptyOptionalURL(t *testing.T) {
	for _, tc := range []struct {
		name     string
		validate artworkURLValidator
	}{
		{name: "syntax", validate: validateProviderArtworkURLSyntax},
		{name: "remote", validate: validateProviderArtworkURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.validate(t.Context(), "", DefaultConfig()); err != nil {
				t.Fatalf("empty optional artwork URL rejected: %v", err)
			}
		})
	}
}

func TestSanitizeManifestArtworkTrimsMetadataAndKeepsValidLogoURL(t *testing.T) {
	useManifestValidationResolver(t, staticResolver{
		"wantmymtv.vercel.app": []string{"93.184.216.34"},
	})
	m := validManifestForTest()
	m.Providers[0].LogoURL = "  https://wantmymtv.vercel.app/public/images/rewindlogo.png  "
	m.Providers[0].LogoAlt = "  MTV Rewind logo  "
	m.Providers[0].FallbackLabel = "  MTV REWIND  "

	got := sanitizeManifestArtwork(t.Context(), m, DefaultConfig(), validateProviderArtworkURL)
	if got.Providers[0].LogoURL != "https://wantmymtv.vercel.app/public/images/rewindlogo.png" {
		t.Fatalf("logo URL was not trimmed and preserved: %+v", got.Providers[0])
	}
	if got.Providers[0].LogoAlt != "MTV Rewind logo" {
		t.Fatalf("logo alt was not trimmed: %+v", got.Providers[0])
	}
	if got.Providers[0].FallbackLabel != "MTV REWIND" {
		t.Fatalf("fallback label was not trimmed: %+v", got.Providers[0])
	}
}

func TestSanitizeManifestArtworkDoesNotMutateSourceManifest(t *testing.T) {
	m := validManifestForTest()
	m.Providers[0].LogoURL = "http://wantmymtv.vercel.app/logo.png"
	m.Providers[0].LogoAlt = "  MTV Rewind logo  "
	m.Providers[0].FallbackLabel = "  MTV REWIND  "

	got := sanitizeManifestArtwork(t.Context(), m, DefaultConfig(), validateProviderArtworkURLSyntax)
	if got.Providers[0].LogoURL != "" {
		t.Fatalf("invalid artwork URL survived sanitize: %+v", got.Providers[0])
	}
	if m.Providers[0].LogoURL != "http://wantmymtv.vercel.app/logo.png" {
		t.Fatalf("source manifest was mutated: %+v", m.Providers[0])
	}
	if m.Providers[0].LogoAlt != "  MTV Rewind logo  " {
		t.Fatalf("source logo alt was mutated: %+v", m.Providers[0])
	}
	if m.Providers[0].FallbackLabel != "  MTV REWIND  " {
		t.Fatalf("source fallback label was mutated: %+v", m.Providers[0])
	}
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
