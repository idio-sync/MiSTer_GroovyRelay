package chassis

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func init() {
	chassisTickInterval = 100 * time.Millisecond
	chassisHeartbeatInterval = 50 * time.Millisecond
}

func TestEmit_FormatsValidSSERecord(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := emit(&buf, "state", stateEnvelope{State: "idle"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := buf.String()
	want := "event: state\ndata: {\"state\":\"idle\"}\n\n"
	if got != want {
		t.Errorf("emit produced:\n%q\nwant:\n%q", got, want)
	}
}

func TestEmit_VfdEnvelopeUsesCamelCaseFieldNames(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := emit(&buf, "vfd", vfdEnvelope{
		Title:        "STANDBY",
		Marquee:      "MISTER LINK OK",
		QueueCurrent: 0,
		QueueTotal:   0,
		Uptime:       "4H 12M",
	})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := buf.String()
	for _, want := range []string{`"title":"STANDBY"`, `"marquee":"MISTER LINK OK"`,
		`"queueCurrent":0`, `"queueTotal":0`, `"uptime":"4H 12M"`} {
		if !strings.Contains(got, want) {
			t.Errorf("emit output missing %q\nfull output:\n%s", want, got)
		}
	}
}

func TestVfdChanged_DetectsEveryFieldDelta(t *testing.T) {
	t.Parallel()
	base := VFDData{
		Title:        "STANDBY",
		Marquee:      "hint",
		QueueCurrent: 0,
		QueueTotal:   0,
		Uptime:       "0H 0M",
	}

	tests := []struct {
		name  string
		mutate func(*VFDData)
	}{
		{"title", func(v *VFDData) { v.Title = "Live Title" }},
		{"marquee", func(v *VFDData) { v.Marquee = "PLEX · 00:00 / 03:00" }},
		{"queueCurrent", func(v *VFDData) { v.QueueCurrent = 1 }},
		{"queueTotal", func(v *VFDData) { v.QueueTotal = 12 }},
		{"uptime", func(v *VFDData) { v.Uptime = "0H 1M" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := base
			tt.mutate(&next)
			if !vfdChanged(base, next) {
				t.Errorf("vfdChanged should return true when %s changes", tt.name)
			}
		})
	}
}

func TestVfdChanged_IgnoresSystemTimeAndState(t *testing.T) {
	t.Parallel()
	base := VFDData{Title: "X", State: "idle", SystemTime: "10:30"}
	next := base
	next.SystemTime = "10:31"
	next.State = "live" // duplicate of ReceiverPageData.State; handled separately
	if vfdChanged(base, next) {
		t.Errorf("vfdChanged should ignore SystemTime and VFDData.State changes")
	}
}

func TestVfdChanged_IdenticalReturnsFalse(t *testing.T) {
	t.Parallel()
	v := VFDData{Title: "X", Marquee: "Y", QueueCurrent: 3, QueueTotal: 12, Uptime: "1H 2M"}
	if vfdChanged(v, v) {
		t.Errorf("vfdChanged should be false for identical inputs")
	}
}

// flushRecorder is httptest.ResponseRecorder + a Flusher implementation
// so SSE handlers can call w.(http.Flusher).Flush() without panic.
// Tracks flushes for assertion.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
	mu      sync.Mutex
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (f *flushRecorder) Flush() {
	f.mu.Lock()
	f.flushes++
	f.mu.Unlock()
}

// nonFlushableWriter implements http.ResponseWriter but deliberately
// does NOT satisfy http.Flusher. httptest.ResponseRecorder satisfies
// Flusher in Go 1.20+, so we hand-roll a minimal writer to drive the
// 500-on-non-flushable branch of handleEvents.
type nonFlushableWriter struct {
	headers http.Header
	body    bytes.Buffer
	status  int
}

func (n *nonFlushableWriter) Header() http.Header        { return n.headers }
func (n *nonFlushableWriter) Write(b []byte) (int, error) { return n.body.Write(b) }
func (n *nonFlushableWriter) WriteHeader(s int)          { n.status = s }

func TestHandleEvents_RejectsNonFlushableResponseWriter(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w := &nonFlushableWriter{headers: http.Header{}}
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil)
	s.handleEvents(w, req)
	if w.status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.status)
	}
}

func TestHandleEvents_SetsCorrectHeaders(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	headers := w.Result().Header
	if got := headers.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := headers.Get("Cache-Control"); got != "no-cache, no-store, must-revalidate" {
		t.Errorf("Cache-Control = %q, want no-cache, no-store, must-revalidate", got)
	}
	if got := headers.Get("Connection"); got != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", got)
	}
	if got := headers.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
}

