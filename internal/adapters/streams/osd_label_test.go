package streams

import (
	"fmt"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
)

func TestOSDChannelLabel(t *testing.T) {
	var presets [12]adapters.PresetEntry
	presets[1] = adapters.PresetEntry{Slot: 2, ProviderID: "mtv-rewind", ChannelID: "80s"}
	presets[10] = adapters.PresetEntry{Slot: 11, ProviderID: "toonami-aftermath", ChannelID: "east"}

	cases := []struct {
		name, provider, channel, channelName, want string
	}{
		{"preset slot gets its number", "mtv-rewind", "80s", "MTV 80s", "CH 02"},
		{"two-digit slot", "toonami-aftermath", "east", "East", "CH 11"},
		{"same channel id, other provider", "cartoon-rewind", "80s", "80s Toons", "80s Toons"},
		{"not in the bank uses channel name", "mtv-rewind", "metal", "Metal", "Metal"},
		{"empty slots never match", "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := osdChannelLabel(presets, tc.provider, tc.channel, tc.channelName); got != tc.want {
				t.Fatalf("osdChannelLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func presetSlotOf(a *Adapter, providerID, channelID string) int {
	for i, p := range a.Presets() {
		if p.ProviderID == providerID && p.ChannelID == channelID {
			return i + 1
		}
	}
	return 0
}

// Resolver path (VOD channels).
func TestStartResolvedStreamSetsOSDChannelLabel(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	if slot := presetSlotOf(a, "mtv-rewind", "metal"); slot != 0 {
		t.Fatalf("test assumes mtv-rewind/metal is not a preset, found slot %d", slot)
	}
	a.resolver = &fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4", Title: "Clip"}}
	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if got := c.lastReq.ChannelLabel; got != "Metal" {
		t.Fatalf("ChannelLabel = %q, want channel name %q", got, "Metal")
	}
}

// Direct live-HLS path (Toonami).
func TestStartResolvedDirectStreamSetsOSDPresetNumber(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	a.bridge.HLSBuffer.Enabled = false
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	slot := presetSlotOf(a, "toonami-aftermath", "east")
	if slot == 0 {
		t.Fatal("test assumes toonami-aftermath/east is in the preset bank")
	}
	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "toonami-aftermath", ChannelID: "east"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if got, want := c.lastReq.ChannelLabel, fmt.Sprintf("CH %02d", slot); got != want {
		t.Fatalf("ChannelLabel = %q, want %q", got, want)
	}
}
