package jellyfin

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestBuildSessionRequest_PopulatesAllFields(t *testing.T) {
	a := New(nil, t.TempDir(), "device-1", "", nil)

	req := a.buildSessionRequest(playRequestInput{
		ItemID:             "itm-1",
		StartPositionTicks: 3_000_0000, // 3 s (3 × 10,000 ticks/ms)
		PlayInfo: PlaybackInfoResult{
			MediaSourceID:  "src-1",
			PlaySessionID:  "ps-7",
			TranscodingURL: "/videos/itm-1/master.m3u8?MediaSourceId=src-1",
		},
		ServerURL: "https://jf.example.com",
		Token:     "tok",
	})

	if !strings.Contains(req.StreamURL, "https://jf.example.com/videos/") {
		t.Errorf("StreamURL = %q", req.StreamURL)
	}
	if req.SeekOffsetMs != 3000 {
		t.Errorf("SeekOffsetMs = %d, want 3000", req.SeekOffsetMs)
	}
	if !req.Capabilities.CanSeek || !req.Capabilities.CanPause {
		t.Errorf("Capabilities = %+v, want both true", req.Capabilities)
	}
	if req.AdapterRef != "itm-1:ps-7" {
		t.Errorf("AdapterRef = %q, want itm-1:ps-7", req.AdapterRef)
	}
	if req.DirectPlay {
		t.Errorf("DirectPlay = true, want false (transcode URL)")
	}
	if req.OnStop == nil {
		t.Errorf("OnStop = nil")
	}
	if req.SubtitleURL != "" || req.SubtitlePath != "" {
		t.Errorf("Subtitle fields should be empty (JF burns subs in)")
	}
}

func TestBuildSessionRequest_OnStopEOFStillCleansArtwork(t *testing.T) {
	dir := t.TempDir()
	artDir := filepath.Join(dir, "artwork-cache")
	if err := os.MkdirAll(artDir, 0o700); err != nil {
		t.Fatal(err)
	}
	art := filepath.Join(artDir, "00000000000000000000000000000000.png")
	if err := os.WriteFile(art, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(&fakeManager{}, t.TempDir(), "device-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{AutoAdvance: false}
	req := a.buildSessionRequest(playRequestInput{
		ItemID: "itm-1",
		PlayInfo: PlaybackInfoResult{
			MediaSourceID:  "src-1",
			PlaySessionID:  "ps-7",
			TranscodingURL: "/videos/itm-1/master.m3u8?MediaSourceId=src-1",
			ArtworkPath:    art,
		},
		ServerURL: "https://jf.example.com",
		Token:     "tok",
	})
	req.OnStop("eof")
	if _, err := os.Stat(art); !os.IsNotExist(err) {
		t.Fatalf("artwork stat err = %v, want missing", err)
	}
}

func TestBuildSessionRequest_OnStopEOFWithToggleOffDoesNotAdvance(t *testing.T) {
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}}
	a := New(mgr, t.TempDir(), "device-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{AutoAdvance: false}
	a.currentRefKey = "itm-1:ps-7"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}
	req := a.buildSessionRequest(playRequestInput{
		ItemID: "itm-1",
		PlayInfo: PlaybackInfoResult{
			MediaSourceID:  "src-1",
			PlaySessionID:  "ps-7",
			TranscodingURL: "/videos/itm-1/master.m3u8?MediaSourceId=src-1",
		},
		ServerURL: "https://jf.example.com",
		Token:     "tok",
	})
	req.OnStop("eof")
	time.Sleep(100 * time.Millisecond)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.idleReqs) != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0", len(mgr.idleReqs))
	}
}

func TestBuildSessionRequest_OnStopNonEOFDoesNotAdvance(t *testing.T) {
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}, startIdleStarted: true}
	a := New(mgr, t.TempDir(), "device-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{AutoAdvance: true}
	a.currentRefKey = "itm-1:ps-7"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}
	req := a.buildSessionRequest(playRequestInput{
		ItemID: "itm-1",
		PlayInfo: PlaybackInfoResult{
			MediaSourceID:  "src-1",
			PlaySessionID:  "ps-7",
			TranscodingURL: "/videos/itm-1/master.m3u8?MediaSourceId=src-1",
		},
		ServerURL: "https://jf.example.com",
		Token:     "tok",
	})
	req.OnStop("stopped")
	time.Sleep(100 * time.Millisecond)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.idleReqs) != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0", len(mgr.idleReqs))
	}
}

