package dataplane

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy/nlc"
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
	cmds     chan fakemister.Command
	fields   chan fakemister.FieldEvent
	plane    *Plane
	cancel   context.CancelFunc
	runErr   chan error
}

// startCoreSession runs a Frames plane against a fake MiSTer that
// impersonates the given core version. The field is 4x1 at 1 byte per pixel
// unless cfg sets FieldWidth (then cfg's FieldHeight and BytesPerPixel are
// used as given).
func startCoreSession(t *testing.T, version byte, idleTimeout time.Duration, cfg PlaneConfig) *coreSession {
	t.Helper()
	l, err := fakemister.NewListener("127.0.0.1:0")
	requireUDPSockets(t, err)
	l.EnableACKs(false)
	l.SetCoreVersion(version)
	l.SetIdleTimeout(idleTimeout)
	sender, err := groovynet.NewSender("127.0.0.1", l.Addr().(*net.UDPAddr).Port, 0)
	requireUDPSockets(t, err)

	if cfg.FieldWidth == 0 {
		cfg.FieldWidth, cfg.FieldHeight, cfg.BytesPerPixel = 4, 1, 1
	}
	fieldBytes := uint32(cfg.FieldWidth * cfg.FieldHeight * cfg.BytesPerPixel)
	cmds := make(chan fakemister.Command, 4096)
	fields := make(chan fakemister.FieldEvent, 4096)
	audios := make(chan fakemister.AudioEvent, 8)
	go l.RunWithFields(cmds, fields, audios, func() uint32 { return fieldBytes })

	cfg.Sender = sender
	cfg.Modeline = groovy.NTSC480i60
	cfg.RGBMode = groovy.RGBMode888
	plane := NewPlane(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- plane.Run(ctx) }()
	cs := &coreSession{listener: l, cmds: cmds, fields: fields, plane: plane, cancel: cancel, runErr: runErr}
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

// patternSource fills every frame with the same position-dependent bytes, so
// the two fields of a frame differ and a decoded field can be checked exactly.
type patternSource struct{}

func (patternSource) ReadFrame(dst []byte) {
	for i := range dst {
		dst[i] = byte(i*7 + (i/96)*13)
	}
}

// initFrom returns the session's INIT command from the fake-mister stream.
func (cs *coreSession) initFrom(t *testing.T, within time.Duration) fakemister.Command {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case c := <-cs.cmds:
			if c.Type == groovy.CmdInit {
				return c
			}
		case <-deadline:
			t.Fatal("no INIT within deadline")
		}
	}
}

// Design §5.2: on GroovyNLC with codec nlc, every field is a 12-byte
// compressed blit whose payload is the NLC encoding of that field; no raw,
// dup or delta header is ever sent.
func TestPlane_NLCSessionOnGroovyNLC(t *testing.T) {
	const w, h, bpp = 32, 4, 3
	for _, tc := range []struct {
		name string
		near int
		pack nlc.Pack
	}{
		{"tiled", 0, nlc.PackTiled},
		{"rice", 0, nlc.PackRice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := startCoreSession(t, 2, 0, PlaneConfig{
				Codec: CodecNLC, NLCNear: tc.near, NLCPack: tc.pack,
				FieldWidth: w, FieldHeight: h, BytesPerPixel: bpp,
				Frames: patternSource{},
			})
			frame := make([]byte, w*2*h*bpp)
			patternSource{}.ReadFrame(frame)
			params := nlc.Params{Width: w, Height: h, Near: tc.near, Pack: tc.pack}
			want := make([]byte, w*h*bpp)
			got := make([]byte, w*h*bpp)
			for i := 0; i < 8; i++ {
				var ev fakemister.FieldEvent
				select {
				case ev = <-cs.fields:
				case <-time.After(2 * time.Second):
					t.Fatalf("field %d did not arrive", i)
				}
				if !ev.Header.Compressed || ev.Header.Delta || ev.Header.Duplicate {
					t.Fatalf("field %d header = %+v, want a plain compressed blit", i, ev.Header)
				}
				if int(ev.Header.CompressedSize) != len(ev.Payload) {
					t.Fatalf("field %d: CompressedSize %d != payload %d", i, ev.Header.CompressedSize, len(ev.Payload))
				}
				if err := nlc.Decode(got, ev.Payload, params); err != nil {
					t.Fatalf("field %d: nlc.Decode: %v", i, err)
				}
				ExtractFieldFromFrameInto(want, frame, w, 2*h, bpp, ev.Header.Field)
				if !bytes.Equal(got, want) {
					t.Fatalf("field %d (parity %d): decoded payload differs from the source field", i, ev.Header.Field)
				}
			}
			if c := cs.plane.EffectiveCodec(); c != CodecNLC {
				t.Fatalf("EffectiveCodec() = %q, want nlc", c)
			}
			initCmd := cs.initFrom(t, time.Second)
			if want := groovy.NLCCompressionByte(tc.near, tc.pack == nlc.PackRice); initCmd.Init.LZ4Frames != want {
				t.Fatalf("INIT[1] = %#x, want %#x", initCmd.Init.LZ4Frames, want)
			}
			// Every BLIT header so far must be the 12-byte compressed form.
			blits := 0
			for drained := false; !drained; {
				select {
				case c := <-cs.cmds:
					if c.Type != groovy.CmdBlitFieldVSync {
						continue
					}
					blits++
					if n := len(c.Raw); n != groovy.BlitHeaderLZ4 {
						t.Fatalf("BLIT header length %d, want %d (no raw/dup/delta under NLC)", n, groovy.BlitHeaderLZ4)
					}
				default:
					drained = true
				}
			}
			if blits == 0 {
				t.Fatal("no BLIT headers observed")
			}
			if n := cs.listener.ZeroSizeLZ4Blits(); n != 0 {
				t.Fatalf("ZeroSizeLZ4Blits = %d, want 0", n)
			}
		})
	}
}

// Design §4.2: codec nlc on the original core falls back to LZ4, and INIT
// says so.
func TestPlane_NLCOnOriginalGroovyFallsBackToLZ4(t *testing.T) {
	cs := startCoreSession(t, 1, 0, PlaneConfig{Codec: CodecNLC, NLCNear: 2, NLCPack: nlc.PackRice, Frames: &fillSource{}})
	cs.waitField(t, 2*time.Second)
	if c := cs.plane.EffectiveCodec(); c != CodecLZ4 {
		t.Fatalf("EffectiveCodec() = %q, want lz4", c)
	}
	if initCmd := cs.initFrom(t, time.Second); initCmd.Init.LZ4Frames != groovy.LZ4ModeDefault {
		t.Fatalf("INIT[1] = %#x, want %#x", initCmd.Init.LZ4Frames, groovy.LZ4ModeDefault)
	}
}
