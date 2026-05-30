# Receiver Chassis URL Adapter Widgets (Phase 4F) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 4D URL-adapter stub in the chassis settings drawer with a real pane — three new standard yt-dlp fields plus two bespoke widgets (an editable host tag-list and a file-backed cookies textarea) — without breaking any Phase 4 isolation invariant.

**Architecture:** Standard fields (rows 1/2/4/5) flow through the existing 4D `SaveTouched` field path once the URL adapter's `Fields()`/`CurrentValues()` expand. The host tag-list and cookies are bespoke per-adapter widgets driven by two new chassis-owned interfaces (`AdapterHostEditor`, `AdapterCookieStore`) satisfied by `cmd/` wrappers, exactly like 4D's `AdapterSettingsSaver`. Host edits persist the whole `ytdlp_hosts` array via a new `*uiserver.AdapterSaver.SaveValues` method that mirrors `SaveTouched`'s read→validate→write→apply pipeline; cookies are file-backed and never touch TOML.

**Tech Stack:** Go 1.26 stdlib (`html/template`, `net/http`, `sync`), BurntSushi/toml, `go:embed` assets, vanilla ES2022. Spec: [`docs/superpowers/specs/2026-05-29-receiver-chassis-url-adapter-widgets-design.md`](../specs/2026-05-29-receiver-chassis-url-adapter-widgets-design.md).

---

## Prerequisites

**Branch 4F from `main`** (4A–4D are merged; 4E is in flight on a separate branch and is independent of 4F):

```bash
git worktree add ../MiSTer_GroovyRelay-4f -b phase-4f-url-widgets main
cd ../MiSTer_GroovyRelay-4f
```

**Verify the 4D contract is present before starting:**
```bash
grep -n "func (r \*AdapterSaver) SaveTouched" internal/uiserver/adapter_saver.go
grep -n "AdapterSettingsSaver" internal/chassis/server.go
grep -n "func (b \*bridgeAdapterSettingsSaver)" cmd/mister-groovy-relay/adapter_settings_saver.go
```
Expected: a match in each. If any is missing, 4D is not in this branch — stop and rebase onto current `main`.

**Naming facts (verified against the tree — do not "correct" these):**

| Looks like | Actually is |
|---|---|
| `chassis.NewServer` | `chassis.New(cfg) (*Server, error)` |
| `*Server.Handler()` | `*Server.Mount(mux *http.ServeMux)` |
| `requireSameOrigin` method | package func `requireSameOrigin(next http.Handler) http.Handler`; **guards POST only** (`sameorigin.go:14`) |
| adapter save success writer | `writeSettingsSuccess(w, scope)` → `{ok:true, scope}` (`settings.go:761`) |
| chip error writer | `writeSettingsChip(w, status, chip)` → `{ok:false, chip}` (`settings.go:768`) |
| field error writer | `writeSettingsFieldErrors(w, status, map[string]string)` → `{ok:false, errors}` (`settings.go:775`) |
| error unwrapper | `emitSaveError(w, err)` — maps `settingsChipError`→chip, `fieldErrorBearerForChassis`→errors (`settings.go:1163`) |
| scope→label | `chassis.WireLabelForScope(scope) (string, bool)` (`settings.go:606`) |
| cmd chip error | `cmdChipError{status, chip}` (`adapter_settings_saver.go:109`) |
| cmd field errors | `cmdAdapterFieldErrors{errs}` (`adapter_settings_saver.go:118`) |
| registry lookup | `adapterLookup interface{ Get(name) (adapters.Adapter, bool) }` (`adapter_settings_saver.go:13`); `*adapters.Registry` satisfies it |

**Invariants (must hold at every commit):** `internal/chassis` imports **neither** `internal/adapters/url` **nor** `internal/uiserver` (`import_check_test.go` enforces this — zero edits expected). The shared `field` template helper and the `uiserver` `overlayTouched` scalar overlay gain **no** new field-type primitive. CI's four gates (`go vet`, `go test`, `go test -race`, `go test -tags=integration`) stay green.

---

## File Structure

**Modified:**
- `internal/adapters/url/adapter.go` — expand `Fields()` (1→4), expand `CurrentValues()` (1→4 keys), add exported `CurrentHosts()`, `SaveCookies`, `ClearCookies`, `CookieStat`, `ValidateCookies` methods.
- `internal/adapters/url/config.go` — add package func `NormalizeHosts([]string) ([]string, error)`.
- `internal/uiserver/adapter_saver.go` — add `SaveValues` method.
- `internal/chassis/settings.go` — add `AdapterHostEditor`, `AdapterCookieStore` interfaces + `CookieStatusView`; add `handleSettingsAdapterHostsPost`, `handleSettingsAdapterCookiesPost`, `handleSettingsAdapterCookiesClear`; add `writeSettingsHosts`, `writeSettingsCookie`, `readCookiesField`.
- `internal/chassis/server.go` — add two `Config` fields; register three routes in `Mount`.
- `internal/chassis/data.go` — extend `AdapterPaneData`; add `"url"` to the adapter loop and populate host/cookie data.
- `internal/chassis/templates.go` — add `fieldByKey` funcmap helper.
- `internal/chassis/templates/settings-adapters.html` — swap the URL stub for the real template call.
- `internal/chassis/static/chassis.css` — port tag-list/cookies/status-pill rules.
- `internal/chassis/static/settings-drawer.js` — add `[data-host-editor]` and `[data-cookies]` handlers.
- `cmd/mister-groovy-relay/main.go` — construct two wrappers; add two `chassis.Config` fields.

**Created:**
- `internal/chassis/templates/settings-adapter-url.html` — URL pane container + `url-host-editor` + `url-cookies` partials.
- `cmd/mister-groovy-relay/adapter_host_editor.go` — `bridgeAdapterHostEditor`.
- `cmd/mister-groovy-relay/adapter_cookie_store.go` — `bridgeAdapterCookieStore`.
- `cmd/mister-groovy-relay/adapter_host_editor_test.go`, `adapter_cookie_store_test.go` — wrapper unit tests.
- `cmd/mister-groovy-relay/url_widgets_e2e_test.go` — integration end-to-end.

**Test files extended:** `internal/adapters/url/adapter_test.go`, `internal/uiserver/adapter_saver_test.go`, `internal/chassis/settings_test.go`, `internal/chassis/data_test.go`, `internal/chassis/chassis_test.go`.

---

## Task 1: Expand URL adapter `Fields()` to four entries

**Files:**
- Modify: `internal/adapters/url/adapter.go:190-201`
- Test: `internal/adapters/url/adapter_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/url/adapter_test.go`:

```go
func TestFields_ExposesYtdlpStandardFields(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	byKey := map[string]adapters.FieldDef{}
	for _, f := range a.Fields() {
		byKey[f.Key] = f
	}
	for _, want := range []struct {
		key  string
		kind adapters.FieldKind
	}{
		{"enabled", adapters.KindBool},
		{"ytdlp_enabled", adapters.KindBool},
		{"ytdlp_format", adapters.KindText},
		{"ytdlp_resolve_timeout_seconds", adapters.KindInt},
	} {
		fd, ok := byKey[want.key]
		if !ok {
			t.Errorf("Fields() missing %q", want.key)
			continue
		}
		if fd.Kind != want.kind {
			t.Errorf("%q Kind = %v, want %v", want.key, fd.Kind, want.kind)
		}
		if fd.ApplyScope != adapters.ScopeHotSwap {
			t.Errorf("%q ApplyScope = %v, want ScopeHotSwap", want.key, fd.ApplyScope)
		}
	}
	// ytdlp_hosts is NOT a standard field — it is driven by the bespoke widget.
	if _, ok := byKey["ytdlp_hosts"]; ok {
		t.Errorf("Fields() must not expose ytdlp_hosts (bespoke widget owns it)")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/adapters/url -run TestFields_ExposesYtdlpStandardFields -v`
Expected: FAIL (missing `ytdlp_enabled`/`ytdlp_format`/`ytdlp_resolve_timeout_seconds`).

- [ ] **Step 3: Replace `Fields()`**

In `internal/adapters/url/adapter.go`, replace the `Fields()` method body with:

```go
func (a *Adapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{
		{
			Key:        "enabled",
			Label:      "Enabled",
			Help:       "Turn the URL adapter on or off. When enabled, the global Cast drawer accepts http(s) media URLs.",
			Kind:       adapters.KindBool,
			Default:    false,
			ApplyScope: adapters.ScopeHotSwap,
		},
		{
			Key:        "ytdlp_enabled",
			Label:      "yt-dlp resolver",
			Help:       "Master switch. When on, mode=auto routes URLs whose host matches the list below through yt-dlp.",
			Kind:       adapters.KindBool,
			Default:    true,
			ApplyScope: adapters.ScopeHotSwap,
		},
		{
			Key:        "ytdlp_format",
			Label:      "yt-dlp format",
			Help:       "Format selector. Default prefers ≤720p video with broad codec compatibility.",
			Kind:       adapters.KindText,
			Default:    "bv*[height<=720]+ba/bv*+ba/b",
			ApplyScope: adapters.ScopeHotSwap,
		},
		{
			Key:        "ytdlp_resolve_timeout_seconds",
			Label:      "Resolve timeout (s)",
			Help:       "Per-URL timeout for yt-dlp resolution (5–120s).",
			Kind:       adapters.KindInt,
			Default:    30,
			ApplyScope: adapters.ScopeHotSwap,
		},
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/adapters/url -run TestFields_ExposesYtdlpStandardFields -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/url/adapter.go internal/adapters/url/adapter_test.go
git commit -m "feat(url): expose yt-dlp standard fields in Fields()"
```

---

## Task 2: Expand URL adapter `CurrentValues()` to four keys

**Files:**
- Modify: `internal/adapters/url/adapter.go:370-374`
- Test: `internal/adapters/url/adapter_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/url/adapter_test.go`:

```go
func TestCurrentValues_IncludesYtdlpFields(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Seed a known config so the values are deterministic.
	a.cfg = Config{
		Enabled:                    true,
		YtdlpEnabled:               false,
		YtdlpFormat:                "best",
		YtdlpResolveTimeoutSeconds: 42,
		YtdlpHosts:                 []string{"youtube.com"},
	}
	v := a.CurrentValues()
	if v["enabled"] != true {
		t.Errorf("enabled = %v, want true", v["enabled"])
	}
	if v["ytdlp_enabled"] != false {
		t.Errorf("ytdlp_enabled = %v, want false", v["ytdlp_enabled"])
	}
	if v["ytdlp_format"] != "best" {
		t.Errorf("ytdlp_format = %v, want best", v["ytdlp_format"])
	}
	if v["ytdlp_resolve_timeout_seconds"] != 42 {
		t.Errorf("ytdlp_resolve_timeout_seconds = %v, want 42", v["ytdlp_resolve_timeout_seconds"])
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/adapters/url -run TestCurrentValues_IncludesYtdlpFields -v`
Expected: FAIL (only `enabled` returned today).

- [ ] **Step 3: Replace `CurrentValues()`**

In `internal/adapters/url/adapter.go`, replace the `CurrentValues()` body with:

```go
// CurrentValues implements ui.ValueProvider via duck-typing — surfaces
// the current cfg values to the UI for form prefill. Must stay in
// lockstep with Fields(): the chassis form prefill (4D's
// AdapterSettingsSaver.Current path) renders blank/off for any Fields()
// key missing here, and this map is also SaveTouched's missing-section
// fallback. ytdlp_hosts is intentionally absent — the host widget reads
// it via CurrentHosts(), and a first-save with no disk section keeps the
// DefaultConfig() host list (ApplyConfig decodes onto DefaultConfig).
func (a *Adapter) CurrentValues() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{
		"enabled":                       a.cfg.Enabled,
		"ytdlp_enabled":                 a.cfg.YtdlpEnabled,
		"ytdlp_format":                  a.cfg.YtdlpFormat,
		"ytdlp_resolve_timeout_seconds": a.cfg.YtdlpResolveTimeoutSeconds,
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/adapters/url -run TestCurrentValues_IncludesYtdlpFields -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/url/adapter.go internal/adapters/url/adapter_test.go
git commit -m "feat(url): expand CurrentValues to cover yt-dlp standard fields"
```

