package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// linkSnapshot reports link state from the persisted token, NOT the
// in-memory LinkState (which is only hydrated during Start). This makes
// the initial drawer render correct even when the adapter is disabled or
// not yet started. It never probes the server or wipes tokens — server_url
// drift / token rejection remain Start()'s responsibility.
//
// Unlike LoadToken, this function distinguishes a JSON parse failure
// (→ error phase) from a missing file (→ unlinked). LoadToken silently
// swallows corrupt JSON; the drawer must surface corruption so the
// operator knows to re-link rather than silently appearing unlinked.
func (a *Adapter) linkSnapshot() adapters.LinkSnapshot {
	data, err := os.ReadFile(a.tokenPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return adapters.LinkSnapshot{
				Phase:          adapters.LinkPhaseUnlinked,
				NeedsServerURL: a.configuredServerURL() == "",
			}
		}
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: err.Error()}
	}

	var tok Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return adapters.LinkSnapshot{
			Phase: adapters.LinkPhaseError,
			Error: fmt.Sprintf("token file corrupt: %v", err),
		}
	}

	if tok.AccessToken == "" {
		return adapters.LinkSnapshot{
			Phase:          adapters.LinkPhaseUnlinked,
			NeedsServerURL: a.configuredServerURL() == "",
		}
	}
	return adapters.LinkSnapshot{
		Phase:    adapters.LinkPhaseLinked,
		LinkedAs: fmt.Sprintf("%s on %s", tok.UserName, tok.ServerID),
	}
}

// Snapshot implements adapters.LinkController.
func (a *Adapter) Snapshot() adapters.LinkSnapshot { return a.linkSnapshot() }

// StartLink implements adapters.LinkController. Reads username/password from
// params, authenticates against the saved server_url, persists the token,
// and (when enabled) restarts the adapter via the lifecycle Start to pick up
// the rotated token. Returns a terminal snapshot; a successful auth whose
// restart fails is still linked, with the restart trouble surfaced in Error.
//
// The start method is named StartLink (not Start) so this interface can be
// implemented directly on *Adapter — the lifecycle Start(context.Context) error
// already exists and two methods with the same name on one receiver will not
// compile.
func (a *Adapter) StartLink(ctx context.Context, params map[string]string) (adapters.LinkSnapshot, error) {
	serverURL := a.configuredServerURL()
	username := strings.TrimSpace(params["username"])
	password := params["password"]

	if serverURL == "" {
		a.link.SetError("set Server URL above and save before linking")
		return adapters.LinkSnapshot{
			Phase:          adapters.LinkPhaseError,
			NeedsServerURL: true,
			Error:          "Set a Server URL above and save before linking.",
		}, nil
	}
	if username == "" || password == "" {
		a.link.SetError("username and password are required")
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: "Username and password are required."}, nil
	}

	a.link.SetLinking()
	res, err := AuthenticateByName(ctx, AuthRequest{
		ServerURL: serverURL, Username: username, Password: password,
		DeviceID: a.deviceID, Version: linkVersion,
	})
	if err != nil {
		a.link.SetError(err.Error())
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: err.Error()}, nil
	}

	tok := Token{
		AccessToken: res.AccessToken, UserID: res.UserID, UserName: res.UserName,
		ServerID: res.ServerID, ServerURL: serverURL,
	}
	if err := SaveToken(a.tokenPath(), tok); err != nil {
		a.link.SetError("link succeeded but persist failed: " + err.Error())
		return adapters.LinkSnapshot{Phase: adapters.LinkPhaseError, Error: "link succeeded but persist failed: " + err.Error()}, nil
	}
	a.link.SetLinked(res.UserName, res.ServerID)

	linkedAs := fmt.Sprintf("%s on %s", res.UserName, res.ServerID)
	a.mu.Lock()
	enabled := a.cfg.Enabled
	a.mu.Unlock()
	if enabled {
		_ = a.Stop()
		if startErr := a.Start(context.Background()); startErr != nil {
			return adapters.LinkSnapshot{
				Phase:    adapters.LinkPhaseLinked,
				LinkedAs: linkedAs,
				Error:    "adapter restart failed: " + startErr.Error(),
			}, nil
		}
	}
	return adapters.LinkSnapshot{Phase: adapters.LinkPhaseLinked, LinkedAs: linkedAs}, nil
}

// PollLink implements adapters.LinkController. Jellyfin links synchronously,
// so polling just reports the current persisted state.
func (a *Adapter) PollLink(_ context.Context) (adapters.LinkSnapshot, error) {
	return a.linkSnapshot(), nil
}

// Unlink implements adapters.LinkController. Best-effort server logout
// (ctx-bounded), wipe the local token, reset link state, stop the adapter.
func (a *Adapter) Unlink(ctx context.Context) (adapters.LinkSnapshot, error) {
	tok, _ := LoadToken(a.tokenPath())
	if tok.AccessToken != "" {
		a.mu.Lock()
		deviceName := a.cfg.DeviceName
		a.mu.Unlock()
		if err := Logout(ctx, LogoutInput{
			ServerURL:  tok.ServerURL,
			Token:      tok.AccessToken,
			DeviceID:   a.deviceID,
			DeviceName: deviceName,
			Version:    linkVersion,
		}); err != nil {
			slog.Info("jellyfin: server-side logout failed; proceeding with local unlink", "err", err)
		}
	}
	_ = WipeToken(a.tokenPath())
	a.link.SetIdle()
	_ = a.Stop()
	return adapters.LinkSnapshot{Phase: adapters.LinkPhaseUnlinked, NeedsServerURL: a.configuredServerURL() == ""}, nil
}

// Compile-time conformance: *Adapter satisfies the link contract directly.
var _ adapters.LinkController = (*Adapter)(nil)
