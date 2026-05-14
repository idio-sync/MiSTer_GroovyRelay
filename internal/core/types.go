package core

import "time"

// State is the session lifecycle state exposed through SessionStatus.State.
// The concrete transition table and machine live in state.go; the type is
// declared here so SessionStatus is self-contained.
type State string

type MediaKind string

const (
	MediaKindVideo MediaKind = "video"
	MediaKindMusic MediaKind = "music"
)

func NormalizeMediaKind(k MediaKind) MediaKind {
	switch k {
	case MediaKindMusic:
		return MediaKindMusic
	default:
		return MediaKindVideo
	}
}

type VisualizerMode string

const (
	VisualizerModeRetroAnalyzer VisualizerMode = "retro_analyzer"
)

type VisualizerMetadata struct {
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
}

type VisualizerRequest struct {
	Enabled  bool
	Mode     VisualizerMode
	Metadata VisualizerMetadata
}

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

	// AudioStreamURL, when non-empty, signals that StreamURL is a
	// video-only stream and audio must be sourced separately (the YouTube
	// DASH path). Manager wires this through to ffmpeg as a SECOND -i
	// input mapped via `-map 1:a:0`. Most adapters (Plex, progressive YT)
	// leave it empty — the single-input fallback preserves today's
	// behavior. See ffmpeg.PipelineSpec.AudioInputURL.
	AudioStreamURL string

	// AudioInputHeaders apply to AudioStreamURL only. yt-dlp returns
	// per-format http_headers and the two streams may carry different
	// User-Agent / Origin requirements. Empty when AudioStreamURL is
	// empty.
	AudioInputHeaders map[string]string

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
	// key or a URL-input session ID). Never inspected by core. Its shape
	// is per-adapter (Plex uses /library/parts/..., URL uses url:hex8,
	// etc.); use Source — not a parse of AdapterRef — to identify the
	// owning adapter.
	AdapterRef string

	// Source is the registered adapter name that originated this session
	// ("plex", "jellyfin", "url", …). Adapters populate it at the same
	// callsite as AdapterRef; the status home reads it via StatusHomeView
	// to render display names without parsing AdapterRef. May be empty
	// in legacy callsites; consumers must treat empty as "unknown".
	Source string

	// DirectPlay is true when the source URL is a direct media URL (FFmpeg
	// seeks via -ss); false when the URL is a transcode/HLS manifest whose
	// offset is encoded server-side. Adapters set this per-session. See §5.3.
	DirectPlay bool

	// OnStop is an optional adapter cleanup hook invoked when this session is
	// stopped or preempted. It must not block core; Manager calls it from a
	// goroutine after the data plane has been cancelled.
	OnStop func(reason string)

	// Title is a short human-readable label for the session, populated
	// by the adapter (Plex item title / Jellyfin item Name / URL filename).
	// Surfaced by the status home; never inspected by core. May be empty.
	Title string

	// MediaKind identifies whether the active item should be reported as
	// video or music. Empty is treated as video for legacy adapters.
	MediaKind MediaKind

	// Visualizer requests FFmpeg-generated video for an audio-only session.
	// Enabled sessions must use MediaKindMusic and a supported Mode.
	Visualizer VisualizerRequest

	// MediaInputPolicy constrains how ffprobe / ffmpeg dereference the
	// stream URL: protocol whitelist, reconnect/redirect behavior,
	// blocked-header deny-list, and read/write timeout. Adapters that hand
	// untrusted URLs to core (DLNA today; future operator-supplied
	// adapters) populate this; existing adapters (Plex, Jellyfin, URL)
	// leave it zero-valued and the resulting FFmpeg argv stays identical
	// to today. core.Manager applies the policy to Probe, ProbeCrop, and
	// BuildCommand consistently — adapter-only URL validation is not
	// sufficient because ffprobe/ffmpeg can otherwise follow redirects,
	// reconnect, or demux child resources after the adapter accepted the
	// original URL. See internal/ffmpeg/policy.go and spec §Architecture /
	// Core Media Input Policy.
	MediaInputPolicy MediaInputPolicy
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
	MediaKind  MediaKind
	Position   time.Duration
	Duration   time.Duration
	AdapterRef string
	StartedAt  time.Time
	Generation uint64
}

// StatusHomeView is the aggregated read-only view consumed by the
// status home and diagnostics pages. Built once per request from
// current state; no caching. See docs/specs/2026-05-08-ui-redesign-pr2-design.md §S3.
type StatusHomeView struct {
	State       State
	MediaKind   MediaKind
	Title       string // empty when idle
	AdapterRef  string // empty when idle
	Source      string // adapter name ("plex", "jellyfin", "url"); empty when idle
	Generation  uint64
	Modeline    string // e.g. "NTSC_480i"; empty when idle
	Position    time.Duration
	Duration    time.Duration
	StartedAt   time.Time
	BlitsTotal  uint64        // fields emitted (one per BLIT_FIELD_VSYNC)
	FramesTotal uint64        // ffmpeg frames consumed
	Underruns   uint64        // dataplane underruns since session start
	WireBytes   uint64        // post-LZ4 bytes sent (drives throughput; §S10)
	LastACKAge  time.Duration // 0 when no plane or no ACK yet
}
