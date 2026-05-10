package streams

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

type loopMode int

const (
	loopNone loopMode = iota
	loopSequential
	loopShuffle
	loopFirstThenShuffle
)

type ItemFailure struct {
	ItemID string
	Reason string
	At     time.Time
}

type ActiveQueue struct {
	SessionID      string
	ProviderID     string
	ProviderName   string
	ChannelID      string
	ChannelName    string
	Items          []StreamItem
	Index          int
	Generation     uint64
	ItemToken      uint64
	Failures       []ItemFailure
	StartedAt      time.Time
	LastResolvedAt time.Time
	cancelResolve  context.CancelFunc

	loopMode  loopMode
	baseItems []StreamItem
}

type queueCapture struct {
	Generation uint64
	ItemToken  uint64
	SessionID  string
	ItemID     string
	AdapterRef string
}

func buildQueue(providerID string, ch Channel, rng *rand.Rand) (*ActiveQueue, error) {
	items := cloneStreamItems(ch.Items)
	if len(items) == 0 {
		return nil, fmt.Errorf("streams provider %q channel %q has no playable items", providerID, ch.ID)
	}

	baseItems := cloneStreamItems(items)
	mode := ch.PlayMode
	if mode == "" {
		mode = PlayShuffle
	}

	loop := loopSequential
	switch mode {
	case PlaySequential:
		loop = loopSequential
	case PlayShuffle:
		loop = loopShuffle
		shuffleItems(items, rng)
	case PlayFirstThenShuffle:
		if len(items) == 1 {
			loop = loopSequential
			break
		}
		loop = loopFirstThenShuffle
		shuffleItems(items[1:], rng)
	default:
		loop = loopSequential
	}

	return &ActiveQueue{
		ProviderID:     providerID,
		ChannelID:      ch.ID,
		ChannelName:    ch.Name,
		Items:          items,
		baseItems:      baseItems,
		loopMode:       loop,
		StartedAt:      time.Now(),
		Generation:     0,
		ItemToken:      0,
		LastResolvedAt: time.Time{},
		cancelResolve:  nil,
	}, nil
}

func buildAdhocQueue(providerID, providerName string, item StreamItem) (*ActiveQueue, error) {
	if item.ID == "" && item.SourceID == "" {
		return nil, fmt.Errorf("streams provider %q ad-hoc item is missing an id", providerID)
	}
	if item.ID == "" {
		item.ID = item.SourceID
	}
	if item.SourceID == "" {
		item.SourceID = item.ID
	}
	if item.URL == "" {
		item.URL = youtubeWatchURL(item.SourceID)
	}
	items := []StreamItem{item}
	return &ActiveQueue{
		ProviderID:     providerID,
		ProviderName:   providerName,
		ChannelID:      reservedAdhocID,
		ChannelName:    providerName + " Link",
		Items:          cloneStreamItems(items),
		baseItems:      cloneStreamItems(items),
		loopMode:       loopNone,
		StartedAt:      time.Now(),
		Generation:     0,
		ItemToken:      0,
		LastResolvedAt: time.Time{},
		cancelResolve:  nil,
	}, nil
}

func (q *ActiveQueue) currentItem() (StreamItem, bool) {
	if q == nil || len(q.Items) == 0 || q.Index < 0 || q.Index >= len(q.Items) {
		return StreamItem{}, false
	}
	return q.Items[q.Index], true
}

func (q *ActiveQueue) advanceNext(rng *rand.Rand) bool {
	if q == nil || len(q.Items) == 0 {
		return false
	}

	switch q.loopMode {
	case loopNone:
		if q.Index >= len(q.Items)-1 {
			return false
		}
		q.Index++
	case loopShuffle:
		if q.Index < len(q.Items)-1 {
			q.Index++
			return true
		}
		q.Items = cloneStreamItems(q.baseItems)
		shuffleItems(q.Items, rng)
		q.Index = 0
	case loopFirstThenShuffle:
		if len(q.Items) == 1 {
			q.Index = 0
			return true
		}
		if q.Index < len(q.Items)-1 {
			q.Index++
			return true
		}
		q.Items = cloneStreamItems(q.baseItems)
		shuffleItems(q.Items[1:], rng)
		q.Index = 1
	default:
		q.Index = (q.Index + 1) % len(q.Items)
	}
	return true
}

func (q *ActiveQueue) canAdvanceNext() bool {
	if q == nil || len(q.Items) == 0 {
		return false
	}
	if q.loopMode == loopNone {
		return q.Index < len(q.Items)-1
	}
	return true
}

func (q *ActiveQueue) advancePrevious() bool {
	if q == nil || len(q.Items) == 0 {
		return false
	}
	if q.loopMode == loopNone {
		if q.Index == 0 {
			return false
		}
		q.Index--
		return true
	}
	if q.loopMode == loopFirstThenShuffle && len(q.Items) > 1 {
		if q.Index <= 1 {
			q.Index = len(q.Items) - 1
		} else {
			q.Index--
		}
		return true
	}
	q.Index = (q.Index - 1 + len(q.Items)) % len(q.Items)
	return true
}

func (q *ActiveQueue) canAdvancePrevious() bool {
	if q == nil || len(q.Items) == 0 {
		return false
	}
	if q.loopMode == loopNone {
		return q.Index > 0
	}
	return true
}

func (q *ActiveQueue) resetForReplay(rng *rand.Rand) {
	if q == nil || len(q.Items) == 0 {
		return
	}
	if q.loopMode == loopFirstThenShuffle && len(q.baseItems) > 1 {
		q.Items = cloneStreamItems(q.baseItems)
		shuffleItems(q.Items[1:], rng)
		q.Index = 0
		return
	}
	if q.loopMode == loopShuffle && len(q.baseItems) > 0 {
		q.Items = cloneStreamItems(q.baseItems)
		shuffleItems(q.Items, rng)
		q.Index = 0
		return
	}
	q.Index = 0
}

func (q *ActiveQueue) moveItemToFront(itemID string) bool {
	if q == nil || itemID == "" {
		return false
	}
	idx := -1
	for i, item := range q.Items {
		if streamItemMatches(item, itemID) {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return idx == 0
	}
	item := q.Items[idx]
	copy(q.Items[1:idx+1], q.Items[:idx])
	q.Items[0] = item
	q.baseItems = cloneStreamItems(q.Items)
	q.Index = 0
	return true
}

func (c queueCapture) matches(q *ActiveQueue) bool {
	if q == nil {
		return false
	}
	item, ok := q.currentItem()
	if !ok {
		return false
	}
	return q.Generation == c.Generation &&
		q.ItemToken == c.ItemToken &&
		q.SessionID == c.SessionID &&
		itemIdentity(item) == c.ItemID &&
		(c.AdapterRef == "" || queueAdapterRef(q, q.ItemToken) == c.AdapterRef)
}

func cloneStreamItems(items []StreamItem) []StreamItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]StreamItem, len(items))
	copy(out, items)
	return out
}

func shuffleItems(items []StreamItem, rng *rand.Rand) {
	if len(items) < 2 {
		return
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	rng.Shuffle(len(items), func(i, j int) {
		items[i], items[j] = items[j], items[i]
	})
}

func itemIdentity(item StreamItem) string {
	if item.ID != "" {
		return item.ID
	}
	return item.SourceID
}

func streamItemMatches(item StreamItem, id string) bool {
	return item.ID == id || item.SourceID == id
}
