package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// SupportedCommands is the canonical list of JF commands the bridge
// claims to honor. JF clients filter the cast-target menu by
// intersecting their required commands against this list. Spec
// §"Capabilities POST" — lock this list down; omitting common
// commands risks invisibility on certain clients.
var SupportedCommands = []string{
	"Play", "PlayState", "PlayNext", "PlayMediaSource",
	"VolumeUp", "VolumeDown", "Mute", "Unmute", "ToggleMute", "SetVolume",
	"SetAudioStreamIndex", "SetSubtitleStreamIndex",
	"SetMaxStreamingBitrate", "SetRepeatMode", "DisplayMessage",
}

// CapabilitiesInput carries everything PostCapabilities needs.
type CapabilitiesInput struct {
	ServerURL           string
	Token               string
	DeviceID            string
	DeviceName          string
	Version             string
	MaxVideoBitrateKbps int
}

// capabilitiesBody is the wire-format struct for /Sessions/Capabilities/Full.
type capabilitiesBody struct {
	PlayableMediaTypes           []string      `json:"PlayableMediaTypes"`
	SupportedCommands            []string      `json:"SupportedCommands"`
	SupportsMediaControl         bool          `json:"SupportsMediaControl"`
	SupportsPersistentIdentifier bool          `json:"SupportsPersistentIdentifier"`
	DeviceProfile                DeviceProfile `json:"DeviceProfile"`
	IconUrl                      string        `json:"IconUrl"`
}

// PostCapabilities advertises the bridge as a JF cast target.
// Returns nil on 2xx, an error otherwise. Idempotent — JF treats
// repeated POSTs from the same DeviceId as updates, not duplicates.
func PostCapabilities(ctx context.Context, in CapabilitiesInput) error {
	body := capabilitiesBody{
		PlayableMediaTypes:           []string{"Video", "Audio"},
		SupportedCommands:            SupportedCommands,
		SupportsMediaControl:         true,
		SupportsPersistentIdentifier: true,
		DeviceProfile:                BuildDeviceProfile(in.MaxVideoBitrateKbps),
		IconUrl:                      iconDataURL,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("jellyfin: marshal capabilities body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(in.ServerURL, "/")+"/Sessions/Capabilities/Full",
		bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("jellyfin: build capabilities request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", BuildAuthHeader(AuthHeaderInput{
		Token:    in.Token,
		Client:   jfClientName,
		Device:   effectiveDeviceName(in.DeviceName),
		DeviceID: in.DeviceID,
		Version:  in.Version,
	}))
	resp, err := jfHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin: capabilities POST: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if msg := strings.TrimSpace(string(body)); msg != "" {
			return fmt.Errorf("jellyfin: capabilities POST: HTTP %d: %s", resp.StatusCode, msg)
		}
		return fmt.Errorf("jellyfin: capabilities POST: HTTP %d", resp.StatusCode)
	}
	return nil
}
