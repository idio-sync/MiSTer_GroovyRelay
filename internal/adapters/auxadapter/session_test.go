package aux

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestStartAUXStreamURLBuildsProbeAndPlayURLs(t *testing.T) {
	fc := &sessionCore{}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, Config{
		Enabled: true,
		Input: AUXInput{
			ID: "aux", Name: "Analog In", Mode: ModeStreamURL,
			AudioOutput:     AudioOutputVisualOnly,
			URL:             "http://capture-host:8090/aux.wav",
			ThreadQueueSize: 64, AnalyzeDurationMillis: 100, ProbeSize: 32768,
		},
	})

	ref, err := a.StartAUX(context.Background(), "")
	if err != nil {
		t.Fatalf("StartAUX: %v", err)
	}
	req := fc.lastRequest
	if ref != "aux:aux" || req.AdapterRef != "aux:aux" || req.Source != "aux" {
		t.Fatalf("bad ref/source: ref=%q req=%+v", ref, req)
	}
	if req.Title != "Analog In" || req.Visualizer.Metadata.Title != "Analog In" || req.Visualizer.Metadata.Artist != "AUX" {
		t.Fatalf("bad title/metadata: %+v", req)
	}
	if req.StreamProbeURL == "" || req.StreamURL == "" || req.StreamProbeURL == req.StreamURL {
		t.Fatalf("probe/play URLs not distinct: probe=%q play=%q", req.StreamProbeURL, req.StreamURL)
	}
	assertProxyURL(t, req.StreamProbeURL, proxyTokenProbe)
	assertProxyURL(t, req.StreamURL, proxyTokenPlay)
	if req.MediaInputPolicy.IsZero() {
		t.Fatal("MediaInputPolicy is zero, want loopback policy")
	}
	if req.AudioOutputMode != core.AudioOutputVisualOnly {
		t.Fatalf("AudioOutputMode = %q", req.AudioOutputMode)
	}
	if !req.Visualizer.Enabled || req.MediaKind != core.MediaKindMusic {
		t.Fatalf("not a music visualizer request: %+v", req)
	}
	if req.Capabilities.CanPause || req.Capabilities.CanSeek {
		t.Fatalf("capabilities = %+v, want no pause/seek", req.Capabilities)
	}
	if a.activeRef != "aux:aux" {
		t.Fatalf("activeRef = %q, want aux:aux", a.activeRef)
	}
}

func TestStartAUXLocalCaptureBuildsCaptureRequest(t *testing.T) {
	fc := &sessionCore{}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, Config{
		Enabled: true,
		Input: AUXInput{
			ID: "aux", Name: "Line In", Mode: ModeLocalCapture,
			AudioOutput: AudioOutputMonitor,
			Format:      "alsa", Device: "hw:1,0",
			SampleRate: 48000, Channels: 2,
			ThreadQueueSize: 64, AnalyzeDurationMillis: 100, ProbeSize: 32768,
		},
	})

	_, err := a.StartAUX(context.Background(), "aux")
	if err != nil {
		t.Fatalf("StartAUX: %v", err)
	}
	req := fc.lastRequest
	if req.StreamURL != "" || req.StreamProbeURL != "" {
		t.Fatalf("local capture set stream URLs: %+v", req)
	}
	if !req.AudioCapture.Enabled || req.AudioCapture.Format != "alsa" || req.AudioCapture.Device != "hw:1,0" {
		t.Fatalf("bad capture request: %+v", req.AudioCapture)
	}
	if req.AudioCapture.SampleRate != 48000 || req.AudioCapture.Channels != 2 {
		t.Fatalf("audio shape = %d/%d, want 48000/2", req.AudioCapture.SampleRate, req.AudioCapture.Channels)
	}
	if req.AudioCapture.AnalyzeDuration != 100*time.Millisecond || req.AudioCapture.ProbeSize != 32768 {
		t.Fatalf("probe settings = %s/%d, want 100ms/32768", req.AudioCapture.AnalyzeDuration, req.AudioCapture.ProbeSize)
	}
	if req.AudioOutputMode != core.AudioOutputMonitor {
		t.Fatalf("AudioOutputMode = %q", req.AudioOutputMode)
	}
}

