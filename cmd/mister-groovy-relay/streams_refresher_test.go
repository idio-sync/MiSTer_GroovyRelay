package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
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

func TestE2E_StreamsRefreshThroughChassis(t *testing.T) {
	t.Parallel()
	fake := &fakeManifestRefresherForChassis{status: streams.RefreshStatus{Source: "remote"}}
	refresher := newBridgeStreamsRefresher(fake)
	srv, err := chassis.New(chassis.Config{
		Version:          "test",
		StartedAt:        time.Now(),
		StreamsRefresher: refresher,
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/ui/settings/action/streams-refresh", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("Status = %d; body = %s", res.StatusCode, body)
	}
	var payload map[string]any
	_ = json.NewDecoder(res.Body).Decode(&payload)
	if okv, _ := payload["ok"].(bool); !okv {
		t.Errorf("payload.ok = %v, want true", payload["ok"])
	}
	if src, _ := payload["source"].(string); src != "remote" {
		t.Errorf("payload.source = %q, want 'remote'", src)
	}
	if fake.lastProviderID != "" {
		t.Errorf("wrapper called RefreshNow with providerID = %q, want \"\" (manifest path)", fake.lastProviderID)
	}
}

type fakeManifestRefresherForChassis struct {
	status         streams.RefreshStatus
	lastProviderID string
}

func (f *fakeManifestRefresherForChassis) RefreshNow(ctx context.Context, providerID string) streams.RefreshStatus {
	f.lastProviderID = providerID
	return f.status
}
