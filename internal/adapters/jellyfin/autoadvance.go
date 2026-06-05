package jellyfin

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/artworkcache"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

const (
	autoAdvanceStopReason  = "eof"
	autoAdvanceSettleDelay = 1 * time.Second
)

func (a *Adapter) autoAdvanceEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.AutoAdvance
}

func (a *Adapter) withAutoAdvance(refKey string, base func(string)) func(string) {
	return func(reason string) {
		if base != nil {
			base(reason)
		}
		if reason != autoAdvanceStopReason {
			return
		}
		a.bgStarts.Add(1)
		go func() {
			defer a.bgStarts.Done()
			a.advanceAfterEOF(refKey)
		}()
	}
}

func (a *Adapter) advanceAfterEOF(refKey string) {
	delay := a.autoAdvanceDelay
	if delay > 0 {
		time.Sleep(delay)
	}
	if !a.autoAdvanceEnabled() {
		return
	}
	if a.core == nil {
		return
	}
	if st := a.core.Status(); st.State != core.StateIdle {
		return
	}
	if a.snapshotCurrentRefKey() != refKey {
		return
	}
	controllerEpoch, ok := a.autoAdvanceControllerEpoch()
	if !ok {
		return
	}
	if _, ok := a.peekQueueHead(); !ok {
		slog.Debug("jellyfin: auto-advance reached end of queue")
		return
	}
	a.startQueuedItemAfterEOF(refKey, controllerEpoch)
}

func (a *Adapter) peekQueueHead() (QueuedItem, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) == 0 {
		return QueuedItem{}, false
	}
	return a.queue[0], true
}

func sameQueueEntry(a, b QueuedItem) bool {
	return a.QueueEntryID != 0 && a.QueueEntryID == b.QueueEntryID
}

func (a *Adapter) queueHeadStillMatches(qi QueuedItem) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.queue) > 0 && sameQueueEntry(a.queue[0], qi)
}

func (a *Adapter) commitAutoAdvance(stoppedRef string, nextRef string, started QueuedItem, controllerEpoch uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pendingControllerStarts > 0 || a.controllerStartEpoch != controllerEpoch {
		return false
	}
	if a.currentRefKey != stoppedRef {
		return false
	}
	if len(a.queue) == 0 || !sameQueueEntry(a.queue[0], started) {
		return false
	}
	a.currentRefKey = nextRef
	a.pendingRollback = ""
	a.queue = a.queue[1:]
	return true
}

func (a *Adapter) rollbackAutoAdvanceCommit(stoppedRef string, nextRef string, started QueuedItem) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRefKey != nextRef {
		return false
	}
	a.currentRefKey = stoppedRef
	a.pendingRollback = ""
	for _, qi := range a.queue {
		if sameQueueEntry(qi, started) {
			return true
		}
	}
	a.queue = append([]QueuedItem{started}, a.queue...)
	return true
}

