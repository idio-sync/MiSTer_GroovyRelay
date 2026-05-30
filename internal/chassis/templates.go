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

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
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
//   - options: constructs a []map[string]any from dict invocations, used to
//     build the Options arg the field helper consumes for select fields.
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
	"list":                    func(args ...string) []string { return args },
	"options":                 optionsHelper,
	"until":                   func(n int) []struct{} { return make([]struct{}, n) },
	"volumeAngle":             volumeAngle,
	"lower":                   strings.ToLower,
	"upper":                   strings.ToUpper,
	"dict":                    dictHelper,
	"itoa":                    itoaHelper,
	"errOf":                   errOfHelper,
	"settingsScopeLabel":      settingsScopeLabelHelper,
	"stub":                    stubHelper,
	"field":                   fieldHelper,
	"humanizeBytes":           humanizeBytes,
	"boolStr":                 boolStr,
	"i64toa":                  i64toa,
	"passwordPlaceholder":     passwordPlaceholder,
	"adapterPane":             adapterPane,
	"fieldKindWire":           fieldKindWire,
	"adapterScopeWire":        adapterScopeWire,
	"adapterFieldValue":       adapterFieldValue,
	"asInt64":                 asInt64,
	"isProviderOverrideField": isProviderOverrideField,
	"isStreamsBytesField":     isStreamsBytesField,
	"fieldByKey":              fieldByKey,
}

// adapterPane returns the AdapterPaneData for the named adapter from the
// SettingsData.Adapters slice. Returns a zero-value pane carrying only the
// name when the adapter is absent (offline test paths / unwired saver) so
// the sub-template renders an empty section rather than erroring.
func adapterPane(adapters []AdapterPaneData, name string) AdapterPaneData {
	for _, a := range adapters {
		if a.Name == name {
			return a
		}
	}
	return AdapterPaneData{Name: name}
}

// fieldByKey returns the FieldDef with the given key, or nil if absent.
// Used by the URL pane template to render specific standard fields in
// mockup order with the host editor interleaved.
func fieldByKey(fields []adapters.FieldDef, key string) *adapters.FieldDef {
	for i := range fields {
		if fields[i].Key == key {
			return &fields[i]
		}
	}
	return nil
}

// fieldKindWire maps an adapters.FieldKind to the Type token fieldHelper
// switches on ("switch"/"number"/"password"/"select"/"text").
func fieldKindWire(k adapters.FieldKind) string {
	switch k {
	case adapters.KindBool:
		return "switch"
	case adapters.KindInt:
		return "number"
	case adapters.KindSecret:
		return "password"
	case adapters.KindEnum:
		return "select"
	case adapters.KindText:
		return "text"
	}
	return "text"
}

// adapterScopeWire maps an adapters.ApplyScope to the chassis wire scope
// label ("hot"/"next"/"recast"/"reboot"). Unknown scopes degrade to "hot"
// so a forward-compatible field still renders a valid badge.
func adapterScopeWire(scope adapters.ApplyScope) string {
	label, ok := scopeLabel(scope)
	if !ok {
		return "hot"
	}
	return label
}

// adapterFieldValue stringifies a config value (sourced from the adapter
// saver's map[string]any) for the fieldHelper Value option. The kind hint
// lets bool values render as "true"/"false" for switch coercion.
func adapterFieldValue(v any, kind string) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatInt(int64(x), 10)
	}
	return ""
}

// asInt64 coerces a config byte-ceiling value to int64 for humanizeBytes.
// TOML integers decode as int64; the int / float64 arms guard test and
// JSON-roundtrip paths.
func asInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

// isProviderOverrideField reports whether a streams field key is a
// per-provider catalog-refresh override ("providers.<id>.catalog_refresh_hours").
// Those rows are rendered by the dedicated provider-override loop, not the
// generic field loop.
func isProviderOverrideField(key string) bool {
	return strings.HasPrefix(key, "providers.")
}

