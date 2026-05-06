//go:build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestStrictJSONStdout starts the bridge with MISTER_GROOVY_LOG_FORMAT=json,
// captures ~1.5s of stdout, and asserts every line is valid JSON.
// Regression guard for the spec's "Docker / journald / piped output keeps
// strict JSON" promise — catches a future PR that accidentally
// fmt.Println's banner output to stdout regardless of mode.
func TestStrictJSONStdout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}

	// Build the bridge into a temp dir so we don't depend on a prior
	// `make build`. Append .exe on Windows so exec.Command can locate it.
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "mister-groovy-relay")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/mister-groovy-relay")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("go build failed: %v\n%s", err, out)
	}

	// Pre-create a minimal valid config so the bridge boots to the
	// listener. mister.host = 127.0.0.1 + an arbitrary high port that
	// nothing listens on — the bridge doesn't validate MiSTer
	// reachability at startup. data_dir = the temp dir so no state
	// leaks into the user's home directory. http_port = 32599 because
	// internal/config.validPort rejects 0.
	dataDir := t.TempDir()
	cfgDir := t.TempDir()
	configPath := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(minimalConfig(dataDir)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "--config", configPath, "--log-level", "info")
	cmd.Env = filterAppEnv(os.Environ())
	cmd.Env = append(cmd.Env, "MISTER_GROOVY_LOG_FORMAT=json")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// Read up to ~1.5s of output, then validate.
	var lines []string
	deadline := time.NewTimer(1500 * time.Millisecond)
	defer deadline.Stop()
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		s := bufio.NewScanner(stdout)
		for s.Scan() {
			lines = append(lines, s.Text())
		}
	}()
	select {
	case <-deadline.C:
		_ = stdout.Close() // forces s.Scan() to return false
	case <-doneCh:
	}
	<-doneCh // wait for goroutine to exit before reading lines

	if len(lines) == 0 {
		t.Fatal("no output captured from bridge stdout in 1.5s")
	}
	t.Logf("captured %d stdout lines", len(lines))

	for i, line := range lines {
		// Skip blank lines defensively.
		if strings.TrimSpace(line) == "" {
			continue
		}
		var v map[string]any
		if err := json.NewDecoder(strings.NewReader(line)).Decode(&v); err != nil {
			t.Errorf("line %d not valid JSON: %v\nline: %q", i, err, line)
		}
	}
}

// minimalConfig returns the smallest config.toml that lets the bridge
// boot to the HTTP listener stage. mister.host can be unreachable;
// http_port must be a valid TCP port (validPort rejects 0).
func minimalConfig(dataDir string) string {
	return fmt.Sprintf(`
[bridge]
data_dir = %q

[bridge.mister]
host = "127.0.0.1"
port = 32100
source_port = 32101

[bridge.ui]
http_port = 32599

[adapters.plex]
enabled = false

[adapters.url]
enabled = false

[adapters.jellyfin]
enabled = false
`, dataDir)
}

// filterAppEnv strips MISTER_GROOVY_* vars from env so test behavior
// doesn't depend on the developer's shell. The test then sets exactly
// the vars it needs.
func filterAppEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "MISTER_GROOVY_") {
			continue
		}
		out = append(out, e)
	}
	return out
}
