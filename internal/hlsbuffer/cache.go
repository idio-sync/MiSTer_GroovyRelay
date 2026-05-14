package hlsbuffer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var cacheNameRE = regexp.MustCompile(`^(playlist|segment|init)-[0-9]{6}\.(m3u8|ts|m4s|mp4|bin)$`)

type CacheEntry struct {
	Name string
	Size int64
}

type SegmentCache struct {
	root        string
	maxSegments int
	maxBytes    int64
	entries     []CacheEntry
	totalBytes  int64
}

func NewSegmentCache(root string, maxSegments int, maxBytes int64) *SegmentCache {
	return &SegmentCache{
		root:        root,
		maxSegments: maxSegments,
		maxBytes:    maxBytes,
	}
}

func SafeCachePath(root, name string) (string, error) {
	base := filepath.Base(name)
	if base != name || !cacheNameRE.MatchString(base) {
		return "", fmt.Errorf("hls cache: unsafe cache filename %q", name)
	}
	cleanRoot := filepath.Clean(root)
	joined := filepath.Clean(filepath.Join(cleanRoot, base))
	if joined == cleanRoot || !strings.HasPrefix(joined, cleanRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("hls cache: path escapes cache root")
	}
	return joined, nil
}

func (c *SegmentCache) Put(name string, body []byte) error {
	if c.maxBytes > 0 && int64(len(body)) > c.maxBytes {
		return fmt.Errorf("hls cache: segment %s exceeds max cache bytes", name)
	}
	path, err := SafeCachePath(c.root, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.root, 0o700); err != nil {
		return fmt.Errorf("hls cache: create root: %w", err)
	}
	c.removeEntry(name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("hls cache: write segment: %w", err)
	}
	c.entries = append(c.entries, CacheEntry{Name: name, Size: int64(len(body))})
	c.totalBytes += int64(len(body))
	return c.enforceLimits()
}

func (c *SegmentCache) Entries() []CacheEntry {
	out := make([]CacheEntry, len(c.entries))
	copy(out, c.entries)
	return out
}

func (c *SegmentCache) TotalBytes() int64 {
	return c.totalBytes
}

func (c *SegmentCache) removeEntry(name string) {
	for i, entry := range c.entries {
		if entry.Name != name {
			continue
		}
		c.totalBytes -= entry.Size
		c.entries = append(c.entries[:i], c.entries[i+1:]...)
		_ = os.Remove(filepath.Join(c.root, name))
		return
	}
}

func (c *SegmentCache) enforceLimits() error {
	for c.overLimit() && len(c.entries) > 0 {
		entry := c.entries[0]
		c.entries = c.entries[1:]
		c.totalBytes -= entry.Size
		if err := os.Remove(filepath.Join(c.root, entry.Name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("hls cache: evict %s: %w", entry.Name, err)
		}
	}
	return nil
}

func (c *SegmentCache) overLimit() bool {
	if c.maxSegments > 0 && len(c.entries) > c.maxSegments {
		return true
	}
	if c.maxBytes > 0 && c.totalBytes > c.maxBytes {
		return true
	}
	return false
}
