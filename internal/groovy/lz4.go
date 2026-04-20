package groovy

import (
	"fmt"

	"github.com/pierrec/lz4/v4"
)

// LZ4Compress compresses src using LZ4 block format (NOT frame format).
// Returns the compressed bytes; caller stores the decompressed length out-of-band
// (in the BLIT header compressedSize / rawSize fields).
func LZ4Compress(src []byte) ([]byte, error) {
	dst := make([]byte, lz4.CompressBlockBound(len(src)))
	var c lz4.Compressor
	n, err := c.CompressBlock(src, dst)
	if err != nil {
		return nil, fmt.Errorf("lz4 compress: %w", err)
	}
	return dst[:n], nil
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
