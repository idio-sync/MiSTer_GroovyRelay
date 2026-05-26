package chassis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestDetectCastKind_URL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"https://example.com/video.mp4", "url"},
		{"  http://example.com/  ", "url"},
		{"HTTPS://example.com", "url"},
		{"magnet:?xt=urn:btih:abc", "magnet"},
		{"  MAGNET:?xt=urn:btih:abc  ", "magnet"},
		{"magnet:abc", "magnet"},
		{"ftp://example.com/x", ""},
		{"not-a-url", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := detectCastKind(tc.in, false)
			if got != tc.want {
				t.Errorf("detectCastKind(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDetectCastKind_FilePresentWinsOverPayload(t *testing.T) {
	t.Parallel()
	if got := detectCastKind("https://example.com/x.mp4", true); got != "file" {
		t.Errorf("detectCastKind with file=true = %q, want file", got)
	}
	if got := detectCastKind("", true); got != "file" {
		t.Errorf("detectCastKind empty payload + file=true = %q, want file", got)
	}
}

func TestWriteCastJSON_SuccessShape(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeCastJSON(rec, 200, true, "")
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if rec.Code != 200 {
		t.Errorf("Code = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if _, present := body["chip"]; present {
		t.Errorf("chip present on success body, want omitted")
	}
}

func TestWriteCastJSON_ErrorShapeIncludesChip(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeCastJSON(rec, 400, false, "BAD URL")
	if rec.Code != 400 {
		t.Errorf("Code = %d, want 400", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if body["ok"] != false || body["chip"] != "BAD URL" {
		t.Errorf("body = %+v, want {ok:false, chip:BAD URL}", body)
	}
}

func TestVerifyCastTabBindings_AllResolveAgainstRegistry(t *testing.T) {
	t.Parallel()
	reg := adapters.NewRegistry()
	if err := reg.Register(urlAdapterStub{}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(torrentAdapterStub{}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCastTabBindings(reg); err != nil {
		t.Fatalf("VerifyCastTabBindings: %v", err)
	}
}

func TestVerifyCastTabBindings_MissingAdapterFails(t *testing.T) {
	t.Parallel()
	reg := adapters.NewRegistry()
	if err := reg.Register(urlAdapterStub{}); err != nil {
		t.Fatal(err)
	}
	// No torrent adapter registered — castKindToTab["magnet"] -> torrent-magnet
	// should not resolve.
	err := VerifyCastTabBindings(reg)
	if err == nil {
		t.Fatal("VerifyCastTabBindings = nil, want missing-tab error")
	}
	if !strings.Contains(err.Error(), "torrent-magnet") {
		t.Errorf("err = %v, want mention of torrent-magnet", err)
	}
}

// urlAdapterStub is a minimal adapters.Adapter + QuickCastProvider for
// VerifyCastTabBindings tests. Real URL adapter behavior is unused.
type urlAdapterStub struct{}

func (urlAdapterStub) Name() string                                     { return "url" }
func (urlAdapterStub) DisplayName() string                              { return "URL" }
func (urlAdapterStub) Fields() []adapters.FieldDef                      { return nil }
func (urlAdapterStub) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (urlAdapterStub) IsEnabled() bool                                  { return true }
func (urlAdapterStub) Start(ctx context.Context) error                  { return nil }
func (urlAdapterStub) Stop() error                                      { return nil }
func (urlAdapterStub) Status() adapters.Status                          { return adapters.Status{} }
func (urlAdapterStub) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeNextCast, nil
}
func (urlAdapterStub) QuickCastTabs() []adapters.QuickCastTab {
	return []adapters.QuickCastTab{{
		ID:       "url",
		Enabled:  true,
		Encoding: adapters.QuickCastEncodingForm,
		Fields:   []adapters.QuickCastField{{Name: "url", Type: "url"}},
	}}
}
func (urlAdapterStub) HandleQuickCast(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	return adapters.QuickCastResult{}, nil
}

type torrentAdapterStub struct{}

func (torrentAdapterStub) Name() string                                     { return "torrent" }
func (torrentAdapterStub) DisplayName() string                              { return "Torrent" }
func (torrentAdapterStub) Fields() []adapters.FieldDef                      { return nil }
func (torrentAdapterStub) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (torrentAdapterStub) IsEnabled() bool                                  { return true }
func (torrentAdapterStub) Start(ctx context.Context) error                  { return nil }
func (torrentAdapterStub) Stop() error                                      { return nil }
func (torrentAdapterStub) Status() adapters.Status                          { return adapters.Status{} }
func (torrentAdapterStub) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeNextCast, nil
}
func (torrentAdapterStub) QuickCastTabs() []adapters.QuickCastTab {
	return []adapters.QuickCastTab{
		{
			ID:       "torrent-magnet",
			Enabled:  true,
			Encoding: adapters.QuickCastEncodingForm,
			Fields:   []adapters.QuickCastField{{Name: "magnet", Type: "text"}},
		},
		{
			ID:       "torrent-file",
			Enabled:  true,
			Encoding: adapters.QuickCastEncodingMultipart,
			Fields:   []adapters.QuickCastField{{Name: "torrent_file", Type: "file"}},
		},
	}
}
func (torrentAdapterStub) HandleQuickCast(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	return adapters.QuickCastResult{}, nil
}

// Ensure stubs implement the http.ResponseWriter-adjacent interfaces the
// test uses — Go compiler verifies these at compile time.
var _ adapters.Adapter = urlAdapterStub{}
var _ adapters.QuickCastProvider = urlAdapterStub{}
var _ adapters.Adapter = torrentAdapterStub{}
var _ adapters.QuickCastProvider = torrentAdapterStub{}

// Silence unused import warning for net/http (used by httptest).
var _ http.ResponseWriter
