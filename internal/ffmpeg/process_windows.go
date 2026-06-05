//go:build windows

package ffmpeg

import (
	"context"
	"fmt"
	"net"
	"time"
)

const tcpAcceptTimeout = 10 * time.Second

type acceptResult struct {
	name string
	conn *net.TCPConn
	err  error
}

// Spawn starts ffmpeg on Windows using loopback TCP sockets for raw video/audio
// streams. os/exec.ExtraFiles is not supported on Windows, so fd 3/4 pipes are
// only used by the Unix implementation.
func Spawn(ctx context.Context, spec PipelineSpec) (*Process, error) {
	spec = withVisualizerCapabilities(ctx, spec)
	audioEnabled := audioOutputEnabled(spec)

	videoListener, err := listenLoopback()
	if err != nil {
		return nil, err
	}
	var audioListener *net.TCPListener
	if audioEnabled {
		audioListener, err = listenLoopback()
		if err != nil {
			_ = videoListener.Close()
			return nil, err
		}
	}

	spec.VideoPipePath = tcpURL(videoListener)
	if audioEnabled {
		spec.AudioPipePath = tcpURL(audioListener)
	}

	cmd := BuildCommand(ctx, spec)
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = videoListener.Close()
		closeListener(audioListener)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = videoListener.Close()
		closeListener(audioListener)
		_ = stderrPipe.Close()
		return nil, err
	}

	p := newProcess(cmd, nil, nil)
	p.forwardStderr(stderrPipe)

	watcherDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = videoListener.Close()
			closeListener(audioListener)
		case <-watcherDone:
		}
	}()

	results := make(chan acceptResult, 2)
	go acceptTCP("video", videoListener, results)
	required := 1
	if audioEnabled {
		required = 2
		go acceptTCP("audio", audioListener, results)
	}

	var videoConn, audioConn *net.TCPConn
	var acceptErr error
	for i := 0; i < required; i++ {
		res := <-results
		if res.err != nil {
			acceptErr = res.err
			break
		}
		if res.name == "video" {
			videoConn = res.conn
		} else {
			audioConn = res.conn
		}
	}
	close(watcherDone)
	_ = videoListener.Close()
	closeListener(audioListener)

	if acceptErr != nil {
		closeConn(videoConn)
		closeConn(audioConn)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		p.wg.Wait()
		// Cancellation aborts the accept by closing the listener (the watcher
		// goroutine above), which surfaces as "use of closed network
		// connection" rather than context.Canceled. Map it back to ctx.Err()
		// so callers (core.Manager.handlePlaneExit) treat a preempt/stop as a
		// clean cancel instead of an error — otherwise the error branch fires a
		// spurious OnStop that races the preempt path's own OnStop. When the
		// 10s accept deadline fires (ffmpeg never connected) ctx.Err() is nil,
		// so a genuine spawn failure still returns the raw error.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, acceptErr
	}

	p.videoPipe = videoConn
	p.audioPipe = audioConn
	p.watchContext(ctx)
	p.launchWaiter()
	return p, nil
}

func listenLoopback() (*net.TCPListener, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	tcp, ok := l.(*net.TCPListener)
	if !ok {
		_ = l.Close()
		return nil, fmt.Errorf("loopback listener is %T, not *net.TCPListener", l)
	}
	return tcp, nil
}

func tcpURL(l *net.TCPListener) string {
	return fmt.Sprintf("tcp://%s", l.Addr().String())
}

func acceptTCP(name string, l *net.TCPListener, ch chan<- acceptResult) {
	_ = l.SetDeadline(time.Now().Add(tcpAcceptTimeout))
	conn, err := l.Accept()
	if err != nil {
		ch <- acceptResult{name: name, err: fmt.Errorf("%s tcp accept: %w", name, err)}
		return
	}
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		_ = conn.Close()
		ch <- acceptResult{name: name, err: fmt.Errorf("%s tcp accept: got %T", name, conn)}
		return
	}
	_ = tcp.SetNoDelay(true)
	ch <- acceptResult{name: name, conn: tcp}
}

func closeListener(l *net.TCPListener) {
	if l != nil {
		_ = l.Close()
	}
}

func closeConn(c *net.TCPConn) {
	if c != nil {
		_ = c.Close()
	}
}
