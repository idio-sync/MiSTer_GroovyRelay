package adapters

import (
	"context"
	"testing"
)

// compile-time proof the result/payload types and the interface are usable.
func TestUserProviderEditor_TypeShape(t *testing.T) {
	t.Parallel()
	form := UserProviderForm{
		DisplayName: "F1 TV",
		BadgeLabel:  "F1",
		BadgeColor:  "amber",
		Groups:      []UserGroupForm{{ID: "g1", Name: "Races", Order: 0}},
		Channels: []UserChannelForm{
			{Name: "Live", URL: "https://cdn.example.com/live.m3u8", Kind: "direct", GroupID: "g1", Order: 0},
		},
	}
	if form.Channels[0].Kind != "direct" {
		t.Fatal("channel kind not set")
	}
	res := UserProviderResult{
		Provider:         CatalogProvider{ID: "user:f1-tv"},
		ClearedSlots:     []int{3, 7},
		AutoEnableNeeded: true,
	}
	if res.Provider.ID != "user:f1-tv" || len(res.ClearedSlots) != 2 || !res.AutoEnableNeeded {
		t.Fatal("result shape wrong")
	}
	vr := VerifyChannelResult{OK: true, Kind: "playlist", ItemCount: 47}
	if !vr.OK || vr.ItemCount != 47 {
		t.Fatal("verify result shape wrong")
	}
	// interface assignability via a local stub (never called).
	var _ UserProviderEditor = stubEditor{}
	_ = context.Background()
}

type stubEditor struct{}

func (stubEditor) CreateUserProvider(context.Context, UserProviderForm) (UserProviderResult, error) {
	return UserProviderResult{}, nil
}
func (stubEditor) UpdateUserProvider(context.Context, string, UserProviderForm) (UserProviderResult, error) {
	return UserProviderResult{}, nil
}
func (stubEditor) DeleteUserProvider(context.Context, string) (UserProviderResult, error) {
	return UserProviderResult{}, nil
}
func (stubEditor) ReorderUserProvider(context.Context, string, ReorderRequest) error { return nil }
func (stubEditor) VerifyChannel(context.Context, VerifyChannelRequest) (VerifyChannelResult, error) {
	return VerifyChannelResult{}, nil
}

func TestUserProviderViewer_TypeShape(t *testing.T) {
	t.Parallel()
	var _ UserProviderViewer = stubViewer{}
	st := UserProviderStatus{ProviderID: "user:mix", Channels: []UserChannelStatus{{ChannelID: "a", State: "ready", ItemCount: 3}}}
	if st.Channels[0].State != "ready" || st.Channels[0].ItemCount != 3 {
		t.Fatal("status shape wrong")
	}
}

type stubViewer struct{}

func (stubViewer) UserProviderForm(string) (UserProviderForm, bool) { return UserProviderForm{}, false }
func (stubViewer) UserProviderStatuses() []UserProviderStatus       { return nil }
