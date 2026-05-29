package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

type adapterLookup interface {
	Get(name string) (adapters.Adapter, bool)
}

type bridgeAdapterSettingsSaver struct {
	saver *uiserver.AdapterSaver
	reg   adapterLookup
}

func newBridgeAdapterSettingsSaver(saver *uiserver.AdapterSaver, reg adapterLookup) *bridgeAdapterSettingsSaver {
	return &bridgeAdapterSettingsSaver{saver: saver, reg: reg}
}

func (b *bridgeAdapterSettingsSaver) Current(name string) (map[string]any, bool) {
	a, ok := b.reg.Get(name)
	if !ok {
		return nil, false
	}
	type currentValuer interface{ CurrentValues() map[string]any }
	cv, ok := a.(currentValuer)
	if !ok {
		return nil, false
	}
	return cv.CurrentValues(), true
}

func (b *bridgeAdapterSettingsSaver) Fields(name string) ([]adapters.FieldDef, bool) {
	a, ok := b.reg.Get(name)
	if !ok {
		return nil, false
	}
	return projectWritableSurface(name, a.Fields()), true
}

// projectWritableSurface filters adapter.Fields() to the 4D-owned subset.
// dlna, torrent: return as-is. streams: keep top-level fields, drop concrete
// provider rows (Catalog pane owns providers.<id>.disabled / hls_buffer_disabled),
// and append one wildcard providers.*.catalog_refresh_hours allowlist entry so
// only that per-provider key is writable from the Streams form pane.
func projectWritableSurface(name string, fields []adapters.FieldDef) []adapters.FieldDef {
	if name != "streams" {
		return fields
	}
	out := make([]adapters.FieldDef, 0, len(fields)+1) // fresh slice; do not alias the adapter's
	for _, fd := range fields {
		if strings.HasPrefix(fd.Key, "providers.") {
			continue
		}
		out = append(out, fd)
	}
	out = append(out, adapters.FieldDef{
		Key:        "providers.*.catalog_refresh_hours",
		Label:      "Catalog Refresh Hours",
		Kind:       adapters.KindInt,
		ApplyScope: adapters.ScopeHotSwap,
		Default:    int64(0),
	})
	return out
}

func (b *bridgeAdapterSettingsSaver) SaveTouched(name string, touched map[string]string) (string, error) {
	a, ok := b.reg.Get(name)
	if !ok {
		return "", &cmdChipError{status: http.StatusNotFound, chip: "UNKNOWN ADAPTER"}
	}
	fields := projectWritableSurface(name, a.Fields())
	scope, err := b.saver.SaveTouched(name, touched, a, fields)
	if err != nil {
		return "", translateSaverError(err)
	}
	label, labelOK := chassis.WireScopeLabel(scope)
	if !labelOK {
		return "", &cmdChipError{status: http.StatusInternalServerError, chip: "WRITE FAILED"}
	}
	return label, nil
}

func translateSaverError(err error) error {
	var feb fieldErrorBearer
	if errors.As(err, &feb) {
		return &cmdAdapterFieldErrors{errs: feb.FieldErrors()}
	}
	return &cmdChipError{status: http.StatusInternalServerError, chip: "WRITE FAILED"}
}

type fieldErrorBearer interface {
	error
	FieldErrors() []adapters.FieldError
}

type cmdChipError struct {
	status int
	chip   string
}

func (e *cmdChipError) Error() string   { return e.chip }
func (e *cmdChipError) StatusCode() int { return e.status }
func (e *cmdChipError) Chip() string    { return e.chip }

type cmdAdapterFieldErrors struct {
	errs []adapters.FieldError
}

func (e *cmdAdapterFieldErrors) Error() string                      { return "adapter field errors" }
func (e *cmdAdapterFieldErrors) FieldErrors() []adapters.FieldError { return e.errs }
