package streams

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"
)

var youtubeIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

const ungroupedGroupID = "ungrouped"

const (
	maxCatalogChannels = maxManifestChannels
	maxCatalogItems    = 250_000
)

func buildYouTubeChannelCatalog(def ProviderDefinition, raw []byte, cfg Config) (ProviderCatalog, error) {
	var playlists map[string][]string
	if err := json.Unmarshal(raw, &playlists); err != nil {
		return ProviderCatalog{}, fmt.Errorf("parse playlist json: %w", err)
	}
	if playlists == nil {
		return ProviderCatalog{}, fmt.Errorf("playlist JSON must be an object")
	}
	if len(playlists) > maxCatalogChannels {
		return ProviderCatalog{}, fmt.Errorf("playlist JSON has %d channels, max %d", len(playlists), maxCatalogChannels)
	}

	groupByID := make(map[string]ChannelGroup, len(def.Groups)+1)
	for _, group := range def.Groups {
		groupByID[group.ID] = ChannelGroup{
			ID:    group.ID,
			Name:  group.Name,
			Order: group.Order,
		}
	}

	defByID := make(map[string]ChannelDefinition, len(def.Channels))
	for _, channel := range def.Channels {
		defByID[channel.ID] = channel
	}

	channels := make([]Channel, 0, len(playlists))
	totalItems := 0
	for id, ids := range playlists {
		if isExcludedPlaylist(def, id) {
			continue
		}
		channelDef, known := defByID[id]
		if !known && !validUnknownPlaylistID(id) {
			continue
		}
		channel := channelFromDefinition(id, channelDef, known, def)
		if !known {
			groupByID[ungroupedGroupID] = ChannelGroup{
				ID:    ungroupedGroupID,
				Name:  "Ungrouped",
				Order: 1_000_000,
			}
		}

		for _, sourceID := range ids {
			if !youtubeIDRE.MatchString(sourceID) {
				continue
			}
			if cfg.MaxItemsPerChannel > 0 && len(channel.Items) >= cfg.MaxItemsPerChannel {
				break
			}
			if totalItems >= maxCatalogItems {
				return ProviderCatalog{}, fmt.Errorf("playlist JSON has more than %d accepted items", maxCatalogItems)
			}
			channel.Items = append(channel.Items, StreamItem{
				ID:       sourceID,
				URL:      "https://www.youtube.com/watch?v=" + sourceID,
				SourceID: sourceID,
			})
			totalItems++
		}
		channels = append(channels, channel)
	}

	groups := make([]ChannelGroup, 0, len(groupByID))
	for _, group := range groupByID {
		groups = append(groups, group)
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

func channelFromDefinition(id string, def ChannelDefinition, known bool, provider ProviderDefinition) Channel {
	if !known {
		return Channel{
			ID:       id,
			Name:     id,
			GroupID:  ungroupedGroupID,
			PlayMode: PlayShuffle,
		}
	}

	playMode := def.PlayMode
	if playMode == "" {
		playMode = provider.DefaultPlayMode
	}
	if playMode == "" {
		playMode = PlayShuffle
	}

	return Channel{
		ID:          def.ID,
		Name:        def.Name,
		Description: def.Description,
		GroupID:     def.GroupID,
		Icon:        def.Icon,
		PlayMode:    playMode,
		Order:       def.Order,
	}
}

func isExcludedPlaylist(def ProviderDefinition, id string) bool {
	return def.ID == "cartoon-rewind" && id == "commercials"
}

func validUnknownPlaylistID(id string) bool {
	return id != reservedAdhocID && manifestIDPattern.MatchString(id)
}

func sortChannelGroups(groups []ChannelGroup) {
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Order != groups[j].Order {
			return groups[i].Order < groups[j].Order
		}
		if groups[i].Name != groups[j].Name {
			return groups[i].Name < groups[j].Name
		}
		return groups[i].ID < groups[j].ID
	})
}

func sortChannels(channels []Channel) {
	sort.SliceStable(channels, func(i, j int) bool {
		if channels[i].Order != channels[j].Order {
			return channels[i].Order < channels[j].Order
		}
		if channels[i].Name != channels[j].Name {
			return channels[i].Name < channels[j].Name
		}
		return channels[i].ID < channels[j].ID
	})
}
