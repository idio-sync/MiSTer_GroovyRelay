package chassis

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
)

func TestChassisCSS_AllSelectorsScoped(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	leaks := findUnscopedSelectors(src)
	if len(leaks) > 0 {
		t.Errorf("found %d unscoped selectors in chassis.css:", len(leaks))
		for _, leak := range leaks {
			t.Errorf("  %s", leak)
		}
	}
}

func TestChassisCSS_LeakProneSelectorsAreScoped(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}

	lines := strings.Split(string(src), "\n")
	bannedSubstrings := []string{
		"body.idle ",
		"body.live ",
		"body:not(.idle)",
		"body.settings-open",
		"body.browse-open",
		"body.catalog-scanning",
		"body[data-event-filter",
	}
	for _, banned := range bannedSubstrings {
		scopedForm := strings.Replace(banned, "body", "body.receiver", 1)
		for i, line := range lines {
			if strings.Contains(line, banned) && !strings.Contains(line, scopedForm) {
				t.Errorf("line %d: leak-prone selector %q without body.receiver prefix: %s", i+1, banned, line)
			}
		}
	}
}

func TestChassisCSS_RulesetCountSanity(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	count := countRulesets(src)
	// The original full-mockup plan suggested 600+ rulesets, but this
	// branch is a staged Phase 0 port. The current parser count is 476
	// scoped non-keyframe rulesets, so 450 leaves room for small cleanup
	// while still catching accidental truncation or a dropped CSS section.
	const minRulesets = 485
	if count < minRulesets {
		t.Errorf("chassis.css has %d rulesets, want at least %d for the staged Phase 0 port", count, minRulesets)
	}
}

func TestChassisCSS_PresetLivePulseHonorsReducedMotionCascade(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)
	baseRule := "body.receiver .preset.live::after"
	baseIdx := strings.Index(text, baseRule)
	if baseIdx == -1 {
		t.Fatalf("chassis.css missing %q", baseRule)
	}
	baseAnimationIdx := strings.Index(text[baseIdx:], "animation: rec-pulse")
	if baseAnimationIdx == -1 {
		t.Fatalf("chassis.css missing rec-pulse animation in %q rule", baseRule)
	}
	afterPulseRule := text[baseIdx+baseAnimationIdx:]
	reduceIdx := strings.Index(afterPulseRule, "@media (prefers-reduced-motion: reduce)")
	if reduceIdx == -1 {
		t.Fatalf("chassis.css must place a reduced-motion override after %q so it wins the cascade", baseRule)
	}
	overrideIdx := strings.Index(afterPulseRule[reduceIdx:], baseRule)
	if overrideIdx == -1 {
		t.Fatalf("reduced-motion block after %q must override the preset live pulse", baseRule)
	}
	overrideSnippet := afterPulseRule[reduceIdx+overrideIdx:]
	if !strings.Contains(overrideSnippet, "animation: none;") {
		t.Fatalf("reduced-motion override for %q must set animation: none", baseRule)
	}
}

func TestChassisCSS_PresetButtonsUseSharedPressAnimation(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)
	activeRule := cssRuleBlock(t, text, "body.receiver .preset:active")
	if !strings.Contains(activeRule, "transform: translateY(1px);") {
		t.Fatalf("preset active rule must match the setup/load-core press travel: %s", activeRule)
	}
	pressedRule := cssRuleBlock(t, text, "body.receiver .preset.pressed")
	if !strings.Contains(pressedRule, "transform: translateY(1px);") {
		t.Fatalf("preset pressed rule must match the setup/load-core press travel: %s", pressedRule)
	}
	lastPresetIdx := strings.LastIndex(text, "body.receiver .preset {")
	if lastPresetIdx == -1 {
		t.Fatalf("chassis.css missing preset button rule")
	}
	lastPresetRule := cssRuleBlock(t, text[lastPresetIdx:], "body.receiver .preset")
	if !strings.Contains(lastPresetRule, "transform 80ms") {
		t.Fatalf("last preset rule must preserve the shared 80ms transform press transition: %s", lastPresetRule)
	}
}

func TestChassisCSS_HistoryEventLogSettingsContracts(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)
	for _, want := range []string{
		"/* ---- 12. History + event log ---- */",
		"/* ---- 13. Settings drawer ---- */",
		"body.receiver .history-section",
		"body.receiver .history-row",
		"body.receiver .history-row .artwork",
		"body.receiver .history-row .title",
		"body.receiver .history-row .source",
		"body.receiver .history-row .when",
		"body.receiver .history-empty",
		"body.receiver .event-log-section",
		"body.receiver .event-log-row",
		"body.receiver .event-log-severity",
		"body.receiver .event-log-filter",
		"body.receiver[data-event-filter=\"info\"] .event-log-row:not(.info)",
		"body.receiver .settings-panel",
		"body.receiver .settings-panel.open",
		"body.receiver.settings-open .settings-panel",
		"body.receiver .settings-tabs",
		"body.receiver .settings-body",
		"body.receiver .settings-pane",
		"body.receiver .settings-pane.active",
		"body.receiver .settings-pane.single-col.active",
		"body.receiver .settings-row",
		"body.receiver .settings-field",
		"body.receiver .settings-placeholder",
		"max-height: 0;",
		"opacity: 0;",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("chassis.css missing Task 22 contract %q", want)
		}
	}
}

