package streams

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
)

func (a *Adapter) StartResolvedStream(ctx context.Context, res streamhandoff.Resolution) (streamhandoff.StartResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	started, _, err := a.startResolvedStream(ctx, res, startCoreSession, true)
	return started, err
}

func (a *Adapter) StartResolvedStreamIfSession(ctx context.Context, res streamhandoff.Resolution, expectedRef string, expectedGeneration uint64) (streamhandoff.StartResult, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if expectedRef == "" || expectedGeneration == 0 {
		return streamhandoff.StartResult{}, false, nil
	}
	coreManager := a.core
	if coreManager == nil {
		return streamhandoff.StartResult{}, false, playbackError(res.ProviderID, "core playback manager is not configured")
	}
	status := coreManager.Status()
	if status.AdapterRef != expectedRef || status.Generation != expectedGeneration {
		return streamhandoff.StartResult{}, false, nil
	}
	return a.startResolvedStream(ctx, res, startCoreSessionIfSession(expectedRef, expectedGeneration), false)
}

func (a *Adapter) startResolvedStream(ctx context.Context, res streamhandoff.Resolution, starter streamCoreStarter, stopPrevious bool) (streamhandoff.StartResult, bool, error) {
	q, err := a.queueFromResolution(res)
	if err != nil {
		return streamhandoff.StartResult{}, false, err
	}

	a.mu.Lock()
	previous := a.active
	if stopPrevious && previous != nil && previous.cancelResolve != nil {
		previous.cancelResolve()
		previous.cancelResolve = nil
	}
	a.active = q
	guard := queueVersionOf(q)
	a.mu.Unlock()

	started, matched, err := a.playCurrentWithStarter(ctx, guard, starter)
	if err != nil {
		if stopPrevious {
			a.restorePreviousQueueAfterFailedReplacement(guard, previous)
		} else {
			a.restorePreviousQueueAfterUnmatchedStart(guard, previous)
		}
		return streamhandoff.StartResult{}, false, err
	}
	if !matched {
		if stopPrevious {
			a.restorePreviousQueueAfterFailedReplacement(guard, previous)
		} else {
			a.restorePreviousQueueAfterUnmatchedStart(guard, previous)
		}
		return streamhandoff.StartResult{}, false, nil
	}
	if !stopPrevious {
		a.cancelDetachedQueueResolve(previous)
	}
	if res.ItemID == "" {
		started.ItemID = ""
	} else {
		started.ItemID = res.ItemID
	}
	return started, true, nil
}

func (a *Adapter) restorePreviousQueueAfterFailedReplacement(guard queueVersion, previous *ActiveQueue) {
	if previous == nil {
		a.clearQueueIfGuardMatches(guard)
		return
	}
	if coreManager := a.core; coreManager != nil {
		if previousRef := activeAdapterRef(previous); previousRef != "" && coreManager.Status().AdapterRef != previousRef {
			a.clearQueueIfGuardMatches(guard)
			return
		}
	}
	a.restorePreviousQueueAfterUnmatchedStart(guard, previous)
}

func (a *Adapter) restorePreviousQueueAfterUnmatchedStart(guard queueVersion, previous *ActiveQueue) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if guard.matches(a.active) {
		a.clearActiveLocked()
		a.active = previous
		return
	}
	if a.active == nil {
		a.active = previous
	}
}

func (a *Adapter) clearQueueIfGuardMatches(guard queueVersion) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if guard.matches(a.active) {
		a.clearActiveLocked()
	}
}

func (a *Adapter) cancelDetachedQueueResolve(q *ActiveQueue) {
	if q == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == q || q.cancelResolve == nil {
		return
	}
	q.cancelResolve()
	q.cancelResolve = nil
}

func (a *Adapter) queueFromResolution(res streamhandoff.Resolution) (*ActiveQueue, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.cfg.Enabled {
		return nil, &StreamsError{
			Kind:    ErrKindProviderDisabled,
			Message: "streams adapter is disabled",
		}
	}
	if providerCfg, ok := a.cfg.Providers[res.ProviderID]; ok && providerCfg.Disabled {
		return nil, &StreamsError{
			Kind:    ErrKindProviderDisabled,
			Message: fmt.Sprintf("streams provider %q is disabled", res.ProviderID),
		}
	}
	cat, ok := a.catalogs[res.ProviderID]
	if !ok {
		return nil, invalidExtraction(res.ProviderID, fmt.Sprintf("provider %q is not cataloged", res.ProviderID))
	}
	providerName := providerDisplayName(cat, a.definitions[res.ProviderID])

	var q *ActiveQueue
	var err error
	switch {
	case res.ChannelID != "" && res.ItemID == "":
		ch := cat.Channel(res.ChannelID)
		if ch == nil {
			return nil, invalidExtraction(res.ProviderID, fmt.Sprintf("channel %q is not in provider %q", res.ChannelID, res.ProviderID))
		}
		q, err = buildQueue(res.ProviderID, *ch, a.rng)
	case res.ItemID != "":
		if !youtubeIDRE.MatchString(res.ItemID) {
			return nil, invalidExtraction(res.ProviderID, fmt.Sprintf("item %q is not a valid YouTube ID", res.ItemID))
		}
		if res.ChannelID != "" && res.ChannelID != reservedAdhocID {
			return nil, invalidExtraction(res.ProviderID, "resolution must identify exactly one channel or item")
		}
		if res.ChannelID == reservedAdhocID {
			q, err = buildAdhocQueue(res.ProviderID, providerName, StreamItem{ID: res.ItemID, SourceID: res.ItemID})
			break
		}
		if ch, item, ok := findCatalogItem(cat, res.ItemID); ok {
			q, err = buildQueue(res.ProviderID, ch, a.rng)
			if err == nil {
				q.moveItemToFront(itemIdentity(item))
			}
			break
		}
		q, err = buildAdhocQueue(res.ProviderID, providerName, StreamItem{ID: res.ItemID, SourceID: res.ItemID})
	default:
		return nil, invalidExtraction(res.ProviderID, "resolution must identify a channel or item")
	}
	if err != nil {
		return nil, playbackError(res.ProviderID, err.Error())
	}
	q.ProviderName = providerName
	q.SessionID = newQueueSessionID()
	q.StartedAt = time.Now()
	return q, nil
}

