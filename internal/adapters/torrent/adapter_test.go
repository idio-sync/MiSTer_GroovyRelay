package torrent

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

type recordingCore struct {
	starts  int
	stops   int
	status  core.SessionStatus
	reqs    []core.SessionRequest
	onStart func(core.SessionRequest) error
}

func (c *recordingCore) StartSession(req core.SessionRequest) error {
	c.starts++
	c.reqs = append(c.reqs, req)
	if c.onStart != nil {
		return c.onStart(req)
	}
	return nil
}

func (c *recordingCore) Status() core.SessionStatus {
	return c.status
}

func (c *recordingCore) Stop() error {
	c.stops++
	return nil
}

func (c *recordingCore) StopIfSession(ref string, generation uint64) (bool, error) {
	if c.status.AdapterRef != ref || c.status.Generation != generation {
		return false, nil
	}
	c.stops++
	c.status = core.SessionStatus{}
	return true, nil
}

type closeOnlyTorrentClient struct {
	closes   int
	closeErr error
}

func testBridgeConfig(t *testing.T) config.BridgeConfig {
	t.Helper()
	return config.BridgeConfig{DataDir: t.TempDir(), UI: config.UIConfig{HTTPPort: 32500}, HostIP: "127.0.0.1"}
}

func (c *closeOnlyTorrentClient) AddMagnet(context.Context, string) (TorrentHandle, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (c *closeOnlyTorrentClient) AddMetaInfo(context.Context, []byte) (TorrentHandle, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (c *closeOnlyTorrentClient) Close() error {
	c.closes++
	return c.closeErr
}

func TestAdapterImplementsCoreInterfaces(t *testing.T) {
	var _ adapters.Adapter = (*Adapter)(nil)
	var _ adapters.RouteProvider = (*Adapter)(nil)
	var _ adapters.PublicRouteProvider = (*Adapter)(nil)
}

func TestNewRequiresBridgeDataDir(t *testing.T) {
	if _, err := New(AdapterConfig{}); err == nil {
		t.Fatal("New without Bridge.DataDir = nil error, want error")
	}
}

func TestNewInitializesPlannedSessionStateShape(t *testing.T) {
	dataDir := t.TempDir()
	factory := func(ClientConfig) (TorrentClient, error) { return nil, nil }
	a, err := New(AdapterConfig{
		Bridge:        config.BridgeConfig{DataDir: dataDir},
		ClientFactory: factory,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if a.bridge.DataDir != dataDir {
		t.Fatalf("bridge.DataDir = %q, want %q", a.bridge.DataDir, dataDir)
	}
	if a.factory == nil {
		t.Fatal("factory is nil")
	}
	if a.sessions == nil {
		t.Fatal("sessions map is nil")
	}
	if a.torrents == nil {
		t.Fatal("torrents map is nil")
	}
	if a.activeToken != "" {
		t.Fatalf("activeToken = %q, want empty", a.activeToken)
	}

	a.sessions["token"] = &Session{}
	a.torrents["hash"] = &torrentUse{torrent: nil, refs: 1}
	a.activeToken = "token"
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(a.sessions) != 0 || len(a.torrents) != 0 || a.activeToken != "" {
		t.Fatalf("Stop did not clear planned state: sessions=%d torrents=%d activeToken=%q", len(a.sessions), len(a.torrents), a.activeToken)
	}
}

func TestStartDoesNotCreateTorrentClient(t *testing.T) {
	factoryCalls := 0
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   &recordingCore{},
		ClientFactory: func(ClientConfig) (TorrentClient, error) {
			factoryCalls++
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("ClientFactory calls = %d, want 0", factoryCalls)
	}
}

func TestSetEnabledGatesIsEnabled(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.IsEnabled() {
		t.Fatal("new adapter IsEnabled = true, want false")
	}
	a.SetEnabled(true)
	if !a.IsEnabled() {
		t.Fatal("after SetEnabled(true), IsEnabled = false")
	}
	a.SetEnabled(false)
	if a.IsEnabled() {
		t.Fatal("after SetEnabled(false), IsEnabled = true")
	}
}

func TestEmitAndLogSafeUsePlannedSignatures(t *testing.T) {
	log := eventlog.New(4)
	a, err := New(AdapterConfig{
		Bridge:   config.BridgeConfig{DataDir: t.TempDir()},
		EventLog: log,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var _ func(string) = a.emit
	var _ func(string, ...any) = a.logSafe

	a.emit("hello\nworld")
	entries := log.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("eventlog entries = %d, want 1", len(entries))
	}
	if entries[0].Severity != eventlog.SeverityInfo {
		t.Fatalf("eventlog severity = %v, want info", entries[0].Severity)
	}
	if entries[0].Message != "hello world" {
		t.Fatalf("eventlog message = %q, want sanitized message", entries[0].Message)
	}
	a.logSafe("bad\r\n%s", "news")
	entries = log.Snapshot()
	if len(entries) != 2 {
		t.Fatalf("eventlog entries after logSafe = %d, want 2", len(entries))
	}
	if entries[1].Message != "bad  news" {
		t.Fatalf("logSafe message = %q, want sanitized formatted message", entries[1].Message)
	}
}

func TestStopRetainsClientWhenCloseFails(t *testing.T) {
	closeErr := errors.New("close failed")
	client := &closeOnlyTorrentClient{closeErr: closeErr}
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.client = client

	if err := a.Stop(); !errors.Is(err, closeErr) {
		t.Fatalf("Stop error = %v, want %v", err, closeErr)
	}
	if client.closes != 1 {
		t.Fatalf("client closes = %d, want 1", client.closes)
	}
	if a.client != client {
		t.Fatal("Stop dropped client after close failure")
	}
}

func TestStopDrainsActiveTorrentSessionEvenWhenAdapterDisabled(t *testing.T) {
	// Stop is the bridge-shutdown path (only caller is main.go on SIGTERM).
	// Gates affect new sessions only — they do NOT preserve an active cast
	// past Stop. Without this drain the BitTorrent client, session dir, and
	// storage dir leak across process exit when the operator toggled the
	// adapter off mid-cast.
	rec := &recordingCore{}
	client := &fakeTorrentClient{}
	cfg := startedTorrentConfig()
	a := newStartedTestAdapter(t, cfg, client, rec)
	started, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	hash := "0123456789abcdef0123456789abcdef01234567"
	storageDir := writeStorageDir(t, a, cfg, hash)
	a.mu.Lock()
	sessionDir := a.sessions[started.Token].SessionDir
	a.mu.Unlock()

	a.SetEnabled(false)
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop after disable: %v", err)
	}

	if client.closes != 1 {
		t.Fatalf("client closes after disable+Stop = %d, want 1", client.closes)
	}
	if client.byHash[hash].drops != 1 {
		t.Fatalf("torrent drops after disable+Stop = %d, want 1", client.byHash[hash].drops)
	}
	if _, err := os.Stat(storageDir); !os.IsNotExist(err) {
		t.Fatalf("storage dir still exists after Stop: %v", err)
	}
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("session dir still exists after Stop: %v", err)
	}
	a.mu.Lock()
	_, sessionLive := a.sessions[started.Token]
	sessionCount := len(a.sessions)
	a.mu.Unlock()
	if sessionLive || sessionCount != 0 {
		t.Fatalf("Stop did not clear active session: live=%v count=%d", sessionLive, sessionCount)
	}

	if _, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("new start after disable succeeded, want disabled gate error")
	}

	// Late OnStop from core after Stop already drained must be a no-op
	// (idempotent via CleanupOnce), not a double-close on the same client.
	rec.reqs[0].OnStop("stopped")
	if client.closes != 1 {
		t.Fatalf("client closes after late OnStop = %d, want 1 (idempotent)", client.closes)
	}
}

func TestApplyConfigRestartCastClosesIdleClient(t *testing.T) {
	client := &fakeTorrentClient{}
	a, err := New(AdapterConfig{Bridge: testBridgeConfig(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.client = client
	a.cfg = startedTorrentConfig()

	raw, meta := torrentPrimitive(t, `
[adapters.torrent]
enabled = true
traffic_acknowledged = true
listen_port = 6881
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeRestartCast {
		t.Fatalf("scope = %s, want restart-cast", scope)
	}
	if client.closes != 1 {
		t.Fatalf("client closes = %d, want 1 for idle restart-cast config change", client.closes)
	}
	a.mu.Lock()
	gotClient := a.client
	a.mu.Unlock()
	if gotClient != nil {
		t.Fatal("client still set after idle restart-cast config change")
	}
}

func TestApplyConfigValidationErrorDoesNotChangeStatus(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	before := a.Status()

	raw, meta := torrentPrimitive(t, `
[adapters.torrent]
metadata_timeout_seconds = 1
`)

	if _, err := a.ApplyConfig(raw, meta); err == nil {
		t.Fatal("ApplyConfig = nil error, want validation error")
	}
	after := a.Status()
	if after.State != before.State || after.LastError != before.LastError || !after.Since.Equal(before.Since) {
		t.Fatalf("status changed after rejected config: before=%+v after=%+v", before, after)
	}
}

func torrentPrimitive(t *testing.T, raw string) (toml.Primitive, toml.MetaData) {
	t.Helper()
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(raw, &envelope)
	if err != nil {
		t.Fatalf("toml.Decode: %v", err)
	}
	return envelope.Adapters["torrent"], meta
}
