package url

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	stdurl "net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
)

const audioClassificationProbeTimeout = 800 * time.Millisecond

// castURL is the shared cast-spawning logic. It validates the URL,
// records into history (so failed casts surface for one-click retry),
// dispatches direct vs. yt-dlp per mode + cfg + probe, resolves if
// needed, builds the SessionRequest with v1.5 caps, and starts the
// session. Returns the AdapterRef, resolvedVia ("direct" or "ytdlp"),
// the HTTP status to use on error, and the error.
//
// Used by HandleQuickCast (chassis Cast drawer), banner resume/replay
// actions, and the Companion* browser-extension entry points (play,
// resume, replay, history play). Each of these re-resolves the URL
// (yt-dlp tokens expire), so they all funnel through here.
type urlSessionStarter func(core.SessionRequest) (bool, error)
type urlStreamStarter func(context.Context, streamhandoff.Resolver, streamhandoff.Resolution) (streamhandoff.StartResult, bool, error)

type urlCastStarter struct {
	startCore   urlSessionStarter
	startStream urlStreamStarter
}

func (a *Adapter) castURL(ctx context.Context, rawURL, mode string) (ref, resolvedVia string, status int, err error) {
	return a.castURLWithHLSBuffer(ctx, rawURL, mode, "auto")
}

func (a *Adapter) castURLWithHLSBuffer(ctx context.Context, rawURL, mode, hlsBufferMode string) (ref, resolvedVia string, status int, err error) {
	return a.castURLWithStarter(ctx, rawURL, mode, hlsBufferMode, urlCastStarter{
		startCore: func(req core.SessionRequest) (bool, error) {
			return true, a.core.StartSession(req)
		},
		startStream: func(ctx context.Context, r streamhandoff.Resolver, res streamhandoff.Resolution) (streamhandoff.StartResult, bool, error) {
			started, err := r.StartResolvedStream(ctx, res)
			return started, true, err
		},
	})
}

func (a *Adapter) castURLGuarded(ctx context.Context, rawURL, mode, expectedRef string, expectedGeneration uint64) (ref, resolvedVia string, status int, err error) {
	if a.core == nil {
		return "", "", http.StatusInternalServerError, fmt.Errorf("core not wired")
	}
	st := a.core.Status()
	if st.AdapterRef != expectedRef || st.Generation != expectedGeneration {
		return "", "", http.StatusConflict, fmt.Errorf("active session changed")
	}
	return a.castURLWithStarter(ctx, rawURL, mode, "auto", urlCastStarter{
		startCore: func(req core.SessionRequest) (bool, error) {
			return a.core.StartSessionIfSession(req, expectedRef, expectedGeneration)
		},
		startStream: func(ctx context.Context, r streamhandoff.Resolver, res streamhandoff.Resolution) (streamhandoff.StartResult, bool, error) {
			guarded, ok := r.(streamhandoff.GuardedResolver)
			if !ok {
				return streamhandoff.StartResult{}, false, nil
			}
			return guarded.StartResolvedStreamIfSession(ctx, res, expectedRef, expectedGeneration)
		},
	})
}

