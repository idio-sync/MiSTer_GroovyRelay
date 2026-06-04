package streams

import (
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// UserProviderForm returns the authoring-shape definition for a user provider
// (spec §8 edit). It is the reverse of formToDefinition: it maps the stored
// ProviderDefinition back to the chassis form payload, including per-channel
// URL/Kind/BadgeColor/Order the display CatalogProvider omits. Returns
// (zero, false) for unknown or non-user IDs.
func (a *Adapter) UserProviderForm(id string) (adapters.UserProviderForm, bool) {
	if a.userStore == nil || !isUserProviderID(id) {
		return adapters.UserProviderForm{}, false
	}
	for _, def := range a.userStore.Snapshot() {
		if def.ID != id {
			continue
		}
		groups := make([]adapters.UserGroupForm, 0, len(def.Groups))
		for _, g := range def.Groups {
			groups = append(groups, adapters.UserGroupForm{ID: g.ID, Name: g.Name, Order: g.Order})
		}
		channels := make([]adapters.UserChannelForm, 0, len(def.Channels))
		for _, c := range def.Channels {
			channels = append(channels, adapters.UserChannelForm{
				ID:       c.ID,
				Name:     c.Name,
				URL:      c.URL,
				Kind:     c.Kind,
				PlayMode: string(c.PlayMode),
				GroupID:  c.GroupID,
				Order:    c.Order,
			})
		}
		return adapters.UserProviderForm{
			ID:          def.ID,
			DisplayName: def.DisplayName,
			BadgeLabel:  def.BadgeLabel,
			BadgeColor:  def.BadgeColor,
			Groups:      groups,
			Channels:    channels,
		}, true
	}
	return adapters.UserProviderForm{}, false
}

// UserProviderStatuses returns per-channel enumeration status for every user
// provider (spec §6/§8). Reads a snapshot of the installed catalogs under a.mu,
// then builds off-lock. ItemCount derives from the runtime Channel.Items;
// State is the per-channel EnumState set by buildUserCatalog.
func (a *Adapter) UserProviderStatuses() []adapters.UserProviderStatus {
	a.mu.Lock()
	type chanRow struct {
		id, state string
		count     int
	}
	rows := map[string][]chanRow{}
	order := make([]string, 0)
	for _, id := range a.definitionOrder {
		if !isUserProviderID(id) {
			continue
		}
		cat, ok := a.catalogs[id]
		if !ok {
			continue
		}
		order = append(order, id)
		list := make([]chanRow, 0, len(cat.Channels))
		for _, ch := range cat.Channels {
			list = append(list, chanRow{id: ch.ID, state: ch.EnumState, count: len(ch.Items)})
		}
		rows[id] = list
	}
	a.mu.Unlock()

	out := make([]adapters.UserProviderStatus, 0, len(order))
	for _, id := range order {
		st := adapters.UserProviderStatus{ProviderID: id}
		for _, r := range rows[id] {
			state := r.state
			if state == "" {
				state = enumStateReady
			}
			st.Channels = append(st.Channels, adapters.UserChannelStatus{ChannelID: r.id, State: state, ItemCount: r.count})
		}
		out = append(out, st)
	}
	return out
}
