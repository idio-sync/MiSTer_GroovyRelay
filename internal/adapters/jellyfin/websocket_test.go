package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// startTestJFServer mounts a minimal JF-shaped server on httptest:
//   - GET /System/Info — returns 200 (token probe)
//   - POST /Sessions/Capabilities/Full — captures body, returns 204
//   - GET  /socket — accepts WS upgrade, exposes the connection to
//                    the test via the returned channels
//   - GET  /Sessions — used by the reconnect probe (Task 4.3)
func startTestJFServer(t *testing.T) (*httptest.Server, <-chan *websocket.Conn, <-chan []byte) {
	t.Helper()
	wsCh := make(chan *websocket.Conn, 1)
	capCh := make(chan []byte, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/System/Info", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Id":"server-1","Version":"10.10.0"}`))
	})
	mux.HandleFunc("/Sessions/Capabilities/Full", func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		select {
		case capCh <- body:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/Sessions", func(w http.ResponseWriter, r *http.Request) {
		// Default: no existing sessions for this DeviceId.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/socket", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("ws accept: %v", err)
			return
		}
		select {
		case wsCh <- conn:
		default:
		}
		// Hold the conn open until the test closes it.
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, wsCh, capCh
}

func TestWSDial_PostsCapabilitiesAndUpgrades(t *testing.T) {
	srv, wsCh, capCh := startTestJFServer(t)

	conn, err := dialWebSocket(t.Context(), wsDialInput{
		ServerURL: srv.URL,
		Token:     "tok",
		DeviceID:  "device-1",
	})
	if err != nil {
		t.Fatalf("dialWebSocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	select {
	case <-wsCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for WS upgrade")
	}
	// Capabilities POST is the caller's responsibility (it lives in
	// startWS, not dialWebSocket); separately tested below.
	_ = capCh
}

func TestStartWS_PostsCapabilitiesBeforeDial(t *testing.T) {
	srv, wsCh, capCh := startTestJFServer(t)

	a := New(nil, t.TempDir(), "device-1")
	a.cfg = Config{ServerURL: srv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := a.startWS(ctx, "tok"); err != nil {
		t.Fatalf("startWS: %v", err)
	}

	select {
	case body := <-capCh:
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("capabilities body: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Capabilities POST not sent")
	}
	select {
	case <-wsCh:
	case <-time.After(3 * time.Second):
		t.Fatal("WS upgrade did not happen")
	}
}

func TestRead_DispatchesByMessageType(t *testing.T) {
	srv, wsCh, _ := startTestJFServer(t)

	a := New(nil, t.TempDir(), "device-1")
	a.cfg = Config{ServerURL: srv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}

	// Hook the dispatch table.
	var (
		mu   sync.Mutex
		seen []string
	)
	a.handleInbound = func(msgType string, data json.RawMessage) {
		mu.Lock()
		seen = append(seen, msgType)
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := a.startWS(ctx, "tok"); err != nil {
		t.Fatal(err)
	}

	// Server-side conn captured; push three message types at the bridge.
	var conn *websocket.Conn
	select {
	case conn = <-wsCh:
	case <-time.After(3 * time.Second):
		t.Fatal("no ws upgrade")
	}
	for _, mt := range []string{"Play", "Playstate", "GeneralCommand"} {
		payload := []byte(`{"MessageType":"` + mt + `","Data":{}}`)
		if err := conn.Write(t.Context(), websocket.MessageText, payload); err != nil {
			t.Fatalf("conn.Write: %v", err)
		}
	}

	// Wait briefly for the dispatcher to observe all three.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("dispatched %d messages, want 3: %v", len(seen), seen)
	}
	want := []string{"Play", "Playstate", "GeneralCommand"}
	for i, w := range want {
		if seen[i] != w {
			t.Errorf("seen[%d] = %q, want %q", i, seen[i], w)
		}
	}
}

func TestStartWS_BuildsCorrectURL(t *testing.T) {
	// Test the URL builder in isolation.
	got := buildSocketURL("https://jellyfin.example.com", "tok-x", "device-y")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "wss" {
		t.Errorf("scheme = %q, want wss", u.Scheme)
	}
	if u.Path != "/socket" {
		t.Errorf("path = %q, want /socket", u.Path)
	}
	if u.Query().Get("api_key") != "tok-x" {
		t.Errorf("api_key = %q", u.Query().Get("api_key"))
	}
	if u.Query().Get("deviceId") != "device-y" {
		t.Errorf("deviceId = %q", u.Query().Get("deviceId"))
	}
	httpURL := buildSocketURL("http://10.0.0.5:8096", "tok", "dev")
	if !strings.HasPrefix(httpURL, "ws://") {
		t.Errorf("http server should map to ws://, got %s", httpURL)
	}
}
