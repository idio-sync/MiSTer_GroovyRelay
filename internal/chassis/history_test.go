package chassis

import (
	"context"
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
	if err := reg.Register(historyAdapterStub{
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

type historyAdapterStub struct {
	entries []companion.CompanionHistoryEntry
}

func (historyAdapterStub) Name() string                                     { return "url" }
func (historyAdapterStub) DisplayName() string                              { return "URL" }
func (historyAdapterStub) Fields() []adapters.FieldDef                      { return nil }
func (historyAdapterStub) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (historyAdapterStub) IsEnabled() bool                                  { return true }
func (historyAdapterStub) Start(context.Context) error                      { return nil }
func (historyAdapterStub) Stop() error                                      { return nil }
func (historyAdapterStub) Status() adapters.Status                          { return adapters.Status{} }
func (historyAdapterStub) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeNextCast, nil
}
func (h historyAdapterStub) CompanionHistory() []companion.CompanionHistoryEntry {
	return h.entries
}
