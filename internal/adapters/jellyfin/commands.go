package jellyfin

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
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
	case "PlayNext", "PlayLast":
		// Implemented in Task 6.4.
		slog.Info("jellyfin: PlayNext/PlayLast not yet implemented (Phase 6.4)", "cmd", p.PlayCommand)
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
	}()
}
