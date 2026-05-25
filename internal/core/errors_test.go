package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

// TestProbeForStart_WrapsErrProbeUnreachableForNetworkFailures proves
// the fault-mapping contract for source reachability failures: DLNA
// handlers can map errors.Is(ErrProbeUnreachable) to UPnP 716 while the
// underlying ffprobe error remains available for slog/operator logging.
func TestProbeForStart_WrapsErrProbeUnreachableForNetworkFailures(t *testing.T) {
	origProbe := probeInputFn
	t.Cleanup(func() { probeInputFn = origProbe })

	sentinelInner := errors.New("ffprobe: dial tcp: connection refused")
	probeInputFn = func(_ context.Context, _ string, _ ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
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

func TestProbeForStart_DoesNotWrapProbeParseFailureAsUnreachable(t *testing.T) {
	origProbe := probeInputFn
	t.Cleanup(func() { probeInputFn = origProbe })

	sentinelInner := errors.New("parse ffprobe: invalid character 'n'")
	probeInputFn = func(_ context.Context, _ string, _ ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		return nil, sentinelInner
	}

	m := newTestManager(t)
	_, _, _, err := m.probeForStart(SessionRequest{StreamURL: "https://example/clip.mp4"})
	if err == nil {
		t.Fatal("probeForStart returned nil error; want wrapped failure")
	}
	if errors.Is(err, ErrProbeUnreachable) {
		t.Errorf("errors.Is(err, ErrProbeUnreachable) = true for parse failure; err=%v", err)
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
	origProbe := probeInputFn
	origCrop := probeCropFn
	t.Cleanup(func() {
		probeInputFn = origProbe
		probeCropFn = origCrop
	})

	probeInputFn = func(_ context.Context, _ string, _ ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
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
