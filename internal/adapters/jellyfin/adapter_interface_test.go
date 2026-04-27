package jellyfin

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// TestAdapter_ImplementsContract is a compile-time check that the
// jellyfin adapter satisfies every interface its consumers expect.
// Compiles → passes; missing-method → fails to build.
func TestAdapter_ImplementsContract(t *testing.T) {
	var _ adapters.Adapter = (*Adapter)(nil)
	var _ adapters.Validator = (*Adapter)(nil)
	var _ adapters.RouteProvider = (*Adapter)(nil)
}

func TestAdapter_NameAndDisplay(t *testing.T) {
	a := New(nil, "/tmp/data", "test-uuid")
	if a.Name() != "jellyfin" {
		t.Errorf("Name() = %q, want %q", a.Name(), "jellyfin")
	}
	if a.DisplayName() != "Jellyfin" {
		t.Errorf("DisplayName() = %q, want %q", a.DisplayName(), "Jellyfin")
	}
}

func TestAdapter_FieldsSchema(t *testing.T) {
	a := New(nil, "/tmp/data", "test-uuid")
	fields := a.Fields()
	wantKeys := []string{"enabled", "server_url", "device_name", "max_video_bitrate_kbps"}
	if len(fields) != len(wantKeys) {
		t.Fatalf("len(Fields()) = %d, want %d", len(fields), len(wantKeys))
	}
	for i, k := range wantKeys {
		if fields[i].Key != k {
			t.Errorf("Fields()[%d].Key = %q, want %q", i, fields[i].Key, k)
		}
	}
}

func TestAdapter_InitialState(t *testing.T) {
	a := New(nil, "/tmp/data", "test-uuid")
	st := a.Status()
	if st.State != adapters.StateStopped {
		t.Errorf("initial State = %v, want StateStopped", st.State)
	}
	if a.IsEnabled() {
		t.Errorf("initial IsEnabled = true, want false")
	}
}
