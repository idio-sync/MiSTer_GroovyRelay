package torrent

import (
	"strings"
	"testing"
)

func TestNewRealClientRejectsEmptyCacheRoot(t *testing.T) {
	_, err := newRealClient(ClientConfig{Config: DefaultConfig(), DataDir: t.TempDir()})
	if err == nil {
		t.Fatal("newRealClient without CacheRoot succeeded, want error")
	}
	if !strings.Contains(err.Error(), "cache root") {
		t.Fatalf("newRealClient error = %q, want cache root error", err.Error())
	}
}