func TestStartAUXUnavailableErrorsWrapSharedSentinel(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		inputID string
	}{
		{name: "disabled", cfg: Config{Enabled: false, Input: DefaultConfig().Input}, inputID: "aux"},
		{name: "no input", cfg: Config{Enabled: true}, inputID: "aux"},
		{name: "input mismatch", cfg: validStreamConfig(), inputID: "other"},
		{name: "unsupported mode", cfg: Config{Enabled: true, Input: AUXInput{ID: "aux", Name: "AUX", Mode: "bogus"}}, inputID: "aux"},
		{name: "validation failure", cfg: Config{Enabled: true, Input: AUXInput{ID: "aux", Name: "AUX", Mode: ModeStreamURL}}, inputID: "aux"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := &sessionCore{}
			a := newTestAdapterWithCore(t, fc)
			a.mustApplyConfig(t, tt.cfg)

			_, err := a.StartAUX(context.Background(), tt.inputID)

			if !errors.Is(err, adapters.ErrSourceUnavailable) {
				t.Fatalf("StartAUX error = %v, want ErrSourceUnavailable", err)
			}
			if fc.starts != 0 {
				t.Fatalf("StartSession calls = %d, want 0", fc.starts)
			}
			if a.Status().LastError == "" {
				t.Fatal("Status.LastError is empty")
			}
		})
	}
}

func TestStopAUXDoesNotStopForeignSession(t *testing.T) {
	fc := &sessionCore{
		status:      core.SessionStatus{AdapterRef: "plex:/library/metadata/42"},
		stopMatched: false,
	}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, DefaultConfig())
	a.activeRef = "aux:aux"

	matched, err := a.StopAUX(context.Background(), "")

	if err != nil {
		t.Fatalf("StopAUX: %v", err)
	}
	if matched {
		t.Fatal("StopAUX matched foreign session")
	}
	if fc.stopCalls != 1 || fc.stopRef != "aux:aux" {
		t.Fatalf("StopIfAdapterRef call = %d/%q, want 1/aux:aux", fc.stopCalls, fc.stopRef)
	}
	if fc.status.AdapterRef != "plex:/library/metadata/42" {
		t.Fatalf("foreign status was stopped: %+v", fc.status)
	}
	if a.activeRef != "" {
		t.Fatalf("activeRef = %q, want stale local ref cleared", a.activeRef)
	}
}

func TestStartAUXImmediateOnStopClearsLocalState(t *testing.T) {
	fc := &sessionCore{
		onStart: func(req core.SessionRequest) {
			req.OnStop("startup ended")
		},
	}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())

	ref, err := a.StartAUX(context.Background(), "aux")

	if err != nil {
		t.Fatalf("StartAUX: %v", err)
	}
	if ref != "aux:aux" {
		t.Fatalf("ref = %q, want aux:aux", ref)
	}
	if a.activeRef != "" {
		t.Fatalf("activeRef = %q, want cleared by immediate OnStop", a.activeRef)
	}
	st := a.AUXStatus(context.Background())
	if st.Active || st.AdapterRef != "" {
		t.Fatalf("AUXStatus after immediate OnStop = %+v, want inactive", st)
	}
}

func TestStopAUXSuccessfulOwnedStopClearsLocalState(t *testing.T) {
	fc := &sessionCore{stopMatched: true}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	if _, err := a.StartAUX(context.Background(), "aux"); err != nil {
		t.Fatalf("StartAUX: %v", err)
	}

	matched, err := a.StopAUX(context.Background(), "aux")

	if err != nil {
		t.Fatalf("StopAUX: %v", err)
	}
	if !matched {
		t.Fatal("StopAUX matched = false, want true")
	}
	if fc.stopCalls != 1 || fc.stopRef != "aux:aux" {
		t.Fatalf("StopIfAdapterRef call = %d/%q, want 1/aux:aux", fc.stopCalls, fc.stopRef)
	}
	if a.activeRef != "" {
		t.Fatalf("activeRef = %q, want cleared", a.activeRef)
	}
	st := a.AUXStatus(context.Background())
	if st.Active || st.AdapterRef != "" {
		t.Fatalf("AUXStatus after StopAUX = %+v, want inactive", st)
	}
}

func TestStopAUXClearsStaleLocalRefOnCoreMismatch(t *testing.T) {
	fc := &sessionCore{
		status:      core.SessionStatus{AdapterRef: "streams:active"},
		stopMatched: false,
	}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	a.activeRef = "aux:aux"
	a.state = adapters.StateRunning

	matched, err := a.StopAUX(context.Background(), "aux")

	if err != nil {
		t.Fatalf("StopAUX: %v", err)
	}
	if matched {
		t.Fatal("StopAUX matched = true, want false")
	}
	if fc.stopCalls != 1 || fc.stopRef != "aux:aux" {
		t.Fatalf("StopIfAdapterRef call = %d/%q, want 1/aux:aux", fc.stopCalls, fc.stopRef)
	}
	if fc.status.AdapterRef != "streams:active" {
		t.Fatalf("foreign status was stopped: %+v", fc.status)
	}
	if a.activeRef != "" {
		t.Fatalf("activeRef = %q, want stale local ref cleared", a.activeRef)
	}
}

