package chassis

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
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

func TestVizEnvelope_JSONCamelCase(t *testing.T) {
	t.Parallel()
	got, err := json.Marshal(vizEnvelope{Mode: config.VisualizerModeStereoScope})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"mode":"stereo_scope"}`
	if string(got) != want {
		t.Errorf("vizEnvelope JSON = %s, want %s", got, want)
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
		name   string
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

func sampleTransportData() TransportData {
	return TransportData{
		State:           "playing",
		SeekFillPercent: 42,
		ElapsedTime:     "01:23",
		TotalTime:       "03:21",
		PercentPlayed:   "42%",
		OffsetMS:        83000,
		DurationMS:      201000,
		ActionsEnabled: ActionsEnabled{
			Previous:    true,
			Next:        true,
			PauseResume: true,
			Stop:        true,
			Replay:      true,
			Seek:        true,
		},
		AdapterRef: "plex:track:abc",
		Generation: 7,
	}
}

func TestTransportEnvelopeFrom_FlattensTransportData(t *testing.T) {
	t.Parallel()
	src := sampleTransportData()
	got := transportEnvelopeFrom(src)

	if got.State != src.State {
		t.Errorf("State = %q, want %q", got.State, src.State)
	}
	if got.SeekFillPercent != src.SeekFillPercent {
		t.Errorf("SeekFillPercent = %d, want %d", got.SeekFillPercent, src.SeekFillPercent)
	}
	if got.ElapsedTime != src.ElapsedTime {
		t.Errorf("ElapsedTime = %q, want %q", got.ElapsedTime, src.ElapsedTime)
	}
	if got.TotalTime != src.TotalTime {
		t.Errorf("TotalTime = %q, want %q", got.TotalTime, src.TotalTime)
	}
	if got.PercentPlayed != src.PercentPlayed {
		t.Errorf("PercentPlayed = %q, want %q", got.PercentPlayed, src.PercentPlayed)
	}
	if got.OffsetMS != src.OffsetMS {
		t.Errorf("OffsetMS = %d, want %d", got.OffsetMS, src.OffsetMS)
	}
	if got.DurationMS != src.DurationMS {
		t.Errorf("DurationMS = %d, want %d", got.DurationMS, src.DurationMS)
	}
	if got.AdapterRef != src.AdapterRef {
		t.Errorf("AdapterRef = %q, want %q", got.AdapterRef, src.AdapterRef)
	}
	if got.Generation != src.Generation {
		t.Errorf("Generation = %d, want %d", got.Generation, src.Generation)
	}

	actionCases := []struct {
		name string
		src  ActionsEnabled
		want actionsEnabledE
	}{
		{"previous", ActionsEnabled{Previous: true}, actionsEnabledE{Previous: true}},
		{"next", ActionsEnabled{Next: true}, actionsEnabledE{Next: true}},
		{"pauseResume", ActionsEnabled{PauseResume: true}, actionsEnabledE{PauseResume: true}},
		{"stop", ActionsEnabled{Stop: true}, actionsEnabledE{Stop: true}},
		{"replay", ActionsEnabled{Replay: true}, actionsEnabledE{Replay: true}},
		{"seek", ActionsEnabled{Seek: true}, actionsEnabledE{Seek: true}},
	}
	for _, tt := range actionCases {
		t.Run(tt.name, func(t *testing.T) {
			input := TransportData{ActionsEnabled: tt.src}
			got := transportEnvelopeFrom(input)
			if got.ActionsEnabled != tt.want {
				t.Errorf("ActionsEnabled = %+v, want %+v", got.ActionsEnabled, tt.want)
			}
		})
	}
}

func TestTransportEnvelope_JSONFormatCamelCase(t *testing.T) {
	t.Parallel()
	got, err := json.Marshal(transportEnvelope{
		State:           "playing",
		SeekFillPercent: 64,
		ElapsedTime:     "02:08",
		TotalTime:       "03:20",
		PercentPlayed:   "64%",
		OffsetMS:        128000,
		DurationMS:      200000,
		ActionsEnabled: actionsEnabledE{
			Previous:    true,
			Next:        true,
			PauseResume: true,
			Stop:        true,
			Replay:      true,
			Seek:        true,
		},
		AdapterRef: "jellyfin:item:123",
		Generation: 12,
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"state":"playing","seekFillPercent":64,"elapsedTime":"02:08","totalTime":"03:20","percentPlayed":"64%","offsetMs":128000,"durationMs":200000,"actionsEnabled":{"previous":true,"next":true,"pauseResume":true,"stop":true,"replay":true,"seek":true},"adapterRef":"jellyfin:item:123","generation":12}`
	if string(got) != want {
		t.Errorf("transportEnvelope JSON = %s, want %s", got, want)
	}
}