type queueVersion struct {
	SessionID  string
	Generation uint64
}

func (v queueVersion) matches(q *ActiveQueue) bool {
	return v.SessionID == "" || (q != nil && q.SessionID == v.SessionID && q.Generation == v.Generation)
}

func queueVersionOf(q *ActiveQueue) queueVersion {
	if q == nil {
		return queueVersion{}
	}
	return queueVersion{SessionID: q.SessionID, Generation: q.Generation}
}

type streamCoreStarter func(SessionManager, core.SessionRequest) (bool, error)

func startCoreSession(coreManager SessionManager, req core.SessionRequest) (bool, error) {
	return true, coreManager.StartSession(req)
}

func startCoreSessionIfSession(expectedRef string, expectedGeneration uint64) streamCoreStarter {
	return func(coreManager SessionManager, req core.SessionRequest) (bool, error) {
		return coreManager.StartSessionIfSession(req, expectedRef, expectedGeneration)
	}
}

func startCoreSessionIfIdle(coreManager SessionManager, req core.SessionRequest) (bool, error) {
	return coreManager.StartSessionIfIdle(req)
}

func (a *Adapter) playCurrent(ctx context.Context) (streamhandoff.StartResult, error) {
	started, _, err := a.playCurrentWithStarter(ctx, queueVersion{}, startCoreSession)
	return started, err
}

func (a *Adapter) playCurrentGuarded(ctx context.Context, guard queueVersion) (streamhandoff.StartResult, error) {
	started, _, err := a.playCurrentWithStarter(ctx, guard, startCoreSession)
	return started, err
}

func (a *Adapter) playCurrentIfCoreIdle(ctx context.Context, guard queueVersion) (streamhandoff.StartResult, error) {
	started, matched, err := a.playCurrentWithStarter(ctx, guard, startCoreSessionIfIdle)
	if err != nil {
		return streamhandoff.StartResult{}, err
	}
	if !matched {
		return streamhandoff.StartResult{}, playbackError("", "active session changed")
	}
	return started, nil
}

