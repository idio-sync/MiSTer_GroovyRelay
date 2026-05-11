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