func TestAUXStatusCoreMismatchReportsInactiveWithoutStaleRef(t *testing.T) {
	fc := &sessionCore{status: core.SessionStatus{}}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	a.activeRef = "aux:aux"
	a.state = adapters.StateRunning

	st := a.AUXStatus(context.Background())

	if st.Active || st.AdapterRef != "" {
		t.Fatalf("AUXStatus on core mismatch = %+v, want inactive with empty AdapterRef", st)
	}
	if a.activeRef != "aux:aux" {
		t.Fatalf("activeRef = %q, want AUXStatus to avoid mutating local state", a.activeRef)
	}
}

func TestStartAUXMissingCoreReturnsUnavailable(t *testing.T) {
	a := &Adapter{
		cfg:       validStreamConfig(),
		httpPort:  32500,
		proxyHTTP: newProxyHTTPClient(),
	}

	_, err := a.StartAUX(context.Background(), "aux")

	if !errors.Is(err, adapters.ErrSourceUnavailable) {
		t.Fatalf("StartAUX error = %v, want ErrSourceUnavailable", err)
	}
}

func TestStopAUXMissingCoreClearsLocalState(t *testing.T) {
	a := &Adapter{activeRef: "aux:aux", state: adapters.StateRunning}

	matched, err := a.StopAUX(context.Background(), "aux")

	if err != nil {
		t.Fatalf("StopAUX: %v", err)
	}
	if matched {
		t.Fatal("StopAUX matched = true, want false")
	}
	if a.activeRef != "" {
		t.Fatalf("activeRef = %q, want cleared", a.activeRef)
	}
}

func TestStopAUXInputMismatchDoesNotStopActiveRef(t *testing.T) {
	fc := &sessionCore{stopMatched: true}
	a := newTestAdapterWithCore(t, fc)
	a.activeRef = "aux:aux"

	matched, err := a.StopAUX(context.Background(), "other")

	if err != nil {
		t.Fatalf("StopAUX: %v", err)
	}
	if matched || fc.stopCalls != 0 {
		t.Fatalf("foreign stop matched=%v calls=%d", matched, fc.stopCalls)
	}
	if a.activeRef != "aux:aux" {
		t.Fatalf("activeRef = %q, want preserved on input mismatch", a.activeRef)
	}
}

func TestStartAUXSameActiveInputIsIdempotent(t *testing.T) {
	fc := &sessionCore{}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())

	ref, err := a.StartAUX(context.Background(), "aux")
	if err != nil {
		t.Fatalf("first StartAUX: %v", err)
	}
	if ref != "aux:aux" {
		t.Fatalf("first StartAUX ref = %q, want aux:aux", ref)
	}
	if got := len(a.proxy.tokens); got != 2 {
		t.Fatalf("proxy tokens after first StartAUX = %d, want 2", got)
	}
	originalTokens := tokenNames(a.proxy.tokens)
	originalProbe := fc.lastRequest.StreamProbeURL
	originalPlay := fc.lastRequest.StreamURL

	ref, err = a.StartAUX(context.Background(), "aux")
	if err != nil {
		t.Fatalf("second StartAUX: %v", err)
	}
	if ref != "aux:aux" {
		t.Fatalf("second StartAUX ref = %q, want aux:aux", ref)
	}

	if got := len(a.proxy.tokens); got != 2 {
		t.Fatalf("proxy tokens after idempotent StartAUX = %d, want original 2 tokens", got)
	}
	if got := tokenNames(a.proxy.tokens); !sameStrings(got, originalTokens) {
		t.Fatalf("proxy tokens after idempotent StartAUX = %#v, want original tokens %#v", got, originalTokens)
	}
	if fc.lastRequest.StreamProbeURL != originalProbe || fc.lastRequest.StreamURL != originalPlay {
		t.Fatalf("last request proxy URLs changed: probe=%q play=%q, want %q/%q", fc.lastRequest.StreamProbeURL, fc.lastRequest.StreamURL, originalProbe, originalPlay)
	}
	if fc.starts != 1 {
		t.Fatalf("StartSession calls = %d, want 1", fc.starts)
	}
}

