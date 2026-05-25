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
	const minRulesets = 450
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
				"grid-template-columns: repeat(3, minmax(96px, 1fr));",
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
			name:     "900 keeps source cluster as five equal columns",
			atRule:   "@container chassis (max-width: 900px)",
			selector: "body.receiver .vfd-source-row .source-cluster",
			want:     []string{"grid-template-columns: repeat(5, minmax(0, 1fr));"},
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
			name:     "520 tightens VFD marquee readability",
			atRule:   "@container chassis (max-width: 520px)",
			selector: "body.receiver .vfd .marquee-line",
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
			want:     []string{"grid-template-columns: 60px auto 1fr auto;"},
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
