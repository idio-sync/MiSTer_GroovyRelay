package plex

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

// tokenFilePath returns the on-disk path to the persisted token/UUID
// file. Single source of truth so unlink, linking, and tokenstore
// agree on the filename (data.json today; kept behind this helper
// so a future rename doesn't drift across files).
func tokenFilePath(dataDir string) string {
	return filepath.Join(dataDir, storedDataFilename)
}

// pollPendingLink runs PollPIN for one in-flight link flow. On
// success it writes the token to TokenStore under a.mu AND checks
// that a.pending still points at this flow. If the user clicked
// "Link" again in the meantime (abandoning this pendingLink), we
// drop the token on the floor rather than persist a stale auth
// token — that's the I2 "rapid re-click" race fix.
func (a *Adapter) pollPendingLink(pl *pendingLink, pinID int, deviceUUID string) {
	token, err := pollForTokenCtx(pl.ctx, pinID, deviceUUID, 15*time.Minute)
	if err != nil {
		// An abandoned flow (rapid re-click, or Stop/disable while a PIN is
		// pending) cancels pl.ctx. That is a race artifact, not a link
		// failure: complete silently — no adapter-link-failed emission — and
		// with an empty error so linkSnapshot reports the flow as unlinked,
		// not errored. Mirrors the success-race abandon guard below.
		if errors.Is(err, context.Canceled) {
			pl.complete("", "")
			return
		}
		a.finishPendingLink(pl, "", err.Error())
		return
	}

	a.mu.Lock()
	if a.pending != pl {
		// Abandoned by a newer flow; don't clobber its state.
		// Do NOT call finishPendingLink here — abandoned flows must
		// not emit events (they are a race artifact, not a real outcome).
		a.mu.Unlock()
		pl.complete("", "abandoned")
		return
	}
	a.cfg.TokenStore.AuthToken = token
	dataDir := a.cfg.Bridge.DataDir
	store := a.cfg.TokenStore
	a.mu.Unlock()

	// SaveStoredData is disk I/O; run outside a.mu so status reads
	// and other handlers don't block on fsync.
	if err := SaveStoredData(dataDir, store); err != nil {
		a.finishPendingLink(pl, "", fmt.Sprintf("token received but save failed: %v", err))
		return
	}
	// Start advertising to plex.tv now rather than on the next restart —
	// Start only launched the loop if a token existed at boot, and Unlink
	// cancels it. A loop still running from a previous link is replaced
	// so it doesn't keep registering with the superseded token. No-op
	// when the adapter is stopped.
	a.mu.Lock()
	if a.regCancel != nil {
		a.regCancel()
		a.regCancel = nil
	}
	a.startRegistrationLocked()
	a.mu.Unlock()
	a.finishPendingLink(pl, token, "")
}

// finishPendingLink marks pl as done and emits the appropriate lifecycle
// event. Exactly one of token or errMsg must be non-empty:
//
//   - token non-empty → success: emit adapter-linked (Info), complete with token.
//   - errMsg non-empty → failure: emit adapter-link-failed (Err), complete with error.
//
// The abandoned-flow branch in pollPendingLink calls pl.complete directly
// without going through here, so abandoned flows never emit.
func (a *Adapter) finishPendingLink(pl *pendingLink, token, errMsg string) {
	if token != "" {
		pl.complete(token, "")
		a.emit(eventlog.SeverityInfo, "adapter-linked")
		return
	}
	pl.complete("", errMsg)
	a.emit(eventlog.SeverityErr, fmt.Sprintf("adapter-link-failed: %s", errMsg))
}

// pollForTokenCtx wraps PollPIN with ctx cancellation so the
// StartLink background poller can exit early when the pendingLink is
// abandoned (re-click, adapter stop).
func pollForTokenCtx(ctx context.Context, pinID int, uuid string, timeout time.Duration) (string, error) {
	type result struct {
		token string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		token, err := PollPIN(pinID, uuid, timeout)
		done <- result{token, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-done:
		return res.token, res.err
	}
}
