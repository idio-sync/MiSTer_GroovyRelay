# Torrent Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a disabled-by-default Torrent adapter that streams operator-supplied magnet links and uploaded `.torrent` files through a loopback-only local media URL into the existing core session manager.

**Architecture:** The adapter owns BitTorrent consent, cache safety, torrent metadata/file selection, a tokenized loopback media route, and cleanup. The core remains protocol-agnostic: the adapter starts a direct-play `core.SessionRequest` with a constrained `MediaInputPolicy`, and core preempts any existing cast exactly as URL, Jellyfin, Plex, and DLNA sessions do.

**Tech Stack:** Go 1.26.2, existing `internal/adapters` interfaces, existing `core.Manager`, `github.com/anacrolix/torrent` pinned in this plan to the currently documented v1.61.0 API, `github.com/anacrolix/torrent/metainfo`, `github.com/anacrolix/torrent/storage`, standard-library `http.ServeContent`.

---

## Source Notes

- Design spec: `docs/superpowers/specs/2026-05-10-torrent-adapter-design.md`
- Existing adapter shape: `internal/adapters/url/adapter.go`, `internal/adapters/streams/routes.go`, `internal/adapters/dlna/routes.go`
- Public routes: `adapters.PublicRouteProvider` in `internal/adapters/adapter.go`
- UI routes: `adapters.RouteProvider` in `internal/adapters/adapter.go`
- Adapter value/UI extensions: `internal/ui/adapter.go`
- Core session contract: `internal/core/types.go`
- Media input policy: `internal/ffmpeg/policy.go`
- anacrolix API reference checked on 2026-05-10: `https://pkg.go.dev/github.com/anacrolix/torrent`, `https://pkg.go.dev/github.com/anacrolix/torrent/metainfo`, `https://pkg.go.dev/github.com/anacrolix/torrent/storage`

## File Structure

- Create `internal/adapters/torrent/config.go`: TOML config, defaults, field schema, validation, apply-scope comparison.
- Create `internal/adapters/torrent/adapter.go`: adapter lifecycle, config decode/apply, status, event emission, consent gate, lazy client creation.
- Create `internal/adapters/torrent/errors.go`: typed adapter errors and HTTP status mapping.
- Create `internal/adapters/torrent/cache.go`: adapter-owned cache root, marker file, cleanup, persistent cache pruning.
- Create `internal/adapters/torrent/select.go`: playable file selection, display-title sanitization, magnet redaction.
- Create `internal/adapters/torrent/client.go`: narrow torrent client interfaces, fake-friendly factory wiring, real anacrolix wrapper.
- Create `internal/adapters/torrent/session.go`: active-session state, token allocation, same-info-hash reuse, idempotent cleanup.
- Create `internal/adapters/torrent/server.go`: `/torrent/session/{token}/media` public route and loopback enforcement.
- Create `internal/adapters/torrent/routes.go`: UI route handlers for panel, status, play magnet, upload torrent, stop.
- Create `internal/adapters/torrent/ui.go`: `ExtraPanelHTML` and panel fragment rendering.
- Create tests beside each file: `*_test.go` files in `internal/adapters/torrent/`.
- Modify `cmd/mister-groovy-relay/main.go`: construct/register the torrent adapter and mount its public route through the existing provider walk.
- Modify `go.mod` and `go.sum`: add `github.com/anacrolix/torrent v1.61.0` and transitive dependencies produced by `go get`.
- Modify `internal/config/example.toml`: add `[adapters.torrent]` with documented defaults.
- Modify `README.md`: document operator consent, legal/traffic warning, magnet/upload use, cache behavior, route scope.
- Modify `THIRD_PARTY_NOTICES.md`: add anacrolix/torrent notice.

## Required Adapter Contracts

- `enabled=false` and `traffic_acknowledged=false` both gate new sessions only. Changing either field while a torrent is already casting does not stop that live session.
- `Start(ctx)` must not open a BitTorrent listen port. The real client is created lazily inside play handling after both gates pass.
- New torrent play calls `core.StartSession`; existing core behavior preempts the current session. The adapter does not refuse with 409 solely because another cast is live.
- Re-adding the same info hash reuses the torrent object inside the client, creates a new tokenized media route, and keeps cleanup idempotent.
- `OnStop` runs in a goroutine after core cancels the data plane. The adapter owns session cleanup by token/session id and must never assume the session is still current after `StartSession` returns.
- Magnet logs keep only `xt=urn:btih:<first-8-hex>` or `magnet:<invalid>`. Drop `tr`, `dn`, `xs`, `ws`, `as`, and every other parameter.
- Media serving accepts IPv4 and IPv6 loopback only via `net.ParseIP(host).IsLoopback()`, covering `127.0.0.0/8` and `::1`.
- Cache deletion only touches marker-bearing directories under the adapter-owned root. Creation order is directory, marker file, data.
- Persistent storage pruning only runs after the adapter has created and marked `<download_dir-or-data_dir>/groovyrelay-torrent/storage`; active storage directory names are excluded from pruning.

---

### Task 1: Config and Adapter Skeleton

**Files:**
- Create: `internal/adapters/torrent/config.go`
- Create: `internal/adapters/torrent/adapter.go`
- Create: `internal/adapters/torrent/config_test.go`
- Create: `internal/adapters/torrent/adapter_test.go`

- [ ] **Step 1: Write failing config tests**

Create `internal/adapters/torrent/config_test.go` with:

```go
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
		t.Fatal("Enabled default = true, want false")
	}
	if cfg.TrafficAcknowledged {
		t.Fatal("TrafficAcknowledged default = true, want false")
	}
	if cfg.DownloadDir != "" {
		t.Fatalf("DownloadDir default = %q, want empty", cfg.DownloadDir)
	}
	if cfg.KeepCompleted {
		t.Fatal("KeepCompleted default = true, want false")
	}
	if cfg.MaxCacheBytes != 20*1024*1024*1024 {
		t.Fatalf("MaxCacheBytes = %d, want 20 GiB", cfg.MaxCacheBytes)
	}
	if cfg.MetadataTimeoutSeconds != 60 {
		t.Fatalf("MetadataTimeoutSeconds = %d, want 60", cfg.MetadataTimeoutSeconds)
	}
	if cfg.StartupBufferSeconds != 10 {
		t.Fatalf("StartupBufferSeconds = %d, want 10", cfg.StartupBufferSeconds)
	}
	if cfg.MaxUploadRateKbps != 512 {
		t.Fatalf("MaxUploadRateKbps = %d, want 512", cfg.MaxUploadRateKbps)
	}
	if cfg.MaxDownloadRateKbps != 0 {
		t.Fatalf("MaxDownloadRateKbps = %d, want 0", cfg.MaxDownloadRateKbps)
	}
	if cfg.ListenPort != 0 {
		t.Fatalf("ListenPort = %d, want 0", cfg.ListenPort)
	}
}

func TestConfigValidateDownloadDirRejectsDangerousRoots(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	cases := []string{
		filepath.VolumeName(dataDir) + string(os.PathSeparator),
		home,
		".." + string(os.PathSeparator) + "bad",
	}
	for _, dir := range cases {
		cfg := DefaultConfig()
		cfg.DownloadDir = dir
		if err := validateConfig(cfg, dataDir); err == nil {
			t.Fatalf("validateConfig(%q) succeeded, want error", dir)
		}
	}
}

func TestValidateConfigDoesNotCreateDownloadDir(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "operator-selected-cache")
	cfg := DefaultConfig()
	cfg.DownloadDir = dir
	if err := validateConfig(cfg, dataDir); err != nil {
		t.Fatalf("validateConfig(%q): %v", dir, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("validateConfig created download_dir or stat failed differently: %v", err)
	}
}

func TestProvisionDownloadRootCreatesOwnedChild(t *testing.T) {
	dataDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.DownloadDir = filepath.Join(dataDir, "operator-selected-cache")
	root, err := provisionDownloadRoot(cfg, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cfg.DownloadDir, "groovyrelay-torrent")
	if root != want {
		t.Fatalf("provisionDownloadRoot = %q, want %q", root, want)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("owned root missing: %v", err)
	}
}

func TestConfigChangeScope(t *testing.T) {
	base := DefaultConfig()
	cases := []struct {
		name string
		edit func(*Config)
		want adapters.ApplyScope
	}{
		{"enabled", func(c *Config) { c.Enabled = true }, adapters.ScopeHotSwap},
		{"traffic_acknowledged", func(c *Config) { c.TrafficAcknowledged = true }, adapters.ScopeHotSwap},
		{"download_dir", func(c *Config) { c.DownloadDir = "cache-a" }, adapters.ScopeRestartCast},
		{"max_upload_rate", func(c *Config) { c.MaxUploadRateKbps = 256 }, adapters.ScopeRestartCast},
		{"max_download_rate", func(c *Config) { c.MaxDownloadRateKbps = 1024 }, adapters.ScopeRestartCast},
		{"listen_port", func(c *Config) { c.ListenPort = 6881 }, adapters.ScopeRestartCast},
		{"keep_completed", func(c *Config) { c.KeepCompleted = true }, adapters.ScopeHotSwap},
		{"max_cache_bytes", func(c *Config) { c.MaxCacheBytes = 1024 }, adapters.ScopeHotSwap},
		{"metadata_timeout", func(c *Config) { c.MetadataTimeoutSeconds = 30 }, adapters.ScopeHotSwap},
		{"startup_buffer", func(c *Config) { c.StartupBufferSeconds = 0 }, adapters.ScopeHotSwap},
	}
	for _, tc := range cases {
		next := base
		tc.edit(&next)
		if got := configChangeScope(base, next); got != tc.want {
			t.Fatalf("%s scope = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestConfigChangeScopeCoversAllFields(t *testing.T) {
	if got := reflect.TypeOf(Config{}).NumField(); got != torrentConfigFieldCount {
		t.Fatalf("torrentConfigFieldCount = %d, Config fields = %d", torrentConfigFieldCount, got)
	}
}
```

- [ ] **Step 2: Run tests and verify the expected compile failure**

Run: `go test ./internal/adapters/torrent`

Expected: FAIL with errors containing `undefined: DefaultConfig`, `undefined: Config`, `undefined: validateConfig`, `undefined: provisionDownloadRoot`, and `undefined: configChangeScope`.

- [ ] **Step 3: Implement config primitives**

Create `internal/adapters/torrent/config.go` with these concrete definitions and validation rules:

