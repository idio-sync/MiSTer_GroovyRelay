package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProcess_LogWaitResultUsesDebugForIntentionalStop(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(old) })

	p := &Process{}
	p.stopRequested.Store(true)
	p.logWaitResult(errors.New("signal: killed"))

	got := buf.String()
	if !strings.Contains(got, "level=DEBUG") {
		t.Fatalf("expected debug log, got %q", got)
	}
	if !strings.Contains(got, "ffmpeg stopped during session teardown") {
		t.Fatalf("expected teardown message, got %q", got)
	}
	if strings.Contains(got, "ffmpeg exited") {
		t.Fatalf("unexpected ffmpeg exited warning: %q", got)
	}
}

func TestProcess_WatchContextMarksContextCancelAsIntentionalStop(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(old) })

	ctx, cancel := context.WithCancel(context.Background())
	p := &Process{stopped: make(chan struct{})}
	p.watchContext(ctx)
	cancel()

	deadline := time.Now().Add(500 * time.Millisecond)
	for !p.stopRequested.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !p.stopRequested.Load() {
		t.Fatal("context cancel did not mark stopRequested")
	}

	p.logWaitResult(errors.New("signal: killed"))

	got := buf.String()
	if !strings.Contains(got, "level=DEBUG") {
		t.Fatalf("expected debug log, got %q", got)
	}
	if strings.Contains(got, "ffmpeg exited") {
		t.Fatalf("unexpected ffmpeg exited warning: %q", got)
	}
}

func TestProcess_LogWaitResultWarnsOnUnexpectedExit(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(old) })

	p := &Process{}
	p.logWaitResult(errors.New("boom"))

	got := buf.String()
	if !strings.Contains(got, "level=WARN") {
		t.Fatalf("expected warn log, got %q", got)
	}
	if !strings.Contains(got, "ffmpeg exited") {
		t.Fatalf("expected ffmpeg exited warning, got %q", got)
	}
}

// TestProcess_BasicLifecycle drives Spawn's lifecycle wiring without relying
// on ffmpeg or on ExtraFiles (which is a Linux/Unix-only feature — cmd.Start
// errors out when ExtraFiles is set on Windows).
//
// We exercise the bookkeeping directly: build a Process wrapping a trivial
// command, confirm Done() fires after Wait, Stop() is idempotent, and the
// pipe readers return io.EOF once the child exits.
func TestProcess_BasicLifecycle(t *testing.T) {
	// A no-op command that exits 0 immediately on either platform.
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "exit", "0")
	default:
		cmd = exec.Command("true")
	}
	videoR, videoW, err := os.Pipe()
	if err != nil {
		t.Fatalf("video pipe: %v", err)
	}
	audioR, audioW, err := os.Pipe()
	if err != nil {
		t.Fatalf("audio pipe: %v", err)
	}
	// Close the write ends so read-side sees EOF after the child "runs".
	videoW.Close()
	audioW.Close()

	p := newProcess(cmd, videoR, audioR)
	if err := p.start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Done() must close after Wait() returns.
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done() did not close within 5s")
	}

	// Pipe readers are the public contract.
	if got := p.VideoPipe(); got == nil {
		t.Error("VideoPipe nil")
	}
	if got := p.AudioPipe(); got == nil {
		t.Error("AudioPipe nil")
	}

	// Reading after child exit should yield EOF promptly.
	buf := make([]byte, 1)
	if _, err := p.VideoPipe().Read(buf); err != io.EOF {
		t.Errorf("VideoPipe read after exit: want EOF, got %v", err)
	}

	// Stop() must be idempotent and not panic after the child has exited.
	p.Stop()
	p.Stop()
}

// TestProcess_SpawnsFFmpeg is the "live" end-to-end test: Spawn a real ffmpeg
// and read the bytes it emits through the platform pipe transport. Unix uses
// ExtraFiles; Windows uses the loopback TCP shim.
func TestProcess_SpawnsFFmpeg(t *testing.T) {
	if findFFBinary("ffmpeg") == "" {
		t.Skip("ffmpeg not findable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	samplePath := filepath.Join("..", "..", "tests", "integration", "testdata", "url", "tiny.mp4")
	if _, err := os.Stat(samplePath); err != nil {
		t.Skipf("fixture missing: %v", err)
	}

	spec := PipelineSpec{
		InputURL:        samplePath,
		InputHeaders:    nil,
		SourceProbe:     &ProbeResult{Width: 320, Height: 240, FrameRate: 24, Interlaced: false},
		OutputWidth:     720,
		OutputHeight:    480,
		FieldOrder:      "tff",
		AspectMode:      "letterbox",
		AudioSampleRate: 48000,
		AudioChannels:   2,
	}

	p, err := Spawn(ctx, spec)
	if err != nil {
		t.Skipf("Spawn failed (environment-dependent): %v", err)
	}
	// Drain until EOF or timeout.
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		_, _ = io.Copy(io.Discard, p.VideoPipe())
	}()
	select {
	case <-p.Done():
	case <-ctx.Done():
		p.Stop()
		t.Fatal("ffmpeg did not exit within timeout")
	}
	<-drainDone
}
