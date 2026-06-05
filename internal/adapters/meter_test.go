package adapters

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
)

func TestHLSMeterOverlayFromStats_AllowsOnlySafeFields(t *testing.T) {
	stats := hlsbuffer.Stats{
		CachedSegments:        4,
		CachedMediaDuration:   6 * time.Second,
		CacheBytes:            1234,
		PlaylistReloadsTotal:  9,
		SegmentDownloadsTotal: 10,
		SelectedVariant: hlsbuffer.Variant{
			URI:       "live.m3u8?token=secret",
			Width:     1280,
			Height:    720,
			Bandwidth: 2200000,
			Codecs:    "avc1.secret",
		},
		FailureReason: "fetch /live.m3u8?token=secret failed",
	}
	got := HLSMeterOverlayFromStats(stats, 12)
	if got.CachedSegments != 4 || got.MaxCachedSegments != 12 || got.CacheBytes != 1234 {
		t.Fatalf("cache fields = %+v", got)
	}
	if got.CachedMediaDurationMS != 6000 {
		t.Fatalf("CachedMediaDurationMS = %d, want 6000", got.CachedMediaDurationMS)
	}
	if got.SelectedVariantWidth != 1280 || got.SelectedVariantHeight != 720 || got.SelectedVariantBPS != 2200000 {
		t.Fatalf("variant fields = %+v", got)
	}
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leak := range []string{"http://", "https://", "://", "/live.m3u8", "token=", "secret", "Authorization", "avc1"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("serialized overlay leaked %q: %s", leak, body)
		}
	}
}

func TestHLSMeterOverlayFromStats_AllowsShortFailureEnums(t *testing.T) {
	got := HLSMeterOverlayFromStats(hlsbuffer.Stats{FailureReason: "playlist-timeout"}, 6)
	if got.FailureReason != "playlist-timeout" {
		t.Fatalf("FailureReason = %q, want playlist-timeout", got.FailureReason)
	}
}
