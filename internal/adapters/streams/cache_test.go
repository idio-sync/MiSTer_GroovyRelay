package streams

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheReadRejectsChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := writeCacheFile(dir, "manifest", []byte(`{"version":1}`), CacheMetadata{
		Schema:    1,
		SourceURL: "https://example.test/providers.json",
	}); err != nil {
		t.Fatalf("writeCacheFile: %v", err)
	}
	metaBytes, err := os.ReadFile(cacheMetaPath(dir, "manifest"))
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	var meta CacheMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	meta.SHA256 = "not-the-real-digest"
	metaBytes, err = json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err := os.WriteFile(cacheMetaPath(dir, "manifest"), metaBytes, 0o600); err != nil {
		t.Fatalf("corrupt meta: %v", err)
	}
	if _, _, err := readCacheFile(dir, "manifest"); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
}

func TestCacheWriteComputesSHAFromBody(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"version":1}`)
	if err := writeCacheFile(dir, "manifest", body, CacheMetadata{
		Schema:    1,
		SourceURL: "https://example.test/providers.json",
		SHA256:    "not-the-real-digest",
	}); err != nil {
		t.Fatalf("writeCacheFile: %v", err)
	}
	gotBody, meta, err := readCacheFile(dir, "manifest")
	if err != nil {
		t.Fatalf("readCacheFile: %v", err)
	}
	if string(gotBody) != string(body) {
		t.Fatalf("body = %s, want %s", gotBody, body)
	}
	if meta.SHA256 != sha256Hex(body) {
		t.Fatalf("metadata sha = %q, want %q", meta.SHA256, sha256Hex(body))
	}
}

func TestCacheWriteOwnsSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"version":1}`)
	if err := writeCacheFile(dir, "manifest", body, CacheMetadata{
		Schema:    99,
		SourceURL: "https://example.test/providers.json",
	}); err != nil {
		t.Fatalf("writeCacheFile: %v", err)
	}
	_, meta, err := readCacheFile(dir, "manifest")
	if err != nil {
		t.Fatalf("readCacheFile: %v", err)
	}
	if meta.Schema != cacheSchemaVersion {
		t.Fatalf("metadata schema = %d, want %d", meta.Schema, cacheSchemaVersion)
	}
}

func TestCacheReadRejectsCorruptMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.meta.json"), []byte(`{bad json`), 0o600); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if _, _, err := readCacheFile(dir, "manifest"); err == nil {
		t.Fatal("corrupt metadata accepted")
	}
}

func TestCacheWriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	if err := writeCacheFile(dir, "catalog-mtv-rewind", []byte(`{"metal":["dQw4w9WgXcQ"]}`), CacheMetadata{
		Schema:    1,
		SourceURL: "https://wantmymtv.vercel.app/public/mtv-playlists.json",
	}); err != nil {
		t.Fatalf("writeCacheFile: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.tmp*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v", matches)
	}
}

func TestCachedRemoteManifestIgnoredWhenDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowRemoteManifest = false
	cfg.AllowCachedRemoteManifest = false
	bundled := validManifestForTest()
	cached := Manifest{Version: 1, Providers: []ProviderDefinition{{
		ID: "cached-provider", Type: "youtube-channel-json", DisplayName: "Cached",
	}}}
	got := mergeManifests(cfg, bundled, &cached, nil, map[string]ProviderFactory{
		"youtube-channel-json": nil,
	})
	if _, ok := got.Provider("cached-provider"); ok {
		t.Fatal("cached remote manifest should be ignored")
	}
}
