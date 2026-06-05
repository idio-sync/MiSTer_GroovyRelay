# Companion Extension Receiver-Chassis Reskin — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reskin the browser companion extension (popup + options page) to the receiver-chassis look, and add a small companion volume passthrough so the reused volume knob works.

**Architecture:** The extension keeps using the existing `/ui/companion/*` JSON API. We add one backend route (`POST /ui/companion/volume`) + an `output_volume` field on the status payload, both delegating to the same volume viewer/saver the chassis uses. The rest is CSS/HTML/JS in `extension/firefox/` — a popup-local stylesheet that mirrors `internal/chassis/static/chassis.css` tokens (aqua VFD, DSEG fonts, instrument buttons), reusing the chassis volume-knob markup but reimplementing its JS against the companion API.

**Tech Stack:** Go (`net/http`, `httptest`) for the bridge; plain HTML/CSS/JS + vitest for the extension; `build.sh` (bash) for packaging.

**Spec:** `docs/superpowers/specs/2026-05-30-companion-extension-receiver-reskin-design.md`

**Note:** `docs/superpowers/` is gitignored in this repo; this plan lives on disk but is not tracked. Commit only source/test/build files.

---

## File Structure

**Bridge (Go):**
- `internal/ui/companion.go` — add `output_volume` to status, add `handleCompanionVolume`, register the route.
- `internal/ui/server.go` — add `CompanionVolumeViewer` + `CompanionVolumeSaver` interfaces and `ui.Config` fields.
- `internal/ui/companion_test.go` — tests for status volume + the volume route.
- `cmd/mister-groovy-relay/main.go` — wire the viewer/saver into `ui.New`.

**Extension:**
- `extension/firefox/src/lib/bridge.js` — add `volume()`, remove `historyPlay`/`historyDelete`.
- `extension/firefox/src/popup/popup.html` — chassis markup, no history.
- `extension/firefox/src/popup/popup.css` — full rewrite to chassis tokens/components.
- `extension/firefox/src/popup/popup.js` — render against new DOM, volume knob, no history.
- `extension/firefox/src/options/options.html` — chassis markup (drop inline `<style>`).
- `extension/firefox/src/options/options.css` — NEW chassis stylesheet.
- `extension/firefox/src/options/options.js` — LED-style status rendering.
- `extension/firefox/src/fonts/` — swap committed woff2 files to DSEG/Inter (no build.sh change; it zips all of `src/`).
- `extension/firefox/test/{popup,options,bridge,manifest}.test.js` — update.
- `extension/firefox/manifest.json` + `package.json` — version bump.
- `THIRD_PARTY_NOTICES.md`, `README.md` — notices + docs.

---

## Task 1: Backend — `CompanionVolumeViewer` / `CompanionVolumeSaver` interfaces + `ui.Config` fields

**Files:**
- Modify: `internal/ui/server.go` (Config struct near line 112)

- [ ] **Step 1: Add the interfaces and Config fields**

In `internal/ui/server.go`, add these interface declarations near the other companion interfaces (e.g. just above `type Config struct`):

```go
// CompanionVolumeViewer reads the live global output volume for the
// companion popup's volume knob. *core.Manager satisfies it via OutputVolume().
type CompanionVolumeViewer interface {
	OutputVolume() int
}

// CompanionVolumeSaver persists a new global output volume (0..100) and
// applies it live. main.go wires the same volumeSaverAdapter used by the chassis.
type CompanionVolumeSaver interface {
	SaveOutputVolume(volume int) error
}
```

Then add two fields to `ui.Config` (right after the existing `CompanionDisplay CompanionDisplayProvider` line, ~114):

```go
	CompanionVolumeViewer CompanionVolumeViewer
	CompanionVolumeSaver  CompanionVolumeSaver
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/ui/...`
Expected: builds clean (fields unused so far is fine — struct fields don't trigger unused errors).

- [ ] **Step 3: Commit**

```bash
git add internal/ui/server.go
git commit -m "feat(ui): add CompanionVolume viewer/saver interfaces + Config fields"
```

---

## Task 2: Backend — `output_volume` in companion status (TDD)

**Files:**
- Modify: `internal/ui/companion.go` (struct ~line 17, handler ~line 83)
- Test: `internal/ui/companion_test.go`

- [ ] **Step 1: Write the failing test**

`internal/ui/companion_test.go` builds servers inline as `New(Config{Registry: reg, ...})`
(there is **no** `cfgForTest`/`emptyRegistryForTest` helper, and `ui.Config` has **no**
`Bridge`/`Manager` fields). It also provides a `companionJSONRequest(t, mux, method, path, body)`
helper that sets the `moz-extension://abc` Origin + `X-Bridge-Extension: 1` headers. Use those.
Add a shared volume fake + the test:

```go
type fakeCompanionVolume struct{ level int }

func (f *fakeCompanionVolume) OutputVolume() int            { return f.level }
func (f *fakeCompanionVolume) SaveOutputVolume(v int) error { f.level = v; return nil }

func TestCompanionStatusIncludesOutputVolume(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "url", displayName: "URL", enabled: true, enabledSet: true, state: adapters.StateRunning}); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{
		Registry:              reg,
		CompanionVolumeViewer: &fakeCompanionVolume{level: 73},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	rw := companionJSONRequest(t, mux, http.MethodGet, "/ui/companion/status", "")
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	var body struct {
		OutputVolume int `json:"output_volume"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.OutputVolume != 73 {
		t.Fatalf("output_volume = %d, want 73", body.OutputVolume)
	}
}
```

> `uiStubAdapter` is the existing stub already used throughout `companion_test.go`
> (e.g. `TestCompanionStatusURLSessionIncludesCapabilitiesAndHistory`). `companionJSONRequest`
> is defined at the top of that file and is GET-safe with an empty body.

- [ ] **Step 2: Run the test, verify it fails**

Run: `go test ./internal/ui/ -run TestCompanionStatusIncludesOutputVolume -v`
Expected: FAIL — `output_volume = 0, want 73` (field not emitted yet).

- [ ] **Step 3: Add the field + populate it**

In `internal/ui/companion.go`, add to `companionStatusResponse` (after `History`):

```go
	OutputVolume int `json:"output_volume"`
```

In `handleCompanionStatus`, the response is built as an **inline literal** passed
to `writeCompanionJSON`. Add the one field to that existing literal:

```go
	writeCompanionJSON(w, r, http.StatusOK, companionStatusResponse{
		OK:         true,
		Configured: true,
		BridgeURL:  companionBridgeURL(r),
		Health: companionHealth{
			Bridge:     "online",
			Mister:     "unknown",
			URLAdapter: s.companionAdapterHealth("url"),
		},
		Session:      s.companionSession(st),
		History:      history,
		OutputVolume: s.companionOutputVolume(), // <-- new line
	})
