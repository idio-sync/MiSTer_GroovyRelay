package aux

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

const auxSourceName = "aux"

func (a *Adapter) AUXStatus(ctx context.Context) adapters.AUXStatus {
	_ = ctx

	a.mu.Lock()
	cfg := a.normalizeConfig(a.cfg)
	activeRef := a.activeRef
	lastErr := a.lastErr
	a.mu.Unlock()

	configured := false
	configCheck := cfg
	configCheck.Enabled = true
	if err := configCheck.Validate(); err == nil {
		configured = true
	}

	status := adapters.AUXStatus{
		Enabled:      cfg.Enabled,
		Configured:   configured,
		Active:       activeRef != "",
		InputID:      cfg.Input.ID,
		DisplayName:  cfg.Input.Name,
		AdapterRef:   activeRef,
		ErrorMessage: lastErr,
	}
	if activeRef != "" && a.core != nil {
		coreStatus := a.core.Status()
		if coreStatus.AdapterRef != activeRef {
			status.Active = false
		}
	}
	return status
}

func (a *Adapter) StartAUX(ctx context.Context, inputID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	a.mu.Lock()
	cfg := a.normalizeConfig(a.cfg)
	if !cfg.Enabled {
		a.mu.Unlock()
		return "", a.recordAUXStartError(unavailableError("AUX is disabled"))
	}
	if requested := strings.TrimSpace(inputID); requested != "" && requested != cfg.Input.ID {
		a.mu.Unlock()
		return "", a.recordAUXStartError(unavailableError("AUX input %q is not configured", requested))
	}
	if err := cfg.Validate(); err != nil {
		a.mu.Unlock()
		return "", a.recordAUXStartError(unavailableError("AUX config validation failed: %v", err))
	}
	coreManager := a.core
	if coreManager == nil {
		a.mu.Unlock()
		return "", a.recordAUXStartError(unavailableError("AUX core is unavailable"))
	}
	req, cleanup, err := a.buildSessionRequestLocked(ctx, cfg.Input)
	if err != nil {
		a.mu.Unlock()
		return "", a.recordAUXStartError(err)
	}
	a.activeRef = req.AdapterRef
	a.state = adapters.StateRunning
	a.lastErr = ""
	a.stateSince = a.auxNow()
	a.mu.Unlock()

	if err := coreManager.StartSession(req); err != nil {
		cleanup()
		wrapped := fmt.Errorf("start AUX session: %w", err)
		a.rollbackAUXStart(req.AdapterRef, wrapped)
		return "", wrapped
	}

	return req.AdapterRef, nil
}

func (a *Adapter) StopAUX(ctx context.Context, inputID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	a.mu.Lock()
	activeRef := a.activeRef
	a.mu.Unlock()
	if activeRef == "" {
		return false, nil
	}
	if requested := strings.TrimSpace(inputID); requested != "" && activeRef != "aux:"+requested {
		return false, nil
	}
	coreManager := a.core
	if coreManager == nil {
		a.clearActiveRef(activeRef)
		return false, nil
	}

	matched, err := coreManager.StopIfAdapterRef(activeRef)
	if !matched {
		a.clearActiveRef(activeRef)
		return false, err
	}
	if err != nil {
		return true, err
	}
	a.clearActiveRef(activeRef)
	return true, nil
}