```go
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

func validateConfig(c Config, dataDir string) error {
	var errs adapters.FieldErrors
	if dataDir == "" {
		errs = append(errs, adapters.FieldError{Key: "download_dir", Msg: "bridge data_dir is required"})
	}
	if c.MaxCacheBytes < 0 {
		errs = append(errs, adapters.FieldError{Key: "max_cache_bytes", Msg: "must be >= 0"})
	}
	if c.MetadataTimeoutSeconds < 5 || c.MetadataTimeoutSeconds > 600 {
		errs = append(errs, adapters.FieldError{Key: "metadata_timeout_seconds", Msg: fmt.Sprintf("must be in [5, 600], got %d", c.MetadataTimeoutSeconds)})
	}
	if c.StartupBufferSeconds < 0 || c.StartupBufferSeconds > 120 {
		errs = append(errs, adapters.FieldError{Key: "startup_buffer_seconds", Msg: fmt.Sprintf("must be in [0, 120], got %d", c.StartupBufferSeconds)})
	}
	if c.MaxUploadRateKbps < 0 {
		errs = append(errs, adapters.FieldError{Key: "max_upload_rate_kbps", Msg: "must be >= 0"})
	}
	if c.MaxDownloadRateKbps < 0 {
		errs = append(errs, adapters.FieldError{Key: "max_download_rate_kbps", Msg: "must be >= 0"})
	}
	if c.ListenPort != 0 && (c.ListenPort < 1024 || c.ListenPort > 65535) {
		errs = append(errs, adapters.FieldError{Key: "listen_port", Msg: fmt.Sprintf("must be 0 or in [1024, 65535], got %d", c.ListenPort)})
	}
	if err := validateDownloadDirShape(c.DownloadDir, dataDir); err != nil {
		errs = append(errs, adapters.FieldError{Key: "download_dir", Msg: err.Error()})
	}
	return errs.Err()
}

func configChangeScope(oldCfg, newCfg Config) adapters.ApplyScope {
	if reflect.TypeOf(Config{}).NumField() != torrentConfigFieldCount {
		return adapters.ScopeRestartCast
	}
	scope := adapters.ScopeHotSwap
	if oldCfg.DownloadDir != newCfg.DownloadDir ||
		oldCfg.MaxUploadRateKbps != newCfg.MaxUploadRateKbps ||
		oldCfg.MaxDownloadRateKbps != newCfg.MaxDownloadRateKbps ||
		oldCfg.ListenPort != newCfg.ListenPort {
		scope = adapters.MaxScope(scope, adapters.ScopeRestartCast)
	}
	return scope
}

func effectiveDownloadDir(downloadDir, dataDir string) string {
	if strings.TrimSpace(downloadDir) == "" {
		return dataDir
	}
	cleaned := filepath.Clean(downloadDir)
	if filepath.IsAbs(cleaned) {
		return cleaned
	}
	return filepath.Join(dataDir, cleaned)
}

func ownedDownloadRoot(downloadDir, dataDir string) string {
	return filepath.Join(effectiveDownloadDir(downloadDir, dataDir), "groovyrelay-torrent")
}

func validateDownloadDirShape(downloadDir, dataDir string) error {
	if strings.TrimSpace(downloadDir) != "" {
		cleanedInput := filepath.Clean(downloadDir)
		for _, part := range strings.Split(cleanedInput, string(os.PathSeparator)) {
			if part == ".." {
				return fmt.Errorf("must not contain '..' after cleaning")
			}
		}
	}
	dir := effectiveDownloadDir(downloadDir, dataDir)
	if dir == "." || dir == "" {
		return fmt.Errorf("must resolve to an absolute or data_dir-relative directory")
	}
	cleaned := filepath.Clean(dir)
	for _, part := range strings.Split(cleaned, string(os.PathSeparator)) {
		if part == ".." {
			return fmt.Errorf("must not contain '..' after cleaning")
		}
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return fmt.Errorf("resolve absolute path: %w", err)
	}
	if isFilesystemRoot(abs) {
		return fmt.Errorf("must not be a filesystem root")
	}
	if home, err := os.UserHomeDir(); err == nil && samePath(abs, home) {
		return fmt.Errorf("must not be the home directory")
	}
	if runtime.GOOS != "windows" && (samePath(abs, "/tmp") || samePath(abs, "/var") || samePath(abs, "/var/tmp")) {
		return fmt.Errorf("must be an adapter-owned child directory, not a broad system directory")
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		return fmt.Errorf("must be a directory")
	}
	return nil
}

func provisionDownloadRoot(cfg Config, dataDir string) (string, error) {
	if err := validateDownloadDirShape(cfg.DownloadDir, dataDir); err != nil {
		return "", err
	}
	root := ownedDownloadRoot(cfg.DownloadDir, dataDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create owned root: %w", err)
	}
	probe := filepath.Join(root, ".groovyrelay-write-test")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("owned root is not writable: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close write probe: %w", err)
	}
	_ = os.Remove(probe)
	return root, nil
}

func isFilesystemRoot(path string) bool {
	vol := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, vol)
	return rest == string(os.PathSeparator)
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(filepath.Clean(a))
	bb, errB := filepath.Abs(filepath.Clean(b))
	if errA != nil || errB != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(aa, bb)
	}
	return aa == bb
}
```

- [ ] **Step 4: Write failing adapter skeleton tests**

Create `internal/adapters/torrent/adapter_test.go` with:

```go
package torrent

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type recordingCore struct {
	reqs []core.SessionRequest
}

func (r *recordingCore) StartSession(req core.SessionRequest) error {
	r.reqs = append(r.reqs, req)
	return nil
}
func (r *recordingCore) Status() core.SessionStatus { return core.SessionStatus{} }
func (r *recordingCore) Stop() error                { return nil }

func TestAdapterImplementsCoreInterfaces(t *testing.T) {
	var _ adapters.Adapter = (*Adapter)(nil)
	var _ adapters.RouteProvider = (*Adapter)(nil)
	var _ adapters.PublicRouteProvider = (*Adapter)(nil)
}

func TestNewRequiresBridgeDataDir(t *testing.T) {
	_, err := New(AdapterConfig{})
	if err == nil {
		t.Fatal("New without Bridge.DataDir succeeded, want error")
	}
}

func TestStartDoesNotCreateTorrentClient(t *testing.T) {
	created := 0
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   &recordingCore{},
		ClientFactory: func(ClientConfig) (TorrentClient, error) {
			created++
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("Start created %d torrent clients, want 0", created)
	}
}

func TestSetEnabledGatesIsEnabled(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	a.SetEnabled(true)
	if !a.IsEnabled() {
		t.Fatal("IsEnabled = false after SetEnabled(true)")
	}
	a.SetEnabled(false)
	if a.IsEnabled() {
		t.Fatal("IsEnabled = true after SetEnabled(false)")
	}
}
```

- [ ] **Step 5: Implement adapter skeleton**

Create `internal/adapters/torrent/adapter.go` with:

```go
package torrent

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

type SessionManager interface {
	StartSession(core.SessionRequest) error
	Status() core.SessionStatus
	Stop() error
}

type ClientFactory func(ClientConfig) (TorrentClient, error)

type AdapterConfig struct {
	Bridge        config.BridgeConfig
	Core          SessionManager
	EventLog      *eventlog.Log
	ClientFactory ClientFactory
}

type Adapter struct {
	bridge   config.BridgeConfig
	core     SessionManager
	eventLog *eventlog.Log
	factory  ClientFactory

	mu          sync.Mutex
	cfg         Config
	state       adapters.State
	lastErr     string
	stateSince  time.Time
	client      TorrentClient
	sessions    map[string]*Session
	torrents    map[string]*torrentUse
	activeToken string
}

type torrentUse struct {
	torrent TorrentHandle
	refs    int
}

func New(cfg AdapterConfig) (*Adapter, error) {
	if cfg.Bridge.DataDir == "" {
		return nil, fmt.Errorf("torrent: AdapterConfig.Bridge.DataDir is required")
	}
	factory := cfg.ClientFactory
	if factory == nil {
		factory = newRealClient
	}
	return &Adapter{
		bridge:     cfg.Bridge,
		core:       cfg.Core,
		eventLog:   cfg.EventLog,
		factory:    factory,
		cfg:        DefaultConfig(),
		state:      adapters.StateStopped,
		stateSince: time.Now(),
		sessions:   make(map[string]*Session),
		torrents:   make(map[string]*torrentUse),
	}, nil
}

func (a *Adapter) Name() string        { return "torrent" }
func (a *Adapter) DisplayName() string { return "Torrent" }

func (a *Adapter) Fields() []adapters.FieldDef {
	return torrentFields()
}

func torrentFields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{Key: "enabled", Label: "Enabled", Help: "Allow new torrent sessions.", Kind: adapters.KindBool, Default: false, ApplyScope: adapters.ScopeHotSwap},
		{Key: "traffic_acknowledged", Label: "I understand this uses BitTorrent traffic", Help: "Required before magnet links or torrent uploads can start network activity.", Kind: adapters.KindBool, Default: false, ApplyScope: adapters.ScopeHotSwap},
		{Key: "download_dir", Label: "Download directory", Help: "Directory where torrent cache data is stored. Empty uses bridge data_dir/groovyrelay-torrent.", Kind: adapters.KindText, Default: "", ApplyScope: adapters.ScopeRestartCast},
		{Key: "keep_completed", Label: "Keep completed data", Help: "Keep completed cache data after playback stops.", Kind: adapters.KindBool, Default: false, ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_cache_bytes", Label: "Maximum cache bytes", Help: "Persistent cache budget. Zero disables pruning.", Kind: adapters.KindInt, Default: int64(20 * 1024 * 1024 * 1024), ApplyScope: adapters.ScopeHotSwap},
		{Key: "metadata_timeout_seconds", Label: "Metadata timeout seconds", Help: "Maximum wait for magnet metadata.", Kind: adapters.KindInt, Default: 60, ApplyScope: adapters.ScopeHotSwap},
		{Key: "startup_buffer_seconds", Label: "Startup buffer seconds", Help: "Seconds to wait for initial data before handing the stream to FFmpeg.", Kind: adapters.KindInt, Default: 10, ApplyScope: adapters.ScopeHotSwap},
		{Key: "max_upload_rate_kbps", Label: "Maximum upload KB/s", Help: "Upload rate limit. Zero means unlimited.", Kind: adapters.KindInt, Default: 512, ApplyScope: adapters.ScopeRestartCast},
		{Key: "max_download_rate_kbps", Label: "Maximum download KB/s", Help: "Download rate limit. Zero means unlimited.", Kind: adapters.KindInt, Default: 0, ApplyScope: adapters.ScopeRestartCast},
		{Key: "listen_port", Label: "Listen port", Help: "BitTorrent listen port. Zero lets the OS choose.", Kind: adapters.KindInt, Default: 0, ApplyScope: adapters.ScopeRestartCast},
	}
}

func (a *Adapter) DecodeConfig(raw toml.Primitive, meta toml.MetaData) error {
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return fmt.Errorf("torrent: decode config: %w", err)
	}
	if err := validateConfig(cfg, a.bridge.DataDir); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	return nil
}

func (a *Adapter) Validate(raw toml.Primitive, meta toml.MetaData) error {
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return fmt.Errorf("torrent: decode config: %w", err)
	}
	return validateConfig(cfg, a.bridge.DataDir)
}

func (a *Adapter) IsEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Enabled
}

func (a *Adapter) Start(context.Context) error {
	a.setState(adapters.StateRunning, "")
	return nil
}

func (a *Adapter) Stop() error {
	a.setState(adapters.StateStopped, "")
	return nil
}

func (a *Adapter) Status() adapters.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return adapters.Status{State: a.state, LastError: a.lastErr, Since: a.stateSince}
}

func (a *Adapter) SetEnabled(v bool) {
	a.mu.Lock()
	a.cfg.Enabled = v
	a.mu.Unlock()
}

func (a *Adapter) ApplyConfig(raw toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	next := DefaultConfig()
	if err := meta.PrimitiveDecode(raw, &next); err != nil {
		return 0, fmt.Errorf("torrent: decode apply config: %w", err)
	}
	if err := validateConfig(next, a.bridge.DataDir); err != nil {
		a.setState(adapters.StateError, err.Error())
		return 0, err
	}
	a.mu.Lock()
	old := a.cfg
	a.cfg = next
	a.mu.Unlock()
	return configChangeScope(old, next), nil
}

func (a *Adapter) CurrentValues() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{
		"enabled":                  a.cfg.Enabled,
		"traffic_acknowledged":     a.cfg.TrafficAcknowledged,
		"download_dir":             a.cfg.DownloadDir,
		"keep_completed":           a.cfg.KeepCompleted,
		"max_cache_bytes":          a.cfg.MaxCacheBytes,
		"metadata_timeout_seconds": a.cfg.MetadataTimeoutSeconds,
		"startup_buffer_seconds":   a.cfg.StartupBufferSeconds,
		"max_upload_rate_kbps":     a.cfg.MaxUploadRateKbps,
		"max_download_rate_kbps":   a.cfg.MaxDownloadRateKbps,
		"listen_port":              a.cfg.ListenPort,
	}
}

func (a *Adapter) ExtraPanelHTML() template.HTML {
	return template.HTML(a.renderPanel())
}

func (a *Adapter) setState(s adapters.State, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = s
	a.lastErr = errMsg
	a.stateSince = time.Now()
}

func (a *Adapter) emit(msg string) {
	if a.eventLog == nil {
		return
	}
	a.eventLog.Append(eventlog.Entry{Time: time.Now(), Severity: eventlog.SeverityInfo, Source: "torrent", Message: msg})
}

func (a *Adapter) logSafe(msg string, args ...any) {
	slog.Info(msg, args...)
}
```