func (a *Adapter) castURLWithStarter(ctx context.Context, rawURL, mode, hlsBufferMode string, starter urlCastStarter) (ref, resolvedVia string, status int, err error) {
	parsed, perr := stdurl.Parse(rawURL)
	if perr != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", http.StatusBadRequest, fmt.Errorf("not a valid URL")
	}
	switch parsed.Scheme {
	case "http", "https":
		// ok
	default:
		return "", "", http.StatusBadRequest,
			fmt.Errorf("scheme not supported in v1: %s (only http and https)", parsed.Scheme)
	}
	hlsBufferMode, herr := normalizeHLSBufferMode(hlsBufferMode)
	if herr != nil {
		return "", "", http.StatusBadRequest, herr
	}

	// Record into history regardless of dispatch / cast outcome, so
	// the operator can re-try a typo URL with one click. Spec §"History
	// / Constraints". MUST come BEFORE the dispatch decision so a
	// resolver failure still records.
	a.history.AddOrBumpWithHLSMode(rawURL, hlsBufferMode)

	// Decide the route. Snapshot resolver under the same lock as cfg
	// and probe — Start writes a.resolver under a.mu, so the read
	// here must hold the lock to avoid a -race detector flag in CI.
	a.mu.Lock()
	cfg := a.cfg
	probe := a.ytdlpProbe
	resolver := a.resolver
	streamResolver := a.streamResolver
	bridge := a.bridge
	hlsBufferOpen := a.hlsBufferOpen
	a.mu.Unlock()
	if resolver != nil {
		// The production resolver resolves the current sidecar/PATH override
		// at play time, so bridge.ytdlp_path hot-swaps must not be blocked by
		// a stale startup version probe.
		probe.OK = true
	}

	if streamResolver != nil {
		res, matched, rerr := streamResolver.ResolveStreamURL(ctx, rawURL)
		if matched {
			if rerr != nil {
				return "", "", http.StatusBadRequest, rerr
			}
			started, matched, serr := starter.startStream(ctx, streamResolver, res)
			if serr != nil {
				return "", "", http.StatusBadRequest, serr
			}
			if !matched {
				return "", "", http.StatusConflict, fmt.Errorf("active session changed")
			}
			if started.AdapterRef == "" {
				return "", "", http.StatusInternalServerError, fmt.Errorf("streams resolver returned empty adapter ref")
			}
			return started.AdapterRef, "streams", http.StatusOK, nil
		}
	}

	// parsed.Hostname() strips any :port suffix.
	useYtdlp, derr := decideRoute(mode, parsed.Hostname(), cfg, probe)
	if derr != nil {
		return "", "", http.StatusBadRequest, derr
	}

	resolvedVia = "direct"
	streamURL := rawURL
	var headers map[string]string
	var audioStreamURL string
	var audioHeaders map[string]string
	var resolvedTitle string
	var resolvedChannel string
	var resolvedUploadDate string
	var mediaPolicy core.MediaInputPolicy
	var mediaKind core.MediaKind
	var hlsSession *hlsbuffer.Session
	var hlsCfg hlsbuffer.Config
	audioClass := adapters.Unknown
	var audioProbe *ffmpeg.ProbeResult
	var resolvedDuration time.Duration

	if !useYtdlp {
		if owncastURL, ok := resolveOwncastHomepageURL(ctx, parsed); ok {
			streamURL = owncastURL
		}
	}

	if useYtdlp {
		if resolver == nil {
			return "", "", http.StatusInternalServerError, fmt.Errorf("resolver not configured")
		}
		res, rerr := resolver.Resolve(ctx, rawURL,
			cfg.YtdlpFormat,
			cookiesPathIfPresent(a.cookiesPath))
		if rerr != nil {
			safeMsg := strings.ReplaceAll(rerr.Error(), rawURL, redactURL(rawURL))
			a.setState(adapters.StateError, safeMsg)
			slog.Warn("yt-dlp resolve failed", "url", redactURL(rawURL), "err", safeMsg)
			return "", "", http.StatusInternalServerError, fmt.Errorf("%s", safeMsg)
		}
		streamURL = res.URL
		headers = res.Headers
		// DASH dual-stream path: when yt-dlp's selector merged
		// separate video + audio formats, the resolver reports
		// AudioURL alongside URL. Plumb both through; core.Manager
		// turns AudioStreamURL into a second ffmpeg -i input.
		// Empty AudioURL preserves the existing single-stream path.
		audioStreamURL = res.AudioURL
		audioHeaders = res.AudioHeaders
		resolvedTitle = res.Title
		resolvedChannel = res.Channel
		resolvedUploadDate = res.UploadDate
		resolvedDuration = res.Duration
		audioClass, audioProbe = a.classifyResolvedURLMedia(ctx, res, mediaPolicy)
		resolvedVia = "ytdlp"
		// Backfill the title onto the just-bumped history entry so
		// history shows "Big Buck Bunny" rather than just the youtu.be
		// shortlink. SetTitle no-ops on empty title and on missing
		// entries, so this is safe to call unconditionally here.
		a.history.SetTitle(rawURL, resolvedTitle)
	} else if shouldBufferDirectM3U8(parsed, hlsBufferMode, bridge) {
		var berr error
		validator := a.validateHLSURL
		if validator == nil {
			validator = defaultValidateHLSURL
		}
		validatedURL, verr := validator(ctx, rawURL, hlsbuffer.TrustModeGenericPublic)
		if verr != nil {
			safeMsg := adapters.SanitizeMediaProbeError(rawURL, verr)
			a.setState(adapters.StateError, safeMsg)
			slog.Warn("url hls validation failed", "url", adapters.RedactMediaURLForLog(rawURL), "err", safeMsg)
			return "", "", http.StatusInternalServerError, fmt.Errorf("%s", safeMsg)
		}
		hlsCfg = hlsbuffer.NormalizeConfig(hlsConfigFromBridge(bridge.HLSBuffer))
		hlsSession, berr = a.openURLHLSBufferWithConfig(ctx, validatedURL, bridge, hlsCfg, hlsBufferOpen)
		if berr != nil {
			safeMsg := adapters.SanitizeMediaProbeError(rawURL, berr)
			a.setState(adapters.StateError, safeMsg)
			slog.Warn("url hls buffer failed", "url", redactURL(rawURL), "err", safeMsg)
			return "", "", http.StatusInternalServerError, fmt.Errorf("%s", safeMsg)
		}
		streamURL = hlsSession.PlaybackPath
		mediaPolicy = hlsSession.Policy
		mediaKind = core.MediaKindVideo
		audioClass, audioProbe = a.classifyURLByProbe(ctx, streamURL, nil, mediaPolicy)
	}

	ref = newAdapterRef()

	// Derive a human-readable title from the URL. For direct file URLs
	// the basename (e.g. "clip.mp4") is the most useful label; for HLS
	// manifests and streaming URLs the path component is often empty or
	// just "/" so we fall back to the host. Spec PR2 §S3.
	title := resolvedTitle // yt-dlp already provided a rich title
	if title == "" {
		base := path.Base(parsed.Path)
		if base == "" || base == "/" || base == "." {
			title = parsed.Host
		} else {
			title = base
		}
	}

	if !useYtdlp && hlsSession == nil && audioClass == adapters.Unknown && !isDirectM3U8(parsed) {
		audioClass, audioProbe = a.classifyURLByProbe(ctx, streamURL, headers, mediaPolicy)
	}

	baseOnStop := a.makeOnStop(rawURL, resolvedTitle)
	onStop := baseOnStop
	if hlsSession != nil {
		onStop = withHLSBufferCleanup(a.hlsMeterClearingOnStop(ref, baseOnStop), hlsSession)
	}

	req := core.SessionRequest{
		StreamURL:         streamURL,
		InputHeaders:      headers,
		AudioStreamURL:    audioStreamURL,
		AudioInputHeaders: audioHeaders,
		// v1.5: unconditional caps + DirectPlay so the now-playing
		// banner's transport controls reach core.Manager. Per-source
		// seekability is enforced by the banner (Duration > 0 gating)
		// and by the resume action's Duration-based branching. Spec
		// §"Capability and DirectPlay flips".
		Capabilities:     core.Capabilities{CanSeek: true, CanPause: true},
		AdapterRef:       ref,
		Source:           "url",
		DirectPlay:       true,
		Title:            title,
		MediaKind:        mediaKind,
		MediaInputPolicy: mediaPolicy,
		// OnStop captures rawURL + resolvedTitle at request-construction
		// time, NOT inside the closure body — by the time OnStop runs,
		// adapter state may have been overwritten by a preempting
		// session.
		OnStop: onStop,
	}
	req.DisplayMetadata = core.DisplayMetadata{Primary: title}
	if useYtdlp {
		req.DisplayMetadata.Secondary = resolvedChannel // may be "" → row collapses
		req.DisplayMetadata.Tertiary = adapters.FormatUploadDate(resolvedUploadDate)
	} else {
		req.DisplayMetadata.Secondary = "URL"
	}
	if audioClass == adapters.AudioOnly {
		duration := resolvedDuration
		if duration == 0 && audioProbe != nil && audioProbe.Duration > 0 {
			duration = time.Duration(audioProbe.Duration * float64(time.Second))
		}
		adapters.ApplyAudioOnlyVisualizer(&req, adapters.AudioOnlyVisualizerMetadata{
			Title:    title,
			Artist:   resolvedChannel,
			Duration: duration,
		})
	}

	if a.core == nil {
		closeHLSSession(hlsSession)
		return "", "", http.StatusInternalServerError, fmt.Errorf("core not wired")
	}
	// Emit cast-requested before StartSession so the event is recorded
	// even if the manager rejects the request (e.g. probe failure).
	// Spec PR2 §S7, Source "url", Severity Info.
	a.emit(eventlog.SeverityInfo, fmt.Sprintf("cast-requested %s", req.AdapterRef))
	matched, serr := starter.startCore(req)
	if serr != nil {
		closeHLSSession(hlsSession)
		safeMsg := strings.ReplaceAll(serr.Error(), rawURL, redactURL(rawURL))
		a.setState(adapters.StateError, safeMsg)
		slog.Warn("url cast failed", "url", redactURL(rawURL), "err", serr)
		return "", "", http.StatusInternalServerError, fmt.Errorf("%s", safeMsg)
	}
	if !matched {
		closeHLSSession(hlsSession)
		return "", "", http.StatusConflict, fmt.Errorf("active session changed")
	}

	a.markRunning(rawURL)
	if hlsSession != nil {
		a.installHLSMeterOverlay(ref, hlsSession, hlsCfg)
	}
	slog.Info("url cast started",
		"url", redactURL(rawURL),
		"ref", ref,
		"resolved_via", resolvedVia)
	if resolvedTitle != "" {
		slog.Debug("url cast resolved", "ref", ref, "title", resolvedTitle)
	}
	return ref, resolvedVia, http.StatusOK, nil
}

