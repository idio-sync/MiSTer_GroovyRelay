package plex

// BuildProfileExtra returns the X-Plex-Client-Profile-Extra string that
// overrides the server-side profile lookup. Structured as semicolon-separated
// conditions. See docs/references/plex-mpv-shim.md §"Device Capability
// Profile" for the syntax.
//
// This is a conservative profile that forces PMS to transcode everything
// to H.264 Main@L3.1 at <=720x480 progressive, AAC 2-channel, <=30 fps.
// 480 is the tallest dimension the MiSTer's NTSC 480i modeline can display;
// forcing a transcode also lets us subtitle-burn-in server-side when needed.
func BuildProfileExtra() string {
	return "" +
		"add-transcode-target(type=videoProfile&protocol=http&container=mp4&codec=h264&videoCodec=h264&audioCodec=aac);" +
		"add-transcode-target-settings(type=videoProfile&context=streaming&CopyInternalSubs=true&BurnSubtitles=true);" +
		"add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.width&value=720&isRequired=true);" +
		"add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.height&value=480&isRequired=true);" +
		"add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.framerate&value=30&isRequired=true);" +
		"add-limitation(scope=audioCodec&scopeName=aac&type=upperBound&name=audio.channels&value=2)"
}

// BuildClientCapabilities returns the X-Plex-Client-Capabilities value we
// announce to PMS when requesting a transcoded stream. Kept terse; PMS uses
// it as a hint when choosing protocol/container.
func BuildClientCapabilities() string {
	return "protocols=http-video,http-mp4-video,http-hls,http-streaming-video,http-streaming-video-720p;" +
		"videoDecoders=h264{profile:baseline,main,high;resolution:480;level:41};" +
		"audioDecoders=aac"
}