// isStreamsBytesField reports whether a streams field key is a byte-ceiling
// field that should render with a humanizeBytes row-end annotation.
func isStreamsBytesField(key string) bool {
	switch key {
	case "max_manifest_bytes", "max_catalog_bytes":
		return true
	}
	return false
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

// optionsHelper builds the []map[string]any shape fieldHelper expects for
// select Options. Templates call it as:
//
//	{{ options (dict "Value" "a") (dict "Value" "b" "Label" "Bee") }}
func optionsHelper(args ...map[string]any) []map[string]any {
	return args
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
	adapter := get("Adapter")
	rowEnd := get("RowEnd")
	skipEmpty, _ := args["SkipEmpty"].(bool)

	// Identity attribute: bridge fields are addressed by data-field (the field
	// key); adapter fields by data-adapter (the adapter name). Mutually
	// exclusive so the 4A bridge JS handlers (which select [data-field]) never
	// match adapter inputs, and the 4D adapter JS handlers (which select
	// [data-adapter]) never match bridge inputs.
	identAttr := fmt.Sprintf(` data-field="%s"`, html.EscapeString(name))
	if adapter != "" {
		identAttr = fmt.Sprintf(` data-adapter="%s"`, html.EscapeString(adapter))
	}

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
		middleHTML = fmt.Sprintf(`<input class="field-input%s%s" name="%s" value="%s" placeholder="%s"%s>`,
			extra, hasValue,
			html.EscapeString(name), html.EscapeString(value), html.EscapeString(placeholder), identAttr)
	case "number":
		hasValue := ""
		if value != "" {
			hasValue = " has-value"
		}
		style := ""
		if inputWidth != "" {
			style = fmt.Sprintf(` style="max-width:%s"`, html.EscapeString(inputWidth))
		}
		middleHTML = fmt.Sprintf(`<input class="field-input num%s" type="number" name="%s" value="%s"%s%s>`,
			hasValue, html.EscapeString(name), html.EscapeString(value), style, identAttr)
	case "password":
		// 4B password rendering: never echo the stored password into the
		// HTML response — render value="" always. Placeholder communicates
		// stored-vs-not-set state to the operator (passwordPlaceholder
		// helper picks "••••••••" or "not set"). has-value is omitted
		// because the input is always empty at server-render time;
		// the client JS adds has-value on operator input.
		skipAttr := ""
		if skipEmpty {
			skipAttr = ` data-skip-empty="true"`
		}
		middleHTML = fmt.Sprintf(`<input class="field-input" type="password" name="%s" value="" placeholder="%s"%s%s>`,
			html.EscapeString(name), html.EscapeString(placeholder), skipAttr, identAttr)
		_ = value // value is intentionally not used for password — preserve-on-empty lives in the server overlay
	case "select":
		options, _ := args["Options"].([]map[string]any)
		var b strings.Builder
		fmt.Fprintf(&b, `<select class="field-input has-value" name="%s"%s>`, html.EscapeString(name), identAttr)
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
		middleHTML = fmt.Sprintf(`<button class="switch%s" name="%s"%s type="button" aria-pressed="%s"></button>`,
			onClass, html.EscapeString(name), identAttr, aria)
	default:
		middleHTML = fmt.Sprintf(`<!-- unknown field type %q -->`, html.EscapeString(typ))
	}

	// Number-with-unit wraps the input + scope badge in a .row-end span;
	// other types put the scope badge as a direct row child. The RowEnd
	// option (e.g. humanizeBytes output for byte-ceiling fields) renders the
	// same .row-end wrapper; it takes precedence over Unit when both are set.
	scopeHTML := fmt.Sprintf(`<span class="scope %s">%s</span>`, html.EscapeString(scope), strings.ToUpper(scope))
	switch {
	case rowEnd != "":
		middleHTML = fmt.Sprintf(`%s<span class="row-end"><span style="font-size:10px;color:var(--vfd-faded);">%s</span>%s</span>`,
			middleHTML, html.EscapeString(rowEnd), scopeHTML)
		scopeHTML = "" // already inside row-end
	case typ == "number" && unit != "":
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
