package streams

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
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
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Groups        []ChannelGroupView  `json:"groups,omitempty"`
	Channels      []ChannelStatusView `json:"channels"`
	UpdatedAt     time.Time           `json:"updated_at,omitempty"`
	LogoURL       string              `json:"logo_url,omitempty"`
	LogoAlt       string              `json:"logo_alt,omitempty"`
	FallbackLabel string              `json:"fallback_label,omitempty"`
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

type panelSelectionRequest struct {
	ProviderID       string
	GroupID          string
	ProviderExplicit bool
	GroupExplicit    bool
	ErrorMessage     string
}

type resolvedPanelSelection struct {
	ProviderID string
	GroupID    string
}

func (a *Adapter) ExtraPanelHTML() template.HTML {
	return template.HTML(a.renderPanel(panelSelectionRequest{}))
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
		def := a.definitions[id]
		p.LogoURL = strings.TrimSpace(def.LogoURL)
		p.LogoAlt = strings.TrimSpace(def.LogoAlt)
		p.FallbackLabel = strings.TrimSpace(def.FallbackLabel)
		if p.FallbackLabel == "" {
			p.FallbackLabel = strings.ToUpper(p.Name)
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
		providers = append(providers, p)
	}

	var active *QueueStatusView
	caps := ControlCapabilities{CanSeek: false}
	if a.active != nil {
		q := a.active
		activeRef := activeAdapterRef(q)
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
				caps.CanPause = true
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

func (a *Adapter) renderPanel(req panelSelectionRequest) string {
	view := a.statusView()
	selection := resolvePanelSelection(view, req)
	var b strings.Builder
	trigger := "every 5s"
	if view.Active != nil {
		trigger = "every 1s"
	}
	fmt.Fprintf(&b, `<section class="streams-panel" id="streams-panel" hx-get="%s" hx-trigger="%s" hx-swap="outerHTML">`,
		escAttr(panelURL(selection)), trigger)
	if req.ErrorMessage != "" {
		fmt.Fprintf(&b, `<div class="gr-callout err streams-error"><p>%s</p></div>`, esc(req.ErrorMessage))
	}
	renderFocusedGuide(&b, view, selection)
	b.WriteString(`</section>`)
	return b.String()
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
		for _, group := range groups {
			fmt.Fprintf(&b, `<section class="streams-channel-group"><h5>%s</h5>`, esc(group.Name))
			for _, ch := range group.Channels {
				fmt.Fprintf(&b, `<p class="streams-provider-channel">%s <span class="muted">%d items</span></p>`,
					esc(ch.Name), ch.ItemCount)
			}
			b.WriteString(`</section>`)
		}
		b.WriteString(`</section>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

func renderFocusedGuide(b *strings.Builder, view StatusView, selection resolvedPanelSelection) {
	provider, ok := selectedProvider(view.Providers, selection.ProviderID)
	b.WriteString(`<div class="streams-guide">`)
	renderNowStrip(b, view, provider, selection)
	renderProviderTabs(b, view.Providers, selection)
	if !ok {
		b.WriteString(`<div class="streams-guide-empty">No stream providers are available.</div>`)
		b.WriteString(`</div>`)
		return
	}
	groups := groupedChannels(provider)
	if len(provider.Channels) == 0 {
		b.WriteString(`<div class="streams-provider-empty">No playable channels are available for this provider.</div>`)
		b.WriteString(`</div>`)
		return
	}
	renderGuideBody(b, provider, groups, selection, view.Active)
	b.WriteString(`</div>`)
}

func selectedProvider(providers []ProviderStatusView, providerID string) (ProviderStatusView, bool) {
	for _, provider := range providers {
		if provider.ID == providerID {
			return provider, true
		}
	}
	return ProviderStatusView{}, false
}

func renderProviderTabs(b *strings.Builder, providers []ProviderStatusView, selection resolvedPanelSelection) {
	b.WriteString(`<div class="streams-provider-tabs" role="navigation" aria-label="Stream providers">`)
	for _, provider := range providers {
		active := provider.ID == selection.ProviderID
		attrs := ""
		if active {
			attrs = ` aria-current="true"`
		}
		fmt.Fprintf(b,
			`<button type="button" class="streams-provider-tab%s" hx-get="%s" hx-target="#streams-panel" hx-swap="outerHTML"%s>`+
				`<span>%s</span><span class="streams-provider-count">%d</span></button>`,
			activeClass(active), escAttr(panelURL(resolvedPanelSelection{ProviderID: provider.ID})), attrs,
			esc(provider.Name), len(provider.Channels))
	}
	b.WriteString(`</div>`)
}

func renderGuideBody(b *strings.Builder, provider ProviderStatusView, groups []groupedChannelView, selection resolvedPanelSelection, active *QueueStatusView) {
	b.WriteString(`<div class="streams-guide-body">`)
	b.WriteString(`<div class="streams-category-rail" role="navigation" aria-label="Channel categories">`)
	for _, group := range groups {
		activeGroup := group.ID == selection.GroupID
		attrs := ""
		if activeGroup {
			attrs = ` aria-current="true"`
		}
		fmt.Fprintf(b,
			`<button type="button" class="streams-category-tab%s" hx-get="%s" hx-target="#streams-panel" hx-swap="outerHTML"%s>`+
				`<span>%s</span><span class="streams-category-count">%d</span></button>`,
			activeClass(activeGroup), escAttr(panelURL(resolvedPanelSelection{ProviderID: provider.ID, GroupID: group.ID})), attrs,
			esc(group.Name), len(group.Channels))
	}
	b.WriteString(`</div>`)
	b.WriteString(`<div class="streams-channel-grid">`)
	for _, group := range groups {
		if group.ID != selection.GroupID {
			continue
		}
		for _, ch := range group.Channels {
			renderChannelCard(b, provider, ch, selection, active)
		}
	}
	b.WriteString(`</div></div>`)
}

func activeClass(active bool) string {
	if active {
		return " active"
	}
	return ""
}

func renderNowStrip(b *strings.Builder, view StatusView, provider ProviderStatusView, selection resolvedPanelSelection) {
	b.WriteString(`<div class="streams-now-strip">`)
	renderProviderArtwork(b, provider)
	b.WriteString(`<div class="streams-now-copy">`)
	if view.Active != nil {
		fmt.Fprintf(b, `<div class="streams-kicker">Now Playing</div><div class="streams-now-line">%s / %s</div>`,
			esc(view.Active.ProviderName), esc(view.Active.ChannelName))
		if view.Active.ItemTitle != "" {
			fmt.Fprintf(b, `<div class="streams-now-title">%s</div>`, esc(view.Active.ItemTitle))
		}
		if view.Active.DurationMS > 0 {
			position := time.Duration(view.Active.PositionMS) * time.Millisecond
			duration := time.Duration(view.Active.DurationMS) * time.Millisecond
			fmt.Fprintf(b, `<div class="streams-position">%s / %s</div>`,
				formatDuration(position), formatDuration(duration))
		}
	} else {
		b.WriteString(`<div class="streams-kicker">Idle</div><div class="streams-now-line">Choose a channel to start playback.</div>`)
	}
	b.WriteString(`</div><div class="streams-now-controls">`)
	button(b, "/ui/adapter/streams/refresh", "Refresh", false, selection)
	if view.Active != nil {
		button(b, "/ui/adapter/streams/previous", "Previous", !view.Capabilities.CanPrevious, selection)
		button(b, "/ui/adapter/streams/next", "Next", !view.Capabilities.CanNext, selection)
		button(b, "/ui/adapter/streams/replay", "Replay", !view.Capabilities.CanReplay, selection)
		button(b, "/ui/adapter/streams/stop", "Stop", !view.Capabilities.CanStop, selection)
	}
	b.WriteString(`</div></div>`)
}

func renderChannelCard(b *strings.Builder, provider ProviderStatusView, ch ChannelStatusView, selection resolvedPanelSelection, active *QueueStatusView) {
	tuned := active != nil && active.ProviderID == provider.ID && active.ChannelID == ch.ID
	tunedClass := ""
	if tuned {
		tunedClass = " tuned"
	}
	fmt.Fprintf(b,
		`<form class="streams-channel-card%s" hx-post="/ui/adapter/streams/play" hx-target="#streams-panel" hx-swap="outerHTML">`,
		tunedClass)
	selectionInputs(b, selection)
	fmt.Fprintf(b,
		`<input type="hidden" name="provider_id" value="%s">`+
			`<input type="hidden" name="channel_id" value="%s">`+
			`<button type="submit"><span class="streams-channel-name">%s</span><span class="streams-channel-meta">%d items</span></button></form>`,
		escAttr(provider.ID), escAttr(ch.ID), esc(ch.Name), ch.ItemCount)
}

func selectionInputs(b *strings.Builder, selection resolvedPanelSelection) {
	if selection.ProviderID != "" {
		fmt.Fprintf(b, `<input type="hidden" name="guide_provider_id" value="%s">`, escAttr(selection.ProviderID))
	}
	if selection.GroupID != "" {
		fmt.Fprintf(b, `<input type="hidden" name="guide_group_id" value="%s">`, escAttr(selection.GroupID))
	}
}

func renderProviderArtwork(b *strings.Builder, provider ProviderStatusView) {
	label := strings.TrimSpace(provider.LogoAlt)
	if label == "" {
		label = provider.Name
	}
	fallback := strings.TrimSpace(provider.FallbackLabel)
	if fallback == "" {
		fallback = provider.Name
	}
	fmt.Fprintf(b, `<div class="streams-provider-art-shell" role="img" aria-label="%s">`, escAttr(label))
	if strings.TrimSpace(provider.LogoURL) != "" {
		fmt.Fprintf(b,
			`<img class="streams-provider-art" src="%s" alt="" loading="lazy" decoding="async" referrerpolicy="no-referrer" data-streams-artwork>`,
			escAttr(provider.LogoURL))
	}
	fmt.Fprintf(b, `<span class="streams-provider-wordmark">%s</span></div>`, esc(fallback))
}

type groupedChannelView struct {
	ID       string
	Name     string
	Channels []ChannelStatusView
}

func selectionFromRequest(r *http.Request) panelSelectionRequest {
	if r == nil {
		return panelSelectionRequest{}
	}
	_ = r.ParseForm()
	providerID, providerExplicit := selectionValue(r, "guide_provider_id", "provider_id")
	groupID, groupExplicit := selectionValue(r, "guide_group_id", "group_id")
	return panelSelectionRequest{
		ProviderID:       providerID,
		GroupID:          groupID,
		ProviderExplicit: providerExplicit,
		GroupExplicit:    groupExplicit,
	}
}

func selectionValue(r *http.Request, primary, fallback string) (string, bool) {
	if values, ok := r.Form[primary]; ok {
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed, true
			}
		}
		return "", true
	}
	if values, ok := r.Form[fallback]; ok {
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed, true
			}
		}
		return "", true
	}
	return "", false
}

func resolvePanelSelection(view StatusView, req panelSelectionRequest) resolvedPanelSelection {
	if len(view.Providers) == 0 {
		return resolvedPanelSelection{}
	}
	providerIdx := -1
	if req.ProviderID != "" {
		for i, provider := range view.Providers {
			if provider.ID == req.ProviderID {
				providerIdx = i
				break
			}
		}
	}
	if providerIdx < 0 && !req.ProviderExplicit && view.Active != nil {
		for i, provider := range view.Providers {
			if provider.ID == view.Active.ProviderID {
				providerIdx = i
				break
			}
		}
	}
	if providerIdx < 0 {
		providerIdx = 0
	}

	provider := view.Providers[providerIdx]
	groups := groupedChannels(provider)
	groupID := ""
	if req.GroupID != "" {
		for _, group := range groups {
			if group.ID == req.GroupID {
				groupID = group.ID
				break
			}
		}
	}
	if groupID == "" && !req.GroupExplicit && view.Active != nil && view.Active.ProviderID == provider.ID {
		for _, channel := range provider.Channels {
			if channel.ID == view.Active.ChannelID {
				groupID = channel.GroupID
				if groupID == "" {
					groupID = ungroupedGroupID
				}
				break
			}
		}
	}
	if groupID == "" && req.GroupExplicit && req.ErrorMessage != "" && req.GroupID != "" {
		groupID = req.GroupID
	}
	if groupID == "" && len(groups) > 0 {
		groupID = groups[0].ID
	}
	return resolvedPanelSelection{ProviderID: provider.ID, GroupID: groupID}
}

func panelURL(selection resolvedPanelSelection) string {
	parts := make([]string, 0, 2)
	if selection.ProviderID != "" {
		parts = append(parts, "provider_id="+url.QueryEscape(selection.ProviderID))
	}
	if selection.GroupID != "" {
		parts = append(parts, "group_id="+url.QueryEscape(selection.GroupID))
	}
	if len(parts) != 0 {
		return "/ui/adapter/streams/panel?" + strings.Join(parts, "&")
	}
	return "/ui/adapter/streams/panel"
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

func button(b *strings.Builder, path, label string, disabled bool, selection resolvedPanelSelection) {
	dis := ""
	if disabled {
		dis = " disabled"
	}
	fmt.Fprintf(b, `<form class="streams-control-form" hx-post="%s" hx-target="#streams-panel" hx-swap="outerHTML">`, escAttr(path))
	selectionInputs(b, selection)
	fmt.Fprintf(b, `<button type="submit"%s>%s</button></form>`, dis, esc(label))
}

func (a *Adapter) respondPanel(w http.ResponseWriter, r *http.Request, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(a.renderPanel(selectionFromRequest(r))))
}

func (a *Adapter) respondPanelWithError(w http.ResponseWriter, r *http.Request, msg string) {
	req := selectionFromRequest(r)
	req.ErrorMessage = msg
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(a.renderPanel(req)))
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
