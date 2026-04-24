package ffmpeg

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// Process wraps a running ffmpeg invocation plus its video/audio read pipes.
// The data plane consumes VideoPipe and AudioPipe; it calls Stop() to tear
// the child down (e.g. on seek or session end) and watches Done() to notice
// a spontaneous exit.
type Process struct {
	cmd       *exec.Cmd
	videoPipe io.ReadCloser
	audioPipe io.ReadCloser

	wg      sync.WaitGroup
	stopped chan struct{}

	stopOnce      sync.Once
	stopRequested atomic.Bool
}

// Spawn starts an ffmpeg subprocess running the pipeline described by spec.
// It uses os.Pipe() pairs for the video and audio streams and hands the
// write ends to the child via cmd.ExtraFiles (fd 3 and fd 4 inside the
// child). The parent's copies of the write ends are closed immediately so
// EOF propagates on the read side when the child exits.
//
// ExtraFiles is a Linux/Unix-only feature; on Windows, cmd.Start() returns
// an error when ExtraFiles is non-nil. Windows is not the deployment target
// (Docker/Linux is), but unit tests that exercise lifecycle wiring should
// use the unexported newProcess/start helpers which do not use ExtraFiles.
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

	cmd := BuildCommand(ctx, spec)
	cmd.ExtraFiles = []*os.File{videoW, audioW}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		videoR.Close()
		videoW.Close()
		audioR.Close()
		audioW.Close()
		return nil, err
	}

	// The write ends are now in the child. Close our copies so the reader
	// side sees EOF as soon as the child exits.
	videoW.Close()
	audioW.Close()

	p := newProcess(cmd, videoR, audioR)
	p.watchContext(ctx)
	p.launchWaiter()
	return p, nil
}

// newProcess is the shared constructor used by both Spawn and tests.
func newProcess(cmd *exec.Cmd, videoR, audioR io.ReadCloser) *Process {
	return &Process{
		cmd:       cmd,
		videoPipe: videoR,
		audioPipe: audioR,
		stopped:   make(chan struct{}),
	}
}

// start is a unit-test helper: Start the child (assumed already configured)
// and launch the Wait() goroutine. Does not configure pipes or ExtraFiles.
func (p *Process) start() error {
	if err := p.cmd.Start(); err != nil {
		return err
	}
	p.launchWaiter()
	return nil
}

// launchWaiter spawns the goroutine that Waits on the child and closes
// stopped when Wait returns.
func (p *Process) launchWaiter() {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.logWaitResult(p.cmd.Wait())
		close(p.stopped)
	}()
}

func (p *Process) logWaitResult(err error) {
	if err == nil {
		return
	}
	if p.stopRequested.Load() {
		slog.Debug("ffmpeg stopped during session teardown", "err", err)
		return
	}
	slog.Warn("ffmpeg exited", "err", err)
}

// watchContext marks the process stop as expected when the parent session
// context is canceled. exec.CommandContext will SIGKILL the child on ctx.Done;
// without this side-band the waiter logs that intentional kill as an
// unexpected "ffmpeg exited" warning.
func (p *Process) watchContext(ctx context.Context) {
	if ctx == nil {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			p.stopRequested.Store(true)
		case <-p.stopped:
		}
	}()
}

// VideoPipe returns the read-end of the video stream. Yields one
// OutputWidth × OutputHeight BGR24 progressive frame per read (59.94 Hz)
// once the filter chain produces output.
func (p *Process) VideoPipe() io.Reader { return p.videoPipe }

// AudioPipe returns the read-end of the audio stream (s16le PCM).
func (p *Process) AudioPipe() io.Reader { return p.audioPipe }

// Stop kills the child if still running and waits for the reaper goroutine
// to finish. Safe to call more than once; after the first call subsequent
// invocations are no-ops.
func (p *Process) Stop() {
	p.stopOnce.Do(func() {
		if p.cmd != nil && p.cmd.Process != nil {
			select {
			case <-p.stopped:
			default:
				p.stopRequested.Store(true)
				// Ignore errors — the process may already be gone.
				_ = p.cmd.Process.Kill()
			}
		}
		p.wg.Wait()
		if p.videoPipe != nil {
			_ = p.videoPipe.Close()
		}
		if p.audioPipe != nil {
			_ = p.audioPipe.Close()
		}
	})
}

// Done returns a channel that is closed once cmd.Wait() returns (either a
// clean exit or a kill).
func (p *Process) Done() <-chan struct{} { return p.stopped }