- [ ] **Step 6: Add temporary stubs for later-task symbols**

Add these compile stubs at the bottom of `adapter.go`. Later tasks replace each stub with production code in the named file.

```go
type ClientConfig struct{}
type TorrentClient interface{ Close() error }
type TorrentHandle interface{}
type Session struct{}

func newRealClient(ClientConfig) (TorrentClient, error) { return nil, fmt.Errorf("torrent client dependency not linked") }
func (a *Adapter) renderPanel() string                  { return `<section id="torrent-panel"></section>` }
func (a *Adapter) UIRoutes() []adapters.Route           { return nil }
func (a *Adapter) MountPublicRoutes(*http.ServeMux)     {}
```

Add `net/http` to the `adapter.go` import list for the temporary public-route stub.

- [ ] **Step 7: Run tests and commit**

Run: `go test ./internal/adapters/torrent`

Expected: PASS.

Commit:

```bash
git add internal/adapters/torrent/config.go internal/adapters/torrent/adapter.go internal/adapters/torrent/config_test.go internal/adapters/torrent/adapter_test.go
git commit -m "feat(torrent): add adapter config skeleton"
```

---

### Task 2: Errors, Redaction, File Selection, and Title Sanitization

**Files:**
- Create: `internal/adapters/torrent/errors.go`
- Create: `internal/adapters/torrent/select.go`
- Create: `internal/adapters/torrent/errors_test.go`
- Create: `internal/adapters/torrent/select_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/adapters/torrent/errors_test.go`:

```go
package torrent

import (
	"errors"
	"net/http"
	"testing"
)

func TestTorrentErrorHTTPStatus(t *testing.T) {
	cases := []struct {
		kind TorrentErrorKind
		want int
	}{
		{ErrDisabled, http.StatusConflict},
		{ErrTrafficNotAcknowledged, http.StatusForbidden},
		{ErrBadInput, http.StatusBadRequest},
		{ErrUploadTooLarge, http.StatusRequestEntityTooLarge},
		{ErrMetadataTimeout, http.StatusGatewayTimeout},
		{ErrNoPlayableFile, http.StatusUnprocessableEntity},
		{ErrExpiredToken, http.StatusNotFound},
		{ErrNonLoopback, http.StatusForbidden},
		{ErrCoreStart, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		err := &TorrentError{Kind: tc.kind, Message: "x"}
		if got := torrentErrorStatus(err); got != tc.want {
			t.Fatalf("%s status = %d, want %d", tc.kind, got, tc.want)
		}
	}
	if got := torrentErrorStatus(errors.New("plain")); got != http.StatusInternalServerError {
		t.Fatalf("plain error status = %d, want 500", got)
	}
}

func TestRedactMagnetKeepsOnlyShortInfoHash(t *testing.T) {
	raw := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Movie&tr=http://tracker.example/announce&xs=http://x&ws=http://w&as=http://a"
	got := redactMagnet(raw)
	want := "magnet:?xt=urn:btih:01234567"
	if got != want {
		t.Fatalf("redactMagnet = %q, want %q", got, want)
	}
}

func TestRedactMagnetInvalid(t *testing.T) {
	if got := redactMagnet("magnet:?dn=Movie"); got != "magnet:<invalid>" {
		t.Fatalf("redactMagnet invalid = %q", got)
	}
	if got := redactMagnet("magnet:?xt=urn:btih:not-hex-at-all"); got != "magnet:<invalid>" {
		t.Fatalf("redactMagnet non-hex = %q", got)
	}
}
```

Create `internal/adapters/torrent/select_test.go`:

```go
package torrent

import "testing"

func TestPickLargestPlayableVideo(t *testing.T) {
	files := []FileCandidate{
		{DisplayPath: "disc/readme.txt", Length: 100},
		{DisplayPath: "extras/trailer.mp4", Length: 1000},
		{DisplayPath: "movie/Movie.mkv", Length: 9000},
	}
	got, err := pickLargestPlayable(files)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayPath != "movie/Movie.mkv" {
		t.Fatalf("selected %q, want movie/Movie.mkv", got.DisplayPath)
	}
}

func TestPickLargestPlayableTieBreaksByDisplayPath(t *testing.T) {
	files := []FileCandidate{
		{DisplayPath: "b/movie.mkv", Length: 100},
		{DisplayPath: "a/movie.mp4", Length: 100},
	}
	got, err := pickLargestPlayable(files)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayPath != "a/movie.mp4" {
		t.Fatalf("selected %q, want lexical first", got.DisplayPath)
	}
}

func TestPickLargestPlayableTieBreaksDuplicatePathByIndex(t *testing.T) {
	files := []FileCandidate{
		{DisplayPath: "movie.mkv", Length: 100, Index: 2},
		{DisplayPath: "movie.mkv", Length: 100, Index: 1},
	}
	got, err := pickLargestPlayable(files)
	if err != nil {
		t.Fatal(err)
	}
	if got.Index != 1 {
		t.Fatalf("selected index %d, want 1", got.Index)
	}
}


func TestPickLargestPlayableReturnsTypedError(t *testing.T) {
	_, err := pickLargestPlayable([]FileCandidate{{DisplayPath: "readme.txt", Length: 1}})
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrNoPlayableFile {
		t.Fatalf("error = %#v, want ErrNoPlayableFile", err)
	}
}

func TestSanitizeTitle(t *testing.T) {
	cases := map[string]string{
		"folder/Movie.Name.1999.mkv": "Movie.Name.1999.mkv",
		"folder\\Movie\x00Name.mp4": "MovieName.mp4",
		".":                         "Torrent video",
		"..":                        "Torrent video",
		"   ":                       "Torrent video",
	}
	for in, want := range cases {
		if got := sanitizeTitle(in); got != want {
			t.Fatalf("sanitizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/adapters/torrent`

Expected: FAIL with undefined symbols from `errors.go` and `select.go`.

- [ ] **Step 3: Implement errors and selection helpers**

Create `internal/adapters/torrent/errors.go`:

```go
package torrent

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type TorrentErrorKind string

const (
	ErrDisabled                TorrentErrorKind = "disabled"
	ErrTrafficNotAcknowledged  TorrentErrorKind = "traffic_not_acknowledged"
	ErrBadInput                TorrentErrorKind = "bad_input"
	ErrUploadTooLarge          TorrentErrorKind = "upload_too_large"
	ErrMetadataTimeout         TorrentErrorKind = "metadata_timeout"
	ErrNoPlayableFile          TorrentErrorKind = "no_playable_file"
	ErrExpiredToken            TorrentErrorKind = "expired_token"
	ErrNonLoopback             TorrentErrorKind = "non_loopback"
	ErrCoreStart               TorrentErrorKind = "core_start"
)

type TorrentError struct {
	Kind    TorrentErrorKind
	Message string
	Err     error
}

func (e *TorrentError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}

func (e *TorrentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func torrentErrorStatus(err error) int {
	var terr *TorrentError
	if !errors.As(err, &terr) {
		return http.StatusInternalServerError
	}
	switch terr.Kind {
	case ErrDisabled:
		return http.StatusConflict
	case ErrTrafficNotAcknowledged, ErrNonLoopback:
		return http.StatusForbidden
	case ErrBadInput:
		return http.StatusBadRequest
	case ErrUploadTooLarge:
		return http.StatusRequestEntityTooLarge
	case ErrMetadataTimeout:
		return http.StatusGatewayTimeout
	case ErrNoPlayableFile:
		return http.StatusUnprocessableEntity
	case ErrExpiredToken:
		return http.StatusNotFound
	case ErrCoreStart:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

func redactMagnet(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "magnet" {
		return "magnet:<invalid>"
	}
	for _, xt := range u.Query()["xt"] {
		const prefix = "urn:btih:"
		if !strings.HasPrefix(strings.ToLower(xt), prefix) {
			continue
		}
		hash := strings.ToLower(xt[len(prefix):])
		if len(hash) < 8 || !isHex(hash) {
			return "magnet:<invalid>"
		}
		return fmt.Sprintf("magnet:?xt=urn:btih:%s", hash[:8])
	}
	return "magnet:<invalid>"
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}
```

