package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestDefaultConfigTOML_SubstitutesDataDir(t *testing.T) {
	got, err := DefaultConfigTOML("/tmp/custom-data-dir")
	if err != nil {
		t.Fatalf("DefaultConfigTOML: %v", err)
	}
	if !strings.Contains(string(got), `data_dir = "/tmp/custom-data-dir"`) {
		t.Fatalf("expected data_dir = \"/tmp/custom-data-dir\" in output; got:\n%s", string(got))
	}
}

func TestDefaultConfigTOML_EmptyDataDirUsesPlatformDefault(t *testing.T) {
	got, err := DefaultConfigTOML("")
	if err != nil {
		t.Fatalf("DefaultConfigTOML(\"\"): %v", err)
	}
	// Output must NOT contain the literal `data_dir = ""` template marker;
	// empty input means "platform default" and the helper substitutes.
	if strings.Contains(string(got), `data_dir = ""`) {
		t.Fatalf("expected platform-default substitution; got literal empty marker:\n%s", string(got))
	}
}

func TestDefaultConfigTOML_ParsesAsSectioned(t *testing.T) {
	body, err := DefaultConfigTOML("/tmp/x")
	if err != nil {
		t.Fatalf("DefaultConfigTOML: %v", err)
	}
	var sec Sectioned
	if _, err := toml.Decode(string(body), &sec); err != nil {
		t.Fatalf("DefaultConfigTOML output does not parse via Sectioned: %v", err)
	}
	if err := sec.Validate(); err != nil {
		t.Fatalf("DefaultConfigTOML output fails Sectioned.Validate(): %v", err)
	}
}

// The embedded template becomes every first-run config.toml. It shipped with
// CRLF line endings, which broke the settings UI's per-adapter saves (issue
// #7); .gitattributes pins it to LF so no checkout re-embeds CRLF.
func TestExampleTOML_HasLFLineEndings(t *testing.T) {
	if strings.Contains(string(ExampleTOML()), "\r") {
		t.Fatal("example.toml contains CR characters; it must be LF-only (see .gitattributes)")
	}
}
