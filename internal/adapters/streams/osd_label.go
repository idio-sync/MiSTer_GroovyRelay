package streams

import (
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// osdChannelLabel is the on-screen-display banner for a streams session.
// A channel that sits in the preset bank gets its slot number ("CH 07"),
// however it was tuned; any other channel shows its own name.
func osdChannelLabel(presets [12]adapters.PresetEntry, providerID, channelID, channelName string) string {
	if providerID != "" {
		for i, p := range presets {
			if p.ProviderID == providerID && p.ChannelID == channelID {
				return fmt.Sprintf("CH %02d", i+1)
			}
		}
	}
	return channelName
}
