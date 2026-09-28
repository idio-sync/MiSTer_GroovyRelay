//go:build integration

package integration

import (
	"context"
	"encoding/binary"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/liveaudio"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// feedSine writes a 440 Hz s16le stereo tone into the relay until ctx ends.
// The relay's bounded buffer paces it to the reader, like a helper.
func feedSine(ctx context.Context, relay *liveaudio.Relay) {
	const frames = 441 // 10 ms
	buf := make([]byte, frames*4)
	phase := 0.0
	for ctx.Err() == nil {
		for i := 0; i < frames; i++ {
			v := int16(8000 * math.Sin(phase))
			phase += 2 * math.Pi * 440 / liveaudio.SampleRate
			binary.LittleEndian.PutUint16(buf[i*4:], uint16(v))
			binary.LittleEndian.PutUint16(buf[i*4+2:], uint16(v))
		}
		_, _ = relay.Write(buf)
		time.Sleep(5 * time.Millisecond) // never faster than ~2x real time while discarding
	}
}

// TestLiveAudioSessionEndToEnd drives a liveaudio.Session against a real
// Manager, real ffmpeg, and the fake MiSTer: live text changes must not
// re-INIT the MiSTer, new artwork in a cover mode must re-INIT exactly once.
func TestLiveAudioSessionEndToEnd(t *testing.T) {
	ffmpegPath := ffmpegPathOrSkip(t)
	skipIfVisualizerFiltersMissing(t, ffmpegPath, ffmpeg.VisualizerModeCoverVU)
	h := newScenarioHarness(t)

	dataDir := t.TempDir()
	// Same sender as the harness, but a bridge with a data dir (artwork)
	// and a cover mode.
	bridge := config.BridgeConfig{
		DataDir: dataDir,
		MiSTer:  config.MisterConfig{Host: "127.0.0.1"},
		Video: config.VideoConfig{
			Modeline:            "NTSC_480i",
			InterlaceFieldOrder: "tff",
			AspectMode:          "letterbox",
			RGBMode:             "rgb888",
			Codec:               config.CodecAuto,
		},
		Audio:      config.AudioConfig{SampleRate: 48000, Channels: 2},
		Visualizer: config.VisualizerConfig{Mode: config.VisualizerModeCoverVU},
	}
	mgr := core.NewManager(bridge, h.Sender)
	t.Cleanup(func() { _ = mgr.Stop() })

	relay, err := liveaudio.NewRelay("/internal/liveaudio/test/pcm/")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	relay.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	httpPort, _ := strconv.Atoi(u.Port())

	feedCtx, stopFeed := context.WithCancel(context.Background())
	t.Cleanup(stopFeed)
	go feedSine(feedCtx, relay)

	sess := liveaudio.NewSession(liveaudio.SessionConfig{
		Source: "spotify", Label: "SPOTIFY", Core: mgr, Relay: relay,
		HTTPPort: httpPort, DataDir: dataDir,
	}, liveaudio.Options{AudioOutput: core.AudioOutputMonitor, PauseGrace: time.Minute})
	t.Cleanup(sess.Close)

	counts := func() (inits, blits int, audio int) {
		snap := h.Recorder.Snapshot()
		return snap.Counts[groovy.CmdInit], snap.Counts[groovy.CmdBlitFieldVSync], int(snap.AudioBytes)
	}
	waitFor := func(what string, timeout time.Duration, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for !cond() {
			if time.Now().After(deadline) {
				inits, blits, audio := counts()
				t.Fatalf("timed out waiting for %s (inits=%d blits=%d audio=%d, core=%+v, lastErr=%q)",
					what, inits, blits, audio, mgr.Status(), sess.LastError())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	blitsGrow := func(what string) {
		t.Helper()
		_, start, _ := counts()
		waitFor(what, 15*time.Second, func() bool { _, b, _ := counts(); return b >= start+60 })
	}

	sess.Handle(liveaudio.Event{Kind: liveaudio.EventPlay})
	waitFor("first INIT", 15*time.Second, func() bool { i, _, _ := counts(); return i >= 1 })
	blitsGrow("fields after start")
	waitFor("monitor audio", 10*time.Second, func() bool { _, _, a := counts(); return a > 0 })

	sess.Handle(liveaudio.Event{Kind: liveaudio.EventTrack, Track: liveaudio.TrackMeta{
		Title: "Blue Monday", Artist: "New Order", Album: "Power, Corruption & Lies",
	}})
	blitsGrow("fields after a text change")
	if inits, _, _ := counts(); inits != 1 {
		t.Fatalf("text-only track change re-INITed the MiSTer (inits=%d)", inits)
	}
	if got := mgr.StatusHomeView().Display.Primary; got != "Blue Monday" {
		t.Fatalf("VFD primary = %q after the track change", got)
	}

	art, err := os.ReadFile(ensureSamplePNG(t, "liveaudio-cover.png"))
	if err != nil {
		t.Fatal(err)
	}
	sess.Handle(liveaudio.Event{Kind: liveaudio.EventArtwork, Track: liveaudio.TrackMeta{ArtworkBytes: art}})
	waitFor("artwork re-INIT", 15*time.Second, func() bool { i, _, _ := counts(); return i >= 2 })
	blitsGrow("fields after the artwork restart")
	if inits, _, _ := counts(); inits != 2 {
		t.Fatalf("artwork change re-INITed %d times, want exactly once", inits-1)
	}
	if st := mgr.Status(); st.AdapterRef != "spotify:1" {
		t.Fatalf("artwork restart changed the session ref to %q", st.AdapterRef)
	}

	sess.Handle(liveaudio.Event{Kind: liveaudio.EventStop})
	waitFor("session end", 10*time.Second, func() bool { return mgr.Status().AdapterRef == "" })
}
