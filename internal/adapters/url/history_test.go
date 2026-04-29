package url

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestHistoryDedupeKey_DifferentUsername(t *testing.T) {
	if dedupeKey("https://alice@host/x") != dedupeKey("https://bob@host/x") {
		t.Error("dedupeKey should ignore username")
	}
}

func TestHistoryDedupeKey_DifferentPassword(t *testing.T) {
	if dedupeKey("https://u:a@host/x") != dedupeKey("https://u:b@host/x") {
		t.Error("dedupeKey should ignore password")
	}
}

func TestHistoryDedupeKey_DifferentPath(t *testing.T) {
	if dedupeKey("https://host/a") == dedupeKey("https://host/b") {
		t.Error("dedupeKey should distinguish paths")
	}
}

func TestHistoryDedupeKey_Unparseable(t *testing.T) {
	// Control characters fail net/url.Parse.
	if k := dedupeKey("\x00not-a-url"); k != "" {
		t.Errorf("dedupeKey on unparseable input should be empty; got %q", k)
	}
}

func TestHistory_RoundTrip(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "h.json")
	h := LoadHistory(tmp)
	h.AddOrBump("https://a.example/1")
	h.AddOrBump("https://b.example/2")
	h.AddOrBump("https://c.example/3")

	h2 := LoadHistory(tmp)
	list := h2.List()
	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}
	if list[0].URL != "https://c.example/3" ||
		list[1].URL != "https://b.example/2" ||
		list[2].URL != "https://a.example/1" {
		t.Errorf("order = %v, want [c, b, a]", list)
	}
}

func TestHistory_LRUBump(t *testing.T) {
	h := LoadHistory("")
	h.AddOrBump("https://a/")
	h.AddOrBump("https://b/")
	h.AddOrBump("https://c/")
	h.AddOrBump("https://b/") // bump existing
	list := h.List()
	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}
	if list[0].URL != "https://b/" || list[1].URL != "https://c/" || list[2].URL != "https://a/" {
		t.Errorf("order = %v, want [b, c, a]", list)
	}
}

func TestHistory_Eviction(t *testing.T) {
	h := LoadHistory("")
	for i := 0; i < 11; i++ {
		h.AddOrBump("https://example/" + string(rune('a'+i)))
	}
	if h.Len() != 10 {
		t.Fatalf("len = %d, want 10", h.Len())
	}
	list := h.List()
	if list[0].URL != "https://example/k" {
		t.Errorf("first = %q, want most recent (k)", list[0].URL)
	}
	if list[9].URL != "https://example/b" {
		t.Errorf("last = %q, want second-oldest (b) after a was evicted", list[9].URL)
	}
	for _, e := range list {
		if e.URL == "https://example/a" {
			t.Error("oldest entry 'a' should have been evicted")
		}
	}
}

func TestHistory_Remove(t *testing.T) {
	h := LoadHistory("")
	h.AddOrBump("https://a/")
	h.AddOrBump("https://b/")
	h.AddOrBump("https://c/") // list = [c, b, a]
	if !h.Remove(1) {         // remove "b"
		t.Fatal("Remove(1) returned false")
	}
	list := h.List()
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	if list[0].URL != "https://c/" || list[1].URL != "https://a/" {
		t.Errorf("after remove, list = %v, want [c, a]", list)
	}
}

func TestHistory_Remove_OutOfRange(t *testing.T) {
	h := LoadHistory("")
	h.AddOrBump("https://a/")
	if h.Remove(5) {
		t.Error("Remove(5) returned true on len-1 history")
	}
	if h.Remove(-1) {
		t.Error("Remove(-1) returned true")
	}
	if h.Len() != 1 {
		t.Errorf("history mutated by out-of-range Remove: len = %d", h.Len())
	}
}

func TestHistory_Get(t *testing.T) {
	h := LoadHistory("")
	h.AddOrBump("https://a/")
	h.AddOrBump("https://b/") // list = [b, a]
	e, ok := h.Get(0)
	if !ok || e.URL != "https://b/" {
		t.Errorf("Get(0) = %+v, %v; want b, true", e, ok)
	}
	if _, ok := h.Get(99); ok {
		t.Error("Get(99) returned ok=true on small history")
	}
}

func TestHistory_CorruptFile_StartsEmpty(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "h.json")
	if err := os.WriteFile(tmp, []byte("not valid json"), 0644); err != nil {
		t.Fatal(err)
	}
	h := LoadHistory(tmp)
	if h.Len() != 0 {
		t.Errorf("len = %d, want 0 after corrupt-file load", h.Len())
	}
}

