package chassis

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestBuildCatalogProviderEnvelope_Shape(t *testing.T) {
	t.Parallel()
	p := adapters.CatalogProvider{
		ID: "user:mix", DisplayName: "Mix", BadgeLabel: "MX", BadgeClass: "u-teal",
		Groups: []adapters.CatalogGroup{{ID: "g1", Name: "Group", Channels: []adapters.CatalogChannel{
			{ID: "a", Name: "A", PlayMode: "SHUFFLE", Live: false},
		}}},
	}
	env := buildCatalogProviderEnvelope(p)
	if env.ID != "user:mix" || env.BadgeClass != "u-teal" {
		t.Fatalf("envelope identity = %+v", env)
	}
	if len(env.Groups) != 1 || len(env.Groups[0].Channels) != 1 || env.Groups[0].Channels[0].ID != "a" {
		t.Fatalf("envelope groups = %+v", env.Groups)
	}
}

func TestProviderStatusEnvelope_AutoEnabledField(t *testing.T) {
	t.Parallel()
	env := providerStatusEnvelope{Provider: "user:mix", AutoEnabledStreams: "on"}
	if env.AutoEnabledStreams != "on" {
		t.Fatal("autoEnabledStreams not carried")
	}
}
