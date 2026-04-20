package core

import "time"

// State is the session lifecycle state exposed through SessionStatus.State.
// The concrete transition table and machine live in state.go; the type is
// declared here so SessionStatus is self-contained.
type State string

// SessionRequest is the adapter-agnostic input to StartSession. Every adapter
// (Plex, and future: URL-input, Jellyfin, DLNA, ...) translates its
// protocol-specific request into one of these before calling the manager.
type SessionRequest struct {
	// StreamURL is a URL FFmpeg can consume (HLS manifest, direct file URL,
	// RTSP, etc). The adapter is responsible for constructing any
	// protocol-specific URL (e.g. Plex transcode URL with token).
	StreamURL string

	// InputHeaders are passed as FFmpeg -headers (e.g. Plex tokens).
	InputHeaders map[string]string

	// SeekOffsetMs is where to start playback (0 = beginning).
	SeekOffsetMs int

	// SubtitleURL is a URL to an external subtitle track to burn in.
	// Empty = no subtitles.
	SubtitleURL string

	// SubtitlePath is a local filesystem path to a subtitle file (SRT or ASS)
	// that the data plane hands to libass via the ffmpeg `subtitles=filename=`
	// filter. Mutually exclusive with SubtitleURL; adapters prefer SubtitlePath
	// and set SubtitleURL only during migration. Libass cannot fetch URLs, so
	// adapters that source captions from the network MUST download to a file
	// first and pass the path here.
	SubtitlePath  string
	SubtitleIndex int

	// Capabilities describe what the adapter's control surface supports.
	// Used by the manager to decide whether Pause/Seek calls are valid.
	Capabilities Capabilities

	// AdapterRef is an opaque handle the adapter can use to correlate
	// status updates back to its own session context (e.g., a Plex media
	// key or a URL-input session ID). Never inspected by core.
	AdapterRef string

	// DirectPlay is true when the source URL is a direct media URL (FFmpeg
	// seeks via -ss); false when the URL is a transcode/HLS manifest whose
	// offset is encoded server-side. Adapters set this per-session. See §5.3.
	DirectPlay bool
}

// Capabilities declares what operations the adapter's control surface can
// honor for a session. The manager consults these before performing Pause
// and Seek so that adapters whose upstream protocol lacks those operations
// can reject them cleanly instead of tearing down the data plane.
type Capabilities struct {
	CanSeek  bool
	CanPause bool
}

// SessionStatus is the adapter-agnostic view of what's currently playing.
// Adapters subscribe to this for their timeline reporting.
type SessionStatus struct {
	State      State
	Position   time.Duration
	Duration   time.Duration
	AdapterRef string
	StartedAt  time.Time
}
