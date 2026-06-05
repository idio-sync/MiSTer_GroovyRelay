package streams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
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
	if req.Source != "streams" || req.Title != "Clip" {
		t.Fatalf("SessionRequest source/title = %q/%q, want streams/Clip", req.Source, req.Title)
	}
	if req.AspectMode != "zoom" {
		t.Fatalf("SessionRequest AspectMode = %q, want zoom for MTV Rewind", req.AspectMode)
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
	item, ok := a.active.currentItem()
	if !ok || item.Title != "Clip" {
		t.Fatalf("active item title = %+v, want Clip", item)
	}
}

func TestStartResolvedStreamLogsStartupTiming(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	a, _ := newTestAdapterWithFakeCore(t)

	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}

	got := logs.String()
	for _, want := range []string{
		"streams playback started",
		"provider=mtv-rewind",
		"channel=metal",
		"item=dQw4w9WgXcQ",
		"resolve_ms=",
		"core_start_ms=",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("startup log missing %q:\n%s", want, got)
		}
	}
}

func TestStartResolvedStreamCartoonRewindUsesZoomAspectOverride(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "cartoon-rewind", ChannelID: "heman"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if core.lastReq.AspectMode != "zoom" {
		t.Fatalf("SessionRequest AspectMode = %q, want zoom for Cartoon Rewind", core.lastReq.AspectMode)
	}
}

func TestStartResolvedStreamIfSessionUsesCoreGenerationGuard(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	fc.status = core.SessionStatus{AdapterRef: "url:https://example.test/watch", Generation: 7}
	a.resolver = &fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4"}}

	started, matched, err := a.StartResolvedStreamIfSession(
		t.Context(),
		streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"},
		"url:https://example.test/watch",
		7,
	)
	if err != nil {
		t.Fatalf("StartResolvedStreamIfSession: %v", err)
	}
	if !matched {
		t.Fatal("StartResolvedStreamIfSession matched = false, want true")
	}
	if fc.startIfCalls != 1 || fc.startIfRef != "url:https://example.test/watch" || fc.startIfGen != 7 {
		t.Fatalf("guard calls=%d ref=%q gen=%d", fc.startIfCalls, fc.startIfRef, fc.startIfGen)
	}
	if fc.lastReq.AdapterRef == "" || fc.lastReq.AdapterRef != started.AdapterRef {
		t.Fatalf("last request AdapterRef=%q started=%+v", fc.lastReq.AdapterRef, started)
	}
}

func TestStartResolvedStreamIfSessionStaleGuardDoesNotStopCurrentStream(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:  "streams-session",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "existing", SourceID: "existing", URL: "https://www.youtube.com/watch?v=existing001"}},
	}
	existingRef := queueAdapterRef(a.active, a.active.ItemToken)
	fc.status = core.SessionStatus{AdapterRef: existingRef, Generation: 11}
	a.resolver = &fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4"}}

	started, matched, err := a.StartResolvedStreamIfSession(
		t.Context(),
		streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"},
		"url:https://example.test/old",
		7,
	)
	if err != nil {
		t.Fatalf("StartResolvedStreamIfSession: %v", err)
	}
	if matched {
		t.Fatal("StartResolvedStreamIfSession matched = true, want false")
	}
	if started != (streamhandoff.StartResult{}) {
		t.Fatalf("StartResult = %+v, want zero", started)
	}
	if fc.stopCalls != 0 || fc.startIfCalls != 0 || fc.startCalls != 0 {
		t.Fatalf("core calls stop=%d startIf=%d start=%d, want no mutation", fc.stopCalls, fc.startIfCalls, fc.startCalls)
	}
	if a.active == nil || a.active.SessionID != "streams-session" {
		t.Fatalf("active queue = %+v, want existing queue preserved", a.active)
	}
	if got := fc.Status().AdapterRef; got != existingRef {
		t.Fatalf("core AdapterRef = %q, want existing %q", got, existingRef)
	}
}

