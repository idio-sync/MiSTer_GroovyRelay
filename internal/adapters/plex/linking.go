package plex

import (
	"encoding/json"
	"fmt"
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
	form.Set("strong", "true")
	form.Set("X-Plex-Client-Identifier", clientID)
	form.Set("X-Plex-Device-Name", deviceName)
	form.Set("X-Plex-Product", "MiSTer_GroovyRelay")
	form.Set("X-Plex-Version", "1.0")

	req, err := http.NewRequest(http.MethodPost, PlexAPIBase+"/api/v2/pins", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
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
		resp, err := http.DefaultClient.Do(req)
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
