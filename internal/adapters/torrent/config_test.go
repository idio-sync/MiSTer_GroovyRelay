package torrent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestDefaultConfigMatchesSpec(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Enabled {
		t.Error("Enabled = true, want false")
	}
	if cfg.TrafficAcknowledged {
		t.Error("TrafficAcknowledged = true, want false")
	}
	if cfg.DownloadDir != "" {
		t.Errorf("DownloadDir = %q, want empty", cfg.DownloadDir)
	}
	if cfg.KeepCompleted {
		t.Error("KeepCompleted = true, want false")
	}
	if cfg.MaxCacheBytes != 20*1024*1024*1024 {
		t.Errorf("MaxCacheBytes = %d, want 20 GiB", cfg.MaxCacheBytes)
	}
	if cfg.MetadataTimeoutSeconds != 60 {
		t.Errorf("MetadataTimeoutSeconds = %d, want 60", cfg.MetadataTimeoutSeconds)
	}
	if cfg.StartupBufferSeconds != 10 {
		t.Errorf("StartupBufferSeconds = %d, want 10", cfg.StartupBufferSeconds)
	}
	if cfg.MaxUploadRateKbps != 512 {
		t.Errorf("MaxUploadRateKbps = %d, want 512", cfg.MaxUploadRateKbps)
	}
	if cfg.MaxDownloadRateKbps != 0 {
		t.Errorf("MaxDownloadRateKbps = %d, want 0", cfg.MaxDownloadRateKbps)
	}
	if cfg.ListenPort != 0 {
		t.Errorf("ListenPort = %d, want 0", cfg.ListenPort)
	}
}

func TestConfigValidateDownloadDirRejectsDangerousRoots(t *testing.T) {
	dataDir := t.TempDir()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	for _, tc := range []struct {
		name        string
		downloadDir string
	}{
		{name: "filesystem root", downloadDir: filepath.VolumeName(dataDir) + string(filepath.Separator)},
		{name: "home dir", downloadDir: homeDir},
		{name: "parent traversal", downloadDir: "../bad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.DownloadDir = tc.downloadDir
			if err := validateConfig(cfg, dataDir); err == nil {
				t.Fatalf("validateConfig(%q) = nil, want error", tc.downloadDir)
			}
		})
	}
}

func TestValidateConfigDoesNotCreateDownloadDir(t *testing.T) {
	dataDir := t.TempDir()
	downloadDir := filepath.Join(dataDir, "cache-parent")
	cfg := DefaultConfig()
	cfg.DownloadDir = downloadDir

	if err := validateConfig(cfg, dataDir); err != nil {
		t.Fatalf("validateConfig: %v", err)
	}
	if _, err := os.Stat(downloadDir); !os.IsNotExist(err) {
		t.Fatalf("validateConfig created %q or stat failed with %v", downloadDir, err)
	}
}

