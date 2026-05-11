package dlna

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// seek_test.go covers the P3.2 Seek SOAP handler + parseUPnPDuration.
//
// Reuses captureSessionManager from play_test.go; matches the
// pause_test.go primer pattern (pausePrimedAdapter) for the happy-path
// shape: enabled adapter with PLAYING state, own ref, and Status()
// reporting that ref + a known Duration.

// avtSendSeek invokes handleAVTransportSOAP with a Seek envelope and
// the given args fragment.
func avtSendSeek(t *testing.T, a *Adapter, argsXML string) *httptest.ResponseRecorder {
	t.Helper()
	req, rr := avtSOAPRequest(t, "Seek", argsXML)
	a.handleAVTransportSOAP(rr, req)
	return rr
}

// seekPrimedAdapter constructs an adapter in the PLAYING state with a
// known Duration (1 hour) so Seek's happy path is reachable. Returns
// the adapter, fake, and active ref. The Status() closure can be
// overridden by tests that need a different Duration or AdapterRef.
func seekPrimedAdapter(t *testing.T) (*Adapter, *captureSessionManager, string) {
	t.Helper()
	a, fake := avtPlayAdapter(t)
	rr := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rr.Code != 200 {
		t.Fatalf("priming Play status = %d, want 200", rr.Code)
	}
	a.mu.Lock()
	ref := a.currentRef
	a.mu.Unlock()
	if ref == "" {
		t.Fatal("priming Play left empty currentRef")
	}
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: ref, Duration: 1 * time.Hour}
	}
	return a, fake, ref
}

// ---- handleSeek ----

func TestSeek_BadInstanceID(t *testing.T) {
	a, fake, _ := seekPrimedAdapter(t)
	rr := avtSendSeek(t, a,
		"<InstanceID>1</InstanceID><Unit>REL_TIME</Unit><Target>00:00:30</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>718</errorCode>") {
		t.Errorf("body missing errorCode 718: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times on bad InstanceID; want 0",
			fake.snapshotSeekCalls())
	}
}

func TestSeek_UnsupportedUnit_Returns710(t *testing.T) {
	// Spec line 379: unsupported seek units → 710. ABS_TIME, TRACK_NR,
	// CHAPTER, FRAME etc. all map to 710 — pretending to support them
	// and silently coercing to REL_TIME would fool the controller.
	for _, unit := range []string{"ABS_TIME", "TRACK_NR", "CHAPTER", "FRAME", "REL_COUNT"} {
		t.Run(unit, func(t *testing.T) {
			a, fake, _ := seekPrimedAdapter(t)
			rr := avtSendSeek(t, a,
				"<InstanceID>0</InstanceID><Unit>"+unit+"</Unit><Target>00:00:30</Target>")
			if rr.Code != 500 {
				t.Errorf("status = %d, want 500", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), "<errorCode>710</errorCode>") {
				t.Errorf("body missing errorCode 710: %s", rr.Body.String())
			}
			if fake.snapshotSeekCalls() != 0 {
				t.Errorf("core.SeekTo called %d times on unit=%s; want 0",
					fake.snapshotSeekCalls(), unit)
			}
		})
	}
}

func TestSeek_EmptyUnitDefaultsToRelTime(t *testing.T) {
	// UPnP defines REL_TIME as the default for non-recording renderers.
	// Some controllers omit the Unit element. We accept that and treat
	// it as REL_TIME.
	a, fake, _ := seekPrimedAdapter(t)
	rr := avtSendSeek(t, a, "<InstanceID>0</InstanceID><Target>00:00:30</Target>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 1 {
		t.Errorf("core.SeekTo called %d times, want 1", fake.snapshotSeekCalls())
	}
}

func TestSeek_Disabled_Returns701(t *testing.T) {
	// Disabled adapter must short-circuit without calling core (avoids
	// preempting a foreign session).
	fake := &captureSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.mu.Lock()
	a.currentRef = "dlna:owned"
	a.transportState = transportStatePlaying
	a.mu.Unlock()

	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:30</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times on disabled adapter; want 0",
			fake.snapshotSeekCalls())
	}
}

func TestSeek_NoSession_Returns701(t *testing.T) {
	// currentRef == "" — there's no DLNA session to seek.
	fake := &captureSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)

	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:30</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times on no-session; want 0",
			fake.snapshotSeekCalls())
	}
}