func TestChassisCSS_HistoryRowsDoNotAdvertiseMissingInteractivity(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)
	rowRule := cssRuleBlock(t, text, "body.receiver .history-row")
	if strings.Contains(rowRule, "cursor: pointer;") {
		t.Fatalf("plain history rows are divs; base .history-row must not use cursor: pointer")
	}
	if strings.Contains(text, "body.receiver .history-row:hover") {
		t.Fatalf("plain history rows are divs; hover affordance must be gated to semantic/actionable rows")
	}
}

func TestChassisCSS_Task23IdleLiveStateOverrideContracts(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)
	for _, want := range []string{
		"/* ---- 14. Idle / live state overrides ---- */",
		"body.receiver.idle .history-row.last-cast",
		"body.receiver.idle .history-row.last-cast .title",
		"body.receiver.idle .history-row.last-cast .name",
		"body.receiver.idle .aux-lbl",
		"body.receiver.idle .aux-screen",
		"body.receiver.browse-open .preset-header .browse-btn",
		"body.receiver.browse-open .preset-header .browse-btn::before",
		"body.receiver.browse-open .catalog-drawer",
		"body.receiver.browse-open .catalog-browser",
		"body.receiver.catalog-scanning .vfd::after",
		"@keyframes scan-blink",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("chassis.css missing Task 23 state override contract %q", want)
		}
	}

	lastCastRule := cssRuleBlock(t, text, "body.receiver.idle .history-row.last-cast")
	if !strings.Contains(lastCastRule, "background: transparent;") {
		t.Fatalf("idle last-cast history rule must clear the active background: %s", lastCastRule)
	}

	scanRule := cssRuleBlock(t, text, "body.receiver.catalog-scanning .vfd::after")
	for _, want := range []string{
		"CATALOG SCAN",
		"animation: scan-blink",
		"pointer-events: none;",
	} {
		if !strings.Contains(scanRule, want) {
			t.Fatalf("catalog scan rule missing %q: %s", want, scanRule)
		}
	}
}

func TestChassisCSS_CatalogScanHonorsReducedMotionCascade(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)
	baseRule := "body.receiver.catalog-scanning .vfd::after"
	baseIdx := strings.Index(text, baseRule)
	if baseIdx == -1 {
		t.Fatalf("chassis.css missing %q", baseRule)
	}
	baseAnimationIdx := strings.Index(text[baseIdx:], "animation: scan-blink")
	if baseAnimationIdx == -1 {
		t.Fatalf("chassis.css missing scan-blink animation in %q rule", baseRule)
	}
	afterScanRule := text[baseIdx+baseAnimationIdx:]
	reduceIdx := strings.Index(afterScanRule, "@media (prefers-reduced-motion: reduce)")
	if reduceIdx == -1 {
		t.Fatalf("chassis.css must place a reduced-motion override after %q so it wins the cascade", baseRule)
	}
	overrideIdx := strings.Index(afterScanRule[reduceIdx:], baseRule)
	if overrideIdx == -1 {
		t.Fatalf("reduced-motion block after %q must override the catalog scan animation", baseRule)
	}
	overrideSnippet := afterScanRule[reduceIdx+overrideIdx:]
	if !strings.Contains(overrideSnippet, "animation: none;") || !strings.Contains(overrideSnippet, "opacity: 0;") {
		t.Fatalf("reduced-motion override for %q must set animation: none and opacity: 0", baseRule)
	}
}