func TestStartAUXSameActiveChecksCoreStatusOutsideAdapterLock(t *testing.T) {
	fc := &sessionCore{status: core.SessionStatus{AdapterRef: "aux:aux"}}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	a.activeRef = "aux:aux"

	statusCalled := false
	statusSawAdapterLocked := false
	fc.onStatus = func() {
		statusCalled = true
		if !a.mu.TryLock() {
			statusSawAdapterLocked = true
			return
		}
		a.mu.Unlock()
	}

	ref, err := a.StartAUX(context.Background(), "aux")

	if err != nil {
		t.Fatalf("StartAUX: %v", err)
	}
	if ref != "aux:aux" {
		t.Fatalf("StartAUX ref = %q, want aux:aux", ref)
	}
	if !statusCalled {
		t.Fatal("core Status was not called for same-active idempotence check")
	}
	if statusSawAdapterLocked {
		t.Fatal("core Status was called while adapter mutex was held")
	}
	if fc.starts != 0 {
		t.Fatalf("StartSession calls = %d, want 0", fc.starts)
	}
}

func TestStartAUXStartSessionErrorReleasesProxyTokensAndDoesNotWrapUnavailable(t *testing.T) {
	wantErr := errors.New("core start failed")
	fc := &sessionCore{startErr: wantErr}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())

	_, err := a.StartAUX(context.Background(), "aux")

	if !errors.Is(err, wantErr) {
		t.Fatalf("StartAUX error = %v, want core error", err)
	}
	if errors.Is(err, adapters.ErrSourceUnavailable) {
		t.Fatalf("StartAUX wrapped ErrSourceUnavailable for runtime error: %v", err)
	}
	if got := len(a.proxy.tokens); got != 0 {
		t.Fatalf("proxy tokens after failed StartSession = %d, want 0", got)
	}
	if a.activeRef != "" {
		t.Fatalf("activeRef = %q, want empty after failed start", a.activeRef)
	}
}

func TestStartAUXSameRefStaleOnStopAfterClearDoesNotClearNewSession(t *testing.T) {
	fc := &sessionCore{}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	if _, err := a.StartAUX(context.Background(), "aux"); err != nil {
		t.Fatalf("first StartAUX: %v", err)
	}
	oldOnStop := fc.lastRequest.OnStop
	oldGen := a.activeGen

	oldOnStop("old session ended")
	if a.activeRef != "" {
		t.Fatalf("activeRef after first OnStop = %q, want cleared", a.activeRef)
	}

	if _, err := a.StartAUX(context.Background(), "aux"); err != nil {
		t.Fatalf("second StartAUX: %v", err)
	}
	if got := a.activeGen; got == oldGen {
		t.Fatalf("activeGen after second StartAUX = %d, want generation newer than stale %d", got, oldGen)
	}

	oldOnStop("late stale callback")

	if got := a.activeRef; got != "aux:aux" {
		t.Fatalf("activeRef after stale same-ref OnStop = %q, want aux:aux", got)
	}
	st := a.AUXStatus(context.Background())
	if !st.Active || st.AdapterRef != "aux:aux" {
		t.Fatalf("AUXStatus after stale same-ref OnStop = %+v, want active aux:aux", st)
	}
}

func TestStartAUXStartSessionErrorWithStaleSameRefAndIdleCoreLeavesInactive(t *testing.T) {
	wantErr := errors.New("core start failed")
	fc := &sessionCore{startErr: wantErr}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	a.activeRef = "aux:aux"
	a.activeGen = 7
	a.state = adapters.StateRunning
	a.stateSince = a.auxNow()

	_, err := a.StartAUX(context.Background(), "aux")

	if !errors.Is(err, wantErr) {
		t.Fatalf("StartAUX error = %v, want core error", err)
	}
	if errors.Is(err, adapters.ErrSourceUnavailable) {
		t.Fatalf("StartAUX wrapped ErrSourceUnavailable for runtime error: %v", err)
	}
	if fc.starts != 1 {
		t.Fatalf("StartSession calls = %d, want 1", fc.starts)
	}
	if got := len(a.proxy.tokens); got != 0 {
		t.Fatalf("proxy tokens after failed stale same-ref start = %d, want 0", got)
	}
	if got := a.activeRef; got != "" {
		t.Fatalf("activeRef after failed stale same-ref start = %q, want empty", got)
	}
	status := a.Status()
	if status.State != adapters.StateError {
		t.Fatalf("Status.State = %v, want error", status.State)
	}
	if status.LastError == "" || !strings.Contains(status.LastError, wantErr.Error()) {
		t.Fatalf("Status.LastError = %q, want runtime error %q", status.LastError, wantErr.Error())
	}
}

