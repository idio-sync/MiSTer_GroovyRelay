package chassis

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/companion"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestSnapshotFromStatusView_PopulatesHistoryFromRegisteredProvider(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	reg := adapters.NewRegistry()
	if err := reg.Register(&historyAdapterStub{
		entries: []companion.CompanionHistoryEntry{
			{
				Title:      "Big Buck Bunny",
				URLDisplay: "youtu.be/abc",
				LastPlayed: now.Add(-2 * time.Hour),
			},
			{
				URLDisplay: "archive.org/details/clip.mp4",
				LastPlayed: now.Add(-25 * time.Hour),
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := nonZeroConfig()
	cfg.Registry = reg

	got := snapshotFromStatusView(cfg, core.StatusHomeView{State: core.StateIdle}, nil, nil, nil, nil, now)

	want := []HistoryRow{
		{Title: "Big Buck Bunny", Source: "URL", When: "2H AGO", Artwork: "URL"},
		{Title: "archive.org/details/clip.mp4", Source: "URL", When: "1D AGO", Artwork: "URL"},
	}
	if len(got.History.Rows) != len(want) {
		t.Fatalf("history rows = %+v, want %+v", got.History.Rows, want)
	}
	for i := range want {
		if got.History.Rows[i] != want[i] {
			t.Fatalf("history row %d = %+v, want %+v", i, got.History.Rows[i], want[i])
		}
	}
}

func TestSnapshotFromStatusView_PopulatesHistoryReplayIDFromReplayProvider(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	reg := adapters.NewRegistry()
	if err := reg.Register(&historyReplayAdapterStub{
		historyAdapterStub: historyAdapterStub{
			entries: []companion.CompanionHistoryEntry{{
				ID:         "h_11111111111111111111111111111111",
				Title:      "Replay Me",
				URLDisplay: "youtu.be/replay",
				LastPlayed: now.Add(-10 * time.Minute),
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := nonZeroConfig()
	cfg.Registry = reg

	got := snapshotFromStatusView(cfg, core.StatusHomeView{State: core.StateIdle}, nil, nil, nil, nil, now)
	if len(got.History.Rows) != 1 {
		t.Fatalf("history rows = %+v, want one row", got.History.Rows)
	}
	if got.History.Rows[0].ReplayID != "h_11111111111111111111111111111111" {
		t.Fatalf("ReplayID = %q, want stable history ID", got.History.Rows[0].ReplayID)
	}
}

func TestHistoryTemplate_RendersReplayButtonOnlyForReplayableRows(t *testing.T) {
	t.Parallel()
	tmpl := parseTemplatesForTest(t)
	data := HistoryData{Rows: []HistoryRow{
		{
			Title:    "Replay Me",
			Source:   "URL",
			When:     "10M AGO",
			Artwork:  "URL",
			ReplayID: "h_11111111111111111111111111111111",
		},
		{
			Title:   "Read Only",
			Source:  "DLNA",
			When:    "1H AGO",
			Artwork: "DLNA",
		},
	}}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "history", data); err != nil {
		t.Fatalf("ExecuteTemplate(history): %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`class="history-replay-btn"`,
		`data-history-replay-id="h_11111111111111111111111111111111"`,
		`aria-label="Recast Replay Me from history"`,
		`title="Recast"`,
		`&#9656;`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("history template missing %q; body:\n%s", want, body)
		}
	}
	if strings.Count(body, `class="history-replay-btn"`) != 1 {
		t.Fatalf("replay button count = %d, want 1; body:\n%s", strings.Count(body, `class="history-replay-btn"`), body)
	}
}

func TestHandleHistoryPlayPost_CallsMatchingReplayProvider(t *testing.T) {
	t.Parallel()
	const historyID = "h_22222222222222222222222222222222"
	player := &recordingHistoryReplayAdapterStub{
		historyAdapterStub: historyAdapterStub{entries: []companion.CompanionHistoryEntry{{
			ID:         historyID,
			Title:      "Replay Me",
			URLDisplay: "youtu.be/replay",
			LastPlayed: time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
		}}},
	}
	reg := adapters.NewRegistry()
	if err := reg.Register(player); err != nil {
		t.Fatal(err)
	}
	cfg := nonZeroConfig()
	cfg.Registry = reg
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	form := url.Values{}
	form.Set("id", historyID)
	req := httptest.NewRequest(http.MethodPost, "/receiver/history/play", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	srv.handleHistoryPlayPost(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if player.playedID != historyID {
		t.Fatalf("playedID = %q, want %q", player.playedID, historyID)
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("success body missing ok=true: %s", rec.Body.String())
	}
}

func TestHandleHistoryPlayPost_UnknownIDReturns404(t *testing.T) {
	t.Parallel()
	reg := adapters.NewRegistry()
	if err := reg.Register(&recordingHistoryReplayAdapterStub{
		historyAdapterStub: historyAdapterStub{entries: []companion.CompanionHistoryEntry{{
			ID:         "h_33333333333333333333333333333333",
			URLDisplay: "youtu.be/known",
			LastPlayed: time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := nonZeroConfig()
	cfg.Registry = reg
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	form := url.Values{}
	form.Set("id", "h_44444444444444444444444444444444")
	req := httptest.NewRequest(http.MethodPost, "/receiver/history/play", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	srv.handleHistoryPlayPost(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("Code = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"chip":"NOT FOUND"`) {
		t.Fatalf("404 body missing NOT FOUND chip: %s", rec.Body.String())
	}
}

type historyAdapterStub struct {
	mu      sync.RWMutex
	entries []companion.CompanionHistoryEntry
}

func (h *historyAdapterStub) setEntries(entries []companion.CompanionHistoryEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append([]companion.CompanionHistoryEntry(nil), entries...)
}

func (*historyAdapterStub) Name() string                                     { return "url" }
func (*historyAdapterStub) DisplayName() string                              { return "URL" }
func (*historyAdapterStub) Fields() []adapters.FieldDef                      { return nil }
func (*historyAdapterStub) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (*historyAdapterStub) IsEnabled() bool                                  { return true }
func (*historyAdapterStub) Start(context.Context) error                      { return nil }
func (*historyAdapterStub) Stop() error                                      { return nil }
func (*historyAdapterStub) Status() adapters.Status                          { return adapters.Status{} }
func (*historyAdapterStub) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeNextCast, nil
}
func (h *historyAdapterStub) CompanionHistory() []companion.CompanionHistoryEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return append([]companion.CompanionHistoryEntry(nil), h.entries...)
}

type historyReplayAdapterStub struct {
	historyAdapterStub
	playedID string
}

func (*historyReplayAdapterStub) CompanionHistoryPlay(context.Context, string) (companion.CompanionPlayResult, error) {
	return companion.CompanionPlayResult{ResolvedVia: "direct"}, nil
}

type recordingHistoryReplayAdapterStub struct {
	historyAdapterStub
	playedID string
}

func (h *recordingHistoryReplayAdapterStub) CompanionHistoryPlay(_ context.Context, id string) (companion.CompanionPlayResult, error) {
	h.playedID = id
	return companion.CompanionPlayResult{ResolvedVia: "direct"}, nil
}
