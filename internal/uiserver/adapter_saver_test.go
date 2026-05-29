package uiserver

import (
	"context"
	"strings"
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