func (a *Adapter) playCurrentWithStarter(ctx context.Context, guard queueVersion, starter streamCoreStarter) (streamhandoff.StartResult, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	a.mu.Lock()
	q := a.active
	if q == nil {
		a.mu.Unlock()
		return streamhandoff.StartResult{}, false, playbackError("", "no active streams queue")
	}
	if !guard.matches(q) {
		a.mu.Unlock()
		return streamhandoff.StartResult{}, false, playbackError("", "stream start was superseded")
	}
	item, ok := q.currentItem()
	if !ok {
		a.clearActiveLocked()
		a.mu.Unlock()
		return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "active streams queue is empty")
	}
	resolver := a.resolver
	coreManager := a.core
	format := a.cfg.YoutubeFormat
	cookiesPath := a.cookiesPath
	pageURL := streamItemURL(item)
	q.ItemToken++
	ref := queueAdapterRef(q, q.ItemToken)
	capture := queueCapture{
		Generation: q.Generation,
		ItemToken:  q.ItemToken,
		SessionID:  q.SessionID,
		ItemID:     itemIdentity(item),
		AdapterRef: ref,
	}
	if q.cancelResolve != nil {
		q.cancelResolve()
	}
	resolveCtx, cancel := context.WithCancel(ctx)
	q.cancelResolve = cancel
	a.mu.Unlock()

	if pageURL == "" {
		cancel()
		a.clearResolveIfCurrent(capture)
		return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "stream item is missing a playable URL")
	}
	if isDirectStreamItem(item) {
		if coreManager == nil {
			cancel()
			a.clearResolveIfCurrent(capture)
			return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "core playback manager is not configured")
		}
		title := streamSessionTitle(item, "")
		if strings.TrimSpace(q.ProviderName) != "" && strings.TrimSpace(q.ChannelName) != "" {
			title = strings.TrimSpace(q.ProviderName) + " / " + strings.TrimSpace(q.ChannelName)
		}
		playbackURL := pageURL
		mediaPolicy := directHLSInputPolicy()
		baseOnStop := a.makeOnStop(capture)
		onStop := baseOnStop
		var hlsSession *hlsbuffer.Session
		var hlsCfg hlsbuffer.Config
		if a.shouldBufferDirectHLS(q, item) {
			open := a.hlsBufferOpen
			if open == nil {
				open = hlsbuffer.OpenSession
			}
			hlsCfg = hlsbuffer.NormalizeConfig(hlsConfigFromBridge(a.bridge.HLSBuffer))
			var err error
			hlsSession, err = open(resolveCtx, hlsbuffer.SessionOptions{
				SourceURL:    pageURL,
				CacheRoot:    a.hlsBufferCacheRoot(),
				Config:       hlsCfg,
				TrustMode:    hlsbuffer.TrustModeBundledToonami,
				OutputHeight: a.hlsOutputHeight(),
			})
			if err != nil {
				cancel()
				a.clearResolveIfCurrent(capture)
				return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, fmt.Sprintf("failed to buffer HLS stream: %v", err))
			}
			playbackURL = hlsSession.PlaybackPath
			mediaPolicy = hlsSession.Policy
			stoppedRef := ref
			onStop = withHLSBufferCleanup(a.hlsMeterClearingOnStop(stoppedRef, baseOnStop), hlsSession)
		}
		req := core.SessionRequest{
			StreamURL:    playbackURL,
			AdapterRef:   ref,
			DirectPlay:   true,
			AspectMode:   streamSessionAspectMode(q.ProviderID),
			Capabilities: core.Capabilities{CanPause: false, CanSeek: false},
			// SECURITY/AUDIO: Toonami Radio currently advertises video HLS. If it becomes audio-only, add MediaKindMusic + VisualizerRequest or remove Radio.
			MediaKind:        core.MediaKindVideo,
			MediaInputPolicy: mediaPolicy,
			OnStop:           onStop,
			Source:           a.Name(),
			Title:            title,
			DisplayMetadata:  streamsDisplayMetadata(q.ProviderName, q.ChannelName, streamSessionTitle(item, "")),
		}
		cancel()
		a.playbackMu.Lock()
		if !a.captureStillActive(capture) {
			a.playbackMu.Unlock()
			closeHLSSession(hlsSession)
			return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "stream start was superseded")
		}
		matched, err := starter(coreManager, req)
		if err != nil {
			a.playbackMu.Unlock()
			closeHLSSession(hlsSession)
			if next, ok := a.recordStartFailureAndAdvance(capture, "failed to start stream playback"); ok {
				a.runBeforeQueueContinuation()
				return a.playCurrentWithStarter(ctx, next, starter)
			}
			return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "failed to start stream playback")
		}
		if !matched {
			a.playbackMu.Unlock()
			closeHLSSession(hlsSession)
			a.clearQueueIfCurrent(capture)
			return streamhandoff.StartResult{}, false, nil
		}
		a.playbackMu.Unlock()

		if hlsSession != nil {
			a.installHLSMeterOverlay(ref, hlsSession, hlsCfg)
		}

		now := time.Now()
		a.mu.Lock()
		if capture.matches(a.active) {
			a.active.LastResolvedAt = now
			a.active.cancelResolve = nil
			setActiveItemTitleLocked(a.active, capture.ItemID, title)
			a.markPlaybackRunningLocked()
		}
		a.mu.Unlock()

		return streamhandoff.StartResult{
			AdapterRef: ref,
			ProviderID: q.ProviderID,
			ChannelID:  q.ChannelID,
			ItemID:     itemIdentity(item),
		}, true, nil
	}
	if resolver == nil {
		cancel()
		a.clearResolveIfCurrent(capture)
		return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "stream resolver is not configured")
	}
	if coreManager == nil {
		cancel()
		a.clearResolveIfCurrent(capture)
		return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "core playback manager is not configured")
	}

	resolved, err := resolver.Resolve(resolveCtx, pageURL, format, cookiesPath)
	cancel()
	if err != nil {
		if next, ok := a.recordStartFailureAndAdvance(capture, "failed to resolve stream item"); ok {
			a.runBeforeQueueContinuation()
			return a.playCurrentWithStarter(ctx, next, starter)
		}
		return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "failed to resolve stream item")
	}
	if resolved == nil || strings.TrimSpace(resolved.URL) == "" {
		if next, ok := a.recordStartFailureAndAdvance(capture, "stream resolver returned no playable media URL"); ok {
			a.runBeforeQueueContinuation()
			return a.playCurrentWithStarter(ctx, next, starter)
		}
		return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "stream resolver returned no playable media URL")
	}
	title := streamSessionTitle(item, resolved.Title)

	req := core.SessionRequest{
		StreamURL:         resolved.URL,
		InputHeaders:      resolved.Headers,
		AudioStreamURL:    resolved.AudioURL,
		AudioInputHeaders: resolved.AudioHeaders,
		AdapterRef:        ref,
		DirectPlay:        true,
		AspectMode:        streamSessionAspectMode(q.ProviderID),
		Capabilities:      core.Capabilities{CanPause: true, CanSeek: false},
		OnStop:            a.makeOnStop(capture),
		Source:            a.Name(),
		Title:             title,
		DisplayMetadata:   streamsDisplayMetadata(q.ProviderName, q.ChannelName, streamSessionTitle(item, resolved.Title)),
	}
	a.playbackMu.Lock()
	if !a.captureStillActive(capture) {
		a.playbackMu.Unlock()
		return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "stream start was superseded")
	}
	matched, err := starter(coreManager, req)
	if err != nil {
		a.playbackMu.Unlock()
		if next, ok := a.recordStartFailureAndAdvance(capture, "failed to start stream playback"); ok {
			a.runBeforeQueueContinuation()
			return a.playCurrentWithStarter(ctx, next, starter)
		}
		return streamhandoff.StartResult{}, false, playbackError(q.ProviderID, "failed to start stream playback")
	}
	if !matched {
		a.playbackMu.Unlock()
		a.clearQueueIfCurrent(capture)
		return streamhandoff.StartResult{}, false, nil
	}
	a.playbackMu.Unlock()

	now := time.Now()
	a.mu.Lock()
	if capture.matches(a.active) {
		a.active.LastResolvedAt = now
		a.active.cancelResolve = nil
		setActiveItemTitleLocked(a.active, capture.ItemID, title)
		a.markPlaybackRunningLocked()
	}
	a.mu.Unlock()

	return streamhandoff.StartResult{
		AdapterRef: ref,
		ProviderID: q.ProviderID,
		ChannelID:  q.ChannelID,
		ItemID:     itemIdentity(item),
	}, true, nil
}

