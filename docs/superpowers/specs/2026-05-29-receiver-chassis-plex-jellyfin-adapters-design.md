# Receiver Chassis Plex + Jellyfin Adapter Sections — Phase 4E Design

**Status:** Approved (brainstorm 2026-05-29)
**Spec lineage:** follows [Phase 4D — Adapters Pane (Simple)](2026-05-28-receiver-chassis-adapters-simple-pane-design.md). Implements the two sections 4D rendered as `Spec 4E — implementation in progress` stubs.
**Plan:** _to be written from this spec via the writing-plans skill._

---

## Goal

Replace the two "Spec 4E" stubs in the receiver chassis Settings drawer Adapters pane with real **Plex** and **Jellyfin** sections. Each section renders the adapter's config fields (exactly as DLNA/Torrent do under 4D) **plus** a shared **Account** sub-section that drives the adapter's link/pairing flow — Plex via the plex.tv PIN flow, Jellyfin via username/password — using the chassis-native vanilla-JS + JSON model established by 4A–4D, with **no htmx** in the drawer.

The link cascade is the only genuinely new work. Config-field saves already function through 4D's adapter-agnostic `AdapterSettingsSaver`; this spec adds the link surface and wires the two sections into the render loop.

---

## Context — what the prior phases left

- **4A** shipped the drawer chrome, the `field` template helper, the JSON envelope vocabulary (`{ok:true,scope:…}` / `{ok:false,errors:{…}}` / `{ok:false,chip:…}`), `requireSameOrigin` ([sameorigin.go:12](../../../internal/chassis/sameorigin.go#L12)), and the Bridge save handler.
- **4B** shipped the Pipeline + Advanced panes and field-renderer helpers.
- **4C** shipped the Catalog pane, `CatalogSettingsManager`, the inline two-step confirm, and `ConfigReset`.
- **4D** shipped the Adapters pane for the three adapters with **no link-state cascade** (DLNA, Torrent, Streams): the per-adapter section templates, `*uiserver.AdapterSaver.SaveTouched`, the chassis-owned `AdapterSettingsSaver` + `StreamsRefresher` interfaces, and the `streams-refresh` **action** pattern (button → `data-settings-action` → `fetch` POST → JSON → result span, single-flight, chassis-owned interface bound in `cmd/`). 4D left Plex and Jellyfin as stubs in [settings-adapters.html](../../../internal/chassis/templates/settings-adapters.html).

**4D facts 4E rides directly:**

| Symbol | Location | 4E use |
|---|---|---|
| `bridgeAdapterSettingsSaver` (adapter-agnostic by-name lookup) | [cmd/.../adapter_settings_saver.go:26-88](../../../cmd/mister-groovy-relay/adapter_settings_saver.go#L26-L88) | Plex/Jellyfin **config saves work unchanged** — both are registered, implement `CurrentValues()`/`Fields()`/`Validate()`. `projectWritableSurface` returns their fields as-is (only `streams` is special-cased). |
| `buildSettingsData` render loop `{"dlna","torrent","streams"}` | [data.go:511](../../../internal/chassis/data.go#L511) | **Must add `plex`, `jellyfin`.** |
| `AdapterPaneData` | [data.go:299](../../../internal/chassis/data.go#L299) | **Extended** with `Linkable bool` + `LinkView`. |
| `StreamsRefresher` action precedent | [settings.go:117-135](../../../internal/chassis/settings.go#L117-L135), [server.go:284](../../../internal/chassis/server.go#L284) | Template for the chassis-owned `AdapterLinker` interface + route mounting + `cmd/` binding. |
| `adapter-field-row` / `field` helper | [settings-adapter-dlna.html:14-28](../../../internal/chassis/templates/settings-adapter-dlna.html#L14-L28) | Renders every config field including `enabled` (as a row, **not** a header toggle). |

---

## Non-Goals

- **Adapter behavior changes.** 4E adds an additive, transport-agnostic link API to the Plex/Jellyfin adapters (see §Adapter-side API) but changes no existing link semantics. The legacy `/ui/adapter/*` link routes and HTML handlers keep working, rewired to delegate to the new core.
- **Legacy `/ui` cutover.** Retiring `/ui/adapter/*` happens **after 4F**, per the 4D design (§Goal 8). 4E adds the chassis-native surface alongside the legacy one.
- **URL adapter.** Stays the "Spec 4F — implementation in progress" stub.
- **Config-field schema changes.** Plex/Jellyfin `Fields()` are consumed as-is. No new fields, no scope re-tiering.
- **New credential mechanisms.** Plex stays plex.tv PIN; Jellyfin stays username/password. No API-key or Quick-Connect path.

---

## Goals (testable)

1. **Plex section renders** in mockup order (near the top of the pane) with: an Account sub-section on top, then the config field block (`enabled`, `device_name`, `profile_name`, `server_url`, `max_video_bitrate_kbps`) via 4D's `adapter-field-row`, with correct scope chips (HOT / REBOOT / RECAST / RECAST / RECAST per [plex/adapter.go:328-382](../../../internal/adapters/plex/adapter.go#L328-L382)).
2. **Jellyfin section renders** lower in the pane with the same structure: Account sub-section on top, then `enabled`, `server_url`, `device_name`, `max_video_bitrate_kbps` (HOT / REBOOT / HOT / RECAST per [jellyfin/adapter.go:222-259](../../../internal/adapters/jellyfin/adapter.go#L222-L259)).
3. **Both stubs are deleted** from [settings-adapters.html](../../../internal/chassis/templates/settings-adapters.html).
4. **Shared link sub-section.** Both sections render the Account sub-section through one template (`settings-link.html`) that branches on `Kind` (`pin`|`credential`) and `Phase` (`unlinked`|`pending`|`linked`|`error`). Structure, chrome, the collapsed `linked` one-liner, and the error treatment are identical between the two adapters; only the expanded-unlinked body differs (PIN button vs credential form) — the one intrinsic divergence.
5. **Plex PIN flow works end-to-end in the drawer:** click → PIN + countdown rendered → JS polls every 2s → transitions to `linked` (or `error` on expiry) → collapses to `✓ Linked as <user>` with Unlink.
6. **Jellyfin credential flow works end-to-end in the drawer:** form submit → synchronous `linked` (or `error` with the message re-rendered on the form). When no `server_url` is configured, the sub-section shows the "set a Server URL first" hint.
7. **Unlink works** for both: revokes/logs out best-effort, clears the token, repaints to `unlinked`.
8. **Config saves unchanged.** Editing any Plex/Jellyfin config field saves through 4D's `SaveTouched` with the correct wire scope and toast.
9. **Chassis isolation preserved.** `internal/chassis` imports no adapter package; `TestProductionImports_NoCrossPackageCoupling` ([import_check_test.go:87](../../../internal/chassis/import_check_test.go#L87)) stays green. The `AdapterLinker` production binding lives in `cmd/`.
10. **`/ui/*` unchanged.** Legacy link routes still function.

---

## Architecture

Three layers, mirroring the 4D `StreamsRefresher` action precedent:

```
 Browser (vanilla JS)                 internal/chassis                     cmd/mister-groovy-relay        internal/adapters/{plex,jellyfin}
 ─────────────────────                ─────────────────                    ──────────────────────        ────────────────────────────────
 settings-link.html  ── fetch ──▶  POST .../link/start   ─┐
 settings-drawer.js  (poll loop)   GET  .../link/status   ├─▶ AdapterLinker ──▶ adapterLinker binding ──▶ adapter.LinkController
                     ◀── JSON ───   POST .../link/unlink  ─┘   (interface)        (maps Snapshot→View,        (Start/Poll/Unlink/
 renders LinkView                                              + single-flight     dispatch by name)            Snapshot)
                                                               gate)
```

- **chassis** owns the `AdapterLinker` interface and the `LinkView` wire shape; it never imports an adapter package.
- **cmd/** binds `AdapterLinker` to the concrete adapters, translating each adapter's `LinkSnapshot` into the chassis `LinkView` and dispatching by adapter name.
- **adapters** expose a transport-agnostic `LinkController` (additive); the existing `/ui` HTML handlers are rewired to call it so there is exactly one orchestration of the PIN poll / credential auth.

### Why the adapter-side API is required (and why it's additive)

The chassis-initiated link **must** drive the adapter's own state machine: Plex's pending-PIN state (`a.pending`, the `pollPendingLink` goroutine) backs `LinkPhase()` ([plex/adapter.go:428-448](../../../internal/adapters/plex/adapter.go#L428-L448)) and the `adapter-linked` / `adapter-link-failed` event emissions; Jellyfin's `link.SetLinked/SetIdle` ([jellyfin/link_state.go](../../../internal/adapters/jellyfin/link_state.go)) drives its phase + eventlog. Two non-options force the extraction: (a) the `cmd/` binding **can't just call the existing handlers** — they return HTML fragments for htmx, while the drawer needs `LinkView` JSON; (b) it **can't re-implement** the PIN-poll loop / auth sequence either — a second copy of the state machine would diverge from `a.pending` / `LinkState`, breaking `LinkPhase()` and the `adapter-linked` / `adapter-link-failed` emissions. So the orchestration that lives inside the legacy handlers ([plex/link_ui.go](../../../internal/adapters/plex/link_ui.go), [jellyfin/link_ui.go](../../../internal/adapters/jellyfin/link_ui.go)) is **extracted** into exported, transport-agnostic methods. The HTML handlers then delegate to the same core. No existing behavior changes; the extraction is removed/simplified when `/ui` retires after 4F.

---

## Adapter-side API (additive)

Each linkable adapter exposes a small controller. Exact method placement is an implementation detail; the contract is:

```go
// LinkController is the transport-agnostic link orchestration a linkable
// adapter exposes. Both the legacy /ui HTML handlers and the chassis
// JSON binding call it, so the adapter's own link-state machine and
// event emissions stay authoritative regardless of which UI initiated.
type LinkController interface {
    // Snapshot returns the current link state without side effects.
    Snapshot() LinkSnapshot
    // Start begins pairing. Plex: params empty; returns a snapshot with
    // Phase=pending + Code + ExpiresInSec, and arms the poll goroutine.
    // Jellyfin: params{"username","password"}; returns a terminal
    // snapshot (Phase=linked or error) synchronously.
    Start(params map[string]string) (LinkSnapshot, error)
    // Poll advances/reads the pending flow. Plex: reads pending state,
    // returning pending|linked|error. Jellyfin: returns Snapshot().
    Poll() (LinkSnapshot, error)
    // Unlink revokes (Plex RevokeDevice) / logs out (Jellyfin Logout)
    // best-effort, clears the token, returns Phase=unlinked. Idempotent.
    Unlink() (LinkSnapshot, error)
}

type LinkSnapshot struct {
    Phase          string // "unlinked" | "pending" | "linked" | "error"
    LinkedAs       string // Plex: username; Jellyfin: "<user> on <serverID>"
    Code           string // Plex pending only
    ExpiresInSec   int    // Plex pending only
    NeedsServerURL bool   // Jellyfin only: true when server_url is empty
    Error          string // human-readable, Phase=error only
}
```

Native-phase → snapshot mapping:

| Adapter | Native phase | Source | `LinkSnapshot.Phase` |
|---|---|---|---|
| Plex | `idle` | `LinkPhase()` ([plex/adapter.go:434](../../../internal/adapters/plex/adapter.go#L434)) | `unlinked` |
| Plex | `pin-issued` | `LinkPhase()` (pending, not expired) | `pending` (+ Code, ExpiresInSec) |
| Plex | `linked` | token present | `linked` (+ LinkedAs) |
| Plex | `error` | expired / failed | `error` |
| Jellyfin | `LinkIdle` | `link.Phase()` ([jellyfin/link_state.go:11-18](../../../internal/adapters/jellyfin/link_state.go#L11-L18)) | `unlinked` (+ NeedsServerURL if `server_url==""`) |
| Jellyfin | `LinkLinking` | in-flight auth | `pending` |
| Jellyfin | `LinkLinked` | token present | `linked` (+ LinkedAs `"<user> on <serverID>"`) |
| Jellyfin | `LinkError` | last attempt failed | `error` (+ Error from `LastError()`) |

Reused primitives (already present): Plex `RequestPIN`/`PollPIN`/`RevokeDevice` ([plex/linking.go](../../../internal/adapters/plex/linking.go)), token store ([plex/tokenstore.go](../../../internal/adapters/plex/tokenstore.go)); Jellyfin `AuthenticateByName`/`Logout` ([jellyfin/linking.go](../../../internal/adapters/jellyfin/linking.go)), token store ([jellyfin/tokenstore.go](../../../internal/adapters/jellyfin/tokenstore.go)).

---

## Chassis-owned contract

In [internal/chassis/settings.go](../../../internal/chassis/settings.go) (mirrors the `StreamsRefresher` block):

```go
// AdapterLinker is the chassis-side interface backing the per-adapter
// /receiver/settings/adapter/{name}/link/* routes. Production binding
// (cmd/mister-groovy-relay) wraps the concrete Plex/Jellyfin adapters'
// LinkController and maps each adapter's snapshot onto LinkView. The
// chassis imports no adapter package.
type AdapterLinker interface {
    // LinkView returns the current render state for an adapter's Account
    // sub-section, or ok=false if the named adapter is not linkable
    // (dlna/torrent/streams/url) or unknown.
    LinkView(name string) (LinkView, bool)
    // StartLink begins pairing. PIN adapters (plex) ignore params and
    // return a pending view. Credential adapters (jellyfin) read
    // params["username"]/["password"] and return a terminal view.
    StartLink(name string, params map[string]string) (LinkView, error)
    // LinkStatus polls progress (PIN adapters). Credential adapters
    // return the current view unchanged.
    LinkStatus(name string) (LinkView, error)
    // Unlink revokes/logs out and clears the token. Idempotent.
    Unlink(name string) (LinkView, error)
}

// LinkView is the wire/render shape for the Account sub-section.
type LinkView struct {
    Kind           string      // "pin" | "credential"
    Phase          string      // "unlinked" | "pending" | "linked" | "error"
    LinkedAs       string      // linked phase
    Code           string      // pin/pending
    ExpiresInSec   int         // pin/pending
    NeedsServerURL bool        // credential adapter with empty server_url
    Error          string      // error phase
    Fields         []LinkField // credential inputs to render (keeps the chassis ignorant of which credentials)
}

type LinkField struct {
    Key   string // form field name, e.g. "username"
    Label string // "Username"
    Kind  string // "text" | "secret"
}
```

**`Fields` is non-empty only for credential adapters.** PIN adapters (Plex) return an empty slice and render a single action button. Jellyfin returns exactly two entries — `{Key:"username", Label:"Username", Kind:"text"}` and `{Key:"password", Label:"Password", Kind:"secret"}`.

`Config.AdapterLinker AdapterLinker` is added in [server.go](../../../internal/chassis/server.go); when nil, the handlers return `{ok:false, chip:"NOT READY"}` (same shape as `StreamsRefresher` nil — see [settings_test.go:1107](../../../internal/chassis/settings_test.go#L1107)).

---

## Wire contract

All three routes mount behind `requireSameOrigin` next to the 4D adapter save route:

| Method · Path | Body | Success | Failure |
|---|---|---|---|
| `POST /receiver/settings/adapter/{name}/link/start` | form-encoded link fields (credential) or empty (PIN) | `200 {ok:true, view:{…}}` | `400 {ok:false, error:"<msg>"}` (missing/blank required field) · `{ok:false, chip:"NOT READY"}` (linker nil — same status as the `StreamsRefresher`-nil response, [settings_test.go:1107](../../../internal/chassis/settings_test.go#L1107)) · `404 {ok:false, chip:"UNKNOWN ADAPTER"}` |
| `GET  /receiver/settings/adapter/{name}/link/status` | — | `200 {ok:true, view:{…}}` | `{ok:false, chip:"NOT READY"}` · `404` |
| `POST /receiver/settings/adapter/{name}/link/unlink` | — | `200 {ok:true, view:{…}}` | `{ok:false, error:"<msg>"}` · `{ok:false, chip:"NOT READY"}` · `404` |

Status-code discipline matches the 4D handlers: same-origin failures and method mismatches are handled by the shared middleware/mux; `400` for caller input errors; chip errors carry their `StatusCode()`. The `NOT READY` chip (linker nil) returns **503 Service Unavailable** on all three routes, matching the `StreamsRefresher` precedent.

**Concurrency / single-flight.** `start` holds a per-adapter `sync.Mutex` for the whole operation (Plex `RequestPIN`; Jellyfin auth). Starting a link while one is already pending **abandons and replaces** the prior pending flow — matching the existing Plex `handleLinkStart`, which serializes on `linkStartMu` and drops any prior pending PIN. The JS additionally disables the trigger while a `start`/`unlink` request is in flight, so the replace path is normally reachable only across separate drawer sessions.

**Link failure is a state, not an HTTP error.** "Code expired" (Plex) and "invalid credentials" (Jellyfin) return **`200` with `view.Phase="error"`** and a populated `view.Error`, so the shared renderer simply repaints into the error state. `400` is reserved for malformed requests (e.g. a credential `start` with a blank username before it ever reaches the adapter).

Route shape: explicit `POST`/`GET` verbs on `…/link/{start,status,unlink}` (not REST `DELETE`), matching the readable action style of `…/action/streams-refresh` and keeping every chassis mutation a POST.

---

## Rendering (placement C — Account on top, collapses when linked)

- **Container:** [settings-adapters.html](../../../internal/chassis/templates/settings-adapters.html) replaces the Plex stub with `{{ template "settings-adapter-plex" (adapterPane .Adapters "plex") }}` and the Jellyfin stub with `{{ template "settings-adapter-jellyfin" (adapterPane .Adapters "jellyfin") }}`, preserving 4D's mockup order (Plex near the top, Jellyfin lower).
- **New section templates** `settings-adapter-plex.html` / `settings-adapter-jellyfin.html`: each emits `<section class="settings-section">` → `<h4>` → `{{ template "settings-link" .LinkView }}` (Account sub-section, on top) → the config field block (`{{ range .Fields }}{{ template "adapter-field-row" … }}`). `enabled` renders as a field-row exactly like DLNA — **no header toggle**.
- **Shared `settings-link.html`:** branches on `.Kind` and `.Phase`:
  - `linked` → collapsed one-liner: `✓ Linked as {{.LinkedAs}}` + Unlink button.
  - `unlinked` + `pin` → "OFF · not linked" + help + **Link Plex Account** button.
  - `unlinked` + `credential` + `NeedsServerURL` → "set a Server URL below (it saves automatically), then link" hint.
  - `unlinked` + `credential` (URL present) → render `.Fields` inputs (username/password) + **Link ▸** submit.
  - `pending` + `pin` → PIN code + countdown + "waiting for plex.tv…".
  - `pending` + `credential` → "↻ Linking…".
  - `error` → badge + `.Error`; PIN shows **Try Again**, credential re-renders the form with the error.
- **Initial state is server-rendered.** `buildSettingsData` gains an `AdapterLinker` parameter (mirroring how 4C extended the signature with `catalogManager`) and calls `LinkView(name)` for linkable adapters at drawer-paint time, baking the result into `AdapterPaneData.LinkView`, so opening the drawer needs no fetch. The `status` GET exists only for the Plex poll.
- **Collapse-when-linked** is CSS/JS driven off `Phase`; `pending`/`error`/`unlinked` are expanded.
- **CSS** (chassis.css): PIN typography (DSEG-ish monospace, VFD glow), countdown, "waiting" amber, the collapsed linked one-liner. Reuses `.settings-subhead`, `.action-result`, `.settings-notice` from 4B–4D.

---

## JS — link handlers + PIN poll controller

In [settings-drawer.js](../../../internal/chassis/static/settings-drawer.js), alongside the `streams-refresh` handler:

- **Start (delegated click/submit):**
  - PIN (`data-link-action="start"` button): POST `…/link/start` → render returned `view`; if `view.Phase==="pending"`, start the poll controller.
  - Credential (form submit): disable submit, show optimistic "Linking…", POST `…/link/start` with the form body → render returned terminal `view`.
- **Poll controller (new surface — the drawer had no poll loop):**
  - One controller **per adapter**; single-flight (guard against duplicate starts; a second start cancels/replaces).
  - `setTimeout` cadence 2s (not `setInterval`, so a slow response can't stack requests); each tick GETs `…/link/status` and repaints.
  - **Stops**, in priority order: (1) `view.Phase ∈ {linked, error}`, (2) `ExpiresInSec ≤ 0`, (3) Unlink, (4) drawer close / Adapters-pane switch (so a hidden drawer isn't polling). The first condition that holds wins.
  - The PIN countdown is re-rendered from each poll response; the server's expiry is authoritative, so a slow tick may make the displayed countdown lag slightly — acceptable.
  - Network error on a tick: surface transient text, keep the loop until expiry (don't kill on one failed poll).
- **Unlink (delegated click):** POST `…/link/unlink` → repaint to `unlinked`; tear down any poll controller.
- Reuses `showNotice` for `chip` errors and the `action-result` slot conventions from 4D.

---

## `server_url` → re-link cascade (documented; no special drawer wiring)

Jellyfin's `server_url` is an ordinary config field saved through 4D's `SaveTouched`. Its `ApplyConfig` scope is **`ScopeRestartBridge`** (REBOOT) ([jellyfin/adapter.go:448-450](../../../internal/adapters/jellyfin/adapter.go#L448-L450)) — the live token is **not** wiped on save. The drawer therefore shows the standard REBOOT toast ("Restart container to apply new Server URL"). The token reconcile happens at the next **`Start()`** ([jellyfin/adapter.go:352-358](../../../internal/adapters/jellyfin/adapter.go#L352-L358)): if `cfg.ServerURL` has drifted from `token.ServerURL`, the adapter wipes the token and `SetIdle`. After the operator restarts, the server-rendered `LinkView` shows `unlinked` and they re-link. **The drawer adds nothing for this case** — recorded here so the implementation does not double-wire it. (Edge: *clearing* `server_url` to empty while linked does not wipe the token — the reconcile guard is `cfg.ServerURL != "" && tok.ServerURL != ""` — but Jellyfin's `Validate` already rejects an empty `server_url` when `enabled=true`, so the drawer surfaces it as a field error at save time rather than a silent broken state.)

---

## ApplyScope positioning

Link/unlink are **actions**, out-of-band of the field-save scope vocabulary (like `streams-refresh`). Their JSON envelopes carry `view`, never `scope`, and they emit no scope toast. Their runtime effects are immediate and owned by the adapters: Plex toggles its plex.tv registration loop and emits `adapter-linked`/`adapter-link-failed`; Jellyfin performs `Stop()`→`Start()` to pick up the new token. Config-field saves continue to use the scope vocabulary unchanged.

---

## File structure

**Modified — chassis:**
- `internal/chassis/settings.go` — `AdapterLinker` interface, `LinkView`/`LinkField` structs, three handlers, per-adapter `start` single-flight gate.
- `internal/chassis/server.go` — `Config.AdapterLinker`; mount the 3 routes behind `requireSameOrigin`.
- `internal/chassis/data.go` — add `plex`/`jellyfin` to the render loop ([:511](../../../internal/chassis/data.go#L511)); extend `AdapterPaneData` with `Linkable`/`LinkView`; populate `LinkView` for linkable adapters.
- `internal/chassis/templates/settings-adapters.html` — swap both stubs for the real section templates.
- `internal/chassis/static/settings-drawer.js` — link handlers + poll controller.
- `internal/chassis/static/chassis.css` — PIN/countdown/linked-collapse styles.
- `internal/chassis/settings_test.go`, `chassis_test.go`, `data_test.go` — handler/render/data tests.

**Created — chassis:**
- `internal/chassis/templates/settings-adapter-plex.html`
- `internal/chassis/templates/settings-adapter-jellyfin.html`
- `internal/chassis/templates/settings-link.html` (shared Account sub-section)

**Modified — adapters (additive):**
- `internal/adapters/plex/` — extract PIN orchestration into the `LinkController` core; rewire `link_ui.go` handlers to delegate.
- `internal/adapters/jellyfin/` — extract credential orchestration into the `LinkController` core; rewire `link_ui.go` handlers to delegate.
- Co-located `_test.go` additions for the extracted core.

**Created — cmd:**
- `cmd/mister-groovy-relay/adapter_linker.go` — production `AdapterLinker` binding (snapshot→view mapping, dispatch by name, `Fields` for the credential adapter), wired into `chassis.Config`.
- `cmd/mister-groovy-relay/adapter_linker_test.go` — wrapper-level mapping/dispatch coverage.
- `cmd/mister-groovy-relay/adapter_linker_e2e_test.go` — integration-tag end-to-end for at least one flow (Jellyfin credential is synchronous and easiest to assert deterministically).

**Unchanged:** `internal/uiserver/*` (the 4D saver already serves Plex/Jellyfin config saves), `internal/core/*`, the URL adapter, `internal/chassis/import_check_test.go` (entries unchanged — chassis still imports no adapter package).

---

## Testing surface (mirrors 4D)

- **Chassis handler tests:** `start`/`status`/`unlink` × {PIN (plex), credential (jellyfin)} × {success, link-failure→`view.Phase=error`, bad input→`400`, linker-nil→`NOT READY`, unknown-adapter→`404`}.
- **Template render tests:** the nine render states — `(pin, unlinked|pending|linked|error)`, `(credential, unlinked|pending|linked|error)`, and `(credential, NeedsServerURL)` — i.e. the approved state gallery; assert the collapsed `linked` one-liner is structurally identical across both adapters; assert `enabled` renders as a field-row (no header toggle); assert section order (Plex above Jellyfin).
- **JS behavior test** (the `testdata/*.behavior.test.js` pattern, cf. [source-cluster.behavior.test.js](../../../internal/chassis/testdata/source-cluster.behavior.test.js)): poll controller single-flight, stop-on-terminal, stop-on-pane-close, countdown decrement, no request stacking.
- **Adapter core tests:** the extracted `LinkController` for each adapter, plus a regression assert that the legacy `/ui` handlers still produce their prior HTML by delegating to the core.
- **cmd/ wrapper + e2e:** snapshot→view mapping; one integration-tag end-to-end save+link.
- **Isolation:** `TestProductionImports_NoCrossPackageCoupling` stays green.

---

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| PIN polling is a new JS surface (no prior poll loop in the drawer). | `setTimeout` (not `setInterval`), single-flight per adapter, hard stop on terminal phase / expiry / pane-close. Bounded by the 15-min PIN expiry. Covered by a dedicated behavior test. |
| Extracting the link core could regress the legacy `/ui` flow. | The extraction is additive; handlers delegate to the same core; a regression test asserts the legacy HTML is unchanged. Both surfaces share one orchestration, so they cannot diverge. |
| Jellyfin `server_url` re-link cascade misunderstood and double-wired. | Documented above as a no-op for the drawer (standard REBOOT toast + post-restart server-rendered state). |
| Stub sections become dead code. | Deleted in this phase (Goal 3); 5-line blocks. |
| `LinkView.Fields` over-engineering (only one credential adapter). | Kept minimal (two entries for Jellyfin); preserves chassis adapter-agnosticism without a registry — cheaper than baking Jellyfin's credentials into a chassis template. |

---

## Decisions (resolved during brainstorming)

| Decision | Choice | Rationale |
|---|---|---|
| Scope | Both Plex + Jellyfin, one unified spec | Roadmap-faithful; one shared link abstraction avoids building the plumbing twice. |
| Where the link flow lives | **Chassis-native JSON** (Approach A) | Only option consistent with the chassis-isolation contract **and** the post-4F `/ui` cutover; reuses the `StreamsRefresher` action precedent. Embedding htmx (B) or deep-linking to `/ui` (C) both create debt 4F must repay. |
| Account sub-section placement | **C — on top, collapses when linked** | Maximizes Plex↔Jellyfin visual consistency: the collapsed `linked` state is identical; only the expanded-unlinked body differs (intrinsic to each service). |
| Link failure transport | `200` + `view.Phase=error` | Link failure is a state the shared renderer repaints, not an HTTP error. |
| `enabled` rendering | Field-row (like DLNA), not a header toggle | Visual consistency with 4D and across both new sections. |
| Adapter-side change | Thin additive `LinkController`; legacy handlers delegate | Chassis-initiated link must drive the adapter's own state machine (phase + events); a re-implementation would diverge. Additive, reversible at 4F. |
