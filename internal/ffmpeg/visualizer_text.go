package ffmpeg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// File names inside VisualizerSpec.LiveTextDir.
const (
	VisualizerTitleFile  = "title.txt"
	VisualizerArtistFile = "artist.txt"
	VisualizerAlbumFile  = "album.txt"
)

// WriteVisualizerText writes the live overlay text files for a
// VisualizerSpec.LiveTextDir pipeline. Call it once before the pipeline
// spawns (drawtext fails to initialise on a missing file) and again on
// every metadata change. Text is normalised the same way as the static
// overlay: trimmed, upper-cased, a blank title reads NOW PLAYING.
//
// Each file is replaced by rename so a per-frame reload never observes a
// missing or half-written file.
func WriteVisualizerText(dir string, md VisualizerMetadata) error {
	title := liveTextLine(md.Title)
	if title == "" {
		title = "NOW PLAYING"
	}
	files := []struct{ name, text string }{
		{VisualizerTitleFile, title},
		{VisualizerArtistFile, liveTextLine(md.Artist)},
		{VisualizerAlbumFile, liveTextLine(md.Album)},
	}
	var errs []error
	for _, f := range files {
		if err := replaceFile(filepath.Join(dir, f.name), f.text); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// liveTextLine collapses s to one upper-cased line. drawtext renders a
// newline as a line break, which would spill out of the fixed-height
// line layer.
func liveTextLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	return strings.ToUpper(strings.TrimSpace(s))
}

// replaceFile replaces path's contents with text without ever letting a
// concurrent ffmpeg reload fail. drawtext aborts the whole filter graph
// when a reload fails, so this matters more than a torn frame.
//
// Elsewhere that is an atomic rename. On Windows every approach that
// swaps or resizes the file races ffmpeg's reload: a rename leaves a window
// in which its open fails with "Permission denied", and a size change
// between its size check and CreateFileMapping/MapViewOfFile fails the
// mapping. So Windows files keep one fixed size (liveTextFileSize, padded
// with NUL bytes: drawtext copies the file into a C string, so the text
// ends at the first NUL) and are overwritten in place. A reload can at
// worst see one frame of mixed old and new text.
func replaceFile(path, text string) error {
	if runtime.GOOS == "windows" {
		return overwriteFixedSize(path, text)
	}
	return renameInto(path, text)
}

// liveTextFileSize is the fixed byte size of a Windows live text file.
const liveTextFileSize = 512

func fixedSizeText(text string) []byte {
	b := []byte(text)
	if len(b) > liveTextFileSize {
		cut := liveTextFileSize
		for cut > 0 && !utf8.RuneStart(b[cut]) {
			cut--
		}
		b = b[:cut]
	}
	out := make([]byte, liveTextFileSize) // zero-filled: the NUL padding
	copy(out, b)
	return out
}

func overwriteFixedSize(path, text string) error {
	buf := fixedSizeText(text)
	var err error
	for attempt := 0; attempt < 40; attempt++ {
		if err = writeAtStart(path, buf); err == nil {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return fmt.Errorf("live visualizer text: %w", err)
}

// writeAtStart writes buf at offset 0 without O_TRUNC, so a file that
// already has len(buf) bytes never changes size.
func writeAtStart(path string, buf []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteAt(buf, 0)
	return errors.Join(werr, f.Close())
}

func renameInto(path, text string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("live visualizer text: %w", err)
	}
	tmpName := tmp.Name()
	_, werr := tmp.WriteString(text)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("live visualizer text: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("live visualizer text: %w", err)
	}
	return nil
}
