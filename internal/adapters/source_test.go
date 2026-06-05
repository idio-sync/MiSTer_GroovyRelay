package adapters

import "testing"

// fakeSourceAvailabilityViewer is a compile-time conformance fixture.
// If the SourceAvailabilityViewer interface changes shape, this fails
// to build, alerting changeset reviewers before the surface drifts.
type fakeSourceAvailabilityViewer struct {
	id   string
	conf bool
}

func (f fakeSourceAvailabilityViewer) SourceID() string { return f.id }
func (f fakeSourceAvailabilityViewer) Configured() bool { return f.conf }

func TestSourceAvailabilityViewer_StructuralConformance(t *testing.T) {
	t.Parallel()
	var v SourceAvailabilityViewer = fakeSourceAvailabilityViewer{id: "streams", conf: true}
	if v.SourceID() != "streams" {
		t.Errorf("SourceID = %q, want streams", v.SourceID())
	}
	if !v.Configured() {
		t.Errorf("Configured = false, want true")
	}
}
