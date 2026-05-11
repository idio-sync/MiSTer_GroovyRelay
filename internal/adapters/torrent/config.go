package torrent

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

const torrentConfigFieldCount = 10
const minCacheBytes int64 = 1 << 30
const maxCacheBytes int64 = 1 << 40

// Config is the [adapters.torrent] TOML section.
type Config struct {
	Enabled                bool   `toml:"enabled"`
	TrafficAcknowledged    bool   `toml:"traffic_acknowledged"`
	DownloadDir            string `toml:"download_dir"`
	KeepCompleted          bool   `toml:"keep_completed"`
	MaxCacheBytes          int64  `toml:"max_cache_bytes"`
	MetadataTimeoutSeconds int    `toml:"metadata_timeout_seconds"`
	StartupBufferSeconds   int    `toml:"startup_buffer_seconds"`
	MaxUploadRateKbps      int    `toml:"max_upload_rate_kbps"`
	MaxDownloadRateKbps    int    `toml:"max_download_rate_kbps"`
	ListenPort             int    `toml:"listen_port"`
}

// DefaultConfig returns the disabled-by-default torrent adapter baseline.
func DefaultConfig() Config {
	return Config{
		Enabled:                false,
		TrafficAcknowledged:    false,
		DownloadDir:            "",
		KeepCompleted:          false,
		MaxCacheBytes:          20 * 1024 * 1024 * 1024,
		MetadataTimeoutSeconds: 60,
		StartupBufferSeconds:   10,
		MaxUploadRateKbps:      512,
		MaxDownloadRateKbps:    0,
		ListenPort:             0,
	}
}

func validateConfig(cfg Config, bridgeDataDir string) error {
	var errs adapters.FieldErrors

	if err := validateDownloadDirShape(cfg.DownloadDir, bridgeDataDir); err != nil {
		errs = append(errs, adapters.FieldError{Key: "download_dir", Msg: err.Error()})
	}
	if cfg.MaxCacheBytes < minCacheBytes || cfg.MaxCacheBytes > maxCacheBytes {
		errs = append(errs, adapters.FieldError{
			Key: "max_cache_bytes",
			Msg: fmt.Sprintf("must be in [%d, %d], got %d", minCacheBytes, maxCacheBytes, cfg.MaxCacheBytes),
		})
	}
	if cfg.MetadataTimeoutSeconds < 5 || cfg.MetadataTimeoutSeconds > 600 {
		errs = append(errs, adapters.FieldError{
			Key: "metadata_timeout_seconds",
			Msg: fmt.Sprintf("must be in [5, 600], got %d", cfg.MetadataTimeoutSeconds),
		})
	}
	if cfg.StartupBufferSeconds < 0 || cfg.StartupBufferSeconds > 120 {
		errs = append(errs, adapters.FieldError{
			Key: "startup_buffer_seconds",
			Msg: fmt.Sprintf("must be in [0, 120], got %d", cfg.StartupBufferSeconds),
		})
	}
	if cfg.MaxUploadRateKbps < 0 {
		errs = append(errs, adapters.FieldError{
			Key: "max_upload_rate_kbps",
			Msg: fmt.Sprintf("must be non-negative, got %d", cfg.MaxUploadRateKbps),
		})
	}
	if cfg.MaxDownloadRateKbps < 0 {
		errs = append(errs, adapters.FieldError{
			Key: "max_download_rate_kbps",
			Msg: fmt.Sprintf("must be non-negative, got %d", cfg.MaxDownloadRateKbps),
		})
	}
	if cfg.ListenPort < 0 || cfg.ListenPort > 65535 || (cfg.ListenPort > 0 && cfg.ListenPort < 1024) {
		errs = append(errs, adapters.FieldError{
			Key: "listen_port",
			Msg: fmt.Sprintf("must be 0 or in [1024, 65535], got %d", cfg.ListenPort),
		})
	}

	return errs.Err()
}

