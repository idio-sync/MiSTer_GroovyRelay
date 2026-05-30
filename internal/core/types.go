package core

import (
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
)

// AudioScopeSnapshot is an alias for dataplane.AudioScopeSnapshot, so
// chassis can read the type via internal/core without importing
// internal/dataplane directly. Using an alias (not a wrapper struct)
// preserves the pointer-return contract: no copies cross package
// boundaries.
type AudioScopeSnapshot = dataplane.AudioScopeSnapshot

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
	VisualizerModeRetroAnalyzer     VisualizerMode = "retro_analyzer"
	VisualizerModeOscilloscopeWave  VisualizerMode = "oscilloscope_wave"
	VisualizerModeStereoScope       VisualizerMode = "stereo_scope"
	VisualizerModeVUCabinet         VisualizerMode = "vu_cabinet"
	VisualizerModeSpectrumWaterfall VisualizerMode = "spectrum_waterfall"
	VisualizerModeRasterPulse       VisualizerMode = "raster_pulse"
	VisualizerModeCoverVU           VisualizerMode = "cover_vu"
	VisualizerModeCoverSpectrum     VisualizerMode = "cover_spectrum"
)

type VisualizerMetadata struct {
	Title       string
	Artist      string
	Album       string
	Duration    time.Duration
	ArtworkPath string
}

// DisplayMetadata is the adapter-agnostic, pre-formatted text the
// receiver VFD renders as three stacked rows. Adapters compose these
// strings (they own per-media-type formatting); core never interprets
// them. Empty tiers render as collapsed rows. See
// docs/superpowers/specs/2026-05-29-receiver-vfd-multirow-metadata-design.md.
type DisplayMetadata struct {
	Primary   string // headline row (biggest)
	Secondary string // attribution row
	Tertiary  string // detail row (dim)
}

type VisualizerRequest struct {
	Enabled  bool
	Mode     VisualizerMode
	Metadata VisualizerMetadata
}

type AudioCaptureInput struct {
	Enabled         bool
	Format          string
	Device          string
	SampleRate      int
	Channels        int
	ThreadQueueSize int
	AnalyzeDuration time.Duration
	ProbeSize       int
}

type AudioOutputMode string

const (
	AudioOutputDefault    AudioOutputMode = ""
	AudioOutputVisualOnly AudioOutputMode = "visual_only"
	AudioOutputMonitor    AudioOutputMode = "monitor"
)

// SessionRequest is the adapter-agnostic input to StartSession. Every adapter
// (Plex, and future: URL-input, Jellyfin, DLNA, ...) translates its
// protocol-specific request into one of these before calling the manager.
type SessionRequest struct {
	// StreamURL is a URL FFmpeg can consume (HLS manifest, direct file URL,
	// RTSP, etc). The adapter is responsible for constructing any
	// protocol-specific URL (e.g. Plex transcode URL with token).
	StreamURL string

	// StreamProbeURL optionally provides a separate probe endpoint for AUX
	// streams whose playback URL should not be consumed by ffprobe.
	StreamProbeURL string

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

	// AudioCapture describes a local audio device input instead of a stream
	// URL. Enabled capture is mutually exclusive with stream inputs.
	AudioCapture AudioCaptureInput

	// AudioOutputMode controls whether captured audio is only visualized or
	// also monitored locally. Empty preserves today's stream playback behavior.
	AudioOutputMode AudioOutputMode

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

	// AspectMode overrides bridge.video.aspect_mode for this one session.
	// Empty preserves the bridge-level setting; valid overrides are
	// "letterbox", "zoom", and "auto".
	AspectMode string

	// OnStop is an optional adapter cleanup hook invoked when this session is
	// stopped or preempted. It must not block core; Manager calls it from a
	// goroutine after the data plane has been cancelled.
	OnStop func(reason string)

	// Title is a short human-readable label for the session, populated
	// by the adapter (Plex item title / Jellyfin item Name / URL filename).
	// Surfaced by the status home; never inspected by core. May be empty.
	Title string

	// DisplayMetadata is the adapter-composed three-row VFD text. When
	// zero-valued, consumers fall back to Title for the primary row.
	DisplayMetadata DisplayMetadata

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
	Title       string          // empty when idle
	Display     DisplayMetadata // adapter-composed VFD rows; empty when idle
	AdapterRef  string          // empty when idle
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
	Meter       MeterHomeView
	AudioDSP          config.AudioDSP // live tone/EQ params (runtime snapshot)
	AudioDSPEngaged   bool            // shaping active → status-bar EQ LED
	AudioDSPPersisted bool            // false while a preview runs ahead of disk
}

type MeterHomeView struct {
	Source   SourceMeterView
	Crop     CropMeterView
	Pipeline PipelineMeterView
	Runtime  RuntimeMeterView
}

type SourceMeterView struct {
	Width                 int
	Height                int
	FrameRate             float64
	Interlaced            bool
	SampleAspectRatioNum  int
	SampleAspectRatioDen  int
	DisplayAspectRatioNum int
	DisplayAspectRatioDen int
	VideoCodec            string
	AudioCodec            string
	AudioRate             int
	AudioChannels         int
	VideoBitrateBPS       int64
	AudioBitrateBPS       int64
	FormatBitrateBPS      int64
}

type CropMeterView struct {
	Mode     string
	Detected bool
	W        int
	H        int
	X        int
	Y        int
}

type PipelineMeterView struct {
	ModelineName        string
	OutputWidth         int
	OutputHeight        int
	FieldHeight         int
	FieldRateHz         float64
	HorizontalKHz       float64
	InterlacedOutput    bool
	Standard            string
	FieldOrder          string
	RGBMode             string
	LZ4Enabled          bool
	DeltaLZ4Enabled     bool
	AudioSampleRate     int
	AudioChannels       int
	AudioOutputVolume   int
	EffectiveAspectMode string
}

type RuntimeMeterView struct {
	BlitsTotal  uint64
	FramesTotal uint64
	Underruns   uint64
	WireBytes   uint64
	LastACKAge  time.Duration
	StartedAt   time.Time
	Generation  uint64
}
