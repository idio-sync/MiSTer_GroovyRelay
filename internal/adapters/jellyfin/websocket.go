package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// inboundEnvelope is the wire envelope of every JF WS message
// received by the bridge.
type inboundEnvelope struct {
	MessageType string          `json:"MessageType"`
	MessageID   string          `json:"MessageId,omitempty"`
	Data        json.RawMessage `json:"Data,omitempty"`
}

// outboundEnvelope is the wire envelope of every WS message the
// bridge sends. Used for KeepAlive and PlaybackStart/Progress/Stopped.
type outboundEnvelope struct {
	MessageType string `json:"MessageType"`
	Data        any    `json:"Data,omitempty"`
}

// wsDialInput carries dial params.
type wsDialInput struct {
	ServerURL  string
	Token      string
	DeviceID   string
	DeviceName string
	Version    string
}

// buildSocketURL converts an http(s) base URL to a ws(s) URL with
// the required query params.
func buildSocketURL(serverURL, token, deviceID string) string {
	u, _ := url.Parse(serverURL)
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	q := url.Values{}
	q.Set("api_key", token)
	q.Set("deviceId", deviceID)
	return fmt.Sprintf("%s://%s/socket?%s", scheme, u.Host, q.Encode())
}

// dialWebSocket opens the JF Sessions WebSocket. 30 s dial timeout.
func dialWebSocket(ctx context.Context, in wsDialInput) (*websocket.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	header := http.Header{}
	header.Set("Authorization", BuildAuthHeader(AuthHeaderInput{
		Token:    in.Token,
		Client:   jfClientName,
		Device:   effectiveDeviceName(in.DeviceName),
		DeviceID: in.DeviceID,
		Version:  in.Version,
	}))
	conn, _, err := websocket.Dial(dialCtx, buildSocketURL(in.ServerURL, in.Token, in.DeviceID), &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err != nil {
		return nil, fmt.Errorf("jellyfin: ws dial: %w", err)
	}
	return conn, nil
}

// inboundDispatcher is declared in adapter.go (Step 2). Do NOT
// re-declare it here — that produces "redeclared in this block."

// startWS spawns a long-lived runSession goroutine. Idempotent: a
// second call is a no-op while the first is still running. The
// goroutine signals exit via a.runDone so Stop() can wait for it.
func (a *Adapter) startWS(ctx context.Context, token string) error {
	a.mu.Lock()
	if a.startCancel != nil {
		a.mu.Unlock()
		return nil
	}
	wsCtx, cancel := context.WithCancel(ctx)
	a.startCancel = cancel
	a.runDone = make(chan struct{})
	done := a.runDone
	a.mu.Unlock()

	go func() {
		defer close(done)
		_ = a.runSession(wsCtx, token)
	}()
	return nil
}

// readLoop drains inbound messages and dispatches each to
// a.handleInbound. Exits on conn close or wsCtx done.
func (a *Adapter) readLoop(ctx context.Context, conn *websocket.Conn) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				slog.Info("jellyfin ws read error", "err", err)
			}
			return
		}
		var env inboundEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			slog.Warn("jellyfin ws: bad envelope", "err", err)
			continue
		}
		if env.MessageType == "ForceKeepAlive" {
			var secs int
			if err := json.Unmarshal(env.Data, &secs); err == nil && secs > 0 {
				a.setKeepAliveInterval(time.Duration(secs) * time.Second)
			}
			continue
		}
		if a.handleInbound != nil {
			a.handleInbound(env.MessageType, env.Data)
		} else {
			slog.Debug("jellyfin ws: no dispatcher", "type", env.MessageType)
		}
	}
}

// realWSConn wraps *websocket.Conn to satisfy the package-local
// wsConn interface. Phase 4.2 adds Write methods.
type realWSConn struct {
	conn *websocket.Conn
}

// Close satisfies wsConn and performs a clean WS close handshake.
func (c *realWSConn) Close() error {
	return c.conn.Close(websocket.StatusNormalClosure, "")
}

// outboundQueueSize bounds the buffered outbound channel. Each item is
// at most ~1 KB; 64 entries is roughly 10 minutes at peak send-rate
// for KeepAlive (1s) + Progress (10s).
const outboundQueueSize = 64