func TestStartResolvedStreamIfSessionRaceDoesNotStopNewerStream(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:  "streams-session",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "existing", SourceID: "existing", URL: "https://www.youtube.com/watch?v=existing001"}},
	}
	existingQueue := a.active
	existingRef := queueAdapterRef(existingQueue, existingQueue.ItemToken)
	fc.status = core.SessionStatus{AdapterRef: "url:https://example.test/watch", Generation: 7}
	fc.statusHook = func() {
		fc.mu.Lock()
		fc.statusHook = nil
		fc.status = core.SessionStatus{AdapterRef: existingRef, Generation: 11}
		fc.mu.Unlock()
	}
	a.resolver = &fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4"}}

	started, matched, err := a.StartResolvedStreamIfSession(
		t.Context(),
		streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"},
		"url:https://example.test/watch",
		7,
	)
	if err != nil {
		t.Fatalf("StartResolvedStreamIfSession: %v", err)
	}
	if matched {
		t.Fatal("StartResolvedStreamIfSession matched = true, want false")
	}
	if started != (streamhandoff.StartResult{}) {
		t.Fatalf("StartResult = %+v, want zero", started)
	}
	if fc.stopCalls != 0 || fc.startIfCalls != 1 || fc.startCalls != 0 {
		t.Fatalf("core calls stop=%d startIf=%d start=%d, want guarded miss without stop", fc.stopCalls, fc.startIfCalls, fc.startCalls)
	}
	if a.active != existingQueue {
		t.Fatalf("active queue = %+v, want existing queue preserved", a.active)
	}
	if got := fc.Status().AdapterRef; got != existingRef {
		t.Fatalf("core AdapterRef = %q, want existing %q", got, existingRef)
	}
}

func TestStartResolvedStreamIfSessionRaceRestoresPreviousQueueOnResolveError(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:  "streams-session",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "existing", SourceID: "existing", URL: "https://www.youtube.com/watch?v=existing001"}},
	}
	existingQueue := a.active
	existingRef := queueAdapterRef(existingQueue, existingQueue.ItemToken)
	fc.status = core.SessionStatus{AdapterRef: "url:https://example.test/watch", Generation: 7}
	fc.statusHook = func() {
		fc.mu.Lock()
		fc.statusHook = nil
		fc.status = core.SessionStatus{AdapterRef: existingRef, Generation: 11}
		fc.mu.Unlock()
	}
	a.resolver = &fakeResolver{err: fmt.Errorf("resolver failed")}

	_, matched, err := a.StartResolvedStreamIfSession(
		t.Context(),
		streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"},
		"url:https://example.test/watch",
		7,
	)
	if err == nil {
		t.Fatal("StartResolvedStreamIfSession err = nil, want resolver error")
	}
	if matched {
		t.Fatal("StartResolvedStreamIfSession matched = true, want false")
	}
	if fc.stopCalls != 0 || fc.startIfCalls != 0 || fc.startCalls != 0 {
		t.Fatalf("core calls stop=%d startIf=%d start=%d, want no core mutation", fc.stopCalls, fc.startIfCalls, fc.startCalls)
	}
	if a.active != existingQueue {
		t.Fatalf("active queue = %+v, want existing queue preserved", a.active)
	}
	if got := fc.Status().AdapterRef; got != existingRef {
		t.Fatalf("core AdapterRef = %q, want existing %q", got, existingRef)
	}
}

func TestStartResolvedDirectStreamSkipsResolverAndSetsPolicy(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	a.bridge.HLSBuffer.Enabled = false
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	resolver := &fakeResolver{res: &ytdlp.Resolution{
		URL:     "https://media.example/should-not-be-used.m3u8",
		Headers: map[string]string{"User-Agent": "yt-dlp"},
	}}
	a.resolver = resolver

	_, err = a.StartResolvedStream(t.Context(), streamhandoff.Resolution{
		ProviderID: "toonami-aftermath",
		ChannelID:  "east",
	})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls = %d, want 0", resolver.calls)
	}
	req := c.lastReq
	if req.StreamURL != "http://api.toonamiaftermath.com:3000/est/playlist.m3u8" {
		t.Fatalf("StreamURL = %q", req.StreamURL)
	}
	if req.Title != "Toonami Aftermath / East" {
		t.Fatalf("Title = %q, want Toonami Aftermath / East", req.Title)
	}
	if req.AspectMode != "" {
		t.Fatalf("AspectMode = %q, want bridge default for non-Rewind stream", req.AspectMode)
	}
	if len(req.InputHeaders) != 0 || len(req.AudioInputHeaders) != 0 {
		t.Fatalf("headers leaked into direct request: video=%v audio=%v", req.InputHeaders, req.AudioInputHeaders)
	}
	if req.Capabilities.CanPause || req.Capabilities.CanSeek {
		t.Fatalf("capabilities = %+v, want no pause/seek", req.Capabilities)
	}
	wantProtocols := "file,http,https,tcp,tls,crypto"
	if got := strings.Join(req.MediaInputPolicy.ProtocolWhitelist, ","); got != wantProtocols {
		t.Fatalf("ProtocolWhitelist = %q, want %q", got, wantProtocols)
	}
	if !req.MediaInputPolicy.DisableRedirects || !req.MediaInputPolicy.DisableReconnect {
		t.Fatalf("redirect/reconnect policy = %+v", req.MediaInputPolicy)
	}
	if req.MediaInputPolicy.RWTimeout != 5*time.Second {
		t.Fatalf("RWTimeout = %s, want 5s", req.MediaInputPolicy.RWTimeout)
	}
	if got := strings.Join(req.MediaInputPolicy.BlockedHeaders, ","); got != "Cookie,Authorization,Proxy-Authorization,Referer" {
		t.Fatalf("BlockedHeaders = %q", got)
	}
}

