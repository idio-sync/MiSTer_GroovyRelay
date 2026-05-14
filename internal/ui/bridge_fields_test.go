package ui

import (
	"reflect"
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
	// appear in registration order. Confirm that order survives.
	wantPrefix := []string{"Network", "Video", "Audio", "Visualizer", "Server", "External Tools", "MiSTer Control"}
	if len(got) < len(wantPrefix) {
		t.Fatalf("got %d sections, want at least %d", len(got), len(wantPrefix))
	}
	for i, name := range wantPrefix {
		if got[i].Name != name {
			t.Errorf("section[%d]: got %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestBridgeFields_HasExternalToolsSection(t *testing.T) {
	fields := bridgeFields()
	want := map[string]bool{
		"ffmpeg_path":  false,
		"ffprobe_path": false,
		"ytdlp_path":   false,
	}
	for _, f := range fields {
		if _, ok := want[f.Key]; !ok {
			continue
		}
		if f.Section != "External Tools" {
			t.Errorf("%s section = %q, want External Tools", f.Key, f.Section)
		}
		if f.Kind != adapters.KindText {
			t.Errorf("%s kind = %v, want KindText", f.Key, f.Kind)
		}
		if f.ApplyScope != adapters.ScopeHotSwap {
			t.Errorf("%s scope = %v, want ScopeHotSwap", f.Key, f.ApplyScope)
		}
		want[f.Key] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("%s not found in bridgeFields()", k)
		}
	}
}

func TestBridgeFields_HasDeltaLZ4Toggle(t *testing.T) {
	fields := bridgeFields()
	var found *adapters.FieldDef
	for i := range fields {
		if fields[i].Key == "video.delta_lz4_enabled" {
			found = &fields[i]
			break
		}
	}
	if found == nil {
		t.Fatal("video.delta_lz4_enabled not found in bridgeFields()")
	}
	if found.Label != "Delta-LZ4" {
		t.Errorf("label = %q, want Delta-LZ4", found.Label)
	}
	if found.Section != "Video" {
		t.Errorf("section = %q, want Video", found.Section)
	}
	if found.Kind != adapters.KindBool {
		t.Errorf("kind = %v, want KindBool", found.Kind)
	}
	if found.Default != true {
		t.Errorf("default = %v, want true", found.Default)
	}
	if found.ApplyScope != adapters.ScopeRestartCast {
		t.Errorf("scope = %v, want ScopeRestartCast", found.ApplyScope)
	}
}

func TestBridgeFields_HasVisualizerMode(t *testing.T) {
	fields := bridgeFields()
	var found *adapters.FieldDef
	for i := range fields {
		if fields[i].Key == "visualizer.mode" {
			found = &fields[i]
			break
		}
	}
	if found == nil {
		t.Fatal("visualizer.mode not found in bridgeFields()")
	}
	if found.Label != "Mode" {
		t.Errorf("label = %q, want Mode", found.Label)
	}
	if found.Help != "Music visualizer mode for the next music cast." {
		t.Errorf("help = %q", found.Help)
	}
	if found.Section != "Visualizer" {
		t.Errorf("section = %q, want Visualizer", found.Section)
	}
	if found.Kind != adapters.KindEnum {
		t.Errorf("kind = %v, want KindEnum", found.Kind)
	}
	if !reflect.DeepEqual(found.Enum, config.SupportedVisualizerModes()) {
		t.Errorf("enum = %v, want %v", found.Enum, config.SupportedVisualizerModes())
	}
	if found.Default != config.VisualizerModeRetroAnalyzer {
		t.Errorf("default = %v, want %q", found.Default, config.VisualizerModeRetroAnalyzer)
	}
	if found.ApplyScope != adapters.ScopeNextCast {
		t.Errorf("scope = %v, want ScopeNextCast", found.ApplyScope)
	}
}

func TestBuildBridgeSections_NormalizesEmptyVisualizerMode(t *testing.T) {
	sections := buildBridgeSections(config.BridgeConfig{}, nil)
	for _, sec := range sections {
		for _, row := range sec.Rows {
			if row.Key != "visualizer.mode" {
				continue
			}
			if row.StringValue != config.VisualizerModeRetroAnalyzer {
				t.Fatalf("visualizer.mode StringValue = %q, want %q", row.StringValue, config.VisualizerModeRetroAnalyzer)
			}
			return
		}
	}
	t.Fatal("visualizer.mode row not found")
}

func TestRowFor_KindAction(t *testing.T) {
	fd := adapters.FieldDef{
		Key:     "mister/launch",
		Label:   "Launch GroovyMiSTer",
		Kind:    adapters.KindAction,
		Section: "Launch",
	}
	cur := config.BridgeConfig{}
	r := rowFor(fd, cur, nil)
	if r.Kind != "action" {
		t.Errorf("Kind: got %q, want %q", r.Kind, "action")
	}
	if r.Label != "Launch GroovyMiSTer" {
		t.Errorf("Label: got %q, want %q", r.Label, "Launch GroovyMiSTer")
	}
	// Action rows do not carry input values.
	if r.StringValue != "" {
		t.Errorf("StringValue: got %q, want empty", r.StringValue)
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

func TestBridgeFields_LaunchIsKindAction(t *testing.T) {
	var found *adapters.FieldDef
	for i := range bridgeFields() {
		fd := bridgeFields()[i]
		if fd.Section == "Launch" {
			found = &fd
			break
		}
	}
	if found == nil {
		t.Fatal("no field with Section=Launch found")
	}
	if found.Kind != adapters.KindAction {
		t.Errorf("Launch field Kind: got %v, want KindAction", found.Kind)
	}
	if found.SectionOrder != 60 {
		t.Errorf("Launch field SectionOrder: got %d, want 60", found.SectionOrder)
	}
	if found.Key != "mister/launch" {
		t.Errorf("Launch field Key: got %q, want %q", found.Key, "mister/launch")
	}
}

func TestBuildBridgeSections_LaunchAppearsLast(t *testing.T) {
	got := buildBridgeSections(config.BridgeConfig{}, nil)
	if len(got) == 0 {
		t.Fatal("no sections")
	}
	last := got[len(got)-1]
	if last.Name != "Launch" {
		t.Errorf("last section: got %q, want %q", last.Name, "Launch")
	}
}