func TestDifferentRefReplacementStartAUXStartSessionErrorPreservesPreviousActiveSession(t *testing.T) {
	wantErr := errors.New("core start failed before preempt")
	fc := &sessionCore{}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	if _, err := a.StartAUX(context.Background(), "aux"); err != nil {
		t.Fatalf("first StartAUX: %v", err)
	}
	oldTokens := tokenNames(a.proxy.tokens)
	oldGen := a.activeGen
	if got := a.activeRef; got != "aux:aux" {
		t.Fatalf("activeRef after first StartAUX = %q, want aux:aux", got)
	}
	if got := fc.Status().AdapterRef; got != "aux:aux" {
		t.Fatalf("core status before failed replacement = %q, want aux:aux", got)
	}

	a.mustApplyConfig(t, Config{
		Enabled: true,
		Input: AUXInput{
			ID: "aux2", Name: "Second AUX", Mode: ModeStreamURL,
			AudioOutput: AudioOutputVisualOnly,
			URL:         "http://capture-host:8090/aux2.wav",
		},
	})
	fc.startErr = wantErr
	_, err := a.StartAUX(context.Background(), "aux2")

	if !errors.Is(err, wantErr) {
		t.Fatalf("StartAUX error = %v, want core error", err)
	}
	if errors.Is(err, adapters.ErrSourceUnavailable) {
		t.Fatalf("StartAUX wrapped ErrSourceUnavailable for runtime error: %v", err)
	}
	if got := a.activeRef; got != "aux:aux" {
		t.Fatalf("activeRef after failed replacement = %q, want aux:aux", got)
	}
	if got := a.activeGen; got != oldGen {
		t.Fatalf("activeGen after failed replacement = %d, want restored %d", got, oldGen)
	}
	st := a.AUXStatus(context.Background())
	if !st.Active || st.AdapterRef != "aux:aux" {
		t.Fatalf("AUXStatus after failed replacement = %+v, want active aux:aux", st)
	}
	if got := tokenNames(a.proxy.tokens); !sameStrings(got, oldTokens) {
		t.Fatalf("proxy tokens after failed replacement = %#v, want original tokens %#v", got, oldTokens)
	}
	status := a.Status()
	if status.LastError == "" || !strings.Contains(status.LastError, wantErr.Error()) {
		t.Fatalf("Status.LastError = %q, want failed replacement error %q", status.LastError, wantErr.Error())
	}
	if st.ErrorMessage == "" || !strings.Contains(st.ErrorMessage, wantErr.Error()) {
		t.Fatalf("AUXStatus.ErrorMessage = %q, want failed replacement error %q", st.ErrorMessage, wantErr.Error())
	}
}

func TestDifferentRefReplacementStartAUXStartSessionErrorWithCoreMismatchLeavesInactive(t *testing.T) {
	wantErr := errors.New("core start failed after core mismatch")
	fc := &sessionCore{}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	if _, err := a.StartAUX(context.Background(), "aux"); err != nil {
		t.Fatalf("first StartAUX: %v", err)
	}
	oldTokens := tokenNames(a.proxy.tokens)
	oldGen := a.activeGen
	fc.status = core.SessionStatus{}

	a.mustApplyConfig(t, Config{
		Enabled: true,
		Input: AUXInput{
			ID: "aux2", Name: "Second AUX", Mode: ModeStreamURL,
			AudioOutput: AudioOutputVisualOnly,
			URL:         "http://capture-host:8090/aux2.wav",
		},
	})
	fc.startErr = wantErr
	_, err := a.StartAUX(context.Background(), "aux2")

	if !errors.Is(err, wantErr) {
		t.Fatalf("StartAUX error = %v, want core error", err)
	}
	if errors.Is(err, adapters.ErrSourceUnavailable) {
		t.Fatalf("StartAUX wrapped ErrSourceUnavailable for runtime error: %v", err)
	}
	if got := a.activeRef; got != "" {
		t.Fatalf("activeRef after failed replacement with core mismatch = %q, want empty", got)
	}
	if got := a.activeGen; got != 0 {
		t.Fatalf("activeGen after failed replacement with core mismatch = %d, want 0", got)
	}
	st := a.AUXStatus(context.Background())
	if st.Active || st.AdapterRef != "" {
		t.Fatalf("AUXStatus after failed replacement with core mismatch = %+v, want inactive", st)
	}
	status := a.Status()
	if status.State != adapters.StateError {
		t.Fatalf("Status.State = %v, want error", status.State)
	}
	if status.LastError == "" || !strings.Contains(status.LastError, wantErr.Error()) {
		t.Fatalf("Status.LastError = %q, want runtime error %q", status.LastError, wantErr.Error())
	}
	if got := len(a.proxy.tokens); got != 0 {
		t.Fatalf("proxy tokens after failed replacement with core mismatch = %d, want 0", got)
	}
	if oldGen == 0 {
		t.Fatal("oldGen = 0, want non-zero setup generation")
	}
	if len(oldTokens) != 2 {
		t.Fatalf("oldTokens = %#v, want two setup tokens", oldTokens)
	}
}

