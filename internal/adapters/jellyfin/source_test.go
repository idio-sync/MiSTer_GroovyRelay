package jellyfin

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestSourceID_ReturnsJellyfin(t *testing.T) {
	t.Parallel()
	var v adapters.SourceAvailabilityViewer = &Adapter{}
	if v.SourceID() != "jellyfin" {
		t.Errorf("SourceID = %q, want jellyfin", v.SourceID())
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
		a := newJellyfinForConfiguredTest(t, m.enabled, m.linked)
		if got := a.Configured(); got != m.want {
			t.Errorf("Configured(enabled=%v, linked=%v) = %v, want %v",
				m.enabled, m.linked, got, m.want)
		}
	}
}

func newJellyfinForConfiguredTest(t *testing.T, enabled, linked bool) *Adapter {
	t.Helper()
	a := &Adapter{link: NewLinkState()}
	a.SetEnabled(enabled)
	if linked {
		a.link.SetLinked("test-user", "test-server")
	}
	return a
}
