package chassis

import (
	"bytes"
	"embed"
	"fmt"
	"html"
	"html/template"
	"math"
	"strconv"
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
	"lower":               strings.ToLower,
	"upper":               strings.ToUpper,
	"dict":                dictHelper,
	"itoa":                itoaHelper,
	"errOf":               errOfHelper,
	"settingsScopeLabel":  settingsScopeLabelHelper,
	"stub":                stubHelper,
	"field":               fieldHelper,
	"humanizeBytes":       humanizeBytes,
	"boolStr":             boolStr,
	"i64toa":              i64toa,
	"passwordPlaceholder": passwordPlaceholder,
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

// dictHelper builds a map[string]any from alternating key-value pairs.
// Used by templates to pass option bags to {{ field }} (Go templates
// have no native named-arg syntax).
func dictHelper(pairs ...any) map[string]any {
	if len(pairs)%2 != 0 {
		panic("chassis: dict expects an even number of arguments")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			panic("chassis: dict keys must be strings")
		}
		m[key] = pairs[i+1]
	}
	return m
}

// itoaHelper wraps strconv.Itoa for the FuncMap.
func itoaHelper(n int) string { return strconv.Itoa(n) }

// boolStr returns "true" or "false" for switch value coercion in
// templates. Use it to feed the `field` helper's Value when the
// underlying Go field is a bool.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// i64toa wraps strconv.FormatInt for int64-backed numeric inputs (e.g.
// HLS buffer's byte ceilings, which are int64 in BridgeConfig.HLSBuffer).
// Sibling of itoaHelper, which is int-only.
func i64toa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// passwordPlaceholder returns the placeholder string for the SSH
// password input: "••••••••" when a password is stored, "not set"
// otherwise. The chassis renders the password field with value="" at
// all times, so the placeholder is the only operator signal that a
// password is configured.
func passwordPlaceholder(stored string) string {
	if stored != "" {
		return "••••••••"
	}
	return "not set"
}

// humanizeBytes formats an int64 byte count as a human-readable string
// using base-1024 (IEC) with SI-style suffixes ("KB"/"MB"/"GB"), matching
// the chassis mockup verbatim (e.g. 268435456 → "256 MB"). The
// technically-correct KiB/MiB/GiB suffixes are intentionally not used —
// operator familiarity wins over technical purity. Values under 1024 are
// rendered with the "B" suffix and no decimal. Fractional values render
// with one decimal place ("1.5 MB"); whole-unit values render integral
// ("1 MB" — not "1.0 MB").
func humanizeBytes(n int64) string {
	const (
		KB int64 = 1024
		MB       = KB * 1024
		GB       = MB * 1024
	)
	switch {
	case n < KB:
		return fmt.Sprintf("%d B", n)
	case n < MB:
		return formatBytesScale(n, KB, "KB")
	case n < GB:
		return formatBytesScale(n, MB, "MB")
	default:
		return formatBytesScale(n, GB, "GB")
	}
}

// formatBytesScale renders n/unit with one decimal place when the result
// is fractional, integral otherwise. Used by humanizeBytes.
func formatBytesScale(n, unit int64, suffix string) string {
	if n%unit == 0 {
		return fmt.Sprintf("%d %s", n/unit, suffix)
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(unit), suffix)
}

// errOfHelper returns the error message for a given field name from a
// settings errors map, or "" if absent or the map is nil.
func errOfHelper(errs map[string]string, name string) string {
	if errs == nil {
		return ""
	}
	return errs[name]
}

// settingsScopeLabelHelper uppercases a scope key ("hot" -> "HOT", etc.).
func settingsScopeLabelHelper(scope string) string {
	return strings.ToUpper(scope)
}

// stubPaneArgs is the option struct settings-drawer.html's stub template
// expects. ID maps to data-pane; Title is the section heading; Spec is
// the "4B" / "4C" label.
type stubPaneArgs struct {
	ID    string
	Title string
	Spec  string
}

// stubHelper constructs a stubPaneArgs for use in templates.
func stubHelper(id, title, spec string) stubPaneArgs {
	return stubPaneArgs{ID: id, Title: title, Spec: spec}
}

