package adapters

import "context"

// UserProviderEditor mutates user-authored catalog providers (spec §8).
// The chassis HTTP handler translates the authoring-form POST/PUT/DELETE
// into these calls. Like PresetEditor, the chassis consumes this interface
// only — the concrete implementation lives in the streams adapter and is
// injected by type assertion in main.go.
//
// Create/Update/Delete each persist through the user-provider store and
// rebuild the live catalog (bypassing the TOML ApplyScope path — spec §10),
// returning the saved provider in chassis-shaped form plus any preset slots
// that were cleared by removed channels. Reorder persists only the Order
// fields (no re-enumeration). Verify is a non-persisting dry-run.
type UserProviderEditor interface {
	CreateUserProvider(ctx context.Context, form UserProviderForm) (UserProviderResult, error)
	UpdateUserProvider(ctx context.Context, id string, form UserProviderForm) (UserProviderResult, error)
	DeleteUserProvider(ctx context.Context, id string) (UserProviderResult, error)
	ReorderUserProvider(ctx context.Context, id string, req ReorderRequest) error
	VerifyChannel(ctx context.Context, req VerifyChannelRequest) (VerifyChannelResult, error)
}

// UserProviderForm is the create/update payload from the authoring form.
// On create, ID is empty and the store assigns a locked "user:" slug. On
// update, ID is the locked provider ID; channel rows carry their locked ID
// (empty ID → new channel, server-assigned).
type UserProviderForm struct {
	ID          string
	DisplayName string
	BadgeLabel  string // glyph, 2-4 chars
	BadgeColor  string // palette token (amber|red|teal|blue|purple|green|cyan|slate)
	Groups      []UserGroupForm
	Channels    []UserChannelForm
}

type UserGroupForm struct {
	ID    string
	Name  string
	Order int
}

type UserChannelForm struct {
	ID       string // empty → server-assigned; locked thereafter
	Name     string
	URL      string
	Kind     string // "" → auto-detect; else playlist|single|direct
	PlayMode string // meaningful only for playlist
	GroupID  string
	Order    int
}

// UserProviderResult is the typed return from Create/Update/Delete.
type UserProviderResult struct {
	Provider         CatalogProvider // saved provider, chassis-shaped (zero on Delete)
	ClearedSlots     []int           // preset slots cleared by removed/deleted channels
	AutoEnableNeeded bool            // create-only: first user provider while Streams disabled
}

// ReorderRequest carries new display/sequential order for a provider's
// channels and groups. Touches only Order; never re-enumerates (spec §8).
type ReorderRequest struct {
	Channels []UserOrderEntry
	Groups   []UserOrderEntry
}

type UserOrderEntry struct {
	ID    string
	Order int
}

// VerifyChannelRequest is a dry-run probe of a single channel URL/kind.
type VerifyChannelRequest struct {
	URL  string
	Kind string // "" → auto-detect
}

// VerifyChannelResult is the advisory dry-run outcome (spec §8/§9 item 4-5).
// JSON tags match the chassis wire envelope.
type VerifyChannelResult struct {
	OK        bool   `json:"ok"`
	Kind      string `json:"kind"`
	ItemCount int    `json:"itemCount,omitempty"` // playlist entry count
	IsLive    bool   `json:"isLive,omitempty"`    // single: yt-dlp is_live (derived, never persisted)
	Message   string `json:"message,omitempty"`   // error/redacted reason when OK=false
}
