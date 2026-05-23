package playback

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type fakeStatusViewer struct {
	view core.StatusHomeView
}

func (f fakeStatusViewer) StatusHomeView() core.StatusHomeView {
	return f.view
}

type countingStatusViewer struct {
	view  core.StatusHomeView
	calls int
}

func (f *countingStatusViewer) StatusHomeView() core.StatusHomeView {
	f.calls++
	return f.view
}

type fakeProvider struct {
	view         adapters.PlaybackBannerAdapterView
	owns         bool
	banners      []adapters.PlaybackBannerSnapshot
	actions      []adapters.PlaybackActionRequest
	actionResult adapters.PlaybackActionResult
	actionErr    error
}

func (f *fakeProvider) PlaybackBanner(_ context.Context, snap adapters.PlaybackBannerSnapshot) (adapters.PlaybackBannerAdapterView, bool) {
	f.banners = append(f.banners, snap)
	return f.view, f.owns
}

func (f *fakeProvider) HandlePlaybackAction(_ context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	f.actions = append(f.actions, req)
	return f.actionResult, f.actionErr
}

type fakeAdapter struct {
	*fakeProvider
	name string
}

func (f *fakeAdapter) Name() string { return f.name }
func (f *fakeAdapter) DisplayName() string {
	return f.name
}
func (f *fakeAdapter) Fields() []adapters.FieldDef { return nil }
func (f *fakeAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error {
	return nil
}
func (f *fakeAdapter) IsEnabled() bool { return true }
func (f *fakeAdapter) Start(context.Context) error {
	return nil
}
func (f *fakeAdapter) Stop() error { return nil }
func (f *fakeAdapter) Status() adapters.Status {
	return adapters.Status{State: adapters.StateRunning}
}
func (f *fakeAdapter) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

type fakeBareAdapter struct {
	name string
}

func (f *fakeBareAdapter) Name() string        { return f.name }
func (f *fakeBareAdapter) DisplayName() string { return f.name }
func (f *fakeBareAdapter) Fields() []adapters.FieldDef {
	return nil
}
func (f *fakeBareAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error {
	return nil
}
func (f *fakeBareAdapter) IsEnabled() bool { return true }
func (f *fakeBareAdapter) Start(context.Context) error {
	return nil
}
func (f *fakeBareAdapter) Stop() error { return nil }
func (f *fakeBareAdapter) Status() adapters.Status {
	return adapters.Status{State: adapters.StateRunning}
}
func (f *fakeBareAdapter) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

func registerFakeProvider(t *testing.T, reg *adapters.Registry, name string, provider *fakeProvider) *fakeAdapter {
	t.Helper()
	adapter := &fakeAdapter{name: name, fakeProvider: provider}
	if err := reg.Register(adapter); err != nil {
		t.Fatalf("Register(%q): %v", name, err)
	}
	return adapter
}

func TestDispatcher_PlaybackView_NoActiveSession(t *testing.T) {
	provider := &fakeProvider{
		view: adapters.PlaybackBannerAdapterView{Title: "ignored"},
		owns: true,
	}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)
	dispatcher := NewDispatcher(fakeStatusViewer{
		view: core.StatusHomeView{State: core.StateIdle},
	}, reg)

	got, ok := dispatcher.PlaybackView(context.Background())
	if ok {
		t.Fatalf("PlaybackView() ok = true, view = %#v; want ok false", got)
	}
	if len(provider.banners) != 0 {
		t.Fatalf("PlaybackBanner called %d times for idle snapshot; want 0", len(provider.banners))
	}
}

func TestDispatcher_PlaybackView_DelegatesToOwningProvider(t *testing.T) {
	startedAt := time.Date(2026, 5, 22, 12, 30, 0, 0, time.UTC)
	wantView := adapters.PlaybackBannerAdapterView{
		Title:         "Provider Title",
		Subtitle:      "Provider Subtitle",
		SourceDisplay: "Plex",
		Actions: []adapters.PlaybackAction{{
			ID:      adapters.PlaybackActionStop,
			Label:   "Stop",
			Enabled: true,
		}},
		Seek: &adapters.PlaybackSeek{Enabled: true, OffsetMS: 1234, DurationMS: 5678},
	}
	provider := &fakeProvider{view: wantView, owns: true}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)
	snap := core.StatusHomeView{
		State:      core.StatePlaying,
		AdapterRef: "plex:/library/metadata/1",
		Source:     "plex",
		Title:      "Snapshot Title",
		Position:   12 * time.Second,
		Duration:   time.Minute,
		StartedAt:  startedAt,
		MediaKind:  core.MediaKindVideo,
		Modeline:   "NTSC_480i",
		Generation: 42,
	}
	dispatcher := NewDispatcher(fakeStatusViewer{view: snap}, reg)

	got, ok := dispatcher.PlaybackView(context.Background())
	if !ok {
		t.Fatalf("PlaybackView() ok = false, want true")
	}
	if !reflect.DeepEqual(got, wantView) {
		t.Fatalf("PlaybackView() = %#v, want %#v", got, wantView)
	}
	if len(provider.banners) != 1 {
		t.Fatalf("PlaybackBanner calls = %d, want 1", len(provider.banners))
	}
	wantSnap := adapters.PlaybackBannerSnapshot{
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
	if !reflect.DeepEqual(provider.banners[0], wantSnap) {
		t.Fatalf("PlaybackBanner snapshot = %#v, want %#v", provider.banners[0], wantSnap)
	}
}

func TestDispatcher_PlaybackProviderForSnapshot_SourceFirstPolicy(t *testing.T) {
	reg := adapters.NewRegistry()
	plexAdapter := registerFakeProvider(t, reg, "plex", &fakeProvider{})
	registerFakeProvider(t, reg, "url", &fakeProvider{})
	dispatcher := NewDispatcher(nil, reg)

	got, ok := dispatcher.playbackProviderForSnapshot(core.StatusHomeView{
		State:      core.StatePlaying,
		Source:     "plex",
		AdapterRef: "url:abc",
	})
	if !ok {
		t.Fatalf("playbackProviderForSnapshot() ok = false, want true")
	}
	if got != plexAdapter {
		t.Fatalf("playbackProviderForSnapshot() = %T(%p), want registered source adapter %T(%p)", got, got, plexAdapter, plexAdapter)
	}

	reg = adapters.NewRegistry()
	if err := reg.Register(&fakeBareAdapter{name: "plex"}); err != nil {
		t.Fatalf("Register bare plex: %v", err)
	}
	urlAdapter := registerFakeProvider(t, reg, "url", &fakeProvider{})
	dispatcher = NewDispatcher(nil, reg)

	got, ok = dispatcher.playbackProviderForSnapshot(core.StatusHomeView{
		State:      core.StatePlaying,
		Source:     "plex",
		AdapterRef: "url:abc",
	})
	if ok {
		t.Fatalf("playbackProviderForSnapshot() = %T(%p), ok true; want no provider from registered non-provider source, not fallback to %p", got, got, urlAdapter)
	}
}

func TestDispatcher_PlaybackProviderForSnapshot_LegacyRefScanFallback(t *testing.T) {
	reg := adapters.NewRegistry()
	urlAdapter := registerFakeProvider(t, reg, "url", &fakeProvider{})
	dispatcher := NewDispatcher(nil, reg)

	got, ok := dispatcher.playbackProviderForSnapshot(core.StatusHomeView{
		State:      core.StatePlaying,
		AdapterRef: "url:abc",
	})
	if !ok {
		t.Fatalf("playbackProviderForSnapshot() ok = false, want true")
	}
	if got != urlAdapter {
		t.Fatalf("playbackProviderForSnapshot() = %T(%p), want registered adapter %T(%p)", got, got, urlAdapter, urlAdapter)
	}
}

func TestDispatcher_PlaybackViewForSnapshot_DoesNotAcquireFreshSnapshot(t *testing.T) {
	provider := &fakeProvider{
		view: adapters.PlaybackBannerAdapterView{Title: "Provider Title"},
		owns: true,
	}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)
	status := &countingStatusViewer{
		view: core.StatusHomeView{
			State:      core.StatePlaying,
			Source:     "plex",
			AdapterRef: "plex:from-status",
		},
	}
	dispatcher := NewDispatcher(status, reg)

	got, ok := dispatcher.PlaybackViewForSnapshot(context.Background(), core.StatusHomeView{
		State:      core.StatePlaying,
		Source:     "plex",
		AdapterRef: "plex:from-argument",
		Generation: 9,
	})
	if !ok {
		t.Fatalf("PlaybackViewForSnapshot() ok = false, want true")
	}
	if got.Title != "Provider Title" {
		t.Fatalf("PlaybackViewForSnapshot() title = %q, want %q", got.Title, "Provider Title")
	}
	if status.calls != 0 {
		t.Fatalf("StatusHomeView calls = %d, want 0", status.calls)
	}
	if len(provider.banners) != 1 {
		t.Fatalf("PlaybackBanner calls = %d, want 1", len(provider.banners))
	}
	if provider.banners[0].AdapterRef != "plex:from-argument" {
		t.Fatalf("PlaybackBanner AdapterRef = %q, want argument snapshot ref", provider.banners[0].AdapterRef)
	}
}

