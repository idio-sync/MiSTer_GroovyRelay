package core

import "errors"

// Typed error sentinels exported for adapters that need to fault-map
// Manager errors more precisely than "any error → generic failure."
//
// DLNA's AVTransport handlers care about the difference between "the
// resource was unreachable" (UPnP error 716, Resource not found) and
// "something else went wrong" (501, Action Failed). Plex / Jellyfin /
// URL adapters don't care — they bubble Manager errors up the SOAP /
// HTTP stack and the controllers don't distinguish. So these sentinels
// are wrapped at Manager call sites only where the distinction matters
// to a caller; zero-policy adapters keep working unchanged because
// errors.Is on a non-wrapping error returns false and they don't switch
// on it.
//
// Spec: docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md
// SOAP fault mapping table: "Backend probe/playback failure → 716
// Resource not found when the resource cannot be reached, otherwise 501
// Action Failed."
var (
	// ErrProbeUnreachable indicates the upstream media probe could not
	// reach the source (DNS failure, TCP refused, ffprobe timeout, HTTP
	// 4xx/5xx, etc.). Wrapped by Manager.probeForStart only when the
	// ffprobe failure looks like a reachability problem; parse/invalid-
	// data failures remain generic probe errors so callers can map them
	// separately.
	ErrProbeUnreachable = errors.New("probe unreachable")

	// ErrPolicyRejected is reserved for the case where an adapter's
	// MediaInputPolicy denies the URL or headers at the core/FFmpeg
	// boundary (e.g. blocked scheme, blacklisted host, oversize header).
	// No Manager call site currently produces this — DLNA's URL
	// validator gates input upstream in the adapter, and FilterHeaders
	// silently drops blocked entries rather than erroring. The sentinel
	// exists so future policy enforcement can wrap it without a
	// signature change, and so adapters can include the case in their
	// errors.Is switches today.
	ErrPolicyRejected = errors.New("policy rejected")

	// ErrPlaneError wraps synchronous data-plane setup failures returned
	// by Manager.startPlaneLocked — i.e. modeline/preset resolution and
	// RGB-mode resolution. These typically reflect bridge config
	// corruption (operator-visible via slog) rather than anything a
	// remote controller can usefully act on, so wrapping them is mainly
	// a categorization aid.
	//
	// Asynchronous plane.Run failures (ffmpeg crashes, INIT-ACK timeouts,
	// mid-stream errors) fire from the plane goroutine via OnStop and
	// don't return through Pause/Stop/SeekTo synchronously, so they are
	// NOT wrapped with this sentinel — the OnStop reason string is the
	// channel adapters use for those.
	ErrPlaneError = errors.New("plane error")
)
