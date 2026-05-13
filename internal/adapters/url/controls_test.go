package url

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// withStatus returns a fakeCore whose Status returns the given value.
func withStatus(s core.SessionStatus) *fakeCore {
	fc := &fakeCore{}
	fc.statusFn = func() core.SessionStatus { return s }
	return fc
}

func TestURLRoutesDoNotMountLegacyTransportControls(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	routes := a.UIRoutes()
	paths := map[string]bool{}
	for _, route := range routes {
		if route.Method == http.MethodPost {
			paths[route.Path] = true
		}
	}
	for _, forbidden := range []string{"pause", "resume", "stop", "replay", "seek"} {
		if paths[forbidden] {
			t.Fatalf("legacy URL transport route %q is still mounted", forbidden)
		}
	}
	for _, want := range []string{"play", "history/play", "history/delete", "cookies"} {
		if !paths[want] {
			t.Fatalf("URL route %q should remain mounted", want)
		}
	}
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

func TestHistoryPlay_ValidIdx_CallsStartSession(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	a.history.AddOrBump("https://a.example/1")
	a.history.AddOrBump("https://b.example/2")
	body := strings.NewReader("idx=1")
	req := httptest.NewRequest(http.MethodPost, "/history/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleHistoryPlay(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if fc.lastReq.StreamURL != "https://a.example/1" {
		t.Errorf("StreamURL = %q, want history[1]", fc.lastReq.StreamURL)
	}
	list := a.history.List()
	if list[0].URL != "https://a.example/1" {
		t.Errorf("after history-play, list[0] = %q, want a bumped", list[0].URL)
	}
}

func TestHistoryPlay_OutOfRange_400(t *testing.T) {
	fc := &fakeCore{}
	a := newTestAdapter(t, fc)
	a.history.AddOrBump("https://a/")
	pre := a.history.List()
	body := strings.NewReader("idx=99")
	req := httptest.NewRequest(http.MethodPost, "/history/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleHistoryPlay(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if fc.lastReq.StreamURL != "" {
		t.Error("StartSession must not be called for out-of-range idx")
	}
	post := a.history.List()
	if len(pre) != len(post) || (len(pre) > 0 && pre[0].URL != post[0].URL) {
		t.Errorf("history mutated by failed handleHistoryPlay; pre=%v post=%v", pre, post)
	}
}

func TestHistoryDelete_ValidIdx_RemovesEntry(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	a.history.AddOrBump("https://a/")
	a.history.AddOrBump("https://b/")
	body := strings.NewReader("idx=0")
	req := httptest.NewRequest(http.MethodPost, "/history/delete", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleHistoryDelete(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if a.history.Len() != 1 {
		t.Errorf("Len = %d, want 1", a.history.Len())
	}
	if list := a.history.List(); list[0].URL != "https://a/" {
		t.Errorf("after delete, list[0] = %q, want a", list[0].URL)
	}
}

func TestHistoryDelete_OutOfRange_400(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	a.history.AddOrBump("https://a/")
	body := strings.NewReader("idx=99")
	req := httptest.NewRequest(http.MethodPost, "/history/delete", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleHistoryDelete(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if a.history.Len() != 1 {
		t.Errorf("Len = %d after no-op delete, want 1", a.history.Len())
	}
}

func TestHistoryPlay_NonInteger_400(t *testing.T) {
	a := newTestAdapter(t, &fakeCore{})
	body := strings.NewReader("idx=abc")
	req := httptest.NewRequest(http.MethodPost, "/history/play", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleHistoryPlay(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
