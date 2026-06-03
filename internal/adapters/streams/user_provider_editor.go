package streams

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// liveEnumerator builds the live (yt-dlp-backed) playlist enumerator from the
// adapter's resolver. a.resolver and a.cookiesPath are set once at Start/New
// time and never mutated, matching the off-lock read pattern in
// refreshCatalogsDefault (refresh.go:346-350).
func (a *Adapter) liveEnumerator(cfg Config) userPlaylistEnumerator {
	return userPlaylistEnumerator{resolver: a.resolver, cookiesPath: a.cookiesPath, cacheDir: a.cacheDir, cfg: cfg}
}

type userCatalogSnapshot struct {
	definitions []ProviderDefinition
	catalogs    []ProviderCatalog
}

// buildUserCatalogSnapshotLive rebuilds the full merged snapshot with LIVE
// playlist enumeration from an explicit user-provider candidate. It is pure
// with respect to adapter state: no store write and no a.mu install.
func (a *Adapter) buildUserCatalogSnapshotLive(ctx context.Context, userProviders []ProviderDefinition) (userCatalogSnapshot, error) {
	cfg := a.configSnapshot()
	defs, catalogs, err := buildSnapshotWithEnumerator(ctx, cfg, a.cacheDir, userProviders, a.liveEnumerator(cfg))
	if err != nil {
		return userCatalogSnapshot{}, fmt.Errorf("streams: build user catalogs: %w", err)
	}
	return userCatalogSnapshot{definitions: defs, catalogs: catalogs}, nil
}

func (a *Adapter) buildUserCatalogSnapshotCacheOnly(ctx context.Context, userProviders []ProviderDefinition) (userCatalogSnapshot, error) {
	cfg := a.configSnapshot()
	defs, catalogs, err := buildSnapshotWithEnumerator(ctx, cfg, a.cacheDir, userProviders, userPlaylistEnumerator{cacheDir: a.cacheDir, cfg: cfg})
	if err != nil {
		return userCatalogSnapshot{}, fmt.Errorf("streams: build user catalogs (cache-only): %w", err)
	}
	return userCatalogSnapshot{definitions: defs, catalogs: catalogs}, nil
}

func (a *Adapter) installUserCatalogSnapshot(snapshot userCatalogSnapshot) {
	a.mu.Lock()
	a.installSnapshotLocked(snapshot.definitions, snapshot.catalogs)
	if a.state != adapters.StateStopped {
		a.state = adapters.StateRunning
	}
	a.mu.Unlock()
}

// rebuildUserCatalogsLive preserves the existing refresh-like convenience path
// for non-mutating callers. Mutating editor methods use the explicit
// build→commit→install sequence above so failed rebuilds never persist an edit.
func (a *Adapter) rebuildUserCatalogsLive(ctx context.Context) error {
	snapshot, err := a.buildUserCatalogSnapshotLive(ctx, a.userStore.Snapshot())
	if err != nil {
		return err
	}
	a.installUserCatalogSnapshot(snapshot)
	return nil
}

// rebuildUserCatalogsCacheOnly rebuilds + installs WITHOUT re-enumerating
// playlists (reuses cached items). Used by Reorder, which only re-sorts by
// Order (spec §8 "Reorder ... does not re-enumerate").
func (a *Adapter) rebuildUserCatalogsCacheOnly(ctx context.Context) error {
	snapshot, err := a.buildUserCatalogSnapshotCacheOnly(ctx, a.userStore.Snapshot())
	if err != nil {
		return err
	}
	a.installUserCatalogSnapshot(snapshot)
	return nil
}

// formToDefinition maps the chassis authoring form to a ProviderDefinition.
// The locked ID (provider + channel) is preserved verbatim; normalization,
// kind auto-detection, slug assignment, and validation all happen inside
// userStore.PlanPut → normalizeUserProvider (spec §4.5/§4.7).
func formToDefinition(id string, form adapters.UserProviderForm) ProviderDefinition {
	groups := make([]GroupDefinition, 0, len(form.Groups))
	for _, g := range form.Groups {
		groups = append(groups, GroupDefinition{ID: g.ID, Name: g.Name, Order: g.Order})
	}
	channels := make([]ChannelDefinition, 0, len(form.Channels))
	for _, c := range form.Channels {
		channels = append(channels, ChannelDefinition{
			ID:       c.ID,
			Name:     c.Name,
			GroupID:  c.GroupID,
			Kind:     c.Kind, // "" → detectChannelKind in normalize
			URL:      c.URL,
			PlayMode: PlayMode(c.PlayMode),
			Order:    c.Order,
		})
	}
	return ProviderDefinition{
		ID:          id,
		Type:        userProviderType,
		DisplayName: form.DisplayName,
		BadgeLabel:  form.BadgeLabel,
		BadgeColor:  form.BadgeColor,
		Groups:      groups,
		Channels:    channels,
	}
}

