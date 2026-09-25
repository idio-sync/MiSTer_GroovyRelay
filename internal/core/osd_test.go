package core

import (
	"context"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/osd"
)

var osdTestNow = time.Date(2026, 9, 24, 21, 41, 0, 0, time.UTC)

// newOSDTestManager returns a Manager wired to an enabled OSD display, a
// frozen clock, stubbed probes, and a plane factory that records every
// PlaneConfig and reports pos as the live position.
func newOSDTestManager(t *testing.T, pos time.Duration) (*Manager, *osd.Display, *[]dataplane.PlaneConfig) {
	t.Helper()
	origProbe, origCrop, origNewPlane := probeInputFn, probeCropFn, newPlane
	t.Cleanup(func() {
		probeInputFn, probeCropFn, newPlane = origProbe, origCrop, origNewPlane
	})
	probeInputFn = func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		return &ffmpeg.ProbeResult{Width: 640, Height: 480, FrameRate: 60, Duration: 600}, nil
	}
	probeCropFn = func(context.Context, string, ffmpeg.CropProbeSpec) (*ffmpeg.CropRect, error) {
		return nil, nil
	}
	var configs []dataplane.PlaneConfig
	newPlane = func(cfg dataplane.PlaneConfig) planeRunner {
		configs = append(configs, cfg)
		return &positionPlane{contextDonePlane: &contextDonePlane{done: make(chan struct{})}, pos: pos}
	}

	display := osd.NewDisplay(osd.Options{Enabled: true})
	m := newTestManager(t, WithOSD(display))
	m.now = func() time.Time { return osdTestNow }
	return m, display, &configs
}

// positionPlane exits on context cancel (so pause and seek can await it)
// and reports a fixed live position.
type positionPlane struct {
	*contextDonePlane
	pos time.Duration
}

func (p *positionPlane) Position() time.Duration { return p.pos }

func osdRequest() SessionRequest {
	return SessionRequest{
		StreamURL:    "http://example.test/movie.mp4",
		AdapterRef:   "url:movie",
		Source:       "url",
		DirectPlay:   true,
		Capabilities: Capabilities{CanSeek: true, CanPause: true},
	}
}

func TestManager_OSDIsPassedToEveryPlane(t *testing.T) {
	m, display, configs := newOSDTestManager(t, 0)
	if err := m.StartSession(osdRequest()); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := m.SeekTo(5000); err != nil {
		t.Fatalf("SeekTo: %v", err)
	}
	if len(*configs) != 2 {
		t.Fatalf("planes built = %d, want 2", len(*configs))
	}
	for i, cfg := range *configs {
		if cfg.OSD != display {
			t.Fatalf("plane %d OSD = %p, want shared display %p", i, cfg.OSD, display)
		}
	}
}

func TestManager_SetOutputVolumeShowsOSDVolume(t *testing.T) {
	m, display, _ := newOSDTestManager(t, 0)
	if err := m.SetOutputVolume(37); err != nil {
		t.Fatalf("SetOutputVolume: %v", err)
	}
	want := osd.Showing{VolumeVisible: true, Volume: 37}
	if got := display.Showing(osdTestNow); got != want {
		t.Fatalf("Showing = %+v, want %+v", got, want)
	}
}

func TestManager_RejectedVolumeShowsNothing(t *testing.T) {
	m, display, _ := newOSDTestManager(t, 0)
	_ = m.SetOutputVolume(101)
	if got := display.Showing(osdTestNow); got != (osd.Showing{}) {
		t.Fatalf("Showing after rejected volume = %+v, want zero", got)
	}
}

func TestManager_SetOutputMutedShowsMutingWithSavedVolume(t *testing.T) {
	m, display, _ := newOSDTestManager(t, 0)
	m.bridge.Audio.OutputVolume = 64
	if err := m.SetOutputMuted(true); err != nil {
		t.Fatalf("SetOutputMuted: %v", err)
	}
	want := osd.Showing{VolumeVisible: true, Volume: 64, Muted: true}
	if got := display.Showing(osdTestNow); got != want {
		t.Fatalf("Showing = %+v, want %+v", got, want)
	}
}

