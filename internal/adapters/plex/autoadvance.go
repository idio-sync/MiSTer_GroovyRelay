package plex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

// autoAdvanceEOFReason is the OnStop reason core.Manager sets on a clean
// ffmpeg exit. Only this reason triggers an advance.
const autoAdvanceEOFReason = "eof"

const autoAdvanceSettleDelay = 1 * time.Second

var errNoNextQueueItem = errors.New("play queue item not found")

func (c *Companion) withAutoAdvance(captured PlayMediaRequest, inner func(string)) func(string) {
	return func(reason string) {
		if inner != nil {
			inner(reason)
		}
		if reason != autoAdvanceEOFReason || !c.autoAdvance.Load() {
			return
		}
		go c.advanceAfterEOF(captured)
	}
}

func (c *Companion) advanceAfterEOF(captured PlayMediaRequest) {
	if c.core == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	next, err := c.resolveNextQueueItem(ctx, captured, func(items []playQueueItem, cur PlayMediaRequest) (playQueueItem, bool) {
		return nextPlayQueueItem(items, cur.PlayQueueItemID, cur.MediaKey, 1)
	})
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

	started, err := c.core.StartSessionIfIdle(req)
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
	c.emit(eventlog.SeverityInfo, fmt.Sprintf("auto-advance %s", req.AdapterRef))
	c.rememberPlaySession(next)
	c.notifyTimeline()
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
