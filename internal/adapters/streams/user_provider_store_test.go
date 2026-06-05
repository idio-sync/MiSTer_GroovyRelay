package streams

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) (*userProviderStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "user_providers.json")
	st, err := newUserProviderStore(path)
	if err != nil {
		t.Fatalf("newUserProviderStore: %v", err)
	}
	return st, path
}

func sampleDef() ProviderDefinition {
	return ProviderDefinition{
		Type:        userProviderType,
		DisplayName: "F1 TV",
		BadgeLabel:  "F1",
		BadgeColor:  "amber",
		Channels: []ChannelDefinition{
			{Name: "Live", Kind: kindSingle, URL: "https://twitch.tv/formula1"},
		},
	}
}

func TestUserStore_PutAssignsIDsAndPersists(t *testing.T) {
	st, path := newTestStore(t)
	saved, err := st.Put(sampleDef())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if saved.ID != "user:f1-tv" {
		t.Errorf("provider ID = %q, want user:f1-tv", saved.ID)
	}
	if saved.Channels[0].ID != "live" {
		t.Errorf("channel ID = %q, want live", saved.Channels[0].ID)
	}

	// Reload from disk: the provider survives a restart.
	reloaded, err := newUserProviderStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Snapshot(); len(got) != 1 || got[0].ID != "user:f1-tv" {
		t.Errorf("reloaded snapshot = %+v, want one user:f1-tv", got)
	}
}

func TestUserStore_PutRejectsBadColorAndKind(t *testing.T) {
	st, _ := newTestStore(t)
	def := sampleDef()
	def.Channels[0].Kind = "bogus"
	if _, err := st.Put(def); err == nil {
		t.Error("expected error for invalid channel kind")
	}

	def = sampleDef()
	def.BadgeColor = "#ff0000"
	if _, err := st.Put(def); err == nil {
		t.Error("expected error for invalid badge color")
	}
}

func TestUserStore_PutRejectsMalformedIDsAndGroups(t *testing.T) {
	st, _ := newTestStore(t)

	def := sampleDef()
	def.ID = "user:Bad"
	if _, err := st.Put(def); err == nil {
		t.Error("expected error for malformed provider ID")
	}

	def = sampleDef()
	def.Channels[0].ID = "adhoc"
	if _, err := st.Put(def); err == nil {
		t.Error("expected error for reserved channel ID")
	}

	def = sampleDef()
	def.Groups = []GroupDefinition{{ID: "sports", Name: "Sports"}, {ID: "sports", Name: "Dupe"}}
	if _, err := st.Put(def); err == nil {
		t.Error("expected error for duplicate group ID")
	}

	def = sampleDef()
	def.Channels[0].GroupID = "missing"
	if _, err := st.Put(def); err == nil {
		t.Error("expected error for unknown group reference")
	}
}

func TestUserStore_PutEnforcesLimits(t *testing.T) {
	st, _ := newTestStore(t)
	def := sampleDef()
	def.Channels = nil
	for i := 0; i < maxChannelsPerProvider+1; i++ {
		def.Channels = append(def.Channels, ChannelDefinition{Name: "Ch", Kind: kindSingle, URL: "https://twitch.tv/x"})
	}
	if _, err := st.Put(def); err == nil {
		t.Error("expected error exceeding maxChannelsPerProvider")
	}
}

func TestUserStore_UpdatePreservesProviderID(t *testing.T) {
	st, _ := newTestStore(t)
	saved, _ := st.Put(sampleDef())

	upd := saved
	upd.DisplayName = "Formula 1" // rename must NOT change the locked ID
	upd.Channels[0].Name = "Race Live" // channel rename also preserves ID
	again, err := st.Put(upd)
	if err != nil {
		t.Fatalf("update Put: %v", err)
	}
	if again.ID != saved.ID {
		t.Errorf("rename changed ID: %q -> %q", saved.ID, again.ID)
	}
	if again.DisplayName != "Formula 1" {
		t.Errorf("rename not applied: DisplayName = %q, want Formula 1", again.DisplayName)
	}
	if again.Channels[0].ID != saved.Channels[0].ID {
		t.Errorf("channel rename changed ID: %q -> %q", saved.Channels[0].ID, again.Channels[0].ID)
	}
	if len(st.Snapshot()) != 1 {
		t.Errorf("update created a duplicate: %d providers", len(st.Snapshot()))
	}
}

func TestUserStore_SnapshotDoesNotShareSlices(t *testing.T) {
	st, _ := newTestStore(t)
	saved, _ := st.Put(sampleDef())
	snap := st.Snapshot()
	snap[0].DisplayName = "Mutated"
	snap[0].Channels[0].Name = "Mutated"

	again := st.Snapshot()[0]
	if again.DisplayName != saved.DisplayName || again.Channels[0].Name != saved.Channels[0].Name {
		t.Errorf("snapshot mutation leaked into store: %+v", again)
	}
}

