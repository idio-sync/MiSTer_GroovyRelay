package adapters

import (
	"context"
	"errors"
	"mime/multipart"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

const (
	PlaybackActionPause    = "pause"
	PlaybackActionResume   = "resume"
	PlaybackActionStop     = "stop"
	PlaybackActionSeek     = "seek"
	PlaybackActionReplay   = "replay"
	PlaybackActionPrevious = "previous"
	PlaybackActionNext     = "next"

	QuickCastEncodingForm      = "form"
	QuickCastEncodingMultipart = "multipart"

	// ErrActiveSessionChangedMessage is the canonical error string providers
	// and the UI route return when a guarded action's session key no longer
	// matches the live session. Surfaced through the banner's transient
	// message slot.
	ErrActiveSessionChangedMessage = "active session changed"
)

// ErrActiveSessionChanged is the typed sentinel form of
// ErrActiveSessionChangedMessage. Providers return or wrap this when the
// caller's adapter_ref + generation no longer matches the active session;
// the playback dispatcher maps it to HTTP 409 for the chassis and to the
// existing inline-error banner message for /ui.
var ErrActiveSessionChanged = errors.New(ErrActiveSessionChangedMessage)

// ErrPlaybackActionUnsupported is returned by the dispatcher when the
// active adapter does not implement PlaybackControlProvider, and by
// providers that recognize the action verb but don't support it on the
// active session. Maps to HTTP 422 on the chassis; /ui surfaces the
// provider's existing inline message.
var ErrPlaybackActionUnsupported = errors.New("active adapter does not expose playback controls")

type playbackActionUnsupportedError struct {
	message string
}

func (e playbackActionUnsupportedError) Error() string {
	if e.message != "" {
		return e.message
	}
	return ErrPlaybackActionUnsupported.Error()
}

func (e playbackActionUnsupportedError) Unwrap() error {
	return ErrPlaybackActionUnsupported
}

// UnsupportedPlaybackActionError returns an error whose visible message
// is exactly the supplied provider message, while errors.Is still matches
// ErrPlaybackActionUnsupported. Providers use this instead of fmt.Errorf
// with %w so /ui's legacy banner text stays byte-for-byte compatible.
func UnsupportedPlaybackActionError(message string) error {
	return playbackActionUnsupportedError{message: message}
}

type PlaybackControlProvider interface {
	// PlaybackBanner returns adapter-specific banner controls and true when
	// the adapter owns the supplied active session. False means the shared UI
	// should keep its default read-only rendering for that snapshot.
	PlaybackBanner(ctx context.Context, snap PlaybackBannerSnapshot) (view PlaybackBannerAdapterView, owns bool)
	HandlePlaybackAction(ctx context.Context, action PlaybackActionRequest) (PlaybackActionResult, error)
}

type QuickCastProvider interface {
	QuickCastTabs() []QuickCastTab
	HandleQuickCast(ctx context.Context, req QuickCastRequest) (QuickCastResult, error)
}

type PlaybackBannerSnapshot struct {
	State      core.State
	Source     string
	Title      string
	AdapterRef string
	Generation uint64
	Position   time.Duration
	Duration   time.Duration
	StartedAt  time.Time
	MediaKind  core.MediaKind
	Modeline   string
}

type PlaybackBannerAdapterView struct {
	Title         string
	Subtitle      string
	SourceDisplay string
	Actions       []PlaybackAction
	Seek          *PlaybackSeek
}

type PlaybackAction struct {
	ID             string
	Label          string
	Icon           string
	Enabled        bool
	DisabledReason string
}

type PlaybackActionRequest struct {
	Action     string
	AdapterRef string
	Generation uint64
	OffsetMS   int
}

type PlaybackActionResult struct {
	Message string
}

type PlaybackSeek struct {
	Enabled        bool
	DisabledReason string
	OffsetMS       int
	DurationMS     int
}

type QuickCastTab struct {
	ID             string
	Label          string
	Enabled        bool
	DisabledReason string
	Encoding       string
	Fields         []QuickCastField
}

type QuickCastRequest struct {
	TabID  string
	Values map[string]string
	File   *QuickCastFile
}

type QuickCastResult struct {
	Message    string
	AdapterRef string
}

type QuickCastField struct {
	Name        string
	Label       string
	Type        string
	Placeholder string
	Required    bool
	Options     []QuickCastOption
}

type QuickCastOption struct {
	Value string
	Label string
}

type QuickCastFile struct {
	FieldName string
	// Header is the parsed multipart file header supplied by the UI route so
	// providers can reuse existing upload validation without re-parsing HTTP.
	Header *multipart.FileHeader
}

// MaxQuickCastBytes caps multipart payloads accepted by QuickCastProvider
// implementations. Shared between /ui/playback/quick-cast and the chassis
// /receiver/cast route so the limit moves in lockstep.
const MaxQuickCastBytes = 4*1024*1024 + 64*1024

// QuickCastError is the typed error returned by QuickCastProvider
// implementations when a quick-cast attempt fails for a known reason.
// The chassis JSON route extracts Status/Chip via errors.As; the
// existing /ui route uses Error() for inline-banner rendering.
//
// Adapter implementations set Status to the HTTP status the chassis
// should emit and Chip to the short uppercase text the chassis chip
// displays. Cause is the wrapped underlying error (may be nil); Message
// is the human-readable form preferred by Error() when set.
type QuickCastError struct {
	Status  int
	Chip    string
	Message string
	Cause   error
}

func (e *QuickCastError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Chip
}

func (e *QuickCastError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
