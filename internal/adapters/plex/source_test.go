package plex

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestSourceID_ReturnsPlex(t *testing.T) {
	t.Parallel()
	var v adapters.SourceAvailabilityViewer = &Adapter{}
	if v.SourceID() != "plex" {
		t.Errorf("SourceID = %q, want plex", v.SourceID())
	}
}

func TestConfigured_RequiresEnabledAndLinked(t *testing.T) {
	t.Parallel()
	type matrix struct {
		enabled, linked, want bool
	}
	for _, m := range []matrix{
		{false, false, false},
		{true, false, false},
		{false, true, false},
		{true, true, true},
	} {
		a := newPlexForConfiguredTest(t, m.enabled, m.linked)
		if got := a.Configured(); got != m.want {
			t.Errorf("Configured(enabled=%v, linked=%v) = %v, want %v",
				m.enabled, m.linked, got, m.want)
		}
	}
}

func newPlexForConfiguredTest(t *testing.T, enabled, linked bool) *Adapter {
	t.Helper()
	a := &Adapter{
		cfg: AdapterConfig{TokenStore: &StoredData{}},
	}
	a.SetEnabled(enabled)
	if linked {
		a.mu.Lock()
		a.cfg.TokenStore.AuthToken = "test-token"
		a.mu.Unlock()
	}
	return a
}
