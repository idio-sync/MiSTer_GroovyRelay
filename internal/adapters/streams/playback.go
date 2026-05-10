package streams

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func (a *Adapter) StartResolvedStream(ctx context.Context, res streamhandoff.Resolution) (streamhandoff.StartResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	q, err := a.queueFromResolution(res)
	if err != nil {
		return streamhandoff.StartResult{}, err
	}

	if err := a.stopPreviousOwnedCore(nil, true, false); err != nil {
		return streamhandoff.StartResult{}, err
	}

	a.mu.Lock()
	if a.active != nil && a.active.cancelResolve != nil {
		a.active.cancelResolve()
		a.active.cancelResolve = nil
	}
	a.active = q
	a.mu.Unlock()

	started, err := a.playCurrent(ctx)
	if err != nil {
		return streamhandoff.StartResult{}, err
	}
	if res.ItemID == "" {
		started.ItemID = ""
	} else {
		started.ItemID = res.ItemID
	}
	return started, nil
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

func (a *Adapter) playCurrent(ctx context.Context) (streamhandoff.StartResult, error) {
	return a.playCurrentGuarded(ctx, queueVersion{})
}

func (a *Adapter) playCurrentGuarded(ctx context.Context, guard queueVersion) (streamhandoff.StartResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	a.mu.Lock()
	q := a.active
	if q == nil {
		a.mu.Unlock()
		return streamhandoff.StartResult{}, playbackError("", "no active streams queue")
	}
	if !guard.matches(q) {
		a.mu.Unlock()
		return streamhandoff.StartResult{}, playbackError("", "stream start was superseded")
	}
	item, ok := q.currentItem()
	if !ok {
		a.clearActiveLocked()
		a.mu.Unlock()
		return streamhandoff.StartResult{}, playbackError(q.ProviderID, "active streams queue is empty")
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
		return streamhandoff.StartResult{}, playbackError(q.ProviderID, "stream item is missing a playable URL")
	}
	if resolver == nil {
		cancel()
		a.clearResolveIfCurrent(capture)
		return streamhandoff.StartResult{}, playbackError(q.ProviderID, "stream resolver is not configured")
	}
	if coreManager == nil {
		cancel()
		a.clearResolveIfCurrent(capture)
		return streamhandoff.StartResult{}, playbackError(q.ProviderID, "core playback manager is not configured")
	}

	resolved, err := resolver.Resolve(resolveCtx, pageURL, format, cookiesPath)
	cancel()
	if err != nil {
		if next, ok := a.recordStartFailureAndAdvance(capture, "failed to resolve stream item"); ok {
			a.runBeforeQueueContinuation()
			return a.playCurrentGuarded(ctx, next)
		}
		return streamhandoff.StartResult{}, playbackError(q.ProviderID, "failed to resolve stream item")
	}
	if resolved == nil || strings.TrimSpace(resolved.URL) == "" {
		if next, ok := a.recordStartFailureAndAdvance(capture, "stream resolver returned no playable media URL"); ok {
			a.runBeforeQueueContinuation()
			return a.playCurrentGuarded(ctx, next)
		}
		return streamhandoff.StartResult{}, playbackError(q.ProviderID, "stream resolver returned no playable media URL")
	}

	req := core.SessionRequest{
		StreamURL:         resolved.URL,
		InputHeaders:      resolved.Headers,
		AudioStreamURL:    resolved.AudioURL,
		AudioInputHeaders: resolved.AudioHeaders,
		AdapterRef:        ref,
		DirectPlay:        true,
		Capabilities:      core.Capabilities{CanPause: true, CanSeek: false},
		OnStop:            a.makeOnStop(capture),
	}
	a.playbackMu.Lock()
	if !a.captureStillActive(capture) {
		a.playbackMu.Unlock()
		return streamhandoff.StartResult{}, playbackError(q.ProviderID, "stream start was superseded")
	}
	if err := coreManager.StartSession(req); err != nil {
		a.playbackMu.Unlock()
		if next, ok := a.recordStartFailureAndAdvance(capture, "failed to start stream playback"); ok {
			a.runBeforeQueueContinuation()
			return a.playCurrentGuarded(ctx, next)
		}
		return streamhandoff.StartResult{}, playbackError(q.ProviderID, "failed to start stream playback")
	}
	a.playbackMu.Unlock()

	now := time.Now()
	a.mu.Lock()
	if capture.matches(a.active) {
		a.active.LastResolvedAt = now
		a.active.cancelResolve = nil
	}
	a.mu.Unlock()

	return streamhandoff.StartResult{
		AdapterRef: ref,
		ProviderID: q.ProviderID,
		ChannelID:  q.ChannelID,
		ItemID:     itemIdentity(item),
	}, nil
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

func (a *Adapter) Replay(ctx context.Context) error {
	if err := a.prepareReplayStart(); err != nil {
		return err
	}
	_, err := a.playCurrent(ctx)
	return err
}

func (a *Adapter) Pause(ctx context.Context) error {
	_ = ctx
	a.mu.Lock()
	ref := activeAdapterRef(a.active)
	coreManager := a.core
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
	if err := a.stopPreviousOwnedCore(canMove, true, true); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		return playbackError("", "no active streams queue")
	}
	if !canMove(a.active) {
		return playbackError(a.active.ProviderID, "queue has no next item")
	}
	if a.active.cancelResolve != nil {
		a.active.cancelResolve()
		a.active.cancelResolve = nil
	}
	a.active.Failures = nil
	if !mutator(a.active) {
		return playbackError(a.active.ProviderID, "queue has no next item")
	}
	return nil
}

func (a *Adapter) prepareReplayStart() error {
	a.mu.Lock()
	q := a.active
	if q == nil {
		a.mu.Unlock()
		return playbackError("", "no active streams queue")
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
			return playbackError(providerID, "provider is not cataloged")
		}
		ch := cat.Channel(channelID)
		if ch == nil {
			a.mu.Unlock()
			return playbackError(providerID, "active channel is not in latest catalog")
		}
		if len(ch.Items) == 0 {
			a.mu.Unlock()
			return playbackError(providerID, "active channel has no playable items")
		}
	}
	a.mu.Unlock()

	if useSnapshot {
		return a.prepareManualStart(func(q *ActiveQueue) bool {
			return q != nil && len(q.Items) > 0
		}, func(q *ActiveQueue) bool {
			q.resetForReplay(a.rng)
			return true
		})
	}

	if err := a.stopPreviousOwnedCore(func(q *ActiveQueue) bool {
		return q != nil &&
			q.ProviderID == providerID &&
			q.ChannelID == channelID &&
			q.SessionID == capturedSessionID &&
			q.Generation == capturedGeneration
	}, true, true); err != nil {
		return err
	}

	if a.beforeReplayReplace != nil {
		a.beforeReplayReplace()
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		return playbackError("", "no active streams queue")
	}
	if a.active.ProviderID != providerID ||
		a.active.ChannelID != channelID ||
		a.active.SessionID != capturedSessionID ||
		a.active.Generation != capturedGeneration+1 {
		return playbackError(providerID, "stream replay was superseded")
	}
	cat, ok := a.catalogs[providerID]
	if !ok {
		return playbackError(providerID, "provider is not cataloged")
	}
	ch := cat.Channel(channelID)
	if ch == nil {
		return playbackError(providerID, "active channel is not in latest catalog")
	}
	channel := *ch
	providerName := providerDisplayName(cat, a.definitions[providerID])
	newQueue, err := buildQueue(providerID, channel, a.rng)
	if err != nil {
		return playbackError(providerID, err.Error())
	}
	newQueue.ProviderName = providerName
	newQueue.SessionID = newQueueSessionID()
	newQueue.StartedAt = time.Now()
	a.active = newQueue
	return nil
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
	if hasInFlightResolve {
		a.clearActiveLocked()
	}
	a.mu.Unlock()

	if hasInFlightResolve {
		return nil
	}
	if coreManager == nil || capture.AdapterRef == "" {
		return playbackError(providerID, "streams does not own the active core session")
	}
	if coreManager.Status().AdapterRef != capture.AdapterRef {
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
		a.restoreQueueIfIdle(q)
		return playbackError(providerID, "streams does not own the active core session")
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