func (a *Adapter) startQueuedItemAfterEOF(stoppedRef string, controllerEpoch uint64) {
	qi, ok := a.peekQueueHead()
	if !ok {
		slog.Debug("jellyfin: auto-advance reached end of queue")
		return
	}

	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	tok, err := LoadToken(a.tokenPath())
	if err != nil || tok.AccessToken == "" {
		slog.Error("jellyfin: auto-advance: no token", "err", err)
		return
	}
	preset, err := a.currentPreset()
	if err != nil {
		slog.Error("jellyfin: auto-advance: modeline", "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	meta := a.fetchItemMetadataBestEffort(ctx, cfg, tok, qi.ItemID)
	info, err := FetchPlaybackInfo(ctx, PlaybackInfoInput{
		ServerURL:           cfg.ServerURL,
		Token:               tok.AccessToken,
		DeviceID:            a.deviceID,
		DeviceName:          cfg.DeviceName,
		Version:             linkVersion,
		ItemID:              qi.ItemID,
		UserID:              tok.UserID,
		MaxVideoBitrateKbps: cfg.MaxVideoBitrateKbps,
		Preset:              preset,
		StartPositionTicks:  qi.StartPositionTicks,
		MediaSourceID:       qi.MediaSourceID,
		AudioStreamIndex:    qi.AudioStreamIndex,
		SubtitleStreamIndex: qi.SubtitleStreamIndex,
		MediaKind:           meta.MediaKind,
	})
	if err != nil {
		artworkcache.Remove(meta.ArtworkPath)
		slog.Error("jellyfin: auto-advance PlaybackInfo failed", "err", err)
		return
	}
	info = mergePlaybackMetadata(info, meta)
	req := a.buildSessionRequest(playRequestInput{
		ItemID:             qi.ItemID,
		StartPositionTicks: qi.StartPositionTicks,
		PlayInfo:           info,
		ServerURL:          cfg.ServerURL,
		Token:              tok.AccessToken,
	})

	if a.core == nil {
		cleanupSessionArtwork(req)
		slog.Error("jellyfin: auto-advance: no core SessionManager")
		return
	}
	if st := a.core.Status(); st.State != core.StateIdle {
		cleanupSessionArtwork(req)
		return
	}
	if a.snapshotCurrentRefKey() != stoppedRef {
		cleanupSessionArtwork(req)
		return
	}
	if !a.queueHeadStillMatches(qi) {
		cleanupSessionArtwork(req)
		return
	}
	if a.controllerStartBlocksAutoAdvance(controllerEpoch) {
		cleanupSessionArtwork(req)
		return
	}

	startedStatus, started, err := a.core.StartSessionIfIdleSnapshot(req)
	if err != nil {
		cleanupSessionArtwork(req)
		slog.Error("jellyfin: auto-advance StartSessionIfIdle failed", "err", err)
		return
	}
	if !started {
		cleanupSessionArtwork(req)
		return
	}

	if a.beforeAutoAdvanceCommit != nil {
		a.beforeAutoAdvanceCommit()
	}
	if !sameCoreSession(a.core.Status(), startedStatus) {
		cleanupSessionArtwork(req)
		a.stopStaleAutoAdvanceSession(startedStatus)
		return
	}
	if !a.commitAutoAdvance(stoppedRef, req.AdapterRef, qi, controllerEpoch) {
		cleanupSessionArtwork(req)
		a.stopStaleAutoAdvanceSession(startedStatus)
		return
	}
	if a.afterAutoAdvanceCommit != nil {
		a.afterAutoAdvanceCommit()
	}
	if !sameCoreSession(a.core.Status(), startedStatus) {
		cleanupSessionArtwork(req)
		a.rollbackAutoAdvanceCommit(stoppedRef, req.AdapterRef, qi)
		a.stopStaleAutoAdvanceSession(startedStatus)
		return
	}

	a.emitEvent(eventlog.SeverityInfo, fmt.Sprintf("auto-advance %s", req.AdapterRef))
	a.recordCompanionHistory(qi.ItemID, info, time.Now())
	a.spawnReporter(reporterParams{
		ItemID:          qi.ItemID,
		PlaySessionID:   info.PlaySessionID,
		MediaSourceID:   info.MediaSourceID,
		AudioIdx:        qi.AudioStreamIndex,
		SubtitleIdx:     qi.SubtitleStreamIndex,
		NowPlayingQueue: a.snapshotNowPlayingQueue(qi.ItemID),
		Auth: RESTAuth{
			ServerURL: cfg.ServerURL, Token: tok.AccessToken,
			DeviceID: a.deviceID, DeviceName: cfg.DeviceName,
			Version: linkVersion,
		},
	})
}

func sameCoreSession(a, b core.SessionStatus) bool {
	return a.AdapterRef != "" && a.AdapterRef == b.AdapterRef && a.Generation != 0 && a.Generation == b.Generation
}

func (a *Adapter) stopStaleAutoAdvanceSession(started core.SessionStatus) {
	if stopped, stopErr := a.core.StopIfSession(started.AdapterRef, started.Generation); stopErr != nil {
		slog.Warn("jellyfin: auto-advance stale started session stop failed", "ref", started.AdapterRef, "generation", started.Generation, "err", stopErr)
	} else if stopped {
		slog.Debug("jellyfin: auto-advance stopped stale started session", "ref", started.AdapterRef, "generation", started.Generation)
	} else {
		slog.Debug("jellyfin: auto-advance stale started session no longer active", "ref", started.AdapterRef, "generation", started.Generation)
	}
}
