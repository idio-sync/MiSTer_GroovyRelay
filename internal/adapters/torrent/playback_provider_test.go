package torrent

import (
	"context"
	"errors"
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
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("HandlePlaybackAction stale error = %v, want stale-session sentinel", err)
	}
	if coreStub.stopRef != "torrent:abc" || coreStub.stopGen != 4 {
		t.Fatalf("stop key = %q/%d, want submitted stale key", coreStub.stopRef, coreStub.stopGen)
	}
}

func TestTorrentStopRejectsForeignAdapterRef(t *testing.T) {
	coreStub := &torrentProviderCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc", Generation: 4}}
	a := &Adapter{core: coreStub}
	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionStop, AdapterRef: "torrent:abc", Generation: 4})
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("HandlePlaybackAction foreign error = %v, want stale-session sentinel", err)
	}
	if coreStub.stopRef != "torrent:abc" || coreStub.stopGen != 4 {
		t.Fatalf("stop key = %q/%d, want submitted foreign key", coreStub.stopRef, coreStub.stopGen)
	}
}

func TestTorrentRejectsUnsupportedActionWithSentinel(t *testing.T) {
	a := &Adapter{}
	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionPause, AdapterRef: "torrent:abc", Generation: 4})
	if !errors.Is(err, adapters.ErrPlaybackActionUnsupported) {
		t.Fatalf("HandlePlaybackAction unsupported error = %v, want unsupported-action sentinel", err)
	}
	const want = "unknown playback action \"pause\""
	if err.Error() != want {
		t.Fatalf("HandlePlaybackAction unsupported message = %q, want %q", err.Error(), want)
	}
}

func TestTorrentQuickCastRejectsDisabledAdapter(t *testing.T) {
	a := &Adapter{cfg: Config{Enabled: false, TrafficAcknowledged: true}}
	_, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{TabID: "torrent-magnet", Values: map[string]string{"magnet": "magnet:?xt=urn:btih:abc"}})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("HandleQuickCast disabled error = %v, want disabled", err)
	}
}

func TestTorrentQuickCastTabsIncludeTorrentURL(t *testing.T) {
	a := &Adapter{cfg: Config{Enabled: true, TrafficAcknowledged: true}}
	var tab adapters.QuickCastTab
	found := false
	for _, candidate := range a.QuickCastTabs() {
		if candidate.ID == "torrent-url" {
			tab = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatal("QuickCastTabs missing torrent-url tab")
	}
	if !tab.Enabled {
		t.Fatalf("torrent-url tab Enabled = false, DisabledReason = %q", tab.DisabledReason)
	}
	if tab.Encoding != adapters.QuickCastEncodingForm {
		t.Fatalf("torrent-url Encoding = %q, want form", tab.Encoding)
	}
	if len(tab.Fields) != 1 {
		t.Fatalf("torrent-url Fields = %#v, want one field", tab.Fields)
	}
	field := tab.Fields[0]
	if field.Name != "torrent_url" || field.Type != "url" {
		t.Fatalf("torrent-url field = %#v, want torrent_url url field", field)
	}
}

func TestTorrentQuickCastTorrentURLDisabledReasons(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "disabled",
			cfg:  Config{Enabled: false, TrafficAcknowledged: true},
			want: "torrent adapter is disabled",
		},
		{
			name: "unacknowledged",
			cfg:  Config{Enabled: true, TrafficAcknowledged: false},
			want: "BitTorrent traffic acknowledgement required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adapter{cfg: tt.cfg}
			var tab adapters.QuickCastTab
			found := false
			for _, candidate := range a.QuickCastTabs() {
				if candidate.ID == "torrent-url" {
					tab = candidate
					found = true
					break
				}
			}
			if !found {
				t.Fatal("QuickCastTabs missing torrent-url tab")
			}
			if tab.Enabled {
				t.Fatalf("torrent-url tab Enabled = true, want disabled")
			}
			if tab.DisabledReason != tt.want {
				t.Fatalf("torrent-url DisabledReason = %q, want %q", tab.DisabledReason, tt.want)
			}
		})
	}
}

func TestTorrentQuickCastRejectsEmptyTorrentURL(t *testing.T) {
	a := &Adapter{cfg: Config{Enabled: true, TrafficAcknowledged: true}}
	_, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{
		TabID:  "torrent-url",
		Values: map[string]string{"torrent_url": " \t\n"},
	})
	if err == nil || !strings.Contains(err.Error(), "torrent_url is required") {
		t.Fatalf("HandleQuickCast empty torrent_url error = %v, want torrent_url is required", err)
	}
}

func TestTorrentQuickCastDoesNotFetchTorrentURLWhenDisabledOrUnacknowledged(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "disabled", cfg: Config{Enabled: false, TrafficAcknowledged: true}},
		{name: "unacknowledged", cfg: Config{Enabled: true, TrafficAcknowledged: false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := &fakeTorrentURLFetcher{body: []byte("metainfo")}
			a := &Adapter{cfg: tt.cfg, urlFetcher: fetcher}
			_, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{
				TabID:  "torrent-url",
				Values: map[string]string{"torrent_url": "https://example.com/file.torrent"},
			})
			if err == nil {
				t.Fatal("HandleQuickCast error = nil, want gate rejection")
			}
			if fetcher.calls != 0 {
				t.Fatalf("fetcher calls = %d, want 0", fetcher.calls)
			}
		})
	}
}

func TestTorrentQuickCastStartsTorrentURL(t *testing.T) {
	rec := &recordingCore{}
	client := &fakeTorrentClient{
		metaTorrent: &fakeTorrent{
			hash:  "cccccccccccccccccccccccccccccccccccccccc",
			name:  "movie",
			files: []FileCandidate{{DisplayPath: "movie.mkv", Length: 10, Index: 0}},
		},
	}
	a := newStartedTestAdapter(t, startedTorrentConfig(), client, rec)
	a.urlFetcher = &fakeTorrentURLFetcher{body: []byte("metainfo")}

	result, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{
		TabID:  "torrent-url",
		Values: map[string]string{"torrent_url": "https://example.com/file.torrent"},
	})
	if err != nil {
		t.Fatalf("HandleQuickCast torrent-url: %v", err)
	}
	if result.Message != "torrent started" {
		t.Fatalf("Message = %q, want torrent started", result.Message)
	}
	if result.AdapterRef == "" {
		t.Fatal("AdapterRef is empty")
	}
	if len(rec.reqs) != 1 {
		t.Fatalf("core requests = %d, want 1", len(rec.reqs))
	}
}
