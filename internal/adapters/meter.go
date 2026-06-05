package adapters

import (
	"context"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
)

type MeterOverlayProvider interface {
	MeterOverlay(ctx context.Context, snap core.StatusHomeView) (MeterOverlay, bool)
}

type MeterOverlay struct {
	HLS *HLSMeterOverlay
}

type HLSMeterOverlay struct {
	CachedSegments        int
	MaxCachedSegments     int
	CachedMediaDurationMS int
	CacheBytes            int64
	PlaylistReloadsTotal  int64
	SegmentDownloadsTotal int64
	SelectedVariantWidth  int
	SelectedVariantHeight int
	SelectedVariantBPS    int64
	FailureReason         string
}

func HLSMeterOverlayFromStats(stats hlsbuffer.Stats, maxCachedSegments int) *HLSMeterOverlay {
	return &HLSMeterOverlay{
		CachedSegments:        stats.CachedSegments,
		MaxCachedSegments:     maxCachedSegments,
		CachedMediaDurationMS: int(stats.CachedMediaDuration.Milliseconds()),
		CacheBytes:            stats.CacheBytes,
		PlaylistReloadsTotal:  stats.PlaylistReloadsTotal,
		SegmentDownloadsTotal: stats.SegmentDownloadsTotal,
		SelectedVariantWidth:  stats.SelectedVariant.Width,
		SelectedVariantHeight: stats.SelectedVariant.Height,
		SelectedVariantBPS:    int64(stats.SelectedVariant.Bandwidth),
		FailureReason:         sanitizeHLSFailureReason(stats.FailureReason),
	}
}

func sanitizeHLSFailureReason(raw string) string {
	reason := strings.TrimSpace(raw)
	if reason == "" {
		return ""
	}
	lower := strings.ToLower(reason)
	blocked := []string{"://", "/", "\\", "token=", "sig=", "secret", "authorization", "cookie", ".m3u8", ".ts", ".m4s"}
	for _, needle := range blocked {
		if strings.Contains(lower, needle) {
			return "hls-error"
		}
	}
	if len(reason) > 64 {
		return "hls-error"
	}
	return reason
}
