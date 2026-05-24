// Package playback provides the shared playback view and action dispatcher
// that sits above core status snapshots and adapter playback providers.
package playback

import (
	"context"
	"errors"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type StatusViewer interface {
	StatusHomeView() core.StatusHomeView
}

type Dispatcher struct {
	status   StatusViewer
	registry *adapters.Registry
}

func NewDispatcher(status StatusViewer, registry *adapters.Registry) *Dispatcher {
	return &Dispatcher{status: status, registry: registry}
}

func (d *Dispatcher) PlaybackView(ctx context.Context) (adapters.PlaybackBannerAdapterView, bool) {
	if d.status == nil {
		return adapters.PlaybackBannerAdapterView{}, false
	}
	return d.PlaybackViewForSnapshot(ctx, d.status.StatusHomeView())
}

func (d *Dispatcher) PlaybackViewForSnapshot(ctx context.Context, snap core.StatusHomeView) (adapters.PlaybackBannerAdapterView, bool) {
	if snap.State == core.StateIdle {
		return adapters.PlaybackBannerAdapterView{}, false
	}
	provider, ok := d.playbackProviderForSnapshot(snap)
	if !ok {
		return adapters.PlaybackBannerAdapterView{}, false
	}
	return provider.PlaybackBanner(ctx, snapshotForProvider(snap))
}

// HandlePlaybackAction guards actions with the active adapter ref and
// generation before handing them to the current playback provider.
func (d *Dispatcher) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	if req.OffsetMS < 0 {
		req.OffsetMS = 0
	}
	if d.status == nil {
		return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
	}
	snap := d.status.StatusHomeView()
	if snap.State == core.StateIdle {
		return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
	}
	if snap.AdapterRef != req.AdapterRef || snap.Generation != req.Generation {
		return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
	}
	provider, ok := d.playbackProviderForSnapshot(snap)
	if !ok {
		return adapters.PlaybackActionResult{}, adapters.ErrPlaybackActionUnsupported
	}
	result, err := provider.HandlePlaybackAction(ctx, req)
	if err != nil {
		if !errors.Is(err, adapters.ErrActiveSessionChanged) && err.Error() == adapters.ErrActiveSessionChangedMessage {
			return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
		}
	}
	return result, err
}

// playbackProviderForSnapshot resolves the adapter that owns the active
// session and exposes the PlaybackControlProvider interface, if any.
//
// Source-first policy: when snap.Source names a registered adapter, that
// adapter is the sole candidate. If it does not implement
// PlaybackControlProvider, this returns false instead of falling through to
// the legacy adapter-ref prefix scan.
func (d *Dispatcher) playbackProviderForSnapshot(snap core.StatusHomeView) (adapters.PlaybackControlProvider, bool) {
	if d.registry == nil || snap.AdapterRef == "" {
		return nil, false
	}
	if snap.Source != "" {
		if a, ok := d.registry.Get(snap.Source); ok {
			p, ok := a.(adapters.PlaybackControlProvider)
			return p, ok
		}
	}
	for _, a := range d.registry.List() {
		if adapterRefBelongsTo(a.Name(), snap.AdapterRef) {
			p, ok := a.(adapters.PlaybackControlProvider)
			return p, ok
		}
	}
	return nil, false
}

func adapterRefBelongsTo(adapterName, ref string) bool {
	return strings.HasPrefix(ref, adapterName+":") || strings.HasPrefix(ref, adapterName+"/")
}

func snapshotForProvider(snap core.StatusHomeView) adapters.PlaybackBannerSnapshot {
	return adapters.PlaybackBannerSnapshot{
		State:      snap.State,
		AdapterRef: snap.AdapterRef,
		Source:     snap.Source,
		Title:      snap.Title,
		Position:   snap.Position,
		Duration:   snap.Duration,
		StartedAt:  snap.StartedAt,
		MediaKind:  snap.MediaKind,
		Modeline:   snap.Modeline,
		Generation: snap.Generation,
	}
}
