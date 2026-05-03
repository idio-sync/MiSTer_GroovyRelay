# Extension Popup Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add always-visible `Launch GroovyMiSTer` and `Open Web UI` actions to the configured browser-extension popup.

**Architecture:** Reuse the bridge's existing `POST /ui/bridge/mister/launch` endpoint through a new extension helper. Keep popup UI behavior local: launch performs a POST and reports status; open web UI calls the browser tabs API with the saved bridge URL.

**Tech Stack:** WebExtension MV3, browser-polyfill, plain JavaScript modules, Vitest + MSW.

---

### Task 1: Launch Helper

**Files:**
- Modify: `extension/firefox/src/lib/bridge.js`
- Test: `extension/firefox/test/bridge.test.js`

- [ ] **Step 1: Write failing tests for launch helper**

Add tests that import `launchGroovyMister`, set `bridgeURL`, and assert:

```js
const result = await launchGroovyMister();

expect(result).toEqual({ ok: true });
expect(captured.headers["x-bridge-extension"]).toBe("1");
```

Also add tests for empty config, timeout, network error, and HTTP 500 text response.

- [ ] **Step 2: Run tests to verify failure**

Run: `cmd.exe /c npx vitest run test/bridge.test.js`

Expected: FAIL because `launchGroovyMister` is not exported.

- [ ] **Step 3: Implement launch helper**

Add `launchGroovyMister()` beside `play()`. It should:

```js
const bridgeURL = await getBridgeURL();
if (!bridgeURL) return { ok: false, error: "Bridge not configured" };
```

Then POST `${bridgeURL}/ui/bridge/mister/launch` with `X-Bridge-Extension: 1`, use the existing timeout machinery, return `{ ok: true }` for 2xx, and return `{ ok: false, status, error }` for non-2xx.

- [ ] **Step 4: Run launch helper tests**

Run: `cmd.exe /c npx vitest run test/bridge.test.js`

Expected: PASS.

### Task 2: Popup Buttons

**Files:**
- Modify: `extension/firefox/src/popup/popup.html`
- Modify: `extension/firefox/src/popup/popup.js`
- Modify: `extension/firefox/src/popup/popup.css`
- Test: `extension/firefox/test/popup.test.js`

- [ ] **Step 1: Write failing popup tests**

Add tests that construct the popup DOM, set a configured `bridgeURL`, call `initPopup(doc)`, and assert:

```js
expect(doc.getElementById("launch-groovy")).not.toBeNull();
expect(doc.getElementById("open-webui")).not.toBeNull();
```

Add tests that click `open-webui` and assert `browser.tabs.create` receives `{ url: "http://192.168.1.50:32500" }`. Add a non-http(s) active-tab test proving `cast` is disabled while `launch-groovy` and `open-webui` remain enabled.

- [ ] **Step 2: Run popup tests to verify failure**

Run: `cmd.exe /c npx vitest run test/popup.test.js`

Expected: FAIL because the buttons do not exist.

- [ ] **Step 3: Add popup UI and handlers**

Add buttons with ids `launch-groovy` and `open-webui`. Import `launchGroovyMister` in `popup.js`, wire click handlers, and add a `launch-status` status element. `open-webui` should call:

```js
await browser.tabs.create({ url: bridgeURL });
```

- [ ] **Step 4: Run popup tests**

Run: `cmd.exe /c npx vitest run test/popup.test.js`

Expected: PASS.

### Task 3: Documentation and Verification

**Files:**
- Modify: `extension/firefox/README.md`

- [ ] **Step 1: Update README feature list**

Mention that the toolbar popup can launch GroovyMiSTer over SSH and open the bridge web UI.

- [ ] **Step 2: Run relevant checks**

Run:

```bash
cmd.exe /c npx vitest run
cmd.exe /c C:/Users/Jake/sdk/go/bin/go.exe test ./internal/ui -count=1
```

Expected: both pass.
