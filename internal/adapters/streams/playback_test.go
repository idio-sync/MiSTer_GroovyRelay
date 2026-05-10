package streams

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestStartResolvedStreamStartsCoreSession(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	resolver := &fakeResolver{res: &ytdlp.Resolution{
		URL:          "https://media.example/video.mp4",
		Headers:      map[string]string{"User-Agent": "test"},
		AudioURL:     "https://media.example/audio.mp4",
		AudioHeaders: map[string]string{"User-Agent": "audio"},
		Title:        "Clip",
	}}
	a.resolver = resolver
	started, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	req := core.lastReq
	if req.StreamURL != "https://media.example/video.mp4" ||
		req.AudioStreamURL != "https://media.example/audio.mp4" ||
		req.AdapterRef == "" ||
		!req.DirectPlay {
		t.Fatalf("SessionRequest = %+v", req)
	}
	if req.InputHeaders["User-Agent"] != "test" || req.AudioInputHeaders["User-Agent"] != "audio" {
		t.Fatalf("SessionRequest headers = video:%v audio:%v", req.InputHeaders, req.AudioInputHeaders)
	}
	if !req.Capabilities.CanPause || req.Capabilities.CanSeek {
		t.Fatalf("streams capabilities = %+v, want pause only", req.Capabilities)
	}
	if req.OnStop == nil {
		t.Fatal("streams sessions should install OnStop")
	}
	if !strings.HasPrefix(req.AdapterRef, "streams:mtv-rewind:metal:") {
		t.Fatalf("AdapterRef = %q", req.AdapterRef)
	}
	if started.AdapterRef != req.AdapterRef || started.ProviderID != "mtv-rewind" || started.ChannelID != "metal" {
		t.Fatalf("StartResult = %+v", started)
	}
	if resolver.calls != 1 || resolver.pageURLs[0] == "" {
		t.Fatalf("resolver calls = %d urls=%v", resolver.calls, resolver.pageURLs)
	}
}

func TestAdapterStopSerializesWithInFlightStart(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	fc.startHook = func(core.SessionRequest) {
		close(startEntered)
		<-releaseStart
	}

	startDone := make(chan error, 1)
	go func() {
		_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
		startDone <- err
	}()
	<-startEntered

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- a.Stop()
	}()

	select {
	case err := <-stopDone:
		t.Fatalf("Stop returned while StartSession was still in flight: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseStart)
	if err := <-startDone; err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := fc.Status().AdapterRef; got != "" {
		t.Fatalf("core AdapterRef after Stop = %q, want stopped", got)
	}
}

func TestStartResolvedStreamEmptyQueueFailsBeforeCoreSession(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	resolver := &fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4"}}
	a.resolver = resolver
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels:   []Channel{{ID: "empty", Name: "Empty", PlayMode: PlaySequential}},
	}})

	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "empty"})
	if err == nil {
		t.Fatal("empty queue should fail")
	}
	if core.startCalls != 0 {
		t.Fatalf("core start calls = %d, want 0", core.startCalls)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls = %d, want 0", resolver.calls)
	}
}

func TestStartResolvedStreamRedactsResolverError(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	rawURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	a.resolver = &fakeResolver{err: fmt.Errorf("yt-dlp failed for %s with token secret", rawURL)}

	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
	if err == nil {
		t.Fatal("StartResolvedStream should fail")
	}
	if strings.Contains(err.Error(), rawURL) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("StartResolvedStream leaked raw resolver error: %q", err.Error())
	}
}

func TestResolveFailureRecordsAndSkipsToNextItem(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels: []Channel{{
			ID:       "metal",
			Name:     "Metal",
			PlayMode: PlaySequential,
			Items: []StreamItem{
				{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
				{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
			},
		}},
	}})
	resolver := &fakeResolver{responses: []fakeResolveResponse{
		{err: fmt.Errorf("yt-dlp failed for https://www.youtube.com/watch?v=aaaaaaaaaaa with token secret")},
		{res: &ytdlp.Resolution{URL: "https://media.example/second.mp4"}},
	}}
	a.resolver = resolver

	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if resolver.calls != 2 {
		t.Fatalf("resolver calls = %d, want 2", resolver.calls)
	}
	if core.startCalls != 1 || core.lastReq.StreamURL != "https://media.example/second.mp4" {
		t.Fatalf("core start calls=%d req=%+v", core.startCalls, core.lastReq)
	}
	if a.active == nil || a.active.Index != 1 {
		t.Fatalf("active queue = %+v, want second item active", a.active)
	}
	if len(a.active.Failures) != 1 || a.active.Failures[0].ItemID != "aaaaaaaaaaa" {
		t.Fatalf("failures = %+v", a.active.Failures)
	}
	if strings.Contains(a.active.Failures[0].Reason, "aaaaaaaaaaa") || strings.Contains(a.active.Failures[0].Reason, "secret") {
		t.Fatalf("failure reason leaked resolver details: %q", a.active.Failures[0].Reason)
	}
}

