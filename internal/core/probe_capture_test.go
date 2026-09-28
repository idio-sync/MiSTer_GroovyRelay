package core

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

// A capture source that serves a single reader (the live audio relay)
// gives the probe its own device, so probing a restart does not steal the
// running ffmpeg's input.
func TestProbeForStart_ProbesCaptureProbeDevice(t *testing.T) {
	origProbe := probeInputFn
	t.Cleanup(func() { probeInputFn = origProbe })

	var got ffmpeg.ProbeInputSpec
	probeInputFn = func(_ context.Context, _ string, in ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		got = in
		return &ffmpeg.ProbeResult{AudioCodec: "pcm_s16le", AudioRate: 44100, AudioChannels: 2}, nil
	}

	m := newTestManager(t)
	req := SessionRequest{
		AudioCapture: AudioCaptureInput{
			Enabled:     true,
			Format:      "s16le",
			Device:      "http://127.0.0.1:32500/pcm/tok",
			ProbeDevice: "http://127.0.0.1:32500/pcm/tok?probe=1",
			SampleRate:  44100,
			Channels:    2,
		},
	}
	if _, _, _, err := m.probeForStart(req); err != nil {
		t.Fatalf("probeForStart: %v", err)
	}
	if got.Capture.Device != req.AudioCapture.ProbeDevice {
		t.Fatalf("probed capture device %q, want the probe device %q", got.Capture.Device, req.AudioCapture.ProbeDevice)
	}

	req.AudioCapture.ProbeDevice = ""
	if _, _, _, err := m.probeForStart(req); err != nil {
		t.Fatalf("probeForStart without a probe device: %v", err)
	}
	if got.Capture.Device != req.AudioCapture.Device {
		t.Fatalf("probed capture device %q, want Device %q when no probe device is set", got.Capture.Device, req.AudioCapture.Device)
	}
}
