package jellyfin

import (
	"context"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// PlaybackProgressInfo is the JF-shape body for PlaybackStart /
// PlaybackProgress / PlaybackStopped messages. Fields verified
// against JF OpenAPI 10.10.x.
type PlaybackProgressInfo struct {
	ItemID                 string `json:"ItemId"`
	MediaSourceID          string `json:"MediaSourceId"`
	PlaySessionID          string `json:"PlaySessionId"`
	PositionTicks          int64  `json:"PositionTicks"`
	IsPaused               bool   `json:"IsPaused"`
	IsMuted                bool   `json:"IsMuted"`
	VolumeLevel            int    `json:"VolumeLevel"`
	AudioStreamIndex       int    `json:"AudioStreamIndex"`
	SubtitleStreamIndex    int    `json:"SubtitleStreamIndex"`
	PlayMethod             string `json:"PlayMethod"`
	RepeatMode             string `json:"RepeatMode"`
	PlaybackOrder          string `json:"PlaybackOrder"`
	CanSeek                bool   `json:"CanSeek"`
	PlaybackStartTimeTicks int64  `json:"PlaybackStartTimeTicks"`
	// Failed is only used in PlaybackStopped; omitempty-out when false
	// would also be acceptable, but JF tolerates explicit "Failed":false.
	Failed bool `json:"Failed,omitempty"`
}

// buildProgressInfo constructs a PlaybackProgressInfo from current
// core.SessionStatus + cached track indices. Volume/Mute/Repeat use
// safe defaults — there's no local volume control on a headless MiSTer.
func (r *reporter) buildProgressInfo(st core.SessionStatus, audIdx, subIdx int) PlaybackProgressInfo {
	return PlaybackProgressInfo{
		ItemID:                 r.itemID,
		MediaSourceID:          r.mediaSourceID,
		PlaySessionID:          r.playSessionID,
		PositionTicks:          int64(st.Position / (100 * time.Nanosecond)),
		IsPaused:               st.State == core.StatePaused,
		IsMuted:                false,
		VolumeLevel:            100,
		AudioStreamIndex:       audIdx,
		SubtitleStreamIndex:    subIdx,
		PlayMethod:             "Transcode",
		RepeatMode:             "RepeatNone",
		PlaybackOrder:          "Default",
		CanSeek:                true,
		PlaybackStartTimeTicks: r.startedAt.UnixNano() / 100,
	}
}

// reporterParams is the input for spawnReporter.
type reporterParams struct {
	ItemID        string
	PlaySessionID string
	MediaSourceID string
	TickInterval  time.Duration // default 10s; overridable for tests
}

// spawnReporter starts the per-session reporter goroutine. Called
// from commands.HandlePlay's startPlayNow after StartSession succeeds.
func (a *Adapter) spawnReporter(p reporterParams) {
	if p.TickInterval == 0 {
		p.TickInterval = 10 * time.Second
	}
	refKey := p.ItemID + ":" + p.PlaySessionID
	ctx, cancel := context.WithCancel(context.Background())
	r := &reporter{
		capturedRefKey: refKey,
		itemID:         p.ItemID,
		playSessionID:  p.PlaySessionID,
		mediaSourceID:  p.MediaSourceID,
		startedAt:      time.Now(),
		wakeup:         make(chan struct{}, 1),
		ticker:         time.NewTicker(p.TickInterval),
		progressBuf:    newRingBuffer(32),
		ctx:            ctx,
		cancel:         cancel,
	}
	a.mu.Lock()
	a.reporters[refKey] = r
	a.mu.Unlock()

	go a.runReporter(r)
}

// stopReporter cancels and unregisters a reporter. Called by tests
// and on Adapter.Stop().
func (a *Adapter) stopReporter(refKey string) {
	a.mu.Lock()
	r, ok := a.reporters[refKey]
	if ok {
		delete(a.reporters, refKey)
	}
	a.mu.Unlock()
	if ok {
		r.cancel()
		r.ticker.Stop()
	}
}

// lookupReporter is a test helper.
func (a *Adapter) lookupReporter(refKey string) *reporter {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reporters[refKey]
}

// runReporter is the per-session goroutine. Emits PlaybackStart once,
// then PlaybackProgress on each tick or wakeup poke, classifying the
// session-end via Status() + currentRefKey identity check.
func (a *Adapter) runReporter(r *reporter) {
	if a.core == nil {
		// Defensive: in production, Adapter.Start refuses to run with
		// nil core. Tests may construct the adapter with nil + never
		// spawn reporters; this guard makes that explicit.
		a.stopReporter(r.capturedRefKey)
		return
	}
	// PlaybackStart immediately.
	st := a.core.Status()
	a.emitProgress(r, st, "PlaybackStart")

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.wakeup:
		case <-r.ticker.C:
		}

		st := a.core.Status()
		cur := a.snapshotCurrentRefKey()

		switch {
		case st.State == core.StateIdle:
			a.emitTerminal(r, st)
			a.stopReporter(r.capturedRefKey)
			return

		case st.AdapterRef == r.capturedRefKey:
			a.emitProgress(r, st, "PlaybackProgress")

		case cur == r.capturedRefKey:
			// External preempt: someone else replaced us in core, but our
			// adapter hasn't moved on (currentRefKey still points to us).
			// Emit PlaybackStopped {Failed:false}.
			a.emitTerminal(r, st)
			a.stopReporter(r.capturedRefKey)
			return

		default:
			// Self-preempt (cur differs from captured): the new session's
			// reporter will emit PlaybackStart. Elide. Exit silently.
			a.stopReporter(r.capturedRefKey)
			return
		}
	}
}

func (a *Adapter) emitProgress(r *reporter, st core.SessionStatus, msgType string) {
	a.mu.Lock()
	audIdx := derefIntOrZero(a.lastAudioStreamIdx)
	subIdx := derefIntOrZero(a.lastSubtitleStreamIdx)
	send := a.sendOutboundFn
	a.mu.Unlock()
	body := r.buildProgressInfo(st, audIdx, subIdx)
	if send != nil {
		send(outboundEnvelope{MessageType: msgType, Data: body})
	}
}

// emitTerminal sends PlaybackStopped to JF.
//
// Known race (acceptable in v1; spec §"Reporter goroutine: single
// source of truth" tradeoff): when a plane error fires, the manager
// transitions FSM to Idle BEFORE notifySessionStop runs in its own
// goroutine. If the reporter ticker happens to fire in the
// microseconds between FSM=Idle and the OnStop goroutine writing
// r.errReason, this function reads errReason="" and sends
// Failed=false instead of Failed=true. Probability is astronomically
// low (10s ticker vs μs goroutine scheduling), but the JF UI may
// occasionally show "completed" for a crashed cast. Closing the
// race requires reordering manager.go's plane-exit goroutine to
// call OnStop synchronously BEFORE the FSM transition — a cleaner
// fix that we may apply in v2.
func (a *Adapter) emitTerminal(r *reporter, st core.SessionStatus) {
	a.mu.Lock()
	audIdx := derefIntOrZero(a.lastAudioStreamIdx)
	subIdx := derefIntOrZero(a.lastSubtitleStreamIdx)
	send := a.sendOutboundFn
	a.mu.Unlock()
	body := r.buildProgressInfo(st, audIdx, subIdx)
	body.Failed = r.errReason == "error"
	if send != nil {
		send(outboundEnvelope{MessageType: "PlaybackStopped", Data: body})
	}
}

func derefIntOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