func TestSeek_ForeignSession_Returns701(t *testing.T) {
	// Spec §Common Action Rules line 294: foreign sessions must remain
	// untouched.
	a, fake := avtPlayAdapter(t)
	a.mu.Lock()
	a.currentRef = "dlna:ours"
	a.transportState = transportStatePlaying
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{
			AdapterRef: "plex:/library/metadata/1234",
			Duration:   1 * time.Hour,
		}
	}

	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:30</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times on foreign session; want 0",
			fake.snapshotSeekCalls())
	}
}

func TestSeek_ForeignSessionWithNonSeekableLoadedHLS_Returns701(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	a.mu.Lock()
	a.currentRef = "dlna:ours"
	a.transportState = transportStatePlaying
	a.loadedCanSeek = false
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{
			AdapterRef: "plex:/library/metadata/1234",
			Duration:   1 * time.Hour,
		}
	}

	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:30</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times on foreign HLS session; want 0",
			fake.snapshotSeekCalls())
	}
}

func TestSeek_RefMismatchAtCoreCall_Returns701(t *testing.T) {
	a, fake, ref := seekPrimedAdapter(t)
	fake.seekIfMismatch = true

	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:30</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times after ref mismatch; want 0",
			fake.snapshotSeekCalls())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRef != ref {
		t.Errorf("currentRef = %q after ref mismatch; want %q", a.currentRef, ref)
	}
	if a.transportState != transportStatePlaying {
		t.Errorf("transportState = %q after ref mismatch; want PLAYING",
			a.transportState)
	}
}

func TestSeek_EmptyTarget_Returns711(t *testing.T) {
	a, fake, _ := seekPrimedAdapter(t)
	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target></Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>711</errorCode>") {
		t.Errorf("body missing errorCode 711: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times on empty target; want 0",
			fake.snapshotSeekCalls())
	}
}

func TestSeek_UnparseableTarget_Returns711(t *testing.T) {
	a, fake, _ := seekPrimedAdapter(t)
	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>not a time</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>711</errorCode>") {
		t.Errorf("body missing errorCode 711: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times on unparseable target; want 0",
			fake.snapshotSeekCalls())
	}
}

func TestSeek_NegativeTarget_Returns711(t *testing.T) {
	// Spec line 378: "reject negative targets". The leading '-' check
	// in parseUPnPDuration returns an error, which maps to 711.
	a, fake, _ := seekPrimedAdapter(t)
	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>-00:01:00</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>711</errorCode>") {
		t.Errorf("body missing errorCode 711: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times on negative target; want 0",
			fake.snapshotSeekCalls())
	}
}

func TestSeek_UnknownDuration_Returns711(t *testing.T) {
	// Spec line 378: "reject unknown-duration sources". Live or
	// pre-probe sources have Duration == 0 — Seek must refuse.
	a, fake := avtPlayAdapter(t)
	a.mu.Lock()
	a.currentRef = "dlna:live"
	a.transportState = transportStatePlaying
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: "dlna:live", Duration: 0}
	}

	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:30</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>711</errorCode>") {
		t.Errorf("body missing errorCode 711: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times on unknown-duration; want 0",
			fake.snapshotSeekCalls())
	}
}

func TestSeek_NonSeekableHLS_Returns711EvenWithKnownDuration(t *testing.T) {
	a, fake := avtPlayAdapter(t)
	a.mu.Lock()
	a.currentRef = "dlna:hls"
	a.transportState = transportStatePlaying
	a.loadedCanSeek = false
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: "dlna:hls", Duration: 1 * time.Hour}
	}

	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:30</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>711</errorCode>") {
		t.Errorf("body missing errorCode 711: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times on non-seekable HLS; want 0",
			fake.snapshotSeekCalls())
	}
}

func TestSeek_TargetBeyondDuration_Returns711(t *testing.T) {
	// Spec line 378: "reject targets beyond known duration". Target
	// 2h, Duration 1h → 711.
	a, fake := avtPlayAdapter(t)
	a.mu.Lock()
	a.currentRef = "dlna:vod"
	a.transportState = transportStatePlaying
	a.mu.Unlock()
	fake.statusFn = func() core.SessionStatus {
		return core.SessionStatus{AdapterRef: "dlna:vod", Duration: 1 * time.Hour}
	}

	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>02:00:00</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>711</errorCode>") {
		t.Errorf("body missing errorCode 711: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times on out-of-range target; want 0",
			fake.snapshotSeekCalls())
	}
}

