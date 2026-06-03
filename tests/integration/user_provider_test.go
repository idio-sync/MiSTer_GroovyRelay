//go:build integration

package integration

// TestUserProvider_AddCastStarDeleteCleanup exercises the full user-provider
// lifecycle through the real streams adapter machinery:
//
//  1. CreateUserProvider — persists a new "direct" (m3u8/HLS) provider and
//     rebuilds the catalog.
//  2. Assert the provider appears in Catalog() with the expected channel.
//  3. CastChannel — starts a session via the fake-mister / real ffmpeg data
//     plane; asserts Init+Switchres reach the fake-mister.
//  4. SetPresetStarred — star the channel; assert it occupies a slot.
//  5. CastPreset — cast via the slot; assert Init+Switchres reach the fake-mister
//     a second time (reset between steps via Stop).
//  6. DeleteUserProvider — assert ClearedSlots contains the starred slot and
//     Presets() no longer references the provider.
//
// The fixture server MUST bind to the machine's non-loopback (LAN) IP because
// the streams adapter enforces SSRF policy at play time: loopback addresses are
// blocked by validateUserProviderIP. A t.Skip fires when the machine has no
// non-loopback IPv4 address (e.g. a pure-IPv6 or loopback-only CI runner).

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streams"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// localLANIP returns the machine's preferred non-loopback IPv4 address by
// performing a UDP "dial" (no actual packet) to 8.8.8.8:53 — the same
// technique used in cmd/mister-groovy-relay/main.go. Returns "" when no
// suitable address is available (e.g. loopback-only or pure-IPv6 host).
func localLANIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	ip := addr.IP
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return ""
	}
	if ip.To4() == nil {
		return "" // IPv6-only — skip
	}
	return ip.String()
}

// newFixtureServer creates an httptest.Server bound to hostIP serving the tiny
// MP4 fixture. Returns the server and the full URL for the fixture. Callers
// must call t.Cleanup(srv.Close).
func newFixtureServer(t *testing.T, hostIP string) (*httptest.Server, string) {
	t.Helper()

	mp4Path := filepath.Join("testdata", "url", "tiny.mp4")
	if _, err := os.Stat(mp4Path); err != nil {
		t.Skipf("fixture missing (%v); run from tests/integration/ with testdata/url/tiny.mp4", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/tiny.mp4", func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(mp4Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "video/mp4")
		// Respond correctly to HEAD (net/http does this automatically when
		// the handler writes headers without a body for HEAD requests).
		if r.Method == http.MethodHead {
			return
		}
		_, _ = io.Copy(w, f)
	})

	// Bind to the LAN IP so SSRF policy allows the address.
	ln, err := net.Listen("tcp", hostIP+":0")
	if err != nil {
		t.Fatalf("bind fixture server to %s: %v", hostIP, err)
	}
	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = ln
	srv.Start()

	port := srv.Listener.Addr().(*net.TCPAddr).Port
	fixtureURL := fmt.Sprintf("http://%s:%d/tiny.mp4", hostIP, port)
	return srv, fixtureURL
}

