package torrent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	atorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"
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
	Drop()
}

func newRealClient(cfg ClientConfig) (TorrentClient, error) {
	if strings.TrimSpace(cfg.CacheRoot) == "" {
		return nil, fmt.Errorf("torrent cache root is required")
	}

	clientConfig := atorrent.NewDefaultClientConfig()
	clientConfig.DataDir = cfg.CacheRoot
	clientConfig.DefaultStorage = storage.NewFileByInfoHash(cfg.CacheRoot)
	clientConfig.NoDefaultPortForwarding = true
	clientConfig.Seed = false
	clientConfig.Debug = false
	clientConfig.ListenPort = cfg.Config.ListenPort

	if cfg.Config.MaxUploadRateKbps > 0 {
		bytesPerSecond := cfg.Config.MaxUploadRateKbps * 1024
		clientConfig.UploadRateLimiter = rate.NewLimiter(rate.Limit(bytesPerSecond), bytesPerSecond)
	}
	if cfg.Config.MaxDownloadRateKbps > 0 {
		bytesPerSecond := cfg.Config.MaxDownloadRateKbps * 1024
		clientConfig.DownloadRateLimiter = rate.NewLimiter(rate.Limit(bytesPerSecond), bytesPerSecond)
	}

	client, err := atorrent.NewClient(clientConfig)
	if err != nil {
		return nil, err
	}
	return &realClient{client: client}, nil
}

type realClient struct {
	client *atorrent.Client
}

func (c *realClient) AddMagnet(_ context.Context, raw string) (TorrentHandle, bool, error) {
	spec, err := atorrent.TorrentSpecFromMagnetUri(raw)
	if err != nil {
		return nil, false, err
	}
	return c.addSpec(spec)
}

func (c *realClient) AddMetaInfo(_ context.Context, data []byte) (TorrentHandle, bool, error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, false, err
	}
	spec, err := atorrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		return nil, false, err
	}
	return c.addSpec(spec)
}

func (c *realClient) Close() error {
	return errors.Join(c.client.Close()...)
}

func (c *realClient) addSpec(spec *atorrent.TorrentSpec) (TorrentHandle, bool, error) {
	torrent, isNew, err := c.client.AddTorrentSpec(spec)
	if err != nil {
		return nil, false, err
	}
	return &realTorrent{torrent: torrent}, isNew, nil
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

func (t *realTorrent) Files() []FileCandidate {
	files := t.torrent.Files()
	candidates := make([]FileCandidate, 0, len(files))
	for index, file := range files {
		candidates = append(candidates, FileCandidate{
			Index:       index,
			DisplayPath: file.DisplayPath(),
			Length:      file.Length(),
		})
	}
	return candidates
}

func (t *realTorrent) Prioritize(index int) {
	file, ok := t.file(index)
	if !ok {
		return
	}
	file.SetPriority(atorrent.PiecePriorityHigh)
}

func (t *realTorrent) BytesCompleted(index int) int64 {
	file, ok := t.file(index)
	if !ok {
		return 0
	}
	return file.BytesCompleted()
}

func (t *realTorrent) Open(ctx context.Context, index int) (ReadSeekCloser, error) {
	file, ok := t.file(index)
	if !ok {
		return nil, fmt.Errorf("torrent file index %d out of range", index)
	}
	reader := file.NewReader()
	reader.SetContext(ctx)
	reader.SetResponsive()
	reader.SetReadahead(4 << 20)
	return reader, nil
}

func (t *realTorrent) Drop() {
	t.torrent.Drop()
}

func (t *realTorrent) file(index int) (*atorrent.File, bool) {
	files := t.torrent.Files()
	if index < 0 || index >= len(files) {
		return nil, false
	}
	return files[index], true
}

func waitDone(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
