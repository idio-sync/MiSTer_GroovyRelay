package plex

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const autoAdvanceSettleDelay = 1 * time.Second

var errNoNextQueueItem = errors.New("play queue item not found")

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
