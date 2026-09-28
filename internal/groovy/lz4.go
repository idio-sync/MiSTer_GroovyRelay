package groovy

import (
	"fmt"

	"github.com/pierrec/lz4/v4"
)

// LZ4Compress compresses src using the LZ4 block format (NOT frame format).
// Returns the compressed bytes and ok=true when compression reduced the size.
// Returns (nil, false) when CompressBlock reports the input as incompressible
// (n == 0) or when the output would be no smaller than the input. Never emit
// an LZ4 header with a zero-length payload (the receiver cannot decode it),
// and never fall back to a RAW header while INIT has compression on — use
// LZ4CompressBlockInto for a block that is always sendable.
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

// LZ4CompressInto compresses src into dst using the caller-supplied
// Compressor, returning the number of bytes written and ok=true when
// compression reduced the size. The caller MUST pass a dst with
// len >= lz4.CompressBlockBound(len(src)). Reusing one Compressor across
// calls is the documented zero-alloc path: lz4.Compressor embeds a
// ~136 KB hash table by value, so a fresh `var c lz4.Compressor` per
// call escapes to the heap (~8 MB/s of garbage at NTSC field rate).
// CompressBlock resets its internal in-use bitmap, so reuse is safe and
// produces identical output across calls.
//
// Returns (0, false) when CompressBlock reports the input as
// incompressible (n == 0) or when the output would be no smaller than the
// input. Panics on programmer error (dst too small) — the library only
// errors in that case.
func LZ4CompressInto(c *lz4.Compressor, dst, src []byte) (int, bool) {
	n, err := c.CompressBlock(src, dst)
	if err != nil {
		panic(fmt.Errorf("lz4 compress (caller-supplied dst): %w", err))
	}
	if n == 0 || n >= len(src) {
		return 0, false
	}
	return n, true
}

// LZ4CompressBlockInto compresses src into dst and always returns a valid
// LZ4 block: when src is incompressible the block is slightly larger than
// src (literal runs), never absent. Use it where the wire already promised
// LZ4 — while INIT has compression on, the Groovy core reads every
// BLIT_FIELD_VSYNC as a compressed blit, so a RAW fallback is not an option.
// dst MUST have len >= lz4.CompressBlockBound(len(src)); the library
// guarantees a non-empty block at that size, and anything smaller panics.
func LZ4CompressBlockInto(c *lz4.Compressor, dst, src []byte) int {
	if bound := lz4.CompressBlockBound(len(src)); len(dst) < bound {
		panic(fmt.Errorf("lz4 compress: dst %d bytes < CompressBlockBound %d", len(dst), bound))
	}
	n, err := c.CompressBlock(src, dst)
	if err != nil || n == 0 {
		panic(fmt.Errorf("lz4 compress (bound-sized dst): n=%d err=%v", n, err))
	}
	return n
}
