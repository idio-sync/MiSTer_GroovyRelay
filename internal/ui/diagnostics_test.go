package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
)

func TestDiagnostics_RendersAllSections(t *testing.T) {
	log := eventlog.New(16)
	log.Append(eventlog.Entry{Time: time.Now(), Severity: eventlog.SeverityInfo, Source: "core", Message: "cast-started plex/test"})
	log.Append(eventlog.Entry{Time: time.Now(), Severity: eventlog.SeverityWarn, Source: "jellyfin", Message: "Sessions WS reconnect"})
	log.Append(eventlog.Entry{Time: time.Now(), Severity: eventlog.SeverityErr, Source: "plex", Message: "plex.tv 503"})
	srv, mux := newTestServer(t, func(c *Config) { c.EventLog = log; c.Version = "test" })
	_ = srv

	r := httptest.NewRequest("GET", "/ui/diagnostics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status: %d", w.Code)
	}
	for _, sub := range []string{
		"Recent events", "Network probe", "Build info",
		"cast-started plex/test", "Sessions WS reconnect", "plex.tv 503",
		"info", "warn", "err",
		"test", // version
	} {
		if !strings.Contains(body, sub) {
			t.Errorf("missing %q", sub)
		}
	}
}

func TestDiagnostics_FiltersEventsBySeverity(t *testing.T) {
	log := eventlog.New(16)
	log.Append(eventlog.Entry{Time: time.Now(), Severity: eventlog.SeverityInfo, Source: "core", Message: "info-only event"})
	log.Append(eventlog.Entry{Time: time.Now(), Severity: eventlog.SeverityWarn, Source: "jellyfin", Message: "warn-only event"})
	log.Append(eventlog.Entry{Time: time.Now(), Severity: eventlog.SeverityErr, Source: "plex", Message: "err-only event"})
	srv, mux := newTestServer(t, func(c *Config) { c.EventLog = log })
	_ = srv

	r := httptest.NewRequest("GET", "/ui/diagnostics?severity=warn", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	if !strings.Contains(body, "warn-only event") {
		t.Fatalf("filtered diagnostics should include warn event: %s", body)
	}
	for _, sub := range []string{"info-only event", "err-only event"} {
		if strings.Contains(body, sub) {
			t.Fatalf("filtered diagnostics should not include %q: %s", sub, body)
		}
	}
	if !strings.Contains(body, `href="/ui/diagnostics?severity=warn" class="gr-chip active" aria-current="page"`) {
		t.Fatalf("warn chip should be active: %s", body)
	}
	if strings.Contains(body, `href="/ui/diagnostics" class="gr-chip active" aria-current="page"`) {
		t.Fatalf("all chip should not be active when warn is selected: %s", body)
	}
}

func TestDiagnostics_RendersProcessUptime(t *testing.T) {
	srv, _ := newTestServer(t, func(c *Config) {
		c.StartedAt = time.Now().Add(-65 * time.Minute)
	})

	data := srv.buildDiagnosticsData()
	if data.Uptime != "1h 05m" {
		t.Fatalf("diagnostics uptime = %q, want 1h 05m", data.Uptime)
	}
}

type fakeProber struct {
	err   error
	delay time.Duration
}

func (f fakeProber) Probe(ctx context.Context) error {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func TestDiagnosticsProbe_PostReturnsCallout(t *testing.T) {
	srv, mux := newTestServer(t, func(c *Config) {
		c.MisterProber = fakeProber{delay: 14 * time.Millisecond}
	})
	_ = srv

	body := strings.NewReader("")
	r := httptest.NewRequest("POST", "/ui/diagnostics/probe", body)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, "ACK") {
		t.Errorf("missing ACK indicator: %s", respBody)
	}
}

func TestDiagnosticsProbe_PostReturnsErrCallout(t *testing.T) {
	srv, mux := newTestServer(t, func(c *Config) {
		c.MisterProber = fakeProber{err: context.DeadlineExceeded}
	})
	_ = srv

	body := strings.NewReader("")
	r := httptest.NewRequest("POST", "/ui/diagnostics/probe", body)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, "probe failed") {
		t.Errorf("missing error indicator: %s", respBody)
	}
}

func TestDiagnosticsProbe_XSSEscaped(t *testing.T) {
	srv, mux := newTestServer(t, func(c *Config) {
		type errStr string
		c.MisterProber = fakeProber{err: &xssErr{msg: "<script>alert(1)</script>"}}
	})
	_ = srv

	body := strings.NewReader("")
	r := httptest.NewRequest("POST", "/ui/diagnostics/probe", body)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	respBody := w.Body.String()
	if strings.Contains(respBody, "<script>") {
		t.Errorf("XSS: raw <script> tag found in probe response")
	}
}

type xssErr struct{ msg string }

func (e *xssErr) Error() string { return e.msg }

func TestDiagnostics_BuildInfoContainsVersion(t *testing.T) {
	srv, mux := newTestServer(t, func(c *Config) { c.Version = "v1.2.3-test" })
	_ = srv

	r := httptest.NewRequest("GET", "/ui/diagnostics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "v1.2.3-test") {
		t.Errorf("version not found in diagnostics page")
	}
}
