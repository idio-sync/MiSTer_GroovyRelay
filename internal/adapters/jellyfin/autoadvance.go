package jellyfin

import (
	"log/slog"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
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
		go a.advanceAfterEOF(refKey)
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
	if _, ok := a.peekQueueHead(); !ok {
		slog.Debug("jellyfin: auto-advance reached end of queue")
		return
	}
}

func (a *Adapter) peekQueueHead() (QueuedItem, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) == 0 {
		return QueuedItem{}, false
	}
	return a.queue[0], true
}