func TestTransportEnvelope_StateValueSpaceDistinctFromBodyClass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		transportState string
		bodyClassState string
	}{
		{"playing", "playing", "live"},
		{"paused", "paused", "live"},
		{"stopped", "stopped", "idle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := transportEnvelopeFrom(TransportData{State: tt.transportState})
			if got.State != tt.transportState {
				t.Errorf("State = %q, want transport state %q", got.State, tt.transportState)
			}
			if got.State == tt.bodyClassState {
				t.Errorf("State = %q, want transport value space distinct from body class state %q", got.State, tt.bodyClassState)
			}
		})
	}
}

func TestTransportChanged_DetectsEveryFieldDelta(t *testing.T) {
	t.Parallel()
	base := sampleTransportData()
	tests := []struct {
		name   string
		mutate func(*TransportData)
	}{
		{"state", func(v *TransportData) { v.State = "paused" }},
		{"seekFillPercent", func(v *TransportData) { v.SeekFillPercent = 43 }},
		{"elapsedTime", func(v *TransportData) { v.ElapsedTime = "01:24" }},
		{"totalTime", func(v *TransportData) { v.TotalTime = "03:22" }},
		{"percentPlayed", func(v *TransportData) { v.PercentPlayed = "43%" }},
		{"offsetMS", func(v *TransportData) { v.OffsetMS = 84000 }},
		{"durationMS", func(v *TransportData) { v.DurationMS = 202000 }},
		{"adapterRef", func(v *TransportData) { v.AdapterRef = "plex:track:def" }},
		{"generation", func(v *TransportData) { v.Generation = 8 }},
		{"actionsEnabled.previous", func(v *TransportData) { v.ActionsEnabled.Previous = !v.ActionsEnabled.Previous }},
		{"actionsEnabled.next", func(v *TransportData) { v.ActionsEnabled.Next = !v.ActionsEnabled.Next }},
		{"actionsEnabled.pauseResume", func(v *TransportData) { v.ActionsEnabled.PauseResume = !v.ActionsEnabled.PauseResume }},
		{"actionsEnabled.stop", func(v *TransportData) { v.ActionsEnabled.Stop = !v.ActionsEnabled.Stop }},
		{"actionsEnabled.replay", func(v *TransportData) { v.ActionsEnabled.Replay = !v.ActionsEnabled.Replay }},
		{"actionsEnabled.seek", func(v *TransportData) { v.ActionsEnabled.Seek = !v.ActionsEnabled.Seek }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := base
			tt.mutate(&next)
			if !transportChanged(base, next) {
				t.Errorf("transportChanged should return true when %s changes", tt.name)
			}
		})
	}
}

