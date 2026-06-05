package streams

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestStreamsPlaybackBannerActions(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.mu.Lock()
	a.active = &ActiveQueue{
		SessionID:    "sess",
		ProviderID:   "mtv",
		ProviderName: "MTV",
		ChannelID:    "metal",
		ChannelName:  "Metal",
		Items:        []StreamItem{{ID: "one", SourceID: "one", Title: "One"}, {ID: "two", SourceID: "two", Title: "Two"}},
		baseItems:    []StreamItem{{ID: "one", SourceID: "one", Title: "One"}, {ID: "two", SourceID: "two", Title: "Two"}},
		Index:        0,
		Generation:   4,
		ItemToken:    2,
		loopMode:     loopSequential,
	}
	a.mu.Unlock()
	ref := "streams:mtv:metal:sess:2"

	view, owns := a.PlaybackBanner(context.Background(), adapters.PlaybackBannerSnapshot{
		State:      core.StatePlaying,
		Source:     "streams",
		AdapterRef: ref,
		Generation: 8,
	})
	if !owns {
		t.Fatal("streams provider did not own streams snapshot")
	}
	got := actionIDs(view.Actions)
	for _, want := range []string{adapters.PlaybackActionNext, adapters.PlaybackActionReplay, adapters.PlaybackActionStop} {
		if !strings.Contains(got, want) {
			t.Fatalf("actions %q missing %q", got, want)
		}
	}
}

// TestStreamsPlaybackBannerSurrenderOwnershipWhenQueueMismatched verifies
// that the streams provider returns owns=false when its source claim
// matches but the active queue does not — e.g. queue cleared, or a
// different streams session was preempted in. The semantics of owns=true
// is "this adapter owns the supplied session"; conflating it with "I
// claim this source name" would let the UI surface streams-specific
// enrichment for a session the adapter no longer controls.
func TestStreamsPlaybackBannerSurrenderOwnershipWhenQueueMismatched(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	// No active queue.
	view, owns := a.PlaybackBanner(context.Background(), adapters.PlaybackBannerSnapshot{
		State:      core.StatePlaying,
		Source:     "streams",
		AdapterRef: "streams:mtv:metal:sess:2",
		Generation: 8,
	})
	if owns {
		t.Fatalf("streams provider should not own snapshot when active queue is nil, view=%+v", view)
	}
	if view.SourceDisplay != "" || len(view.Actions) != 0 {
		t.Fatalf("expected empty view when surrendering ownership, got %+v", view)
	}

	// Different streams session active — ref mismatch.
	a.mu.Lock()
	a.active = &ActiveQueue{
		SessionID:  "other",
		ProviderID: "mtv",
		ChannelID:  "rock",
		Items:      []StreamItem{{ID: "x"}},
		baseItems:  []StreamItem{{ID: "x"}},
	}
	a.mu.Unlock()
	view, owns = a.PlaybackBanner(context.Background(), adapters.PlaybackBannerSnapshot{
		State:      core.StatePlaying,
		Source:     "streams",
		AdapterRef: "streams:mtv:metal:sess:2",
		Generation: 8,
	})
	if owns {
		t.Fatalf("streams provider should not own snapshot when ref mismatched, view=%+v", view)
	}
}

func TestStreamsPlaybackActionRejectsStaleCoreGeneration(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.mu.Lock()
	a.active = &ActiveQueue{SessionID: "sess", ProviderID: "mtv", ProviderName: "MTV", ChannelID: "metal", ChannelName: "Metal", Items: []StreamItem{{ID: "one"}, {ID: "two"}}, baseItems: []StreamItem{{ID: "one"}, {ID: "two"}}, Index: 0, Generation: 4, ItemToken: 2}
	a.mu.Unlock()
	ref := "streams:mtv:metal:sess:2"
	fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: ref, Generation: 9}

	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionStop, AdapterRef: ref, Generation: 8})
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("stale streams action err = %v, want stale-session sentinel", err)
	}
	if fc.stopIfSessionCalls != 0 {
		t.Fatalf("stale streams action called core stop %d times", fc.stopIfSessionCalls)
	}
}