func TestDifferentRefReplacementStartAUXStartSessionErrorAfterPreemptLeavesInactive(t *testing.T) {
	wantErr := errors.New("core start failed after preempt")
	fc := &sessionCore{}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	if _, err := a.StartAUX(context.Background(), "aux"); err != nil {
		t.Fatalf("first StartAUX: %v", err)
	}
	oldOnStop := fc.lastRequest.OnStop
	oldTokens := tokenNames(a.proxy.tokens)
	if len(oldTokens) != 2 {
		t.Fatalf("tokens after first StartAUX = %#v, want 2 tokens", oldTokens)
	}

	a.mustApplyConfig(t, Config{
		Enabled: true,
		Input: AUXInput{
			ID: "aux2", Name: "Second AUX", Mode: ModeStreamURL,
			AudioOutput: AudioOutputVisualOnly,
			URL:         "http://capture-host:8090/aux2.wav",
		},
	})
	fc.onStart = func(req core.SessionRequest) {
		if fc.starts == 2 {
			oldOnStop("preempted")
		}
	}
	fc.startErr = wantErr
	_, err := a.StartAUX(context.Background(), "aux2")

	if !errors.Is(err, wantErr) {
		t.Fatalf("StartAUX error = %v, want core error", err)
	}
	if errors.Is(err, adapters.ErrSourceUnavailable) {
		t.Fatalf("StartAUX wrapped ErrSourceUnavailable for runtime error: %v", err)
	}
	if got := len(a.proxy.tokens); got != 0 {
		t.Fatalf("proxy tokens after failed preempting replacement = %d, want 0", got)
	}
	if got := a.activeRef; got != "" {
		t.Fatalf("activeRef after failed preempting replacement = %q, want empty", got)
	}
	st := a.AUXStatus(context.Background())
	if st.Active || st.AdapterRef != "" {
		t.Fatalf("AUXStatus after failed preempting replacement = %+v, want inactive", st)
	}
	if st.ErrorMessage == "" || !strings.Contains(st.ErrorMessage, wantErr.Error()) {
		t.Fatalf("AUXStatus.ErrorMessage = %q, want runtime error %q", st.ErrorMessage, wantErr.Error())
	}
	status := a.Status()
	if status.State != adapters.StateError {
		t.Fatalf("Status.State = %v, want error", status.State)
	}
	if status.LastError == "" || !strings.Contains(status.LastError, wantErr.Error()) {
		t.Fatalf("Status.LastError = %q, want runtime error %q", status.LastError, wantErr.Error())
	}
}

