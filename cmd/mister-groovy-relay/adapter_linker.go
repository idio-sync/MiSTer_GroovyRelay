package main

import (
	"context"
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
)

// adapterLinker binds chassis.AdapterLinker to the registry's
// adapters.LinkController-implementing adapters (Plex, Jellyfin). It maps
// adapters.LinkSnapshot → chassis.LinkView and decides Kind/Fields by name.
// The chassis never imports an adapter package; this binding does the join.
type adapterLinker struct{ reg adapterLookup }

func newAdapterLinker(reg adapterLookup) *adapterLinker { return &adapterLinker{reg: reg} }

func (l *adapterLinker) controller(name string) (adapters.LinkController, bool) {
	a, ok := l.reg.Get(name)
	if !ok {
		return nil, false
	}
	lc, ok := a.(adapters.LinkController)
	return lc, ok
}

func linkKind(name string) string {
	if name == "jellyfin" {
		return "credential"
	}
	return "pin" // plex
}

func linkFields(name string) []chassis.LinkField {
	if name == "jellyfin" {
		return []chassis.LinkField{
			{Key: "username", Label: "Username", Kind: "text"},
			{Key: "password", Label: "Password", Kind: "secret"},
		}
	}
	return nil
}

func toLinkView(name string, snap adapters.LinkSnapshot) chassis.LinkView {
	v := chassis.LinkView{
		Kind:           linkKind(name),
		Phase:          snap.Phase,
		LinkedAs:       snap.LinkedAs,
		Code:           snap.Code,
		ExpiresInSec:   snap.ExpiresInSec,
		NeedsServerURL: snap.NeedsServerURL,
		Error:          snap.Error,
	}
	// Credential adapters render their inputs whenever not linked.
	if linkKind(name) == "credential" && snap.Phase != adapters.LinkPhaseLinked {
		v.Fields = linkFields(name)
	}
	return v
}

func (l *adapterLinker) LinkView(name string) (chassis.LinkView, bool) {
	lc, ok := l.controller(name)
	if !ok {
		return chassis.LinkView{}, false
	}
	return toLinkView(name, lc.Snapshot()), true
}

func (l *adapterLinker) StartLink(ctx context.Context, name string, params map[string]string) (chassis.LinkView, error) {
	lc, ok := l.controller(name)
	if !ok {
		return chassis.LinkView{}, fmt.Errorf("adapter %q is not linkable", name)
	}
	snap, err := lc.StartLink(ctx, params)
	if err != nil {
		return chassis.LinkView{}, err
	}
	return toLinkView(name, snap), nil
}

func (l *adapterLinker) LinkStatus(ctx context.Context, name string) (chassis.LinkView, error) {
	lc, ok := l.controller(name)
	if !ok {
		return chassis.LinkView{}, fmt.Errorf("adapter %q is not linkable", name)
	}
	snap, err := lc.PollLink(ctx)
	if err != nil {
		return chassis.LinkView{}, err
	}
	return toLinkView(name, snap), nil
}

func (l *adapterLinker) Unlink(ctx context.Context, name string) (chassis.LinkView, error) {
	lc, ok := l.controller(name)
	if !ok {
		return chassis.LinkView{}, fmt.Errorf("adapter %q is not linkable", name)
	}
	snap, err := lc.Unlink(ctx)
	if err != nil {
		return chassis.LinkView{}, err
	}
	return toLinkView(name, snap), nil
}
