package dlna

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestAVTransportEventSnapshotReflectsAdapterAndCoreState(t *testing.T) {
	const ref = "dlna:test-ref"
	fake := &captureSessionManager{
		statusFn: func() core.SessionStatus {
			return core.SessionStatus{
				AdapterRef: ref,
				Position:   42 * time.Second,
				Duration:   9 * time.Minute,
			}
		},
	}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.mu.Lock()
	a.loadedURI = "http://192.168.1.99/movie.mp4"
	a.loadedMeta = DIDLMetadata{Duration: 5 * time.Minute}
	a.currentRef = ref
	a.transportState = transportStatePlaying
	a.mu.Unlock()

	a.publishAVTransportLastChange()

	got := eventSnapshot(t, a, serviceAVTransport)["LastChange"]
	for _, want := range []string{
		`<TransportState val="PLAYING"></TransportState>`,
		`<TransportStatus val="OK"></TransportStatus>`,
		`<CurrentTrackURI val="http://192.168.1.99/movie.mp4"></CurrentTrackURI>`,
		`<CurrentTrackDuration val="00:09:00"></CurrentTrackDuration>`,
		`<RelativeTimePosition val="00:00:42"></RelativeTimePosition>`,
		`<CurrentTransportActions val="Pause,Stop,Seek"></CurrentTransportActions>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("AVTransport LastChange missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `<CurrentTrackDuration val="00:05:00">`) {
		t.Fatalf("AVTransport LastChange used metadata duration instead of active core duration:\n%s", got)
	}
}

func TestRenderingControlSetVolumePublishesLastChange(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rcSendSetVolume(t, a, 25)

	got := eventSnapshot(t, a, serviceRenderingControl)["LastChange"]
	for _, want := range []string{
		`<Volume channel="Master" val="25"></Volume>`,
		`<Mute channel="Master" val="0"></Mute>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("RenderingControl LastChange missing %q:\n%s", want, got)
		}
	}
}

func TestOnStopPublishesAVTransportStopped(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	fake.statusFn = func() core.SessionStatus {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.currentRef == "" {
			return core.SessionStatus{}
		}
		return core.SessionStatus{
			AdapterRef: a.currentRef,
			Duration:   2 * time.Minute,
			Position:   3 * time.Second,
		}
	}

	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("Play status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	onStop := fake.lastReq().OnStop
	if onStop == nil {
		t.Fatal("captured OnStop nil; cannot exercise callback")
	}

	onStop("stopped")

	got := eventSnapshot(t, a, serviceAVTransport)["LastChange"]
	for _, want := range []string{
		`<TransportState val="STOPPED"></TransportState>`,
		`<TransportStatus val="OK"></TransportStatus>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("AVTransport LastChange missing %q after OnStop:\n%s", want, got)
		}
	}
}

func TestSeekSuccessPublishesUpdatedAVTransportPosition(t *testing.T) {
	const ref = "dlna:seek-ref"
	fake := &captureSessionManager{}
	fake.statusFn = func() core.SessionStatus {
		position := 10 * time.Second
		if fake.snapshotSeekCalls() > 0 {
			position = 25 * time.Second
		}
		return core.SessionStatus{
			AdapterRef: ref,
			Duration:   1 * time.Minute,
			Position:   position,
		}
	}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	a.mu.Lock()
	a.loadedURI = "http://192.168.1.99/movie.mp4"
	a.currentRef = ref
	a.transportState = transportStatePlaying
	a.mu.Unlock()

	a.publishAVTransportLastChange()
	before := eventSnapshot(t, a, serviceAVTransport)["LastChange"]
	if !strings.Contains(before, `<RelativeTimePosition val="00:00:10"></RelativeTimePosition>`) {
		t.Fatalf("initial AVTransport LastChange missing old relative position:\n%s", before)
	}

	rr := avtSendSeek(t, a, "<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:25</Target>")
	if rr.Code != 200 {
		t.Fatalf("Seek status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	got := eventSnapshot(t, a, serviceAVTransport)["LastChange"]
	if !strings.Contains(got, `<RelativeTimePosition val="00:00:25"></RelativeTimePosition>`) {
		t.Fatalf("AVTransport LastChange missing updated seek position:\n%s", got)
	}
}

func TestConnectionManagerEventSnapshotIsSeededAndStatic(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	before := eventSnapshot(t, a, serviceConnectionManager)
	if before["SourceProtocolInfo"] != "" {
		t.Fatalf("SourceProtocolInfo = %q, want empty", before["SourceProtocolInfo"])
	}
	if before["SinkProtocolInfo"] != sinkProtocolInfo {
		t.Fatalf("SinkProtocolInfo = %q, want %q", before["SinkProtocolInfo"], sinkProtocolInfo)
	}
	if before["CurrentConnectionIDs"] != "0" {
		t.Fatalf("CurrentConnectionIDs = %q, want 0", before["CurrentConnectionIDs"])
	}

	a.publishAVTransportLastChange()
	rcSendSetVolume(t, a, 25)

	after := eventSnapshot(t, a, serviceConnectionManager)
	if len(after) != len(before) {
		t.Fatalf("ConnectionManager snapshot size changed: got %d, want %d (%v)", len(after), len(before), after)
	}
	for k, want := range before {
		if after[k] != want {
			t.Fatalf("ConnectionManager snapshot[%s] = %q, want %q", k, after[k], want)
		}
	}
}

func eventSnapshot(t *testing.T, a *Adapter, service eventService) eventProperties {
	t.Helper()
	if a.events == nil {
		t.Fatal("adapter event manager is nil")
	}
	a.events.mu.Lock()
	defer a.events.mu.Unlock()
	return cloneEventProperties(a.events.snapshots[service])
}

func rcSendSetVolume(t *testing.T, a *Adapter, volume int) {
	t.Helper()
	req, rr := rcSOAPRequest("SetVolume",
		fmt.Sprintf("<InstanceID>0</InstanceID><Channel>Master</Channel><DesiredVolume>%d</DesiredVolume>", volume))
	a.handleRenderingControlSOAP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("SetVolume status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}
