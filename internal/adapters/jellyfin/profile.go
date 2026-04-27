package jellyfin

// DeviceProfile is the subset of JF's DeviceProfile schema the bridge
// uses. Fields are JSON-tagged to match JF's PascalCase wire format.
// Schema reference: Jellyfin OpenAPI stable 10.10.x; spec
// docs/specs/2026-04-25-jellyfin-adapter-design.md §"DeviceProfile".
type DeviceProfile struct {
	Name                string               `json:"Name"`
	MaxStreamingBitrate int                  `json:"MaxStreamingBitrate"`
	DirectPlayProfiles  []DirectPlayProfile  `json:"DirectPlayProfiles"`
	TranscodingProfiles []TranscodingProfile `json:"TranscodingProfiles"`
	CodecProfiles       []CodecProfile       `json:"CodecProfiles"`
	SubtitleProfiles    []SubtitleProfile    `json:"SubtitleProfiles"`
	ContainerProfiles   []ContainerProfile   `json:"ContainerProfiles"`
}

type DirectPlayProfile struct {
	Container  string `json:"Container"`
	AudioCodec string `json:"AudioCodec,omitempty"`
	VideoCodec string `json:"VideoCodec,omitempty"`
	Type       string `json:"Type"`
}

type TranscodingProfile struct {
	Container             string `json:"Container"`
	Type                  string `json:"Type"`
	VideoCodec            string `json:"VideoCodec"`
	AudioCodec            string `json:"AudioCodec"`
	Protocol              string `json:"Protocol"`
	Context               string `json:"Context"`
	MaxAudioChannels      string `json:"MaxAudioChannels"`
	EstimateContentLength bool   `json:"EstimateContentLength"`
	EnableMpegtsM2TsMode  bool   `json:"EnableMpegtsM2TsMode"`
	BreakOnNonKeyFrames   bool   `json:"BreakOnNonKeyFrames"`
}

type CodecProfile struct {
	Type       string             `json:"Type"`
	Codec      string             `json:"Codec"`
	Conditions []ProfileCondition `json:"Conditions"`
}

type ProfileCondition struct {
	Condition  string `json:"Condition"`
	Property   string `json:"Property"`
	Value      string `json:"Value"`
	IsRequired bool   `json:"IsRequired"`
}

type SubtitleProfile struct {
	Format string `json:"Format"`
	Method string `json:"Method"`
}

type ContainerProfile struct {
	Type       string             `json:"Type"`
	Container  string             `json:"Container"`
	Conditions []ProfileCondition `json:"Conditions,omitempty"`
}

// BuildDeviceProfile constructs the DeviceProfile sent to JF in both
// /Sessions/Capabilities/Full and every /Items/{id}/PlaybackInfo body.
// The shape forces server-side transcode to MPEG-TS / H.264 / AAC /
// stereo / ≤720×480 / ≤30 fps with subtitles burned in.
func BuildDeviceProfile(maxVideoBitrateKbps int) DeviceProfile {
	return DeviceProfile{
		Name:                "MiSTer_GroovyRelay",
		MaxStreamingBitrate: maxVideoBitrateKbps * 1000,
		DirectPlayProfiles:  nil, // explicit nil → no direct play
		TranscodingProfiles: []TranscodingProfile{{
			Container:             "ts",
			Type:                  "Video",
			VideoCodec:            "h264",
			AudioCodec:            "aac",
			Protocol:              "http",
			Context:               "Streaming",
			MaxAudioChannels:      "2",
			EstimateContentLength: false,
			EnableMpegtsM2TsMode:  false,
			BreakOnNonKeyFrames:   false,
		}},
		CodecProfiles: []CodecProfile{{
			Type:  "Video",
			Codec: "h264",
			Conditions: []ProfileCondition{
				{Condition: "LessThanEqual", Property: "Width", Value: "720", IsRequired: true},
				{Condition: "LessThanEqual", Property: "Height", Value: "480", IsRequired: true},
				{Condition: "LessThanEqual", Property: "VideoFramerate", Value: "30", IsRequired: true},
			},
		}},
		SubtitleProfiles: []SubtitleProfile{
			{Format: "srt", Method: "Encode"},
			{Format: "ass", Method: "Encode"},
			{Format: "pgs", Method: "Encode"},
		},
	}
}
