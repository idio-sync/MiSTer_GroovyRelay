package adapters

import (
	"context"
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
)

type PlaybackControlProvider interface {
	PlaybackBanner(ctx context.Context, snap PlaybackBannerSnapshot) (PlaybackBannerAdapterView, bool)
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
	Header    *multipart.FileHeader
}