func TestStreamsDirectHLSUsesBufferByDefault(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	enableBridgeHLSBufferForTest(a)
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	var gotOpts hlsbuffer.SessionOptions
	var closeCalls int
	a.hlsBufferOpen = func(ctx context.Context, opts hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		gotOpts = opts
		return &hlsbuffer.Session{
			PlaybackPath: "/tmp/local-buffered.m3u8",
			Policy:       core.MediaInputPolicy{ProtocolWhitelist: []string{"file"}},
			Stats:        func() hlsbuffer.Stats { return hlsbuffer.Stats{} },
			Close: func() error {
				closeCalls++
				return nil
			},
		}, nil
	}

	_, err = a.StartResolvedStream(t.Context(), streamhandoff.Resolution{
		ProviderID: "toonami-aftermath",
		ChannelID:  "east",
	})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if gotOpts.SourceURL != "http://api.toonamiaftermath.com:3000/est/playlist.m3u8" {
		t.Fatalf("hls SourceURL = %q", gotOpts.SourceURL)
	}
	if gotOpts.TrustMode != hlsbuffer.TrustModeBundledToonami {
		t.Fatalf("TrustMode = %v, want bundled Toonami", gotOpts.TrustMode)
	}
	if gotOpts.CacheRoot == "" || gotOpts.Config.StartSegments != 2 || gotOpts.Config.LiveEdgeSegments != 3 {
		t.Fatalf("hls options = %+v", gotOpts)
	}
	req := c.lastReq
	if req.StreamURL != "/tmp/local-buffered.m3u8" {
		t.Fatalf("StreamURL = %q, want buffered local playlist", req.StreamURL)
	}
	if got := strings.Join(req.MediaInputPolicy.ProtocolWhitelist, ","); got != "file" {
		t.Fatalf("ProtocolWhitelist = %q, want file", got)
	}
	if req.MediaKind != core.MediaKindVideo {
		t.Fatalf("MediaKind = %q, want video", req.MediaKind)
	}
	req.OnStop("stopped")
	if closeCalls != 1 {
		t.Fatalf("buffer Close calls after OnStop = %d, want 1", closeCalls)
	}
}

func TestStreamsDirectHLSOptOutUsesDirectPath(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	a.cfg.Providers["toonami-aftermath"] = ProviderConfig{
		Channels: map[string]ChannelConfig{
			"east": {HLSBufferDisabled: true},
		},
	}
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		t.Fatal("hlsBufferOpen should not be called when channel opts out")
		return nil, nil
	}

	_, err = a.StartResolvedStream(t.Context(), streamhandoff.Resolution{
		ProviderID: "toonami-aftermath",
		ChannelID:  "east",
	})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if c.lastReq.StreamURL != "http://api.toonamiaftermath.com:3000/est/playlist.m3u8" {
		t.Fatalf("StreamURL = %q, want direct path", c.lastReq.StreamURL)
	}
}

