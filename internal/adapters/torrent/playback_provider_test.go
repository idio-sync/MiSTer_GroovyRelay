package torrent

import (
	"context"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type torrentProviderCoreStub struct {
	status  core.SessionStatus
	stopRef string
	stopGen uint64
}

func (s *torrentProviderCoreStub) StartSession(core.SessionRequest) error { return nil }
func (s *torrentProviderCoreStub) Status() core.SessionStatus             { return s.status }
func (s *torrentProviderCoreStub) Stop() error                            { return nil }
func (s *torrentProviderCoreStub) StopIfSession(ref string, gen uint64) (bool, error) {
	s.stopRef, s.stopGen = ref, gen
	return ref == s.status.AdapterRef && gen == s.status.Generation, nil
}

func TestTorrentPlaybackBannerStop(t *testing.T) {
	coreStub := &torrentProviderCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "torrent:abc", Generation: 4}}
	a := &Adapter{core: coreStub, cfg: Config{Enabled: true, TrafficAcknowledged: true}}
	view, owns := a.PlaybackBanner(context.Background(), adapters.PlaybackBannerSnapshot{State: core.StatePlaying, Source: "torrent", AdapterRef: "torrent:abc", Generation: 4})
	if !owns {
		t.Fatal("torrent provider did not own torrent snapshot")
	}
	if len(view.Actions) != 1 || view.Actions[0].ID != adapters.PlaybackActionStop {
		t.Fatalf("actions = %#v, want stop", view.Actions)
	}
}

func TestTorrentStopUsesFullSessionKey(t *testing.T) {
	coreStub := &torrentProviderCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "torrent:abc", Generation: 4}}
	a := &Adapter{core: coreStub}
	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionStop, AdapterRef: "torrent:abc", Generation: 4})
	if err != nil {
		t.Fatalf("HandlePlaybackAction stop: %v", err)
	}
	if coreStub.stopRef != "torrent:abc" || coreStub.stopGen != 4 {
		t.Fatalf("stop key = %q/%d", coreStub.stopRef, coreStub.stopGen)
	}
}

func TestTorrentStopRejectsStaleGeneration(t *testing.T) {
	coreStub := &torrentProviderCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "torrent:abc", Generation: 5}}
	a := &Adapter{core: coreStub}
	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionStop, AdapterRef: "torrent:abc", Generation: 4})
	if err == nil || !strings.Contains(err.Error(), "active session changed") {
		t.Fatalf("HandlePlaybackAction stale error = %v, want active session changed", err)
	}
	if coreStub.stopRef != "torrent:abc" || coreStub.stopGen != 4 {
		t.Fatalf("stop key = %q/%d, want submitted stale key", coreStub.stopRef, coreStub.stopGen)
	}
}

func TestTorrentStopRejectsForeignAdapterRef(t *testing.T) {
	coreStub := &torrentProviderCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc", Generation: 4}}
	a := &Adapter{core: coreStub}
	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionStop, AdapterRef: "torrent:abc", Generation: 4})
	if err == nil || !strings.Contains(err.Error(), "active session changed") {
		t.Fatalf("HandlePlaybackAction foreign error = %v, want active session changed", err)
	}
	if coreStub.stopRef != "torrent:abc" || coreStub.stopGen != 4 {
		t.Fatalf("stop key = %q/%d, want submitted foreign key", coreStub.stopRef, coreStub.stopGen)
	}
}

func TestTorrentQuickCastRejectsDisabledAdapter(t *testing.T) {
	a := &Adapter{cfg: Config{Enabled: false, TrafficAcknowledged: true}}
	_, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{TabID: "torrent-magnet", Values: map[string]string{"magnet": "magnet:?xt=urn:btih:abc"}})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("HandleQuickCast disabled error = %v, want disabled", err)
	}
}
