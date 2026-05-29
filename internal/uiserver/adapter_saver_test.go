package uiserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestReplaceAdapterSectionRemovesDescendantTables(t *testing.T) {
	doc := []byte(`
[bridge]
data_dir = "/tmp/mister"

[adapters.streams]
enabled = true
remote_provider_allowed_hosts = ["old.example"]

[adapters.streams.providers.mtv-rewind]
disabled = true
catalog_refresh_hours = 24

[adapters.url]
enabled = true
`)
	replacement := []byte(`
enabled = true
remote_provider_allowed_hosts = "trusted.example"
providers.mtv-rewind.disabled = false
providers.mtv-rewind.catalog_refresh_hours = 12
`)

	got := string(replaceAdapterSection(doc, "streams", replacement))
	if strings.Contains(got, "[adapters.streams.providers.mtv-rewind]") {
		t.Fatalf("old provider subtable still present:\n%s", got)
	}
	if !strings.Contains(got, "[adapters.url]\nenabled = true") {
		t.Fatalf("unrelated adapter section was not preserved:\n%s", got)
	}
	if _, err := toml.Decode(got, &struct{}{}); err != nil {
		t.Fatalf("rewritten TOML does not parse: %v\n%s", err, got)
	}
}

type fakeAdapterWithCurrent struct {
	values map[string]any
}

func (f *fakeAdapterWithCurrent) Name() string                { return "fake" }
func (f *fakeAdapterWithCurrent) DisplayName() string         { return "Fake" }
func (f *fakeAdapterWithCurrent) Status() adapters.Status     { return adapters.Status{} }
func (f *fakeAdapterWithCurrent) Fields() []adapters.FieldDef { return nil }
func (f *fakeAdapterWithCurrent) DecodeConfig(toml.Primitive, toml.MetaData) error {
	return nil
}
func (f *fakeAdapterWithCurrent) IsEnabled() bool             { return true }
func (f *fakeAdapterWithCurrent) Start(context.Context) error { return nil }
func (f *fakeAdapterWithCurrent) Stop() error                 { return nil }
func (f *fakeAdapterWithCurrent) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}
func (f *fakeAdapterWithCurrent) CurrentValues() map[string]any {
	return f.values
}

type fakeAdapterNoCurrent struct{}

func (f *fakeAdapterNoCurrent) Name() string                { return "no-current" }
func (f *fakeAdapterNoCurrent) DisplayName() string         { return "No Current" }
func (f *fakeAdapterNoCurrent) Status() adapters.Status     { return adapters.Status{} }
func (f *fakeAdapterNoCurrent) Fields() []adapters.FieldDef { return nil }
func (f *fakeAdapterNoCurrent) DecodeConfig(toml.Primitive, toml.MetaData) error {
	return nil
}
func (f *fakeAdapterNoCurrent) IsEnabled() bool             { return false }
func (f *fakeAdapterNoCurrent) Start(context.Context) error { return nil }
func (f *fakeAdapterNoCurrent) Stop() error                 { return nil }
func (f *fakeAdapterNoCurrent) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

func TestCurrentValuesOf_DuckTypeMatch(t *testing.T) {
	t.Parallel()
	a := &fakeAdapterWithCurrent{values: map[string]any{"enabled": true, "name": "X"}}
	got, ok := currentValuesOf(a)
	if !ok {
		t.Fatalf("currentValuesOf returned ok=false for adapter with CurrentValues")
	}
	if got["enabled"] != true || got["name"] != "X" {
		t.Errorf("got = %#v, want map with enabled=true, name=X", got)
	}
}

func TestCurrentValuesOf_NoMethod(t *testing.T) {
	t.Parallel()
	a := &fakeAdapterNoCurrent{}
	_, ok := currentValuesOf(a)
	if ok {
		t.Errorf("currentValuesOf returned ok=true for adapter without CurrentValues; want false")
	}
}

func TestOverlayTouched_BoolField(t *testing.T) {
	t.Parallel()
	current := map[string]any{"enabled": false, "name": "M"}
	touched := map[string]string{"enabled": "true"}
	fields := []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool},
		{Key: "name", Kind: adapters.KindText},
	}
	got, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) != 0 {
		t.Fatalf("ferrs = %v, want none", ferrs)
	}
	if got["enabled"] != true {
		t.Errorf("enabled = %v, want true", got["enabled"])
	}
	if got["name"] != "M" {
		t.Errorf("name = %v, want unchanged 'M'", got["name"])
	}
}

func TestOverlayTouched_IntField(t *testing.T) {
	t.Parallel()
	current := map[string]any{"port": int64(32100)}
	touched := map[string]string{"port": "32200"}
	fields := []adapters.FieldDef{{Key: "port", Kind: adapters.KindInt}}
	got, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) != 0 {
		t.Fatalf("ferrs = %v, want none", ferrs)
	}
	if got["port"] != int64(32200) {
		t.Errorf("port = %v (%T), want int64(32200)", got["port"], got["port"])
	}
}

