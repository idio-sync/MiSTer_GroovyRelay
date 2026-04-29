package jellyfin

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// playMessageData is the JF Play message Data field.
type playMessageData struct {
	ItemIDs             []string `json:"ItemIds"`
	StartPositionTicks  int64    `json:"StartPositionTicks"`
	PlayCommand         string   `json:"PlayCommand"`
	ControllingUserID   string   `json:"ControllingUserId"`
	SubtitleStreamIndex *int     `json:"SubtitleStreamIndex,omitempty"`
	AudioStreamIndex    *int     `json:"AudioStreamIndex,omitempty"`
	MediaSourceID       string   `json:"MediaSourceId,omitempty"`
	StartIndex          int      `json:"StartIndex,omitempty"`
}

// HandlePlay processes an inbound Play message.
func (a *Adapter) HandlePlay(data json.RawMessage) {
	var p playMessageData
	if err := json.Unmarshal(data, &p); err != nil {
		slog.Warn("jellyfin: bad Play payload", "err", err)
		return
	}
	if len(p.ItemIDs) == 0 {
		slog.Warn("jellyfin: Play with no ItemIds")
		return
	}

	switch p.PlayCommand {
	case "", "PlayNow":
		a.startPlayNow(p)
	case "PlayNext":
		a.queueAt(p, 0)
	case "PlayLast":
		a.queueAt(p, -1) // -1 means append
	case "PlayInstantMix", "PlayShuffle":
		slog.Warn("jellyfin: PlayCommand simplified to PlayNow", "requested", p.PlayCommand)
		a.startPlayNow(p)
	default:
		slog.Warn("jellyfin: unknown PlayCommand", "cmd", p.PlayCommand)
	}
}

// startPlayNow runs the PlaybackInfo → StartSession sequence for ItemIds[0].
func (a *Adapter) startPlayNow(p playMessageData) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	tok, err := LoadToken(a.tokenPath())
	if err != nil || tok.AccessToken == "" {
		slog.Error("jellyfin: startPlayNow: no token", "err", err)
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		info, err := FetchPlaybackInfo(ctx, PlaybackInfoInput{
			ServerURL:           cfg.ServerURL,
			Token:               tok.AccessToken,
			DeviceID:            a.deviceID,
			DeviceName:          cfg.DeviceName,
			Version:             linkVersion,
			ItemID:              p.ItemIDs[0],
			UserID:              tok.UserID,
			MaxVideoBitrateKbps: cfg.MaxVideoBitrateKbps,
			StartPositionTicks:  p.StartPositionTicks,
			MediaSourceID:       p.MediaSourceID,
			AudioStreamIndex:    p.AudioStreamIndex,
			SubtitleStreamIndex: p.SubtitleStreamIndex,
		})
		if err != nil {
			slog.Error("jellyfin: PlaybackInfo failed", "err", err)
			return
		}

		req := a.buildSessionRequest(playRequestInput{
			ItemID:             p.ItemIDs[0],
			StartPositionTicks: p.StartPositionTicks,
			PlayInfo:           info,
			ServerURL:          cfg.ServerURL,
			Token:              tok.AccessToken,
		})

		prev := a.beginSelfPreempt(req.AdapterRef)
		if a.core == nil {
			a.rollbackSelfPreempt(prev)
			slog.Error("jellyfin: no core SessionManager")
			return
		}
		if err := a.core.StartSession(req); err != nil {
			a.rollbackSelfPreempt(prev)
			slog.Error("jellyfin: StartSession failed", "err", err)
			return
		}
		a.commitSelfPreempt()

		a.spawnReporter(reporterParams{
			ItemID:          p.ItemIDs[0],
			PlaySessionID:   info.PlaySessionID,
			MediaSourceID:   info.MediaSourceID,
			AudioIdx:        p.AudioStreamIndex,
			SubtitleIdx:     p.SubtitleStreamIndex,
			NowPlayingQueue: a.snapshotNowPlayingQueue(p.ItemIDs[0]),
			Auth: RESTAuth{
				ServerURL: cfg.ServerURL, Token: tok.AccessToken,
				DeviceID: a.deviceID, DeviceName: cfg.DeviceName,
				Version: linkVersion,
			},
		})
	}()
}

// snapshotNowPlayingQueue returns a QueueItem slice with the current
// item first followed by any adapter-queued items. Read under
// Adapter.mu.
func (a *Adapter) snapshotNowPlayingQueue(currentItemID string) []QueueItem {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]QueueItem, 0, 1+len(a.queue))
	out = append(out, QueueItem{ID: currentItemID, PlaylistItemID: currentItemID})
	for _, qi := range a.queue {
		out = append(out, QueueItem{ID: qi.ItemID, PlaylistItemID: qi.ItemID})
	}
	return out
}