func directHLSInputPolicy() core.MediaInputPolicy {
	return core.MediaInputPolicy{
		// SECURITY: file is needed by FFmpeg HLS internals; this is safe only
		// because direct-streams is bundled-only and host/path validated.
		ProtocolWhitelist: []string{"file", "http", "https", "tcp", "tls", "crypto"},
		DisableRedirects:  true,
		DisableReconnect:  true,
		RWTimeout:         5 * time.Second,
		BlockedHeaders:    []string{"Cookie", "Authorization", "Proxy-Authorization", "Referer"},
	}
}

func (a *Adapter) shouldBufferDirectHLS(q *ActiveQueue, item StreamItem) bool {
	if !item.Direct || !a.bridge.HLSBuffer.Enabled {
		return false
	}
	if strings.TrimSpace(os.Getenv("GROOVY_HLS_BUFFER")) == "0" {
		return false
	}
	providerCfg, ok := a.cfg.Providers[q.ProviderID]
	if ok {
		if providerCfg.HLSBufferDisabled {
			return false
		}
		if channelCfg, ok := providerCfg.Channels[q.ChannelID]; ok && channelCfg.HLSBufferDisabled {
			return false
		}
	}
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(streamItemURL(item))), ".m3u8")
}

func (a *Adapter) hlsBufferCacheRoot() string {
	return filepath.Join(a.cacheDir, "hls")
}

func (a *Adapter) hlsOutputHeight() int {
	switch a.bridge.Video.Modeline {
	case "PAL_576i", "PAL_288p":
		return 576
	default:
		return 480
	}
}

func hlsConfigFromBridge(c config.HLSBufferConfig) hlsbuffer.Config {
	return hlsbuffer.Config{
		Enabled:                c.Enabled,
		LiveEdgeSegments:       c.LiveEdgeSegments,
		StartSegments:          c.StartSegments,
		MaxCachedSegments:      c.MaxCachedSegments,
		MaxCacheBytes:          c.MaxCacheBytes,
		MaxPlaylistBytes:       c.MaxPlaylistBytes,
		MaxSegmentBytes:        c.MaxSegmentBytes,
		SegmentTimeout:         time.Duration(c.SegmentTimeoutSeconds) * time.Second,
		PlaylistTimeout:        time.Duration(c.PlaylistTimeoutSeconds) * time.Second,
		MaxVariantHeight:       c.MaxVariantHeight,
		StaleCacheReapInterval: time.Duration(c.StaleCacheReapHours) * time.Hour,
	}
}

func withHLSBufferCleanup(base func(string), session *hlsbuffer.Session) func(string) {
	return func(reason string) {
		if base != nil {
			base(reason)
		}
		closeHLSSession(session)
	}
}

func closeHLSSession(session *hlsbuffer.Session) {
	if session != nil && session.Close != nil {
		_ = session.Close()
	}
}

func isDirectStreamItem(item StreamItem) bool {
	return item.Direct
}

func streamSessionAspectMode(providerID string) string {
	switch providerID {
	case "mtv-rewind", "cartoon-rewind":
		return "zoom"
	default:
		return ""
	}
}

func streamSessionTitle(item StreamItem, resolvedTitle string) string {
	if title := strings.TrimSpace(resolvedTitle); title != "" {
		return title
	}
	return strings.TrimSpace(item.Title)
}

// streamsDisplayMetadata composes VFD tiers for a stream/saved cast:
// Primary = channel name (the recognizable station), Secondary =
// provider, Tertiary = the specific item title when it differs from the
// channel (the "what's on right now" within a channel). Empty channel
// falls back to the item title as Primary. (The spec named "group" for
// the tertiary; the per-item title is what's in scope at this callsite
// and is more informative for now-playing.)
func streamsDisplayMetadata(providerName, channelName, itemTitle string) core.DisplayMetadata {
	channel := strings.TrimSpace(channelName)
	item := strings.TrimSpace(itemTitle)
	if channel == "" {
		channel = item
	}
	d := core.DisplayMetadata{Primary: channel, Secondary: strings.TrimSpace(providerName)}
	if item != "" && !strings.EqualFold(item, channel) {
		d.Tertiary = item
	}
	return d
}

func setActiveItemTitleLocked(q *ActiveQueue, itemID, title string) {
	if q == nil || itemID == "" || title == "" {
		return
	}
	if item, ok := q.currentItem(); ok && itemIdentity(item) == itemID {
		q.Items[q.Index].Title = title
	}
	for i := range q.baseItems {
		if streamItemMatches(q.baseItems[i], itemID) {
			q.baseItems[i].Title = title
			return
		}
	}
}

func (a *Adapter) Next(ctx context.Context) error {
	if err := a.prepareManualStart(func(q *ActiveQueue) bool {
		return q.canAdvanceNext()
	}, func(q *ActiveQueue) bool {
		return q.advanceNext(a.rng)
	}); err != nil {
		return err
	}
	_, err := a.playCurrent(ctx)
	return err
}

func (a *Adapter) NextGuarded(ctx context.Context, ref string, generation uint64) error {
	return a.moveGuarded(ctx, ref, generation, func(q *ActiveQueue) bool {
		return q.canAdvanceNext()
	}, func(q *ActiveQueue) bool {
		return q.advanceNext(a.rng)
	})
}

