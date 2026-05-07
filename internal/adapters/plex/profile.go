package plex

import (
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// BuildProfileExtra returns the X-Plex-Client-Profile-Extra string that
// overrides the server-side profile lookup. Structured as semicolon-separated
// conditions.
//
// This is a conservative streaming profile that forces PMS onto one stable
// shape: progressive HTTP / MPEG-TS transport, H.264 video at <=720x480
// progressive, AAC stereo, <=30 fps. Keeping the server-side output
// predictable avoids source-to-source variance where PMS might otherwise
// choose a different container/codec path for media that is "already close
// enough" to our target.
//
// MPEG-TS is the container PMS already produces internally for HLS segments
// and the same container Jellyfin's adapter requests; its per-packet sync
// bytes give the streaming demuxer fast resync on any in-stream hiccup,
// which avoids the visible frame corruption that progressive MKV's
// length-prefixed clusters can produce until the next keyframe.
//
// 480 is the tallest dimension the MiSTer's NTSC 480i modeline can display;
// forcing a transcode also lets us subtitle-burn-in server-side when needed.
func BuildProfileExtra(preset core.ModelinePreset) string {
	preset = defaultPreset(preset)
	capW := int(preset.Modeline.HActive)
	h := preset.Modeline.SourceHeight()
	fps := preset.Modeline.SourceFps()
	return "" +
		"add-transcode-target(type=videoProfile&context=streaming&protocol=http&container=mpegts&videoCodec=h264&audioCodec=aac);" +
		"add-transcode-target-audio-codec(type=videoProfile&context=streaming&protocol=http&audioCodec=aac);" +
		"add-transcode-target-settings(type=videoProfile&context=streaming&CopyInternalSubs=true&BurnSubtitles=true);" +
		fmt.Sprintf("add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.width&value=%d&isRequired=true);", capW) +
		fmt.Sprintf("add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.height&value=%d&isRequired=true);", h) +
		fmt.Sprintf("add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.framerate&value=%d&isRequired=true);", fps) +
		"add-limitation(scope=audioCodec&scopeName=aac&type=upperBound&name=audio.channels&value=2)"
}

// BuildClientCapabilities returns the X-Plex-Client-Capabilities value we
// announce to PMS when requesting a transcoded stream. Kept terse; PMS uses
// it as a hint when choosing protocol/container. Advertise only the
// progressive-HTTP / H.264 / AAC stereo shape we actually want so PMS does
// not optimize into a different "compatible" path on already-low-resolution
// sources.
func BuildClientCapabilities(preset core.ModelinePreset) string {
	preset = defaultPreset(preset)
	w, h := preset.Modeline.PlexTranscodeSize()
	return "protocols=http-streaming-video,http-mp2t-video;" +
		fmt.Sprintf("videoDecoders=h264{profile:baseline,main,high;resolution:%dx%d;level:31};", w, h) +
		"audioDecoders=aac{channels:2}"
}

func defaultPreset(preset core.ModelinePreset) core.ModelinePreset {
	if preset.Name != "" {
		return preset
	}
	preset, _ = core.ResolvePreset("")
	return preset
}
