package url

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
)

// withStatus returns a fakeCore whose Status returns the given value.
func withStatus(s core.SessionStatus) *fakeCore {
	fc := &fakeCore{}
	fc.statusFn = func() core.SessionStatus { return s }
	return fc
}

func TestCompanionPause_URLSessionCallsPause(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StatePlaying, AdapterRef: "url:abc"})
	a := newTestAdapter(t, fc)
	if err := a.CompanionPause(context.Background()); err != nil {
		t.Fatalf("CompanionPause error = %v", err)
	}
	if !fc.pauseCalled {
		t.Fatal("Pause was not called")
	}
}

func TestCompanionPause_ForeignSessionReturns409(t *testing.T) {
	fc := withStatus(core.SessionStatus{State: core.StatePlaying, AdapterRef: "plex:abc"})
	a := newTestAdapter(t, fc)
	err := a.CompanionPause(context.Background())
	var ce interface{ HTTPStatus() int }
	if !errors.As(err, &ce) || ce.HTTPStatus() != http.StatusConflict {
		t.Fatalf("error = %v, want companion 409", err)
	}
	if fc.pauseCalled {
		t.Fatal("Pause called for foreign session")
	}
}

func TestCompanionSeekAbsoluteClampsAndCallsSeekTo(t *testing.T) {
	fc := withStatus(core.SessionStatus{
		State:      core.StatePlaying,
		AdapterRef: "url:abc",
		Duration:   time.Minute,
	})
	a := newTestAdapter(t, fc)
	if err := a.CompanionSeek(context.Background(), 90_000); err != nil {
		t.Fatalf("CompanionSeek error = %v", err)
	}
	if fc.seekOffsetMs != 60_000 {
		t.Fatalf("seek offset = %d, want duration clamp 60000", fc.seekOffsetMs)
	}
}

func TestCompanionHistoryPlayUsesID(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	a.history.AddOrBump("https://example.com/a")
	id := a.history.List()[0].ID
	res, err := a.CompanionHistoryPlay(context.Background(), id)
	if err != nil {
		t.Fatalf("CompanionHistoryPlay error = %v", err)
	}
	if res.AdapterRef == "" || res.ResolvedVia != "direct" {
		t.Fatalf("result = %+v", res)
	}
	if fc.lastReq.StreamURL != "https://example.com/a" {
		t.Fatalf("StreamURL = %q", fc.lastReq.StreamURL)
	}
}

func TestCompanionHistoryDeleteUnknownIDReturns404(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	err := a.CompanionHistoryDelete(context.Background(), "h_00000000000000000000000000000000")
	var ce interface{ HTTPStatus() int }
	if !errors.As(err, &ce) || ce.HTTPStatus() != http.StatusNotFound {
		t.Fatalf("error = %v, want companion 404", err)
	}
}

// History replay/delete used to be reachable through the legacy
// idx-based POST /history/{play,delete} form routes. The surviving entry
// points are the ID-keyed CompanionHistoryPlay / CompanionHistoryDelete
// (browser-extension API); these tests pin the same behaviors there.

func TestCompanionHistoryPlay_BumpsReplayedEntry(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	a.history.AddOrBump("https://a.example/1")
	a.history.AddOrBump("https://b.example/2")
	id := a.history.List()[1].ID // a.example, the older entry
	if _, err := a.CompanionHistoryPlay(context.Background(), id); err != nil {
		t.Fatalf("CompanionHistoryPlay error = %v", err)
	}
	if fc.lastReq.StreamURL != "https://a.example/1" {
		t.Errorf("StreamURL = %q, want history[1]", fc.lastReq.StreamURL)
	}
	list := a.history.List()
	if list[0].URL != "https://a.example/1" {
		t.Errorf("after history-play, list[0] = %q, want a bumped", list[0].URL)
	}
}

func TestCompanionHistoryPlay_UsesStoredHLSBufferMode(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	enableURLHLSBufferForTest(a)
	a.history.AddOrBumpWithHLSMode("https://example.com/live.m3u8", "off")
	a.hlsBufferOpen = func(context.Context, hlsbuffer.SessionOptions) (*hlsbuffer.Session, error) {
		t.Fatal("hlsBufferOpen should not be called for history entry with hls_buffer=off")
		return nil, nil
	}

	id := a.history.List()[0].ID
	if _, err := a.CompanionHistoryPlay(context.Background(), id); err != nil {
		t.Fatalf("CompanionHistoryPlay error = %v", err)
	}
	if fc.lastReq.StreamURL != "https://example.com/live.m3u8" {
		t.Fatalf("StreamURL = %q, want direct history URL", fc.lastReq.StreamURL)
	}
}

func TestCompanionHistoryPlay_UnknownIDReturns404WithoutSideEffects(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	a.history.AddOrBump("https://a/")
	pre := a.history.List()
	_, err := a.CompanionHistoryPlay(context.Background(), "h_00000000000000000000000000000000")
	var ce interface{ HTTPStatus() int }
	if !errors.As(err, &ce) || ce.HTTPStatus() != http.StatusNotFound {
		t.Fatalf("error = %v, want companion 404", err)
	}
	if fc.lastReq.StreamURL != "" {
		t.Error("StartSession must not be called for an unknown history id")
	}
	post := a.history.List()
	if len(pre) != len(post) || (len(pre) > 0 && pre[0].URL != post[0].URL) {
		t.Errorf("history mutated by failed CompanionHistoryPlay; pre=%v post=%v", pre, post)
	}
}

func TestCompanionHistoryDelete_RemovesEntry(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	a.history.AddOrBump("https://a/")
	a.history.AddOrBump("https://b/")
	id := a.history.List()[0].ID // b, the newest entry
	if err := a.CompanionHistoryDelete(context.Background(), id); err != nil {
		t.Fatalf("CompanionHistoryDelete error = %v", err)
	}
	if a.history.Len() != 1 {
		t.Errorf("Len = %d, want 1", a.history.Len())
	}
	if list := a.history.List(); list[0].URL != "https://a/" {
		t.Errorf("after delete, list[0] = %q, want a", list[0].URL)
	}
}

func TestCompanionHistoryDelete_UnknownIDLeavesHistoryIntact(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	a.history.AddOrBump("https://a/")
	if err := a.CompanionHistoryDelete(context.Background(), "h_00000000000000000000000000000000"); err == nil {
		t.Fatal("CompanionHistoryDelete(unknown) error = nil, want 404")
	}
	if a.history.Len() != 1 {
		t.Errorf("Len = %d after no-op delete, want 1", a.history.Len())
	}
}
