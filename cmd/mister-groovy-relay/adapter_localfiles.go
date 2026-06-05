package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/localfiles"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

type localFilesAdapter interface {
	adapters.Adapter
	BrowseContext(ctx context.Context, libName, rel string) ([]localfiles.BrowseEntry, error)
	Cast(ctx context.Context, libName, rel string) error
	CurrentLibraries() []localfiles.Library
}

// bridgeLocalFiles satisfies the chassis Local Files contracts from cmd. The
// adapter owns browse/cast semantics; AdapterSaver owns library TOML writes.
type bridgeLocalFiles struct {
	adapter localFilesAdapter
	saver   *uiserver.AdapterSaver
}

func newBridgeLocalFiles(adapter localFilesAdapter, saver *uiserver.AdapterSaver) *bridgeLocalFiles {
	return &bridgeLocalFiles{adapter: adapter, saver: saver}
}

func (b *bridgeLocalFiles) Browse(ctx context.Context, lib, path string) ([]chassis.LocalFileEntry, error) {
	if b == nil || b.adapter == nil {
		return nil, &cmdChipError{status: http.StatusServiceUnavailable, chip: "NOT READY"}
	}
	entries, err := b.adapter.BrowseContext(ctx, lib, path)
	if err != nil {
		return nil, err
	}
	out := make([]chassis.LocalFileEntry, len(entries))
	for i, entry := range entries {
		out[i] = chassis.LocalFileEntry{
			Name:      entry.Name,
			Rel:       entry.Rel,
			IsDir:     entry.IsDir,
			Playable:  entry.Playable,
			DurationS: entry.DurationS,
			AudioOnly: entry.AudioOnly,
		}
	}
	return out, nil
}

func (b *bridgeLocalFiles) Cast(ctx context.Context, lib, path string) error {
	if b == nil || b.adapter == nil {
		return &cmdChipError{status: http.StatusServiceUnavailable, chip: "NOT READY"}
	}
	return b.adapter.Cast(ctx, lib, path)
}

func (b *bridgeLocalFiles) Libraries() []chassis.LocalFileLibraryRow {
	if b == nil || b.adapter == nil {
		return nil
	}
	libs := b.adapter.CurrentLibraries()
	out := make([]chassis.LocalFileLibraryRow, len(libs))
	for i, lib := range libs {
		out[i] = chassis.LocalFileLibraryRow{Name: lib.Name, Root: lib.Root}
	}
	return out
}

func (b *bridgeLocalFiles) SetLibraries(rows []chassis.LocalFileLibraryRow) (string, []chassis.LocalFileLibraryRow, error) {
	if b == nil || b.adapter == nil || b.saver == nil {
		return "", nil, &cmdChipError{status: http.StatusServiceUnavailable, chip: "NOT READY"}
	}
	normalized := normalizeLocalFileLibraryRows(rows)
	wire := make([]map[string]any, len(normalized))
	for i, row := range normalized {
		wire[i] = map[string]any{"name": row.Name, "root": row.Root}
	}
	scope, err := b.saver.SaveValues(adapterNameLocalFiles, map[string]any{"library": wire}, []string{"library"}, b.adapter)
	if err != nil {
		return "", nil, translateSaverError(err)
	}
	label, ok := chassis.WireLabelForScope(scope)
	if !ok {
		return "", nil, &cmdChipError{status: http.StatusInternalServerError, chip: "WRITE FAILED"}
	}
	return label, normalized, nil
}

func normalizeLocalFileLibraryRows(rows []chassis.LocalFileLibraryRow) []chassis.LocalFileLibraryRow {
	out := make([]chassis.LocalFileLibraryRow, len(rows))
	for i, row := range rows {
		out[i] = chassis.LocalFileLibraryRow{
			Name: strings.TrimSpace(row.Name),
			Root: strings.TrimSpace(row.Root),
		}
	}
	return out
}

const adapterNameLocalFiles = "localfiles"
