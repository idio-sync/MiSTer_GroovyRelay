# Receiver Chassis Settings Drawer — Phase 4A Design

**Status:** Brainstormed; awaiting implementation plan.

**Scope:** First sub-spec of Phase 4 (Settings & Adapters). Ships a working settings drawer reachable from the chassis transport row's gear button, with the Network pane fully functional end-to-end (9 bridge fields auto-saving on blur through the existing `uiserver.BridgeSaver`, one action button for MiSTer connectivity probe) and the remaining four panes (Pipeline, Adapters, Catalog, Advanced) rendered as visible-but-pending placeholders so the drawer chrome is complete. 4A establishes the field-renderer template helper, the auto-save-on-blur interaction model, the 4-tier scope-badge taxonomy, the JSON error envelope, and the same-origin / CSRF posture that every later Phase 4 sub-spec (4B–4F) reuses without modification.

**Repo location:** Committed under `docs/superpowers/specs/`. That directory is normally gitignored; this spec is force-added per the receiver chassis rollout convention.

## Context

Phase 3A shipped the input row + bundled preset bank ([2026-05-25-receiver-chassis-input-and-presets-design.md](2026-05-25-receiver-chassis-input-and-presets-design.md)). Phase 3B shipped source-cluster lamps, the Streams catalog drawer, user-curated preset editing, and search-filter ([2026-05-25-receiver-chassis-source-cluster-and-catalog-design.md](2026-05-25-receiver-chassis-source-cluster-and-catalog-design.md)). With 3B merged the chassis can drive a cast end-to-end; what it cannot yet do is **configure the bridge**. Operators still need to leave `/receiver/*` and open the legacy `/ui/*` to change MiSTer host, ports, paths, or any adapter setting.

Phase 4 per the foundation spec ([2026-05-21-receiver-chassis-foundation-design.md:17](2026-05-21-receiver-chassis-foundation-design.md)) is **"Settings & adapters — bridge settings + adapter forms ported to chassis style."** It is the last meaningful surface before the final cutover that retires `/ui/*`.

