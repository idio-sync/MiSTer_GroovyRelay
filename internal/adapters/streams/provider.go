package streams

import "time"

type Provider interface{}

type ProviderFactory func(ProviderDefinition) (Provider, error)

type Manifest struct {
	Version   int                  `json:"version"`
	Providers []ProviderDefinition `json:"providers"`
}

func (m Manifest) Provider(id string) (ProviderDefinition, bool) {
	for _, provider := range m.Providers {
		if provider.ID == id {
			return provider, true
		}
	}
	return ProviderDefinition{}, false
}

type ProviderDefinition struct {
	ID                  string              `json:"id"`
	Type                string              `json:"type"`
	DisplayName         string              `json:"display_name"`
	LogoURL             string              `json:"logo_url,omitempty"`
	LogoAlt             string              `json:"logo_alt,omitempty"`
	FallbackLabel       string              `json:"fallback_label,omitempty"`
	BadgeColor          string              `json:"badge_color,omitempty"`
	BaseURL             string              `json:"base_url"`
	PlaylistURL         string              `json:"playlist_url"`
	URLRules            []URLRule           `json:"url_rules"`
	DefaultChannel      string              `json:"default_channel"`
	DefaultPlayMode     PlayMode            `json:"default_play_mode"`
	CatalogRefreshHours *int                `json:"catalog_refresh_hours,omitempty"`
	Groups              []GroupDefinition   `json:"groups"`
	Channels            []ChannelDefinition `json:"channels"`
}

type URLRule struct {
	ID         string   `json:"id"`
	Schemes    []string `json:"schemes"`
	Hosts      []string `json:"hosts"`
	Path       string   `json:"path,omitempty"`
	PathPrefix string   `json:"path_prefix,omitempty"`
	Target     string   `json:"target"`
	QueryParam string   `json:"query_param,omitempty"`
}

type GroupDefinition struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Order int    `json:"order"`
}

type ChannelDefinition struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	GroupID     string   `json:"group_id,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	URL         string   `json:"url,omitempty"`
	PlayMode    PlayMode `json:"play_mode,omitempty"`
	Order       int      `json:"order"`
}

type ProviderCatalog struct {
	ProviderID string
	Name       string
	Groups     []ChannelGroup
	Channels   []Channel
	UpdatedAt  time.Time
}

func (c ProviderCatalog) Channel(id string) *Channel {
	for i := range c.Channels {
		if c.Channels[i].ID == id {
			return &c.Channels[i]
		}
	}
	return nil
}

type ChannelGroup struct {
	ID    string
	Name  string
	Order int
}

type Channel struct {
	ID          string
	Name        string
	Description string
	GroupID     string
	Icon        string
	Items       []StreamItem
	PlayMode    PlayMode
	Order       int
}

type StreamItem struct {
	ID       string
	Title    string
	URL      string
	SourceID string
	Direct   bool
}

type PlayMode string

const (
	PlaySequential       PlayMode = "sequential"
	PlayShuffle          PlayMode = "shuffle"
	PlayFirstThenShuffle PlayMode = "first_then_shuffle"
)

type CacheMetadata struct {
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	FetchedAt    time.Time `json:"fetched_at,omitempty"`
	SourceURL    string    `json:"source_url,omitempty"`
	Schema       int       `json:"schema"`
	SHA256       string    `json:"sha256"`
}
