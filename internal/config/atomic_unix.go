//go:build !windows

package config

import "os"

// fsyncDir flushes directory metadata so the preceding rename is durable
// across a crash. On Unix/Linux (and MiSTer), strict: returns any error.
// Close errors are surfaced too so we don't set a defer-masking pattern
// for later data-file writers that copy this shape.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