func TestStreamsPlaybackActionRejectsForeignAdapterRef(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.mu.Lock()
	a.active = &ActiveQueue{SessionID: "sess", ProviderID: "mtv", ChannelID: "metal", Items: []StreamItem{{ID: "one"}}, Index: 0, Generation: 4, ItemToken: 2}
	a.mu.Unlock()
	fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc", Generation: 8}

	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionStop, AdapterRef: "streams:mtv:metal:sess:2", Generation: 8})
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("foreign streams action err = %v, want stale-session sentinel", err)
	}
	if fc.stopIfSessionCalls != 0 {
		t.Fatalf("foreign streams action called core stop %d times", fc.stopIfSessionCalls)
	}
}

func TestStreamsPlaybackActionRejectsUnsupportedActionWithSentinel(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.mu.Lock()
	a.active = &ActiveQueue{SessionID: "sess", ProviderID: "mtv", ChannelID: "metal", Items: []StreamItem{{ID: "one"}}, Index: 0, Generation: 4, ItemToken: 2}
	a.mu.Unlock()
	ref := "streams:mtv:metal:sess:2"
	fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: ref, Generation: 8}

	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionPause, AdapterRef: ref, Generation: 8})
	if !errors.Is(err, adapters.ErrPlaybackActionUnsupported) {
		t.Fatalf("unsupported streams action err = %v, want unsupported-action sentinel", err)
	}
	const want = "unknown playback action \"pause\""
	if err.Error() != want {
		t.Fatalf("unsupported streams action message = %q, want %q", err.Error(), want)
	}
}

func TestStreamsPlaybackActionUnavailableControlsUseUnsupportedSentinel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
		queue  ActiveQueue
	}{
		{
			name:   "previous unavailable at first item",
			action: adapters.PlaybackActionPrevious,
			queue: ActiveQueue{
				SessionID:  "sess",
				ProviderID: "mtv",
				ChannelID:  "metal",
				Items:      []StreamItem{{ID: "one"}},
				baseItems:  []StreamItem{{ID: "one"}},
				Index:      0,
				ItemToken:  2,
				loopMode:   loopNone,
			},
		},
		{
			name:   "next unavailable at last item",
			action: adapters.PlaybackActionNext,
			queue: ActiveQueue{
				SessionID:  "sess",
				ProviderID: "mtv",
				ChannelID:  "metal",
				Items:      []StreamItem{{ID: "one"}},
				baseItems:  []StreamItem{{ID: "one"}},
				Index:      0,
				ItemToken:  2,
				loopMode:   loopNone,
			},
		},
		{
			name:   "replay unavailable when provider catalog missing",
			action: adapters.PlaybackActionReplay,
			queue: ActiveQueue{
				SessionID:  "sess",
				ProviderID: "missing",
				ChannelID:  "metal",
				Items:      []StreamItem{{ID: "one"}},
				baseItems:  []StreamItem{{ID: "one"}},
				Index:      0,
				ItemToken:  2,
				loopMode:   loopSequential,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, fc := newTestAdapterWithFakeCore(t)
			a.mu.Lock()
			a.active = &tc.queue
			a.mu.Unlock()
			ref := activeAdapterRef(&tc.queue)
			fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: ref, Generation: 8}

			_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: tc.action, AdapterRef: ref, Generation: 8})
			if !errors.Is(err, adapters.ErrPlaybackActionUnsupported) {
				t.Fatalf("unavailable streams %s err = %v, want unsupported-action sentinel", tc.action, err)
			}
			if err == nil || err.Error() == adapters.ErrPlaybackActionUnsupported.Error() {
				t.Fatalf("unavailable streams %s should preserve provider message, got %v", tc.action, err)
			}
		})
	}
}

