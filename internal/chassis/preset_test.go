package chassis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type fakePresetCaster struct {
	mu      sync.Mutex
	calls   []int
	respond func(slot int) error
}

func (f *fakePresetCaster) CastPreset(ctx context.Context, slot int) error {
	f.mu.Lock()
	f.calls = append(f.calls, slot)
	f.mu.Unlock()
	if f.respond != nil {
		return f.respond(slot)
	}
	return nil
}

func newServerWithPresetCasterForTest(t *testing.T, caster adapters.PresetCaster) *Server {
	t.Helper()
	cfg := nonZeroConfig()
	cfg.PresetCaster = caster
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func TestHandlePresetCast_Success(t *testing.T) {
	t.Parallel()
	caster := &fakePresetCaster{}
	srv := newServerWithPresetCasterForTest(t, caster)
	req := httptest.NewRequest(http.MethodPost, "/ui/preset/7/cast", strings.NewReader(""))
	req.SetPathValue("slot", "7")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handlePresetCast(rec, req)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(caster.calls) != 1 || caster.calls[0] != 7 {
		t.Errorf("calls = %v, want [7]", caster.calls)
	}
}

func TestHandlePresetCast_NilCasterReturns404(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetCasterForTest(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/ui/preset/1/cast", strings.NewReader(""))
	req.SetPathValue("slot", "1")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handlePresetCast(rec, req)
	if rec.Code != 404 {
		t.Errorf("Code = %d, want 404", rec.Code)
	}
}

func TestHandlePresetCast_OutOfRangeSlots(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetCasterForTest(t, &fakePresetCaster{})
	for _, slot := range []string{"0", "13", "-1", "abc"} {
		t.Run("slot="+slot, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/ui/preset/"+slot+"/cast", strings.NewReader(""))
			req.SetPathValue("slot", slot)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			rec := httptest.NewRecorder()
			srv.handlePresetCast(rec, req)
			if rec.Code != 400 {
				t.Errorf("Code = %d, want 400", rec.Code)
			}
			var body map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body["chip"] != "BAD SLOT" {
				t.Errorf("chip = %v, want BAD SLOT", body["chip"])
			}
		})
	}
}

func TestHandlePresetCast_QuickCastErrorPropagates(t *testing.T) {
	t.Parallel()
	caster := &fakePresetCaster{
		respond: func(slot int) error {
			return &adapters.QuickCastError{Status: 503, Chip: "NOT READY"}
		},
	}
	srv := newServerWithPresetCasterForTest(t, caster)
	req := httptest.NewRequest(http.MethodPost, "/ui/preset/3/cast", strings.NewReader(""))
	req.SetPathValue("slot", "3")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handlePresetCast(rec, req)
	if rec.Code != 503 {
		t.Errorf("Code = %d, want 503", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "NOT READY" {
		t.Errorf("chip = %v, want NOT READY", body["chip"])
	}
}

func TestHandlePresetCast_UntypedErrorCollapsesToCastFailed(t *testing.T) {
	t.Parallel()
	caster := &fakePresetCaster{respond: func(slot int) error { return errors.New("synthetic") }}
	srv := newServerWithPresetCasterForTest(t, caster)
	req := httptest.NewRequest(http.MethodPost, "/ui/preset/3/cast", strings.NewReader(""))
	req.SetPathValue("slot", "3")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handlePresetCast(rec, req)
	if rec.Code != 500 {
		t.Errorf("Code = %d, want 500", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "CAST FAILED" {
		t.Errorf("chip = %v, want CAST FAILED", body["chip"])
	}
}

func TestReceiverPresetCastRouteRejectsMissingFetchSite(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetCasterForTest(t, &fakePresetCaster{})
	mux := http.NewServeMux()
	srv.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/ui/preset/1/cast", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403", rec.Code)
	}
}

type fakePresetEditor struct {
	mu        sync.Mutex
	starCalls []struct {
		Provider, Channel string
		Starred           bool
	}
	moveCalls   [][2]int
	starRespond func(p, c string, starred bool) (adapters.PresetStarResult, error)
	moveRespond func(from, to int) error
}

func (f *fakePresetEditor) SetPresetStarred(ctx context.Context, p, c string, starred bool) (adapters.PresetStarResult, error) {
	f.mu.Lock()
	f.starCalls = append(f.starCalls, struct {
		Provider, Channel string
		Starred           bool
	}{p, c, starred})
	f.mu.Unlock()
	if f.starRespond != nil {
		return f.starRespond(p, c, starred)
	}
	if starred {
		return adapters.PresetStarResult{Starred: true, Slot: 1}, nil
	}
	return adapters.PresetStarResult{Starred: false, Cleared: []int{1}}, nil
}

func (f *fakePresetEditor) MovePreset(ctx context.Context, from, to int) error {
	f.mu.Lock()
	f.moveCalls = append(f.moveCalls, [2]int{from, to})
	f.mu.Unlock()
	if f.moveRespond != nil {
		return f.moveRespond(from, to)
	}
	return nil
}

func newServerWithPresetEditorForTest(t *testing.T, editor adapters.PresetEditor) *Server {
	t.Helper()
	cfg := nonZeroConfig()
	cfg.PresetEditor = editor
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func postStar(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ui/preset/star", strings.NewReader(body))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handlePresetStar(rec, req)
	return rec
}

func postMove(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ui/preset/move", strings.NewReader(body))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handlePresetMove(rec, req)
	return rec
}

func TestPresetStar_AddSuccess(t *testing.T) {
	t.Parallel()
	editor := &fakePresetEditor{}
	srv := newServerWithPresetEditorForTest(t, editor)
	rec := postStar(t, srv, "provider=mtv-rewind&channel=80s&starred=true")
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":true,"starred":true,"slot":1}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestPresetStar_RemoveSuccess(t *testing.T) {
	t.Parallel()
	editor := &fakePresetEditor{}
	srv := newServerWithPresetEditorForTest(t, editor)
	rec := postStar(t, srv, "provider=mtv-rewind&channel=80s&starred=false")
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":true,"starred":false,"cleared":[1]}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestPresetStar_StrictLexicalRejectsBoolean1(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, &fakePresetEditor{})
	for _, val := range []string{"1", "0", "t", "f", "TRUE", "False", "yes", ""} {
		t.Run("starred="+val, func(t *testing.T) {
			rec := postStar(t, srv, "provider=mtv-rewind&channel=80s&starred="+val)
			if rec.Code != 400 {
				t.Errorf("Code = %d, want 400 for starred=%q", rec.Code, val)
			}
			var got map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if got["chip"] != "BAD INPUT" {
				t.Errorf("chip = %v, want BAD INPUT", got["chip"])
			}
		})
	}
}

func TestPresetStar_MissingProviderOrChannelReturns400(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, &fakePresetEditor{})
	for _, body := range []string{
		"provider=&channel=80s&starred=true",
		"provider=mtv-rewind&channel=&starred=true",
		"channel=80s&starred=true",
		"provider=mtv-rewind&starred=true",
	} {
		t.Run(body, func(t *testing.T) {
			rec := postStar(t, srv, body)
			if rec.Code != 400 {
				t.Errorf("body=%q Code = %d, want 400", body, rec.Code)
			}
		})
	}
}

func TestPresetStar_NilEditorReturns404(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, nil)
	rec := postStar(t, srv, "provider=mtv-rewind&channel=80s&starred=true")
	if rec.Code != 404 {
		t.Errorf("Code = %d, want 404", rec.Code)
	}
}

func TestPresetStar_BankFullPropagates(t *testing.T) {
	t.Parallel()
	editor := &fakePresetEditor{starRespond: func(p, c string, starred bool) (adapters.PresetStarResult, error) {
		return adapters.PresetStarResult{}, &adapters.QuickCastError{Status: 409, Chip: "BANK FULL"}
	}}
	srv := newServerWithPresetEditorForTest(t, editor)
	rec := postStar(t, srv, "provider=mtv-rewind&channel=80s&starred=true")
	if rec.Code != 409 {
		t.Fatalf("Code = %d, want 409", rec.Code)
	}
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":false,"chip":"BANK FULL"}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestPresetMove_SwapSuccess(t *testing.T) {
	t.Parallel()
	editor := &fakePresetEditor{}
	srv := newServerWithPresetEditorForTest(t, editor)
	rec := postMove(t, srv, "from=7&to=3")
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(editor.moveCalls) != 1 || editor.moveCalls[0] != [2]int{7, 3} {
		t.Errorf("moveCalls = %v, want [[7 3]]", editor.moveCalls)
	}
}

func TestPresetMove_FromEqualsToNoOpButStillSuccess(t *testing.T) {
	t.Parallel()
	editor := &fakePresetEditor{}
	srv := newServerWithPresetEditorForTest(t, editor)
	rec := postMove(t, srv, "from=5&to=5")
	if rec.Code != 200 {
		t.Errorf("Code = %d, want 200", rec.Code)
	}
}

func TestPresetMove_OutOfRangeReturnsBadSlot(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, &fakePresetEditor{})
	for _, body := range []string{
		"from=0&to=1", "from=1&to=0", "from=13&to=1", "from=1&to=13",
		"from=abc&to=1", "from=1&to=abc", "from=&to=1", "from=1&to=",
	} {
		t.Run(body, func(t *testing.T) {
			rec := postMove(t, srv, body)
			if rec.Code != 400 {
				t.Errorf("body=%q Code = %d, want 400", body, rec.Code)
			}
			var got map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if got["chip"] != "BAD SLOT" {
				t.Errorf("chip = %v, want BAD SLOT", got["chip"])
			}
		})
	}
}

func TestPresetMove_NilEditorReturns404EvenForFromEqualsTo(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, nil)
	rec := postMove(t, srv, "from=1&to=1")
	if rec.Code != 404 {
		t.Errorf("Code = %d, want 404 (404 precedes from==to no-op)", rec.Code)
	}
}

func TestPresetStarRouteRequiresSameOrigin(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, &fakePresetEditor{})
	mux := http.NewServeMux()
	srv.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/ui/preset/star",
		strings.NewReader("provider=mtv-rewind&channel=80s&starred=true"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403", rec.Code)
	}
}

func TestPresetMoveRouteRequiresSameOrigin(t *testing.T) {
	t.Parallel()
	srv := newServerWithPresetEditorForTest(t, &fakePresetEditor{})
	mux := http.NewServeMux()
	srv.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/ui/preset/move",
		strings.NewReader("from=1&to=2"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403", rec.Code)
	}
}