```

Add the helper near `companionHealth`:

```go
// companionOutputVolume returns the live output volume, or 0 when no
// viewer is wired (tests / degraded config).
func (s *Server) companionOutputVolume() int {
	if s.cfg.CompanionVolumeViewer == nil {
		return 0
	}
	return s.cfg.CompanionVolumeViewer.OutputVolume()
}
```

- [ ] **Step 4: Run the test, verify it passes**

Run: `go test ./internal/ui/ -run TestCompanionStatusIncludesOutputVolume -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/companion.go internal/ui/companion_test.go
git commit -m "feat(ui): include output_volume in companion status"
```

---

## Task 3: Backend — `POST /ui/companion/volume` route (TDD)

**Files:**
- Modify: `internal/ui/companion.go` (route mount ~line 249, new handler)
- Test: `internal/ui/companion_test.go`

- [ ] **Step 1: Write the failing tests**

Reuse the `fakeCompanionVolume` type added in Task 2 and the `companionJSONRequest` helper.
Add to `internal/ui/companion_test.go`:

```go
func TestCompanionVolumeSetsAndValidates(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "url", displayName: "URL", enabled: true, enabledSet: true, state: adapters.StateRunning}); err != nil {
		t.Fatal(err)
	}
	saver := &fakeCompanionVolume{}
	s, err := New(Config{Registry: reg, CompanionVolumeSaver: saver})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Mount(mux)

	// happy path
	rw := companionJSONRequest(t, mux, http.MethodPost, "/ui/companion/volume", `{"output_volume":42}`)
	if rw.Code != http.StatusOK {
		t.Fatalf("ok status = %d, want 200; body=%s", rw.Code, rw.Body.String())
	}
	if saver.level != 42 {
		t.Fatalf("saved = %d, want 42", saver.level)
	}

	// out of range
	if rw := companionJSONRequest(t, mux, http.MethodPost, "/ui/companion/volume", `{"output_volume":150}`); rw.Code != http.StatusBadRequest {
		t.Fatalf("range status = %d, want 400", rw.Code)
	}

	// missing field
	if rw := companionJSONRequest(t, mux, http.MethodPost, "/ui/companion/volume", `{}`); rw.Code != http.StatusBadRequest {
		t.Fatalf("missing status = %d, want 400", rw.Code)
	}
}

func TestCompanionVolumeRejectsNonExtension(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(&uiStubAdapter{name: "url", displayName: "URL", enabled: true, enabledSet: true, state: adapters.StateRunning}); err != nil {
		t.Fatal(err)
	}
	s, _ := New(Config{Registry: reg, CompanionVolumeSaver: &fakeCompanionVolume{}})
	mux := http.NewServeMux()
	s.Mount(mux)

	// No extension Origin / header — must be rejected by the gate.
	req := httptest.NewRequest(http.MethodPost, "/ui/companion/volume", strings.NewReader(`{"output_volume":10}`))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rw.Code)
	}
}
```

> Note: `decodeCompanionJSON` uses `DisallowUnknownFields`, so test bodies must contain
> only `output_volume`. `companionJSONRequest` already sets the extension Origin + header +
> JSON content-type; the second test deliberately bypasses it to exercise the gate.

- [ ] **Step 2: Run the tests, verify they fail**

Run: `go test ./internal/ui/ -run TestCompanionVolume -v`
Expected: FAIL — route returns 404/405 (not registered yet).

- [ ] **Step 3: Add the handler + register the route**

In `internal/ui/companion.go`, add the handler:

```go
func (s *Server) handleCompanionVolume(w http.ResponseWriter, r *http.Request) {
	if !requireCompanionJSON(w, r) {
		return
	}
	var req struct {
		OutputVolume *int `json:"output_volume"`
	}
	if !decodeCompanionJSON(w, r, &req) {
		return
	}
	if req.OutputVolume == nil {
		writeCompanionError(w, r, http.StatusBadRequest, "missing output_volume")
		return
	}
	v := *req.OutputVolume
	if v < 0 || v > 100 {
		writeCompanionError(w, r, http.StatusBadRequest, "output_volume must be in 0..100")
		return
	}
	if s.cfg.CompanionVolumeSaver == nil {
		writeCompanionError(w, r, http.StatusServiceUnavailable, "volume control not available")
		return
	}
	if err := s.cfg.CompanionVolumeSaver.SaveOutputVolume(v); err != nil {
		writeCompanionError(w, r, http.StatusInternalServerError, "volume save failed")
		return
	}
	writeCompanionJSON(w, r, http.StatusOK, map[string]any{"ok": true, "output_volume": v})
}
```

Register it alongside the other `mountCompanion` calls (after the `launch` lines, ~line 249):

```go
	s.mountCompanion(mux, http.MethodPost, "/ui/companion/volume", s.handleCompanionVolume)
```

- [ ] **Step 4: Run the tests, verify they pass**

Run: `go test ./internal/ui/ -run TestCompanionVolume -v`
Expected: PASS (both tests).

- [ ] **Step 5: Run the full ui package**

Run: `go test ./internal/ui/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/companion.go internal/ui/companion_test.go
git commit -m "feat(ui): add POST /ui/companion/volume passthrough"
```

---

## Task 4: Backend — wire volume viewer/saver in main.go

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go` (volumeSaverAdapter usage + `ui.New` literal ~line 348)

- [ ] **Step 1: Use a shared volume adapter instance**

The adapter type is `type volumeSaverAdapter struct { bs *uiserver.BridgeSaver }`
(main.go ~line 23) and the chassis config currently builds `&volumeSaverAdapter{bs: saver}`
at ~line 425. In `main.go`, just before the `ui.New(ui.Config{...})` call (line 351),
add a shared instance:

```go
	volumeBridge := &volumeSaverAdapter{bs: saver}
```

(`saver` is the existing `*uiserver.BridgeSaver`. `coreMgr` is the `*core.Manager`
that already satisfies the read side via `OutputVolume()`.)

- [ ] **Step 2: Pass the fields into `ui.New` and reuse the instance for chassis**

Add to the `ui.Config{...}` literal at line 351 (after `CompanionDisplay: urlAdapter,`):

```go
		CompanionVolumeViewer: coreMgr,
		CompanionVolumeSaver:  volumeBridge,
```

And change the chassis config's `VolumeSaver:` (line ~425) from `&volumeSaverAdapter{bs: saver}`
to the shared `volumeBridge` so there's a single instance:

