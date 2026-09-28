package fakemister

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// lz4ModeRig runs RunWithFields and hands back a connected client socket.
type lz4ModeRig struct {
	l      *Listener
	conn   *net.UDPConn
	cmds   chan Command
	fields chan FieldEvent
}

func newLZ4ModeRig(t *testing.T, fieldBytes uint32) *lz4ModeRig {
	t.Helper()
	l, err := NewListener("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &lz4ModeRig{
		l:      l,
		cmds:   make(chan Command, 64),
		fields: make(chan FieldEvent, 16),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.RunWithFields(r.cmds, r.fields, make(chan AudioEvent, 4), func() uint32 { return fieldBytes })
	}()
	conn, err := net.DialUDP("udp", nil, l.Addr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	r.conn = conn
	t.Cleanup(func() {
		conn.Close()
		l.Close()
		<-done
	})
	return r
}

func (r *lz4ModeRig) send(t *testing.T, pkts ...[]byte) {
	t.Helper()
	for _, p := range pkts {
		if _, err := r.conn.Write(p); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *lz4ModeRig) sendPayload(t *testing.T, payload []byte) {
	t.Helper()
	for i := 0; i < len(payload); i += groovy.MaxDatagram {
		end := min(i+groovy.MaxDatagram, len(payload))
		r.send(t, payload[i:end])
	}
}

func (r *lz4ModeRig) awaitField(t *testing.T) FieldEvent {
	t.Helper()
	select {
	case fe := <-r.fields:
		return fe
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for field event")
		return FieldEvent{}
	}
}

// awaitBlitCmd returns the next BLIT_FIELD_VSYNC command, skipping others.
func (r *lz4ModeRig) awaitBlitCmd(t *testing.T) Command {
	t.Helper()
	for {
		select {
		case c := <-r.cmds:
			if c.Type == groovy.CmdBlitFieldVSync {
				return c
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for BLIT command")
			return Command{}
		}
	}
}

func compressibleField(n int) []byte {
	f := make([]byte, n)
	for i := range f {
		f[i] = byte(i % 13)
	}
	return f
}

func lz4Blit(t *testing.T, frame uint32, field []byte) ([]byte, []byte) {
	t.Helper()
	payload, ok := groovy.LZ4Compress(field)
	if !ok {
		t.Fatal("test field did not compress")
	}
	hdr := groovy.BuildBlitHeader(groovy.BlitOpts{
		Frame: frame, Compressed: true, CompressedSize: uint32(len(payload)),
	})
	return hdr, payload
}

// Under LZ4 the real core ignores the dup flag of a 9-byte header and arms
// a compressed blit of size 0; the next short datagram trips its lost-packet
// abort and is then re-read as a command, so the following field survives.
func TestRunWithFields_LZ4SessionDupHeaderArmsZeroSizeBlit(t *testing.T) {
	const fieldBytes = 4000
	r := newLZ4ModeRig(t, fieldBytes)
	r.send(t, groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRateOff, 0, groovy.RGBMode888))
	r.send(t, groovy.BuildBlitHeader(groovy.BlitOpts{Frame: 1, Duplicate: true}))

	dup := r.awaitBlitCmd(t)
	if dup.Blit.Duplicate || !dup.Blit.Compressed || dup.Blit.CompressedSize != 0 {
		t.Fatalf("9-byte header under LZ4 parsed as %+v, want compressed size 0 (core view)", *dup.Blit)
	}

	field := compressibleField(fieldBytes)
	hdr, payload := lz4Blit(t, 2, field)
	r.send(t, hdr)
	r.sendPayload(t, payload)
	fe := r.awaitField(t)
	if fe.Header.Frame != 2 || !bytes.Equal(fe.Payload, payload) {
		t.Fatalf("field after zero-size blit = frame %d, %d bytes; want frame 2 intact", fe.Header.Frame, len(fe.Payload))
	}
	if got := r.l.ZeroSizeLZ4Blits(); got != 1 {
		t.Fatalf("ZeroSizeLZ4Blits = %d, want 1", got)
	}
}

// An 8-byte RAW header under LZ4 is also a zero-size compressed blit; the
// RAW payload's first full chunk is swallowed as LZ4 data and the rest is
// command-channel noise. No field is delivered for it.
func TestRunWithFields_LZ4SessionRawHeaderLosesField(t *testing.T) {
	const fieldBytes = 4000
	r := newLZ4ModeRig(t, fieldBytes)
	r.send(t, groovy.BuildInit(groovy.LZ4ModeDefault, groovy.AudioRateOff, 0, groovy.RGBMode888))
	r.send(t, groovy.BuildBlitHeader(groovy.BlitOpts{Frame: 1}))
	raw := bytes.Repeat([]byte{groovy.CmdInit}, fieldBytes) // payload bytes that look like INIT
	r.sendPayload(t, raw)

	field := compressibleField(fieldBytes)
	hdr, payload := lz4Blit(t, 2, field)
	r.send(t, hdr)
	r.sendPayload(t, payload)
	fe := r.awaitField(t)
	if fe.Header.Frame != 2 {
		t.Fatalf("first delivered field = frame %d, want 2 (RAW field under LZ4 must be lost)", fe.Header.Frame)
	}
	if got := r.l.ZeroSizeLZ4Blits(); got != 1 {
		t.Fatalf("ZeroSizeLZ4Blits = %d, want 1", got)
	}
	for len(r.cmds) > 0 {
		if c := <-r.cmds; c.Type == groovy.CmdInit && len(c.Raw) > 26 {
			t.Fatalf("RAW payload chunk (%d bytes) parsed as INIT", len(c.Raw))
		}
	}
}

// With compression off the same headers keep their RAW meaning.
func TestRunWithFields_RawSessionHonoursDupAndRawHeaders(t *testing.T) {
	const fieldBytes = 100
	r := newLZ4ModeRig(t, fieldBytes)
	r.send(t, groovy.BuildInit(groovy.LZ4ModeOff, groovy.AudioRateOff, 0, groovy.RGBMode888))
	r.send(t, groovy.BuildBlitHeader(groovy.BlitOpts{Frame: 1, Duplicate: true}))
	if dup := r.awaitBlitCmd(t); !dup.Blit.Duplicate {
		t.Fatalf("9-byte header on a RAW session parsed as %+v, want duplicate", *dup.Blit)
	}
	r.send(t, groovy.BuildBlitHeader(groovy.BlitOpts{Frame: 2}))
	r.sendPayload(t, compressibleField(fieldBytes))
	if fe := r.awaitField(t); fe.Header.Frame != 2 || len(fe.Payload) != fieldBytes {
		t.Fatalf("RAW field = frame %d, %d bytes", fe.Header.Frame, len(fe.Payload))
	}
	if got := r.l.ZeroSizeLZ4Blits(); got != 0 {
		t.Fatalf("ZeroSizeLZ4Blits = %d on a RAW session, want 0", got)
	}
}
