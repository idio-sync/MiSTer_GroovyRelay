package chassis

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type fakeAUXStarter struct {
	mu          sync.Mutex
	status      adapters.AUXStatus
	startCalls  []string
	startErr    error
	stopCalls   []string
	stopMatched bool
	stopErr     error
}

func (f *fakeAUXStarter) AUXStatus(context.Context) adapters.AUXStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeAUXStarter) setStatus(status adapters.AUXStatus) {
	f.mu.Lock()
	f.status = status
	f.mu.Unlock()
}

func (f *fakeAUXStarter) StartAUX(_ context.Context, inputID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls = append(f.startCalls, inputID)
	return "aux:" + inputID, f.startErr
}

func (f *fakeAUXStarter) StopAUX(_ context.Context, inputID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls = append(f.stopCalls, inputID)
	return f.stopMatched, f.stopErr
}

func TestNewStoresAUXStarter(t *testing.T) {
	fake := &fakeAUXStarter{}
	srv, err := New(Config{
		Version:   "test",
		StartedAt: time.Unix(1, 0),
		AUX:       fake,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if srv.aux == nil {
		t.Fatalf("Server.aux is nil, want configured AUX starter")
	}
}

func TestAUXStartRouteStartsConfiguredInput(t *testing.T) {
	fake := &fakeAUXStarter{}
	s := &Server{aux: fake}

	w := postAUX(t, s.handleAUXStartPost, "/receiver/aux/start", url.Values{
		"input_id": {" aux-line-in "},
	}.Encode())

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", w.Body.String())
	}
	if got, want := fake.startCalls, []string{"aux-line-in"}; !equalStrings(got, want) {
		t.Fatalf("StartAUX calls = %#v, want %#v", got, want)
	}
}

func TestAUXStartRouteRejectsWrongInputID(t *testing.T) {
	fake := &fakeAUXStarter{startErr: fmt.Errorf("%w: AUX input unavailable", adapters.ErrSourceUnavailable)}
	s := &Server{aux: fake}

	w := postAUX(t, s.handleAUXStartPost, "/receiver/aux/start", url.Values{
		"input_id": {"wrong"},
	}.Encode())

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusUnprocessableEntity, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", got, "application/json")
	}
	if !strings.Contains(w.Body.String(), "AUX input unavailable") {
		t.Fatalf("body = %q, want JSON containing AUX input unavailable", w.Body.String())
	}
	if got, want := fake.startCalls, []string{"wrong"}; !equalStrings(got, want) {
		t.Fatalf("StartAUX calls = %#v, want %#v", got, want)
	}
}

func TestAUXStartRouteUsesSameOriginProtection(t *testing.T) {
	fake := &fakeAUXStarter{}
	cfg := nonZeroConfig()
	cfg.AUX = fake
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	req := httptest.NewRequest(http.MethodPost, "/receiver/aux/start", strings.NewReader(url.Values{
		"input_id": {"aux"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	assertJSONError(t, w, http.StatusForbidden, "cross-site request blocked")
	if len(fake.startCalls) != 0 {
		t.Fatalf("StartAUX calls = %#v, want none", fake.startCalls)
	}
}

func TestAUXStopRouteNoopsWithoutAUX(t *testing.T) {
	s := &Server{}

	w := postAUX(t, s.handleAUXStopPost, "/receiver/aux/stop", url.Values{
		"input_id": {"aux"},
	}.Encode())

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", w.Body.String())
	}
}

func TestAUXStopRouteDoesNotRequireForeignStopMatch(t *testing.T) {
	fake := &fakeAUXStarter{stopMatched: false}
	s := &Server{aux: fake}

	w := postAUX(t, s.handleAUXStopPost, "/receiver/aux/stop", url.Values{
		"input_id": {" foreign "},
	}.Encode())

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", w.Body.String())
	}
	if got, want := fake.stopCalls, []string{"foreign"}; !equalStrings(got, want) {
		t.Fatalf("StopAUX calls = %#v, want %#v", got, want)
	}
}

func postAUX(t *testing.T, handler http.HandlerFunc, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