// startWriteLoop is called from startWS after the read goroutine has
// been spawned. It owns the conn write side; every other goroutine
// (KeepAlive ticker, command handlers, reporting) sends via
// a.sendOutbound which puts items on a single channel.
func (a *Adapter) startWriteLoop(ctx context.Context, conn *websocket.Conn) {
	a.mu.Lock()
	outboundCh := make(chan outboundEnvelope, outboundQueueSize)
	a.outboundCh = outboundCh
	keepaliveSet := make(chan time.Duration, 1)
	a.keepaliveSet = keepaliveSet
	a.mu.Unlock()

	go func() {
		var ticker *time.Ticker
		var tickerC <-chan time.Time
		defer func() {
			if ticker != nil {
				ticker.Stop()
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case d := <-keepaliveSet:
				if ticker != nil {
					ticker.Stop()
				}
				if d > 0 {
					ticker = time.NewTicker(d)
					tickerC = ticker.C
				} else {
					ticker = nil
					tickerC = nil
				}
			case <-tickerC:
				_ = a.writeFrame(ctx, conn, outboundEnvelope{MessageType: "KeepAlive"})
			case msg := <-outboundCh:
				_ = a.writeFrame(ctx, conn, msg)
			}
		}
	}()
}

// writeFrame marshals env and writes it to conn. Errors are logged
// only — the read loop will detect a dead conn and trigger reconnect.
func (a *Adapter) writeFrame(ctx context.Context, conn *websocket.Conn, env outboundEnvelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		slog.Warn("jellyfin ws: marshal outbound", "err", err, "type", env.MessageType)
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, data); err != nil {
		slog.Info("jellyfin ws: write error", "err", err, "type", env.MessageType)
		return err
	}
	return nil
}

// sendOutbound enqueues a message for the write loop. Three paths:
//   - WS conn live + outboundCh has room: send to ch (fast path).
//   - WS conn live + outboundCh full: drop with warn.
//   - WS conn down (outboundCh nil): push to a.pendingBuf (drop-oldest
//     ring of capacity 32). Drained on the next successful runOneConn.
func (a *Adapter) sendOutbound(env outboundEnvelope) {
	a.mu.Lock()
	ch := a.outboundCh
	if ch == nil {
		if a.pendingBuf == nil {
			a.pendingBuf = newRingBuffer(32)
		}
		a.pendingBuf.push(env)
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	select {
	case ch <- env:
	default:
		slog.Warn("jellyfin ws: outbound queue full, dropping", "type", env.MessageType)
	}
}

// setKeepAliveInterval is called from the ForceKeepAlive handler in
// readLoop. Pass d=0 to stop sending KeepAlives.
func (a *Adapter) setKeepAliveInterval(d time.Duration) {
	a.mu.Lock()
	ch := a.keepaliveSet
	a.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- d:
	default:
		select {
		case <-ch:
		default:
		}
		ch <- d
	}
}

