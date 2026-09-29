package nlc

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// goldenCase is one entry of testdata/vectors.json (format documented in
// tools/nlcvectors/README.md).
type goldenCase struct {
	Name          string `json:"name"`
	Input         string `json:"input"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Near          int    `json:"near"`
	Pack          string `json:"pack"`
	EncodedSize   int    `json:"encoded_size"`
	EncodedSHA256 string `json:"encoded_sha256"`
	DecodedSHA256 string `json:"decoded_sha256"`
}

func (c goldenCase) params(t testing.TB) Params {
	t.Helper()
	var pk Pack
	switch c.Pack {
	case "tiled":
		pk = PackTiled
	case "rice":
		pk = PackRice
	default:
		t.Fatalf("%s: unknown pack %q", c.Name, c.Pack)
	}
	return Params{Width: c.Width, Height: c.Height, Near: c.Near, Pack: pk}
}

var (
	goldenOnce  sync.Once
	goldenCases []goldenCase
	goldenErr   error
	gzCacheMu   sync.Mutex
	gzCache     = map[string][]byte{}
)

func loadGolden(t testing.TB) []goldenCase {
	t.Helper()
	goldenOnce.Do(func() {
		raw, err := os.ReadFile(filepath.Join("testdata", "vectors.json"))
		if err != nil {
			goldenErr = err
			return
		}
		goldenErr = json.Unmarshal(raw, &goldenCases)
	})
	if goldenErr != nil {
		t.Fatalf("load vectors.json: %v", goldenErr)
	}
	if len(goldenCases) != 64 {
		t.Fatalf("vectors.json has %d cases, want 64", len(goldenCases))
	}
	return goldenCases
}

// gunzipFile returns the decompressed contents of a testdata file, cached.
func gunzipFile(t testing.TB, rel string) []byte {
	t.Helper()
	gzCacheMu.Lock()
	defer gzCacheMu.Unlock()
	if b, ok := gzCache[rel]; ok {
		return b
	}
	f, err := os.Open(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatalf("open %s: %v", rel, err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip %s: %v", rel, err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip %s: %v", rel, err)
	}
	gzCache[rel] = b
	return b
}

func goldenInput(t testing.TB, c goldenCase) []byte {
	return gunzipFile(t, filepath.Join("inputs", c.Input+".rgb.gz"))
}

func goldenEncoded(t testing.TB, c goldenCase) []byte {
	return gunzipFile(t, filepath.Join("encoded", c.Name+".nlc.gz"))
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// lineOfOffset walks the v2 line records of a (reference) encoded stream and
// returns the line whose record contains byte offset off, plus the byte
// offset within that record, or -1 when off is past the last record.
func lineOfOffset(enc []byte, off int) (line, within int) {
	pos := 0
	for y := 0; pos+headerBytes <= len(enc); y++ {
		n := headerBytes
		for k := 0; k < numPlanes; k++ {
			n += int(enc[pos+2*k]) | int(enc[pos+2*k+1])<<8
		}
		if off < pos+n {
			return y, off - pos
		}
		pos += n
	}
	return -1, 0
}

func firstDiff(a, b []byte) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

// checkEncoded asserts got is byte-identical to the golden encoding of c.
func checkEncoded(t *testing.T, c goldenCase, got []byte) {
	t.Helper()
	want := goldenEncoded(t, c)
	if len(want) != c.EncodedSize {
		t.Fatalf("golden file %s.nlc.gz has %d bytes, vectors.json says %d", c.Name, len(want), c.EncodedSize)
	}
	if len(got) != c.EncodedSize {
		t.Errorf("encoded size = %d, want %d", len(got), c.EncodedSize)
	}
	if h := sha256Hex(got); h != c.EncodedSHA256 {
		t.Errorf("encoded sha256 = %s, want %s", h, c.EncodedSHA256)
	}
	if off := firstDiff(got, want); off >= 0 {
		line, within := lineOfOffset(want, off)
		t.Errorf("encoded bytes differ at offset %d (line %d, byte %d of its record; got %d bytes, want %d)",
			off, line, within, len(got), len(want))
	}
}

func TestGolden(t *testing.T) {
	for _, c := range loadGolden(t) {
		t.Run(c.Name, func(t *testing.T) {
			p := c.params(t)
			src := goldenInput(t, c)
			if len(src) != FrameBytes(p) {
				t.Fatalf("input %s has %d bytes, want %d", c.Input, len(src), FrameBytes(p))
			}

			enc, err := NewEncoder(p)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			dst := make([]byte, MaxEncodedSize(p))
			n, err := enc.EncodeInto(dst, src)
			if err != nil {
				t.Fatalf("EncodeInto: %v", err)
			}
			checkEncoded(t, c, dst[:n])

			out := make([]byte, FrameBytes(p))
			if err := Decode(out, goldenEncoded(t, c), p); err != nil {
				t.Fatalf("Decode(golden): %v", err)
			}
			if h := sha256Hex(out); h != c.DecodedSHA256 {
				t.Errorf("decoded sha256 = %s, want %s", h, c.DecodedSHA256)
			}
		})
	}
}

// TestRoundTrip: Go encode then Go decode is exact at near 0 and matches the
// reference decode hash at near 1..3.
func TestRoundTrip(t *testing.T) {
	for _, c := range loadGolden(t) {
		t.Run(c.Name, func(t *testing.T) {
			p := c.params(t)
			src := goldenInput(t, c)
			enc, err := NewEncoder(p)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			dst := make([]byte, MaxEncodedSize(p))
			n, err := enc.EncodeInto(dst, src)
			if err != nil {
				t.Fatalf("EncodeInto: %v", err)
			}
			out := make([]byte, FrameBytes(p))
			if err := Decode(out, dst[:n], p); err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if c.Near == 0 && !bytes.Equal(out, src) {
				t.Errorf("near 0 round trip is not lossless (first diff at %d)", firstDiff(out, src))
			}
			if h := sha256Hex(out); h != c.DecodedSHA256 {
				t.Errorf("decoded sha256 = %s, want %s", h, c.DecodedSHA256)
			}
		})
	}
}

// TestEncoderReuse: one Encoder encodes several different inputs of the same
// Params, twice over and in varying order, and every output still matches
// its golden vector. Catches stale scratch between calls.
func TestEncoderReuse(t *testing.T) {
	byParams := map[Params][]goldenCase{}
	var order []Params
	for _, c := range loadGolden(t) {
		p := c.params(t)
		if _, ok := byParams[p]; !ok {
			order = append(order, p)
		}
		byParams[p] = append(byParams[p], c)
	}
	reused := 0
	for _, p := range order {
		cases := byParams[p]
		if len(cases) < 2 {
			continue
		}
		reused++
		enc, err := NewEncoder(p)
		if err != nil {
			t.Fatalf("NewEncoder(%+v): %v", p, err)
		}
		dst := make([]byte, MaxEncodedSize(p))
		seq := append(append([]goldenCase{}, cases...), cases...)
		for i := len(cases) - 1; i >= 0; i-- {
			seq = append(seq, cases[i])
		}
		for i, c := range seq {
			// Poison dst so leftovers from a previous call cannot hide.
			for j := range dst {
				dst[j] = 0xA5
			}
			n, err := enc.EncodeInto(dst, goldenInput(t, c))
			if err != nil {
				t.Fatalf("%s (call %d): EncodeInto: %v", c.Name, i, err)
			}
			t.Run(c.Name, func(t *testing.T) { checkEncoded(t, c, dst[:n]) })
		}
	}
	if reused == 0 {
		t.Fatal("no Params shared by two golden inputs; reuse not exercised")
	}
}

// TestSerialMatchesParallel encodes each golden case with the three planes
// run one after another on one goroutine and checks it equals EncodeInto.
func TestSerialMatchesParallel(t *testing.T) {
	for _, c := range loadGolden(t) {
		p := c.params(t)
		enc, err := NewEncoder(p)
		if err != nil {
			t.Fatal(err)
		}
		src := goldenInput(t, c)
		enc.src = src
		for k := 0; k < numPlanes; k++ {
			enc.encodePlane(k)
		}
		enc.src = nil
		dst := make([]byte, MaxEncodedSize(p))
		n, err := enc.assemble(dst)
		if err != nil {
			t.Fatalf("%s: serial assemble: %v", c.Name, err)
		}
		t.Run(c.Name, func(t *testing.T) { checkEncoded(t, c, dst[:n]) })
	}
}
