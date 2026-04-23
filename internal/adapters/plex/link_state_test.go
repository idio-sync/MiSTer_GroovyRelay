package plex

import (
	"context"
	"testing"
	"time"
)

func TestPendingLink_InitialState(t *testing.T) {
	pl := newPendingLink("ABCD", 12345, time.Now().Add(15*time.Minute))
	if got := pl.Code(); got != "ABCD" {
		t.Errorf("Code = %q", got)
	}
	if pl.PinID() != 12345 {
		t.Errorf("PinID = %d", pl.PinID())
	}
	if pl.Done() {
		t.Error("new PendingLink should not be Done")
	}
}

func TestPendingLink_Expired(t *testing.T) {
	pl := newPendingLink("ABCD", 1, time.Now().Add(-1*time.Second))
	if !pl.Expired() {
		t.Error("past expiry: Expired() should be true")
	}
}

func TestPendingLink_CompleteSetsTokenAndDone(t *testing.T) {
	pl := newPendingLink("X", 1, time.Now().Add(time.Minute))
	pl.complete("the-token", "")
	if !pl.Done() {
		t.Error("Done() should be true after complete")
	}
	if pl.Token() != "the-token" {
		t.Errorf("Token = %q", pl.Token())
	}
}

func TestPendingLink_AbandonStopsPolling(t *testing.T) {
	pl := newPendingLink("X", 1, time.Now().Add(time.Minute))
	_ = context.Background() // keep context import live
	pl.abandon()
	select {
	case <-pl.ctx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Error("abandon() should cancel the polling context")
	}
}