func TestTransportChanged_IdenticalReturnsFalse(t *testing.T) {
	t.Parallel()
	v := sampleTransportData()
	if transportChanged(v, v) {
		t.Errorf("transportChanged should be false for identical inputs")
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

func (n *nonFlushableWriter) Header() http.Header         { return n.headers }
func (n *nonFlushableWriter) Write(b []byte) (int, error) { return n.body.Write(b) }
func (n *nonFlushableWriter) WriteHeader(s int)           { n.status = s }

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
	if !strings.Contains(body, "event: transport\n") {
		t.Errorf("body missing initial transport event")
	}
	if !strings.Contains(body, `"state":"stopped"`) {
		t.Errorf("body missing stopped transport payload")
	}
}

func TestHandleEvents_EmitsInitialVisualizerEventOnConnect(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.VisualizerViewer = &fakeVisualizerViewer{mode: config.VisualizerModeStereoScope}
	s, err := New(cfg)
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
	stateIdx := strings.Index(body, "event: state\n")
	vfdIdx := strings.Index(body, "event: vfd\n")
	vizIdx := strings.Index(body, "event: visualizer\n")
	transportIdx := strings.Index(body, "event: transport\n")
	if vizIdx < 0 {
		t.Fatalf("body missing initial visualizer event:\n%s", body)
	}
	if transportIdx < 0 {
		t.Fatalf("body missing initial transport event:\n%s", body)
	}
	if !(stateIdx >= 0 && vfdIdx > stateIdx && vizIdx > vfdIdx && transportIdx > vizIdx) {
		t.Errorf("initial event order should be state, vfd, visualizer, transport; body:\n%s", body)
	}
	if !strings.Contains(body, `"mode":"stereo_scope"`) {
		t.Errorf("body missing visualizer mode payload:\n%s", body)
	}
}

func TestHandleEvents_EmitsTransportEventOnInitialConnect(t *testing.T) {
	t.Parallel()
	sv := &mutableSessionViewer{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Initial Track",
		Source:     "plex",
		AdapterRef: "plex:item:initial",
		Generation: 3,
		Position:   64 * time.Second,
		Duration:   100 * time.Second,
	}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
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
	if !strings.Contains(body, "event: transport\n") {
		t.Fatalf("body missing initial transport event:\n%s", body)
	}
	for _, want := range []string{
		`"state":"playing"`,
		`"seekFillPercent":64`,
		`"elapsedTime":"01:04"`,
		`"totalTime":"01:40"`,
		`"percentPlayed":"64%"`,
		`"offsetMs":64000`,
		`"durationMs":100000`,
		`"adapterRef":"plex:item:initial"`,
		`"generation":3`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("initial transport event missing %s; body:\n%s", want, body)
		}
	}
}

