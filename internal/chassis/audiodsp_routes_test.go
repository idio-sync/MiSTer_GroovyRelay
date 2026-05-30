package chassis

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

type fakeDSPController struct {
	cur       config.AudioDSP
	preview   *config.AudioDSP
	previewN  int
	committed *config.AudioDSP
	setN      int
}

func (f *fakeDSPController) AudioDSP() config.AudioDSP { return f.cur }
func (f *fakeDSPController) PreviewAudioDSP(d config.AudioDSP) error {
	f.preview = &d
	f.previewN++
	return nil
}
func (f *fakeDSPController) SetAudioDSP(d config.AudioDSP) error {
	f.committed = &d
	f.setN++
	return nil
}

// dspStatusErr is a typed saver error exposing StatusCode(), mirroring the
// uiserver settingsError contract the route maps to an HTTP status.
type dspStatusErr struct {
	status int
	msg    string
}

func (e dspStatusErr) Error() string   { return e.msg }
func (e dspStatusErr) StatusCode() int { return e.status }

type fakeDSPSaver struct {
	saved   *config.AudioDSP
	saveErr error
	mem     map[int]config.AudioDSPMemory
	current config.AudioDSP
}

func (f *fakeDSPSaver) SaveAudioDSP(d config.AudioDSP) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = &d
	return nil
}
func (f *fakeDSPSaver) SaveAudioDSPMemory(slot int, name string, v config.AudioDSP) error {
	if f.mem == nil {
		f.mem = map[int]config.AudioDSPMemory{}
	}
	f.mem[slot] = config.AudioDSPMemory{Slot: slot, Name: name, Stored: true, Bass: v.Bass, EQ: append([]float64(nil), v.EQ...)}
	return nil
}
func (f *fakeDSPSaver) RecallAudioDSPMemory(slot int) (config.AudioDSPMemory, bool) {
	m, ok := f.mem[slot]
	return m, ok
}
func (f *fakeDSPSaver) CurrentAudioDSP() config.AudioDSP { return f.current }

func newDSPTestServer(t *testing.T, ctl *fakeDSPController, saver *fakeDSPSaver) *http.ServeMux {
	t.Helper()
	srv, err := New(Config{Version: "t", StartedAt: time.Unix(0, 0), AudioDSPController: ctl, AudioDSPSaver: saver})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	return mux
}

func TestHandleAudioDSP_PreviewMergesPatch(t *testing.T) {
	t.Parallel()
	ctl := &fakeDSPController{cur: config.DefaultAudioDSP()}
	saver := &fakeDSPSaver{}
	mux := newDSPTestServer(t, ctl, saver)
	body := `{"commit":false,"params":{"bass":4}}`
	req := httptest.NewRequest("POST", "/receiver/audio/dsp", strings.NewReader(body))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if ctl.previewN != 1 || ctl.preview == nil || ctl.preview.Bass != 4 {
		t.Errorf("preview not applied with merged bass: %+v (n=%d)", ctl.preview, ctl.previewN)
	}
	if saver.saved != nil {
		t.Error("preview must not persist")
	}
}

func TestHandleAudioDSP_CommitPersists(t *testing.T) {
	t.Parallel()
	ctl := &fakeDSPController{cur: config.DefaultAudioDSP()}
	saver := &fakeDSPSaver{}
	mux := newDSPTestServer(t, ctl, saver)
	body := `{"commit":true,"params":{"treble":3}}`
	req := httptest.NewRequest("POST", "/receiver/audio/dsp", strings.NewReader(body))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if saver.saved == nil || saver.saved.Treble != 3 {
		t.Errorf("commit did not persist merged treble: %+v", saver.saved)
	}
}

func TestHandleAudioDSP_RejectsOutOfRange(t *testing.T) {
	t.Parallel()
	ctl := &fakeDSPController{cur: config.DefaultAudioDSP()}
	mux := newDSPTestServer(t, ctl, &fakeDSPSaver{})
	body := `{"commit":true,"params":{"bass":99}}`
	req := httptest.NewRequest("POST", "/receiver/audio/dsp", strings.NewReader(body))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for out-of-range bass", rec.Code)
	}
}

// TestHandleAudioDSP_FailedCommitMapsStatusAndReconciles covers both the
// status-mapping path (a typed saver error → its StatusCode, not a blanket 500)
// and the failed-commit reconcile (runtime restored to persisted truth via the
// committed SetAudioDSP path so persisted is marked true again).
func TestHandleAudioDSP_FailedCommitMapsStatusAndReconciles(t *testing.T) {
	t.Parallel()
	persisted := config.DefaultAudioDSP()
	persisted.Treble = 2
	ctl := &fakeDSPController{cur: config.DefaultAudioDSP()}
	saver := &fakeDSPSaver{
		current: persisted,
		saveErr: dspStatusErr{status: http.StatusConflict, msg: "PORT IN USE"},
	}
	mux := newDSPTestServer(t, ctl, saver)
	body := `{"commit":true,"params":{"bass":4}}`
	req := httptest.NewRequest("POST", "/receiver/audio/dsp", strings.NewReader(body))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 from the typed saver error (not a blanket 500); body=%s", rec.Code, rec.Body)
	}
	if ctl.setN != 1 || ctl.committed == nil || ctl.committed.Treble != 2 {
		t.Errorf("failed commit did not reconcile runtime to persisted truth via SetAudioDSP: setN=%d committed=%+v", ctl.setN, ctl.committed)
	}
}

func TestHandleAudioDSPMemory_Store(t *testing.T) {
	t.Parallel()
	cur := config.DefaultAudioDSP()
	cur.EQ[5] = 6
	ctl := &fakeDSPController{cur: cur}
	saver := &fakeDSPSaver{}
	mux := newDSPTestServer(t, ctl, saver)
	req := httptest.NewRequest("POST", "/receiver/audio/dsp/memory", strings.NewReader(`{"op":"store","slot":2,"name":"Rock"}`))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	if m, ok := saver.mem[2]; !ok || m.EQ[5] != 6 || m.Name != "Rock" {
		t.Errorf("memory slot 2 not stored from current params: %+v", saver.mem[2])
	}
}

func TestHandleAudioDSPMemory_RecallCommits(t *testing.T) {
	t.Parallel()
	ctl := &fakeDSPController{cur: config.DefaultAudioDSP()}
	saver := &fakeDSPSaver{mem: map[int]config.AudioDSPMemory{
		1: {Slot: 1, Stored: true, Treble: 5, EQ: make([]float64, 10)},
	}}
	mux := newDSPTestServer(t, ctl, saver)
	req := httptest.NewRequest("POST", "/receiver/audio/dsp/memory", strings.NewReader(`{"op":"recall","slot":1}`))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	if saver.saved == nil || saver.saved.Treble != 5 {
		t.Errorf("recall did not commit the stored voicing: %+v", saver.saved)
	}
}

func TestHandleAudioDSPMemory_RecallEmptyIs404(t *testing.T) {
	t.Parallel()
	mux := newDSPTestServer(t, &fakeDSPController{cur: config.DefaultAudioDSP()}, &fakeDSPSaver{})
	req := httptest.NewRequest("POST", "/receiver/audio/dsp/memory", strings.NewReader(`{"op":"recall","slot":3}`))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for empty slot recall", rec.Code)
	}
}
