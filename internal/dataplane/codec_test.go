package dataplane

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy/nlc"
)

func TestResolveCodec(t *testing.T) {
	cases := []struct {
		in   Codec
		core groovy.Core
		want Codec
		warn bool
	}{
		{CodecAuto, groovy.CoreGroovy, CodecLZ4, false},
		{CodecAuto, groovy.CoreGroovyNLC, CodecLZ4, false}, // §4.2: NLC opt-in only
		{CodecLZ4, groovy.CoreGroovyNLC, CodecLZ4, false},
		{CodecRaw, groovy.CoreGroovyNLC, CodecRaw, false},
		{"", groovy.CoreGroovy, CodecRaw, false}, // bare PlaneConfig keeps today's raw default
		{"bogus", groovy.CoreGroovy, CodecLZ4, true},
		{CodecNLC, groovy.CoreGroovyNLC, CodecNLC, false},
		{CodecNLC, groovy.CoreGroovy, CodecLZ4, true},
		{CodecNLC, groovy.CoreUnknown, CodecLZ4, false}, // provisional NewPlane resolution
		{CodecAuto, groovy.CoreUnknown, CodecLZ4, false},
		{CodecLZ4, groovy.CoreGroovy, CodecLZ4, false},
		{CodecLZ4, groovy.CoreUnknown, CodecLZ4, false},
		{CodecRaw, groovy.CoreGroovy, CodecRaw, false},
		{CodecRaw, groovy.CoreUnknown, CodecRaw, false},
	}
	for _, c := range cases {
		got, warn := ResolveCodec(c.in, c.core)
		if got != c.want || (warn != "") != c.warn {
			t.Errorf("ResolveCodec(%q, %v) = %q, %q; want %q, warn=%v", c.in, c.core, got, warn, c.want, c.warn)
		}
	}
}

func TestResolveCodec_NLCOnOriginalCoreWarning(t *testing.T) {
	if _, warn := ResolveCodec(CodecNLC, groovy.CoreGroovy); warn != "codec nlc needs the GroovyNLC core; using lz4" {
		t.Fatalf("warning = %q", warn)
	}
}

func TestInitCompressionByte(t *testing.T) {
	if initCompressionByte(CodecLZ4, 3, true) != groovy.LZ4ModeDefault || initCompressionByte(CodecRaw, 3, true) != groovy.LZ4ModeOff {
		t.Fatal("unexpected INIT compression byte")
	}
	for _, c := range []struct {
		near int
		rice bool
	}{{0, false}, {3, false}, {2, true}} {
		if got, want := initCompressionByte(CodecNLC, c.near, c.rice), groovy.NLCCompressionByte(c.near, c.rice); got != want {
			t.Errorf("initCompressionByte(nlc, %d, %v) = %#x, want %#x", c.near, c.rice, got, want)
		}
	}
}

// nlcTestPlane is a bare Plane (no sockets, no frame pool) for exercising
// applySessionCodec and the sendField NLC path directly.
func nlcTestPlane(cfg PlaneConfig) *Plane {
	p := &Plane{cfg: cfg, headerScratch: make([]byte, groovy.BlitHeaderLZ4Delta), periodMsNumer: 1001, periodMsDenom: 60}
	p.codec, _ = ResolveCodec(cfg.Codec, groovy.CoreUnknown)
	p.effCodec.Store(p.codec)
	return p
}