func TestStreamsDirectHLSCleansBufferWhenCoreStartFails(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	enableBridgeHLSBufferForTest(a)
	c.startErr = fmt.Errorf("core start failed")
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	var closeCalls int
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		return &hlsbuffer.Session{
			PlaybackPath: "/tmp/local-buffered.m3u8",
			Policy:       core.MediaInputPolicy{ProtocolWhitelist: []string{"file"}},
			Stats:        func() hlsbuffer.Stats { return hlsbuffer.Stats{} },
			Close: func() error {
				closeCalls++
				return nil
			},
		}, nil
	}

	_, err = a.StartResolvedStream(t.Context(), streamhandoff.Resolution{
		ProviderID: "toonami-aftermath",
		ChannelID:  "east",
	})
	if err == nil {
		t.Fatal("StartResolvedStream error = nil, want core start failure")
	}
	if closeCalls != 1 {
		t.Fatalf("buffer Close calls = %d, want 1", closeCalls)
	}
}

func TestStreamsDirectHLSSurfacesBufferOpenError(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	enableBridgeHLSBufferForTest(a)
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		return nil, fmt.Errorf("hls url: Toonami Aftermath host must be api.toonamiaftermath.com:3000")
	}

	_, err = a.StartResolvedStream(t.Context(), streamhandoff.Resolution{
		ProviderID: "toonami-aftermath",
		ChannelID:  "east",
	})
	if err == nil {
		t.Fatal("StartResolvedStream error = nil, want hls buffer open error")
	}
	if !strings.Contains(err.Error(), "Toonami Aftermath host must be") {
		t.Fatalf("err = %q, want underlying hls buffer message to be surfaced", err)
	}
}

func enableBridgeHLSBufferForTest(a *Adapter) {
	a.bridge.HLSBuffer.Enabled = true
	a.bridge.HLSBuffer.LiveEdgeSegments = 3
	a.bridge.HLSBuffer.StartSegments = 2
	a.bridge.HLSBuffer.MaxCachedSegments = 6
	a.bridge.HLSBuffer.MaxCacheBytes = 268435456
	a.bridge.HLSBuffer.MaxPlaylistBytes = 1048576
	a.bridge.HLSBuffer.MaxSegmentBytes = 52428800
	a.bridge.HLSBuffer.SegmentTimeoutSeconds = 10
	a.bridge.HLSBuffer.PlaylistTimeoutSeconds = 10
	a.bridge.HLSBuffer.MaxVariantHeight = 720
	a.bridge.HLSBuffer.StaleCacheReapHours = 24
}

func TestReplayDirectStreamRebuildsFromCatalogItem(t *testing.T) {
	a, c := newTestAdapterWithFakeCore(t)
	a.bridge.HLSBuffer.Enabled = false
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})

	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "toonami-aftermath", ChannelID: "east"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if err := a.Replay(t.Context()); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if c.startCalls != 2 {
		t.Fatalf("StartSession calls = %d, want 2", c.startCalls)
	}
	if c.lastReq.StreamURL != "http://api.toonamiaftermath.com:3000/est/playlist.m3u8" {
		t.Fatalf("replay StreamURL = %q", c.lastReq.StreamURL)
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

func TestDirectStopQueueSerializesWithInFlightStart(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})

	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	fc.startHook = func(core.SessionRequest) {
		close(startEntered)
		<-releaseStart
	}

	startDone := make(chan error, 1)
	go func() {
		_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "toonami-aftermath", ChannelID: "east"})
		startDone <- err
	}()
	<-startEntered

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- a.StopQueue(t.Context())
	}()

	select {
	case err := <-stopDone:
		t.Fatalf("StopQueue returned while direct StartSession was still in flight: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseStart)
	if err := <-startDone; err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("StopQueue: %v", err)
	}
	if got := fc.Status().AdapterRef; got != "" {
		t.Fatalf("core AdapterRef after StopQueue = %q, want stopped", got)
	}
	if a.active != nil {
		t.Fatalf("active queue after StopQueue = %+v, want nil", a.active)
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

func TestGlobalResolveFailureFailsFastWithoutRetryingQueue(t *testing.T) {
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
	resolver := &fakeResolver{err: fmt.Errorf("ytdlp: no such option: --js-runtimes")}
	a.resolver = resolver

	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
	if err == nil {
		t.Fatal("StartResolvedStream should fail when yt-dlp itself is incompatible")
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1 fail-fast attempt", resolver.calls)
	}
	if core.startCalls != 0 {
		t.Fatalf("core start calls = %d, want 0", core.startCalls)
	}
	if a.active != nil {
		t.Fatalf("active queue = %+v, want cleared after global resolver failure", a.active)
	}
}

func TestResolveFailureCachesBadItemForNextColdStart(t *testing.T) {
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
		{err: fmt.Errorf("ytdlp: video unavailable")},
		{res: &ytdlp.Resolution{URL: "https://media.example/second.mp4"}},
		{res: &ytdlp.Resolution{URL: "https://media.example/second-again.mp4"}},
	}}
	a.resolver = resolver

	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"}); err != nil {
		t.Fatalf("first StartResolvedStream: %v", err)
	}
	if resolver.calls != 2 {
		t.Fatalf("first start resolver calls = %d, want 2", resolver.calls)
	}

	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"}); err != nil {
		t.Fatalf("second StartResolvedStream: %v", err)
	}
	if resolver.calls != 3 {
		t.Fatalf("total resolver calls = %d, want one call on second cold start", resolver.calls)
	}
	if got := resolver.pageURLs[2]; !strings.Contains(got, "bbbbbbbbbbb") {
		t.Fatalf("second cold start resolved %q, want cached-bad first item skipped", got)
	}
	if core.startCalls != 2 || core.lastReq.StreamURL != "https://media.example/second-again.mp4" {
		t.Fatalf("core start calls=%d lastReq=%+v", core.startCalls, core.lastReq)
	}
}