Create `internal/adapters/torrent/select.go`:

```go
package torrent

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type FileCandidate struct {
	DisplayPath string
	Length      int64
	Index       int
}

var playableExtensions = map[string]struct{}{
	".mp4": {}, ".m4v": {}, ".mkv": {}, ".avi": {}, ".mov": {},
	".mpg": {}, ".mpeg": {}, ".ts": {}, ".webm": {}, ".wmv": {},
}

func pickLargestPlayable(files []FileCandidate) (FileCandidate, error) {
	playable := make([]FileCandidate, 0, len(files))
	for _, f := range files {
		if _, ok := playableExtensions[strings.ToLower(filepath.Ext(f.DisplayPath))]; ok {
			playable = append(playable, f)
		}
	}
	if len(playable) == 0 {
		return FileCandidate{}, &TorrentError{Kind: ErrNoPlayableFile, Message: "torrent contains no playable video file"}
	}
	sort.SliceStable(playable, func(i, j int) bool {
		if playable[i].Length != playable[j].Length {
			return playable[i].Length > playable[j].Length
		}
		if playable[i].DisplayPath != playable[j].DisplayPath {
			return playable[i].DisplayPath < playable[j].DisplayPath
		}
		return playable[i].Index < playable[j].Index
	})
	return playable[0], nil
}

func sanitizeTitle(displayPath string) string {
	clean := strings.ReplaceAll(displayPath, "\\", "/")
	base := filepath.Base(clean)
	var b strings.Builder
	for _, r := range base {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		return "Torrent video"
	}
	return out
}
```

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/adapters/torrent`

Expected: PASS.

Commit:

```bash
git add internal/adapters/torrent/errors.go internal/adapters/torrent/select.go internal/adapters/torrent/errors_test.go internal/adapters/torrent/select_test.go
git commit -m "feat(torrent): add safe input helpers"
```

---

### Task 3: Cache Safety

**Files:**
- Create: `internal/adapters/torrent/cache.go`
- Create: `internal/adapters/torrent/cache_test.go`

- [ ] **Step 1: Write failing cache tests**

Create `internal/adapters/torrent/cache_test.go`:

```go
package torrent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheRootUsesOwnedChild(t *testing.T) {
	dataDir := t.TempDir()
	cfg := DefaultConfig()
	got := cacheRoot(cfg, dataDir)
	want := filepath.Join(dataDir, "groovyrelay-torrent", "storage")
	if got != want {
		t.Fatalf("cacheRoot = %q, want %q", got, want)
	}
}

func TestCacheRootWithConfiguredDownloadDirUsesOwnedChild(t *testing.T) {
	dataDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.DownloadDir = filepath.Join(dataDir, "cache-parent")
	got := cacheRoot(cfg, dataDir)
	want := filepath.Join(cfg.DownloadDir, "groovyrelay-torrent", "storage")
	if got != want {
		t.Fatalf("cacheRoot = %q, want %q", got, want)
	}
}

func TestCreateSessionDirWritesMarkerBeforeData(t *testing.T) {
	root := t.TempDir()
	dir, err := createSessionDir(root, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, markerFileName)); err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	if !isMarkedSessionDir(dir) {
		t.Fatalf("isMarkedSessionDir(%q) = false, want true", dir)
	}
}

func TestRemoveSessionDirRefusesUnmarkedDirectory(t *testing.T) {
	root := t.TempDir()
	unmarked := filepath.Join(root, "session-a")
	if err := os.MkdirAll(unmarked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeSessionDir(root, unmarked); err == nil {
		t.Fatal("removeSessionDir unmarked succeeded, want error")
	}
	if _, err := os.Stat(unmarked); err != nil {
		t.Fatalf("unmarked directory was removed: %v", err)
	}
}

func TestRemoveSessionDirRemovesOnlyMarkedChild(t *testing.T) {
	root := t.TempDir()
	dir, err := createSessionDir(root, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeSessionDir(root, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("session dir still exists or stat failed differently: %v", err)
	}
}

func TestRemoveSessionDirRefusesRoot(t *testing.T) {
	root := t.TempDir()
	if err := removeSessionDir(root, root); err == nil {
		t.Fatal("removeSessionDir(root, root) succeeded, want error")
	}
}

func TestPruneStorageCacheRequiresMarkedRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "oldhash"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pruneStorageCache(root, 1, nil); err == nil {
		t.Fatal("pruneStorageCache on unmarked root succeeded, want error")
	}
}

func TestPruneStorageCacheSkipsActiveNewFileByInfoHashDir(t *testing.T) {
	root := t.TempDir()
	if err := ensureStorageRoot(root); err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(root, infoHashStorageDirName("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	activeKey := infoHashStorageDirName("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	activeDir := filepath.Join(root, activeKey)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "data.bin"), []byte("old-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeDir, "data.bin"), []byte("active-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	active := map[string]struct{}{activeKey: {}}
	if err := pruneStorageCache(root, int64(len("active-data")), active); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old inactive dir still exists or stat failed differently: %v", err)
	}
	if _, err := os.Stat(activeDir); err != nil {
		t.Fatalf("active dir was removed: %v", err)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/adapters/torrent`

Expected: FAIL with undefined cache helpers.

- [ ] **Step 3: Implement cache helper**

Create `internal/adapters/torrent/cache.go`:

```go
package torrent

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const markerFileName = ".groovyrelay-torrent-session.json"
const storageRootMarkerName = ".groovyrelay-torrent-storage-root.json"

type sessionMarker struct {
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
}

func cacheRoot(cfg Config, dataDir string) string {
	return filepath.Join(ownedDownloadRoot(cfg.DownloadDir, dataDir), "storage")
}

func sessionRoot(cfg Config, dataDir string) string {
	return filepath.Join(ownedDownloadRoot(cfg.DownloadDir, dataDir), "sessions")
}

func infoHashStorageDirName(infoHash string) string {
	return strings.ToLower(infoHash)
}

func createSessionDir(root, sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("session id required")
	}
	dir := filepath.Join(root, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	marker := sessionMarker{SessionID: sessionID, CreatedAt: time.Now().UTC()}
	body, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, markerFileName), body, 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

func isMarkedSessionDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, markerFileName))
	return err == nil && !info.IsDir()
}

func removeSessionDir(root, dir string) error {
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return err
	}
	dirAbs, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return err
	}
	if samePath(rootAbs, dirAbs) {
		return fmt.Errorf("refuse to remove torrent session root")
	}
	rel, err := filepath.Rel(rootAbs, dirAbs)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return fmt.Errorf("refuse to remove path outside torrent session root")
	}
	if !isMarkedSessionDir(dirAbs) {
		return fmt.Errorf("refuse to remove unmarked torrent session directory")
	}
	return os.RemoveAll(dirAbs)
}

func ensureStorageRoot(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(map[string]any{
		"kind":       "groovyrelay-torrent-storage-root",
		"created_at": time.Now().UTC(),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, storageRootMarkerName), body, 0o600)
}

func isMarkedStorageRoot(root string) bool {
	info, err := os.Stat(filepath.Join(root, storageRootMarkerName))
	return err == nil && !info.IsDir()
}

type cacheEntry struct {
	Path    string
	Name    string
	Size    int64
	ModTime time.Time
}

func pruneStorageCache(root string, maxBytes int64, active map[string]struct{}) error {
	if maxBytes <= 0 {
		return nil
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return err
	}
	if !isMarkedStorageRoot(rootAbs) {
		return fmt.Errorf("refuse to prune unmarked torrent storage root")
	}
	entries, total, err := collectCacheEntries(rootAbs)
	if err != nil {
		return err
	}
	if total <= maxBytes {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ModTime.Before(entries[j].ModTime)
	})
	for _, entry := range entries {
		if _, ok := active[entry.Name]; ok {
			continue
		}
		if err := os.RemoveAll(entry.Path); err != nil {
			return err
		}
		total -= entry.Size
		if total <= maxBytes {
			return nil
		}
	}
	return nil
}

func collectCacheEntries(root string) ([]cacheEntry, int64, error) {
	children, err := os.ReadDir(root)
	if err != nil {
		return nil, 0, err
	}
	var entries []cacheEntry
	var total int64
	for _, child := range children {
		if !child.IsDir() {
			continue
		}
		path := filepath.Join(root, child.Name())
		info, err := child.Info()
		if err != nil {
			return nil, 0, err
		}
		size, err := dirSize(path)
		if err != nil {
			return nil, 0, err
		}
		total += size
		entries = append(entries, cacheEntry{Path: path, Name: child.Name(), Size: size, ModTime: info.ModTime()})
	}
	return entries, total, nil
}

func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
```

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/adapters/torrent`

Expected: PASS.

Commit:

```bash
git add internal/adapters/torrent/cache.go internal/adapters/torrent/cache_test.go
git commit -m "feat(torrent): add cache deletion guardrails"
```

---

### Task 4: Client Abstraction and Session Orchestration Tests

**Files:**
- Replace temporary stubs in: `internal/adapters/torrent/adapter.go`
- Create: `internal/adapters/torrent/client.go`
- Create: `internal/adapters/torrent/session.go`
- Create: `internal/adapters/torrent/session_test.go`

- [ ] **Step 1: Write fake torrent/client tests**

Create `internal/adapters/torrent/session_test.go`:

```go
package torrent

import (
	"context"
	"errors"
	"io"
	"sync"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type fakeTorrentClient struct {
	magnets []string
	byHash  map[string]*fakeTorrent
}

func (f *fakeTorrentClient) AddMagnet(ctx context.Context, raw string) (TorrentHandle, bool, error) {
	if f.byHash == nil {
		f.byHash = make(map[string]*fakeTorrent)
	}
	f.magnets = append(f.magnets, raw)
	hash := "0123456789abcdef0123456789abcdef01234567"
	if strings.Contains(raw, "other") {
		hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}
	if existing := f.byHash[hash]; existing != nil {
		return existing, false, nil
	}
	t := &fakeTorrent{hash: hash, name: "movie", files: []FileCandidate{{DisplayPath: "movie.mkv", Length: 10, Index: 0}}}
	f.byHash[hash] = t
	return t, true, nil
}

func (f *fakeTorrentClient) AddMetaInfo(ctx context.Context, body []byte) (TorrentHandle, bool, error) {
	return nil, false, errors.New("not used")
}

func (f *fakeTorrentClient) Close() error { return nil }

type fakeTorrent struct {
	hash  string
	name  string
	files []FileCandidate
}

func (f *fakeTorrent) InfoHash() string                        { return f.hash }
func (f *fakeTorrent) StorageKey() string                      { return infoHashStorageDirName(f.hash) }
func (f *fakeTorrent) Name() string                            { return f.name }
func (f *fakeTorrent) WaitInfo(context.Context) error          { return nil }
func (f *fakeTorrent) Files() []FileCandidate                  { return f.files }
func (f *fakeTorrent) Prioritize(int)                          {}
func (f *fakeTorrent) BytesCompleted(index int) int64 {
	if index < 0 || index >= len(f.files) {
		return 0
	}
	return f.files[index].Length
}
func (f *fakeTorrent) Open(context.Context, int) (ReadSeekCloser, error) {
	return stringReadSeekCloser{Reader: strings.NewReader("video")}, nil
}

type stringReadSeekCloser struct{ *strings.Reader }

func (s stringReadSeekCloser) Close() error { return nil }

var _ io.ReadSeeker = (*strings.Reader)(nil)

func newStartedTestAdapter(t *testing.T, cfg Config, client *fakeTorrentClient, core *recordingCore) *Adapter {
	t.Helper()
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir(), UI: config.UIConfig{HTTPPort: 32500}, HostIP: "127.0.0.1"},
		Core:   core,
		ClientFactory: func(ClientConfig) (TorrentClient, error) {
			return client, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.cfg = cfg
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestStartMagnetRequiresEnabledAndTrafficAcknowledged(t *testing.T) {
	cfg := DefaultConfig()
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, cfg, client, core)
	_, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrDisabled {
		t.Fatalf("disabled err = %#v, want ErrDisabled", err)
	}
	cfg.Enabled = true
	a = newStartedTestAdapter(t, cfg, client, core)
	_, err = a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrTrafficNotAcknowledged {
		t.Fatalf("traffic err = %#v, want ErrTrafficNotAcknowledged", err)
	}
	if len(client.magnets) != 0 {
		t.Fatalf("client used before consent: %d calls", len(client.magnets))
	}
}

func TestStartMagnetBuildsCoreRequestWithPolicy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.TrafficAcknowledged = true
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, cfg, client, core)
	started, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if started.Token == "" {
		t.Fatal("started token empty")
	}
	if len(core.reqs) != 1 {
		t.Fatalf("core StartSession calls = %d, want 1", len(core.reqs))
	}
	req := core.reqs[0]
	if req.Source != "torrent" {
		t.Fatalf("Source = %q, want torrent", req.Source)
	}
	if req.AdapterRef == "" || !strings.HasPrefix(req.AdapterRef, "torrent:") {
		t.Fatalf("AdapterRef = %q, want torrent prefix", req.AdapterRef)
	}
	if !req.DirectPlay || !req.Capabilities.CanPause || !req.Capabilities.CanSeek {
		t.Fatalf("capabilities/direct play wrong: %#v direct=%v", req.Capabilities, req.DirectPlay)
	}
	if req.MediaInputPolicy.RWTimeout != 30*time.Second {
		t.Fatalf("RWTimeout = %s, want 30s", req.MediaInputPolicy.RWTimeout)
	}
	if got := strings.Join(req.MediaInputPolicy.ProtocolWhitelist, ","); got != "http,tcp" {
		t.Fatalf("ProtocolWhitelist = %q, want http,tcp", got)
	}
	if got := strings.Join(req.MediaInputPolicy.BlockedHeaders, ","); got != "Cookie,Authorization,Proxy-Authorization" {
		t.Fatalf("BlockedHeaders = %q", got)
	}
}

func TestSameInfoHashReusesTorrentObject(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.TrafficAcknowledged = true
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, cfg, client, core)
	first, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == second.Token {
		t.Fatalf("tokens equal: %q", first.Token)
	}
	if got := a.torrents["0123456789abcdef0123456789abcdef01234567"].refs; got != 2 {
		t.Fatalf("torrent refs = %d, want 2", got)
	}
}

func TestActiveSessionSurvivesMidSessionGateToggle(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.TrafficAcknowledged = true
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, cfg, client, core)
	started, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	a.SetEnabled(false)
	a.mu.Lock()
	a.cfg.TrafficAcknowledged = false
	_, stillActive := a.sessions[started.Token]
	a.mu.Unlock()
	if !stillActive {
		t.Fatal("active session was removed after enabled/traffic_acknowledged gates changed")
	}
	_, err = a.startMagnet(context.Background(), "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrDisabled {
		t.Fatalf("new session after disabled err = %#v, want ErrDisabled", err)
	}
}

func TestOnStopCleanupIsIdempotentUnderConcurrentCalls(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.TrafficAcknowledged = true
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, cfg, client, core)
	started, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if len(core.reqs) != 1 || core.reqs[0].OnStop == nil {
		t.Fatalf("core request missing OnStop: %#v", core.reqs)
	}
	var wg sync.WaitGroup
	for _, reason := range []string{"stopped", "preempted", "error"} {
		wg.Add(1)
		go func(reason string) {
			defer wg.Done()
			core.reqs[0].OnStop(reason)
		}(reason)
	}
	wg.Wait()
	a.mu.Lock()
	_, sessionExists := a.sessions[started.Token]
	_, torrentExists := a.torrents["0123456789abcdef0123456789abcdef01234567"]
	a.mu.Unlock()
	if sessionExists {
		t.Fatal("session still registered after concurrent OnStop cleanup")
	}
	if torrentExists {
		t.Fatal("torrent ref still registered after concurrent OnStop cleanup")
	}
}

func TestDifferentInfoHashStartsSecondCoreSession(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.TrafficAcknowledged = true
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, cfg, client, core)
	first, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=other")
	if err != nil {
		t.Fatal(err)
	}
	if first.AdapterRef == second.AdapterRef {
		t.Fatalf("adapter refs equal: %q", first.AdapterRef)
	}
	if len(core.reqs) != 2 {
		t.Fatalf("core StartSession calls = %d, want 2", len(core.reqs))
	}
	if core.reqs[0].AdapterRef == core.reqs[1].AdapterRef {
		t.Fatalf("core adapter refs equal: %q", core.reqs[0].AdapterRef)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/adapters/torrent`

Expected: FAIL with undefined client/session methods and temporary stub type conflicts.

- [ ] **Step 3: Replace stubs with real interfaces**

Remove the temporary type stubs from `adapter.go`. Create `internal/adapters/torrent/client.go`:

```go
package torrent

import (
	"context"
	"fmt"
	"io"
)

type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

type ClientConfig struct {
	Config    Config
	DataDir   string
	CacheRoot string
}

type TorrentClient interface {
	AddMagnet(context.Context, string) (TorrentHandle, bool, error)
	AddMetaInfo(context.Context, []byte) (TorrentHandle, bool, error)
	Close() error
}

type TorrentHandle interface {
	InfoHash() string
	StorageKey() string
	Name() string
	WaitInfo(context.Context) error
	Files() []FileCandidate
	Prioritize(index int)
	BytesCompleted(index int) int64
	Open(context.Context, int) (ReadSeekCloser, error)
}

func newRealClient(ClientConfig) (TorrentClient, error) {
	return nil, fmt.Errorf("torrent client dependency not linked")
}
```

Task 7 replaces the `newRealClient` stub with the real anacrolix implementation.

- [ ] **Step 4: Implement session orchestration**

Create `internal/adapters/torrent/session.go`:

