package chassis

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type fakeSourceViewer struct {
	id, configured string
}

func (f fakeSourceViewer) SourceID() string { return f.id }
func (f fakeSourceViewer) Configured() bool { return f.configured == "yes" }

func TestParseAdapterRefSource_KnownPrefixes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ref, want string
	}{
		{"", ""},
		{"streams:mtv-rewind:80s:abc:def", "streams"},
		{"plex:server/key/123", "plex"},
		{"jellyfin:item/abc", "jellyfin"},
		{"dlna:urn:xyz", "dlna"},
		{"weird-no-prefix", ""},
		{"unknown:source:x", ""},
	}
	for _, c := range cases {
		got := parseAdapterRefSource(c.ref)
		if got != c.want {
			t.Errorf("parseAdapterRefSource(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestApplySourceLampState_LampSlotsDerivedFromViewersAndRef(t *testing.T) {
	t.Parallel()
	base := &ReceiverPageData{Source: SourceData{Buttons: []SourceButton{
		{Label: "STREAMS", Action: ""},
		{Label: "PLEX", Action: ""},
		{Label: "JELLYFIN", Action: ""},
		{Label: "DLNA", Action: ""},
		{Label: "AUX", Action: SourceActionAUXStart}, // AUX must NOT get Configured/Casting touched
	}}}
	viewers := []adapters.SourceAvailabilityViewer{
		fakeSourceViewer{id: "streams", configured: "yes"},
		fakeSourceViewer{id: "plex", configured: "no"},
		fakeSourceViewer{id: "jellyfin", configured: "yes"},
		fakeSourceViewer{id: "dlna", configured: "no"},
	}
	applySourceLampState(base, viewers, "streams:mtv-rewind:80s:abc:def")
	want := []struct {
		label               string
		configured, casting bool
	}{
		{"STREAMS", true, true},
		{"PLEX", false, false},
		{"JELLYFIN", true, false},
		{"DLNA", false, false},
		{"AUX", false, false}, // AUX path is the existing applyAUXSourceState — these stay zero here
	}
	for i, w := range want {
		got := base.Source.Buttons[i]
		if got.Label != w.label {
			t.Errorf("button[%d].Label = %q, want %q", i, got.Label, w.label)
		}
		if got.Configured != w.configured {
			t.Errorf("button[%d=%s].Configured = %v, want %v", i, w.label, got.Configured, w.configured)
		}
		if got.Casting != w.casting {
			t.Errorf("button[%d=%s].Casting = %v, want %v", i, w.label, got.Casting, w.casting)
		}
	}
}

func TestApplySourceLampState_EmptyRefClearsCasting(t *testing.T) {
	t.Parallel()
	base := &ReceiverPageData{Source: SourceData{Buttons: []SourceButton{
		{Label: "STREAMS", Action: "", Casting: true}, // stale from prior tick
	}}}
	applySourceLampState(base, []adapters.SourceAvailabilityViewer{
		fakeSourceViewer{id: "streams", configured: "yes"},
	}, "")
	if base.Source.Buttons[0].Casting {
		t.Errorf("Casting = true, want false (empty ref must clear)")
	}
}