func TestChassisCSS_Task24ResponsiveContainerContracts(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := strings.ReplaceAll(string(src), "\r\n", "\n")

	for _, want := range []string{
		"/* ---- 15. Responsive (container queries) ---- */",
		"@container chassis (max-width: 1180px)",
		"@container chassis (max-width: 900px)",
		"@container chassis (max-width: 600px)",
		"@container vfd (max-width: 720px)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("chassis.css missing Task 24 responsive contract %q", want)
		}
	}

	receiverRule := cssRuleBlock(t, text, "body.receiver .receiver")
	for _, want := range []string{
		"container-name: chassis;",
		"container-type: inline-size;",
	} {
		if !strings.Contains(receiverRule, want) {
			t.Fatalf("receiver container declaration missing %q: %s", want, receiverRule)
		}
	}

	vfdRule := cssRuleBlock(t, text, "body.receiver .screen.vfd,\nbody.receiver .vfd")
	for _, want := range []string{
		"container-name: vfd;",
		"container-type: inline-size;",
	} {
		if !strings.Contains(vfdRule, want) {
			t.Fatalf("vfd container declaration missing %q: %s", want, vfdRule)
		}
	}

	contracts := []struct {
		name     string
		atRule   string
		selector string
		want     []string
	}{
		{
			name:     "1180 hides throughput and ack scopes",
			atRule:   "@container chassis (max-width: 1180px)",
			selector: "body.receiver .meter-screen--compact .throughput-wrap,\n  body.receiver .meter-screen--compact .ack-wrap",
			want:     []string{"display: none;"},
		},
		{
			name:     "1180 drops speed and link readout groups",
			atRule:   "@container chassis (max-width: 1180px)",
			selector: "body.receiver .meter-screen--compact .meter-readout-line .net-grp .grp.speed-grp,\n  body.receiver .meter-screen--compact .meter-readout-line .net-grp .grp.link-grp",
			want:     []string{"display: none;"},
		},
		{
			name:     "1180 source cluster becomes a stable 3x2 footprint",
			atRule:   "@container chassis (max-width: 1180px)",
			selector: "body.receiver .vfd-source-row .source-cluster",
			want: []string{
				"grid-template-columns: repeat(3, minmax(82px, 1fr));",
				"grid-template-rows: 1fr 1fr;",
			},
		},
		{
			name:     "900 hides the goniometer",
			atRule:   "@container chassis (max-width: 900px)",
			selector: "body.receiver .meter-screen--compact .gonio-wrap",
			want:     []string{"display: none;"},
		},
		{
			name:     "900 stacks vfd and source row",
			atRule:   "@container chassis (max-width: 900px)",
			selector: "body.receiver .vfd-source-row",
			want:     []string{"grid-template-columns: 1fr;"},
		},
		{
			name:     "900 keeps source cluster with auto-fit columns",
			atRule:   "@container chassis (max-width: 900px)",
			selector: "body.receiver .vfd-source-row .source-cluster",
			want:     []string{"grid-template-columns: repeat(auto-fit, minmax(82px, 1fr));"},
		},
		{
			name:     "900 tightens the chassis chrome",
			atRule:   "@container chassis (max-width: 900px)",
			selector: "body.receiver .receiver .screw",
			want:     []string{"display: none;"},
		},
		{
			name:     "900 adjusts history rows",
			atRule:   "@container chassis (max-width: 900px)",
			selector: "body.receiver .history-row .source",
			want:     []string{"display: none;"},
		},
		{
			name:     "900 adjusts settings tabs",
			atRule:   "@container chassis (max-width: 900px)",
			selector: "body.receiver .settings-tabs",
			want:     []string{"overflow-x: auto;"},
		},
		{
			name:     "900 adjusts catalog body",
			atRule:   "@container chassis (max-width: 900px)",
			selector: "body.receiver .catalog-body",
			want:     []string{"grid-template-columns: 132px 1fr;"},
		},
		{
			name:     "600 hides meter source strip and field flip",
			atRule:   "@container chassis (max-width: 600px)",
			selector: "body.receiver .meter-screen.meter-screen--compact .meter-source-strip,\n  body.receiver .field-flip",
			want:     []string{"display: none;"},
		},
		{
			name:     "520 wraps input controls below source buttons",
			atRule:   "@container chassis (max-width: 520px)",
			selector: "body.receiver .input-section > div",
			want:     []string{"flex-wrap: wrap;"},
		},
		{
			name:     "520 lets input prompt use a full row",
			atRule:   "@container chassis (max-width: 520px)",
			selector: "body.receiver .input-section .input-panel",
			want:     []string{"flex: 1 1 100% !important;", "flex-basis: 100% !important;"},
		},
		{
			name:     "520 tightens VFD tertiary readability",
			atRule:   "@container chassis (max-width: 520px)",
			selector: "body.receiver .vfd .tier-tertiary",
			want:     []string{"font-size: 10px;", "letter-spacing: 0.04em;"},
		},
		{
			name:     "601-900 keeps full load core label",
			atRule:   "@container chassis (min-width: 601px) and (max-width: 900px)",
			selector: "body.receiver .load-core-btn .label-text",
			want:     []string{"display: inline;"},
		},
		{
			name:     "601-900 removes compact synthetic core label",
			atRule:   "@container chassis (min-width: 601px) and (max-width: 900px)",
			selector: "body.receiver .load-core-btn::after",
			want:     []string{"content: none;"},
		},
		{
			name:     "600 collapses transport controls",
			atRule:   "@container chassis (max-width: 600px)",
			selector: "body.receiver .transport-strip",
			want:     []string{"grid-template-columns: 60px auto 1fr auto auto;"},
		},
		{
			name:     "600 adjusts input panel",
			atRule:   "@container chassis (max-width: 600px)",
			selector: "body.receiver .input-section .input-panel",
			want:     []string{"width: 100%;"},
		},
		{
			name:     "600 adjusts preset bank",
			atRule:   "@container chassis (max-width: 600px)",
			selector: "body.receiver .preset-bank",
			want:     []string{"grid-template-columns: repeat(2, 1fr);"},
		},
		{
			name:     "600 adjusts history timestamps",
			atRule:   "@container chassis (max-width: 600px)",
			selector: "body.receiver .history-row .when",
			want:     []string{"display: none;"},
		},
		{
			name:     "600 adjusts catalog body",
			atRule:   "@container chassis (max-width: 600px)",
			selector: "body.receiver .catalog-body",
			want:     []string{"grid-template-columns: 1fr;"},
		},
		{
			name:     "600 adjusts settings rows",
			atRule:   "@container chassis (max-width: 600px)",
			selector: "body.receiver .settings-row",
			want:     []string{"grid-template-columns: 1fr;"},
		},
		{
			name:     "720 hides VFD right panel",
			atRule:   "@container vfd (max-width: 720px)",
			selector: "body.receiver .vfd .right-panel",
			want:     []string{"display: none;"},
		},
	}

	for _, contract := range contracts {
		rule := cssRuleBlockInAtRules(t, text, contract.atRule, contract.selector)
		for _, want := range contract.want {
			if !strings.Contains(rule, want) {
				t.Errorf("%s: rule %q missing %q: %s", contract.name, contract.selector, want, rule)
			}
		}
	}
}

