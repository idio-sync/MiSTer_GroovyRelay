// Package liveaudio is the protocol-agnostic half of the live audio
// receivers (Spotify Connect, AirPlay): it supervises the helper process,
// relays its raw PCM to ffmpeg over a loopback HTTP route, and runs the
// Idle/Live/Held session state machine against core. Protocol adapters
// only translate helper events into Event values.
//
// Spec: docs/superpowers/specs/2026-09-24-live-audio-receivers-design.md.
package liveaudio

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// The helpers are configured to emit s16le, 44.1 kHz, stereo.
const (
	SampleRate     = 44100
	Channels       = 2
	bytesPerFrame  = Channels * 2
	BytesPerSecond = SampleRate * bytesPerFrame
	PCMFormat      = "s16le"
)

const (
	// relayCapacity bounds the buffered audio, and so the latency a helper
	// that runs ahead of real time (librespot's pipe backend) can build up
	// before its writes block.
	relayCapacity = BytesPerSecond / 5 // 200 ms
	relayTick     = 10 * time.Millisecond
	// relayMaxBurst caps one write after a stalled reader, so a downstream
	// hiccup costs audio instead of permanently adding latency.
	relayMaxBurst = BytesPerSecond / 10 // 100 ms
)

type clock interface {
	Now() time.Time
	NewTicker(time.Duration) ticker
}

type ticker interface {
	C() <-chan time.Time
	Stop()
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) NewTicker(d time.Duration) ticker {
	return realTicker{time.NewTicker(d)}
}

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

// Relay moves a helper's PCM stream to one paced HTTP reader: ffmpeg's
// capture input. The reader receives exactly real-time audio; whenever
// the helper has nothing buffered (pause, gaps, startup) it receives
// silence, so the visualizer keeps running instead of ffmpeg stalling.
// That silence also turns the buffer into a self-stabilizing jitter
// buffer: the first shortfalls pad it until the helper's delivery jitter
// no longer empties it.
//
// Only the newest reader is served; a new connection (ffprobe, then
// ffmpeg, then ffmpeg again after an artwork restart) ends the previous
// one.
type Relay struct {
	route string // mux pattern prefix, e.g. "/internal/liveaudio/spotify/pcm/"
	token string
	clock clock

	mu      sync.Mutex
	cond    *sync.Cond
	buf     []byte
	discard bool
	reader  uint64 // id of the reader being served; 0 = none
	nextID  uint64
}

// NewRelay builds a relay whose reader route lives under route, which
// must start and end with "/". The relay starts in discard mode.
func NewRelay(route string) (*Relay, error) {
	token, err := RandomToken()
	if err != nil {
		return nil, err
	}
	return newRelay(route, token, realClock{}), nil
}

func newRelay(route, token string, c clock) *Relay {
	r := &Relay{route: route, token: token, clock: c, discard: true}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// RandomToken returns an unguessable URL-safe token for a loopback route.
func RandomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("liveaudio: token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// Mount registers the reader route on the shared mux.
func (r *Relay) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET "+r.route+"{token}", r.ServeHTTP)
}

// URL is the loopback URL ffmpeg reads, for a bridge listening on httpPort.
func (r *Relay) URL(httpPort int) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s%s", httpPort, r.route, r.token)
}

// SetDiscard switches discard mode. While discarding, Write drops input so
// an idle helper never blocks; otherwise a full buffer blocks Write.
// Entering discard mode also empties the buffer.
func (r *Relay) SetDiscard(discard bool) {
	r.mu.Lock()
	r.discard = discard
	if discard {
		r.buf = r.buf[:0]
	}
	r.mu.Unlock()
	r.cond.Broadcast()
}

// Write makes the relay a helper's stdout. It blocks while the buffer is
// full (unless discarding); Reset releases a blocked Write. It never fails.
func (r *Relay) Write(p []byte) (int, error) {
	r.push(p)
	return len(p), nil
}

// push appends p, waiting for room unless discarding.
func (r *Relay) push(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(p) > 0 {
		if r.discard {
			return
		}
		room := relayCapacity - len(r.buf)
		if room <= 0 {
			r.cond.Wait()
			continue
		}
		n := min(room, len(p))
		r.buf = append(r.buf, p[:n]...)
		p = p[n:]
	}
}

// take removes up to n bytes, whole frames only.
func (r *Relay) take(n int) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	avail := len(r.buf) - len(r.buf)%bytesPerFrame
	n = min(n, avail)
	out := make([]byte, n)
	copy(out, r.buf[:n])
	r.buf = r.buf[:copy(r.buf, r.buf[n:])]
	r.cond.Broadcast()
	return out
}

// Reset ends the current reader, enters discard mode, and empties the
// buffer, releasing any Write blocked on it. The relay stays usable.
func (r *Relay) Reset() {
	r.mu.Lock()
	r.reader = 0
	r.discard = true
	r.buf = r.buf[:0]
	r.mu.Unlock()
	r.cond.Broadcast()
}

func (r *Relay) claimReader() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.reader = r.nextID
	return r.reader
}

func (r *Relay) stillServing(id uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reader == id
}

func (r *Relay) releaseReader(id uint64) {
	r.mu.Lock()
	if r.reader == id {
		r.reader = 0
	}
	r.mu.Unlock()
}

func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if !RemoteIsLoopback(req.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.PathValue("token")), []byte(r.token)) != 1 {
		http.NotFound(w, req)
		return
	}
	id := r.claimReader()
	defer r.releaseReader(id)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush() // headers now, not on the first tick
	}

	tick := r.clock.NewTicker(relayTick)
	defer tick.Stop()
	start := r.clock.Now()
	var sent int64
	for {
		select {
		case <-req.Context().Done():
			return
		case <-tick.C():
		}
		if !r.stillServing(id) {
			return
		}
		due := framesIn(r.clock.Now().Sub(start)) * bytesPerFrame
		need := due - sent
		if need <= 0 {
			continue
		}
		if need > relayMaxBurst {
			need = relayMaxBurst
			sent = due - need
		}
		chunk := r.take(int(need))
		if pad := int(need) - len(chunk); pad > 0 {
			chunk = append(chunk, make([]byte, pad)...) // silence
		}
		if _, err := w.Write(chunk); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		sent += need
	}
}

// framesIn is the whole number of frames played in d, in integer math
// that neither drifts nor overflows over a days-long session.
func framesIn(d time.Duration) int64 {
	secs, rem := int64(d/time.Second), int64(d%time.Second)
	return secs*SampleRate + rem*SampleRate/int64(time.Second)
}

// RemoteIsLoopback reports whether an http.Request.RemoteAddr is a
// loopback address. Unmap first: an IPv4-mapped IPv6 loopback
// (::ffff:127.0.0.1) is not IsLoopback until unmapped.
func RemoteIsLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.Unmap().IsLoopback()
}
