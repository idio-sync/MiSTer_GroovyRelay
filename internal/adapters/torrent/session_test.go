package torrent

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type fakeTorrentClient struct {
	magnets  []string
	metainfo [][]byte
	byHash   map[string]*fakeTorrent
	closes   int
	files    []FileCandidate
	waitErr  error
}

func (f *fakeTorrentClient) AddMagnet(ctx context.Context, raw string) (TorrentHandle, bool, error) {
	if f.byHash == nil {
		f.byHash = make(map[string]*fakeTorrent)
	}
	f.magnets = append(f.magnets, raw)
	hash := "0123456789abcdef0123456789abcdef01234567"
	if strings.Contains(raw, "aaaaaaaa") || strings.Contains(raw, "other") {
		hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}
	if existing := f.byHash[hash]; existing != nil {
		return existing, false, nil
	}
	files := f.files
	if files == nil {
		files = []FileCandidate{
			{DisplayPath: "movie.mkv", Length: 10, Index: 0},
		}
	}
	t := &fakeTorrent{
		hash:    hash,
		name:    "movie",
		files:   files,
		waitErr: f.waitErr,
	}
	f.byHash[hash] = t
	return t, true, nil
}

func (f *fakeTorrentClient) AddMetaInfo(ctx context.Context, body []byte) (TorrentHandle, bool, error) {
	f.metainfo = append(f.metainfo, body)
	return nil, false, errors.New("not used")
}

func (f *fakeTorrentClient) Close() error {
	f.closes++
	return nil
}

type fakeTorrent struct {
	hash    string
	name    string
	files   []FileCandidate
	waitErr error
	drops   int
}

func (f *fakeTorrent) InfoHash() string   { return f.hash }
func (f *fakeTorrent) StorageKey() string { return infoHashStorageDirName(f.hash) }
func (f *fakeTorrent) Name() string       { return f.name }
func (f *fakeTorrent) WaitInfo(context.Context) error {
	if f.waitErr != nil {
		return f.waitErr
	}
	return nil
}
func (f *fakeTorrent) Files() []FileCandidate { return f.files }
func (f *fakeTorrent) Prioritize(int)         {}
func (f *fakeTorrent) BytesCompleted(index int) int64 {
	if index < 0 || index >= len(f.files) {
		return 0
	}
	return f.files[index].Length
}
func (f *fakeTorrent) Open(context.Context, int) (ReadSeekCloser, error) {
	return stringReadSeekCloser{Reader: strings.NewReader("video")}, nil
}
func (f *fakeTorrent) Drop() {
	f.drops++
}

type stringReadSeekCloser struct{ *strings.Reader }

func (s stringReadSeekCloser) Close() error { return nil }

var _ io.ReadSeeker = (*strings.Reader)(nil)

func startedTorrentConfig() Config {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.TrafficAcknowledged = true
	cfg.StartupBufferSeconds = 0
	return cfg
}

func newStartedTestAdapter(t *testing.T, cfg Config, client *fakeTorrentClient, rec *recordingCore) *Adapter {
	t.Helper()
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{
			DataDir: t.TempDir(),
			HostIP:  "not-an-ip",
			UI:      config.UIConfig{HTTPPort: 32500},
		},
		Core: rec,
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
	factoryCalls := 0
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{DataDir: t.TempDir()},
		Core:   core,
		ClientFactory: func(ClientConfig) (TorrentClient, error) {
			factoryCalls++
			return client, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, err = a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrDisabled {
		t.Fatalf("disabled err = %#v, want ErrDisabled", err)
	}
	a.SetEnabled(true)
	_, err = a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrTrafficNotAcknowledged {
		t.Fatalf("traffic err = %#v, want ErrTrafficNotAcknowledged", err)
	}
	if factoryCalls != 0 || len(client.magnets) != 0 {
		t.Fatalf("client touched before gates passed: factory=%d magnets=%d", factoryCalls, len(client.magnets))
	}
}

func TestStartMagnetBuildsCoreRequestWithPolicy(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, startedTorrentConfig(), client, core)
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
	if started.AdapterRef != req.AdapterRef {
		t.Fatalf("started AdapterRef = %q, want %q", started.AdapterRef, req.AdapterRef)
	}
	if !req.DirectPlay || !req.Capabilities.CanPause || !req.Capabilities.CanSeek {
		t.Fatalf("capabilities/direct play wrong: %#v direct=%v", req.Capabilities, req.DirectPlay)
	}
	if req.Title != "movie.mkv" || started.Title != "movie.mkv" {
		t.Fatalf("title = %q started=%q, want movie.mkv", req.Title, started.Title)
	}
	if !strings.HasPrefix(req.StreamURL, "http://127.0.0.1:32500/torrent/session/") || !strings.HasSuffix(req.StreamURL, "/media") {
		t.Fatalf("StreamURL = %q, want tokenized loopback media URL", req.StreamURL)
	}
	if req.MediaInputPolicy.RWTimeout != 30*time.Second {
		t.Fatalf("RWTimeout = %s, want 30s", req.MediaInputPolicy.RWTimeout)
	}
	if got := strings.Join(req.MediaInputPolicy.ProtocolWhitelist, ","); got != "http,tcp" {
		t.Fatalf("ProtocolWhitelist = %q, want http,tcp", got)
	}
	if !req.MediaInputPolicy.DisableRedirects || !req.MediaInputPolicy.DisableReconnect {
		t.Fatalf("redirect/reconnect policy wrong: %#v", req.MediaInputPolicy)
	}
	if got := strings.Join(req.MediaInputPolicy.BlockedHeaders, ","); got != "Cookie,Authorization,Proxy-Authorization" {
		t.Fatalf("BlockedHeaders = %q", got)
	}
}