func TestProvisionDownloadRootCreatesOwnedChild(t *testing.T) {
	dataDir := t.TempDir()
	downloadDir := filepath.Join(dataDir, "cache-parent")
	cfg := DefaultConfig()
	cfg.DownloadDir = downloadDir

	got, err := provisionDownloadRoot(cfg, dataDir)
	if err != nil {
		t.Fatalf("provisionDownloadRoot: %v", err)
	}
	want := filepath.Join(downloadDir, "groovyrelay-torrent")
	if !samePath(got, want) {
		t.Fatalf("provisionDownloadRoot = %q, want %q", got, want)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("owned root stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("owned root is not a directory: %q", want)
	}
	if _, err := os.Stat(filepath.Join(want, ".groovyrelay-write-test")); !os.IsNotExist(err) {
		t.Fatalf("write test file still exists or stat failed with %v", err)
	}
}

func TestProvisionDownloadRootResolvesRelativeDownloadDirUnderBridgeDataDir(t *testing.T) {
	dataDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.DownloadDir = "cache"

	got, err := provisionDownloadRoot(cfg, dataDir)
	if err != nil {
		t.Fatalf("provisionDownloadRoot: %v", err)
	}
	want := filepath.Join(dataDir, "cache", "groovyrelay-torrent")
	if got != want {
		t.Fatalf("provisionDownloadRoot = %q, want %q", got, want)
	}
	if filepath.Dir(filepath.Dir(got)) != dataDir {
		t.Fatalf("provisionDownloadRoot parent = %q, want bridge data dir %q", filepath.Dir(filepath.Dir(got)), dataDir)
	}
	if info, err := os.Stat(want); err != nil {
		t.Fatalf("owned root stat: %v", err)
	} else if !info.IsDir() {
		t.Fatalf("owned root is not a directory: %q", want)
	}
}

func TestDownloadRootHelpersUseStringArguments(t *testing.T) {
	dataDir := t.TempDir()

	got := effectiveDownloadDir("cache", dataDir)
	want := filepath.Join(dataDir, "cache")
	if got != want {
		t.Fatalf("effectiveDownloadDir = %q, want %q", got, want)
	}

	got = ownedDownloadRoot("cache", dataDir)
	want = filepath.Join(dataDir, "cache", "groovyrelay-torrent")
	if got != want {
		t.Fatalf("ownedDownloadRoot = %q, want %q", got, want)
	}
}

func TestValidateConfigInspectsRelativeDownloadDirUnderBridgeDataDir(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "cache"), []byte("cwd file"), 0o600); err != nil {
		t.Fatalf("WriteFile cwd cache: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir temp cwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	cfg := DefaultConfig()
	cfg.DownloadDir = "cache"
	if err := validateConfig(cfg, t.TempDir()); err != nil {
		t.Fatalf("validateConfig inspected relative download_dir against cwd: %v", err)
	}
}

func TestValidateConfigRejectsCacheBytesOutsideDesignBounds(t *testing.T) {
	const oneGiB = int64(1024 * 1024 * 1024)
	const oneTiB = int64(1024 * 1024 * 1024 * 1024)

	cfg := DefaultConfig()
	for _, maxBytes := range []int64{oneGiB, oneTiB} {
		cfg.MaxCacheBytes = maxBytes
		if err := validateConfig(cfg, t.TempDir()); err != nil {
			t.Fatalf("validateConfig with max_cache_bytes=%d: %v", maxBytes, err)
		}
	}

	for _, maxBytes := range []int64{0, oneGiB - 1, oneTiB + 1} {
		cfg.MaxCacheBytes = maxBytes
		if err := validateConfig(cfg, t.TempDir()); err == nil {
			t.Fatalf("validateConfig with max_cache_bytes=%d = nil, want error", maxBytes)
		}
	}
}

func TestValidateConfigRejectsInvalidNumericBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*Config)
	}{
		{name: "negative cache bytes", mut: func(c *Config) { c.MaxCacheBytes = -1 }},
		{name: "metadata timeout below minimum", mut: func(c *Config) { c.MetadataTimeoutSeconds = 4 }},
		{name: "metadata timeout above maximum", mut: func(c *Config) { c.MetadataTimeoutSeconds = 601 }},
		{name: "startup buffer below minimum", mut: func(c *Config) { c.StartupBufferSeconds = -1 }},
		{name: "startup buffer above maximum", mut: func(c *Config) { c.StartupBufferSeconds = 121 }},
		{name: "privileged listen port", mut: func(c *Config) { c.ListenPort = 1023 }},
		{name: "listen port above maximum", mut: func(c *Config) { c.ListenPort = 65536 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tc.mut(&cfg)
			if err := validateConfig(cfg, t.TempDir()); err == nil {
				t.Fatal("validateConfig = nil, want error")
			}
		})
	}
}

func TestConfigChangeScope(t *testing.T) {
	base := DefaultConfig()

	for _, tc := range []struct {
		name string
		mut  func(*Config)
		want adapters.ApplyScope
	}{
		{name: "enabled", mut: func(c *Config) { c.Enabled = !c.Enabled }, want: adapters.ScopeHotSwap},
		{name: "traffic_acknowledged", mut: func(c *Config) { c.TrafficAcknowledged = !c.TrafficAcknowledged }, want: adapters.ScopeHotSwap},
		{name: "download_dir", mut: func(c *Config) { c.DownloadDir = filepath.Join(t.TempDir(), "cache") }, want: adapters.ScopeRestartCast},
		{name: "keep_completed", mut: func(c *Config) { c.KeepCompleted = !c.KeepCompleted }, want: adapters.ScopeHotSwap},
		{name: "max_cache_bytes", mut: func(c *Config) { c.MaxCacheBytes++ }, want: adapters.ScopeHotSwap},
		{name: "metadata_timeout_seconds", mut: func(c *Config) { c.MetadataTimeoutSeconds++ }, want: adapters.ScopeHotSwap},
		{name: "startup_buffer_seconds", mut: func(c *Config) { c.StartupBufferSeconds++ }, want: adapters.ScopeHotSwap},
		{name: "max_upload_rate_kbps", mut: func(c *Config) { c.MaxUploadRateKbps++ }, want: adapters.ScopeRestartCast},
		{name: "max_download_rate_kbps", mut: func(c *Config) { c.MaxDownloadRateKbps++ }, want: adapters.ScopeRestartCast},
		{name: "listen_port", mut: func(c *Config) { c.ListenPort++ }, want: adapters.ScopeRestartCast},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := base
			tc.mut(&next)
			if got := configChangeScope(base, next); got != tc.want {
				t.Fatalf("configChangeScope for %s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

func TestConfigChangeScopeCoversAllFields(t *testing.T) {
	if got := reflect.TypeOf(Config{}).NumField(); got != torrentConfigFieldCount {
		t.Fatalf("Config field count = %d, want torrentConfigFieldCount %d", got, torrentConfigFieldCount)
	}
}