func TestReceiverLocalFilesButtonUsesUploadButtonStyle(t *testing.T) {
	t.Parallel()
	cssBytes, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile chassis.css: %v", err)
	}
	css := string(cssBytes)
	for _, want := range []string{
		`body.receiver .upload-btn`,
		`body.receiver .receiver-localfiles-drawer .catalog-top`,
		`body.receiver .receiver-localfiles-drawer .field-input`,
		`body.receiver .receiver-localfiles-drawer .widget-err`,
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("chassis.css missing local files/input styling hook %q", want)
		}
	}
}

func TestSourceClusterResponsiveSixCapableLayout(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)

	for _, want := range []string{
		"body.receiver .source-cluster {\n  display: grid;",
		"width: clamp(480px, 38vw, 600px);",
		"grid-template-columns: repeat(auto-fit, minmax(82px, 1fr));",
		"grid-template-rows: 1fr;",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("base source cluster six-capable layout missing %q", want)
		}
	}

	wideCompact := cssRuleBlockInAtRules(t, text, "@container chassis (max-width: 1180px)", "body.receiver .vfd-source-row .source-cluster")
	for _, want := range []string{
		"width: 100%;",
		"grid-template-columns: repeat(3, minmax(82px, 1fr));",
		"grid-template-rows: 1fr 1fr;",
	} {
		if !strings.Contains(wideCompact, want) {
			t.Fatalf("1180 source cluster six-capable layout missing %q: %s", want, wideCompact)
		}
	}

	narrowRule := cssRuleBlockInAtRules(t, text, "@container chassis (max-width: 900px)", "body.receiver .vfd-source-row .source-cluster")
	if !strings.Contains(narrowRule, "grid-template-columns: repeat(auto-fit, minmax(82px, 1fr));") {
		t.Fatalf("900 source cluster six-capable layout missing auto-fit columns: %s", narrowRule)
	}

	narrowButtonRule := cssRuleBlockInAtRules(t, text, "@container chassis (max-width: 900px)", "body.receiver .vfd-source-row .source-cluster .hw-btn")
	if !strings.Contains(narrowButtonRule, "min-width: 0;") {
		t.Fatalf("900 source buttons must be allowed to shrink without overflow: %s", narrowButtonRule)
	}
}

