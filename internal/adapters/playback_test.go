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

func TestQuickCastError_ErrorPrefersMessageOverChip(t *testing.T) {
	t.Parallel()
	e := &QuickCastError{Status: 400, Chip: "BAD URL", Message: "url could not be parsed"}
	if got := e.Error(); got != "url could not be parsed" {
		t.Errorf("Error() = %q, want %q", got, "url could not be parsed")
	}
}

func TestQuickCastError_ErrorFallsBackToCauseThenChip(t *testing.T) {
	t.Parallel()
	cause := fmt.Errorf("underlying failure")
	e := &QuickCastError{Status: 500, Chip: "CAST FAILED", Cause: cause}
	if got := e.Error(); got != "underlying failure" {
		t.Errorf("Error() with cause = %q, want %q", got, "underlying failure")
	}
	e2 := &QuickCastError{Status: 409, Chip: "BLOCKED"}
	if got := e2.Error(); got != "BLOCKED" {
		t.Errorf("Error() chip fallback = %q, want %q", got, "BLOCKED")
	}
}

func TestQuickCastError_UnwrapReturnsCause(t *testing.T) {
	t.Parallel()
	cause := fmt.Errorf("io: deadline exceeded")
	e := &QuickCastError{Status: 504, Chip: "TIMEOUT", Cause: cause}
	if got := errors.Unwrap(e); got != cause {
		t.Errorf("Unwrap() = %v, want %v", got, cause)
	}
}

func TestQuickCastError_ErrorsAsRoundTrip(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("wrap: %w", &QuickCastError{Status: 413, Chip: "FILE TOO BIG"})
	var qerr *QuickCastError
	if !errors.As(wrapped, &qerr) {
		t.Fatalf("errors.As failed to extract *QuickCastError from %v", wrapped)
	}
	if qerr.Status != 413 || qerr.Chip != "FILE TOO BIG" {
		t.Errorf("extracted = %+v, want Status=413 Chip=%q", qerr, "FILE TOO BIG")
	}
}

func TestQuickCastError_NilSafety(t *testing.T) {
	t.Parallel()
	var e *QuickCastError
	if got := e.Error(); got != "" {
		t.Errorf("nil.Error() = %q, want empty", got)
	}
	if got := e.Unwrap(); got != nil {
		t.Errorf("nil.Unwrap() = %v, want nil", got)
	}
}

func TestMaxQuickCastBytes_Exported(t *testing.T) {
	t.Parallel()
	// Sanity-check the exported constant exists and matches the legacy value.
	const want = 4*1024*1024 + 64*1024
	if MaxQuickCastBytes != want {
		t.Errorf("MaxQuickCastBytes = %d, want %d", MaxQuickCastBytes, want)
	}
}
