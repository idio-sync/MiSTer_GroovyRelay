package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/localfiles"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

func tomlEscape(s string) string {
	var out []rune
	for _, r := range s {
		if r == '\\' || r == '"' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}

func TestBridgeLocalFilesImplementsChassisInterfaces(t *testing.T) {
	var _ chassis.LocalFilesService = (*bridgeLocalFiles)(nil)
	var _ chassis.LocalFilesLibraryEditor = (*bridgeLocalFiles)(nil)
}

func TestBridgeLocalFilesBrowseMapsEntries(t *testing.T) {
	adapter := &fakeBridgeLocalFilesAdapter{
		browseEntries: []localfiles.BrowseEntry{{
			Name:      "Album",
			Rel:       "music/Album",
			IsDir:     true,
			Playable:  false,
			DurationS: 0,
			AudioOnly: false,
		}, {
			Name:      "song.flac",
			Rel:       "music/song.flac",
			IsDir:     false,
			Playable:  true,
			DurationS: 12.5,
			AudioOnly: true,
		}},
	}
	bridge := newBridgeLocalFiles(adapter, nil)

	entries, err := bridge.Browse(context.Background(), "Music", "music")
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if adapter.lastBrowseLib != "Music" || adapter.lastBrowseRel != "music" {
		t.Fatalf("adapter BrowseContext got lib=%q rel=%q, want Music/music", adapter.lastBrowseLib, adapter.lastBrowseRel)
	}
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(entries))
	}
	if entries[1].Name != "song.flac" || entries[1].Rel != "music/song.flac" || !entries[1].Playable || !entries[1].AudioOnly || entries[1].DurationS != 12.5 {
		t.Fatalf("mapped playable entry = %+v", entries[1])
	}
}

func TestBridgeLocalFilesCastDelegates(t *testing.T) {
	adapter := &fakeBridgeLocalFilesAdapter{}
	bridge := newBridgeLocalFiles(adapter, nil)

	if err := bridge.Cast(context.Background(), "Movies", "film.mkv"); err != nil {
		t.Fatalf("Cast: %v", err)
	}
	if adapter.lastCastLib != "Movies" || adapter.lastCastRel != "film.mkv" {
		t.Fatalf("adapter Cast got lib=%q rel=%q, want Movies/film.mkv", adapter.lastCastLib, adapter.lastCastRel)
	}
}