// newStreamsHarness builds a streams.Adapter wired to a scenarioHarness
// (fake-mister + core.Manager) and returns both. The adapter is enabled
// (Enabled=true) without calling Start. The LAN-IP fixture URL passes the
// user-provider SSRF policy because validateUserProviderIP allows
// RFC1918/LAN (private) addresses; only loopback/link-local/metadata
// addresses are blocked — which is precisely why the fixture server binds to
// the machine's LAN IP rather than 127.0.0.1.
func newStreamsHarness(t *testing.T) (*streams.Adapter, *scenarioHarness) {
	t.Helper()
	h := newScenarioHarness(t)

	dir := t.TempDir()
	// Pre-create an empty preset file so the store starts with all 12 slots
	// vacant. Without this the bundled-default preset hydration fills the bank
	// with built-in channel references that prevent user-star insertion.
	presetsPath := filepath.Join(dir, "chassis_presets.json")
	if err := os.WriteFile(presetsPath, []byte(`{"version":1,"slots":[]}`), 0o600); err != nil {
		t.Fatalf("pre-create empty presets file: %v", err)
	}

	bridgeCfg := config.BridgeConfig{
		DataDir: dir,
		MiSTer: config.MisterConfig{
			Host:       "127.0.0.1",
			Port:       h.Listener.Addr().(*net.UDPAddr).Port,
			SourcePort: 0,
		},
		Video: config.VideoConfig{
			Modeline:            "NTSC_480i",
			RGBMode:             "rgb888",
			InterlaceFieldOrder: "tff",
			AspectMode:          "letterbox",
			LZ4Enabled:          false,
		},
		Audio: config.AudioConfig{SampleRate: 48000, Channels: 2},
	}

	a, err := streams.New(streams.AdapterConfig{
		Bridge: bridgeCfg,
		Core:   h.Manager,
	})
	if err != nil {
		t.Fatalf("streams.New: %v", err)
	}
	// Enable without calling Start so the adapter doesn't spin up the remote
	// manifest refresh loop (no manifest URL is set, and the fixture is LAN-only).
	a.SetEnabled(true)

	return a, h
}

// waitForInitInUserProviderTest polls h.Recorder for at least one Init and one
// Switchres within d, then stops the manager so the plane goroutine exits
// cleanly before subsequent sub-steps.
func waitForInitInUserProviderTest(t *testing.T, h *scenarioHarness, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		snap := h.Recorder.Snapshot()
		if snap.Counts[groovy.CmdInit] >= 1 && snap.Counts[groovy.CmdSwitchres] >= 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	snap := h.Recorder.Snapshot()
	t.Fatalf("no Init+Switchres within %v: counts=%+v", d, snap.Counts)
}

