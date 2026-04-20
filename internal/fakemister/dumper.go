package fakemister

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
)

// Dumper writes periodically sampled BLIT field PNGs and a streaming WAV for
// AUDIO reassembly output. Not a performance-critical path — fake-mister uses
// this to produce human-inspectable artifacts from captured sessions.
type Dumper struct {
	dir         string
	sampleEvery int
	mu          sync.Mutex
	audioFile   *os.File
	audioBytes  int
}

// NewDumper creates the output directory (MkdirAll; ignores already-exists)
// and returns a dumper. If sampleEvery <= 0, MaybeDumpField is a no-op.
func NewDumper(dir string, sampleEvery int) *Dumper {
	_ = os.MkdirAll(dir, 0o755)
	return &Dumper{dir: dir, sampleEvery: sampleEvery}
}

// MaybeDumpField writes a PNG of the given RGB888 payload to the dump dir if
// frame is a multiple of sampleEvery. The file is named field_NNNNNNNN.png.
// rgb888 must be width*height*3 bytes, R,G,B, row-major, no padding.
func (d *Dumper) MaybeDumpField(frame uint32, width, height int, rgb888 []byte) error {
	if d.sampleEvery <= 0 || int(frame)%d.sampleEvery != 0 {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 3
			img.Set(x, y, color.RGBA{R: rgb888[i], G: rgb888[i+1], B: rgb888[i+2], A: 255})
		}
	}
	path := filepath.Join(d.dir, fmt.Sprintf("field_%08d.png", frame))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// StartAudio opens <dir>/audio.wav and writes a placeholder WAV header.
// CloseAudio patches the header with the final data length.
func (d *Dumper) StartAudio(sampleRate, channels int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	path := filepath.Join(d.dir, "audio.wav")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	// Write placeholder WAV header; patched on close.
	writeWAVHeader(f, sampleRate, channels, 0)
	d.audioFile = f
	d.audioBytes = 0
	return nil
}

// WriteAudio appends raw PCM bytes to the open WAV body. No-op if
// StartAudio hasn't been called (or CloseAudio already ran).
func (d *Dumper) WriteAudio(pcm []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.audioFile == nil {
		return nil
	}
	n, err := d.audioFile.Write(pcm)
	d.audioBytes += n
	return err
}

// CloseAudio rewrites the RIFF/data sizes in the header to match the actual
// bytes written, then closes the file.
func (d *Dumper) CloseAudio(sampleRate, channels int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.audioFile == nil {
		return nil
	}
	d.audioFile.Seek(0, 0)
	writeWAVHeader(d.audioFile, sampleRate, channels, d.audioBytes)
	err := d.audioFile.Close()
	d.audioFile = nil
	return err
}

// writeWAVHeader writes a 44-byte canonical PCM WAV header. bitsPerSample is
// fixed at 16 (matches Groovy's fixed 16-bit signed LE PCM).
func writeWAVHeader(w *os.File, sampleRate, channels, dataBytes int) {
	bitsPerSample := 16
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	h := make([]byte, 44)
	copy(h[0:4], "RIFF")
	binary.LittleEndian.PutUint32(h[4:8], uint32(36+dataBytes))
	copy(h[8:12], "WAVE")
	copy(h[12:16], "fmt ")
	binary.LittleEndian.PutUint32(h[16:20], 16)
	binary.LittleEndian.PutUint16(h[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(h[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(h[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(h[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(h[34:36], uint16(bitsPerSample))
	copy(h[36:40], "data")
	binary.LittleEndian.PutUint32(h[40:44], uint32(dataBytes))
	w.Write(h)
}