---

## Task 3: Add `NormalizeHosts` package func + `CurrentHosts` method

**Files:**
- Modify: `internal/adapters/url/config.go`, `internal/adapters/url/adapter.go`
- Test: `internal/adapters/url/config_test.go`, `internal/adapters/url/adapter_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/adapters/url/config_test.go`:

```go
func TestNormalizeHosts_LowercasesAndTrims(t *testing.T) {
	got, err := NormalizeHosts([]string{"  YouTube.com  ", "Twitch.TV"})
	if err != nil {
		t.Fatalf("NormalizeHosts: %v", err)
	}
	want := []string{"youtube.com", "twitch.tv"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNormalizeHosts_RejectsURLSyntax(t *testing.T) {
	_, err := NormalizeHosts([]string{"https://youtube.com/watch"})
	if err == nil {
		t.Fatal("NormalizeHosts accepted a host with URL syntax; want error")
	}
	fe, ok := err.(adapters.FieldErrors)
	if !ok {
		t.Fatalf("err type = %T, want adapters.FieldErrors", err)
	}
	if len(fe) == 0 || fe[0].Key != "ytdlp_hosts" {
		t.Errorf("FieldErrors = %v, want one keyed ytdlp_hosts", fe)
	}
}

func TestNormalizeHosts_EmptyListOK(t *testing.T) {
	got, err := NormalizeHosts(nil)
	if err != nil {
		t.Fatalf("NormalizeHosts(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
```

Append to `internal/adapters/url/adapter_test.go`:

```go
func TestCurrentHosts_ReturnsCopy(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.cfg.YtdlpHosts = []string{"youtube.com", "twitch.tv"}
	got := a.CurrentHosts()
	if len(got) != 2 || got[0] != "youtube.com" || got[1] != "twitch.tv" {
		t.Fatalf("CurrentHosts() = %v", got)
	}
	// Mutating the returned slice must not affect the adapter's config.
	got[0] = "evil.com"
	if a.cfg.YtdlpHosts[0] != "youtube.com" {
		t.Errorf("CurrentHosts returned an aliased slice; cfg mutated to %v", a.cfg.YtdlpHosts)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/adapters/url -run 'TestNormalizeHosts|TestCurrentHosts' -v`
Expected: FAIL (`undefined: NormalizeHosts`, `a.CurrentHosts undefined`).

- [ ] **Step 3: Add `NormalizeHosts` to `config.go`**

Append to `internal/adapters/url/config.go`:

```go
// NormalizeHosts validates and lowercases a candidate yt-dlp host list
// using the exact rules Config.Validate enforces, returning the cleaned
// list. It constructs a throwaway DefaultConfig (so the timeout range
// check passes) and runs Validate, which lowercases YtdlpHosts in place
// only when every entry is valid. Pure: it does not mutate any adapter
// state. Returns adapters.FieldErrors (keyed "ytdlp_hosts") on invalid
// input. This is the single normalization source the chassis host route
// uses — the uiserver saver cannot recover normalized values because the
// adapters.Validator contract returns no values.
func NormalizeHosts(hosts []string) ([]string, error) {
	probe := DefaultConfig()
	probe.YtdlpHosts = hosts
	if err := probe.Validate(); err != nil {
		return nil, err
	}
	return probe.YtdlpHosts, nil
}
```

- [ ] **Step 4: Add `CurrentHosts` to `adapter.go`**

Append to `internal/adapters/url/adapter.go` (near `CurrentValues`):

```go
// CurrentHosts returns a copy of the current yt-dlp host allowlist for
// paint by the chassis host-editor widget. The copy prevents callers
// from aliasing the config slice.
func (a *Adapter) CurrentHosts() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.cfg.YtdlpHosts))
	copy(out, a.cfg.YtdlpHosts)
	return out
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/adapters/url -run 'TestNormalizeHosts|TestCurrentHosts' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/url/config.go internal/adapters/url/adapter.go internal/adapters/url/config_test.go internal/adapters/url/adapter_test.go
git commit -m "feat(url): add NormalizeHosts + CurrentHosts for the chassis host widget"
```

---

## Task 4: Add exported cookie methods (`ValidateCookies`, `SaveCookies`, `ClearCookies`, `CookieStat`)

**Files:**
- Modify: `internal/adapters/url/cookies.go`
- Test: `internal/adapters/url/cookies_test.go`

These wrap the existing unexported `validateCookies`/`saveCookies`/`clearCookies`/`statCookies` so the `cmd/` cookie wrapper (package `main`, which cannot reach unexported helpers) can compose them. The legacy `handleCookiesSet`/`handleCookiesClear` HTTP handlers are left untouched (they belong to `/ui/*`).

- [ ] **Step 1: Write the failing tests**

Append to `internal/adapters/url/cookies_test.go`:

```go
func TestAdapter_CookieMethods_RoundTrip(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Initially absent.
	if _, ok, err := a.CookieStat(); err != nil || ok {
		t.Fatalf("CookieStat initial = (_, %v, %v), want (_, false, nil)", ok, err)
	}
	// Validate accepts good cookies.
	if err := a.ValidateCookies([]byte(sampleCookies)); err != nil {
		t.Fatalf("ValidateCookies(good): %v", err)
	}
	// Save writes the file.
	st, err := a.SaveCookies([]byte(sampleCookies))
	if err != nil {
		t.Fatalf("SaveCookies: %v", err)
	}
	if st.Size != int64(len(sampleCookies)) {
		t.Errorf("Size = %d, want %d", st.Size, len(sampleCookies))
	}
	got, ok, err := a.CookieStat()
	if err != nil || !ok {
		t.Fatalf("CookieStat after save = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got.Size != st.Size {
		t.Errorf("CookieStat Size = %d, want %d", got.Size, st.Size)
	}
	// Clear removes it.
	if err := a.ClearCookies(); err != nil {
		t.Fatalf("ClearCookies: %v", err)
	}
	if _, ok, _ := a.CookieStat(); ok {
		t.Errorf("CookieStat after clear ok=true, want false")
	}
}

func TestAdapter_ValidateCookies_RejectsGarbage(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.ValidateCookies([]byte("not cookies")); err == nil {
		t.Fatal("ValidateCookies accepted garbage; want error")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/adapters/url -run 'TestAdapter_CookieMethods_RoundTrip|TestAdapter_ValidateCookies_RejectsGarbage' -v`
Expected: FAIL (`a.CookieStat undefined`, etc.).

- [ ] **Step 3: Add the exported methods**

Append to `internal/adapters/url/cookies.go`:

```go
// ValidateCookies runs the lenient Netscape-format check on raw cookie
// bytes without writing anything. Exported for the chassis cookie route
// wrapper (package main cannot call the unexported validateCookies).
func (a *Adapter) ValidateCookies(raw []byte) error {
	return validateCookies(raw)
}

// SaveCookies validates then atomically writes raw cookie bytes to the
// adapter's cookies file, returning the resulting stat. Exported wrapper
// over saveCookies for the chassis cookie route.
func (a *Adapter) SaveCookies(raw []byte) (CookiesStat, error) {
	if err := validateCookies(raw); err != nil {
		return CookiesStat{}, err
	}
	return saveCookies(a.cookiesPath, raw)
}

// ClearCookies removes the cookies file (idempotent). Exported wrapper
// over clearCookies for the chassis cookie route.
func (a *Adapter) ClearCookies() error {
	return clearCookies(a.cookiesPath)
}

// CookieStat reports the cookies file size + mtime, or ok=false if the
// file is absent. Exported wrapper over statCookies for paint-time status.
func (a *Adapter) CookieStat() (CookiesStat, bool, error) {
	return statCookies(a.cookiesPath)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapters/url -run 'TestAdapter_CookieMethods_RoundTrip|TestAdapter_ValidateCookies_RejectsGarbage' -v`
Expected: PASS.

- [ ] **Step 5: Run the whole url package + commit**

Run: `go test ./internal/adapters/url`
Expected: PASS.

```bash
git add internal/adapters/url/cookies.go internal/adapters/url/cookies_test.go
git commit -m "feat(url): export cookie save/clear/stat/validate methods for chassis route"
```

---

## Task 5: Add `uiserver.AdapterSaver.SaveValues` (happy path)

**Files:**
- Modify: `internal/uiserver/adapter_saver.go`
- Test: `internal/uiserver/adapter_saver_test.go`

`SaveValues` mirrors `SaveTouched` (`adapter_saver.go:384-438`) but replaces the scalar `overlayTouched` with a clone-and-set of already-typed values, gated by an `allowedKeys` allowlist (so it can persist `ytdlp_hosts`, which is not a `FieldDef`). It reuses `readAdapterSectionMap`, `cloneMap`, `encodeAdapterMap`, `decodeAdapterSection`, `replaceAdapterSection`, `currentValuesOf`, `adapterFieldErrors`, and `ApplyError` verbatim.

- [ ] **Step 1: Write the failing test**

Append to `internal/uiserver/adapter_saver_test.go` (reuses the existing `fakeFullAdapter` and `newTempConfigWithSection` helpers):

```go
func TestSaveValues_PersistsArrayField(t *testing.T) {
	t.Parallel()
	path := newTempConfigWithSection(t, "url", `enabled = true
ytdlp_format = "best"
ytdlp_hosts = ["youtube.com"]
`)
	mu := &sync.Mutex{}
	saver := NewAdapterSaver(path, mu)
	adapter := &fakeFullAdapter{
		values: map[string]any{"enabled": true, "ytdlp_format": "best"},
		scope:  adapters.ScopeHotSwap,
	}
	scope, err := saver.SaveValues(
		"url",
		map[string]any{"ytdlp_hosts": []string{"youtube.com", "twitch.tv"}},
		[]string{"ytdlp_hosts"},
		adapter,
	)
	if err != nil {
		t.Fatalf("SaveValues: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Errorf("scope = %v, want ScopeHotSwap", scope)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"youtube.com"`) || !strings.Contains(string(got), `"twitch.tv"`) {
		t.Errorf("disk missing new hosts array:\n%s", got)
	}
	// Other keys preserved.
	if !strings.Contains(string(got), `enabled = true`) || !strings.Contains(string(got), `ytdlp_format = "best"`) {
		t.Errorf("disk dropped sibling keys:\n%s", got)
	}
	if n := len(adapter.applied); n != 1 {
		t.Fatalf("ApplyConfig calls = %d, want 1", n)
	}
}

