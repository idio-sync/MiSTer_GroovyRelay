package adapters

import (
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestPlaybackBannerSnapshotCarriesFullSessionKey(t *testing.T) {
	snap := PlaybackBannerSnapshot{
		State:      core.StatePlaying,
		Source:     "url",
		Title:      "clip.mp4",
		AdapterRef: "url:abc",
		Generation: 42,
		Position:   5 * time.Second,
		Duration:   60 * time.Second,
		MediaKind:  core.MediaKindVideo,
		Modeline:   "NTSC_480i",
	}
	if snap.AdapterRef != "url:abc" || snap.Generation != 42 {
		t.Fatalf("session key = %q/%d", snap.AdapterRef, snap.Generation)
	}
}

func TestQuickCastEncodingConstants(t *testing.T) {
	if QuickCastEncodingForm != "form" {
		t.Fatalf("QuickCastEncodingForm = %q", QuickCastEncodingForm)
	}
	if QuickCastEncodingMultipart != "multipart" {
		t.Fatalf("QuickCastEncodingMultipart = %q", QuickCastEncodingMultipart)
	}
}