func TestConcurrentSameRefStartAUXSerializesStartSession(t *testing.T) {
	fc := newBlockingStartCore()
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	type startResult struct {
		ref string
		err error
	}
	resultCh := make(chan startResult, 2)

	go func() {
		ref, err := a.StartAUX(context.Background(), "aux")
		resultCh <- startResult{ref: ref, err: err}
	}()
	first := fc.waitForStart(t, 1)

	attempted := make(chan struct{})
	go func() {
		close(attempted)
		ref, err := a.StartAUX(context.Background(), "aux")
		resultCh <- startResult{ref: ref, err: err}
	}()
	<-attempted
	fc.assertNoStart(t, 2, 50*time.Millisecond)

	first.release()
	firstResult := <-resultCh
	if firstResult.err != nil {
		t.Fatalf("first StartAUX: %v", firstResult.err)
	}
	if firstResult.ref != "aux:aux" {
		t.Fatalf("first StartAUX ref = %q, want aux:aux", firstResult.ref)
	}
	select {
	case call := <-fc.started:
		call.release()
		t.Fatalf("second StartAUX entered StartSession call %d, want idempotent return", call.n)
	case secondResult := <-resultCh:
		if secondResult.err != nil {
			t.Fatalf("second StartAUX: %v", secondResult.err)
		}
		if secondResult.ref != "aux:aux" {
			t.Fatalf("second StartAUX ref = %q, want aux:aux", secondResult.ref)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second StartAUX")
	}

	if got := len(a.proxy.tokens); got != 2 {
		t.Fatalf("proxy tokens after serialized starts = %d, want original session tokens only", got)
	}
	if got := fc.startCallCount(); got != 1 {
		t.Fatalf("StartSession calls = %d, want 1", got)
	}
}

func TestStopAUXWaitsForInFlightSameRefStartAUX(t *testing.T) {
	fc := newBlockingStartCore()
	fc.stopMatched = true
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	errCh := make(chan error, 1)
	stopCh := make(chan error, 1)

	go func() {
		_, err := a.StartAUX(context.Background(), "aux")
		errCh <- err
	}()
	first := fc.waitForStart(t, 1)

	attempted := make(chan struct{})
	go func() {
		close(attempted)
		matched, err := a.StopAUX(context.Background(), "aux")
		if err != nil {
			stopCh <- err
			return
		}
		if !matched {
			stopCh <- errors.New("StopAUX matched = false, want true")
			return
		}
		stopCh <- nil
	}()
	<-attempted
	fc.assertNoStop(t, 50*time.Millisecond)

	first.release()
	if err := <-errCh; err != nil {
		t.Fatalf("StartAUX: %v", err)
	}
	if err := <-stopCh; err != nil {
		t.Fatalf("StopAUX: %v", err)
	}
	if fc.stopRef != "aux:aux" {
		t.Fatalf("StopIfAdapterRef ref = %q, want aux:aux", fc.stopRef)
	}
}

func TestOnStopReleasesProxyTokensAndClearsActiveAUXState(t *testing.T) {
	fc := &sessionCore{}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	ref, err := a.StartAUX(context.Background(), "aux")
	if err != nil {
		t.Fatalf("StartAUX: %v", err)
	}
	if got := len(a.proxy.tokens); got != 2 {
		t.Fatalf("proxy tokens after start = %d, want 2", got)
	}

	fc.lastRequest.OnStop("eof")

	if got := len(a.proxy.tokens); got != 0 {
		t.Fatalf("proxy tokens after OnStop = %d, want 0", got)
	}
	if a.activeRef != "" {
		t.Fatalf("activeRef = %q, want cleared", a.activeRef)
	}
	st := a.AUXStatus(context.Background())
	if st.Active || st.AdapterRef != "" {
		t.Fatalf("AUXStatus after OnStop = %+v, want inactive", st)
	}
	if ref != "aux:aux" {
		t.Fatalf("ref = %q, want aux:aux", ref)
	}
}

func TestStaleOnStopDoesNotClearNewerActiveAUXRef(t *testing.T) {
	fc := &sessionCore{}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	if _, err := a.StartAUX(context.Background(), "aux"); err != nil {
		t.Fatalf("first StartAUX: %v", err)
	}
	oldOnStop := fc.lastRequest.OnStop
	a.mustApplyConfig(t, Config{
		Enabled: true,
		Input: AUXInput{
			ID: "aux2", Name: "Second AUX", Mode: ModeStreamURL,
			AudioOutput: AudioOutputVisualOnly,
			URL:         "http://capture-host:8090/aux2.wav",
		},
	})
	if _, err := a.StartAUX(context.Background(), "aux2"); err != nil {
		t.Fatalf("second StartAUX: %v", err)
	}

	oldOnStop("preempted")

	if a.activeRef != "aux:aux2" {
		t.Fatalf("activeRef after stale OnStop = %q, want aux:aux2", a.activeRef)
	}
}

func TestClearActiveSessionIgnoresStaleSameRefGeneration(t *testing.T) {
	fc := &sessionCore{}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, validStreamConfig())
	if _, err := a.StartAUX(context.Background(), "aux"); err != nil {
		t.Fatalf("StartAUX: %v", err)
	}
	staleGen := a.activeGen
	a.activeGen++

	if cleanup := a.clearActiveSession("aux:aux", staleGen); cleanup != nil {
		t.Fatal("clearActiveSession returned cleanup for stale generation")
	}

	if a.activeRef != "aux:aux" {
		t.Fatalf("activeRef after stale clear = %q, want aux:aux", a.activeRef)
	}
	st := a.AUXStatus(context.Background())
	if !st.Active || st.AdapterRef != "aux:aux" {
		t.Fatalf("AUXStatus after stale clear = %+v, want active aux:aux", st)
	}
}

func TestStartAUXLocalCaptureDefaultsSampleRateAndChannelsFromBridge(t *testing.T) {
	fc := &sessionCore{}
	a := newTestAdapterWithCore(t, fc)
	a.mustApplyConfig(t, Config{
		Enabled: true,
		Input: AUXInput{
			ID: "aux", Name: "Line In", Mode: ModeLocalCapture,
			AudioOutput: AudioOutputMonitor,
			Format:      "alsa", Device: "hw:1,0",
		},
	})

	if _, err := a.StartAUX(context.Background(), "aux"); err != nil {
		t.Fatalf("StartAUX: %v", err)
	}

	if got := fc.lastRequest.AudioCapture.SampleRate; got != 44100 {
		t.Fatalf("SampleRate = %d, want bridge default 44100", got)
	}
	if got := fc.lastRequest.AudioCapture.Channels; got != 1 {
		t.Fatalf("Channels = %d, want bridge default 1", got)
	}
}