func TestHistory_UnknownVersion_StartsEmpty(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "h.json")
	if err := os.WriteFile(tmp, []byte(`{"version": 999, "entries": []}`), 0644); err != nil {
		t.Fatal(err)
	}
	h := LoadHistory(tmp)
	if h.Len() != 0 {
		t.Errorf("len = %d, want 0 after unknown-version load", h.Len())
	}
}

func TestHistory_ConcurrentAddOrBump(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "h.json")
	h := LoadHistory(tmp)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h.AddOrBump("https://example/" + string(rune('0'+(i%10))))
		}(i)
	}
	wg.Wait()
	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var hf historyFile
	if err := json.Unmarshal(data, &hf); err != nil {
		t.Errorf("file not valid JSON: %v\n%s", err, data)
	}
	list := h.List()
	if len(list) > historyMaxEntries {
		t.Errorf("len = %d, exceeds max %d", len(list), historyMaxEntries)
	}
	for _, e := range list {
		if e.LastPlayedAt.IsZero() {
			t.Errorf("entry %q has zero timestamp", e.URL)
		}
		if dedupeKey(e.URL) == "" {
			t.Errorf("entry %q is unparseable", e.URL)
		}
	}
}

func TestHistory_EmptyPath_NoSave(t *testing.T) {
	h := LoadHistory("")
	h.AddOrBump("https://a/")
	if h.Len() != 1 {
		t.Errorf("Add to in-memory history failed: len = %d", h.Len())
	}
}

func TestHistory_SaveFailure_DisablesPersistence(t *testing.T) {
	// Parent dir doesn't exist → config.WriteAtomic's tempfile-create
	// step (atomic.go:38, OpenFile O_CREATE|O_EXCL) fails, returning
	// "atomic: create tmp" before reaching os.Rename. saveLocked sets
	// persistDisabled = true on any non-nil WriteAtomic error.
	bad := filepath.Join(t.TempDir(), "no-such-dir", "h.json")
	h := LoadHistory(bad)
	h.AddOrBump("https://a/")
	h.AddOrBump("https://b/") // second save should also no-op silently
	if h.Len() != 2 {
		t.Errorf("in-memory ops should still work, len = %d", h.Len())
	}
	h.mu.Lock()
	disabled := h.persistDisabled
	h.mu.Unlock()
	if !disabled {
		t.Error("persistDisabled should be true after rename failure")
	}
}

func TestHistory_DedupeStripsUserinfo(t *testing.T) {
	h := LoadHistory("")
	h.AddOrBump("https://a@host/x")
	h.AddOrBump("https://b:secret@host/x")
	if h.Len() != 1 {
		t.Errorf("len = %d, want 1 after userinfo-only difference", h.Len())
	}
	list := h.List()
	if list[0].URL != "https://b:secret@host/x" {
		t.Errorf("stored URL = %q, want most recent (with creds)", list[0].URL)
	}
}

func TestHistory_DedupeStripsUsername(t *testing.T) {
	h := LoadHistory("")
	h.AddOrBump("https://alice@host/x")
	h.AddOrBump("https://bob@host/x")
	if h.Len() != 1 {
		t.Errorf("len = %d, want 1 (different usernames should collapse)", h.Len())
	}
}

func TestHistory_UnparseableURL_NotRecorded(t *testing.T) {
	h := LoadHistory("")
	h.AddOrBump("\x00not-a-url")
	if h.Len() != 0 {
		t.Errorf("len = %d, want 0 (unparseable URLs must not be recorded)", h.Len())
	}
}

func TestHistory_SetTitle_AttachesAndPersists(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "h.json")
	h := LoadHistory(tmp)
	h.AddOrBump("https://youtu.be/abc")
	h.SetTitle("https://youtu.be/abc", "Big Buck Bunny")

	h2 := LoadHistory(tmp)
	list := h2.List()
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].Title != "Big Buck Bunny" {
		t.Errorf("title = %q, want %q", list[0].Title, "Big Buck Bunny")
	}
}

func TestHistory_SetTitle_EmptyIsNoop(t *testing.T) {
	h := LoadHistory("")
	h.AddOrBump("https://youtu.be/abc")
	h.SetTitle("https://youtu.be/abc", "First Title")
	h.SetTitle("https://youtu.be/abc", "") // must not blank
	list := h.List()
	if list[0].Title != "First Title" {
		t.Errorf("empty SetTitle blanked existing title; got %q", list[0].Title)
	}
}

