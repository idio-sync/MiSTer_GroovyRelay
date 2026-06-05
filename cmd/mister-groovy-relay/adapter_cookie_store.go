package main

import (
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
)

// cookieAdapter is the exported cookie surface the URL adapter provides.
// Concrete url.CookiesStat in the signature means only the URL adapter
// matches — exactly the intent.
type cookieAdapter interface {
	ValidateCookies(raw []byte) error
	SaveCookies(raw []byte) (url.CookiesStat, error)
	ClearCookies() error
	CookieStat() (url.CookiesStat, bool, error)
}

// bridgeAdapterCookieStore satisfies chassis.AdapterCookieStore. It composes
// the URL adapter's exported cookie methods; the chassis route owns its own
// JSON envelope (the legacy /ui handlers are not reused).
type bridgeAdapterCookieStore struct {
	reg adapterLookup
}

func newBridgeAdapterCookieStore(reg adapterLookup) *bridgeAdapterCookieStore {
	return &bridgeAdapterCookieStore{reg: reg}
}

func (b *bridgeAdapterCookieStore) lookup(name string) (cookieAdapter, bool) {
	a, ok := b.reg.Get(name)
	if !ok {
		return nil, false
	}
	ca, ok := a.(cookieAdapter)
	return ca, ok
}

func (b *bridgeAdapterCookieStore) CookieStatus(name string) (chassis.CookieStatusView, bool) {
	ca, ok := b.lookup(name)
	if !ok {
		return chassis.CookieStatusView{}, false
	}
	stat, present, err := ca.CookieStat()
	if err != nil {
		// Wired but stat errored — present as not-loaded (the pill shows
		// "not loaded" rather than a hard error at paint time).
		return chassis.CookieStatusView{Loaded: false}, true
	}
	return cookieView(stat, present), true
}

func (b *bridgeAdapterCookieStore) SaveCookies(name, raw string) (chassis.CookieStatusView, error) {
	ca, ok := b.lookup(name)
	if !ok {
		return chassis.CookieStatusView{}, &cmdChipError{status: http.StatusNotFound, chip: "UNKNOWN ADAPTER"}
	}
	if err := ca.ValidateCookies([]byte(raw)); err != nil {
		// Bad user input → 400 field error keyed for the widget.
		return chassis.CookieStatusView{}, &cmdAdapterFieldErrors{
			errs: []adapters.FieldError{{Key: "cookies", Msg: err.Error()}},
		}
	}
	stat, err := ca.SaveCookies([]byte(raw))
	if err != nil {
		// Filesystem failure → 500 chip, no path/OS error echoed.
		return chassis.CookieStatusView{}, &cmdChipError{status: http.StatusInternalServerError, chip: "WRITE FAILED"}
	}
	return cookieView(stat, true), nil
}

func (b *bridgeAdapterCookieStore) ClearCookies(name string) (chassis.CookieStatusView, error) {
	ca, ok := b.lookup(name)
	if !ok {
		return chassis.CookieStatusView{}, &cmdChipError{status: http.StatusNotFound, chip: "UNKNOWN ADAPTER"}
	}
	if err := ca.ClearCookies(); err != nil {
		return chassis.CookieStatusView{}, &cmdChipError{status: http.StatusInternalServerError, chip: "WRITE FAILED"}
	}
	return chassis.CookieStatusView{Loaded: false}, nil
}

func cookieView(stat url.CookiesStat, present bool) chassis.CookieStatusView {
	if !present {
		return chassis.CookieStatusView{Loaded: false}
	}
	return chassis.CookieStatusView{
		Loaded: true,
		Bytes:  stat.Size,
		SetAt:  stat.Mtime.UTC().Format("2006-01-02 15:04:05Z"),
	}
}