Phase 4 is decomposed into six sub-specs because the mockup's settings surface is roughly 3–5× the size of 3B's and contains intricate per-adapter UIs (Plex PIN flow, Jellyfin sync auth, URL adapter's yt-dlp host tag list and cookies textarea) that each warrant their own focused brainstorm:

- **4A (this spec)** — Settings drawer plumbing + Network pane (vertical slice; establishes every pattern for 4B–4F).
- 4B — Pipeline pane + Advanced pane (more generic fields; one new action button "Launch core").
- 4C — Catalog pane (provider toggles + custom-manifest add + per-provider HLS override + the deferred "restore-defaults" preset button).
- 4D — Adapters pane: simple cases (DLNA, Streams, Torrent — no link state).
- 4E — Adapters pane: link cascades (Plex 4-state PIN flow, Jellyfin 2-state sync auth).
- 4F — Adapters pane: URL adapter custom widgets (yt-dlp host tag list, cookies textarea).

The previous Phase 3D framing ("slot rename, restore-defaults, settings-drawer integration") is retired: slot rename is dropped, settings-drawer becomes its own Phase 4, restore-defaults folds into Phase 4C (the Catalog pane).

**Mockup reference:** [`docs/superpowers/reference/2026-05-21-receiver-v24.html`](../reference/2026-05-21-receiver-v24.html). This spec cites mockup line numbers throughout.

**Existing infrastructure to reuse, not rebuild:**
- [`internal/uiserver/bridge_saver.go`](../../../internal/uiserver/bridge_saver.go) — `BridgeSaver` already does diff-vs-current, preflight (TCP/UDP bind checks and data-dir writability checks for restart-bridge changes), atomic write, current-config snapshots, and per-scope runtime dispatch (HotSwap / NextCast / RestartCast / RestartBridge). 4A wires the same saver instance into chassis through a chassis-owned interface so `internal/chassis` does **not** import `internal/uiserver`; the only saver-layer change is an additive typed-error wrapper so chassis can preserve status/chip details without string parsing.
- [`internal/uiserver/adapter_saver.go`](../../../internal/uiserver/adapter_saver.go) — `AdapterSaver` (reused starting in 4D for adapter section writes).
- The settings panel's **outer chrome** (`.settings-tab`, `.settings-pane`, `.settings-section`, `.settings-body`, `.settings-tabs`, `.settings-panel`, `.settings-close`, `.settings-spacer`) was ported into [`internal/chassis/static/chassis.css`](../../../internal/chassis/static/chassis.css) during Phase 0 (foundation). The **interior** rules — `.field-row`, `.field-input` (+ `.has-value`, `.num`, `.path`), `.switch` (+ `.on`, `::before`, mobile sizing), `.action-btn` (+ `.primary`, `.ghost`), `.action-result` (+ `.shown`, `.ok`, `.err`), `.scope` (+ `.hot`, `.recast`, `.reboot`), `.field-row .row-end`, `.field-row.has-err`, `.field-row .field-err` (+ `::before`) — are **not yet present** in `chassis.css`. 4A ports these from the mockup ([v24:1973-1997, 2000-2020, 2040-2060, 2936-2949, 2951, 2960-2973, 3059-3073, 3389-3391](../reference/2026-05-21-receiver-v24.html)) and adds one new rule, `.scope.next`, for the 4-tier scope vocabulary.

## Goals

1. **The gear button works.** Clicking `⚙ Setup` in the transport row toggles `body.settings-open`; the existing CSS expands the settings panel from collapsed to expanded. Closing via the `✕ Close` button or by clicking the gear again collapses it back.
2. **All five tabs visible from day one.** The tab strip renders all five tabs (Network, Pipeline, Adapters · N, Catalog · 3, Advanced) on first paint. Clicking a tab switches the active pane purely client-side; no server round trip.
3. **Network pane is fully functional.** All 9 bridge fields (MiSTer connection × 3, Bridge HTTP × 3, External tools × 3) render with their current values, auto-save on blur, validate inline, and dispatch through `BridgeSaver.Save()` with correct scope.
4. **One action button works.** "Test MiSTer connectivity" runs the existing safe status-probe pattern against the currently-saved `bridge.mister.{host,port}` and renders the result (latency, timeout, or socket error) in the `.action-result` slot under the button. `mister_source_port` is still saved and preflighted, but cannot be exercised by this probe while the process's main sender already owns that UDP source port.
5. **The field renderer generalizes.** A single Go template helper (`field`) supports the six field types (text, number, password, path, select, switch) that Network + Pipeline + Adapters + Catalog + Advanced all use. 4B–4F add zero new field-type primitives.
6. **JSON contract is reusable.** Success: `{ok:true, scope:"hot|next|recast|reboot"}`. Field-validation failure: `{ok:false, errors:{<field>:"<msg>"}}`. Whole-form failure: `{ok:false, chip:"<chip>"}`. 4B–4F use the same envelope.
7. **`internal/chassis/` adds no new concrete-adapter imports.** Phase 4 saves go through `BridgeSaver` (and later `AdapterSaver`); chassis never imports `internal/adapters/{streams,plex,jellyfin,dlna,torrent,url}`. `import_check_test.go` continues to enforce isolation.
8. **`/ui/*` is unchanged.** 4A is purely additive under `/receiver/*`. Cutover happens after 4F.

## Non-Goals

- **Pipeline, Adapters, Catalog, Advanced pane content.** Their tabs render and their panes are clickable, but each shows a "Spec 4X — implementation in progress" placeholder card.
- **Link cascades.** Plex's 4-state PIN flow (unlinked / pending / linked / expired per [v24:4299-4336](../reference/2026-05-21-receiver-v24.html#L4299-L4336)) and Jellyfin's 2-state sync auth (per [v24:4488-4509](../reference/2026-05-21-receiver-v24.html#L4488-L4509)) require their own state-machine design. Phase 4E.
- **Adapter custom widgets.** yt-dlp host tag-list (add/remove individual hosts, per [v24:4375-4389](../reference/2026-05-21-receiver-v24.html#L4375-L4389)) and cookies textarea + save/clear + status pill (per [v24:4400-4411](../reference/2026-05-21-receiver-v24.html#L4400-L4411)) need their own data model and POST shapes. Phase 4F.
- **Launch core action button.** Lives in the Pipeline pane ([v24:4262-4267](../reference/2026-05-21-receiver-v24.html#L4262-L4267)). Phase 4B.
- **Restore-defaults preset button.** Lives in the Catalog pane. Phase 4C.
- **Drawer open/close state persistence.** Refresh closes the drawer. No localStorage, no cookie. Matches the 3B catalog drawer's ephemeral state. Could be added later as a one-line localStorage write if it becomes annoying.
- **Pane state restoration across refresh** (last-active tab). Refresh resets to Network. Same rationale as above.
- **Cross-field validation that requires the server.** Network field decoders stay per-field where possible (IPv4 format, port range, absolute-path check, external-tool executable check). Cross-field constraints (e.g., `mister_port ≠ ui_http_port` collision check) are out of scope; the existing `BridgeSaver.Save` preflight catches genuine bind conflicts via TCP/UDP probes where the bridge would actually bind.
- **First-run banner / setup wizard.** The `.first-run-complete` sentinel ([`internal/uiserver/bridge_saver.go:29`](../../../internal/uiserver/bridge_saver.go#L29)) stays the legacy UI's concern. The chassis settings drawer is just a settings drawer; a "first run" flow lives elsewhere if at all.
- **Mobile-responsive polish, accessibility audit beyond visible-focus and semantic HTML.** Foundation spec defers both to Phase 5.
- **Removing fields from the drawer.** No deprecation flow needed today.
- **Authentication.** LAN-only trust model, same as the rest of the chassis. Same-origin middleware is the boundary.

## Design Decisions

| Decision | Resolution |
|---|---|
| Drawer entry point | `⚙ Setup` button already present in [`internal/chassis/templates/transport.html`](../../../internal/chassis/templates/transport.html). 4A wires its `onclick` via a `data-settings-toggle` attribute (additive to the existing id; no breaking changes to existing tests). |
| Drawer open/close mechanism | Toggle `body.settings-open` class. The gear button uses `toggle` semantics — clicking it while the drawer is open closes the drawer (same as clicking the `✕ Close` button). CSS at [v24:1848-1873](../reference/2026-05-21-receiver-v24.html#L1848-L1873) already drives the collapse/expand transition. No JavaScript animations. |
| Tab visibility | All five tabs visible from 4A. Clicking a non-Network tab reveals a "Spec 4X — implementation in progress" placeholder card. Rejected: hiding tabs until their spec ships (operator can't see the roadmap; surprise when new tabs appear). Rejected: greyed-out tabs (visually ambiguous — could mean "not configured" vs "not implemented"). |
| Field commit model | Per-field auto-save on blur (or change for select / switch). Matches the mockup's deliberate absence of Save buttons. Matches `/ui/*` HTMX behavior. Localizes errors. Avoids dirty-state tracking. |
| Scope badge vocabulary | 4-tier: `HOT` / `NEXT` / `RECAST` / `REBOOT`. The mockup's 3-tier (`HOT` / `RECAST` / `REBOOT`) collapses `ScopeNextCast` under `RECAST`; we expose all four because `NextCast` (silent — current cast unaffected) and `RestartCast` (visible — current cast stops) communicate genuinely different things to the operator. One new CSS rule needed (`.scope.next`) — defer the exact color to the field-renderer task. |
| HTTP route shape | One route per bridge section + per adapter + per action. `POST /receiver/settings/bridge` accepts any subset of bridge fields; `POST /receiver/settings/adapter/{name}` (4D+); `POST /receiver/settings/action/{name}` for action buttons. Mirrors the saver layer's existing partial-update shape. |
| Form encoding | `application/x-www-form-urlencoded`. Matches the rest of `/receiver/*`. JSON request bodies would need new client serialization; form encoding is what `<form>` and `FormData` produce natively. |
| Initial drawer rendering | Server-rendered at page load with all current field values. Matches 3A/3B convention (preset bank, catalog drawer both server-rendered initially). Refresh restores exactly what the operator was looking at; no "loading…" state for opening Settings. |
| Subsequent updates | JSON responses, not HTML fragments. Client paints `.field-err`, toggles `.has-value`, etc. purely from response data. No HTMX (chassis stays HTMX-free). |
| Error envelope (settings save) | `errors` and `chip` are mutually exclusive. `errors` is a `map[string]string` keyed by form-field name (server can return multiple field errors in one envelope). `chip` is a single string for whole-form errors. Client picks render target based on which key is present. |
| Error envelope (action) | Action endpoints (probe-mister, future launch-core, etc.) use `error` (singular string, free-form) for in-handler operational results that the UI renders into the action's `.action-result` slot. They reuse `chip` only for upstream/wiring failures (`NOT READY` when the action's dependency is nil; `403` for wrong origin). Rationale: settings saves and action results land in different DOM targets and the UI wants more diagnostic data from actions (latency, host, etc.) than a single chip can carry. |
| Field-row error UX | Inline `.field-err` div below the input, plus `.field-row.has-err` class on the outer row. Cleared when the field next saves successfully or when the operator focuses+blurs without change. |
| Whole-form error UX | Toast via a new drawer-local settings notice slot, chip text per error (e.g. `BAD INPUT`, `PORT IN USE`, `WRITE FAILED`). Slot HTML: `<div class="settings-notice" id="settings-notice" role="status" aria-live="polite" hidden></div>`, sibling of `.settings-body` inside `.settings-panel`. Two CSS classes for variants: `.settings-notice.ok` (REBOOT-scope success), `.settings-notice.err` (chip failures). Client sets text + class + un-hides; auto-clears after 5s or on next successful save. Field-row stays marked with the error until next successful save attempt. Rejected: reusing 3A's input chip, because it is anchored to the cast input row and currently exposed only as `window.Chassis.input.showError`. |
| REBOOT-scope success UX | Toast: `"Restart container to apply new <field-label>"`. Field renders with the new value (input already holds what was typed). Disk has the new value; live state has the old. No persistent banner in 4A. |
| Probe action target | Uses currently-saved `bridge.mister.{host,port}`, not form values. Operator must save before probing. The saved `source_port` cannot be rebound for a one-off probe because the main bridge sender owns it for the process lifetime; source-port validity is covered by `BridgeSaver.Save` preflight and then by the next bridge restart. |
| Probe action timeout | Hard 1s server-side timeout, matching the mockup's `"ACK in 4.2ms"` example and the legacy diagnostics prober's timeout. Single-flight per click (client disables button while in-flight). |
| Probe response shape | 200 on **both** success and timeout — the probe itself ran cleanly in both cases. 5xx is reserved for socket errors (cannot construct probe). Distinct from operational ack-or-not. |
| Field renderer surface | One template helper `field` taking a `dict`-style option bag. Supports text / number / password / path / select / switch. No bespoke helpers per type — option-bag pattern is verbose but readable in templates. |
| Stub pane shape | Single `<div class="settings-section wide">` with an h4 and an `.action-result.shown` line saying which spec covers it. No interactive content. Identical structure across all four stubs. |
| `internal/chassis/import_check_test.go` | Forbidden-imports list unchanged. 4A imports `internal/config` and the existing root `internal/adapters` package only. It defines chassis-owned interfaces for bridge saving/probing so production code in `internal/chassis` still does **not** import `internal/uiserver` or any concrete adapter package. |
| CSS additions | ~30 rules totaling ~80 lines, ported verbatim from the mockup (see §Context for full selector list and source line ranges). Scoped under `body.receiver` per chassis convention. Plus one new rule (`.scope.next`) for the 4-tier badge — color picks a tone between `.scope.hot` (cyan) and `.scope.recast` (amber); exact OKLCH chosen during implementation. |
| Switch field POST shape | Form value `"true"` / `"false"`. Client renders `.switch.on` / `.switch` based on response (`.has-value` is not relevant for switches). Optimistic toggle on click, revert on 4xx. |

## Implementation Checklist (sketch — implementation plan elaborates)

- `internal/chassis/settings.go` (new): `handleSettingsBridgePost`, `handleSettingsActionProbeMister`, the per-field decoder table, error envelope writers (`writeSettingsFieldErrors`, `writeSettingsChip`, `writeSettingsSuccess`, `writeProbeResult`).
- `internal/chassis/settings_test.go` (new): handler unit tests for every success/error branch listed in §Wire Contract.
- `internal/chassis/templates/settings-drawer.html` (rewrite from current 8-line stub): top-level drawer, drawer-local toast/notice slot, 5-tab strip, 5 panes (Network + 4 stubs).
- `internal/chassis/templates/settings-field.html` (new): the one row template that `field` helper expands into. (Or implemented as inline `{{- define -}}` blocks in `settings-drawer.html` — implementation plan picks.)
- `internal/chassis/static/settings-drawer.js` (new): gear button + close button toggle, tab switching, field blur → POST, switch click → POST, response handling (success / field-error / chip-error), action button click handler with single-flight, render probe result.
- `internal/chassis/templates/shell.html` (extend): add a deferred `/receiver/static/settings-drawer.js?v={{.Version}}` script tag. Existing chassis JS is explicit-script-tag based; embedding the static file alone is not enough.
- `internal/chassis/data.go` (extend): grow `SettingsData` from `{Open bool}` to `{Open, Bridge config.BridgeConfig, Errors map[string]string, AdapterCount, CatalogProviderCount int}`; add `buildSettingsData(bridge, registry, catalogViewer)` helper.
- `internal/chassis/session.go` (extend): `snapshotFromStatusView` and `idleSnapshot` both call `buildSettingsData` with the live bridge snapshot from `BridgeSettingsSaver.Current()` when wired, falling back to startup `cfg.Bridge` only for nil-saver tests/offline render paths.
- `internal/chassis/settings.go` (new): define the chassis-owned `BridgeSettingsSaver`, `settingsChipError`, and `Prober` narrow interfaces.
- `internal/chassis/server.go` (extend): add `Config.BridgeSaver BridgeSettingsSaver` + `Config.Prober Prober`; store on `*Server`; mount the two new method-specific routes.
- `internal/chassis/templates.go` (extend): register `field`, `dict`, `stub`, `errOf`, `itoa` (or `tos` if more general), and `settingsScopeLabel` helpers in the template FuncMap.
- `internal/chassis/templates/transport.html` (one-line edit): add `data-settings-toggle` attribute to the gear button. Existing `id="gear-btn"` stays for any test references.
- `internal/chassis/chassis_test.go` (extend): template render tests for drawer, Network pane, field helper per type, stub pane, scope badge variants, drawer-local toast slot, and `settings-drawer.js` script inclusion.
- `internal/chassis/static/chassis.css` (extend, ~80 lines): port the interior settings rules listed in §Context — `.field-row` (+ variants), `.field-input` (+ `.has-value`, `.num`, `.path`), `.switch` (+ `.on`, `::before`, focus-visible, mobile @media), `.action-btn` (+ `.primary`, `.ghost`), `.action-result` (+ `.shown`, `.ok`, `.err`), `.scope` (+ `.hot`, `.recast`, `.reboot`), `.field-row .row-end`, `.field-row.has-err`, `.field-row .field-err` (+ `::before`). All scoped under `body.receiver` per chassis CSS convention. Add two new rules: `.scope.next` (color between `.scope.hot` and `.scope.recast`) and `.settings-notice` (+ `.ok`, `.err` variants — see Design Decisions row for HTML shape; ~10 lines).
- `internal/uiserver/bridge_saver.go` (extend): wrap validation/preflight failures in an additive typed error that exposes `StatusCode() int` and `Chip() string` while preserving `Unwrap()`. `Save()` signature stays `(adapters.ApplyScope, error)`.
- `internal/uiserver/bridge_saver_test.go` (extend): tests for typed validation/preflight errors and the existing per-field scope returned by `Save()` for each Network field.
- `cmd/mister-groovy-relay/main.go` (extend): construct `BridgeSaver` (already constructed for `/ui/*` — same instance reused), pass into `chassis.Config`; construct a `Prober` (wraps existing `groovynet.Sender` probe capability) and pass it.
- `tests/integration/chassis_test.go` (extend): end-to-end save + probe coverage, see §Testing.

**Files intentionally unchanged in 4A:**
- `internal/ui/*` — legacy `/ui/*` keeps working.
- `internal/core/*` — no new core surface.
- `internal/adapters/*` — no new adapter interfaces in 4A. Adapter-touching specs are 4D–4F.

## Wire Contract — HTTP Routes

### `POST /receiver/settings/bridge`

**Headers:** browser-supplied `Sec-Fetch-Site: same-origin` or `same-site` (enforced by `requireSameOrigin`). Client JavaScript must **not** attempt to set `Sec-*` headers manually; those are browser-controlled forbidden request headers. Go handler tests set the header synthetically.

**Body:**
```
Content-Type: application/x-www-form-urlencoded

mister_host=192.168.1.42
```

Any subset of supported field names is accepted. Missing keys mean "don't change this field." Empty values *are* a change (clearing the field).

**Server logic:**

1. `requireSameOrigin` middleware. Wrong origin → 403.
2. `r.ParseForm()`. Parse failure → 400 `{ok:false, chip:"BAD INPUT"}`.
3. Build `touched := map[string]string{}` from `r.PostForm`. If empty → 400 `{ok:false, chip:"BAD INPUT"}`.
4. For each touched field, look up its decoder and run it against the supplied string. Collect all errors into a `map[string]string`. If `len(errors) > 0` → 400 `{ok:false, errors:errors}`. Saver is **not** called when any field decode fails.
5. Read the live saved bridge snapshot from `BridgeSettingsSaver.Current()` and overlay decoded touched fields onto that copy. Do **not** use startup `cfg.Bridge`; otherwise the drawer/probe can go stale after the first save. The form-name → `config.BridgeConfig` overlay table is colocated with the chassis decoder table in `internal/chassis/settings.go` — one entry per supported field, mapping the form-name to a closure that writes the decoded value into the right struct path (e.g., `"mister_host"` → `func(c *config.BridgeConfig, v any) { c.MiSTer.Host = v.(string) }`). The decoder, scope-label-from-`ApplyScope`, and overlay tables are validated against each other by a unit test so adding a field to one without the others fails the build.
6. Call `BridgeSettingsSaver.Save(patch)`, which is satisfied by the existing `*uiserver.BridgeSaver` instance in production.
7. On typed saver errors satisfying the chassis-owned `settingsChipError` interface (`{ error; StatusCode() int; Chip() string }`, declared in `internal/chassis/settings.go` — see Architecture): map status + chip directly into the response (e.g., `409 {ok:false, chip:"PORT IN USE"}`). The handler matches **structurally** — `errors.As(err, new(settingsChipError))` works against an interface type in Go 1.21+ and is the standard idiom; the chassis must not import the concrete `*uiserver.settingsError` (or whatever the wrapper is named) directly, since that would re-introduce a `internal/uiserver` import the chassis isolation contract forbids. 4A adds the wrapper in `internal/uiserver` for validation/preflight failures without changing `Save()`'s signature; the wrapper's `Unwrap()` returns the original error for compatibility with `errors.Is`/`As` on downstream consumers. Minimum mapping:
   - TCP/UDP bind preflight failure for `ui_http_port` / `mister_source_port` → `409 PORT IN USE`
   - `data_dir` writable preflight failure → `409 PATH NOT WRITABLE`
   - `config.Sectioned.Validate` failure that survives chassis-side field validation → `400 BAD INPUT`
8. On unexpected saver error (marshal/write/runtime side effect failure that is not typed): 500 `{ok:false, chip:"WRITE FAILED"}`.
9. On success: use the `adapters.ApplyScope` returned by `Save()` and map it through a chassis response-label helper. Do **not** serialize `ApplyScope.String()` directly: current values are `"hot-swap"`, `"next-cast"`, `"restart-cast"`, `"restart-bridge"`, while the chassis JSON contract is `"hot"`, `"next"`, `"recast"`, `"reboot"`. Unknown scope → `500 {ok:false, chip:"WRITE FAILED"}`. Respond `200 {ok:true, scope:"hot|next|recast|reboot"}`.

**Responses:**

| Status | Body | When |
|---|---|---|
| 200 | `{"ok":true,"scope":"hot"}` | Touched fields all hot-swappable |
| 200 | `{"ok":true,"scope":"reboot"}` | At least one touched field is REBOOT scope |
| 400 | `{"ok":false,"errors":{"mister_host":"not a valid IPv4 or hostname"}}` | One or more per-field validation failures |
| 400 | `{"ok":false,"chip":"BAD INPUT"}` | Form parse failure or empty body |
| 403 | (middleware) | Wrong origin |
| 409 | `{"ok":false,"chip":"PORT IN USE"}` | TCP/UDP bind preflight failed for a port change |
| 409 | `{"ok":false,"chip":"PATH NOT WRITABLE"}` | `data_dir` preflight failed |
| 500 | `{"ok":false,"chip":"WRITE FAILED"}` | Disk write, unexpected saver error, or unknown `ApplyScope` |
| 503 | `{"ok":false,"chip":"NOT READY"}` | `BridgeSettingsSaver` not wired (defensive; main.go always wires) |

### `POST /receiver/settings/action/probe-mister`

**Headers:** browser-supplied `Sec-Fetch-Site: same-origin` or `same-site` (enforced by `requireSameOrigin`; not manually set by client JS).

**Body:** Empty (action button has no parameters).

**Server logic:**

1. `requireSameOrigin` middleware.
2. If `s.cfg.Prober == nil` or `s.cfg.BridgeSaver == nil` → 503 `{ok:false, chip:"NOT READY"}`.
3. Read the current saved `config.BridgeConfig` from `BridgeSettingsSaver.Current()`; this supplies `bridge.mister.{host,port}`. Do not read startup `cfg.Bridge`.
4. Call `prober.ProbeMister(ctx, bridgeCfg)` with a 1s context timeout. The prober wrapper lives in `cmd/mister-groovy-relay` and reuses the legacy diagnostics pattern: construct a temporary `groovynet.Sender` with saved `host` + `port` and source port `0`, send `CMD_GET_STATUS`, wait for an ACK, and time the round trip. It intentionally does **not** bind `bridge.mister.source_port`, because `cmd/mister-groovy-relay/main.go` already constructed the process-wide sender with that source port and `groovynet.NewSender` rejects duplicate source-port binds.
5. On success: `200 {ok:true, latency_ms:4.2, host:"192.168.1.42", port:32100}`.
6. On timeout: `200 {ok:false, error:"timeout", elapsed_ms:1000}`. The prober normalizes status-probe `net.Error` timeouts to `context.DeadlineExceeded` before returning to chassis. Distinct from operational failure — the probe ran cleanly; the MiSTer didn't answer.
7. On any other error (socket open/send/read failure, malformed ACK packet): `500 {ok:false, error:"<sanitized message>"}`. The chassis sanitizes error strings to avoid leaking host details into logs (mirrors existing transport error sanitization in 3B).

**Responses:**

| Status | Body | When |
|---|---|---|
| 200 | `{"ok":true,"latency_ms":4.2,"host":"192.168.1.42","port":32100}` | MiSTer ACKed |
| 200 | `{"ok":false,"error":"timeout","elapsed_ms":1000}` | Probe ran cleanly, no ACK in 1s |
| 403 | (middleware) | Wrong origin |
| 500 | `{"ok":false,"error":"socket: <message>"}` | Could not run the probe |
| 503 | `{"ok":false,"chip":"NOT READY"}` | `Prober` or `BridgeSettingsSaver` not wired (defensive; main.go always wires both) |

Client renders into `#probe-mister-result` in the `.action-result` slot under the button:

- Success: `▸ ACK in {{latency_ms}}ms · MiSTer {{host}}:{{port}}`
- Timeout: `▸ NO ACK · {{elapsed_ms}}ms timeout · check host/port`
- Error: `▸ ERROR · {{error}}`

The `.shown` CSS class gates visibility; `.action-result.shown.ok` and `.action-result.shown.err` provide success/error coloring from the CSS rules 4A ports.

## Architecture — Data Flow

**At page load (`GET /receiver`):**

```
chassis shell renders
  └─ snapshotFromStatusView populates SnapshotData.Settings via buildSettingsData,
     using BridgeSettingsSaver.Current() when wired
  └─ settings-drawer.html renders into the .settings-panel slot, hidden
     (body.settings-open is absent on initial paint)
     Server emits HTML for every Network field with the current bridge value;
     four stub panes render their placeholder cards; tabs render with badge counts.
```

**On gear button click:**

```
User clicks ⚙ Setup
  └─ settings-drawer.js handler: body.classList.toggle('settings-open')
     CSS transitions handle the visual expand/collapse.
     No server round trip.
```

**On Network field edit:**

```
User edits an input
  └─ settings-drawer.js onblur handler (or onchange for select/switch)
     └─ POST /receiver/settings/bridge
        Body: { <field_name>: <new_value> }
     └─ Response handling:
        ├─ 200 {ok:true, scope:"hot"}  → input gains .has-value, no toast
        ├─ 200 {ok:true, scope:"next"} → same
        ├─ 200 {ok:true, scope:"recast"} → same; SSE transport event will reflect cast-stop separately
        ├─ 200 {ok:true, scope:"reboot"} → toast "Restart container to apply <field-label>"
        ├─ 400 {ok:false, errors:{...}} → render .field-err inline on each named field
        └─ 4xx/5xx {ok:false, chip:"..."} → toast chip text; mark row .has-err
```

**On probe button click:**

```
User clicks ▸ Test MiSTer connectivity
  └─ settings-drawer.js: disable button, clear .action-result
     └─ POST /receiver/settings/action/probe-mister (empty body)
     └─ Response handling: render formatted result into .action-result.shown
        Re-enable button.
```

**On tab click:**

```
User clicks a tab
  └─ settings-drawer.js: toggle .active class on the tab; toggle .active on the pane
     No server round trip. Network and stub panes are all in the DOM at all times.
```

## Architecture — The Field Renderer

Single Go template FuncMap helper. Registered as `field` in [`internal/chassis/templates.go`](../../../internal/chassis/templates.go).

```go
// FieldArgs is the option bag the {{ field ... }} helper consumes.
// All fields are optional except Name and Type.
type FieldArgs struct {
    Name        string        // POST key + DOM identifier ("mister_host")
    Type        string        // "text" | "number" | "password" | "path" | "select" | "switch"
    Label       string        // "Host"
    Help        string        // optional <span class="help"> tail on the label
    Value       string        // current value, always string; type-specific upstream
    Placeholder string        // optional placeholder for empty inputs
    Scope       string        // "hot" | "next" | "recast" | "reboot"
    Unit        string        // optional unit suffix ("GiB", "px", "ms"), renders inside .row-end
    Options     []FieldOption // required for type=select; ignored otherwise
    InputWidth  string        // optional max-width (e.g. "90px") — empty = full width
    Error       string        // server-rendered error message; empty = no error
}

type FieldOption struct {
    Value string
    Label string // empty = falls back to Value
}
```

Template usage:

```go
{{ field (dict
    "Name"  "mister_host"
    "Type"  "text"
    "Label" "Host"
    "Help"  "IP or hostname of your MiSTer on the LAN."
    "Value" .Bridge.MiSTer.Host
    "Scope" "reboot"
    "Error" (errOf .Errors "mister_host")
) }}
```

`dict` is a local 6-line template helper (`func(pairs ...any) map[string]any`). `errOf` has the explicit signature `func(map[string]string, string) string` because template funcs do not receive the current dot implicitly. `itoa` is `strconv.Itoa` wrapped for the FuncMap.

**Output HTML by type:**

| Type | Middle slot |
|---|---|
| `text` | `<input class="field-input{{ if .Value }} has-value{{ end }}" name="{{.Name}}" value="{{.Value}}" placeholder="{{.Placeholder}}">` |
| `number` | `<input class="field-input num{{ if .Value }} has-value{{ end }}" type="number" name="{{.Name}}" value="{{.Value}}"{{ if .InputWidth }} style="max-width:{{.InputWidth}}"{{ end }}>`, optionally wrapped in `<span class="row-end">…unit + scope</span>` |
| `password` | `<input class="field-input{{ if .Value }} has-value{{ end }}" type="password" name="{{.Name}}" value="{{.Value}}">` |
| `path` | `<input class="field-input path{{ if .Value }} has-value{{ end }}" name="{{.Name}}" value="{{.Value}}" placeholder="{{.Placeholder}}">` |
| `select` | `<select class="field-input has-value" name="{{.Name}}">…<option value="{{.Value}}"{{ if eq .Value $.Value }} selected{{ end }}>{{ or .Label .Value }}</option>…</select>` |
| `switch` | `<button class="switch{{ if eq .Value "true" }} on{{ end }}" data-field="{{.Name}}" type="button" aria-pressed="{{ eq .Value "true" }}"></button>` |

All types render the outer `<div class="field-row{{ if .Error }} has-err{{ end }}">` with the `<label>…</label>` first and `<span class="scope {{.Scope}}">{{ upper .Scope }}</span>` last. When `Error != ""`, an additional `<div class="field-err">{{.Error}}</div>` renders between input and scope.

**Why option-bag instead of positional args:** Go templates don't support named parameters. Positional with 10+ args is illegible. Option-bag via `dict` is verbose at the call site but every field is self-documenting and adding a new optional knob doesn't break existing callers.

## Network Pane Fields

### Section: MiSTer connection `[bridge.mister]`

The `[bridge.mister]` TOML section is **split across two panes**: Network owns connection fields (host, port, source_port), Pipeline owns SSH credentials (`ssh_user`, `ssh_password`) per the mockup ([v24:4253-4260](../reference/2026-05-21-receiver-v24.html#L4253-L4260)). The shared section header is intentional — operators reading the TOML hint expect everything under `[bridge.mister]` somewhere in Settings, just organized by responsibility. 4B (Pipeline) adds the SSH fields under a separate `MiSTer control` section heading without touching this table.

| Form name | Type | Label | Help | Scope | Default | Validation |
|---|---|---|---|---|---|---|
| `mister_host` | text | Host | IP or hostname of your MiSTer on the LAN. | REBOOT | — (operator must set) | empty → `"is required"`. Non-empty must parse as IPv4 (`net.ParseIP`) or DNS hostname (RFC-952 char check). Invalid → `"not a valid IPv4 or hostname"`. |
| `mister_port` | number | Port | UDP port the MiSTer's Groovy core listens on. | REBOOT | 32100 | int in `[1, 65535]`. Out-of-range → `"port out of range (1-65535)"`. Non-numeric → `"must be a whole number"`. |
| `mister_source_port` | number | Source port | Our stable source UDP port. Must stay the same across restarts. | REBOOT | 32101 | int in `[1, 65535]`. Out-of-range → `"port out of range (1-65535)"`. Non-numeric → `"must be a whole number"`. |

Followed by the **probe-mister** action button row (see §Wire Contract).

### Section: Bridge HTTP `[bridge.ui]`

| Form name | Type | Label | Help | Scope | Default | Validation |
|---|---|---|---|---|---|---|
| `ui_http_port` | number | HTTP port | Plex Companion HTTP + Settings UI (shared listener). | REBOOT | 32500 | int in `[1, 65535]`. Out-of-range → `"port out of range (1-65535)"`. Empty/non-numeric → `"must be a whole number"`. Saver preflight then validates bindability on `0.0.0.0:<port>`; on bind failure responds `409 {chip:"PORT IN USE"}`. |
| `host_ip` | text | Host IP | LAN IP advertised to Plex. Leave blank to auto-detect. | REBOOT | `""` (placeholder shows `auto-detect`) | empty allowed (clears the field); if non-empty, must parse as IPv4. Invalid → `"not a valid IPv4 address"`. |
| `data_dir` | path | Data directory | Where plex.json and other persistent state live. Leave empty for OS default. | REBOOT | `""` (placeholder shows `auto`) | empty allowed (clears the field); if non-empty, must be absolute (`filepath.IsAbs`). Relative → `"must be an absolute path"`. No existence check. |

### Section: External tools (override sidecar paths)

| Form name | Type | Label | Help | Scope | Default | Validation |
|---|---|---|---|---|---|---|
| `ffmpeg_path` | path | FFmpeg path | Empty = bundled sidecar, then PATH. | HOT | `""` (placeholder `auto`) | empty allowed; if non-empty, must be absolute, exist, be a file, and be executable (or have `.exe` extension on Windows), matching `config.Sectioned.Validate`. Missing/not executable → `"not a usable executable path"`. |
| `ffprobe_path` | path | FFprobe path | (no help text) | HOT | `""` (placeholder `auto`) | same as ffmpeg_path |
| `ytdlp_path` | path | yt-dlp path | (no help text) | HOT | `""` (placeholder `auto`) | same as ffmpeg_path |

**External-tool hot-swap dispatch.** The three path fields dispatch via the existing `OverrideUpdater` interface on `BridgeSaver.tools` ([`internal/uiserver/bridge_saver.go:42-49`](../../../internal/uiserver/bridge_saver.go#L42-L49)). When the saved value changes, `BridgeSaver.Save` calls `s.tools.FFmpeg.UpdateOverride(newPath)` (etc.). No new wiring needed in 4A beyond passing the existing `BridgeSaver` instance into `chassis.Config`.

## Architecture — `SettingsData` and Snapshot Wiring

### `internal/chassis/data.go`

```go
// Was:
type SettingsData struct { Open bool }

// Becomes:
type SettingsData struct {
    Open                 bool
    Bridge               config.BridgeConfig // current values, used for first render
    Errors               map[string]string   // empty on first render
    AdapterCount         int                 // for the Adapters tab badge
    CatalogProviderCount int                 // for the Catalog tab badge
}

// buildSettingsData populates the struct from current bridge + registry +
// catalog viewer state. Called from snapshotFromStatusView and idleSnapshot.
// Errors is always returned empty by buildSettingsData; it is populated only
// when the server re-renders the drawer after a redirect following a failed
// save (unused in 4A — every error path returns JSON).
func buildSettingsData(
    bridge config.BridgeConfig,
    registry *adapters.Registry,
    catalog adapters.StreamsCatalogViewer,
) SettingsData {
    // The registry contains 7 adapters today: plex, jellyfin, url, streams,
    // aux, torrent, dlna (per cmd/mister-groovy-relay/main.go). AUX is a
    // hardware-button surface, not a configurable settings target; it has
    // no [adapters.aux] TOML section and no fields in the Adapters pane.
    // Mockup shows "Adapters · 6" — matches 7 registered − 1 AUX = 6.
    adapterCount := 0
    if registry != nil {
        for _, a := range registry.List() {
            if a.Name() == "aux" {
                continue
            }
            adapterCount++
        }
    }
    catalogProviderCount := 0
    if catalog != nil {
        catalogProviderCount = len(catalog.Catalog())
    }
    return SettingsData{
        Bridge:               bridge,
        AdapterCount:         adapterCount,
        CatalogProviderCount: catalogProviderCount,
        // Open and Errors stay zero-valued.
    }
}
```

### `internal/chassis/session.go`

Both `snapshotFromStatusView` and `idleSnapshot` call `buildSettingsData(currentBridge, s.cfg.Registry, s.cfg.StreamsCatalogViewer)` and assign to `snapshot.Settings`. `Server.buildSnapshot` obtains `currentBridge` from `s.bridgeSaver.Current()` when the saver is wired; nil-saver tests/offline render paths fall back to `s.cfg.Bridge`. This matters because the settings drawer and probe action must reflect saves made after process start.

### `internal/chassis/server.go`

```go
type Config struct {
    // … existing fields …

    BridgeSaver BridgeSettingsSaver // 4A: bridge current/read/save, satisfied by *uiserver.BridgeSaver
    Prober      Prober              // 4A: MiSTer connectivity probe
}

// BridgeSettingsSaver is the narrow chassis-side interface for bridge
// settings. Production passes *uiserver.BridgeSaver, but internal/chassis
// does not import internal/uiserver.
type BridgeSettingsSaver interface {
    Current() config.BridgeConfig
    Save(config.BridgeConfig) (adapters.ApplyScope, error)
}

// settingsChipError is matched structurally so saver-layer typed errors can
// carry HTTP/chip details across the interface boundary without a uiserver import.
type settingsChipError interface {
    error
    StatusCode() int
    Chip() string
}

// Prober is the narrow chassis-side interface the probe-mister action uses.
// Implemented by a small wrapper around groovynet.Sender; lives
// in main.go construction so the chassis doesn't depend on groovynet.
type Prober interface {
    ProbeMister(ctx context.Context, bridge config.BridgeConfig) (ProbeResult, error)
}

type ProbeResult struct {
    LatencyMs float64
    Host      string
    Port      int
}
```

New mux routes in `Server.RegisterRoutes`:

```go
mux.Handle("POST /receiver/settings/bridge",
    requireSameOrigin(http.HandlerFunc(s.handleSettingsBridgePost)))
mux.Handle("POST /receiver/settings/action/probe-mister",
    requireSameOrigin(http.HandlerFunc(s.handleSettingsActionProbeMister)))
```

## Architecture — Client JS (`settings-drawer.js`)

New file. Plain ES2022, no framework. Mirrors the patterns established by `preset-bank.js`, `catalog-browser.js`, `input-cast.js`.

```js
// Module pattern; same shape as catalog-browser.js.
(function () {
  const body = document.body;
  const drawer = document.querySelector('.settings-panel');
  if (!drawer) return;

  // Gear button toggle.
  const gear = document.querySelector('[data-settings-toggle], #gear-btn');
  if (gear) gear.addEventListener('click', () => body.classList.toggle('settings-open'));
  const close = document.getElementById('settings-close');
  if (close) close.addEventListener('click', () => body.classList.remove('settings-open'));

  // Tab switching.
  const tabs = drawer.querySelectorAll('.settings-tab');
  const panes = drawer.querySelectorAll('.settings-pane');
  tabs.forEach(t => t.addEventListener('click', () => {
    tabs.forEach(x => x.classList.remove('active'));
    panes.forEach(x => x.classList.remove('active'));
    t.classList.add('active');
    drawer.querySelector(`.settings-pane[data-pane="${t.dataset.tab}"]`).classList.add('active');
  }));

  // Field auto-save on blur (text/number/password/path) and on change (select).
  // Switches POST on click.
  drawer.querySelectorAll('input.field-input, select.field-input').forEach(el => {
    const eventName = el.tagName === 'SELECT' ? 'change' : 'blur';
    el.addEventListener(eventName, () => saveField(el.name, el.value));
  });
  drawer.querySelectorAll('button.switch[data-field]').forEach(el => {
    el.addEventListener('click', () => {
      const next = !el.classList.contains('on');
      el.classList.toggle('on', next);  // optimistic
      el.setAttribute('aria-pressed', next ? 'true' : 'false');
      saveField(el.dataset.field, next ? 'true' : 'false', () => {
        // revert on error
        el.classList.toggle('on', !next);
        el.setAttribute('aria-pressed', !next ? 'true' : 'false');
      });
    });
  });

  async function saveField(name, value, onError) {
    clearFieldErrors(name);
    const form = new FormData();
    form.set(name, value);
    let body = {};
    try {
      const res = await fetch('/receiver/settings/bridge',
        { method: 'POST', body: new URLSearchParams(form), credentials: 'same-origin' });
      body = await res.json().catch(() => ({}));
      if (res.ok && body.ok) {
        markFieldHasValue(name, value);
        if (body.scope === 'reboot') toastReboot(name);
        return;
      }
    } catch (_) {
      body = { chip: 'WRITE FAILED' };
    }
    if (body.errors) {
      paintFieldErrors(body.errors);
    } else if (body.chip) {
      toastChip(body.chip);
      markFieldHasErr(name);
    } else {
      toastChip('WRITE FAILED');
      markFieldHasErr(name);
    }
    if (onError) onError();
  }

  // Probe action button — single-flight.
  const probeBtn = document.getElementById('probe-mister-btn');
  const probeOut = document.getElementById('probe-mister-result');
  if (probeBtn) probeBtn.addEventListener('click', async () => {
    if (probeBtn.disabled) return;
    probeBtn.disabled = true;
    probeOut.className = 'action-result';
    probeOut.textContent = '';
    try {
      const res = await fetch('/receiver/settings/action/probe-mister',
        { method: 'POST', credentials: 'same-origin' });
      const body = await res.json().catch(() => ({}));
      if (body.chip) {
        toastChip(body.chip);
      } else {
        renderProbeResult(probeOut, body);
      }
    } catch (_) {
      renderProbeResult(probeOut, { ok: false, error: 'network error' });
    } finally {
      probeBtn.disabled = false;
    }
  });

  // Helpers (paintFieldErrors, clearFieldErrors, markFieldHasValue,
  // markFieldHasErr, renderProbeResult, toastChip, toastReboot) are small
  // DOM manipulations; spelled out in the implementation plan.
})();
```

The toast helpers write into the drawer-local notice slot added by `settings-drawer.html`; they do not reuse the cast input chip because that surface belongs to the input row and auto-resets based on pasted URL state.

## Edge Cases

| Case | Behavior |
|---|---|
| Probe vs in-flight cast | The probe uses the existing status-probe pattern (`CMD_GET_STATUS` from an ephemeral source port), not INIT and not the bridge's stable source port. It does not share a socket with the active Drainer, so it is allowed while a cast is active. |
| Probe vs pending source-port save | `mister_source_port` is restart-bridge scope. Saving it updates disk and preflights bindability, but the running process's main sender keeps the old bound port until restart. The Network probe therefore does not claim to validate source-port changes before restart. |
| Concurrent edits across two browser tabs | `BridgeSaver.mu` serializes; second write wins. Stale tab is stale until refresh. No optimistic-locking machinery. |
| REBOOT save + further edits to other fields | Each save writes a short notice into the drawer-local toast slot. Later notices replace earlier ones; no queue in 4A. |
| Switch optimistic toggle on 4xx | Client reverts the `.on` class and `aria-pressed`, then renders the field-error (`.field-err`) on the row. The field helper's error slot renders for every type uniformly when `Error != ""` (see §Architecture — The Field Renderer); switches use the same DOM as text/number fields. |
| Single-flight on probe button | Client disables the button while a request is in flight; re-enables on response. Prevents double-click stacking. |
| Field value with HTML metacharacters (`<`, `>`, `&`) | Go `html/template` auto-escapes `{{.Value}}` in both element content and `value="…"` attribute contexts. Server-side. |
| Probe after edit but before save | The just-typed (unsaved) value is **ignored** by the probe — it uses the currently-saved config. The help text "Verifies the address + ports above" honestly describes this. Operator must save first. |
| `data_dir` change on running bridge | REBOOT scope. Disk records new path; live bridge keeps using the old path until restart. No file migration. The first-run sentinel (`.first-run-complete`, per [`internal/uiserver/bridge_saver.go:29`](../../../internal/uiserver/bridge_saver.go#L29)) does not move with the operator's `data_dir` change — on next restart against the new `data_dir`, the legacy `/ui/*` first-run banner reappears until dismissed there. Out of scope to follow the sentinel across path moves; documenting so the implementation plan does not mistake this for a bug. |
| External-tool path change on running bridge | HOT scope. `OverrideUpdater.UpdateOverride(newPath)` mutates the resolver's override field; next ffmpeg/ffprobe/yt-dlp invocation picks it up. |
| Hand-edit of config.toml while bridge runs | Drawer rendered at page load shows stale values. Operator refreshes. Same behavior as `/ui/*`. |

## Testing

### Unit tests — `internal/chassis/settings_test.go`

Per-decoder:
- `decodeMisterHost` valid IPv4 → returns IPv4 string.
- `decodeMisterHost` valid hostname → returns hostname string.
- `decodeMisterHost` empty → error `"is required"`.
- `decodeMisterHost` invalid → error `"not a valid IPv4 or hostname"`.
- `decodePort` valid → returns int.
- `decodePort` zero / negative / >65535 → error.
- `decodeOptionalIPv4` empty → returns empty string (allowed).
- `decodeOptionalIPv4` invalid → error.
- `decodeOptionalAbsPath` empty → returns empty string.
- `decodeOptionalAbsPath` relative → error `"must be an absolute path"`.
- `decodeOptionalAbsPath` absolute → returns path.
- `decodeOptionalExecutablePath` empty → returns empty string.
- `decodeOptionalExecutablePath` relative → error `"must be an absolute path"`.
- `decodeOptionalExecutablePath` missing / directory / non-executable → error `"not a usable executable path"`.
- `decodeOptionalExecutablePath` temp executable file (or `.exe` on Windows) → returns path.

Total: ~22 small tests, one per decoder branch.

`buildSettingsData`:
- Empty registry → `AdapterCount == 0`.
- Registry with one adapter → `AdapterCount == 1`.
- Registry with one adapter + AUX → `AdapterCount == 1` (AUX excluded).
- Nil catalog viewer → `CatalogProviderCount == 0`.
- Catalog with 3 providers → `CatalogProviderCount == 3`.
- Bridge config passes through verbatim.

`field` helper output per type:
- text → contains `<input class="field-input" name="..." value="..." …>`, scope badge, no `has-err`.
- text with `Value != ""` → contains `has-value` class.
- text with `Error != ""` → outer `field-row has-err`, contains `.field-err`.
- number with `Unit != ""` → contains `.row-end` wrapper with unit + scope.
- select renders `<option selected>` matching `Value`.
- switch with `Value == "true"` → `<button class="switch on" data-field="…" aria-pressed="true">`.

`stub`, `errOf`, and `settingsScopeLabel` helpers each get one test.

### Handler tests — `internal/chassis/settings_test.go`

- `POST /receiver/settings/bridge` success, hot scope → 200, `BridgeSettingsSaver.Save` called with the full overlaid bridge config, response `{ok:true, scope:"hot"}`.
- `POST /receiver/settings/bridge` success, next-cast scope returned by mock saver → 200, response `{ok:true, scope:"next"}`.
- `POST /receiver/settings/bridge` success, restart-cast scope returned by mock saver → 200, response `{ok:true, scope:"recast"}`.
- `POST /receiver/settings/bridge` success, reboot scope → 200, response `{ok:true, scope:"reboot"}`.
- `POST /receiver/settings/bridge` mixed changed fields → mock saver returns max-wins scope; changed HOT + changed REBOOT → `scope:"reboot"`.
- `POST /receiver/settings/bridge` bad IPv4 → 400, `errors:{mister_host:"…"}`, saver NOT called.
- `POST /receiver/settings/bridge` two bad fields → 400, both errors in `errors` map.
- `POST /receiver/settings/bridge` empty body → 400, `chip:"BAD INPUT"`.
- `POST /receiver/settings/bridge` wrong origin → 403 (middleware).
- `POST /receiver/settings/bridge` mock saver returns preflight typed-error (chip=PORT IN USE, status=409) → 409 `chip:"PORT IN USE"`.
- `POST /receiver/settings/bridge` mock saver returns unexpected error → 500 `chip:"WRITE FAILED"`.
- `POST /receiver/settings/bridge` reads `BridgeSettingsSaver.Current()` for the base config, not startup `cfg.Bridge`.
- `POST /receiver/settings/bridge` nil saver → 503 `chip:"NOT READY"`.

Probe handler:
- `POST /receiver/settings/action/probe-mister` mock prober returns latency → 200, response shape includes `latency_ms`, `host`, `port`.
- `POST /receiver/settings/action/probe-mister` passes the current `config.BridgeConfig` from `BridgeSettingsSaver.Current()` to the prober; the prober uses `MiSTer.Host` and `MiSTer.Port` for the status probe and does not bind `MiSTer.SourcePort`.
- `POST /receiver/settings/action/probe-mister` mock prober returns context.DeadlineExceeded (or a normalized `groovynet.IsInitACKTimeout` path) → 200, `{ok:false, error:"timeout", elapsed_ms:1000}`.
- `POST /receiver/settings/action/probe-mister` mock prober returns socket error → 500 `error:"socket: …"`.
- `POST /receiver/settings/action/probe-mister` nil prober → 503 `chip:"NOT READY"`.
- `POST /receiver/settings/action/probe-mister` wrong origin → 403.

Saver-layer tests (unit, lives in `internal/uiserver/bridge_saver_test.go`):
- Validation/preflight failures return an error satisfying `StatusCode() int`, `Chip() string`, and `Unwrap()`.
- Bind failures map to `409 PORT IN USE`; data-dir writability failures map to `409 PATH NOT WRITABLE`; leftover config validation failures map to `400 BAD INPUT`.
- One save per Network field confirms the existing `Save()` return scope is correct.

### Template render tests — `internal/chassis/chassis_test.go`

- Drawer renders with 5 tab buttons and 5 panes from a known `SettingsData`.
- Adapters tab badge equals `AdapterCount`; Catalog tab badge equals `CatalogProviderCount`.
- Stub pane contains `"Spec 4X — implementation in progress"` text and the appropriate spec label.
- Probe action template renders `#probe-mister-btn` and an empty `#probe-mister-result`.
- Shell includes deferred `/receiver/static/settings-drawer.js?v=...`.
- Drawer renders a settings notice/toast target for chip and REBOOT messages.

### JS behavior — manual verification checklist

The chassis project has no JS test runner today (no jsdom, no Playwright, no Vitest), so these contracts are exercised manually against a running dev server until JS test infra is introduced (out of scope for 4A; a Phase 5 polish candidate). Treat this as a release-checklist the implementer walks before declaring 4A done:

- Gear button toggles `body.settings-open`; close button clears it.
- Tab click toggles active tab/pane without a network request (verify via DevTools Network panel).
- Blur on a changed text/path/number field sends exactly one form-encoded POST with the field name/value and `credentials:"same-origin"`; the client must **not** attempt to set `Sec-Fetch-Site` (browsers reject; manual check via Network panel that the request reaches the server).
- Switch click optimistically toggles and reverts on a forced 4xx (test by stubbing the route with `mister_host=` to force a 400).
- Field-error JSON paints `.field-err` and `.has-err`; success clears them and updates `.has-value`.
- Chip JSON and REBOOT success render into the drawer-local notice slot.
- Probe button single-flights, renders success/timeout/error into `.action-result`, and renders `chip` responses into the drawer-local notice slot.
- Network exceptions on save/probe surface an error in the notice slot and leave controls usable (test by killing the bridge process mid-edit).

### Integration tests — `tests/integration/chassis_test.go`

- Start chassis with real `BridgeSaver` and a tmp `config.toml`.
- `GET /receiver` → response body contains the drawer HTML, all 9 Network field rows, all 5 tab buttons.
- `POST /receiver/settings/bridge` with `mister_host=192.168.1.42`, valid → 200, `scope:"reboot"`, disk file contains the new value, in-memory `Sectioned.Bridge.MiSTer.Host` updated.
- `POST /receiver/settings/bridge` with `mister_host=` (cleared) → 400, `errors:{mister_host:"is required"}`, no disk write.
- `POST /receiver/settings/bridge` with `ffmpeg_path=<temp executable>`, valid → 200, `scope:"hot"`, `OverrideUpdater.UpdateOverride` was called with the new path.
- `POST /receiver/settings/action/probe-mister` via a fake prober that always succeeds → 200, latency populated.
- `POST /receiver/settings/action/probe-mister` via a fake prober that always times out → 200, `error:"timeout"`.

## Forward Compatibility

- **Phase 4B** (Pipeline + Advanced) reuses the same field helper, route shape, error envelope, scope dispatch, and toast pattern. Adds: one new action button (`launch-core`); ~20 new fields all using existing field types.
- **Phase 4C** (Catalog) adds the `restore-defaults` action button alongside a small provider-row partial (icon + meta + count + switch). The provider rows are sufficiently uniform that they can be a second template helper alongside `field`.
- **Phase 4D** (Adapters, simple cases) introduces `POST /receiver/settings/adapter/{name}` and the `AdapterSaver` integration. Same envelope; per-adapter scope dispatch. 4D mirrors 4A's pattern with a chassis-owned `AdapterSettingsSaver` interface that `*uiserver.AdapterSaver` satisfies from outside — `internal/chassis` continues to **not** import `internal/uiserver`, and the structural `settingsChipError` interface from 4A is reused for adapter-side preflight typed errors.
- **Phase 4E** (Plex / Jellyfin link cascades) adds a per-adapter "link state" sub-template and a small set of action routes (`/receiver/settings/adapter/plex/link`, `…/unlink`, `…/poll`). The state model is per-adapter; the wire envelope (`{ok, chip, …}`) is the same.
- **Phase 4F** (URL adapter custom widgets) adds the yt-dlp host tag-list and the cookies textarea. Both are bespoke widgets with their own minimal POST shapes; the JSON envelope still applies.
- **Final chassis cutover** retires `/ui/*` once 4F lands. The chassis settings drawer becomes the only settings surface. The `uiserver.{Bridge,Adapter}Saver` instances continue to exist (they're the saver layer regardless of UI); only the legacy `internal/ui/*` templates and routes are removed.