// userInputError wraps validation/normalization failures as client-facing
// 400s so the chassis renders them inline. Store persistence failures are
// internal errors and MUST bubble as 500s.
func userInputError(err error) error {
	if err == nil {
		return nil
	}
	var qerr *adapters.QuickCastError
	if errors.As(err, &qerr) {
		return err
	}
	return &adapters.QuickCastError{Status: http.StatusBadRequest, Chip: "BAD INPUT", Message: err.Error()}
}

// CreateUserProvider persists a new user provider and rebuilds the live
// catalog. AutoEnableNeeded is true when this is the FIRST user provider and
// Streams is currently disabled — the chassis then invokes the injected
// EnsureStarted capability (spec §10).
func (a *Adapter) CreateUserProvider(ctx context.Context, form adapters.UserProviderForm) (adapters.UserProviderResult, error) {
	if a.userStore == nil {
		return adapters.UserProviderResult{}, fmt.Errorf("streams: user provider store not initialized")
	}
	a.userEditMu.Lock()
	defer a.userEditMu.Unlock()
	wasDisabled := !a.IsEnabled()

	saved, candidate, err := a.userStore.PlanPut(formToDefinition("", form))
	if err != nil {
		return adapters.UserProviderResult{}, userInputError(err)
	}
	firstProvider := len(candidate) == 1
	snapshot, err := a.buildUserCatalogSnapshotLive(ctx, candidate)
	if err != nil {
		return adapters.UserProviderResult{}, err
	}
	if err := a.userStore.CommitProviders(candidate); err != nil {
		return adapters.UserProviderResult{}, err
	}
	a.installUserCatalogSnapshot(snapshot)
	a.presetStore.Rehydrate()
	return adapters.UserProviderResult{
		Provider:         buildChassisCatalogProvider(saved),
		AutoEnableNeeded: firstProvider && wasDisabled,
	}, nil
}

// UpdateUserProvider replaces a user provider's definition (preserving locked
// provider/channel IDs via userStore.PlanPut) and clears preset slots for any
// channel that was removed in the update (spec §4.5: "Previously-known channel
// ID absent from the submitted provider → delete that channel and clear any
// preset slots that reference (providerID, channelID)").
func (a *Adapter) UpdateUserProvider(ctx context.Context, id string, form adapters.UserProviderForm) (adapters.UserProviderResult, error) {
	if a.userStore == nil {
		return adapters.UserProviderResult{}, fmt.Errorf("streams: user provider store not initialized")
	}
	if !isUserProviderID(id) {
		return adapters.UserProviderResult{}, userInputError(fmt.Errorf("provider id %q is not a user provider", id))
	}
	a.userEditMu.Lock()
	defer a.userEditMu.Unlock()
	if !a.userStore.Exists(id) {
		return adapters.UserProviderResult{}, notFoundUserProvider(id)
	}
	prevChannelIDs := a.userChannelIDs(id)

	saved, candidate, err := a.userStore.PlanPut(formToDefinition(id, form))
	if err != nil {
		return adapters.UserProviderResult{}, userInputError(err)
	}
	snapshot, err := a.buildUserCatalogSnapshotLive(ctx, candidate)
	if err != nil {
		return adapters.UserProviderResult{}, err
	}
	if err := a.userStore.CommitProviders(candidate); err != nil {
		return adapters.UserProviderResult{}, err
	}
	a.installUserCatalogSnapshot(snapshot)

	// Clear presets for channels that existed before but are gone now.
	savedChannelIDs := map[string]struct{}{}
	for _, ch := range saved.Channels {
		savedChannelIDs[ch.ID] = struct{}{}
	}
	var cleared []int
	for chID := range prevChannelIDs {
		if _, ok := savedChannelIDs[chID]; !ok {
			slots, err := a.presetStore.ClearChannelSlots(id, chID)
			if err != nil {
				a.presetStore.Rehydrate() // catalog already installed; refresh surviving slots before bailing
				return adapters.UserProviderResult{}, err
			}
			cleared = append(cleared, slots...)
		}
	}
	a.presetStore.Rehydrate()
	sort.Ints(cleared)
	return adapters.UserProviderResult{
		Provider:     buildChassisCatalogProvider(saved),
		ClearedSlots: cleared,
	}, nil
}

