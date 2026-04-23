package integration

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

// TestIntegration_Save_InterlaceFlip_LiveApply exercises the
// Phase-7 hero path through the real HTTP stack: load sectioned
// config, stand up a core.Manager + UI server, POST a bridge save
// that flips interlace_field_order, and verify both the in-memory
// manager state and the on-disk config moved.
//
// Skips the Plex adapter entirely so we don't fight for port 32500;
// the interlace path doesn't involve the adapter registry.
func TestIntegration_Save_InterlaceFlip_LiveApply(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(testInterlaceConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	sec, err := config.LoadSectioned(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Real sender bound to an OS-assigned port — a stub would need
	// extracting an interface, out of scope for Phase 8.
	sender, err := groovynet.NewSender("127.0.0.1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	coreMgr := core.NewManager(sec.Bridge, sender)
	reg := adapters.NewRegistry()

	saver := &integrationBridgeSaver{
		path: cfgPath, sec: sec, core: coreMgr, registry: reg,
	}
	uiSrv, err := ui.New(ui.Config{Registry: reg, BridgeSaver: saver})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	uiSrv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	if coreMgr.CurrentInterlaceOrder() != "tff" {
		t.Fatalf("initial interlace = %q, want tff", coreMgr.CurrentInterlaceOrder())
	}

	form := testBridgeFormBody("bff")
	req, _ := http.NewRequest("POST", ts.URL+"/ui/bridge/save", bytes.NewBufferString(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "applied live") {
		t.Errorf("expected hot-swap toast, body: %s", body)
	}

	if coreMgr.CurrentInterlaceOrder() != "bff" {
		t.Errorf("post-save interlace = %q, want bff", coreMgr.CurrentInterlaceOrder())
	}

	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), `interlace_field_order = "bff"`) {
		t.Errorf("on-disk config did not update:\n%s", data)
	}
}

const testInterlaceConfig = `
[bridge]
data_dir = "."

[bridge.video]
modeline = "NTSC_480i"
interlace_field_order = "tff"
aspect_mode = "auto"
rgb_mode = "rgb888"
lz4_enabled = true

[bridge.audio]
sample_rate = 48000
channels = 2

[bridge.mister]
host = "127.0.0.1"
port = 32100
source_port = 32101

[bridge.ui]
http_port = 32500

[adapters.plex]
enabled = false
device_name = "TestBridge"
profile_name = "Plex Home Theater"
`

func testBridgeFormBody(interlaceOrder string) string {
	return fmt.Sprintf(
		"mister.host=127.0.0.1"+
			"&mister.port=32100"+
			"&mister.source_port=32101"+
			"&host_ip="+
			"&video.modeline=NTSC_480i"+
			"&video.interlace_field_order=%s"+
			"&video.aspect_mode=auto"+
			"&video.lz4_enabled=true"+
			"&audio.sample_rate=48000"+
			"&audio.channels=2"+
			"&ui.http_port=32500"+
			"&data_dir=.",
		interlaceOrder)
}

// integrationBridgeSaver mirrors cmd/mister-groovy-relay's
// runtimeBridgeSaver inline here rather than extracting into a
// shared package. If Phase 8+ adds more integration tests that need
// this, factor into internal/uiserver/.
type integrationBridgeSaver struct {
	mu       sync.Mutex
	path     string
	sec      *config.Sectioned
	core     *core.Manager
	registry *adapters.Registry
}

func (r *integrationBridgeSaver) Current() config.BridgeConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sec.Bridge
}

func (r *integrationBridgeSaver) Save(newCfg config.BridgeConfig) (adapters.ApplyScope, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	old := r.sec.Bridge
	changed := integrationDiffBridge(old, newCfg)
	scope := adapters.ScopeHotSwap
	for _, k := range changed {
		scope = adapters.MaxScope(scope, integrationScopeFor(k))
	}

	r.sec.Bridge = newCfg
	r.core.UpdateBridge(newCfg)

	// Bridge-only rewrite preserves the [adapters.plex] section.
	data, err := os.ReadFile(r.path)
	if err != nil {
		return 0, err
	}
	without := integrationStripBridge(data)
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(struct {
		Bridge config.BridgeConfig `toml:"bridge"`
	}{newCfg}); err != nil {
		return 0, err
	}
	out := append(append(without, '\n'), buf.Bytes()...)
	if err := config.WriteAtomic(r.path, out); err != nil {
		return 0, err
	}

	if scope == adapters.ScopeHotSwap && integrationContains(changed, "video.interlace_field_order") {
		if err := r.core.SetInterlaceFieldOrder(newCfg.Video.InterlaceFieldOrder); err != nil {
			return 0, err
		}
	}
	return scope, nil
}

func integrationDiffBridge(oldCfg, newCfg config.BridgeConfig) []string {
	var keys []string
	if oldCfg.Video.InterlaceFieldOrder != newCfg.Video.InterlaceFieldOrder {
		keys = append(keys, "video.interlace_field_order")
	}
	if oldCfg.Video.AspectMode != newCfg.Video.AspectMode {
		keys = append(keys, "video.aspect_mode")
	}
	if oldCfg.MiSTer.Host != newCfg.MiSTer.Host {
		keys = append(keys, "mister.host")
	}
	if oldCfg.UI.HTTPPort != newCfg.UI.HTTPPort {
		keys = append(keys, "ui.http_port")
	}
	// Integration test only exercises a handful of fields; full diff
	// is in cmd/mister-groovy-relay/main.go.
	return keys
}

func integrationScopeFor(key string) adapters.ApplyScope {
	switch key {
	case "video.interlace_field_order":
		return adapters.ScopeHotSwap
	case "video.aspect_mode":
		return adapters.ScopeRestartCast
	default:
		return adapters.ScopeRestartBridge
	}
}

func integrationContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func integrationStripBridge(doc []byte) []byte {
	lines := strings.Split(string(doc), "\n")
	out := make([]string, 0, len(lines))
	skipping := false
	for _, ln := range lines {
		tr := strings.TrimSpace(ln)
		if strings.HasPrefix(tr, "[bridge") {
			skipping = true
			continue
		}
		if skipping && strings.HasPrefix(tr, "[") && strings.HasSuffix(tr, "]") {
			skipping = false
		}
		if !skipping {
			out = append(out, ln)
		}
	}
	return []byte(strings.Join(out, "\n"))
}

// Ensure net import is referenced even when unused in this file's
// future variations — integration tests often grow network helpers.
var _ = net.IPv4zero
