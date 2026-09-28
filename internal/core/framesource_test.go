package core

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/osd"
)

type blankSource struct{}

func (blankSource) ReadFrame(dst []byte) { clear(dst) }

func frameSourceRequest() SessionRequest {
	return SessionRequest{
		Frames:     blankSource{},
		AdapterRef: "calibration:1",
		Source:     "calibration",
		Title:      "Test pattern",
		QuietOSD:   true,
	}
}

// A frame-source session skips every probe, hands the source to the plane,
// runs video-only, and (with QuietOSD) shows no start banner.
func TestManager_FrameSourceSessionSkipsProbesAndAudio(t *testing.T) {
	m, display, configs := newOSDTestManager(t, 0)
	probeInputFn = func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		t.Error("frame-source session must not probe")
		return nil, nil
	}
	probeCropFn = func(context.Context, string, ffmpeg.CropProbeSpec) (*ffmpeg.CropRect, error) {
		t.Error("frame-source session must not crop-probe")
		return nil, nil
	}

	if err := m.StartSession(frameSourceRequest()); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if len(*configs) != 1 {
		t.Fatalf("planes built = %d, want 1", len(*configs))
	}
	cfg := (*configs)[0]
	if cfg.Frames == nil {
		t.Fatal("PlaneConfig.Frames not set")
	}
	if cfg.AudioRate != 0 || cfg.AudioChans != 0 || !cfg.SuppressAudioOutput {
		t.Fatalf("audio = %d Hz / %d ch / suppress=%v, want video-only", cfg.AudioRate, cfg.AudioChans, cfg.SuppressAudioOutput)
	}
	if got := display.Showing(osdTestNow); got != (osd.Showing{}) {
		t.Fatalf("QuietOSD session showed %+v, want nothing", got)
	}
	if st := m.Status(); st.AdapterRef != "calibration:1" || st.State != StatePlaying {
		t.Fatalf("status = ref %q state %v, want calibration:1 playing", st.AdapterRef, st.State)
	}
}

func TestManager_FrameSourceRejectsMediaInputs(t *testing.T) {
	m, _, configs := newOSDTestManager(t, 0)
	for name, mutate := range map[string]func(*SessionRequest){
		"stream url": func(r *SessionRequest) { r.StreamURL = "http://example.test/a.mp4" },
		"audio url":  func(r *SessionRequest) { r.AudioStreamURL = "http://example.test/a.m4a" },
		"capture":    func(r *SessionRequest) { r.AudioCapture.Enabled = true },
		"visualizer": func(r *SessionRequest) { r.Visualizer.Enabled = true },
		"probe url":  func(r *SessionRequest) { r.StreamProbeURL = "http://example.test/p" },
		"bad aspect": func(r *SessionRequest) { r.AspectMode = "stretch" },
	} {
		req := frameSourceRequest()
		mutate(&req)
		if err := m.StartSession(req); err == nil {
			t.Errorf("%s: StartSession accepted an invalid frame-source request", name)
		}
	}
	if len(*configs) != 0 {
		t.Fatalf("planes built = %d, want 0", len(*configs))
	}
}

// Without QuietOSD the start banner still shows, so the flag is what
// suppresses it.
func TestManager_StartBannerShowsWithoutQuietOSD(t *testing.T) {
	m, display, _ := newOSDTestManager(t, 0)
	req := frameSourceRequest()
	req.QuietOSD = false
	if err := m.StartSession(req); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if got := display.Showing(osdTestNow); got.Channel == "" || got.Transport != osd.TransportPlay {
		t.Fatalf("showing = %+v, want a channel banner and PLAY", got)
	}
}
