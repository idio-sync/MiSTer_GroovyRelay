package plex

import (
	"context"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestLinkSnapshot_Unlinked(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{}
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseUnlinked {
		t.Errorf("Phase = %q, want unlinked", got.Phase)
	}
}

func TestLinkSnapshot_Linked(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{AuthToken: "tok"}
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseLinked {
		t.Errorf("Phase = %q, want linked", got.Phase)
	}
	if got.LinkedAs != "" {
		t.Errorf("LinkedAs = %q, want empty (Plex stores no identity)", got.LinkedAs)
	}
}

func TestLinkSnapshot_Pending(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{}
	a.pending = newPendingLink("K3F9", 42, time.Now().Add(10*time.Minute))
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhasePending {
		t.Errorf("Phase = %q, want pending", got.Phase)
	}
	if got.Code != "K3F9" {
		t.Errorf("Code = %q, want K3F9", got.Code)
	}
	if got.ExpiresInSec <= 0 || got.ExpiresInSec > 600 {
		t.Errorf("ExpiresInSec = %d, want (0,600]", got.ExpiresInSec)
	}
}

func TestLinkSnapshot_Expired(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{}
	a.pending = newPendingLink("K3F9", 42, time.Now().Add(-time.Second))
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseError {
		t.Errorf("Phase = %q, want error", got.Phase)
	}
}

func TestLinkController_PollReadsState(t *testing.T) {
	a := &Adapter{}
	a.cfg.TokenStore = &StoredData{AuthToken: "tok"}
	got, err := a.PollLink(context.Background())
	if err != nil {
		t.Fatalf("PollLink err = %v", err)
	}
	if got.Phase != adapters.LinkPhaseLinked {
		t.Errorf("Phase = %q, want linked", got.Phase)
	}
}

func TestLinkController_Conformance(t *testing.T) {
	var _ adapters.LinkController = (*Adapter)(nil)
}