func TestSaveValues_RejectsDisallowedKey(t *testing.T) {
	t.Parallel()
	path := newTempConfigWithSection(t, "url", "enabled = true\n")
	saver := NewAdapterSaver(path, &sync.Mutex{})
	adapter := &fakeFullAdapter{values: map[string]any{"enabled": true}}
	_, err := saver.SaveValues("url", map[string]any{"enabled": false}, []string{"ytdlp_hosts"}, adapter)
	var ferrs *adapterFieldErrors
	if !errors.As(err, &ferrs) {
		t.Fatalf("err = %v (%T), want *adapterFieldErrors", err, err)
	}
	if len(ferrs.Errs) != 1 || ferrs.Errs[0].Key != "enabled" {
		t.Errorf("ferrs = %+v, want one entry for disallowed 'enabled'", ferrs.Errs)
	}
	if len(adapter.applied) != 0 {
		t.Errorf("ApplyConfig must not run when a key is rejected")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/uiserver -run TestSaveValues -v`
Expected: FAIL (`saver.SaveValues undefined`).

- [ ] **Step 3: Add `SaveValues`**

Append to `internal/uiserver/adapter_saver.go`:

```go
// SaveValues writes explicitly-allowed typed values (including non-scalar
// values such as []string arrays) into the [adapters.<name>] section,
// reusing SaveTouched's read → merge → encode → validate → write-atomic →
// ApplyConfig pipeline under the shared saver mutex. Unlike SaveTouched it
// does not run the scalar overlayTouched step; callers pass already-typed
// Go values that the TOML encoder handles directly (encodeAdapterMap
// already serializes []string and nested tables). Any key in values that
// is not present in allowedKeys is rejected before disk is touched — this
// is the writable-surface allowlist for keys that have no FieldDef (e.g.
// ytdlp_hosts). Callers that need normalized values (e.g. lowercased
// hosts) must normalize BEFORE calling: the adapters.Validator re-check
// here returns only an error, never normalized values.
func (r *AdapterSaver) SaveValues(name string, values map[string]any, allowedKeys []string, adapter adapters.Adapter) (adapters.ApplyScope, error) {
	allow := make(map[string]bool, len(allowedKeys))
	for _, k := range allowedKeys {
		allow[k] = true
	}
	for k := range values {
		if !allow[k] {
			return 0, &adapterFieldErrors{Errs: []adapters.FieldError{{Key: k, Msg: "field not writable"}}}
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	doc, err := os.ReadFile(r.path)
	if err != nil {
		return 0, fmt.Errorf("read config: %w", err)
	}
	current, found, err := readAdapterSectionMap(doc, name)
	if err != nil {
		return 0, err
	}
	if !found {
		var ok bool
		current, ok = currentValuesOf(adapter)
		if !ok {
			current = map[string]any{}
		}
	}

	merged := cloneMap(current)
	for k, v := range values {
		merged[k] = v
	}

	snippet, err := encodeAdapterMap(name, merged)
	if err != nil {
		return 0, fmt.Errorf("encode merged config: %w", err)
	}
	prim, meta, err := decodeAdapterSection(snippet, name)
	if err != nil {
		return 0, fmt.Errorf("decode re-encoded section: %w", err)
	}
	if validator, ok := adapter.(adapters.Validator); ok {
		if err := validator.Validate(prim, meta); err != nil {
			if ferr, ok := err.(adapters.FieldErrors); ok {
				return 0, &adapterFieldErrors{Errs: []adapters.FieldError(ferr)}
			}
			return 0, fmt.Errorf("validate: %w", err)
		}
	}

	updated := replaceAdapterSection(doc, name, snippet)
	if err := config.WriteAtomic(r.path, updated); err != nil {
		return 0, fmt.Errorf("write config: %w", err)
	}
	scope, err := adapter.ApplyConfig(prim, meta)
	if err != nil {
		return scope, &ApplyError{Scope: scope, Err: err}
	}
	return scope, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/uiserver -run TestSaveValues -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/uiserver/adapter_saver.go internal/uiserver/adapter_saver_test.go
git commit -m "feat(uiserver): add AdapterSaver.SaveValues for typed/array field persistence"
```

---

## Task 6: `SaveValues` rejects invalid values before writing disk

**Files:**
- Modify: `internal/uiserver/adapter_saver_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/uiserver/adapter_saver_test.go`:

```go
func TestSaveValues_ValidateFailureLeavesDiskUntouched(t *testing.T) {
	t.Parallel()
	original := `enabled = true
ytdlp_hosts = ["youtube.com"]
`
	path := newTempConfigWithSection(t, "url", original)
	saver := NewAdapterSaver(path, &sync.Mutex{})
	adapter := &fakeFullAdapter{
		values:   map[string]any{"enabled": true},
		validErr: adapters.FieldErrors{{Key: "ytdlp_hosts", Msg: "entry contains URL syntax characters"}},
	}
	_, err := saver.SaveValues(
		"url",
		map[string]any{"ytdlp_hosts": []string{"https://bad/"}},
		[]string{"ytdlp_hosts"},
		adapter,
	)
	var ferrs *adapterFieldErrors
	if !errors.As(err, &ferrs) {
		t.Fatalf("err = %v (%T), want *adapterFieldErrors", err, err)
	}
	if len(ferrs.Errs) != 1 || ferrs.Errs[0].Key != "ytdlp_hosts" {
		t.Errorf("ferrs = %+v, want ytdlp_hosts error", ferrs.Errs)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "bad") {
		t.Errorf("disk was mutated despite Validate failure:\n%s", got)
	}
	if len(adapter.applied) != 0 {
		t.Errorf("ApplyConfig ran despite Validate failure")
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/uiserver -run TestSaveValues_ValidateFailureLeavesDiskUntouched -v`
Expected: PASS (Task 5's implementation already validates before write — this pins the behavior).

- [ ] **Step 3: (no impl change)** — if it FAILS, fix `SaveValues` so `validator.Validate` runs strictly before `replaceAdapterSection`/`config.WriteAtomic`; never weaken the test.

- [ ] **Step 4: Commit**

```bash
git add internal/uiserver/adapter_saver_test.go
git commit -m "test(uiserver): SaveValues rejects invalid values before disk write"
```

---

## Task 7: `SaveValues` preserves nested subtables + serializes under the shared mutex

**Files:**
- Modify: `internal/uiserver/adapter_saver_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/uiserver/adapter_saver_test.go`:

```go
func TestSaveValues_PreservesNestedSubtables(t *testing.T) {
	t.Parallel()
	body := `enabled = true
ytdlp_hosts = ["youtube.com"]

[adapters.url.nested]
keep = "me"
`
	path := newTempConfigWithSection(t, "url", body)
	saver := NewAdapterSaver(path, &sync.Mutex{})
	adapter := &fakeFullAdapter{
		values: map[string]any{
			"enabled":     true,
			"ytdlp_hosts": []any{"youtube.com"},
			"nested":      map[string]any{"keep": "me"},
		},
		scope: adapters.ScopeHotSwap,
	}
	if _, err := saver.SaveValues("url", map[string]any{"ytdlp_hosts": []string{"vimeo.com"}}, []string{"ytdlp_hosts"}, adapter); err != nil {
		t.Fatalf("SaveValues: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"vimeo.com"`) {
		t.Errorf("new host not written:\n%s", got)
	}
	if !strings.Contains(string(got), `[adapters.url.nested]`) || !strings.Contains(string(got), `keep = "me"`) {
		t.Errorf("nested subtable lost:\n%s", got)
	}
}

func TestSaveValues_ConcurrentSavesSerialize(t *testing.T) {
	t.Parallel()
	path := newTempConfigWithSection(t, "url", "enabled = true\nytdlp_hosts = [\"a.com\"]\n")
	saver := NewAdapterSaver(path, &sync.Mutex{})
	adapter := &fakeFullAdapter{values: map[string]any{"enabled": true}, scope: adapters.ScopeHotSwap}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			host := fmt.Sprintf("h%d.com", n)
			if _, err := saver.SaveValues("url", map[string]any{"ytdlp_hosts": []string{host}}, []string{"ytdlp_hosts"}, adapter); err != nil {
				t.Errorf("SaveValues(n=%d): %v", n, err)
			}
		}(i)
	}
	wg.Wait()
	// File must remain valid TOML parseable as a section after the storm.
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "[adapters.url]") {
		t.Errorf("section header lost after concurrent saves:\n%s", got)
	}
	var decoded map[string]any
	if _, err := toml.Decode(string(got), &decoded); err != nil {
		t.Fatalf("config is not valid TOML after concurrent saves: %v\n%s", err, got)
	}
}
```

- [ ] **Step 2: Run with the race detector**

Run: `go test -race ./internal/uiserver -run TestSaveValues -v`
Expected: PASS, no race warnings. (CI runs `-race`; locally `-race` needs cgo/gcc — if unavailable, run without `-race` and rely on CI for the race gate.)

- [ ] **Step 3: (no impl change unless a failure appears)** — if subtables are lost, audit that `SaveValues` reuses `readAdapterSectionMap`/`replaceAdapterSection` (which preserve descendants). Fix the implementation, never the test.

- [ ] **Step 4: Commit**

```bash
git add internal/uiserver/adapter_saver_test.go
git commit -m "test(uiserver): SaveValues preserves subtables and serializes concurrent saves"
```

---

## Task 8: Add `AdapterHostEditor` interface + `Config` field + nil guard

**Files:**
- Modify: `internal/chassis/settings.go`, `internal/chassis/server.go`
- Test: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/settings_test.go`:

```go
func TestHandleSettingsAdapterHostsPost_NotReadyWhenNil(t *testing.T) {
	t.Parallel()
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0)}}
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/adapter/url/hosts",
		strings.NewReader(`{"hosts":["youtube.com"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Code = %d, want 503", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if chip, _ := body["chip"].(string); chip != "NOT READY" {
		t.Errorf("chip = %q, want NOT READY", chip)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis -run TestHandleSettingsAdapterHostsPost_NotReadyWhenNil -v`
Expected: FAIL (route not registered → 404).

- [ ] **Step 3: Add the interface + Config field + handler skeleton + route**

In `internal/chassis/settings.go`, add near `AdapterSettingsSaver`:

```go
// AdapterHostEditor edits an adapter's host allowlist (the URL adapter's
// ytdlp_hosts). Chassis-owned; satisfied by a cmd wrapper. Kept separate
// from AdapterSettingsSaver because the host list is a []string with no
// FieldDef and is persisted via uiserver.AdapterSaver.SaveValues.
type AdapterHostEditor interface {
	// Hosts returns the adapter's current host list for paint, ok=false
	// for unknown / non-host-editing adapters.
	Hosts(name string) (hosts []string, ok bool)
	// SetHosts validates+normalizes the whole list, persists it atomically,
	// and returns the wire scope ("hot") plus the normalized list.
	SetHosts(name string, hosts []string) (scope string, normalized []string, err error)
}
```

Add the handler + success writer to `internal/chassis/settings.go`:

```go
// handleSettingsAdapterHostsPost handles POST /receiver/settings/adapter/{name}/hosts.
// Body: {"hosts":[...]}. Mirrors handleSettingsAdapterPost's error envelope.
func (s *Server) handleSettingsAdapterHostsPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdapterHostEditor == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	var payload struct {
		Hosts []string `json:"hosts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	scope, normalized, err := s.cfg.AdapterHostEditor.SetHosts(name, payload.Hosts)
	if err != nil {
		emitSaveError(w, err)
		return
	}
	writeSettingsHosts(w, scope, normalized)
}

func writeSettingsHosts(w http.ResponseWriter, scope string, hosts []string) {
	if hosts == nil {
		hosts = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "scope": scope, "hosts": hosts})
}
```

In `internal/chassis/server.go`, add to the `Config` struct (near `AdapterSettingsSaver`):

```go
	// 4F: URL adapter custom widgets.
	AdapterHostEditor  AdapterHostEditor
	AdapterCookieStore AdapterCookieStore
```

In `Mount` (after the `POST /receiver/settings/adapter/{name}` registration), add:

```go
	mux.Handle("POST /receiver/settings/adapter/{name}/hosts",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterHostsPost)))
```

Define `AdapterCookieStore` + `CookieStatusView` here too (both 4F `Config` fields reference them, so they must exist for this task to compile). Task 10 then only adds the cookie *handlers*, not these types. Add to `internal/chassis/settings.go`:

```go
// AdapterCookieStore manages an adapter's file-backed cookie store (the
// URL adapter's url_cookies.txt). Chassis-owned; satisfied by a cmd
// wrapper. Cookies are a file, never TOML, so they bypass the saver.
type AdapterCookieStore interface {
	CookieStatus(name string) (CookieStatusView, bool)
	SaveCookies(name, raw string) (CookieStatusView, error)
	ClearCookies(name string) (CookieStatusView, error)
}

// CookieStatusView is the paint-time + response view of the cookies file.
type CookieStatusView struct {
	Loaded bool   // false → "not loaded"
	Bytes  int64  // file size when loaded
	SetAt  string // "2006-01-02 15:04:05Z" (UTC); "" when absent
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/chassis -run TestHandleSettingsAdapterHostsPost_NotReadyWhenNil -v`
Expected: PASS.

- [ ] **Step 5: Confirm isolation invariant still holds**

Run: `go test ./internal/chassis -run TestProductionImports_NoCrossPackageCoupling`
Expected: PASS (no new forbidden imports).

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/server.go internal/chassis/settings_test.go
git commit -m "feat(chassis): AdapterHostEditor + AdapterCookieStore interfaces, hosts route nil-guard"
```

---

## Task 9: `handleSettingsAdapterHostsPost` success + error branches

**Files:**
- Modify: `internal/chassis/settings_test.go`

This task adds a test fake for `AdapterHostEditor` and pins the success, field-error, unknown-adapter, and cross-origin paths. The handler already routes through `emitSaveError`, so no handler change is expected.

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/settings_test.go`:

```go
type fakeHostEditor struct {
	hosts      []string
	hostsOK    bool
	scope      string
	normalized []string
	err        error
	gotName    string
	gotHosts   []string
}

func (f *fakeHostEditor) Hosts(name string) ([]string, bool) { return f.hosts, f.hostsOK }
func (f *fakeHostEditor) SetHosts(name string, hosts []string) (string, []string, error) {
	f.gotName = name
	f.gotHosts = hosts
	if f.err != nil {
		return "", nil, f.err
	}
	return f.scope, f.normalized, nil
}

func postHosts(t *testing.T, s *Server, name, jsonBody string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/adapter/"+name+"/hosts",
		strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandleSettingsAdapterHostsPost_Success(t *testing.T) {
	t.Parallel()
	fe := &fakeHostEditor{scope: "hot", normalized: []string{"youtube.com", "twitch.tv"}}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterHostEditor: fe}}
	rec := postHosts(t, s, "url", `{"hosts":["YouTube.com","Twitch.tv"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ok"] != true || body["scope"] != "hot" {
		t.Errorf("body = %v, want ok=true scope=hot", body)
	}
	hosts, _ := body["hosts"].([]any)
	if len(hosts) != 2 || hosts[0] != "youtube.com" {
		t.Errorf("hosts = %v, want normalized [youtube.com twitch.tv]", body["hosts"])
	}
	if fe.gotName != "url" || len(fe.gotHosts) != 2 {
		t.Errorf("SetHosts got (%q, %v)", fe.gotName, fe.gotHosts)
	}
}

func TestHandleSettingsAdapterHostsPost_FieldError(t *testing.T) {
	t.Parallel()
	fe := &fakeHostEditor{err: &fakeFieldErr{key: "hosts", msg: "entry contains URL syntax characters"}}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterHostEditor: fe}}
	rec := postHosts(t, s, "url", `{"hosts":["https://x/"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Code = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errs, _ := body["errors"].(map[string]any)
	if msg, _ := errs["hosts"].(string); !strings.Contains(msg, "URL syntax") {
		t.Errorf("errors[hosts] = %v, want URL-syntax message", errs["hosts"])
	}
}

func TestHandleSettingsAdapterHostsPost_UnknownAdapter(t *testing.T) {
	t.Parallel()
	fe := &fakeHostEditor{err: &fakeChipErr{status: http.StatusNotFound, chip: "UNKNOWN ADAPTER"}}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterHostEditor: fe}}
	rec := postHosts(t, s, "nope", `{"hosts":[]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Code = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleSettingsAdapterHostsPost_CrossOriginBlocked(t *testing.T) {
	t.Parallel()
	fe := &fakeHostEditor{scope: "hot"}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterHostEditor: fe}}
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/adapter/url/hosts",
		strings.NewReader(`{"hosts":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403", rec.Code)
	}
}
```

Add these small typed-error fakes to `settings_test.go` if they do not already exist (check first with `grep -n "fakeChipErr\|fakeFieldErr" internal/chassis/settings_test.go`; only add the ones missing):

```go
type fakeChipErr struct {
	status int
	chip   string
}

func (e *fakeChipErr) Error() string   { return e.chip }
func (e *fakeChipErr) StatusCode() int { return e.status }
func (e *fakeChipErr) Chip() string    { return e.chip }

type fakeFieldErr struct {
	key string
	msg string
}

func (e *fakeFieldErr) Error() string { return e.msg }
func (e *fakeFieldErr) FieldErrors() []adapters.FieldError {
	return []adapters.FieldError{{Key: e.key, Msg: e.msg}}
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/chassis -run TestHandleSettingsAdapterHostsPost -v`
Expected: PASS for all four (handler already wired in Task 8; these pin behavior). If the field-error or unknown-adapter test fails, confirm `emitSaveError` recognizes `*fakeFieldErr`/`*fakeChipErr` via the `fieldErrorBearerForChassis` / `settingsChipError` interfaces.

- [ ] **Step 3: Commit**

```bash
git add internal/chassis/settings_test.go
git commit -m "test(chassis): hosts route success/field-error/404/cross-origin"
```

---

## Task 10: Cookies route handlers (`save` + `clear`)

**Files:**
- Modify: `internal/chassis/settings.go`, `internal/chassis/server.go`
- Test: `internal/chassis/settings_test.go`

(`AdapterCookieStore` + `CookieStatusView` were defined in Task 8.)

- [ ] **Step 1: Write the failing tests**

Append to `internal/chassis/settings_test.go`:

```go
type fakeCookieStore struct {
	status   CookieStatusView
	statusOK bool
	saveView CookieStatusView
	saveErr  error
	clearErr error
	gotRaw   string
}

func (f *fakeCookieStore) CookieStatus(name string) (CookieStatusView, bool) {
	return f.status, f.statusOK
}
func (f *fakeCookieStore) SaveCookies(name, raw string) (CookieStatusView, error) {
	f.gotRaw = raw
	if f.saveErr != nil {
		return CookieStatusView{}, f.saveErr
	}
	return f.saveView, nil
}
func (f *fakeCookieStore) ClearCookies(name string) (CookieStatusView, error) {
	if f.clearErr != nil {
		return CookieStatusView{}, f.clearErr
	}
	return CookieStatusView{Loaded: false}, nil
}

func postCookies(t *testing.T, s *Server, path, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandleSettingsAdapterCookiesPost_SuccessForm(t *testing.T) {
	t.Parallel()
	cs := &fakeCookieStore{saveView: CookieStatusView{Loaded: true, Bytes: 128, SetAt: "2026-05-29 00:00:00Z"}}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterCookieStore: cs}}
	rec := postCookies(t, s, "/receiver/settings/adapter/url/cookies",
		"application/x-www-form-urlencoded", "cookies="+url.QueryEscape("data"))
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cs.gotRaw != "data" {
		t.Errorf("SaveCookies raw = %q, want data", cs.gotRaw)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	cookie, _ := body["cookie"].(map[string]any)
	if cookie["loaded"] != true || cookie["set_at"] != "2026-05-29 00:00:00Z" {
		t.Errorf("cookie view = %v", cookie)
	}
}

func TestHandleSettingsAdapterCookiesPost_SuccessJSON(t *testing.T) {
	t.Parallel()
	cs := &fakeCookieStore{saveView: CookieStatusView{Loaded: true, Bytes: 5}}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterCookieStore: cs}}
	rec := postCookies(t, s, "/receiver/settings/adapter/url/cookies",
		"application/json", `{"cookies":"abcde"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cs.gotRaw != "abcde" {
		t.Errorf("SaveCookies raw = %q, want abcde", cs.gotRaw)
	}
}

func TestHandleSettingsAdapterCookiesPost_FieldError(t *testing.T) {
	t.Parallel()
	cs := &fakeCookieStore{saveErr: &fakeFieldErr{key: "cookies", msg: "no Netscape-format lines"}}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterCookieStore: cs}}
	rec := postCookies(t, s, "/receiver/settings/adapter/url/cookies",
		"application/x-www-form-urlencoded", "cookies=junk")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Code = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errs, _ := body["errors"].(map[string]any)
	if _, ok := errs["cookies"]; !ok {
		t.Errorf("errors missing 'cookies': %v", body)
	}
}

func TestHandleSettingsAdapterCookiesClear_Success(t *testing.T) {
	t.Parallel()
	cs := &fakeCookieStore{}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterCookieStore: cs}}
	rec := postCookies(t, s, "/receiver/settings/adapter/url/cookies/clear",
		"application/x-www-form-urlencoded", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	cookie, _ := body["cookie"].(map[string]any)
	if cookie["loaded"] != false {
		t.Errorf("after clear loaded = %v, want false", cookie["loaded"])
	}
}

func TestHandleSettingsAdapterCookiesPost_NotReadyWhenNil(t *testing.T) {
	t.Parallel()
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0)}}
	rec := postCookies(t, s, "/receiver/settings/adapter/url/cookies",
		"application/x-www-form-urlencoded", "cookies=x")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Code = %d, want 503", rec.Code)
	}
}

func TestHandleSettingsAdapterCookiesPost_CrossOriginBlocked(t *testing.T) {
	t.Parallel()
	cs := &fakeCookieStore{saveView: CookieStatusView{Loaded: true}}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterCookieStore: cs}}
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/receiver/settings/adapter/url/cookies",
		strings.NewReader("cookies=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403", rec.Code)
	}
	if cs.gotRaw != "" {
		t.Errorf("cross-origin request reached SaveCookies with %q", cs.gotRaw)
	}
}

