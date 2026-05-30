package main

import (
	"context"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
)

// fakeLinkAdapter embeds adapters.Adapter and implements adapters.LinkController.
// Used for "plex" and "jellyfin" entries in fakeReg.
type fakeLinkAdapter struct {
	adapters.Adapter // embed nil; only LinkController methods used in this test
	snap             adapters.LinkSnapshot
}

func (f *fakeLinkAdapter) Snapshot() adapters.LinkSnapshot { return f.snap }
func (f *fakeLinkAdapter) StartLink(_ context.Context, _ map[string]string) (adapters.LinkSnapshot, error) {
	return f.snap, nil
}
func (f *fakeLinkAdapter) PollLink(_ context.Context) (adapters.LinkSnapshot, error) {
	return f.snap, nil
}
func (f *fakeLinkAdapter) Unlink(_ context.Context) (adapters.LinkSnapshot, error) {
	return adapters.LinkSnapshot{Phase: adapters.LinkPhaseUnlinked}, nil
}

// fakeNonLinkAdapter embeds adapters.Adapter but defines NONE of the
// LinkController methods. Registering it as "dlna" correctly tests the
// not-linkable path: the type assertion a.(adapters.LinkController) fails.
type fakeNonLinkAdapter struct {
	adapters.Adapter
}

type fakeReg struct{ m map[string]adapters.Adapter }

func (r fakeReg) Get(name string) (adapters.Adapter, bool) { a, ok := r.m[name]; return a, ok }

func TestAdapterLinker_KindAndFields(t *testing.T) {
	reg := fakeReg{m: map[string]adapters.Adapter{
		"plex":     &fakeLinkAdapter{snap: adapters.LinkSnapshot{Phase: "unlinked"}},
		"jellyfin": &fakeLinkAdapter{snap: adapters.LinkSnapshot{Phase: "unlinked", NeedsServerURL: true}},
		"dlna":     &fakeNonLinkAdapter{}, // does NOT implement LinkController
	}}
	l := newAdapterLinker(reg)

	// plex: kind="pin", no fields when unlinked
	pv, ok := l.LinkView("plex")
	if !ok || pv.Kind != "pin" || len(pv.Fields) != 0 {
		t.Errorf("plex view = %+v ok=%v, want pin + no fields", pv, ok)
	}

	// jellyfin: kind="credential", 2 fields when not linked
	jv, ok := l.LinkView("jellyfin")
	if !ok || jv.Kind != "credential" || len(jv.Fields) != 2 {
		t.Errorf("jellyfin view = %+v ok=%v, want credential + 2 fields", jv, ok)
	}

	// dlna: not linkable — fakeNonLinkAdapter has no Link methods
	_, ok = l.LinkView("dlna")
	if ok {
		t.Errorf("dlna LinkView ok=true, want false (non-linkable adapter)")
	}

	// unknown adapter
	_, ok = l.LinkView("unknown")
	if ok {
		t.Errorf("unknown LinkView ok=true, want false")
	}
}

// Compile-time assertion: *adapterLinker satisfies chassis.AdapterLinker.
var _ chassis.AdapterLinker = (*adapterLinker)(nil)