func TestBridgeLocalFilesSetLibrariesPersistsArrayOfTables(t *testing.T) {
	dir := t.TempDir()
	movies := t.TempDir()
	music := t.TempDir()
	cfgPath := dir + "/config.toml"
	if err := os.WriteFile(cfgPath, []byte(`[bridge]
mister.host = "x"

[adapters.localfiles]
enabled = true
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	adapter, err := localfiles.New(localfiles.AdapterConfig{})
	if err != nil {
		t.Fatalf("localfiles.New: %v", err)
	}
	bridge := newBridgeLocalFiles(adapter, uiserver.NewAdapterSaver(cfgPath, &sync.Mutex{}))

	scope, normalized, err := bridge.SetLibraries([]chassis.LocalFileLibraryRow{
		{Name: " Movies ", Root: " " + movies + " "},
		{Name: "Music", Root: music},
	})
	if err != nil {
		t.Fatalf("SetLibraries: %v", err)
	}
	if scope != "hot" {
		t.Errorf("scope = %q, want hot", scope)
	}
	if len(normalized) != 2 || normalized[0].Name != "Movies" || normalized[0].Root != movies || normalized[1].Name != "Music" || normalized[1].Root != music {
		t.Fatalf("normalized = %+v, want trimmed Movies/Music roots", normalized)
	}

	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(got)
	if strings.Count(text, "[[adapters.localfiles.library]]") != 2 {
		t.Fatalf("config.toml localfiles library tables count mismatch:\n%s", text)
	}
	if !strings.Contains(text, `name = "Movies"`) || !strings.Contains(text, `root = "`+tomlEscape(movies)+`"`) {
		t.Fatalf("config.toml missing Movies library:\n%s", text)
	}
	libs := bridge.Libraries()
	if len(libs) != 2 || libs[0].Name != "Movies" || libs[1].Root != music {
		t.Fatalf("Libraries after apply = %+v, want persisted rows reflected in adapter", libs)
	}
}

func TestBridgeLocalFilesSetLibrariesRejectsInvalidAndLeavesDiskUntouched(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	cfgPath := dir + "/config.toml"
	original := []byte(`[bridge]
mister.host = "x"

[adapters.localfiles]
enabled = true

[[adapters.localfiles.library]]
name = "Movies"
root = "` + tomlEscape(root) + `"
`)
	if err := os.WriteFile(cfgPath, original, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	adapter, err := localfiles.New(localfiles.AdapterConfig{})
	if err != nil {
		t.Fatalf("localfiles.New: %v", err)
	}
	bridge := newBridgeLocalFiles(adapter, uiserver.NewAdapterSaver(cfgPath, &sync.Mutex{}))

	_, _, err = bridge.SetLibraries([]chassis.LocalFileLibraryRow{{Name: " ", Root: root}})
	if err == nil {
		t.Fatal("SetLibraries accepted an empty library name; want field error")
	}
	var fieldErrs interface{ FieldErrors() []adapters.FieldError }
	if !errors.As(err, &fieldErrs) {
		t.Fatalf("err = %v (%T), want field errors", err, err)
	}
	if got := fieldErrs.FieldErrors()[0].Key; got != "library.0.name" {
		t.Fatalf("field error key = %q, want library.0.name", got)
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("config.toml changed after invalid save:\n%s", got)
	}
}

type fakeBridgeLocalFilesAdapter struct {
	browseEntries []localfiles.BrowseEntry
	browseErr     error
	castErr       error
	libraries     []localfiles.Library

	lastBrowseLib string
	lastBrowseRel string
	lastCastLib   string
	lastCastRel   string
}

func (f *fakeBridgeLocalFilesAdapter) Name() string            { return "localfiles" }
func (f *fakeBridgeLocalFilesAdapter) DisplayName() string     { return "Local Files" }
func (f *fakeBridgeLocalFilesAdapter) Status() adapters.Status { return adapters.Status{} }
func (f *fakeBridgeLocalFilesAdapter) IsEnabled() bool         { return true }
func (f *fakeBridgeLocalFilesAdapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{{Key: "enabled", Kind: adapters.KindBool, ApplyScope: adapters.ScopeHotSwap}}
}
func (f *fakeBridgeLocalFilesAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (f *fakeBridgeLocalFilesAdapter) Validate(toml.Primitive, toml.MetaData) error     { return nil }
func (f *fakeBridgeLocalFilesAdapter) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}
func (f *fakeBridgeLocalFilesAdapter) Start(context.Context) error { return nil }
func (f *fakeBridgeLocalFilesAdapter) Stop() error                 { return nil }
func (f *fakeBridgeLocalFilesAdapter) CurrentLibraries() []localfiles.Library {
	out := make([]localfiles.Library, len(f.libraries))
	copy(out, f.libraries)
	return out
}
func (f *fakeBridgeLocalFilesAdapter) BrowseContext(_ context.Context, lib, rel string) ([]localfiles.BrowseEntry, error) {
	f.lastBrowseLib = lib
	f.lastBrowseRel = rel
	if f.browseErr != nil {
		return nil, f.browseErr
	}
	out := make([]localfiles.BrowseEntry, len(f.browseEntries))
	copy(out, f.browseEntries)
	return out, nil
}
func (f *fakeBridgeLocalFilesAdapter) Cast(_ context.Context, lib, rel string) error {
	f.lastCastLib = lib
	f.lastCastRel = rel
	return f.castErr
}
