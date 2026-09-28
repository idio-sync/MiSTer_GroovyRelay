package core

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/osd"
)

// The calibrated picture size/position reaches both the ffmpeg pipeline (in
// raster lines) and the OSD (in field rows, halved on interlaced output).
func TestManager_PictureGeometryReachesPipelineAndOSD(t *testing.T) {
	cases := []struct {
		modeline    string
		wantPicture ffmpeg.PictureArea
		wantOSD     osd.Rect
	}{
		{"NTSC_480i", ffmpeg.PictureArea{X: 40, Y: 20, W: 648, H: 432}, osd.Rect{X: 40, Y: 10, W: 648, H: 216}},
		{"NTSC_240p", ffmpeg.PictureArea{X: 40, Y: 10, W: 648, H: 216}, osd.Rect{X: 40, Y: 10, W: 648, H: 216}},
	}
	for _, tc := range cases {
		t.Run(tc.modeline, func(t *testing.T) {
			origProbe := probeInputFn
			origCrop := probeCropFn
			origNewPlane := newPlane
			t.Cleanup(func() {
				probeInputFn = origProbe
				probeCropFn = origCrop
				newPlane = origNewPlane
			})
			probeInputFn = func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
				return &ffmpeg.ProbeResult{Width: 640, Height: 480, FrameRate: 60}, nil
			}
			probeCropFn = func(context.Context, string, ffmpeg.CropProbeSpec) (*ffmpeg.CropRect, error) { return nil, nil }
			var captured dataplane.PlaneConfig
			done := make(chan struct{})
			t.Cleanup(func() { close(done) })
			newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
				captured = cfg
				return &blockingDonePlane{done: done}
			}

			m := newTestManager(t)
			m.bridge.Video.Modeline = tc.modeline
			m.bridge.Video.PictureHSize = 90
			m.bridge.Video.PictureVSize = 90
			m.bridge.Video.PictureHOffset = 4
			m.bridge.Video.PictureVOffset = -2
			if err := m.StartSession(SessionRequest{StreamURL: "http://example/clip.mp4", AdapterRef: "url:clip", DirectPlay: true}); err != nil {
				t.Fatalf("StartSession: %v", err)
			}
			if got := captured.SpawnSpec.Picture; got != tc.wantPicture {
				t.Fatalf("pipeline picture = %+v, want %+v", got, tc.wantPicture)
			}
			if got := captured.OSDPicture; got != tc.wantOSD {
				t.Fatalf("OSD picture = %+v, want %+v", got, tc.wantOSD)
			}
		})
	}
}
