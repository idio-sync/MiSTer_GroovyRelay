package chassis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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
	// No torrent adapter registered — both castKindToTab["magnet"] and
	// ["file"] reference torrent-* tabs that won't resolve. We assert
	// only that the error names a torrent tab; which one is reported
	// first depends on sortedKeys order, which is an implementation
	// detail the test should not couple to.
	err := VerifyCastTabBindings(reg)
	if err == nil {
		t.Fatal("VerifyCastTabBindings = nil, want missing-tab error")
	}
	if !strings.Contains(err.Error(), "torrent-") {
		t.Errorf("err = %v, want mention of a torrent-* tab", err)
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

func TestHandleCastPost_URLSuccess(t *testing.T) {
	t.Parallel()
	calls := &recordedQuickCasts{}
	srv := newServerWithAdaptersForTest(t, calls)
	form := url.Values{}
	form.Set("kind", "url")
	form.Set("payload", "https://example.com/video.mp4")
	req := httptest.NewRequest(http.MethodPost, "/ui/cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := calls.last(); got.TabID != "url" || got.Values["url"] != "https://example.com/video.mp4" {
		t.Errorf("recorded = %+v, want TabID=url Values[url]=...", got)
	}
}

func TestHandleCastPost_MagnetRoutesToTorrent(t *testing.T) {
	t.Parallel()
	calls := &recordedQuickCasts{}
	srv := newServerWithAdaptersForTest(t, calls)
	form := url.Values{}
	form.Set("kind", "magnet")
	form.Set("payload", "magnet:?xt=urn:btih:abc")
	req := httptest.NewRequest(http.MethodPost, "/ui/cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := calls.last(); got.TabID != "torrent-magnet" {
		t.Errorf("TabID = %q, want torrent-magnet", got.TabID)
	}
	if got := calls.last(); got.Values["magnet"] != "magnet:?xt=urn:btih:abc" {
		t.Errorf("Values[magnet] = %q, want the magnet uri", got.Values["magnet"])
	}
}

func TestHandleCastPost_FileUploadPopulatesFile(t *testing.T) {
	t.Parallel()
	calls := &recordedQuickCasts{}
	srv := newServerWithAdaptersForTest(t, calls)
	body, contentType := makeMultipart(t, "torrent_file", "example.torrent", []byte("d8:announce..."))
	req := httptest.NewRequest(http.MethodPost, "/ui/cast", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := calls.last(); got.TabID != "torrent-file" || got.File == nil {
		t.Errorf("recorded = %+v, want TabID=torrent-file with File set", got)
	}
	if got := calls.last(); got.File.FieldName != "torrent_file" {
		t.Errorf("File.FieldName = %q, want torrent_file", got.File.FieldName)
	}
}

func TestHandleCastPost_FileUploadWrongFieldReturns400(t *testing.T) {
	t.Parallel()
	srv := newServerWithAdaptersForTest(t, &recordedQuickCasts{})
	body, contentType := makeMultipart(t, "file", "example.torrent", []byte("d8:announce..."))
	req := httptest.NewRequest(http.MethodPost, "/ui/cast", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Code = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCastPost_FileUploadTooLargeReturns413(t *testing.T) {
	t.Parallel()
	srv := newServerWithAdaptersForTest(t, &recordedQuickCasts{})
	body, contentType := makeMultipart(t, "torrent_file", "huge.torrent", bytes.Repeat([]byte("x"), adapters.MaxQuickCastBytes+1))
	req := httptest.NewRequest(http.MethodPost, "/ui/cast", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("Code = %d, want 413; body = %s", rec.Code, rec.Body.String())
	}
	var bodyOut map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &bodyOut)
	if bodyOut["chip"] != "FILE TOO BIG" {
		t.Errorf("chip = %v, want FILE TOO BIG", bodyOut["chip"])
	}
}

func TestHandleCastPost_FileUploadMultipleFilesReturns400(t *testing.T) {
	t.Parallel()
	srv := newServerWithAdaptersForTest(t, &recordedQuickCasts{})
	body, contentType := makeMultipartFiles(t, []multipartFilePart{
		{fieldName: "torrent_file", filename: "one.torrent", data: []byte("one")},
		{fieldName: "torrent_file", filename: "two.torrent", data: []byte("two")},
	})
	req := httptest.NewRequest(http.MethodPost, "/ui/cast", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Code = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCastPost_BadInputReturns400(t *testing.T) {
	t.Parallel()
	srv := newServerWithAdaptersForTest(t, &recordedQuickCasts{})
	form := url.Values{}
	form.Set("kind", "url")
	form.Set("payload", "not-a-url")
	req := httptest.NewRequest(http.MethodPost, "/ui/cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != 400 {
		t.Errorf("Code = %d, want 400", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "BAD INPUT" {
		t.Errorf("chip = %v, want BAD INPUT", body["chip"])
	}
}

func TestHandleCastPost_AdapterQuickCastErrorPropagates(t *testing.T) {
	t.Parallel()
	calls := &recordedQuickCasts{
		respond: func(req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
			return adapters.QuickCastResult{}, &adapters.QuickCastError{Status: 413, Chip: "FILE TOO BIG"}
		},
	}
	srv := newServerWithAdaptersForTest(t, calls)
	body, contentType := makeMultipart(t, "torrent_file", "huge.torrent", []byte("..."))
	req := httptest.NewRequest(http.MethodPost, "/ui/cast", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != 413 {
		t.Errorf("Code = %d, want 413", rec.Code)
	}
	var bodyOut map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &bodyOut)
	if bodyOut["chip"] != "FILE TOO BIG" {
		t.Errorf("chip = %v, want FILE TOO BIG", bodyOut["chip"])
	}
}

func TestHandleCastPost_UntypedErrorCollapsesToCastFailed(t *testing.T) {
	t.Parallel()
	calls := &recordedQuickCasts{
		respond: func(req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
			return adapters.QuickCastResult{}, fmt.Errorf("synthetic untyped error")
		},
	}
	srv := newServerWithAdaptersForTest(t, calls)
	form := url.Values{}
	form.Set("kind", "url")
	form.Set("payload", "https://example.com/x.mp4")
	req := httptest.NewRequest(http.MethodPost, "/ui/cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != 500 {
		t.Errorf("Code = %d, want 500", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "CAST FAILED" {
		t.Errorf("chip = %v, want CAST FAILED", body["chip"])
	}
}

func TestHandleCastPost_MultipartWithNoFileReturns400(t *testing.T) {
	t.Parallel()
	srv := newServerWithAdaptersForTest(t, &recordedQuickCasts{})
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("kind", "file"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/ui/cast", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Code = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "BAD INPUT" {
		t.Errorf("chip = %v, want BAD INPUT", body["chip"])
	}
}

func TestReceiverCastPostRouteRejectsMissingFetchSite(t *testing.T) {
	t.Parallel()
	srv := newServerWithAdaptersForTest(t, &recordedQuickCasts{})
	mux := http.NewServeMux()
	srv.Mount(mux)
	form := url.Values{}
	form.Set("kind", "url")
	form.Set("payload", "https://example.com/x.mp4")
	req := httptest.NewRequest(http.MethodPost, "/ui/cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403", rec.Code)
	}
}

type recordedQuickCasts struct {
	mu      sync.Mutex
	calls   []adapters.QuickCastRequest
	respond func(adapters.QuickCastRequest) (adapters.QuickCastResult, error)
}

func (r *recordedQuickCasts) record(req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	r.mu.Unlock()
	if r.respond != nil {
		return r.respond(req)
	}
	return adapters.QuickCastResult{}, nil
}

func (r *recordedQuickCasts) last() adapters.QuickCastRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return adapters.QuickCastRequest{}
	}
	return r.calls[len(r.calls)-1]
}

func newServerWithAdaptersForTest(t *testing.T, calls *recordedQuickCasts) *Server {
	t.Helper()
	reg := adapters.NewRegistry()
	if err := reg.Register(routedURLStub{calls: calls}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(routedTorrentStub{calls: calls}); err != nil {
		t.Fatal(err)
	}
	cfg := nonZeroConfig()
	cfg.Registry = reg
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

type routedURLStub struct {
	urlAdapterStub
	calls *recordedQuickCasts
}

func (s routedURLStub) HandleQuickCast(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	return s.calls.record(req)
}

type routedTorrentStub struct {
	torrentAdapterStub
	calls *recordedQuickCasts
}

func (s routedTorrentStub) HandleQuickCast(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	return s.calls.record(req)
}

func makeMultipart(t *testing.T, fieldName, filename string, data []byte) (io.Reader, string) {
	t.Helper()
	return makeMultipartFiles(t, []multipartFilePart{{fieldName: fieldName, filename: filename, data: data}})
}

type multipartFilePart struct {
	fieldName string
	filename  string
	data      []byte
}

func makeMultipartFiles(t *testing.T, files []multipartFilePart) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("kind", "file"); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		part, err := w.CreateFormFile(file.fieldName, file.filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(file.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func TestWritePresetStarSuccess_StarredEmitsSlotNoCleared(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writePresetStarSuccess(rec, true, 5, nil)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200", rec.Code)
	}
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":true,"starred":true,"slot":5}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestWritePresetStarSuccess_UnstarredEmitsClearedNoSlot(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writePresetStarSuccess(rec, false, 0, []int{3, 9})
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":true,"starred":false,"cleared":[3,9]}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestWritePresetStarSuccess_UnstarredEmptyClearedOmitsCleared(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writePresetStarSuccess(rec, false, 0, nil)
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":true,"starred":false}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestWritePresetMoveSuccess_MinimalShape(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writePresetMoveSuccess(rec)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200", rec.Code)
	}
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":true}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestWritePresetEditError_NoSlotOrCleared(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writePresetEditError(rec, 409, "BANK FULL")
	if rec.Code != 409 {
		t.Fatalf("Code = %d, want 409", rec.Code)
	}
	got := strings.TrimSpace(rec.Body.String())
	want := `{"ok":false,"chip":"BANK FULL"}`
	if got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}
