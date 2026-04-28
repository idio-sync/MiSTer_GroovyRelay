package jellyfin

import (
	"strings"
	"testing"
)

// TestExtraPanelHTML_WrapsLinkFragmentInTarget covers the UI
// integration gap that left the link form unreachable in the browser:
// without a #jf-link wrapper rendered server-side, htmx can't swap
// form responses into the panel. ExtraPanelHTML must include the
// wrapper id so swaps land. Verifies all three link states render
// inside the wrapper and that swap responses (renderLinkFragment)
// do NOT include the wrapper themselves (so innerHTML swaps don't
// nest wrappers).
func TestExtraPanelHTML_WrapsLinkFragmentInTarget(t *testing.T) {
	a := New(nil, t.TempDir(), "dev-1")
	// Idle state with a configured Server URL renders the link form.
	a.cfg.ServerURL = "https://jf.example.com"

	got := string(a.ExtraPanelHTML())
	if !strings.HasPrefix(got, `<div id="jf-link">`) {
		t.Errorf("ExtraPanelHTML missing wrapper prefix; got: %s", got)
	}
	if !strings.HasSuffix(got, `</div>`) {
		t.Errorf("ExtraPanelHTML missing closing tag; got: %s", got)
	}
	if !strings.Contains(got, `hx-post="/ui/adapter/jellyfin/link/start"`) {
		t.Errorf("idle ExtraPanelHTML should embed link form; got: %s", got)
	}
	if strings.Contains(got, `name="server_url"`) {
		t.Errorf("link form must NOT carry a server_url input — URL comes from cfg; got: %s", got)
	}

	a.link.SetLinked("alice", "sid-1")
	got = string(a.ExtraPanelHTML())
	if !strings.Contains(got, "Linked as alice on sid-1") {
		t.Errorf("linked ExtraPanelHTML missing identity; got: %s", got)
	}
	if !strings.Contains(got, `hx-post="/ui/adapter/jellyfin/unlink"`) {
		t.Errorf("linked ExtraPanelHTML missing unlink button; got: %s", got)
	}

	// Swap responses must NOT include the #jf-link wrapper, otherwise
	// htmx innerHTML swaps would nest wrappers.
	frag := a.linkFragmentHTML("")
	if strings.Contains(frag, `id="jf-link"`) {
		t.Errorf("linkFragmentHTML must not include wrapper id; got: %s", frag)
	}
}

// TestExtraPanelHTML_IdleWithoutServerURL_PromptsToSaveSettings covers
// the empty-config path: when no Server URL has been saved yet, the
// link form is suppressed and the operator is told to save the URL in
// the Settings section first.
func TestExtraPanelHTML_IdleWithoutServerURL_PromptsToSaveSettings(t *testing.T) {
	a := New(nil, t.TempDir(), "dev-1")
	got := string(a.ExtraPanelHTML())
	if strings.Contains(got, `hx-post="/ui/adapter/jellyfin/link/start"`) {
		t.Errorf("link form should be suppressed without a saved Server URL; got: %s", got)
	}
	if !strings.Contains(got, "Server URL") {
		t.Errorf("idle-without-URL state should prompt operator about Server URL; got: %s", got)
	}
}

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
