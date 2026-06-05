package main

import (
	"context"
	"errors"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
)

type streamsManifestRefresher interface {
	RefreshNow(ctx context.Context, providerID string) streams.RefreshStatus
}

type bridgeStreamsRefresher struct {
	adapter streamsManifestRefresher
}

func newBridgeStreamsRefresher(a streamsManifestRefresher) *bridgeStreamsRefresher {
	return &bridgeStreamsRefresher{adapter: a}
}

func (b *bridgeStreamsRefresher) RefreshNow(ctx context.Context) (chassis.StreamsRefreshResult, error) {
	if b.adapter == nil {
		return chassis.StreamsRefreshResult{}, errors.New("streams adapter not registered")
	}
	start := time.Now()
	status := b.adapter.RefreshNow(ctx, "")
	return chassis.StreamsRefreshResult{
		Source:     status.Source,
		DurationMS: time.Since(start).Milliseconds(),
		Err:        status.Err,
	}, nil
}