func TestChassisCSS_DenseResponsivePassContracts(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)

	for _, want := range []string{
		"@container chassis (max-width: 760px)",
		"body.receiver .input-controls",
		"body.receiver .audio-deck > .deck-sect",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dense responsive pass missing contract %q", want)
		}
	}

	inputRule := cssRuleBlock(t, text, "body.receiver .input-controls")
	for _, want := range []string{
		"display: flex;",
		"gap: 6px;",
		"min-width: 0;",
	} {
		if !strings.Contains(inputRule, want) {
			t.Fatalf("input controls should own the flexible row layout, missing %q: %s", want, inputRule)
		}
	}

	vfdSource1180 := cssRuleBlockInAtRules(t, text, "@container chassis (max-width: 1180px)", "body.receiver .vfd-source-row")
	if !strings.Contains(vfdSource1180, "grid-template-columns: minmax(0, 1fr) minmax(270px, 34%);") {
		t.Fatalf("1180 VFD/source row should reserve a bounded source column: %s", vfdSource1180)
	}

	meter760 := cssRuleBlockInAtRules(t, text, "@container chassis (max-width: 760px)", "body.receiver .meter-screen.meter-screen--compact .meter-row")
	if !strings.Contains(meter760, "grid-template-columns: minmax(0, 1fr);") {
		t.Fatalf("760 meter rows should collapse to one column before they overflow: %s", meter760)
	}

	meterAudio600 := cssRuleBlockInAtRules(t, text, "@container chassis (max-width: 600px)", "body.receiver .meter-screen.meter-screen--compact .meter-mid-row .audio-grp")
	if !strings.Contains(meterAudio600, "display: none;") {
		t.Fatalf("phone meter should hide nonessential audio scopes: %s", meterAudio600)
	}

	sourceLamp600 := cssRuleBlockInAtRules(t, text, "@container chassis (max-width: 600px)", "body.receiver .source-cluster .lamp")
	for _, want := range []string{
		"min-height: 58px;",
		"grid-template-rows: 25px minmax(0, 1fr);",
	} {
		if !strings.Contains(sourceLamp600, want) {
			t.Fatalf("phone source lamps should become compact indicators, missing %q: %s", want, sourceLamp600)
		}
	}

	sourceState600 := cssRuleBlockInAtRules(t, text, "@container chassis (max-width: 600px)", "body.receiver .source-cluster .lamp .state")
	if !strings.Contains(sourceState600, "display: none;") {
		t.Fatalf("phone source lamps should hide routine state text: %s", sourceState600)
	}
	sourceAlert600 := cssRuleBlockInAtRules(t, text, "@container chassis (max-width: 600px)", "body.receiver .source-cluster .lamp.casting .state,\n  body.receiver .source-cluster .lamp.issue .state")
	if !strings.Contains(sourceAlert600, "display: block;") {
		t.Fatalf("phone source lamps should still expose live/error state text: %s", sourceAlert600)
	}

	presetSearch600 := cssRuleBlockInAtRules(t, text, "@container chassis (max-width: 600px)", "body.receiver .preset-header .search-field")
	if !strings.Contains(presetSearch600, "flex: 1 1 100%;") {
		t.Fatalf("phone preset search should wrap to a full row: %s", presetSearch600)
	}

	audioDeck600 := cssRuleBlockInAtRules(t, text, "@container chassis (max-width: 600px)", "body.receiver .audio-deck")
	for _, want := range []string{
		"scroll-snap-type: x proximity;",
		"padding: 12px 12px;",
	} {
		if !strings.Contains(audioDeck600, want) {
			t.Fatalf("phone audio deck should stay dense but horizontally navigable, missing %q: %s", want, audioDeck600)
		}
	}

	transport420 := cssRuleBlockInAtRules(t, text, "@container chassis (max-width: 420px)", "body.receiver .trn")
	if !strings.Contains(transport420, "min-width: 42px;") {
		t.Fatalf("phone transport controls should keep usable touch width: %s", transport420)
	}
}

func TestSourceClusterLampsUsePassiveReceiverIndicatorVocabulary(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)

	clusterStart := strings.Index(text, "body.receiver .source-cluster {\n  display: grid;")
	if clusterStart == -1 {
		t.Fatalf("missing primary source cluster rule")
	}
	clusterRule := cssRuleBlock(t, text[clusterStart:], "body.receiver .source-cluster")
	for _, want := range []string{
		"gap: 0;",
		"padding: 4px;",
		"background: linear-gradient(180deg, #202024 0%, #17171a 100%);",
		"inset 0 -1px 0 rgba(0, 0, 0, 0.68)",
	} {
		if !strings.Contains(clusterRule, want) {
			t.Fatalf("source cluster should be one shared recessed indicator rail, missing %q: %s", want, clusterRule)
		}
	}

	lampSectionStart := strings.Index(text, "/* ---- Source-cluster lamps (3B) ---- */")
	if lampSectionStart == -1 {
		t.Fatalf("missing source-cluster lamp section")
	}
	lampText := text[lampSectionStart:]

	lampRule := cssRuleBlock(t, lampText, "body.receiver .source-cluster .lamp")
	for _, want := range []string{
		"grid-template-columns: minmax(0, 1fr);",
		"grid-template-rows: 40px minmax(0, 1fr) 15px;",
		"background: transparent;",
		"border: 0;",
		"inset 1px 0 0 rgba(255, 255, 255, 0.035)",
		"cursor: default;",
		"transition:",
	} {
		if !strings.Contains(lampRule, want) {
			t.Fatalf("source lamp must read as a passive receiver indicator, missing %q: %s", want, lampRule)
		}
	}
	for _, banned := range []string{
		"background: linear-gradient(180deg, #4a4a50 0%, #2a2a2e 50%, #1a1a1c 100%);",
		"background: linear-gradient(180deg, #252529 0%, #202024 100%);",
		"0 1px 2px rgba(0, 0, 0, 0.4)",
	} {
		if strings.Contains(lampRule, banned) {
			t.Fatalf("source lamp must not keep raised button styling %q: %s", banned, lampRule)
		}
	}

	nameRule := cssRuleBlock(t, lampText, "body.receiver .source-cluster .lamp .nameplate")
	for _, banned := range []string{
		"color: #111113;",
		"background: linear-gradient(180deg, #c4c4c8, #74747b);",
	} {
		if strings.Contains(nameRule, banned) {
			t.Fatalf("source lamp nameplate must not keep the silver badge styling %q: %s", banned, nameRule)
		}
	}
	if !strings.Contains(nameRule, "background: transparent;") {
		t.Fatalf("source lamp nameplate should be label text on the hardware faceplate: %s", nameRule)
	}
	if !strings.Contains(nameRule, "justify-self: center;") {
		t.Fatalf("source lamp label should sit like printed faceplate text: %s", nameRule)
	}

	wellRule := cssRuleBlock(t, lampText, "body.receiver .source-cluster .lamp .led-well")
	for _, want := range []string{
		"width: 34px;",
		"height: 34px;",
		"background: radial-gradient(circle at center, #030304 0%, #0b0b0d 58%, #303036 100%);",
		"border-radius: 50%;",
	} {
		if !strings.Contains(wellRule, want) {
			t.Fatalf("source lamp LED well must look like a recessed physical indicator cup, missing %q: %s", want, wellRule)
		}
	}

	ledRule := cssRuleBlock(t, lampText, "body.receiver .source-cluster .lamp .led")
	for _, want := range []string{
		"width: 21px;",
		"height: 21px;",
		"border: 1px solid #050506;",
		"box-shadow: inset 0 1px 1px rgba(0, 0, 0, 0.72);",
	} {
		if !strings.Contains(ledRule, want) {
			t.Fatalf("source lamp LED must use the same small indicator scale as the status bar, missing %q: %s", want, ledRule)
		}
	}
	if strings.Contains(ledRule, "width: 24px;") || strings.Contains(ledRule, "height: 24px;") {
		t.Fatalf("source lamp LED must not keep the oversized lens: %s", ledRule)
	}

	stateRule := cssRuleBlock(t, lampText, "body.receiver .source-cluster .lamp .state")
	for _, want := range []string{
		"background: transparent;",
		"border: 0;",
		"box-shadow: none;",
		"font-size: 6px;",
		"opacity: 0.72;",
	} {
		if !strings.Contains(stateRule, want) {
			t.Fatalf("source lamp state text should be printed on the faceplate, missing %q: %s", want, stateRule)
		}
	}

	offStateRule := cssRuleBlock(t, lampText, "body.receiver .source-cluster .lamp.unavailable .state")
	if !strings.Contains(offStateRule, "visibility: hidden;") {
		t.Fatalf("unavailable source lamps should communicate off state by dark lamp, not visible OFF text: %s", offStateRule)
	}
}