func TestStreamsPlaybackActionStopUsesFullSessionKey(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.mu.Lock()
	a.active = &ActiveQueue{SessionID: "sess", ProviderID: "mtv", ChannelID: "metal", Items: []StreamItem{{ID: "one"}}, Index: 0, Generation: 4, ItemToken: 2}
	a.mu.Unlock()
	ref := "streams:mtv:metal:sess:2"
	fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: ref, Generation: 8}

	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionStop, AdapterRef: ref, Generation: 8})
	if err != nil {
		t.Fatalf("HandlePlaybackAction stop: %v", err)
	}
	if fc.stopIfSessionCalls != 1 || fc.stopIfSessionRef != ref || fc.stopIfSessionGen != 8 {
		t.Fatalf("StopIfSession calls=%d key=%q/%d", fc.stopIfSessionCalls, fc.stopIfSessionRef, fc.stopIfSessionGen)
	}
	if a.active != nil {
		t.Fatalf("active queue after stop = %+v, want nil", a.active)
	}
}

func TestStreamsPlaybackActionNextUsesGuardedReplacementStart(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.mu.Lock()
	a.active = &ActiveQueue{
		SessionID:  "sess",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		Items: []StreamItem{
			{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
			{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
		},
		baseItems: []StreamItem{
			{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
			{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
		},
		Index:     0,
		ItemToken: 2,
		loopMode:  loopSequential,
	}
	a.mu.Unlock()
	ref := "streams:mtv-rewind:metal:sess:2"
	fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: ref, Generation: 8}

	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionNext, AdapterRef: ref, Generation: 8})
	if err != nil {
		t.Fatalf("HandlePlaybackAction next: %v", err)
	}
	if fc.stopIfSessionCalls != 0 {
		t.Fatalf("StopIfSession calls=%d, want 0", fc.stopIfSessionCalls)
	}
	if fc.startIfCalls != 1 || fc.startIfRef != ref || fc.startIfGen != 8 {
		t.Fatalf("StartSessionIfSession calls=%d key=%q/%d", fc.startIfCalls, fc.startIfRef, fc.startIfGen)
	}
	if fc.startIdleCalls != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0", fc.startIdleCalls)
	}
	if a.active == nil || a.active.Index != 1 {
		t.Fatalf("active queue after next = %+v, want second item", a.active)
	}
}

