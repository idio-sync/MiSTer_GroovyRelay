package chassis

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeStatus(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	var m map[string]bool
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("bad status JSON %q: %v", body, err)
	}
	return m
}

func TestHandleSetupStatus(t *testing.T) {
	// No controller wired → complete:true.
	s := &Server{cfg: Config{}}
	rec := httptest.NewRecorder()
	s.handleSetupStatus(rec, httptest.NewRequest(http.MethodGet, "/receiver/setup/status", nil))
	if got := decodeStatus(t, rec.Body.Bytes()); !got["complete"] {
		t.Fatalf("no controller: want complete:true, got %+v", got)
	}

	// First-run, host set, no source → hostSet:true sourceEnabled:false complete:false.
	saver := hostSaver("10.0.0.5", true)
	s2 := &Server{cfg: Config{BridgeSaver: saver}} // empty Registry → no source
	s2.firstRun = resolveFirstRun(saver)
	rec = httptest.NewRecorder()
	s2.handleSetupStatus(rec, httptest.NewRequest(http.MethodGet, "/receiver/setup/status", nil))
	got := decodeStatus(t, rec.Body.Bytes())
	if !got["hostSet"] || got["sourceEnabled"] || got["complete"] {
		t.Fatalf("host-only: got %+v", got)
	}
}

func TestHandleSetupFinish(t *testing.T) {
	// No controller → 200 no-op.
	s := &Server{cfg: Config{}}
	rec := httptest.NewRecorder()
	s.handleSetupFinish(rec, httptest.NewRequest(http.MethodPost, "/receiver/setup/finish", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("no controller: got %d want 200", rec.Code)
	}

	// First-run but incomplete (no host) → 409.
	saver := hostSaver("", true)
	s2 := &Server{cfg: Config{BridgeSaver: saver}}
	s2.firstRun = resolveFirstRun(saver)
	rec = httptest.NewRecorder()
	s2.handleSetupFinish(rec, httptest.NewRequest(http.MethodPost, "/receiver/setup/finish", nil))
	if rec.Code != http.StatusConflict || saver.dismissed != 0 {
		t.Fatalf("incomplete: got %d dismissed=%d", rec.Code, saver.dismissed)
	}

	// Dismiss failure → 500.
	bad := hostSaver("h", true)
	bad.dismissErr = errors.New("disk full")
	badSrv := &Server{cfg: Config{BridgeSaver: bad, Registry: enabledRegistry()}}
	badSrv.firstRun = resolveFirstRun(bad)
	rec = httptest.NewRecorder()
	badSrv.handleSetupFinish(rec, httptest.NewRequest(http.MethodPost, "/receiver/setup/finish", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("dismiss-fail: got %d want 500", rec.Code)
	}

	// Complete → 200 + DismissFirstRun called once.
	ok := hostSaver("h", true)
	okSrv := &Server{cfg: Config{BridgeSaver: ok, Registry: enabledRegistry()}}
	okSrv.firstRun = resolveFirstRun(ok)
	rec = httptest.NewRecorder()
	okSrv.handleSetupFinish(rec, httptest.NewRequest(http.MethodPost, "/receiver/setup/finish", nil))
	if rec.Code != http.StatusOK || ok.dismissed != 1 {
		t.Fatalf("complete: got %d dismissed=%d", rec.Code, ok.dismissed)
	}
}
