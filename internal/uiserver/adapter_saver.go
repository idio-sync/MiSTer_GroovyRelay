package uiserver

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// AdapterSaver replaces the [adapters.<name>] section of the on-disk
// config.toml with a new TOML snippet. Uses a line-level rewrite
// (replaceAdapterSection) rather than re-encoding the whole Sectioned
// — BurntSushi's encoder doesn't round-trip toml.Primitive values
// faithfully, so a full re-encode would lose adapter sections the UI
// doesn't currently touch.
type AdapterSaver struct {
	path string
	mu   *sync.Mutex // shared with BridgeSaver for same-file serialization
}

// NewAdapterSaver constructs an AdapterSaver that rewrites the given
// config path. Pass BridgeSaver.Mu() as the mutex so bridge + adapter
// saves serialize against each other; both paths read-modify-write the
// same file.
func NewAdapterSaver(path string, mu *sync.Mutex) *AdapterSaver {
	return &AdapterSaver{path: path, mu: mu}
}

func (r *AdapterSaver) Save(name string, rawTOMLSection []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	updated := replaceAdapterSection(data, name, rawTOMLSection)
	return config.WriteAtomic(r.path, updated)
}

// replaceAdapterSection rewrites (or appends) the [adapters.<name>]
// block inside doc. It removes the parent table and descendant adapter
// subtables before inserting the normalized replacement at the first
// removed location. Descendant removal matters for adapters that expose
// dynamic dotted keys through the generic UI: leaving old subtables behind
// can make the next TOML parse fail with duplicate definitions.
func replaceAdapterSection(doc []byte, name string, section []byte) []byte {
	section = bytes.TrimRight(section, "\r\n\t ")
	section = append(section, '\n')

	header := fmt.Sprintf("[adapters.%s]", name)
	descendantPrefix := fmt.Sprintf("[adapters.%s.", name)
	lines := strings.Split(string(doc), "\n")

	outLines := make([]string, 0, len(lines))
	inserted := false
	removedAny := false

	for i := 0; i < len(lines); {
		tr := strings.TrimSpace(lines[i])
		if tr == header || (strings.HasPrefix(tr, descendantPrefix) && strings.HasSuffix(tr, "]")) {
			if !inserted {
				outLines = append(outLines, header)
				outLines = append(outLines, strings.Split(strings.TrimRight(string(section), "\n"), "\n")...)
				inserted = true
			}
			removedAny = true
			i++
			for i < len(lines) {
				next := strings.TrimSpace(lines[i])
				if strings.HasPrefix(next, "[") && strings.HasSuffix(next, "]") {
					break
				}
				i++
			}
			continue
		}
		outLines = append(outLines, lines[i])
		i++
	}

	if !removedAny {
		// Append. Ensure doc ends with a newline before concatenating.
		out := strings.TrimRight(string(doc), "\r\n\t ") + "\n\n"
		out += header + "\n" + string(section)
		return []byte(out)
	}
	return []byte(strings.Join(outLines, "\n"))
}

// overlayTouched merges typed values from `touched` (string-encoded
// form values) onto `current` (the adapter's in-memory snapshot),
// using the FieldDef table for type dispatch. Returns the merged map
// plus per-field errors for any key that fails to decode or is not
// in the schema. The result is a fresh map; `current` is not mutated.
//
// Dotted-key keys (e.g. providers.foo.catalog_refresh_hours) are
// matched against schema entries whose Key uses the * wildcard
// (providers.*.catalog_refresh_hours). Wildcard-matching values are
// nested into a map-of-maps shape compatible with the BurntSushi
// TOML encoder.
func overlayTouched(current map[string]any, touched map[string]string, fields []adapters.FieldDef) (map[string]any, []adapters.FieldError) {
	out := cloneMap(current)
	var errs []adapters.FieldError
	for key, raw := range touched {
		fd, dotted, ok := matchFieldDef(fields, key)
		if !ok {
			errs = append(errs, adapters.FieldError{Key: key, Msg: "unknown field"})
			continue
		}
		if fd.Kind == adapters.KindAction {
			// Action buttons carry no value; skip silently rather than
			// treating them as an unsupported kind.
			continue
		}
		val, perr := decodeTouchedValue(raw, fd.Kind)
		if perr != "" {
			errs = append(errs, adapters.FieldError{Key: key, Msg: perr})
			continue
		}
		if dotted {
			if err := setDottedValue(out, key, val); err != nil {
				errs = append(errs, adapters.FieldError{Key: key, Msg: err.Error()})
			}
			continue
		}
		out[key] = val
	}
	return out, errs
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		// Deep-copies nested map[string]any; slices are copied by
		// reference (safe today — adapter CurrentValues() never nests
		// slices; revisit if that changes).
		if child, ok := v.(map[string]any); ok {
			out[k] = cloneMap(child)
			continue
		}
		out[k] = v
	}
	return out
}

// matchFieldDef returns the FieldDef matching key. Exact match wins;
// otherwise a wildcard FieldDef whose Key uses * in dotted segments
// (e.g. providers.*.catalog_refresh_hours) is matched against the
// dotted form of key. The dotted bool tells the caller whether to
// invoke setDottedValue or assign directly.
func matchFieldDef(fields []adapters.FieldDef, key string) (adapters.FieldDef, bool, bool) {
	for _, fd := range fields {
		if fd.Key == key {
			return fd, false, true
		}
	}
	for _, fd := range fields {
		if !strings.Contains(fd.Key, "*") {
			continue
		}
		if dottedKeyMatchesWildcard(key, fd.Key) {
			return fd, true, true
		}
	}
	return adapters.FieldDef{}, false, false
}