func TestResolveFailureCapStopsAndClearsQueue(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.cfg.MaxConsecutiveFailures = 1
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels: []Channel{{
			ID:       "metal",
			Name:     "Metal",
			PlayMode: PlaySequential,
			Items: []StreamItem{
				{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
				{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
			},
		}},
	}})
	a.resolver = &fakeResolver{err: fmt.Errorf("yt-dlp failed for https://www.youtube.com/watch?v=aaaaaaaaaaa with token secret")}

	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
	if err == nil {
		t.Fatal("StartResolvedStream should fail when failure cap is reached")
	}
	if strings.Contains(err.Error(), "aaaaaaaaaaa") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("StartResolvedStream leaked resolver details: %q", err.Error())
	}
	if a.active != nil {
		t.Fatalf("active queue = %+v, want cleared", a.active)
	}
	if core.startCalls != 0 {
		t.Fatalf("core start calls = %d, want 0", core.startCalls)
	}
}

func TestResolveFailureStartSessionFailureSkipsToNextItem(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels: []Channel{{
			ID:       "metal",
			Name:     "Metal",
			PlayMode: PlaySequential,
			Items: []StreamItem{
				{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
				{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
			},
		}},
	}})
	a.resolver = &fakeResolver{responses: []fakeResolveResponse{
		{res: &ytdlp.Resolution{URL: "https://media.example/first.mp4"}},
		{res: &ytdlp.Resolution{URL: "https://media.example/second.mp4"}},
	}}
	core.startErrs = []error{fmt.Errorf("core failed for https://media.example/first.mp4 with token secret"), nil}

	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if core.startCalls != 2 || core.lastReq.StreamURL != "https://media.example/second.mp4" {
		t.Fatalf("core start calls=%d req=%+v", core.startCalls, core.lastReq)
	}
	if a.active == nil || a.active.Index != 1 {
		t.Fatalf("active queue = %+v, want second item active", a.active)
	}
	if len(a.active.Failures) != 1 || a.active.Failures[0].ItemID != "aaaaaaaaaaa" {
		t.Fatalf("failures = %+v", a.active.Failures)
	}
	if strings.Contains(a.active.Failures[0].Reason, "media.example") || strings.Contains(a.active.Failures[0].Reason, "secret") {
		t.Fatalf("failure reason leaked core details: %q", a.active.Failures[0].Reason)
	}
}

func TestStartResolvedStreamStaleInitialErrorDoesNotClearManualAdvancedQueue(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels: []Channel{{
			ID:       "metal",
			Name:     "Metal",
			PlayMode: PlaySequential,
			Items: []StreamItem{
				{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
				{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
			},
		}},
	}})
	resolver := newBlockedFirstResolver()
	a.resolver = resolver

	done := make(chan error, 1)
	go func() {
		_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
		done <- err
	}()

	<-resolver.entered
	if err := a.Next(t.Context()); err != nil {
		t.Fatalf("Next: %v", err)
	}
	resolver.releaseInitial()
	if err := <-done; err == nil {
		t.Fatal("initial StartResolvedStream should return stale resolve error")
	}
	if a.active == nil {
		t.Fatal("stale initial error cleared manually advanced queue")
	}
	if a.active.Generation != 1 || a.active.Index != 1 || a.active.Items[a.active.Index].ID != "bbbbbbbbbbb" {
		t.Fatalf("active queue = %+v, want manually advanced second item", a.active)
	}
}

