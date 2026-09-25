package liveaudio

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeClock drives relay pacing deterministically. Advance moves time and
// fires every live ticker.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
}

type fakeTicker struct {
	c       chan time.Time
	stopped bool
}

func (t *fakeTicker) C() <-chan time.Time { return t.c }
func (t *fakeTicker) Stop()               { t.stopped = true }

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTicker(time.Duration) ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTicker{c: make(chan time.Time, 1)}
	c.tickers = append(c.tickers, t)
	return t
}

func (c *fakeClock) tickerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tickers)
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	tickers := append([]*fakeTicker(nil), c.tickers...)
	c.mu.Unlock()
	for _, t := range tickers {
		select {
		case t.c <- now:
		default:
		}
	}
}

const testRoute = "/internal/liveaudio/test/pcm/"

type relayHarness struct {
	t     *testing.T
	clock *fakeClock
	relay *Relay
	srv   *httptest.Server
}

func newRelayHarness(t *testing.T) *relayHarness {
	t.Helper()
	clk := newFakeClock()
	relay := newRelay(testRoute, "tok", clk)
	mux := http.NewServeMux()
	relay.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &relayHarness{t: t, clock: clk, relay: relay, srv: srv}
}

// open connects a reader and waits until its handler is ticking.
func (h *relayHarness) open() io.ReadCloser {
	h.t.Helper()
	before := h.clock.tickerCount()
	resp, err := http.Get(h.srv.URL + testRoute + "tok")
	if err != nil {
		h.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("status = %d", resp.StatusCode)
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.clock.tickerCount() == before {
		if time.Now().After(deadline) {
			h.t.Fatal("reader handler never started ticking")
		}
		time.Sleep(time.Millisecond)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp.Body
}

func readN(t *testing.T, r io.Reader, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(r, buf)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read %d bytes: %v", n, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading %d bytes", n)
	}
	return buf
}

func fill(b byte, n int) []byte { return bytes.Repeat([]byte{b}, n) }

const bytesPer10ms = BytesPerSecond / 100 // 1764

func TestRelayPacesRealAudioThenPadsSilence(t *testing.T) {
	h := newRelayHarness(t)
	h.relay.SetDiscard(false)
	h.relay.push(fill(0x11, 2*bytesPer10ms+880))
	body := h.open()

	for i := 0; i < 2; i++ {
		h.clock.Advance(10 * time.Millisecond)
		if got := readN(t, body, bytesPer10ms); !bytes.Equal(got, fill(0x11, bytesPer10ms)) {
			t.Fatalf("tick %d: not all real audio", i)
		}
	}
	h.clock.Advance(10 * time.Millisecond)
	got := readN(t, body, bytesPer10ms)
	if !bytes.Equal(got[:880], fill(0x11, 880)) || !bytes.Equal(got[880:], fill(0, bytesPer10ms-880)) {
		t.Fatal("underrun tick should be the remaining audio then silence")
	}
	h.clock.Advance(10 * time.Millisecond)
	if got := readN(t, body, bytesPer10ms); !bytes.Equal(got, fill(0, bytesPer10ms)) {
		t.Fatal("empty buffer should produce pure silence")
	}
}

