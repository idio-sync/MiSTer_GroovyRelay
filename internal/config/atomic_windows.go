//go:build windows

package config

// fsyncDir is a no-op on Windows. NTFS does not expose directory fsync
// via os.File.Sync (it returns "Access is denied" on a directory handle).
// Rename durability on NTFS is provided by the filesystem itself after
// the preceding file fsync, so the WriteAtomic guarantee still holds:
// a crash leaves either the old or new contents intact, never torn.
func fsyncDir(dir string) error {
	_ = dir
	return nil
}