func (a *Adapter) Previous(ctx context.Context) error {
	if err := a.prepareManualStart(func(q *ActiveQueue) bool {
		return q.canAdvancePrevious()
	}, func(q *ActiveQueue) bool {
		return q.advancePrevious()
	}); err != nil {
		return err
	}
	_, err := a.playCurrent(ctx)
	return err
}

func (a *Adapter) PreviousGuarded(ctx context.Context, ref string, generation uint64) error {
	return a.moveGuarded(ctx, ref, generation, func(q *ActiveQueue) bool {
		return q.canAdvancePrevious()
	}, func(q *ActiveQueue) bool {
		return q.advancePrevious()
	})
}

func (a *Adapter) moveGuarded(ctx context.Context, ref string, generation uint64, canMove, mutator func(*ActiveQueue) bool) error {
	expected, err := a.stopPreviousOwnedCoreGuarded(ref, generation, canMove, true, true)
	if err != nil {
		return err
	}
	a.mu.Lock()
	if a.active == nil {
		a.mu.Unlock()
		return playbackError("", "no active streams queue")
	}
	if expected.SessionID != "" && !expected.matches(a.active) {
		a.mu.Unlock()
		return playbackError("", "active session changed")
	}
	if !mutator(a.active) {
		providerID := a.active.ProviderID
		a.mu.Unlock()
		return playbackError(providerID, "queue has no next item")
	}
	a.active.Failures = nil
	next := queueVersionOf(a.active)
	a.mu.Unlock()
	_, err = a.playCurrentIfCoreIdle(ctx, next)
	return err
}

func (a *Adapter) Replay(ctx context.Context) error {
	next, err := a.prepareReplayStart()
	if err != nil {
		return err
	}
	_, err = a.playCurrentGuarded(ctx, next)
	return err
}

func (a *Adapter) ReplayGuarded(ctx context.Context, ref string, generation uint64) error {
	next, err := a.prepareReplayStartGuarded(ref, generation)
	if err != nil {
		return err
	}
	_, err = a.playCurrentIfCoreIdle(ctx, next)
	return err
}

func (a *Adapter) Pause(ctx context.Context) error {
	_ = ctx
	a.mu.Lock()
	q := a.active
	ref := activeAdapterRef(q)
	coreManager := a.core
	if item, ok := q.currentItem(); ok && item.Direct {
		a.mu.Unlock()
		return playbackError(q.ProviderID, "direct live streams do not support pause")
	}
	a.mu.Unlock()
	if ref == "" {
		return playbackError("", "no active streams queue")
	}
	if coreManager == nil {
		return playbackError("", "core playback manager is not configured")
	}
	matched, err := coreManager.PauseIfAdapterRef(ref)
	if err != nil {
		return playbackError("", "failed to pause stream playback")
	}
	if !matched {
		return playbackError("", "streams does not own the active core session")
	}
	return nil
}

func (a *Adapter) StopQueue(ctx context.Context) error {
	_ = ctx
	return a.stopActiveQueue()
}

func (a *Adapter) StopQueueGuarded(ctx context.Context, ref string, generation uint64) error {
	_ = ctx
	return a.stopActiveQueueGuarded(ref, generation)
}

func (a *Adapter) makeOnStop(capture queueCapture) func(string) {
	return func(reason string) {
		var next queueVersion
		var shouldPlay bool
		ctx := context.Background()

		a.mu.Lock()
		q := a.active
		if !capture.matches(q) {
			a.mu.Unlock()
			return
		}
		if (reason == "preempted" || reason == "stopped") && a.consumeExpectedStopLocked(capture) {
			a.mu.Unlock()
			return
		}

		switch reason {
		case "eof":
			if !q.advanceNext(a.rng) {
				a.clearActiveLocked()
			} else {
				q.Generation++
				next = queueVersionOf(q)
				q.Failures = nil
				shouldPlay = true
			}
		case "preempted", "stopped":
			a.clearActiveLocked()
		case "error":
			q.Failures = append(q.Failures, ItemFailure{
				ItemID: capture.ItemID,
				Reason: "session stopped with error",
				At:     time.Now(),
			})
			maxFailures := a.cfg.MaxConsecutiveFailures
			if maxFailures < 1 {
				maxFailures = 1
			}
			if len(q.Failures) >= maxFailures || !q.advanceNext(a.rng) {
				a.setStateLocked(adapters.StateError, "streams playback failed")
				a.clearActiveLocked()
			} else {
				q.Generation++
				next = queueVersionOf(q)
				shouldPlay = true
			}
		}
		if shouldPlay && a.loopCtx != nil {
			ctx = a.loopCtx
		}
		a.mu.Unlock()

		if shouldPlay {
			a.runBeforeQueueContinuation()
			_, _ = a.playCurrentGuarded(ctx, next)
		}
	}
}

func (a *Adapter) prepareManualStart(canMove, mutator func(*ActiveQueue) bool) error {
	_, err := a.prepareManualStartWithStop(a.stopPreviousOwnedCoreForStart, canMove, mutator)
	return err
}

type stopPreviousCoreFunc func(canProceed func(*ActiveQueue) bool, bumpGeneration bool, requireOwned bool) (queueVersion, error)

func (a *Adapter) stopPreviousOwnedCoreForStart(canProceed func(*ActiveQueue) bool, bumpGeneration bool, requireOwned bool) (queueVersion, error) {
	return queueVersion{}, a.stopPreviousOwnedCore(canProceed, bumpGeneration, requireOwned)
}

