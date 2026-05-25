package hlsbuffer

import (
	"net/http"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type Config struct {
	Enabled                bool
	LiveEdgeSegments       int
	StartSegments          int
	MaxCachedSegments      int
	MaxCacheBytes          int64
	MaxPlaylistBytes       int64
	MaxSegmentBytes        int64
	SegmentTimeout         time.Duration
	PlaylistTimeout        time.Duration
	MaxVariantHeight       int
	StaleCacheReapInterval time.Duration
}

type SessionOptions struct {
	SourceURL    string
	CacheRoot    string
	Config       Config
	TrustMode    TrustMode
	OutputHeight int
	Validator    URLValidator
	Client       *http.Client
}

type Stats struct {
	CachedSegments        int
	CachedMediaDuration   time.Duration
	CacheBytes            int64
	PlaylistReloadsTotal  int64
	SegmentDownloadsTotal int64
	SelectedVariant       Variant
	FailureReason         string
}

type Session struct {
	PlaybackPath string
	Policy       core.MediaInputPolicy
	Stats        func() Stats
	Close        func() error
}

func NormalizeConfig(c Config) Config {
	return normalizeSessionConfig(c)
}