```go
package torrent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type StartedSession struct {
	Token      string `json:"token"`
	AdapterRef string `json:"adapter_ref"`
	Title      string `json:"title"`
}

type Session struct {
	ID          string
	Token       string
	InfoHash    string
	StorageKey  string
	FileIndex   int
	Title       string
	SessionDir  string
	Torrent     TorrentHandle
	KeepData    bool
	CleanupOnce cleanupOnce
}

type cleanupOnce struct {
	done bool
}

func (a *Adapter) startMagnet(ctx context.Context, raw string) (*StartedSession, error) {
	if _, err := url.Parse(raw); err != nil {
		return nil, &TorrentError{Kind: ErrBadInput, Message: "invalid magnet link", Err: err}
	}
	cfg, err := a.snapshotForStart()
	if err != nil {
		return nil, err
	}
	client, err := a.ensureClient(cfg)
	if err != nil {
		return nil, err
	}
	t, _, err := client.AddMagnet(ctx, raw)
	if err != nil {
		return nil, &TorrentError{Kind: ErrBadInput, Message: "magnet could not be added", Err: err}
	}
	return a.startTorrentHandle(ctx, cfg, t)
}

func (a *Adapter) startTorrentBytes(ctx context.Context, body []byte) (*StartedSession, error) {
	cfg, err := a.snapshotForStart()
	if err != nil {
		return nil, err
	}
	client, err := a.ensureClient(cfg)
	if err != nil {
		return nil, err
	}
	t, _, err := client.AddMetaInfo(ctx, body)
	if err != nil {
		return nil, &TorrentError{Kind: ErrBadInput, Message: "torrent file could not be added", Err: err}
	}
	return a.startTorrentHandle(ctx, cfg, t)
}

func (a *Adapter) snapshotForStart() (Config, error) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if !cfg.Enabled {
		return Config{}, &TorrentError{Kind: ErrDisabled, Message: "torrent adapter is disabled"}
	}
	if !cfg.TrafficAcknowledged {
		return Config{}, &TorrentError{Kind: ErrTrafficNotAcknowledged, Message: "BitTorrent traffic must be acknowledged before starting a torrent"}
	}
	return cfg, nil
}

func (a *Adapter) ensureClient(cfg Config) (TorrentClient, error) {
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client != nil {
		return client, nil
	}
	root := cacheRoot(cfg, a.bridge.DataDir)
	if _, err := provisionDownloadRoot(cfg, a.bridge.DataDir); err != nil {
		return nil, err
	}
	if err := ensureStorageRoot(root); err != nil {
		return nil, err
	}
	newClient, err := a.factory(ClientConfig{Config: cfg, DataDir: a.bridge.DataDir, CacheRoot: root})
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		_ = newClient.Close()
		return a.client, nil
	}
	a.client = newClient
	return newClient, nil
}

func (a *Adapter) startTorrentHandle(ctx context.Context, cfg Config, t TorrentHandle) (*StartedSession, error) {
	metaCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.MetadataTimeoutSeconds)*time.Second)
	defer cancel()
	if err := t.WaitInfo(metaCtx); err != nil {
		return nil, &TorrentError{Kind: ErrMetadataTimeout, Message: "timed out waiting for torrent metadata", Err: err}
	}
	file, err := pickLargestPlayable(t.Files())
	if err != nil {
		return nil, err
	}
	t.Prioritize(file.Index)
	waitForStartupBuffer(ctx, cfg, t, file.Index)
	sessionID := newID("torrent")
	token := newID("tok")
	root := sessionRoot(cfg, a.bridge.DataDir)
	dir, err := createSessionDir(root, sessionID)
	if err != nil {
		return nil, err
	}
	s := &Session{
		ID:         sessionID,
		Token:      token,
		InfoHash:   t.InfoHash(),
		StorageKey: t.StorageKey(),
		FileIndex:  file.Index,
		Title:      sanitizeTitle(file.DisplayPath),
		SessionDir: dir,
		Torrent:    t,
		KeepData:   cfg.KeepCompleted,
	}
	a.registerSession(s)
	req := core.SessionRequest{
		StreamURL:      a.mediaURL(token),
		Capabilities:   core.Capabilities{CanSeek: true, CanPause: true},
		AdapterRef:     "torrent:" + sessionID,
		Source:         "torrent",
		DirectPlay:     true,
		Title:          s.Title,
		MediaInputPolicy: core.MediaInputPolicy{
			ProtocolWhitelist: []string{"http", "tcp"},
			DisableRedirects:  true,
			DisableReconnect:  true,
			RWTimeout:         30 * time.Second,
			BlockedHeaders:    []string{"Cookie", "Authorization", "Proxy-Authorization"},
		},
		OnStop: func(reason string) {
			a.cleanupSession(token, reason)
		},
	}
	if a.core == nil {
		a.cleanupSession(token, "error")
		return nil, &TorrentError{Kind: ErrCoreStart, Message: "core not wired"}
	}
	if err := a.core.StartSession(req); err != nil {
		a.cleanupSession(token, "error")
		return nil, &TorrentError{Kind: ErrCoreStart, Message: "core start failed", Err: err}
	}
	return &StartedSession{Token: token, AdapterRef: req.AdapterRef, Title: s.Title}, nil
}

func (a *Adapter) registerSession(s *Session) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions[s.Token] = s
	a.activeToken = s.Token
	use := a.torrents[s.InfoHash]
	if use == nil {
		use = &torrentUse{torrent: s.Torrent}
		a.torrents[s.InfoHash] = use
	}
	use.refs++
}

func (a *Adapter) cleanupSession(token, reason string) {
	a.mu.Lock()
	s := a.sessions[token]
	if s == nil || s.CleanupOnce.done {
		a.mu.Unlock()
		return
	}
	s.CleanupOnce.done = true
	delete(a.sessions, token)
	if a.activeToken == token {
		a.activeToken = ""
	}
	if use := a.torrents[s.InfoHash]; use != nil {
		use.refs--
		if use.refs <= 0 {
			delete(a.torrents, s.InfoHash)
		}
	}
	cfg := a.cfg
	active := make(map[string]struct{}, len(a.sessions))
	for _, live := range a.sessions {
		active[live.StorageKey] = struct{}{}
	}
	a.mu.Unlock()
	if !s.KeepData {
		_ = removeSessionDir(sessionRoot(cfg, a.bridge.DataDir), s.SessionDir)
	}
	_ = pruneStorageCache(cacheRoot(cfg, a.bridge.DataDir), cfg.MaxCacheBytes, active)
	a.logSafe("torrent session stopped", "token", token, "reason", reason, "hash", shortHash(s.InfoHash))
}

func waitForStartupBuffer(ctx context.Context, cfg Config, t TorrentHandle, index int) {
	if cfg.StartupBufferSeconds <= 0 || t.BytesCompleted(index) > 0 {
		return
	}
	timer := time.NewTimer(time.Duration(cfg.StartupBufferSeconds) * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		case <-ticker.C:
			if t.BytesCompleted(index) > 0 {
				return
			}
		}
	}
}

func (a *Adapter) mediaURL(token string) string {
	host := a.bridge.HostIP
	if net.ParseIP(host) == nil {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d/torrent/session/%s/media", host, a.bridge.UI.HTTPPort, token)
}

func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}

func shortHash(hash string) string {
	if len(hash) < 8 {
		return hash
	}
	return hash[:8]
}
```

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/adapters/torrent`

Expected: PASS.

Commit:

```bash
git add internal/adapters/torrent/client.go internal/adapters/torrent/session.go internal/adapters/torrent/session_test.go internal/adapters/torrent/adapter.go
git commit -m "feat(torrent): orchestrate torrent sessions"
```

---

### Task 5: Loopback-Only Media Route

**Files:**
- Create: `internal/adapters/torrent/server.go`
- Create: `internal/adapters/torrent/server_test.go`
- Modify: `internal/adapters/torrent/adapter.go`

- [ ] **Step 1: Write failing route tests**

Create `internal/adapters/torrent/server_test.go`:

```go
package torrent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsLoopbackRemoteAcceptsIPv4AndIPv6Loopback(t *testing.T) {
	for _, remote := range []string{"127.0.0.1:1234", "127.42.0.9:1234", "[::1]:1234"} {
		if !isLoopbackRemote(remote) {
			t.Fatalf("isLoopbackRemote(%q) = false, want true", remote)
		}
	}
}

func TestIsLoopbackRemoteRejectsLAN(t *testing.T) {
	for _, remote := range []string{"192.168.1.5:1234", "10.0.0.5:1234", "example.com:1234"} {
		if isLoopbackRemote(remote) {
			t.Fatalf("isLoopbackRemote(%q) = true, want false", remote)
		}
	}
}