func (a *Adapter) prepareManualStartWithStop(stopPrevious stopPreviousCoreFunc, canMove, mutator func(*ActiveQueue) bool) (queueVersion, error) {
	expected, err := stopPrevious(canMove, true, true)
	if err != nil {
		return queueVersion{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		return queueVersion{}, playbackError("", "no active streams queue")
	}
	if !canMove(a.active) {
		return queueVersion{}, playbackError(a.active.ProviderID, "queue has no next item")
	}
	if expected.SessionID != "" && !expected.matches(a.active) {
		return queueVersion{}, playbackError(a.active.ProviderID, "active session changed")
	}
	if a.active.cancelResolve != nil {
		a.active.cancelResolve()
		a.active.cancelResolve = nil
	}
	a.active.Failures = nil
	if !mutator(a.active) {
		return queueVersion{}, playbackError(a.active.ProviderID, "queue has no next item")
	}
	return queueVersionOf(a.active), nil
}

func (a *Adapter) prepareReplayStart() (queueVersion, error) {
	return a.prepareReplayStartWithStop(a.stopPreviousOwnedCoreForStart)
}

func (a *Adapter) prepareReplayStartGuarded(ref string, generation uint64) (queueVersion, error) {
	return a.prepareReplayStartWithStop(func(canProceed func(*ActiveQueue) bool, bumpGeneration bool, requireOwned bool) (queueVersion, error) {
		return a.stopPreviousOwnedCoreGuarded(ref, generation, canProceed, bumpGeneration, requireOwned)
	})
}

func (a *Adapter) prepareReplayStartWithStop(stopPrevious stopPreviousCoreFunc) (queueVersion, error) {
	a.mu.Lock()
	q := a.active
	if q == nil {
		a.mu.Unlock()
		return queueVersion{}, playbackError("", "no active streams queue")
	}
	providerID := q.ProviderID
	channelID := q.ChannelID
	capturedSessionID := q.SessionID
	capturedGeneration := q.Generation
	useSnapshot := channelID == reservedAdhocID
	if !useSnapshot {
		cat, ok := a.catalogs[providerID]
		if !ok {
			a.mu.Unlock()
			return queueVersion{}, playbackError(providerID, "provider is not cataloged")
		}
		ch := cat.Channel(channelID)
		if ch == nil {
			a.mu.Unlock()
			return queueVersion{}, playbackError(providerID, "active channel is not in latest catalog")
		}
		if len(ch.Items) == 0 {
			a.mu.Unlock()
			return queueVersion{}, playbackError(providerID, "active channel has no playable items")
		}
	}
	a.mu.Unlock()

	if useSnapshot {
		return a.prepareManualStartWithStop(stopPrevious, func(q *ActiveQueue) bool {
			return q != nil && len(q.Items) > 0
		}, func(q *ActiveQueue) bool {
			q.resetForReplay(a.rng)
			return true
		})
	}

	_, err := stopPrevious(func(q *ActiveQueue) bool {
		return q != nil &&
			q.ProviderID == providerID &&
			q.ChannelID == channelID &&
			q.SessionID == capturedSessionID &&
			q.Generation == capturedGeneration
	}, true, true)
	if err != nil {
		return queueVersion{}, err
	}

	if a.beforeReplayReplace != nil {
		a.beforeReplayReplace()
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		return queueVersion{}, playbackError("", "no active streams queue")
	}
	if a.active.ProviderID != providerID ||
		a.active.ChannelID != channelID ||
		a.active.SessionID != capturedSessionID ||
		a.active.Generation != capturedGeneration+1 {
		return queueVersion{}, playbackError(providerID, "stream replay was superseded")
	}
	cat, ok := a.catalogs[providerID]
	if !ok {
		return queueVersion{}, playbackError(providerID, "provider is not cataloged")
	}
	ch := cat.Channel(channelID)
	if ch == nil {
		return queueVersion{}, playbackError(providerID, "active channel is not in latest catalog")
	}
	channel := *ch
	providerName := providerDisplayName(cat, a.definitions[providerID])
	newQueue, err := buildQueue(providerID, channel, a.rng)
	if err != nil {
		return queueVersion{}, playbackError(providerID, err.Error())
	}
	newQueue.ProviderName = providerName
	newQueue.SessionID = newQueueSessionID()
	newQueue.StartedAt = time.Now()
	a.active = newQueue
	return queueVersionOf(a.active), nil
}

func (a *Adapter) stopActiveQueue() error {
	a.mu.Lock()
	q := a.active
	if q == nil {
		a.mu.Unlock()
		return playbackError("", "no active streams queue")
	}
	providerID := q.ProviderID
	item, ok := q.currentItem()
	if !ok {
		a.clearActiveLocked()
		a.mu.Unlock()
		return playbackError(providerID, "active streams queue is empty")
	}
	capture := queueCapture{
		Generation: q.Generation,
		ItemToken:  q.ItemToken,
		SessionID:  q.SessionID,
		ItemID:     itemIdentity(item),
		AdapterRef: activeAdapterRef(q),
	}
	coreManager := a.core
	hasInFlightResolve := a.active.cancelResolve != nil
	directInFlightStart := hasInFlightResolve && item.Direct
	if hasInFlightResolve && !directInFlightStart {
		a.clearActiveLocked()
	}
	a.mu.Unlock()

	if hasInFlightResolve && !directInFlightStart {
		return nil
	}
	if coreManager == nil || capture.AdapterRef == "" {
		return playbackError(providerID, "streams does not own the active core session")
	}
	if !directInFlightStart && coreManager.Status().AdapterRef != capture.AdapterRef {
		return playbackError(providerID, "streams does not own the active core session")
	}

	a.clearQueueIfCurrent(capture)

	if a.beforeStopQueuePlaybackLock != nil {
		a.beforeStopQueuePlaybackLock(capture)
	}

	a.playbackMu.Lock()
	matched, err := coreManager.StopIfAdapterRef(capture.AdapterRef)
	a.playbackMu.Unlock()
	if err != nil {
		return playbackError("", "failed to stop stream playback")
	}
	if !matched {
		if directInFlightStart {
			return nil
		}
		a.restoreQueueIfIdle(q)
		return playbackError(providerID, "streams does not own the active core session")
	}
	return nil
}

func (a *Adapter) stopActiveQueueGuarded(ref string, generation uint64) error {
	a.mu.Lock()
	q := a.active
	if q == nil {
		a.mu.Unlock()
		return playbackError("", "no active streams queue")
	}
	providerID := q.ProviderID
	item, ok := q.currentItem()
	if !ok {
		a.clearActiveLocked()
		a.mu.Unlock()
		return playbackError(providerID, "active streams queue is empty")
	}
	capture := queueCapture{
		Generation: q.Generation,
		ItemToken:  q.ItemToken,
		SessionID:  q.SessionID,
		ItemID:     itemIdentity(item),
		AdapterRef: activeAdapterRef(q),
	}
	coreManager := a.core
	hasInFlightResolve := a.active.cancelResolve != nil
	directInFlightStart := hasInFlightResolve && item.Direct
	if hasInFlightResolve && !directInFlightStart {
		a.clearActiveLocked()
	}
	a.mu.Unlock()

	if capture.AdapterRef != ref {
		if hasInFlightResolve && !directInFlightStart {
			a.restoreQueueIfIdle(q)
		}
		return playbackError(providerID, "active session changed")
	}
	if hasInFlightResolve && !directInFlightStart {
		return nil
	}
	if coreManager == nil || ref == "" {
		return playbackError(providerID, "streams does not own the active core session")
	}
	if !directInFlightStart {
		status := coreManager.Status()
		if status.AdapterRef != ref || status.Generation != generation {
			return playbackError(providerID, "active session changed")
		}
	}

	a.clearQueueIfCurrent(capture)

	if a.beforeStopQueuePlaybackLock != nil {
		a.beforeStopQueuePlaybackLock(capture)
	}

	a.playbackMu.Lock()
	matched, err := coreManager.StopIfSession(ref, generation)
	a.playbackMu.Unlock()
	if err != nil {
		return playbackError("", "failed to stop stream playback")
	}
	if !matched {
		if directInFlightStart {
			return nil
		}
		a.restoreQueueIfIdle(q)
		return playbackError(providerID, "active session changed")
	}
	return nil
}

func (a *Adapter) restoreQueueIfIdle(q *ActiveQueue) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		a.active = q
	}
}

