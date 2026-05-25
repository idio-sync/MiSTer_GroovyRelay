package chassis

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// stateEnvelope is the payload for the `state` SSE event. Explicit
// struct tag because Go's default JSON encoder emits PascalCase
// ("State") and the wire format mandates camelCase ("state").
type stateEnvelope struct {
	State string `json:"state"`
}

type vizEnvelope struct {
	Mode string `json:"mode"`
}

type sourceEnvelope struct {
	Buttons []sourceButtonEnvelope `json:"buttons"`
}

type sourceButtonEnvelope struct {
	Label       string `json:"label"`
	Action      string `json:"action"`
	Active      bool   `json:"active"`
	Lit         bool   `json:"lit"`
	Unavailable bool   `json:"unavailable"`
	InputID     string `json:"inputId"`
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

type transportEnvelope struct {
	State           string          `json:"state"`
	SeekFillPercent int             `json:"seekFillPercent"`
	ElapsedTime     string          `json:"elapsedTime"`
	TotalTime       string          `json:"totalTime"`
	PercentPlayed   string          `json:"percentPlayed"`
	OffsetMS        int             `json:"offsetMs"`
	DurationMS      int             `json:"durationMs"`
	ActionsEnabled  actionsEnabledE `json:"actionsEnabled"`
	AdapterRef      string          `json:"adapterRef"`
	Generation      uint64          `json:"generation"`
}

type actionsEnabledE struct {
	Previous    bool `json:"previous"`
	Next        bool `json:"next"`
	PauseResume bool `json:"pauseResume"`
	Stop        bool `json:"stop"`
	Replay      bool `json:"replay"`
	Seek        bool `json:"seek"`
}

// volumeEnvelope is the payload for the `volume` SSE event. Kept
// separate from transportEnvelope so volume-only changes (the chassis
// knob) don't force the transport UI to repaint, and so transport-only
// changes (state/seek) don't echo the unchanged volume.
type volumeEnvelope struct {
	OutputVolume int `json:"outputVolume"`
}

func volumeEnvelopeFrom(t TransportData) volumeEnvelope {
	return volumeEnvelope{OutputVolume: t.OutputVolume}
}

func transportEnvelopeFrom(t TransportData) transportEnvelope {
	return transportEnvelope{
		State:           t.State,
		SeekFillPercent: t.SeekFillPercent,
		ElapsedTime:     t.ElapsedTime,
		TotalTime:       t.TotalTime,
		PercentPlayed:   t.PercentPlayed,
		OffsetMS:        t.OffsetMS,
		DurationMS:      t.DurationMS,
		ActionsEnabled: actionsEnabledE{
			Previous:    t.ActionsEnabled.Previous,
			Next:        t.ActionsEnabled.Next,
			PauseResume: t.ActionsEnabled.PauseResume,
			Stop:        t.ActionsEnabled.Stop,
			Replay:      t.ActionsEnabled.Replay,
			Seek:        t.ActionsEnabled.Seek,
		},
		AdapterRef: t.AdapterRef,
		Generation: t.Generation,
	}
}

func sourceEnvelopeFromSnapshot(data ReceiverPageData) sourceEnvelope {
	out := sourceEnvelope{Buttons: make([]sourceButtonEnvelope, 0, len(data.Source.Buttons))}
	for _, button := range data.Source.Buttons {
		out.Buttons = append(out.Buttons, sourceButtonEnvelope{
			Label:       button.Label,
			Action:      button.Action,
			Active:      button.Active,
			Lit:         button.Lit,
			Unavailable: button.Unavailable,
			InputID:     button.InputID,
		})
	}
	return out
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

func transportChanged(a, b TransportData) bool {
	return a.State != b.State ||
		a.SeekFillPercent != b.SeekFillPercent ||
		a.ElapsedTime != b.ElapsedTime ||
		a.TotalTime != b.TotalTime ||
		a.PercentPlayed != b.PercentPlayed ||
		a.OffsetMS != b.OffsetMS ||
		a.DurationMS != b.DurationMS ||
		a.ActionsEnabled != b.ActionsEnabled ||
		a.AdapterRef != b.AdapterRef ||
		a.Generation != b.Generation
}

func meterChanged(curr, last MeterData) bool {
	return curr.State != last.State ||
		curr.Paused != last.Paused ||
		curr.Generation != last.Generation ||
		curr.SampleSeq != last.SampleSeq ||
		curr.MidRow.Standard != last.MidRow.Standard ||
		curr.MidRow.FieldOrder != last.MidRow.FieldOrder ||
		curr.MidRow.InterlacedOutput != last.MidRow.InterlacedOutput ||
		curr.Readout.Output != last.Readout.Output ||
		curr.Readout.Aspect != last.Readout.Aspect ||
		curr.Readout.Pipe != last.Readout.Pipe ||
		curr.Readout.Link != last.Readout.Link
}

func (s *Server) logMeterEmitRefused(reason string) {
	if s.meterRefusalLog == nil {
		return
	}
	if s.meterRefusalLog.Allow(time.Now()) {
		slog.Debug("chassis: meter emit refused", "reason", reason)
	}
}

// volumeChanged isolates the OutputVolume diff so the `volume` SSE
// event can fire without piggy-backing on `transport`. Deliberately
// excludes every other TransportData field: a state/seek change that
// leaves volume untouched must not emit a duplicate volume event.
func volumeChanged(a, b TransportData) bool {
	return a.OutputVolume != b.OutputVolume
}

func sourceChanged(prev, next sourceEnvelope) bool {
	if len(prev.Buttons) != len(next.Buttons) {
		return true
	}
	for i := range prev.Buttons {
		if prev.Buttons[i] != next.Buttons[i] {
			return true
		}
	}
	return false
}

// handleEvents serves a long-lived SSE stream at GET /receiver/events.
// Scaffolding only — the diff ticker that emits change events lands
// in the next task. This implementation handles:
//   - 500 when the ResponseWriter cannot flush
//   - SSE response headers
//   - retry: 3000 directive (pins browser reconnect cadence)
//   - initial state + vfd + visualizer + transport snapshot
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

	last := s.cache.Get()
	if err := emit(w, "state", stateEnvelope{State: string(last.State)}); err != nil {
		return
	}
	if err := emit(w, "vfd", vfdEnvelopeFrom(last.VFD)); err != nil {
		return
	}
	lastSource := sourceEnvelopeFromSnapshot(last)
	if err := emit(w, "source", lastSource); err != nil {
		return
	}
	if err := emit(w, "visualizer", vizEnvelope{Mode: last.Visualizer.ActiveMode}); err != nil {
		return
	}
	if err := emit(w, "transport", transportEnvelopeFrom(last.Transport)); err != nil {
		return
	}
	if err := emit(w, "volume", volumeEnvelopeFrom(last.Transport)); err != nil {
		return
	}
	if err := emit(w, "meter", meterEnvelopeFrom(last.Meter)); err != nil {
		return
	}
	flusher.Flush()

	tick := time.NewTicker(chassisTickInterval)
	defer tick.Stop()
	heartbeat := time.NewTicker(chassisHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			curr := s.cache.Get()
			if curr.State != last.State {
				if err := emit(w, "state", stateEnvelope{State: string(curr.State)}); err != nil {
					return
				}
				last.State = curr.State
			}
			if vfdChanged(curr.VFD, last.VFD) {
				if err := emit(w, "vfd", vfdEnvelopeFrom(curr.VFD)); err != nil {
					return
				}
				last.VFD = curr.VFD
			}
			if curr.Visualizer.ActiveMode != last.Visualizer.ActiveMode {
				if err := emit(w, "visualizer", vizEnvelope{Mode: curr.Visualizer.ActiveMode}); err != nil {
					return
				}
				last.Visualizer.ActiveMode = curr.Visualizer.ActiveMode
			}
			currSource := sourceEnvelopeFromSnapshot(curr)
			if sourceChanged(lastSource, currSource) {
				if err := emit(w, "source", currSource); err != nil {
					return
				}
				lastSource = currSource
			}
			// Volume diff must precede transport diff: transportChanged's
			// branch overwrites last.Transport wholesale, which would
			// erase the old OutputVolume before we could compare it.
			// volumeChanged is volume-only, so emitting it here for a
			// pure knob change does not double-fire as a transport event.
			if volumeChanged(curr.Transport, last.Transport) {
				if err := emit(w, "volume", volumeEnvelopeFrom(curr.Transport)); err != nil {
					return
				}
				last.Transport.OutputVolume = curr.Transport.OutputVolume
			}
			if transportChanged(curr.Transport, last.Transport) {
				if err := emit(w, "transport", transportEnvelopeFrom(curr.Transport)); err != nil {
					return
				}
				last.Transport = curr.Transport
			}
			if meterChanged(curr.Meter, last.Meter) {
				if err := emit(w, "meter", meterEnvelopeFrom(curr.Meter)); err != nil {
					return
				}
				last.Meter = curr.Meter
			} else {
				s.logMeterEmitRefused("unchanged")
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// chassisTickInterval is the diff-ticker cadence. Package-level var so
// tests can shorten it from init() before any test runs. Production
// default 250 ms; tests override to 100 ms (still slow enough to be
// reliable on busy CI workers).
var chassisTickInterval = 250 * time.Millisecond

// chassisHeartbeatInterval is the cadence at which the handler emits
// `: heartbeat` SSE comments to defeat reverse-proxy idle timeouts.
// Production default 30s; tests override.
var chassisHeartbeatInterval = 30 * time.Second

// snapshotCache holds the most recent ReceiverPageData read from
// SessionViewer. New seeds it synchronously; Mount starts a single
// background goroutine that refreshes it every chassisTickInterval.
// All connected SSE handlers read via Get (RLock), so Manager.mu
// pressure is decoupled from tab count.
type snapshotCache struct {
	mu   sync.RWMutex
	data ReceiverPageData
}

// Get returns a value copy of the cached snapshot. The copy shares
// slice backing arrays (Sources / Visualizers / History rows) with the
// cached struct, so callers must treat the returned data as read-only.
// The refresher only Sets fresh snapshots produced by snapshotFromSession;
// it never mutates a previously-stored slice in place.
func (c *snapshotCache) Get() ReceiverPageData {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data
}

func (c *snapshotCache) Set(d ReceiverPageData) {
	c.mu.Lock()
	c.data = d
	c.mu.Unlock()
}