```go
		VolumeSaver: volumeBridge,
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: builds clean.

- [ ] **Step 4: Run the full test suite for touched packages**

Run: `go test ./internal/ui/... ./cmd/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mister-groovy-relay/main.go
git commit -m "feat(cmd): wire companion volume viewer/saver into ui.Config"
```

---

## Task 5: Extension — `bridge.js` volume method + drop history client (TDD)

**Files:**
- Modify: `extension/firefox/src/lib/bridge.js`
- Test: `extension/firefox/test/bridge.test.js`

- [ ] **Step 1: Write the failing test**

`bridge.test.js` uses **msw** (`setupServer` + `http.post(...)` interceptors), not a
fetch capture. The `server`, `beforeAll/afterEach/afterAll` wiring, and `browser.storage`
mock already exist at the top of the file. Add a `describe` block matching the `play()`
pattern:

```js
describe("volume()", () => {
  it("POSTs output_volume to the companion volume endpoint", async () => {
    let captured;
    server.use(
      http.post("http://192.168.1.50:32500/ui/companion/volume", async ({ request }) => {
        captured = {
          headers: Object.fromEntries(request.headers),
          body: await request.json(),
        };
        return HttpResponse.json({ ok: true, output_volume: 55 });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await bridge.volume(55);

    expect(result).toEqual({ ok: true, output_volume: 55 });
    expect(captured.body).toEqual({ output_volume: 55 });
    expect(captured.headers["x-bridge-extension"]).toBe("1");
    expect(captured.headers["content-type"]).toBe("application/json");
  });
});
```

> There are **no** `historyPlay`/`historyDelete` tests in `bridge.test.js` (it covers
> play/control/getStatus/launch/plexTimelinePoll only), so nothing to remove here — the
> history-test removal is entirely in `popup.test.js` (Task 9). `companionFetch` returns
> `{ ok: true, ...data }` on a 2xx, so the result spreads the JSON body as shown.

- [ ] **Step 2: Run it, verify it fails**

Run: `cd extension/firefox && npx vitest run test/bridge.test.js`
Expected: FAIL — `bridge.volume is not a function`.

- [ ] **Step 3: Add `volume()`, remove history methods**

In `extension/firefox/src/lib/bridge.js`, add:

```js
export async function volume(level) {
  return companionFetch("/ui/companion/volume", {
    method: "POST",
    body: { output_volume: level },
  });
}
```

Delete the `historyPlay` and `historyDelete` exported functions (lines ~85-97).

- [ ] **Step 4: Run it, verify it passes**

Run: `cd extension/firefox && npx vitest run test/bridge.test.js`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add extension/firefox/src/lib/bridge.js extension/firefox/test/bridge.test.js
git commit -m "feat(ext): add companion volume client; drop history client methods"
```

---

## Task 6: Extension — swap the committed font files in src/fonts/

**Files:**
- Delete: `extension/firefox/src/fonts/{SpaceGrotesk-600,InterTight-400,InterTight-500,JetBrainsMono-400}.woff2`
- Create: `extension/firefox/src/fonts/{DSEG14Classic-Regular,DSEG14Classic-Bold,DSEG7Classic-Regular,DSEG7Classic-Bold,Inter-Variable}.woff2`

> Note: `build.sh` has **no** font array — it zips the whole `src/` tree
> (`includes = ["manifest.json", "src", "icons"]`). The fonts are committed
> files under `src/fonts/`; swapping them is the entire change. `manifest.test.js`
> asserts these files exist via `fs.existsSync` (updated in Task 11).

- [ ] **Step 1: Copy the chassis fonts in, remove the old ones**

```bash
cd "extension/firefox/src/fonts"
cp ../../../../internal/chassis/static/fonts/DSEG14Classic-Regular.woff2 .
cp ../../../../internal/chassis/static/fonts/DSEG14Classic-Bold.woff2 .
cp ../../../../internal/chassis/static/fonts/DSEG7Classic-Regular.woff2 .
cp ../../../../internal/chassis/static/fonts/DSEG7Classic-Bold.woff2 .
cp ../../../../internal/chassis/static/fonts/Inter-Variable.woff2 .
git rm SpaceGrotesk-600.woff2 InterTight-400.woff2 InterTight-500.woff2 JetBrainsMono-400.woff2
```

(Adjust the `../` depth if your shell's CWD differs; the source dir is
`internal/chassis/static/fonts/` from the repo root.)

- [ ] **Step 2: Verify the directory contents**

Run: `ls extension/firefox/src/fonts/`
Expected: exactly the 5 new `.woff2` files (+ any existing `LICENSE` if present); none of the 4 old families.

- [ ] **Step 3: Commit**

```bash
git add extension/firefox/src/fonts/
git commit -m "assets(ext): swap popup fonts to DSEG + Inter (committed in src/fonts)"
```

---

## Task 7: Extension — popup CSS (full rewrite to chassis)

**Files:**
- Rewrite: `extension/firefox/src/popup/popup.css`

- [ ] **Step 1: Replace popup.css with the chassis stylesheet**

Write `extension/firefox/src/popup/popup.css` with the full content below. (Derived from the approved mockups; reuses chassis recipes, scoped to the popup root rather than `body.receiver`.)

```css
/* Fonts (bundled woff2 in ../fonts) */
@font-face { font-family:'DSEG14-Classic'; src:url('../fonts/DSEG14Classic-Regular.woff2') format('woff2'); font-weight:400; font-display:block; }
@font-face { font-family:'DSEG14-Classic'; src:url('../fonts/DSEG14Classic-Bold.woff2') format('woff2'); font-weight:700; font-display:block; }
@font-face { font-family:'DSEG7-Classic'; src:url('../fonts/DSEG7Classic-Regular.woff2') format('woff2'); font-weight:400; font-display:block; }
@font-face { font-family:'DSEG7-Classic'; src:url('../fonts/DSEG7Classic-Bold.woff2') format('woff2'); font-weight:700; font-display:block; }
@font-face { font-family:'Inter'; src:url('../fonts/Inter-Variable.woff2') format('woff2-variations'); font-weight:100 900; font-display:swap; }

:root {
  --vfd: oklch(0.92 0.20 175);
  --vfd-bright: oklch(0.96 0.18 175);
  --vfd-glow: oklch(0.85 0.22 175 / 0.75);
  --vfd-glow-soft: oklch(0.85 0.22 175 / 0.32);
  --vfd-dim: oklch(0.55 0.16 175 / 0.78);
  --vfd-faded: oklch(0.55 0.16 175 / 0.35);
  --amber: oklch(0.82 0.18 75);
  --ok: oklch(0.78 0.14 150);
  --err: oklch(0.66 0.20 28);
}

* { box-sizing: border-box; }
body {
  margin: 0; width: 360px;
  background: linear-gradient(180deg, #2a2a2e, #1f1f23);
  color: #c0c0c4;
  font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  -webkit-font-smoothing: antialiased;
}
.popup-shell { position: relative; padding: 12px; display: flex; flex-direction: column; gap: 10px; }
.popup-shell::before {
  content: ""; position: absolute; inset: 0; pointer-events: none;
  background: linear-gradient(180deg, transparent, rgba(0,0,0,.28)),
              repeating-linear-gradient(90deg, transparent 0 1px, rgba(255,255,255,.015) 1px 2px);
}
.popup-shell > * { position: relative; z-index: 1; }

/* status bar */
.status-bar { display: flex; align-items: center; justify-content: space-between; gap: 8px;
  padding: 1px 2px 8px; border-bottom: 1px solid #0a0a0b; box-shadow: 0 1px 0 rgba(255,255,255,.04); }
.leds { display: flex; gap: 9px; align-items: center; }
.led { display: flex; align-items: center; gap: 4px; }
.led .light { width: 7px; height: 7px; border-radius: 50%; background: #2a2a2c; box-shadow: inset 0 1px 1px rgba(0,0,0,.6); }
.led.green.on .light { background: #00ff66; box-shadow: 0 0 6px #00ff66cc; }
.led.aqua.on .light { background: oklch(0.90 0.22 175); box-shadow: 0 0 5px oklch(0.90 0.22 175 / .9); }
.led.red.on .light { background: #ff3030; box-shadow: 0 0 6px #ff3030cc; animation: rec-pulse 1.6s ease-in-out infinite; }
.led .lbl { font-size: 8px; letter-spacing: .16em; color: #5a5a5e; text-transform: uppercase; }
.led.on .lbl { color: #c0c0c4; }
@keyframes rec-pulse { 0%,100%{opacity:1} 50%{opacity:.45} }
.brand-plate { padding: 4px 8px; border: 1px solid #0a0a0b; border-radius: 2px; text-align: right;
  background: linear-gradient(180deg,#1a1a1c,#0a0a0b); box-shadow: inset 0 1px 0 rgba(255,255,255,.05); }
.brand-plate .name { font-size: 12px; font-weight: 900; letter-spacing: .14em; color: #d4d4d8; }

/* VFD screen */
.screen-frame { padding: 4px; border: 1px solid #000; border-radius: 3px;
  background: linear-gradient(180deg,#0a0a0b,#050506);
  box-shadow: inset 0 2px 6px rgba(0,0,0,.8), inset 0 -1px 0 rgba(255,255,255,.06), 0 1px 0 rgba(255,255,255,.06); }
.vfd { position: relative; border-radius: 2px; overflow: hidden; padding: 11px 12px;
  color: var(--vfd); text-shadow: 0 0 3px var(--vfd-glow), 0 0 1px var(--vfd-bright);
  background: radial-gradient(ellipse at center, oklch(0.14 0.04 175), oklch(0.08 0.03 175) 80%);
  display: grid; grid-template-columns: minmax(0,1fr) auto; gap: 12px; align-items: center; }
.vfd::after { content:""; position:absolute; inset:0; pointer-events:none; mix-blend-mode:multiply;
  background: repeating-linear-gradient(0deg, transparent 0 2px, rgba(0,0,0,.18) 2px 3px); }
.vfd > * { position: relative; z-index: 1; min-width: 0; }
.tier { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; text-transform: uppercase; font-family: 'DSEG14-Classic', monospace; }
.tier.is-empty { display: none; }
.tier-primary { font-size: 17px; font-weight: 700; letter-spacing: .04em; line-height: 1.05; margin-bottom: 6px; }
.tier-secondary { font-size: 11px; letter-spacing: .06em; margin-bottom: 4px; }
.tier-tertiary { font-size: 10px; letter-spacing: .08em; color: var(--vfd-dim); }
.vfd-right { text-align: right; display: grid; gap: 2px; padding-left: 11px; border-left: 1px solid oklch(0.16 0.04 175 / .55); }
.vfd-right .k { font-size: 8px; letter-spacing: .2em; color: var(--vfd-dim); text-shadow: none; text-transform: uppercase; }
.vfd-right .v { font-family: 'DSEG7-Classic', monospace; font-size: 18px; font-weight: 700; text-shadow: 0 0 4px var(--vfd-glow); }

/* transport */
.transport { display: flex; flex-direction: column; gap: 8px; }
.transport-row { display: flex; gap: 6px; }
.trn { flex: 1; height: 33px; border: 1px solid #0a0a0b; border-radius: 2px; color: #c0c0c4; font-size: 14px; cursor: pointer;
  background: linear-gradient(180deg,#4a4a50,#2a2a2e 50%,#1a1a1c);
  box-shadow: inset 0 1px 0 rgba(255,255,255,.12), inset 0 -1px 0 rgba(0,0,0,.4), 0 1px 2px rgba(0,0,0,.4); }
.trn.primary { color: var(--vfd); text-shadow: 0 0 5px var(--vfd-glow-soft); flex: 1.4; }
.trn:disabled { opacity: .4; cursor: default; }
.seek { position: relative; height: 6px; border-radius: 3px; background: rgba(0,0,0,.5); box-shadow: inset 0 1px 2px rgba(0,0,0,.6); overflow: hidden; }
.seek .fill { position: absolute; inset: 0 auto 0 0; width: var(--seek-percent, 0%); background: var(--vfd); box-shadow: 0 0 6px var(--vfd-glow); }
.seek-time { display: flex; gap: 7px; align-items: baseline; font-size: 11px; color: var(--vfd);
  font-family: 'DSEG7-Classic', monospace; text-shadow: 0 0 3px var(--vfd-glow-soft); }
.seek-time .sep, .seek-time .pct { color: var(--vfd-dim); text-shadow: none; }
.seek-time .pct { margin-left: auto; }
.progress-wrap[hidden] { display: none; }

/* volume control (ported from chassis .volume-control) */
.volume-control { display: flex; align-items: center; gap: 14px; padding: 11px 14px; border: 1px solid #0a0a0b; border-radius: 4px;
  background: linear-gradient(180deg,#222226,#161619); box-shadow: inset 0 1px 0 rgba(255,255,255,.05); --volume-angle: -135deg; }
.volume-dial { position: relative; width: 50px; height: 50px; border-radius: 50%; flex: 0 0 auto; cursor: grab;
  background: radial-gradient(circle at 38% 32%, #5a5a62, #34343a 45%, #1a1a1c);
  box-shadow: inset 0 1px 0 rgba(255,255,255,.18), inset 0 -3px 6px rgba(0,0,0,.5), 0 3px 6px rgba(0,0,0,.5);
  transform: rotate(var(--volume-angle)); transition: transform 80ms; }
.volume-notch { position: absolute; top: 5px; left: 50%; width: 3px; height: 15px; background: var(--vfd); border-radius: 2px;
  box-shadow: 0 0 5px var(--vfd-glow); transform: translateX(-50%); }
.volume-meta { display: flex; flex-direction: column; gap: 6px; flex: 1; min-width: 0; }
.volume-top { display: flex; align-items: baseline; justify-content: space-between; }
.volume-label { font: 600 9px 'Inter', sans-serif; letter-spacing: .22em; color: var(--vfd-dim); text-transform: uppercase; }
.volume-value { font-family: 'DSEG7-Classic', monospace; font-size: 20px; font-weight: 700; color: var(--vfd); text-shadow: 0 0 5px var(--vfd-glow); }
.volume-tick-ring { display: flex; gap: 2px; }
.volume-tick { width: 3px; height: 10px; border-radius: 1px; background: oklch(0.30 0.02 250); }
.volume-tick.on { background: var(--vfd); box-shadow: 0 0 4px var(--vfd-glow); }
.volume-range { position: absolute; width: 1px; height: 1px; opacity: 0; pointer-events: none; }
.volume-control.saving .volume-value { color: var(--amber); }
.volume-control.failed .volume-value { color: var(--err); }

/* idle */
.cast-box { padding: 11px 12px; border: 1px solid #0a0a0b; border-radius: 3px; background: linear-gradient(180deg,#222226,#161619); }
.cast-box .eyebrow { font-size: 9px; letter-spacing: .22em; color: var(--vfd-dim); text-transform: uppercase; }
.tab-url { font-family: 'DSEG7-Classic', monospace; font-size: 11px; color: #c0c0c4; margin: 6px 0 10px; word-break: break-all; line-height: 1.4; }
.cast-btn { width: 100%; height: 36px; border: 1px solid #0a0a0b; border-radius: 2px; cursor: pointer; color: var(--vfd);
  font-size: 11px; letter-spacing: .18em; text-transform: uppercase; background: linear-gradient(180deg,#3a3a40,#22222a); text-shadow: 0 0 5px var(--vfd-glow-soft); }
.cast-btn:disabled { opacity: .4; cursor: default; color: var(--vfd-faded); text-shadow: none; }
.health-grid { display: flex; gap: 6px; }
.health-grid div { flex: 1; padding: 7px 8px; border: 1px solid #0a0a0b; border-radius: 2px; background: rgba(0,0,0,.3); text-align: center; }
.health-grid span { display: block; font-size: 8px; letter-spacing: .16em; color: #7a7a82; text-transform: uppercase; }
.health-grid strong { display: block; font-size: 9px; letter-spacing: .12em; color: var(--vfd); text-transform: uppercase; margin-top: 3px; text-shadow: 0 0 4px var(--vfd-glow-soft); }

/* footer + status */
.actions { display: flex; gap: 7px; }
.actions button { flex: 1; height: 30px; border: 1px solid #0a0a0b; border-radius: 2px; color: #b4b4ba; font-size: 10px; letter-spacing: .14em;
  text-transform: uppercase; cursor: pointer; background: linear-gradient(180deg,#34343a,#202024); }
.actions button.primary { color: var(--amber); }
.status { min-height: 18px; font-size: 11px; color: var(--vfd-dim); overflow-wrap: anywhere; }
.status.ok { color: var(--ok); } .status.err { color: var(--err); }
[hidden] { display: none !important; }

@media (prefers-reduced-motion: reduce) {
  .led.red.on .light { animation: none; }
  .volume-dial { transition: none; }
}
```

- [ ] **Step 2: Commit**

```bash
git add extension/firefox/src/popup/popup.css
git commit -m "style(ext): rewrite popup.css to receiver-chassis tokens"
```

---

## Task 8: Extension — popup HTML restructure (no history)

**Files:**
- Rewrite: `extension/firefox/src/popup/popup.html`

- [ ] **Step 1: Replace popup.html body markup**

Write `extension/firefox/src/popup/popup.html` with this structure (IDs/`data-*` chosen so popup.js can target them; volume control rendered ONCE, below both views; no `#history`):

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <link rel="stylesheet" href="popup.css">
</head>
<body>
  <div id="popup" class="popup-shell" data-state="loading">
    <div class="status-bar">
      <div class="leds">
        <span class="led green on" id="led-pwr"><span class="light"></span><span class="lbl">PWR</span></span>
        <span class="led aqua" id="led-link"><span class="light"></span><span class="lbl">LINK</span></span>
        <span class="led red" id="led-cast"><span class="light"></span><span class="lbl">CAST</span></span>
      </div>
      <div class="brand-plate"><span class="name">GROOVYRELAY</span></div>
    </div>

    <!-- unconfigured -->
    <section id="unconfigured" class="view" hidden>
      <div class="cast-box">
        <p class="eyebrow">Setup required</p>
        <div class="tab-url">Bridge URL is not set.</div>
        <button type="button" id="configure" class="cast-btn">Configure</button>
      </div>
    </section>

    <!-- active -->
    <section id="active-view" class="view" hidden>
      <div class="screen-frame"><div class="vfd">
        <div>
          <div id="vfd-primary" class="tier tier-primary"></div>
          <div id="vfd-secondary" class="tier tier-secondary"></div>
          <div id="vfd-tertiary" class="tier tier-tertiary"></div>
        </div>
        <div class="vfd-right">
          <div class="k">State</div>
          <div id="vfd-state" class="v">--</div>
        </div>
      </div></div>
      <div class="transport">
        <div class="transport-row">
          <button type="button" id="replay" class="trn" title="Replay">&#x27f2;</button>
          <button type="button" id="pause-resume" class="trn primary" title="Pause / Resume">
            <span data-state-icon="playing">&#x23f8;</span><span data-state-icon="paused" hidden>&#x25b6;</span>
          </button>
          <button type="button" id="stop" class="trn" title="Stop">&#x23f9;</button>
        </div>
        <div id="progress-wrap" class="progress-wrap" hidden>
          <div class="seek" id="seek" style="--seek-percent:0%;"><div class="fill"></div></div>
          <div class="seek-time">
            <span id="position-label">0:00</span><span class="sep">/</span>
            <span id="duration-label">0:00</span><span id="percent-label" class="pct"></span>
          </div>
        </div>
      </div>
    </section>

    <!-- idle -->
    <section id="idle-view" class="view" hidden>
      <div class="cast-box">
        <p class="eyebrow">Cast this tab</p>
        <div id="tab-url" class="tab-url"></div>
        <button type="button" id="cast" class="cast-btn">Cast tab</button>
      </div>
      <div class="health-grid">
        <div><span>Bridge</span><strong id="health-bridge">--</strong></div>
        <div><span>MiSTer</span><strong id="health-mister">--</strong></div>
        <div><span>URL</span><strong id="health-url">--</strong></div>
      </div>
    </section>

    <!-- shared volume control -->
    <div class="volume-control" id="volume-control" data-volume-value="0" style="--volume-angle:-135deg;">
      <div class="volume-dial" aria-hidden="true"><span class="volume-notch"></span></div>
      <div class="volume-meta">
        <div class="volume-top"><span class="volume-label">Volume</span><span class="volume-value" id="volume-value">0</span></div>
        <div class="volume-tick-ring" id="volume-tick-ring" aria-hidden="true"></div>
      </div>
      <input class="volume-range" id="volume-range" type="range" min="0" max="100" step="1" value="0" aria-label="Volume">
    </div>

    <div class="actions">
      <button type="button" id="launch-groovy" class="primary">&#9656; Launch</button>
      <button type="button" id="open-webui">Web UI</button>
      <button type="button" id="open-options">Setup</button>
    </div>
    <div id="status" class="status" role="status" aria-live="polite"></div>
  </div>

  <script src="../lib/browser-polyfill.js"></script>
  <script type="module" src="popup.js"></script>
</body>
</html>
```

- [ ] **Step 2: Sanity-load**

Run: `cd extension/firefox && npx vitest run test/popup.test.js` (will fail until Task 9 — expected at this point; this step just confirms the HTML parses in jsdom without throwing in the fixture).

- [ ] **Step 3: Commit**

```bash
git add extension/firefox/src/popup/popup.html
git commit -m "feat(ext): chassis popup markup; remove history list"
```

---

## Task 9: Extension — popup.js render + volume knob (TDD)

**Files:**
- Modify: `extension/firefox/src/popup/popup.js`
- Test: `extension/firefox/test/popup.test.js`

- [ ] **Step 1: Update the test fixture + rewrite history test as volume test**

`test/popup.test.js` mocks `bridge` via `vi.spyOn(bridge, "getStatus")`, renders the DOM
through a local `popupMarkup()` fixture (~lines 5-65) injected by `renderPopup()`
(`beforeEach`), then calls the imported `initPopup(document)` which returns a `state`
object with `pollTimer` (tests `clearInterval(state.pollTimer)` at the end). There is **no**
`loadPopup()` helper — use `initPopup(document)` and set `bridgeURL` in storage first.

Do all of:

1. **Replace the `popupMarkup()` fixture body** with the new markup from Task 8 (so the
   fixture's IDs match: `#led-pwr/link/cast`, `#unconfigured/#active-view/#idle-view`,
   `#vfd-primary/secondary/tertiary`, `#vfd-state`, `#replay/#pause-resume/#stop`,
   `#progress-wrap/#seek/#position-label/#duration-label/#percent-label`,
   `#volume-control/#volume-value/#volume-tick-ring/#volume-range`, `#tab-url/#cast`,
   `#health-bridge/mister/url`, `#launch-groovy/#open-webui/#open-options`, `#status`).
2. **Delete** the `"plays and deletes history rows by opaque id"` test (~lines 225-247)
   and remove every `history: [...]` / `history: []` field from the `playingStatus()`
   helper and other mock status objects.
3. **Update assertions that reference removed IDs:** the existing tests use
   `#media-title` and `#source-line` (old markup). Change them to `#vfd-primary` /
   `#vfd-secondary`. The "active remote first" test should assert
   `getElementById("vfd-primary").textContent === "Night"` and
   `getElementById("vfd-secondary").textContent === "archive.org/night"`. The "disables
   stale controls" test references `#pause` — change to `#pause-resume`.
4. **Add** the two new tests:

```js
it("renders VFD tiers and volume from status", async () => {
  vi.spyOn(bridge, "getStatus").mockResolvedValue(playingStatus({
    session: { state: "playing", title: "BIG BUCK BUNNY", source_display: "archive.org",
      resolved_via: "direct", position_ms: 30000, duration_ms: 120000,
      capabilities: { can_pause: true, can_stop: true, can_replay: true, can_seek: true } },
    output_volume: 64,
  }));
  await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
  const state = await initPopup(document);
  expect(document.getElementById("vfd-primary").textContent).toBe("BIG BUCK BUNNY");
  expect(document.getElementById("vfd-tertiary").textContent).toBe("direct");
  expect(document.getElementById("volume-value").textContent).toBe("64");
  clearInterval(state.pollTimer);
});

it("posts volume on range change", async () => {
  const spy = vi.spyOn(bridge, "volume").mockResolvedValue({ ok: true, output_volume: 80 });
  vi.spyOn(bridge, "getStatus").mockResolvedValue(playingStatus({ output_volume: 50 }));
  await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
  const state = await initPopup(document);
  const range = document.getElementById("volume-range");
  range.value = "80";
  range.dispatchEvent(new Event("change"));
  await new Promise((r) => setTimeout(r, 0));
  expect(spy).toHaveBeenCalledWith(80);
  clearInterval(state.pollTimer);
});
```

> Note `playingStatus()` will need an `output_volume` key threaded through (add a default,
> e.g. `output_volume: 50`, to the helper's returned object). Keep the existing
> seek-hidden, stale-controls, polling-pause, open-webui, and launch tests (with the ID
> renames above).

- [ ] **Step 2: Run, verify failures**

Run: `cd extension/firefox && npx vitest run test/popup.test.js`
Expected: FAIL (new IDs not populated; `volume` not wired).

- [ ] **Step 3: Rewrite popup.js render**

`popup.js` exports `initPopup` and `truncate` (both imported by the test suite — keep
both exported). The current file has a `#configured`/`#unconfigured` two-wrapper model with
`showConfigured()`/`showUnconfigured()` (~lines 217-225) and a `renderHistory()` (~lines
187-215). The new markup (Task 8) has **no `#configured` wrapper** — it has flat sibling
sections `#unconfigured` / `#active-view` / `#idle-view` plus a shared volume control.

Make these changes:

- **Remove `renderHistory` entirely** and its call in `render()`. Remove any history
  wiring in `bindStaticActions`.
- **Replace `showConfigured`/`showUnconfigured`** with a single visibility switch in
  `render()` (no `#configured` element exists anymore):

```js
function setView(doc, view) {
  // view ∈ "unconfigured" | "active" | "idle"
  doc.getElementById("unconfigured").hidden = view !== "unconfigured";
  doc.getElementById("active-view").hidden = view !== "active";
  doc.getElementById("idle-view").hidden = view !== "idle";
}
```

  In `initPopup`, the unconfigured early-return calls `setView(doc, "unconfigured")`
  instead of `showUnconfigured(doc)`. The shared `.volume-control` and `.actions`/`#status`
  are always visible (outside the three sections), so they need no show/hide.

- **`render(doc, state)`**: set `#popup` `data-state` to `stale` or the session state;
  choose `setView` from `active = session.state === "playing" || "paused"`; call
  `renderLeds`, `renderActive` or `renderIdle`, and `renderVolume`.

- **`renderLeds(doc, state, snapshot)`** (spec LED→health mapping):

```js
function renderLeds(doc, state, snapshot) {
  const session = snapshot.session || {};
  const health = snapshot.health || {};
  const reachable = !state.stale;                 // a successful poll happened
  const playing = session.state === "playing" || session.state === "paused";
  doc.getElementById("led-pwr").classList.toggle("on", reachable);
  doc.getElementById("led-link").classList.toggle("on", reachable && health.bridge === "online");
  doc.getElementById("led-cast").classList.toggle("on", playing);
}
```

- **`renderActive(doc, state, session, caps)`**: set the three VFD tiers + `is-empty`,
  the state readout, progress, transport enablement, and the pause/resume glyph:

```js
function setTier(doc, id, text) {
  const el = doc.getElementById(id);
  el.textContent = text || "";
  el.classList.toggle("is-empty", !text);
}
// ...inside renderActive:
setTier(doc, "vfd-primary", session.title || session.source_display);
setTier(doc, "vfd-secondary", session.source_display);
setTier(doc, "vfd-tertiary", session.resolved_via);
doc.getElementById("vfd-state").textContent = (session.state || "").toUpperCase();

const playing = session.state === "playing";
doc.querySelector('[data-state-icon="playing"]').hidden = !playing;
doc.querySelector('[data-state-icon="paused"]').hidden = playing;

doc.getElementById("replay").disabled = disabledFor(state, caps.can_replay);
doc.getElementById("stop").disabled = disabledFor(state, caps.can_stop);
// pause/resume button enabled if either capability is available for the current state:
const canToggle = playing ? caps.can_pause : caps.can_resume;
doc.getElementById("pause-resume").disabled = disabledFor(state, canToggle);

const duration = Number(session.duration_ms || 0);
const position = Number(session.position_ms || 0);
const wrap = doc.getElementById("progress-wrap");
wrap.hidden = duration <= 0;
if (duration > 0) {
  doc.getElementById("position-label").textContent = formatTime(position);
  doc.getElementById("duration-label").textContent = formatTime(duration);
  const pct = Math.max(0, Math.min(100, Math.round((position / duration) * 100)));
  doc.getElementById("percent-label").textContent = `${pct}%`;
  doc.getElementById("seek").style.setProperty("--seek-percent", `${pct}%`);
}
```

- **Pause/resume click handler** (single button; pick action from current state) in
  `bindStaticActions`:

```js
doc.getElementById("pause-resume")?.addEventListener("click", () => {
  const playing = state.snapshot?.session?.state === "playing";
  runCommand(doc, state, () => bridge.control(playing ? "pause" : "resume"),
    playing ? "Pause failed" : "Resume failed");
});
doc.getElementById("replay")?.addEventListener("click", () =>
  runCommand(doc, state, () => bridge.control("replay"), "Replay failed"));
doc.getElementById("stop")?.addEventListener("click", () =>
  runCommand(doc, state, () => bridge.control("stop"), "Stop failed"));
```

  (Seek is rendered read-only as a progress bar in this reskin — the chassis seek is a
  drag widget, but the companion popup keeps the existing behavior of showing position;
  if the previous popup had a seek `input`, preserve its `change → bridge.control("seek",
  {offset_ms})` handler. The old popup used an `<input id="seek">`; the new markup uses a
  display-only `.seek` bar, so the seek **control** is dropped — note this is consistent
  with the mockup. Keep `caps.can_seek` only for the progress display.)

- **`renderIdle(doc, state, caps)`**: set `#tab-url`, enable/disable `#cast` (from
  `castable` + `caps.can_play`), set `#health-bridge`/`#health-mister`/`#health-url` from
  `snapshot.health.bridge`/`.mister`/`.url_adapter` (render `mister` muted since it's
  always `unknown`).

- **`renderVolume` + tick ring** (flat bar — see Step 3a). Bind `#volume-range`:

```js
doc.getElementById("volume-range")?.addEventListener("input", (e) => {
  // live visual feedback only; no network until change
  renderVolumeVisual(doc, Number(e.target.value));
});
doc.getElementById("volume-range")?.addEventListener("change", (e) => {
  runCommand(doc, state, () => bridge.volume(Number(e.target.value)), "Volume failed");
});
```

  Optional pointer drag on `.volume-dial` may set `#volume-range.value` and dispatch
  `input`/`change`; jsdom lacks layout geometry, so keep drag untested and the range
  input as the testable control.

- Keep the 2s poll, `commanding` pause, `disabledFor`, capability gating, and
  `runCommand` helper unchanged.

- [ ] **Step 3a: Tick ring is a flat bar (decision)**

The receiver's radial tick ring depends on 21 per-tick `.tick-N { --tick-angle }` rules +
absolute positioning. The popup deliberately renders a **flat horizontal tick bar**
instead (simpler, reads fine at popup scale, matches the approved mockup). So Task 7's
`.volume-tick-ring { display: flex }` + plain `.volume-tick` is correct and intentional —
do **not** port the 21 radial rules. `buildTickRing` emits plain spans; `renderVolume`
only toggles `.on`:

```js
function buildTickRing(doc) {
  const ring = doc.getElementById("volume-tick-ring");
  ring.textContent = "";
  for (let i = 0; i < 21; i++) {
    const t = doc.createElement("span");
    t.className = "volume-tick";
    ring.appendChild(t);
  }
}
function renderVolumeVisual(doc, v) {
  v = Math.max(0, Math.min(100, Number(v) || 0));
  doc.getElementById("volume-value").textContent = String(v);
  doc.getElementById("volume-control").style.setProperty("--volume-angle", `${-135 + (v / 100) * 270}deg`);
  const onCount = Math.round((v / 100) * 21);
  doc.querySelectorAll(".volume-tick").forEach((t, i) => t.classList.toggle("on", i < onCount));
}
function renderVolume(doc, v) {
  renderVolumeVisual(doc, v);
  doc.getElementById("volume-range").value = String(Math.max(0, Math.min(100, Number(v) || 0)));
}
```

  Call `buildTickRing(doc)` once in `initPopup` before the first render.

- [ ] **Step 4: Run, verify pass**

Run: `cd extension/firefox && npx vitest run test/popup.test.js`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add extension/firefox/src/popup/popup.js extension/firefox/test/popup.test.js
git commit -m "feat(ext): SSE-free chassis popup render + volume knob; drop history"
```

---

## Task 10: Extension — options page reskin (TDD)

**Files:**
- Rewrite: `extension/firefox/src/options/options.html`
- Create: `extension/firefox/src/options/options.css`
- Modify: `extension/firefox/src/options/options.js`
- Test: `extension/firefox/test/options.test.js`

- [ ] **Step 1: Confirm the options test surface (no new test needed)**

`test/options.test.js` imports **only** `validateBridgeURL` and `testConnection`
(msw-based) — it has **no** `initOptionsPage`/DOM harness, so do **not** add a DOM/LED
test here (an `initOptionsPage(document)` test would need a harness the suite doesn't have).
The reskin is markup/CSS only; `options.js` logic (validation + `testConnection` + the
existing `setStatus(#status, "ok"/"err")` helper) is unchanged, so these tests keep passing
as-is. This step is a no-op verification:

Run: `cd extension/firefox && npx vitest run test/options.test.js`
Expected: PASS (before and after the markup change, since the imported functions are untouched).

- [ ] **Step 2: Write options.html (chassis markup, external CSS)**

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>MiSTer GroovyRelay Companion — Options</title>
  <link rel="stylesheet" href="options.css">
</head>
<body>
  <div class="panel">
    <div class="status-bar">
      <span class="led aqua on"><span class="light"></span><span class="lbl">Config</span></span>
      <div class="brand-plate"><span class="name">GROOVYRELAY</span></div>
    </div>
    <div class="section-title">Bridge configuration</div>
    <label class="field-label" for="bridge-url">Bridge URL</label>
    <div class="input-frame">
      <input type="text" id="bridge-url" placeholder="http://192.168.1.50:32500" autocomplete="off" spellcheck="false">
    </div>
    <p class="hint">The address of your MiSTer GroovyRelay bridge. Firefox sync carries it across devices.</p>
    <div class="row">
      <button type="button" id="save" class="btn primary">&#9656; Save</button>
      <button type="button" id="test" class="btn">Test connection</button>
    </div>
    <div id="status" class="result" role="status" aria-live="polite"></div>
  </div>
  <script src="../lib/browser-polyfill.js"></script>
  <script type="module" src="options.js"></script>
</body>
</html>
```

- [ ] **Step 3: Write options.css**

Create `extension/firefox/src/options/options.css` reusing the same `@font-face` + `:root` token block as `popup.css` (copy those two blocks verbatim), then:

```css
* { box-sizing: border-box; }
body { margin: 0; padding: 34px 24px; background: #161618; color: #c9c9cf;
  font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; -webkit-font-smoothing: antialiased; }
.panel { max-width: 480px; margin: 0 auto; padding: 18px 20px 20px; border-radius: 5px; position: relative;
  background: linear-gradient(180deg,#2a2a2e,#1f1f23);
  box-shadow: 0 1px 0 rgba(255,255,255,.08) inset, 0 0 0 1px #0a0a0b, 0 24px 60px -20px rgba(0,0,0,.8);
  display: flex; flex-direction: column; gap: 14px; }
.status-bar { display: flex; align-items: center; justify-content: space-between; padding: 2px 2px 11px;
  border-bottom: 1px solid #0a0a0b; box-shadow: 0 1px 0 rgba(255,255,255,.04); }
.led { display: flex; align-items: center; gap: 5px; }
.led .light { width: 7px; height: 7px; border-radius: 50%; background: #2a2a2c; box-shadow: inset 0 1px 1px rgba(0,0,0,.6); }
.led.aqua.on .light { background: oklch(0.90 0.22 175); box-shadow: 0 0 6px oklch(0.90 0.22 175 / .9); }
.led .lbl { font-size: 8px; letter-spacing: .18em; color: #c0c0c4; text-transform: uppercase; }
.brand-plate { padding: 5px 10px; border: 1px solid #0a0a0b; border-radius: 2px;
  background: linear-gradient(180deg,#1a1a1c,#0a0a0b); }
.brand-plate .name { font-size: 13px; font-weight: 900; letter-spacing: .14em; color: #d4d4d8; }
.section-title { font-size: 11px; font-weight: 700; letter-spacing: .24em; text-transform: uppercase;
  color: var(--vfd-dim); text-shadow: 0 0 4px var(--vfd-glow-soft); }
.field-label { font-size: 9px; letter-spacing: .2em; text-transform: uppercase; color: #8a8a92; }
.input-frame { padding: 3px; border: 1px solid #000; border-radius: 3px; background: linear-gradient(180deg,#0a0a0b,#050506);
  box-shadow: inset 0 2px 6px rgba(0,0,0,.8); }
.input-frame input { width: 100%; border: 0; outline: 0; padding: 11px 12px; border-radius: 2px;
  background: radial-gradient(ellipse at center, oklch(0.13 0.03 175), oklch(0.07 0.02 175) 90%);
  color: var(--vfd); font-family: 'DSEG7-Classic', 'Courier New', monospace; font-size: 14px; letter-spacing: .04em;
  text-shadow: 0 0 3px var(--vfd-glow-soft); caret-color: var(--vfd); }
.hint { font-size: 11px; color: #7a7a82; line-height: 1.5; margin: 0; }
.row { display: flex; gap: 9px; }
.btn { flex: 1; height: 38px; border: 1px solid #0a0a0b; border-radius: 2px; cursor: pointer; font-size: 11px;
  letter-spacing: .18em; text-transform: uppercase; color: #c0c0c4;
  background: linear-gradient(180deg,#4a4a50,#2a2a2e 50%,#1a1a1c);
  box-shadow: inset 0 1px 0 rgba(255,255,255,.12), inset 0 -1px 0 rgba(0,0,0,.4), 0 1px 2px rgba(0,0,0,.4); }
.btn.primary { color: var(--amber); }
.result { min-height: 20px; padding: 10px 12px; border: 1px solid #0a0a0b; border-radius: 3px;
  background: linear-gradient(180deg,#0a0a0b,#050506); font-family: 'Courier New', monospace; font-size: 12px;
  letter-spacing: .06em; color: var(--vfd-dim); }
.result.ok { color: var(--ok); text-shadow: 0 0 4px oklch(0.78 0.14 150 / .5); }
.result.err { color: var(--err); }
```

- [ ] **Step 4: Confirm options.js needs no change**

`options.js` already does `const statusEl = doc.getElementById("status")` and
`setStatus(statusEl, "ok"/"err", msg)` toggles the classes. The new `options.html` keeps
`id="status"`, so **no `options.js` change is required**. Verify by reading the file; only
touch it if `#status` was renamed (it wasn't).

- [ ] **Step 5: Run, verify pass**

Run: `cd extension/firefox && npx vitest run test/options.test.js`
Expected: PASS (logic untouched).

- [ ] **Step 6: Commit**

```bash
git add extension/firefox/src/options/ extension/firefox/test/options.test.js
git commit -m "style(ext): reskin options page to receiver chassis"
```

---

## Task 11: Extension — manifest test, version bump, notices, README

**Files:**
- Modify: `extension/firefox/test/manifest.test.js`
- Modify: `extension/firefox/manifest.json` + `extension/firefox/package.json`
- Modify: `THIRD_PARTY_NOTICES.md`, `extension/firefox/README.md`

- [ ] **Step 1: Update the manifest test font assertion**

In `test/manifest.test.js` (~lines 6-9), replace the hardcoded font filenames in the `extensionFiles` list with the new set:

```js
"src/fonts/DSEG14Classic-Regular.woff2",
"src/fonts/DSEG14Classic-Bold.woff2",
"src/fonts/DSEG7Classic-Regular.woff2",
"src/fonts/DSEG7Classic-Bold.woff2",
"src/fonts/Inter-Variable.woff2",
```

Leave the `data_collection_permissions` and `strict_min_version: "140.0"` assertions untouched.

- [ ] **Step 2: Run the manifest test**

Run: `cd extension/firefox && npx vitest run test/manifest.test.js`
Expected: PASS — the test calls `fs.existsSync("src/fonts/<file>")` against the
committed files swapped in Task 6, so no build step is required.

- [ ] **Step 3: Bump versions in lockstep**

Edit `manifest.json` `"version"` and `package.json` `"version"` to the next version (e.g. `0.3.0` — minor bump for the reskin).

Run: `cd extension/firefox && node scripts/check-versions.mjs`
Expected: exits 0 (versions match).

- [ ] **Step 4: Update THIRD_PARTY_NOTICES.md**

In the "Bundled UI Fonts" section, replace the Space Grotesk / Inter Tight / JetBrains Mono text with:

```
The companion extension bundles WOFF2 font assets for DSEG (7- and
14-segment display faces), distributed under the MIT License by Keshikan,
and Inter, distributed under the SIL Open Font License 1.1. The bundled
files are served locally from the extension package; no remote font service
is used at runtime.
```

> Spec §7 also mentions `LICENSES/` and a build.sh license-copy step. **Neither needs a
> change:** `LICENSES/` contains only `README.md` (no per-font files), and `build.sh` has
> no license step (it just zips `src/`). The repo-root `THIRD_PARTY_NOTICES.md` edit above
> is the only notices change.

- [ ] **Step 5: Update README**

In `extension/firefox/README.md`, refresh the "What it does" wording to mention the receiver-styled popup + volume control, and note the popup now matches the bridge's receiver UI. Remove any history-list mention.

- [ ] **Step 6: Run the full extension suite + lint + build**

Run: `cd extension/firefox && npm test && npm run lint && npm run build`
Expected: all pass; build produces the packaged zip/xpi with the new fonts.

- [ ] **Step 7: Commit**

```bash
git add extension/firefox/manifest.json extension/firefox/package.json \
  extension/firefox/test/manifest.test.js THIRD_PARTY_NOTICES.md extension/firefox/README.md
git commit -m "chore(ext): bump version, update font notices + README for reskin"
```

---

## Task 12: Final verification

- [ ] **Step 1: Full Go suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 2: Full extension suite + build**

Run: `cd extension/firefox && npm test && npm run build`
Expected: PASS; packaged artifact emitted.

- [ ] **Step 3: Manual smoke (load unpacked)**

Load `extension/firefox/` unpacked in Firefox and a Chromium browser. Verify: popup renders the chassis look (active VFD + transport + volume knob; idle cast panel + health tiles); turning the knob changes MiSTer output volume via `/ui/companion/volume`; options page renders the chassis form and Test connection lights the result LED.

- [ ] **Step 4: Commit any smoke fixes, then done.**

---

## Self-Review Notes (author)

- **Spec coverage:** §6 volume passthrough → Tasks 1-4; fonts/build → Task 6; popup CSS/HTML/JS → Tasks 7-9; options → Task 10; history removal → Tasks 5,8,9; notices/version/tests → Task 11; behavior preserved (poll, caps, context-menu cast) → unchanged in popup.js (Task 9) and background.js (untouched).
- **Type consistency:** `CompanionVolumeViewer.OutputVolume()` + `CompanionVolumeSaver.SaveOutputVolume(int)` used identically in Tasks 1-4; `bridge.volume(level)` posts `{output_volume}` matching the Task 3 handler; `output_volume` JSON field consistent across status (Task 2) and route (Task 3) and client (Task 5).
- **Known read-this-first spots:** the exact fixtures/helpers in `bridge.test.js`, `popup.test.js`, `options.test.js` are now matched to the live harnesses (Go: `New(Config{Registry:...})` + `companionJSONRequest` + `uiStubAdapter` + `fakeCompanionVolume`; JS: msw `server.use(http.post(...))`; popup: `popupMarkup()` fixture + `initPopup(document)` + `playingStatus()` with an `output_volume` key; options imports only `validateBridgeURL`/`testConnection`).

- **Code-review corrections folded in (2026-05-30):**
  - Task 2/3 Go tests use the real inline `Config` shape (no invented `Bridge`/`Manager`/`cfgForTest`); reuse `companionJSONRequest` + `fakeCompanionVolume`.
  - Task 5 uses the msw `server.use` pattern, not a `lastFetch` capture; no history tests exist in `bridge.test.js` to remove.
  - Task 9 explicitly replaces `showConfigured`/`showUnconfigured` (no `#configured` wrapper anymore) with `setView`, adds the single `#pause-resume` handler + glyph swap, adds `renderLeds` (PWR/LINK/CAST → poll-success/`health.bridge`/session-state), and drops the draggable seek input (progress is display-only).
  - Tick ring is intentionally a **flat bar** (not the receiver's radial 21-`.tick-N`); spec + Task 7 CSS + Task 9 JS all agree.
  - Task 10 adds no phantom `initOptionsPage` DOM test (the suite imports only `validateBridgeURL`/`testConnection`); `options.js` needs no change since `#status` is preserved.
  - Task 11 notes `LICENSES/` + `build.sh` need no font-license change; only repo-root `THIRD_PARTY_NOTICES.md`.
