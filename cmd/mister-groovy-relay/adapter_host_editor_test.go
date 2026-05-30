package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

// fakeURLAdapter implements adapters.Adapter + the host/validate surface
// the host editor wrapper duck-types.
type fakeURLAdapter struct {
	mu    sync.Mutex
	hosts []string
}

func (f *fakeURLAdapter) Name() string            { return "url" }
func (f *fakeURLAdapter) DisplayName() string     { return "URL" }
func (f *fakeURLAdapter) Status() adapters.Status { return adapters.Status{} }
func (f *fakeURLAdapter) IsEnabled() bool         { return true }
func (f *fakeURLAdapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{{Key: "enabled", Kind: adapters.KindBool}}
}
func (f *fakeURLAdapter) CurrentValues() map[string]any                    { return map[string]any{"enabled": true} }
func (f *fakeURLAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (f *fakeURLAdapter) Validate(toml.Primitive, toml.MetaData) error     { return nil }
func (f *fakeURLAdapter) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}
func (f *fakeURLAdapter) Start(context.Context) error { return nil }
func (f *fakeURLAdapter) Stop() error                 { return nil }
func (f *fakeURLAdapter) CurrentHosts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.hosts))
	copy(out, f.hosts)
	return out
}

func TestBridgeAdapterHostEditor_SetHostsNormalizesAndPersists(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	if err := os.WriteFile(cfgPath, []byte(`[bridge]
mister.host = "x"

[adapters.url]
enabled = true
ytdlp_hosts = ["youtube.com"]
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	saver := uiserver.NewAdapterSaver(cfgPath, &sync.Mutex{})
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"url": &fakeURLAdapter{hosts: []string{"youtube.com"}}}}
	ed := newBridgeAdapterHostEditor(saver, reg)

	scope, normalized, err := ed.SetHosts("url", []string{"  YouTube.com ", "Twitch.TV"})
	if err != nil {
		t.Fatalf("SetHosts: %v", err)
	}
	if scope != "hot" {
		t.Errorf("scope = %q, want hot", scope)
	}
	if len(normalized) != 2 || normalized[0] != "youtube.com" || normalized[1] != "twitch.tv" {
		t.Errorf("normalized = %v, want [youtube.com twitch.tv]", normalized)
	}
	got, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(got), `"twitch.tv"`) {
		t.Errorf("config.toml missing normalized host:\n%s", got)
	}
}

func TestBridgeAdapterHostEditor_SetHostsRejectsBadHost(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	_ = os.WriteFile(cfgPath, []byte("[bridge]\nmister.host = \"x\"\n\n[adapters.url]\nenabled = true\n"), 0o600)
	saver := uiserver.NewAdapterSaver(cfgPath, &sync.Mutex{})
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"url": &fakeURLAdapter{}}}
	ed := newBridgeAdapterHostEditor(saver, reg)

	_, _, err := ed.SetHosts("url", []string{"https://bad/"})
	if err == nil {
		t.Fatal("SetHosts accepted bad host; want field error")
	}
	feb, ok := err.(interface{ FieldErrors() []adapters.FieldError })
	if !ok {
		t.Fatalf("err type = %T, want FieldErrors bearer", err)
	}
	if feb.FieldErrors()[0].Key != "hosts" {
		t.Errorf("field error key = %q, want hosts", feb.FieldErrors()[0].Key)
	}
}

func TestBridgeAdapterHostEditor_UnknownAdapter(t *testing.T) {
	saver := uiserver.NewAdapterSaver(t.TempDir()+"/config.toml", &sync.Mutex{})
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{}}
	ed := newBridgeAdapterHostEditor(saver, reg)
	_, _, err := ed.SetHosts("url", nil)
	ce, ok := err.(interface{ StatusCode() int })
	if !ok || ce.StatusCode() != 404 {
		t.Fatalf("err = %v, want 404 chip error", err)
	}
}

func TestBridgeAdapterHostEditor_SetHostsRejectsNonHostAdapter(t *testing.T) {
	saver := uiserver.NewAdapterSaver(t.TempDir()+"/config.toml", &sync.Mutex{})
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{
		"streams": &fakeStreamsAdapter{current: map[string]any{"enabled": true}},
	}}
	ed := newBridgeAdapterHostEditor(saver, reg)
	_, _, err := ed.SetHosts("streams", []string{"youtube.com"})
	ce, ok := err.(interface {
		StatusCode() int
		Chip() string
	})
	if !ok || ce.StatusCode() != http.StatusNotFound || ce.Chip() != "UNKNOWN ADAPTER" {
		t.Fatalf("err = %v, want 404 UNKNOWN ADAPTER chip", err)
	}
}

func TestBridgeAdapterHostEditor_EmptyEntryReKeyedToHosts(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	_ = os.WriteFile(cfgPath, []byte("[bridge]\nmister.host = \"x\"\n\n[adapters.url]\nenabled = true\n"), 0o600)
	saver := uiserver.NewAdapterSaver(cfgPath, &sync.Mutex{})
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"url": &fakeURLAdapter{}}}
	ed := newBridgeAdapterHostEditor(saver, reg)
	// An empty-string entry triggers Validate's "entries must not be empty"
	// FieldError keyed ytdlp_hosts; the wrapper must re-key it to "hosts".
	_, _, err := ed.SetHosts("url", []string{""})
	feb, ok := err.(interface{ FieldErrors() []adapters.FieldError })
	if !ok {
		t.Fatalf("err type = %T, want field-error bearer", err)
	}
	if feb.FieldErrors()[0].Key != "hosts" {
		t.Errorf("field error key = %q, want hosts (re-keyed from ytdlp_hosts)", feb.FieldErrors()[0].Key)
	}
}