func validStreamConfig() Config {
	return Config{
		Enabled: true,
		Input: AUXInput{
			ID: "aux", Name: "AUX", Mode: ModeStreamURL,
			AudioOutput:     AudioOutputVisualOnly,
			URL:             "http://capture-host:8090/aux.wav",
			ThreadQueueSize: 64, AnalyzeDurationMillis: 100, ProbeSize: 32768,
		},
	}
}

func (a *Adapter) mustApplyConfig(t *testing.T, cfg Config) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg = cfg
	a.enableErr = nil
	a.lastErr = ""
}

func assertProxyURL(t *testing.T, raw string, kind proxyTokenKind) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse proxy URL %q: %v", raw, err)
	}
	if u.Scheme != "http" || u.Host != "127.0.0.1:32500" || u.Path != "/internal/aux-proxy/" {
		t.Fatalf("proxy URL = %q, want loopback aux proxy", raw)
	}
	if got := u.Query().Get("kind"); got != string(kind) {
		t.Fatalf("proxy kind = %q, want %q", got, kind)
	}
	if strings.TrimSpace(u.Query().Get("aux_token")) == "" {
		t.Fatalf("proxy URL %q has empty token", raw)
	}
}

type sessionCore struct {
	starts      int
	startErr    error
	onStart     func(core.SessionRequest)
	lastRequest core.SessionRequest
	stopCalls   int
	stopRef     string
	stopErr     error
	stopMatched bool
	status      core.SessionStatus
	onStatus    func()
}

func (f *sessionCore) StartSession(req core.SessionRequest) error {
	f.starts++
	f.lastRequest = req
	if f.onStart != nil {
		f.onStart(req)
	}
	if f.startErr != nil {
		return f.startErr
	}
	f.status = core.SessionStatus{AdapterRef: req.AdapterRef}
	return nil
}

func (f *sessionCore) StopIfAdapterRef(ref string) (bool, error) {
	f.stopCalls++
	f.stopRef = ref
	if f.stopMatched {
		f.status = core.SessionStatus{}
	}
	return f.stopMatched, f.stopErr
}

func (f *sessionCore) Status() core.SessionStatus {
	if f.onStatus != nil {
		f.onStatus()
	}
	return f.status
}

type blockingStartCore struct {
	mu          sync.Mutex
	startCalls  int
	stopCalls   int
	stopRef     string
	stopMatched bool
	status      core.SessionStatus
	started     chan blockingStartCall
	stopped     chan struct{}
}

type blockingStartCall struct {
	n       int
	req     core.SessionRequest
	release func()
}

func newBlockingStartCore() *blockingStartCore {
	return &blockingStartCore{
		started: make(chan blockingStartCall, 4),
		stopped: make(chan struct{}, 4),
	}
}

func (f *blockingStartCore) StartSession(req core.SessionRequest) error {
	release := make(chan struct{})
	f.mu.Lock()
	f.startCalls++
	n := f.startCalls
	f.mu.Unlock()

	f.started <- blockingStartCall{
		n:   n,
		req: req,
		release: func() {
			close(release)
		},
	}
	<-release

	f.mu.Lock()
	f.status = core.SessionStatus{AdapterRef: req.AdapterRef}
	f.mu.Unlock()
	return nil
}

func (f *blockingStartCore) StopIfAdapterRef(ref string) (bool, error) {
	f.mu.Lock()
	f.stopCalls++
	f.stopRef = ref
	matched := f.stopMatched
	if matched {
		f.status = core.SessionStatus{}
	}
	f.mu.Unlock()
	f.stopped <- struct{}{}
	return matched, nil
}

func (f *blockingStartCore) Status() core.SessionStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *blockingStartCore) startCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startCalls
}

func (f *blockingStartCore) waitForStart(t *testing.T, want int) blockingStartCall {
	t.Helper()
	select {
	case call := <-f.started:
		if call.n != want {
			t.Fatalf("StartSession call = %d, want %d", call.n, want)
		}
		return call
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for StartSession call %d", want)
		return blockingStartCall{}
	}
}

func (f *blockingStartCore) assertNoStart(t *testing.T, want int, d time.Duration) {
	t.Helper()
	select {
	case call := <-f.started:
		t.Fatalf("StartSession call %d entered before call %d was released", call.n, want-1)
	case <-time.After(d):
	}
}

func (f *blockingStartCore) assertNoStop(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-f.stopped:
		t.Fatal("StopIfAdapterRef entered before StartSession returned")
	case <-time.After(d):
	}
}
