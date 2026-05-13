package streams

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"
)

type StatusView struct {
	Providers    []ProviderStatusView `json:"providers"`
	Active       *QueueStatusView     `json:"active,omitempty"`
	Capabilities ControlCapabilities  `json:"capabilities"`
}

type ProviderStatusView struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	Groups    []ChannelGroupView  `json:"groups,omitempty"`
	Channels  []ChannelStatusView `json:"channels"`
	UpdatedAt time.Time           `json:"updated_at,omitempty"`
}

type ChannelGroupView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ChannelStatusView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	GroupID     string `json:"group_id,omitempty"`
	ItemCount   int    `json:"item_count"`
	PlayMode    string `json:"play_mode,omitempty"`
}

type QueueStatusView struct {
	ProviderID   string    `json:"provider_id"`
	ProviderName string    `json:"provider_name,omitempty"`
	ChannelID    string    `json:"channel_id"`
	ChannelName  string    `json:"channel_name,omitempty"`
	ItemID       string    `json:"item_id,omitempty"`
	ItemTitle    string    `json:"item_title,omitempty"`
	Index        int       `json:"index"`
	Total        int       `json:"total"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	AdapterRef   string    `json:"adapter_ref,omitempty"`
	PositionMS   int64     `json:"position_ms,omitempty"`
	DurationMS   int64     `json:"duration_ms,omitempty"`
}

type ControlCapabilities struct {
	CanStop     bool `json:"can_stop"`
	CanReplay   bool `json:"can_replay"`
	CanNext     bool `json:"can_next"`
	CanPrevious bool `json:"can_previous"`
	CanPause    bool `json:"can_pause"`
	CanSeek     bool `json:"can_seek"`
}

func (a *Adapter) ExtraPanelHTML() template.HTML {
	return template.HTML(a.renderPanel())
}

func (a *Adapter) statusView() StatusView {
	a.mu.Lock()
	defer a.mu.Unlock()

	providers := make([]ProviderStatusView, 0, len(a.catalogs))
	ids := make([]string, 0, len(a.catalogs))
	for id := range a.catalogs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if providerCfg, ok := a.cfg.Providers[id]; ok && providerCfg.Disabled {
			continue
		}
		cat := a.catalogs[id]
		p := ProviderStatusView{
			ID:        cat.ProviderID,
			Name:      cat.Name,
			UpdatedAt: cat.UpdatedAt,
			Groups:    make([]ChannelGroupView, 0, len(cat.Groups)),
			Channels:  make([]ChannelStatusView, 0, len(cat.Channels)),
		}
		if p.Name == "" {
			p.Name = providerDisplayName(cat, a.definitions[id])
		}
		for _, group := range cat.Groups {
			p.Groups = append(p.Groups, ChannelGroupView{
				ID:   group.ID,
				Name: group.Name,
			})
		}
		for _, ch := range sortedChannels(cat.Channels) {
			if len(ch.Items) == 0 {
				continue
			}
			p.Channels = append(p.Channels, ChannelStatusView{
				ID:          ch.ID,
				Name:        ch.Name,
				Description: ch.Description,
				GroupID:     ch.GroupID,
				ItemCount:   len(ch.Items),
				PlayMode:    string(ch.PlayMode),
			})
		}
		if len(p.Channels) == 0 {
			continue
		}
		providers = append(providers, p)
	}

	var active *QueueStatusView
	caps := ControlCapabilities{CanSeek: false}
	if a.active != nil {
		q := a.active
		activeRef := activeAdapterRef(q)
		currentDirect := false
		active = &QueueStatusView{
			ProviderID:   q.ProviderID,
			ProviderName: q.ProviderName,
			ChannelID:    q.ChannelID,
			ChannelName:  q.ChannelName,
			Index:        q.Index,
			Total:        len(q.Items),
			StartedAt:    q.StartedAt,
			AdapterRef:   activeRef,
		}
		if item, ok := q.currentItem(); ok {
			active.ItemID = itemIdentity(item)
			active.ItemTitle = item.Title
			currentDirect = item.Direct
		}
		if activeRef != "" && a.core != nil {
			coreStatus := a.core.Status()
			if coreStatus.AdapterRef == activeRef {
				active.PositionMS = coreStatus.Position.Milliseconds()
				active.DurationMS = coreStatus.Duration.Milliseconds()
				caps.CanStop = true
				caps.CanReplay = a.canReplayLocked(q)
				caps.CanNext = q.canAdvanceNext()
				caps.CanPrevious = q.canAdvancePrevious()
				caps.CanPause = !currentDirect
			}
		}
	}

	return StatusView{
		Providers:    providers,
		Active:       active,
		Capabilities: caps,
	}
}

func (a *Adapter) canReplayLocked(q *ActiveQueue) bool {
	if q == nil || len(q.Items) == 0 {
		return false
	}
	if q.ChannelID == reservedAdhocID {
		return true
	}
	cat, ok := a.catalogs[q.ProviderID]
	if !ok {
		return false
	}
	ch := cat.Channel(q.ChannelID)
	return ch != nil && len(ch.Items) > 0
}

func (a *Adapter) renderPanel() string {
	view := a.statusView()
	var b strings.Builder
	trigger := "every 5s"
	if view.Active != nil {
		trigger = "every 1s"
	}
	fmt.Fprintf(&b, `<section class="streams-panel" id="streams-panel" hx-get="/ui/adapter/streams/panel" hx-trigger="%s" hx-swap="outerHTML">`, trigger)
	b.WriteString(`<h3>Streams</h3>`)
	b.WriteString(`<div class="controls">`)
	button(&b, "/ui/adapter/streams/refresh", "Refresh", false)
	b.WriteString(`</div>`)

	if view.Active != nil {
		fmt.Fprintf(&b, `<p class="status run">Playing: %s / %s</p>`,
			esc(view.Active.ProviderName), esc(view.Active.ChannelName))
		if view.Active.ItemTitle != "" {
			fmt.Fprintf(&b, `<p class="streams-now-title">%s</p>`, esc(view.Active.ItemTitle))
		}
		if view.Active.DurationMS > 0 {
			position := time.Duration(view.Active.PositionMS) * time.Millisecond
			duration := time.Duration(view.Active.DurationMS) * time.Millisecond
			fmt.Fprintf(&b, `<p class="position">%s / %s</p>`,
				formatDuration(position), formatDuration(duration))
		}
		b.WriteString(`<div class="controls">`)
		button(&b, "/ui/adapter/streams/previous", "Previous", !view.Capabilities.CanPrevious)
		button(&b, "/ui/adapter/streams/next", "Next", !view.Capabilities.CanNext)
		button(&b, "/ui/adapter/streams/replay", "Replay", !view.Capabilities.CanReplay)
		button(&b, "/ui/adapter/streams/stop", "Stop", !view.Capabilities.CanStop)
		b.WriteString(`</div>`)
	} else {
		b.WriteString(`<p class="status">Idle</p>`)
	}

	b.WriteString(renderProvidersFromView(view.Providers))
	b.WriteString(`</section>`)
	return b.String()
}

func (a *Adapter) renderProviders() string {
	return renderProvidersFromView(a.statusView().Providers)
}

func renderProvidersFromView(providers []ProviderStatusView) string {
	var b strings.Builder
	b.WriteString(`<div class="streams-providers">`)
	for _, p := range providers {
		fmt.Fprintf(&b, `<section class="streams-provider"><h4>%s</h4>`, esc(p.Name))
		if len(p.Channels) == 0 {
			b.WriteString(`<p class="muted">No channels</p>`)
		}
		groups := groupedChannels(p)
		if len(groups) != 0 {
			b.WriteString(`<div class="streams-channel-groups">`)
		}
		for _, group := range groups {
			fmt.Fprintf(&b, `<section class="streams-channel-group"><h5>%s</h5><table class="streams-channel-table"><tbody>`, esc(group.Name))
			for i, ch := range group.Channels {
				if i%streamsChannelsPerRow == 0 {
					b.WriteString(`<tr>`)
				}
				fmt.Fprintf(&b,
					`<td class="streams-channel-cell">`+
						`<form class="streams-channel" hx-post="/ui/adapter/streams/play" hx-target="#streams-panel" hx-swap="outerHTML">`+
						`<input type="hidden" name="provider_id" value="%s">`+
						`<input type="hidden" name="channel_id" value="%s">`+
						`<button type="submit">%s</button>`+
						`<span class="muted">%d items</span>`+
						`</form>`+
						`</td>`,
					escAttr(p.ID), escAttr(ch.ID), esc(ch.Name), ch.ItemCount)
				if i%streamsChannelsPerRow == streamsChannelsPerRow-1 || i == len(group.Channels)-1 {
					b.WriteString(`</tr>`)
				}
			}
			b.WriteString(`</tbody></table></section>`)
		}
		if len(groups) != 0 {
			b.WriteString(`</div>`)
		}
		b.WriteString(`</section>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

const streamsChannelsPerRow = 3

type groupedChannelView struct {
	ID       string
	Name     string
	Channels []ChannelStatusView
}

func groupedChannels(provider ProviderStatusView) []groupedChannelView {
	groups := make([]groupedChannelView, 0, len(provider.Groups)+1)
	indexByID := make(map[string]int, len(provider.Groups)+1)
	for _, group := range provider.Groups {
		name := group.Name
		if name == "" {
			name = group.ID
		}
		indexByID[group.ID] = len(groups)
		groups = append(groups, groupedChannelView{ID: group.ID, Name: name})
	}

	for _, ch := range provider.Channels {
		groupID := ch.GroupID
		if groupID == "" {
			groupID = ungroupedGroupID
		}
		idx, ok := indexByID[groupID]
		if !ok {
			name := "Other Channels"
			if groupID != ungroupedGroupID {
				name = groupID
			}
			idx = len(groups)
			indexByID[groupID] = idx
			groups = append(groups, groupedChannelView{ID: groupID, Name: name})
		}
		groups[idx].Channels = append(groups[idx].Channels, ch)
	}

	out := groups[:0]
	for _, group := range groups {
		if len(group.Channels) != 0 {
			out = append(out, group)
		}
	}
	return out
}

func button(b *strings.Builder, path, label string, disabled bool) {
	dis := ""
	if disabled {
		dis = " disabled"
	}
	fmt.Fprintf(b, `<button type="button" hx-post="%s" hx-target="#streams-panel" hx-swap="outerHTML"%s>%s</button>`,
		escAttr(path), dis, esc(label))
}

func (a *Adapter) respondPanel(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(a.renderPanel()))
}

func (a *Adapter) respondControlError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<div class="gr-callout err" id="streams-panel"><p>%s</p></div>`, esc(msg))
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d / time.Second)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func sortedChannels(channels []Channel) []Channel {
	out := append([]Channel(nil), channels...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func esc(s string) string {
	return template.HTMLEscapeString(s)
}

func escAttr(s string) string {
	return template.HTMLEscapeString(s)
}
