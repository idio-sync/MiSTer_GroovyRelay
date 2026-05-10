package dlna

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// getpositioninfo_test.go covers the P3.3 refinement of the
// GetPositionInfo handler. Spec §Query Actions line 407: Track=1 when a
// URI is loaded, Track=0 otherwise; TrackURI is the stored URI; RelTime
// comes from core.Status().Position only when the active session ref
// matches the current dlna: ref.

// gpiAdapter builds an Adapter wired with a fakeSessionManager that
// reports the supplied SessionStatus. Tests can poke loadedURI /
// loadedMeta / currentRef under mu before invoking the handler.
func gpiAdapter(t *testing.T, st core.SessionStatus) (*Adapter, *fakeSessionManager) {
	t.Helper()
	fake := &fakeSessionManager{
		statusFn: func() core.SessionStatus { return st },
	}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a, fake
}

// runGetPositionInfo dispatches a GetPositionInfo SOAP envelope through
// the supplied adapter and returns the response recorder.
func runGetPositionInfo(t *testing.T, a *Adapter) *httptest.ResponseRecorder {
	t.Helper()
	req, rr := avtSOAPRequest(t, "GetPositionInfo", "<InstanceID>0</InstanceID>")
	a.handleAVTransportSOAP(rr, req)
	return rr
}

// gpiContains asserts the body contains every wanted substring.
func gpiContains(t *testing.T, body string, wants []string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q\nbody:\n%s", w, body)
		}
	}
}

// TestGetPositionInfo_NoURILoaded asserts the no-URI shape: Track=0 and
// every duration / time field is zero. core.Status() is irrelevant (the
// handler should never claim a position when it doesn't even own a URI).
func TestGetPositionInfo_NoURILoaded(t *testing.T) {
	a, _ := gpiAdapter(t, core.SessionStatus{})
	rr := runGetPositionInfo(t, a)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	gpiContains(t, rr.Body.String(), []string{
		"<Track>0</Track>",
		"<TrackDuration>00:00:00</TrackDuration>",
		"<TrackURI></TrackURI>",
		"<RelTime>00:00:00</RelTime>",
		"<AbsTime>00:00:00</AbsTime>",
		"<RelCount>0</RelCount>",
		"<AbsCount>0</AbsCount>",
	})
}

// TestGetPositionInfo_URILoaded_NoPlayback covers the load-but-not-yet-
// playing case: SetAVTransportURI populated loadedURI + loadedMeta but
// no Play has fired, so currentRef is still "" and core.Status() is the
// idle zero-value. TrackDuration falls back to the metadata duration.
func TestGetPositionInfo_URILoaded_NoPlayback(t *testing.T) {
	a, _ := gpiAdapter(t, core.SessionStatus{})
	a.mu.Lock()
	a.loadedURI = "http://192.168.1.10/movie.mp4"
	a.loadedMetaRaw = "<DIDL-Lite/>"
	a.loadedMeta.Duration = 3600 * time.Second // 01:00:00
	a.mu.Unlock()

	rr := runGetPositionInfo(t, a)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	gpiContains(t, rr.Body.String(), []string{
		"<Track>1</Track>",
		"<TrackDuration>01:00:00</TrackDuration>",
		"<TrackURI>http://192.168.1.10/movie.mp4</TrackURI>",
		"<RelTime>00:00:00</RelTime>",
		"<AbsTime>00:00:00</AbsTime>",
	})
}

// TestGetPositionInfo_OwnPlayback covers the happy path: own session
// active, core.Status() reports Duration=2h Position=30m. Live duration
// wins over metadata duration so the controller sees the probe result.
func TestGetPositionInfo_OwnPlayback(t *testing.T) {
	const ourRef = "dlna:abc123"
	a, _ := gpiAdapter(t, core.SessionStatus{
		AdapterRef: ourRef,
		Duration:   2 * time.Hour,
		Position:   30 * time.Minute,
	})
	a.mu.Lock()
	a.loadedURI = "http://192.168.1.10/movie.mp4"
	a.loadedMetaRaw = "<DIDL-Lite/>"
	a.loadedMeta.Duration = 1 * time.Hour // metadata says 1h, core says 2h: core wins
	a.currentRef = ourRef
	a.mu.Unlock()

	rr := runGetPositionInfo(t, a)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	gpiContains(t, rr.Body.String(), []string{
		"<Track>1</Track>",
		"<TrackDuration>02:00:00</TrackDuration>",
		"<RelTime>00:30:00</RelTime>",
		"<AbsTime>00:30:00</AbsTime>",
	})
}

// TestGetPositionInfo_ForeignSessionActive covers the case where a
// non-DLNA adapter (e.g. plex) has an active core session. The DLNA
// adapter still has its loaded URI but it cannot claim the foreign
// session's position — RelTime/AbsTime stay zero. TrackDuration falls
// back to the metadata duration the controller round-tripped.
func TestGetPositionInfo_ForeignSessionActive(t *testing.T) {
	a, _ := gpiAdapter(t, core.SessionStatus{
		AdapterRef: "plex:foo",
		Duration:   2 * time.Hour,
		Position:   45 * time.Minute,
	})
	a.mu.Lock()
	a.loadedURI = "http://192.168.1.10/movie.mp4"
	a.loadedMetaRaw = "<DIDL-Lite/>"
	a.loadedMeta.Duration = 90 * time.Minute // 01:30:00
	a.currentRef = "dlna:our-ref"            // NOT matching plex:foo
	a.mu.Unlock()

	rr := runGetPositionInfo(t, a)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	gpiContains(t, rr.Body.String(), []string{
		"<Track>1</Track>",
		"<TrackDuration>01:30:00</TrackDuration>",
		"<RelTime>00:00:00</RelTime>",
		"<AbsTime>00:00:00</AbsTime>",
	})
}

// TestGetPositionInfo_OwnLiveStream covers the unknown-duration shape
// (Duration=0 from the probe, e.g. an HLS LIVE manifest). RelTime still
// reports the elapsed-time position so controllers tracking a wall clock
// stay in sync; TrackDuration is "00:00:00" because we have nothing
// authoritative to surface.
func TestGetPositionInfo_OwnLiveStream(t *testing.T) {
	const ourRef = "dlna:livestream"
	a, _ := gpiAdapter(t, core.SessionStatus{
		AdapterRef: ourRef,
		Duration:   0,
		Position:   5 * time.Minute,
	})
	a.mu.Lock()
	a.loadedURI = "http://example.com/live.m3u8"
	a.loadedMeta.Duration = 0 // metadata also unknown
	a.currentRef = ourRef
	a.mu.Unlock()

	rr := runGetPositionInfo(t, a)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	gpiContains(t, rr.Body.String(), []string{
		"<Track>1</Track>",
		"<TrackDuration>00:00:00</TrackDuration>",
		"<RelTime>00:05:00</RelTime>",
		"<AbsTime>00:05:00</AbsTime>",
	})
}