func TestHandleSettingsAdapterCookiesPost_UnknownAdapter(t *testing.T) {
	t.Parallel()
	cs := &fakeCookieStore{saveErr: &fakeChipErr{status: http.StatusNotFound, chip: "UNKNOWN ADAPTER"}}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterCookieStore: cs}}
	rec := postCookies(t, s, "/receiver/settings/adapter/streams/cookies",
		"application/x-www-form-urlencoded", "cookies=x")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Code = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "streams") {
		t.Errorf("unknown-adapter response leaked adapter details: %s", rec.Body.String())
	}
}

func TestHandleSettingsAdapterCookiesPost_OversizeIsFieldError(t *testing.T) {
	t.Parallel()
	cs := &fakeCookieStore{saveView: CookieStatusView{Loaded: true}}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterCookieStore: cs}}
	rec := postCookies(t, s, "/receiver/settings/adapter/url/cookies",
		"application/x-www-form-urlencoded", "cookies="+strings.Repeat("x", (1<<20)+2))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Code = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errs, _ := body["errors"].(map[string]any)
	if _, ok := errs["cookies"]; !ok {
		t.Errorf("oversize response missing errors.cookies: %v", body)
	}
	if cs.gotRaw != "" {
		t.Errorf("oversize request reached SaveCookies with %d bytes", len(cs.gotRaw))
	}
}

func TestHandleSettingsAdapterCookiesPost_WriteFailureIsGenericChip(t *testing.T) {
	t.Parallel()
	cs := &fakeCookieStore{saveErr: &fakeChipErr{status: http.StatusInternalServerError, chip: "WRITE FAILED"}}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterCookieStore: cs}}
	rec := postCookies(t, s, "/receiver/settings/adapter/url/cookies",
		"application/x-www-form-urlencoded", "cookies=x")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("Code = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "WRITE FAILED") {
		t.Errorf("write failure response missing generic chip: %s", rec.Body.String())
	}
}

func TestHandleSettingsAdapterCookiesClear_WriteFailureIsGenericChip(t *testing.T) {
	t.Parallel()
	cs := &fakeCookieStore{clearErr: &fakeChipErr{status: http.StatusInternalServerError, chip: "WRITE FAILED"}}
	s := &Server{cfg: Config{Version: "test", StartedAt: time.Unix(0, 0), AdapterCookieStore: cs}}
	rec := postCookies(t, s, "/receiver/settings/adapter/url/cookies/clear",
		"application/x-www-form-urlencoded", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("Code = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "WRITE FAILED") {
		t.Errorf("clear failure response missing generic chip: %s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis -run TestHandleSettingsAdapterCookies -v`
Expected: FAIL (routes not registered → 404).

- [ ] **Step 3: Add the handlers + reader + route registrations**

Append to `internal/chassis/settings.go`:

```go
const maxCookiesBody = 1 << 20 // 1 MiB; mirrors the URL adapter's legacy cap.

type cookieFieldError string

func (e cookieFieldError) Error() string { return string(e) }
func (e cookieFieldError) FieldErrors() []adapters.FieldError {
	return []adapters.FieldError{{Key: "cookies", Msg: string(e)}}
}

func cookiesReadError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return cookieFieldError("cookies file must be 1 MiB or smaller")
	}
	return cookieFieldError("invalid cookies payload")
}

// readCookiesField extracts the cookies payload from a form-encoded or
// JSON body under a 1 MiB cap. Chassis-local (it must not import the URL
// adapter); mirrors the legacy adapter parser shape.
func readCookiesField(r *http.Request) (string, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxCookiesBody+1)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var payload struct {
			Cookies string `json:"cookies"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return "", cookiesReadError(err)
		}
		return payload.Cookies, nil
	}
	if err := r.ParseForm(); err != nil {
		return "", cookiesReadError(err)
	}
	return r.PostForm.Get("cookies"), nil
}

// handleSettingsAdapterCookiesPost handles POST /receiver/settings/adapter/{name}/cookies.
func (s *Server) handleSettingsAdapterCookiesPost(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdapterCookieStore == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	raw, err := readCookiesField(r)
	if err != nil {
		emitSaveError(w, err)
		return
	}
	view, err := s.cfg.AdapterCookieStore.SaveCookies(name, raw)
	if err != nil {
		emitSaveError(w, err)
		return
	}
	writeSettingsCookie(w, view)
}

// handleSettingsAdapterCookiesClear handles POST /receiver/settings/adapter/{name}/cookies/clear.
func (s *Server) handleSettingsAdapterCookiesClear(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdapterCookieStore == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	view, err := s.cfg.AdapterCookieStore.ClearCookies(name)
	if err != nil {
		emitSaveError(w, err)
		return
	}
	writeSettingsCookie(w, view)
}

func writeSettingsCookie(w http.ResponseWriter, v CookieStatusView) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
		"cookie": map[string]any{
			"loaded": v.Loaded,
			"bytes":  v.Bytes,
			"set_at": v.SetAt,
		},
	})
}
```

In `internal/chassis/server.go` `Mount`, after the hosts route, add:

```go
	mux.Handle("POST /receiver/settings/adapter/{name}/cookies",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterCookiesPost)))
	mux.Handle("POST /receiver/settings/adapter/{name}/cookies/clear",
		requireSameOrigin(http.HandlerFunc(s.handleSettingsAdapterCookiesClear)))
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/chassis -run TestHandleSettingsAdapterCookies -v`
Expected: PASS for all.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/settings.go internal/chassis/server.go internal/chassis/settings_test.go
git commit -m "feat(chassis): cookies save/clear routes over AdapterCookieStore"
```

