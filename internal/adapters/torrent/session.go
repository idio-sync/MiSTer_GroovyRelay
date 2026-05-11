package torrent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
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
	SessionRoot string
	StorageRoot string
	Torrent     TorrentHandle
	KeepData    bool
	CleanupOnce cleanupOnce
}

var randRead = rand.Read

type cleanupOnce struct {
	done bool
}

func (a *Adapter) startMagnet(ctx context.Context, raw string) (*StartedSession, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "magnet" {
		if err == nil {
			err = fmt.Errorf("unsupported magnet URL scheme")
		}
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
	t, isNew, err := client.AddMagnet(ctx, raw)
	if err != nil {
		// Drop the raw anacrolix error — it can embed the full magnet URI
		// (including dn/tr/ws/xs params). Keep only the redacted form so
		// downstream loggers walking Unwrap() can't leak the magnet body.
		return nil, &TorrentError{
			Kind:    ErrBadInput,
			Message: "magnet could not be added",
			Err:     fmt.Errorf("add %s failed", redactMagnet(raw)),
		}
	}
	return a.startTorrentHandle(ctx, cfg, t, isNew)
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
	t, isNew, err := client.AddMetaInfo(ctx, body)
	if err != nil {
		return nil, &TorrentError{Kind: ErrBadInput, Message: "torrent file could not be added", Err: err}
	}
	return a.startTorrentHandle(ctx, cfg, t, isNew)
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

func (a *Adapter) startTorrentHandle(ctx context.Context, cfg Config, t TorrentHandle, isNew bool) (*StartedSession, error) {
	metaCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.MetadataTimeoutSeconds)*time.Second)
	defer cancel()
	if err := t.WaitInfo(metaCtx); err != nil {
		a.cleanupFailedStart(cfg, t, isNew)
		return nil, &TorrentError{Kind: ErrMetadataTimeout, Message: "timed out waiting for torrent metadata", Err: err}
	}
	file, err := pickLargestPlayable(t.Files())
	if err != nil {
		a.cleanupFailedStart(cfg, t, isNew)
		return nil, err
	}
	t.Prioritize(file.Index)
	waitForStartupBuffer(ctx, cfg, t, file.Index)
	sessionID, err := newID("torrent")
	if err != nil {
		a.cleanupFailedStart(cfg, t, isNew)
		return nil, &TorrentError{Kind: ErrCoreStart, Message: "could not allocate torrent session id", Err: err}
	}
	token, err := newID("tok")
	if err != nil {
		a.cleanupFailedStart(cfg, t, isNew)
		return nil, &TorrentError{Kind: ErrCoreStart, Message: "could not allocate torrent session token", Err: err}
	}
	sessionRootPath := sessionRoot(cfg, a.bridge.DataDir)
	storageRootPath := cacheRoot(cfg, a.bridge.DataDir)
	dir, err := createSessionDir(sessionRootPath, sessionID)
	if err != nil {
		a.cleanupFailedStart(cfg, t, isNew)
		return nil, err
	}
	s := &Session{
		ID:          sessionID,
		Token:       token,
		InfoHash:    t.InfoHash(),
		StorageKey:  t.StorageKey(),
		FileIndex:   file.Index,
		Title:       sanitizeTitle(file.DisplayPath),
		SessionDir:  dir,
		SessionRoot: sessionRootPath,
		StorageRoot: storageRootPath,
		Torrent:     t,
		KeepData:    cfg.KeepCompleted,
	}
	a.registerSession(s)
	req := core.SessionRequest{
		StreamURL:    a.mediaURL(token),
		Capabilities: core.Capabilities{CanSeek: true, CanPause: true},
		AdapterRef:   "torrent:" + sessionID,
		Source:       "torrent",
		DirectPlay:   true,
		Title:        s.Title,
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

func (a *Adapter) cleanupFailedStart(cfg Config, t TorrentHandle, isNew bool) {
	if t == nil {
		return
	}
	// Skip drop/delete whenever any session is still using this info hash, even
	// on the isNew=true branch — a concurrent same-hash start may have already
	// registered and would otherwise lose its torrent and storage out from under
	// it.
	if a.hasLiveTorrentRef(t.InfoHash()) {
		return
	}
	storageKey := t.StorageKey()
	t.Drop()
	if !cfg.KeepCompleted && storageKey != "" {
		_ = removeStorageDir(cacheRoot(cfg, a.bridge.DataDir), storageKey)
	}
}

func (a *Adapter) hasLiveTorrentRef(infoHash string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	use := a.torrents[infoHash]
	return use != nil && use.refs > 0
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
	use.keepData = use.keepData || s.KeepData
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
	var dropTorrent TorrentHandle
	removeStorage := false
	if use := a.torrents[s.InfoHash]; use != nil {
		use.refs--
		if use.refs <= 0 {
			delete(a.torrents, s.InfoHash)
			dropTorrent = use.torrent
			removeStorage = !use.keepData
		}
	}
	cfg := a.cfg
	active := make(map[string]struct{}, len(a.sessions))
	for _, live := range a.sessions {
		if live.StorageRoot == s.StorageRoot {
			active[live.StorageKey] = struct{}{}
		}
	}
	var closeClient TorrentClient
	if len(a.sessions) == 0 && a.client != nil && (!cfg.Enabled || a.restartClientPending) {
		closeClient = a.client
		a.client = nil
		a.restartClientPending = false
	}
	a.mu.Unlock()
	if !s.KeepData {
		_ = removeSessionDir(s.SessionRoot, s.SessionDir)
	}
	if dropTorrent != nil {
		dropTorrent.Drop()
	}
	if removeStorage {
		_ = removeStorageDir(s.StorageRoot, s.StorageKey)
	}
	// Prune walks the cache dir on disk; offload so a slow disk doesn't
	// delay a concurrent startMagnet waiting for post-cleanup state.
	// Stop() drains a.pruneWG before exit so this goroutine completes
	// before the process tears down.
	a.pruneWG.Add(1)
	go func() {
		defer a.pruneWG.Done()
		if err := pruneStorageCache(s.StorageRoot, cfg.MaxCacheBytes, active); err != nil {
			a.logSafe("torrent cache prune failed token=%s err=%s", token, err)
		}
	}()
	if closeClient != nil {
		if err := closeClient.Close(); err != nil {
			a.setState(adapters.StateError, err.Error())
			return
		}
	}
	a.logSafe("torrent session stopped token=%s reason=%s hash=%s", token, reason, shortHash(s.InfoHash))
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
	return fmt.Sprintf("http://127.0.0.1:%d/torrent/session/%s/media", a.bridge.UI.HTTPPort, token)
}

func newID(prefix string) (string, error) {
	// 16 bytes = 128 bits of entropy. The token gates access to a tokenized
	// loopback media URL; loopback enforcement is the primary defense, but a
	// 64-bit token was below the 2026 norm for "high-entropy" identifiers.
	var b [16]byte
	if _, err := randRead(b[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(b[:]), nil
}

func shortHash(hash string) string {
	if len(hash) < 8 {
		return hash
	}
	return hash[:8]
}
