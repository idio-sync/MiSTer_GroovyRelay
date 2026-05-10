package uiserver

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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