---

## Task 11: Extend `AdapterPaneData` + populate the URL pane in `settingsDataFromConfig`

**Files:**
- Modify: `internal/chassis/data.go`
- Test: `internal/chassis/data_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/data_test.go`:

```go
func TestSettingsData_URLPanePopulatesWidgets(t *testing.T) {
	t.Parallel()
	saver := &fakeAdapterSettingsSaver{
		fields: map[string][]adapters.FieldDef{
			"url": {{Key: "enabled", Kind: adapters.KindBool}},
		},
		current: map[string]map[string]any{
			"url": {"enabled": true},
		},
	}
	he := &fakeHostEditor{hosts: []string{"youtube.com"}, hostsOK: true}
	cs := &fakeCookieStore{status: CookieStatusView{Loaded: true, Bytes: 64, SetAt: "2026-05-29 00:00:00Z"}, statusOK: true}
	cfg := Config{
		Version:              "test",
		StartedAt:            time.Unix(0, 0),
		AdapterSettingsSaver: saver,
		AdapterHostEditor:    he,
		AdapterCookieStore:   cs,
	}
	data := settingsDataFromConfig(cfg)
	var urlPane *AdapterPaneData
	for i := range data.Adapters {
		if data.Adapters[i].Name == "url" {
			urlPane = &data.Adapters[i]
		}
	}
	if urlPane == nil {
		t.Fatal("url pane not present in SettingsData.Adapters")
	}
	if !urlPane.HasHostEditor || len(urlPane.Hosts) != 1 || urlPane.Hosts[0] != "youtube.com" {
		t.Errorf("host editor data = %+v", urlPane)
	}
	if !urlPane.HasCookieStore || urlPane.Cookie == nil || !urlPane.Cookie.Loaded || urlPane.Cookie.Bytes != 64 {
		t.Errorf("cookie data = %+v", urlPane.Cookie)
	}
	if urlPane.Hint != "PASTE-IN" {
		t.Errorf("Hint = %q, want PASTE-IN", urlPane.Hint)
	}
}
```

> The `fakeAdapterSettingsSaver` already exists in the chassis test package (`settings_test.go:1577`). Confirm its `fields`/`current` field names with `grep -n "type fakeAdapterSettingsSaver" -A12 internal/chassis/settings_test.go` and adjust the literal above to match if they differ.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis -run TestSettingsData_URLPanePopulatesWidgets -v`
Expected: FAIL (`urlPane.HasHostEditor undefined`; and `url` not in the loop).

- [ ] **Step 3: Extend the struct**

In `internal/chassis/data.go`, replace the `AdapterPaneData` struct with:

```go
type AdapterPaneData struct {
	Name      string
	Hint      string
	Fields    []adapters.FieldDef
	Values    map[string]any
	Providers []AdapterProviderRow

	// 4F — URL bespoke widgets. Flags gate rendering so a valid empty
	// host list still shows the tag editor. Zero/nil for other adapters.
	HasHostEditor  bool
	Hosts          []string
	HasCookieStore bool
	Cookie         *CookieStatusView
}
```

- [ ] **Step 4: Add `"url"` to the adapter loop + populate widgets**

In `internal/chassis/data.go`, in `settingsDataFromConfig`, change the loop slice and add the URL branch. Replace the existing loop body so it reads:

```go
	if saver := cfg.AdapterSettingsSaver; saver != nil {
		for _, name := range []string{"dlna", "torrent", "streams", "url"} {
			fields, ok := saver.Fields(name)
			if !ok {
				continue
			}
			values, _ := saver.Current(name)
			pane := AdapterPaneData{
				Name:   name,
				Hint:   buildAdapterHint(cfg, name, values),
				Fields: fields,
				Values: values,
			}
			if name == "streams" {
				pane.Providers = buildStreamsProviderRows(cfg)
			}
			if name == "url" {
				pane.Hint = "PASTE-IN"
				if he := cfg.AdapterHostEditor; he != nil {
					if hosts, ok := he.Hosts("url"); ok {
						pane.HasHostEditor = true
						pane.Hosts = hosts
					}
				}
				if store := cfg.AdapterCookieStore; store != nil {
					if view, ok := store.CookieStatus("url"); ok {
						pane.HasCookieStore = true
						v := view
						pane.Cookie = &v
					}
				}
			}
			data.Adapters = append(data.Adapters, pane)
		}
	}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/chassis -run TestSettingsData_URLPanePopulatesWidgets -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/data.go internal/chassis/data_test.go
git commit -m "feat(chassis): populate URL adapter pane host + cookie widget data"
```

---

## Task 12: Add `fieldByKey` template helper

**Files:**
- Modify: `internal/chassis/templates.go`
- Test: `internal/chassis/chassis_test.go`

The URL template must interleave the host editor between `ytdlp_enabled` and `ytdlp_format`, so it renders standard fields by key rather than ranging. `fieldByKey` returns the `FieldDef` pointer for a key (`nil` if absent) so templates can skip missing fields instead of rendering empty `name=""` controls.

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/chassis_test.go`:

```go
func TestFieldByKey(t *testing.T) {
	t.Parallel()
	fields := []adapters.FieldDef{
		{Key: "enabled", Kind: adapters.KindBool},
		{Key: "ytdlp_format", Kind: adapters.KindText},
	}
	got := fieldByKey(fields, "ytdlp_format")
	if got == nil || got.Key != "ytdlp_format" || got.Kind != adapters.KindText {
		t.Errorf("fieldByKey = %+v, want ytdlp_format/Text", got)
	}
	missing := fieldByKey(fields, "nope")
	if missing != nil {
		t.Errorf("fieldByKey(missing) = %+v, want nil", missing)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/chassis -run TestFieldByKey -v`
Expected: FAIL (`undefined: fieldByKey`).

- [ ] **Step 3: Add the helper + register it in the funcmap**

In `internal/chassis/templates.go`, add the function:

```go
// fieldByKey returns the FieldDef with the given key, or nil if absent.
// Used by the URL pane template to render specific standard fields in
// mockup order with the host editor interleaved.
func fieldByKey(fields []adapters.FieldDef, key string) *adapters.FieldDef {
	for i := range fields {
		if fields[i].Key == key {
			return &fields[i]
		}
	}
	return nil
}
```

Register it in the template `FuncMap` (find the existing `template.FuncMap{...}` literal in `templates.go` that registers `dict`, `field`, `adapterPane`, etc. — add this entry alongside them):

```go
		"fieldByKey": fieldByKey,
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/chassis -run TestFieldByKey -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): add fieldByKey template helper for interleaved URL pane"
```

---

## Task 13: URL pane template + container swap

