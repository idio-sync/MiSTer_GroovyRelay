//go:build !windows

package ffmpeg

import (
	"context"
	"os"
)

// Spawn starts an ffmpeg subprocess running the pipeline described by spec.
// It uses os.Pipe() pairs for the video and audio streams and hands the
// write ends to the child via cmd.ExtraFiles (fd 3 and fd 4 inside the
// child). The parent's copies of the write ends are closed immediately so
// EOF propagates on the read side when the child exits.
//
// ExtraFiles is a Linux/Unix-only feature. Windows uses process_windows.go's
// TCP loopback shim instead.
func Spawn(ctx context.Context, spec PipelineSpec) (*Process, error) {
	videoR, videoW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	audioR, audioW, err := os.Pipe()
	if err != nil {
		videoR.Close()
		videoW.Close()
		return nil, err
	}

	// The child addresses these pipes as fd 3 and fd 4. This must match the
	// order of cmd.ExtraFiles below.
	spec.VideoPipePath = "pipe:3"
	spec.AudioPipePath = "pipe:4"

	spec = withVisualizerCapabilities(ctx, spec)
	cmd := BuildCommand(ctx, spec)
	cmd.ExtraFiles = []*os.File{videoW, audioW}
	// Forward stderr line-by-line into slog instead of dumping raw to the
	// bridge's stderr. With "-loglevel warning" set in pipeline.go, every
	// line FFmpeg emits is at warning severity or worse — surface them as
	// slog.Warn so they show up alongside the bridge's structured logs.
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		videoR.Close()
		videoW.Close()
		audioR.Close()
		audioW.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		videoR.Close()
		videoW.Close()
		audioR.Close()
		audioW.Close()
		_ = stderrPipe.Close()
		return nil, err
	}

	// The write ends are now in the child. Close our copies so the reader
	// side sees EOF as soon as the child exits.
	videoW.Close()
	audioW.Close()

	p := newProcess(cmd, videoR, audioR)
	p.forwardStderr(stderrPipe)
	p.watchContext(ctx)
	p.launchWaiter()
	return p, nil
}
