package core

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"

// MediaInputPolicy is the adapter-facing alias for the input-side FFmpeg
// constraint set. It is a type alias — not a wrapper — so adapters can
// build core.SessionRequest{MediaInputPolicy: core.MediaInputPolicy{...}}
// and the same value travels untouched into the FFmpeg call sites
// (Probe, ProbeCrop, BuildCommand) that core.Manager dispatches.
//
// The full struct definition and behavior live in internal/ffmpeg/policy.go;
// see that file for the field-by-field semantics. The alias lives here so
// adapters never need to import internal/ffmpeg directly: core remains the
// adapter API surface (per spec §4.5).
type MediaInputPolicy = ffmpeg.MediaInputPolicy
