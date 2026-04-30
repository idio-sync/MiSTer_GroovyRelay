package plex

// BuildProfileExtra returns the X-Plex-Client-Profile-Extra string that
// overrides the server-side profile lookup. Structured as semicolon-separated
// conditions.
//
// This is a conservative streaming profile that forces PMS onto one stable
// shape: progressive HTTP / Matroska transport, H.264 video at <=720x480
// progressive, AAC stereo, <=30 fps. Keeping the server-side output
// predictable avoids source-to-source variance where PMS might otherwise
// choose a different container/codec path for media that is "already close
// enough" to our target.
//
// 480 is the tallest dimension the MiSTer's NTSC 480i modeline can display;
// forcing a transcode also lets us subtitle-burn-in server-side when needed.
func BuildProfileExtra() string {
	return "" +
		"add-transcode-target(type=videoProfile&context=streaming&protocol=http&container=mkv&videoCodec=h264&audioCodec=aac);" +
		"add-transcode-target-audio-codec(type=videoProfile&context=streaming&protocol=http&audioCodec=aac);" +
		"add-transcode-target-settings(type=videoProfile&context=streaming&CopyInternalSubs=true&BurnSubtitles=true);" +
		"add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.width&value=720&isRequired=true);" +
		"add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.height&value=480&isRequired=true);" +
		"add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.framerate&value=30&isRequired=true);" +
		"add-limitation(scope=audioCodec&scopeName=aac&type=upperBound&name=audio.channels&value=2)"
}

// BuildClientCapabilities returns the X-Plex-Client-Capabilities value we
// announce to PMS when requesting a transcoded stream. Kept terse; PMS uses
// it as a hint when choosing protocol/container. Advertise only the
// progressive-HTTP / H.264 / AAC stereo shape we actually want so PMS does
// not optimize into a different "compatible" path on already-low-resolution
// sources.
func BuildClientCapabilities() string {
	return "protocols=http-streaming-video,http-mkv-video;" +
		"videoDecoders=h264{profile:baseline,main,high;resolution:720x480;level:31};" +
		"audioDecoders=aac{channels:2}"
}
