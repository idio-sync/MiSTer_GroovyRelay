package streams

import (
	"context"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
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

func TestStreamsPlaybackActionRejectsStaleCoreGeneration(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.mu.Lock()
	a.active = &ActiveQueue{SessionID: "sess", ProviderID: "mtv", ProviderName: "MTV", ChannelID: "metal", ChannelName: "Metal", Items: []StreamItem{{ID: "one"}, {ID: "two"}}, baseItems: []StreamItem{{ID: "one"}, {ID: "two"}}, Index: 0, Generation: 4, ItemToken: 2}
	a.mu.Unlock()
	ref := "streams:mtv:metal:sess:2"
	fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: ref, Generation: 9}

	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionStop, AdapterRef: ref, Generation: 8})
	if err == nil || !strings.Contains(err.Error(), "active session changed") {
		t.Fatalf("stale streams action err = %v, want active session changed", err)
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
	if err == nil || !strings.Contains(err.Error(), "active session changed") {
		t.Fatalf("foreign streams action err = %v, want active session changed", err)
	}
	if fc.stopIfSessionCalls != 0 {
		t.Fatalf("foreign streams action called core stop %d times", fc.stopIfSessionCalls)
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

func TestStreamsPlaybackActionNextUsesGuardedStopAndIdleStart(t *testing.T) {
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
	if fc.stopIfSessionCalls != 1 || fc.stopIfSessionRef != ref || fc.stopIfSessionGen != 8 {
		t.Fatalf("StopIfSession calls=%d key=%q/%d", fc.stopIfSessionCalls, fc.stopIfSessionRef, fc.stopIfSessionGen)
	}
	if fc.startIdleCalls != 1 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 1", fc.startIdleCalls)
	}
	if a.active == nil || a.active.Index != 1 {
		t.Fatalf("active queue after next = %+v, want second item", a.active)
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
	if err == nil || !strings.Contains(err.Error(), "active session changed") {
		t.Fatalf("stale race err = %v, want active session changed", err)
	}
	if fc.stopIfSessionCalls != 1 || fc.startIdleCalls != 0 {
		t.Fatalf("core calls stopIf=%d startIdle=%d, want stop guard only", fc.stopIfSessionCalls, fc.startIdleCalls)
	}
	if a.active == nil || a.active.Generation != 4 || a.active.Index != 0 {
		t.Fatalf("active queue after stale race = %+v, want original generation/index", a.active)
	}
}

func TestStreamsPlaybackActionNextDoesNotMutateNewerQueueAfterMatchedStop(t *testing.T) {
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
	fc.stopHook = func() {
		a.mu.Lock()
		a.active = newer
		a.mu.Unlock()
		fc.mu.Lock()
		fc.status = core.SessionStatus{State: core.StatePlaying, AdapterRef: newerRef, Generation: 12}
		fc.mu.Unlock()
	}

	_, err := a.HandlePlaybackAction(context.Background(), adapters.PlaybackActionRequest{Action: adapters.PlaybackActionNext, AdapterRef: oldRef, Generation: 8})
	if err == nil || !strings.Contains(err.Error(), "active session changed") {
		t.Fatalf("newer queue race err = %v, want active session changed", err)
	}
	if fc.stopIfSessionCalls != 1 || fc.startIdleCalls != 0 {
		t.Fatalf("core calls stopIf=%d startIdle=%d, want matched stop without new start", fc.stopIfSessionCalls, fc.startIdleCalls)
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