func TestDefaultFailureBudgetSkipsBadMTVRunAndContinuesQueue(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	items := []StreamItem{
		{ID: "aaaaaaaaaaa", SourceID: "aaaaaaaaaaa", URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa"},
		{ID: "bbbbbbbbbbb", SourceID: "bbbbbbbbbbb", URL: "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
		{ID: "ccccccccccc", SourceID: "ccccccccccc", URL: "https://www.youtube.com/watch?v=ccccccccccc"},
		{ID: "ddddddddddd", SourceID: "ddddddddddd", URL: "https://www.youtube.com/watch?v=ddddddddddd"},
		{ID: "eeeeeeeeeee", SourceID: "eeeeeeeeeee", URL: "https://www.youtube.com/watch?v=eeeeeeeeeee"},
		{ID: "ffffffffff0", SourceID: "ffffffffff0", URL: "https://www.youtube.com/watch?v=ffffffffff0"},
		{ID: "ggggggggggg", SourceID: "ggggggggggg", URL: "https://www.youtube.com/watch?v=ggggggggggg"},
	}
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels: []Channel{{
			ID:       "metal",
			Name:     "Metal",
			PlayMode: PlaySequential,
			Items:    items,
		}},
	}})
	responses := make([]fakeResolveResponse, 0, len(items))
	for i := 0; i < len(items)-1; i++ {
		responses = append(responses, fakeResolveResponse{err: fmt.Errorf("yt-dlp could not resolve item %d", i)})
	}
	responses = append(responses, fakeResolveResponse{res: &ytdlp.Resolution{URL: "https://media.example/seventh.mp4"}})
	a.resolver = &fakeResolver{responses: responses}

	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
	if err != nil {
		t.Fatalf("StartResolvedStream should continue past a short bad MTV run: %v", err)
	}
	if core.startCalls != 1 || core.lastReq.StreamURL != "https://media.example/seventh.mp4" {
		t.Fatalf("core start calls=%d req=%+v", core.startCalls, core.lastReq)
	}
	if a.active == nil || a.active.Index != len(items)-1 {
		t.Fatalf("active queue = %+v, want final playable item active", a.active)
	}
}