func TestHandleEvents_EmitsRetryDirectiveOnConnect(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.HasPrefix(body, "retry: 3000\n\n") {
		t.Errorf("body should start with retry: 3000 directive; got:\n%s",
			body[:min(120, len(body))])
	}
}

func TestHandleEvents_EmitsInitialSnapshotOnConnect(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "event: state\n") {
		t.Errorf("body missing initial state event:\n%s", body)
	}
	if !strings.Contains(body, `"state":"idle"`) {
		t.Errorf("body missing idle state payload")
	}
	if !strings.Contains(body, "event: vfd\n") {
		t.Errorf("body missing initial vfd event")
	}
	if !strings.Contains(body, `"title":"STANDBY"`) {
		t.Errorf("body missing STANDBY title payload")
	}
}

func TestHandleEvents_NilSessionStreamsIdleOnly(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.session != nil {
		t.Fatalf("expected nil session by default (nonZeroConfig does not set Session); got %v", s.session)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `"state":"idle"`) {
		t.Errorf("nil session should still emit initial idle snapshot; body:\n%s", body)
	}
}

func TestHandleEvents_TerminatesOnClientDisconnect(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		s.handleEvents(w, req)
		close(done)
	}()
	cancel()
	select {
	case <-done:
		// handler returned
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleEvents did not return within 200ms of context cancel")
	}
}

// mutableSessionViewer flips between idle and live snapshots on
// demand. Lets tests drive state transitions through handleEvents
// without spinning up a real bridge.
type mutableSessionViewer struct {
	mu   sync.Mutex
	view core.StatusHomeView
}

func (m *mutableSessionViewer) StatusHomeView() core.StatusHomeView {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.view
}

func (m *mutableSessionViewer) set(view core.StatusHomeView) {
	m.mu.Lock()
	m.view = view
	m.mu.Unlock()
}

func TestHandleEvents_EmitsStateEventOnTransition(t *testing.T) {
	t.Parallel()
	sv := &mutableSessionViewer{view: core.StatusHomeView{State: core.StateIdle}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Mount is called even though this test invokes handleEvents directly:
	// Task 13 makes Mount start the shared snapshot-cache refresher that
	// Task 14's handleEvents path reads. Task 13 also adds Close cleanup.
	s.Mount(http.NewServeMux())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	// Trigger a state transition shortly after the handler boots.
	go func() {
		time.Sleep(150 * time.Millisecond) // > one diff tick (100ms in tests)
		sv.set(core.StatusHomeView{
			State: core.StatePlaying, Title: "T", Source: "plex",
		})
		// Leave room for both the shared cache refresher and the per-handler
		// diff ticker to observe the mutation after Task 14.
		time.Sleep(350 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	// Initial snapshot: state:idle. Then after the mutation: state:live.
	if !strings.Contains(body, `"state":"idle"`) {
		t.Errorf("missing initial idle state in body:\n%s", body)
	}
	if !strings.Contains(body, `"state":"live"`) {
		t.Errorf("missing transition-to-live state event in body:\n%s", body)
	}
}

func TestHandleEvents_EmitsVfdEventOnTitleChange(t *testing.T) {
	t.Parallel()
	sv := &mutableSessionViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Title: "First Track", Source: "plex",
	}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Mount starts the shared snapshot-cache refresher once Task 13 lands;
	// before that it is harmless and keeps this test linearly valid.
	s.Mount(http.NewServeMux())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	go func() {
		time.Sleep(150 * time.Millisecond)
		sv.set(core.StatusHomeView{
			State: core.StatePlaying, Title: "Second Track", Source: "plex",
		})
		time.Sleep(350 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `"title":"First Track"`) {
		t.Errorf("missing initial title event:\n%s", body)
	}
	if !strings.Contains(body, `"title":"Second Track"`) {
		t.Errorf("missing title-change vfd event:\n%s", body)
	}
}

// chassisHeartbeatInterval is a package-level var for the same reason
// chassisTickInterval is — tests shorten it once during package init to
// keep the suite fast without runtime races.
//
// We assert heartbeat by leaving the handler running for 3x the
// (shortened) interval and counting `: heartbeat\n\n` occurrences.

func TestHandleEvents_EmitsHeartbeatComments(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(180 * time.Millisecond) // > 3x heartbeat
		cancel()
	}()
	s.handleEvents(w, req)

	count := strings.Count(w.Body.String(), ": heartbeat\n\n")
	if count < 2 {
		t.Errorf("expected at least 2 heartbeat comments after ~180ms with 50ms interval, got %d. body:\n%s",
			count, w.Body.String())
	}
}
