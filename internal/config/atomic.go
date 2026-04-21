package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path via a tempfile-plus-rename sequence:
// tempfile in the same directory, fsync the tempfile, rename over the
// destination, fsync the parent directory. A crash at any step leaves
// either the original contents or the new contents intact — never torn.
//
// The tempfile suffix uses a random hex string to prevent collisions
// when two writes race (though callers should serialize via the
// per-adapter mutex in internal/ui).
//
// Directory fsync is delegated to fsyncDir, which has an OS-specific
// implementation: strict on Unix, no-op on Windows (NTFS provides
// rename durability without a separate dir-fsync call).
//
// If fsyncDir returns an error after the rename succeeds, WriteAtomic
// returns the error but the destination file already holds the new
// contents; the error signals that the rename may not survive a crash,
// not that the file is absent or torn. Callers should not rollback by
// deleting the destination in response to this error.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)

	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("atomic: rand: %w", err)
	}
	tmp := path + ".tmp." + hex.EncodeToString(suffix[:])

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("atomic: create tmp: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("atomic: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("atomic: fsync tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("atomic: close tmp: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("atomic: rename: %w", err)
	}

	if err := fsyncDir(dir); err != nil {
		return fmt.Errorf("atomic: fsync dir: %w", err)
	}
	return nil
}
