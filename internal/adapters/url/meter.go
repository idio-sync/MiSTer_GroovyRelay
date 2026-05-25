package url

import (
	"context"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/hlsbuffer"
)

type hlsMeterHandle struct {
	ref               string
	generation        uint64
	stats             func() hlsbuffer.Stats
	maxCachedSegments int
}

func (a *Adapter) installHLSMeterOverlay(ref string, session *hlsbuffer.Session, cfg hlsbuffer.Config) {
	if session == nil || session.Stats == nil || a.core == nil {
		return
	}
	st := a.core.Status()
	a.mu.Lock()
	defer a.mu.Unlock()
	if st.AdapterRef == ref && st.Generation != 0 {
		a.activeOverlay = &hlsMeterHandle{
			ref:               ref,
			generation:        st.Generation,
			stats:             session.Stats,
			maxCachedSegments: cfg.MaxCachedSegments,
		}
	}
}

func (a *Adapter) clearHLSMeterOverlay(ref string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeOverlay != nil && a.activeOverlay.ref == ref {
		a.activeOverlay = nil
	}
}

func (a *Adapter) hlsMeterClearingOnStop(ref string, base func(string)) func(string) {
	return func(reason string) {
		a.clearHLSMeterOverlay(ref)
		if base != nil {
			base(reason)
		}
	}
}

func (a *Adapter) MeterOverlay(ctx context.Context, snap core.StatusHomeView) (adapters.MeterOverlay, bool) {
	a.mu.Lock()
	h := a.activeOverlay
	a.mu.Unlock()
	if h == nil || h.ref != snap.AdapterRef || h.generation != snap.Generation || h.stats == nil {
		return adapters.MeterOverlay{}, false
	}
	return adapters.MeterOverlay{HLS: adapters.HLSMeterOverlayFromStats(h.stats(), h.maxCachedSegments)}, true
}
