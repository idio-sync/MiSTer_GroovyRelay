package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PlexAPIBase is the root URL for plex.tv account API calls. Tests override
// this to point at httptest.NewServer; production code leaves it at the
// plex.tv default. Declared as a package-level var (rather than a const) so
// unit tests never hit the real network.
var PlexAPIBase = "https://plex.tv"

// pollInterval is how long PollPIN waits between poll attempts. Exposed as
// a var for tests to shorten.
var pollInterval = 2 * time.Second

// registerInterval is the plex.tv device refresh cadence. plex.tv will mark
// the device offline if we stop registering for too long; 60s is well under
// the documented timeout. Exposed as a var so tests don't have to wait a
// full minute for the ticker to fire.
var registerInterval = 60 * time.Second

// PinResponse matches the JSON returned by the plex.tv PIN endpoints. When
// the PIN has not yet been claimed AuthToken is the empty string; once the
// user enters the PIN in plex.tv/link it fills in.
//
// See docs/references/plex-mpv-shim.md for the full flow.
type PinResponse struct {
	ID        int    `json:"id"`
	Code      string `json:"code"`
	AuthToken string `json:"authToken"`
}

// RequestPIN creates a new plex.tv PIN tied to clientID/deviceName. The
// returned PinResponse carries the 4-character Code the user types at
// plex.tv/link. AuthToken will be empty until the PIN is claimed.
func RequestPIN(clientID, deviceName string) (*PinResponse, error) {
	form := url.Values{}
	// Do NOT set strong=true. "Strong" PINs are ~25-char opaque tokens
	// meant for machine auth flows; plex.tv/link only accepts the short
	// 4-character human Code returned when strong is omitted/false.
	form.Set("X-Plex-Client-Identifier", clientID)
	form.Set("X-Plex-Device-Name", deviceName)
	form.Set("X-Plex-Product", "MiSTer_GroovyRelay")
	form.Set("X-Plex-Version", "1.0")
	// X-Plex-Provides=player is baked into the plex.tv device record at link
	// time. Without it the device is registered but not classified as a
	// player, so controllers refuse to cast media to it.
	form.Set("X-Plex-Provides", "player")

	req, err := http.NewRequest(http.MethodPost, PlexAPIBase+"/api/v2/pins", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := plexHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("pin request failed: %d", resp.StatusCode)
	}
	var pr PinResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// PollPIN repeatedly GETs the PIN by ID until it either has an AuthToken
// (the user completed the link) or the timeout elapses. Transient transport
// errors are swallowed and retried; the poll cadence is controlled by the
// package-level pollInterval var.
func PollPIN(id int, clientID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet,
			fmt.Sprintf("%s/api/v2/pins/%d?X-Plex-Client-Identifier=%s", PlexAPIBase, id, clientID),
			nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := plexHTTPClient.Do(req)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}
		var pr PinResponse
		_ = json.NewDecoder(resp.Body).Decode(&pr)
		resp.Body.Close()
		if pr.AuthToken != "" {
			return pr.AuthToken, nil
		}
		time.Sleep(pollInterval)
	}
	return "", fmt.Errorf("pin expired without auth token")
}

// RegisterDevice PUTs the bridge's LAN URI to plex.tv/devices/{uuid}. This
// is how the device shows up in the Plex mobile/web cast picker when the
// controller is on cellular data (outside the LAN). Requires a valid auth
// token; a one-shot call, intended to be driven by RunRegistrationLoop.
func RegisterDevice(uuid, token, hostIP string, httpPort int) error {
	form := url.Values{}
	form.Set("Connection[][uri]", fmt.Sprintf("http://%s:%d", hostIP, httpPort))
	// Re-assert provides=player on every refresh so the device record stays
	// classified as a player even if a prior link created it without the flag.
	form.Set("X-Plex-Provides", "player")
	form.Set("X-Plex-Product", "MiSTer_GroovyRelay")
	form.Set("X-Plex-Version", "1.0")
	req, err := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/devices/%s?X-Plex-Token=%s", PlexAPIBase, uuid, token),
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := plexHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("plex.tv register: %s", resp.Status)
	}
	return nil
}

// RunRegistrationLoop performs an immediate RegisterDevice and then keeps it
// refreshed on the registerInterval cadence until ctx is cancelled. Errors
// from the periodic refresh are logged at WARN but do not stop the loop —
// transient plex.tv hiccups should self-heal on the next tick.
func RunRegistrationLoop(ctx context.Context, uuid, token, hostIP string, httpPort int) {
	tick := time.NewTicker(registerInterval)
	defer tick.Stop()
	if err := RegisterDevice(uuid, token, hostIP, httpPort); err != nil {
		slog.Warn("plex.tv register failed", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := RegisterDevice(uuid, token, hostIP, httpPort); err != nil {
				slog.Warn("plex.tv register failed", "err", err)
			}
		}
	}
}
