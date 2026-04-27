package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/coder/websocket"
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
	ServerURL string
	Token     string
	DeviceID  string
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
	conn, _, err := websocket.Dial(dialCtx, buildSocketURL(in.ServerURL, in.Token, in.DeviceID), nil)
	if err != nil {
		return nil, fmt.Errorf("jellyfin: ws dial: %w", err)
	}
	return conn, nil
}

// inboundDispatcher is declared in adapter.go (Step 2). Do NOT
// re-declare it here — that produces "redeclared in this block."

// startWS posts Capabilities, dials the WS, and starts the read
// goroutine. Sets a.startCancel so Stop() can tear it all down.
// Idempotent: if startWS is already running, returns nil without
// re-dialing.
func (a *Adapter) startWS(ctx context.Context, token string) error {
	a.mu.Lock()
	if a.startCancel != nil {
		a.mu.Unlock()
		return nil
	}
	cfg := a.cfg
	deviceID := a.deviceID
	a.mu.Unlock()

	if err := PostCapabilities(ctx, CapabilitiesInput{
		ServerURL:           cfg.ServerURL,
		Token:               token,
		DeviceID:            deviceID,
		Version:             linkVersion,
		MaxVideoBitrateKbps: cfg.MaxVideoBitrateKbps,
	}); err != nil {
		return err
	}

	conn, err := dialWebSocket(ctx, wsDialInput{
		ServerURL: cfg.ServerURL,
		Token:     token,
		DeviceID:  deviceID,
	})
	if err != nil {
		return err
	}

	wsCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.startCancel = cancel
	a.ws = &realWSConn{conn: conn}
	a.mu.Unlock()

	a.startWriteLoop(wsCtx, conn)
	go a.readLoop(wsCtx, conn)
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
	a.outboundCh = make(chan outboundEnvelope, outboundQueueSize)
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
			case msg := <-a.outboundCh:
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

