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
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/calibration"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

type fakeCalibration struct {
	snap     calibration.Snapshot
	startErr error
	prevErr  error
	saveErr  error
	previews []config.PictureGeometry
	starts   int
	saves    int
	cancels  int
}

func (f *fakeCalibration) Snapshot() calibration.Snapshot { return f.snap }
func (f *fakeCalibration) Start() error {
	f.starts++
	if f.startErr == nil {
		f.snap.State = calibration.StateActive
	}
	return f.startErr
}
func (f *fakeCalibration) Preview(g config.PictureGeometry) error {
	if f.prevErr != nil {
		return f.prevErr
	}
	f.previews = append(f.previews, g)
	return nil
}
func (f *fakeCalibration) Save() error { f.saves++; return f.saveErr }
func (f *fakeCalibration) Cancel()     { f.cancels++ }

func newCalibrationTestServer(t *testing.T, ctl CalibrationController) *http.ServeMux {
	t.Helper()
	srv, err := New(Config{Version: "t", StartedAt: time.Unix(0, 0), Calibration: ctl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	return mux
}

func postCalibration(mux *http.ServeMux, path, body string, sameOrigin bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	if sameOrigin {
		req.Header.Set("Origin", "http://"+req.Host)
	} else {
		req.Header.Set("Sec-Fetch-Site", "cross-site")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestCalibrationStart(t *testing.T) {
	t.Parallel()
	ctl := &fakeCalibration{}
	mux := newCalibrationTestServer(t, ctl)
	rec := postCalibration(mux, "/ui/calibration/start", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var body struct {
		OK          bool                 `json:"ok"`
		Calibration calibration.Snapshot `json:"calibration"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.OK || body.Calibration.State != calibration.StateActive {
		t.Fatalf("body = %s (err %v)", rec.Body, err)
	}

	ctl.startErr = calibration.ErrBusy
	rec = postCalibration(mux, "/ui/calibration/start", "", true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"BUSY"`) {
		t.Fatalf("busy start = %d %s, want 409 BUSY", rec.Code, rec.Body)
	}
}

func TestCalibrationRoutesRejectCrossSite(t *testing.T) {
	t.Parallel()
	ctl := &fakeCalibration{}
	mux := newCalibrationTestServer(t, ctl)
	for _, path := range []string{"/ui/calibration/start", "/ui/calibration/preview", "/ui/calibration/save", "/ui/calibration/cancel"} {
		if rec := postCalibration(mux, path, `{}`, false); rec.Code != http.StatusForbidden {
			t.Errorf("%s cross-site = %d, want 403", path, rec.Code)
		}
	}
	if ctl.starts+ctl.saves+ctl.cancels+len(ctl.previews) != 0 {
		t.Fatalf("cross-site request reached the controller: %+v", ctl)
	}
}

func TestCalibrationPreview(t *testing.T) {
	t.Parallel()
	ctl := &fakeCalibration{}
	mux := newCalibrationTestServer(t, ctl)
	rec := postCalibration(mux, "/ui/calibration/preview", `{"hSize":92.5,"vSize":95,"hOffset":-3,"vOffset":2}`, true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	want := config.PictureGeometry{HSize: 92.5, VSize: 95, HOffset: -3, VOffset: 2}
	if len(ctl.previews) != 1 || ctl.previews[0] != want {
		t.Fatalf("previews = %+v, want %+v", ctl.previews, want)
	}

	rec = postCalibration(mux, "/ui/calibration/preview", `{"hSize":70,"vSize":95,"hOffset":99}`, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid preview = %d, want 400", rec.Code)
	}
	var body struct {
		Errors map[string]string `json:"errors"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	for _, key := range []string{"hSize", "hOffset", "vOffset"} {
		if body.Errors[key] == "" {
			t.Errorf("missing field error for %s in %s", key, rec.Body)
		}
	}
	if len(ctl.previews) != 1 {
		t.Fatal("invalid preview reached the controller")
	}

	ctl.prevErr = calibration.ErrNotActive
	rec = postCalibration(mux, "/ui/calibration/preview", `{"hSize":90,"vSize":90,"hOffset":0,"vOffset":0}`, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("preview while idle = %d, want 409", rec.Code)
	}
}

func TestCalibrationSaveAndCancel(t *testing.T) {
	t.Parallel()
	ctl := &fakeCalibration{}
	mux := newCalibrationTestServer(t, ctl)
	if rec := postCalibration(mux, "/ui/calibration/save", "", true); rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body)
	}
	ctl.saveErr = calibration.ErrNotActive
	if rec := postCalibration(mux, "/ui/calibration/save", "", true); rec.Code != http.StatusConflict {
		t.Fatalf("save while idle = %d, want 409", rec.Code)
	}
	ctl.saveErr = errors.New("disk full")
	if rec := postCalibration(mux, "/ui/calibration/save", "", true); rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "WRITE FAILED") {
		t.Fatalf("failed save = %d %s, want 500 WRITE FAILED", rec.Code, rec.Body)
	}
	if rec := postCalibration(mux, "/ui/calibration/cancel", "", true); rec.Code != http.StatusNoContent || ctl.cancels != 1 {
		t.Fatalf("cancel = %d (cancels %d)", rec.Code, ctl.cancels)
	}
}

func TestCalibrationRoutesUnwired(t *testing.T) {
	t.Parallel()
	mux := newCalibrationTestServer(t, nil)
	if rec := postCalibration(mux, "/ui/calibration/start", "", true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired start = %d, want 503", rec.Code)
	}
}

// syncCalibration is a Snapshot source safe to change while the SSE loop
// reads it.
type syncCalibration struct {
	fakeCalibration
	mu sync.Mutex
}

func (f *syncCalibration) Snapshot() calibration.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func (f *syncCalibration) set(s calibration.Snapshot) {
	f.mu.Lock()
	f.snap = s
	f.mu.Unlock()
}

// The calibration SSE event is sent on connect and again when the snapshot
// changes; an unwired controller sends none (covered by the burst-order test).
func TestCalibrationSSE(t *testing.T) {
	ctl := &syncCalibration{}
	ctl.set(calibration.Snapshot{State: calibration.StateIdle, Draft: config.PictureGeometry{HSize: 100, VSize: 100}})
	srv, err := New(Config{Version: "t", StartedAt: time.Unix(0, 0), Calibration: ctl})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/ui/events", nil).WithContext(ctx)
	w := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleEvents(w, req)
	}()
	waitFor := func(n int) string {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if body := w.BodyString(); strings.Count(body, "event: calibration\n") >= n {
				return body
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for calibration event #%d; body:\n%s", n, w.BodyString())
		return ""
	}
	body := waitFor(1)
	if !strings.Contains(body, `"state":"idle"`) {
		t.Fatalf("initial calibration event missing idle state:\n%s", body)
	}
	ctl.set(calibration.Snapshot{State: calibration.StateEnded, EndReason: calibration.EndCast, Draft: config.PictureGeometry{HSize: 92.5, VSize: 100}})
	body = waitFor(2)
	if !strings.Contains(body, `"endReason":"cast"`) || !strings.Contains(body, `"hSize":92.5`) {
		t.Fatalf("changed calibration event missing fields:\n%s", body)
	}
	cancel()
	<-done
}
