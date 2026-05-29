# Receiver Chassis Adapters Pane (Simple) — Phase 4D Design

**Status:** Brainstormed; code-review fixes applied; awaiting implementation plan.

**Scope:** Fourth sub-spec of Phase 4 (Settings & Adapters). Replaces the single "Spec 4D–4F — implementation in progress" stub that ships in [Phase 4A](2026-05-27-receiver-chassis-settings-drawer-network-pane-design.md) and survives [Phase 4B](2026-05-27-receiver-chassis-pipeline-advanced-panes-design.md)/[Phase 4C](2026-05-28-receiver-chassis-catalog-pane-design.md) with a six-section Adapters pane: three fully functional adapter forms (DLNA, Torrent, Streams catalog) and three 4A-style per-adapter stubs (Plex, Jellyfin, URL) that 4E/4F replace. Adds one save route (`POST /receiver/settings/adapter/{name}`), one action route (`POST /receiver/settings/action/streams-refresh`), two chassis-owned interfaces (`AdapterSettingsSaver`, `StreamsRefresher`), three per-adapter section templates plus one container template, two CSS rules (`.settings-section .hint`, `.settings-subhead`), and two production wrappers in `cmd/mister-groovy-relay/`. Extends `internal/uiserver/adapter_saver.go` with one additive method (`SaveTouched`) paralleling 4A's `BridgeSaver.SaveTouched` so per-field auto-save composes correctly under the existing shared saver mutex. Reuses every other primitive 4A/4B/4C established without modification.

**Repo location:** Lives under `docs/superpowers/specs/`. This subtree is ignored by default, so the spec must be force-added (`git add -f`) when committing per the receiver chassis rollout convention.

## Context

[Phase 4A](2026-05-27-receiver-chassis-settings-drawer-network-pane-design.md) shipped the settings drawer chrome, the Network pane (9 bridge fields + probe-mister action), the `field` template helper covering six input types, the JSON error envelope (`{ok, scope, errors, chip, error}`), the 4-tier scope vocabulary (HOT/NEXT/RECAST/REBOOT), the drawer-local `.settings-notice` slot, the `BridgeSettingsSaver`/`Prober`/`settingsChipError` chassis-owned interfaces, and the additive `*uiserver.settingsError` typed wrapper.

[Phase 4B](2026-05-27-receiver-chassis-pipeline-advanced-panes-design.md) shipped the Pipeline pane (9 fields + launch-core action) and the Advanced pane's HLS-buffer (×11) + Logging (×1) sections, the `CoreLauncher` interface, the `humanizeBytes` / `boolStr` / `i64toa` / `passwordPlaceholder` / `options` / `list` helpers, and the `SkipEmpty` field-renderer flag.

[Phase 4C](2026-05-28-receiver-chassis-catalog-pane-design.md) ships the Catalog pane (per-provider enabled toggles + global direct-stream HLS-buffer override), the Advanced pane's Diagnostics section with the restore-defaults action, the `CatalogSettingsManager` + `ConfigReset` interfaces, the inline two-step confirm pattern, and the `Origin`/`Kind` fields on `adapters.CatalogProvider`.

**Implementation precondition:** 4D lands on top of the completed 4C code, not directly on the current pre-4C tree. If implementing from a branch where `CatalogSettingsManager`, `SettingsData.CatalogChannelCount`, `CatalogProviderState`, or `adapters.CatalogProvider.Origin/Kind` are missing, land 4C first (or include those 4C contracts in the same preparatory commit) before starting this spec. 4D intentionally consumes those contracts; it does not redefine them.

What 4A/4B/4C leave behind: a working drawer with four real panes (Network, Pipeline, Catalog, Advanced) and one stub pane labelled "Spec 4D–4F — implementation in progress" at the Adapters slot. 4D fills in that pane for the three adapters that have no link-state cascade (DLNA, Streams, Torrent), and renders 4A-style stubs for the three that do (Plex, Jellyfin, URL).

The three target adapters already own everything 4D consumes:

- [`internal/adapters/dlna/`](../../../internal/adapters/dlna/) — 4 fields, single-table `Validate()`, no `bridge.DataDir` dependency.
- [`internal/adapters/torrent/`](../../../internal/adapters/torrent/) — 10 fields, range-bound `validateConfig` requiring `bridge.DataDir` for the `download_dir` path check.
- [`internal/adapters/streams/`](../../../internal/adapters/streams/) — 15 top-level fields + dynamic per-provider rows (3 fields per provider) appended by `Fields()`, with a per-field `fieldScopes` table at [config.go:234-250](../../../internal/adapters/streams/config.go#L234-L250).

The existing [`internal/uiserver/adapter_saver.go`](../../../internal/uiserver/adapter_saver.go) implements full-section saves: `AdapterSaver.Save(name, rawTOMLSection []byte)` overwrites the entire `[adapters.<name>]` block via line-level rewrite, holding the same shared mutex `BridgeSaver` uses. The legacy `/ui/*` form submits the complete schema per save (operator clicks "Save", all `Fields()` keys come back in `r.Form`), so the legacy `formToAdapterTOML` at [internal/ui/adapter.go:429](../../../internal/ui/adapter.go#L429) serializes the full section trivially. **4D's per-field auto-save shape has no full schema in the POST body** — only the touched key arrives. 4D therefore extends `AdapterSaver` with one additive method `SaveTouched(name string, touched map[string]string, adapter adapters.Adapter, fields []adapters.FieldDef) (adapters.ApplyScope, error)` that mirrors `BridgeSaver.SaveTouched`'s pattern: takes the shared mutex; reads the current `[adapters.<name>]` section plus descendant subtables from disk (the saver owns the file path); decodes the current section into a full TOML tree that preserves keys not rendered by 4D (for example Streams provider `channels` and channel-level overrides); overlays only the touched values type-checked against the provided writable `FieldDef` surface; re-serializes the complete section; validates via `adapters.Validator` when implemented; writes atomically; then calls the registered adapter's `ApplyConfig` for runtime dispatch. The chassis-owned `AdapterSettingsSaver` interface (introduced in §Architecture) is a thin wrapper around this. `internal/chassis` continues to not import `internal/uiserver` or any concrete adapter package.

**Mockup reference:** [`docs/superpowers/reference/2026-05-21-receiver-v24.html`](../reference/2026-05-21-receiver-v24.html). Adapters pane lives at lines [4272-4530](../reference/2026-05-21-receiver-v24.html#L4272-L4530); DLNA section at [4339-4361](../reference/2026-05-21-receiver-v24.html#L4339-L4361); Torrent section at [4414-4471](../reference/2026-05-21-receiver-v24.html#L4414-L4471); Streams catalog section at [4512-4529](../reference/2026-05-21-receiver-v24.html#L4512-L4529); section `.hint` styling visible inline on each `<h4>`.

**What changed since 4C** (everything else is reuse):

| Change | Scope |
|---|---|
| New save route `POST /receiver/settings/adapter/{name}` | One handler dispatching by adapter name; reuses 4A field decoders |
| New action route `POST /receiver/settings/action/streams-refresh` | One handler invoking `StreamsRefresher.RefreshNow` (single manifest-refresh call) |
| New chassis-owned interfaces `AdapterSettingsSaver` (3 methods) + `StreamsRefresher` (1 method) | Both satisfied from outside (production wrappers in `cmd/mister-groovy-relay/`) |
| New chassis-owned struct `StreamsRefreshResult` | Return shape for the streams-refresh action — scalar status (`Source`, `DurationMS`, `Err`), not per-provider slice |
| **New `internal/uiserver/adapter_saver.go` method `SaveTouched`** | Additive; mirrors 4A's `BridgeSaver.SaveTouched`. Read-current full section + descendant subtables → overlay touched keys → re-encode complete section → validate → write atomically → apply runtime config under the shared saver mutex. The chassis cannot get this property by composing existing methods (no read-current accessor on the saver, and the legacy `Save(name, fullSection)` taking a full schema body would race other concurrent auto-saves) — the new method is the structural fix. |
| New template `settings-adapters.html` | Container; emits six `<section>` blocks in mockup order |
| New per-adapter templates `settings-adapter-dlna.html` / `settings-adapter-torrent.html` / `settings-adapter-streams.html` | One per real adapter; stubs inlined into the container |
| Two new CSS rules `.settings-section .hint` (ported from mockup) and `.settings-subhead` (new for Streams' per-provider sub-section) | ~10 lines total, scoped under `body.receiver` |
| Two production wrappers in `cmd/mister-groovy-relay/` (`adapter_settings_saver.go`, `streams_refresher.go`) | Glue plus adapter dispatch and a single manifest-refresh entry point |
| Client JS extension for `button.switch[data-adapter]` + `input.field-input[data-adapter]` save handlers | Sibling to the 4A `data-field` handler; same shape, different route prefix. Also narrows the existing bridge text/select selector to `[data-field]` so adapter blurs cannot post to `/receiver/settings/bridge`. |

**4A/4B/4C patterns 4D reuses verbatim (do not redesign):**

- `field` template helper with six types (text / number / password / path / select / switch) and the option-bag `dict` calling convention. **All four primitives used by 4D's three adapters fit into text / number / path / switch — no new field type.**
- Per-field auto-save: blur on text/number/path, change on select, click on switch.
- JSON envelopes — settings save: `{ok:true, scope:"hot|next|recast|reboot"}`, `{ok:false, errors:{<field>:"<msg>"}}`, `{ok:false, chip:"<chip>"}`; actions: `{ok:true, ...}`, `{ok:false, error:"<msg>"}`, `{ok:false, chip:"NOT READY"}` for wiring failures. HTTP statuses match the existing 4A handlers: validation errors use 400, typed chip errors use their `StatusCode()`, and successful saves/actions use 200.
- Route prefix: `POST /receiver/settings/adapter/<name>` accepts any subset of the adapter's 4D writable field surface (for Streams: all 15 top-level fields plus only `providers.<id>.catalog_refresh_hours`); `POST /receiver/settings/action/<name>` for action buttons.
- `requireSameOrigin` middleware on every save and action route.
- `settingsChipError` structural interface (4D's `AdapterSettingsSaver` returns errors implementing it identically to `BridgeSettingsSaver`).
- Drawer-local `.settings-notice` slot for chip/REBOOT toasts; `.field-row .field-err` for per-field errors.
- `scopeLabel` helper for `adapters.ApplyScope` → wire label mapping.
- `humanizeBytes` for Torrent's `max_cache_bytes` and Streams' `max_manifest_bytes` / `max_catalog_bytes` row-end hints.
- `internal/chassis/import_check_test.go` continues to forbid `internal/ui`, `internal/uiserver`, `internal/misterctl`, and every concrete adapter package. 4D **adds no new entries** — `AdapterSettingsSaver` is satisfied from `cmd/mister-groovy-relay/` (which already imports `internal/uiserver`), and `StreamsRefresher` is satisfied by another `cmd/` wrapper that imports `internal/adapters/streams`.

## Goals

1. **DLNA section fully functional.** All 4 fields (`enabled`, `device_name`, `autoplay_on_set_uri`, `allow_public_source_urls`) render with current values, autosave on blur/click, validate inline against `dlna.Config.Validate`, and dispatch through `AdapterSettingsSaver.SaveTouched` with the declared scope from `dlna.Adapter.Fields()`. `device_name` is the only REBOOT-scope field in 4D and exercises the `Restart container to apply` toast pattern from 4A.

2. **Torrent section fully functional.** All 10 fields render with current values and autosave with the declared scope (HOT for `enabled` / `traffic_acknowledged` / `keep_completed` / `max_cache_bytes` / `metadata_timeout_seconds` / `startup_buffer_seconds`; RECAST for `download_dir` / `max_upload_rate_kbps` / `max_download_rate_kbps` / `listen_port`). Numeric validation surfaces as per-field `errors` messages from `torrent.validateConfig`. `max_cache_bytes` renders with a `humanizeBytes` hint in `.row-end`. The mockup's `Max .torrent file size` row is dropped — no corresponding `Config` field exists.

3. **Streams section fully functional.** All 15 top-level fields render with autosave. The per-provider sub-section under a `.settings-subhead` heading shows one row per registered provider with the provider's `catalog_refresh_hours` override (the only per-provider field 4C deferred to 4D — `disabled` and `hls_buffer_disabled` stay owned by 4C's Catalog pane). A `↻ Refresh manifest now` action button at the bottom of the section invokes `StreamsRefresher.RefreshNow` (a single call to `streams.Adapter.RefreshNow(ctx, "")` — which is the canonical manifest-refresh entry point and what ripples to catalogs) and renders the scalar status (`source` / `duration_ms` / `error`) in the row's `.action-result` slot. No per-provider fan-out; `streams.Adapter.RefreshNow(ctx, providerID)` only refreshes one provider's catalog (not the manifest) and is not the right primitive for this button.

4. **Three stub adapter sections render.** Plex, Jellyfin, and URL appear in mockup order as `<section class="settings-section"><h4>{name} <span class="hint">— pending</span></h4><div class="action-result shown">Spec 4E — implementation in progress</div></section>` (Plex / Jellyfin) or `Spec 4F` (URL). Matches 4A's stub convention so the operator can see the roadmap; stubs are deleted when 4E/4F lands.

5. **Two new chassis-owned interfaces; zero new chassis-forbidden imports.** `AdapterSettingsSaver` (three methods: `Current`, `Fields`, `SaveTouched`) and `StreamsRefresher` (one method: `RefreshNow`) are declared in [internal/chassis/settings.go](../../../internal/chassis/settings.go) alongside the 4A/4B/4C interfaces. Both are satisfied from `cmd/mister-groovy-relay/`. `internal/chassis` continues to have zero imports of `internal/uiserver`, `internal/misterctl`, or any concrete adapter package. The forbidden-imports test at [internal/chassis/import_check_test.go](../../../internal/chassis/import_check_test.go) keeps its existing entries; 4D adds none.

6. **Visual fidelity to the v24 mockup, mockup-vs-code scope mismatches explicitly flagged.** The Adapters pane structure (six stacked `.settings-section` blocks, no sub-tabs, no rail) matches the mockup verbatim. Section header h4 + `<span class="hint">` pattern matches. The mockup has seven scope-tier mismatches (Torrent: `enabled` HOT vs mockup REBOOT, `download_dir` RECAST vs REBOOT, `startup_buffer_seconds` HOT vs RECAST, `max_upload_rate_kbps` RECAST vs HOT, `max_download_rate_kbps` RECAST vs HOT, `listen_port` RECAST vs REBOOT; DLNA: `enabled` HOT vs REBOOT) and one invented field (Torrent's `Max .torrent file size`). The chassis renders the declared `ApplyScope` from each adapter's `Fields()` and drops the invented field; the mockup is the candidate for revision, not the chassis. This matches 4C's discipline with HOT-vs-RECAST on legacy `/ui/*`.

7. **Zero new wire-envelope carrier keys, scope tiers, or field-renderer types.** The 4A six-primitive renderer covers DLNA / Torrent / Streams entirely. Settings-save success/failure shape is identical to 4A's bridge-save (`{ok, scope, errors, chip}`). Action success/failure carrier shape is identical to 4A's probe-mister and 4B's launch-core (`{ok, error, chip}`); `streams-refresh` extends the success body with structured `summary` / `source` / `duration_ms` fields the same way probe-mister extends with `latency_ms` / `host` — additive to a fixed carrier, not a new envelope. One new chip value `BUSY` is added to the existing chip vocabulary for the server-side single-flight guard.

8. **`/ui/*` unchanged.** 4D is purely additive under `/receiver/*`. The legacy adapter form at `/ui/adapter/<name>` keeps working; cutover happens after 4F.

## Non-Goals

- **Plex / Jellyfin / URL adapter forms.** Stubs only; full forms ship in Phase 4E (link cascades — Plex's 4-state PIN flow at [v24:4299-4336](../reference/2026-05-21-receiver-v24.html#L4299-L4336), Jellyfin's 2-state sync auth at [v24:4488-4509](../reference/2026-05-21-receiver-v24.html#L4488-L4509)) and Phase 4F (URL adapter's yt-dlp host tag list at [v24:4375-4389](../reference/2026-05-21-receiver-v24.html#L4375-L4389) and cookies textarea at [v24:4400-4411](../reference/2026-05-21-receiver-v24.html#L4400-L4411)).
- **Additional manifest URLs widget for Streams.** Mockup at [v24:4565-4572](../reference/2026-05-21-receiver-v24.html#L4565-L4572) shows it; 4C deferred to 4D; 4D defers to its own follow-up spec. Requires a new `streams.Config.AdditionalManifestURLs []string` field, merge logic in the manifest refresh path (de-dup keying, per-manifest fetch status, host-allowlist enforcement per source, per-manifest error surfacing) — a meaty design problem worth its own brainstorm. The existing single `manifest_url` field is fully editable through 4D.
- **Tag-list widget for `remote_provider_allowed_hosts`.** Renders as `KindText` with comma-separated values, matching the existing `Fields()` registration at [streams/adapter.go:163](../../../internal/adapters/streams/adapter.go#L163). The tag-list primitive is introduced in Phase 4F for the URL adapter's `yt_dlp_hosts` field; a small follow-up could retrofit Streams to use it once 4F's primitive is proven.
- **Per-provider refresh buttons.** The single "Refresh manifest now" button satisfies the operator's main need. Per-provider granularity would inflate the per-provider override row's complexity (each row would need its own action slot, its own single-flight guard, its own error surface); not worth the visual cost for a Phase-5-grade convenience.
- **Per-provider `disabled` and `hls_buffer_disabled` rows in the Streams form.** Owned exclusively by the 4C Catalog pane. The clean boundary is part of the value 4D adds: Catalog = on/off, Streams form = tuning.
- **`Max .torrent file size` field.** Mockup at [v24:4466-4470](../reference/2026-05-21-receiver-v24.html#L4466-L4470) invented it; no corresponding `torrent.Config` field exists. Dropped without ceremony. A future expansion is possible but not surfaced as a placeholder in 4D.
- **Mockup scope-tier corrections.** The seven mismatches flagged in Goal 6 are documented but not corrected in code. Code is authoritative; the mockup is a visual reference, not a behavior spec. Fixing the mockup is a doc-only cleanup, out of scope for 4D.
- **Pane-tab persistence across drawer reopen.** 4A's "refresh resets to Network" rule applies — Adapters pane scroll position and any in-flight unsaved input are not preserved across drawer close/reopen.
- **`internal/eventlog/` integration.** Streams refresh and per-adapter saves don't emit event-log entries in 4D; that wiring belongs to Phase 5 (Observability). The existing INFO/ERR adapter loggers already cover the operationally interesting paths.
- **`AdapterSaver` rewrite or preflight extensions.** The existing `AdapterSaver.Save(name, fullSection []byte)` stays as-is for legacy `/ui/*` consumers. 4D adds **one** additive method (`SaveTouched`) — the minimum-viable structural change to support per-field auto-save without re-encoding the whole schema on every keystroke. No new chassis error type is introduced; errors cross the boundary through the existing structural `settingsChipError` interface. No preflight extensions (no adapter field is preflight-relevant the way bridge ports are).
- **Adapter Disable / Remove actions.** The mockup has none; the legacy `/ui/adapter/<name>` page does. 4D matches the mockup — toggle `enabled` to disable, edit TOML to remove. Section-level Disable/Remove buttons would change the chassis-vs-saver contract (`AdapterSaver` has no "remove section" path today).
- **Drawer open/close state persistence.** Inherits 4A's "refresh closes the drawer" rule. No localStorage write for 4D.
- **Authentication / CSRF token.** Same LAN-only trust model and same-origin posture as 4A/4B/4C.

## Design Decisions

| Decision | Resolution |
|---|---|
| Adapters pane sub-nav | None. Vertical stack of six `<section>` blocks in mockup order: Plex stub, DLNA, URL stub, Torrent, Jellyfin stub, Streams. Matches mockup at [v24:4272-4530](../reference/2026-05-21-receiver-v24.html#L4272-L4530). Rejected: horizontal sub-tab strip (mockup doesn't show it; would scale awkwardly to 6 adapters); vertical adapter rail on the left (heavier layout, no mockup precedent); accordion (no mockup precedent). |
| Per-adapter section ordering | Mockup order (Plex, DLNA, URL, Torrent, Jellyfin, Streams). Operators scrolling the pane see Plex first (the most common configuration) and Streams last (with the longest form). Stubs interleave with real sections; the visual impact of "stub then real then stub" is acceptable and lasts only until 4E/4F. |
| Section width modifier | DLNA uses base `.settings-section` (4 fields fit comfortably); Torrent uses `.settings-section wide` (10 numeric-heavy fields; mockup precedent at [v24:4414](../reference/2026-05-21-receiver-v24.html#L4414)); Streams uses base `.settings-section` (15 fields are mostly narrow inputs; mockup precedent at [v24:4512](../reference/2026-05-21-receiver-v24.html#L4512)). The `wide` class is already in 4A's chrome CSS. |
| Section header structure | `<h4>{DisplayName} <span class="hint">{Kind} · {State}</span></h4>`. Kind is a fixed per-adapter string (DLNA: `PUSH`, Torrent: `PASTE-IN · BT`, Streams: `PULL`). DLNA state is derived from the rendered `enabled` value (`LISTENING` / `DISABLED`) so 4D does not add a status-reporting interface. Torrent omits runtime state because there's no clean three-state runtime to surface without inventing one — header reads `Torrent · PASTE-IN · BT`. Streams uses `{N} CHANNELS · see Catalog tab` where N is the channel sum from `StreamsCatalogViewer.Catalog()`, already wired in 4C. No polling — operator closes/reopens drawer to refresh. |
| Field surface for Streams | All 15 top-level fields per 4B precedent. Admin knobs (`max_*_bytes`, `max_consecutive_failures`, request timeouts) stay visible because operators need them to tune; hiding them would force operators back to `/ui/*` or TOML editing. |
| Per-provider override sub-section in Streams | One row per registered provider, exposing only `catalog_refresh_hours` (the field 4C explicitly carved out for 4D's full streams form). Disabled / HLS-buffer overrides stay in the Catalog pane (4C). Sub-heading is `<h5 class="settings-subhead">Provider overrides</h5>` — one new minor CSS rule (~5 lines, scoped under `body.receiver`). |
| Per-provider row structure | `<div class="field-row provider-override"><label>{DisplayName} <span class="help">Refresh cadence override; 0 = inherit global.</span></label><input class="field-input num" name="providers.{ID}.catalog_refresh_hours" value="{CatalogRefreshHours}"><span class="scope hot">HOT</span></div>`. Reuses the existing `.field-row` grid layout; no new structural CSS. |
| Streams refresh action | Single `↻ Refresh manifest now` button at the bottom of the Streams section. POST `/receiver/settings/action/streams-refresh` (empty body). Server invokes `StreamsRefresher.RefreshNow(ctx)` once with a fixed 30s context; the wrapper calls `streams.Adapter.RefreshNow(ctx, "")` — the manifest-refresh entry point — exactly once. Response renders a one-line summary in the row's `.action-result` slot. Single-flight on the client side (4A pattern). Per-provider fan-out was considered and rejected: `streams.Adapter.RefreshNow(ctx, providerID)` only refreshes one provider's catalog (not the manifest); the manifest-refresh path internally ripples catalog refreshes, so a single manifest call covers what the button promises. |
| Streams refresh response shape | `{ok:true, summary:"Manifest refreshed from {source} in {duration_ms}ms", source:"{source}", duration_ms:{N}}` on success; `{ok:false, error:"manifest refresh failed: {reason}"}` on `RefreshStatus.Err != nil` (200 because the action ran cleanly — refresh failure is body detail, matching 4A's probe-mister 200-on-timeout rule); `{ok:false, chip:"NOT READY"}` when `StreamsRefresher` is nil; `{ok:false, chip:"BUSY"}` on concurrent click. The `summary` / `source` / `duration_ms` triple is additive to 4A's action envelope (carrier shape `{ok, error, chip}` is identical). |
| Save route shape | `POST /receiver/settings/adapter/{name}` — one handler dispatching by adapter name. `{name}` matches the registered adapter ID (`dlna` / `torrent` / `streams`); unknown names return 404 + `{ok:false, chip:"UNKNOWN ADAPTER"}`. Body is `application/x-www-form-urlencoded`. DLNA/Torrent accept their full `Fields()` keys. Streams accepts the 15 top-level fields plus a synthesized wildcard allowlist entry for `providers.<id>.catalog_refresh_hours`; other concrete provider keys are rejected. |
| Save error envelope | Same carrier shape as 4A bridge save. Per-field decoder/validation errors round-trip as `{ok:false, errors:{<dotted-key>:"<msg>"}}` with HTTP 400. Whole-form failures (preflight collisions, write errors, decode errors) surface as `{ok:false, chip:"<chip>"}` with the existing `BAD INPUT` / `PORT IN USE` / `WRITE FAILED` vocabulary and the status carried by `settingsChipError` when present. |
| REBOOT-scope success UX | Reuses 4A's `Restart container to apply new <field-label>` toast in the drawer-local `.settings-notice` slot. DLNA's `device_name` is the only REBOOT-scope field across 4D's three adapters — exercises the path exactly once. |
| Stub section copy | Plex / Jellyfin → "Spec 4E — implementation in progress"; URL → "Spec 4F — implementation in progress". Hint span reads "— pending" on the h4. Matches 4A's stub shape verbatim. |
| Mockup scope mismatches | Seven mismatches documented in Goal 6 / §Field surface tables but **not** corrected in code. Code's declared `ApplyScope` is authoritative. The mockup is the candidate for revision; the chassis renders what the adapter declares. |
| Mockup-invented field | `Max .torrent file size` ([v24:4466-4470](../reference/2026-05-21-receiver-v24.html#L4466-L4470)) — no corresponding `torrent.Config` field. Dropped from 4D. A future Torrent expansion could add it; not 4D's job. |
| Chassis-owned interfaces split | Two narrow interfaces (`AdapterSettingsSaver`, `StreamsRefresher`) rather than one composite. They are satisfied by different production objects (a saver wrapper over `*uiserver.AdapterSaver` vs a refresh wrapper over `*streams.Adapter`); composing them keeps each implementation small and unit-testable in isolation. |
| Forbidden imports | No new entries. `AdapterSettingsSaver` is satisfied by a wrapper in `cmd/mister-groovy-relay/` that imports `internal/uiserver` and the adapter registry; the chassis sees only the interface. `StreamsRefresher` is satisfied by another `cmd/` wrapper that imports `internal/adapters/streams`. Neither is a chassis-side import. |
| Validation surface | Two-tier: chassis-side per-field decoders (parse `"true"/"false"` for switches, parse integers, trim whitespace, reject empty when `Required: true` flag is set on the `FieldDef`); adapter-side `Validate` (called by `*uiserver.AdapterSaver` after decoding the partial TOML; surfaces `adapters.FieldErrors` as the per-field `errors` map). Cross-field validation happens in the adapter's `Validate` (Torrent's `download_dir` against `bridge.DataDir`; Streams' host-list normalization), unchanged from today's `/ui/*` behavior. |
| `bridge.DataDir` threading for Torrent | Already threaded today through `*uiserver.AdapterSaver`'s call to `torrent.Adapter.Validate` (the adapter reads `a.bridge.DataDir` at validate time). Chassis doesn't see or pass it; the production wrapper inherits the existing behavior. |
| Adapter field routing | Bridge inputs/switches/selects carry `data-field="<bridge-key>"`; adapter inputs/switches carry `data-adapter="<adapter-id>"` and no `data-field`. The existing bridge JS selector is narrowed from `input.field-input, select.field-input` to `input.field-input[data-field], select.field-input[data-field]`; the existing switch selector already uses `button.switch[data-field]`. Adapter text/number/path inputs bind to `input.field-input[data-adapter]`; adapter switches bind to `button.switch[data-adapter]`. This prevents an adapter blur from also posting a stray bridge save. |
| Client JS save-handler shape | Sibling to 4A's `button.switch[data-field]` / `input.field-input[data-field]` handlers. New selectors `[data-adapter]` carry the adapter name; the POST target is `/receiver/settings/adapter/{adapter}` instead of `/receiver/settings/bridge`. The two handlers are deliberately distinct (selectors don't overlap) — a single click on a bridge switch fires the bridge handler; a click on an adapter switch fires the adapter handler; a single blur on an adapter input fires only the adapter handler. No router lookup; simple selector dispatch. |
| Action button JS shape | New `[data-settings-action="streams-refresh"]` handler matches 4B's `launch-core` shape verbatim. Single-flight (button disabled while in-flight); renders `summary` into the action's `.action-result` slot on 200; renders `error` into the same slot on `{ok:false, error}`; `chip` surfaces in `.settings-notice`. |
| TOCTOU between drawer paint and save | Closed by the new `*uiserver.AdapterSaver.SaveTouched` introduced in 4D. The method takes the saver's shared mutex, reads the current `[adapters.<name>]` section and descendant subtables from disk (the snapshot the chassis showed at drawer paint is no longer load-bearing), overlays the touched key into the complete current TOML tree, validates, writes, and applies — same pattern as 4A's `*uiserver.BridgeSaver.SaveTouched`. Two parallel chassis auto-saves on different fields each acquire the saver mutex in turn; both observe the prior writer's value before overlaying their own. Unrendered nested keys (Streams provider `channels`, channel HLS overrides, future adapter-only metadata) are preserved. No stale-snapshot clobber and no data-loss rewrite. The 4A-grade discipline now applies to adapter saves as well. |

## Architecture — Adapter Sections

### DLNA section

Four fields, plain stack, no action button, no sub-section. Renders from `dlna.Adapter.Fields()` and `dlna.Adapter.CurrentValues()` verbatim. Section width: base `.settings-section`.

| Key | FieldDef Kind | Code scope | Mockup scope | Notes |
|---|---|---|---|---|
| `enabled` | `KindBool` | HOT | REBOOT ⚠ | Mockup wrong; code authoritative |
| `device_name` | `KindText`, required | REBOOT | REBOOT ✓ | Validate: non-empty, ≤64 runes, printable chars only |
| `autoplay_on_set_uri` | `KindBool` | HOT | HOT ✓ | |
| `allow_public_source_urls` | `KindBool` | HOT | HOT ✓ | Help text surfaces SSRF warning verbatim from [v24:4357](../reference/2026-05-21-receiver-v24.html#L4357) |

Section header: `<h4>DLNA <span class="hint">PUSH · {LISTENING|DISABLED}</span></h4>`. 4D derives `LISTENING` from the rendered `enabled` value and `DISABLED` otherwise, avoiding a third chassis-owned status interface in this phase. Runtime-status nuance can be added later if the Adapters pane grows live health badges.

### Torrent section

Ten fields from `Fields()` verbatim; section width: `.settings-section wide`.

| Key | FieldDef Kind | Code scope | Mockup scope | Notes |
|---|---|---|---|---|
| `enabled` | `KindBool` | HOT | REBOOT ⚠ | Mockup wrong |
| `traffic_acknowledged` | `KindBool` | HOT | HOT ✓ | Required-true gate for the adapter to actually run; field renderer surfaces help line as warning |
| `download_dir` | `KindText` (path) | RECAST | REBOOT ⚠ | Mockup wrong; placeholder `<data_dir>/torrents` |
| `keep_completed` | `KindBool` | HOT | HOT ✓ | |
| `max_cache_bytes` | `KindInt` | HOT | HOT ✓ | `humanizeBytes` hint in `.row-end`; range `[1 GiB, 1 TiB]` |
| `metadata_timeout_seconds` | `KindInt` | HOT | HOT ✓ | Range `[5, 600]` |
| `startup_buffer_seconds` | `KindInt` | HOT | RECAST ⚠ | Mockup wrong; range `[0, 120]` |
| `max_upload_rate_kbps` | `KindInt` | RECAST | HOT ⚠ | Mockup wrong; non-negative |
| `max_download_rate_kbps` | `KindInt` | RECAST | HOT ⚠ | Mockup wrong; non-negative |
| `listen_port` | `KindInt` | RECAST | REBOOT ⚠ | Mockup wrong; `0 or [1024, 65535]` |

Section header: `<h4>Torrent <span class="hint">PASTE-IN · BT</span></h4>`. Static hint — no runtime state suffix (Torrent has no clean three-state runtime to surface without inventing one).

Mockup's `Max .torrent file size` row dropped — no corresponding `torrent.Config` field.

Validation delegated to `torrent.validateConfig` via the existing `*uiserver.AdapterSaver` path. `bridge.DataDir` threading already in place — the torrent adapter reads `a.bridge.DataDir` at `Validate` time; chassis doesn't see or pass it.

### Streams section

15 top-level fields + per-provider sub-section + 1 action row; section width: base `.settings-section`.

**Top-level fields** (rendered in `Fields()` declaration order; mockup is silent on order so code drives):

| Key | FieldDef Kind | Code scope | Notes |
|---|---|---|---|
| `enabled` | `KindBool` | HOT | |
| `manifest_url` | `KindText`, required | HOT | |
| `manifest_refresh_hours` | `KindInt` | HOT | Range `[1, 168]` |
| `catalog_refresh_hours` | `KindInt` | HOT | Range `[1, 168]` — global default for providers that don't override |
| `max_manifest_bytes` | `KindInt` | HOT | Range `[1 KiB, 8 MiB]`, `humanizeBytes` hint in `.row-end` |
| `max_catalog_bytes` | `KindInt` | HOT | Range `[1 KiB, 64 MiB]`, `humanizeBytes` hint |
| `max_items_per_channel` | `KindInt` | HOT | Range `[1, 50000]` |
| `max_consecutive_failures` | `KindInt` | HOT | Range `[1, 100]` |
| `manifest_request_timeout_seconds` | `KindInt` | HOT | Range `[1, 60]` |
| `catalog_request_timeout_seconds` | `KindInt` | HOT | Range `[1, 120]` |
| `youtube_format` | `KindText`, required | RECAST | Free-form yt-dlp format selector |
| `allow_remote_manifest` | `KindBool` | HOT | |
| `allow_cached_remote_manifest` | `KindBool` | HOT | |
| `allow_local_manifest_urls` | `KindBool` | HOT | SSRF-relevant; help text flags as warning |
| `remote_provider_allowed_hosts` | `KindText` | HOT | Comma-separated host list; matches existing `Fields()` registration. Tag-list widget deferred to 4F. |

**Per-provider sub-section.** Below the top-level fields, render:

```html
<h5 class="settings-subhead">Provider overrides</h5>
{{ range .Providers }}
  <div class="field-row provider-override">
    <label>{{.DisplayName}} <span class="help">Refresh cadence override; 0 = inherit global.</span></label>
    <input class="field-input num" name="providers.{{.ID}}.catalog_refresh_hours" value="{{.CatalogRefreshHours}}" data-adapter="streams">
    <span class="scope hot">HOT</span>
  </div>
{{ end }}
```

Only `catalog_refresh_hours` is exposed here. The per-provider `disabled` and `hls_buffer_disabled` flags are owned by the Catalog pane (4C).

**Provider list source.** Provider rows are sourced through 4C's `CatalogSettingsManager.Providers()` interface, not directly from `streams.Adapter.Catalog()`. Reusing 4C's interface keeps the 4D Streams pane and the 4C Catalog pane consistent (same provider list, same ordering, same source of truth) and avoids a second registry-discovery code path in the chassis. The chassis wrapper for `Providers()` already projects `cfg.Providers[id].CatalogRefreshHours` (default zero when no override) — 4D reads that projection. Ordering matches 4C: catalog registration order. The chassis-side wrapper does not re-sort, and tests assert registration order.

`.settings-subhead` is a new minor CSS rule (~5 lines).

**Action row** at the bottom of the Streams section:

```html
<div class="field-row action-row">
  <label>Manifest refresh</label>
  <button class="action-btn" data-settings-action="streams-refresh">↻ Refresh manifest now</button>
  <span class="action-result"></span>
</div>
```

Triggers `POST /receiver/settings/action/streams-refresh` (action envelope from 4A: structured `summary` / `source` / `duration_ms` on success; `error` / `chip` on failure). The button is single-flight on the client side. The server handler calls `StreamsRefresher.RefreshNow(ctx)` exactly once — which calls `streams.Adapter.RefreshNow(ctx, "")` — and the structured result is whatever `streams.RefreshStatus` carries (source label, fetch timestamp, error if any).

Section header: `<h4>Streams catalog <span class="hint">PULL · {N} CHANNELS · see Catalog tab</span></h4>`. N is the `CatalogChannelCount` value 4C already populates on `SettingsData` (computed from `CatalogSettingsManager.Providers()`); 4D reads the existing field rather than re-summing. The "see Catalog tab" suffix is rendered only when the Catalog tab itself is functional (4C's `CatalogSettingsManager` non-nil) — when it's nil (e.g., in an isolated unit test), the suffix is omitted and the hint reads `PULL · {N} CHANNELS`.

### Stub sections (Plex / Jellyfin / URL)

Inlined into the container template `settings-adapters.html`:

```html
<section class="settings-section">
  <h4>Plex <span class="hint">— pending</span></h4>
  <div class="action-result shown">Spec 4E — implementation in progress</div>
</section>
<!-- Jellyfin: same, "Spec 4E" -->
<!-- URL: same, "Spec 4F" -->
```

Matches 4A's stub shape. Deleted when 4E/4F lands.

## Architecture — Wire Contract

### Settings save: `POST /receiver/settings/adapter/{name}`

**Request.** Method POST, Content-Type `application/x-www-form-urlencoded`. Path `{name}` ∈ {`dlna`, `torrent`, `streams`} (the three real adapters) or any other registered adapter name (stub adapters return 404). Body is a form-urlencoded payload with any subset of the adapter's 4D writable field surface. DLNA and Torrent use their full `Fields()` table. Streams uses all 15 top-level fields plus the single per-provider override key `providers.<id>.catalog_refresh_hours`; `providers.<id>.disabled` and `providers.<id>.hls_buffer_disabled` are rejected as `BAD INPUT` in this route because the Catalog pane owns them.

**Response — success.** HTTP 200, body `{ok:true, scope:"hot|next|recast|reboot"}`. Scope is the max-wins aggregation across the touched fields' declared `ApplyScope`, computed by the existing `*uiserver.AdapterSaver` and mapped to the wire label via `scopeLabel`. REBOOT-scope saves additionally trigger the drawer-local `.settings-notice.ok` toast with the `Restart container to apply new <field-label>` text.

**Response — per-field error.** HTTP 400, body `{ok:false, errors:{<dotted-key>:"<msg>"}}`. `<dotted-key>` is exactly the form-field name. Multiple per-field errors are returned in one response.

**Response — whole-form error.** HTTP status follows the typed `settingsChipError.StatusCode()` when present (otherwise 500), body `{ok:false, chip:"<chip>"}`. Chip vocabulary: `BAD INPUT` (decode failure, unknown key, malformed body); `PORT IN USE` (preflight bind collision, rare for adapters but possible on torrent `listen_port` RECAST); `WRITE FAILED` (atomic write failure).

**Response — wiring failures.** HTTP 503, body `{ok:false, chip:"NOT READY"}` when `AdapterSettingsSaver` is nil in `chassis.Config`. HTTP 404, body `{ok:false, chip:"UNKNOWN ADAPTER"}` when `{name}` is not in the registry. HTTP 403, body `{"error":"cross-site request blocked"}` for cross-origin requests (existing `requireSameOrigin` middleware).

### Streams refresh action: `POST /receiver/settings/action/streams-refresh`

**Request.** Method POST, empty body.

**Response — success.** HTTP 200, body `{ok:true, summary:"Manifest refreshed from {source} in {duration_ms}ms", source:"{source}", duration_ms:{N}}`. `source` is the `RefreshStatus.Source` string from `streams.Adapter.RefreshNow(ctx, "")` (typically `"remote"` or `"cache"`); `duration_ms` is the chassis-measured wall-clock time around the single `RefreshNow` call.

**Response — refresh failure.** HTTP 200, body `{ok:false, error:"manifest refresh failed: {reason}"}`. The action ran cleanly but `RefreshStatus.Err` was non-nil — matches 4A's probe-mister 200-on-failure pattern. Reason is the trimmed error message capped at 200 chars (matching 4A's `sanitizeProbeError` cap for cross-action consistency).

**Response — wiring failures.** HTTP 503, body `{ok:false, chip:"NOT READY"}` when `StreamsRefresher` is nil. HTTP 403, body `{"error":"cross-site request blocked"}` for cross-origin requests.

**Concurrency.** The chassis handler owns a per-process `sync.Mutex` and uses `TryLock()` before invoking `RefreshNow`; if the lock is already held, the handler returns HTTP 409, body `{ok:false, chip:"BUSY"}`. (Aligns with the client-side single-flight guard but provides a backstop.) Lock ordering: the chassis single-flight mutex is acquired before `RefreshNow` is invoked; `RefreshNow` internally acquires `streams.Adapter.a.mu` for snapshot reads and re-installs. The chassis-side mutex does **not** call into `AdapterSettingsSaver.SaveTouched` from within its critical section — refresh and per-field saves use disjoint lock paths. See §Risks for the full lock-ordering discipline.

**Timeout.** A fixed 30s context wraps the `RefreshNow` call. If it elapses, the call is cancelled (via `ctx.Done()` propagation inside `streams.Adapter.refreshOnce`); the response surfaces as a refresh failure with `error:"manifest refresh failed: context deadline exceeded"`. The chassis single-flight mutex is released as soon as `RefreshNow` returns.

## Architecture — Code Structure

### Modified files (existing)

| File | Change |
|---|---|
| [internal/chassis/settings.go](../../../internal/chassis/settings.go) | Add `AdapterSettingsSaver`, `StreamsRefresher`, `StreamsRefreshResult` interfaces and types alongside the 4A/4B/4C declarations. Add `handleSettingsAdapterPost(name)`, `handleSettingsActionStreamsRefresh`. Extend `buildSettingsData` to populate per-adapter `Fields` + `Values` + `Providers` (for streams) for the three real adapters; emit stub sentinels for the three stub adapters. Estimated ~250 LOC. |
| [internal/chassis/server.go](../../../internal/chassis/server.go) | Add `AdapterSettingsSaver` and `StreamsRefresher` as nullable fields on `chassis.Config`. Mount `POST /receiver/settings/adapter/{name}` and `POST /receiver/settings/action/streams-refresh` behind `requireSameOrigin`. The save route is a single handler that dispatches by `{name}` to the registered adapter; a small lookup table keys the real adapters by ID. |
| [internal/chassis/templates/settings-drawer.html](../../../internal/chassis/templates/settings-drawer.html) | Replace the placeholder `<div class="settings-pane" data-pane="adapters">` block with `{{ template "settings-adapters" . }}`. The `data-pane="adapters"` div wrapper stays; only the inner content changes. |
| [internal/chassis/static/settings-drawer.js](../../../internal/chassis/static/settings-drawer.js) | Narrow the existing bridge text/select selector to `input.field-input[data-field], select.field-input[data-field]`. Add handlers for `button.switch[data-adapter]` and `input.field-input[data-adapter]` that POST to `/receiver/settings/adapter/{adapter}`. The 4A bridge handlers stay unchanged in behavior after selector narrowing; these are additive siblings. Add action handler for `[data-settings-action="streams-refresh"]` that mirrors 4B's `launch-core` shape with the new `summary` / `source` / `duration_ms` success body. |
| [internal/chassis/static/chassis.css](../../../internal/chassis/static/chassis.css) | Port `.settings-section .hint` rule from mockup (already used on existing section headers — minor font-size + color), add new `.settings-subhead` rule for Streams' per-provider sub-section. Both scoped under `body.receiver`. ~10 lines total. |
| [internal/chassis/import_check_test.go](../../../internal/chassis/import_check_test.go) | **No edits.** Forbidden-imports list unchanged. |
| [internal/uiserver/adapter_saver.go](../../../internal/uiserver/adapter_saver.go) | **Add `SaveTouched(name string, touched map[string]string, adapter adapters.Adapter, fields []adapters.FieldDef) (adapters.ApplyScope, error)`.** Mirrors `BridgeSaver.SaveTouched`'s discipline (acquire shared mutex; read current section and descendant subtables; overlay touched keys into the complete TOML tree; re-encode the full section without dropping unrendered nested keys; validate via `adapters.Validator` when implemented; write atomically; call `adapter.ApplyConfig` for runtime dispatch). The existing `Save(name, rawTOMLSection []byte)` is preserved as-is — legacy `/ui/*` still uses it. The production wrapper supplies the concrete adapter from the registry and a 4D writable-field allowlist; `AdapterSaver` does not own the registry directly. ~110 LOC including nested-preservation tests. |

### New files (chassis)

| File | Purpose |
|---|---|
| `internal/chassis/templates/settings-adapters.html` | Container template `{{ define "settings-adapters" }}` that emits six `<section>` blocks in mockup order (Plex stub, DLNA, URL stub, Torrent, Jellyfin stub, Streams). Stubs are inlined; real sections delegate to per-adapter templates via `{{ template "settings-adapter-dlna" . }}` / `-torrent` / `-streams`. |
| `internal/chassis/templates/settings-adapter-dlna.html` | DLNA `<section>` block with 4 field rows. |
| `internal/chassis/templates/settings-adapter-torrent.html` | Torrent `<section class="settings-section wide">` block with 10 field rows. |
| `internal/chassis/templates/settings-adapter-streams.html` | Streams `<section>` block with 15 top-level field rows + `<h5 class="settings-subhead">Provider overrides</h5>` + N per-provider rows + 1 action row. |
| `internal/chassis/settings_adapters_test.go` | Handler unit tests for the save route (every success/error branch listed in §Wire Contract), the action route (success, refresh failure, nil refresher, busy guard, context timeout), the unknown-adapter path, the nil-saver path, and the render tests for each adapter template against fixture `Fields()` / `CurrentValues()` / `Providers` data. |

### New files (`cmd/mister-groovy-relay/`)

| File | Purpose |
|---|---|
| `cmd/mister-groovy-relay/adapter_settings_saver.go` | Implements `chassis.AdapterSettingsSaver` over `*uiserver.AdapterSaver` + the adapter registry. `Current(name)` reads `adapter.CurrentValues()`; `Fields(name)` reads `adapter.Fields()` and projects the 4D writable surface (for Streams, keeps top-level fields, removes concrete provider rows, and appends one wildcard `providers.*.catalog_refresh_hours` allowlist entry); `SaveTouched(name, touched)` delegates to the new `*uiserver.AdapterSaver.SaveTouched` with the concrete adapter plus that writable-field allowlist. It returns errors satisfying `settingsChipError` structurally (propagated from `uiserver` or implemented by a tiny local `cmd` error type) so the chip/REBOOT path is identical to 4A without exporting a chassis concrete error. |
| `cmd/mister-groovy-relay/streams_refresher.go` | Implements `chassis.StreamsRefresher` over `*streams.Adapter`. `RefreshNow(ctx)` measures wall-clock around a single `streams.Adapter.RefreshNow(ctx, "")` call (the manifest-refresh entry point — providerID `""` is the documented sentinel). Returns `StreamsRefreshResult{Source, DurationMS, Err}` reflecting the `streams.RefreshStatus` shape. The 30s timeout is applied by the chassis handler around the call (`context.WithTimeout`), not duplicated here. |

### Test infrastructure

`internal/chassis/settings_adapters_test.go` uses `httptest.NewRecorder` against the chassis HTTP handlers with stub `AdapterSettingsSaver` and `StreamsRefresher` implementations defined in the test file. Stub `AdapterSettingsSaver` returns canned `Fields()` slices, canned `CurrentValues()` maps, and configurable success/error outcomes for `SaveTouched`. Stub `StreamsRefresher` returns a configurable `StreamsRefreshResult` with optional `Err` injection plus a configurable artificial delay (for the concurrent-click `BUSY` test).

`cmd/mister-groovy-relay/` gains wrapper-level tests plus one integration-tag DLNA route test (per §Testing) that exercise the real `*uiserver.AdapterSaver` against a temp `config.toml`, validating disk-side round-trip and in-memory adapter state where constructor cost is reasonable.

### Forbidden-imports check

`internal/chassis/import_check_test.go` is **not** modified. Its existing entries already forbid:
- `internal/ui` (legacy)
- `internal/uiserver`
- `internal/misterctl` (added in 4B)
- `internal/adapters/plex`, `internal/adapters/jellyfin`, `internal/adapters/url`, `internal/adapters/dlna`, `internal/adapters/streams`, `internal/adapters/torrent`, `internal/adapters/auxadapter` (every concrete adapter package)

`internal/adapters/auxadapter` is out of 4D scope by design — it's the visualizer aux-input adapter (different lifecycle, no `[adapters.auxadapter]` TOML section the chassis needs to render). The Adapters tab badge in 4D shows `· N` where `N = len(registry.List())` minus any registered-but-not-renderable adapters; if `auxadapter` shows up in the registry it is filtered from the count to match the six-section render.

Production wrappers in `cmd/mister-groovy-relay/` are where the `internal/uiserver` and `internal/adapters/streams` imports live; the chassis sees only the two new interfaces.

## Testing

### Unit tests (`internal/chassis/settings_adapters_test.go`)

- **Render tests** — for DLNA / Torrent, render the section template against a stub `AdapterSettingsSaver` with a fixture `Fields()` table and assert every `Fields()` key appears as a `name=` attribute on exactly one input or button. For Streams, assert all 15 top-level keys appear, assert `providers.<id>.catalog_refresh_hours` appears once per fixture provider, and assert `providers.<id>.disabled` / `providers.<id>.hls_buffer_disabled` are absent. For all three sections, assert: (a) rendered `.scope` badges match the declared `ApplyScope`; (b) prefilled `value=` attributes match `CurrentValues()`; (c) Streams provider rows render in catalog registration order; (d) the Streams refresh action row renders with `data-settings-action="streams-refresh"`; (e) `humanizeBytes` hint appears in `.row-end` for `max_cache_bytes` / `max_manifest_bytes` / `max_catalog_bytes`; (f) adapter inputs carry `data-adapter` and no `data-field`.
- **JS routing test** — exercise the drawer script against a fixture containing one bridge input and one adapter input. Blurring the bridge input posts only to `/receiver/settings/bridge`; blurring the adapter input posts only to `/receiver/settings/adapter/<name>`. This pins the selector narrowing from `input.field-input, select.field-input` to `[data-field]` for bridge fields.
- **Save handler — success branches** — POST `/receiver/settings/adapter/dlna` with `enabled=true` → 200 + `{ok:true, scope:"hot"}`; POST `/receiver/settings/adapter/dlna` with `device_name=NewName` → 200 + `{ok:true, scope:"reboot"}`; POST `/receiver/settings/adapter/torrent` with `download_dir=/srv/torrents` → 200 + `{ok:true, scope:"recast"}`; POST `/receiver/settings/adapter/streams` with `providers.foo.catalog_refresh_hours=12` → 200 + `{ok:true, scope:"hot"}` and the stub saver's touched map records exactly `providers.foo.catalog_refresh_hours`.
- **Save handler — per-field error** — stub saver returns `adapters.FieldErrors{{Key:"max_cache_bytes",Msg:"must be in [1 GiB, 1 TiB]"}}` → 400 + `{ok:false, errors:{max_cache_bytes:"must be in [1 GiB, 1 TiB]"}}`.
- **Save handler — chip error** — stub saver returns an error satisfying `settingsChipError` with `StatusCode() == 500` and `Chip() == "WRITE FAILED"` → 500 + `{ok:false, chip:"WRITE FAILED"}`.
- **Save handler — bad input** — POST with unknown key → 400 + `{ok:false, chip:"BAD INPUT"}`; POST with malformed body → 400 + `{ok:false, chip:"BAD INPUT"}`.
- **Save handler — hidden Streams provider keys** — POST `/receiver/settings/adapter/streams` with `providers.foo.disabled=true` or `providers.foo.hls_buffer_disabled=true` → 400 + `{ok:false, chip:"BAD INPUT"}`. These fields stay owned by the Catalog pane.
- **Save handler — wiring failures** — nil `AdapterSettingsSaver` → 503 + `{ok:false, chip:"NOT READY"}`; unknown `{name}` → 404 + `{ok:false, chip:"UNKNOWN ADAPTER"}`; cross-origin → 403 + `{"error":"cross-site request blocked"}`.
- **Streams refresh action tests** — success (200 + `{ok:true, summary, source, duration_ms}` with stub `StreamsRefresher` returning `StreamsRefreshResult{Source:"remote", DurationMS:42}`); refresh failure (200 + `{ok:false, error}` with stub returning `Err: errors.New("connection refused")`); nil refresher (503 + `{ok:false, chip:"NOT READY"}`); concurrent click (409 + `{ok:false, chip:"BUSY"}` — drive by using a stub refresher that blocks on a channel and asserting two concurrent requests produce one 200 + one 409); context timeout (200 + `{ok:false, error:"manifest refresh failed: context deadline exceeded"}` — use a stub refresher that respects ctx and returns ctx.Err() when cancelled).
- **Stub section render test** — assert Plex / Jellyfin render with `Spec 4E` and URL renders with `Spec 4F`; assert all three render the `— pending` hint span.

### Wrapper / integration tests (`cmd/mister-groovy-relay/`)

- `adapter_settings_e2e_test.go` (`//go:build integration`) — end-to-end POST against a real `*uiserver.AdapterSaver` and DLNA adapter with a temp `config.toml`; assert the disk-side TOML round-trips through `[adapters.dlna]`, the in-memory adapter state updates, and the wire scope matches the field's declared `ApplyScope`.
- `adapter_settings_saver_test.go` — wrapper-level Torrent and Streams save coverage using minimal fake adapters, including RECAST scope on Torrent `download_dir`, top-level Streams saves, per-provider `providers.<id>.catalog_refresh_hours`, and projection that exposes one wildcard `providers.*.catalog_refresh_hours` allowlist while hiding Catalog-owned provider keys.
- `streams_refresher_test.go` — wrapper-level streams refresh and chassis route coverage using the wrapper's narrow `RefreshNow(ctx, providerID)` interface; assert the wrapper passes `providerID == ""`, the action response matches `Source` / `Err` / measured duration, and the chassis 30s context wraps the call.

### Verifications

- Mockup-deviation discipline: every scope-tier mismatch in §Field surface tables is documented in the spec but not corrected in code. Assert at least one render test exercises each mismatch (e.g., test asserts Torrent `enabled` row renders `<span class="scope hot">HOT</span>`, not REBOOT).
- `import_check_test.go` runs unchanged; new chassis files import only `internal/adapters` (root package, not concrete adapter packages) and standard library.
- Existing 4A/4B/4C handler tests are not touched. The Adapters route is additive; no shared handler state.
- Force-add verification: because `docs/superpowers/` is ignored, `git status --short --untracked-files=all` plus `git check-ignore -v` should be checked before commit; commit uses `git add -f docs/superpowers/specs/2026-05-28-receiver-chassis-adapters-simple-pane-design.md`.

## Risks

- **`AdapterSettingsSaver.Current` polling cost on drawer open.** Three adapters × N fields = ~30 in-memory lookups per drawer paint. All under per-adapter `mu`; sub-millisecond. Not a concern.
- **Streams refresh stalls on a slow upstream manifest source.** The 30s chassis-side context caps the wait; the manifest fetch surfaces as a refresh failure with `error:"manifest refresh failed: context deadline exceeded"`. Client-side single-flight + server-side `TryLock`/`BUSY` chip on concurrent clicks prevent operator-induced overlap. The chassis single-flight mutex releases as soon as `RefreshNow` returns (or context cancels).
- **Lock-ordering discipline across saver, single-flight mutex, and adapter `mu`.** The shared `*uiserver.AdapterSaver` mutex (also held by `BridgeSaver`) protects on-disk writes; per-adapter `a.mu` protects in-memory swaps; the chassis streams-refresh single-flight mutex protects only the `streams-refresh` action. The disciplined orderings are: (a) save path — chassis HTTP handler → `AdapterSettingsSaver.SaveTouched` → `*uiserver.AdapterSaver.SaveTouched` (takes saver mutex) → optional `adapters.Validator.Validate` + `adapter.ApplyConfig` (takes `a.mu` inside the adapter). (b) refresh path — chassis HTTP handler → chassis single-flight mutex → `StreamsRefresher.RefreshNow` → `streams.Adapter.RefreshNow(ctx, "")` (takes `a.mu` inside `refreshOnce`). The two paths share `a.mu` but never compose: refresh never calls saver, save never calls refresh. The single-flight mutex is never held while waiting on the saver mutex. No cross-orderings; no deadlock surface.
- **Concurrent saves to the streams section while refresh runs.** A per-field save POST (e.g., `manifest_refresh_hours=12`) arriving while `RefreshNow` is running goes through the disjoint saver mutex path. The save acquires saver mutex first; the refresh's `RefreshNow` may then re-read the now-updated config snapshot mid-flight. This is the same race window that exists today between background refresh-loop ticks and operator config saves — `RefreshNow` returns whatever status it computed against the snapshot it saw. Documented; no regression vs current behavior.
- **Provider disappears between page render and per-provider POST.** The chassis renders provider IDs at drawer-paint time. A background manifest refresh during the operator's review can remove a provider before the operator blurs a `providers.<id>.catalog_refresh_hours` field. The save path's `*uiserver.AdapterSaver.SaveTouched` will overlay the touched key into the current TOML section regardless — leaving an orphaned `[adapters.streams.providers.<id>]` entry with an unused `catalog_refresh_hours` value and no live `Catalog()` provider. This is harmless: `streams.Adapter.ApplyConfig` accepts unknown providers (they sit in `cfg.Providers` as configuration ready for a future re-add); the next refresh re-converges. One unit test asserts the chassis handler returns 200 (not 404) for the POST in this race. Matches the legacy `/ui/*` form's tolerance.
- **Mockup-vs-code scope mismatches are operator-visible.** An operator who studies the mockup expecting Torrent `enabled` to be REBOOT will see HOT and may worry. The spec captures every discrepancy explicitly so reviewers know it's intentional; the mockup is the candidate for revision, not the chassis.
- **Per-provider override row count grows with the manifest.** A manifest with 20 providers would add 20 rows to the Streams section. Acceptable for the bundled manifest (~3 providers today); needs revisiting if the deferred Add-manifest widget ever lands.
- **Stub sections in 4D become dead code in 4E/4F.** Plex / Jellyfin stub blocks are deleted when 4E lands; URL stub when 4F lands. The deletions are 5 lines each; trivial. Pre-flagging in this spec avoids the "why was this here?" archaeology.
- **The chassis-side `BUSY` lock on `streams-refresh` is process-wide.** Two operators in two browser tabs both clicking refresh produce one fast 200 and one fast 409. Acceptable; the action is operator convenience, not a transactional contract.
- **Streams provider ordering depends on 4C's provider projection.** The Catalog pane (4C) and the 4D Streams form both render providers in catalog registration order. Tests assert this so a future wrapper change cannot accidentally sort one pane differently.

## Open Questions

None. All design decisions captured above.
