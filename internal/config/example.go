package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed example.toml
var exampleTOML []byte

// ExampleTOML returns the bundled default config.toml content. Load uses
// it to seed a missing config file on first run so operators never have
// to hand-copy the example into their data_dir.
func ExampleTOML() []byte {
	b := make([]byte, len(exampleTOML))
	copy(b, exampleTOML)
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
	return fmt.Sprintf("wrote default config to %s — edit mister_host and restart", e.Path)
}

// writeDefaultConfig creates parent dirs if needed and drops the embedded
// example at path with mode 0644. Tokens live in a separate file with
// 0600; the config is meant to be edited by the operator.
func writeDefaultConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(path, exampleTOML, 0o644); err != nil {
		return fmt.Errorf("write default config: %w", err)
	}
	return nil
}
