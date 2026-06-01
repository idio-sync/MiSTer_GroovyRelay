package plex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

// autoAdvanceEOFReason is the OnStop reason core.Manager sets on a clean
// ffmpeg exit. Only this reason triggers an advance.
const autoAdvanceEOFReason = "eof"

const autoAdvanceSettleDelay = 1 * time.Second

var errNoNextQueueItem = errors.New("play queue item not found")

type autoAdvanceSession struct {
	transcodeSessionID string
}

func (c *Companion) withAutoAdvance(captured PlayMediaRequest, inner func(string)) func(string) {
	return func(reason string) {
		epoch := c.autoAdvanceEpoch.Load()
		current := c.autoAdvanceRequestIsCurrent(captured)
		shouldAdvance := reason == autoAdvanceEOFReason && current && c.autoAdvanceStillCurrent(epoch)
		if inner != nil {
			inner(reason)
		}
		if !shouldAdvance {
			return
		}
		go c.advanceAfterEOFAtEpoch(captured, epoch)
	}
}

func (c *Companion) advanceAfterEOF(captured PlayMediaRequest) {
	c.advanceAfterEOFAtEpoch(captured, c.autoAdvanceEpoch.Load())
}

func (c *Companion) advanceAfterEOFAtEpoch(captured PlayMediaRequest, epoch uint64) {
	if c.core == nil {
		return
	}
	if !c.autoAdvanceStillCurrent(epoch) {
		slog.Debug("plex auto-advance: stood down before resolve, request is stale", "key", captured.MediaKey)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	next, err := c.resolveNextQueueItem(ctx, captured, func(items []playQueueItem, cur PlayMediaRequest) (playQueueItem, bool) {
		return nextPlayQueueItem(items, cur.PlayQueueItemID, cur.MediaKey, 1)
	})
	if !c.autoAdvanceStillCurrent(epoch) {
		slog.Debug("plex auto-advance: stood down after resolve, request is stale", "key", captured.MediaKey)
		return
	}
	if err != nil {
		if errors.Is(err, errNoNextQueueItem) {
			slog.Info("plex auto-advance: end of queue", "key", captured.MediaKey)
			c.emit(eventlog.SeverityInfo, "auto-advance end of queue")
		} else {
			slog.Warn("plex auto-advance: resolve next item failed", "key", captured.MediaKey, "err", err)
			c.emit(eventlog.SeverityWarn, fmt.Sprintf("auto-advance resolve failed: %v", err))
		}
		return
	}

	preset, err := c.currentPreset()
	if err != nil {
		slog.Warn("plex auto-advance: resolve preset failed", "err", err)
		c.emit(eventlog.SeverityWarn, fmt.Sprintf("auto-advance preset failed: %v", err))
		return
	}
	req := c.sessionRequestForPlay(ctx, next, preset)

	if c.autoAdvanceDelay > 0 {
		time.Sleep(c.autoAdvanceDelay)
	}
	if !c.autoAdvanceStillCurrent(epoch) {
		cleanupSessionArtwork(req)
		slog.Debug("plex auto-advance: stood down, request is stale", "key", next.MediaKey)
		return
	}

	startedStatus, started, err := c.core.StartSessionIfIdleSnapshot(req)
	if err != nil {
		cleanupSessionArtwork(req)
		slog.Warn("plex auto-advance: start failed", "key", next.MediaKey, "err", err)
		c.emit(eventlog.SeverityWarn, fmt.Sprintf("auto-advance start failed: %v", err))
		return
	}
	if !started {
		cleanupSessionArtwork(req)
		slog.Debug("plex auto-advance: stood down, session no longer idle", "key", next.MediaKey)
		return
	}
	if !c.rememberAutoAdvanceSessionIfCurrent(startedStatus, next, epoch) {
		cleanupSessionArtwork(req)
		if stopped, stopErr := c.core.StopIfSession(startedStatus.AdapterRef, startedStatus.Generation); stopErr != nil {
			slog.Warn("plex auto-advance: stale started session stop failed", "key", next.MediaKey, "err", stopErr)
		} else if stopped {
			slog.Debug("plex auto-advance: stopped stale session after late cancellation", "key", next.MediaKey)
		} else {
			slog.Debug("plex auto-advance: stood down, started session is no longer active", "key", next.MediaKey)
		}
		return
	}
	c.emit(eventlog.SeverityInfo, fmt.Sprintf("auto-advance %s", req.AdapterRef))
	c.notifyTimeline()
}

func (c *Companion) beginAutoAdvanceIntent() func() {
	c.autoAdvanceMu.Lock()
	defer c.autoAdvanceMu.Unlock()
	c.autoAdvanceIntents.Add(1)
	c.cancelPendingAutoAdvanceLocked()
	return func() {
		c.autoAdvanceMu.Lock()
		defer c.autoAdvanceMu.Unlock()
		c.autoAdvanceIntents.Add(-1)
	}
}

func (c *Companion) cancelPendingAutoAdvance() uint64 {
	c.autoAdvanceMu.Lock()
	defer c.autoAdvanceMu.Unlock()
	return c.cancelPendingAutoAdvanceLocked()
}

func (c *Companion) cancelPendingAutoAdvanceLocked() uint64 {
	return c.autoAdvanceEpoch.Add(1)
}

func (c *Companion) autoAdvanceRequestIsCurrent(captured PlayMediaRequest) bool {
	current := c.autoAdvanceCurrent.Load()
	if current == nil || captured.TranscodeSessionID == "" {
		return false
	}
	if current.transcodeSessionID != captured.TranscodeSessionID {
		return false
	}
	return true
}

func (c *Companion) autoAdvanceStillCurrent(epoch uint64) bool {
	return c.autoAdvance.Load() && c.autoAdvanceEpoch.Load() == epoch && c.autoAdvanceIntents.Load() == 0
}

func (c *Companion) setAutoAdvanceCurrentSession(p PlayMediaRequest) {
	if p.TranscodeSessionID == "" {
		c.autoAdvanceCurrent.Store(nil)
		return
	}
	c.autoAdvanceCurrent.Store(&autoAdvanceSession{
		transcodeSessionID: p.TranscodeSessionID,
	})
}

func (c *Companion) rememberAutoAdvanceSessionIfCurrent(started core.SessionStatus, next PlayMediaRequest, epoch uint64) bool {
	c.autoAdvanceMu.Lock()
	defer c.autoAdvanceMu.Unlock()
	if !c.autoAdvanceStillCurrent(epoch) {
		return false
	}
	st := c.core.Status()
	if !autoAdvanceStartedSessionMatches(st, started) {
		return false
	}
	c.cancelPendingAutoAdvanceLocked()
	c.sessMu.Lock()
	c.lastPlay = next
	c.setAutoAdvanceCurrentSession(next)
	c.sessMu.Unlock()

	st = c.core.Status()
	if !autoAdvanceStartedSessionMatches(st, started) {
		c.sessMu.Lock()
		if c.lastPlay.TranscodeSessionID == next.TranscodeSessionID {
			c.lastPlay = PlayMediaRequest{}
			c.autoAdvanceCurrent.Store(nil)
		}
		c.sessMu.Unlock()
		return false
	}
	return true
}

func autoAdvanceStartedSessionMatches(current, started core.SessionStatus) bool {
	return started.AdapterRef != "" &&
		started.Generation != 0 &&
		current.AdapterRef == started.AdapterRef &&
		current.Generation == started.Generation
}

func (c *Companion) resolveNextQueueItem(
	ctx context.Context,
	p PlayMediaRequest,
	selectItem func([]playQueueItem, PlayMediaRequest) (playQueueItem, bool),
) (PlayMediaRequest, error) {
	if p.MediaKey == "" {
		return PlayMediaRequest{}, fmt.Errorf("no plex session")
	}
	if p.ContainerKey == "" {
		return PlayMediaRequest{}, errNoNextQueueItem
	}
	pq, err := c.fetchPlayQueue(ctx, p)
	if err != nil {
		return PlayMediaRequest{}, err
	}
	item, ok := selectItem(pq.Items, p)
	if !ok {
		return PlayMediaRequest{}, errNoNextQueueItem
	}
	key := item.Key
	if key == "" && item.RatingKey != "" {
		key = "/library/metadata/" + item.RatingKey
	}
	if key == "" {
		return PlayMediaRequest{}, fmt.Errorf("play queue item has no media key")
	}
	p.MediaKey = key
	p.Title = ""
	p.PlayQueueItemID = item.PlayQueueItemID
	p.PlayQueueID = firstNonEmpty(p.PlayQueueID, pq.PlayQueueID)
	p.PlayQueueVersion = firstNonEmpty(p.PlayQueueVersion, pq.PlayQueueVersion)
	p.OffsetMs = 0
	p.TranscodeSessionID = NewTranscodeSessionID()
	return p, nil
}