func TestUserProvider_AddCastStarDeleteCleanup(t *testing.T) {
	hostIP := localLANIP()
	if hostIP == "" {
		t.Skip("no non-loopback IPv4 address found; streams SSRF policy blocks loopback")
	}

	_ = ffmpegPathOrSkip(t) // skip if ffmpeg is not on PATH

	srv, fixtureURL := newFixtureServer(t, hostIP)
	t.Cleanup(srv.Close)

	ctx := context.Background()
	a, h := newStreamsHarness(t)

	// ------------------------------------------------------------------ //
	// Step 1: CreateUserProvider                                            //
	// ------------------------------------------------------------------ //
	form := adapters.UserProviderForm{
		DisplayName: "Integration Test TV",
		BadgeLabel:  "IT",
		BadgeColor:  "teal",
		Channels: []adapters.UserChannelForm{
			{
				Name: "Live",
				URL:  fixtureURL,
				Kind: "direct", // explicit; detectChannelKind would pick "single" for .mp4
			},
		},
	}
	result, err := a.CreateUserProvider(ctx, form)
	if err != nil {
		t.Fatalf("CreateUserProvider: %v", err)
	}
	providerID := result.Provider.ID
	if providerID == "" {
		t.Fatalf("CreateUserProvider: returned empty provider ID")
	}
	t.Logf("created provider %s", providerID)

	// ------------------------------------------------------------------ //
	// Step 2: Assert provider appears in Catalog()                         //
	// ------------------------------------------------------------------ //
	catalog := a.Catalog()
	var foundProvider *adapters.CatalogProvider
	for i := range catalog {
		if catalog[i].ID == providerID {
			foundProvider = &catalog[i]
			break
		}
	}
	if foundProvider == nil {
		t.Fatalf("provider %s not found in Catalog(); catalog=%+v", providerID, catalog)
	}
	// Find the channel ID from the catalog.
	var channelID string
	for _, g := range foundProvider.Groups {
		for _, ch := range g.Channels {
			channelID = ch.ID
			break
		}
		if channelID != "" {
			break
		}
	}
	if channelID == "" {
		t.Fatalf("provider %s has no channels in Catalog(); provider=%+v", providerID, foundProvider)
	}
	t.Logf("found channel %s/%s in catalog", providerID, channelID)

	// ------------------------------------------------------------------ //
	// Step 3: CastChannel — assert Init+Switchres reach fake-mister        //
	// ------------------------------------------------------------------ //
	if err := a.CastChannel(ctx, providerID, channelID); err != nil {
		t.Fatalf("CastChannel: %v", err)
	}
	waitForInitInUserProviderTest(t, h, 8*time.Second)
	t.Logf("CastChannel produced Init+Switchres on fake-mister")

	// Stop the active session so the manager is idle before CastPreset.
	if err := h.Manager.Stop(); err != nil {
		t.Fatalf("Stop after CastChannel: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if st := h.Manager.Status(); st.State != core.StateIdle {
		t.Fatalf("manager not idle after Stop: %q", st.State)
	}

	// ------------------------------------------------------------------ //
	// Step 4: SetPresetStarred                                              //
	// ------------------------------------------------------------------ //
	starResult, err := a.SetPresetStarred(ctx, providerID, channelID, true)
	if err != nil {
		t.Fatalf("SetPresetStarred: %v", err)
	}
	if !starResult.Starred {
		t.Fatalf("SetPresetStarred: Starred = false")
	}
	starredSlot := starResult.Slot
	if starredSlot < 1 || starredSlot > 12 {
		t.Fatalf("SetPresetStarred: slot %d out of range", starredSlot)
	}
	t.Logf("starred slot %d", starredSlot)

	// Verify the slot is occupied in Presets().
	presets := a.Presets()
	entry := presets[starredSlot-1]
	if entry.ProviderID != providerID || entry.ChannelID != channelID {
		t.Fatalf("Presets()[%d] = {%s,%s}, want {%s,%s}",
			starredSlot-1, entry.ProviderID, entry.ChannelID, providerID, channelID)
	}

	// ------------------------------------------------------------------ //
	// Step 5: CastPreset — assert Init+Switchres reach fake-mister again   //
	// ------------------------------------------------------------------ //
	initCountBefore := h.Recorder.Snapshot().Counts[groovy.CmdInit]
	if err := a.CastPreset(ctx, starredSlot); err != nil {
		t.Fatalf("CastPreset(%d): %v", starredSlot, err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		snap := h.Recorder.Snapshot()
		if snap.Counts[groovy.CmdInit] > initCountBefore {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	snap := h.Recorder.Snapshot()
	if snap.Counts[groovy.CmdInit] <= initCountBefore {
		t.Errorf("CastPreset(%d): no new Init within 8s; counts=%+v", starredSlot, snap.Counts)
	}
	t.Logf("CastPreset produced Init on fake-mister (total Init=%d)", snap.Counts[groovy.CmdInit])

	// Stop the active session before delete.
	if err := h.Manager.Stop(); err != nil {
		t.Fatalf("Stop after CastPreset: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// ------------------------------------------------------------------ //
	// Step 6: DeleteUserProvider — assert slot cleanup                     //
	// ------------------------------------------------------------------ //
	delResult, err := a.DeleteUserProvider(ctx, providerID)
	if err != nil {
		t.Fatalf("DeleteUserProvider(%s): %v", providerID, err)
	}
	// ClearedSlots must contain the starred slot.
	slotCleared := false
	for _, s := range delResult.ClearedSlots {
		if s == starredSlot {
			slotCleared = true
			break
		}
	}
	if !slotCleared {
		t.Errorf("DeleteUserProvider: ClearedSlots=%v does not contain starred slot %d",
			delResult.ClearedSlots, starredSlot)
	}

	// Presets() must no longer reference the deleted provider.
	presets = a.Presets()
	for i, p := range presets {
		if p.ProviderID == providerID {
			t.Errorf("Presets()[%d] still references deleted provider %s after delete", i, providerID)
		}
	}

	// Provider must be absent from Catalog().
	for _, p := range a.Catalog() {
		if p.ID == providerID {
			t.Errorf("Catalog() still contains deleted provider %s", providerID)
		}
	}
	t.Logf("DeleteUserProvider: slot %d cleared, provider removed from catalog", starredSlot)
}