func TestHistory_SetTitle_UnknownURLIsNoop(t *testing.T) {
	h := LoadHistory("")
	h.AddOrBump("https://a/")
	h.SetTitle("https://b/", "Should Not Apply")
	list := h.List()
	if list[0].Title != "" {
		t.Errorf("title attached to wrong entry: %q", list[0].Title)
	}
}

func TestHistory_SetTitle_MatchesByDedupeKey(t *testing.T) {
	// Userinfo differs but dedupe key matches → title attaches.
	h := LoadHistory("")
	h.AddOrBump("https://alice@host/x")
	h.SetTitle("https://bob:secret@host/x", "Shared Page")
	list := h.List()
	if list[0].Title != "Shared Page" {
		t.Errorf("title = %q, want match by dedupe key", list[0].Title)
	}
}

func TestHistory_AddOrBump_PreservesTitleAcrossBump(t *testing.T) {
	// Re-casting an existing URL must not blank a title that was set
	// by a previous successful resolve. yt-dlp will normally re-set
	// it, but the bump itself shouldn't strip metadata.
	h := LoadHistory("")
	h.AddOrBump("https://youtu.be/abc")
	h.SetTitle("https://youtu.be/abc", "Big Buck Bunny")
	h.AddOrBump("https://youtu.be/abc") // re-cast
	list := h.List()
	if list[0].Title != "Big Buck Bunny" {
		t.Errorf("AddOrBump dropped title across bump; got %q", list[0].Title)
	}
}

func TestHistory_AddOrBump_PreservesTitleWhenURLChanges(t *testing.T) {
	// Same dedupe key, different stored URL (creds rotated). Title
	// should still carry forward.
	h := LoadHistory("")
	h.AddOrBump("https://alice@host/x")
	h.SetTitle("https://alice@host/x", "Page Title")
	h.AddOrBump("https://bob:secret@host/x")
	list := h.List()
	if list[0].URL != "https://bob:secret@host/x" {
		t.Errorf("URL = %q, want most-recent creds", list[0].URL)
	}
	if list[0].Title != "Page Title" {
		t.Errorf("Title = %q, want preserved across creds rotation", list[0].Title)
	}
}

func TestHistory_TitleOmitemptyOnDisk(t *testing.T) {
	// Entries without a title must not emit "title":"" — that would
	// bloat the file and break the spec's documented schema for
	// minimum-payload entries.
	tmp := filepath.Join(t.TempDir(), "h.json")
	h := LoadHistory(tmp)
	h.AddOrBump("https://example/no-title")

	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"title"`)) {
		t.Errorf("on-disk JSON includes title key for an untitled entry: %s", data)
	}
}

func TestHistory_LoadEntryWithoutTitle_BackCompat(t *testing.T) {
	// Pre-title schema: old files will not have a title key. Loader
	// must accept them and leave Title at its zero value.
	tmp := filepath.Join(t.TempDir(), "h.json")
	raw := `{"version":1,"entries":[{"url":"https://example/x","last_played_at":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(tmp, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	h := LoadHistory(tmp)
	list := h.List()
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].Title != "" {
		t.Errorf("pre-title entry got non-empty title: %q", list[0].Title)
	}
	if list[0].URL != "https://example/x" {
		t.Errorf("URL roundtrip failed: %q", list[0].URL)
	}
}

func TestHistory_LoadDedupesAndDropsUnparseable(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "h.json")
	// Hand-crafted file with a duplicate dedupe key (different creds)
	// and an unparseable URL — both should be cleaned up on load.
	// "http://%zz" has an invalid percent-escape, which net/url.Parse
	// rejects ("invalid URL escape"); dedupeKey returns "" for it.
	raw := `{"version":1,"entries":[
		{"url":"https://alice@host/x","last_played_at":"2026-01-01T00:00:00Z"},
		{"url":"https://bob@host/x","last_played_at":"2026-01-02T00:00:00Z"},
		{"url":"http://%zz","last_played_at":"2026-01-03T00:00:00Z"}
	]}`
	if err := os.WriteFile(tmp, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	h := LoadHistory(tmp)
	if h.Len() != 1 {
		t.Errorf("len = %d, want 1 after dedupe + drop-unparseable", h.Len())
	}
	list := h.List()
	if len(list) > 0 && list[0].URL != "https://alice@host/x" {
		t.Errorf("kept URL = %q, want first occurrence (alice)", list[0].URL)
	}
}
