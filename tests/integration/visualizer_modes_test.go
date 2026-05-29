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
		usesCover     bool
	}{
		{"vu cabinet", ffmpeg.VisualizerModeVUCabinet, false, false},
		{"waterfall", ffmpeg.VisualizerModeSpectrumWaterfall, false, false},
		{"raster pulse", ffmpeg.VisualizerModeRasterPulse, false, false},
		{"cover vu", ffmpeg.VisualizerModeCoverVU, false, true},
		{"cover spectrum", ffmpeg.VisualizerModeCoverSpectrum, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ffmpegPath := ffmpegPathOrSkip(t)
			skipIfVisualizerFiltersMissing(t, ffmpegPath, tc.mode)
			inputPath := ensureSampleWAV(t, "visualizer-source.wav", 2)

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			spec := ffmpeg.PipelineSpec{
				FFmpegPath:          ffmpegPath,
				InputURL:            inputPath,
				SourceProbe:         &ffmpeg.ProbeResult{AudioRate: 48000},
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
			if tc.usesCover {
				spec.Visualizer.Metadata.ArtworkPath = ensureSamplePNG(t, "visualizer-cover.png")
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
