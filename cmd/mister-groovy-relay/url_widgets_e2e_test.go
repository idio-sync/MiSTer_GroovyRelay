//go:build integration
// +build integration

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

func newURLE2EServer(t *testing.T) (*httptest.Server, string, *url.Adapter) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	dataDirFwd := strings.ReplaceAll(dir, `\`, `/`)
	body := `[bridge]
mister.host = "127.0.0.1"
data_dir = "` + dataDirFwd + `"

[adapters.url]
enabled = true
ytdlp_enabled = true
ytdlp_hosts = ["youtube.com"]
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	a, err := url.New(url.AdapterConfig{Bridge: config.BridgeConfig{DataDir: dir}})
	if err != nil {
		t.Fatalf("url.New: %v", err)
	}
	if err := decodeAdapterSectionFromFile(t, cfgPath, "url", a); err != nil {
		t.Fatalf("decodeAdapterSectionFromFile: %v", err)
	}
	saver := uiserver.NewAdapterSaver(cfgPath, &sync.Mutex{})
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"url": a}}
	srv, err := chassis.New(chassis.Config{
		Version:              "test",
		StartedAt:            time.Now(),
		AdapterSettingsSaver: newBridgeAdapterSettingsSaver(saver, reg),
		AdapterHostEditor:    newBridgeAdapterHostEditor(saver, reg),
		AdapterCookieStore:   newBridgeAdapterCookieStore(reg),
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, cfgPath, a
}

func e2ePost(t *testing.T, ts *httptest.Server, path, ct, body string) map[string]any {
	t.Helper()
	req, err := http.NewRequest("POST", ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(res.Body)
		t.Fatalf("POST %s status = %d; body=%s", path, res.StatusCode, out)
	}
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode %s response: %v", path, err)
	}
	return payload
}

func TestE2E_URLHosts_Persist(t *testing.T) {
	ts, cfgPath, a := newURLE2EServer(t)
	payload := e2ePost(t, ts, "/ui/settings/adapter/url/hosts",
		"application/json", `{"hosts":["YouTube.com","vimeo.com"]}`)
	if payload["scope"] != "hot" {
		t.Errorf("scope = %v, want hot", payload["scope"])
	}
	got, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(got), `"youtube.com"`) || !strings.Contains(string(got), `"vimeo.com"`) {
		t.Errorf("config.toml missing normalized hosts:\n%s", got)
	}
	// In-memory side: adapter reflects the new (normalized) list.
	hosts := a.CurrentHosts()
	if len(hosts) != 2 || hosts[0] != "youtube.com" || hosts[1] != "vimeo.com" {
		t.Errorf("CurrentHosts = %v, want [youtube.com vimeo.com]", hosts)
	}
}

func TestE2E_URLCookies_SaveAndClear(t *testing.T) {
	ts, _, a := newURLE2EServer(t)
	netscape := "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t1893456000\tSID\tx\n"
	save := e2ePost(t, ts, "/ui/settings/adapter/url/cookies",
		"application/json", `{"cookies":`+jsonString(netscape)+`}`)
	cookie, _ := save["cookie"].(map[string]any)
	if cookie["loaded"] != true {
		t.Errorf("after save loaded = %v, want true", cookie["loaded"])
	}
	if _, err := os.Stat(a.CookiesPath()); err != nil {
		t.Errorf("cookies file not written: %v", err)
	}
	clr := e2ePost(t, ts, "/ui/settings/adapter/url/cookies/clear",
		"application/x-www-form-urlencoded", "")
	cookie, _ = clr["cookie"].(map[string]any)
	if cookie["loaded"] != false {
		t.Errorf("after clear loaded = %v, want false", cookie["loaded"])
	}
	if _, err := os.Stat(a.CookiesPath()); !os.IsNotExist(err) {
		t.Errorf("cookies file lingered after clear: %v", err)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