// hasExistingSession probes /Sessions?DeviceId=<id> to see whether
// JF already has a session row for this DeviceId. Returns true only
// when the GET succeeds AND the response contains an entry whose
// DeviceId matches in.DeviceID.
func hasExistingSession(ctx context.Context, in CapabilitiesInput) (bool, error) {
	q := url.Values{}
	q.Set("DeviceId", in.DeviceID)
	q.Set("api_key", in.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(in.ServerURL, "/")+"/Sessions?"+q.Encode(), nil)
	if err != nil {
		return false, err
	}
	resp, err := jfHTTPClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var sessions []struct {
		DeviceID string `json:"DeviceId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return false, err
	}
	for _, s := range sessions {
		if s.DeviceID == in.DeviceID {
			return true, nil
		}
	}
	return false, nil
}

// runSession is the long-lived "stay registered as a JF cast target"
// driver. It loops: POST Capabilities (or skip if probe shows we're
// already registered) → dial WS → run read+write loops → backoff on
// disconnect. Exits when ctx is cancelled.
//
// On the first iteration, capabilitiesPosted is false, forcing one
// POST. On subsequent iterations, the /Sessions probe decides — but
// only AFTER a previously-successful WS run (hadSuccessfulRun gate),
// preventing POST duplication during rapid dial-failure loops.
func (a *Adapter) runSession(ctx context.Context, token string) error {
	a.mu.Lock()
	cfg := a.cfg
	deviceID := a.deviceID
	a.mu.Unlock()

	capabilitiesPosted := false
	hadSuccessfulRun := false
	backoff := time.Second
	const maxBackoff = 60 * time.Second

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		shouldPost := !capabilitiesPosted
		if capabilitiesPosted && hadSuccessfulRun {
			present, err := hasExistingSession(ctx, CapabilitiesInput{
				ServerURL: cfg.ServerURL, Token: token, DeviceID: deviceID,
			})
			if err == nil && !present {
				shouldPost = true
			}
		}
		if shouldPost {
			if err := PostCapabilities(ctx, CapabilitiesInput{
				ServerURL:           cfg.ServerURL,
				Token:               token,
				DeviceID:            deviceID,
				DeviceName:          cfg.DeviceName,
				Version:             linkVersion,
				MaxVideoBitrateKbps: cfg.MaxVideoBitrateKbps,
			}); err != nil {
				slog.Warn("jellyfin capabilities post failed; cast target will not appear until retry succeeds", "err", err)
				a.setState(adapters.StateError, err.Error())
				goto wait
			}
			capabilitiesPosted = true
		}

		{
			conn, err := dialWebSocket(ctx, wsDialInput{
				ServerURL:  cfg.ServerURL,
				Token:      token,
				DeviceID:   deviceID,
				DeviceName: cfg.DeviceName,
				Version:    linkVersion,
			})
			if err == nil {
				// Dial succeeded → the server created a session row. Mark
				// hadSuccessfulRun so the probe fires on the next iteration.
				hadSuccessfulRun = true
				a.setState(adapters.StateRunning, "")
				runStart := time.Now()
				a.runOneConn(ctx, conn)
				_ = conn.Close(websocket.StatusNormalClosure, "")
				if ctx.Err() == nil {
					a.setState(adapters.StateStarting, "websocket disconnected; reconnecting")
				}
				// Only reset backoff if the connection lasted a reasonable
				// time (≥5 s), indicating a healthy server.
				if time.Since(runStart) >= 5*time.Second {
					backoff = time.Second
				}
			} else if ctx.Err() == nil {
				slog.Warn("jellyfin websocket dial failed; cast target will not appear until retry succeeds", "err", err)
				a.setState(adapters.StateError, err.Error())
			}
		}

	wait:
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(jitter(backoff)):
		}
		backoff = backoff * 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// runOneConn runs the read+write loops for a single conn. Returns
// when the conn closes for any reason. On return, both goroutines
// have exited.
func (a *Adapter) runOneConn(ctx context.Context, conn *websocket.Conn) {
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	a.mu.Lock()
	a.ws = &realWSConn{conn: conn}
	a.mu.Unlock()

	a.startWriteLoop(connCtx, conn)

	// Drain any pending messages buffered while the WS was down.
	a.drainPending()

	// readLoop runs in this goroutine; when it returns, the conn is dead.
	a.readLoop(connCtx, conn)

	cancel()

	a.mu.Lock()
	a.ws = nil
	a.outboundCh = nil
	a.keepaliveSet = nil
	a.mu.Unlock()
}

// jitter adds 0–25% jitter to d for the reconnect backoff so a fleet
// of bridges doesn't thunder against a recovering JF server.
func jitter(d time.Duration) time.Duration {
	delta := time.Duration(uint64(time.Now().UnixNano())%uint64(d/4) + 1)
	return d + delta
}

// drainPending pushes everything in a.pendingBuf to a.outboundCh
// using the standard sendOutbound discipline. Called by runOneConn
// just after startWriteLoop has set up the outbound channel.
func (a *Adapter) drainPending() {
	a.mu.Lock()
	buf := a.pendingBuf
	a.pendingBuf = nil
	a.mu.Unlock()
	if buf == nil {
		return
	}
	for _, env := range buf.drainAll() {
		a.sendOutbound(env)
	}
}
