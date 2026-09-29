package nlc

import (
	"errors"
	"testing"
)

func findCase(t testing.TB, name string) goldenCase {
	t.Helper()
	for _, c := range loadGolden(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("golden case %q not found", name)
	return goldenCase{}
}

func TestParamsValidate(t *testing.T) {
	good := Params{Width: 720, Height: 240, Near: 0, Pack: PackTiled}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	if err := (Params{Width: MaxWidth, Height: MaxHeight}).Validate(); err != nil {
		t.Fatalf("params at the size caps rejected: %v", err)
	}
	bad := []Params{
		{Width: 0, Height: 240},
		{Width: 720, Height: 0},
		{Width: -1, Height: 240},
		{Width: 720, Height: -5},
		{Width: 720, Height: 240, Near: -1},
		{Width: 720, Height: 240, Near: 4},
		{Width: 720, Height: 240, Pack: 2},
		{Width: 720, Height: 240, Pack: 255},
		{Width: MaxWidth + 1, Height: 240},  // a bogus modeline must not OOM
		{Width: 720, Height: MaxHeight + 1}, // (review follow-up)
	}
	for _, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want error", p)
		}
		if _, err := NewEncoder(p); err == nil {
			t.Errorf("NewEncoder(%+v) = nil error, want error", p)
		}
		if err := Decode(make([]byte, 16), make([]byte, 16), p); err == nil {
			t.Errorf("Decode with %+v = nil error, want error", p)
		}
	}
}

func TestSizes(t *testing.T) {
	p := Params{Width: 720, Height: 240}
	if got, want := FrameBytes(p), 720*240*3; got != want {
		t.Errorf("FrameBytes = %d, want %d", got, want)
	}
	// 2*raw + W*H + H*40 + 1024, as nlc_max_encoded_size.
	if got, want := MaxEncodedSize(p), 2*720*240*3+720*240+240*40+1024; got != want {
		t.Errorf("MaxEncodedSize = %d, want %d", got, want)
	}
}

func TestEncodeIntoErrors(t *testing.T) {
	p := Params{Width: 96, Height: 24, Near: 0, Pack: PackRice}
	enc, err := NewEncoder(p)
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]byte, MaxEncodedSize(p))
	for _, n := range []int{0, FrameBytes(p) - 1, FrameBytes(p) + 1} {
		if _, err := enc.EncodeInto(dst, make([]byte, n)); !errors.Is(err, ErrSrcSize) {
			t.Errorf("src len %d: err = %v, want ErrSrcSize", n, err)
		}
	}
	src := make([]byte, FrameBytes(p))
	if _, err := enc.EncodeInto(make([]byte, MaxEncodedSize(p)-1), src); !errors.Is(err, ErrDstTooSmall) {
		t.Errorf("short dst: err = %v, want ErrDstTooSmall", err)
	}
	if _, err := enc.EncodeInto(nil, src); !errors.Is(err, ErrDstTooSmall) {
		t.Errorf("nil dst: err = %v, want ErrDstTooSmall", err)
	}
	// The encoder still works after rejected calls.
	if _, err := enc.EncodeInto(dst, src); err != nil {
		t.Errorf("EncodeInto after errors: %v", err)
	}
}

func TestDecodeErrors(t *testing.T) {
	for _, name := range []string{"noise_n0_rice", "noise_n0_tiled", "odd_n2_rice", "edges_n1_tiled"} {
		t.Run(name, func(t *testing.T) {
			c := findCase(t, name)
			p := c.params(t)
			enc := goldenEncoded(t, c)
			out := make([]byte, FrameBytes(p))

			// Wrong dst size.
			for _, n := range []int{0, FrameBytes(p) - 1, FrameBytes(p) + 1} {
				if err := Decode(make([]byte, n), enc, p); !errors.Is(err, ErrDstSize) {
					t.Errorf("dst len %d: err = %v, want ErrDstSize", n, err)
				}
			}

			// Every proper prefix of a valid stream is rejected.
			for n := 0; n < len(enc); n++ {
				if err := Decode(out, enc[:n], p); err == nil {
					t.Fatalf("truncated to %d of %d bytes: Decode returned nil", n, len(enc))
				}
			}

			// Trailing garbage is rejected.
			if err := Decode(out, append(append([]byte{}, enc...), 0), p); err == nil {
				t.Error("trailing byte: Decode returned nil")
			}

			// A zero-length plane-0 segment on line 0 makes its reader overrun.
			bad := append([]byte{}, enc...)
			bad[0], bad[1] = 0, 0
			if err := Decode(out, bad, p); err == nil {
				t.Error("zeroed plane-0 length: Decode returned nil")
			}

			// A segment length pointing past the end is rejected.
			bad = append([]byte{}, enc...)
			bad[2], bad[3] = 0xff, 0xff
			if err := Decode(out, bad, p); err == nil {
				t.Error("oversized plane-1 length: Decode returned nil")
			}

			// Garbage bytes: any outcome but a panic is acceptable.
			bad = append([]byte{}, enc...)
			for i := headerBytes; i < len(bad); i += 7 {
				bad[i] ^= 0x5a
			}
			_ = Decode(out, bad, p)

			// The valid stream still decodes.
			if err := Decode(out, enc, p); err != nil {
				t.Errorf("valid stream: %v", err)
			}
		})
	}
}

