package chassis

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// stateEnvelope is the payload for the `state` SSE event. Explicit
// struct tag because Go's default JSON encoder emits PascalCase
// ("State") and the wire format mandates camelCase ("state").
type stateEnvelope struct {
	State string `json:"state"`
}

// vfdEnvelope is the payload for the `vfd` SSE event. Carries the
// minimal Phase 1 fields the client needs to update the VFD spans;
// Spec 5 (telemetry) and later specs add their own envelope types
// for other event names.
type vfdEnvelope struct {
	Title        string `json:"title"`
	Marquee      string `json:"marquee"`
	QueueCurrent int    `json:"queueCurrent"`
	QueueTotal   int    `json:"queueTotal"`
	Uptime       string `json:"uptime"`
}

// vfdEnvelopeFrom flattens a VFDData into the wire-format envelope.
// Kept separate from vfdEnvelope's definition so the envelope is a
// pure data type and the conversion lives next to its caller in
// handleEvents.
func vfdEnvelopeFrom(v VFDData) vfdEnvelope {
	return vfdEnvelope{
		Title:        v.Title,
		Marquee:      v.Marquee,
		QueueCurrent: v.QueueCurrent,
		QueueTotal:   v.QueueTotal,
		Uptime:       v.Uptime,
	}
}

// emit writes one SSE record (event line + data line + terminating
// blank line). Returns the underlying writer error so callers can
// detect mid-write client disconnects and bail cleanly.
func emit(w io.Writer, name string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, body); err != nil {
		return err
	}
	return nil
}

// vfdChanged enumerates exactly the fields that participate in the
// `vfd` event payload. Other fields on VFDData (notably SystemTime,
// which is client-ticker-driven via [data-system-time], and the
// duplicated State that mirrors ReceiverPageData.State and is handled
// by a separate `state` event) are deliberately excluded.
//
// Explicit field-level compare beats reflect.DeepEqual for speed
// (no reflection) and clarity (the function definition IS the spec
// of which fields are part of the Phase 1 VFD wire-format surface).
func vfdChanged(a, b VFDData) bool {
	return a.Title != b.Title ||
		a.Marquee != b.Marquee ||
		a.QueueCurrent != b.QueueCurrent ||
		a.QueueTotal != b.QueueTotal ||
		a.Uptime != b.Uptime
}

// handleEvents serves a long-lived SSE stream at GET /receiver/events.
// Scaffolding only — the diff ticker that emits change events lands
// in the next task. This implementation handles:
//   - 500 when the ResponseWriter cannot flush
//   - SSE response headers
//   - retry: 3000 directive (pins browser reconnect cadence)
//   - initial state + vfd snapshot
//   - clean termination on r.Context().Done()
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	if _, err := io.WriteString(w, "retry: 3000\n\n"); err != nil {
		return
	}

	last := snapshotFromSession(s.cfg, s.session, time.Now())
	if err := emit(w, "state", stateEnvelope{State: string(last.State)}); err != nil {
		return
	}
	if err := emit(w, "vfd", vfdEnvelopeFrom(last.VFD)); err != nil {
		return
	}
	flusher.Flush()

	tick := time.NewTicker(chassisTickInterval)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			curr := snapshotFromSession(s.cfg, s.session, time.Now())
			if curr.State != last.State {
				if err := emit(w, "state", stateEnvelope{State: string(curr.State)}); err != nil {
					return
				}
				last.State = curr.State
			}
			flusher.Flush()
		}
	}
}

// chassisTickInterval is the diff-ticker cadence. Package-level var so
// tests can shorten it via TestMain / setter without changing
// production behaviour. Production default 250 ms; tests override to
// 100 ms (still slow enough to be reliable on busy CI workers).
var chassisTickInterval = 250 * time.Millisecond
