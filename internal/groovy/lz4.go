package groovy

import (
	"fmt"

	"github.com/pierrec/lz4/v4"
)

// LZ4Compress compresses src using the LZ4 block format (NOT frame format).
// Returns the compressed bytes and ok=true when compression reduced the size.
// Returns (nil, false) when CompressBlock reports the input as incompressible
// (n == 0) or when the output would be no smaller than the input. Callers
// emit the RAW BLIT header variant in the ok=false case — never an LZ4 header
// with zero-length payload (the receiver cannot decode that).
//
// A genuine lz4 library error still panics: the library only errors on
// programmer mistakes (e.g. dst too small), and the dst sizing below is
// bounded correctly.
func LZ4Compress(src []byte) ([]byte, bool) {
	dst := make([]byte, lz4.CompressBlockBound(len(src)))
	var c lz4.Compressor
	n, err := c.CompressBlock(src, dst)
	if err != nil {
		panic(fmt.Errorf("lz4 compress (dst sized by CompressBlockBound): %w", err))
	}
	if n == 0 || n >= len(src) {
		return nil, false
	}
	return dst[:n], true
}

// LZ4Decompress reverses LZ4Compress. rawLen MUST equal the original src length.
func LZ4Decompress(compressed []byte, rawLen int) ([]byte, error) {
	dst := make([]byte, rawLen)
	n, err := lz4.UncompressBlock(compressed, dst)
	if err != nil {
		return nil, fmt.Errorf("lz4 decompress: %w", err)
	}
	if n != rawLen {
		return nil, fmt.Errorf("lz4 decompress: got %d bytes, want %d", n, rawLen)
	}
	return dst, nil
}

// LZ4CompressInto compresses src into dst, returning the number of bytes
// written and ok=true when compression reduced the size. The caller MUST
// pass a dst with len >= lz4.CompressBlockBound(len(src)). Identical
// behavior to LZ4Compress except the output buffer is supplied by the
// caller; intended for the data plane's hot tick path where re-allocating
// the output on every field would churn the heap.
//
// Returns (0, false) when CompressBlock reports the input as
// incompressible (n == 0) or when the output would be no smaller than the
// input. Panics on programmer error (dst too small) — the library only
// errors in that case.
func LZ4CompressInto(dst, src []byte) (int, bool) {
	var c lz4.Compressor
	n, err := c.CompressBlock(src, dst)
	if err != nil {
		panic(fmt.Errorf("lz4 compress (caller-supplied dst): %w", err))
	}
	if n == 0 || n >= len(src) {
		return 0, false
	}
	return n, true
}
