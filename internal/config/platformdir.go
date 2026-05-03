package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DefaultDataDir returns the per-platform directory for bridge state. Empty
// means the current environment does not expose enough information to derive
// a safe default.
func DefaultDataDir() string {
	return defaultDataDirFor(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func defaultDataDirFor(goos string, getenv func(string) string, userHomeDir func() (string, error)) string {
	switch goos {
	case "windows":
		if appdata := getenv("APPDATA"); appdata != "" {
			return filepath.Join(appdata, "mister-groovy-relay")
		}
		if up := getenv("USERPROFILE"); up != "" {
			return filepath.Join(up, "AppData", "Roaming", "mister-groovy-relay")
		}
	case "darwin":
		if home, err := userHomeDir(); err == nil && home != "" {
			return filepath.Join(home, "Library", "Application Support", "mister-groovy-relay")
		}
	default:
		if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "mister-groovy-relay")
		}
		if home, err := userHomeDir(); err == nil && home != "" {
			return filepath.Join(home, ".config", "mister-groovy-relay")
		}
	}
	return ""
}

// DefaultConfigPath returns the per-platform default config.toml path.
func DefaultConfigPath() string {
	if d := DefaultDataDir(); d != "" {
		return filepath.Join(d, "config.toml")
	}
	return ""
}

// ResolveDataDir applies the platform default for blank data_dir values.
func ResolveDataDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	if d := DefaultDataDir(); d != "" {
		return d, nil
	}
	return "", fmt.Errorf("bridge.data_dir is empty and no platform default is available; set bridge.data_dir explicitly")
}

func defaultDataDirForConfigWrite() (string, error) {
	if os.Getenv("MISTER_GROOVY_RUNTIME") == "docker" {
		return "/config", nil
	}
	return ResolveDataDir("")
}
