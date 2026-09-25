package liveaudio

import (
	"fmt"
	"time"
)

// EventKind is a protocol-neutral player event from a helper.
type EventKind int

const (
	// EventPlay: audio is (about to be) flowing. Starts a session when
	// idle, resumes a held one, and is ignored while live.
	EventPlay EventKind = iota + 1
	// EventPause: the sender paused. The session holds for the grace
	// window, then ends.
	EventPause
	// EventResume: the sender resumed. Same handling as EventPlay.
	EventResume
	// EventTrack: new track text, optionally with artwork (URL or bytes).
	// Artwork left empty keeps the current artwork.
	EventTrack
	// EventArtwork: artwork only (AirPlay delivers it separately from the
	// text). Empty bytes clear the artwork.
	EventArtwork
	// EventStop: the sender stopped or disconnected. Ends the session.
	EventStop
)

func (k EventKind) String() string {
	switch k {
	case EventPlay:
		return "play"
	case EventPause:
		return "pause"
	case EventResume:
		return "resume"
	case EventTrack:
		return "track"
	case EventArtwork:
		return "artwork"
	case EventStop:
		return "stop"
	default:
		return fmt.Sprintf("event(%d)", int(k))
	}
}

// TrackMeta is what a helper knows about the current track.
type TrackMeta struct {
	Title    string
	Artist   string
	Album    string
	Duration time.Duration // informational; the live overlay shows no progress

	ArtworkURL   string // https URL to fetch (Spotify)
	ArtworkBytes []byte // encoded image (AirPlay PICT)
}

// Event is one helper event.
type Event struct {
	Kind  EventKind
	Track TrackMeta
}

func (m TrackMeta) hasArtwork() bool {
	return m.ArtworkURL != "" || len(m.ArtworkBytes) > 0
}