func TestMediaRouteRejectsNonLoopback(t *testing.T) {
	a := &Adapter{sessions: map[string]*Session{}}
	req := httptest.NewRequest(http.MethodGet, "/torrent/session/missing/media", nil)
	req.RemoteAddr = "192.168.1.5:1234"
	rr := httptest.NewRecorder()
	a.handleMedia(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestMediaRouteMissingToken(t *testing.T) {
	a := &Adapter{sessions: map[string]*Session{}}
	req := httptest.NewRequest(http.MethodGet, "/torrent/session/missing/media", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	a.handleMedia(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestMediaRouteServesRange(t *testing.T) {
	torrent := &fakeTorrent{hash: "01234567", files: []FileCandidate{{DisplayPath: "movie.mkv", Length: 5, Index: 0}}}
	a := &Adapter{sessions: map[string]*Session{
		"tok": {Token: "tok", Torrent: torrent, FileIndex: 0, Title: "movie.mkv"},
	}}
	req := httptest.NewRequest(http.MethodGet, "/torrent/session/tok/media", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Range", "bytes=1-3")
	rr := httptest.NewRecorder()
	a.handleMedia(rr, req)
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 body=%q", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != "ide" {
		t.Fatalf("body = %q, want ide", got)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "video") && ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/adapters/torrent`

Expected: FAIL with undefined media route helpers.

- [ ] **Step 3: Implement public media route**

Create `internal/adapters/torrent/server.go`:

```go
package torrent

import (
	"net"
	"net/http"
	"path"
	"strings"
	"time"
)

func (a *Adapter) MountPublicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /torrent/session/{token}/media", a.handleMedia)
}

func (a *Adapter) handleMedia(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		http.Error(w, "loopback required", http.StatusForbidden)
		return
	}
	token := r.PathValue("token")
	if token == "" {
		token = tokenFromPath(r.URL.Path)
	}
	a.mu.Lock()
	s := a.sessions[token]
	a.mu.Unlock()
	if s == nil {
		http.Error(w, "media token not found", http.StatusNotFound)
		return
	}
	reader, err := s.Torrent.Open(r.Context(), s.FileIndex)
	if err != nil {
		http.Error(w, "open torrent media", http.StatusInternalServerError)
		return
	}
	defer reader.Close()
	http.ServeContent(w, r, s.Title, time.Time{}, reader)
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func tokenFromPath(p string) string {
	clean := path.Clean(p)
	parts := strings.Split(clean, "/")
	if len(parts) >= 4 && parts[1] == "torrent" && parts[2] == "session" {
		return parts[3]
	}
	return ""
}
```

Remove the temporary `MountPublicRoutes` stub from `adapter.go`.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/adapters/torrent`

Expected: PASS.

Commit:

```bash
git add internal/adapters/torrent/server.go internal/adapters/torrent/server_test.go internal/adapters/torrent/adapter.go
git commit -m "feat(torrent): serve tokenized loopback media"
```

---

### Task 6: UI Routes and Panel

**Files:**
- Create: `internal/adapters/torrent/routes.go`
- Create: `internal/adapters/torrent/ui.go`
- Create: `internal/adapters/torrent/routes_test.go`
- Create: `internal/adapters/torrent/ui_test.go`
- Modify: `internal/adapters/torrent/adapter.go`

- [ ] **Step 1: Write failing UI route tests**

Create `internal/adapters/torrent/routes_test.go`:

```go
package torrent

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUIRoutes(t *testing.T) {
	a := &Adapter{}
	routes := a.UIRoutes()
	want := map[string]string{
		"GET panel":          "",
		"GET status":         "",
		"POST play":          "",
		"POST upload":        "",
		"POST stop":          "",
	}
	for _, route := range routes {
		want[route.Method+" "+route.Path] = "seen"
	}
	for key, seen := range want {
		if seen != "seen" {
			t.Fatalf("missing route %s", key)
		}
	}
}

func TestHandlePlayMagnetUsesFormField(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.TrafficAcknowledged = true
	core := &recordingCore{}
	a := newStartedTestAdapter(t, cfg, &fakeTorrentClient{}, core)
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/torrent/play", strings.NewReader("magnet=magnet%3A%3Fxt%3Durn%3Abtih%3A0123456789abcdef0123456789abcdef01234567"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	a.handlePlay(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rr.Code, rr.Body.String())
	}
}

func TestHandleUploadRejectsOversizedTorrent(t *testing.T) {
	a := &Adapter{}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("torrent_file", "too-large.torrent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(strings.Repeat("x", maxTorrentUploadBytes+1))); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/torrent/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	a.handleUpload(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
}
```

Create `internal/adapters/torrent/ui_test.go`:

```go
package torrent

import (
	"strings"
	"testing"
)

func TestExtraPanelHTMLContainsConsentAndUpload(t *testing.T) {
	a := &Adapter{cfg: DefaultConfig(), sessions: map[string]*Session{}}
	html := string(a.ExtraPanelHTML())
	for _, want := range []string{"torrent-panel", "name=\"magnet\"", "name=\"torrent_file\"", "traffic_acknowledged"} {
		if !strings.Contains(html, want) {
			t.Fatalf("panel missing %q: %s", want, html)
		}
	}
}

func TestRenderPanelEscapesActiveTitle(t *testing.T) {
	a := &Adapter{
		cfg:         DefaultConfig(),
		sessions:    map[string]*Session{"tok": {Token: "tok", Title: `<script>alert(1)</script>`}},
		activeToken: "tok",
	}
	html := a.renderPanel()
	if strings.Contains(html, "<script>alert") {
		t.Fatalf("panel did not escape title: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("panel missing escaped title: %s", html)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/adapters/torrent`

Expected: FAIL with undefined handlers and UI helpers.

- [ ] **Step 3: Implement UI routes**

Create `internal/adapters/torrent/routes.go`:

```go
package torrent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

const maxTorrentUploadBytes = 4 * 1024 * 1024
const maxTorrentMultipartOverheadBytes = 64 * 1024

func (a *Adapter) UIRoutes() []adapters.Route {
	return []adapters.Route{
		{Method: http.MethodGet, Path: "panel", Handler: a.handlePanel},
		{Method: http.MethodGet, Path: "status", Handler: a.handleStatus},
		{Method: http.MethodPost, Path: "play", Handler: a.handlePlay},
		{Method: http.MethodPost, Path: "upload", Handler: a.handleUpload},
		{Method: http.MethodPost, Path: "stop", Handler: a.handleStop},
	}
}

func (a *Adapter) handlePanel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(a.renderPanel()))
}

func (a *Adapter) handleStatus(w http.ResponseWriter, r *http.Request) {
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, a.statusView())
		return
	}
	a.handlePanel(w, r)
}

func (a *Adapter) handlePlay(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.respondRouteError(w, r, http.StatusBadRequest, "parse form: "+err.Error())
		return
	}
	raw := strings.TrimSpace(r.Form.Get("magnet"))
	if raw == "" {
		a.respondRouteError(w, r, http.StatusBadRequest, "magnet is required")
		return
	}
	started, err := a.startMagnet(r.Context(), raw)
	if err != nil {
		a.respondRouteError(w, r, torrentErrorStatus(err), err.Error())
		return
	}
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, started)
		return
	}
	a.handlePanel(w, r)
}

func (a *Adapter) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTorrentUploadBytes+maxTorrentMultipartOverheadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		a.respondRouteError(w, r, http.StatusBadRequest, "parse upload: "+err.Error())
		return
	}
	var body []byte
	found := false
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				a.respondRouteError(w, r, http.StatusRequestEntityTooLarge, "torrent upload exceeds 4 MiB")
				return
			}
			a.respondRouteError(w, r, http.StatusBadRequest, "read multipart: "+err.Error())
			return
		}
		if part.FormName() != "torrent_file" {
			_ = part.Close()
			continue
		}
		found = true
		body, err = io.ReadAll(io.LimitReader(part, maxTorrentUploadBytes+1))
		_ = part.Close()
		if err != nil {
			a.respondRouteError(w, r, http.StatusBadRequest, "read torrent_file: "+err.Error())
			return
		}
		break
	}
	if !found {
		a.respondRouteError(w, r, http.StatusBadRequest, "torrent_file is required")
		return
	}
	if len(body) > maxTorrentUploadBytes {
		a.respondRouteError(w, r, http.StatusRequestEntityTooLarge, "torrent file exceeds 4 MiB")
		return
	}
	started, err := a.startTorrentBytes(r.Context(), body)
	if err != nil {
		a.respondRouteError(w, r, torrentErrorStatus(err), err.Error())
		return
	}
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, started)
		return
	}
	a.handlePanel(w, r)
}

func (a *Adapter) handleStop(w http.ResponseWriter, r *http.Request) {
	if a.core == nil {
		a.respondRouteError(w, r, http.StatusInternalServerError, "core not wired")
		return
	}
	if err := a.core.Stop(); err != nil {
		a.respondRouteError(w, r, http.StatusConflict, err.Error())
		return
	}
	if wantsJSON(r) {
		respondJSON(w, http.StatusOK, a.statusView())
		return
	}
	a.handlePanel(w, r)
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *Adapter) respondRouteError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	a.setState(adapters.StateError, msg)
	if wantsJSON(r) {
		respondJSON(w, status, map[string]string{"error": msg})
		return
	}
	http.Error(w, msg, status)
}
```

- [ ] **Step 4: Implement UI rendering**

Create `internal/adapters/torrent/ui.go`:

```go
package torrent

import (
	"fmt"
	"html/template"
	"strings"
)

type statusView struct {
	Enabled             bool   `json:"enabled"`
	TrafficAcknowledged bool   `json:"traffic_acknowledged"`
	ActiveTitle         string `json:"active_title,omitempty"`
	ActiveToken         string `json:"active_token,omitempty"`
}

func (a *Adapter) statusView() statusView {
	a.mu.Lock()
	defer a.mu.Unlock()
	view := statusView{
		Enabled:             a.cfg.Enabled,
		TrafficAcknowledged: a.cfg.TrafficAcknowledged,
		ActiveToken:         a.activeToken,
	}
	if s := a.sessions[a.activeToken]; s != nil {
		view.ActiveTitle = s.Title
	}
	return view
}

func (a *Adapter) renderPanel() string {
	view := a.statusView()
	var b strings.Builder
	b.WriteString(`<section class="torrent-panel" id="torrent-panel" hx-get="/ui/adapter/torrent/panel" hx-trigger="every 5s" hx-swap="outerHTML">`)
	b.WriteString(`<h3>Torrent</h3>`)
	if !view.Enabled {
		b.WriteString(`<p class="status">Disabled</p>`)
	} else if !view.TrafficAcknowledged {
		b.WriteString(`<p class="status err">BitTorrent traffic acknowledgement required</p>`)
	} else if view.ActiveTitle != "" {
		fmt.Fprintf(&b, `<p class="status run">Playing: <code>%s</code></p>`, template.HTMLEscapeString(view.ActiveTitle))
	} else {
		b.WriteString(`<p class="status">Idle</p>`)
	}
	b.WriteString(`<p class="muted">Enable <code>traffic_acknowledged</code> before starting magnet links or torrent uploads.</p>`)
	b.WriteString(`<form hx-post="/ui/adapter/torrent/play" hx-target="#torrent-panel" hx-swap="outerHTML" autocomplete="off">`)
	b.WriteString(`<label>Magnet <input type="text" name="magnet" required></label>`)
	b.WriteString(`<button type="submit">Play Magnet</button>`)
	b.WriteString(`</form>`)
	b.WriteString(`<form hx-post="/ui/adapter/torrent/upload" hx-target="#torrent-panel" hx-swap="outerHTML" enctype="multipart/form-data">`)
	b.WriteString(`<input type="file" name="torrent_file" accept=".torrent" required>`)
	b.WriteString(`<button type="submit">Upload Torrent</button>`)
	b.WriteString(`</form>`)
	if view.ActiveToken != "" {
		b.WriteString(`<form hx-post="/ui/adapter/torrent/stop" hx-target="#torrent-panel" hx-swap="outerHTML">`)
		b.WriteString(`<button type="submit">Stop</button>`)
		b.WriteString(`</form>`)
	}
	b.WriteString(`</section>`)
	return b.String()
}
```

Remove the temporary `renderPanel` and `UIRoutes` stubs from `adapter.go`.

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/adapters/torrent`

Expected: PASS.

Commit:

```bash
git add internal/adapters/torrent/routes.go internal/adapters/torrent/ui.go internal/adapters/torrent/routes_test.go internal/adapters/torrent/ui_test.go internal/adapters/torrent/adapter.go
git commit -m "feat(torrent): add UI routes and panel"
```

---

### Task 7: Real anacrolix/torrent Wrapper

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `internal/adapters/torrent/client.go`
- Create: `internal/adapters/torrent/client_real_test.go`

- [ ] **Step 1: Add dependency**

Run:

```bash
go get github.com/anacrolix/torrent@v1.61.0
go mod tidy
```

Expected: `go.mod` includes `github.com/anacrolix/torrent v1.61.0`, a direct requirement for `golang.org/x/time` if needed by the `rate` import, and `go.sum` includes new module checksums.

- [ ] **Step 2: Write compile-focused real wrapper test**

Create `internal/adapters/torrent/client_real_test.go`:

```go
package torrent

import "testing"

func TestNewRealClientRejectsEmptyCacheRoot(t *testing.T) {
	_, err := newRealClient(ClientConfig{Config: DefaultConfig(), DataDir: t.TempDir()})
	if err == nil {
		t.Fatal("newRealClient without CacheRoot succeeded, want error")
	}
}
```

- [ ] **Step 3: Implement real wrapper in `client.go`**

Replace the import block in `internal/adapters/torrent/client.go` with this combined import block. Use aliases because this package is also named `torrent`.

```go
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	atorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"
)
```

Delete the temporary `newRealClient` stub from Task 4, then append this implementation to `internal/adapters/torrent/client.go`. `Torrent.GotInfo()` returns `events.Done` in anacrolix/torrent v1.61.0; `waitDone(ctx, done <-chan struct{})` is the compile-time check that this remains selectable as a receive-only done channel.

```go

type realClient struct {
	client *atorrent.Client
}

func newRealClient(cfg ClientConfig) (TorrentClient, error) {
	if cfg.CacheRoot == "" {
		return nil, fmt.Errorf("torrent cache root is required")
	}
	tc := atorrent.NewDefaultClientConfig()
	tc.DataDir = cfg.CacheRoot
	tc.DefaultStorage = storage.NewFileByInfoHash(cfg.CacheRoot)
	tc.NoDefaultPortForwarding = true
	tc.Seed = false
	tc.Debug = false
	tc.SetListenAddr(":" + strconv.Itoa(cfg.Config.ListenPort))
	if cfg.Config.MaxUploadRateKbps > 0 {
		tc.UploadRateLimiter = rate.NewLimiter(rate.Limit(cfg.Config.MaxUploadRateKbps*1024), cfg.Config.MaxUploadRateKbps*1024)
	}
	if cfg.Config.MaxDownloadRateKbps > 0 {
		tc.DownloadRateLimiter = rate.NewLimiter(rate.Limit(cfg.Config.MaxDownloadRateKbps*1024), cfg.Config.MaxDownloadRateKbps*1024)
	}
	client, err := atorrent.NewClient(tc)
	if err != nil {
		return nil, err
	}
	return &realClient{client: client}, nil
}

func (c *realClient) AddMagnet(ctx context.Context, raw string) (TorrentHandle, bool, error) {
	spec, err := atorrent.TorrentSpecFromMagnetUri(raw)
	if err != nil {
		return nil, false, err
	}
	t, isNew, err := c.client.AddTorrentSpec(spec)
	if err != nil {
		return nil, false, err
	}
	return &realTorrent{torrent: t}, isNew, nil
}

func (c *realClient) AddMetaInfo(ctx context.Context, body []byte) (TorrentHandle, bool, error) {
	mi, err := metainfo.Load(bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	spec, err := atorrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		return nil, false, err
	}
	t, isNew, err := c.client.AddTorrentSpec(spec)
	if err != nil {
		return nil, false, err
	}
	return &realTorrent{torrent: t}, isNew, nil
}

func (c *realClient) Close() error {
	if errs := c.client.Close(); len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

type realTorrent struct {
	torrent *atorrent.Torrent
}

func (t *realTorrent) InfoHash() string {
	return t.torrent.InfoHash().HexString()
}

func (t *realTorrent) StorageKey() string {
	return infoHashStorageDirName(t.InfoHash())
}

func (t *realTorrent) Name() string {
	return t.torrent.Name()
}

func (t *realTorrent) WaitInfo(ctx context.Context) error {
	return waitDone(ctx, t.torrent.GotInfo())
}

func waitDone(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *realTorrent) Files() []FileCandidate {
	files := t.torrent.Files()
	out := make([]FileCandidate, 0, len(files))
	for i, f := range files {
		out = append(out, FileCandidate{DisplayPath: f.DisplayPath(), Length: f.Length(), Index: i})
	}
	return out
}

func (t *realTorrent) Prioritize(index int) {
	files := t.torrent.Files()
	if index < 0 || index >= len(files) {
		return
	}
	files[index].SetPriority(atorrent.PiecePriorityHigh)
}

func (t *realTorrent) BytesCompleted(index int) int64 {
	files := t.torrent.Files()
	if index < 0 || index >= len(files) {
		return 0
	}
	return files[index].BytesCompleted()
}

func (t *realTorrent) Open(ctx context.Context, index int) (ReadSeekCloser, error) {
	files := t.torrent.Files()
	if index < 0 || index >= len(files) {
		return nil, fmt.Errorf("torrent file index %d out of range", index)
	}
	reader := files[index].NewReader()
	reader.SetContext(ctx)
	reader.SetResponsive()
	reader.SetReadahead(4 << 20)
	return reader, nil
}
```

- [ ] **Step 4: Run focused tests and cross-platform build**

Run: `go test ./internal/adapters/torrent`

Expected: PASS.

Run: `GOOS=windows GOARCH=amd64 go test -c ./internal/adapters/torrent -o /tmp/torrent-adapter-windows.test.exe`

Expected: PASS build with exit code 0. This catches Windows-only anacrolix/uTP compile issues before the adapter is registered in `main.go`.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/adapters/torrent/client.go internal/adapters/torrent/client_real_test.go
git commit -m "feat(torrent): wire anacrolix client"
```

---

### Task 8: Main Registration and Public Route Mount

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`
- Create: `internal/adapters/torrent/adapter_interface_test.go`

- [ ] **Step 1: Write adapter interface test**

Create `internal/adapters/torrent/adapter_interface_test.go`:

```go
package torrent

import "testing"

func TestNameAndDisplayName(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: testBridgeConfig(t)})
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "torrent" {
		t.Fatalf("Name = %q, want torrent", a.Name())
	}
	if a.DisplayName() != "Torrent" {
		t.Fatalf("DisplayName = %q, want Torrent", a.DisplayName())
	}
}
```

Add this helper to `adapter_test.go`:

```go
func testBridgeConfig(t *testing.T) config.BridgeConfig {
	t.Helper()
	return config.BridgeConfig{DataDir: t.TempDir(), UI: config.UIConfig{HTTPPort: 32500}, HostIP: "127.0.0.1"}
}
```

- [ ] **Step 2: Register the adapter**

Modify `cmd/mister-groovy-relay/main.go`:

1. Add import alias:

```go
torrentadapter "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/torrent"
```

2. After URL and Streams adapters are constructed, construct and register the torrent adapter before DLNA:

```go
torrentAdapter, err := torrentadapter.New(torrentadapter.AdapterConfig{
	Bridge:   sec.Bridge,
	Core:     coreMgr,
	EventLog: elog,
})
if err != nil {
	dieFriendly("torrent adapter init", err)
}
if err := reg.Register(torrentAdapter); err != nil {
	dieFriendly("registry register torrent", err)
}
```

The existing `PublicRouteProvider` loop will mount `/torrent/session/{token}/media`.

- [ ] **Step 3: Run package build tests**

Run: `go test ./cmd/mister-groovy-relay ./internal/adapters/torrent`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/mister-groovy-relay/main.go internal/adapters/torrent/adapter_interface_test.go internal/adapters/torrent/adapter_test.go
git commit -m "feat(torrent): register adapter"
```

---

### Task 9: Documentation and Example Config

**Files:**
- Modify: `internal/config/example.toml`
- Modify: `README.md`
- Modify: `THIRD_PARTY_NOTICES.md`

- [ ] **Step 1: Update example config**

Add this exact section to `internal/config/example.toml` under the existing adapter sections:

```toml
[adapters.torrent]
enabled = false
traffic_acknowledged = false
download_dir = ""
keep_completed = false
max_cache_bytes = 21474836480
metadata_timeout_seconds = 60
startup_buffer_seconds = 10
max_upload_rate_kbps = 512
max_download_rate_kbps = 0
listen_port = 0
```

- [ ] **Step 2: Update README**

Add a `Torrent adapter` subsection near the other adapter descriptions:

```markdown
### Torrent adapter

The Torrent adapter can cast a magnet link or uploaded `.torrent` file through MiSTer Groovy Relay. It is disabled by default and also requires `traffic_acknowledged = true` before any BitTorrent client is created or any BitTorrent listen port opens.

Use it only for content you have the right to download and upload. BitTorrent traffic can be visible to peers and network operators. The default upload limit is 512 KiB/s; set `max_upload_rate_kbps = 0` for unlimited upload or a lower positive value for a stricter cap.

Torrent media is served only to the local bridge process through `/torrent/session/{token}/media`, and that route rejects non-loopback clients. Cache data lives under `<download_dir-or-data_dir>/groovyrelay-torrent/`; session cache data is deleted after playback unless `keep_completed = true`.
```

- [ ] **Step 3: Update third-party notices**

Add a notice entry to `THIRD_PARTY_NOTICES.md`:

```markdown
### github.com/anacrolix/torrent

MiSTer Groovy Relay uses `github.com/anacrolix/torrent` for BitTorrent magnet and metainfo handling in the optional Torrent adapter. See the module's license in its upstream repository and the version pinned in `go.mod`.
```

- [ ] **Step 4: Run documentation diff check and commit**

Run: `git diff --check -- internal/config/example.toml README.md THIRD_PARTY_NOTICES.md`

Expected: no output and exit code 0.

Commit:

```bash
git add internal/config/example.toml README.md THIRD_PARTY_NOTICES.md
git commit -m "docs(torrent): document torrent adapter"
```

---

### Task 10: Integration Verification and Cleanup

**Files:**
- Modify only files created or touched by prior tasks if verification finds defects.

- [ ] **Step 1: Run focused adapter tests**

Run: `go test ./internal/adapters/torrent`

Expected: PASS.

- [ ] **Step 2: Run adapter and UI boundary tests**

Run: `go test ./internal/adapters/... ./internal/ui/...`

Expected: PASS.

- [ ] **Step 3: Run core and command tests**

Run: `go test ./internal/core ./internal/ffmpeg ./cmd/mister-groovy-relay`

Expected: PASS.

- [ ] **Step 4: Run full test suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 5: Run static diff checks**

Run: `git diff --check`

Expected: no output and exit code 0.

- [ ] **Step 6: Inspect touched files**

Run: `git status --short`

Expected: only intentional torrent adapter, dependency, registration, example config, README, and notice changes appear as uncommitted work. Pre-existing unrelated files may still appear in the shared worktree; do not revert them.

- [ ] **Step 7: Commit verification fixes if any were needed**

If Steps 1-5 required fixes, commit those fixes:

```bash
git add internal/adapters/torrent cmd/mister-groovy-relay/main.go go.mod go.sum internal/config/example.toml README.md THIRD_PARTY_NOTICES.md
git commit -m "fix(torrent): complete verification fixes"
```

If Steps 1-5 passed without changes, skip this commit.

---

## Final Acceptance Checklist

- [ ] Adapter appears in the settings UI as `Torrent`.
- [ ] `[adapters.torrent] enabled=false` and `traffic_acknowledged=false` are defaults.
- [ ] Starting the bridge with the adapter enabled does not create a BitTorrent client until a play/upload request passes both gates.
- [ ] Unchecking `enabled` or `traffic_acknowledged` blocks new torrent sessions and leaves the current core session untouched.
- [ ] Magnet links and `.torrent` uploads map to exactly one core session each.
- [ ] A second same-info-hash session reuses the torrent handle and receives a new token.
- [ ] Media route accepts `127.0.0.0/8` and `::1`, rejects LAN/non-IP hosts, and supports byte ranges.
- [ ] Session cache deletion refuses roots, home directories, broad system directories, outside-root paths, and unmarked directories.
- [ ] Persistent storage pruning respects `max_cache_bytes`, refuses unmarked storage roots, and skips active storage directory names.
- [ ] `core.SessionRequest` sets `Source`, `AdapterRef`, `DirectPlay`, pause/seek capabilities, title, `OnStop`, and the required `MediaInputPolicy`.
- [ ] Logs and errors never include raw magnet trackers or display names from magnet query params.
- [ ] README and example config describe consent, legality/traffic visibility, upload limits, cache policy, and route scope.