func TestChassisCSS_AudioScopeCanvasesFillFrames(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)

	spectrumRule := cssRuleBlock(t, text, "body.receiver .meter-screen--compact .spectrum")
	if strings.Contains(spectrumRule, "grid-template-columns") {
		t.Fatalf("spectrum canvas frame must not keep the old miniature grid layout: %s", spectrumRule)
	}
	for _, want := range []string{
		"position: relative;",
		"overflow: hidden;",
		"width: clamp(250px",
		"height: clamp(74px",
	} {
		if !strings.Contains(spectrumRule, want) {
			t.Fatalf("spectrum frame missing %q: %s", want, spectrumRule)
		}
	}
	canvasRule := cssRuleBlock(t, text, "body.receiver .meter-screen--compact .spectrum canvas")
	for _, want := range []string{"display: block;", "width: 100%;", "height: 100%;"} {
		if !strings.Contains(canvasRule, want) {
			t.Fatalf("spectrum canvas rule missing %q: %s", want, canvasRule)
		}
	}
	gonioRule := cssRuleBlock(t, text, "body.receiver .gonio")
	if !strings.Contains(gonioRule, "height: clamp(74px") {
		t.Fatalf("goniometer frame should match the enlarged audio scope height: %s", gonioRule)
	}
}

func TestChassisCSS_FieldOrderLockIsStaticNotAnimated(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)

	for _, selector := range []string{
		"body.receiver .field-flip .dot",
		"body.receiver .field-flip .lbl",
	} {
		rule := cssRuleBlock(t, text, selector)
		if strings.Contains(rule, "animation:") {
			t.Fatalf("%s must reflect configured field order, not animate between odd/even: %s", selector, rule)
		}
	}
	for _, want := range []string{
		`body.receiver .field-flip[data-field-order="tff"] .dot.odd`,
		`body.receiver .field-flip[data-field-order="tff"] .row[data-field-row="tff"] .lbl`,
		`body.receiver .field-flip[data-field-order="bff"] .dot.even`,
		`body.receiver .field-flip[data-field-order="bff"] .row[data-field-row="bff"] .lbl`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("field-order CSS missing static selector %q", want)
		}
	}
}

func TestChassisCSS_SeekHeadTracksSeekPercentVariable(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)
	seekRule := cssRuleBlock(t, text, "body.receiver .seek-bar")
	if !strings.Contains(seekRule, "--seek-percent: 0%;") {
		t.Fatalf("seek bar should define a stable seek-position variable: %s", seekRule)
	}
	fillRule := cssRuleBlock(t, text, "body.receiver .seek-bar .fill")
	if !strings.Contains(fillRule, "width: var(--seek-percent);") {
		t.Fatalf("seek fill should be driven by --seek-percent: %s", fillRule)
	}
	headRule := cssRuleBlock(t, text, "body.receiver .seek-bar .head")
	if !strings.Contains(headRule, "left: calc(var(--seek-percent) - 6px);") {
		t.Fatalf("seek head should track --seek-percent: %s", headRule)
	}
}

