package core

import (
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func buildMeterHomeView(req SessionRequest, probe *ffmpeg.ProbeResult, crop *ffmpeg.CropRect, aspectMode string, preset ModelinePreset, modeline groovy.Modeline, fieldHeight int, rgbMode string, audioRate int, audioChans int, bridge config.BridgeConfig) MeterHomeView {
	var source SourceMeterView
	if probe != nil {
		source = SourceMeterView{
			Width:                 probe.Width,
			Height:                probe.Height,
			FrameRate:             probe.FrameRate,
			Interlaced:            probe.Interlaced,
			SampleAspectRatioNum:  probe.SampleAspectRatioNum,
			SampleAspectRatioDen:  probe.SampleAspectRatioDen,
			DisplayAspectRatioNum: probe.DisplayAspectRatioNum,
			DisplayAspectRatioDen: probe.DisplayAspectRatioDen,
			VideoCodec:            probe.VideoCodec,
			AudioCodec:            probe.AudioCodec,
			AudioRate:             probe.AudioRate,
			AudioChannels:         probe.AudioChannels,
			VideoBitrateBPS:       probe.VideoBitrateBPS,
			AudioBitrateBPS:       probe.AudioBitrateBPS,
			FormatBitrateBPS:      probe.FormatBitrateBPS,
		}
	}

	var cropView CropMeterView
	cropView.Mode = aspectMode
	if crop != nil {
		cropView.Detected = true
		cropView.W = crop.W
		cropView.H = crop.H
		cropView.X = crop.X
		cropView.Y = crop.Y
	}

	return MeterHomeView{
		Source: source,
		Crop:   cropView,
		Pipeline: PipelineMeterView{
			ModelineName:        preset.Name,
			OutputWidth:         int(modeline.HActive),
			OutputHeight:        int(modeline.VActive),
			FieldHeight:         fieldHeight,
			FieldRateHz:         modeline.FieldRate(),
			HorizontalKHz:       horizontalKHz(modeline),
			InterlacedOutput:    modeline.Interlaced(),
			Standard:            standardForModeline(preset.Name),
			FieldOrder:          bridge.Video.InterlaceFieldOrder,
			RGBMode:             rgbMode,
			LZ4Enabled:          bridge.Video.LZ4Enabled,
			DeltaLZ4Enabled:     bridge.Video.DeltaLZ4Enabled,
			AudioSampleRate:     audioRate,
			AudioChannels:       audioChans,
			AudioOutputVolume:   bridge.Audio.OutputVolume,
			EffectiveAspectMode: aspectMode,
		},
	}
}

func horizontalKHz(m groovy.Modeline) float64 {
	if m.HTotal == 0 {
		return 0
	}
	return m.PClock * 1000 / float64(m.HTotal)
}

func standardForModeline(name string) string {
	switch {
	case strings.HasPrefix(name, "NTSC_"):
		return "ntsc"
	case strings.HasPrefix(name, "PAL_"):
		return "pal"
	default:
		return ""
	}
}
