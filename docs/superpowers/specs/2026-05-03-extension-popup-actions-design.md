# Extension Popup Actions

Date: 2026-05-03
Status: approved for implementation

## Problem

The browser extension popup can cast the active tab, but two common operator actions still require switching to the bridge web UI:

- Launch the GroovyMiSTer core over SSH.
- Open the bridge web UI.

The bridge already exposes the launch action at `POST /ui/bridge/mister/launch`, and the web UI already uses that route. The extension should reuse it instead of duplicating SSH behavior.

## Goals

- Show the configured popup actions whenever a bridge URL is saved:
  - `Cast this tab`
  - `Launch GroovyMiSTer`
  - `Open Web UI`
- Keep `Launch GroovyMiSTer` visible even when the active tab cannot be cast.
- Open the configured bridge URL in a browser tab without checking bridge health first.
- Surface launch success and failure inline in the popup.

## Non-Goals

- No new bridge-side launch endpoint.
- No SSH configuration in the extension.
- No health-check gating before showing popup actions.
- No playback controls beyond the existing cast action.

## Design

Add `launchGroovyMister()` to `extension/firefox/src/lib/bridge.js`. It reads the stored bridge URL, POSTs `/ui/bridge/mister/launch`, sends `X-Bridge-Extension: 1`, and returns the same `{ ok, error }` style used by `play()`. The bridge route returns an HTML fragment, so the helper treats any 2xx as success and reads non-2xx text as the error body.

Update the configured popup state to include two buttons below `Cast this tab`:

- `Launch GroovyMiSTer`: calls `launchGroovyMister()`, disables itself while the request is in flight, and writes success/error into a launch status line.
- `Open Web UI`: calls `browser.tabs.create({ url: bridgeURL })`. It does not call the bridge and does not need a status line unless the browser API rejects.

The existing `Cast this tab` button remains disabled for non-http(s) active tabs. The new launch and open buttons remain enabled as long as `bridgeURL` is configured.

## Error Handling

- Missing bridge URL: helper returns `Bridge not configured`.
- Launch timeout: helper returns `Bridge timed out`.
- Network error: helper returns `Bridge unreachable: <message>`.
- HTTP error: helper returns `Launch failed: <response text or HTTP status>`.
- Browser tab-open failure: popup displays the browser API error inline.

## Tests

- `bridge.test.js`: cover launch POST path, headers, success, timeout, network error, and HTTP error.
- `popup.test.js`: cover that configured popup renders the launch/open buttons, launch remains enabled for non-http(s) tabs, launch click calls the helper and updates status, and open click uses the stored bridge URL.

## Security

Launch uses the same trusted-LAN posture as the web UI action. The extension sends the existing `X-Bridge-Extension: 1` signal, and the bridge-side CORS/CSRF handling remains responsible for accepting extension-origin requests.
