package plex

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// linkSnapshot reports the current link state as an adapters.LinkSnapshot,
// mirroring the state logic in ExtraPanelHTML/handleLinkStatus. Plex
// persists only a device UUID + auth token (no account identity), so a
// linked snapshot carries an empty LinkedAs and the UI renders plain
// "Linked".
func (a *Adapter) linkSnapshot() adapters.LinkSnapshot {
	token := a.snapshotToken()
	pending := a.snapshotPending()

	switch {
	case token != "":
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseLinked}
	case pending != nil && pending.Done() && pending.Error() != "":
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: pending.Error()}
	case pending != nil && pending.Expired():
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: "link code expired"}
	case pending != nil && !pending.Done():
		tl := pending.TimeLeft()
		return adapters.LinkSnapshot{
			Phase:        adapters.LinkPhasePending,
			Code:         pending.Code(),
			ExpiresInSec: int(tl / time.Second),
		}
	default:
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseUnlinked}
	}
}

// Snapshot implements adapters.LinkController.
func (a *Adapter) Snapshot() adapters.LinkSnapshot { return a.linkSnapshot() }

// StartLink implements adapters.LinkController. params is ignored (Plex's
// PIN flow takes no inputs). It mirrors handleLinkStart: abandon any
// in-flight flow, request a fresh PIN under linkStartMu, store the
// pendingLink, and arm the background poller. ctx bounds the RequestPIN
// round-trip; the poller itself runs to the 15-minute expiry regardless
// of ctx (it must outlive the originating request).
func (a *Adapter) StartLink(ctx context.Context, _ map[string]string) (adapters.LinkSnapshot, error) {
	a.linkStartMu.Lock()
	defer a.linkStartMu.Unlock()

	if old := a.snapshotPending(); old != nil && !old.Done() {
		old.abandon()
	}

	deviceUUID := a.cfg.TokenStore.DeviceUUID
	deviceName := a.snapshotCfg().DeviceName

	type pinResult struct {
		pin *PinResponse
		err error
	}
	done := make(chan pinResult, 1)
	go func() {
		pin, err := RequestPIN(deviceUUID, deviceName, a.cfg.Version)
		done <- pinResult{pin, err}
	}()
	var pin *PinResponse
	select {
	case <-ctx.Done():
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: "plex.tv request cancelled"}, nil
	case res := <-done:
		if res.err != nil {
			return adapters.LinkSnapshot{
				Phase: adapters.LinkPhaseError,
				Error: "plex.tv unreachable: " + res.err.Error(),
			}, nil
		}
		pin = res.pin
	}

	pl := newPendingLink(pin.Code, pin.ID, time.Now().Add(15*time.Minute))
	a.mu.Lock()
	a.pending = pl
	a.mu.Unlock()

	go a.pollPendingLink(pl, pin.ID, deviceUUID)

	return a.linkSnapshot(), nil
}

// PollLink implements adapters.LinkController by reading current state.
// The actual plex.tv polling happens in the background pollPendingLink
// goroutine; this just reports the latest snapshot so the chassis status
// route never stacks plex.tv network calls per browser tick.
func (a *Adapter) PollLink(_ context.Context) (adapters.LinkSnapshot, error) {
	return a.linkSnapshot(), nil
}

// Unlink implements adapters.LinkController. Mirrors handleUnlink:
// best-effort RevokeDevice (ctx-bounded), rotate the token file aside,
// clear the in-memory token, and cancel the plex.tv registration loop.
func (a *Adapter) Unlink(ctx context.Context) (adapters.LinkSnapshot, error) {
	a.mu.Lock()
	uuid := a.cfg.TokenStore.DeviceUUID
	token := a.cfg.TokenStore.AuthToken
	a.mu.Unlock()
	if token != "" {
		done := make(chan error, 1)
		go func() { done <- RevokeDevice(uuid, token) }()
		select {
		case <-ctx.Done():
			slog.Info("plex.tv revoke cancelled; proceeding with local cleanup")
		case err := <-done:
			if err != nil {
				slog.Info("plex.tv revoke failed; proceeding with local cleanup", "err", err)
			}
		}
	}

	src := tokenFilePath(a.cfg.Bridge.DataDir)
	dst := filepath.Join(a.cfg.Bridge.DataDir,
		fmt.Sprintf(".%s.unlinked-%d", storedDataFilename, time.Now().Unix()))
	_ = os.Rename(src, dst)

	a.mu.Lock()
	a.cfg.TokenStore.AuthToken = ""
	cancel := a.regCancel
	a.regCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	return adapters.LinkSnapshot{Phase: adapters.LinkPhaseUnlinked}, nil
}

// Compile-time conformance: *Adapter satisfies the link contract.
var _ adapters.LinkController = (*Adapter)(nil)
