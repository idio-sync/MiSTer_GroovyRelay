package localfiles

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type Config struct {
	Enabled   bool      `toml:"enabled"`
	Libraries []Library `toml:"library"`
}

type Library struct {
	Name string `toml:"name"`
	Root string `toml:"root"`
}

func DefaultConfig() Config {
	return Config{}
}

func decodeConfig(raw toml.Primitive, meta toml.MetaData) (Config, error) {
	cfg := DefaultConfig()
	if isZeroPrimitive(raw) {
		return cfg, nil
	}
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	var errs adapters.FieldErrors
	seen := map[string]struct{}{}
	for i, lib := range cfg.Libraries {
		prefix := "library." + itoa(i)
		name := strings.TrimSpace(lib.Name)
		if name == "" {
			errs = append(errs, adapters.FieldError{Key: prefix + ".name", Msg: "name is required"})
		} else {
			folded := strings.ToLower(name)
			if _, ok := seen[folded]; ok {
				errs = append(errs, adapters.FieldError{Key: prefix + ".name", Msg: fmt.Sprintf("duplicate library name %q", name)})
			}
			seen[folded] = struct{}{}
		}

		root := strings.TrimSpace(lib.Root)
		if root == "" {
			errs = append(errs, adapters.FieldError{Key: prefix + ".root", Msg: "root is required"})
			continue
		}
		if err := validateLibraryRoot(root); err != nil {
			errs = append(errs, adapters.FieldError{Key: prefix + ".root", Msg: err.Error()})
		}
	}
	return errs.Err()
}

func isZeroPrimitive(raw toml.Primitive) bool {
	return reflect.ValueOf(raw).IsZero()
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func validateLibraryRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("root does not exist")
		}
		return fmt.Errorf("root cannot be inspected: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("root must be a directory")
	}
	f, err := os.Open(root)
	if err != nil {
		return fmt.Errorf("root is not readable: %w", err)
	}
	defer f.Close()
	if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("root is not readable: %w", err)
	}
	return nil
}