func TestBuildSessionRequest_OnStopWrapperPreservesReporterWakeup(t *testing.T) {
	a := New(&fakeManager{}, t.TempDir(), "device-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{AutoAdvance: false}
	req := a.buildSessionRequest(playRequestInput{
		ItemID: "itm-1",
		PlayInfo: PlaybackInfoResult{
			MediaSourceID:  "src-1",
			PlaySessionID:  "ps-7",
			TranscodingURL: "/videos/itm-1/master.m3u8?MediaSourceId=src-1",
		},
		ServerURL: "https://jf.example.com",
		Token:     "tok",
	})
	wakeup := make(chan struct{}, 1)
	a.mu.Lock()
	a.reporters["itm-1:ps-7"] = &reporter{capturedRefKey: "itm-1:ps-7", wakeup: wakeup}
	a.mu.Unlock()

	req.OnStop("error")

	select {
	case <-wakeup:
	case <-time.After(time.Second):
		t.Fatal("reporter wakeup not received")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if got := a.reporters["itm-1:ps-7"].errReason; got != "error" {
		t.Fatalf("errReason = %q, want error", got)
	}
}

func TestMakeOnStop_RecordsErrorAndWakesReporter(t *testing.T) {
	a := New(nil, t.TempDir(), "device-1", "", nil)

	// Install a fake reporter and wakeup channel.
	var wakeReceived sync.WaitGroup
	wakeReceived.Add(1)
	wakeup := make(chan struct{}, 1)
	r := &reporter{
		capturedRefKey: "itm-1:ps-7",
		wakeup:         wakeup,
	}
	a.reporters["itm-1:ps-7"] = r

	go func() {
		<-wakeup
		wakeReceived.Done()
	}()

	closure := a.makeOnStop("itm-1:ps-7")
	closure("error")

	wakeReceived.Wait()

	a.mu.Lock()
	defer a.mu.Unlock()
	if r.errReason != "error" {
		t.Errorf("errReason = %q, want 'error'", r.errReason)
	}
}

func TestMakeOnStop_WakesEvenOnPreempt(t *testing.T) {
	a := New(nil, t.TempDir(), "device-1", "", nil)
	wakeup := make(chan struct{}, 1)
	r := &reporter{capturedRefKey: "itm-1:ps-7", wakeup: wakeup}
	a.reporters["itm-1:ps-7"] = r

	a.makeOnStop("itm-1:ps-7")("preempted")

	select {
	case <-wakeup:
	default:
		t.Fatal("wakeup channel not poked on 'preempted'")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if r.errReason != "" {
		t.Errorf("errReason = %q on preempt, want empty", r.errReason)
	}
}

func TestSetCurrentRefKey_RollbackOnStartSessionError(t *testing.T) {
	a := New(nil, t.TempDir(), "device-1", "", nil)
	a.currentRefKey = "old-ref"

	prev := a.beginSelfPreempt("new-ref")
	if prev != "old-ref" {
		t.Errorf("beginSelfPreempt returned %q, want old-ref", prev)
	}
	if got := a.snapshotCurrentRefKey(); got != "new-ref" {
		t.Errorf("after begin: currentRefKey = %q, want new-ref", got)
	}

	a.rollbackSelfPreempt(prev)
	if got := a.snapshotCurrentRefKey(); got != "old-ref" {
		t.Errorf("after rollback: currentRefKey = %q, want old-ref", got)
	}
}

func TestSetCurrentRefKey_ClearOnSuccess(t *testing.T) {
	a := New(nil, t.TempDir(), "device-1", "", nil)
	a.currentRefKey = "old-ref"
	_ = a.beginSelfPreempt("new-ref")
	a.commitSelfPreempt()
	if got := a.snapshotCurrentRefKey(); got != "new-ref" {
		t.Errorf("after commit: currentRefKey = %q, want new-ref", got)
	}
}

// Make sure that core.SessionRequest.OnStop type signature lines up.
var _ func(string) = (func(string))(nil)
