package chassis

import (
	"context"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type fakeAUXStarter struct{}

func (fakeAUXStarter) AUXStatus(context.Context) adapters.AUXStatus {
	return adapters.AUXStatus{}
}

func (fakeAUXStarter) StartAUX(context.Context, string) (string, error) {
	return "", nil
}

func (fakeAUXStarter) StopAUX(context.Context, string) (bool, error) {
	return false, nil
}

func TestNewStoresAUXStarter(t *testing.T) {
	fake := fakeAUXStarter{}
	srv, err := New(Config{
		Version:   "test",
		StartedAt: time.Unix(1, 0),
		AUX:       fake,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if srv.aux == nil {
		t.Fatalf("Server.aux is nil, want configured AUX starter")
	}
}
