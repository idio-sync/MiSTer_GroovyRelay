package chassis

import (
	"embed"
	"fmt"
	"html/template"
	"strings"
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
//   - pad2: zero-padded two-digit strings for clock display.
//   - dim: returns the CSS class string for inactive lamps.
//   - list: constructs a string slice for small template membership probes.
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
	"list": func(args ...string) []string { return args },
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
