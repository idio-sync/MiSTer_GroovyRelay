package chassis

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// SessionViewer is the narrow read-only view of bridge session state
// the chassis needs. *core.Manager satisfies this structurally via its
// StatusHomeView() method. Tests inject fakes; production wires
// *core.Manager. Mirrors internal/ui.StatusViewer.
//
// Phase 1 / Spec 2 consumes only StatusHomeView(). Spec 3 (transport
// controls) will extend this interface with Pause / Play / Stop / SeekTo
// or introduce a sibling SessionController interface — to be decided
// in that spec's review.
type SessionViewer interface {
	StatusHomeView() core.StatusHomeView
}

// TransportViewer is the read-only playback banner source for chassis
// transport data. Optional; later tasks own snapshot population.
type TransportViewer interface {
	PlaybackViewForSnapshot(ctx context.Context, snap core.StatusHomeView) (adapters.PlaybackBannerAdapterView, bool)
}

// TransportController handles playback control actions from chassis
// transport UI. Optional; later tasks own HTTP handlers.
type TransportController interface {
	HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error)
}

// snapshotFromSession builds the page-render data from current bridge
// session state. When sv is nil OR the bridge is idle, falls back to
// idleSnapshot. When live (playing or paused), overrides VFD title +
// marquee + State; queue stays 0/0 placeholder until a later spec
// surfaces real queue data on StatusHomeView. Visualizer mode is always
// sourced from vv when available, falling back to cfg.
//
// State mapping (per spec):
//
//	core.StateIdle    -> chassis StateIdle ("idle")
//	core.StatePlaying -> chassis StateLive ("live")
//	core.StatePaused  -> chassis StateLive ("live")
//
// Paused maps to live so the chassis stays bright during transport
// pause; the transport-row controls (Spec 3) own pause indication.
func snapshotFromSession(cfg Config, sv SessionViewer, vv VisualizerViewer, now time.Time) ReceiverPageData {
	base := idleSnapshot(cfg, now)
	if sv != nil {
		view := sv.StatusHomeView()
		switch view.State {
		case core.StatePlaying, core.StatePaused:
			base.State = StateLive
			base.VFD.State = string(StateLive)
			base.VFD.Title = view.Title
			base.VFD.Marquee = formatLiveMarquee(view)
			// QueueCurrent / QueueTotal stay 0/0 (Phase 1 placeholder).
			// SystemTime + Uptime are computed in idleSnapshot from `now` and
			// cfg.StartedAt; they remain valid in live state.
		default:
			// idle/unknown keep idleSnapshot data
		}
	}
	base.Visualizer.ActiveMode = liveVisualizerMode(cfg, vv)
	return base
}

func liveVisualizerMode(cfg Config, vv VisualizerViewer) string {
	if vv == nil {
		return defaultVisualizerMode(cfg)
	}
	return config.NormalizeVisualizerMode(vv.VisualizerMode())
}

// formatLiveMarquee composes the VFD marquee string for live state per
// spec §"Field mapping": "<UPPER(Source)> · <position> / <duration>".
// Examples:
//
//	PLEX, 04:23, 09:56  → "PLEX · 04:23 / 09:56"
//	PLEX, 04:23, 0      → "PLEX · 04:23 / --:--"   (unknown duration)
//	plex, 0,      0     → "PLEX · 00:00 / --:--"   (start of unknown stream)
//	"",   0,      0     → "BRIDGE · 00:00 / --:--" (empty source fallback)
//	1h4m5s position with same duration → "PLEX · 1:04:05 / 1:04:05"
//
// Source fallback is "BRIDGE" (NOT "PLAYING") per spec §"Field mapping".
func formatLiveMarquee(view core.StatusHomeView) string {
	src := strings.ToUpper(view.Source)
	if src == "" {
		src = "BRIDGE"
	}
	return fmt.Sprintf("%s · %s / %s", src,
		formatPlaybackPosition(view.Position),
		formatPlaybackDuration(view.Duration))
}

// formatPlaybackPosition renders the current position. Negative durations
// clamp to "00:00"; non-negative durations truncate to whole seconds.
// Below one hour → "MM:SS"; >= one hour → "H:MM:SS" (single-digit hours
// for < 10h; multi-digit hours expand naturally via %d).
func formatPlaybackPosition(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return formatPlaybackClock(d)
}

// formatPlaybackDuration renders the total duration. d <= 0 means
// "unknown" per spec §"Field mapping" and renders as "--:--"; positive
// durations use the same MM:SS / H:MM:SS formatting as position.
func formatPlaybackDuration(d time.Duration) string {
	if d <= 0 {
		return "--:--"
	}
	return formatPlaybackClock(d)
}

// formatPlaybackClock is the shared MM:SS / H:MM:SS formatter. d is
// assumed non-negative — callers clamp.
func formatPlaybackClock(d time.Duration) string {
	total := int(d / time.Second)
	hours := total / 3600
	minutes := (total / 60) % 60
	seconds := total % 60
	if hours > 0 {
		// Single-digit hours per spec example ("1:04:05"). %d (not %02d)
		// for the hours component; minutes/seconds always two digits.
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}
