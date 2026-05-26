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
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/7/cast", strings.NewReader(""))
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
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/1/cast", strings.NewReader(""))
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
			req := httptest.NewRequest(http.MethodPost, "/receiver/preset/"+slot+"/cast", strings.NewReader(""))
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
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/3/cast", strings.NewReader(""))
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
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/3/cast", strings.NewReader(""))
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
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/1/cast", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403", rec.Code)
	}
}
