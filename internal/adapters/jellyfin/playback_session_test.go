package jellyfin

import (
	"strings"
	"sync"
	"testing"
)

func TestBuildSessionRequest_PopulatesAllFields(t *testing.T) {
	a := New(nil, t.TempDir(), "device-1")

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

func TestMakeOnStop_RecordsErrorAndWakesReporter(t *testing.T) {
	a := New(nil, t.TempDir(), "device-1")

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
	a := New(nil, t.TempDir(), "device-1")
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
	a := New(nil, t.TempDir(), "device-1")
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
	a := New(nil, t.TempDir(), "device-1")
	a.currentRefKey = "old-ref"
	_ = a.beginSelfPreempt("new-ref")
	a.commitSelfPreempt()
	if got := a.snapshotCurrentRefKey(); got != "new-ref" {
		t.Errorf("after commit: currentRefKey = %q, want new-ref", got)
	}
}

// Make sure that core.SessionRequest.OnStop type signature lines up.
var _ func(string) = (func(string))(nil)