// fieldHelper renders one field row. The option bag (built by dict in
// templates) supports the keys: Name, Type, Label, Help, Value,
// Placeholder, Scope, Unit, Options, InputWidth, Error.
//
// All values are HTML-escaped. Type=switch renders a <button>, not an
// <input>; switches POST via client JS, not by form submission.
func fieldHelper(args map[string]any) template.HTML {
	get := func(key string) string {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	name := get("Name")
	typ := get("Type")
	label := get("Label")
	help := get("Help")
	value := get("Value")
	placeholder := get("Placeholder")
	scope := get("Scope")
	unit := get("Unit")
	inputWidth := get("InputWidth")
	errMsg := get("Error")

	rowClass := "field-row"
	if errMsg != "" {
		rowClass += " has-err"
	}

	// Label cell.
	var labelHTML string
	if help != "" {
		labelHTML = fmt.Sprintf(`<label>%s <span class="help">%s</span></label>`,
			html.EscapeString(label), html.EscapeString(help))
	} else {
		labelHTML = fmt.Sprintf(`<label>%s</label>`, html.EscapeString(label))
	}

	// Middle cell — type-specific.
	var middleHTML string
	switch typ {
	case "text", "path":
		extra := ""
		if typ == "path" {
			extra = " path"
		}
		hasValue := ""
		if value != "" {
			hasValue = " has-value"
		}
		middleHTML = fmt.Sprintf(`<input class="field-input%s%s" name="%s" value="%s" placeholder="%s">`,
			extra, hasValue,
			html.EscapeString(name), html.EscapeString(value), html.EscapeString(placeholder))
	case "number":
		hasValue := ""
		if value != "" {
			hasValue = " has-value"
		}
		style := ""
		if inputWidth != "" {
			style = fmt.Sprintf(` style="max-width:%s"`, html.EscapeString(inputWidth))
		}
		middleHTML = fmt.Sprintf(`<input class="field-input num%s" type="number" name="%s" value="%s"%s>`,
			hasValue, html.EscapeString(name), html.EscapeString(value), style)
	case "password":
		middleHTML = fmt.Sprintf(`<input class="field-input has-value" type="password" name="%s" value="%s">`,
			html.EscapeString(name), html.EscapeString(value))
	case "select":
		options, _ := args["Options"].([]map[string]any)
		var b strings.Builder
		fmt.Fprintf(&b, `<select class="field-input has-value" name="%s">`, html.EscapeString(name))
		for _, opt := range options {
			ov, _ := opt["Value"].(string)
			ol, _ := opt["Label"].(string)
			if ol == "" {
				ol = ov
			}
			selected := ""
			if ov == value {
				selected = " selected"
			}
			fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`,
				html.EscapeString(ov), selected, html.EscapeString(ol))
		}
		b.WriteString(`</select>`)
		middleHTML = b.String()
	case "switch":
		onClass := ""
		aria := "false"
		if value == "true" {
			onClass = " on"
			aria = "true"
		}
		middleHTML = fmt.Sprintf(`<button class="switch%s" data-field="%s" type="button" aria-pressed="%s"></button>`,
			onClass, html.EscapeString(name), aria)
	default:
		middleHTML = fmt.Sprintf(`<!-- unknown field type %q -->`, html.EscapeString(typ))
	}

	// Number-with-unit wraps the input + scope badge in a .row-end span;
	// other types put the scope badge as a direct row child.
	scopeHTML := fmt.Sprintf(`<span class="scope %s">%s</span>`, html.EscapeString(scope), strings.ToUpper(scope))
	if typ == "number" && unit != "" {
		middleHTML = fmt.Sprintf(`%s<span class="row-end"><span style="font-size:10px;color:var(--vfd-faded);">%s</span>%s</span>`,
			middleHTML, html.EscapeString(unit), scopeHTML)
		scopeHTML = "" // already inside row-end
	}

	errHTML := ""
	if errMsg != "" {
		errHTML = fmt.Sprintf(`<div class="field-err">%s</div>`, html.EscapeString(errMsg))
	}

	return template.HTML(fmt.Sprintf(`<div class="%s">%s%s%s%s</div>`,
		rowClass, labelHTML, middleHTML, errHTML, scopeHTML))
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
