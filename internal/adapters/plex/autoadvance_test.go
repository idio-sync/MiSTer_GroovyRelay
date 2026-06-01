package plex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestAutoAdvance_ConfigDefaultsOff(t *testing.T) {
	if DefaultConfig().AutoAdvance {
		t.Fatal("auto_advance must default to false")
	}
}

func TestAutoAdvance_CurrentValuesIncludesKey(t *testing.T) {
	a := &Adapter{}
	a.plexCfg = Config{AutoAdvance: true}
	got := a.CurrentValues()["auto_advance"]
	if got != true {
		t.Fatalf("CurrentValues[auto_advance] = %v, want true", got)
	}
}

func TestAutoAdvance_ScopeIsHotSwap(t *testing.T) {
	if scopeForPlexField("auto_advance") != adaptersScopeHotSwapForTest() {
		t.Fatalf("auto_advance scope = %v, want ScopeHotSwap", scopeForPlexField("auto_advance"))
	}
}

func TestAutoAdvance_DiffDetectsChange(t *testing.T) {
	old := Config{AutoAdvance: false}
	neu := Config{AutoAdvance: true}
	changed := diffPlexConfig(old, neu)
	found := false
	for _, k := range changed {
		if k == "auto_advance" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diffPlexConfig did not report auto_advance change: %v", changed)
	}
}

func TestAutoAdvance_CompanionMirrorSeedsAndUpdates(t *testing.T) {
	c := NewCompanion(CompanionConfig{AutoAdvance: true}, nil)
	if !c.autoAdvance.Load() {
		t.Fatal("NewCompanion did not seed autoAdvance from CompanionConfig")
	}
	c.SetAutoAdvance(false)
	if c.autoAdvance.Load() {
		t.Fatal("SetAutoAdvance(false) did not update the mirror")
	}
}

func TestAutoAdvance_ApplyConfigUpdatesRunningCompanionMirror(t *testing.T) {
	a, err := NewAdapter(AdapterConfig{
		Bridge: config.BridgeConfig{
			DataDir: t.TempDir(),
			UI:      config.UIConfig{HTTPPort: 32500},
		},
		Core:       &fakeCore{},
		TokenStore: &StoredData{DeviceUUID: "uuid-auto-advance"},
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a.plexCfg = DefaultConfig()
	a.plexCfg.AutoAdvance = false
	a.ensureFinalized()
	if a.companion == nil {
		t.Fatal("companion was not finalized")
	}
	if a.companion.autoAdvance.Load() {
		t.Fatal("initial companion autoAdvance = true, want false")
	}

	raw, meta := sectionPrimitive(t, `
enabled = true
device_name = "MiSTer"
profile_name = "Plex Home Theater"
max_video_bitrate_kbps = 1500
auto_advance = true
`)
	scope, err := a.ApplyConfig(raw, meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Fatalf("scope = %v, want ScopeHotSwap", scope)
	}
	if !a.companion.autoAdvance.Load() {
		t.Fatal("ApplyConfig did not update companion autoAdvance mirror")
	}
}

func adaptersScopeHotSwapForTest() adapters.ApplyScope { return adapters.ScopeHotSwap }

func TestAutoAdvance_FakeCoreImplementsStartSessionIfIdle(t *testing.T) {
	var sm SessionManager = &fakeCore{}
	started, err := sm.StartSessionIfIdle(core.SessionRequest{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !started {
		t.Fatal("idle fakeCore should report started=true")
	}
}

func newPlayQueueServer(t *testing.T, body string) (*httptest.Server, PlayMediaRequest) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/playQueues/") {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(body))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	u, _ := url.Parse(srv.URL)
	host, port := u.Hostname(), u.Port()
	p := PlayMediaRequest{
		PlexServerScheme:  "http",
		PlexServerAddress: host,
		PlexServerPort:    port,
		ContainerKey:      "/playQueues/77",
		MediaKey:          "/library/metadata/200",
		MediaType:         "track",
		PlayQueueItemID:   "1002",
		PlayQueueID:       "77",
	}
	return srv, p
}

const threeItemQueue = `<?xml version="1.0"?>
<MediaContainer playQueueID="77" playQueueVersion="3">
  <Track key="/library/metadata/100" ratingKey="100" playQueueItemID="1001"/>
  <Track key="/library/metadata/200" ratingKey="200" playQueueItemID="1002"/>
  <Track key="/library/metadata/300" ratingKey="300" playQueueItemID="1003"/>
</MediaContainer>`

func TestResolveNextQueueItem_AdvancesToNext(t *testing.T) {
	srv, p := newPlayQueueServer(t, threeItemQueue)
	defer srv.Close()
	c := NewCompanion(CompanionConfig{}, &fakeCore{})

	next, err := c.resolveNextQueueItem(context.Background(), p, func(items []playQueueItem, cur PlayMediaRequest) (playQueueItem, bool) {
		return nextPlayQueueItem(items, cur.PlayQueueItemID, cur.MediaKey, 1)
	})
	if err != nil {
		t.Fatalf("resolveNextQueueItem: %v", err)
	}
	if next.MediaKey != "/library/metadata/300" {
		t.Fatalf("next.MediaKey = %q, want /library/metadata/300", next.MediaKey)
	}
	if next.PlayQueueItemID != "1003" {
		t.Fatalf("next.PlayQueueItemID = %q, want 1003", next.PlayQueueItemID)
	}
	if next.OffsetMs != 0 {
		t.Fatalf("next.OffsetMs = %d, want 0", next.OffsetMs)
	}
	if next.TranscodeSessionID == "" || next.TranscodeSessionID == p.TranscodeSessionID {
		t.Fatalf("next.TranscodeSessionID must be freshly minted, got %q", next.TranscodeSessionID)
	}
}

func TestResolveNextQueueItem_EndOfQueueSentinel(t *testing.T) {
	srv, p := newPlayQueueServer(t, threeItemQueue)
	defer srv.Close()
	p.PlayQueueItemID = "1003" // last item
	c := NewCompanion(CompanionConfig{}, &fakeCore{})

	_, err := c.resolveNextQueueItem(context.Background(), p, func(items []playQueueItem, cur PlayMediaRequest) (playQueueItem, bool) {
		return nextPlayQueueItem(items, cur.PlayQueueItemID, cur.MediaKey, 1)
	})
	if !errors.Is(err, errNoNextQueueItem) {
		t.Fatalf("want errNoNextQueueItem at end of queue, got %v", err)
	}
}

func TestResolveNextQueueItem_EmptyContainerKeySentinel(t *testing.T) {
	srv, p := newPlayQueueServer(t, threeItemQueue)
	defer srv.Close()
	p.ContainerKey = ""
	c := NewCompanion(CompanionConfig{}, &fakeCore{})

	_, err := c.resolveNextQueueItem(context.Background(), p, func(items []playQueueItem, cur PlayMediaRequest) (playQueueItem, bool) {
		return nextPlayQueueItem(items, cur.PlayQueueItemID, cur.MediaKey, 1)
	})
	if !errors.Is(err, errNoNextQueueItem) {
		t.Fatalf("want errNoNextQueueItem for empty ContainerKey, got %v", err)
	}
}