func (a *Adapter) buildSessionRequestLocked(ctx context.Context, input AUXInput) (core.SessionRequest, func(), error) {
	_ = ctx

	ref := "aux:" + input.ID
	cleanup := func() {}
	req := core.SessionRequest{
		AdapterRef: ref,
		Source:     auxSourceName,
		Title:      input.Name,
		MediaKind:  core.MediaKindMusic,
		Capabilities: core.Capabilities{
			CanPause: false,
			CanSeek:  false,
		},
		Visualizer: core.VisualizerRequest{
			Enabled: true,
			Metadata: core.VisualizerMetadata{
				Title:  input.Name,
				Artist: "AUX",
			},
		},
		AudioOutputMode: coreAudioOutputMode(input.AudioOutput),
	}

	switch input.Mode {
	case ModeStreamURL:
		probeURL, playURL, release, err := a.mintProxyPairLocked(input.URL)
		if err != nil {
			return core.SessionRequest{}, nil, unavailableError("AUX stream unavailable: %v", err)
		}
		req.StreamProbeURL = probeURL
		req.StreamURL = playURL
		req.MediaInputPolicy = auxLoopbackPolicy()
		cleanup = release
	case ModeLocalCapture:
		req.AudioCapture = core.AudioCaptureInput{
			Enabled:         true,
			Format:          input.Format,
			Device:          input.Device,
			SampleRate:      effectiveSampleRate(input.SampleRate, a.bridge.Audio.SampleRate),
			Channels:        effectiveChannels(input.Channels, a.bridge.Audio.Channels),
			ThreadQueueSize: input.ThreadQueueSize,
			AnalyzeDuration: time.Duration(input.AnalyzeDurationMillis) * time.Millisecond,
			ProbeSize:       input.ProbeSize,
		}
	default:
		return core.SessionRequest{}, nil, unavailableError("unsupported AUX input mode %q", input.Mode)
	}

	req.OnStop = func(reason string) {
		cleanup()
		a.onStopForRef(ref)(reason)
	}
	return req, cleanup, nil
}

func (a *Adapter) mintProxyPairLocked(upstream string) (string, string, func(), error) {
	probeURL, err := a.mintProxyURL(proxyTokenProbe, upstream, 5*time.Second)
	if err != nil {
		return "", "", nil, err
	}
	playURL, err := a.mintProxyURL(proxyTokenPlay, upstream, maxProxyTokenTTL)
	if err != nil {
		a.releaseProxyURLs(probeURL)
		return "", "", nil, err
	}
	release := a.releaseProxyURLsFunc(probeURL, playURL)
	return probeURL, playURL, release, nil
}

func (a *Adapter) releaseProxyURLsFunc(rawURLs ...string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			a.releaseProxyURLs(rawURLs...)
		})
	}
}

func (a *Adapter) releaseProxyURLs(rawURLs ...string) {
	tokens := make([]string, 0, len(rawURLs))
	for _, raw := range rawURLs {
		if token := proxyTokenFromURL(raw); token != "" {
			tokens = append(tokens, token)
		}
	}
	if len(tokens) == 0 {
		return
	}
	a.proxy.release(tokens...)
}

func proxyTokenFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Query().Get("aux_token")
}

func auxLoopbackPolicy() core.MediaInputPolicy {
	return core.MediaInputPolicy{
		ProtocolWhitelist: []string{"http", "tcp"},
		DisableReconnect:  true,
		RWTimeout:         5 * time.Second,
	}
}

func coreAudioOutputMode(mode string) core.AudioOutputMode {
	switch mode {
	case AudioOutputVisualOnly:
		return core.AudioOutputVisualOnly
	case AudioOutputMonitor:
		return core.AudioOutputMonitor
	default:
		return core.AudioOutputDefault
	}
}

func effectiveSampleRate(value, fallback int) int {
	if value != 0 {
		return value
	}
	return fallback
}

func effectiveChannels(value, fallback int) int {
	if value != 0 {
		return value
	}
	return fallback
}

func unavailableError(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("%w: %s", adapters.ErrSourceUnavailable, msg)
}

func (a *Adapter) recordAUXStartError(err error) error {
	a.mu.Lock()
	a.state = adapters.StateError
	a.lastErr = err.Error()
	a.stateSince = a.auxNow()
	a.mu.Unlock()
	return err
}

func (a *Adapter) rollbackAUXStart(ref string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeRef != ref {
		return
	}
	a.activeRef = ""
	a.state = adapters.StateError
	a.lastErr = err.Error()
	a.stateSince = a.auxNow()
}

func (a *Adapter) onStopForRef(ref string) func(string) {
	return func(reason string) {
		_ = reason
		a.clearActiveRef(ref)
	}
}

func (a *Adapter) clearActiveRef(ref string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeRef != ref {
		return
	}
	a.activeRef = ""
	a.state = adapters.StateStopped
	a.lastErr = ""
	a.stateSince = a.auxNow()
}

func (a *Adapter) auxNow() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}
