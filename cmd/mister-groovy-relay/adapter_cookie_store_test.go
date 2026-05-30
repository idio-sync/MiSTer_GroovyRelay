package main

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

const sampleNetscape = "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t1893456000\tSID\tx\n"

func newURLAdapter(t *testing.T) *url.Adapter {
	t.Helper()
	a, err := url.New(url.AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("url.New: %v", err)
	}
	return a
}

func TestBridgeAdapterCookieStore_SaveStatClear(t *testing.T) {
	a := newURLAdapter(t)
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"url": a}}
	store := newBridgeAdapterCookieStore(reg)

	// Initially not loaded.
	view, ok := store.CookieStatus("url")
	if !ok || view.Loaded {
		t.Fatalf("initial status = (%+v, %v), want not-loaded, ok", view, ok)
	}
	// Save.
	view, err := store.SaveCookies("url", sampleNetscape)
	if err != nil {
		t.Fatalf("SaveCookies: %v", err)
	}
	if !view.Loaded || view.Bytes != int64(len(sampleNetscape)) || view.SetAt == "" {
		t.Errorf("save view = %+v", view)
	}
	// Clear.
	view, err = store.ClearCookies("url")
	if err != nil {
		t.Fatalf("ClearCookies: %v", err)
	}
	if view.Loaded {
		t.Errorf("after clear Loaded = true, want false")
	}
}

func TestBridgeAdapterCookieStore_SaveRejectsGarbageAsFieldError(t *testing.T) {
	a := newURLAdapter(t)
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"url": a}}
	store := newBridgeAdapterCookieStore(reg)
	_, err := store.SaveCookies("url", "not cookies")
	feb, ok := err.(interface{ FieldErrors() []adapters.FieldError })
	if !ok {
		t.Fatalf("err type = %T, want field-error bearer", err)
	}
	if feb.FieldErrors()[0].Key != "cookies" {
		t.Errorf("field error key = %q, want cookies", feb.FieldErrors()[0].Key)
	}
}

func TestBridgeAdapterCookieStore_UnknownAdapter(t *testing.T) {
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{}}
	store := newBridgeAdapterCookieStore(reg)
	if _, ok := store.CookieStatus("url"); ok {
		t.Errorf("CookieStatus(unknown) ok=true, want false")
	}
	_, err := store.SaveCookies("url", sampleNetscape)
	ce, ok := err.(interface{ StatusCode() int })
	if !ok || ce.StatusCode() != 404 {
		t.Fatalf("SaveCookies(unknown) err = %v, want 404 chip", err)
	}
}

func TestBridgeAdapterCookieStore_NonCookieAdapter(t *testing.T) {
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{
		"streams": &fakeStreamsAdapter{current: map[string]any{"enabled": true}},
	}}
	store := newBridgeAdapterCookieStore(reg)
	if _, ok := store.CookieStatus("streams"); ok {
		t.Errorf("CookieStatus(non-cookie) ok=true, want false")
	}
	_, err := store.SaveCookies("streams", sampleNetscape)
	ce, ok := err.(interface {
		StatusCode() int
		Chip() string
	})
	if !ok || ce.StatusCode() != http.StatusNotFound || ce.Chip() != "UNKNOWN ADAPTER" {
		t.Fatalf("SaveCookies(non-cookie) err = %v, want 404 UNKNOWN ADAPTER", err)
	}
}

type fakeCookieAdapter struct {
	fakeStreamsAdapter
	saveErr  error
	clearErr error
}

func (f *fakeCookieAdapter) ValidateCookies(raw []byte) error { return nil }
func (f *fakeCookieAdapter) SaveCookies(raw []byte) (url.CookiesStat, error) {
	return url.CookiesStat{}, f.saveErr
}
func (f *fakeCookieAdapter) ClearCookies() error { return f.clearErr }
func (f *fakeCookieAdapter) CookieStat() (url.CookiesStat, bool, error) {
	return url.CookiesStat{}, false, nil
}

func TestBridgeAdapterCookieStore_FilesystemFailuresAreGenericChips(t *testing.T) {
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{
		"url": &fakeCookieAdapter{
			fakeStreamsAdapter: fakeStreamsAdapter{current: map[string]any{"enabled": true}},
			saveErr:            errors.New("/tmp/secret/url_cookies.txt: permission denied"),
			clearErr:           errors.New("/tmp/secret/url_cookies.txt: permission denied"),
		},
	}}
	store := newBridgeAdapterCookieStore(reg)
	for name, err := range map[string]error{
		"save":  func() error { _, err := store.SaveCookies("url", sampleNetscape); return err }(),
		"clear": func() error { _, err := store.ClearCookies("url"); return err }(),
	} {
		ce, ok := err.(interface {
			StatusCode() int
			Chip() string
		})
		if !ok || ce.StatusCode() != http.StatusInternalServerError || ce.Chip() != "WRITE FAILED" {
			t.Fatalf("%s err = %v, want 500 WRITE FAILED", name, err)
		}
		if strings.Contains(err.Error(), "/tmp/secret") {
			t.Fatalf("%s error leaked filesystem path: %v", name, err)
		}
	}
}