func TestDispatcher_HandlePlaybackAction_StaleGenerationReturnsSentinel(t *testing.T) {
	provider := &fakeProvider{}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)
	dispatcher := NewDispatcher(fakeStatusViewer{
		view: core.StatusHomeView{
			State:      core.StatePlaying,
			Source:     "plex",
			AdapterRef: "plex:abc",
			Generation: 7,
		},
	}, reg)

	_, err := dispatcher.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{
		Action:     adapters.PlaybackActionStop,
		AdapterRef: "plex:abc",
		Generation: 99,
	})
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("HandlePlaybackAction stale generation error = %v, want ErrActiveSessionChanged", err)
	}
	if len(provider.actions) != 0 {
		t.Fatalf("HandlePlaybackAction provider calls = %d, want 0", len(provider.actions))
	}
}

func TestDispatcher_HandlePlaybackAction_UnsupportedAdapterReturnsSentinel(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&fakeBareAdapter{name: "plex"}); err != nil {
		t.Fatalf("Register bare plex: %v", err)
	}
	dispatcher := NewDispatcher(fakeStatusViewer{
		view: core.StatusHomeView{
			State:      core.StatePlaying,
			Source:     "plex",
			AdapterRef: "plex:abc",
			Generation: 7,
		},
	}, reg)

	_, err := dispatcher.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{
		Action:     adapters.PlaybackActionStop,
		AdapterRef: "plex:abc",
		Generation: 7,
	})
	if !errors.Is(err, adapters.ErrPlaybackActionUnsupported) {
		t.Fatalf("HandlePlaybackAction unsupported adapter error = %v, want ErrPlaybackActionUnsupported", err)
	}
}

