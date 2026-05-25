//go:build integration

package integration

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

func TestVisualizerModesSpawnRealFFmpeg(t *testing.T) {
	cases := []struct {
		name          string
		mode          ffmpeg.VisualizerMode
		suppressAudio bool
	}{
		{"vu cabinet", ffmpeg.VisualizerModeVUCabinet, false},
		{"neon grid", ffmpeg.VisualizerModeNeonGrid, false},
		{"raster pulse", ffmpeg.VisualizerModeRasterPulse, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ffmpegPath := ffmpegPathOrSkip(t)
			skipIfVisualizerFiltersMissing(t, ffmpegPath, tc.mode)
			inputPath := ensureSampleMP4(t, "visualizer-source.mp4", 2)

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			spec := ffmpeg.PipelineSpec{
				FFmpegPath:          ffmpegPath,
				InputURL:            inputPath,
				SourceProbe:         &ffmpeg.ProbeResult{Width: 320, Height: 240, FrameRate: 24, AudioRate: 48000, Interlaced: false},
				OutputWidth:         720,
				OutputHeight:        480,
				OutputFpsExpr:       "60000/1001",
				FieldOrder:          "tff",
				AspectMode:          "letterbox",
				AudioSampleRate:     48000,
				AudioChannels:       2,
				SuppressAudioOutput: tc.suppressAudio,
				Visualizer:          ffmpeg.VisualizerSpec{Enabled: true, Mode: tc.mode},
			}

			p, err := ffmpeg.Spawn(ctx, spec)
			if err != nil {
				t.Fatalf("Spawn failed for %s with ffmpeg %q: %v", tc.mode, ffmpegPath, err)
			}
			defer p.Stop()

			videoDone := make(chan error, 1)
			audioDone := make(chan error, 1)
			go func() {
				frame := make([]byte, spec.OutputWidth*spec.OutputHeight*3)
				_, err := io.ReadFull(p.VideoPipe(), frame)
				videoDone <- err
			}()
			go func() {
				block := make([]byte, 4096)
				_, err := io.ReadFull(p.AudioPipe(), block)
				audioDone <- err
			}()

			for i := 0; i < 2; i++ {
				select {
				case err := <-videoDone:
					if err != nil {
						t.Fatalf("read video frame for %s: %v", tc.mode, err)
					}
				case err := <-audioDone:
					if err != nil {
						t.Fatalf("read audio block for %s: %v", tc.mode, err)
					}
				case <-ctx.Done():
					t.Fatalf("timed out reading visualizer output for %s: %v", tc.mode, ctx.Err())
				case <-p.Done():
					t.Fatalf("ffmpeg exited before visualizer output was read for %s", tc.mode)
				}
			}
		})
	}
}
