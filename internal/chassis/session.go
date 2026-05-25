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
// transport data. Optional; nil/unowned snapshots render read-only.
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
func snapshotFromSession(cfg Config, sv SessionViewer, vv VisualizerViewer, tv TransportViewer, aux AUXStarter, now time.Time) ReceiverPageData {
	if sv == nil {
		base := idleSnapshot(cfg, now)
		applyAUXSourceState(&base, aux)
		base.Visualizer.ActiveMode = liveVisualizerMode(cfg, vv)
		return base
	}
	return snapshotFromStatusView(cfg, sv.StatusHomeView(), vv, tv, aux, now)
}

// snapshotFromStatusView is snapshotFromSession's worker variant for
// callers that already hold a StatusHomeView (Server.buildSnapshot reads
// the view once and reuses it for both transport data and meter
// sampling, keeping the per-tick StatusHomeView() count at one).
func snapshotFromStatusView(cfg Config, view core.StatusHomeView, vv VisualizerViewer, tv TransportViewer, aux AUXStarter, now time.Time) ReceiverPageData {
	base := idleSnapshot(cfg, now)
	switch view.State {
	case core.StatePlaying, core.StatePaused:
		base.State = StateLive
		base.VFD.State = string(StateLive)
		base.VFD.Title = view.Title
		base.VFD.Marquee = formatLiveMarquee(view)
		// QueueCurrent / QueueTotal stay 0/0 (Phase 1 placeholder).
		// SystemTime + Uptime are computed in idleSnapshot from `now` and
		// cfg.StartedAt; they remain valid in live state.
		base.Transport = buildTransportData(view, tv, context.Background())
	default:
		// idle/unknown keep idleSnapshot data
	}
	applyAUXSourceState(&base, aux)
	base.Visualizer.ActiveMode = liveVisualizerMode(cfg, vv)
	return base
}

func liveVisualizerMode(cfg Config, vv VisualizerViewer) string {
	if vv == nil {
		return defaultVisualizerMode(cfg)
	}
	return config.NormalizeVisualizerMode(vv.VisualizerMode())
}

func buildTransportData(view core.StatusHomeView, tv TransportViewer, ctx context.Context) TransportData {
	percent := computeSeekFillPercent(view.Position, view.Duration)
	out := TransportData{
		State:           transportStateFromCore(view.State),
		SeekFillPercent: percent,
		ElapsedTime:     formatPlaybackPosition(view.Position),
		TotalTime:       formatPlaybackDuration(view.Duration),
		OffsetMS:        clampMSNonNegative(int(view.Position / time.Millisecond)),
		DurationMS:      clampMSNonNegative(int(view.Duration / time.Millisecond)),
		AdapterRef:      view.AdapterRef,
		Generation:      view.Generation,
	}
	if percent > 0 || view.Duration > 0 {
		out.PercentPlayed = fmt.Sprintf("%d%%", percent)
	}
	if tv == nil {
		return out
	}

	adapterView, owns := tv.PlaybackViewForSnapshot(ctx, view)
	if !owns {
		return out
	}
	if adapterView.Seek != nil {
		if adapterView.Seek.DurationMS > 0 {
			out.DurationMS = adapterView.Seek.DurationMS
		}
		if adapterView.Seek.OffsetMS >= 0 {
			out.OffsetMS = adapterView.Seek.OffsetMS
		}
	}
	out.ActionsEnabled = actionsEnabledFromAdapterView(adapterView)
	return out
}

func transportStateFromCore(s core.State) string {
	switch s {
	case core.StatePlaying:
		return "playing"
	case core.StatePaused:
		return "paused"
	default:
		return "stopped"
	}
}

func computeSeekFillPercent(pos, dur time.Duration) int {
	if dur <= 0 {
		return 0
	}
	percent := int((float64(pos) / float64(dur)) * 100)
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func clampMSNonNegative(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func actionsEnabledFromAdapterView(view adapters.PlaybackBannerAdapterView) ActionsEnabled {
	var out ActionsEnabled
	for _, action := range view.Actions {
		if !action.Enabled {
			continue
		}
		switch action.ID {
		case adapters.PlaybackActionPrevious:
			out.Previous = true
		case adapters.PlaybackActionNext:
			out.Next = true
		case adapters.PlaybackActionPause, adapters.PlaybackActionResume:
			out.PauseResume = true
		case adapters.PlaybackActionStop:
			out.Stop = true
		case adapters.PlaybackActionReplay:
			out.Replay = true
		}
	}
	out.Seek = view.Seek != nil && view.Seek.Enabled
	return out
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