func TestDispatcher_HandlePlaybackAction_DispatchesToProvider(t *testing.T) {
	wantResult := adapters.PlaybackActionResult{Message: "stopped"}
	provider := &fakeProvider{actionResult: wantResult}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)
	dispatcher := NewDispatcher(fakeStatusViewer{
		view: core.StatusHomeView{
			State:      core.StatePlaying,
			Source:     "plex",
			AdapterRef: "plex:abc",
			Generation: 7,
		},
	}, reg)
	req := adapters.PlaybackActionRequest{
		Action:     adapters.PlaybackActionStop,
		AdapterRef: "plex:abc",
		Generation: 7,
	}

	got, err := dispatcher.HandlePlaybackAction(context.Background(), req)
	if err != nil {
		t.Fatalf("HandlePlaybackAction dispatch error = %v", err)
	}
	if !reflect.DeepEqual(got, wantResult) {
		t.Fatalf("HandlePlaybackAction result = %#v, want %#v", got, wantResult)
	}
	if len(provider.actions) != 1 {
		t.Fatalf("HandlePlaybackAction provider calls = %d, want 1", len(provider.actions))
	}
	if !reflect.DeepEqual(provider.actions[0], req) {
		t.Fatalf("HandlePlaybackAction provider request = %#v, want %#v", provider.actions[0], req)
	}
}

func TestDispatcher_HandlePlaybackAction_ClampsNegativeOffsetToZero(t *testing.T) {
	provider := &fakeProvider{}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)
	dispatcher := NewDispatcher(fakeStatusViewer{
		view: core.StatusHomeView{
			State:      core.StatePlaying,
			Source:     "plex",
			AdapterRef: "plex:abc",
			Generation: 7,
		},
	}, reg)

	_, err := dispatcher.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{
		Action:     adapters.PlaybackActionSeek,
		AdapterRef: "plex:abc",
		Generation: 7,
		OffsetMS:   -500,
	})
	if err != nil {
		t.Fatalf("HandlePlaybackAction seek error = %v", err)
	}
	if len(provider.actions) != 1 {
		t.Fatalf("HandlePlaybackAction provider calls = %d, want 1", len(provider.actions))
	}
	if provider.actions[0].OffsetMS != 0 {
		t.Fatalf("HandlePlaybackAction provider OffsetMS = %d, want 0", provider.actions[0].OffsetMS)
	}
}

func TestDispatcher_HandlePlaybackAction_NormalizesLegacyStaleMessage(t *testing.T) {
	provider := &fakeProvider{actionErr: fmt.Errorf("active session changed")}
	reg := adapters.NewRegistry()
	registerFakeProvider(t, reg, "plex", provider)
	dispatcher := NewDispatcher(fakeStatusViewer{
		view: core.StatusHomeView{
			State:      core.StatePlaying,
			Source:     "plex",
			AdapterRef: "plex:abc",
			Generation: 7,
		},
	}, reg)

	_, err := dispatcher.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{
		Action:     adapters.PlaybackActionStop,
		AdapterRef: "plex:abc",
		Generation: 7,
	})
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("HandlePlaybackAction legacy stale error = %v, want ErrActiveSessionChanged", err)
	}
}