func TestChassisCSS_VolumeTickRingUsesCenteredRadialTransforms(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)
	tickRule := cssRuleBlock(t, text, "body.receiver .volume-tick")
	if strings.Contains(tickRule, "transform-origin: 0 30px") {
		t.Fatalf("volume ticks should not rotate around an off-center origin: %s", tickRule)
	}
	if !strings.Contains(tickRule, `transform: translate(-50%, -50%) rotate(var(--tick-angle)) translateY(calc(var(--volume-tick-radius, 34px) * -1));`) {
		t.Fatalf("volume ticks should use one centered radial transform: %s", tickRule)
	}
	if !strings.Contains(text, "body.receiver .volume-tick.tick-10 { --tick-angle: 0deg; }") {
		t.Fatalf("volume tick angle classes should assign --tick-angle values")
	}
}

func TestChassisCSS_MasterVolumeUsesLargeSonyStyleIndicatorKnob(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)

	masterRule := cssRuleBlock(t, text, "body.receiver [data-volume-knob]")
	for _, want := range []string{
		"width: 118px;",
		"height: 118px;",
		"--volume-ring-size: 112px;",
		"--volume-dial-size: 82px;",
		"--volume-tick-radius: 53px;",
	} {
		if !strings.Contains(masterRule, want) {
			t.Fatalf("master volume should use the larger receiver-knob scale, missing %q: %s", want, masterRule)
		}
	}

	dialRule := cssRuleBlock(t, text, "body.receiver [data-volume-knob] .volume-dial")
	for _, want := range []string{
		"border: 2px solid #050506;",
		"linear-gradient(135deg, rgba(255,255,255,0.22), rgba(255,255,255,0) 30%)",
		"radial-gradient(circle at 50% 52%, #4a4a50 0 7%, #242428 32%, #111113 64%, #050506 100%)",
		"0 2px 8px rgba(0,0,0,0.78)",
	} {
		if !strings.Contains(dialRule, want) {
			t.Fatalf("master volume dial should read as a heavy Sony-style hardware knob, missing %q: %s", want, dialRule)
		}
	}

	notchRule := cssRuleBlock(t, text, "body.receiver [data-volume-knob] .volume-notch")
	for _, want := range []string{
		"width: 8px;",
		"height: 21px;",
		"background: var(--vfd);",
		"box-shadow: 0 0 8px var(--vfd-glow)",
	} {
		if !strings.Contains(notchRule, want) {
			t.Fatalf("master volume should have a VFD-colored front-panel indicator, missing %q: %s", want, notchRule)
		}
	}
	for _, banned := range []string{
		"var(--lock-amber",
		"#d99340",
		"217, 147, 64",
	} {
		if strings.Contains(notchRule, banned) {
			t.Fatalf("master volume indicator should not use the amber accent %q: %s", banned, notchRule)
		}
	}

	if strings.Contains(cssRuleBlock(t, text, "body.receiver .audio-deck .dsp-knob"), "width: 118px;") {
		t.Fatalf("tone and balance knobs should stay smaller than the master volume")
	}
}

func TestChassisCSS_BrowseButtonHasBaseButtonChrome(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile(static/chassis.css): %v", err)
	}
	text := string(src)
	rule := cssRuleBlock(t, text, "body.receiver .browse-btn")
	for _, want := range []string{
		"background:",
		"border: 1px solid",
		"box-shadow:",
		"text-transform: uppercase;",
		"cursor: pointer;",
	} {
		if !strings.Contains(rule, want) {
			t.Fatalf("browse button base chrome missing %q: %s", want, rule)
		}
	}
}

func TestFindUnscopedSelectors_FixtureGood(t *testing.T) {
	t.Parallel()
	src := []byte(`
:root { --foo: red; }
@font-face { font-family: 'X'; src: url('x.woff2'); }
body.receiver .foo { color: red; }
body.receiver.idle .bar { opacity: 0.5; }
body.receiver .foo, body.receiver .baz { color: blue; }
body.receiver .ok, body.receiver .also-ok { color: green; }
@container chassis (max-width: 900px) {
  body.receiver .foo { display: none; }
}
@media (max-width: 900px) {
  body.receiver .bar { display: none; }
}
@keyframes spin { 0% { transform: rotate(0); } 100% { transform: rotate(360deg); } }
`)
	leaks := findUnscopedSelectors(src)
	if len(leaks) != 0 {
		t.Errorf("expected zero leaks for well-scoped fixture, got: %v", leaks)
	}
}

func TestFindUnscopedSelectors_FixtureBad(t *testing.T) {
	t.Parallel()
	src := []byte(`
.foo { color: red; }
body.idle .bar { opacity: 0.5; }
.foo, body.receiver .baz { color: blue; }
body.receiver .ok, .leak-after-scoped { color: red; }
@container chassis (max-width: 900px) { .inside-container { display: none; } }
@media (max-width: 900px) { .inside-media { display: none; } }
`)
	leaks := findUnscopedSelectors(src)
	want := map[string]bool{
		".foo":               true,
		"body.idle .bar":     true,
		".leak-after-scoped": true,
		".inside-container":  true,
		".inside-media":      true,
	}
	if len(leaks) != 6 {
		t.Errorf("expected 6 leaks for fixture, got %d: %v", len(leaks), leaks)
	}
	for _, leak := range leaks {
		if !want[leak] {
			t.Errorf("unexpected leak: %q", leak)
		}
	}
}

