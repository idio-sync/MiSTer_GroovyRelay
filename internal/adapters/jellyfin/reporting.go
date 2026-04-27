package jellyfin

import (
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
