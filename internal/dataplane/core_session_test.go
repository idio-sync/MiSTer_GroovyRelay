package dataplane

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
)

// slowStartSource withholds its first frame for delay, then fills frames
// instantly, which models a slow transcode start inside the prebuffer.
type slowStartSource struct {
	delay   time.Duration
	started atomic.Bool
}

func (s *slowStartSource) ReadFrame(dst []byte) {
	if !s.started.Swap(true) {
		time.Sleep(s.delay)
	}
	for i := range dst {
		dst[i] = 0x33
	}
}

type coreSession struct {
	listener *fakemister.Listener
	fields   chan fakemister.FieldEvent
	plane    *Plane
	cancel   context.CancelFunc
	runErr   chan error
}

// startCoreSession runs a Frames plane against a fake MiSTer that
// impersonates the given core version.
func startCoreSession(t *testing.T, version byte, idleTimeout time.Duration, cfg PlaneConfig) *coreSession {
	t.Helper()
	l, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)
	l.EnableACKs(false)
	l.SetCoreVersion(version)
	l.SetIdleTimeout(idleTimeout)
	sender, err := groovynet.NewSender("127.0.0.1", l.Addr().(*net.UDPAddr).Port, 0)
	requireUDPSockets(t, err)

	const fieldBytes = 4 * 1 * 1
	cmds := make(chan fakemister.Command, 4096)
	fields := make(chan fakemister.FieldEvent, 4096)
	audios := make(chan fakemister.AudioEvent, 8)
	go l.RunWithFields(cmds, fields, audios, func() uint32 { return fieldBytes })

	cfg.Sender = sender
	cfg.Modeline = groovy.NTSC480i60
	cfg.FieldWidth, cfg.FieldHeight, cfg.BytesPerPixel = 4, 1, 1
	cfg.RGBMode = groovy.RGBMode888
	plane := NewPlane(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- plane.Run(ctx) }()
	cs := &coreSession{listener: l, fields: fields, plane: plane, cancel: cancel, runErr: runErr}
	t.Cleanup(func() {
		cancel()
		if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Plane.Run() = %v", err)
		}
		_ = sender.Close()
		_ = l.Close()
	})
	return cs
}

func (cs *coreSession) waitField(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case <-cs.fields:
	case <-time.After(within):
		t.Fatalf("no field within %v (reaps=%d)", within, cs.listener.Reaps())
	}
}

func TestPlane_DetectsGroovyNLC(t *testing.T) {
	cs := startCoreSession(t, 2, 0, PlaneConfig{Codec: CodecAuto, Frames: &fillSource{}})
	cs.waitField(t, 2*time.Second)
	if cs.plane.Core() != groovy.CoreGroovyNLC {
		t.Fatalf("Core() = %v, want groovynlc", cs.plane.Core())
	}
	if cs.plane.EffectiveCodec() != CodecLZ4 {
		t.Fatalf("EffectiveCodec() = %q, want lz4 (auto stays LZ4, §4.2)", cs.plane.EffectiveCodec())
	}
}

func TestPlane_DetectsOriginalGroovy(t *testing.T) {
	cs := startCoreSession(t, 1, 0, PlaneConfig{Codec: CodecAuto, Frames: &fillSource{}})
	cs.waitField(t, 2*time.Second)
	if cs.plane.Core() != groovy.CoreGroovy {
		t.Fatalf("Core() = %v, want groovy", cs.plane.Core())
	}
}

// Design §1.2 / §3.3: the prebuffer is the only silent window. With the
// keepalive, a fork core with a short idle timeout keeps the session.
func TestPlane_KeepaliveSurvivesSlowPrebuffer(t *testing.T) {
	cs := startCoreSession(t, 2, 400*time.Millisecond, PlaneConfig{
		Frames:        &slowStartSource{delay: 1500 * time.Millisecond},
		KeepaliveIdle: 100 * time.Millisecond,
	})
	cs.waitField(t, 4*time.Second)
	if n := cs.listener.Reaps(); n != 0 {
		t.Fatalf("session reaped %d times despite keepalive", n)
	}
}

// Negative control: the same scenario without a keepalive is reaped,
// which proves the test above is sensitive.
func TestPlane_NoKeepaliveIsReapedDuringSlowPrebuffer(t *testing.T) {
	cs := startCoreSession(t, 2, 400*time.Millisecond, PlaneConfig{
		Frames:        &slowStartSource{delay: 1500 * time.Millisecond},
		KeepaliveIdle: time.Hour,
	})
	deadline := time.Now().Add(4 * time.Second)
	for cs.listener.Reaps() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if cs.listener.Reaps() == 0 {
		t.Fatal("expected the idle core to reap a silent prebuffer")
	}
}
