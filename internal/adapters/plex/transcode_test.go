package plex

import (
	"strings"
	"testing"
)

func TestBuildTranscodeURL_ContainsExpectedParams(t *testing.T) {
	req := TranscodeRequest{
		PlexServerURL: "http://192.168.1.10:32400",
		MediaPath:     "/library/metadata/42",
		Token:         "xyz",
		OffsetMs:      0,
		OutputWidth:   720,
		OutputHeight:  480,
		ClientID:      "client-id-abc",
	}
	u := BuildTranscodeURL(req)
	for _, substr := range []string{
		"directPlay=0", "directStream=0", "copyts=1",
		"videoResolution=720x480", "X-Plex-Token=xyz",
	} {
		if !strings.Contains(u, substr) {
			t.Errorf("url missing %q: %s", substr, u)
		}
	}
}