func (a *Adapter) classifyResolvedURLMedia(ctx context.Context, res *ytdlp.Resolution, policy core.MediaInputPolicy) (adapters.AudioClassification, *ffmpeg.ProbeResult) {
	if res == nil {
		return adapters.Unknown, nil
	}
	if strings.TrimSpace(res.AudioURL) != "" {
		return adapters.Video, nil
	}
	if isM3U8URLString(res.URL) {
		return adapters.Video, nil
	}
	if class := adapters.ClassifyCodecs(res.VCodec, res.ACodec); class != adapters.Unknown {
		return class, nil
	}
	return a.classifyURLByProbe(ctx, res.URL, res.Headers, policy)
}

func (a *Adapter) classifyURLByProbe(ctx context.Context, mediaURL string, headers map[string]string, policy core.MediaInputPolicy) (adapters.AudioClassification, *ffmpeg.ProbeResult) {
	if strings.TrimSpace(mediaURL) == "" || a.ffprobe == nil || a.probeMedia == nil {
		return adapters.Unknown, nil
	}
	ffprobePath, err := a.ffprobe.Resolve()
	if err != nil {
		slog.Warn("url media probe unavailable",
			"url", adapters.RedactMediaURLForLog(mediaURL),
			"err", adapters.SanitizeMediaProbeError(mediaURL, err))
		return adapters.Unknown, nil
	}
	probe, err := a.probeMedia(ctx, ffprobePath, ffmpeg.ProbeInputSpec{
		URL:     mediaURL,
		Headers: headers,
		Policy:  policy,
		Timeout: audioClassificationProbeTimeout,
	})
	if err != nil {
		slog.Warn("url media probe failed",
			"url", adapters.RedactMediaURLForLog(mediaURL),
			"err", adapters.SanitizeMediaProbeError(mediaURL, err))
		return adapters.Unknown, nil
	}
	return adapters.ClassifyProbeResult(probe), probe
}

func normalizeHLSBufferMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "auto":
		return "auto", nil
	case "off":
		return "off", nil
	default:
		return "", fmt.Errorf("hls_buffer must be one of auto, off (got %q)", raw)
	}
}

func shouldBufferDirectM3U8(parsed *stdurl.URL, hlsBufferMode string, bridge config.BridgeConfig) bool {
	if parsed == nil || hlsBufferMode == "off" || !bridge.HLSBuffer.Enabled {
		return false
	}
	if strings.TrimSpace(os.Getenv("GROOVY_HLS_BUFFER")) == "0" {
		return false
	}
	return isDirectM3U8(parsed)
}

func isDirectM3U8(parsed *stdurl.URL) bool {
	return parsed != nil && strings.HasSuffix(strings.ToLower(strings.TrimSpace(parsed.Path)), ".m3u8")
}

func isM3U8URLString(raw string) bool {
	parsed, err := stdurl.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return isDirectM3U8(parsed)
}

func (a *Adapter) openURLHLSBuffer(ctx context.Context, rawURL string, bridge config.BridgeConfig, open hlsBufferOpener) (*hlsbuffer.Session, error) {
	return a.openURLHLSBufferWithConfig(ctx, rawURL, bridge, hlsConfigFromBridge(bridge.HLSBuffer), open)
}

func (a *Adapter) openURLHLSBufferWithConfig(ctx context.Context, rawURL string, bridge config.BridgeConfig, hlsCfg hlsbuffer.Config, open hlsBufferOpener) (*hlsbuffer.Session, error) {
	if bridge.DataDir == "" {
		return nil, fmt.Errorf("url hls buffer: bridge data_dir is required")
	}
	cacheRoot := filepath.Join(bridge.DataDir, "url", "hls")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return nil, fmt.Errorf("url hls buffer: create cache root: %w", err)
	}
	if open == nil {
		open = hlsbuffer.OpenSession
	}
	return open(ctx, hlsbuffer.SessionOptions{
		SourceURL:    rawURL,
		CacheRoot:    cacheRoot,
		Config:       hlsCfg,
		TrustMode:    hlsbuffer.TrustModeGenericPublic,
		OutputHeight: hlsOutputHeightFromBridge(bridge),
	})
}

func defaultValidateHLSURL(ctx context.Context, rawURL string, mode hlsbuffer.TrustMode) (string, error) {
	return hlsbuffer.URLValidator{}.Validate(ctx, rawURL, mode)
}