func (a *Adapter) clearActiveLocked() {
	if a.active == nil {
		return
	}
	if a.active.cancelResolve != nil {
		a.active.cancelResolve()
		a.active.cancelResolve = nil
	}
	a.active = nil
	a.expectedStops = nil
}

func (a *Adapter) captureStillActive(capture queueCapture) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return capture.matches(a.active)
}

func (a *Adapter) clearResolveIfCurrent(capture queueCapture) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if capture.matches(a.active) && a.active.cancelResolve != nil {
		a.active.cancelResolve = nil
	}
}

func (a *Adapter) clearQueueIfCurrent(capture queueCapture) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if capture.matches(a.active) {
		a.clearActiveLocked()
	}
}

func (a *Adapter) markExpectedStopIfCurrent(capture queueCapture) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !capture.matches(a.active) {
		return false
	}
	if a.expectedStops == nil {
		a.expectedStops = map[queueCapture]struct{}{}
	}
	a.expectedStops[capture] = struct{}{}
	return true
}

func (a *Adapter) clearExpectedStop(capture queueCapture) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.expectedStops == nil {
		return
	}
	delete(a.expectedStops, capture)
}

func (a *Adapter) consumeExpectedStopLocked(capture queueCapture) bool {
	if a.expectedStops == nil {
		return false
	}
	if _, ok := a.expectedStops[capture]; !ok {
		return false
	}
	delete(a.expectedStops, capture)
	return true
}

func (a *Adapter) recordStartFailureAndAdvance(capture queueCapture, reason string) (queueVersion, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	q := a.active
	if !capture.matches(q) {
		return queueVersion{}, false
	}
	q.cancelResolve = nil
	q.Failures = append(q.Failures, ItemFailure{
		ItemID: capture.ItemID,
		Reason: reason,
		At:     time.Now(),
	})
	maxFailures := a.cfg.MaxConsecutiveFailures
	if maxFailures < 1 {
		maxFailures = 1
	}
	if len(q.Failures) >= maxFailures || !q.advanceNext(a.rng) {
		a.setStateLocked(adapters.StateError, "streams playback failed")
		a.clearActiveLocked()
		return queueVersion{}, false
	}
	q.Generation++
	return queueVersionOf(q), true
}

func (a *Adapter) runBeforeQueueContinuation() {
	if a.beforeQueueContinuation != nil {
		a.beforeQueueContinuation()
	}
}

