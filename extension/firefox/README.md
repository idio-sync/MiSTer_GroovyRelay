# MiSTer GroovyRelay Companion (Browser Extension)

WebExtension for the [MiSTer GroovyRelay bridge](../../README.md). Cast pages, links, and videos from your browser to a MiSTer FPGA driving a 15 kHz CRT.

## What it does (v1)

- **Toolbar button:** opens a popup. Click "Cast this tab" to send the active tab's URL to the bridge.
- **Right-click on a link:** "Cast link to MiSTer" sends the link target to the bridge.
- **Right-click on an HTML5 `<video>`:** "Cast video to MiSTer" sends the video's `src`. Blob URLs from MSE-driven players such as YouTube or Twitch cannot be cast this way; use the link context menu on those sites.
- **Options page:** configure the bridge URL once; Firefox sync carries it across devices.

## Install

### Firefox (signed XPI from GitHub Releases)

1. Download `companion-extension-<version>-signed.xpi` from the bridge repo's [Releases page](https://github.com/idio-sync/MiSTer_GroovyRelay/releases).
2. Open Firefox, navigate to `about:addons`.
3. Click the gear icon, then "Install Add-on From File...".
4. Pick the downloaded XPI.
5. Approve the install prompt.

Firefox 140+ is required.

### Chrome / Edge / Brave / Opera / Vivaldi (unpacked dev install)

1. Clone the [bridge repo](https://github.com/idio-sync/MiSTer_GroovyRelay).
2. Open `chrome://extensions` or the equivalent page for your browser.
3. Toggle "Developer mode" on.
4. Click "Load unpacked" and pick `extension/firefox/` from your clone.

Web Store / Edge Add-ons listings are deferred to a later release.

### Safari

Not supported in v1. The MV3 manifest is Safari-compatible in principle, but Safari's signing/notarization pipeline is heavier than the v1 effort budget.

## Configure

After install, the options page should open automatically. If not:

- Firefox: `about:addons` -> MiSTer GroovyRelay Companion -> Preferences.
- Chrome: `chrome://extensions` -> Details -> Extension options.

Enter the bridge URL, for example `http://192.168.1.50:32500`, click "Test Connection" to verify, then "Save."

## Data collection

When you cast a tab, link, or video, the extension sends that URL to the bridge URL you configured. It does not send telemetry or analytics data.

## Develop

```bash
cd extension/firefox
npm ci
npm test
npm run lint
npm run build
```

The bridge-side change required to make this extension work is already in `main`. The extension sends `X-Bridge-Extension: 1` on POSTs; the bridge accepts it together with an `moz-extension://`, `chrome-extension://`, or `safari-web-extension://` Origin.

## Release automation

GitHub Actions signs the Firefox add-on through AMO and uploads the signed XPI when a `v*` tag is pushed.

Repository secrets required:

- `AMO_JWT_ISSUER`
- `AMO_JWT_SECRET`

Project release tags and extension versions are allowed to differ. Before tagging a release, keep the extension `package.json` and `manifest.json` versions in sync with each other. Bump the extension version when you need AMO to sign a new add-on package.

## Spec

Full design at [docs/specs/2026-04-25-companion-extension-design.md](../../docs/specs/2026-04-25-companion-extension-design.md).