func findUnscopedSelectors(src []byte) []string {
	var leaks []string
	// tdewolff/parse v2.8.12 does not descend into @container blocks.
	// Normalize them to a known grouping at-rule so nested selectors are
	// still checked without changing the CSS under test.
	src = bytes.ReplaceAll(src, []byte("@container"), []byte("@media"))
	p := css.NewParser(parse.NewInput(bytes.NewReader(src)), false)
	skipAtRuleDepth := 0
	for {
		gt, _, data := p.Next()
		switch gt {
		case css.ErrorGrammar:
			return leaks
		case css.BeginAtRuleGrammar:
			at := strings.TrimSpace(cssGrammarText(p, data))
			if isSelectorExemptAtRule(at) || skipAtRuleDepth > 0 {
				skipAtRuleDepth++
			}
		case css.EndAtRuleGrammar:
			if skipAtRuleDepth > 0 {
				skipAtRuleDepth--
			}
		case css.BeginRulesetGrammar:
			if skipAtRuleDepth > 0 {
				continue
			}
			sel := strings.TrimSpace(cssGrammarText(p, data))
			for _, part := range strings.Split(sel, ",") {
				part = strings.TrimSpace(part)
				if part == "" || isAllowlistedSelector(part) || strings.HasPrefix(part, "body.receiver") {
					continue
				}
				leaks = append(leaks, part)
			}
		}
	}
}

func countRulesets(src []byte) int {
	src = bytes.ReplaceAll(src, []byte("@container"), []byte("@media"))
	p := css.NewParser(parse.NewInput(bytes.NewReader(src)), false)
	count := 0
	skipAtRuleDepth := 0
	for {
		gt, _, data := p.Next()
		switch gt {
		case css.ErrorGrammar:
			return count
		case css.BeginAtRuleGrammar:
			at := strings.TrimSpace(cssGrammarText(p, data))
			if isSelectorExemptAtRule(at) || skipAtRuleDepth > 0 {
				skipAtRuleDepth++
			}
		case css.EndAtRuleGrammar:
			if skipAtRuleDepth > 0 {
				skipAtRuleDepth--
			}
		case css.BeginRulesetGrammar:
			if skipAtRuleDepth == 0 {
				count++
			}
		}
	}
}

func cssGrammarText(p *css.Parser, data []byte) string {
	var b strings.Builder
	b.Write(data)
	for _, token := range p.Values() {
		b.Write(token.Data)
	}
	return b.String()
}

func cssRuleBlock(t *testing.T, text, selector string) string {
	t.Helper()
	block, ok := cssRuleBlockMaybe(text, selector)
	if !ok {
		t.Fatalf("missing CSS selector block %q", selector)
	}
	return block
}

func cssRuleBlockMaybe(text, selector string) (string, bool) {
	start := strings.Index(text, selector+" {")
	if start == -1 {
		return "", false
	}
	bodyStart := start + len(selector+" {")
	depth := 1
	for i := bodyStart; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start:i], true
			}
		}
	}
	return "", false
}

func cssRuleBlockInAtRules(t *testing.T, text, atRule, selector string) string {
	t.Helper()
	for _, atRuleBlock := range cssAtRuleBlocks(t, text, atRule) {
		if rule, ok := cssRuleBlockMaybe(atRuleBlock, selector); ok {
			return rule
		}
	}
	t.Fatalf("missing CSS selector block %q inside %q", selector, atRule)
	return ""
}

func cssAtRuleBlocks(t *testing.T, text, atRule string) []string {
	t.Helper()
	var blocks []string
	searchFrom := 0
	for {
		start := strings.Index(text[searchFrom:], atRule+" {")
		if start == -1 {
			break
		}
		start += searchFrom
		bodyStart := start + len(atRule+" {")
		depth := 1
		for i := bodyStart; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					blocks = append(blocks, text[bodyStart:i])
					searchFrom = i + 1
					goto next
				}
			}
		}
		t.Fatalf("CSS at-rule %q starting at byte %d is not closed", atRule, start)
	next:
	}
	if len(blocks) == 0 {
		t.Fatalf("missing CSS at-rule %q", atRule)
	}
	return blocks
}

func isSelectorExemptAtRule(at string) bool {
	at = strings.TrimPrefix(strings.TrimSpace(at), "@")
	return strings.HasPrefix(at, "font-face") || strings.HasPrefix(at, "keyframes")
}

func isAllowlistedSelector(sel string) bool {
	switch sel {
	case ":root", "from", "to":
		return true
	}
	return len(sel) > 1 && sel[len(sel)-1] == '%'
}
