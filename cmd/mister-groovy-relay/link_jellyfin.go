package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/jellyfin"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/plex"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"golang.org/x/term"
)

// runLinkJellyfin runs the headless --link-jellyfin flow: prompts on
// stdin for server URL / username / password, calls
// jellyfin.AuthenticateByName, persists the token via SaveToken, and
// prints "Linked as <user> on <server>." on success.
//
// The device UUID is read from the existing plex.LoadStoredData
// (the bridge persists DeviceUUID under the Plex tokenstore by
// historical accident; the JF adapter reuses the same value — one
// MiSTer = one device across protocols).
func runLinkJellyfin(sec *config.Sectioned) error {
	rd := bufio.NewReader(os.Stdin)

	fmt.Print("Jellyfin server URL: ")
	serverURL, err := rd.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read server_url: %w", err)
	}
	serverURL = strings.TrimSpace(serverURL)

	fmt.Print("Jellyfin username: ")
	user, err := rd.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read username: %w", err)
	}
	user = strings.TrimSpace(user)

	fmt.Print("Jellyfin password (input hidden): ")
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		// Fall back to plain ReadString if not a TTY (e.g. CI).
		pwLine, err2 := rd.ReadString('\n')
		if err2 != nil {
			return fmt.Errorf("read password: %w / %w", err, err2)
		}
		pw = []byte(strings.TrimSpace(pwLine))
	}
	fmt.Println()

	store, err := plex.LoadStoredData(sec.Bridge.DataDir)
	if err != nil || store.DeviceUUID == "" {
		return fmt.Errorf("device uuid: run --link (Plex) or start the bridge once to mint device_uuid")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := jellyfin.AuthenticateByName(ctx, jellyfin.AuthRequest{
		ServerURL: serverURL,
		Username:  user,
		Password:  string(pw),
		DeviceID:  store.DeviceUUID,
		Version:   version, // main.go's package-level global
	})
	if err != nil {
		return err
	}

	tokPath := jellyfin.TokenPathFor(sec.Bridge.DataDir)
	if err := jellyfin.SaveToken(tokPath, jellyfin.Token{
		AccessToken: res.AccessToken,
		UserID:      res.UserID,
		UserName:    res.UserName,
		ServerID:    res.ServerID,
		ServerURL:   serverURL,
	}); err != nil {
		return err
	}

	fmt.Printf("Linked as %s on %s.\n", res.UserName, res.ServerID)
	return nil
}