// playstateRequestData is the JF Playstate Data field.
type playstateRequestData struct {
	Command           string `json:"Command"`
	SeekPositionTicks int64  `json:"SeekPositionTicks,omitempty"`
	ControllingUserID string `json:"ControllingUserId,omitempty"`
}

// HandlePlaystate translates Pause / Unpause / Stop / Seek /
// PlayPause / NextTrack / PreviousTrack into core.Manager calls.
func (a *Adapter) HandlePlaystate(data json.RawMessage) {
	var p playstateRequestData
	if err := json.Unmarshal(data, &p); err != nil {
		slog.Warn("jellyfin: bad Playstate payload", "err", err)
		return
	}
	if a.core == nil {
		return
	}
	switch p.Command {
	case "Pause":
		_ = a.core.Pause()
	case "Unpause":
		_ = a.core.Play()
	case "PlayPause":
		st := a.core.Status()
		if st.State == core.StatePlaying {
			_ = a.core.Pause()
		} else if st.State == core.StatePaused {
			_ = a.core.Play()
		}
	case "Stop":
		_ = a.core.Stop()
	case "Seek":
		ms := int(p.SeekPositionTicks / 10_000)
		_ = a.core.SeekTo(ms)
	case "NextTrack":
		if qi, ok := a.popQueueHead(); ok {
			a.startQueuedItem(qi)
		}
	case "PreviousTrack":
		// v1: no history; PreviousTrack is a no-op. Documented gap.
		slog.Debug("jellyfin: PreviousTrack ignored (v1 has no playback history)")
	default:
		slog.Debug("jellyfin: unhandled Playstate.Command", "cmd", p.Command)
	}
}

// generalCommandData is the JF GeneralCommand Data field.
type generalCommandData struct {
	Name              string            `json:"Name"`
	Arguments         map[string]string `json:"Arguments"`
	ControllingUserID string            `json:"ControllingUserId"`
}

// HandleGeneralCommand handles Volume, Mute, track-switch, and
// DisplayMessage. v1 does not have local volume so Volume/Mute are
// recorded for reporting but not acted on. Track-switch records the
// requested index and (in Phase 8) re-issues PlaybackInfo + StartSession.
func (a *Adapter) HandleGeneralCommand(data json.RawMessage) {
	var g generalCommandData
	if err := json.Unmarshal(data, &g); err != nil {
		slog.Warn("jellyfin: bad GeneralCommand payload", "err", err)
		return
	}
	switch g.Name {
	case "DisplayMessage":
		slog.Info("jellyfin: DisplayMessage", "header", g.Arguments["Header"], "text", g.Arguments["Text"])
	case "SetAudioStreamIndex":
		i, err := strconv.Atoi(g.Arguments["Index"])
		if err != nil {
			return
		}
		a.trackSwitch(trackSwitchInput{audioIdx: &i})
	case "SetSubtitleStreamIndex":
		i, err := strconv.Atoi(g.Arguments["Index"])
		if err != nil {
			return
		}
		a.trackSwitch(trackSwitchInput{subtitleIdx: &i})
	case "VolumeUp", "VolumeDown", "Mute", "Unmute", "ToggleMute", "SetVolume":
		slog.Debug("jellyfin: volume/mute command logged but no-op", "name", g.Name)
	case "SetMaxStreamingBitrate":
		slog.Debug("jellyfin: SetMaxStreamingBitrate noted; takes effect on next play")
	default:
		slog.Debug("jellyfin: unhandled GeneralCommand", "name", g.Name)
	}
}

// trackSwitchInput carries which track is being changed. Exactly one
// of audioIdx / subtitleIdx is non-nil per call.
type trackSwitchInput struct {
	audioIdx    *int
	subtitleIdx *int
}

