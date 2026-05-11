package torrent

import (
	"context"
	"errors"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

type recordingCore struct {
	starts int
	stops  int
	status core.SessionStatus
}

func (c *recordingCore) StartSession(core.SessionRequest) error {
	c.starts++
	return nil
}

func (c *recordingCore) Status() core.SessionStatus {
	return c.status
}

func (c *recordingCore) Stop() error {
	c.stops++
	return nil
}

type fakeTorrentClient struct {
	closes   int
	closeErr error
}

func (c *fakeTorrentClient) Close() error {
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
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
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

	a.emit("hello\n%s", "torrent")
	entries := log.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("eventlog entries = %d, want 1", len(entries))
	}
	if entries[0].Severity != eventlog.SeverityInfo {
		t.Fatalf("eventlog severity = %v, want info", entries[0].Severity)
	}
	if entries[0].Message != "hello torrent" {
		t.Fatalf("eventlog message = %q, want sanitized formatted message", entries[0].Message)
	}
	if got := a.logSafe("bad\r\n%s", "news"); got != "bad  news" {
		t.Fatalf("logSafe = %q, want sanitized formatted message", got)
	}
}

func TestStopRetainsClientWhenCloseFails(t *testing.T) {
	closeErr := errors.New("close failed")
	client := &fakeTorrentClient{closeErr: closeErr}
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