**Files:**
- Create: `internal/chassis/templates/settings-adapter-url.html`
- Modify: `internal/chassis/templates/settings-adapters.html`
- Test: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/chassis_test.go`. (Find the existing template-render harness with `grep -n "renderSettingsDrawer\|ExecuteTemplate\|renderTemplate" internal/chassis/chassis_test.go` and use the same render helper; the test below assumes a helper `renderSettingsAdapters(t, data)` that executes the `settings-adapters` template against a `SettingsData`. If the existing tests render via `srv.Mount` + GET `/receiver`, adapt accordingly — the assertions on output substrings are what matter.)

```go
func TestRenderURLPane_TagsAndCookiePill(t *testing.T) {
	t.Parallel()
	data := SettingsData{
		Adapters: []AdapterPaneData{{
			Name:           "url",
			Hint:           "PASTE-IN",
			Fields:         []adapters.FieldDef{{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap}, {Key: "ytdlp_enabled", Kind: adapters.KindBool, Label: "yt-dlp resolver", ApplyScope: adapters.ScopeHotSwap}, {Key: "ytdlp_format", Kind: adapters.KindText, Label: "yt-dlp format", ApplyScope: adapters.ScopeHotSwap}, {Key: "ytdlp_resolve_timeout_seconds", Kind: adapters.KindInt, Label: "Resolve timeout (s)", ApplyScope: adapters.ScopeHotSwap}},
			Values:         map[string]any{"enabled": true, "ytdlp_enabled": true, "ytdlp_format": "best", "ytdlp_resolve_timeout_seconds": 30},
			HasHostEditor:  true,
			Hosts:          []string{"youtube.com", "twitch.tv"},
			HasCookieStore: true,
			Cookie:         &CookieStatusView{Loaded: true, Bytes: 64, SetAt: "2026-05-29 00:00:00Z"},
		}},
	}
	out := renderSettingsAdapters(t, data)
	for _, want := range []string{
		`data-host-editor="url"`,
		`data-host="youtube.com"`,
		`data-remove-host="twitch.tv"`,
		`data-add-host`,
		`data-cookies="url"`,
		`data-cookies-save`,
		`data-cookies-clear`,
		`64 B · set 2026-05-29 00:00:00Z`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered URL pane missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderURLPane_EmptyHostsStillShowsEditor(t *testing.T) {
	t.Parallel()
	data := SettingsData{
		Adapters: []AdapterPaneData{{
			Name:          "url",
			Hint:          "PASTE-IN",
			Fields:        []adapters.FieldDef{{Key: "enabled", Kind: adapters.KindBool, Label: "Enabled", ApplyScope: adapters.ScopeHotSwap}},
			Values:        map[string]any{"enabled": false},
			HasHostEditor: true,
			Hosts:         nil,
			HasCookieStore: true,
			Cookie:        &CookieStatusView{Loaded: false},
		}},
	}
	out := renderSettingsAdapters(t, data)
	if !strings.Contains(out, `data-add-host`) {
		t.Errorf("empty host list dropped the tag editor:\n%s", out)
	}
	if !strings.Contains(out, "not loaded") {
		t.Errorf("cookie pill missing 'not loaded':\n%s", out)
	}
	if strings.Contains(out, `name=""`) {
		t.Errorf("missing URL fields rendered an empty-name input:\n%s", out)
	}
}
```

If a `renderSettingsAdapters` helper does not already exist, add it next to the other render helpers in `chassis_test.go`. Use the package's real test template loader, `parseTemplatesForTest(t)` (defined at `chassis_test.go:2563`, returns `*template.Template`; used the same way at `chassis_test.go:1035` for the `source-cluster` render test):

```go
func renderSettingsAdapters(t *testing.T, data SettingsData) string {
	t.Helper()
	tmpl := parseTemplatesForTest(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "settings-adapters", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}
```

> Ensure `bytes` is imported in `chassis_test.go` (other render tests already use it). Executing the `"settings-adapters"` template directly works because the new `settings-adapter-url.html` is in the embedded `templates/` glob and `parseTemplatesForTest` parses the whole set, so the `{{ template "settings-adapter-url" ... }}` call resolves.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/chassis -run TestRenderURLPane -v`
Expected: FAIL (template `settings-adapter-url` not defined; stub still rendered).

- [ ] **Step 3: Create `settings-adapter-url.html`**

Create `internal/chassis/templates/settings-adapter-url.html`:

```html
{{- define "settings-adapter-url" -}}
<section class="settings-section wide">
  <h4>URL <span class="hint">{{ .Hint }}</span></h4>
  {{ with fieldByKey .Fields "enabled" }}{{ template "adapter-field-row" (dict "Adapter" "url" "Field" . "Values" $.Values) }}{{ end }}
  {{ with fieldByKey .Fields "ytdlp_enabled" }}{{ template "adapter-field-row" (dict "Adapter" "url" "Field" . "Values" $.Values) }}{{ end }}
  {{ if .HasHostEditor }}{{ template "url-host-editor" . }}{{ end }}
  {{ with fieldByKey .Fields "ytdlp_format" }}{{ template "adapter-field-row" (dict "Adapter" "url" "Field" . "Values" $.Values) }}{{ end }}
  {{ with fieldByKey .Fields "ytdlp_resolve_timeout_seconds" }}{{ template "adapter-field-row" (dict "Adapter" "url" "Field" . "Values" $.Values) }}{{ end }}
  {{ if .HasCookieStore }}{{ template "url-cookies" . }}{{ end }}
</section>
{{- end -}}

{{- define "url-host-editor" -}}
<div class="field-row" style="grid-template-columns: 1fr 1fr auto;" data-host-editor="url">
  <label>yt-dlp hosts <span class="help">Hosts that auto-route through yt-dlp. Curated default — add others at your own risk (they break frequently).</span></label>
  <div class="tag-list">
    {{ range .Hosts }}
    <span class="tag" data-host="{{ . }}">{{ . }}<span class="x" data-remove-host="{{ . }}">✕</span></span>
    {{ end }}
    <span class="tag add" data-add-host tabindex="0">+ add host</span>
    <div class="widget-err" data-host-err hidden></div>
  </div>
  <span class="scope hot">HOT</span>
</div>
{{- end -}}

{{- define "url-cookies" -}}
<div class="field-row" style="grid-template-columns: 1fr 1fr auto;" data-cookies="url">
  <label>Cookies <span class="help">Paste Netscape cookies.txt for authenticated streams. Stored on disk; persists across restarts.</span></label>
  <div class="cookies-form">
    <textarea class="cookies-paste" data-cookies-text rows="5" spellcheck="false" autocomplete="off" placeholder="# Netscape HTTP Cookie File&#10;# (paste exported cookies.txt content here)"></textarea>
    <div class="cookies-actions">
      <button type="button" class="action-btn" data-cookies-save>Save cookies</button>
      <button type="button" class="action-btn ghost" data-cookies-clear>Clear</button>
      <span class="status-pill{{ if not .Cookie.Loaded }} dim{{ end }}" data-cookies-pill>{{ if .Cookie.Loaded }}{{ .Cookie.Bytes }} B · set {{ .Cookie.SetAt }}{{ else }}not loaded{{ end }}</span>
    </div>
    <div class="widget-err" data-cookies-err hidden></div>
  </div>
  <span class="scope hot">HOT</span>
</div>
{{- end -}}
```

- [ ] **Step 4: Swap the stub in `settings-adapters.html`**

In `internal/chassis/templates/settings-adapters.html`, replace the URL stub block:

```html
  <!-- URL (4F) -->
  <section class="settings-section">
    <h4>URL <span class="hint">— pending</span></h4>
    <div class="action-result shown">▸ Spec 4F — implementation in progress</div>
  </section>
```

with:

```html
  {{ template "settings-adapter-url" (adapterPane .Adapters "url") }}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/chassis -run TestRenderURLPane -v`
Expected: PASS both.

- [ ] **Step 6: Run the whole chassis package**

Run: `go test ./internal/chassis`
Expected: PASS (no other template test regressed by the stub removal).

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/templates/settings-adapter-url.html internal/chassis/templates/settings-adapters.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): URL adapter pane template (tag-list + cookies widgets)"
```

---

## Task 14: CSS for tag-list, cookies form, status pill

**Files:**
- Modify: `internal/chassis/static/chassis.css`

No automated test (pure styling); verified visually after the JS task. Match the existing `body.receiver` selector convention and oklch palette.

- [ ] **Step 1: Append the rules**

Append to `internal/chassis/static/chassis.css`:

```css
/* 4F — URL adapter custom widgets */
body.receiver .tag-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}
body.receiver .tag {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 8px;
  background: oklch(0.12 0.02 250);
  border: 1px solid oklch(0.18 0.02 250);
  border-radius: 4px;
  font-size: 12px;
}
body.receiver .tag .x {
  cursor: pointer;
  opacity: 0.6;
}
body.receiver .tag .x:hover { opacity: 1; }
body.receiver .tag.add {
  cursor: pointer;
  color: var(--vfd);
  border-style: dashed;
  background: transparent;
}
body.receiver .cookies-form {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
body.receiver .cookies-paste {
  width: 100%;
  resize: vertical;
  font-family: ui-monospace, monospace;
  font-size: 12px;
  background: oklch(0.08 0.02 250);
  border: 1px solid oklch(0.16 0.02 250);
  color: inherit;
  padding: 8px;
  border-radius: 4px;
}
body.receiver .cookies-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
body.receiver .status-pill {
  padding: 3px 8px;
  border-radius: 10px;
  font-size: 11px;
  background: oklch(0.20 0.06 175);
  color: var(--vfd);
}
body.receiver .status-pill.dim {
  background: oklch(0.12 0.02 250);
  color: oklch(0.55 0.02 250);
}
body.receiver .widget-err {
  color: oklch(0.72 0.18 25);
  font-size: 11px;
  margin-top: 4px;
}
```

- [ ] **Step 2: Verify the asset still builds + bundles**

Run: `go build ./...`
Expected: success (the `go:embed` of `static/*` picks up the edited file automatically).

- [ ] **Step 3: Commit**

```bash
git add internal/chassis/static/chassis.css
git commit -m "feat(chassis): CSS for URL host tag-list, cookies form, status pill"
```

---

## Task 15: JS handlers for the host editor and cookies widgets

**Files:**
- Modify: `internal/chassis/static/settings-drawer.js`

No Go test (browser behavior is verified manually in Task 18's e2e + a real browser). Reuse `showNotice` for toasts; write widget errors to the widget's own `[data-host-err]` / `[data-cookies-err]` element (the field-error helpers can't target these widgets).

- [ ] **Step 1: Append the host editor handlers**

Append to `internal/chassis/static/settings-drawer.js`:

```javascript
// 4F — URL host tag-list. Whole-list replace on every add/remove.
function urlHostEditor() {
  return document.querySelector('[data-host-editor="url"]');
}

function currentHostSet() {
  const ed = urlHostEditor();
  if (!ed) return [];
  return Array.from(ed.querySelectorAll('.tag[data-host]'))
    .map((t) => t.getAttribute('data-host'));
}

async function putHosts(hosts) {
  const ed = urlHostEditor();
  const errEl = ed ? ed.querySelector('[data-host-err]') : null;
  if (errEl) { errEl.hidden = true; errEl.textContent = ''; }
  try {
    const res = await fetch('/receiver/settings/adapter/url/hosts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ hosts }),
    });
    const payload = await res.json();
    if (!payload.ok) {
      if (payload.errors && payload.errors.hosts && errEl) {
        errEl.textContent = payload.errors.hosts;
        errEl.hidden = false;
      } else if (payload.chip) {
        showNotice(payload.chip, 'err');
      }
      return false;
    }
    renderHostTags(payload.hosts || []);
    return true;
  } catch (e) {
    showNotice('NETWORK ERROR', 'err');
    return false;
  }
}

function renderHostTags(hosts) {
  const ed = urlHostEditor();
  if (!ed) return;
  const list = ed.querySelector('.tag-list');
  const addBtn = list.querySelector('.tag.add');
  list.querySelectorAll('.tag[data-host]').forEach((t) => t.remove());
  for (const h of hosts) {
    const span = document.createElement('span');
    span.className = 'tag';
    span.setAttribute('data-host', h);
    span.textContent = h;
    const x = document.createElement('span');
    x.className = 'x';
    x.setAttribute('data-remove-host', h);
    x.textContent = '✕';
    span.appendChild(x);
    list.insertBefore(span, addBtn);
  }
}

document.addEventListener('click', (ev) => {
  const rm = ev.target.closest('[data-remove-host]');
  if (rm && urlHostEditor() && urlHostEditor().contains(rm)) {
    ev.preventDefault();
    const host = rm.getAttribute('data-remove-host');
    putHosts(currentHostSet().filter((h) => h !== host));
    return;
  }
  const add = ev.target.closest('[data-add-host]');
  if (add && urlHostEditor() && urlHostEditor().contains(add)) {
    ev.preventDefault();
    const host = (prompt('Add yt-dlp host (e.g. example.com):') || '').trim();
    if (!host) return;
    const set = currentHostSet();
    if (set.includes(host)) return;
    putHosts(set.concat([host]));
  }
});
```

- [ ] **Step 2: Append the cookies handlers**

Append to `internal/chassis/static/settings-drawer.js`:

```javascript
// 4F — URL cookies widget. Explicit Save/Clear; repaint pill from response.
function urlCookies() {
  return document.querySelector('[data-cookies="url"]');
}

function paintCookiePill(cookie) {
  const w = urlCookies();
  if (!w) return;
  const pill = w.querySelector('[data-cookies-pill]');
  if (!pill) return;
  if (cookie && cookie.loaded) {
    pill.classList.remove('dim');
    pill.textContent = `${cookie.bytes} B · set ${cookie.set_at}`;
  } else {
    pill.classList.add('dim');
    pill.textContent = 'not loaded';
  }
}

async function postCookies(path, body, contentType) {
  const w = urlCookies();
  const errEl = w ? w.querySelector('[data-cookies-err]') : null;
  if (errEl) { errEl.hidden = true; errEl.textContent = ''; }
  try {
    const res = await fetch(path, {
      method: 'POST',
      headers: contentType ? { 'Content-Type': contentType } : {},
      body,
    });
    const payload = await res.json();
    if (!payload.ok) {
      if (payload.errors && payload.errors.cookies && errEl) {
        errEl.textContent = payload.errors.cookies;
        errEl.hidden = false;
      } else if (payload.chip) {
        showNotice(payload.chip, 'err');
      }
      return;
    }
    paintCookiePill(payload.cookie);
  } catch (e) {
    showNotice('NETWORK ERROR', 'err');
  }
}

document.addEventListener('click', (ev) => {
  const save = ev.target.closest('[data-cookies-save]');
  if (save && urlCookies() && urlCookies().contains(save)) {
    ev.preventDefault();
    const text = urlCookies().querySelector('[data-cookies-text]');
    const body = new URLSearchParams();
    body.set('cookies', text ? text.value : '');
    postCookies('/receiver/settings/adapter/url/cookies', body.toString(),
      'application/x-www-form-urlencoded');
    return;
  }
  const clear = ev.target.closest('[data-cookies-clear]');
  if (clear && urlCookies() && urlCookies().contains(clear)) {
    ev.preventDefault();
    postCookies('/receiver/settings/adapter/url/cookies/clear', null, null);
    const text = urlCookies().querySelector('[data-cookies-text]');
    if (text) text.value = '';
  }
});
```

- [ ] **Step 3: Verify build + JS syntax**

Run: `go build ./...`
Expected: success.
Run (if `node` is available): `node --check internal/chassis/static/settings-drawer.js`
Expected: no syntax errors. (If `node` is unavailable, skip; the e2e + manual check covers behavior.)

- [ ] **Step 4: Commit**

```bash
git add internal/chassis/static/settings-drawer.js
git commit -m "feat(chassis): JS handlers for URL host tag-list and cookies widgets"
```

---

## Task 16: `bridgeAdapterHostEditor` cmd wrapper

**Files:**
- Create: `cmd/mister-groovy-relay/adapter_host_editor.go`
- Create: `cmd/mister-groovy-relay/adapter_host_editor_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/mister-groovy-relay/adapter_host_editor_test.go`:

```go
package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

// fakeURLAdapter implements adapters.Adapter + the host/validate surface
// the host editor wrapper duck-types.
type fakeURLAdapter struct {
	mu    sync.Mutex
	hosts []string
}

func (f *fakeURLAdapter) Name() string            { return "url" }
func (f *fakeURLAdapter) DisplayName() string     { return "URL" }
func (f *fakeURLAdapter) Status() adapters.Status { return adapters.Status{} }
func (f *fakeURLAdapter) IsEnabled() bool         { return true }
func (f *fakeURLAdapter) Fields() []adapters.FieldDef {
	return []adapters.FieldDef{{Key: "enabled", Kind: adapters.KindBool}}
}
func (f *fakeURLAdapter) CurrentValues() map[string]any { return map[string]any{"enabled": true} }
func (f *fakeURLAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error { return nil }
func (f *fakeURLAdapter) Validate(toml.Primitive, toml.MetaData) error     { return nil }
func (f *fakeURLAdapter) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}
func (f *fakeURLAdapter) Start(context.Context) error { return nil }
func (f *fakeURLAdapter) Stop() error                 { return nil }
func (f *fakeURLAdapter) CurrentHosts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.hosts))
	copy(out, f.hosts)
	return out
}

func TestBridgeAdapterHostEditor_SetHostsNormalizesAndPersists(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	if err := os.WriteFile(cfgPath, []byte(`[bridge]
mister.host = "x"

[adapters.url]
enabled = true
ytdlp_hosts = ["youtube.com"]
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	saver := uiserver.NewAdapterSaver(cfgPath, &sync.Mutex{})
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"url": &fakeURLAdapter{hosts: []string{"youtube.com"}}}}
	ed := newBridgeAdapterHostEditor(saver, reg)

	scope, normalized, err := ed.SetHosts("url", []string{"  YouTube.com ", "Twitch.TV"})
	if err != nil {
		t.Fatalf("SetHosts: %v", err)
	}
	if scope != "hot" {
		t.Errorf("scope = %q, want hot", scope)
	}
	if len(normalized) != 2 || normalized[0] != "youtube.com" || normalized[1] != "twitch.tv" {
		t.Errorf("normalized = %v, want [youtube.com twitch.tv]", normalized)
	}
	got, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(got), `"twitch.tv"`) {
		t.Errorf("config.toml missing normalized host:\n%s", got)
	}
}

func TestBridgeAdapterHostEditor_SetHostsRejectsBadHost(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	_ = os.WriteFile(cfgPath, []byte("[bridge]\nmister.host = \"x\"\n\n[adapters.url]\nenabled = true\n"), 0o600)
	saver := uiserver.NewAdapterSaver(cfgPath, &sync.Mutex{})
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"url": &fakeURLAdapter{}}}
	ed := newBridgeAdapterHostEditor(saver, reg)

	_, _, err := ed.SetHosts("url", []string{"https://bad/"})
	if err == nil {
		t.Fatal("SetHosts accepted bad host; want field error")
	}
	feb, ok := err.(interface{ FieldErrors() []adapters.FieldError })
	if !ok {
		t.Fatalf("err type = %T, want FieldErrors bearer", err)
	}
	if feb.FieldErrors()[0].Key != "hosts" {
		t.Errorf("field error key = %q, want hosts", feb.FieldErrors()[0].Key)
	}
}

func TestBridgeAdapterHostEditor_UnknownAdapter(t *testing.T) {
	saver := uiserver.NewAdapterSaver(t.TempDir()+"/config.toml", &sync.Mutex{})
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{}}
	ed := newBridgeAdapterHostEditor(saver, reg)
	_, _, err := ed.SetHosts("url", nil)
	ce, ok := err.(interface{ StatusCode() int })
	if !ok || ce.StatusCode() != 404 {
		t.Fatalf("err = %v, want 404 chip error", err)
	}
}

func TestBridgeAdapterHostEditor_SetHostsRejectsNonHostAdapter(t *testing.T) {
	saver := uiserver.NewAdapterSaver(t.TempDir()+"/config.toml", &sync.Mutex{})
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{
		"streams": &fakeStreamsAdapter{current: map[string]any{"enabled": true}},
	}}
	ed := newBridgeAdapterHostEditor(saver, reg)
	_, _, err := ed.SetHosts("streams", []string{"youtube.com"})
	ce, ok := err.(interface {
		StatusCode() int
		Chip() string
	})
	if !ok || ce.StatusCode() != http.StatusNotFound || ce.Chip() != "UNKNOWN ADAPTER" {
		t.Fatalf("err = %v, want 404 UNKNOWN ADAPTER chip", err)
	}
}

func TestBridgeAdapterHostEditor_EmptyEntryReKeyedToHosts(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	_ = os.WriteFile(cfgPath, []byte("[bridge]\nmister.host = \"x\"\n\n[adapters.url]\nenabled = true\n"), 0o600)
	saver := uiserver.NewAdapterSaver(cfgPath, &sync.Mutex{})
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"url": &fakeURLAdapter{}}}
	ed := newBridgeAdapterHostEditor(saver, reg)
	// An empty-string entry triggers Validate's "entries must not be empty"
	// FieldError keyed ytdlp_hosts; the wrapper must re-key it to "hosts".
	_, _, err := ed.SetHosts("url", []string{""})
	feb, ok := err.(interface{ FieldErrors() []adapters.FieldError })
	if !ok {
		t.Fatalf("err type = %T, want field-error bearer", err)
	}
	if feb.FieldErrors()[0].Key != "hosts" {
		t.Errorf("field error key = %q, want hosts (re-keyed from ytdlp_hosts)", feb.FieldErrors()[0].Key)
	}
}
```

> `fakeRegistry` already exists in `adapter_settings_saver_test.go` (same package). Reuse it.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/mister-groovy-relay -run TestBridgeAdapterHostEditor -v`
Expected: FAIL (`newBridgeAdapterHostEditor undefined`).

- [ ] **Step 3: Create the wrapper**

Create `cmd/mister-groovy-relay/adapter_host_editor.go`:

```go
package main

import (
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/uiserver"
)

// bridgeAdapterHostEditor satisfies chassis.AdapterHostEditor from outside
// the chassis package. It normalizes the host list via the URL adapter's
// exported rules (the single normalization source), then persists the
// whole ytdlp_hosts array via the shared AdapterSaver. The saver is the
// sole validator+writer+applier; this wrapper does NOT call ApplyConfig.
type bridgeAdapterHostEditor struct {
	saver *uiserver.AdapterSaver
	reg   adapterLookup
}

func newBridgeAdapterHostEditor(saver *uiserver.AdapterSaver, reg adapterLookup) *bridgeAdapterHostEditor {
	return &bridgeAdapterHostEditor{saver: saver, reg: reg}
}

func (b *bridgeAdapterHostEditor) Hosts(name string) ([]string, bool) {
	a, ok := b.reg.Get(name)
	if !ok {
		return nil, false
	}
	h, ok := a.(interface{ CurrentHosts() []string })
	if !ok {
		return nil, false
	}
	return h.CurrentHosts(), true
}

func (b *bridgeAdapterHostEditor) SetHosts(name string, hosts []string) (string, []string, error) {
	a, ok := b.reg.Get(name)
	if !ok {
		return "", nil, &cmdChipError{status: http.StatusNotFound, chip: "UNKNOWN ADAPTER"}
	}
	if _, ok := a.(interface{ CurrentHosts() []string }); !ok {
		return "", nil, &cmdChipError{status: http.StatusNotFound, chip: "UNKNOWN ADAPTER"}
	}
	cleaned, err := url.NormalizeHosts(hosts)
	if err != nil {
		return "", nil, mapHostFieldErrors(err)
	}
	scope, err := b.saver.SaveValues(name, map[string]any{"ytdlp_hosts": cleaned}, []string{"ytdlp_hosts"}, a)
	if err != nil {
		return "", nil, translateSaverError(err)
	}
	label, ok := chassis.WireLabelForScope(scope)
	if !ok {
		return "", nil, &cmdChipError{status: http.StatusInternalServerError, chip: "WRITE FAILED"}
	}
	return label, cleaned, nil
}

// mapHostFieldErrors re-keys the adapter's "ytdlp_hosts" FieldErrors to the
// widget wire key "hosts" so the chassis envelope reads {errors:{hosts:...}}.
func mapHostFieldErrors(err error) error {
	if fe, ok := err.(adapters.FieldErrors); ok {
		out := make([]adapters.FieldError, 0, len(fe))
		for _, e := range fe {
			key := e.Key
			if key == "ytdlp_hosts" {
				key = "hosts"
			}
			out = append(out, adapters.FieldError{Key: key, Msg: e.Msg})
		}
		return &cmdAdapterFieldErrors{errs: out}
	}
	return &cmdAdapterFieldErrors{errs: []adapters.FieldError{{Key: "hosts", Msg: err.Error()}}}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/mister-groovy-relay -run TestBridgeAdapterHostEditor -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/adapter_host_editor.go cmd/mister-groovy-relay/adapter_host_editor_test.go
git commit -m "feat(cmd): bridgeAdapterHostEditor wrapper over AdapterSaver.SaveValues"
```

---

## Task 17: `bridgeAdapterCookieStore` cmd wrapper

**Files:**
- Create: `cmd/mister-groovy-relay/adapter_cookie_store.go`
- Create: `cmd/mister-groovy-relay/adapter_cookie_store_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/mister-groovy-relay/adapter_cookie_store_test.go`:

```go
package main

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

const sampleNetscape = "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t1893456000\tSID\tx\n"

func newURLAdapter(t *testing.T) *url.Adapter {
	t.Helper()
	a, err := url.New(url.AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("url.New: %v", err)
	}
	return a
}

func TestBridgeAdapterCookieStore_SaveStatClear(t *testing.T) {
	a := newURLAdapter(t)
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"url": a}}
	store := newBridgeAdapterCookieStore(reg)

	// Initially not loaded.
	view, ok := store.CookieStatus("url")
	if !ok || view.Loaded {
		t.Fatalf("initial status = (%+v, %v), want not-loaded, ok", view, ok)
	}
	// Save.
	view, err := store.SaveCookies("url", sampleNetscape)
	if err != nil {
		t.Fatalf("SaveCookies: %v", err)
	}
	if !view.Loaded || view.Bytes != int64(len(sampleNetscape)) || view.SetAt == "" {
		t.Errorf("save view = %+v", view)
	}
	// Clear.
	view, err = store.ClearCookies("url")
	if err != nil {
		t.Fatalf("ClearCookies: %v", err)
	}
	if view.Loaded {
		t.Errorf("after clear Loaded = true, want false")
	}
}

func TestBridgeAdapterCookieStore_SaveRejectsGarbageAsFieldError(t *testing.T) {
	a := newURLAdapter(t)
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{"url": a}}
	store := newBridgeAdapterCookieStore(reg)
	_, err := store.SaveCookies("url", "not cookies")
	feb, ok := err.(interface{ FieldErrors() []adapters.FieldError })
	if !ok {
		t.Fatalf("err type = %T, want field-error bearer", err)
	}
	if feb.FieldErrors()[0].Key != "cookies" {
		t.Errorf("field error key = %q, want cookies", feb.FieldErrors()[0].Key)
	}
}

func TestBridgeAdapterCookieStore_UnknownAdapter(t *testing.T) {
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{}}
	store := newBridgeAdapterCookieStore(reg)
	if _, ok := store.CookieStatus("url"); ok {
		t.Errorf("CookieStatus(unknown) ok=true, want false")
	}
	_, err := store.SaveCookies("url", sampleNetscape)
	ce, ok := err.(interface{ StatusCode() int })
	if !ok || ce.StatusCode() != 404 {
		t.Fatalf("SaveCookies(unknown) err = %v, want 404 chip", err)
	}
}

func TestBridgeAdapterCookieStore_NonCookieAdapter(t *testing.T) {
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{
		"streams": &fakeStreamsAdapter{current: map[string]any{"enabled": true}},
	}}
	store := newBridgeAdapterCookieStore(reg)
	if _, ok := store.CookieStatus("streams"); ok {
		t.Errorf("CookieStatus(non-cookie) ok=true, want false")
	}
	_, err := store.SaveCookies("streams", sampleNetscape)
	ce, ok := err.(interface {
		StatusCode() int
		Chip() string
	})
	if !ok || ce.StatusCode() != http.StatusNotFound || ce.Chip() != "UNKNOWN ADAPTER" {
		t.Fatalf("SaveCookies(non-cookie) err = %v, want 404 UNKNOWN ADAPTER", err)
	}
}

type fakeCookieAdapter struct {
	fakeStreamsAdapter
	saveErr  error
	clearErr error
}

func (f *fakeCookieAdapter) ValidateCookies(raw []byte) error { return nil }
func (f *fakeCookieAdapter) SaveCookies(raw []byte) (url.CookiesStat, error) {
	return url.CookiesStat{}, f.saveErr
}
func (f *fakeCookieAdapter) ClearCookies() error { return f.clearErr }
func (f *fakeCookieAdapter) CookieStat() (url.CookiesStat, bool, error) {
	return url.CookiesStat{}, false, nil
}

func TestBridgeAdapterCookieStore_FilesystemFailuresAreGenericChips(t *testing.T) {
	reg := &fakeRegistry{entries: map[string]adapters.Adapter{
		"url": &fakeCookieAdapter{
			fakeStreamsAdapter: fakeStreamsAdapter{current: map[string]any{"enabled": true}},
			saveErr:            errors.New("/tmp/secret/url_cookies.txt: permission denied"),
			clearErr:           errors.New("/tmp/secret/url_cookies.txt: permission denied"),
		},
	}}
	store := newBridgeAdapterCookieStore(reg)
	for name, err := range map[string]error{
		"save":  func() error { _, err := store.SaveCookies("url", sampleNetscape); return err }(),
		"clear": func() error { _, err := store.ClearCookies("url"); return err }(),
	} {
		ce, ok := err.(interface {
			StatusCode() int
			Chip() string
		})
		if !ok || ce.StatusCode() != http.StatusInternalServerError || ce.Chip() != "WRITE FAILED" {
			t.Fatalf("%s err = %v, want 500 WRITE FAILED", name, err)
		}
		if strings.Contains(err.Error(), "/tmp/secret") {
			t.Fatalf("%s error leaked filesystem path: %v", name, err)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/mister-groovy-relay -run TestBridgeAdapterCookieStore -v`
Expected: FAIL (`newBridgeAdapterCookieStore undefined`).

- [ ] **Step 3: Create the wrapper**

Create `cmd/mister-groovy-relay/adapter_cookie_store.go`:

```go
package main

import (
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
)

// cookieAdapter is the exported cookie surface the URL adapter provides.
// Concrete url.CookiesStat in the signature means only the URL adapter
// matches — exactly the intent.
type cookieAdapter interface {
	ValidateCookies(raw []byte) error
	SaveCookies(raw []byte) (url.CookiesStat, error)
	ClearCookies() error
	CookieStat() (url.CookiesStat, bool, error)
}

// bridgeAdapterCookieStore satisfies chassis.AdapterCookieStore. It composes
// the URL adapter's exported cookie methods; the chassis route owns its own
// JSON envelope (the legacy /ui handlers are not reused).
type bridgeAdapterCookieStore struct {
	reg adapterLookup
}

func newBridgeAdapterCookieStore(reg adapterLookup) *bridgeAdapterCookieStore {
	return &bridgeAdapterCookieStore{reg: reg}
}

func (b *bridgeAdapterCookieStore) lookup(name string) (cookieAdapter, bool) {
	a, ok := b.reg.Get(name)
	if !ok {
		return nil, false
	}
	ca, ok := a.(cookieAdapter)
	return ca, ok
}

func (b *bridgeAdapterCookieStore) CookieStatus(name string) (chassis.CookieStatusView, bool) {
	ca, ok := b.lookup(name)
	if !ok {
		return chassis.CookieStatusView{}, false
	}
	stat, present, err := ca.CookieStat()
	if err != nil {
		// Wired but stat errored — present as not-loaded (the pill shows
		// "not loaded" rather than a hard error at paint time).
		return chassis.CookieStatusView{Loaded: false}, true
	}
	return cookieView(stat, present), true
}

func (b *bridgeAdapterCookieStore) SaveCookies(name, raw string) (chassis.CookieStatusView, error) {
	ca, ok := b.lookup(name)
	if !ok {
		return chassis.CookieStatusView{}, &cmdChipError{status: http.StatusNotFound, chip: "UNKNOWN ADAPTER"}
	}
	if err := ca.ValidateCookies([]byte(raw)); err != nil {
		// Bad user input → 400 field error keyed for the widget.
		return chassis.CookieStatusView{}, &cmdAdapterFieldErrors{
			errs: []adapters.FieldError{{Key: "cookies", Msg: err.Error()}},
		}
	}
	stat, err := ca.SaveCookies([]byte(raw))
	if err != nil {
		// Filesystem failure → 500 chip, no path/OS error echoed.
		return chassis.CookieStatusView{}, &cmdChipError{status: http.StatusInternalServerError, chip: "WRITE FAILED"}
	}
	return cookieView(stat, true), nil
}

func (b *bridgeAdapterCookieStore) ClearCookies(name string) (chassis.CookieStatusView, error) {
	ca, ok := b.lookup(name)
	if !ok {
		return chassis.CookieStatusView{}, &cmdChipError{status: http.StatusNotFound, chip: "UNKNOWN ADAPTER"}
	}
	if err := ca.ClearCookies(); err != nil {
		return chassis.CookieStatusView{}, &cmdChipError{status: http.StatusInternalServerError, chip: "WRITE FAILED"}
	}
	return chassis.CookieStatusView{Loaded: false}, nil
}

func cookieView(stat url.CookiesStat, present bool) chassis.CookieStatusView {
	if !present {
		return chassis.CookieStatusView{Loaded: false}
	}
	return chassis.CookieStatusView{
		Loaded: true,
		Bytes:  stat.Size,
		SetAt:  stat.Mtime.UTC().Format("2006-01-02 15:04:05Z"),
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/mister-groovy-relay -run TestBridgeAdapterCookieStore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/adapter_cookie_store.go cmd/mister-groovy-relay/adapter_cookie_store_test.go
git commit -m "feat(cmd): bridgeAdapterCookieStore wrapper over URL adapter cookie methods"
```

---

## Task 18: Wire the two wrappers into `main.go`

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Construct the wrappers**

In `cmd/mister-groovy-relay/main.go`, immediately after the existing `adapterSaverWrapper := newBridgeAdapterSettingsSaver(adapterSaver, reg)` line (~main.go:387), add:

```go
	adapterHostEditor := newBridgeAdapterHostEditor(adapterSaver, reg)
	adapterCookieStore := newBridgeAdapterCookieStore(reg)
```

- [ ] **Step 2: Pass them into `chassis.Config`**

In the `chassis.New(chassis.Config{...})` literal (~main.go:393-422), after the `AdapterSettingsSaver:` / `StreamsRefresher:` lines, add:

```go
		AdapterHostEditor:  adapterHostEditor,
		AdapterCookieStore: adapterCookieStore,
```

- [ ] **Step 3: Build the binary**

Run: `go build ./cmd/mister-groovy-relay`
Expected: success.

- [ ] **Step 4: Run vet + the full unit suite**

Run: `go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/main.go
git commit -m "feat(cmd): wire URL host editor + cookie store into chassis.Config"
```

---

## Task 19: End-to-end integration tests (hosts + cookies)

**Files:**
- Create: `cmd/mister-groovy-relay/url_widgets_e2e_test.go`

Drives the real URL adapter + chassis routes + cmd wrappers through `httptest`, asserting disk persistence. Mirrors the build-tag + setup idiom of `adapter_settings_e2e_test.go`.

- [ ] **Step 1: Write the failing test**

Create `cmd/mister-groovy-relay/url_widgets_e2e_test.go`:

```go
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
	payload := e2ePost(t, ts, "/receiver/settings/adapter/url/hosts",
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
	save := e2ePost(t, ts, "/receiver/settings/adapter/url/cookies",
		"application/json", `{"cookies":`+jsonString(netscape)+`}`)
	cookie, _ := save["cookie"].(map[string]any)
	if cookie["loaded"] != true {
		t.Errorf("after save loaded = %v, want true", cookie["loaded"])
	}
	if _, err := os.Stat(a.CookiesPath()); err != nil {
		t.Errorf("cookies file not written: %v", err)
	}
	clr := e2ePost(t, ts, "/receiver/settings/adapter/url/cookies/clear",
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
```

> `decodeAdapterSectionFromFile` and `fakeRegistry` already exist in the package's existing test files (`adapter_settings_e2e_test.go:193`, `adapter_settings_saver_test.go:29`). Reuse them; do not redefine.

- [ ] **Step 2: Run the integration tests to verify they fail/pass**

Run: `go test -tags=integration ./cmd/mister-groovy-relay -run 'TestE2E_URLHosts_Persist|TestE2E_URLCookies_SaveAndClear' -v`
Expected: PASS (all wiring is in place by Task 18). If a route 404s, re-check Task 8/10 route registration; if hosts aren't normalized, re-check Task 16.

- [ ] **Step 3: Commit**

```bash
git add cmd/mister-groovy-relay/url_widgets_e2e_test.go
git commit -m "test(cmd): e2e URL hosts persist + cookies save/clear through chassis routes"
```

---

## Task 20: Full-suite verification + legacy-coexistence check

**Files:** none (verification only).

- [ ] **Step 1: Run every CI gate**

```bash
go vet ./...
go test ./...
go test -tags=integration ./...
```
Expected: all PASS. (CI additionally runs `go test -race ./...`; if cgo/gcc is unavailable locally, rely on CI for the race gate.)

- [ ] **Step 2: Confirm the isolation invariant**

Run: `go test ./internal/chassis -run TestProductionImports_NoCrossPackageCoupling -v`
Expected: PASS — `internal/chassis` still imports neither `internal/adapters/url` nor `internal/uiserver`.

- [ ] **Step 3: Legacy `/ui/*` coexistence check (spec §5.1 gate)**

Confirm the legacy generic `/ui/*` adapter form tolerates the three newly-exposed `Fields()` entries. Inspect how the legacy UI renders the URL adapter:

```bash
grep -rn "Fields()" internal/ui/adapter.go
```
If the legacy generic form ranges over `Fields()` for the URL adapter, the three new rows will appear there too (harmless — they are ordinary fields, and `/ui/*` is retired immediately after 4F). The URL adapter's custom `renderPanel` (`internal/adapters/url/ui.go`) renders only the hosts line / cookies section and is unaffected. Record the finding in the PR description; no code change required unless the legacy form errors on the new fields (it should not).

- [ ] **Step 4: Manual smoke test (browser)**

Run the bridge against the fake MiSTer per `CLAUDE.md`, open the receiver settings drawer → URL pane, and confirm: the four standard fields render and auto-save; adding/removing a host updates the tag-list and persists; pasting cookies + Save flips the pill to "N B · set …"; Clear flips it back to "not loaded". (No automated coverage for the browser layer; this is the JS acceptance check.)

- [ ] **Step 5: Final commit (if Step 3 produced notes worth recording)**

If you added any clarifying comment or doc note during verification:

```bash
git add -A
git commit -m "docs(4f): record legacy /ui coexistence verification note"
```

Otherwise, 4F is complete — open the PR.

---

## Notes for the implementer

- **Commit cadence:** each task ends with a commit. Code files are not gitignored — normal `git add`/`commit`. (Only `docs/superpowers/**` needs `git add -f`; this plan does not touch those at implementation time.)
- **Route genericity:** routes use `{name}` (`/adapter/{name}/hosts`, `/adapter/{name}/cookies`) even though only `url` is wired, matching the interface signatures and the 4D/4E per-adapter pattern. Unknown adapters return 404 via the wrappers.
- **Single applier:** `SaveValues` is the sole validator+writer+applier for host edits. The host wrapper normalizes (for the response + 400 path) but must never call `ApplyConfig` itself.
- **No new field primitive:** `ytdlp_hosts` and cookies never enter `adapters.FieldKind` / `overlayTouched` / the `field` template helper. The invariant holds.