// trackSwitch implements the spec's §"Track switching mid-cast"
// 5-step ordering: snapshot Status, build new req with SeekOffsetMs
// from the snapshot, reserve currentRefKey, call StartSession, restore
// Pause if the prior session was paused. No-ops if the new index
// matches the current one.
func (a *Adapter) trackSwitch(in trackSwitchInput) {
	if a.core == nil {
		return
	}

	// 1) Snapshot Status() BEFORE anything else.
	st := a.core.Status()
	if st.State != core.StatePlaying && st.State != core.StatePaused {
		return // nothing to switch
	}

	// Find current item from currentRefKey, and pull the active
	// reporter's per-session indices so we can:
	//   (a) no-op when the requested index matches the current one
	//   (b) carry the un-touched track forward into the new session
	cur := a.snapshotCurrentRefKey()
	itemID, _, ok := splitRefKey(cur)
	if !ok || itemID == "" {
		return
	}

	a.mu.Lock()
	var cachedAud, cachedSub *int
	if r, ok := a.reporters[cur]; ok {
		cachedAud = r.audioIdx
		cachedSub = r.subtitleIdx
	}
	cfg := a.cfg
	a.mu.Unlock()

	if in.audioIdx != nil && cachedAud != nil && *in.audioIdx == *cachedAud {
		return
	}
	if in.subtitleIdx != nil && cachedSub != nil && *in.subtitleIdx == *cachedSub {
		return
	}

	// Carry forward the unchanged track so the new reporter reports
	// both indices, not just the one that just changed.
	nextAud := in.audioIdx
	if nextAud == nil {
		nextAud = cachedAud
	}
	nextSub := in.subtitleIdx
	if nextSub == nil {
		nextSub = cachedSub
	}
	tok, err := LoadToken(a.tokenPath())
	if err != nil || tok.AccessToken == "" {
		slog.Error("jellyfin: trackSwitch: no token")
		return
	}

	wasPaused := st.State == core.StatePaused
	posTicks := int64(st.Position / (100 * time.Nanosecond))

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// 2) Build new SessionRequest with SeekOffsetMs from the snapshot.
		info, err := FetchPlaybackInfo(ctx, PlaybackInfoInput{
			ServerURL:           cfg.ServerURL,
			Token:               tok.AccessToken,
			DeviceID:            a.deviceID,
			DeviceName:          cfg.DeviceName,
			Version:             linkVersion,
			ItemID:              itemID,
			UserID:              tok.UserID,
			MaxVideoBitrateKbps: cfg.MaxVideoBitrateKbps,
			StartPositionTicks:  posTicks,
			AudioStreamIndex:    in.audioIdx,
			SubtitleStreamIndex: in.subtitleIdx,
		})
		if err != nil {
			slog.Error("jellyfin: trackSwitch PlaybackInfo failed", "err", err)
			return
		}
		req := a.buildSessionRequest(playRequestInput{
			ItemID:             itemID,
			StartPositionTicks: posTicks,
			PlayInfo:           info,
			ServerURL:          cfg.ServerURL,
			Token:              tok.AccessToken,
		})

		// 3) Reserve currentRefKey atomically (rollback on error).
		prev := a.beginSelfPreempt(req.AdapterRef)

		// 4) Call StartSession.
		if err := a.core.StartSession(req); err != nil {
			a.rollbackSelfPreempt(prev)
			slog.Error("jellyfin: trackSwitch StartSession failed", "err", err)
			return
		}
		a.commitSelfPreempt()

		// 5) Restore Pause if the prior session was paused.
		if wasPaused {
			_ = a.core.Pause()
		}

		a.spawnReporter(reporterParams{
			ItemID:          itemID,
			PlaySessionID:   info.PlaySessionID,
			MediaSourceID:   info.MediaSourceID,
			AudioIdx:        nextAud,
			SubtitleIdx:     nextSub,
			NowPlayingQueue: a.snapshotNowPlayingQueue(itemID),
			Auth: RESTAuth{
				ServerURL: cfg.ServerURL, Token: tok.AccessToken,
				DeviceID: a.deviceID, DeviceName: cfg.DeviceName,
				Version: linkVersion,
			},
		})
	}()
}

// splitRefKey splits "<itemId>:<playSessionId>" into its parts.
// Item IDs are GUIDs (no colons), so splitting on the first ':' is safe.
func splitRefKey(k string) (itemID, playSessionID string, ok bool) {
	for i := 0; i < len(k); i++ {
		if k[i] == ':' {
			return k[:i], k[i+1:], true
		}
	}
	return "", "", false
}

// queueAt enqueues all items in p.ItemIDs into adapter.queue. pos==0
// inserts at the front (PlayNext), pos<0 appends (PlayLast).
func (a *Adapter) queueAt(p playMessageData, pos int) {
	items := make([]QueuedItem, 0, len(p.ItemIDs))
	for _, id := range p.ItemIDs {
		items = append(items, QueuedItem{
			ItemID:              id,
			StartPositionTicks:  0,
			MediaSourceID:       p.MediaSourceID,
			AudioStreamIndex:    p.AudioStreamIndex,
			SubtitleStreamIndex: p.SubtitleStreamIndex,
		})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if pos < 0 {
		a.queue = append(a.queue, items...)
	} else {
		a.queue = append(items, a.queue...)
	}
}

// popQueueHead returns the next queued item or zero-value if empty.
func (a *Adapter) popQueueHead() (QueuedItem, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) == 0 {
		return QueuedItem{}, false
	}
	head := a.queue[0]
	a.queue = a.queue[1:]
	return head, true
}

// startQueuedItem turns a QueuedItem into a Play and runs the PlayNow flow.
func (a *Adapter) startQueuedItem(qi QueuedItem) {
	a.startPlayNow(playMessageData{
		ItemIDs:             []string{qi.ItemID},
		StartPositionTicks:  qi.StartPositionTicks,
		MediaSourceID:       qi.MediaSourceID,
		AudioStreamIndex:    qi.AudioStreamIndex,
		SubtitleStreamIndex: qi.SubtitleStreamIndex,
		PlayCommand:         "PlayNow",
	})
}