func TestUserStore_Delete(t *testing.T) {
	st, _ := newTestStore(t)
	saved, _ := st.Put(sampleDef())
	ok, err := st.Delete(saved.ID)
	if err != nil || !ok {
		t.Fatalf("Delete ok=%v err=%v", ok, err)
	}
	if len(st.Snapshot()) != 0 {
		t.Error("provider not removed")
	}
	if ok, _ := st.Delete("user:missing"); ok {
		t.Error("Delete of missing ID returned ok=true")
	}
}

func TestUserStore_LoadDropsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user_providers.json")
	// Garbage file must not crash load; store comes up empty.
	if err := writeFileString(path, "{not json"); err != nil {
		t.Fatal(err)
	}
	st, err := newUserProviderStore(path)
	if err != nil {
		t.Fatalf("load garbage: %v", err)
	}
	if len(st.Snapshot()) != 0 {
		t.Error("expected empty store after malformed file")
	}
}

func TestUserStore_LoadEnforcesProviderLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user_providers.json")
	providers := make([]ProviderDefinition, 0, maxUserProviders+1)
	for i := 0; i < maxUserProviders+1; i++ {
		def := sampleDef()
		def.ID = fmt.Sprintf("user:p-%d", i)
		def.DisplayName = fmt.Sprintf("Provider %d", i)
		providers = append(providers, def)
	}
	body, err := json.Marshal(userManifestFile{Version: userManifestVersion, Providers: providers})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileString(path, string(body)); err != nil {
		t.Fatal(err)
	}
	st, err := newUserProviderStore(path)
	if err != nil {
		t.Fatalf("load over limit: %v", err)
	}
	if got := len(st.Snapshot()); got != maxUserProviders {
		t.Errorf("loaded providers = %d, want %d", got, maxUserProviders)
	}
}

func TestUserStore_LoadDropsVersionMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user_providers.json")
	def := sampleDef()
	def.ID = "user:f1-tv"
	body, err := json.Marshal(userManifestFile{Version: 999, Providers: []ProviderDefinition{def}})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileString(path, string(body)); err != nil {
		t.Fatal(err)
	}
	st, err := newUserProviderStore(path)
	if err != nil {
		t.Fatalf("load version mismatch: %v", err)
	}
	if got := len(st.Snapshot()); got != 0 {
		t.Errorf("expected empty store on version mismatch, got %d providers", got)
	}
}

func writeFileString(path, s string) error {
	return os.WriteFile(path, []byte(s), 0o600)
}

func TestUserStore_PutRejectsUnsafeURL(t *testing.T) {
	st, _ := newTestStore(t)
	def := sampleDef()
	def.Channels[0].URL = "file:///etc/shadow.m3u8"
	def.Channels[0].Kind = "" // let kind auto-detect; URL must still be rejected
	if _, err := st.Put(def); err == nil {
		t.Error("expected error for file:// channel url")
	}

	def2 := sampleDef()
	def2.Channels[0].URL = "http://127.0.0.1:8080/stream.m3u8"
	if _, err := st.Put(def2); err == nil {
		t.Error("expected error for loopback channel url")
	}
}

func TestUserStore_PutAcceptsLANURL(t *testing.T) {
	st, _ := newTestStore(t)
	def := sampleDef()
	def.Channels[0].URL = "http://192.168.1.40:8080/stream.m3u8"
	if _, err := st.Put(def); err != nil {
		t.Errorf("LAN url should be accepted, got: %v", err)
	}
}

// TestUserStore_AdhocChannelAutoAssign verifies that a channel named "Adhoc"
// (which slugifies to the reserved sentinel "adhoc") is never assigned that
// ID — it must receive "adhoc-2" or any non-reserved value instead.
func TestUserStore_AdhocChannelAutoAssign(t *testing.T) {
	st, _ := newTestStore(t)
	def := sampleDef()
	def.Channels = []ChannelDefinition{
		{Name: "Adhoc", Kind: kindSingle, URL: "https://twitch.tv/formula1"},
	}
	saved, err := st.Put(def)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got := saved.Channels[0].ID
	if got == reservedAdhocID {
		t.Errorf("channel ID = %q; must not be the reserved sentinel %q", got, reservedAdhocID)
	}
	if got != "adhoc-2" {
		t.Errorf("channel ID = %q, want adhoc-2", got)
	}
}

// TestUserStore_BadgeLabelTrimmed verifies that leading/trailing whitespace in
// BadgeLabel is stripped before storage.
func TestUserStore_BadgeLabelTrimmed(t *testing.T) {
	st, _ := newTestStore(t)
	def := sampleDef()
	def.BadgeLabel = "  F1  "
	saved, err := st.Put(def)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if saved.BadgeLabel != "F1" {
		t.Errorf("BadgeLabel = %q, want %q", saved.BadgeLabel, "F1")
	}
}

// TestUserStore_URLRulesRejected verifies that a user provider carrying
// non-empty URLRules is rejected at Put time.
func TestUserStore_URLRulesRejected(t *testing.T) {
	st, _ := newTestStore(t)
	def := sampleDef()
	def.URLRules = []URLRule{{ID: "r1"}}
	if _, err := st.Put(def); err == nil {
		t.Error("expected error for non-empty url_rules on user provider, got nil")
	}
}
