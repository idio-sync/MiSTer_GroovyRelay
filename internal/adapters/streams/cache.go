package streams

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

const cacheSchemaVersion = 1

var cacheKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

func readCacheFile(dir, key string) ([]byte, CacheMetadata, error) {
	if err := validateCacheKey(key); err != nil {
		return nil, CacheMetadata{}, err
	}

	metaBytes, err := os.ReadFile(cacheMetaPath(dir, key))
	if err != nil {
		return nil, CacheMetadata{}, err
	}
	var meta CacheMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, CacheMetadata{}, fmt.Errorf("read cache metadata %q: %w", key, err)
	}
	if meta.Schema != cacheSchemaVersion {
		return nil, CacheMetadata{}, fmt.Errorf("cache %q schema = %d, want %d", key, meta.Schema, cacheSchemaVersion)
	}
	if strings.TrimSpace(meta.SHA256) == "" {
		return nil, CacheMetadata{}, fmt.Errorf("cache %q metadata missing sha256", key)
	}

	body, err := os.ReadFile(cacheBodyPath(dir, key))
	if err != nil {
		return nil, CacheMetadata{}, err
	}
	if got := sha256Hex(body); !strings.EqualFold(got, meta.SHA256) {
		return nil, CacheMetadata{}, fmt.Errorf("cache %q checksum mismatch", key)
	}
	return body, meta, nil
}

func writeCacheFile(dir, key string, body []byte, meta CacheMetadata) error {
	if err := validateCacheKey(key); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	meta.Schema = cacheSchemaVersion
	if meta.FetchedAt.IsZero() {
		meta.FetchedAt = time.Now().UTC()
	}
	meta.SHA256 = sha256Hex(body)

	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := config.WriteAtomic(cacheBodyPath(dir, key), body); err != nil {
		return err
	}
	if err := config.WriteAtomic(cacheMetaPath(dir, key), metaBytes); err != nil {
		return err
	}
	return nil
}

func validateCacheKey(key string) error {
	if !cacheKeyPattern.MatchString(key) {
		return fmt.Errorf("invalid cache key %q", key)
	}
	return nil
}

func cacheBodyPath(dir, key string) string {
	return filepath.Join(dir, key+".json")
}

func cacheMetaPath(dir, key string) string {
	return filepath.Join(dir, key+".meta.json")
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
