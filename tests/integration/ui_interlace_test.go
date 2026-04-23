package integration

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovynet"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

// TestIntegration_Save_InterlaceFlip_LiveApply exercises the Phase-7
// hero path through the real HTTP stack AND the real save code path:
// load sectioned config, stand up a core.Manager + UI server wired to
// uiserver.BridgeSaver (the same type cmd/mister-groovy-relay uses),
// POST a bridge save that flips interlace_field_order, and verify
// both the in-memory manager state and the on-disk config moved.
//
// Reusing uiserver.NewBridgeSaver here (rather than a test-local
// reimplementation) closes the review C3 gap: any regression in the
// production save path — pre-flight probes, diff, marshal, ordering —
// is now caught by this test.
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

	saver := uiserver.NewBridgeSaver(cfgPath, sec, coreMgr, reg)
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

	// The [adapters.plex] section must survive the bridge-only
	// rewrite — this is the "preserve adapter sections intact"
	// invariant the production saver relies on.
	if !strings.Contains(string(data), "[adapters.plex]") {
		t.Errorf("bridge save dropped the [adapters.plex] section:\n%s", data)
	}
	if !strings.Contains(string(data), `device_name = "TestBridge"`) {
		t.Errorf("bridge save dropped the plex device_name:\n%s", data)
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
