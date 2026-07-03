// Package dataplane owns the field pump (NTSC 59.94 Hz, PAL 50 Hz): timer, pipe readers, and
// the Plane orchestrator that stitches FFmpeg output to the Groovy sender.
//
// The clock is a push-driven free-run at the source cadence (no pull from
// the MiSTer — see docs/references/groovy_mister.md §"Clock Discipline").
package dataplane
