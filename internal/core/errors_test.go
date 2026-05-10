package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

// TestProbeForStart_WrapsErrProbeUnreachable proves the P3.0 contract:
// any probeFn failure flows back through probeForStart with
// ErrProbeUnreachable in the error chain, so DLNA handlers can fault-
// map via errors.Is. The underlying ffprobe error remains accessible
// for slog / operator logging.
func TestProbeForStart_WrapsErrProbeUnreachable(t *testing.T) {
	origProbe := probeFn
	t.Cleanup(func() { probeFn = origProbe })

	sentinelInner := errors.New("ffprobe: dial tcp: connection refused")
	probeFn = func(_ context.Context, _, _ string, _ ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return nil, sentinelInner
	}

	m := newTestManager(t)
	_, _, _, err := m.probeForStart(SessionRequest{StreamURL: "https://example/clip.mp4"})
	if err == nil {
		t.Fatal("probeForStart returned nil error; want wrapped failure")
	}
	if !errors.Is(err, ErrProbeUnreachable) {
		t.Errorf("errors.Is(err, ErrProbeUnreachable) = false; err=%v", err)
	}
	if !errors.Is(err, sentinelInner) {
		t.Errorf("inner ffprobe error not preserved in chain: %v", err)
	}
}

// TestErrPolicyRejected_Exists is a smoke test pinning the sentinel's
// existence and uniqueness. P3.0 reserves this for future MediaInputPolicy
// boundary rejection; the value must remain importable so adapters can
// pre-wire errors.Is switches without a follow-up signature change.
func TestErrPolicyRejected_Exists(t *testing.T) {
	if ErrPolicyRejected == nil {
		t.Fatal("ErrPolicyRejected sentinel is nil")
	}
	// Distinctness check: the three sentinels MUST be separate values
	// so a wrap site can pick one without the others matching.
	if errors.Is(ErrPolicyRejected, ErrProbeUnreachable) ||
		errors.Is(ErrPolicyRejected, ErrPlaneError) {
		t.Error("ErrPolicyRejected aliases another core sentinel")
	}
}

// TestErrPlaneError_WrapsModelineFailure proves resolve errors in
// startPlaneLocked surface ErrPlaneError. We can't easily reach
// startPlaneLocked synchronously without spinning up a sender + active
// session, so the test exercises the wrap pattern via a SessionRequest
// that drives the corrupted bridge config through Manager.StartSession.
//
// The ffprobe stub returns a usable result so probeForStart succeeds
// and execution reaches the resolve step under m.mu.
func TestErrPlaneError_WrapsModelineFailure(t *testing.T) {
	origProbe := probeFn
	origCrop := probeCropFn
	t.Cleanup(func() {
		probeFn = origProbe
		probeCropFn = origCrop
	})

	probeFn = func(_ context.Context, _, _ string, _ ffmpeg.MediaInputPolicy) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 1920, Height: 1080, FrameRate: 23.976}, nil
	}
	probeCropFn = func(_ context.Context, _, _ string, _ map[string]string, _ time.Duration, _ ffmpeg.MediaInputPolicy) (*ffmpeg.CropRect, error) {
		return nil, nil
	}

	m := newTestManager(t)
	// Corrupt the modeline name so ResolvePreset fails. This is
	// representative of operator-visible config corruption — the only
	// realistic way these errors fire in production.
	m.bridge.Video.Modeline = "NOT_A_REAL_MODELINE"

	err := m.StartSession(SessionRequest{StreamURL: "https://example/clip.mp4"})
	if err == nil {
		t.Fatal("StartSession returned nil; want plane setup failure")
	}
	if !errors.Is(err, ErrPlaneError) {
		t.Errorf("errors.Is(err, ErrPlaneError) = false; err=%v", err)
	}
}
