package chassis

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"math"
	"strings"
	textTemplate "text/template"
)

// chassisTemplatesFS holds the html/template files used to render the
// chassis page. Embedded at build so the binary is self-contained;
// no filesystem reads at runtime.
//
//go:embed templates/*.html
var chassisTemplatesFS embed.FS

// chassisStaticFS holds chassis.css, chassis.js, and woff2 fonts.
// Embedded wholesale; served under /receiver/static/ via Mount.
//
//go:embed static
var chassisStaticFS embed.FS

// templateFuncs supplies the helpers the chassis templates need. The
// first three are duplicated verbatim from internal/ui/server.go:
// during the parallel-replacement period the chassis package has zero
// imports of internal/ui, so we accept coupling-by-copy rather than
// coupling-by-import. The final cutover spec deduplicates.
//
// Chassis-specific helpers:
//   - htmlComment: emits trusted sentinel comments for composition tests.
//     html/template strips literal HTML comments from template output.
//   - pad2: zero-padded two-digit strings for clock display.
//   - dim: returns the CSS class string for inactive lamps.
//   - list: constructs a string slice for small template membership probes.
//   - until: returns n placeholders for repeated template elements.
var templateFuncs = template.FuncMap{
	"inc":        func(i int) int { return i + 1 },
	"replaceAll": strings.ReplaceAll,
	"hasString": func(haystack []string, needle string) bool {
		for _, s := range haystack {
			if s == needle {
				return true
			}
		}
		return false
	},
	"pad2": func(n int) string {
		if n < 10 {
			return fmt.Sprintf("0%d", n)
		}
		return fmt.Sprintf("%d", n)
	},
	"dim": func(active bool) string {
		if active {
			return ""
		}
		return "dim"
	},
	"htmlComment": func(s string) template.HTML {
		return template.HTML("<!-- " + s + " -->")
	},
	"list":        func(args ...string) []string { return args },
	"until":       func(n int) []struct{} { return make([]struct{}, n) },
	"volumeAngle": volumeAngle,
}

// volumeAngle maps the output_volume (0..100) to the dial rotation in
// degrees over a -135..135 arc. Out-of-range inputs are clamped so the
// template helper is total — callers cannot blow up rendering by feeding
// an out-of-spec configured volume.
func volumeAngle(volume int) int {
	if volume < 0 {
		volume = 0
	}
	if volume > 100 {
		volume = 100
	}
	return -135 + int(math.Round(float64(volume)*2.7))
}

// parseTemplates parses the embedded chassis templates with the helper
// FuncMap pre-registered. Called once at server startup from New.
func parseTemplates() (*template.Template, error) {
	tmpl, err := template.New("chassis").Funcs(templateFuncs).ParseFS(chassisTemplatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("chassis: parse templates: %w", err)
	}
	return tmpl, nil
}

// preprocessCSS substitutes {{.Version}} placeholders inside the
// embedded chassis.css and returns the result. Uses text/template
// because CSS is not HTML and must not receive context-aware escaping.
func preprocessCSS(src []byte, version string) ([]byte, error) {
	tmpl, err := textTemplate.New("chassis.css").Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("chassis: parse CSS template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"Version": version}); err != nil {
		return nil, fmt.Errorf("chassis: execute CSS template: %w", err)
	}
	return buf.Bytes(), nil
}