// TestRiceEscape exercises the escape branch of rice_put/rice_get, which the
// golden vectors cannot reach: with adaptive k, every u in a tile of T <= 16
// satisfies u <= sum <= T<<k, so q = u>>k <= 16 < NLC_RICE_LIMIT.
func TestRiceEscape(t *testing.T) {
	buf := make([]byte, 1<<16)
	for k := 0; k <= 15; k++ {
		var vals []uint32
		for u := uint32(0); u < 4096; u += 1 + u/64 {
			vals = append(vals, u)
		}
		vals = append(vals, 4095, uint32(riceLimit)<<uint(k), uint32(riceLimit)<<uint(k)-1)
		var bw bitW
		bw.init(buf)
		wantBits := 0
		for _, u := range vals {
			if u>>uint(k) >= riceLimit && u >= 1<<riceUBits {
				continue // the escape payload is 12 bits wide; not representable
			}
			ricePut(&bw, u, k)
			if q := u >> uint(k); q < riceLimit {
				wantBits += int(q) + 1 + k
			} else {
				wantBits += riceLimit + 1 + riceUBits
			}
		}
		n := bw.finish()
		if bw.ovf || n != (wantBits+7)/8 {
			t.Fatalf("k=%d: wrote %d bytes (ovf=%v), want %d", k, n, bw.ovf, (wantBits+7)/8)
		}
		var br bitR
		br.init(buf[:n])
		for _, u := range vals {
			if u>>uint(k) >= riceLimit && u >= 1<<riceUBits {
				continue
			}
			if got := riceGet(&br, k); got != u {
				t.Fatalf("k=%d: riceGet = %d, want %d", k, got, u)
			}
		}
		if br.overrun {
			t.Fatalf("k=%d: reader overran", k)
		}
	}
}

// TestPrimitives pins a few hand-checked values of the scalar helpers.
func TestPrimitives(t *testing.T) {
	for v := -1100; v <= 1100; v++ {
		if got := unzz(zz(v)); got != v {
			t.Fatalf("unzz(zz(%d)) = %d", v, got)
		}
	}
	if zz(0) != 0 || zz(-1) != 1 || zz(1) != 2 || zz(-2) != 3 {
		t.Error("zz order wrong")
	}
	for r := 0; r < 256; r += 5 {
		for g := 0; g < 256; g += 3 {
			for b := 0; b < 256; b += 7 {
				y, co, cg := rgbToYCoCg(r, g, b)
				if y < 0 || y > 255 || co < -255 || co > 255 || cg < -255 || cg > 255 {
					t.Fatalf("ycocg(%d,%d,%d) = %d,%d,%d out of range", r, g, b, y, co, cg)
				}
				if r2, g2, b2 := ycocgToRGB(y, co, cg); r2 != r || g2 != g || b2 != b {
					t.Fatalf("ycocg round trip (%d,%d,%d) -> (%d,%d,%d)", r, g, b, r2, g2, b2)
				}
			}
		}
	}
	// nl_quant: symmetric rounding to the nearest multiple of 2*near+1.
	for _, tc := range []struct{ e, near, want int }{
		{0, 1, 0}, {1, 1, 0}, {2, 1, 1}, {-2, 1, -1}, {4, 1, 1}, {5, 1, 2},
		{2, 2, 0}, {3, 2, 1}, {-7, 2, -1}, {-8, 2, -2}, {3, 3, 0}, {4, 3, 1}, {-11, 3, -2},
		{17, 0, 17}, {-17, 0, -17},
	} {
		if got := nlQuant(tc.e, tc.near); got != tc.want {
			t.Errorf("nlQuant(%d, %d) = %d, want %d", tc.e, tc.near, got, tc.want)
		}
	}
	if medPredict(10, 20, 25) != 10 || medPredict(10, 20, 5) != 20 || medPredict(10, 20, 15) != 15 {
		t.Error("medPredict wrong")
	}
	if bitlen(0) != 0 || bitlen(1) != 1 || bitlen(3) != 2 || bitlen(1020) != 10 {
		t.Error("bitlen wrong")
	}
}

// TestEncodeAllocs checks the steady-state zero-allocation target.
func TestEncodeAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not meaningful under -race")
	}
	c := findCase(t, "field_n0_rice")
	p := c.params(t)
	enc, err := NewEncoder(p)
	if err != nil {
		t.Fatal(err)
	}
	src := goldenInput(t, c)
	dst := make([]byte, MaxEncodedSize(p))
	if _, err := enc.EncodeInto(dst, src); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(20, func() {
		if _, err := enc.EncodeInto(dst, src); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Errorf("EncodeInto allocates %.1f times per call, want 0", allocs)
	}
}

func FuzzDecode(f *testing.F) {
	for _, c := range loadGolden(f) {
		p := c.params(f)
		// The fuzz body maps w,h to w%1024+1, h%256+1, so seed with dims-1.
		f.Add(goldenEncoded(f, c), uint16(p.Width-1), uint16(p.Height-1), uint8(p.Near), p.Pack == PackRice)
	}
	f.Fuzz(func(t *testing.T, data []byte, w, h uint16, near uint8, rice bool) {
		p := Params{Width: int(w%1024) + 1, Height: int(h%256) + 1, Near: int(near % 4), Pack: PackTiled}
		if rice {
			p.Pack = PackRice
		}
		_ = Decode(make([]byte, FrameBytes(p)), data, p)
	})
}

func BenchmarkEncode(b *testing.B) {
	for _, name := range []string{"field_n0_tiled", "field_n0_rice"} {
		c := findCase(b, name)
		b.Run(c.Pack, func(b *testing.B) {
			p := c.params(b)
			enc, err := NewEncoder(p)
			if err != nil {
				b.Fatal(err)
			}
			src := goldenInput(b, c)
			dst := make([]byte, MaxEncodedSize(p))
			b.SetBytes(int64(FrameBytes(p)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := enc.EncodeInto(dst, src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
