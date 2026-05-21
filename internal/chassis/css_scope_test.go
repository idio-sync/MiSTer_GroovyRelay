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
	start := strings.Index(text, selector+" {")
	if start == -1 {
		t.Fatalf("missing CSS selector block %q", selector)
	}
	end := strings.Index(text[start:], "\n}")
	if end == -1 {
		t.Fatalf("CSS selector block %q is not closed", selector)
	}
	return text[start : start+end]
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
