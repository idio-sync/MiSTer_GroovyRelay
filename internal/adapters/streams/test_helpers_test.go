package streams

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

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

func (a *Adapter) replaceDefinitionsForTest(defs []ProviderDefinition) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definitions = map[string]ProviderDefinition{}
	a.definitionOrder = a.definitionOrder[:0]
	for _, def := range defs {
		a.definitions[def.ID] = def
		a.definitionOrder = append(a.definitionOrder, def.ID)
	}
}

func (a *Adapter) replaceCatalogsForTest(cats []ProviderCatalog) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.catalogs = map[string]ProviderCatalog{}
	for _, cat := range cats {
		a.catalogs[cat.ProviderID] = cat
	}
}
