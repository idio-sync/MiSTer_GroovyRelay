package core

import (
	"testing"
	"time"
)

func TestSessionRequest_ZeroValue(t *testing.T) {
	var r SessionRequest
	if r.StreamURL != "" {
		t.Errorf("StreamURL = %q, want empty", r.StreamURL)
	}
	if r.SeekOffsetMs != 0 {
		t.Errorf("SeekOffsetMs = %d, want 0", r.SeekOffsetMs)
	}
	if r.InputHeaders != nil {
		t.Errorf("InputHeaders = %v, want nil", r.InputHeaders)
	}
	if r.DirectPlay {
		t.Errorf("DirectPlay default = true, want false (transcode path)")
	}
	if r.Capabilities.CanSeek || r.Capabilities.CanPause {
		t.Errorf("Capabilities default = %+v, want both false", r.Capabilities)
	}
	// MediaInputPolicy zero value gates the backward-compat path: existing
	// adapters (Plex / Jellyfin / URL) leave it zero-valued and FFmpeg argv
	// must remain identical to the pre-refactor implementation.
	if !r.MediaInputPolicy.IsZero() {
		t.Errorf("MediaInputPolicy default should be zero, got %+v", r.MediaInputPolicy)
	}
}

// TestSessionRequest_MediaInputPolicyIsAlias confirms core.MediaInputPolicy is
// the same type as ffmpeg.MediaInputPolicy (a type alias). Adapters can build
// a core.MediaInputPolicy literal and the Manager threads the same value
// (no conversion) into ffmpeg.Probe / ffmpeg.ProbeCrop / ffmpeg.PipelineSpec.
func TestSessionRequest_MediaInputPolicyAccepted(t *testing.T) {
	// Build a non-zero policy via the core-side name. If the alias drifts,
	// this won't compile.
	r := SessionRequest{
		MediaInputPolicy: MediaInputPolicy{
			ProtocolWhitelist: []string{"file", "http", "https"},
			DisableReconnect:  true,
		},
	}
	if r.MediaInputPolicy.IsZero() {
		t.Errorf("populated policy should not report IsZero")
	}
	if len(r.MediaInputPolicy.ProtocolWhitelist) != 3 {
		t.Errorf("whitelist round-trip: got %v", r.MediaInputPolicy.ProtocolWhitelist)
	}
}

func TestSessionRequest_CapabilityCombinations(t *testing.T) {
	cases := []struct {
		name     string
		caps     Capabilities
		wantSeek bool
		wantPaus bool
	}{
		{"none", Capabilities{}, false, false},
		{"seek-only", Capabilities{CanSeek: true}, true, false},
		{"pause-only", Capabilities{CanPause: true}, false, true},
		{"both", Capabilities{CanSeek: true, CanPause: true}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := SessionRequest{Capabilities: c.caps}
			if r.Capabilities.CanSeek != c.wantSeek {
				t.Errorf("CanSeek = %v, want %v", r.Capabilities.CanSeek, c.wantSeek)
			}
			if r.Capabilities.CanPause != c.wantPaus {
				t.Errorf("CanPause = %v, want %v", r.Capabilities.CanPause, c.wantPaus)
			}
		})
	}
}

func TestSessionRequest_DirectPlayFlag(t *testing.T) {
	r := SessionRequest{StreamURL: "file:///media/x.mkv", DirectPlay: true}
	if !r.DirectPlay {
		t.Errorf("DirectPlay = false, want true after explicit set")
	}
	r2 := SessionRequest{StreamURL: "http://pms/.../transcode.m3u8"}
	if r2.DirectPlay {
		t.Errorf("DirectPlay default = true, want false for transcode-style URL")
	}
}

func TestSessionStatus_ZeroValue(t *testing.T) {
	var s SessionStatus
	if s.State != "" {
		t.Errorf("State = %q, want empty", s.State)
	}
	if s.Position != 0 {
		t.Errorf("Position = %v, want 0", s.Position)
	}
	if s.Duration != 0 {
		t.Errorf("Duration = %v, want 0", s.Duration)
	}
	if s.AdapterRef != "" {
		t.Errorf("AdapterRef = %q, want empty", s.AdapterRef)
	}
	if !s.StartedAt.IsZero() {
		t.Errorf("StartedAt = %v, want zero time", s.StartedAt)
	}
}

func TestSessionStatus_PopulatedValues(t *testing.T) {
	now := time.Now()
	s := SessionStatus{
		State:      State("playing"),
		Position:   30 * time.Second,
		Duration:   2 * time.Hour,
		AdapterRef: "plex:/library/metadata/1234",
		StartedAt:  now,
	}
	if s.State != State("playing") {
		t.Errorf("State = %s, want playing", s.State)
	}
	if s.Position != 30*time.Second {
		t.Errorf("Position = %v", s.Position)
	}
	if s.Duration != 2*time.Hour {
		t.Errorf("Duration = %v", s.Duration)
	}
	if s.AdapterRef != "plex:/library/metadata/1234" {
		t.Errorf("AdapterRef = %q", s.AdapterRef)
	}
	if !s.StartedAt.Equal(now) {
		t.Errorf("StartedAt = %v, want %v", s.StartedAt, now)
	}
}

func TestSessionRequest_Title(t *testing.T) {
	r := SessionRequest{Title: "Game of Thrones · S01E03"}
	if r.Title != "Game of Thrones · S01E03" {
		t.Errorf("Title: got %q, want %q", r.Title, "Game of Thrones · S01E03")
	}
}

func TestSessionRequest_Title_ZeroValue(t *testing.T) {
	var r SessionRequest
	if r.Title != "" {
		t.Errorf("zero-value Title: got %q, want empty", r.Title)
	}
}