func TestOverlayTouched_BadInt(t *testing.T) {
	t.Parallel()
	current := map[string]any{"port": int64(32100)}
	touched := map[string]string{"port": "not-a-number"}
	fields := []adapters.FieldDef{{Key: "port", Kind: adapters.KindInt}}
	_, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) == 0 {
		t.Fatalf("ferrs empty, want one entry for 'port'")
	}
	if ferrs[0].Key != "port" {
		t.Errorf("ferrs[0].Key = %q, want 'port'", ferrs[0].Key)
	}
}

func TestOverlayTouched_UnknownKey(t *testing.T) {
	t.Parallel()
	current := map[string]any{"enabled": true}
	touched := map[string]string{"unknown": "x"}
	fields := []adapters.FieldDef{{Key: "enabled", Kind: adapters.KindBool}}
	_, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) == 0 {
		t.Fatalf("ferrs empty, want one entry for unknown key")
	}
	if ferrs[0].Key != "unknown" {
		t.Errorf("ferrs[0].Key = %q, want 'unknown'", ferrs[0].Key)
	}
}

func TestOverlayTouched_DottedProviderKey(t *testing.T) {
	t.Parallel()
	current := map[string]any{
		"enabled":   true,
		"providers": map[string]any{},
	}
	touched := map[string]string{"providers.foo.catalog_refresh_hours": "12"}
	fields := []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool},
		{Key: "providers.*.catalog_refresh_hours", Kind: adapters.KindInt},
	}
	got, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) != 0 {
		t.Fatalf("ferrs = %v, want none", ferrs)
	}
	providers, ok := got["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers not a map: %#v", got["providers"])
	}
	foo, ok := providers["foo"].(map[string]any)
	if !ok {
		t.Fatalf("providers.foo not a map: %#v", providers["foo"])
	}
	if foo["catalog_refresh_hours"] != int64(12) {
		t.Errorf("providers.foo.catalog_refresh_hours = %v, want 12", foo["catalog_refresh_hours"])
	}
}

func TestOverlayTouched_DottedCollision(t *testing.T) {
	t.Parallel()
	current := map[string]any{"providers": "not-a-map"}
	touched := map[string]string{"providers.foo.catalog_refresh_hours": "12"}
	fields := []adapters.FieldDef{
		{Key: "providers.*.catalog_refresh_hours", Kind: adapters.KindInt},
	}
	_, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) == 0 {
		t.Fatalf("ferrs empty, want one entry for collision on non-table segment")
	}
	if ferrs[0].Key != "providers.foo.catalog_refresh_hours" {
		t.Errorf("ferrs[0].Key = %q, want 'providers.foo.catalog_refresh_hours'", ferrs[0].Key)
	}
}

func TestOverlayTouched_DoesNotMutateCurrent(t *testing.T) {
	t.Parallel()
	current := map[string]any{
		"enabled": false,
		"providers": map[string]any{
			"foo": map[string]any{"catalog_refresh_hours": int64(6)},
		},
	}
	touched := map[string]string{
		"enabled":                             "true",
		"providers.foo.catalog_refresh_hours": "12",
	}
	fields := []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool},
		{Key: "providers.*.catalog_refresh_hours", Kind: adapters.KindInt},
	}
	got, ferrs := overlayTouched(current, touched, fields)
	if len(ferrs) != 0 {
		t.Fatalf("ferrs = %v, want none", ferrs)
	}
	// Sanity: the returned map reflects the changes.
	if got["enabled"] != true {
		t.Errorf("got enabled = %v, want true", got["enabled"])
	}
	// The original current must be untouched at both levels.
	if current["enabled"] != false {
		t.Errorf("current[enabled] mutated to %v, want false", current["enabled"])
	}
	origProviders, ok := current["providers"].(map[string]any)
	if !ok {
		t.Fatalf("current providers not a map: %#v", current["providers"])
	}
	origFoo, ok := origProviders["foo"].(map[string]any)
	if !ok {
		t.Fatalf("current providers.foo not a map: %#v", origProviders["foo"])
	}
	if origFoo["catalog_refresh_hours"] != int64(6) {
		t.Errorf("current providers.foo.catalog_refresh_hours mutated to %v, want 6", origFoo["catalog_refresh_hours"])
	}
}

func TestEncodeAdapterMap_TopLevelFields(t *testing.T) {
	t.Parallel()
	merged := map[string]any{
		"enabled":     true,
		"device_name": "MiSTer",
		"port":        int64(32100),
	}
	got, err := encodeAdapterMap("dlna", merged)
	if err != nil {
		t.Fatalf("encodeAdapterMap err = %v", err)
	}
	for _, want := range []string{
		`enabled = true`,
		`device_name = "MiSTer"`,
		`port = 32100`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("encoded does not contain %q\nencoded:\n%s", want, got)
		}
	}
}

