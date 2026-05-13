package url

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type providerCoreStub struct {
	status       core.SessionStatus
	pauseRef     string
	pauseGen     uint64
	playRef      string
	playGen      uint64
	stopRef      string
	stopGen      uint64
	seekRef      string
	seekGen      uint64
	seekOffset   int
	startRef     string
	startGen     uint64
	startReq     core.SessionRequest
	startMatched bool
}

func (s *providerCoreStub) StartSession(core.SessionRequest) error { return nil }
func (s *providerCoreStub) Status() core.SessionStatus             { return s.status }
func (s *providerCoreStub) Pause() error                           { return nil }
func (s *providerCoreStub) Play() error                            { return nil }
func (s *providerCoreStub) Stop() error                            { return nil }
func (s *providerCoreStub) SeekTo(int) error                       { return nil }
func (s *providerCoreStub) PauseIfSession(ref string, gen uint64) (bool, error) {
	s.pauseRef, s.pauseGen = ref, gen
	return ref == s.status.AdapterRef && gen == s.status.Generation, nil
}
func (s *providerCoreStub) PlayIfSession(ref string, gen uint64) (bool, error) {
	s.playRef, s.playGen = ref, gen
	return ref == s.status.AdapterRef && gen == s.status.Generation, nil
}
func (s *providerCoreStub) StopIfSession(ref string, gen uint64) (bool, error) {
	s.stopRef, s.stopGen = ref, gen
	return ref == s.status.AdapterRef && gen == s.status.Generation, nil
}
func (s *providerCoreStub) SeekToIfSession(ref string, gen uint64, offset int) (bool, error) {
	s.seekRef, s.seekGen, s.seekOffset = ref, gen, offset
	return ref == s.status.AdapterRef && gen == s.status.Generation, nil
}
func (s *providerCoreStub) StartSessionIfSession(req core.SessionRequest, ref string, gen uint64) (bool, error) {
	s.startReq, s.startRef, s.startGen = req, ref, gen
	return s.startMatched, nil
}

func TestURLPlaybackBannerOwnsURLSession(t *testing.T) {
	coreStub := &providerCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc", Generation: 3, Duration: time.Minute}}
	a := &Adapter{core: coreStub}
	view, owns := a.PlaybackBanner(context.Background(), adapters.PlaybackBannerSnapshot{State: core.StatePlaying, Source: "url", AdapterRef: "url:abc", Generation: 3, Duration: time.Minute})
	if !owns {
		t.Fatal("URL provider did not own url snapshot")
	}
	if len(view.Actions) != 3 {
		t.Fatalf("actions = %#v, want pause/stop/replay", view.Actions)
	}
	if view.Seek == nil || !view.Seek.Enabled {
		t.Fatalf("seek = %#v, want enabled", view.Seek)
	}
}

func TestURLPlaybackActionUsesFullSessionKey(t *testing.T) {
	coreStub := &providerCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc", Generation: 3}}
	a := &Adapter{core: coreStub}
	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionPause, AdapterRef: "url:abc", Generation: 3})
	if err != nil {
		t.Fatalf("HandlePlaybackAction pause: %v", err)
	}
	if coreStub.pauseRef != "url:abc" || coreStub.pauseGen != 3 {
		t.Fatalf("pause key = %q/%d", coreStub.pauseRef, coreStub.pauseGen)
	}
}

func TestURLPlaybackActionRejectsStaleSameAdapterGeneration(t *testing.T) {
	coreStub := &providerCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc", Generation: 4}}
	a := &Adapter{core: coreStub}
	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionPause, AdapterRef: "url:abc", Generation: 3})
	if err == nil || !strings.Contains(err.Error(), "active session changed") {
		t.Fatalf("stale generation err = %v, want active session changed", err)
	}
	if coreStub.pauseRef != "url:abc" || coreStub.pauseGen != 3 {
		t.Fatalf("pause guard should receive stale key for recheck, got %q/%d", coreStub.pauseRef, coreStub.pauseGen)
	}
}

func TestURLPlaybackActionRejectsForeignAdapterRef(t *testing.T) {
	coreStub := &providerCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "streams:abc", Generation: 4}}
	a := &Adapter{core: coreStub}
	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionStop, AdapterRef: "url:abc", Generation: 4})
	if err == nil || !strings.Contains(err.Error(), "active session changed") {
		t.Fatalf("foreign adapter err = %v, want active session changed", err)
	}
	if coreStub.stopRef != "url:abc" || coreStub.stopGen != 4 {
		t.Fatalf("stop guard should receive submitted key for recheck, got %q/%d", coreStub.stopRef, coreStub.stopGen)
	}
}

func TestURLQuickCastRejectsDisabledAdapter(t *testing.T) {
	a := &Adapter{core: &providerCoreStub{}}
	_, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{Values: map[string]string{"url": "https://example.test/video.mp4"}})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled quick-cast err = %v, want disabled", err)
	}
}