func configChangeScope(oldCfg, newCfg Config) adapters.ApplyScope {
	if reflect.TypeOf(Config{}).NumField() != torrentConfigFieldCount {
		return adapters.ScopeRestartCast
	}
	if oldCfg.DownloadDir != newCfg.DownloadDir ||
		oldCfg.MaxUploadRateKbps != newCfg.MaxUploadRateKbps ||
		oldCfg.MaxDownloadRateKbps != newCfg.MaxDownloadRateKbps ||
		oldCfg.ListenPort != newCfg.ListenPort {
		return adapters.ScopeRestartCast
	}
	return adapters.ScopeHotSwap
}

func effectiveDownloadDir(downloadDir, bridgeDataDir string) string {
	downloadDir = strings.TrimSpace(downloadDir)
	if downloadDir == "" {
		return filepath.Clean(bridgeDataDir)
	}
	if filepath.IsAbs(downloadDir) {
		return filepath.Clean(downloadDir)
	}
	return filepath.Clean(filepath.Join(bridgeDataDir, downloadDir))
}

func ownedDownloadRoot(downloadDir, bridgeDataDir string) string {
	return filepath.Join(effectiveDownloadDir(downloadDir, bridgeDataDir), "groovyrelay-torrent")
}

func validateDownloadDirShape(downloadDir, bridgeDataDir string) error {
	if strings.TrimSpace(bridgeDataDir) == "" {
		return fmt.Errorf("bridge data_dir is required")
	}
	if err := validatePathShape("bridge data_dir", bridgeDataDir); err != nil {
		return err
	}
	if strings.TrimSpace(downloadDir) == "" {
		return nil
	}
	if hasUnresolvedParent(downloadDir) {
		return fmt.Errorf("download_dir must not contain unresolved .. segments")
	}
	return validatePathShape("download_dir", effectiveDownloadDir(downloadDir, bridgeDataDir))
}

func validatePathShape(label, path string) error {
	cleaned := filepath.Clean(path)
	if hasUnresolvedParent(cleaned) {
		return fmt.Errorf("%s must not contain unresolved .. segments", label)
	}
	if isFilesystemRoot(cleaned) {
		return fmt.Errorf("%s must not be a filesystem root", label)
	}
	if isHomeDir(cleaned) {
		return fmt.Errorf("%s must not be the user home directory", label)
	}
	if isBroadSystemDir(cleaned) {
		return fmt.Errorf("%s must not be a broad system directory", label)
	}
	if info, err := os.Stat(cleaned); err == nil && !info.IsDir() {
		return fmt.Errorf("%s exists and is not a directory", label)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("%s cannot be inspected: %w", label, err)
	}
	return nil
}

func provisionDownloadRoot(cfg Config, bridgeDataDir string) (string, error) {
	if err := validateConfig(cfg, bridgeDataDir); err != nil {
		return "", err
	}
	root := ownedDownloadRoot(cfg.DownloadDir, bridgeDataDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}

	probe := filepath.Join(root, ".groovyrelay-write-test")
	if err := os.WriteFile(probe, []byte("ok\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Remove(probe); err != nil {
		return "", err
	}
	return root, nil
}

func isFilesystemRoot(path string) bool {
	cleaned := filepath.Clean(path)
	volume := filepath.VolumeName(cleaned)
	if volume != "" {
		root := volume + string(filepath.Separator)
		return samePath(cleaned, root)
	}
	return cleaned == string(filepath.Separator)
}

func samePath(a, b string) bool {
	aAbs, aErr := filepath.Abs(a)
	bAbs, bErr := filepath.Abs(b)
	if aErr == nil {
		a = aAbs
	}
	if bErr == nil {
		b = bAbs
	}
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func hasUnresolvedParent(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return true
		}
	}
	return false
}

func isHomeDir(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	return samePath(path, home)
}

func isBroadSystemDir(path string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	for _, broad := range []string{"/tmp", "/var", "/var/tmp"} {
		if samePath(path, broad) {
			return true
		}
	}
	return false
}