func TestRelayCapsBurstAfterStall(t *testing.T) {
	h := newRelayHarness(t)
	body := h.open()
	h.clock.Advance(time.Second) // reader stalled for a second
	readN(t, body, relayMaxBurst)
	h.clock.Advance(10 * time.Millisecond)
	readN(t, body, bytesPer10ms)
	// Nothing more may be pending: the stall's debt was dropped, not queued.
	h.relay.push(fill(0x22, 4)) // discard mode: dropped
	extra := make(chan int, 1)
	go func() {
		n, _ := body.Read(make([]byte, 1))
		extra <- n
	}()
	select {
	case n := <-extra:
		t.Fatalf("reader got %d unexpected byte(s) beyond the paced amount", n)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRelayNewReaderTakesOver(t *testing.T) {
	h := newRelayHarness(t)
	first := h.open()
	second := h.open()
	h.clock.Advance(10 * time.Millisecond)
	readN(t, second, bytesPer10ms)
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(first)
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("superseded reader was not ended")
	}
}

func TestRelayResetEndsReaderAndReleasesFeed(t *testing.T) {
	h := newRelayHarness(t)
	h.relay.SetDiscard(false)
	body := h.open()

	fed := make(chan struct{})
	go func() {
		h.relay.push(fill(0x33, relayCapacity+bytesPerFrame))
		close(fed)
	}()
	select {
	case <-fed:
		t.Fatal("push past capacity should block while nothing is read")
	case <-time.After(50 * time.Millisecond):
	}
	h.relay.Reset()
	select {
	case <-fed:
	case <-time.After(5 * time.Second):
		t.Fatal("Reset did not release the blocked feed")
	}
	h.clock.Advance(10 * time.Millisecond)
	if _, err := io.ReadAll(body); err != nil {
		t.Fatalf("reader after Reset: %v", err)
	}
}

func TestRelayDiscardDropsInput(t *testing.T) {
	r := newRelay(testRoute, "tok", newFakeClock())
	done := make(chan struct{})
	go func() {
		r.push(fill(0x44, 10*relayCapacity))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("push blocked in discard mode")
	}
	if got := r.take(relayCapacity); len(got) != 0 {
		t.Fatalf("discard mode buffered %d bytes", len(got))
	}
}

func TestRelayTakeReturnsWholeFrames(t *testing.T) {
	r := newRelay(testRoute, "tok", newFakeClock())
	r.SetDiscard(false)
	r.push(fill(0x55, 6))
	if got := r.take(100); len(got) != 4 {
		t.Fatalf("take = %d bytes, want 4 (one whole frame)", len(got))
	}
	r.push(fill(0x55, 2))
	if got := r.take(100); len(got) != 4 {
		t.Fatalf("take after completing the frame = %d bytes, want 4", len(got))
	}
}

func TestRelayRejectsForeignCallersAndBadTokens(t *testing.T) {
	r := newRelay(testRoute, "tok", newFakeClock())
	mux := http.NewServeMux()
	r.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, testRoute+"tok", nil)
	req.RemoteAddr = "192.168.1.20:5000"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("LAN caller status = %d, want 403", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, testRoute+"wrong", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad token status = %d, want 404", rec.Code)
	}
}

func TestRemoteIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:1":          true,
		"[::1]:1":              true,
		"[::ffff:127.0.0.1]:1": true,
		"10.0.0.1:1":           false,
		"[::ffff:10.0.0.1]:1":  false,
		"garbage":              false,
	}
	for addr, want := range cases {
		if got := remoteIsLoopback(addr); got != want {
			t.Errorf("remoteIsLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

// TestRelayFeedsRealFFmpegAtRealTime reads the relay with the same input
// flags core passes for a live audio session: one second of audio must
// take about one second of wall time.
func TestRelayFeedsRealFFmpegAtRealTime(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	relay, err := NewRelay(testRoute)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	relay.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	relay.SetDiscard(false)
	go relay.push(fill(0x10, BytesPerSecond/2)) // half real audio, half silence

	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	start := time.Now()
	out, err := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-v", "error",
		"-f", PCMFormat, "-sample_rate", strconv.Itoa(SampleRate), "-ch_layout", "stereo",
		"-i", relay.URL(port), "-t", "1", "-f", "null", "-").CombinedOutput()
	elapsed := time.Since(start)
	relay.Reset()
	if err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	if elapsed < 800*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("1 s of audio took %v; want real-time pacing", elapsed)
	}
}

func TestFramesIn(t *testing.T) {
	cases := map[time.Duration]int64{
		10 * time.Millisecond:                 441,
		time.Second:                           44100,
		1500 * time.Millisecond:               66150,
		30*24*time.Hour + 10*time.Millisecond: 30*24*3600*44100 + 441,
	}
	for d, want := range cases {
		if got := framesIn(d); got != want {
			t.Errorf("framesIn(%v) = %d, want %d", d, got, want)
		}
	}
}

func TestRelayURL(t *testing.T) {
	r := newRelay(testRoute, "tok", newFakeClock())
	if got, want := r.URL(32500), "http://127.0.0.1:32500"+testRoute+"tok"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}