func TestResolveFailureContinuationDoesNotStartNewerQueue(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:  "old-session",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		Items: []StreamItem{
			{ID: "old-1", SourceID: "old-1", URL: "https://www.youtube.com/watch?v=old11111111"},
			{ID: "old-2", SourceID: "old-2", URL: "https://www.youtube.com/watch?v=old22222222"},
		},
		loopMode: loopSequential,
	}
	newer := &ActiveQueue{
		SessionID:  "new-session",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		Generation: 7,
		ItemToken:  3,
		Items:      []StreamItem{{ID: "new", SourceID: "new", URL: "https://www.youtube.com/watch?v=new33333333"}},
	}
	a.resolver = &fakeResolver{responses: []fakeResolveResponse{
		{err: fmt.Errorf("first item failed")},
		{res: &ytdlp.Resolution{URL: "https://media.example/newer.mp4"}},
	}}
	a.beforeQueueContinuation = func() {
		a.beforeQueueContinuation = nil
		a.mu.Lock()
		a.active = newer
		a.mu.Unlock()
	}

	if _, err := a.playCurrent(t.Context()); err == nil {
		t.Fatal("stale resolve-failure continuation should report superseded start")
	}
	if core.startCalls != 0 {
		t.Fatalf("stale resolve-failure continuation started newer queue: start calls = %d", core.startCalls)
	}
	if a.active == nil || a.active.SessionID != "new-session" {
		t.Fatalf("newer queue was not preserved: %+v", a.active)
	}
}

func TestStartResolvedStreamReplacementFailureStopsPreviousOwnedSession(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:    "old-session",
		ProviderID:   "mtv-rewind",
		ProviderName: "MTV Rewind",
		ChannelID:    "metal",
		ChannelName:  "Metal",
		Items:        []StreamItem{{ID: "old", SourceID: "old"}},
		ItemToken:    1,
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)
	a.resolver = &fakeResolver{err: fmt.Errorf("resolver failed before replacement start")}

	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
	if err == nil {
		t.Fatal("StartResolvedStream replacement should fail")
	}
	if core.stopCalls != 1 {
		t.Fatalf("core stop calls = %d, want previous session stopped", core.stopCalls)
	}
	if got := core.Status().AdapterRef; got != "" {
		t.Fatalf("core AdapterRef after failed replacement = %q, want stopped", got)
	}
}

func TestManualNextFailureStopsPreviousOwnedSession(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:    "s1",
		ProviderID:   "mtv-rewind",
		ProviderName: "MTV Rewind",
		ChannelID:    "metal",
		ChannelName:  "Metal",
		Items: []StreamItem{
			{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
			{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
		},
		ItemToken: 1,
		loopMode:  loopNone,
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)
	a.resolver = &fakeResolver{err: fmt.Errorf("resolver failed before manual replacement start")}

	if err := a.Next(t.Context()); err == nil {
		t.Fatal("Next should fail when replacement resolve fails")
	}
	if core.stopCalls != 1 {
		t.Fatalf("core stop calls = %d, want previous session stopped", core.stopCalls)
	}
	if got := core.Status().AdapterRef; got != "" {
		t.Fatalf("core AdapterRef after failed manual replacement = %q, want stopped", got)
	}
}

func TestStopQueueDuringInFlightResolutionClearsAndPreventsLateStart(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	resolver := newBlockingSuccessResolver(&ytdlp.Resolution{URL: "https://media.example/late.mp4"})
	t.Cleanup(resolver.release)
	a.resolver = resolver

	done := make(chan error, 1)
	go func() {
		_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
		done <- err
	}()

	<-resolver.entered
	if err := a.StopQueue(t.Context()); err != nil {
		t.Fatalf("StopQueue during in-flight resolution: %v", err)
	}
	if a.active != nil {
		t.Fatalf("StopQueue should clear active queue during in-flight resolution, got %+v", a.active)
	}
	if core.stopCalls != 0 {
		t.Fatalf("guarded core stop calls = %d, want 0 without matching owner", core.stopCalls)
	}
	if core.rawStopCalls != 0 {
		t.Fatalf("raw core stop calls = %d, want 0", core.rawStopCalls)
	}

	resolver.release()
	if err := <-done; err == nil {
		t.Fatal("late StartResolvedStream should report superseded start after StopQueue")
	}
	if core.startCalls != 0 {
		t.Fatalf("late resolver result started core session: start calls = %d", core.startCalls)
	}
}

func TestOutOfCatalogItemBuildsAdhocQueue(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.resolver = &fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4"}}
	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ItemID: "abcdefghijk"})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if a.active.ChannelID != "adhoc" || a.active.ChannelName != "MTV Rewind Link" {
		t.Fatalf("active queue = %+v", a.active)
	}
	if len(a.active.Items) != 1 || a.active.Items[0].ID != "abcdefghijk" || a.active.loopMode != loopNone {
		t.Fatalf("adhoc queue items = %+v loopMode=%v", a.active.Items, a.active.loopMode)
	}
}

