package adapters

import (
	"errors"
	"fmt"
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

func TestErrActiveSessionChanged_IsErrorsIsFriendly(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("provider context: %w", ErrActiveSessionChanged)
	if !errors.Is(wrapped, ErrActiveSessionChanged) {
		t.Fatalf("wrapped sentinel should satisfy errors.Is")
	}
	if ErrActiveSessionChanged.Error() != ErrActiveSessionChangedMessage {
		t.Errorf("Error() = %q, want %q", ErrActiveSessionChanged.Error(), ErrActiveSessionChangedMessage)
	}
}

func TestErrPlaybackActionUnsupported_IsErrorsIsFriendly(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("provider context: %w", ErrPlaybackActionUnsupported)
	if !errors.Is(wrapped, ErrPlaybackActionUnsupported) {
		t.Fatalf("wrapped sentinel should satisfy errors.Is")
	}
}

func TestUnsupportedPlaybackActionError_PreservesMessageAndUnwraps(t *testing.T) {
	t.Parallel()
	const msg = "streams adapter does not support previous"
	err := UnsupportedPlaybackActionError(msg)
	if err.Error() != msg {
		t.Fatalf("Error() = %q, want %q", err.Error(), msg)
	}
	if !errors.Is(err, ErrPlaybackActionUnsupported) {
		t.Fatalf("UnsupportedPlaybackActionError should unwrap to ErrPlaybackActionUnsupported")
	}
}
