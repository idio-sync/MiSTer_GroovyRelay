package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed example.toml
var exampleTOML []byte

//go:embed example_sectioned.toml
var sectionedExampleTOML []byte

// ExampleTOML returns the bundled default config.toml content. Load uses
// it to seed a missing config file on first run so operators never have
// to hand-copy the example into their data_dir.
func ExampleTOML() []byte {
	b := make([]byte, len(exampleTOML))
	copy(b, exampleTOML)
	return b
}

// SectionedExampleTOML returns the bundled sectioned config.toml content.
// LoadSectioned uses it so the first-run file already matches the new
// schema instead of writing a flat legacy template and then migrating it.
func SectionedExampleTOML() []byte {
	b := make([]byte, len(sectionedExampleTOML))
	copy(b, sectionedExampleTOML)
	return b
}

// ErrConfigCreated is returned by Load when no config file existed at the
// requested path and a default one has just been written there. It is not
// a failure — main() uses it to print a friendly "edit this and restart"
// message and exit non-zero so the operator notices before the bridge
// starts with unconfigured defaults.
type ErrConfigCreated struct {
	Path string
}

func (e *ErrConfigCreated) Error() string {
	return fmt.Sprintf("wrote default config to %s — edit the MiSTer host and restart", e.Path)
}

// writeDefaultConfig creates parent dirs if needed and drops the embedded
// example at path with mode 0644. Tokens live in a separate file with
// 0600; the config is meant to be edited by the operator.
func writeDefaultConfig(path string) error {
	return writeEmbeddedConfig(path, exampleTOML)
}

// writeDefaultSectionedConfig creates parent dirs if needed and drops the
// sectioned embedded example at path with mode 0644.
func writeDefaultSectionedConfig(path string) error {
	return writeEmbeddedConfig(path, sectionedExampleTOML)
}

func writeEmbeddedConfig(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write default config: %w", err)
	}
	return nil
}