func TestStreamsPlaybackActionNextResolveFailureKeepsCurrentSession(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.cfg.MaxConsecutiveFailures = 1
	a.mu.Lock()
	a.active = &ActiveQueue{
		SessionID:  "sess",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		Generation: 4,
		Items: []StreamItem{
			{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
			{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
		},
		baseItems: []StreamItem{
			{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
			{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
		},
		Index:     0,
		ItemToken: 2,
		loopMode:  loopSequential,
	}
	a.mu.Unlock()
	ref := "streams:mtv-rewind:metal:sess:2"
	fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: ref, Generation: 8}
	a.resolver = &fakeResolver{err: errors.New("yt-dlp could not resolve next item")}

	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{
		Action:     adapters.PlaybackActionNext,
		AdapterRef: ref,
		Generation: 8,
	})
	if err == nil {
		t.Fatal("HandlePlaybackAction next should report the resolve failure")
	}
	if fc.stopIfSessionCalls != 0 {
		t.Fatalf("StopIfSession calls = %d, want 0 so the current stream keeps playing", fc.stopIfSessionCalls)
	}
	if got := fc.Status().AdapterRef; got != ref {
		t.Fatalf("core AdapterRef after failed next = %q, want current %q", got, ref)
	}
	if a.active == nil || a.active.Index != 0 || a.active.Generation != 4 || activeAdapterRef(a.active) != ref {
		t.Fatalf("active queue after failed next = %+v, want original current queue", a.active)
	}
}

func TestStreamsPlaybackActionNextDoesNotMutateQueueOnStaleGenerationRace(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.mu.Lock()
	a.active = &ActiveQueue{
		SessionID:  "sess",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		Generation: 4,
		Items: []StreamItem{
			{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
			{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
		},
		baseItems: []StreamItem{
			{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
			{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
		},
		Index:     0,
		ItemToken: 2,
		loopMode:  loopSequential,
	}
	a.mu.Unlock()
	ref := "streams:mtv-rewind:metal:sess:2"
	fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: ref, Generation: 8}
	fc.statusHook = func() {
		fc.mu.Lock()
		fc.statusHook = nil
		fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: ref, Generation: 9}
		fc.mu.Unlock()
	}

	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionNext, AdapterRef: ref, Generation: 8})
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("stale race err = %v, want stale-session sentinel", err)
	}
	if fc.stopIfSessionCalls != 0 || fc.startIfCalls != 0 || fc.startIdleCalls != 0 {
		t.Fatalf("core calls stopIf=%d startIf=%d startIdle=%d, want no core mutation", fc.stopIfSessionCalls, fc.startIfCalls, fc.startIdleCalls)
	}
	if a.active == nil || a.active.Generation != 4 || a.active.Index != 0 {
		t.Fatalf("active queue after stale race = %+v, want original generation/index", a.active)
	}
}

func TestStreamsPlaybackActionNextDoesNotRestoreOverNewerQueue(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.mu.Lock()
	a.active = &ActiveQueue{
		SessionID:  "old-sess",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		Generation: 4,
		Items: []StreamItem{
			{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
			{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
		},
		baseItems: []StreamItem{
			{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
			{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
		},
		Index:     0,
		ItemToken: 2,
		loopMode:  loopSequential,
	}
	a.mu.Unlock()
	oldRef := "streams:mtv-rewind:metal:old-sess:2"
	newer := &ActiveQueue{
		SessionID:  "new-sess",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		Generation: 1,
		Items: []StreamItem{
			{ID: "ccccccccccc", SourceID: "ccccccccccc", URL: "https://www.youtube.com/watch?v=ccccccccccc"},
			{ID: "ddddddddddd", SourceID: "ddddddddddd", URL: "https://www.youtube.com/watch?v=ddddddddddd"},
		},
		baseItems: []StreamItem{
			{ID: "ccccccccccc", SourceID: "ccccccccccc", URL: "https://www.youtube.com/watch?v=ccccccccccc"},
			{ID: "ddddddddddd", SourceID: "ddddddddddd", URL: "https://www.youtube.com/watch?v=ddddddddddd"},
		},
		Index:     0,
		ItemToken: 1,
		loopMode:  loopSequential,
	}
	newerRef := "streams:mtv-rewind:metal:new-sess:1"
	fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: oldRef, Generation: 8}
	resolver := newBlockingSuccessResolver(&ytdlp.Resolution{URL: "https://media.example/replacement.mp4"})
	t.Cleanup(resolver.release)
	a.resolver = resolver

	done := make(chan error, 1)
	go func() {
		_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionNext, AdapterRef: oldRef, Generation: 8})
		done <- err
	}()

	<-resolver.entered
	a.mu.Lock()
	a.active = newer
	a.mu.Unlock()
	fc.mu.Lock()
	fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: newerRef, Generation: 12}
	fc.mu.Unlock()
	resolver.release()

	err := <-done
	if !errors.Is(err, adapters.ErrActiveSessionChanged) {
		t.Fatalf("newer queue race err = %v, want stale-session sentinel", err)
	}
	if fc.stopIfSessionCalls != 0 || fc.startCalls != 0 {
		t.Fatalf("core calls stopIf=%d start=%d, want no core mutation", fc.stopIfSessionCalls, fc.startCalls)
	}
	if a.active != newer || newer.Index != 0 || newer.Generation != 1 {
		t.Fatalf("newer queue after race = %+v, want untouched newer queue", a.active)
	}
}

func actionIDs(actions []adapters.PlaybackAction) string {
	var ids []string
	for _, a := range actions {
		ids = append(ids, a.ID)
	}
	return strings.Join(ids, ",")
}