func TestSuccessfulStartClearsPlaybackErrorState(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.state = adapters.StateError
	a.lastErr = "streams playback failed"

	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	status := a.Status()
	if status.State != adapters.StateRunning || status.LastError != "" {
		t.Fatalf("status after successful start = %+v, want running with no error", status)
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

func TestStartResolvedStreamReplacementResolveFailureKeepsPreviousOwnedSession(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	oldQueue := &ActiveQueue{
		SessionID:    "old-session",
		ProviderID:   "mtv-rewind",
		ProviderName: "MTV Rewind",
		ChannelID:    "metal",
		ChannelName:  "Metal",
		Items:        []StreamItem{{ID: "old", SourceID: "old"}},
		ItemToken:    1,
	}
	a.active = oldQueue
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)
	a.resolver = &fakeResolver{err: fmt.Errorf("resolver failed before replacement start")}

	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"})
	if err == nil {
		t.Fatal("StartResolvedStream replacement should fail")
	}
	if core.stopCalls != 0 {
		t.Fatalf("core stop calls = %d, want previous session left running", core.stopCalls)
	}
	if got, want := core.Status().AdapterRef, queueAdapterRef(oldQueue, oldQueue.ItemToken); got != want {
		t.Fatalf("core AdapterRef after failed replacement = %q, want previous %q", got, want)
	}
	if a.active != oldQueue {
		t.Fatalf("active queue after failed replacement = %+v, want previous queue", a.active)
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

func TestManualNextIgnoresExpectedStoppedCallback(t *testing.T) {
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
	a.resolver = &fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4"}}
	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	core.stopHook = func() {
		core.lastReq.OnStop("stopped")
	}

	if err := a.Next(t.Context()); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if a.active == nil || a.active.Index != 1 || a.active.Items[a.active.Index].ID != "bbbbbbbbbbb" {
		t.Fatalf("active queue after Next = %+v, want second item", a.active)
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

func TestReplayIgnoresExpectedStoppedCallback(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
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
	a.resolver = &fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4"}}
	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "mtv-rewind", ChannelID: "metal"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	core.stopHook = func() {
		core.lastReq.OnStop("stopped")
	}

	if err := a.Replay(t.Context()); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if a.active == nil || len(a.active.Items) != 1 || a.active.Items[0].ID != "freshfresh1" {
		t.Fatalf("active queue after replay = %+v, want latest catalog item", a.active)
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

func (r *blockedFirstResolver) EnumeratePlaylist(_ context.Context, _ string, _ string, _ int) ([]ytdlp.PlaylistEntry, error) {
	return nil, nil
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

func (r *blockingSuccessResolver) EnumeratePlaylist(_ context.Context, _ string, _ string, _ int) ([]ytdlp.PlaylistEntry, error) {
	return nil, nil
}

func TestStreamsMeterOverlayUsesCoreGeneration(t *testing.T) {
	a, fc := newTestAdapterWithFakeCore(t)
	enableBridgeHLSBufferForTest(a)
	a.bridge.HLSBuffer.MaxCachedSegments = 9
	fc.status = core.SessionStatus{State: core.StatePlaying, Generation: 21}
	def := bundledToonamiAftermathDefinition()
	cat, err := buildDirectStreamsCatalog(def)
	if err != nil {
		t.Fatalf("buildDirectStreamsCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})

	var openedMaxSegments int
	stats := hlsbuffer.Stats{
		CachedSegments:        2,
		CachedMediaDuration:   5 * time.Second,
		CacheBytes:            4096,
		SegmentDownloadsTotal: 2,
		SelectedVariant:       hlsbuffer.Variant{URI: "relative/live.m3u8?sig=secret", Width: 640, Height: 480, Bandwidth: 1200000, Codecs: "avc1.secret"},
	}
	a.hlsBufferOpen = func(ctx context.Context, opts hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		openedMaxSegments = opts.Config.MaxCachedSegments
		return &hlsbuffer.Session{
			PlaybackPath: filepath.Join(t.TempDir(), "playlist.m3u8"),
			Stats:        func() hlsbuffer.Stats { return stats },
			Close:        func() error { return nil },
		}, nil
	}

	started, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "toonami-aftermath", ChannelID: "east"})
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if openedMaxSegments != 9 {
		t.Fatalf("opened MaxCachedSegments = %d, want normalized configured value 9", openedMaxSegments)
	}
	a.bridge.HLSBuffer.MaxCachedSegments = 99

	snap := core.StatusHomeView{State: core.StatePlaying, AdapterRef: started.AdapterRef, Source: "streams", Generation: 21}
	if snap.AdapterRef != started.AdapterRef || snap.Generation == 0 {
		t.Fatalf("core snapshot = %+v started=%+v", snap, started)
	}
	overlay, ok := a.MeterOverlay(context.Background(), snap)
	if !ok || overlay.HLS == nil {
		t.Fatalf("MeterOverlay ok=%v overlay=%+v", ok, overlay)
	}
	if overlay.HLS.CachedSegments != 2 || overlay.HLS.MaxCachedSegments != openedMaxSegments {
		t.Fatalf("HLS overlay = %+v", overlay.HLS)
	}
	stale := snap
	stale.Generation++
	if _, ok := a.MeterOverlay(context.Background(), stale); ok {
		t.Fatalf("stale core generation should not own overlay")
	}
	body, _ := json.Marshal(overlay)
	for _, leak := range []string{"http://", "https://", "://", "/live.m3u8", "token=", "sig=", "secret", "Authorization", "relative/", "avc1"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("overlay leaked %q: %s", leak, body)
		}
	}
}

func TestStreamsMeterOverlayClearsBeforeBaseOnStopAndClose(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	ref := "streams:test:channel:1"
	snap := core.StatusHomeView{State: core.StatePlaying, AdapterRef: ref, Source: "streams", Generation: 21}
	a.activeOverlay = &hlsMeterHandle{
		ref:               ref,
		generation:        21,
		stats:             func() hlsbuffer.Stats { return hlsbuffer.Stats{CachedSegments: 1} },
		maxCachedSegments: 6,
	}
	var order []string
	var baseSawOverlay bool
	baseOnStop := func(reason string) {
		order = append(order, "base")
		_, baseSawOverlay = a.MeterOverlay(context.Background(), snap)
	}
	onStop := withHLSBufferCleanup(a.hlsMeterClearingOnStop(ref, baseOnStop), &hlsbuffer.Session{
		Close: func() error {
			order = append(order, "close")
			return nil
		},
	})
	onStop("stopped")
	if baseSawOverlay {
		t.Fatal("overlay should be cleared before base OnStop state mutation")
	}
	if _, ok := a.MeterOverlay(context.Background(), core.StatusHomeView{State: core.StateIdle}); ok {
		t.Fatal("overlay should be cleared after stop")
	}
	if len(order) != 2 || order[0] != "base" || order[1] != "close" {
		t.Fatalf("stop order = %#v, want base before close", order)
	}
}

func TestAdapterImplementsMeterOverlayProvider(t *testing.T) {
	var _ adapters.MeterOverlayProvider = (*Adapter)(nil)
}

func TestStreamsDisplayMetadata(t *testing.T) {
	d := streamsDisplayMetadata("Adult Swim", "Toonami Aftermath", "Action Block")
	if d.Primary != "Toonami Aftermath" || d.Secondary != "Adult Swim" || d.Tertiary != "Action Block" {
		t.Fatalf("d = %+v", d)
	}
	same := streamsDisplayMetadata("YouTube", "Lofi Radio", "Lofi Radio")
	if same.Tertiary != "" {
		t.Fatalf("expected empty tertiary, got %+v", same)
	}
	fb := streamsDisplayMetadata("YouTube", "", "Some Video")
	if fb.Primary != "Some Video" {
		t.Fatalf("fallback primary = %+v", fb)
	}
}

func installUserDirectAdapter(t *testing.T) (*Adapter, *fakeCore) {
	t.Helper()
	a, c := newTestAdapterWithFakeCore(t)
	def := ProviderDefinition{
		ID: "user:cdn", Type: userProviderType, DisplayName: "CDN", BadgeLabel: "CD", BadgeColor: "teal",
		Channels: []ChannelDefinition{{ID: "live", Name: "Live", Kind: kindDirect, URL: "https://cdn.example.com/live.m3u8"}},
	}
	cat, err := buildUserCatalog(context.Background(), def, userPlaylistEnumerator{})
	if err != nil {
		t.Fatalf("buildUserCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{"cdn.example.com": {"93.184.216.34"}}}
	a.userRedirectDoer = stubDoer{} // no redirect → 200
	return a, c
}

func TestUserDirect_UsesUserPolicyAndPrevalidatedURL(t *testing.T) {
	a, c := installUserDirectAdapter(t)
	enableBridgeHLSBufferForTest(a) // even with the buffer enabled, user direct must skip it
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		t.Fatal("hlsBufferOpen must not be called for user providers")
		return nil, nil
	}
	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "user:cdn", ChannelID: "live"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if c.lastReq.StreamURL != "https://cdn.example.com/live.m3u8" {
		t.Fatalf("StreamURL = %q, want prevalidated direct URL", c.lastReq.StreamURL)
	}
	want := userDirectInputPolicy()
	got := c.lastReq.MediaInputPolicy
	for _, p := range got.ProtocolWhitelist {
		if p == "file" {
			t.Fatalf("user direct policy must not whitelist 'file': %v", got.ProtocolWhitelist)
		}
	}
	if len(got.ProtocolWhitelist) != len(want.ProtocolWhitelist) {
		t.Fatalf("ProtocolWhitelist = %v, want %v", got.ProtocolWhitelist, want.ProtocolWhitelist)
	}
}

func TestUserDirect_BlockedRedirectFailsCast(t *testing.T) {
	a, c := installUserDirectAdapter(t)
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{
		"cdn.example.com":  {"93.184.216.34"},
		"evil.example.com": {"169.254.169.254"},
	}}
	a.userRedirectDoer = stubDoer{resp: map[string]*http.Response{
		"https://cdn.example.com/live.m3u8": redirectResp("https://evil.example.com/meta"),
	}}
	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "user:cdn", ChannelID: "live"})
	if err == nil {
		t.Fatal("StartResolvedStream succeeded, want failure on metadata-host redirect")
	}
	if c.startCalls != 0 {
		t.Fatalf("core start calls = %d, want 0 (URL never reaches FFmpeg)", c.startCalls)
	}
}

func installUserSingleAdapter(t *testing.T, resolved *ytdlp.Resolution) (*Adapter, *fakeCore) {
	t.Helper()
	a, c := newTestAdapterWithFakeCore(t)
	def := ProviderDefinition{
		ID: "user:tw", Type: userProviderType, DisplayName: "TW", BadgeLabel: "TW", BadgeColor: "purple",
		Channels: []ChannelDefinition{{ID: "vod", Name: "VOD", Kind: kindSingle, URL: "https://www.twitch.tv/foo"}},
	}
	cat, err := buildUserCatalog(context.Background(), def, userPlaylistEnumerator{})
	if err != nil {
		t.Fatalf("buildUserCatalog: %v", err)
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{def})
	a.replaceCatalogsForTest([]ProviderCatalog{cat})
	a.resolver = &fakeResolver{res: resolved}
	return a, c
}

func TestUserSingle_BlockedResolvedHostFailsCast(t *testing.T) {
	a, c := installUserSingleAdapter(t, &ytdlp.Resolution{URL: "https://media.evil.com/v.mp4"})
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{"media.evil.com": {"169.254.169.254"}}}
	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "user:tw", ChannelID: "vod"})
	if err == nil {
		t.Fatal("StartResolvedStream succeeded, want failure on metadata resolved host")
	}
	if c.startCalls != 0 {
		t.Fatalf("core start calls = %d, want 0 (resolved URL never reaches FFmpeg)", c.startCalls)
	}
}

func TestUserSingle_BlockedAudioURLFailsCast(t *testing.T) {
	a, c := installUserSingleAdapter(t, &ytdlp.Resolution{
		URL:      "https://media.ok.com/v.mp4",
		AudioURL: "https://media.evil.com/a.mp4",
	})
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{
		"media.ok.com":   {"93.184.216.34"},
		"media.evil.com": {"127.0.0.1"},
	}}
	_, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "user:tw", ChannelID: "vod"})
	if err == nil {
		t.Fatal("StartResolvedStream succeeded, want failure on blocked AudioURL")
	}
	if c.startCalls != 0 {
		t.Fatalf("core start calls = %d, want 0", c.startCalls)
	}
}

func TestUserSingle_SafeResolvedHostCasts(t *testing.T) {
	a, c := installUserSingleAdapter(t, &ytdlp.Resolution{URL: "https://media.ok.com/v.mp4"})
	a.userURLResolver = stubHostResolver{hosts: map[string][]string{"media.ok.com": {"93.184.216.34"}}}
	if _, err := a.StartResolvedStream(t.Context(), streamhandoff.Resolution{ProviderID: "user:tw", ChannelID: "vod"}); err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if c.lastReq.StreamURL != "https://media.ok.com/v.mp4" {
		t.Fatalf("StreamURL = %q, want resolved URL", c.lastReq.StreamURL)
	}
}
