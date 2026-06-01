package localfiles

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type localPlaybackCoreStub struct {
	matched bool
	err     error
	calls   []localPlaybackCoreCall
}

type localPlaybackCoreCall struct {
	name     string
	ref      string
	gen      uint64
	offsetMS int
}

func (s *localPlaybackCoreStub) StartSession(core.SessionRequest) error { return nil }

func (s *localPlaybackCoreStub) PauseIfSession(ref string, gen uint64) (bool, error) {
	s.calls = append(s.calls, localPlaybackCoreCall{name: "pause", ref: ref, gen: gen})
	return s.matched, s.err
}

func (s *localPlaybackCoreStub) PlayIfSession(ref string, gen uint64) (bool, error) {
	s.calls = append(s.calls, localPlaybackCoreCall{name: "play", ref: ref, gen: gen})
	return s.matched, s.err
}

func (s *localPlaybackCoreStub) StopIfSession(ref string, gen uint64) (bool, error) {
	s.calls = append(s.calls, localPlaybackCoreCall{name: "stop", ref: ref, gen: gen})
	return s.matched, s.err
}

func (s *localPlaybackCoreStub) SeekToIfSession(ref string, gen uint64, offsetMS int) (bool, error) {
	s.calls = append(s.calls, localPlaybackCoreCall{name: "seek", ref: ref, gen: gen, offsetMS: offsetMS})
	return s.matched, s.err
}

func TestPlaybackBannerAdvertisesLocalFileTransportControls(t *testing.T) {
	a := &Adapter{}

	view, owns := a.PlaybackBanner(context.Background(), adapters.PlaybackBannerSnapshot{
		State:      core.StatePlaying,
		Source:     "localfiles",
		AdapterRef: "localfiles:abc123",
		Title:      "Movie",
		Position:   12 * time.Second,
		Duration:   time.Minute,
	})

	if !owns {
		t.Fatal("PlaybackBanner owns = false, want true")
	}
	if view.SourceDisplay != "Local Files" || view.Title != "Movie" {
		t.Fatalf("view identity = %q/%q, want Local Files/Movie", view.SourceDisplay, view.Title)
	}
	gotActions := playbackActionIDs(view.Actions)
	wantActions := []string{
		adapters.PlaybackActionPause,
		adapters.PlaybackActionStop,
		adapters.PlaybackActionReplay,
	}
	if !reflect.DeepEqual(gotActions, wantActions) {
		t.Fatalf("actions = %#v, want %#v", gotActions, wantActions)
	}
	if view.Seek == nil || !view.Seek.Enabled || view.Seek.OffsetMS != 12000 || view.Seek.DurationMS != 60000 {
		t.Fatalf("seek = %#v, want enabled 12000/60000", view.Seek)
	}
}

func TestPlaybackBannerAdvertisesResumeForPausedLocalFile(t *testing.T) {
	a := &Adapter{}

	view, owns := a.PlaybackBanner(context.Background(), adapters.PlaybackBannerSnapshot{
		State:      core.StatePaused,
		Source:     "localfiles",
		AdapterRef: "localfiles:abc123",
	})

	if !owns {
		t.Fatal("PlaybackBanner owns = false, want true")
	}
	gotActions := playbackActionIDs(view.Actions)
	wantActions := []string{
		adapters.PlaybackActionResume,
		adapters.PlaybackActionStop,
		adapters.PlaybackActionReplay,
	}
	if !reflect.DeepEqual(gotActions, wantActions) {
		t.Fatalf("actions = %#v, want %#v", gotActions, wantActions)
	}
}

func TestPlaybackActionsUseGuardedCoreControls(t *testing.T) {
	for _, tc := range []struct {
		name     string
		action   string
		offsetMS int
		want     localPlaybackCoreCall
	}{
		{
			name:   "pause",
			action: adapters.PlaybackActionPause,
			want:   localPlaybackCoreCall{name: "pause", ref: "localfiles:abc123", gen: 9},
		},
		{
			name:   "resume",
			action: adapters.PlaybackActionResume,
			want:   localPlaybackCoreCall{name: "play", ref: "localfiles:abc123", gen: 9},
		},
		{
			name:   "stop",
			action: adapters.PlaybackActionStop,
			want:   localPlaybackCoreCall{name: "stop", ref: "localfiles:abc123", gen: 9},
		},
		{
			name:     "seek",
			action:   adapters.PlaybackActionSeek,
			offsetMS: 15000,
			want:     localPlaybackCoreCall{name: "seek", ref: "localfiles:abc123", gen: 9, offsetMS: 15000},
		},
		{
			name:     "replay",
			action:   adapters.PlaybackActionReplay,
			offsetMS: 45000,
			want:     localPlaybackCoreCall{name: "seek", ref: "localfiles:abc123", gen: 9, offsetMS: 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coreStub := &localPlaybackCoreStub{matched: true}
			a := &Adapter{core: coreStub}

			_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{
				Action:     tc.action,
				AdapterRef: "localfiles:abc123",
				Generation: 9,
				OffsetMS:   tc.offsetMS,
			})
			if err != nil {
				t.Fatalf("HandlePlaybackAction: %v", err)
			}
			if !reflect.DeepEqual(coreStub.calls, []localPlaybackCoreCall{tc.want}) {
				t.Fatalf("core calls = %#v, want %#v", coreStub.calls, []localPlaybackCoreCall{tc.want})
			}
		})
	}
}

func TestPlaybackActionRejectsStaleLocalFileSession(t *testing.T) {
	coreStub := &localPlaybackCoreStub{matched: false}
	a := &Adapter{core: coreStub}

	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{
		Action:     adapters.PlaybackActionStop,
		AdapterRef: "localfiles:stale",
		Generation: 4,
	})

	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("HandlePlaybackAction stale err = %v, want ErrActiveSessionChanged", err)
	}
}

func playbackActionIDs(actions []adapters.PlaybackAction) []string {
	ids := make([]string, 0, len(actions))
	for _, action := range actions {
		if action.Enabled {
			ids = append(ids, action.ID)
		}
	}
	return ids
}