func TestHandleEvents_RefreshesInitialSnapshotBeforeEmitting(t *testing.T) {
	t.Parallel()
	sv := &mutableSessionViewer{view: core.StatusHomeView{State: core.StateIdle}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sv.set(core.StatusHomeView{
		State: core.StatePlaying, Title: "Fresh Title", Source: "plex",
	})

	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `"state":"live"`) {
		t.Errorf("initial SSE snapshot should refresh to live before emitting; body:\n%s", body)
	}
	if !strings.Contains(body, `"title":"Fresh Title"`) {
		t.Errorf("initial SSE VFD payload should include fresh title; body:\n%s", body)
	}
	if strings.Contains(body, `"title":"STANDBY"`) {
		t.Errorf("initial SSE VFD payload used stale cached idle snapshot; body:\n%s", body)
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
	t.Cleanup(func() { _ = s.Close() })

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
	t.Cleanup(func() { _ = s.Close() })

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

func TestHandleEvents_EmitsVisualizerEventOnModeChange(t *testing.T) {
	t.Parallel()
	viewer := &fakeVisualizerViewer{mode: config.VisualizerModeRetroAnalyzer}
	cfg := nonZeroConfig()
	cfg.VisualizerViewer = viewer
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Mount(http.NewServeMux())
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	go func() {
		time.Sleep(150 * time.Millisecond)
		viewer.set(config.VisualizerModeOscilloscopeWave)
		time.Sleep(350 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `"mode":"retro_analyzer"`) {
		t.Errorf("missing initial visualizer mode event:\n%s", body)
	}
	if !strings.Contains(body, `"mode":"oscilloscope_wave"`) {
		t.Errorf("missing visualizer mode-change event:\n%s", body)
	}
	if got := strings.Count(body, "event: visualizer\n"); got < 2 {
		t.Errorf("visualizer event count = %d, want at least 2; body:\n%s", got, body)
	}
}

func TestEventsEmitSourceWhenAUXStateChanges(t *testing.T) {
	t.Parallel()
	aux := &fakeAUXStarter{status: adapters.AUXStatus{
		Enabled:    true,
		Configured: true,
		InputID:    "aux",
	}}
	cfg := nonZeroConfig()
	cfg.AUX = aux
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Mount(http.NewServeMux())
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	go func() {
		time.Sleep(150 * time.Millisecond)
		aux.setStatus(adapters.AUXStatus{
			Enabled:    true,
			Configured: true,
			Active:     true,
			InputID:    "aux",
		})
		time.Sleep(350 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if got := strings.Count(body, "event: source\n"); got < 2 {
		t.Fatalf("source event count = %d, want initial plus changed event; body:\n%s", got, body)
	}
	records := strings.Split(body, "event: source\n")
	last := records[len(records)-1]
	line := strings.SplitN(last, "\n", 2)[0]
	payload := strings.TrimPrefix(line, "data: ")
	var env sourceEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		t.Fatalf("unmarshal source payload %q: %v\nbody:\n%s", payload, err, body)
	}
	activeLabels := map[string]bool{}
	for _, button := range env.Buttons {
		if button.Active {
			activeLabels[button.Label] = true
		}
	}
	if !activeLabels["AUX"] || len(activeLabels) != 1 {
		t.Fatalf("active source labels = %#v, want only AUX active; payload=%s", activeLabels, payload)
	}
}

func TestHandleEvents_EmitsTransportEventOnStateTransition(t *testing.T) {
	t.Parallel()
	sv := &mutableSessionViewer{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Transport Track",
		Source:     "plex",
		AdapterRef: "plex:item:transport",
		Generation: 1,
		Position:   10 * time.Second,
		Duration:   100 * time.Second,
	}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	go func() {
		time.Sleep(150 * time.Millisecond)
		sv.set(core.StatusHomeView{
			State:      core.StatePaused,
			Title:      "Transport Track",
			Source:     "plex",
			AdapterRef: "plex:item:transport",
			Generation: 2,
			Position:   20 * time.Second,
			Duration:   100 * time.Second,
		})
		s.cache.Set(snapshotFromSession(s.cfg, s.session, s.visualizerViewer, s.transportViewer, s.aux, time.Now()))
		time.Sleep(250 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if got := strings.Count(body, "event: transport\n"); got < 2 {
		t.Fatalf("transport event count = %d, want at least 2; body:\n%s", got, body)
	}
	for _, want := range []string{
		`"state":"playing"`,
		`"state":"paused"`,
		`"seekFillPercent":20`,
		`"elapsedTime":"00:20"`,
		`"generation":2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("transport transition event missing %s; body:\n%s", want, body)
		}
	}
}

func TestHandleEvents_VisualizerEventOmittedWhenModeUnchanged(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.VisualizerViewer = &fakeVisualizerViewer{mode: config.VisualizerModeRetroAnalyzer}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Mount(http.NewServeMux())
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(350 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if got := strings.Count(body, "event: visualizer\n"); got != 1 {
		t.Errorf("visualizer event count = %d, want 1 initial event only; body:\n%s", got, body)
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

// failingWriter implements http.ResponseWriter + http.Flusher but
// returns io.ErrClosedPipe from Write after the first N bytes,
// simulating a TCP RST while the handler is mid-stream.
type failingWriter struct {
	headers http.Header
	written int
	cutoff  int
	flushes int
}

func (f *failingWriter) Header() http.Header { return f.headers }
func (f *failingWriter) Write(b []byte) (int, error) {
	if f.written >= f.cutoff {
		return 0, io.ErrClosedPipe
	}
	f.written += len(b)
	return len(b), nil
}
func (f *failingWriter) WriteHeader(int) {}
func (f *failingWriter) Flush()          { f.flushes++ }

func TestHandleEvents_BailsOnMidWriteDisconnect(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &failingWriter{headers: http.Header{}, cutoff: 20} // fail after a few bytes
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		s.handleEvents(w, req)
		close(done)
	}()
	select {
	case <-done:
		// handler returned quickly on write error
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleEvents did not bail on mid-write disconnect within 200ms")
	}
}

func TestHandleEvents_MultipleConcurrentConnections(t *testing.T) {
	t.Parallel()
	sv := &mutableSessionViewer{view: core.StatusHomeView{State: core.StateIdle}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Mount(http.NewServeMux()) // starts the shared cache refresher after Task 13
	t.Cleanup(func() { _ = s.Close() })

	const n = 5
	bodies := make([]*flushRecorder, n)
	ctxs := make([]context.CancelFunc, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		ctx, cancel := context.WithCancel(context.Background())
		ctxs[i] = cancel
		bodies[i] = newFlushRecorder()
		req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handleEvents(bodies[i], req)
		}()
	}

	// Drive a state transition; every connected handler should observe it.
	time.Sleep(150 * time.Millisecond)
	sv.set(core.StatusHomeView{State: core.StatePlaying, Title: "X", Source: "plex"})
	time.Sleep(350 * time.Millisecond)
	for _, cancel := range ctxs {
		cancel()
	}
	wg.Wait()

	for i, w := range bodies {
		body := w.Body.String()
		if !strings.Contains(body, `"state":"live"`) {
			t.Errorf("connection %d missed the live transition:\n%s", i, body)
		}
	}
}

func TestMount_RegistersEventsRoute(t *testing.T) {
	t.Parallel()
	s, err := New(nonZeroConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /receiver/events status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
}

// countingViewer wraps a SessionViewer and counts StatusHomeView calls.
// Used by snapshot-cache tests to assert call cadence.
type countingViewer struct {
	mu    sync.Mutex
	calls int
	view  core.StatusHomeView
}

func (c *countingViewer) StatusHomeView() core.StatusHomeView {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.view
}

func (c *countingViewer) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestSnapshotCache_SeedsSynchronouslyBeforeFirstSSE(t *testing.T) {
	t.Parallel()
	// A New-only server (no Mount) should already have a valid cached
	// snapshot reflecting the live SessionViewer state — proving the
	// seed call in New happens synchronously and the first SSE
	// connection cannot emit a zero-value or stale "vfd" frame.
	cv := &countingViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Title: "Live Title", Source: "plex",
	}}
	cfg := nonZeroConfig()
	cfg.Session = cv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Do NOT call Mount; we're proving the seed is synchronous in New.
	if cv.Calls() < 1 {
		t.Fatalf("StatusHomeView not called during New; want >= 1 (synchronous seed)")
	}
	snap := s.cache.Get()
	if snap.State != StateLive {
		t.Errorf("cached snapshot State = %q, want %q (the live SessionViewer)", snap.State, StateLive)
	}
	if snap.VFD.Title != "Live Title" {
		t.Errorf("cached snapshot VFD.Title = %q, want %q", snap.VFD.Title, "Live Title")
	}
}

func TestServerClose_StopsSnapshotCacheRefresher(t *testing.T) {
	t.Parallel()
	cv := &countingViewer{view: core.StatusHomeView{State: core.StateIdle}}
	cfg := nonZeroConfig()
	cfg.Session = cv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	// Let the refresher tick a few times.
	time.Sleep(350 * time.Millisecond)
	preClose := cv.Calls()

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Subsequent ticks (if the goroutine hadn't actually stopped) would
	// increment the call counter; pin down by sleeping > 3 intervals.
	time.Sleep(400 * time.Millisecond)
	postClose := cv.Calls()

	// Allow at most one in-flight tick to land between Close and the
	// goroutine actually returning.
	if delta := postClose - preClose; delta > 1 {
		t.Errorf("StatusHomeView calls after Close: pre=%d post=%d (delta %d); refresher did not stop", preClose, postClose, delta)
	}

	// Idempotence: calling Close twice must not panic or block.
	if err := s.Close(); err != nil {
		t.Errorf("second Close returned error: %v (want nil; Close must be idempotent)", err)
	}
}

func TestSnapshotCache_SingleStatusHomeViewCallPerTickRegardlessOfTabs(t *testing.T) {
	t.Parallel()

	cv := &countingViewer{view: core.StatusHomeView{State: core.StateIdle}}
	cfg := nonZeroConfig()
	cfg.Session = cv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux) // starts the refresher goroutine
	t.Cleanup(func() { _ = s.Close() })

	// Open 5 SSE handlers; let them run for ~5 cache ticks; cancel.
	const tabs = 5
	const ticks = 5
	var wg sync.WaitGroup
	ctxs := make([]context.CancelFunc, tabs)
	for i := 0; i < tabs; i++ {
		i := i
		ctx, cancel := context.WithCancel(context.Background())
		ctxs[i] = cancel
		req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
		w := newFlushRecorder()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handleEvents(w, req)
			_ = i
		}()
	}
	time.Sleep(time.Duration(ticks) * chassisTickInterval)
	for _, c := range ctxs {
		c()
	}
	wg.Wait()

	got := cv.Calls()
	// One call from New's synchronous seed + ~one per tick. Allow
	// generous slack for scheduler jitter on slow CI; still vastly
	// less than the per-tab-polling worst case of tabs*ticks plus seed.
	maxAllowed := ticks*2 + 2
	if got > maxAllowed {
		t.Errorf("StatusHomeView called %d times across %d tabs over %d ticks; want <= %d (single shared refresher, not per-tab polling)",
			got, tabs, ticks, maxAllowed)
	}
	if got < 1 {
		t.Errorf("StatusHomeView never called; expected at least the New-time seed call")
	}
}