func TestReplayRebuildsChannelFromLatestCatalog(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:  "s1",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "old", SourceID: "old", URL: "https://www.youtube.com/watch?v=oldoldold01"}},
		loopMode:   loopSequential,
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels: []Channel{{
			ID:       "metal",
			Name:     "Metal Fresh",
			PlayMode: PlaySequential,
			Items: []StreamItem{{
				ID:       "freshfresh1",
				SourceID: "freshfresh1",
				URL:      "https://www.youtube.com/watch?v=freshfresh1",
			}},
		}},
	}})

	if err := a.Replay(t.Context()); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if a.active == nil || len(a.active.Items) != 1 || a.active.Items[0].ID != "freshfresh1" {
		t.Fatalf("active queue after replay = %+v, want latest catalog item", a.active)
	}
	if a.active.ChannelName != "Metal Fresh" {
		t.Fatalf("channel name = %q, want latest catalog name", a.active.ChannelName)
	}
}

func TestReplayEmptyLatestCatalogChannelDoesNotStopCurrentSession(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:  "s1",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "old", SourceID: "old", URL: "https://www.youtube.com/watch?v=oldoldold01"}},
		loopMode:   loopSequential,
	}
	activeRef := queueAdapterRef(a.active, a.active.ItemToken)
	core.status.AdapterRef = activeRef
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels:   []Channel{{ID: "metal", Name: "Metal Empty", PlayMode: PlaySequential}},
	}})

	if err := a.Replay(t.Context()); err == nil {
		t.Fatal("Replay should fail when the latest catalog channel has no playable items")
	}
	if core.stopCalls != 0 {
		t.Fatalf("core stop calls = %d, want 0", core.stopCalls)
	}
	if got := core.Status().AdapterRef; got != activeRef {
		t.Fatalf("core AdapterRef = %q, want %q", got, activeRef)
	}
	if a.active == nil || a.active.SessionID != "s1" || a.active.Items[0].ID != "old" {
		t.Fatalf("active queue was replaced or cleared: %+v", a.active)
	}
}

func TestReplayDoesNotReplaceNewerSameChannelQueue(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	old := &ActiveQueue{
		SessionID:  "old-session",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "old", SourceID: "old", URL: "https://www.youtube.com/watch?v=oldoldold01"}},
		loopMode:   loopSequential,
	}
	newer := &ActiveQueue{
		SessionID:  "new-session",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "new", SourceID: "new", URL: "https://www.youtube.com/watch?v=newnewnew01"}},
		loopMode:   loopSequential,
	}
	a.active = old
	core.status.AdapterRef = queueAdapterRef(old, old.ItemToken)
	a.beforeReplayReplace = func() {
		a.mu.Lock()
		a.active = newer
		a.mu.Unlock()
		core.mu.Lock()
		core.status.AdapterRef = queueAdapterRef(newer, newer.ItemToken)
		core.mu.Unlock()
	}

	if err := a.Replay(t.Context()); err == nil {
		t.Fatal("Replay should fail when a newer same-channel queue replaces the captured queue")
	}
	if a.active == nil || a.active.SessionID != "new-session" {
		t.Fatalf("newer queue was not preserved: %+v", a.active)
	}
	if a.active.Items[0].ID != "new" {
		t.Fatalf("newer queue item = %+v", a.active.Items)
	}
}

type blockedFirstResolver struct {
	entered chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls int
}

func newBlockedFirstResolver() *blockedFirstResolver {
	return &blockedFirstResolver{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *blockedFirstResolver) Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*ytdlp.Resolution, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()

	if call == 1 {
		close(r.entered)
		<-r.release
		return nil, fmt.Errorf("late resolver failure")
	}
	return &ytdlp.Resolution{URL: "https://media.example/second.mp4"}, nil
}

func (r *blockedFirstResolver) releaseInitial() {
	close(r.release)
}

type blockingSuccessResolver struct {
	entered     chan struct{}
	releaseCh   chan struct{}
	res         *ytdlp.Resolution
	enterOnce   sync.Once
	releaseOnce sync.Once
}

func newBlockingSuccessResolver(res *ytdlp.Resolution) *blockingSuccessResolver {
	return &blockingSuccessResolver{
		entered:   make(chan struct{}),
		releaseCh: make(chan struct{}),
		res:       res,
	}
}

func (r *blockingSuccessResolver) Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*ytdlp.Resolution, error) {
	r.enterOnce.Do(func() { close(r.entered) })
	select {
	case <-r.releaseCh:
		return r.res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *blockingSuccessResolver) release() {
	r.releaseOnce.Do(func() { close(r.releaseCh) })
}
