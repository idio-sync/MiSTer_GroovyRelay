package streams

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestSourceID_ReturnsStreams(t *testing.T) {
	t.Parallel()
	var v adapters.SourceAvailabilityViewer = &Adapter{}
	if v.SourceID() != "streams" {
		t.Errorf("SourceID = %q, want streams", v.SourceID())
	}
}

func TestConfigured_TracksIsEnabled(t *testing.T) {
	t.Parallel()
	a := &Adapter{}
	a.SetEnabled(true)
	if !a.Configured() {
		t.Errorf("Configured(enabled=true) = false, want true")
	}
	a.SetEnabled(false)
	if a.Configured() {
		t.Errorf("Configured(enabled=false) = true, want false")
	}
}
