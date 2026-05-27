package dlna

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestSourceID_ReturnsDLNA(t *testing.T) {
	t.Parallel()
	var v adapters.SourceAvailabilityViewer = &Adapter{}
	if v.SourceID() != "dlna" {
		t.Errorf("SourceID = %q, want dlna", v.SourceID())
	}
}

func TestConfigured_TracksIsEnabled(t *testing.T) {
	t.Parallel()
	a := newDLNAForConfiguredTest(t, false)
	if a.Configured() {
		t.Errorf("Configured(enabled=false) = true, want false")
	}
	a = newDLNAForConfiguredTest(t, true)
	if !a.Configured() {
		t.Errorf("Configured(enabled=true) = false, want true")
	}
}

// newDLNAForConfiguredTest builds a minimal Adapter for Configured()
// tests. DLNA's SSDP discovery is passive — no link state, no
// credentials — so flipping Enabled via SetEnabled is the entire
// fixture surface needed.
func newDLNAForConfiguredTest(t *testing.T, enabled bool) *Adapter {
	t.Helper()
	a := &Adapter{}
	a.SetEnabled(enabled)
	return a
}
