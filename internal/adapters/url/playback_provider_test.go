package url

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
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
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("stale generation err = %v, want stale-session sentinel", err)
	}
	if coreStub.pauseRef != "url:abc" || coreStub.pauseGen != 3 {
		t.Fatalf("pause guard should receive stale key for recheck, got %q/%d", coreStub.pauseRef, coreStub.pauseGen)
	}
}

func TestURLPlaybackActionRejectsForeignAdapterRef(t *testing.T) {
	coreStub := &providerCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "streams:abc", Generation: 4}}
	a := &Adapter{core: coreStub}
	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionStop, AdapterRef: "url:abc", Generation: 4})
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("foreign adapter err = %v, want stale-session sentinel", err)
	}
	if coreStub.stopRef != "url:abc" || coreStub.stopGen != 4 {
		t.Fatalf("stop guard should receive submitted key for recheck, got %q/%d", coreStub.stopRef, coreStub.stopGen)
	}
}

func TestURLPlaybackActionRejectsUnsupportedActionWithSentinel(t *testing.T) {
	a := &Adapter{core: &providerCoreStub{}}
	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionPrevious, AdapterRef: "url:abc", Generation: 4})
	if !errors.Is(err, adapters.ErrPlaybackActionUnsupported) {
		t.Fatalf("unsupported action err = %v, want unsupported-action sentinel", err)
	}
	const want = "unknown playback action \"previous\""
	if err.Error() != want {
		t.Fatalf("unsupported action message = %q, want %q", err.Error(), want)
	}
}

func TestURLPlaybackResumeGuardedCastStaleErrorUsesSentinel(t *testing.T) {
	coreStub := &providerCoreStub{status: core.SessionStatus{State: core.StateIdle, AdapterRef: "url:old", Generation: 4}}
	a := &Adapter{core: coreStub, history: LoadHistory("")}
	a.mu.Lock()
	a.lastURL = "https://example.test/video.mp4"
	a.mu.Unlock()

	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionResume, AdapterRef: "url:old", Generation: 4})
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("resume stale guarded cast err = %v, want stale-session sentinel", err)
	}
	if err.Error() != adapters.ErrActiveSessionChangedMessage {
		t.Fatalf("resume stale guarded cast message = %q, want %q", err.Error(), adapters.ErrActiveSessionChangedMessage)
	}
}

func TestURLPlaybackReplayGuardedCastStaleErrorUsesSentinel(t *testing.T) {
	coreStub := &providerCoreStub{status: core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:old", Generation: 4}}
	a := &Adapter{core: coreStub, history: LoadHistory("")}
	a.mu.Lock()
	a.lastURL = "https://example.test/video.mp4"
	a.mu.Unlock()

	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionReplay, AdapterRef: "url:old", Generation: 4})
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("replay stale guarded cast err = %v, want stale-session sentinel", err)
	}
	if err.Error() != adapters.ErrActiveSessionChangedMessage {
		t.Fatalf("replay stale guarded cast message = %q, want %q", err.Error(), adapters.ErrActiveSessionChangedMessage)
	}
}

func TestURLQuickCastRejectsDisabledAdapter(t *testing.T) {
	a := &Adapter{core: &providerCoreStub{}}
	_, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{Values: map[string]string{"url": "https://example.test/video.mp4"}})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled quick-cast err = %v, want disabled", err)
	}
}

func TestHandleQuickCast_WrapsDisabledAsQuickCastErrorBlocked(t *testing.T) {
	t.Parallel()
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(false)
	_, err = a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{
		TabID:  "url",
		Values: map[string]string{"url": "https://example.test/video.mp4"},
	})
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusConflict {
		t.Errorf("Status = %d, want 409", qerr.Status)
	}
	if qerr.Chip != "BLOCKED" {
		t.Errorf("Chip = %q, want BLOCKED", qerr.Chip)
	}
}

func TestHandleQuickCast_WrapsParseFailureAsBadURL(t *testing.T) {
	t.Parallel()
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	_, err = a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{
		TabID:  "url",
		Values: map[string]string{"url": ""},
	})
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", qerr.Status)
	}
	if qerr.Chip != "BAD URL" {
		t.Errorf("Chip = %q, want BAD URL", qerr.Chip)
	}
}

func TestHandleQuickCast_PreservesPlainErrorMessage(t *testing.T) {
	t.Parallel()
	// /ui consumers still call err.Error() so the human-readable message
	// must be preserved through the wrap.
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	_, err = a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{
		TabID:  "url",
		Values: map[string]string{"url": ""},
	})
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if got := err.Error(); !strings.Contains(strings.ToLower(got), "url") {
		t.Errorf("Error() = %q, want a message mentioning url", got)
	}
}
