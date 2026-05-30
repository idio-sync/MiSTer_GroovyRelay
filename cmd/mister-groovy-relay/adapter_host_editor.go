package main

import (
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

// bridgeAdapterHostEditor satisfies chassis.AdapterHostEditor from outside
// the chassis package. It normalizes the host list via the URL adapter's
// exported rules (the single normalization source), then persists the
// whole ytdlp_hosts array via the shared AdapterSaver. The saver is the
// sole validator+writer+applier; this wrapper does NOT call ApplyConfig.
type bridgeAdapterHostEditor struct {
	saver *uiserver.AdapterSaver
	reg   adapterLookup
}

func newBridgeAdapterHostEditor(saver *uiserver.AdapterSaver, reg adapterLookup) *bridgeAdapterHostEditor {
	return &bridgeAdapterHostEditor{saver: saver, reg: reg}
}

func (b *bridgeAdapterHostEditor) Hosts(name string) ([]string, bool) {
	a, ok := b.reg.Get(name)
	if !ok {
		return nil, false
	}
	h, ok := a.(interface{ CurrentHosts() []string })
	if !ok {
		return nil, false
	}
	return h.CurrentHosts(), true
}

func (b *bridgeAdapterHostEditor) SetHosts(name string, hosts []string) (string, []string, error) {
	a, ok := b.reg.Get(name)
	if !ok {
		return "", nil, &cmdChipError{status: http.StatusNotFound, chip: "UNKNOWN ADAPTER"}
	}
	if _, ok := a.(interface{ CurrentHosts() []string }); !ok {
		return "", nil, &cmdChipError{status: http.StatusNotFound, chip: "UNKNOWN ADAPTER"}
	}
	cleaned, err := url.NormalizeHosts(hosts)
	if err != nil {
		return "", nil, mapHostFieldErrors(err)
	}
	scope, err := b.saver.SaveValues(name, map[string]any{"ytdlp_hosts": cleaned}, []string{"ytdlp_hosts"}, a)
	if err != nil {
		return "", nil, translateSaverError(err)
	}
	label, ok := chassis.WireLabelForScope(scope)
	if !ok {
		return "", nil, &cmdChipError{status: http.StatusInternalServerError, chip: "WRITE FAILED"}
	}
	return label, cleaned, nil
}

// mapHostFieldErrors re-keys the adapter's "ytdlp_hosts" FieldErrors to the
// widget wire key "hosts" so the chassis envelope reads {errors:{hosts:...}}.
func mapHostFieldErrors(err error) error {
	if fe, ok := err.(adapters.FieldErrors); ok {
		out := make([]adapters.FieldError, 0, len(fe))
		for _, e := range fe {
			key := e.Key
			if key == "ytdlp_hosts" {
				key = "hosts"
			}
			out = append(out, adapters.FieldError{Key: key, Msg: e.Msg})
		}
		return &cmdAdapterFieldErrors{errs: out}
	}
	return &cmdAdapterFieldErrors{errs: []adapters.FieldError{{Key: "hosts", Msg: err.Error()}}}
}
