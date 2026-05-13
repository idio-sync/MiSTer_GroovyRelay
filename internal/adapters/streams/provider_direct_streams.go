package streams

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

var toonamiAftermathPaths = map[string]struct{}{
	"/est/playlist.m3u8":    {},
	"/pst/playlist.m3u8":    {},
	"/movies/playlist.m3u8": {},
	"/radio/playlist.m3u8":  {},
}

var directStreamURLValidators = map[string]func(*url.URL) error{
	"toonami-aftermath": validateToonamiAftermathURL,
}

func buildDirectStreamsCatalog(def ProviderDefinition) (ProviderCatalog, error) {
	if def.Type != directStreamsProviderType {
		return ProviderCatalog{}, fmt.Errorf("provider %q type %q is unsupported", def.ID, def.Type)
	}
	groupByID := make(map[string]ChannelGroup, len(def.Groups))
	groups := make([]ChannelGroup, 0, len(def.Groups))
	for _, group := range def.Groups {
		g := ChannelGroup{ID: group.ID, Name: group.Name, Order: group.Order}
		groupByID[group.ID] = g
		groups = append(groups, g)
	}
	channels := make([]Channel, 0, len(def.Channels))
	for _, chDef := range def.Channels {
		if err := validateDirectStreamChannelURL(def.ID, chDef.URL); err != nil {
			return ProviderCatalog{}, fmt.Errorf("provider %q channel %q url: %w", def.ID, chDef.ID, err)
		}
		ch := channelFromDefinition(chDef.ID, chDef, true, def)
		ch.Items = []StreamItem{{
			ID:       ch.ID,
			Title:    ch.Name,
			URL:      chDef.URL,
			SourceID: ch.ID,
			Direct:   true,
		}}
		if ch.GroupID != "" {
			if _, ok := groupByID[ch.GroupID]; !ok {
				return ProviderCatalog{}, fmt.Errorf("provider %q channel %q references unknown group %q", def.ID, ch.ID, ch.GroupID)
			}
		}
		channels = append(channels, ch)
	}
	sortChannelGroups(groups)
	sortChannels(channels)
	return ProviderCatalog{
		ProviderID: def.ID,
		Name:       def.DisplayName,
		Groups:     groups,
		Channels:   channels,
		UpdatedAt:  time.Now(),
	}, nil
}

func validateDirectStreamChannelURL(providerID, raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("is required")
	}
	if trimmed != raw {
		return fmt.Errorf("surrounding whitespace is not allowed")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.User != nil {
		return fmt.Errorf("userinfo is not allowed")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("host is required")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("scheme %q is not allowed", scheme)
	}
	validate, ok := directStreamURLValidators[providerID]
	if !ok {
		return fmt.Errorf("provider %q has no direct-stream URL validator", providerID)
	}
	return validate(u)
}

func validateToonamiAftermathURL(u *url.URL) error {
	host := strings.ToLower(u.Host)
	if host != "api.toonamiaftermath.com:3000" {
		return fmt.Errorf("host %q is not allowed", host)
	}
	if _, ok := toonamiAftermathPaths[u.EscapedPath()]; !ok {
		return fmt.Errorf("path %q is not allowed", u.EscapedPath())
	}
	if u.RawQuery != "" {
		return fmt.Errorf("query string is not allowed")
	}
	if u.Fragment != "" {
		return fmt.Errorf("fragment is not allowed")
	}
	return nil
}
