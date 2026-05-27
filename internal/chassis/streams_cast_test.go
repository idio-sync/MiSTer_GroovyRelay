package chassis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type fakeStreamsCaster struct {
	mu      sync.Mutex
	calls   [][2]string
	respond func(provider, channel string) error
}

func (f *fakeStreamsCaster) CastChannel(ctx context.Context, providerID, channelID string) error {
	f.mu.Lock()
	f.calls = append(f.calls, [2]string{providerID, channelID})
	f.mu.Unlock()
	if f.respond != nil {
		return f.respond(providerID, channelID)
	}
	return nil
}

func newServerWithStreamsCasterForTest(t *testing.T, caster adapters.StreamsCaster) *Server {
	t.Helper()
	cfg := nonZeroConfig()
	cfg.StreamsCaster = caster
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func postStreamsCast(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/receiver/streams/cast", strings.NewReader(body))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleStreamsCast(rec, req)
	return rec
}

func TestStreamsCast_Success(t *testing.T) {
	t.Parallel()
	caster := &fakeStreamsCaster{}
	srv := newServerWithStreamsCasterForTest(t, caster)
	rec := postStreamsCast(t, srv, "provider=mtv-rewind&channel=80s")
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(caster.calls) != 1 || caster.calls[0] != [2]string{"mtv-rewind", "80s"} {
		t.Errorf("calls = %v, want [[mtv-rewind 80s]]", caster.calls)
	}
}

func TestStreamsCast_MissingProviderOrChannelReturns400(t *testing.T) {
	t.Parallel()
	srv := newServerWithStreamsCasterForTest(t, &fakeStreamsCaster{})
	for _, body := range []string{
		"",
		"provider=",
		"channel=80s",
		"provider=mtv-rewind",
		"provider=&channel=80s",
		"provider=mtv-rewind&channel=",
	} {
		t.Run(body, func(t *testing.T) {
			rec := postStreamsCast(t, srv, body)
			if rec.Code != 400 {
				t.Errorf("body=%q Code = %d, want 400", body, rec.Code)
			}
			var got map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if got["chip"] != "BAD INPUT" {
				t.Errorf("body=%q chip = %v, want BAD INPUT", body, got["chip"])
			}
		})
	}
}

func TestStreamsCast_NilCasterReturns404(t *testing.T) {
	t.Parallel()
	srv := newServerWithStreamsCasterForTest(t, nil)
	rec := postStreamsCast(t, srv, "provider=mtv-rewind&channel=80s")
	if rec.Code != 404 {
		t.Errorf("Code = %d, want 404", rec.Code)
	}
}

func TestStreamsCast_QuickCastErrorPropagates(t *testing.T) {
	t.Parallel()
	caster := &fakeStreamsCaster{respond: func(p, c string) error {
		return &adapters.QuickCastError{Status: 503, Chip: "NOT READY"}
	}}
	srv := newServerWithStreamsCasterForTest(t, caster)
	rec := postStreamsCast(t, srv, "provider=mtv-rewind&channel=80s")
	if rec.Code != 503 {
		t.Errorf("Code = %d, want 503", rec.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["chip"] != "NOT READY" {
		t.Errorf("chip = %v, want NOT READY", got["chip"])
	}
}

func TestStreamsCast_UntypedErrorCollapsesTo500(t *testing.T) {
	t.Parallel()
	caster := &fakeStreamsCaster{respond: func(p, c string) error {
		return errors.New("synthetic")
	}}
	srv := newServerWithStreamsCasterForTest(t, caster)
	rec := postStreamsCast(t, srv, "provider=mtv-rewind&channel=80s")
	if rec.Code != 500 {
		t.Errorf("Code = %d, want 500", rec.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["chip"] != "CAST FAILED" {
		t.Errorf("chip = %v, want CAST FAILED", got["chip"])
	}
}

func TestStreamsCast_RouteRequiresSameOrigin(t *testing.T) {
	t.Parallel()
	srv := newServerWithStreamsCasterForTest(t, &fakeStreamsCaster{})
	mux := http.NewServeMux()
	srv.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/receiver/streams/cast",
		strings.NewReader("provider=mtv-rewind&channel=80s"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// No Sec-Fetch-Site header.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403", rec.Code)
	}
}