func dottedKeyMatchesWildcard(key, pattern string) bool {
	keyParts := strings.Split(key, ".")
	patParts := strings.Split(pattern, ".")
	if len(keyParts) != len(patParts) {
		return false
	}
	for i, p := range patParts {
		if p == "*" {
			continue
		}
		if p != keyParts[i] {
			return false
		}
	}
	return true
}

// decodeTouchedValue parses a string-encoded form value into the
// typed Go value the TOML encoder expects. Numeric kinds become
// int64; bool kind parses "true"/"false"; text kinds pass through.
func decodeTouchedValue(raw string, kind adapters.FieldKind) (any, string) {
	switch kind {
	case adapters.KindBool:
		switch raw {
		case "true":
			return true, ""
		case "false":
			return false, ""
		default:
			return nil, fmt.Sprintf("not a bool: %q", raw)
		}
	case adapters.KindInt:
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, fmt.Sprintf("not an integer: %q", raw)
		}
		return n, ""
	case adapters.KindText, adapters.KindSecret, adapters.KindEnum:
		return raw, ""
	default:
		return nil, fmt.Sprintf("unsupported kind: %v", kind)
	}
}

// setDottedValue assigns val at the dotted path in m, creating
// intermediate map[string]any nodes as needed.
func setDottedValue(m map[string]any, key string, val any) error {
	parts := strings.Split(key, ".")
	cur := m
	for i, p := range parts[:len(parts)-1] {
		child, ok := cur[p]
		if !ok {
			next := map[string]any{}
			cur[p] = next
			cur = next
			continue
		}
		nextMap, ok := child.(map[string]any)
		if !ok {
			return fmt.Errorf("path %q: segment %q is not a table", key, strings.Join(parts[:i+1], "."))
		}
		cur = nextMap
	}
	cur[parts[len(parts)-1]] = val
	return nil
}

// currentValuesOf returns the adapter's current in-memory values as a
// generic map[string]any, or false if the adapter does not implement
// the optional CurrentValues() method. This is the same duck-typed
// interface internal/ui consumes for form prefill — keeping it
// optional preserves backwards compatibility with adapters that only
// implement the core adapters.Adapter contract.
func currentValuesOf(a adapters.Adapter) (map[string]any, bool) {
	type currentValuer interface {
		CurrentValues() map[string]any
	}
	cv, ok := a.(currentValuer)
	if !ok {
		return nil, false
	}
	return cv.CurrentValues(), true
}

// encodeAdapterMap serializes a generic map[string]any into a TOML body
// for [adapters.<name>]. Top-level keys remain bare `key = value` lines;
// nested tables are rewritten from BurntSushi's relative [providers.foo]
// form to absolute [adapters.<name>.providers.foo] headers. That keeps
// replaceAdapterSection's existing contract (it inserts the parent header)
// while preserving descendant adapter subtables.
func encodeAdapterMap(name string, m map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("encode adapter map: %w", err)
	}
	return prefixAdapterSubtableHeaders(name, buf.Bytes()), nil
}

func prefixAdapterSubtableHeaders(name string, body []byte) []byte {
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inner := strings.Trim(trimmed, "[]")
			line = fmt.Sprintf("[adapters.%s.%s]", name, inner)
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

// decodeAdapterSection wraps a bare body snippet (key = value lines,
// no [adapters.<name>] header) in the appropriate header and decodes
// it into a toml.Primitive + MetaData handle the adapter's
// Validate() / ApplyConfig() methods can consume. Mirrors the same
// pattern internal/ui/adapter.go uses; lives in uiserver so the new
// SaveTouched method can call it without importing internal/ui.
func decodeAdapterSection(body []byte, name string) (toml.Primitive, toml.MetaData, error) {
	wrapper := fmt.Sprintf("[adapters.%s]\n%s", name, body)
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(wrapper, &envelope)
	if err != nil {
		return toml.Primitive{}, toml.MetaData{}, fmt.Errorf("decode adapter section %q: %w", name, err)
	}
	return envelope.Adapters[name], meta, nil
}

// readAdapterSectionMap reads the latest on-disk [adapters.<name>] table
// plus all [adapters.<name>.*] descendant tables into a generic map. This
// is the source of truth for SaveTouched overlays; CurrentValues() is only
// a fallback for a missing section, never the primary preservation path.
func readAdapterSectionMap(doc []byte, name string) (map[string]any, bool, error) {
	body, ok := extractAdapterSectionBody(doc, name)
	if !ok {
		return nil, false, nil
	}
	prim, meta, err := decodeAdapterSection(body, name)
	if err != nil {
		return nil, true, err
	}
	current := map[string]any{}
	if err := meta.PrimitiveDecode(prim, &current); err != nil {
		return nil, true, fmt.Errorf("decode current adapter section %q: %w", name, err)
	}
	return current, true, nil
}

func extractAdapterSectionBody(doc []byte, name string) ([]byte, bool) {
	parent := fmt.Sprintf("[adapters.%s]", name)
	descendantPrefix := fmt.Sprintf("[adapters.%s.", name)
	lines := strings.Split(string(doc), "\n")
	out := make([]string, 0)
	found := false
	for i := 0; i < len(lines); {
		tr := strings.TrimSpace(lines[i])
		if tr == parent || (strings.HasPrefix(tr, descendantPrefix) && strings.HasSuffix(tr, "]")) {
			found = true
			if tr != parent {
				out = append(out, lines[i])
			}
			i++
			for i < len(lines) {
				next := strings.TrimSpace(lines[i])
				if strings.HasPrefix(next, "[") && strings.HasSuffix(next, "]") {
					break
				}
				out = append(out, lines[i])
				i++
			}
			continue
		}
		i++
	}
	return []byte(strings.Join(out, "\n")), found
}