func TestApplySessionCodec_NLC(t *testing.T) {
	p := nlcTestPlane(PlaneConfig{Codec: CodecNLC, FieldWidth: 32, FieldHeight: 4, BytesPerPixel: 3, NLCNear: 1, NLCPack: nlc.PackRice})
	p.deltaLZ4Enabled = true // provisional LZ4 may have enabled delta
	p.fieldPrev = [2][]byte{make([]byte, 384), make([]byte, 384)}
	p.fieldDeltaScratch, p.fieldDeltaLZ4Scratch = make([]byte, 384), make([]byte, 512)
	p.applySessionCodec(groovy.CoreGroovyNLC)
	if p.codec != CodecNLC || p.EffectiveCodec() != CodecNLC {
		t.Fatalf("codec = %q, effective = %q; want nlc", p.codec, p.EffectiveCodec())
	}
	if p.deltaLZ4Enabled {
		t.Fatal("delta-LZ4 still enabled under NLC")
	}
	if p.fieldPrev[0] != nil || p.fieldPrev[1] != nil || p.fieldDeltaScratch != nil || p.fieldDeltaLZ4Scratch != nil {
		t.Fatal("delta history kept under NLC without field diagnostics")
	}
	params := nlc.Params{Width: 32, Height: 4, Near: 1, Pack: nlc.PackRice}
	if p.nlcEnc == nil || len(p.nlcScratch) != nlc.MaxEncodedSize(params) {
		t.Fatalf("encoder = %v, scratch = %d; want an encoder and %d bytes", p.nlcEnc, len(p.nlcScratch), nlc.MaxEncodedSize(params))
	}
	if got, want := p.initCompressionByte(), groovy.NLCCompressionByte(1, true); got != want {
		t.Fatalf("INIT[1] = %#x, want %#x", got, want)
	}
	enc := p.nlcEnc
	p.applySessionCodec(groovy.CoreGroovyNLC)
	if p.nlcEnc != enc {
		t.Fatal("encoder re-allocated; want one per Plane")
	}
}

func TestApplySessionCodec_NLCOnOriginalCoreKeepsLZ4(t *testing.T) {
	p := nlcTestPlane(PlaneConfig{Codec: CodecNLC, FieldWidth: 32, FieldHeight: 4, BytesPerPixel: 3})
	p.deltaLZ4Enabled = true
	p.applySessionCodec(groovy.CoreGroovy)
	if p.EffectiveCodec() != CodecLZ4 || !p.deltaLZ4Enabled || p.nlcEnc != nil {
		t.Fatalf("effective = %q, delta = %v, encoder = %v; want lz4, delta kept, no encoder",
			p.EffectiveCodec(), p.deltaLZ4Enabled, p.nlcEnc)
	}
	if got := p.initCompressionByte(); got != groovy.LZ4ModeDefault {
		t.Fatalf("INIT[1] = %#x, want LZ4", got)
	}
}

// §4.2: an encoder that cannot be built falls back to LZ4 for the session,
// in both the published codec and the INIT byte.
func TestApplySessionCodec_EncoderFailureFallsBackToLZ4(t *testing.T) {
	for name, cfg := range map[string]PlaneConfig{
		"oversize field": {Codec: CodecNLC, FieldWidth: 5000, FieldHeight: 240, BytesPerPixel: 3},
		"not rgb888":     {Codec: CodecNLC, FieldWidth: 32, FieldHeight: 4, BytesPerPixel: 4},
		"invalid near":   {Codec: CodecNLC, FieldWidth: 32, FieldHeight: 4, BytesPerPixel: 3, NLCNear: 7},
	} {
		t.Run(name, func(t *testing.T) {
			p := nlcTestPlane(cfg)
			p.applySessionCodec(groovy.CoreGroovyNLC)
			if p.codec != CodecLZ4 || p.EffectiveCodec() != CodecLZ4 {
				t.Fatalf("codec = %q, effective = %q; want lz4 fallback", p.codec, p.EffectiveCodec())
			}
			if p.nlcEnc != nil || p.nlcScratch != nil {
				t.Fatal("NLC scratch allocated despite the fallback")
			}
			if got := p.initCompressionByte(); got != groovy.LZ4ModeDefault {
				t.Fatalf("INIT[1] = %#x, want LZ4", got)
			}
		})
	}
}

// A progressive session whose ffmpeg output height diverges from
// FieldHeight (SpawnSpec.OutputHeight set independently) would hand
// sendField a payload the FieldHeight-sized encoder can't accept on every
// tick. ensureNLCEncoder must catch that mismatch up front and fall back
// to LZ4, the same as any other encoder-build failure.
func TestApplySessionCodec_ProgressiveHeightMismatchFallsBackToLZ4(t *testing.T) {
	cfg := PlaneConfig{
		Codec:         CodecNLC,
		FieldWidth:    32,
		FieldHeight:   4,
		BytesPerPixel: 3,
	}
	cfg.SpawnSpec.OutputHeight = 8 // != FieldHeight, so resolveVideoHeight() diverges
	p := nlcTestPlane(cfg)
	p.applySessionCodec(groovy.CoreGroovyNLC)
	if p.codec != CodecLZ4 || p.EffectiveCodec() != CodecLZ4 {
		t.Fatalf("codec = %q, effective = %q; want lz4 fallback", p.codec, p.EffectiveCodec())
	}
	if p.nlcEnc != nil || p.nlcScratch != nil {
		t.Fatal("NLC scratch allocated despite the payload-size mismatch")
	}
	if got := p.initCompressionByte(); got != groovy.LZ4ModeDefault {
		t.Fatalf("INIT[1] = %#x, want LZ4", got)
	}
}