func TestSameInfoHashReusesTorrentObject(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, startedTorrentConfig(), client, core)
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
	if len(client.byHash) != 1 {
		t.Fatalf("client torrents = %d, want 1", len(client.byHash))
	}
	if got := a.torrents["0123456789abcdef0123456789abcdef01234567"].refs; got != 2 {
		t.Fatalf("torrent refs = %d, want 2", got)
	}
}

func TestActiveSessionSurvivesMidSessionGateToggle(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, startedTorrentConfig(), client, core)
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
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, startedTorrentConfig(), client, core)
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

func TestStartMagnetReturnsStartedSessionWhenCoreStopsBeforeReturning(t *testing.T) {
	rec := &recordingCore{}
	rec.onStart = func(req core.SessionRequest) error {
		req.OnStop("preempted")
		return nil
	}
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, startedTorrentConfig(), client, rec)
	started, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if started.Token == "" || started.AdapterRef == "" {
		t.Fatalf("started session missing identifiers: %#v", started)
	}
	a.mu.Lock()
	_, live := a.sessions[started.Token]
	a.mu.Unlock()
	if live {
		t.Fatal("session still live after core fired OnStop before returning")
	}
}

func TestDifferentInfoHashStartsSecondCoreSession(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	a := newStartedTestAdapter(t, startedTorrentConfig(), client, core)
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

func TestStartMagnetDropsTorrentWhenWaitInfoFailsBeforeSession(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{waitErr: errors.New("metadata failed")}
	cfg := startedTorrentConfig()
	a := newStartedTestAdapter(t, cfg, client, core)
	_, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrMetadataTimeout {
		t.Fatalf("startMagnet err = %#v, want ErrMetadataTimeout", err)
	}
	hash := "0123456789abcdef0123456789abcdef01234567"
	if client.byHash[hash].drops != 1 {
		t.Fatalf("torrent drops = %d, want 1", client.byHash[hash].drops)
	}
	a.mu.Lock()
	sessionCount := len(a.sessions)
	_, torrentRef := a.torrents[hash]
	a.mu.Unlock()
	if sessionCount != 0 || torrentRef {
		t.Fatalf("failed start left registered state: sessions=%d torrentRef=%v", sessionCount, torrentRef)
	}
}

func TestStartMagnetDropsTorrentAndDeletesStorageWhenNoPlayableFileBeforeSession(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{
		files: []FileCandidate{{DisplayPath: "notes.txt", Length: 10, Index: 0}},
	}
	cfg := startedTorrentConfig()
	a := newStartedTestAdapter(t, cfg, client, core)
	hash := "0123456789abcdef0123456789abcdef01234567"
	storageDir := writeStorageDir(t, a, cfg, hash)

	_, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrNoPlayableFile {
		t.Fatalf("startMagnet err = %#v, want ErrNoPlayableFile", err)
	}
	if client.byHash[hash].drops != 1 {
		t.Fatalf("torrent drops = %d, want 1", client.byHash[hash].drops)
	}
	if _, err := os.Stat(storageDir); !os.IsNotExist(err) {
		t.Fatalf("storage dir still exists or stat failed differently after no-playable failure: %v", err)
	}
	a.mu.Lock()
	sessionCount := len(a.sessions)
	_, torrentRef := a.torrents[hash]
	a.mu.Unlock()
	if sessionCount != 0 || torrentRef {
		t.Fatalf("failed start left registered state: sessions=%d torrentRef=%v", sessionCount, torrentRef)
	}
}

func TestFailedSecondSameHashStartKeepsActiveReusedTorrent(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	cfg := startedTorrentConfig()
	a := newStartedTestAdapter(t, cfg, client, core)
	active, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	hash := "0123456789abcdef0123456789abcdef01234567"
	storageDir := writeStorageDir(t, a, cfg, hash)
	badDownloadDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badDownloadDir, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.cfg.DownloadDir = badDownloadDir
	a.mu.Unlock()

	_, err = a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err == nil {
		t.Fatal("second same-hash start succeeded, want session dir creation failure")
	}
	if client.byHash[hash].drops != 0 {
		t.Fatalf("reused active torrent drops = %d, want 0", client.byHash[hash].drops)
	}
	if _, err := os.Stat(storageDir); err != nil {
		t.Fatalf("active storage dir was removed: %v", err)
	}
	a.mu.Lock()
	refs := a.torrents[hash].refs
	_, activeLive := a.sessions[active.Token]
	sessionCount := len(a.sessions)
	a.mu.Unlock()
	if refs != 1 || !activeLive || sessionCount != 1 {
		t.Fatalf("active state after failed reused start: refs=%d activeLive=%v sessions=%d", refs, activeLive, sessionCount)
	}
}

func TestCleanupDropsTorrentAndDeletesStorageWhenLastRefStops(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	cfg := startedTorrentConfig()
	a := newStartedTestAdapter(t, cfg, client, core)
	started, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	hash := "0123456789abcdef0123456789abcdef01234567"
	storageDir := writeStorageDir(t, a, cfg, hash)

	core.reqs[0].OnStop("stopped")

	if client.byHash[hash].drops != 1 {
		t.Fatalf("torrent drops = %d, want 1", client.byHash[hash].drops)
	}
	if _, err := os.Stat(storageDir); !os.IsNotExist(err) {
		t.Fatalf("storage dir still exists or stat failed differently after %s stopped: %v", started.Token, err)
	}
}

func TestCleanupKeepsStorageWhenKeepCompleted(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	cfg := startedTorrentConfig()
	cfg.KeepCompleted = true
	a := newStartedTestAdapter(t, cfg, client, core)
	_, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	hash := "0123456789abcdef0123456789abcdef01234567"
	storageDir := writeStorageDir(t, a, cfg, hash)

	core.reqs[0].OnStop("stopped")

	if client.byHash[hash].drops != 1 {
		t.Fatalf("torrent drops = %d, want 1", client.byHash[hash].drops)
	}
	if _, err := os.Stat(storageDir); err != nil {
		t.Fatalf("storage dir was removed with keep_completed=true: %v", err)
	}
}

func TestCleanupDoesNotDropOrDeleteStorageUntilLastSameHashRefStops(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	cfg := startedTorrentConfig()
	a := newStartedTestAdapter(t, cfg, client, core)
	first, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	hash := "0123456789abcdef0123456789abcdef01234567"
	storageDir := writeStorageDir(t, a, cfg, hash)

	core.reqs[0].OnStop("stopped")

	if client.byHash[hash].drops != 0 {
		t.Fatalf("torrent drops after first stop = %d, want 0", client.byHash[hash].drops)
	}
	if _, err := os.Stat(storageDir); err != nil {
		t.Fatalf("storage dir removed before final ref stopped: %v", err)
	}
	a.mu.Lock()
	refs := a.torrents[hash].refs
	_, firstLive := a.sessions[first.Token]
	_, secondLive := a.sessions[second.Token]
	a.mu.Unlock()
	if refs != 1 || firstLive || !secondLive {
		t.Fatalf("state after first stop: refs=%d firstLive=%v secondLive=%v", refs, firstLive, secondLive)
	}

	core.reqs[1].OnStop("stopped")

	if client.byHash[hash].drops != 1 {
		t.Fatalf("torrent drops after final stop = %d, want 1", client.byHash[hash].drops)
	}
	if _, err := os.Stat(storageDir); !os.IsNotExist(err) {
		t.Fatalf("storage dir still exists or stat failed differently after final stop: %v", err)
	}
}

func TestMediaURLAlwaysUsesLoopbackEvenWhenBridgeHostIPIsLAN(t *testing.T) {
	core := &recordingCore{}
	client := &fakeTorrentClient{}
	cfg := startedTorrentConfig()
	a, err := New(AdapterConfig{
		Bridge: config.BridgeConfig{
			DataDir: t.TempDir(),
			HostIP:  "192.168.1.50",
			UI:      config.UIConfig{HTTPPort: 32500},
		},
		Core: core,
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
	if _, err := a.startMagnet(context.Background(), "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"); err != nil {
		t.Fatal(err)
	}
	if len(core.reqs) != 1 {
		t.Fatalf("core requests = %d, want 1", len(core.reqs))
	}
	if strings.Contains(core.reqs[0].StreamURL, "192.168.1.50") {
		t.Fatalf("StreamURL = %q, want loopback not configured LAN HostIP", core.reqs[0].StreamURL)
	}
	if !strings.HasPrefix(core.reqs[0].StreamURL, "http://127.0.0.1:32500/") {
		t.Fatalf("StreamURL = %q, want loopback URL", core.reqs[0].StreamURL)
	}
}

func writeStorageDir(t *testing.T, a *Adapter, cfg Config, hash string) string {
	t.Helper()
	dir := filepath.Join(cacheRoot(cfg, a.bridge.DataDir), infoHashStorageDirName(hash))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), []byte("torrent-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
