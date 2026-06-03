package chassis

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// --- SSE envelope shapes (Phase 5 defines the shape; Phase 6 wires emission
// into handleEvents and renders the chips client-side). ---

// catalogProviderEnvelope is one provider in a `catalog` SSE event.
type catalogProviderEnvelope struct {
	ID          string                 `json:"id"`
	DisplayName string                 `json:"displayName"`
	BadgeLabel  string                 `json:"badgeLabel"`
	BadgeClass  string                 `json:"badgeClass"`
	Live        bool                   `json:"live"`
	Groups      []catalogGroupEnvelope `json:"groups"`
}

type catalogGroupEnvelope struct {
	ID       string                   `json:"id"`
	Name     string                   `json:"name"`
	Channels []catalogChannelEnvelope `json:"channels"`
}

type catalogChannelEnvelope struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	PlayMode string `json:"playMode"`
	Live     bool   `json:"live"`
}

// catalogEnvelope is the `catalog` SSE event payload.
type catalogEnvelope struct {
	Providers []catalogProviderEnvelope `json:"providers"`
}

// providerStatusEnvelope is the `providerStatus` SSE event payload: per-channel
// enumeration status plus the optional auto-enable signal (spec §8). The chassis
// renders "Enumerating…"/"N videos"/"✗ error" chips and the auto-enable toast
// from this in Phase 6.
type providerStatusEnvelope struct {
	Provider           string                  `json:"provider"`
	Channels           []channelStatusEnvelope `json:"channels,omitempty"`
	AutoEnabledStreams string                  `json:"autoEnabledStreams,omitempty"` // "on" | "restart-required"
}

type channelStatusEnvelope struct {
	Channel   string `json:"channel"`
	State     string `json:"state"` // "ready" | "pending" | "error"
	ItemCount int    `json:"itemCount"`
}

func buildCatalogProviderEnvelope(p adapters.CatalogProvider) catalogProviderEnvelope {
	groups := make([]catalogGroupEnvelope, 0, len(p.Groups))
	for _, g := range p.Groups {
		chans := make([]catalogChannelEnvelope, 0, len(g.Channels))
		for _, c := range g.Channels {
			chans = append(chans, catalogChannelEnvelope{ID: c.ID, Name: c.Name, PlayMode: c.PlayMode, Live: c.Live})
		}
		groups = append(groups, catalogGroupEnvelope{ID: g.ID, Name: g.Name, Channels: chans})
	}
	return catalogProviderEnvelope{
		ID: p.ID, DisplayName: p.DisplayName, BadgeLabel: p.BadgeLabel,
		BadgeClass: p.BadgeClass, Live: p.Live, Groups: groups,
	}
}
