# MiSTer SSH Launch Button (v1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Launch GroovyMiSTer" button to the Settings UI Bridge panel that SSHes into the saved MiSTer host and writes `load_core /media/fat/_Utility/Groovy.rbf` into the MiSTer's `/dev/MiSTer_cmd` FIFO, eliminating the last terminal step from the cast workflow.

**Architecture:** New `internal/misterctl/` package owns SSH dial+exec behind a package-level `dialAndRun` injection seam. New `MisterLauncher` interface in `ui.Config` mirrors the existing `BridgeSaver`/`AdapterSaver` seams. main.go wires a `bridgeMisterLauncher` closure adapter that reads live credentials from `BridgeSaver.Current()` at click time. Two new bridge config fields (`bridge.mister.ssh_user`, `bridge.mister.ssh_password`) carry the credentials; both have `ScopeHotSwap` apply scope. The launch button lives outside the bridge save form to avoid `required`-validation coupling and `hx-include` form-field bloat.

**Tech Stack:** Go 1.26, BurntSushi/toml, html/template, htmx, `golang.org/x/crypto/ssh` (new dep), `golang.org/x/crypto/ssh.InsecureIgnoreHostKey`. Tests use stdlib `testing` + `httptest`.

**Spec:** [docs/specs/2026-04-25-mister-ssh-launch-design.md](docs/specs/2026-04-25-mister-ssh-launch-design.md)

**Order rationale:** Tasks are sequenced bottom-up: config schema → scope dispatch → form rendering → form parsing → SSH package → handler/template → main.go wiring. Each task's tests can pass independently of later tasks. The `cmd/`-package closure (Task 12) depends on `misterctl` (Task 7–8) and the `MisterLauncher` interface (Task 9). The HTML template addition (Task 11) depends on the handler (Task 10). main.go wiring (Task 13) is the last code-touching task; the optional smoke test (Task 14) is verification only.

---

## Task 1: Add SSHUser/SSHPassword to MisterConfig + defaults + example.toml

**Why:** The bridge config grows two new optional fields. They round-trip through TOML on Load/Save and have a sensible default for `SSHUser` ("root", the stock MiSTer username). The empty-by-default `SSHPassword` value forces the operator to type their password in the UI before the launch button works — no stock weak password ships in our default config file.

**Files:**
- Modify: `internal/config/config.go` (extend `MisterConfig` struct)
- Modify: `internal/config/migration.go` (`defaultBridge()` at line 239)
- Modify: `internal/config/example.toml` ([bridge.mister] block at lines 19–22)
- Test: `internal/config/config_test.go` (append round-trip test)

- [ ] **Step 1: Write the failing TOML round-trip test**

Append to `internal/config/config_test.go`:

```go
// TestSectioned_RoundTripSSHFields confirms the new SSH credential
// fields decode + re-encode through BurntSushi/toml without loss.
// Catches a forgotten struct tag or a missed migration helper if
// either drifts in a future refactor.
func TestSectioned_RoundTripSSHFields(t *testing.T) {
	const input = `
[bridge.mister]
host = "192.168.1.42"
port = 32100
source_port = 32101
ssh_user = "alice"
ssh_password = "hunter2"
`
	s, _, err := loadSectionedFromBytes([]byte(input))
	if err != nil {
		t.Fatalf("loadSectionedFromBytes: %v", err)
	}
	if s.Bridge.MiSTer.SSHUser != "alice" {
		t.Errorf("SSHUser = %q, want alice", s.Bridge.MiSTer.SSHUser)
	}
	if s.Bridge.MiSTer.SSHPassword != "hunter2" {
		t.Errorf("SSHPassword = %q, want hunter2", s.Bridge.MiSTer.SSHPassword)
	}
}

// TestDefaultBridge_SSHUserIsRoot pins the default user so a future
// refactor of defaultBridge can't silently change it.
func TestDefaultBridge_SSHUserIsRoot(t *testing.T) {
	b := defaultBridge()
	if b.MiSTer.SSHUser != "root" {
		t.Errorf("default SSHUser = %q, want root", b.MiSTer.SSHUser)
	}
	if b.MiSTer.SSHPassword != "" {
		t.Errorf("default SSHPassword = %q, want empty", b.MiSTer.SSHPassword)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run "RoundTripSSHFields|DefaultBridge_SSHUserIsRoot" -v`