func TestEncodeAdapterMap_NestedProviders(t *testing.T) {
	t.Parallel()
	merged := map[string]any{
		"enabled": true,
		"providers": map[string]any{
			"foo": map[string]any{"catalog_refresh_hours": int64(12)},
		},
	}
	got, err := encodeAdapterMap("streams", merged)
	if err != nil {
		t.Fatalf("encodeAdapterMap err = %v", err)
	}
	for _, want := range []string{
		`enabled = true`,
		`[adapters.streams.providers.foo]`,
		`catalog_refresh_hours = 12`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("encoded does not contain %q\nencoded:\n%s", want, got)
		}
	}
}

func TestDecodeAdapterSection_RoundTrip(t *testing.T) {
	t.Parallel()
	snippet := []byte("enabled = true\ndevice_name = \"MiSTer\"\n")
	prim, meta, err := decodeAdapterSection(snippet, "dlna")
	if err != nil {
		t.Fatalf("decodeAdapterSection err = %v", err)
	}
	var got struct {
		Enabled    bool   `toml:"enabled"`
		DeviceName string `toml:"device_name"`
	}
	if err := meta.PrimitiveDecode(prim, &got); err != nil {
		t.Fatalf("PrimitiveDecode err = %v", err)
	}
	if got.Enabled != true || got.DeviceName != "MiSTer" {
		t.Errorf("decoded = %+v", got)
	}
}

// fakeFullAdapter implements adapters.Adapter plus CurrentValues; it
// records the ApplyConfig call for assertions and reports a fixed
// scope from ApplyConfig. The `fields` slice is per-test so Task 7's
// nested-subtable preservation test can inject a wildcard schema
// (providers.*.catalog_refresh_hours) without retrofitting Tasks 5/6.
type fakeFullAdapter struct {
	mu        sync.Mutex
	values    map[string]any
	fields    []adapters.FieldDef
	scope     adapters.ApplyScope
	validErr  error
	applyErr  error
	applied   []map[string]any
	applyHook func(decoded map[string]any) // used by Task 8
}

func (f *fakeFullAdapter) Name() string            { return "fake" }
func (f *fakeFullAdapter) DisplayName() string     { return "Fake" }
func (f *fakeFullAdapter) Status() adapters.Status { return adapters.Status{} }
func (f *fakeFullAdapter) IsEnabled() bool         { return false }
func (f *fakeFullAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (f *fakeFullAdapter) Start(context.Context) error                      { return nil }
func (f *fakeFullAdapter) Stop() error                                      { return nil }
func (f *fakeFullAdapter) Fields() []adapters.FieldDef {
	if f.fields != nil {
		return f.fields
	}
	return []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool},
		{Key: "device_name", Kind: adapters.KindText},
	}
}
func (f *fakeFullAdapter) CurrentValues() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]any, len(f.values))
	for k, v := range f.values {
		out[k] = v
	}
	return out
}
func (f *fakeFullAdapter) Validate(prim toml.Primitive, meta toml.MetaData) error {
	return f.validErr
}
func (f *fakeFullAdapter) ApplyConfig(prim toml.Primitive, meta toml.MetaData) (adapters.ApplyScope, error) {
	if f.applyErr != nil {
		return 0, f.applyErr
	}
	var decoded map[string]any
	_ = meta.PrimitiveDecode(prim, &decoded)
	f.mu.Lock()
	f.applied = append(f.applied, decoded)
	if f.applyHook != nil {
		f.applyHook(decoded)
	}
	f.mu.Unlock()
	return f.scope, nil
}

func newTempConfigWithSection(t *testing.T, name string, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	contents := fmt.Sprintf("[bridge]\nmister.host = \"x\"\n\n[adapters.%s]\n%s", name, body)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestSaveTouched_HappyPath(t *testing.T) {
	t.Parallel()
	path := newTempConfigWithSection(t, "dlna", `enabled = false
device_name = "Old"
`)
	mu := &sync.Mutex{}
	saver := NewAdapterSaver(path, mu)
	adapter := &fakeFullAdapter{
		values: map[string]any{"enabled": false, "device_name": "Old"},
		scope:  adapters.ScopeHotSwap,
	}
	scope, err := saver.SaveTouched("dlna", map[string]string{"enabled": "true"}, adapter, adapter.Fields())
	if err != nil {
		t.Fatalf("SaveTouched err = %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v, want ScopeHotSwap", scope)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(got), `enabled = true`) {
		t.Errorf("disk does not contain enabled = true:\n%s", got)
	}
	if !strings.Contains(string(got), `device_name = "Old"`) {
		t.Errorf("disk does not preserve device_name:\n%s", got)
	}
	if n := len(adapter.applied); n != 1 {
		t.Fatalf("ApplyConfig calls = %d, want 1", n)
	}
}