// DeleteUserProvider removes a user provider, rebuilds the catalog (the
// provider disappears), and clears every preset slot referencing the
// provider's channels (spec §4.5/§9 item 8).
func (a *Adapter) DeleteUserProvider(ctx context.Context, id string) (adapters.UserProviderResult, error) {
	if a.userStore == nil {
		return adapters.UserProviderResult{}, fmt.Errorf("streams: user provider store not initialized")
	}
	if !isUserProviderID(id) {
		return adapters.UserProviderResult{}, userInputError(fmt.Errorf("provider id %q is not a user provider", id))
	}
	a.userEditMu.Lock()
	defer a.userEditMu.Unlock()
	candidate, ok := a.userStore.PlanDelete(id)
	if !ok {
		return adapters.UserProviderResult{}, notFoundUserProvider(id)
	}
	// Rebuild WITHOUT the deleted provider, then clear its preset slots by
	// provider ID (the channels are gone from the catalog, so the resolver-gated
	// SetStarred path cannot do this — that is why ClearProviderSlots exists).
	snapshot, err := a.buildUserCatalogSnapshotLive(ctx, candidate)
	if err != nil {
		return adapters.UserProviderResult{}, err
	}
	if err := a.userStore.CommitProviders(candidate); err != nil {
		return adapters.UserProviderResult{}, err
	}
	a.installUserCatalogSnapshot(snapshot)
	cleared, err := a.presetStore.ClearProviderSlots(id)
	if err != nil {
		a.presetStore.Rehydrate() // catalog already installed; refresh surviving slots before bailing
		return adapters.UserProviderResult{}, err
	}
	a.presetStore.Rehydrate()
	return adapters.UserProviderResult{ClearedSlots: cleared}, nil
}

// ReorderUserProvider applies new Order values to a stored provider's channels
// and groups, then rebuilds CACHE-ONLY so playlist items are reused (no
// yt-dlp). Unknown IDs in the request are ignored (defensive: the browser may
// be stale). Order-only edits never affect preset references.
func (a *Adapter) ReorderUserProvider(ctx context.Context, id string, req adapters.ReorderRequest) error {
	if a.userStore == nil {
		return fmt.Errorf("streams: user provider store not initialized")
	}
	if !isUserProviderID(id) {
		return userInputError(fmt.Errorf("provider id %q is not a user provider", id))
	}
	a.userEditMu.Lock()
	defer a.userEditMu.Unlock()
	var target ProviderDefinition
	found := false
	for _, def := range a.userStore.Snapshot() {
		if def.ID == id {
			target = def
			found = true
			break
		}
	}
	if !found {
		return notFoundUserProvider(id)
	}
	chOrder := map[string]int{}
	for _, e := range req.Channels {
		chOrder[e.ID] = e.Order
	}
	grOrder := map[string]int{}
	for _, e := range req.Groups {
		grOrder[e.ID] = e.Order
	}
	for i := range target.Channels {
		if o, ok := chOrder[target.Channels[i].ID]; ok {
			target.Channels[i].Order = o
		}
	}
	for i := range target.Groups {
		if o, ok := grOrder[target.Groups[i].ID]; ok {
			target.Groups[i].Order = o
		}
	}
	_, candidate, err := a.userStore.PlanPut(target)
	if err != nil {
		return userInputError(err)
	}
	snapshot, err := a.buildUserCatalogSnapshotCacheOnly(ctx, candidate)
	if err != nil {
		return err
	}
	if err := a.userStore.CommitProviders(candidate); err != nil {
		return err
	}
	a.installUserCatalogSnapshot(snapshot)
	a.presetStore.Rehydrate()
	return nil
}

// userChannelIDs returns the set of channel IDs for a stored user provider
// (empty set if absent). Read off the store snapshot — no lock on a.mu.
func (a *Adapter) userChannelIDs(id string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, def := range a.userStore.Snapshot() {
		if def.ID != id {
			continue
		}
		for _, ch := range def.Channels {
			out[ch.ID] = struct{}{}
		}
	}
	return out
}