Expected: FAIL — `MiSTer.SSHUser undefined` / `MiSTer.SSHPassword undefined` (struct fields don't exist yet).

- [ ] **Step 3: Add the new fields to `MisterConfig`**

Edit `internal/config/config.go` lines 134–138:

```go
type MisterConfig struct {
	Host        string `toml:"host"`
	Port        int    `toml:"port"`
	SourcePort  int    `toml:"source_port"`
	SSHUser     string `toml:"ssh_user"`
	SSHPassword string `toml:"ssh_password"`
}
```

- [ ] **Step 4: Set the default user in `defaultBridge()`**

Edit `internal/config/migration.go` lines 255–258 (the `MiSTer:` literal inside `defaultBridge()`):

```go
MiSTer: MisterConfig{
	Port:       d.MisterPort,
	SourcePort: d.SourcePort,
	SSHUser:    "root",
},
```

Do NOT extend the legacy `defaults()` helper in `config.go` — it's freezing as v1 ages and growing fields there for parity is a maintenance trap.

- [ ] **Step 5: Update example.toml**

Edit `internal/config/example.toml` lines 19–22 (the `[bridge.mister]` block):

```toml
[bridge.mister]
host = "192.168.1.50"             # MiSTer IP or hostname (required)
port = 32100                      # Groovy UDP port on the MiSTer (default 32100)
source_port = 32101               # Our stable source UDP port (kept across casts)
ssh_user = "root"                 # MiSTer's stock SSH user
# ssh_password = ""               # Required to use the Launch GroovyMiSTer button.
                                  # MiSTer's stock password is "1".
```

The `ssh_password` line stays commented out so the bridge does not ship a stock weak password literal in default config files.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/config/ -run "RoundTripSSHFields|DefaultBridge_SSHUserIsRoot" -v`
Expected: PASS.

Run: `go test ./internal/config/...` (full package suite)
Expected: PASS — no regressions in existing tests.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/migration.go internal/config/example.toml internal/config/config_test.go
git commit -m "feat(config): add ssh_user/ssh_password to bridge.mister"
```

---

## Task 2: Wire `mister.ssh_user` / `mister.ssh_password` into scope dispatch

**Why:** `FieldDef.ApplyScope` is informational only — runtime scope dispatch happens in `internal/uiserver/bridge_saver.go::scopeForBridgeField`, whose default branch returns `ScopeRestartBridge` for every `mister.*` key today. Without explicit cases, every save touching the new SSH fields would demand a bridge restart, defeating the snapshot pattern (the closure adapter reads live credentials at click time, so SSH credential edits should apply hot). `diffBridgeConfig` also needs entries for the new fields so saves correctly *detect* changes — without diff entries, even the explicit `scopeForBridgeField` case would never fire.

**Files:**
- Modify: `internal/uiserver/bridge_saver.go` (`diffBridgeConfig` at line 244, `scopeForBridgeField` at line 288)
- Test: `internal/uiserver/bridge_saver_test.go` (create file if absent; append otherwise)

- [ ] **Step 1: Check if bridge_saver_test.go exists**

Run: `ls internal/uiserver/bridge_saver_test.go 2>/dev/null && echo EXISTS || echo CREATE`

If EXISTS, append tests below to it. If CREATE, write a new file with the package declaration + imports below at top.

- [ ] **Step 2: Write the failing test for scope + diff**

Either append to `internal/uiserver/bridge_saver_test.go` or create it with this content (full file shown — adapt as needed):

```go
package uiserver

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// TestDiffBridgeConfig_SSHFields confirms ssh_user and ssh_password
// edits surface as changed keys so scopeForBridgeField gets a chance
// to dispatch them.
func TestDiffBridgeConfig_SSHFields(t *testing.T) {
	old := config.BridgeConfig{
		MiSTer: config.MisterConfig{
			Host: "192.168.1.42", Port: 32100, SourcePort: 32101,
			SSHUser: "root", SSHPassword: "",
		},
	}
	newCfg := old
	newCfg.MiSTer.SSHUser = "alice"
	newCfg.MiSTer.SSHPassword = "hunter2"

	keys := diffBridgeConfig(old, newCfg)
	if !containsStr(keys, "mister.ssh_user") {
		t.Errorf("expected mister.ssh_user in diff keys, got %v", keys)
	}
	if !containsStr(keys, "mister.ssh_password") {
		t.Errorf("expected mister.ssh_password in diff keys, got %v", keys)
	}
}

// TestScopeForBridgeField_SSHFieldsHotSwap confirms the new SSH keys
// are explicitly hot-swap, not the default ScopeRestartBridge.
func TestScopeForBridgeField_SSHFieldsHotSwap(t *testing.T) {
	for _, k := range []string{"mister.ssh_user", "mister.ssh_password"} {
		t.Run(k, func(t *testing.T) {
			got := scopeForBridgeField(k)
			if got != adapters.ScopeHotSwap {
				t.Errorf("scopeForBridgeField(%q) = %v, want ScopeHotSwap", k, got)
			}
		})
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/uiserver/ -run "DiffBridgeConfig_SSHFields|ScopeForBridgeField_SSHFieldsHotSwap" -v`
Expected: FAIL — diff doesn't surface the keys; scope returns `ScopeRestartBridge`.

- [ ] **Step 4: Add diff entries**

Edit `internal/uiserver/bridge_saver.go` `diffBridgeConfig` (around line 273, after the `MiSTer.SourcePort` check). Append after the existing `mister.source_port` block:

```go
if oldCfg.MiSTer.SSHUser != newCfg.MiSTer.SSHUser {
	keys = append(keys, "mister.ssh_user")
}
if oldCfg.MiSTer.SSHPassword != newCfg.MiSTer.SSHPassword {
	keys = append(keys, "mister.ssh_password")
}
```

- [ ] **Step 5: Add scope dispatch case**

Edit `internal/uiserver/bridge_saver.go` `scopeForBridgeField` (around line 288). Add a new case before the existing `video.modeline,...` case:

```go
func scopeForBridgeField(key string) adapters.ApplyScope {
	switch key {
	case "video.interlace_field_order":
		return adapters.ScopeHotSwap
	case "mister.ssh_user", "mister.ssh_password":
		return adapters.ScopeHotSwap
	case "video.modeline",
		"video.aspect_mode",
		"video.rgb_mode",
		"video.lz4_enabled",
		"audio.sample_rate",
		"audio.channels":
		return adapters.ScopeRestartCast
	default:
		// mister.host, mister.port, mister.source_port, host_ip,
		// data_dir, ui.http_port — all restart-bridge.
		return adapters.ScopeRestartBridge
	}
}
```

The default-branch comment is updated to enumerate `mister.*` keys explicitly (now that two of them aren't restart-bridge any more) so a future reader doesn't read "all `mister.*`" as authoritative.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/uiserver/ -run "DiffBridgeConfig_SSHFields|ScopeForBridgeField_SSHFieldsHotSwap" -v`
Expected: PASS.

Run: `go test ./internal/uiserver/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/uiserver/bridge_saver.go internal/uiserver/bridge_saver_test.go
git commit -m "feat(uiserver): hot-swap scope for ssh_user/ssh_password"
```

---

## Task 3: Add `mister.ssh_user` / `mister.ssh_password` to bridgeFields()

**Why:** `bridgeFields()` is the bridge panel's form schema. Adding the two new entries renders them as form rows under a new "MiSTer Control" section. The `Default` column is informational; runtime defaults come from `defaultBridge()` (Task 1) and runtime scope from `scopeForBridgeField` (Task 2). This task only adds the schema.

**Files:**
- Modify: `internal/ui/bridge_fields.go` (append to the slice at line 12)
- Test: `internal/ui/bridge_fields_test.go` (create new file)

- [ ] **Step 1: Write the failing test**

Create `internal/ui/bridge_fields_test.go`:

```go
package ui

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// TestBridgeFields_HasMisterControlSection verifies the SSH user and
// password fields are present under the new "MiSTer Control" section
// with the right kinds and apply-scope.
func TestBridgeFields_HasMisterControlSection(t *testing.T) {
	fields := bridgeFields()
	var user, pass *adapters.FieldDef
	for i, f := range fields {
		f := f
		switch f.Key {
		case "mister.ssh_user":
			user = &fields[i]
		case "mister.ssh_password":
			pass = &fields[i]
		}
	}
	if user == nil {
		t.Fatal("mister.ssh_user not found in bridgeFields()")
	}
	if pass == nil {
		t.Fatal("mister.ssh_password not found in bridgeFields()")
	}
	if user.Section != "MiSTer Control" {
		t.Errorf("ssh_user section = %q, want MiSTer Control", user.Section)
	}
	if pass.Section != "MiSTer Control" {
		t.Errorf("ssh_password section = %q, want MiSTer Control", pass.Section)
	}
	if user.Kind != adapters.KindText {
		t.Errorf("ssh_user kind = %v, want KindText", user.Kind)
	}
	if pass.Kind != adapters.KindSecret {
		t.Errorf("ssh_password kind = %v, want KindSecret", pass.Kind)
	}
	if user.ApplyScope != adapters.ScopeHotSwap {
		t.Errorf("ssh_user scope = %v, want ScopeHotSwap", user.ApplyScope)
	}
	if pass.ApplyScope != adapters.ScopeHotSwap {
		t.Errorf("ssh_password scope = %v, want ScopeHotSwap", pass.ApplyScope)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestBridgeFields_HasMisterControlSection -v`
Expected: FAIL — the two FieldDefs don't exist yet.

- [ ] **Step 3: Append the two FieldDef entries**

Edit `internal/ui/bridge_fields.go`. Find the `// ---- Server ----` block (around line 115) and add a new block **before it** (so MiSTer Control sits between Audio and Server in the rendered order):

```go
		// ---- MiSTer Control ----
		{
			Key:        "mister.ssh_user",
			Label:      "SSH User",
			Help:       "User to SSH into the MiSTer as. MiSTer's stock user is root.",
			Kind:       adapters.KindText,
			Default:    "root",
			ApplyScope: adapters.ScopeHotSwap,
			Section:    "MiSTer Control",
		},
		{
			Key:        "mister.ssh_password",
			Label:      "SSH Password",
			Help:       "MiSTer's stock password is 1. Stored plaintext in config.toml; the bridge does not verify the MiSTer's host key (LAN-only trust model).",
			Kind:       adapters.KindSecret,
			Default:    "",
			ApplyScope: adapters.ScopeHotSwap,
			Section:    "MiSTer Control",
		},

		// ---- Server ----
```

(The trailing `// ---- Server ----` comment is shown for context — leave it where it was, just insert the MiSTer Control block immediately above.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ui/ -run TestBridgeFields_HasMisterControlSection -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/bridge_fields.go internal/ui/bridge_fields_test.go
git commit -m "feat(ui): add MiSTer Control fields to bridge schema"
```

---

## Task 4: Render `KindSecret` rows + lookup `mister.ssh_user`

**Why:** `bridge.go::rowFor` doesn't handle `KindSecret` today (only KindText/KindInt/KindBool/KindEnum). Adding the case mirrors the existing precedent in `internal/ui/adapter.go:167–171`. `bridgeLookupString` gets a new case for `mister.ssh_user` so the form prefills from the saved config — but **no** case for `mister.ssh_password`, enforcing the no-echo invariant for stored secrets.

**Files:**
- Modify: `internal/ui/bridge.go` (`bridgeLookupString` at line 247, `rowFor` at line 211)
- Test: `internal/ui/bridge_test.go` (append a new test)

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/bridge_test.go`. Also extend `fakeBridgeSaver.Current()` (at lines 19–33) to populate the new SSH fields:

```go
// (Replace the existing fakeBridgeSaver.Current() body with this version
// — identical except the new SSHUser / SSHPassword in MiSTer.)
func (f *fakeBridgeSaver) Current() config.BridgeConfig {
	return config.BridgeConfig{
		DataDir: "/config",
		Video: config.VideoConfig{
			Modeline:            "NTSC_480i",
			InterlaceFieldOrder: "tff",
			AspectMode:          "auto",
			RGBMode:             "rgb888",
			LZ4Enabled:          true,
		},
		Audio: config.AudioConfig{SampleRate: 48000, Channels: 2},
		MiSTer: config.MisterConfig{
			Host: "192.168.1.42", Port: 32100, SourcePort: 32101,
			SSHUser: "alice", SSHPassword: "hunter2",
		},
		UI: config.UIConfig{HTTPPort: 32500},
	}
}
```

Then append these new tests at the end of the file:

```go
// TestHandleBridge_GET_RendersSSHUserPrefilled confirms ssh_user
// renders as a normal text input prefilled from the saver.
func TestHandleBridge_GET_RendersSSHUserPrefilled(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()
	if !strings.Contains(body, `name="mister.ssh_user"`) {
		t.Error("ssh_user input not rendered")
	}
	if !strings.Contains(body, `value="alice"`) {
		t.Error("ssh_user value not prefilled (expected alice)")
	}
}

// TestHandleBridge_GET_DoesNotEchoSSHPassword guards the no-echo
// invariant: the stored password must NEVER appear in the rendered
// HTML, regardless of what's in the saver. The input is rendered
// as type=password with no value attribute.
func TestHandleBridge_GET_DoesNotEchoSSHPassword(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()
	if strings.Contains(body, "hunter2") {
		t.Error("stored password leaked into rendered HTML")
	}
	if !strings.Contains(body, `name="mister.ssh_password"`) {
		t.Error("ssh_password input not rendered")
	}
	if !strings.Contains(body, `type="password"`) {
		t.Error("ssh_password should render as type=password")
	}
	if !strings.Contains(body, "Leave empty to keep existing") {
		t.Error("ssh_password placeholder missing")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run "RendersSSHUserPrefilled|DoesNotEchoSSHPassword" -v`
Expected: FAIL — `KindSecret` not rendered, `mister.ssh_user` not looked up.

(Existing `TestHandleBridge_POST_Success` may also fail because the body doesn't include the new fields. That's fine — Task 5 + Task 6 will fix the parser; for now keep this task's test scope tight.)

- [ ] **Step 3: Add `mister.ssh_user` to `bridgeLookupString`**

Edit `internal/ui/bridge.go` at the switch in `bridgeLookupString` (around line 247–267). Add a new case between `case "mister.host":` and `case "host_ip":`:

```go
func bridgeLookupString(key string, cur config.BridgeConfig) string {
	switch key {
	case "mister.host":
		return cur.MiSTer.Host
	case "mister.ssh_user":
		return cur.MiSTer.SSHUser
	case "host_ip":
		return cur.HostIP
	// ... (rest unchanged)
	}
	return ""
}
```

Do **not** add a case for `mister.ssh_password`. The no-echo invariant requires the stored password to never reach the rendered HTML; the `KindSecret` case in `rowFor` (next step) leaves `StringValue` empty regardless.

- [ ] **Step 4: Add `KindSecret` case to `rowFor`**

Edit `internal/ui/bridge.go::rowFor` (around line 211–241). After the `case adapters.KindEnum:` block, add a new case:

```go
	case adapters.KindEnum:
		r.Kind = "enum"
		// int-valued enums (sample_rate, channels) still serialize
		// as strings on the wire — select/option values must match
		// the TOML-form strings.
		r.StringValue = bridgeLookupString(fd.Key, cur)
	case adapters.KindSecret:
		// Mirrors internal/ui/adapter.go:167–171.
		r.Kind = "text"
		r.InputType = "password"
		r.Placeholder = "Leave empty to keep existing"
		// StringValue stays empty: never echo a stored password into HTML.
		// The preserve-on-empty conditional in handleBridgePOST recovers
		// the prior value when the operator submits without retyping.
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run "RendersSSHUserPrefilled|DoesNotEchoSSHPassword" -v`
Expected: PASS.

(Other `TestHandleBridge_POST_*` tests may still fail — Task 5 fixes them.)

- [ ] **Step 6: Commit**

```bash
git add internal/ui/bridge.go internal/ui/bridge_test.go
git commit -m "feat(ui): render KindSecret + lookup ssh_user on bridge panel"
```

---

## Task 5: Parse `ssh_user` / `ssh_password` from form submissions

**Why:** `parseBridgeForm` translates POSTed form values into a `BridgeConfig`. Without new entries for the SSH keys, every save would zero them out (the parser writes `out.MiSTer = config.MisterConfig{...}` field-by-field, omitting any field it doesn't read). Existing form tests need fixture updates to include the new fields.

**Files:**
- Modify: `internal/ui/form.go` (`parseBridgeForm` at line 38)
- Modify: `internal/ui/form_test.go` (extend fixtures + add coverage)
- Modify: `internal/ui/bridge_test.go` (extend the existing `TestHandleBridge_POST_Success` fixture body)

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/form_test.go`:

```go
// TestParseBridgeForm_SSHFields confirms ssh_user / ssh_password
// round-trip through parseBridgeForm into BridgeConfig.MiSTer.
func TestParseBridgeForm_SSHFields(t *testing.T) {
	form := url.Values{}
	form.Set("mister.host", "192.168.1.42")
	form.Set("mister.port", "32100")
	form.Set("mister.source_port", "32101")
	form.Set("mister.ssh_user", "alice")
	form.Set("mister.ssh_password", "hunter2")
	form.Set("video.modeline", "NTSC_480i")
	form.Set("video.interlace_field_order", "tff")
	form.Set("video.aspect_mode", "auto")
	form.Set("audio.sample_rate", "48000")
	form.Set("audio.channels", "2")
	form.Set("ui.http_port", "32500")
	form.Set("data_dir", "/config")

	got, err := parseBridgeForm(form)
	if err != nil {
		t.Fatalf("parseBridgeForm: %v", err)
	}
	if got.MiSTer.SSHUser != "alice" {
		t.Errorf("SSHUser = %q, want alice", got.MiSTer.SSHUser)
	}
	if got.MiSTer.SSHPassword != "hunter2" {
		t.Errorf("SSHPassword = %q, want hunter2", got.MiSTer.SSHPassword)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestParseBridgeForm_SSHFields -v`
Expected: FAIL — fields don't make it into the candidate (zero-value).

- [ ] **Step 3: Extend `parseBridgeForm`**

Edit `internal/ui/form.go::parseBridgeForm` (around line 42–46). After the `out.MiSTer.SourcePort` line, add:

```go
	out.MiSTer.Host = form.Get("mister.host")
	out.MiSTer.Port = parseIntField(form, "mister.port", errs)
	out.MiSTer.SourcePort = parseIntField(form, "mister.source_port", errs)
	out.MiSTer.SSHUser = form.Get("mister.ssh_user")
	out.MiSTer.SSHPassword = form.Get("mister.ssh_password")
	out.HostIP = form.Get("host_ip")
```

`form.Get` returns the empty string when the key is absent — that's correct for both new fields. Empty-password handling is in Task 6 (preserve-on-empty in the handler), not here.

- [ ] **Step 4: Run the new test to verify it passes**

Run: `go test ./internal/ui/ -run TestParseBridgeForm_SSHFields -v`
Expected: PASS.

- [ ] **Step 5: Update `TestHandleBridge_POST_Success` fixture**

Edit `internal/ui/bridge_test.go::TestHandleBridge_POST_Success` (lines 116–155). Find the `body := strings.NewReader(...)` block (lines 120–132) and add the two SSH fields:

```go
	body := strings.NewReader(
		"mister.host=192.168.1.99" +
			"&mister.port=32100" +
			"&mister.source_port=32101" +
			"&mister.ssh_user=root" +
			"&mister.ssh_password=" +
			"&host_ip=" +
			"&video.modeline=NTSC_480i" +
			"&video.interlace_field_order=bff" +
			"&video.aspect_mode=auto" +
			"&video.lz4_enabled=true" +
			"&audio.sample_rate=48000" +
			"&audio.channels=2" +
			"&ui.http_port=32500" +
			"&data_dir=/config")
```

The `mister.ssh_password=` with no value mirrors the operator submitting the form without re-typing the password. (The preserve-on-empty conditional that handles this lands in Task 6; for now an empty value just yields an empty SSHPassword in the saved candidate. The test asserts on `MiSTer.Host` and `Video.InterlaceFieldOrder` only, so that's fine.)

Apply the same fixture extension to `TestHandleBridge_POST_ValidationError` (lines 157–189) — add `&mister.ssh_user=root` and `&mister.ssh_password=` to its body too. The test asserts on validation failure, not field values.

- [ ] **Step 6: Run all bridge tests to verify**

Run: `go test ./internal/ui/...`
Expected: PASS — all existing tests + the new one.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/form.go internal/ui/form_test.go internal/ui/bridge_test.go
git commit -m "feat(ui): parse ssh_user/ssh_password from bridge form"
```

---

## Task 6: Preserve-on-empty for `ssh_password` in handleBridgePOST

**Why:** The placeholder "Leave empty to keep existing" must actually do what it says, or every bridge save with a touched-but-not-password field silently clears the stored password. The fix is a single conditional in `handleBridgePOST` after `parseBridgeForm` returns: if the candidate's `SSHPassword` is empty, copy the prior value from the saver. No abstraction — if a future field needs the same behavior, generalize then.

**Files:**
- Modify: `internal/ui/bridge.go` (`handleBridgePOST` at line 98)
- Test: `internal/ui/bridge_test.go` (append new test)

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/bridge_test.go`:

```go
// TestHandleBridge_POST_PreservesSSHPasswordOnEmpty verifies that an
// empty ssh_password in the form submission preserves the prior
// stored password (matching the placeholder "Leave empty to keep
// existing"). Without this, every save would silently clear the
// password whenever the operator edited an unrelated field.
func TestHandleBridge_POST_PreservesSSHPasswordOnEmpty(t *testing.T) {
	saver := &fakeBridgeSaver{}
	mux := newBridgeTestServer(t, saver)

	body := strings.NewReader(
		"mister.host=192.168.1.99" +
			"&mister.port=32100" +
			"&mister.source_port=32101" +
			"&mister.ssh_user=root" +
			"&mister.ssh_password=" + // intentionally empty
			"&host_ip=" +
			"&video.modeline=NTSC_480i" +
			"&video.interlace_field_order=tff" +
			"&video.aspect_mode=auto" +
			"&video.lz4_enabled=true" +
			"&audio.sample_rate=48000" +
			"&audio.channels=2" +
			"&ui.http_port=32500" +
			"&data_dir=/config")

	req := httptest.NewRequest("POST", "/ui/bridge/save", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
	}
	if saver.got == nil {
		t.Fatal("saver.Save not called")
	}
	// fakeBridgeSaver.Current() returns SSHPassword = "hunter2".
	// Empty-form submit should preserve it.
	if saver.got.MiSTer.SSHPassword != "hunter2" {
		t.Errorf("SSHPassword = %q, want preserved value 'hunter2'", saver.got.MiSTer.SSHPassword)
	}
}

// TestHandleBridge_POST_OverwritesSSHPasswordWhenProvided verifies the
// operator can change the password by typing a new value. The empty-
// preserve must not lock the password to its initial value.
func TestHandleBridge_POST_OverwritesSSHPasswordWhenProvided(t *testing.T) {
	saver := &fakeBridgeSaver{}
	mux := newBridgeTestServer(t, saver)

	body := strings.NewReader(
		"mister.host=192.168.1.99" +
			"&mister.port=32100" +
			"&mister.source_port=32101" +
			"&mister.ssh_user=root" +
			"&mister.ssh_password=newsecret" +
			"&host_ip=" +
			"&video.modeline=NTSC_480i" +
			"&video.interlace_field_order=tff" +
			"&video.aspect_mode=auto" +
			"&video.lz4_enabled=true" +
			"&audio.sample_rate=48000" +
			"&audio.channels=2" +
			"&ui.http_port=32500" +
			"&data_dir=/config")

	req := httptest.NewRequest("POST", "/ui/bridge/save", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d", rw.Code)
	}
	if saver.got == nil {
		t.Fatal("saver.Save not called")
	}
	if saver.got.MiSTer.SSHPassword != "newsecret" {
		t.Errorf("SSHPassword = %q, want overwrite to 'newsecret'", saver.got.MiSTer.SSHPassword)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run "PreservesSSHPasswordOnEmpty|OverwritesSSHPasswordWhenProvided" -v`
Expected:
- `PreservesSSHPasswordOnEmpty`: FAIL (saved value is empty, not "hunter2").
- `OverwritesSSHPasswordWhenProvided`: PASS already (parseBridgeForm reads the form value and the saver records it).

- [ ] **Step 3: Add the preserve-on-empty conditional**

Edit `internal/ui/bridge.go::handleBridgePOST` (around lines 98–127). Find the block right after `parseBridgeForm` returns (around lines 107–114) and add the conditional just before the `Sectioned.Validate` call (around line 119):

```go
	candidate, parseErr := parseBridgeForm(r.Form)
	if parseErr != nil {
		if fe, ok := parseErr.(FormErrors); ok {
			data := bridgePanelData{Sections: buildBridgeSections(candidate, fe)}
			s.renderPanel(w, "bridge-panel", data)
			return
		}
	}

	// Preserve the stored ssh_password when the operator submits with
	// the field empty. Mirrors the "Leave empty to keep existing"
	// placeholder shown in rowFor's KindSecret case. Without this, any
	// save touching an unrelated field would silently clear the
	// password every time.
	if candidate.MiSTer.SSHPassword == "" {
		candidate.MiSTer.SSHPassword = s.cfg.BridgeSaver.Current().MiSTer.SSHPassword
	}

	// Validate via Sectioned.Validate (covers ports, enum membership,
	// required-mister-host, etc.). Keeps the save path using the same
	// rules as boot-time validation.
	sec := &config.Sectioned{Bridge: candidate}
	if err := sec.Validate(); err != nil {
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run "PreservesSSHPasswordOnEmpty|OverwritesSSHPasswordWhenProvided" -v`
Expected: PASS for both.

Run: `go test ./internal/ui/...`
Expected: PASS — full package.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/bridge.go internal/ui/bridge_test.go
git commit -m "feat(ui): preserve ssh_password on empty form submit"
```

---

## Task 7: Create `internal/misterctl/` package skeleton with injection seam

**Why:** The launcher logic is encapsulated in a dedicated package so `internal/ui/` doesn't depend on `golang.org/x/crypto/ssh`. The package exposes one function (`LaunchGroovy`) and a package-level `dialAndRun` variable that tests swap to capture parameters or return canned errors. The real SSH dial+exec implementation lands in Task 8; this task is just the skeleton + tests against the seam.

**Files:**
- Create: `internal/misterctl/launcher.go`
- Create: `internal/misterctl/launcher_test.go`

- [ ] **Step 1: Create the launcher package skeleton**

Create `internal/misterctl/launcher.go`:

```go
// Package misterctl runs ad-hoc remote commands against the MiSTer
// over SSH. v1 has exactly one operation: LaunchGroovy, which writes
// `load_core /media/fat/_Utility/Groovy.rbf` into the MiSTer's
// /dev/MiSTer_cmd FIFO so the FPGA loads the Groovy core. This is
// the only currently-supported way to put the MiSTer into the right
// core for the bridge to stream into.
//
// The package is package-pure: no globals beyond the dialAndRun
// injection seam, no goroutines beyond the in-flight SSH session,
// no logging. Logging is the caller's responsibility. The password
// is never logged here — by construction, no log call exists in the
// package.
//
// Host-key verification: ssh.InsecureIgnoreHostKey. The bridge is a
// LAN tool and the MiSTer regenerates host keys on reflash; pinning
// keys would surface as a confusing "Host key changed" error after
// every firmware update without adding meaningful protection on a
// trusted residential LAN. If this assumption breaks (mixed LAN,
// public-network deployment), revisit.
package misterctl

import (
	"context"
	"errors"
	"time"
)

// Params bundles the inputs LaunchGroovy needs. Construct fresh on
// each call; the struct holds no shared state.
type Params struct {
	Host     string        // bridge.mister.host, no port suffix
	User     string        // bridge.mister.ssh_user
	Password string        // bridge.mister.ssh_password
	Timeout  time.Duration // dial+exec total budget; 5s in main.go
}

// launchCommand is the literal shell command written to /dev/MiSTer_cmd.
// Hard-coded per the upstream Groovy_MiSTer install instructions which
// specify /media/fat/_Utility as the core's home directory.
const launchCommand = `echo "load_core /media/fat/_Utility/Groovy.rbf" > /dev/MiSTer_cmd`

// dialAndRun is the SSH dial + session.Run sequence; var so tests can
// inject a fake without standing up a real ssh.Server. Production code
// uses realDialAndRun (defined in launcher_ssh.go in Task 8).
var dialAndRun = func(ctx context.Context, p Params) error {
	return errors.New("misterctl: real SSH not yet wired (Task 8)")
}

// LaunchGroovy dials the MiSTer over SSH, runs the canonical
// load-core command, and returns nil on exec success or a wrapped
// error on auth/dial/exec failure. The password is never logged.
//
// LaunchGroovy itself does NOT validate Host == ""; empty-host
// short-circuiting belongs in the UI-layer caller (which has the
// BridgeSaver context to surface a meaningful "MiSTer host not
// configured" message). Keeping LaunchGroovy pure makes it easier
// to reuse from a future CLI flag or alternate caller without
// inheriting that policy.
func LaunchGroovy(ctx context.Context, p Params) error {
	return dialAndRun(ctx, p)
}
```

- [ ] **Step 2: Write the failing tests**

Create `internal/misterctl/launcher_test.go`:

```go
// Note: tests in this file MUST NOT call t.Parallel(). They swap the
// package-level dialAndRun variable to inject fakes; parallel tests
// would race the swap and produce flaky results. Run them serially.

package misterctl

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLaunchGroovy_PassesParams(t *testing.T) {
	var got Params
	prev := dialAndRun
	t.Cleanup(func() { dialAndRun = prev })
	dialAndRun = func(_ context.Context, p Params) error {
		got = p
		return nil
	}

	want := Params{
		Host:     "192.168.1.42",
		User:     "alice",
		Password: "hunter2",
		Timeout:  5 * time.Second,
	}
	if err := LaunchGroovy(context.Background(), want); err != nil {
		t.Fatalf("LaunchGroovy: %v", err)
	}
	if got != want {
		t.Errorf("dialAndRun got %+v, want %+v", got, want)
	}
}

func TestLaunchGroovy_PropagatesError(t *testing.T) {
	sentinel := errors.New("dial timeout")
	prev := dialAndRun
	t.Cleanup(func() { dialAndRun = prev })
	dialAndRun = func(_ context.Context, _ Params) error { return sentinel }

	err := LaunchGroovy(context.Background(), Params{Host: "x"})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want sentinel propagated", err)
	}
}

func TestLaunchGroovy_RespectsContext(t *testing.T) {
	prev := dialAndRun
	t.Cleanup(func() { dialAndRun = prev })
	dialAndRun = func(ctx context.Context, _ Params) error {
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	err := LaunchGroovy(ctx, Params{Host: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
```

- [ ] **Step 3: Run tests to verify they pass**

Run: `go test ./internal/misterctl/ -v`
Expected: PASS — all three tests. The default `dialAndRun` returns the placeholder error, but every test swaps it.

- [ ] **Step 4: Commit**

```bash
git add internal/misterctl/launcher.go internal/misterctl/launcher_test.go
git commit -m "feat(misterctl): launcher skeleton with dialAndRun seam"
```

---

## Task 8: Implement real SSH dial+exec in `realDialAndRun`

**Why:** This task wires up the actual SSH client. It lands the `golang.org/x/crypto` dependency, implements the dial+session.Run flow with `InsecureIgnoreHostKey` and a context-honoring goroutine pattern, and replaces the `dialAndRun` placeholder with `realDialAndRun`. Tests still use the seam (no fake SSH server needed); manual verification against a real MiSTer happens once during implementation.

**Files:**
- Modify: `internal/misterctl/launcher.go` (replace `dialAndRun` placeholder)
- Create: `internal/misterctl/launcher_ssh.go` (the real implementation)
- Modify: `go.mod` / `go.sum` (`go get` the new dep)

- [ ] **Step 1: Add the dependency**

Run: `go -C c:/Users/Jake/Git/MiSTer_GroovyRelay get golang.org/x/crypto/ssh@latest`

Expected: writes `golang.org/x/crypto vX.Y.Z` into `go.mod` and updates `go.sum`.

If the `go -C` syntax doesn't work in your shell, run instead:

```bash
cd c:/Users/Jake/Git/MiSTer_GroovyRelay && go get golang.org/x/crypto/ssh@latest
```

- [ ] **Step 2: Create the real implementation file**

Create `internal/misterctl/launcher_ssh.go`:

```go
package misterctl

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"golang.org/x/crypto/ssh"
)

// sshPort is the port the MiSTer's sshd listens on. Hard-coded —
// MiSTers ship with stock sshd config and we don't expose an
// override field. If a custom MiSTer setup ever needs a different
// port, it's a spec change, not a runtime knob.
const sshPort = 22

// realDialAndRun is the production implementation of dialAndRun.
// Wired in init() so tests retain the ability to swap dialAndRun
// without overwriting this variable.
func realDialAndRun(ctx context.Context, p Params) error {
	cfg := &ssh.ClientConfig{
		User:            p.User,
		Auth:            []ssh.AuthMethod{ssh.Password(p.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         p.Timeout,
	}

	addr := net.JoinHostPort(p.Host, strconv.Itoa(sshPort))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	// Run the launchCommand on a goroutine so we can honor ctx
	// cancellation. session.Run does not take a context; closing the
	// client interrupts an in-flight Run.
	done := make(chan error, 1)
	go func() { done <- session.Run(launchCommand) }()

	select {
	case <-ctx.Done():
		// Force the session to close so the goroutine doesn't leak.
		_ = client.Close()
		<-done // drain
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("exec: %w", err)
		}
		return nil
	}
}

func init() {
	dialAndRun = realDialAndRun
}
```

- [ ] **Step 3: Remove the placeholder `dialAndRun` definition**

Edit `internal/misterctl/launcher.go`. Replace the placeholder `dialAndRun` block (the `var dialAndRun = func(...) { return errors.New("...") }`) with a forward declaration that gets the real value at init time:

```go
// dialAndRun is the SSH dial + session.Run sequence; var so tests can
// inject a fake without standing up a real ssh.Server. Production
// value is realDialAndRun (assigned in launcher_ssh.go's init).
var dialAndRun func(ctx context.Context, p Params) error
```

Also remove the now-unused `errors` import from `launcher.go`.

- [ ] **Step 4: Run tests to verify everything still passes**

Run: `go test ./internal/misterctl/ -v`
Expected: PASS — all three tests still pass (they swap `dialAndRun` at runtime; the production wiring via `init()` is overridden by the test setup).

Run: `go build ./...`
Expected: clean build — the new dep is fetched and the package compiles.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/misterctl/launcher.go internal/misterctl/launcher_ssh.go
git commit -m "feat(misterctl): real SSH dial+exec via golang.org/x/crypto/ssh"
```

---

## Task 9: Add `MisterLauncher` interface to `ui.Config`

**Why:** This is the seam between the UI package and `internal/misterctl/`. The interface stays narrow (`Launch(ctx) error`) so any caller can supply an implementation; main.go wires the real one in Task 13. Adding the field to `ui.Config` here lets later tasks reference it; the handler that consumes it lands in Task 10.

**Files:**
- Modify: `internal/ui/server.go` (extend the `Config` struct)

- [ ] **Step 1: Add the interface and Config field**

Edit `internal/ui/server.go`. After the `AdapterSaver` interface definition (around lines 45–47) and before the `Config` struct (around line 54), add:

```go
// MisterLauncher abstracts the load-core-over-SSH operation so the
// UI package doesn't depend on internal/misterctl directly. Mirrors
// BridgeSaver / AdapterSaver — main.go wires a real implementation
// (a closure that snapshots live credentials from BridgeSaver and
// calls misterctl.LaunchGroovy). Optional: nil surfaces as 500 at
// click time, so unit tests that don't exercise the launch button
// can construct Server with MisterLauncher=nil.
type MisterLauncher interface {
	Launch(ctx context.Context) error
}
```

This requires `context` to be imported. Edit the import block at the top of `server.go` (around lines 7–16):

```go
import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)
```

Then extend the `Config` struct (around line 54):

```go
type Config struct {
	Registry       *adapters.Registry
	BridgeSaver    BridgeSaver
	AdapterSaver   AdapterSaver
	MisterLauncher MisterLauncher
}
```

- [ ] **Step 2: Verify all packages still build**

Run: `go build ./...`
Expected: clean build. No tests need updating yet — `MisterLauncher` is a new optional field that defaults to nil.

Run: `go test ./internal/ui/...`
Expected: PASS — existing tests don't set `MisterLauncher`; nil is a valid value at construction time.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/server.go
git commit -m "feat(ui): add MisterLauncher interface to Config"
```

---

## Task 10: Add `handleBridgeMisterLaunch` handler + result fragment template

**Why:** The handler is a thin shim that calls `MisterLauncher.Launch`, captures success/error, and renders the result fragment. The fragment is a tiny `<div class="status-line">` with green or red styling matching the existing Plex link section. Mounting via `mountPOST` automatically wraps in `csrfMiddleware`.

**Files:**
- Modify: `internal/ui/server.go` (`Mount()` method)
- Modify: `internal/ui/bridge.go` (add handler + data type)
- Create: `internal/ui/templates/mister-launch-result.html`
- Modify: `internal/ui/bridge_test.go` (handler tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/ui/bridge_test.go`:

```go
// fakeMisterLauncher implements MisterLauncher for tests.
type fakeMisterLauncher struct {
	called bool
	err    error
}

func (f *fakeMisterLauncher) Launch(_ context.Context) error {
	f.called = true
	return f.err
}

func TestHandleBridgeMisterLaunch_Success(t *testing.T) {
	saver := &fakeBridgeSaver{}
	launcher := &fakeMisterLauncher{}
	reg := adapters.NewRegistry()
	s, err := New(Config{Registry: reg, BridgeSaver: saver, MisterLauncher: launcher})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("POST", "/ui/bridge/mister/launch", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
	}
	if !launcher.called {
		t.Error("launcher.Launch not called")
	}
	body := rw.Body.String()
	if !strings.Contains(body, "Sent") {
		t.Errorf("expected success fragment with 'Sent', got: %s", body)
	}
	if !strings.Contains(body, "192.168.1.42") {
		t.Error("expected host in success message")
	}
	if !strings.Contains(body, `class="status-line run"`) {
		t.Error("expected green status-line class on success")
	}
}

func TestHandleBridgeMisterLaunch_Error(t *testing.T) {
	saver := &fakeBridgeSaver{}
	launcher := &fakeMisterLauncher{err: errors.New("dial timeout")}
	reg := adapters.NewRegistry()
	s, _ := New(Config{Registry: reg, BridgeSaver: saver, MisterLauncher: launcher})
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("POST", "/ui/bridge/mister/launch", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != 200 {
		t.Fatalf("status = %d", rw.Code)
	}
	body := rw.Body.String()
	if !strings.Contains(body, "SSH failed") {
		t.Errorf("expected error fragment with 'SSH failed', got: %s", body)
	}
	if !strings.Contains(body, "dial timeout") {
		t.Error("expected error message in body")
	}
	if !strings.Contains(body, `class="status-line err"`) {
		t.Error("expected red status-line class on error")
	}
}

func TestHandleBridgeMisterLaunch_NoLauncher(t *testing.T) {
	// MisterLauncher nil → 500. Confirms the construct-without-launcher
	// path doesn't panic but does fail loudly at click time.
	saver := &fakeBridgeSaver{}
	reg := adapters.NewRegistry()
	s, _ := New(Config{Registry: reg, BridgeSaver: saver})
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("POST", "/ui/bridge/mister/launch", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rw.Code)
	}
}
```

The new tests need `context` and `errors` imports. Update the import block at the top of `internal/ui/bridge_test.go`:

```go
import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestHandleBridgeMisterLaunch -v`
Expected: FAIL — route not mounted, handler/template not defined.

- [ ] **Step 3: Add the handler + data type**

Edit `internal/ui/bridge.go`. Append at the end of the file:

```go
// launchResultData is the template root for the launch-result
// fragment. Class = "run" for green / "err" for red; Message holds
// the operator-facing copy (success: "Sent — <command> delivered to
// <host>", error: "SSH failed: <error>").
type launchResultData struct {
	Class   string
	Message string
}

// handleBridgeMisterLaunch invokes MisterLauncher.Launch (with a
// 6-second context budget — 1s slack on top of the 5s SSH dial
// timeout) and renders the result fragment swapped into the launch
// section's slot. The handler is the only place SSH errors surface
// to the operator; spec §"Failure modes" enumerates the cases.
//
// CSRF wrapping is automatic via mountPOST (server.go::Mount).
func (s *Server) handleBridgeMisterLaunch(w http.ResponseWriter, r *http.Request) {
	if s.cfg.MisterLauncher == nil {
		http.Error(w, "launcher not wired", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	err := s.cfg.MisterLauncher.Launch(ctx)
	data := launchResultData{}
	if err != nil {
		data.Class = "err"
		data.Message = fmt.Sprintf("SSH failed: %v", err)
	} else {
		host := s.cfg.BridgeSaver.Current().MiSTer.Host
		data.Class = "run"
		data.Message = fmt.Sprintf("Sent — load_core /media/fat/_Utility/Groovy.rbf delivered to %s", host)
	}
	s.renderPanel(w, "mister-launch-result", data)
}
```

The new handler needs `context` and `time` imports. Update `internal/ui/bridge.go`'s import block (at the top of the file, around lines 3–10):

```go
import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)
```

- [ ] **Step 4: Mount the route**

Edit `internal/ui/server.go::Mount()` (around line 89). Find the bridge-panel route mounts (lines 107–109) and add a new line after `dismiss-first-run`:

```go
	// Bridge panel.
	mux.HandleFunc("GET /ui/bridge", s.handleBridgeGET)
	s.mountPOST(mux, "/ui/bridge/save", s.handleBridgePOST)
	s.mountPOST(mux, "/ui/bridge/dismiss-first-run", s.handleBridgeDismissFirstRun)
	s.mountPOST(mux, "/ui/bridge/mister/launch", s.handleBridgeMisterLaunch)
```

- [ ] **Step 5: Create the result fragment template**

Create `internal/ui/templates/mister-launch-result.html`:

```html
{{define "mister-launch-result"}}
<div class="status-line {{.Class}}">{{.Message}}</div>
{{end}}
```

Reuses the existing `.status-line` palette already used by the Plex link section (lines 44, 58, 73 of `internal/adapters/plex/link_ui.go`).

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run TestHandleBridgeMisterLaunch -v`
Expected: PASS — all three tests.

Run: `go test ./internal/ui/...`
Expected: PASS — full package.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/bridge.go internal/ui/server.go internal/ui/templates/mister-launch-result.html internal/ui/bridge_test.go
git commit -m "feat(ui): add launch handler + result fragment template"
```

---

## Task 11: Append the Launch section block to bridge-panel.html

**Why:** The button must live outside the bridge save `<form>` to avoid `required`-validation coupling and `hx-include` form-field bloat. The block is unconditional — bridges always have the SSH credential fields configured, so always rendering the launch button is correct (matching `adapter-panel.html`'s `ExtraHTMLProvider` pattern). The button's `hx-disabled-elt="this"` provides implicit click serialization.

**Files:**
- Modify: `internal/ui/templates/bridge-panel.html`
- Modify: `internal/ui/bridge_test.go` (verify the launch button renders)

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/bridge_test.go`:

```go
// TestHandleBridge_GET_RendersLaunchSection verifies the post-form
// Launch section block is rendered with the launch button and result
// slot. The block is unconditional — every bridge GET should include
// it.
func TestHandleBridge_GET_RendersLaunchSection(t *testing.T) {
	mux := newBridgeTestServer(t, &fakeBridgeSaver{})
	req := httptest.NewRequest("GET", "/ui/bridge", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	body := rw.Body.String()
	wantSnippets := []string{
		`hx-post="/ui/bridge/mister/launch"`,
		`id="mister-launch-slot"`,
		"Launch GroovyMiSTer",
		`type="button"`,
	}
	for _, w := range wantSnippets {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in body", w)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestHandleBridge_GET_RendersLaunchSection -v`
Expected: FAIL — none of the snippets are in the rendered body.

- [ ] **Step 3: Append the Launch section block to the template**

Edit `internal/ui/templates/bridge-panel.html`. Find the closing `</form>` tag at line 52 and the `{{end}}` at line 53. Insert the new block **between** them:

```html
	<div style="margin-top: 24px; text-align: right;">
		<button type="submit" class="btn">Save Bridge ▸</button>
	</div>
</form>

{{/*
  Launch section is hard-coded outside the auto-rendered .Sections
  loop. Number "06" is hard-coded; if the bridge ever grows another
  auto-rendered section, drop the leading number here or pick a
  non-numeric label. See spec
  docs/specs/2026-04-25-mister-ssh-launch-design.md
  ("Launch button placement").
*/}}
<div class="section">
	<h3><span class="num">06 —</span> Launch</h3>
	<div class="field">
		<label>Load Groovy core on the MiSTer</label>
		<div>
			<button type="button" class="btn"
				hx-post="/ui/bridge/mister/launch"
				hx-target="#mister-launch-slot"
				hx-swap="innerHTML"
				hx-disabled-elt="this"
				hx-headers='{"Sec-Fetch-Site":"same-origin"}'>
				Launch GroovyMiSTer
			</button>
			<div id="mister-launch-slot" style="margin-top: 8px;"></div>
			<div class="help">
				Sends <code>load_core /media/fat/_Utility/Groovy.rbf</code>
				to <code>/dev/MiSTer_cmd</code> over SSH using the
				credentials in section 05.
			</div>
		</div>
	</div>
</div>
{{end}}
```

The closing `{{end}}` is the existing one at the end of the `bridge-panel` define block. Make sure the new block lands inside `{{define "bridge-panel"}}...{{end}}`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ui/ -run TestHandleBridge_GET_RendersLaunchSection -v`
Expected: PASS.

Run: `go test ./internal/ui/...`
Expected: PASS — full package.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/templates/bridge-panel.html internal/ui/bridge_test.go
git commit -m "feat(ui): add Launch GroovyMiSTer button to bridge panel"
```

---

## Task 12: Add `bridgeMisterLauncher` closure adapter in `cmd/`

**Why:** This is the seam between `ui.MisterLauncher` and `misterctl.LaunchGroovy`. The closure snapshots live credentials from `BridgeSaver.Current()` at each call, so credential edits made in the UI apply hot — no restart needed. The closure also owns the empty-host short-circuit (returns a "MiSTer host not configured" error before dialing). Lives in `cmd/mister-groovy-relay/` because that's the only place that already imports both `ui` and `misterctl`; placing it in `internal/uiserver/` would invert the boundary by pulling SSH client code into uiserver.

**Files:**
- Create: `cmd/mister-groovy-relay/launcher.go`
- Create: `cmd/mister-groovy-relay/launcher_test.go`

- [ ] **Step 1: Create the closure adapter**

Create `cmd/mister-groovy-relay/launcher.go`:

```go
package main

import (
	"context"
	"errors"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/misterctl"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ui"
)

// bridgeMisterLauncher is the closure adapter wiring ui.MisterLauncher
// to misterctl.LaunchGroovy. Snapshots host/user/password from the
// live BridgeSaver at each call so credential edits apply hot — no
// bridge restart needed.
//
// Owns the empty-host short-circuit: returns "MiSTer host not
// configured" before dialing if BridgeSaver.Current().MiSTer.Host is
// empty. (LaunchGroovy itself is policy-free; UI-layer "config not
// set" semantics belong here.)
type bridgeMisterLauncher struct {
	bridge  ui.BridgeSaver
	timeout time.Duration
}

func (b bridgeMisterLauncher) Launch(ctx context.Context) error {
	cur := b.bridge.Current()
	if cur.MiSTer.Host == "" {
		return errors.New("MiSTer host not configured (set bridge.mister.host)")
	}
	return misterctl.LaunchGroovy(ctx, misterctl.Params{
		Host:     cur.MiSTer.Host,
		User:     cur.MiSTer.SSHUser,
		Password: cur.MiSTer.SSHPassword,
		Timeout:  b.timeout,
	})
}
```

- [ ] **Step 2: Write the failing tests**

The closure-seam test needs to capture the `Params` that `LaunchGroovy` would have received. The simplest way is to swap `misterctl.dialAndRun` (the package-level seam from Task 7); since tests in this file run inside the `main` package, they import the misterctl package and can access its exported helpers. Because `dialAndRun` is package-private, expose a small test seam in `internal/misterctl/`.

Add a test-only file (build-tagged or a small public helper) so external tests can swap `dialAndRun`. The cleanest shape is a public test helper that reflects the package-private seam.

Create `internal/misterctl/launcher_testseam.go` (no build tag — small, intentional public API):

```go
package misterctl

import "context"

// SwapDialForTesting replaces dialAndRun with fn and returns the
// previous value. Production code never calls this; it exists so
// callers in other packages (the cmd-package closure-seam test) can
// inject a capture without standing up a real ssh.Server.
//
// Callers MUST restore the previous value (typically via t.Cleanup).
// The package-level var is not goroutine-safe under swap, so tests
// using SwapDialForTesting must NOT call t.Parallel().
func SwapDialForTesting(fn func(context.Context, Params) error) func(context.Context, Params) error {
	prev := dialAndRun
	dialAndRun = fn
	return prev
}
```

Then create `cmd/mister-groovy-relay/launcher_test.go`:

```go
// Note: these tests swap misterctl.dialAndRun via SwapDialForTesting
// and MUST NOT call t.Parallel().

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/misterctl"
)

// fakeBridgeSaver is the minimal BridgeSaver used by the closure-seam
// test. Implements only Current(); Save() is unused here.
type fakeBridgeSaver struct {
	cur config.BridgeConfig
}

func (f *fakeBridgeSaver) Current() config.BridgeConfig             { return f.cur }
func (f *fakeBridgeSaver) Save(_ config.BridgeConfig) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

func TestBridgeMisterLauncher_PassesParams(t *testing.T) {
	var got misterctl.Params
	prev := misterctl.SwapDialForTesting(func(_ context.Context, p misterctl.Params) error {
		got = p
		return nil
	})
	t.Cleanup(func() { misterctl.SwapDialForTesting(prev) })

	saver := &fakeBridgeSaver{cur: config.BridgeConfig{
		MiSTer: config.MisterConfig{
			Host: "192.168.1.42", Port: 32100, SourcePort: 32101,
			SSHUser: "alice", SSHPassword: "hunter2",
		},
	}}
	launcher := bridgeMisterLauncher{bridge: saver, timeout: 5 * time.Second}

	if err := launcher.Launch(context.Background()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	want := misterctl.Params{
		Host:     "192.168.1.42",
		User:     "alice",
		Password: "hunter2",
		Timeout:  5 * time.Second,
	}
	if got != want {
		t.Errorf("misterctl.LaunchGroovy got %+v, want %+v", got, want)
	}
}

func TestBridgeMisterLauncher_EmptyHostShortCircuits(t *testing.T) {
	dialed := false
	prev := misterctl.SwapDialForTesting(func(_ context.Context, _ misterctl.Params) error {
		dialed = true
		return nil
	})
	t.Cleanup(func() { misterctl.SwapDialForTesting(prev) })

	saver := &fakeBridgeSaver{cur: config.BridgeConfig{
		MiSTer: config.MisterConfig{Host: ""}, // empty
	}}
	launcher := bridgeMisterLauncher{bridge: saver, timeout: 5 * time.Second}

	err := launcher.Launch(context.Background())
	if err == nil {
		t.Fatal("expected empty-host error, got nil")
	}
	if !strings.Contains(err.Error(), "MiSTer host not configured") {
		t.Errorf("err = %q, want 'MiSTer host not configured'", err)
	}
	if dialed {
		t.Error("dialAndRun called on empty-host short-circuit")
	}
}
```

- [ ] **Step 3: Run tests to verify they pass**

Run: `go test ./cmd/mister-groovy-relay/ -v`
Expected: PASS — both closure-seam tests.

Run: `go test ./internal/misterctl/ -v`
Expected: PASS — the addition of `SwapDialForTesting` doesn't break existing tests.

- [ ] **Step 4: Commit**

```bash
git add cmd/mister-groovy-relay/launcher.go cmd/mister-groovy-relay/launcher_test.go internal/misterctl/launcher_testseam.go
git commit -m "feat(cmd): bridgeMisterLauncher closure adapter + tests"
```

---

## Task 13: Wire the launcher into main.go

**Why:** Final wiring step. Construct a `bridgeMisterLauncher` and pass it through `ui.Config`. The bridge starts surfacing the launch button on the next process start.

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go` (the `ui.New(ui.Config{...})` call at line 153)

- [ ] **Step 1: Construct the launcher and pass it to ui.New**

Edit `cmd/mister-groovy-relay/main.go`. Find the `ui.New` call (around lines 153–157):

```go
	uiSrv, err := ui.New(ui.Config{Registry: reg, BridgeSaver: saver, AdapterSaver: adapterSaver})
	if err != nil {
		slog.Error("ui init", "err", err)
		os.Exit(1)
	}
```

Replace with:

```go
	misterLauncher := bridgeMisterLauncher{bridge: saver, timeout: 5 * time.Second}

	uiSrv, err := ui.New(ui.Config{
		Registry:       reg,
		BridgeSaver:    saver,
		AdapterSaver:   adapterSaver,
		MisterLauncher: misterLauncher,
	})
	if err != nil {
		slog.Error("ui init", "err", err)
		os.Exit(1)
	}
```

(`time` is already imported in main.go — confirm by checking the existing import block at the top of the file.)

- [ ] **Step 2: Verify the binary builds**

Run: `go build ./...`
Expected: clean build.

Run: `go test ./...`
Expected: PASS — full repo test suite.

- [ ] **Step 3: Run linter**

Run: `make lint`
Expected: clean — `go vet` passes.

- [ ] **Step 4: Run race detector**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/main.go
git commit -m "feat(cmd): wire bridgeMisterLauncher into ui.Config"
```

---

## Task 14 (optional): Manual smoke test against a real MiSTer

**Why:** The `internal/misterctl/` test suite covers the seam but does not exercise the real SSH dial+exec path. The spec calls this out as manual verification once during implementation. If a real MiSTer isn't available, skip this task — the wire-level behavior is implicitly covered by the standard `golang.org/x/crypto/ssh` test suite, and the seam tests confirm the params flow correctly.

**Procedure:**

- [ ] **Step 1: Start the bridge**

```bash
cd c:/Users/Jake/Git/MiSTer_GroovyRelay
make build
./mister-groovy-relay --config /tmp/test-config.toml
```

(Adjust the config path if needed — the bridge auto-creates a default config.toml on first run if missing.)

- [ ] **Step 2: Open the Settings UI in a browser**

Navigate to `http://localhost:32500/ui/bridge`.

- [ ] **Step 3: Configure SSH credentials**

In the "MiSTer Control" section (section 05):
- Set SSH User to `root`.
- Set SSH Password to `1` (or your MiSTer's actual password).
- Click "Save Bridge ▸".
- Confirm the toast says "Saved — applied live." (hot-swap scope).

- [ ] **Step 4: Verify host configured**

In the "Network" section (section 01), confirm "MiSTer Host" matches your real MiSTer's IP address. Save if changed.

- [ ] **Step 5: Click Launch GroovyMiSTer**

Click the button in section 06.

Expected outcomes (one of):
- **Success:** green "Sent — load_core /media/fat/_Utility/Groovy.rbf delivered to <ip>" within ~500ms. The MiSTer's CRT switches to the Groovy core within a second or two.
- **Wrong password:** red "SSH failed: ssh: handshake failed: ssh: unable to authenticate, ..."
- **Wrong host / MiSTer offline:** red "SSH failed: dial tcp <ip>:22: i/o timeout" within ~5s.
- **`Groovy.rbf` not in `_Utility/`:** green success toast (echo writes to FIFO regardless), but the CRT does not switch cores. Known sharp edge per spec.

- [ ] **Step 6: Document results**

If everything works: nothing more to do.
If something doesn't: capture the exact error message and revisit either Task 8 (real SSH impl) or the spec's failure-modes table.

---

## Self-review checklist (verified before plan publication)

- [x] **Spec coverage:** Each item in the spec's Implementer's checklist has a corresponding task. Cross-referenced:
  - Config fields → Task 1
  - `defaultBridge` + example.toml → Task 1
  - `scopeForBridgeField` + `diffBridgeConfig` → Task 2
  - `bridgeFields()` → Task 3
  - `bridgeLookupString` ssh_user only + `rowFor` KindSecret → Task 4
  - `parseBridgeForm` → Task 5
  - `handleBridgePOST` preserve-on-empty → Task 6
  - `internal/misterctl/launcher.go` + tests → Tasks 7, 8
  - `MisterLauncher` interface → Task 9
  - `handleBridgeMisterLaunch` + result fragment → Task 10
  - `bridge-panel.html` Launch section → Task 11
  - `mister-launch-result.html` template → Task 10
  - Closure adapter + closure-seam tests → Task 12
  - main.go wiring → Task 13
  - `go.mod` dep → Task 8
  - Manual smoke test → Task 14
- [x] **No placeholders:** Every step shows actual code or actual commands. No "TBD", no "implement appropriate validation", no "similar to Task N".
- [x] **Type consistency:** `Params`, `BridgeConfig`, `MisterConfig`, `MisterLauncher`, `bridgeMisterLauncher`, `launchResultData`, `dialAndRun`, `realDialAndRun`, `SwapDialForTesting`, `LaunchGroovy`, `Launch`, `Current`, `Save`, `bridgeLookupString`, `rowFor`, `parseBridgeForm`, `handleBridgePOST`, `handleBridgeMisterLaunch`, `scopeForBridgeField`, `diffBridgeConfig`, `defaultBridge`, `bridgeFields`, `Mount`, `mountPOST`, `renderPanel` — verified consistent across every task.
- [x] **TDD discipline:** Every code-changing task has a failing-test step before the implementation step.
- [x] **Frequent commits:** Each task ends with a single commit covering only the files it touched.