func TestSeek_HugeTargetOverflow_Returns711(t *testing.T) {
	a, fake, _ := seekPrimedAdapter(t)

	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>3000000:00:00</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>711</errorCode>") {
		t.Errorf("body missing errorCode 711: %s", rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 0 {
		t.Errorf("core.SeekTo called %d times on overflowing target; want 0",
			fake.snapshotSeekCalls())
	}
}

func TestSeek_HappyPath_CallsCoreSeekToWithCorrectOffset(t *testing.T) {
	a, fake, _ := seekPrimedAdapter(t)

	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:30</Target>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 1 {
		t.Errorf("core.SeekTo called %d times, want 1", fake.snapshotSeekCalls())
	}
	if got := fake.lastSeekOffset(); got != 30000 {
		t.Errorf("offsetMs = %d, want 30000 (30s)", got)
	}
	// Seek must NOT mutate transportState — PLAYING stays PLAYING.
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.transportState != transportStatePlaying {
		t.Errorf("transportState = %q after Seek; want PLAYING (no transition)",
			a.transportState)
	}
}

func TestSeek_CommaDecimalTarget(t *testing.T) {
	// Locale-affected controllers may emit "00:01:30,500" instead of
	// "00:01:30.500". Both must parse to the same offset (90500ms).
	a, fake, _ := seekPrimedAdapter(t)

	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:01:30,500</Target>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fake.snapshotSeekCalls() != 1 {
		t.Errorf("core.SeekTo called %d times, want 1", fake.snapshotSeekCalls())
	}
	if got := fake.lastSeekOffset(); got != 90500 {
		t.Errorf("offsetMs = %d, want 90500 (1m30.5s)", got)
	}
}

func TestSeek_CoreFailure_Returns501AndRedactsLastError(t *testing.T) {
	a, fake, _ := seekPrimedAdapter(t)
	leakPayload := "/var/lib/groovy/socket: connection refused on 10.0.0.42:32100"
	fake.seekErr = errors.New("dataplane seek: " + leakPayload)

	rr := avtSendSeek(t, a,
		"<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:30</Target>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>501</errorCode>") {
		t.Errorf("body missing errorCode 501: %s", rr.Body.String())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastError == "" {
		t.Error("lastError empty after Seek failure; want descriptive message")
	}
	// Redaction discipline: raw err.Error() must NOT leak.
	if strings.Contains(a.lastError, leakPayload) {
		t.Errorf("lastError = %q; leaked path/host fragment from wrapped error",
			a.lastError)
	}
	if !strings.HasPrefix(a.lastError, "Seek failed") {
		t.Errorf("lastError = %q; want it to start with %q prefix",
			a.lastError, "Seek failed")
	}
	// Seek failure does NOT change transport state.
	if a.transportState != transportStatePlaying {
		t.Errorf("transportState = %q after Seek failure; want PLAYING (no transition)",
			a.transportState)
	}
}

// ---- parseUPnPDuration ----

func TestParseUPnPDuration(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"zero", "00:00:00", 0, false},
		{"30 seconds", "00:00:30", 30 * time.Second, false},
		{"one hour", "01:00:00", 1 * time.Hour, false},
		{"with fractional dot", "00:00:30.500", 30*time.Second + 500*time.Millisecond, false},
		{"with fractional comma", "00:00:30,500", 30*time.Second + 500*time.Millisecond, false},
		{"empty", "", 0, true},
		{"negative", "-00:00:30", 0, true},
		{"missing colons", "30", 0, true},
		{"out-of-range minute", "00:60:00", 0, true},
		{"out-of-range second", "00:00:60", 0, true},
		{"overflowing hours", "3000000:00:00", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUPnPDuration(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseUPnPDuration(%q) = (%v, nil), want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseUPnPDuration(%q) unexpected error: %v", tt.in, err)
				return
			}
			if got != tt.want {
				t.Errorf("parseUPnPDuration(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
