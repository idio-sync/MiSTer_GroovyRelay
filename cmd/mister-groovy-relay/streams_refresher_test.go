package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
)

func TestBridgeStreamsRefresher_NilAdapter(t *testing.T) {
	t.Parallel()
	r := newBridgeStreamsRefresher(nil)
	_, err := r.RefreshNow(context.Background())
	if err == nil {
		t.Errorf("RefreshNow(nil adapter) err = nil, want non-nil")
	}
}

func TestBridgeStreamsRefresher_DelegatesToManifestPath(t *testing.T) {
	t.Parallel()
	fake := &fakeManifestRefresher{status: streams.RefreshStatus{Source: "remote", FetchedAt: time.Now()}}
	r := newBridgeStreamsRefresher(fake)
	result, err := r.RefreshNow(context.Background())
	if err != nil {
		t.Fatalf("RefreshNow err = %v", err)
	}
	if fake.lastProviderID != "" {
		t.Errorf("providerID = %q, want empty manifest-path sentinel", fake.lastProviderID)
	}
	if result.Source != "remote" {
		t.Errorf("Source = %q, want 'remote'", result.Source)
	}
}

func TestBridgeStreamsRefresher_PropagatesErr(t *testing.T) {
	t.Parallel()
	fake := &fakeManifestRefresher{
		status: streams.RefreshStatus{Source: "remote", Err: errors.New("dns: no such host")},
	}
	r := newBridgeStreamsRefresher(fake)
	result, _ := r.RefreshNow(context.Background())
	if result.Err == nil || result.Err.Error() != "dns: no such host" {
		t.Errorf("result.Err = %v, want 'dns: no such host'", result.Err)
	}
}

type fakeManifestRefresher struct {
	status         streams.RefreshStatus
	lastProviderID string
}

func (f *fakeManifestRefresher) RefreshNow(ctx context.Context, providerID string) streams.RefreshStatus {
	f.lastProviderID = providerID
	return f.status
}
