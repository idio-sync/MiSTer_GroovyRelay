package streams

import (
	"strings"
	"time"
)

const badStreamItemTTL = 6 * time.Hour

type badStreamItem struct {
	reason string
	at     time.Time
}

func badStreamItemKey(providerID, itemID string) string {
	providerID = strings.TrimSpace(providerID)
	itemID = strings.TrimSpace(itemID)
	if providerID == "" || itemID == "" {
		return ""
	}
	return providerID + "\x00" + itemID
}

func (a *Adapter) markBadStreamItemIDLocked(providerID, itemID, reason string, now time.Time) {
	key := badStreamItemKey(providerID, itemID)
	if key == "" {
		return
	}
	if a.badItems == nil {
		a.badItems = map[string]badStreamItem{}
	}
	a.badItems[key] = badStreamItem{reason: reason, at: now}
}

func (a *Adapter) isBadStreamItemLocked(providerID string, item StreamItem, now time.Time) bool {
	itemID := itemIdentity(item)
	key := badStreamItemKey(providerID, itemID)
	if key == "" || len(a.badItems) == 0 {
		return false
	}
	bad, ok := a.badItems[key]
	if !ok {
		return false
	}
	if !bad.at.IsZero() && now.Sub(bad.at) > badStreamItemTTL {
		delete(a.badItems, key)
		return false
	}
	return true
}

func (a *Adapter) filterKnownBadStreamItemsLocked(q *ActiveQueue, now time.Time) int {
	if q == nil || len(a.badItems) == 0 {
		return 0
	}
	filteredItems, skipped := a.filterKnownBadStreamItemSliceLocked(q.ProviderID, q.Items, now)
	filteredBase, _ := a.filterKnownBadStreamItemSliceLocked(q.ProviderID, q.baseItems, now)
	q.Items = filteredItems
	q.baseItems = filteredBase
	if q.Index >= len(q.Items) {
		q.Index = 0
	}
	return skipped
}

func (a *Adapter) filterKnownBadStreamItemSliceLocked(providerID string, items []StreamItem, now time.Time) ([]StreamItem, int) {
	if len(items) == 0 {
		return nil, 0
	}
	out := items[:0]
	skipped := 0
	for _, item := range items {
		if a.isBadStreamItemLocked(providerID, item, now) {
			skipped++
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil, skipped
	}
	return out, skipped
}

func cacheableStreamItemFailure(reason string) bool {
	switch reason {
	case "failed to resolve stream item", "stream resolver returned no playable media URL":
		return true
	default:
		return false
	}
}

func streamResolveFailureKind(err error) (kind string, global bool) {
	if err == nil {
		return "", false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "no such option") && strings.Contains(msg, "js-runtimes"):
		return "yt-dlp-too-old", true
	case strings.Contains(msg, "binary not configured"),
		strings.Contains(msg, "resolve binary"),
		strings.Contains(msg, "executable file not found"),
		strings.Contains(msg, "no such file or directory"):
		return "yt-dlp-unavailable", true
	case strings.Contains(msg, "sign in to confirm") ||
		strings.Contains(msg, "not a bot") ||
		strings.Contains(msg, "precondition check failed") ||
		strings.Contains(msg, "po token") ||
		strings.Contains(msg, "http error 429") ||
		strings.Contains(msg, "too many requests"):
		return "youtube-access-blocked", true
	case strings.Contains(msg, "timed out"):
		return "resolver-timeout", false
	default:
		return "item-unavailable", false
	}
}

func durationMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64(d / time.Millisecond)
}
