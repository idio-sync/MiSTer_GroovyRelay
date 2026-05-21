//go:build integration

package integration

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

func TestReceiverEndToEnd(t *testing.T) {
	t.Parallel()

	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:    config.BridgeConfig{UI: config.UIConfig{HTTPPort: 32500}},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "integration-test",
		StartedAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		HostIP:    "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	mux := http.NewServeMux()
	chassisSrv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/receiver")
	if err != nil {
		t.Fatalf("GET /receiver: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /receiver status = %d, body = %s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /receiver body: %v", err)
	}
	for _, want := range []string{
		`<!-- chassis:shell -->`,
		`<!-- chassis:vfd -->`,
		`<!-- chassis:meter -->`,
		`<!-- chassis:transport -->`,
		`<!-- chassis:visualizer-bank -->`,
		`<!-- chassis:source-cluster -->`,
		`<!-- chassis:input-row -->`,
		`<!-- chassis:preset-bank -->`,
		`<!-- chassis:history -->`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("GET /receiver HTML missing marker %q", want)
		}
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("parse /receiver HTML: %v", err)
	}
	classes := collectClasses(doc)

	for _, want := range []string{
		"vfd",
		"meter-screen",
		"transport-strip",
		"viz-bank",
		"source-cluster",
		"input-section",
		"preset-bank",
		"history-section",
	} {
		if !classes[want] {
			t.Errorf("GET /receiver HTML missing class %q", want)
		}
	}
}

func TestMount_DoesNotShadowUIRoutes(t *testing.T) {
	t.Parallel()

	uiSrv, err := ui.New(ui.Config{
		Registry: adapters.NewRegistry(),
		Version:  "integration-test",
	})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	chassisSrv, err := chassis.New(chassis.Config{
		Bridge:    config.BridgeConfig{UI: config.UIConfig{HTTPPort: 32500}},
		Manager:   &core.Manager{},
		Registry:  adapters.NewRegistry(),
		Version:   "integration-test",
		StartedAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		HostIP:    "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}

	mux := http.NewServeMux()
	uiSrv.Mount(mux)
	chassisSrv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	for _, tc := range []struct {
		path              string
		contentTypePrefix string
		want              []string
		notWant           []string
	}{
		{
			path: "/ui",
			want: []string{`class="gr-shell"`},
		},
		{
			path:              "/ui/static/app.css",
			contentTypePrefix: "text/css",
			want:              []string{".gr-shell {", ".gr-sidebar {"},
			notWant:           []string{`class="gr-shell"`, `<!-- chassis:shell -->`},
		},
		{
			path: "/receiver",
			want: []string{`<!-- chassis:shell -->`},
		},
		{
			path:              "/receiver/static/chassis.css",
			contentTypePrefix: "text/css",
			want:              []string{"body.receiver .meter-screen", "body.receiver .transport-strip"},
			notWant:           []string{`class="gr-shell"`, `<!-- chassis:shell -->`},
		},
	} {
		resp, err := http.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s body: %v", tc.path, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close %s body: %v", tc.path, closeErr)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", tc.path, resp.StatusCode, body)
		}
		if tc.contentTypePrefix != "" {
			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, tc.contentTypePrefix) {
				t.Fatalf("GET %s Content-Type = %q, want prefix %q", tc.path, got, tc.contentTypePrefix)
			}
		}
		bodyText := string(body)
		for _, want := range tc.want {
			if !strings.Contains(bodyText, want) {
				t.Fatalf("GET %s body missing %q", tc.path, want)
			}
		}
		for _, notWant := range tc.notWant {
			if strings.Contains(bodyText, notWant) {
				t.Fatalf("GET %s body unexpectedly contained %q", tc.path, notWant)
			}
		}
	}
}

func collectClasses(n *html.Node) map[string]bool {
	classes := make(map[string]bool)
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			for _, attr := range node.Attr {
				if attr.Key != "class" {
					continue
				}
				for _, className := range strings.Fields(attr.Val) {
					classes[className] = true
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return classes
}
