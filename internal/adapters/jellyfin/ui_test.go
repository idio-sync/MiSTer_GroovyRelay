package jellyfin

import "testing"

func TestLinkSummary_ReflectsState(t *testing.T) {
	a := New(nil, t.TempDir(), "dev-1")
	got := a.LinkSummary()
	if got.Phase != "idle" {
		t.Errorf("Phase = %q, want idle", got.Phase)
	}

	a.link.SetLinked("alice", "sid-1")
	got = a.LinkSummary()
	if got.Phase != "linked" || got.UserName != "alice" || got.ServerID != "sid-1" {
		t.Errorf("LinkSummary after Linked = %+v", got)
	}

	a.link.SetError("boom")
	got = a.LinkSummary()
	if got.Phase != "error" || got.LastError != "boom" {
		t.Errorf("LinkSummary after Error = %+v", got)
	}
}
