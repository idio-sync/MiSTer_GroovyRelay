package jellyfin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestProgressInfo_FieldsPopulated(t *testing.T) {
	r := &reporter{
		capturedRefKey: "itm-1:ps-7",
		itemID:         "itm-1",
		playSessionID:  "ps-7",
		mediaSourceID:  "src-1",
		startedAt:      time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
	}
	st := core.SessionStatus{
		State:    core.StatePlaying,
		Position: 90 * time.Second,
		Duration: 30 * time.Minute,
	}
	audIdx := 1
	subIdx := 2
	body := r.buildProgressInfo(st, audIdx, subIdx)

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(data, &got)

	if got["ItemId"] != "itm-1" {
		t.Errorf("ItemId = %v", got["ItemId"])
	}
	if got["MediaSourceId"] != "src-1" {
		t.Errorf("MediaSourceId = %v", got["MediaSourceId"])
	}
	if got["PlaySessionId"] != "ps-7" {
		t.Errorf("PlaySessionId = %v", got["PlaySessionId"])
	}
	if got["PositionTicks"].(float64) != 900_000_000 { // 90 seconds × 10M
		t.Errorf("PositionTicks = %v", got["PositionTicks"])
	}
	if got["IsPaused"] != false {
		t.Errorf("IsPaused = %v", got["IsPaused"])
	}
	if got["PlayMethod"] != "Transcode" {
		t.Errorf("PlayMethod = %v", got["PlayMethod"])
	}
	if got["AudioStreamIndex"].(float64) != 1 {
		t.Errorf("AudioStreamIndex = %v", got["AudioStreamIndex"])
	}
	if got["SubtitleStreamIndex"].(float64) != 2 {
		t.Errorf("SubtitleStreamIndex = %v", got["SubtitleStreamIndex"])
	}
}

func TestProgressInfo_PausedReportsTrue(t *testing.T) {
	r := &reporter{itemID: "i", playSessionID: "ps", mediaSourceID: "s", startedAt: time.Now()}
	st := core.SessionStatus{State: core.StatePaused}
	body := r.buildProgressInfo(st, 0, 0)
	if !body.IsPaused {
		t.Errorf("IsPaused = false, want true")
	}
}

func TestRingBuffer_DropsOldestOnOverflow(t *testing.T) {
	rb := newRingBuffer(2)
	rb.push(outboundEnvelope{MessageType: "1"})
	rb.push(outboundEnvelope{MessageType: "2"})
	rb.push(outboundEnvelope{MessageType: "3"}) // should drop "1"

	got := rb.drainAll()
	if len(got) != 2 {
		t.Fatalf("len(drained) = %d, want 2", len(got))
	}
	if got[0].MessageType != "2" || got[1].MessageType != "3" {
		t.Errorf("drained = %+v, want [2 3]", got)
	}
}

func TestRingBuffer_DrainAllEmptyAfter(t *testing.T) {
	rb := newRingBuffer(4)
	rb.push(outboundEnvelope{MessageType: "x"})
	_ = rb.drainAll()
	got := rb.drainAll()
	if len(got) != 0 {
		t.Errorf("second drain = %d, want 0", len(got))
	}
}
