package jellyfin

import (
	"context"
	"log/slog"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// msgKind identifies which playback-lifecycle endpoint to hit. Kept
// internal to the package because the REST helpers are split across
// three differently-shaped bodies.
type msgKind int

const (
	msgKindStart msgKind = iota
	msgKindProgress
)

// progressTickInterval is JF's expected cadence for /Sessions/Playing/Progress.
const progressTickInterval = 10 * time.Second

// pingTickInterval is the cadence for /Sessions/Playing/Ping. JF
// reaps idle transcoders after ~30s; pinging every 30s keeps them
// warm even if a progress tick is dropped or delayed.
const pingTickInterval = 30 * time.Second

// reporterParams is the input for spawnReporter.
type reporterParams struct {
	ItemID         string
	PlaySessionID  string
	MediaSourceID  string
	PlaylistItemID string

	// AudioIdx and SubtitleIdx seed the reporter's per-session
	// selection. Pointer-nil means "no track explicitly chosen" — JF
	// then keeps the server-side default rather than us asserting
	// "stream 0 is selected."
	AudioIdx    *int
	SubtitleIdx *int

	// NowPlayingQueue is a snapshot of the adapter's queue at spawn.
	// Reported in PlaybackProgress so JF dashboards can render
	// "Up next."
	NowPlayingQueue []QueueItem

	// Auth carries the identity used on every REST call. Snapshotted
	// at spawn so a token rotation mid-cast doesn't cause progress
	// reports to authenticate as a stale identity.
	Auth RESTAuth

	// TickInterval overrides progressTickInterval. Tests inject a
	// shorter cadence; production uses 0 → defaults to 10 s.
	TickInterval time.Duration
}

// buildProgressInfo constructs a PlaybackProgressInfo for both
// PlaybackStart and PlaybackProgress (JF treats them the same shape).
func (r *reporter) buildProgressInfo(st core.SessionStatus, sessionID string) PlaybackProgressInfo {
	pos := int64(st.Position / (100 * time.Nanosecond))
	startTicks := dotNetTicks(r.startedAt)
	vol := 100
	return PlaybackProgressInfo{
		ItemID:                 r.itemID,
		SessionID:              sessionID,
		MediaSourceID:          r.mediaSourceID,
		PlaySessionID:          r.playSessionID,
		PositionTicks:          &pos,
		PlaybackStartTimeTicks: &startTicks,
		VolumeLevel:            &vol,
		AudioStreamIndex:       r.audioIdx,
		SubtitleStreamIndex:    r.subtitleIdx,
		IsPaused:               st.State == core.StatePaused,
		IsMuted:                false,
		PlayMethod:             "Transcode",
		RepeatMode:             "RepeatNone",
		PlaybackOrder:          "Default",
		CanSeek:                true,
		NowPlayingQueue:        r.nowPlayingQueue,
		PlaylistItemID:         r.playlistItemID,
	}
}

// buildStopInfo constructs a PlaybackStopInfo from the final
// SessionStatus and the captured session id.
func (r *reporter) buildStopInfo(st core.SessionStatus, sessionID string) PlaybackStopInfo {
	pos := int64(st.Position / (100 * time.Nanosecond))
	return PlaybackStopInfo{
		ItemID:          r.itemID,
		SessionID:       sessionID,
		MediaSourceID:   r.mediaSourceID,
		PlaySessionID:   r.playSessionID,
		PositionTicks:   &pos,
		Failed:          r.errReason == "error",
		PlaylistItemID:  r.playlistItemID,
		NowPlayingQueue: r.nowPlayingQueue,
	}
}

// spawnReporter starts the per-session reporter goroutine. Called
// from commands.HandlePlay's startPlayNow after StartSession succeeds.
func (a *Adapter) spawnReporter(p reporterParams) {
	tickInterval := p.TickInterval
	if tickInterval == 0 {
		tickInterval = progressTickInterval
	}
	refKey := p.ItemID + ":" + p.PlaySessionID
	ctx, cancel := context.WithCancel(context.Background())
	r := &reporter{
		capturedRefKey:  refKey,
		itemID:          p.ItemID,
		playSessionID:   p.PlaySessionID,
		mediaSourceID:   p.MediaSourceID,
		playlistItemID:  p.PlaylistItemID,
		audioIdx:        p.AudioIdx,
		subtitleIdx:     p.SubtitleIdx,
		nowPlayingQueue: p.NowPlayingQueue,
		auth:            p.Auth,
		startedAt:       time.Now(),
		wakeup:          make(chan struct{}, 1),
		ticker:          time.NewTicker(tickInterval),
		pingTicker:      time.NewTicker(pingTickInterval),
		ctx:             ctx,
		cancel:          cancel,
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
		r.pingTicker.Stop()
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
// session-end via Status() + currentRefKey identity check. Pings the
// transcoder out-of-band on a slower ticker.
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
	a.emit(r, st, msgKindStart)

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.pingTicker.C:
			a.emitPing(r)
			continue
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
			a.emit(r, st, msgKindProgress)

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

// emit POSTs a PlaybackStart or PlaybackProgress to JF. Errors are
// logged but not retried — the next tick supersedes a lost update,
// and JF's session row can be rebuilt from the next successful one.
func (a *Adapter) emit(r *reporter, st core.SessionStatus, kind msgKind) {
	body := r.buildProgressInfo(st, a.snapshotSessionID())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var err error
	switch kind {
	case msgKindStart:
		err = postPlaybackStart(ctx, r.auth, body)
	case msgKindProgress:
		err = postPlaybackProgress(ctx, r.auth, body)
	}
	if err != nil {
		slog.Warn("jellyfin: progress report failed",
			"playSessionId", r.playSessionID, "kind", kind, "err", err)
	}
}

// emitTerminal POSTs PlaybackStopped. postPlaybackStopped retries
// once on transient failure to avoid leaving JF showing a stuck
// session.
func (a *Adapter) emitTerminal(r *reporter, st core.SessionStatus) {
	body := r.buildStopInfo(st, a.snapshotSessionID())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := postPlaybackStopped(ctx, r.auth, body); err != nil {
		slog.Warn("jellyfin: stopped report failed",
			"playSessionId", r.playSessionID, "err", err)
	}
}

// emitPing fires the transcoder-keepalive ping. Fire-and-forget; no
// retry, no log on transient error (the next tick is 30 s away).
func (a *Adapter) emitPing(r *reporter) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = postPlaybackPing(ctx, r.auth, r.playSessionID)
}