func (a *Adapter) stopPreviousOwnedCore(canProceed func(*ActiveQueue) bool, bumpGeneration bool, requireOwned bool) error {
	a.mu.Lock()
	q := a.active
	if q == nil {
		a.mu.Unlock()
		return nil
	}
	if canProceed != nil && !canProceed(q) {
		providerID := q.ProviderID
		a.mu.Unlock()
		return playbackError(providerID, "queue has no next item")
	}
	ref := activeAdapterRef(q)
	providerID := q.ProviderID
	coreManager := a.core
	hasInFlightResolve := q.cancelResolve != nil
	a.mu.Unlock()

	if coreManager == nil || ref == "" {
		if requireOwned && !hasInFlightResolve {
			return playbackError(providerID, "streams does not own the active core session")
		}
		a.cancelAndBumpQueueIfCurrent(q, bumpGeneration)
		return nil
	}
	if requireOwned && !hasInFlightResolve && coreManager.Status().AdapterRef != ref {
		return playbackError(providerID, "streams does not own the active core session")
	}

	a.cancelAndBumpQueueIfCurrent(q, bumpGeneration)

	a.playbackMu.Lock()
	matched, err := coreManager.StopIfAdapterRef(ref)
	a.playbackMu.Unlock()
	if err != nil {
		return playbackError(providerID, "failed to stop previous stream playback")
	}
	if requireOwned && !matched && !hasInFlightResolve {
		return playbackError(providerID, "streams does not own the active core session")
	}
	return nil
}

func (a *Adapter) stopPreviousOwnedCoreGuarded(ref string, generation uint64, canProceed func(*ActiveQueue) bool, bumpGeneration bool, requireOwned bool) (queueVersion, error) {
	a.mu.Lock()
	q := a.active
	if q == nil {
		a.mu.Unlock()
		return queueVersion{}, nil
	}
	if canProceed != nil && !canProceed(q) {
		providerID := q.ProviderID
		a.mu.Unlock()
		return queueVersion{}, playbackError(providerID, "queue has no next item")
	}
	item, ok := q.currentItem()
	if !ok {
		providerID := q.ProviderID
		a.mu.Unlock()
		return queueVersion{}, playbackError(providerID, "active streams queue is empty")
	}
	activeRef := activeAdapterRef(q)
	capture := queueCapture{
		Generation: q.Generation,
		ItemToken:  q.ItemToken,
		SessionID:  q.SessionID,
		ItemID:     itemIdentity(item),
		AdapterRef: activeRef,
	}
	providerID := q.ProviderID
	coreManager := a.core
	hasInFlightResolve := q.cancelResolve != nil
	a.mu.Unlock()

	if activeRef != ref {
		return queueVersion{}, playbackError(providerID, "active session changed")
	}
	if coreManager == nil || ref == "" {
		if requireOwned && !hasInFlightResolve {
			return queueVersion{}, playbackError(providerID, "streams does not own the active core session")
		}
		a.cancelAndBumpQueueIfCurrent(q, bumpGeneration)
		return queueVersionOf(q), nil
	}

	if !a.markExpectedStopIfCurrent(capture) {
		return queueVersion{}, playbackError(providerID, "active session changed")
	}

	a.playbackMu.Lock()
	matched, err := coreManager.StopIfSession(ref, generation)
	a.playbackMu.Unlock()
	if err != nil {
		a.clearExpectedStop(capture)
		return queueVersion{}, playbackError(providerID, "failed to stop previous stream playback")
	}
	if !matched {
		a.clearExpectedStop(capture)
		return queueVersion{}, playbackError(providerID, "active session changed")
	}
	a.cancelAndBumpQueueIfCurrent(q, bumpGeneration)
	return queueVersionOf(q), nil
}

func (a *Adapter) cancelAndBumpQueueIfCurrent(q *ActiveQueue, bumpGeneration bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active != q {
		return
	}
	if q.cancelResolve != nil {
		q.cancelResolve()
		q.cancelResolve = nil
	}
	if bumpGeneration {
		q.Generation++
	}
}

func (a *Adapter) setStateLocked(state adapters.State, errMsg string) {
	a.state = state
	a.lastErr = errMsg
	a.stateSince = time.Now()
}

func (a *Adapter) markPlaybackRunningLocked() {
	if a.state == adapters.StateStopped {
		return
	}
	if a.state == adapters.StateRunning && a.lastErr == "" {
		return
	}
	a.setStateLocked(adapters.StateRunning, "")
}

func playbackError(providerID, message string) *StreamsError {
	if providerID != "" {
		message = fmt.Sprintf("streams provider %q: %s", providerID, message)
	}
	return &StreamsError{Kind: ErrKindPlayback, Message: message}
}

func providerDisplayName(cat ProviderCatalog, def ProviderDefinition) string {
	if cat.Name != "" {
		return cat.Name
	}
	if def.DisplayName != "" {
		return def.DisplayName
	}
	if cat.ProviderID != "" {
		return cat.ProviderID
	}
	return def.ID
}

func findCatalogItem(cat ProviderCatalog, itemID string) (Channel, StreamItem, bool) {
	for _, ch := range cat.Channels {
		for _, item := range ch.Items {
			if streamItemMatches(item, itemID) {
				return ch, item, true
			}
		}
	}
	return Channel{}, StreamItem{}, false
}

func streamItemURL(item StreamItem) string {
	if item.URL != "" {
		return item.URL
	}
	id := item.SourceID
	if id == "" {
		id = item.ID
	}
	if id == "" {
		return ""
	}
	return youtubeWatchURL(id)
}

func youtubeWatchURL(id string) string {
	return "https://www.youtube.com/watch?v=" + id
}

func queueAdapterRef(q *ActiveQueue, token uint64) string {
	return fmt.Sprintf("streams:%s:%s:%s:%d", q.ProviderID, q.ChannelID, q.SessionID, token)
}

func activeAdapterRef(q *ActiveQueue) string {
	if q == nil || q.ItemToken == 0 {
		return ""
	}
	return queueAdapterRef(q, q.ItemToken)
}

func newQueueSessionID() string {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return fmt.Sprintf("streams-session-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
