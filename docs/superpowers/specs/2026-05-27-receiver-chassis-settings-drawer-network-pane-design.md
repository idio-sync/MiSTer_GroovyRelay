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
- [`internal/uiserver/bridge_saver.go`](../../../internal/uiserver/bridge_saver.go) — `BridgeSaver` already does diff-vs-current, preflight (UDP bind check for restart-bridge port changes), atomic write, and per-scope runtime dispatch (HotSwap / NextCast / RestartCast / RestartBridge). 4A wires the chassis routes through this saver unchanged.
- [`internal/uiserver/adapter_saver.go`](../../../internal/uiserver/adapter_saver.go) — `AdapterSaver` (reused starting in 4D for adapter section writes).
- 85+ `.settings-*` CSS selectors already in [`internal/chassis/static/chassis.css`](../../../internal/chassis/static/chassis.css). The mockup's CSS was ported during Phase 0 (foundation); 4A adds zero new CSS rules.

## Goals

1. **The gear button works.** Clicking `⚙ Setup` in the transport row toggles `body.settings-open`; the existing CSS expands the settings panel from collapsed to expanded. Closing via the `✕ Close` button or by clicking the gear again collapses it back.
2. **All five tabs visible from day one.** The tab strip renders all five tabs (Network, Pipeline, Adapters · N, Catalog · 3, Advanced) on first paint. Clicking a tab switches the active pane purely client-side; no server round trip.
3. **Network pane is fully functional.** All 9 bridge fields (MiSTer connection × 3, Bridge HTTP × 3, External tools × 3) render with their current values, auto-save on blur, validate inline, and dispatch through `BridgeSaver.Save()` with correct scope.
4. **One action button works.** "Test MiSTer connectivity" probes the live `groovynet.Sender` against the currently-saved `bridge.mister.{host,port,source_port}` and renders the result (latency + LZ4-Δ negotiation, or timeout, or socket error) in the `.action-result` slot under the button.
5. **The field renderer generalizes.** A single Go template helper (`field`) supports the five field types (text, number, password, path, select, switch) that Network + Pipeline + Adapters + Catalog + Advanced all use. 4B–4F add zero new field-type primitives.
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
- **Cross-field validation that requires the server.** All Network field validations are pure functions of the single field value (IPv4 format, port range, absolute-path check). Cross-field constraints (e.g., `mister_port ≠ ui_http_port` collision check) are out of scope; the existing `BridgeSaver.Save` preflight catches genuine port-bind conflicts via UDP-bind probes anyway.
- **First-run banner / setup wizard.** The `.first-run-complete` sentinel ([`internal/uiserver/bridge_saver.go:29`](../../../internal/uiserver/bridge_saver.go#L29)) stays the legacy UI's concern. The chassis settings drawer is just a settings drawer; a "first run" flow lives elsewhere if at all.
- **Mobile-responsive polish, accessibility audit beyond visible-focus and semantic HTML.** Foundation spec defers both to Phase 5.
- **Removing fields from the drawer.** No deprecation flow needed today.
- **Authentication.** LAN-only trust model, same as the rest of the chassis. Same-origin middleware is the boundary.

## Design Decisions

| Decision | Resolution |
|---|---|
| Drawer entry point | `⚙ Setup` button already present in [`internal/chassis/templates/transport.html`](../../../internal/chassis/templates/transport.html). 4A wires its `onclick` via a `data-settings-toggle` attribute (additive to the existing id; no breaking changes to existing tests). |
| Drawer open/close mechanism | Toggle `body.settings-open` class. CSS at [v24:1848-1873](../reference/2026-05-21-receiver-v24.html#L1848-L1873) already drives the collapse/expand transition. No JavaScript animations. |
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
| Whole-form error UX | Toast via existing chassis toast surface, chip text per error (e.g. `BAD INPUT`, `PORT IN USE`, `WRITE FAILED`). Field-row stays marked with the error until next successful save attempt. |
| REBOOT-scope success UX | Toast: `"Restart container to apply new <field-label>"`. Field renders with the new value (input already holds what was typed). Disk has the new value; live state has the old. No persistent banner in 4A. |
| Probe action target | Uses currently-saved `bridge.mister.{host,port,source_port}`, not form values. Operator must save before probing. Probe button's help text already says "Verifies the address + ports above" — honest about this. |
| Probe action timeout | Hard 1s server-side timeout, matching the mockup's `"ACK in 4.2ms"` example and the existing `groovynet.Sender.Probe` semantics if any. Single-flight per click (client disables button while in-flight). |
| Probe response shape | 200 on **both** success and timeout — the probe itself ran cleanly in both cases. 5xx is reserved for socket errors (cannot construct probe). Distinct from operational ack-or-not. |
| Field renderer surface | One template helper `field` taking a `dict`-style option bag. Supports text / number / password / path / select / switch. No bespoke helpers per type — option-bag pattern is verbose but readable in templates. |
| Stub pane shape | Single `<div class="settings-section wide">` with an h4 and an `.action-result.shown` line saying which spec covers it. No interactive content. Identical structure across all four stubs. |
| `internal/chassis/import_check_test.go` | Forbidden-imports list unchanged. 4A imports `internal/config` (already allowed for the data model) and `internal/uiserver` (new — `BridgeSaver` and adjacent types). |
| CSS additions | Zero new rules expected. Verify during plan that `.scope.next` either exists already or borrows from `.scope.recast`. If it doesn't exist, one new rule is the entire CSS surface of 4A. |
| Switch field POST shape | Form value `"true"` / `"false"`. Client renders `.switch.on` / `.switch` based on response (`.has-value` is not relevant for switches). Optimistic toggle on click, revert on 4xx. |

## Implementation Checklist (sketch — implementation plan elaborates)

- `internal/chassis/settings.go` (new): `handleSettingsBridgePost`, `handleSettingsActionProbeMister`, the per-field decoder table, error envelope writers (`writeSettingsFieldErrors`, `writeSettingsChip`, `writeSettingsSuccess`, `writeProbeResult`).
- `internal/chassis/settings_test.go` (new): handler unit tests for every success/error branch listed in §Wire Contract.
- `internal/chassis/templates/settings-drawer.html` (rewrite from current 8-line stub): top-level drawer, 5-tab strip, 5 panes (Network + 4 stubs).
- `internal/chassis/templates/settings-field.html` (new): the one row template that `field` helper expands into. (Or implemented as inline `{{- define -}}` blocks in `settings-drawer.html` — implementation plan picks.)
- `internal/chassis/static/settings-drawer.js` (new): gear button + close button toggle, tab switching, field blur → POST, switch click → POST, response handling (success / field-error / chip-error), action button click handler with single-flight, render probe result.
- `internal/chassis/data.go` (extend): grow `SettingsData` from `{Open bool}` to `{Open, Bridge config.BridgeConfig, Errors map[string]string, AdapterCount, CatalogProviderCount int}`; add `buildSettingsData(bridge, registry, catalogViewer)` helper.
- `internal/chassis/session.go` (extend): `snapshotFromStatusView` and `idleSnapshot` both call `buildSettingsData` and populate `snapshot.Settings`.
- `internal/chassis/server.go` (extend): add `Config.BridgeSaver *uiserver.BridgeSaver` + `Config.Prober Prober` (new narrow interface); store on `*Server`; mount the two new routes.
- `internal/chassis/templates.go` (extend): register `field`, `dict`, `stub`, `errOf`, `itoa` (or `tos` if more general) helpers in the template FuncMap.
- `internal/chassis/templates/transport.html` (one-line edit): add `data-settings-toggle` attribute to the gear button. Existing `id="gear-btn"` stays for any test references.
- `internal/chassis/chassis_test.go` (extend): template render tests for drawer, Network pane, field helper per type, stub pane, scope badge variants.
- `internal/chassis/static/chassis.css` (one rule possibly): `.scope.next` color if not already present.
- `internal/uiserver/bridge_saver.go` (extend): add additive `ScopeForField(name string) (adapters.ApplyScope, bool)` method. Same per-field table the saver uses internally for dispatch; exposed publicly so the chassis handler can compute the scope it returns to the client without duplicating the table. `Save()` signature unchanged.
- `internal/uiserver/bridge_saver_test.go` (extend): one test per Network field confirming the right scope; one test confirming unknown field returns `(_, false)`.
- `cmd/mister-groovy-relay/main.go` (extend): construct `BridgeSaver` (already constructed for `/ui/*` — same instance reused), pass into `chassis.Config`; construct a `Prober` (wraps existing `groovynet.Sender` probe capability) and pass it.
- `tests/integration/chassis_test.go` (extend): end-to-end save + probe coverage, see §Testing.

**Files intentionally unchanged in 4A:**
- `internal/ui/*` and `internal/uiserver/*` — 4A consumes `uiserver.BridgeSaver` unchanged. `/ui/*` keeps working.
- `internal/core/*` — no new core surface.
- `internal/adapters/*` — no new adapter interfaces in 4A. Adapter-touching specs are 4D–4F.

## Wire Contract — HTTP Routes

### `POST /receiver/settings/bridge`

**Headers:** `Sec-Fetch-Site: same-origin` (enforced by `requireSameOrigin`).

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
5. Apply decoded values to a copy of the current `config.BridgeConfig`. Compute the patch (full BridgeConfig with touched fields overlaid).
6. Call `BridgeSaver.Save(patch)`.
7. On `*adapters.QuickCastError`-style typed error (the saver layer reuses this envelope for preflight failures): map status + chip directly into the response (e.g., `409 {ok:false, chip:"PORT IN USE"}`).
8. On unexpected saver error: 500 `{ok:false, chip:"WRITE FAILED"}`.
9. On success: derive the max-wins scope. 4A adds one small additive method to `BridgeSaver` — `ScopeForField(name string) (adapters.ApplyScope, bool)` — which the saver computes from the same per-field switch statement that drives its existing internal dispatch. The chassis handler walks the touched-field set, calls `ScopeForField` for each, takes the max, and returns the resulting string label. No change to existing `BridgeSaver.Save` signature; `/ui/*` callers are not affected. Respond `200 {ok:true, scope:"hot|next|recast|reboot"}`.

**Responses:**

| Status | Body | When |
|---|---|---|
| 200 | `{"ok":true,"scope":"hot"}` | Touched fields all hot-swappable |
| 200 | `{"ok":true,"scope":"reboot"}` | At least one touched field is REBOOT scope |
| 400 | `{"ok":false,"errors":{"mister_host":"not a valid IPv4 or hostname"}}` | One or more per-field validation failures |
| 400 | `{"ok":false,"chip":"BAD INPUT"}` | Form parse failure or empty body |
| 403 | (middleware) | Wrong origin |
| 409 | `{"ok":false,"chip":"PORT IN USE"}` | Preflight UDP-bind probe failed for a port change |
| 500 | `{"ok":false,"chip":"WRITE FAILED"}` | Disk write or unexpected saver error |

### `POST /receiver/settings/action/probe-mister`

**Headers:** `Sec-Fetch-Site: same-origin`.

**Body:** Empty (action button has no parameters).

**Server logic:**

1. `requireSameOrigin` middleware.
2. If `s.cfg.Prober == nil` → 503 `{ok:false, chip:"NOT READY"}`.
3. Read current `bridge.mister.{host,port,source_port}` from in-memory `Sectioned`.
4. Call `prober.ProbeMister(ctx, host, port)` with a 1s context timeout. The prober uses the live `groovynet.Sender` (which has the bound source port) and waits for ACK.
5. On success: `200 {ok:true, latency_ms:4.2, host:"192.168.1.42", port:32100, lz4_negotiated:true}`.
6. On context-deadline-exceeded: `200 {ok:false, error:"timeout", elapsed_ms:1000}`. Distinct from operational failure — the probe ran cleanly; the MiSTer didn't answer.
7. On any other error (socket bind failure, malformed packet, etc.): `500 {ok:false, error:"<sanitized message>"}`. The chassis sanitizes error strings to avoid leaking host details into logs (mirrors existing transport error sanitization in 3B).

**Responses:**

| Status | Body | When |
|---|---|---|
| 200 | `{"ok":true,"latency_ms":4.2,"host":"192.168.1.42","port":32100,"lz4_negotiated":true}` | MiSTer ACKed |
| 200 | `{"ok":false,"error":"timeout","elapsed_ms":1000}` | Probe ran cleanly, no ACK in 1s |
| 403 | (middleware) | Wrong origin |
| 500 | `{"ok":false,"error":"socket: <message>"}` | Could not run the probe |
| 503 | `{"ok":false,"chip":"NOT READY"}` | `Prober` not wired (defensive; main.go always wires) |

Client renders into `#probe-mister-result` in the `.action-result` slot under the button:

- Success: `▸ ACK in {{latency_ms}}ms · MiSTer {{host}}:{{port}}{{ if .lz4_negotiated }} · LZ4-Δ negotiated{{ end }}`
- Timeout: `▸ NO ACK · {{elapsed_ms}}ms timeout · check host/port`
- Error: `▸ ERROR · {{error}}`

The `.shown` CSS class gates visibility; `.action-result.shown.ok` and `.action-result.shown.err` provide success/error coloring (CSS already in place per mockup port).

## Architecture — Data Flow

**At page load (`GET /receiver`):**

```
chassis shell renders
  └─ snapshotFromStatusView populates SnapshotData.Settings via buildSettingsData
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
    "Error" (errOf "mister_host")
) }}
```

`dict` is a local 6-line template helper (`func(pairs ...any) map[string]any`). `errOf` is a closure baked into the template's FuncMap that reads from `.Settings.Errors[name]`. `itoa` is `strconv.Itoa` wrapped for the FuncMap.

**Output HTML by type:**

| Type | Middle slot |
|---|---|
| `text` | `<input class="field-input{{ if .Value }} has-value{{ end }}" name="{{.Name}}" value="{{.Value}}" placeholder="{{.Placeholder}}">` |
| `number` | `<input class="field-input num{{ if .Value }} has-value{{ end }}" type="number" name="{{.Name}}" value="{{.Value}}"{{ if .InputWidth }} style="max-width:{{.InputWidth}}"{{ end }}>`, optionally wrapped in `<span class="row-end">…unit + scope</span>` |
| `password` | `<input class="field-input has-value" type="password" name="{{.Name}}" value="{{.Value}}">` |
| `path` | `<input class="field-input path{{ if .Value }} has-value{{ end }}" name="{{.Name}}" value="{{.Value}}" placeholder="{{.Placeholder}}">` |
| `select` | `<select class="field-input has-value" name="{{.Name}}">…<option value="{{.Value}}"{{ if eq .Value $.Value }} selected{{ end }}>{{ or .Label .Value }}</option>…</select>` |
| `switch` | `<button class="switch{{ if eq .Value "true" }} on{{ end }}" data-field="{{.Name}}" type="button" aria-pressed="{{ eq .Value "true" }}"></button>` |

All types render the outer `<div class="field-row{{ if .Error }} has-err{{ end }}">` with the `<label>…</label>` first and `<span class="scope {{.Scope}}">{{ upper .Scope }}</span>` last. When `Error != ""`, an additional `<div class="field-err">{{.Error}}</div>` renders between input and scope.

**Why option-bag instead of positional args:** Go templates don't support named parameters. Positional with 10+ args is illegible. Option-bag via `dict` is verbose at the call site but every field is self-documenting and adding a new optional knob doesn't break existing callers.

## Network Pane Fields

### Section: MiSTer connection `[bridge.mister]`

| Form name | Type | Label | Help | Scope | Default | Validation |
|---|---|---|---|---|---|---|
| `mister_host` | text | Host | IP or hostname of your MiSTer on the LAN. | REBOOT | — (operator must set) | non-empty; must parse as IPv4 (`net.ParseIP`) **or** as a DNS hostname (RFC-952 char check). Error: `"not a valid IPv4 or hostname"` |
| `mister_port` | number | Port | UDP port the MiSTer's Groovy core listens on. | REBOOT | 32100 | int in `[1, 65535]`. Error: `"port out of range (1-65535)"` |
| `mister_source_port` | number | Source port | Our stable source UDP port. Must stay the same across restarts. | REBOOT | 32101 | int in `[1, 65535]`. Error: `"port out of range (1-65535)"` |

Followed by the **probe-mister** action button row (see §Wire Contract).

### Section: Bridge HTTP `[bridge.ui]`

| Form name | Type | Label | Help | Scope | Default | Validation |
|---|---|---|---|---|---|---|
| `ui_http_port` | number | HTTP port | Plex Companion HTTP + Settings UI (shared listener). | REBOOT | 32500 | int in `[1, 65535]`. Saver preflight then validates bindability on `0.0.0.0:<port>`; on bind failure responds `409 {chip:"PORT IN USE"}`. |
| `host_ip` | text | Host IP | LAN IP advertised to Plex. Leave blank to auto-detect. | REBOOT | `""` (placeholder shows `auto-detect`) | empty allowed; if non-empty, must parse as IPv4. Error: `"not a valid IPv4 address"` |
| `data_dir` | path | Data directory | Where plex.json and other persistent state live. Leave empty for OS default. | REBOOT | `""` (placeholder shows `auto`) | empty allowed; if non-empty, must be absolute (`filepath.IsAbs`). Error: `"must be an absolute path"`. No existence check. |

### Section: External tools (override sidecar paths)

| Form name | Type | Label | Help | Scope | Default | Validation |
|---|---|---|---|---|---|---|
| `ffmpeg_path` | path | FFmpeg path | Empty = bundled sidecar, then PATH. | HOT | `""` (placeholder `auto`) | empty allowed; if non-empty, must be absolute. |
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
    adapterCount := 0
    for _, a := range registry.List() {
        if a.Name() == "aux" {
            continue // AUX is not a configurable adapter in the chassis sense
        }
        adapterCount++
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

Both `snapshotFromStatusView` and `idleSnapshot` call `buildSettingsData(s.cfg.Sectioned.Bridge, s.cfg.Registry, s.cfg.StreamsCatalogViewer)` and assign to `snapshot.Settings`.

### `internal/chassis/server.go`

```go
type Config struct {
    // … existing fields …

    BridgeSaver *uiserver.BridgeSaver // 4A: bridge field saves
    Prober      Prober                // 4A: MiSTer connectivity probe
}

// Prober is the narrow chassis-side interface the probe-mister action uses.
// Implemented by a small wrapper around the existing groovynet.Sender; lives
// in main.go construction so the chassis doesn't depend on groovynet.
type Prober interface {
    ProbeMister(ctx context.Context, host string, port int) (ProbeResult, error)
}

type ProbeResult struct {
    LatencyMs     float64
    Host          string
    Port          int
    LZ4Negotiated bool
}
```

New mux routes in `Server.RegisterRoutes`:

```go
mux.HandleFunc("/receiver/settings/bridge",
    s.requireSameOrigin(s.handleSettingsBridgePost))
mux.HandleFunc("/receiver/settings/action/probe-mister",
    s.requireSameOrigin(s.handleSettingsActionProbeMister))
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
    const res = await fetch('/receiver/settings/bridge',
      { method: 'POST', body: new URLSearchParams(form), headers: { 'Sec-Fetch-Site': 'same-origin' } });
    const body = await res.json().catch(() => ({}));
    if (res.ok && body.ok) {
      markFieldHasValue(name, value);
      if (body.scope === 'reboot') toastReboot(name);
      return;
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
    const res = await fetch('/receiver/settings/action/probe-mister',
      { method: 'POST', headers: { 'Sec-Fetch-Site': 'same-origin' } });
    const body = await res.json().catch(() => ({}));
    renderProbeResult(probeOut, body);
    probeBtn.disabled = false;
  });

  // Helpers (paintFieldErrors, clearFieldErrors, markFieldHasValue,
  // markFieldHasErr, renderProbeResult, toastChip, toastReboot) are small
  // DOM manipulations; spelled out in the implementation plan.
})();
```

The toast helpers reuse the existing chassis toast surface (created by 3A's input-cast.js). 4A adds nothing to that surface; it just calls into it.

## Edge Cases

| Case | Behavior |
|---|---|
| Probe vs in-flight cast | Probe packet goes out on the live `groovynet.Sender` — MiSTer sees a normal packet from the existing session and ACKs it without dropping. No quiescing of the data plane needed. |
| Concurrent edits across two browser tabs | `BridgeSaver.mu` serializes; second write wins. Stale tab is stale until refresh. No optimistic-locking machinery. |
| REBOOT save + further edits to other fields | Each save toasts independently. Multiple REBOOT toasts stack/replace per the existing chassis toast surface (chassis toasts stack — visible queue at top-right). |
| Switch optimistic toggle on 4xx | Client reverts the `.on` class and `aria-pressed`, then renders the field-error (`.field-err`) on the row. The field helper's error slot renders for every type uniformly when `Error != ""` (see §Architecture — The Field Renderer); switches use the same DOM as text/number fields. |
| Single-flight on probe button | Client disables the button while a request is in flight; re-enables on response. Prevents double-click stacking. |
| Field value with HTML metacharacters (`<`, `>`, `&`) | Go `html/template` auto-escapes `{{.Value}}` in both element content and `value="…"` attribute contexts. Server-side. |
| Probe after edit but before save | The just-typed (unsaved) value is **ignored** by the probe — it uses the currently-saved config. The help text "Verifies the address + ports above" honestly describes this. Operator must save first. |
| `data_dir` change on running bridge | REBOOT scope. Disk records new path; live bridge keeps using the old path until restart. No file migration. |
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

Total: ~18 small tests, one per decoder branch.

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

`stub` and `errOf` helpers each get one test.

### Handler tests — `internal/chassis/settings_test.go`

- `POST /receiver/settings/bridge` success, hot scope → 200, `BridgeSaver.Save` called with diff, response `{ok:true, scope:"hot"}`.
- `POST /receiver/settings/bridge` success, reboot scope → 200, response `{ok:true, scope:"reboot"}`.
- `POST /receiver/settings/bridge` mixed scopes → max-wins; touched HOT + REBOOT → `scope:"reboot"`.
- `POST /receiver/settings/bridge` bad IPv4 → 400, `errors:{mister_host:"…"}`, saver NOT called.
- `POST /receiver/settings/bridge` two bad fields → 400, both errors in `errors` map.
- `POST /receiver/settings/bridge` empty body → 400, `chip:"BAD INPUT"`.
- `POST /receiver/settings/bridge` wrong origin → 403 (middleware).
- `POST /receiver/settings/bridge` mock saver returns preflight typed-error (chip=PORT IN USE, status=409) → 409 `chip:"PORT IN USE"`.
- `POST /receiver/settings/bridge` mock saver returns unexpected error → 500 `chip:"WRITE FAILED"`.

Probe handler:
- `POST /receiver/settings/action/probe-mister` mock prober returns latency → 200, response shape includes `latency_ms`, `host`, `port`, `lz4_negotiated`.
- `POST /receiver/settings/action/probe-mister` mock prober returns context.DeadlineExceeded → 200, `{ok:false, error:"timeout", elapsed_ms:1000}`.
- `POST /receiver/settings/action/probe-mister` mock prober returns socket error → 500 `error:"socket: …"`.
- `POST /receiver/settings/action/probe-mister` nil prober → 503 `chip:"NOT READY"`.
- `POST /receiver/settings/action/probe-mister` wrong origin → 403.

### Template render tests — `internal/chassis/chassis_test.go`

- Drawer renders with 5 tab buttons and 5 panes from a known `SettingsData`.
- Adapters tab badge equals `AdapterCount`; Catalog tab badge equals `CatalogProviderCount`.
- Stub pane contains `"Spec 4X — implementation in progress"` text and the appropriate spec label.
- Probe action template renders `#probe-mister-btn` and an empty `#probe-mister-result`.

### Integration tests — `tests/integration/chassis_test.go`

- Start chassis with real `BridgeSaver` and a tmp `config.toml`.
- `GET /receiver` → response body contains the drawer HTML, all 9 Network field rows, all 5 tab buttons.
- `POST /receiver/settings/bridge` with `mister_host=192.168.1.42`, valid → 200, `scope:"reboot"`, disk file contains the new value, in-memory `Sectioned.Bridge.MiSTer.Host` updated.
- `POST /receiver/settings/bridge` with `mister_host=` (cleared) → 400, `errors:{mister_host:"is required"}`, no disk write.
- `POST /receiver/settings/bridge` with `ffmpeg_path=/usr/local/bin/ffmpeg`, valid → 200, `scope:"hot"`, `OverrideUpdater.UpdateOverride` was called with the new path.
- `POST /receiver/settings/action/probe-mister` via a fake prober that always succeeds → 200, latency populated.
- `POST /receiver/settings/action/probe-mister` via a fake prober that always times out → 200, `error:"timeout"`.

## Forward Compatibility

- **Phase 4B** (Pipeline + Advanced) reuses the same field helper, route shape, error envelope, scope dispatch, and toast pattern. Adds: one new action button (`launch-core`); ~20 new fields all using existing field types.
- **Phase 4C** (Catalog) adds the `restore-defaults` action button alongside a small provider-row partial (icon + meta + count + switch). The provider rows are sufficiently uniform that they can be a second template helper alongside `field`.
- **Phase 4D** (Adapters, simple cases) introduces `POST /receiver/settings/adapter/{name}` and the `AdapterSaver` integration. Same envelope; per-adapter scope dispatch.
- **Phase 4E** (Plex / Jellyfin link cascades) adds a per-adapter "link state" sub-template and a small set of action routes (`/receiver/settings/adapter/plex/link`, `…/unlink`, `…/poll`). The state model is per-adapter; the wire envelope (`{ok, chip, …}`) is the same.
- **Phase 4F** (URL adapter custom widgets) adds the yt-dlp host tag-list and the cookies textarea. Both are bespoke widgets with their own minimal POST shapes; the JSON envelope still applies.
- **Final chassis cutover** retires `/ui/*` once 4F lands. The chassis settings drawer becomes the only settings surface. The `uiserver.{Bridge,Adapter}Saver` instances continue to exist (they're the saver layer regardless of UI); only the legacy `internal/ui/*` templates and routes are removed.