func TestManager_StartSessionShowsChannelAndPlay(t *testing.T) {
	cases := []struct {
		name        string
		label       string
		source      string
		wantChannel string
	}{
		{"explicit label", "CH 07", "streams", "CH 07"},
		{"falls back to source", "", "plex", "PLEX"},
		{"no label or source", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, display, _ := newOSDTestManager(t, 0)
			t.Cleanup(func() { _ = m.Stop() })
			req := osdRequest()
			req.ChannelLabel, req.Source = tc.label, tc.source
			if err := m.StartSession(req); err != nil {
				t.Fatalf("StartSession: %v", err)
			}
			want := osd.Showing{Channel: tc.wantChannel, Transport: osd.TransportPlay}
			if got := display.Showing(osdTestNow); got != want {
				t.Fatalf("Showing = %+v, want %+v", got, want)
			}
		})
	}
}

func TestManager_GuardedStartShowsChannelAndPlay(t *testing.T) {
	m, display, _ := newOSDTestManager(t, 0)
	t.Cleanup(func() { _ = m.Stop() })
	req := osdRequest()
	req.ChannelLabel = "CH 03"
	if started, err := m.StartSessionIfIdle(req); err != nil || !started {
		t.Fatalf("StartSessionIfIdle = %t, %v", started, err)
	}
	want := osd.Showing{Channel: "CH 03", Transport: osd.TransportPlay}
	if got := display.Showing(osdTestNow); got != want {
		t.Fatalf("Showing = %+v, want %+v", got, want)
	}
}

func TestManager_FailedStartShowsNothing(t *testing.T) {
	m, display, _ := newOSDTestManager(t, 0)
	probeInputFn = func(context.Context, string, ffmpeg.ProbeInputSpec) (*ffmpeg.ProbeResult, error) {
		return nil, context.DeadlineExceeded
	}
	if err := m.StartSession(osdRequest()); err == nil {
		t.Fatal("StartSession succeeded with failing probe")
	}
	if got := display.Showing(osdTestNow); got != (osd.Showing{}) {
		t.Fatalf("Showing after failed start = %+v, want zero", got)
	}
}

func TestManager_ResumeShowsPlayWithoutChannel(t *testing.T) {
	m, display, _ := newOSDTestManager(t, 0)
	t.Cleanup(func() { _ = m.Stop() })
	if err := m.StartSession(osdRequest()); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := m.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	later := osdTestNow.Add(time.Minute) // start banner long gone
	m.now = func() time.Time { return later }
	if err := m.Play(); err != nil {
		t.Fatalf("Play: %v", err)
	}
	want := osd.Showing{Transport: osd.TransportPlay}
	if got := display.Showing(later); got != want {
		t.Fatalf("Showing after resume = %+v, want %+v", got, want)
	}
}

func TestManager_SeekShowsDirection(t *testing.T) {
	cases := []struct {
		name     string
		pos      time.Duration
		targetMs int
		want     osd.Transport
	}{
		{"forward", 10 * time.Second, 60_000, osd.TransportFastForward},
		{"backward", 60 * time.Second, 10_000, osd.TransportRewind},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, display, _ := newOSDTestManager(t, tc.pos)
			t.Cleanup(func() { _ = m.Stop() })
			if err := m.StartSession(osdRequest()); err != nil {
				t.Fatalf("StartSession: %v", err)
			}
			later := osdTestNow.Add(time.Minute)
			m.now = func() time.Time { return later }
			if err := m.SeekTo(tc.targetMs); err != nil {
				t.Fatalf("SeekTo: %v", err)
			}
			if got := display.Showing(later).Transport; got != tc.want {
				t.Fatalf("Transport = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestManager_WithoutOSDIsNilSafe(t *testing.T) {
	m := newTestManager(t)
	if err := m.SetOutputVolume(20); err != nil {
		t.Fatalf("SetOutputVolume without OSD: %v", err)
	}
	if err := m.SetOutputMuted(true); err != nil {
		t.Fatalf("SetOutputMuted without OSD: %v", err)
	}
}
