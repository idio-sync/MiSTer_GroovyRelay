package localfiles

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestDefaultConfigDisabled(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
	if len(cfg.Libraries) != 0 {
		t.Fatalf("Libraries = %v, want empty", cfg.Libraries)
	}
}

func TestDecodeConfigZeroPrimitive(t *testing.T) {
	if !isZeroPrimitive(toml.Primitive{}) {
		t.Fatalf("isZeroPrimitive = false, want true for zero toml.Primitive")
	}

	cfg, err := decodeConfig(toml.Primitive{}, toml.MetaData{})
	if err != nil {
		t.Fatalf("decodeConfig zero: %v", err)
	}
	if cfg.Enabled || len(cfg.Libraries) != 0 {
		t.Fatalf("cfg = %+v, want default", cfg)
	}
}

func TestDecodeConfigLibraries(t *testing.T) {
	root := t.TempDir()
	raw, meta := localfilesPrimitive(t, `
enabled = true
[[adapters.localfiles.library]]
name = "Movies"
root = "`+tomlEscape(root)+`"
`)
	cfg, err := decodeConfig(raw, meta)
	if err != nil {
		t.Fatalf("decodeConfig: %v", err)
	}
	if !cfg.Enabled || len(cfg.Libraries) != 1 {
		t.Fatalf("cfg = %+v, want enabled one library", cfg)
	}
	if cfg.Libraries[0].Name != "Movies" || cfg.Libraries[0].Root != root {
		t.Fatalf("library = %+v", cfg.Libraries[0])
	}
}

func TestValidateLibraries(t *testing.T) {
	root := t.TempDir()
	file := root + "/file.txt"
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	missing := root + "/missing"

	tests := []struct {
		name string
		cfg  Config
		key  string
		msg  string
	}{
		{
			name: "valid",
			cfg:  Config{Libraries: []Library{{Name: "Movies", Root: root}}},
		},
		{
			name: "empty name",
			cfg:  Config{Libraries: []Library{{Name: "", Root: root}}},
			key:  "library.0.name",
			msg:  "name",
		},
		{
			name: "duplicate folded name",
			cfg:  Config{Libraries: []Library{{Name: "Movies", Root: root}, {Name: "movies", Root: root}}},
			key:  "library.1.name",
			msg:  "duplicate",
		},
		{
			name: "empty root",
			cfg:  Config{Libraries: []Library{{Name: "Movies"}}},
			key:  "library.0.root",
			msg:  "root",
		},
		{
			name: "missing root",
			cfg:  Config{Libraries: []Library{{Name: "Movies", Root: missing}}},
			key:  "library.0.root",
			msg:  "exist",
		},
		{
			name: "file root",
			cfg:  Config{Libraries: []Library{{Name: "Movies", Root: file}}},
			key:  "library.0.root",
			msg:  "directory",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.key == "" {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate = nil, want error")
			}
			var ferrs adapters.FieldErrors
			if !errors.As(err, &ferrs) {
				t.Fatalf("error type = %T, want FieldErrors", err)
			}
			if !fieldErrorsContain(ferrs, tc.key, tc.msg) {
				t.Fatalf("FieldErrors = %v, want key %q msg containing %q", ferrs, tc.key, tc.msg)
			}
		})
	}
}

func TestValidateConfigAllowsEmptyReadableDirectory(t *testing.T) {
	cfg := Config{Libraries: []Library{{Name: "Empty", Root: t.TempDir()}}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate empty dir: %v", err)
	}
}

func TestValidateConfigRejectsUnreadableDirectoryWhenEnforced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read directories regardless of permission bits")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows permissions model does not support unreadable directories via chmod")
	}
	root := t.TempDir()
	locked := root + "/locked"
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	defer os.Chmod(locked, 0o755)

	err := (Config{Libraries: []Library{{Name: "Locked", Root: locked}}}).Validate()
	if err == nil {
		t.Fatalf("Validate unreadable = nil, want error")
	}
}

func fieldErrorsContain(errs adapters.FieldErrors, key, msg string) bool {
	for _, err := range errs {
		if err.Key == key && strings.Contains(strings.ToLower(err.Msg), strings.ToLower(msg)) {
			return true
		}
	}
	return false
}
