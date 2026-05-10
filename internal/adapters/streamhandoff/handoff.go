package streamhandoff

import "context"

type Resolver interface {
	ResolveStreamURL(ctx context.Context, rawURL string) (Resolution, bool, error)
	StartResolvedStream(ctx context.Context, res Resolution) (StartResult, error)
}

type Resolution struct {
	AdapterRef string
	ProviderID string
	ChannelID  string
	ItemID     string
}

type StartResult struct {
	AdapterRef string `json:"adapter_ref"`
	ProviderID string `json:"provider_id"`
	ChannelID  string `json:"channel_id,omitempty"`
	ItemID     string `json:"item_id,omitempty"`
}