// §5.2 step 5: an encoder error skips the field: nothing on the wire, the
// error counted, and stats with no payload.
func TestSendField_NLCEncodeErrorSendsNothing(t *testing.T) {
	p := nlcTestPlane(PlaneConfig{Codec: CodecNLC, FieldWidth: 4, FieldHeight: 1, BytesPerPixel: 3})
	p.applySessionCodec(groovy.CoreGroovyNLC)
	sender := &scriptedFieldSender{}
	p.fieldSender = sender
	for i := 0; i < 3; i++ {
		stats := p.sendField(uint32(i+1), 0, make([]byte, 5)) // not FrameBytes: ErrSrcSize
		if stats.payloadBytes != 0 || stats.compressedBytes != 0 || stats.wireBytes != 0 {
			t.Fatalf("stats = %+v, want no payload", stats)
		}
		if !stats.skipped {
			t.Fatalf("stats.skipped = false, want true so the tick loop excludes this field from its counters")
		}
	}
	if len(sender.headers) != 0 || len(sender.payloads) != 0 {
		t.Fatalf("sent %d headers, %d payloads; want none", len(sender.headers), len(sender.payloads))
	}
	if n := p.nlcEncodeErrors.Load(); n != 3 {
		t.Fatalf("nlcEncodeErrors = %d, want 3", n)
	}
	if p.WireBytes() != 0 {
		t.Fatalf("WireBytes = %d, want 0", p.WireBytes())
	}
	// Matching holdField's underrun hold: MarkBlitSent(0) resets the
	// congestion window even though nothing went on the wire.
	if want := []int{0, 0, 0}; !equalInts(sender.markBlitSentArgs, want) {
		t.Fatalf("MarkBlitSent calls = %v, want %v", sender.markBlitSentArgs, want)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSendField_NLCSendsCompressedBlit(t *testing.T) {
	p := nlcTestPlane(PlaneConfig{Codec: CodecNLC, FieldWidth: 4, FieldHeight: 2, BytesPerPixel: 3})
	p.applySessionCodec(groovy.CoreGroovyNLC)
	sender := &scriptedFieldSender{}
	p.fieldSender = sender
	raw := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24}
	stats := p.sendField(7, 1, raw)
	if len(sender.headers) != 1 || len(sender.payloads) != 1 {
		t.Fatalf("sent %d headers, %d payloads; want 1 each", len(sender.headers), len(sender.payloads))
	}
	hdr, payload := sender.headers[0], sender.payloads[0]
	if len(hdr) != groovy.BlitHeaderLZ4 {
		t.Fatalf("header length %d, want %d", len(hdr), groovy.BlitHeaderLZ4)
	}
	if got := binary.LittleEndian.Uint32(hdr[8:12]); int(got) != len(payload) {
		t.Fatalf("header size %d, payload %d", got, len(payload))
	}
	if stats.compressedBytes != len(payload) || stats.payloadBytes != len(payload) {
		t.Fatalf("stats = %+v, payload %d", stats, len(payload))
	}
	dec := make([]byte, len(raw))
	if err := nlc.Decode(dec, payload, nlc.Params{Width: 4, Height: 2}); err != nil || !bytes.Equal(dec, raw) {
		t.Fatalf("decode = %v, %v; want %v", dec, err, raw)
	}
}

func TestPlane_InitCompressionFollowsCodec(t *testing.T) {
	for codec, want := range map[Codec]byte{
		CodecLZ4:  groovy.LZ4ModeDefault,
		CodecAuto: groovy.LZ4ModeDefault,
		CodecRaw:  groovy.LZ4ModeOff,
		"":        groovy.LZ4ModeOff,
		CodecNLC:  groovy.LZ4ModeDefault, // the fake MiSTer reports the original core
	} {
		initCmd, _, _ := runPlaneUntilInit(t, PlaneConfig{Codec: codec})
		if initCmd.Init == nil || initCmd.Init.LZ4Frames != want {
			t.Errorf("codec %q: INIT[1] = %v, want %d", codec, initCmd.Init, want)
		}
	}
}
