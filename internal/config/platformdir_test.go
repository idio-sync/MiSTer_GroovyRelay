package config

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDefaultDataDirFor(t *testing.T) {
	home := func() (string, error) { return filepath.FromSlash("/home/jake"), nil }
	noHome := func() (string, error) { return "", errors.New("no home") }

	cases := []struct {
		name string
		goos string
		env  map[string]string
		home func() (string, error)
		want string
	}{
		{
			name: "windows appdata", goos: "windows",
			env: map[string]string{"APPDATA": `C:\Users\Jake\AppData\Roaming`}, home: noHome,
			want: filepath.Join(`C:\Users\Jake\AppData\Roaming`, "mister-groovy-relay"),
		},
		{
			name: "windows userprofile fallback", goos: "windows",
			env: map[string]string{"USERPROFILE": `C:\Users\Jake`}, home: noHome,
			want: filepath.Join(`C:\Users\Jake`, "AppData", "Roaming", "mister-groovy-relay"),
		},
		{
			name: "darwin", goos: "darwin", home: home,
			want: filepath.Join(filepath.FromSlash("/home/jake"), "Library", "Application Support", "mister-groovy-relay"),
		},
		{
			name: "linux xdg", goos: "linux",
			env: map[string]string{"XDG_CONFIG_HOME": filepath.FromSlash("/cfg")}, home: home,
			want: filepath.Join(filepath.FromSlash("/cfg"), "mister-groovy-relay"),
		},
		{
			name: "linux home", goos: "linux", home: home,
			want: filepath.Join(filepath.FromSlash("/home/jake"), ".config", "mister-groovy-relay"),
		},
		{name: "no default", goos: "linux", home: noHome, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			if tc.home == nil {
				tc.home = noHome
			}
			got := defaultDataDirFor(tc.goos, getenv, tc.home)
			if got != tc.want {
				t.Fatalf("defaultDataDirFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveDataDirBlankUsesDefault(t *testing.T) {
	got, err := ResolveDataDir("")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	if got == "" {
		t.Fatal("ResolveDataDir returned blank default")
	}
}
