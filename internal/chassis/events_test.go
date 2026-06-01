package chassis

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/companion"
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

func TestTransportEnvelopeIncludesCanonicalSource(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	snap := snapshotFromStatusView(cfg, core.StatusHomeView{
		State:      core.StatePlaying,
		Source:     "plex",
		AdapterRef: "/library/metadata/42",
		Generation: 7,
	}, nil, nil, nil, nil, time.Now())

	body, err := json.Marshal(transportEnvelopeFrom(snap.Transport))
	if err != nil {
		t.Fatalf("Marshal transport envelope: %v", err)
	}
	if !strings.Contains(string(body), `"source":"plex"`) {
		t.Fatalf("transport envelope missing canonical source: %s", body)
	}
}

func TestEmit_VfdEnvelopeUsesCamelCaseFieldNames(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := emit(&buf, "vfd", vfdEnvelope{
		Primary:      "STANDBY",
		Secondary:    "MISTER LINK OK",
		QueueCurrent: 0,
		QueueTotal:   0,
		Uptime:       "4H 12M",
	})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := buf.String()
	for _, want := range []string{`"primary":"STANDBY"`, `"secondary":"MISTER LINK OK"`,
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

func TestSourceEnvelopeFromSnapshotIncludesStableActionsForEveryButton(t *testing.T) {
	t.Parallel()
	data := idleSnapshot(nonZeroConfig(), time.Unix(1, 0))
	data.Source.Buttons[0].Configured = true
	data.Source.Buttons[0].Casting = true
	data.Source.Buttons[0].Issue = true
	env := sourceEnvelopeFromSnapshot(data)
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal source envelope: %v", err)
	}
	if !strings.Contains(string(body), `"configured":true`) ||
		!strings.Contains(string(body), `"casting":true`) ||
		!strings.Contains(string(body), `"issue":true`) {
		t.Fatalf("source envelope missing lamp state fields: %s", body)
	}

	// Lamp slots (STREAMS/PLEX/JELLYFIN/DLNA) render as indicator
	// lamps with empty Action; only AUX retains a non-empty Action
	// so it renders as a clickable hw-btn.
	wantActions := map[string]string{
		"STREAMS":  "",
		"PLEX":     "",
		"JELLYFIN": "",
		"DLNA":     "",
		"AUX":      "aux-start",
	}
	if len(env.Buttons) != len(wantActions) {
		t.Fatalf("source envelope button count = %d, want %d: %+v", len(env.Buttons), len(wantActions), env.Buttons)
	}
	for _, button := range env.Buttons {
		want, ok := wantActions[button.Label]
		if !ok {
			t.Errorf("source envelope unexpected button %q", button.Label)
			continue
		}
		if button.Action != want {
			t.Errorf("source envelope action for %q = %q, want %q", button.Label, button.Action, want)
		}
		delete(wantActions, button.Label)
	}
	for label := range wantActions {
		t.Errorf("source envelope missing button %q", label)
	}
}

func TestSourceChangedDetectsLampStateDelta(t *testing.T) {
	t.Parallel()
	prevData := idleSnapshot(nonZeroConfig(), time.Unix(1, 0))
	nextData := idleSnapshot(nonZeroConfig(), time.Unix(1, 0))
	nextData.Source.Buttons[0].Configured = true
	if !sourceChanged(sourceEnvelopeFromSnapshot(prevData), sourceEnvelopeFromSnapshot(nextData)) {
		t.Errorf("sourceChanged missed Configured lamp state change")
	}
	nextData = idleSnapshot(nonZeroConfig(), time.Unix(1, 0))
	nextData.Source.Buttons[0].Casting = true
	if !sourceChanged(sourceEnvelopeFromSnapshot(prevData), sourceEnvelopeFromSnapshot(nextData)) {
		t.Errorf("sourceChanged missed Casting lamp state change")
	}
	nextData = idleSnapshot(nonZeroConfig(), time.Unix(1, 0))
	nextData.Source.Buttons[0].Issue = true
	if !sourceChanged(sourceEnvelopeFromSnapshot(prevData), sourceEnvelopeFromSnapshot(nextData)) {
		t.Errorf("sourceChanged missed Issue lamp state change")
	}
}

func TestVfdChanged_DetectsEveryFieldDelta(t *testing.T) {
	t.Parallel()
	base := VFDData{
		Primary:      "STANDBY",
		Secondary:    "hint",
		QueueCurrent: 0,
		QueueTotal:   0,
		Uptime:       "0H 0M",
	}

	tests := []struct {
		name   string
		mutate func(*VFDData)
	}{
		{"primary", func(v *VFDData) { v.Primary = "Live Primary" }},
		{"secondary", func(v *VFDData) { v.Secondary = "Sec" }},
		{"tertiary", func(v *VFDData) { v.Tertiary = "Ter" }},
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
	base := VFDData{Primary: "X", State: "idle", SystemTime: "10:30"}
	next := base
	next.SystemTime = "10:31"
	next.State = "live" // duplicate of ReceiverPageData.State; handled separately
	if vfdChanged(base, next) {
		t.Errorf("vfdChanged should ignore SystemTime and VFDData.State changes")
	}
}

func TestVfdChanged_IdenticalReturnsFalse(t *testing.T) {
	t.Parallel()
	v := VFDData{Primary: "X", Secondary: "Y", QueueCurrent: 3, QueueTotal: 12, Uptime: "1H 2M"}
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
		Source:     "plex",
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
	if got.Source != src.Source {
		t.Errorf("Source = %q, want %q", got.Source, src.Source)
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
		{"source", func(v *TransportData) { v.Source = "jellyfin" }},
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

func (f *flushRecorder) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ResponseRecorder.Write(b)
}

func (f *flushRecorder) WriteString(s string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ResponseRecorder.WriteString(s)
}

func (f *flushRecorder) Flush() {
	f.mu.Lock()
	f.flushes++
	f.mu.Unlock()
}

// BodyString returns the accumulated SSE body safely under the mutex.
// Use this instead of f.Body.String() when another goroutine may be writing.
func (f *flushRecorder) BodyString() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Body.String()
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
	if !strings.Contains(body, `"primary":"STANDBY"`) {
		t.Errorf("body missing STANDBY primary payload")
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
	sourceIdx := strings.Index(body, "event: source\n")
	vizIdx := strings.Index(body, "event: visualizer\n")
	transportIdx := strings.Index(body, "event: transport\n")
	volumeIdx := strings.Index(body, "event: volume\n")
	if vizIdx < 0 {
		t.Fatalf("body missing initial visualizer event:\n%s", body)
	}
	if transportIdx < 0 {
		t.Fatalf("body missing initial transport event:\n%s", body)
	}
	if volumeIdx < 0 {
		t.Fatalf("body missing initial volume event:\n%s", body)
	}
	if !(stateIdx >= 0 && vfdIdx > stateIdx && sourceIdx > vfdIdx && vizIdx > sourceIdx && transportIdx > vizIdx && volumeIdx > transportIdx) {
		t.Errorf("initial event order should be state, vfd, source, visualizer, transport, volume; body:\n%s", body)
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

func TestHandleEvents_InitialBurstReadsFromCache(t *testing.T) {
	t.Parallel()
	sv := &mutableSessionViewer{view: core.StatusHomeView{
		State: core.StatePlaying, Title: "Seeded Title", Source: "plex",
	}}
	cfg := nonZeroConfig()
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Change session after New() — the cache still holds the seed snapshot.
	// The initial burst reads from cache, not from the live session.
	sv.set(core.StatusHomeView{
		State: core.StatePlaying, Title: "Post-Seed Title", Source: "plex",
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
	// Initial burst reflects the cache (seeded at New time from Seeded Title).
	if !strings.Contains(body, `"state":"live"`) {
		t.Errorf("initial SSE burst should reflect cached live state; body:\n%s", body)
	}
	if !strings.Contains(body, `"primary":"Seeded Title"`) {
		t.Errorf("initial SSE VFD payload should match cache seed; body:\n%s", body)
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

// mutableVolumeViewer is the volume counterpart to mutableSessionViewer.
// Tests use it to flip the live output volume mid-stream so the volume
// diff loop in handleEvents has something to react to.
type mutableVolumeViewer struct {
	mu     sync.Mutex
	volume int
}

func (m *mutableVolumeViewer) OutputVolume() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.volume
}

func (m *mutableVolumeViewer) set(volume int) {
	m.mu.Lock()
	m.volume = volume
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
	if !strings.Contains(body, `"primary":"First Track"`) {
		t.Errorf("missing initial primary event:\n%s", body)
	}
	if !strings.Contains(body, `"primary":"Second Track"`) {
		t.Errorf("missing primary-change vfd event:\n%s", body)
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

func TestHandleEvents_EmitsHistoryWhenRegistryHistoryChanges(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	history := &historyAdapterStub{}
	reg := adapters.NewRegistry()
	if err := reg.Register(history); err != nil {
		t.Fatal(err)
	}
	cfg := nonZeroConfig()
	cfg.Registry = reg
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
		history.setEntries([]companion.CompanionHistoryEntry{{
			Title:      "Big Buck Bunny",
			URLDisplay: "/library/metadata/42",
			LastPlayed: now,
		}})
		time.Sleep(350 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	body := w.Body.String()
	if got := strings.Count(body, "event: history\n"); got < 2 {
		t.Fatalf("history event count = %d, want initial plus changed event; body:\n%s", got, body)
	}
	if !strings.Contains(body, `"title":"Big Buck Bunny"`) {
		t.Fatalf("changed history event missing Plex/Jellyfin-compatible title; body:\n%s", body)
	}
	if !strings.Contains(body, `"source":"URL"`) {
		t.Fatalf("changed history event missing source label; body:\n%s", body)
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
		s.cache.Set(snapshotFromSession(s.cfg, s.session, s.visualizerViewer, s.volumeViewer, s.transportViewer, s.aux, time.Now()))
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

func TestHandleEvents_InitialSnapshotIncludesVolume(t *testing.T) {
	t.Parallel()
	vv := &mutableVolumeViewer{volume: 73}
	cfg := nonZeroConfig()
	cfg.VolumeViewer = vv
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

	if !strings.Contains(body, "event: volume\n") {
		t.Fatalf("SSE stream missing volume event:\n%s", body)
	}
	if !strings.Contains(body, `"outputVolume":73`) {
		t.Fatalf("SSE stream missing outputVolume 73:\n%s", body)
	}
}

func TestHandleEvents_EmitsVolumeWhenChanged(t *testing.T) {
	t.Parallel()
	vv := &mutableVolumeViewer{volume: 40}
	cfg := nonZeroConfig()
	cfg.VolumeViewer = vv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.Mount(http.NewServeMux())

	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(150 * time.Millisecond)
		vv.set(41)
		time.Sleep(350 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)
	body := w.Body.String()

	if got := strings.Count(body, "event: volume\n"); got < 2 {
		t.Fatalf("volume event count = %d, want initial plus changed event; body:\n%s", got, body)
	}
	if !strings.Contains(body, `"outputVolume":40`) || !strings.Contains(body, `"outputVolume":41`) {
		t.Fatalf("SSE stream missing initial/changed volume payloads:\n%s", body)
	}
}

func TestHandleEvents_DoesNotRepeatUnchangedVolume(t *testing.T) {
	t.Parallel()
	vv := &mutableVolumeViewer{volume: 40}
	cfg := nonZeroConfig()
	cfg.VolumeViewer = vv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.Mount(http.NewServeMux())

	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(400 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)

	if got := strings.Count(w.Body.String(), "event: volume\n"); got != 1 {
		t.Fatalf("volume event count = %d, want exactly 1 (initial only); body:\n%s", got, w.Body.String())
	}
}

func TestVolumeChanged_ComparesOnlyOutputVolume(t *testing.T) {
	if !volumeChanged(TransportData{OutputVolume: 1}, TransportData{OutputVolume: 2}) {
		t.Fatal("volumeChanged = false, want true for changed output volume")
	}
	if volumeChanged(TransportData{OutputVolume: 2, State: "playing"}, TransportData{OutputVolume: 2, State: "paused"}) {
		t.Fatal("volumeChanged = true, want false for transport-only changes")
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

func TestHandleEvents_InitialBurstIncludesMeterAfterTransportFromCache(t *testing.T) {
	cfg := nonZeroConfig()
	sv := &countingViewer{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Cached Meter",
		AdapterRef: "url:cached",
		Source:     "url",
		Generation: 2,
	}}
	cfg.Session = sv
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before := sv.Calls()
	body := readInitialSSE(t, s)
	after := sv.Calls()
	if after != before {
		t.Fatalf("initial SSE called StatusHomeView %d extra times; want cached burst only", after-before)
	}
	transportIdx := strings.Index(body, "event: transport\n")
	meterIdx := strings.Index(body, "event: meter\n")
	if transportIdx < 0 || meterIdx < 0 || meterIdx <= transportIdx {
		t.Fatalf("meter must be emitted after transport; body:\n%s", body)
	}
	if !strings.Contains(body, `"generation":2`) {
		t.Fatalf("meter payload missing generation: body:\n%s", body)
	}
}

func readInitialSSE(t *testing.T, s *Server) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	w := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleEvents(w, req)
	}()
	deadline := time.After(300 * time.Millisecond)
	for {
		if strings.Contains(w.BodyString(), "event: meter\n") {
			cancel()
			<-done
			return w.BodyString()
		}
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("timed out waiting for initial meter event; body:\n%s", w.BodyString())
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// readSSEUntilMeterACK connects to handleEvents and accumulates SSE body
// until a meter event with a non-"--" ackMS value appears, or the
// deadline expires. Used by tests that require the meter sampler's 500ms
// window to fire (ackMS is only computed after the first full interval).
func readSSEUntilMeterACK(t *testing.T, s *Server, timeout time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	w := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleEvents(w, req)
	}()
	// ackMS values look like "04.0", "12.3", etc. — never "--" once
	// the 500ms sampling window fires. Match any non-dash ackMS value.
	hasLiveACK := func(body string) bool {
		idx := 0
		for {
			pos := strings.Index(body[idx:], `"ackMS":"`)
			if pos < 0 {
				return false
			}
			// pos is relative to body[idx:]; convert to absolute index of the
			// first character after the needle so we can inspect body[pos].
			pos = idx + pos + len(`"ackMS":"`)
			if pos < len(body) && body[pos] != '-' {
				return true
			}
			// Advance past this occurrence so the next iteration searches further.
			idx = pos
		}
	}
	deadline := time.After(timeout)
	for {
		body := w.BodyString()
		if hasLiveACK(body) {
			cancel()
			<-done
			return body
		}
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("timed out waiting for meter event with non-'--' ackMS; body:\n%s", w.BodyString())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestMeterChangedGating(t *testing.T) {
	base := idleSnapshot(nonZeroConfig(), time.Unix(1, 0)).Meter
	base.State = "live"
	base.Generation = 10
	base.SampleSeq = 1
	base.MidRow.Standard = "ntsc"
	base.MidRow.FieldOrder = "tff"
	base.MidRow.InterlacedOutput = true
	base.Readout.Output = "INTERLACE 480i - BT.601"
	base.Readout.Aspect = "4:3 LETTERBOX"
	base.Readout.Pipe = "LZ4+D - TFF"
	base.Readout.Link = "MiSTer - 4ms"
	cases := []struct {
		name string
		edit func(*MeterData)
		want bool
	}{
		{"sample boundary", func(m *MeterData) { m.SampleSeq++ }, true},
		{"pause flip", func(m *MeterData) { m.Paused = true }, true},
		{"generation", func(m *MeterData) { m.Generation++ }, true},
		{"structural field", func(m *MeterData) { m.MidRow.FieldOrder = "bff" }, true},
		{"idle clear", func(m *MeterData) { m.State = "idle" }, true},
		{"ack text jitter suppressed", func(m *MeterData) { m.MidRow.MSAck = "05.0" }, false},
	}
	for _, tc := range cases {
		next := base
		tc.edit(&next)
		if got := meterChanged(next, base); got != tc.want {
			t.Errorf("%s: meterChanged = %v, want %v", tc.name, got, tc.want)
		}
	}
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
	if snap.VFD.Primary != "Live Title" {
		t.Errorf("cached snapshot VFD.Primary = %q, want %q", snap.VFD.Primary, "Live Title")
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

func TestReceiverEventsMeterPayloadIncludesLowRateFields(t *testing.T) {
	cfg := nonZeroConfig()
	sv := &mutableSessionViewer{view: core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Integration Meter",
		AdapterRef: "url:meter",
		Source:     "url",
		Generation: 3,
		Meter: core.MeterHomeView{
			Source:   core.SourceMeterView{Width: 720, Height: 480, VideoCodec: "h264", AudioCodec: "aac", AudioRate: 48000, AudioChannels: 2},
			Pipeline: core.PipelineMeterView{ModelineName: "NTSC_480i", Standard: "ntsc", FieldOrder: "tff", FieldRateHz: 59.94, HorizontalKHz: 15.7, InterlacedOutput: true, AudioSampleRate: 48000, AudioChannels: 2},
			Runtime:  core.RuntimeMeterView{Generation: 3, BlitsTotal: 100, WireBytes: 1000000, LastACKAge: 4 * time.Millisecond},
		},
	}}
	s, err := New(Config{Version: cfg.Version, StartedAt: cfg.StartedAt, Bridge: cfg.Bridge, Session: sv})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Mount starts the shared cache refresher. The meter sampler's ackMS
	// readout only populates after a 500ms sampling window fires, observed
	// through a chain of 250ms refresher + SSE ticks — nominally ~0.5-1.1s
	// even on a fast box. Wait generously (5s): under `go test -race` on CI
	// the goroutine-scheduling slowdown inflates that chain well past a tight
	// 1.5s deadline (the original flake). The deadline only bounds failure
	// latency; a healthy run returns as soon as the populated event lands.
	s.Mount(http.NewServeMux())
	t.Cleanup(func() { _ = s.Close() })
	body := readSSEUntilMeterACK(t, s, 5*time.Second)
	if !strings.Contains(body, "event: meter\n") {
		t.Fatalf("missing meter event:\n%s", body)
	}
	for _, want := range []string{`"audioIn":"AAC - STEREO"`, `"fieldRateHz":59.94`, `"interlacedOutput":true`, `"ackMS":"04.0"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("meter payload missing %s:\n%s", want, body)
		}
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

func TestHandleEvents_InitialBurstIncludesAudio(t *testing.T) {
	cfg := nonZeroConfig()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	s.handleEvents(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "event: audio\n") {
		t.Errorf("initial burst missing audio event:\n%s", body)
	}
}

func TestHandleEvents_LiveAudioEmitsAtCadence(t *testing.T) {
	cfg := nonZeroConfig()
	viewer := &countingAudioViewer{
		snap: &core.AudioScopeSnapshot{Generation: 1, SampleRate: 48000, Channels: 2},
	}
	cfg.AudioScopeViewer = viewer
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(1 * time.Second)
		cancel()
	}()
	s.handleEvents(w, req)
	total := strings.Count(w.Body.String(), "event: audio\n")
	tickCount := total - 1
	if tickCount < 29 || tickCount > 31 {
		t.Errorf("audio tick count over 1 s = %d (total=%d), want 30 ± 1", tickCount, total)
	}
}

func TestHandleEvents_PendingSuppressedAtCadence(t *testing.T) {
	cfg := nonZeroConfig()
	cfg.AudioScopeViewer = &fakeAudioViewer{snap: nil}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()
	s.handleEvents(w, req)
	count := strings.Count(w.Body.String(), "event: audio\n")
	if count != 1 {
		t.Errorf("idle audio event count = %d, want 1 (initial only)", count)
	}
}

func TestHandleEvents_GenerationFlipDirectLiveToLive(t *testing.T) {
	cfg := nonZeroConfig()
	viewer := &mutableAudioViewer{snap: &core.AudioScopeSnapshot{Generation: 1, SampleRate: 48000, Channels: 2}}
	cfg.AudioScopeViewer = viewer
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(50 * time.Millisecond)
		viewer.setSnap(&core.AudioScopeSnapshot{Generation: 2, SampleRate: 48000, Channels: 2})
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)
	body := w.Body.String()
	if !strings.Contains(body, `"generation":1`) {
		t.Errorf("missing gen=1 emission: %s", body)
	}
	if !strings.Contains(body, `"generation":2`) {
		t.Errorf("missing gen=2 emission: %s", body)
	}
}

func TestHandleEvents_GenerationFlipViaPending(t *testing.T) {
	cfg := nonZeroConfig()
	viewer := &mutableAudioViewer{snap: &core.AudioScopeSnapshot{Generation: 1, SampleRate: 48000, Channels: 2}}
	cfg.AudioScopeViewer = viewer
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() {
		time.Sleep(40 * time.Millisecond)
		viewer.setSnap(nil)
		time.Sleep(60 * time.Millisecond)
		viewer.setSnap(&core.AudioScopeSnapshot{Generation: 2, SampleRate: 48000, Channels: 2})
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()
	s.handleEvents(w, req)
	body := w.Body.String()
	gen1 := strings.Index(body, `"generation":1`)
	pending := strings.Index(body, `"status":"pending"`)
	gen2 := strings.Index(body, `"generation":2`)
	if gen1 < 0 || pending < 0 || gen2 < 0 {
		t.Fatalf("missing one of gen1/pending/gen2: %s", body)
	}
	if !(gen1 < pending && pending < gen2) {
		t.Errorf("ordering wrong: gen1=%d pending=%d gen2=%d\n%s", gen1, pending, gen2, body)
	}
	if strings.Index(body[pending:], `"generation":1`) >= 0 {
		t.Errorf("stale gen=1 emission after pending: %s", body)
	}
}

func TestHandleEvents_PendingShapeExact(t *testing.T) {
	cfg := nonZeroConfig()
	cfg.AudioScopeViewer = &fakeAudioViewer{snap: nil}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	s.handleEvents(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "event: audio\ndata: {\"status\":\"pending\"}\n\n") {
		t.Errorf("expected exact pending wire bytes, got:\n%s", body)
	}
}

func TestHandleEvents_LiveLegitimateZerosOnWire(t *testing.T) {
	cfg := nonZeroConfig()
	cfg.AudioScopeViewer = &fakeAudioViewer{snap: &core.AudioScopeSnapshot{
		Generation: 1, SampleRate: 48000, Channels: 2,
		PhaseCorr: 0.0, LUFSShort: 0.0,
	}}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	s.handleEvents(w, req)
	body := w.Body.String()
	if !strings.Contains(body, `"phaseCorr":0,`) {
		t.Errorf("phaseCorr=0 was erased on wire: %s", body)
	}
	if !strings.Contains(body, `"lufsShort":0,`) {
		t.Errorf("lufsShort=0 was erased on wire: %s", body)
	}
}

func TestHandleEvents_InitialBurstAudioIsLast(t *testing.T) {
	cfg := nonZeroConfig()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() { time.Sleep(40 * time.Millisecond); cancel() }()
	s.handleEvents(w, req)
	body := w.Body.String()
	audioIdx := strings.Index(body, "event: audio\n")
	if audioIdx < 0 {
		t.Fatalf("no audio event in initial burst:\n%s", body)
	}
	for _, prior := range []string{"event: state\n", "event: vfd\n", "event: source\n", "event: visualizer\n", "event: transport\n", "event: volume\n", "event: meter\n"} {
		idx := strings.Index(body, prior)
		if idx < 0 {
			continue
		}
		if idx > audioIdx {
			t.Errorf("audio appeared before %s: audioIdx=%d priorIdx=%d", prior, audioIdx, idx)
		}
	}
}

func TestHandleEvents_PanicInViewerSkipsFrame(t *testing.T) {
	cfg := nonZeroConfig()
	viewer := &panickingAudioViewer{
		snap:       &core.AudioScopeSnapshot{Generation: 1, SampleRate: 48000, Channels: 2},
		panicOnNth: 2,
	}
	cfg.AudioScopeViewer = viewer
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	w := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/receiver/events", nil).WithContext(ctx)
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()
	s.handleEvents(w, req)
	body := w.Body.String()
	audioCount := strings.Count(body, "event: audio\n")
	if audioCount < 2 {
		t.Errorf("audio event count = %d, want >= 2 (initial + post-panic recovery); body:\n%s", audioCount, body)
	}
}

type panickingAudioViewer struct {
	mu         sync.Mutex
	snap       *core.AudioScopeSnapshot
	calls      int
	panicOnNth int
}

func (p *panickingAudioViewer) AudioScopes() *core.AudioScopeSnapshot {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	if n == p.panicOnNth {
		panic("synthetic panic for test")
	}
	return p.snap
}

type countingAudioViewer struct {
	mu   sync.Mutex
	snap *core.AudioScopeSnapshot
	n    int
}

func (c *countingAudioViewer) AudioScopes() *core.AudioScopeSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return c.snap
}

type mutableAudioViewer struct {
	mu   sync.Mutex
	snap *core.AudioScopeSnapshot
}

func (m *mutableAudioViewer) AudioScopes() *core.AudioScopeSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snap
}

func (m *mutableAudioViewer) setSnap(s *core.AudioScopeSnapshot) {
	m.mu.Lock()
	m.snap = s
	m.mu.Unlock()
}

func TestPresetsChanged_PersistentTriplesOnly(t *testing.T) {
	t.Parallel()
	a := [12]adapters.PresetEntry{
		{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "80s", Title: "MTV 80s"},
	}
	b := a
	b[0].Title = "MTV 80s — Renamed" // display-only change
	if presetsChanged(a, b) {
		t.Errorf("presetsChanged ignored title rename, but reported a change")
	}
	c := a
	c[0].ChannelID = "90s" // persistent triple changed
	if !presetsChanged(a, c) {
		t.Errorf("presetsChanged missed a real channel-id mutation")
	}
}

func TestPresetsChanged_EmptyVsFilled(t *testing.T) {
	t.Parallel()
	empty := [12]adapters.PresetEntry{}
	filled := empty
	filled[2].ProviderID = "mtv-rewind"
	filled[2].ChannelID = "amp"
	if !presetsChanged(empty, filled) {
		t.Errorf("presetsChanged missed an add into an empty slot")
	}
}

func TestPresetEnvelopeFromSnapshot_Shape(t *testing.T) {
	t.Parallel()
	snap := [12]adapters.PresetEntry{
		{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "1stday", Title: "First Day", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
		// remaining slots empty (Slot will be 0 — that's OK; the envelope re-derives slot=i+1)
	}
	env := presetEnvelopeFromSnapshot(snap)
	if len(env.Slots) != 12 {
		t.Fatalf("len(Slots) = %d, want 12", len(env.Slots))
	}
	if env.Slots[0].Slot != 1 || env.Slots[0].Provider != "mtv-rewind" {
		t.Errorf("Slots[0] = %+v, want Slot=1 Provider=mtv-rewind", env.Slots[0])
	}
	if env.Slots[1].Provider != "" {
		t.Errorf("empty Slots[1].Provider = %q, want empty", env.Slots[1].Provider)
	}
	if env.Slots[1].Slot != 2 {
		t.Errorf("empty Slots[1].Slot = %d, want 2", env.Slots[1].Slot)
	}
}

func TestHandleEvents_InitialBurstIncludesPresetsBetweenMeterAndAudio(t *testing.T) {
	t.Parallel()
	cfg := nonZeroConfig()
	cfg.PresetViewer = bundledFakeViewer()
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/receiver/events", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /receiver/events: %v", err)
	}
	defer resp.Body.Close()

	// Read the initial burst by collecting event names until we see "audio",
	// then assert the position invariant.
	rd := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	var names []string
	for time.Now().Before(deadline) {
		line, err := rd.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(line, "event: ") {
			names = append(names, strings.TrimSpace(strings.TrimPrefix(line, "event: ")))
			if names[len(names)-1] == "audio" {
				break
			}
		}
	}
	// Expected: [state, vfd, source, visualizer, transport, volume, audioDsp, meter, presets, history, audio]
	want := []string{"state", "vfd", "source", "visualizer", "transport", "volume", "audioDsp", "meter", "presets", "history", "audio"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("initial burst events = %v, want %v", names, want)
	}
}

func TestAudioDSPChanged(t *testing.T) {
	t.Parallel()
	a := AudioStripData{EQ: make([]float64, 10)}
	b := a
	if audioDSPChanged(a, b) {
		t.Error("identical strips should not change")
	}
	b.Bass = 3
	if !audioDSPChanged(a, b) {
		t.Error("bass change should be detected")
	}
	c := a
	c.EQ = append([]float64(nil), a.EQ...)
	c.EQ[2] = -1
	if !audioDSPChanged(a, c) {
		t.Error("EQ change should be detected")
	}
	d := a
	d.Persisted = !a.Persisted
	if !audioDSPChanged(a, d) {
		t.Error("persisted flip should be detected")
	}
}

func TestAudioDspEnvelope_Shape(t *testing.T) {
	t.Parallel()
	s := AudioStripData{Enabled: true, Bass: 4, EQ: make([]float64, 10), Engaged: true, Persisted: true}
	env := audioDspEnvelopeFrom(s)
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{`"params"`, `"bass":4`, `"engaged":true`, `"persisted":true`, `"eq":[`} {
		if !strings.Contains(got, want) {
			t.Errorf("envelope JSON missing %q: %s", want, got)
		}
	}
}
