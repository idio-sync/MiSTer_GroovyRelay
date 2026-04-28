package ui

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// TestBridgeFields_HasMisterControlSection verifies the SSH user and
// password fields are present under the new "MiSTer Control" section
// with the right kinds and apply-scope.
func TestBridgeFields_HasMisterControlSection(t *testing.T) {
	fields := bridgeFields()
	var user, pass *adapters.FieldDef
	for i, f := range fields {
		f := f
		switch f.Key {
		case "mister.ssh_user":
			user = &fields[i]
		case "mister.ssh_password":
			pass = &fields[i]
		}
	}
	if user == nil {
		t.Fatal("mister.ssh_user not found in bridgeFields()")
	}
	if pass == nil {
		t.Fatal("mister.ssh_password not found in bridgeFields()")
	}
	if user.Section != "MiSTer Control" {
		t.Errorf("ssh_user section = %q, want MiSTer Control", user.Section)
	}
	if pass.Section != "MiSTer Control" {
		t.Errorf("ssh_password section = %q, want MiSTer Control", pass.Section)
	}
	if user.Kind != adapters.KindText {
		t.Errorf("ssh_user kind = %v, want KindText", user.Kind)
	}
	if pass.Kind != adapters.KindSecret {
		t.Errorf("ssh_password kind = %v, want KindSecret", pass.Kind)
	}
	if user.ApplyScope != adapters.ScopeHotSwap {
		t.Errorf("ssh_user scope = %v, want ScopeHotSwap", user.ApplyScope)
	}
	if pass.ApplyScope != adapters.ScopeHotSwap {
		t.Errorf("ssh_password scope = %v, want ScopeHotSwap", pass.ApplyScope)
	}
}

func TestBuildBridgeSections_OrdersBySectionOrder(t *testing.T) {
	cur := config.BridgeConfig{}
	got := buildBridgeSections(cur, nil)

	// Pre-existing bridge fields all have SectionOrder=0, so they
	// appear in registration order: Network, Video, Audio, Server,
	// MiSTer Control. Confirm that order survives.
	wantPrefix := []string{"Network", "Video", "Audio", "Server", "MiSTer Control"}
	if len(got) < len(wantPrefix) {
		t.Fatalf("got %d sections, want at least %d", len(got), len(wantPrefix))
	}
	for i, name := range wantPrefix {
		if got[i].Name != name {
			t.Errorf("section[%d]: got %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestModelineEnumOptions_ExperimentalSuffix(t *testing.T) {
	got := modelineEnumOptions()
	want := []string{
		"NTSC_480i",
		"NTSC_240p",
		"PAL_576i (experimental)",
		"PAL_288p (experimental)",
	}
	if len(got) != len(want) {
		t.Fatalf("modelineEnumOptions() returned %d items, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("modelineEnumOptions()[%d] = %q, want %q", i, got[i], w)
		}
	}
}