func hlsOutputHeightFromBridge(bridge config.BridgeConfig) int {
	switch bridge.Video.Modeline {
	case "PAL_576i", "PAL_288p":
		return 576
	default:
		return 480
	}
}

func hlsConfigFromBridge(c config.HLSBufferConfig) hlsbuffer.Config {
	return hlsbuffer.Config{
		Enabled:                c.Enabled,
		LiveEdgeSegments:       c.LiveEdgeSegments,
		StartSegments:          c.StartSegments,
		MaxCachedSegments:      c.MaxCachedSegments,
		MaxCacheBytes:          c.MaxCacheBytes,
		MaxPlaylistBytes:       c.MaxPlaylistBytes,
		MaxSegmentBytes:        c.MaxSegmentBytes,
		SegmentTimeout:         time.Duration(c.SegmentTimeoutSeconds) * time.Second,
		PlaylistTimeout:        time.Duration(c.PlaylistTimeoutSeconds) * time.Second,
		MaxVariantHeight:       c.MaxVariantHeight,
		StaleCacheReapInterval: time.Duration(c.StaleCacheReapHours) * time.Hour,
	}
}

func withHLSBufferCleanup(base func(string), session *hlsbuffer.Session) func(string) {
	return func(reason string) {
		if base != nil {
			base(reason)
		}
		closeHLSSession(session)
	}
}

func closeHLSSession(session *hlsbuffer.Session) {
	if session != nil && session.Close != nil {
		_ = session.Close()
	}
}

// decideRoute is pure: returns whether to invoke yt-dlp, or an error
// for malformed mode values / forced-ytdlp-when-disabled.
func decideRoute(mode, host string, cfg Config, probe ytdlpProbe) (useYtdlp bool, err error) {
	switch mode {
	case "auto":
		if !cfg.YtdlpEnabled || !probe.OK {
			return false, nil
		}
		return ytdlp.Match(host, cfg.YtdlpHosts), nil
	case "ytdlp":
		if !cfg.YtdlpEnabled {
			return false, fmt.Errorf("yt-dlp resolver is disabled in adapter config")
		}
		if !probe.OK {
			return false, fmt.Errorf("yt-dlp binary not found at runtime")
		}
		return true, nil
	case "direct":
		return false, nil
	default:
		return false, fmt.Errorf("mode must be one of auto, ytdlp, direct (got %q)", mode)
	}
}

// cookiesPathIfPresent returns the cookies path if the file exists,
// or "" otherwise. The resolver passes "" → no --cookies flag.
func cookiesPathIfPresent(path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// makeOnStop captures rawURL + title at request-construction time so
// the closure body uses the captured values, not adapter mutable
// fields that may have been overwritten by a preempting session
// (review fix I3 / spec §"Lifecycle integration").
func (a *Adapter) makeOnStop(rawURL, title string) func(reason string) {
	return func(reason string) {
		switch reason {
		case "eof", "preempted", "stopped", "":
			a.setState(adapters.StateStopped, "")
		default:
			a.setState(adapters.StateError, reason)
		}
		slog.Debug("url session ended",
			"reason", reason,
			"url", redactURL(rawURL),
			"title", title)
	}
}

// markRunning records the active URL and transitions to StateRunning.
func (a *Adapter) markRunning(url string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = adapters.StateRunning
	a.lastErr = ""
	a.lastURL = url
	a.stateSince = time.Now()
}

// snapshotLastURL returns a.lastURL under a.mu. Used by banner and companion
// controls that need the most-recent URL for guarded replay/resume or for
// credential redaction in error paths.
func (a *Adapter) snapshotLastURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastURL
}

// newAdapterRef returns "url:<8 hex>". 4 random bytes is plenty of entropy
// for a single-active-session adapter; collisions are inconsequential
// since AdapterRef is opaque to core.
func newAdapterRef() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "url:" + hex.EncodeToString(b[:])
}

// redactURL returns the URL with any user:password authority component
// stripped. Uses url.URL.Redacted() under the hood; on parse failure
// returns "<unparseable url>" rather than echoing arbitrary user input.
func redactURL(raw string) string {
	u, err := stdurl.Parse(raw)
	if err != nil || u == nil {
		return "<unparseable url>"
	}
	return u.Redacted()
}

// redactErr returns err.Error() with any occurrence of lastURL replaced by its
// redacted form.
func redactErr(err error, lastURL string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if lastURL == "" {
		return msg
	}
	return strings.ReplaceAll(msg, lastURL, redactURL(lastURL))
}
