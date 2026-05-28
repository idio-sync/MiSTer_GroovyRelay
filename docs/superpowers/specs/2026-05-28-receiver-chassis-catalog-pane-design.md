# Receiver Chassis Catalog Pane — Phase 4C Design

**Status:** Brainstormed; awaiting implementation plan.

**Scope:** Third sub-spec of Phase 4 (Settings & Adapters). Replaces the "Spec 4C — implementation in progress" stub that ships in [Phase 4A](2026-05-27-receiver-chassis-settings-drawer-network-pane-design.md) with the real **Catalog** pane — a server-rendered list of streams-adapter providers (icon + meta + channel count + enabled switch) plus a global "Disable HLS buffer for direct-stream providers" switch — and extends [Phase 4B](2026-05-27-receiver-chassis-pipeline-advanced-panes-design.md)'s Advanced pane with a new Diagnostics section containing a destructive **Reset settings to defaults** action with an inline two-step confirm. Adds three new action/save routes (`POST /receiver/settings/catalog/provider/{id}`, `POST /receiver/settings/catalog/direct-stream-hls-buffer`, `POST /receiver/settings/action/restore-defaults`), two chassis-owned interfaces (`CatalogSettingsManager`, `ConfigReset`), one provider-row template partial, four narrow edits to the streams adapter (`ConfigSnapshot`, `ApplyConfigValue`, `StopActiveCast`, and `Catalog()` Origin/Kind population), and two production wrappers in `cmd/mister-groovy-relay/`. Reuses every primitive 4A/4B established without modification.

**Repo location:** Committed under `docs/superpowers/specs/`. Force-added per the receiver chassis rollout convention.

## Context

[Phase 4A](2026-05-27-receiver-chassis-settings-drawer-network-pane-design.md) shipped the settings drawer chrome, the Network pane (9 bridge fields + probe-mister action), the `field` template helper covering six input types, the JSON error envelope (`{ok, scope, errors, chip, error}`), the 4-tier scope vocabulary (HOT/NEXT/RECAST/REBOOT), the drawer-local `.settings-notice` slot, the `BridgeSettingsSaver`/`Prober`/`settingsChipError` chassis-owned interfaces, and the additive `*uiserver.settingsError` typed wrapper.

[Phase 4B](2026-05-27-receiver-chassis-pipeline-advanced-panes-design.md) shipped the Pipeline pane (9 fields + launch-core action) and the Advanced pane's HLS-buffer (×11) + Logging (×1) sections, the `CoreLauncher` interface, the `humanizeBytes` / `boolStr` / `i64toa` / `passwordPlaceholder` / `options` / `list` helpers, and the `SkipEmpty` field-renderer flag. It deferred three Advanced rows (activity ring, build info, reset-to-defaults) and the entire Catalog pane.

What 4A/4B left behind: a working drawer with two visible-but-empty placeholder panes — one labelled "Spec 4C — implementation in progress" for Catalog, and the second "Spec 4D–4F" for Adapters — plus an Advanced pane that ends at Logging with no Diagnostics section. 4C fills in the Catalog stub and grows Advanced's Diagnostics section to contain its first row (`Reset settings to defaults`).

The streams adapter at [internal/adapters/streams/](../../../internal/adapters/streams/) already owns the data model 4C surfaces: `Config.Providers map[string]ProviderConfig`, with `ProviderConfig{Disabled, CatalogRefreshHours, HLSBufferDisabled, Channels}`. The 3B catalog browse drawer already consumes `StreamsCatalogViewer.Catalog() []CatalogProvider` for the provider-tabs + channel-grid widget. 4C adds a parallel mutation interface alongside the existing read-only viewer; the chassis does not import `internal/adapters/streams` and does not duplicate the read view.

**Mockup reference:** [`docs/superpowers/reference/2026-05-21-receiver-v24.html`](../reference/2026-05-21-receiver-v24.html). Catalog pane lives at lines [4532-4582](../reference/2026-05-21-receiver-v24.html#L4532-L4582); Advanced pane's Diagnostics section at [4654-4671](../reference/2026-05-21-receiver-v24.html#L4654-L4671); `.provider-row` CSS at [2098-2121](../reference/2026-05-21-receiver-v24.html#L2098-L2121); `.settings-pane.single-col` at [1942](../reference/2026-05-21-receiver-v24.html#L1942).

**What changed since 4B** (everything else is reuse):

| Change | Scope |
|---|---|
| New routes `POST /receiver/settings/catalog/provider/{id}`, `POST /receiver/settings/catalog/direct-stream-hls-buffer`, `POST /receiver/settings/action/restore-defaults` | Three handlers, one shared decoder |
| New chassis-owned interfaces `CatalogSettingsManager` + `ConfigReset` | Two methods + one method, separate concerns |
| New chassis-owned types `CatalogProviderState`, `CatalogProviderPatch` | Render-side + mutation-side structs declared alongside the interfaces in `internal/chassis/settings.go` |
| New exported `config.DefaultConfigTOML(dataDir string) ([]byte, error)` helper | Replaces an aspirational `config.Default()` reference (see §Architecture — Production Wrappers) |
| New template partial `settings-catalog.html` (with `settings-catalog-provider-row` sub-define) | One file, ~50 lines |
| New Advanced-pane Diagnostics section (one row) | One additive block in `settings-advanced.html` |
| Streams-package edits | New `ConfigSnapshot` + new `ApplyConfigValue` + new `StopActiveCast` + `Catalog()` populates new Origin/Kind. **No scope-table edit** — chassis-side floor handles RECAST reporting, and the production wrapper invokes `StopActiveCast` for Catalog RECAST saves. |
| `internal/adapters` edit | `CatalogProvider` gains `Origin` + `Kind` fields (consumed in 4C only by the Catalog pane `.stat` line) |
| Two new fields on `adapters.CatalogProvider` (`Origin`, `Kind`) | Backwards-compatible additions |
| Two production wrappers in `cmd/mister-groovy-relay/` (`catalog_manager.go`, `config_reset.go`) | Glue plus Catalog declared-scope/active-stop dispatch |
| Inline two-step confirm pattern (CSS + JS) | ~10 lines CSS + ~60 lines JS |
| `.provider-row` CSS family ported from mockup | ~25 lines, scoped under `body.receiver` |

**4A/4B patterns 4C reuses verbatim (do not redesign):**

- `field` template helper (used only for the few non-row inputs in 4C; provider-row uses its own partial).
- Per-field auto-save: click on switch.
- JSON envelopes — settings save: `{ok:true, scope:"hot|next|recast|reboot"}`, `{ok:false, errors:{<field>:"<msg>"}}`, `{ok:false, chip:"<chip>"}`; actions: `{ok:true, ...}`, `{ok:false, error:"<msg>"}`, `{ok:false, chip:"NOT READY"}` for wiring failures.
- `requireSameOrigin` middleware on every new route.
- `settingsChipError` structural interface (4C adds a second concrete satisfier, `*configResetError`).
- Drawer-local `.settings-notice` slot for chip/REBOOT toasts.
- `scopeLabel` helper for `adapters.ApplyScope` → wire label mapping.
- `internal/chassis/import_check_test.go` continues to forbid `internal/ui`, `internal/uiserver`, `internal/misterctl`, and every concrete adapter package. 4C **adds no new entries** — the streams adapter is reached only through `CatalogSettingsManager`, and `config.DefaultConfigTOML` is called from the `cmd/mister-groovy-relay` reset wrapper, not from `internal/chassis`.

## Goals

1. **Catalog tab fully functional.** All providers returned by `CatalogSettingsManager.Providers()` render as `.provider-row` blocks (icon + meta + channel count + `enabled` switch) under a "Bundled providers" section header that includes live provider + channel counts. Clicking a row switch auto-saves through `POST /receiver/settings/catalog/provider/{id}` with `enabled=true|false` and applies HOT-scope side effects. A single global "Disable HLS buffer for direct-stream providers" switch under a separate "Per-provider HLS buffer override" section saves through `POST /receiver/settings/catalog/direct-stream-hls-buffer`, flipping `providers.<id>.hls_buffer_disabled` for every `Live` provider in one save, with RECAST scope (current cast drops; next play uses the new posture).
2. **Restore-defaults works end to end.** A new Diagnostics section in the Advanced pane contains a single `Reset settings to defaults` row. Clicking the destructive `⚠ Reset…` button arms an inline confirm (`This wipes config.toml. [Cancel] [Confirm reset]`); confirming POSTs `/receiver/settings/action/restore-defaults`, rewrites `config.toml` with the bundled default TOML (sourced via a new exported `config.DefaultConfigTOML(dataDir string) ([]byte, error)` helper that substitutes the operator's current `bridge.data_dir` into the embedded `example.toml` template), returns `200 {ok:true, scope:"reboot"}`, and toasts `Defaults restored — restart container to apply` into the drawer-local `.settings-notice` slot. `bridge.data_dir` is **preserved** through the reset so the operator's persistent state in `data_dir` (device UUID, plex.tv token, streams cache, `.first-run-complete`) stays chained to the post-reset config; the `data_dir` directory itself is not touched at all.
3. **Two new chassis-owned interfaces; zero new chassis-forbidden imports.** `CatalogSettingsManager` (three methods) and `ConfigReset` (one method) are declared in [internal/chassis/settings.go](../../../internal/chassis/settings.go) alongside the 4A/4B interfaces. Both are satisfied from outside (production wrappers in `cmd/mister-groovy-relay/`). `internal/chassis` continues to have zero imports of `internal/uiserver`, `internal/misterctl`, or any concrete adapter package. The forbidden-imports test at [internal/chassis/import_check_test.go](../../../internal/chassis/import_check_test.go) keeps its existing entries; 4C adds none.
4. **Visual fidelity to the v24 mockup.** Provider-row DOM mirrors [v24:4536-4544](../reference/2026-05-21-receiver-v24.html#L4536-L4544) verbatim: `.provider-row > .icon[.cartoon|.toonami] + .meta(.name+.stat) + .channel-count + .switch`. The "Per-provider HLS buffer override" section mirrors [v24:4574-4581](../reference/2026-05-21-receiver-v24.html#L4574-L4581). The Diagnostics section reuses the mockup's destructive button styling at [v24:4669](../reference/2026-05-21-receiver-v24.html#L4669) verbatim. Deltas vs the mockup are limited to (a) replacing literal channel counts and stat lines with template-driven data and (b) adding a `.scope.recast` badge to the global HLS-override switch row for consistency with every other settings field.
5. **Chassis Catalog pane reports the accurate runtime scope.** Per-provider `hls_buffer_disabled` toggles render with the `.scope.recast` badge and dispatch a RECAST-scope save. The mechanism is a **declared-scope floor** inside the production `catalogManager` wrapper (`cmd/mister-groovy-relay/catalog_manager.go`): after `ApplyConfigValue` returns the diff-aggregated scope, the wrapper floors it at the patch's declared scope so a no-op save (operator clicks a switch back to its current value, or flips the global switch when zero Live providers exist) still emits the wire scope the field's declared contract demands. This is the canonical Catalog-side mechanism; it does not touch any streams-adapter scope tables. **Legacy `/ui/*` is unchanged:** the streams adapter's existing `FieldDef.ApplyScope` at [internal/adapters/streams/adapter.go:197](../../../internal/adapters/streams/adapter.go#L197) keeps reporting `ScopeHotSwap` for `providers.<id>.hls_buffer_disabled` (matches today's behavior — not a regression). Genuinely fixing the legacy form requires editing both `FieldDef.ApplyScope` and `configChangeScope` together; deferred to whichever future spec rationalizes streams-adapter scope reporting, since `internal/uiserver`'s legacy form is on the chopping block after 4F anyway.
6. **Zero new wire-envelope keys, scope tiers, or field-renderer types.** Per-row switches use the same `<button class="switch">` shape as 4A/4B (with `data-catalog-provider` / `data-catalog-field` attributes instead of inheriting the field renderer's emission), because the provider-row's three-column layout differs structurally from the field renderer's three-column layout. The restore-defaults action route reuses 4A's bridge-save success envelope (`{ok:true, scope:"reboot"}`) rather than introducing an action-flavored variant, so the client toast handler is identical to bridge REBOOT saves.
7. **`/ui/*` unchanged.** 4C is purely additive under `/receiver/*`. The legacy streams adapter form keeps reporting HOT for HLS-override toggles (matches today's behavior); the inaccuracy is documented but not fixed in 4C — see §Goal 5 for rationale. Cutover happens after 4F.

## Non-Goals

- **Custom-manifest "Add" widget.** The mockup at [v24:4565-4572](../reference/2026-05-21-receiver-v24.html#L4565-L4572) shows an "Add an additional provider via a hosted manifest.json" input + Add button. This requires extending `streams.Config` with `additional_manifest_urls []string` plus merge logic in the manifest refresh path (de-duplication, per-manifest refresh status, host-allowlist semantics). Deferred to Phase 4D where the full `[adapters.streams]` form lives — 4D is the natural home for editing `manifest_url`-shaped fields and is where the streams package's significant new work belongs.
- **Per-provider force-refresh actions.** The streams adapter exposes `RefreshNow(ctx, providerID)`, but the mockup's Catalog pane shows no refresh affordance. Deferred to 4D where the streams adapter form is the natural home for a "Refresh manifest" action.
- **Per-channel HLS-buffer-disabled UI.** `streams.ChannelConfig.HLSBufferDisabled` remains config-file-only. Per-channel granularity is a power-user feature; surfacing it would explode the Catalog pane's row count and visual density.
- **Per-provider `catalog_refresh_hours` UI.** Operator-rare; lives in 4D's full streams form (the existing adapter-Fields() registration already exposes it via the generic UI).
- **Read-only diagnostics.** Last-fetch timestamps, fetch error counters, channel-fetch latency — these belong to Phase 5 Observability. The Catalog pane shows only `name + stat + channel count`.
- **Activity-ring and build-info Diagnostics rows.** 4C grows the Diagnostics section header and adds the restore-defaults row only. The activity-ring row (mockup [v24:4656-4660](../reference/2026-05-21-receiver-v24.html#L4656-L4660)) and build-info row ([v24:4661-4665](../reference/2026-05-21-receiver-v24.html#L4661-L4665)) ship in Phase 5.
- **Pre-reset backup file.** A "reset creates `config.toml.pre-reset` backup" affordance is a Phase 5 polish candidate. The 4A migration already leaves `config.toml.pre-ui-migration` in place as the operator's recovery anchor; that is enough for 4C's "config.toml only" reset semantics.
- **`data_dir` factory reset.** A button that wipes `data_dir` (device UUID, Plex token, streams cache, `.first-run-complete`) was considered and rejected: too destructive for an inline-confirm widget; operators who want a true factory reset can `rm -rf data_dir` outside the UI. The chassis design decision is "config-only reset is recoverable; data_dir reset is not, so it deserves a heavier confirm pattern we are not designing in 4C."
- **`.scope.next` activation.** The dormant `.scope.next` CSS badge from 4A is not activated by 4C. Per-provider HLS overrides became RECAST (not NEXT) because the operator's intent is "change immediately" — drop and replay. NEXT awaits a future feature where silent-for-current-cast semantics are genuinely the right answer.
- **`.scope.next` field activation through per-provider `catalog_refresh_hours` or any other Catalog-adjacent key.** Same reasoning; 4D may revisit.
- **Confirm-modal CSS surface.** 4C uses an inline two-step confirm (button row morphs in place). A richer modal pattern (typed confirmation, dimmed overlay) is deferred to whichever phase needs it — 4E's Plex unlink is a likely candidate. The CSS surface added in 4C (`.field-row.confirming`, `.action-btn.cancel`, `.action-btn.confirm`) is small enough that a future modal pattern can ignore it without conflict.
- **Live byte-unit recomputation or other dynamic re-render.** Drawer state is server-rendered per page load; refresh restores the current snapshot. Catalog pane has no humanized-byte hints in scope.
- **Authentication / CSRF token.** Same LAN-only trust model and same-origin posture as 4A/4B.

## Design Decisions

| Decision | Resolution |
|---|---|
| Catalog pane visual layout | Match v24 mockup exactly: per-row enabled switch only (no per-row HLS toggle), single global HLS-override switch in a separate section below. Per-row HLS toggles were considered and rejected for visual-fidelity. |
| Provider-row partial | New `{{define "settings-catalog-provider-row"}}` partial in `settings-catalog.html`. Three-column flex layout (.icon + .meta + .channel-count + .switch) does not fit the `field` renderer's (.label + .input + .scope) shape, so the provider-row is templated directly. The `<button class="switch">` element matches the field renderer's switch output byte-for-byte except for the data attributes. |
| Provider-row switch attribute naming | Carries `data-catalog-provider="{id}"` + `data-catalog-field="enabled"`. **Deliberately avoids `data-field`** because the existing 4A bridge-switch click handler selects `button.switch[data-field]` at [internal/chassis/static/settings-drawer.js:187](../../../internal/chassis/static/settings-drawer.js#L187); reusing the attribute would make both handlers fire on a single click — the 4A path would POST a stray `enabled=true` to `/receiver/settings/bridge` and surface a spurious BAD INPUT toast. Same reasoning for the global HLS-override switch (`data-catalog-direct-hls`, no `data-field`). A unit test on the rendered template asserts no `data-field` attribute appears on either catalog switch. |
| Provider-row stat line | Format: `{Origin} · {Kind}{ · default: <code>{DefaultChannel}</code> if set}`. Origin and Kind are new fields on `adapters.CatalogProvider` (see Streams Adapter Changes); DefaultChannel exists today. The mockup's `<code style="font-family:inherit;color:var(--vfd);background:transparent;">` becomes a CSS rule `.provider-row .stat code { font-family: inherit; color: var(--vfd); background: transparent; }`. |
| Channel + provider counts | Driven by `CatalogManager.Providers()`. Existing `CatalogProviderCount` remains the tab badge count from `StreamsCatalogViewer.Catalog()`; new `CatalogPaneProviderCount` becomes `len(providers)` and `CatalogChannelCount` is the sum of `provider.ChannelCount` across all providers. The pane count + channel count feed the section header's hint text. |
| Catalog pane class | `<div class="settings-pane single-col">` — single-column override per mockup. One new CSS rule (`body.receiver .settings-pane.single-col.active { grid-template-columns: 1fr; }`) ported from [v24:1942](../reference/2026-05-21-receiver-v24.html#L1942). |
| Global HLS-override semantic | Switch reflects "every Live provider has `hls_buffer_disabled == true`." Mixed state (some live providers disabled, some not) renders as **off**; flipping it on then brings everyone to disabled. Rationale: simpler operator mental model than a tri-state indicator; operator who needs mixed state edits `config.toml` directly. |
| Global HLS-override save shape | One POST → one save → one `ApplyConfigValue` call → one reported scope. The production wrapper iterates `streams.Adapter.Catalog()` to find live providers, mutates each in a snapshot, calls `ApplyConfigValue` once, floors the reported scope to RECAST, and invokes `StopActiveCast` for the RECAST side effect. Avoids N round trips; avoids partial-failure middle states. |
| Per-provider HLS scope | RECAST as the chassis-side declared scope. The production wrapper inside `cmd/mister-groovy-relay/catalog_manager.go` enforces this via a declared-scope floor (max-wins between the diff-reported scope and `ScopeRestartCast` when the patch touches HLS-buffer-disabled). Rationale: `shouldBufferDirectHLS` is consulted at queue-item start, not mid-stream, so the in-flight cast needs to drop and replay for the new flag to be observable — RECAST honors that intent. The streams adapter's internal scope tables (`scopeForField`, `FieldDef.ApplyScope`, `configChangeScope`) are **not** edited in 4C — see §Architecture — Why no `scopeForField` edit. Legacy `/ui/*` continues to report HOT for these toggles (acknowledged inaccuracy, deferred). |
| Per-provider enabled scope | HOT. Unchanged from the streams adapter's existing scope table. The enabled flag gates UI surfacing and queue selection but does not affect any in-flight cast (already-playing channel keeps playing; UI for that provider just hides). |
| Restore-defaults visual placement | New "Diagnostics" section in the Advanced pane (not Catalog). Matches the mockup's placement at [v24:4654-4671](../reference/2026-05-21-receiver-v24.html#L4654-L4671). 4C ships the section header + the restore-defaults row only; activity-ring and build-info rows ship in Phase 5. |
| Restore-defaults route shape | `POST /receiver/settings/action/restore-defaults` — action namespace. Empty body. Confirm is client-side; server sees only the final confirmed POST. |
| Restore-defaults success envelope | `{ok:true, scope:"reboot"}` — reuses 4A's bridge-save REBOOT envelope so the client toast helper is the same. The action's failure envelope (`error` / `chip`) still applies for non-success paths. |
| Restore-defaults scope | REBOOT. Disk gets defaults; live process keeps old config until restart. No in-memory mutation. Operator-visible toast reads `Defaults restored — restart container to apply`. |
| Restore-defaults blast radius | `config.toml` only. `data_dir` is untouched (device UUID, Plex token, streams cache, `.first-run-complete` sentinel survive). Operator who wants a true factory reset can `rm -rf data_dir` separately. |
| Restore-defaults confirm UX | Inline two-step. Click `⚠ Reset…` → button row swaps to `This wipes config.toml. [Cancel] [Confirm reset]`. 10-second armed timeout auto-reverts. Cancel/idle restoration is purely client-side; only the final confirmed POST hits the server. Rejected: full modal overlay (overkill for a recoverable config-only reset); typed confirmation (friction without proportional risk reduction). |
| Confirm-modal CSS surface | One new `.field-row.confirming` rule (suppresses the trailing `.scope` badge during the armed state) + two new button variants (`.action-btn.cancel`, `.action-btn.confirm`). ~10 lines total. Variants exist alongside the mockup's destructive inline style, which is preserved verbatim for the idle button. |
| Chassis-owned interfaces split | Two narrow interfaces (`CatalogSettingsManager`, `ConfigReset`) instead of one. They are satisfied by different production objects (streams adapter wrapper vs config-default writer); composing them keeps each implementation small and unit-testable in isolation. |
| Forbidden imports | No new entries. `CatalogSettingsManager` is satisfied by a wrapper in `cmd/mister-groovy-relay/`; the wrapper imports `internal/adapters/streams` but the chassis does not. `ConfigReset` is satisfied by another `cmd/` wrapper that imports `internal/config` (already chassis-allowed). |
| Streams adapter API additions | Three new public methods: `ConfigSnapshot() Config` (deep copy), `ApplyConfigValue(newCfg Config, save func(string, []byte) error) (ApplyScope, error)`, and `StopActiveCast() error`. All are narrow, surgically scoped to the production wrapper's needs. `ApplyConfigValue` mirrors the existing `ApplyConfig` validation/snapshot/swap path but accepts a typed `Config` and embeds the save call; `StopActiveCast` mirrors the adapter's existing active-ref guarded stop flow without stopping the refresh loop. |
| `adapters.CatalogProvider` shape extension | Two new string fields (`Origin`, `Kind`). Backwards compatible — empty strings render cleanly in both the 3B browse drawer (which ignores them) and the new 4C Catalog pane (which conditionally omits the relevant `.stat`-line segments). Sourced inside the streams adapter from `ProviderDefinition`'s manifest URL host (via `url.Parse`) and provider-type tag (already known at definition-build time). |
| TOCTOU between snapshot and apply | Acknowledged. The `uiserver.AdapterSaver` mutex shared with `BridgeSaver` serializes disk writes; the streams adapter's `a.mu` serializes in-memory swaps. The remaining window — snapshot → mutate-in-userspace → enter saver lock — is the same last-writer-wins window the legacy `/ui/*` adapter save tolerates. A future `streams.Adapter.SaveTouched(apply func(*Config))` mirror of `BridgeSettingsSaver.SaveTouched` is a polish candidate if concurrent-tab edits become a reported issue. Operator-visible: stale tab is stale until refresh; no data corruption. |
| Pre-reset `config.toml` backup | Not in 4C. The 4A migration leaves `config.toml.pre-ui-migration` in place; that's the existing recovery anchor. A "reset creates dated backup" affordance is Phase 5 polish. |
| Empty `CatalogManager` / nil saver paths | The Catalog pane still renders with an empty providers list (section header reads `0 PROVIDERS · 0 CHANNELS`). Operators see the panel; non-functional but discoverable. If `ConfigReset` is nil, the restore-defaults row still renders and the POST returns `503 chip:"NOT READY"` like the other 4A/4B action routes (defensive — `main.go` always wires it). |

## Implementation Checklist (sketch — implementation plan elaborates)

- [internal/chassis/settings.go](../../../internal/chassis/settings.go): extend with the `CatalogSettingsManager` + `ConfigReset` interface declarations, the `CatalogProviderState` struct, and three new handlers (`handleSettingsCatalogProviderPost`, `handleSettingsCatalogDirectStreamHLSBufferPost`, `handleSettingsActionRestoreDefaults`). Reuses existing helpers `writeSettingsChip`, `writeSettingsFieldErrors`, `writeSettingsSuccess`, `scopeLabel`.
- [internal/chassis/settings_test.go](../../../internal/chassis/settings_test.go): handler unit tests for every success/error branch listed in §Wire Contract (~20 tests).
- [internal/chassis/data.go](../../../internal/chassis/data.go): extend `SettingsData` with `CatalogPaneProviderCount int`, `CatalogProviders []CatalogProviderState`, `CatalogChannelCount int`, `DirectStreamHLSBufferDisabled bool`. Extend `buildSettingsData` to accept `catalogManager CatalogSettingsManager` and populate the Catalog-pane fields from it while preserving existing `CatalogProviderCount` tab-badge behavior.
- [internal/chassis/session.go](../../../internal/chassis/session.go): `snapshotFromStatusView` and `idleSnapshot` already call `buildSettingsData` via 4A; 4C just lets the new fields populate from `CatalogSettingsManager.Providers()`.
- [internal/chassis/server.go](../../../internal/chassis/server.go): extend `Config` with `CatalogManager CatalogSettingsManager` and `ConfigReset ConfigReset` fields. Mount three new routes in `Server.Mount`.
- [internal/chassis/templates/settings-catalog.html](../../../internal/chassis/templates/settings-catalog.html) (new): defines `{{define "settings-catalog"}}` plus `{{define "settings-catalog-provider-row"}}`.
- [internal/chassis/templates/settings-drawer.html](../../../internal/chassis/templates/settings-drawer.html) (one-line edit): replace `{{ template "settings-stub" (stub "catalog" "Streams catalog" "4C") }}` with `{{ template "settings-catalog" .Settings }}`.
- [internal/chassis/templates/settings-advanced.html](../../../internal/chassis/templates/settings-advanced.html) (additive): append the Diagnostics section with the restore-defaults row.
- [internal/chassis/static/settings-drawer.js](../../../internal/chassis/static/settings-drawer.js): extend with provider-row switch handlers, global HLS-override switch handler, and the inline-confirm state machine for restore-defaults. Reuses the existing `toastChip` / `toastReboot` helpers; the latter gains an optional label-override argument (one-line extension).
- [internal/chassis/static/chassis.css](../../../internal/chassis/static/chassis.css): port `.provider-row` family (~25 lines) + `.settings-pane.single-col` (~2 lines) + `.field-row.confirming` (~3 lines) + `.action-btn.cancel` and `.action-btn.confirm` variants (~5 lines). All scoped under `body.receiver` per chassis convention.
- [internal/chassis/templates.go](../../../internal/chassis/templates.go): no new FuncMap entries needed. The provider-row partial uses only existing helpers.
- [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go): template render tests for the Catalog pane, the provider-row partial (one per badge variant), the global HLS-override row, the Diagnostics section with restore-defaults, `buildSettingsData` extension (~12 tests).
- [internal/adapters/streams/adapter.go](../../../internal/adapters/streams/adapter.go): add `ConfigSnapshot() Config` (deep copy via new `deepCopyConfig` helper), `ApplyConfigValue(newCfg Config, save func(string, []byte) error) (adapters.ApplyScope, error)`, and `StopActiveCast() error`. The internal helper `encodeSectionTOML(cfg Config) ([]byte, error)` is exposed as a sibling of the existing `configToWire`. **No scope-table edit:** `scopeForField` is not consumed by production code today (only by its own unit test); the chassis-side declared-scope floor inside `catalogManager` is the canonical mechanism for reporting RECAST per §Goal 5.
- [internal/adapters/streams/adapter.go](../../../internal/adapters/streams/adapter.go): extend `Catalog()` builder to populate `Origin` (parsed from `ProviderDefinition.BaseURL` host with `PlaylistURL.Host` fallback) and `Kind` (from `ProviderDefinition.Type`) on the returned `adapters.CatalogProvider` values.
- [internal/adapters/catalog.go](../../../internal/adapters/catalog.go): extend `CatalogProvider` with `Origin string` and `Kind string` fields.
- [internal/adapters/streams/adapter_test.go](../../../internal/adapters/streams/adapter_test.go): unit tests for the new `ConfigSnapshot`, `ApplyConfigValue`, `StopActiveCast`, and `Catalog()` field population (~11 tests). No `scopeForField` test updates — the table is unchanged in 4C.
- [cmd/mister-groovy-relay/catalog_manager.go](../../../cmd/mister-groovy-relay/catalog_manager.go) (new): production `catalogManager` wrapper satisfying `chassis.CatalogSettingsManager`. Composes `*streams.Adapter` and `*uiserver.AdapterSaver`.
- [cmd/mister-groovy-relay/catalog_manager_test.go](../../../cmd/mister-groovy-relay/catalog_manager_test.go) (new): unit tests for `Providers`, `UpdateProvider` (enabled-only, hls-only, both-keys, all-nil-patch), `SetDirectStreamHLSBuffer`, and RECAST active-stop dispatch (~8 tests).
- [internal/config/example.go](../../../internal/config/example.go) (extend): export a new `DefaultConfigTOML(dataDir string) ([]byte, error)` helper alongside the existing package-private `defaultConfigTOML()`. The new function wraps `ExampleTOML()` and substitutes the supplied `dataDir` into the `data_dir = ""` marker (mirrors `defaultConfigTOML`'s line-replace pattern at [internal/config/example.go:60-66](../../../internal/config/example.go#L60-L66)). When `dataDir == ""`, falls through to the platform default via `defaultDataDirForConfigWrite()` (matches first-run semantics). Encoding-side-only: does not touch disk. **Why a new exported helper and not `toml.NewEncoder(&buf).Encode(Sectioned)`:** BurntSushi/toml does not round-trip `toml.Primitive` values, which means re-encoding `Sectioned` would silently drop adapter sections (the same reason `AdapterSaver.replaceAdapterSection` exists in [internal/uiserver/adapter_saver.go:13-18](../../../internal/uiserver/adapter_saver.go#L13-L18)). The embedded `example.toml` is the authoritative round-trippable source. Add a precondition unit test in `internal/config/example_test.go` that asserts `DefaultConfigTOML("/some/path")` parses cleanly via `LoadSectioned` and passes `Sectioned.Validate()`.
- [cmd/mister-groovy-relay/config_reset.go](../../../cmd/mister-groovy-relay/config_reset.go) (new): production `configReset` wrapper satisfying `chassis.ConfigReset`. Holds the shared `BridgeSaver.Mu()` mutex, reads the current `bridge.data_dir` directly from the shared `*config.Sectioned` (not via `BridgeSaver.Current()`, which would re-lock the mutex), calls `config.DefaultConfigTOML(dataDir)`, writes via `config.WriteAtomic`, and leaves in-memory state untouched.
- [cmd/mister-groovy-relay/config_reset_test.go](../../../cmd/mister-groovy-relay/config_reset_test.go) (new): unit tests for reset success, disk failure, `data_dir` preservation, and no self-deadlock around the shared mutex (~5 tests).
- [cmd/mister-groovy-relay/main.go](../../../cmd/mister-groovy-relay/main.go): construct the two wrappers, pass into `chassis.Config.CatalogManager` and `chassis.Config.ConfigReset`. The `*streams.Adapter` and `*uiserver.AdapterSaver` instances already exist at this point in startup (registered in steps 7-8 of the startup sequence per CLAUDE.md).
- [tests/integration/chassis_test.go](../../../tests/integration/chassis_test.go): end-to-end coverage for per-provider save, global HLS toggle, active-streams recast stop, restore-defaults success + failure (~9 tests).
- [tests/integration/catalog_scope_test.go](../../../tests/integration/catalog_scope_test.go) (new): cross-side drift catcher — boot real `streams.Adapter` + real wrapper, assert the chassis wire label and active-stop side effect match the wrapper-reported scope for enabled and HLS-buffer toggles (3 tests).

**Files intentionally unchanged in 4C:**
- `internal/uiserver/*` — `BridgeSaver`'s typed-error wrapper, `AdapterSaver`'s atomic section-write semantics, and the shared mutex all cover 4C without modification.
- `internal/misterctl/*` — 4C does not exercise the SSH launcher.
- `internal/core/*` — no new core surface. Streams already has `SessionManager.StopIfAdapterRef`; `StopActiveCast` uses that existing active-ref guarded control path instead of adding `DropActiveCast` to the streams interface.
- `internal/ui/*` — legacy `/ui/*` keeps working. The streams adapter form there still reports HOT for HLS-override toggles via its existing field definitions and scope table; the acknowledged inaccuracy is deferred per §Goal 5.
- `internal/launchcore/*` — 4C does not exercise the launch-core flow.
- `internal/chassis/import_check_test.go` — no new forbidden entries.

## Wire Contract — HTTP Routes

### `POST /receiver/settings/catalog/provider/{id}`

**Headers:** browser-supplied `Sec-Fetch-Site: same-origin` or `same-site` (enforced by `requireSameOrigin`). Client JS must not set `Sec-*` headers manually.

**Body:** `application/x-www-form-urlencoded`. Accepted keys:

| Form name | Type | Values |
|---|---|---|
| `enabled` | bool | `"true"` \| `"false"` |
| `hls_buffer_disabled` | bool | `"true"` \| `"false"` |

Either or both may appear. Missing keys mean "do not change that field." Empty body → `400 chip:"BAD INPUT"`.

**Server logic:**

1. `requireSameOrigin` middleware. Wrong origin → 403.
2. `s.cfg.CatalogManager == nil` → 503 `chip:"NOT READY"`.
3. `r.ParseForm()`. Parse failure → 400 `chip:"BAD INPUT"`.
4. `id := r.PathValue("id")`. Resolve against `CatalogManager.Providers()` (one snapshot, reused for the rest of the request). Unknown id → 404 `{ok:false, error:"unknown provider"}`.
5. Decode `enabled` and `hls_buffer_disabled` if present. Either bad bool → 400 `errors:{<field>:"must be true or false"}`. Both keys absent after parse → 400 `chip:"BAD INPUT"`.
6. Dispatch through `CatalogManager.UpdateProvider(id, patch)` where `patch.Enabled` and `patch.HLSBufferDisabled` are `*bool` populated from the decoded form values (nil for any key the operator did not send). Any-fields-set check happens at decode time; the handler never calls `UpdateProvider` with an all-nil patch. One POST → one snapshot → one save/apply → one wrapper-reported `ApplyScope` after the declared-scope floor and any required active-stop side effect. The single-method shape closes the TOCTOU window that two sequential per-key saves would open between snapshot and apply.
7. Typed-error mapping (mirrors 4A's bridge-save path): error satisfying `settingsChipError` → propagate status + chip. Other errors → 500 `chip:"WRITE FAILED"`.
8. Success → 200 `{ok:true, scope:"<label>"}`. Map `adapters.ApplyScope` via `scopeLabel`.

**Responses:**

| Status | Body | When |
|---|---|---|
| 200 | `{"ok":true,"scope":"hot"}` | Only `enabled` saved |
| 200 | `{"ok":true,"scope":"recast"}` | `hls_buffer_disabled` saved (or both — RECAST max-wins after the wrapper's declared-scope floor) |
| 400 | `{"ok":false,"errors":{"enabled":"must be true or false"}}` | Bad bool |
| 400 | `{"ok":false,"chip":"BAD INPUT"}` | Empty body / both keys missing / parse failure |
| 403 | (middleware) | Wrong origin |
| 404 | `{"ok":false,"error":"unknown provider"}` | Path id does not match any provider |
| 500 | `{"ok":false,"chip":"WRITE FAILED"}` | Disk write or apply failure |
| 503 | `{"ok":false,"chip":"NOT READY"}` | `CatalogManager` not wired |

### `POST /receiver/settings/catalog/direct-stream-hls-buffer`

The global "Disable HLS buffer for direct-stream providers" switch ([v24:4579](../reference/2026-05-21-receiver-v24.html#L4579)).

**Body:** `disabled=true|false`. Required. Missing → 400 `chip:"BAD INPUT"`.

**Server logic:**

1. Middleware + nil-manager checks identical to per-provider route.
2. `r.ParseForm()`; missing `disabled` → 400 `chip:"BAD INPUT"`.
3. Decode `disabled`. Bad bool → 400 `errors:{disabled:"must be true or false"}`.
4. `CatalogManager.SetDirectStreamHLSBuffer(v)`. Wrapper iterates the catalog snapshot for `Live == true` providers and applies the flag in one save (see §Architecture).
5. Typed-error mapping identical to per-provider route.
6. Success → 200 `{ok:true, scope:"recast"}`. If the catalog has zero Live providers, the save no-ops on disk semantics (byte-identical content); the response still reads RECAST because the field's *declared* scope is RECAST regardless of whether the current operation produced an actual diff.

**Responses:**

| Status | Body | When |
|---|---|---|
| 200 | `{"ok":true,"scope":"recast"}` | Save succeeded (including all-no-op) |
| 400 | `{"ok":false,"errors":{"disabled":"must be true or false"}}` | Bad bool |
| 400 | `{"ok":false,"chip":"BAD INPUT"}` | Empty body / missing key |
| 403 | (middleware) | Wrong origin |
| 500 | `{"ok":false,"chip":"WRITE FAILED"}` | Disk / apply failure |
| 503 | `{"ok":false,"chip":"NOT READY"}` | `CatalogManager` not wired |

### `POST /receiver/settings/action/restore-defaults`

**Body:** Empty. Confirm is entirely client-side; server sees only the final confirmed POST.

**Server logic:**

1. `requireSameOrigin` middleware. Wrong origin → 403.
2. `s.cfg.ConfigReset == nil` → 503 `chip:"NOT READY"`.
3. `ConfigReset.ResetToDefaults()`. Production wrapper:
   - Reads the current `bridge.data_dir` from the shared `*config.Sectioned` while holding the shared saver mutex.
   - Calls `config.DefaultConfigTOML(dataDir)` to render the bundled `example.toml` with that `data_dir` substituted.
   - `config.WriteAtomic(path, body)` — atomic-rename semantics shared with `BridgeSaver`.
   - Does not touch the `data_dir` directory contents. Does not mutate in-memory bridge or adapter state.
4. Disk failure → typed `*configResetError` satisfying `settingsChipError` (`StatusCode()==500`, `Chip()=="WRITE FAILED"`). Chassis maps via the existing 4A `errors.As(err, &ce)` path.
5. Success → 200 `{ok:true, scope:"reboot"}`. Client toasts the dedicated message (see §Architecture — Client JS).

**Responses:**

| Status | Body | When |
|---|---|---|
| 200 | `{"ok":true,"scope":"reboot"}` | Defaults written |
| 403 | (middleware) | Wrong origin |
| 500 | `{"ok":false,"chip":"WRITE FAILED"}` | Encode / write failure |
| 503 | `{"ok":false,"chip":"NOT READY"}` | `ConfigReset` not wired |

## Architecture — Chassis-Owned Interfaces

Declared in [internal/chassis/settings.go](../../../internal/chassis/settings.go) alongside `BridgeSettingsSaver`, `Prober`, `CoreLauncher`, and `settingsChipError`:

```go
// CatalogSettingsManager is the chassis-side interface for Catalog-pane
// state mutation. Production passes a thin wrapper around *streams.Adapter
// from cmd/mister-groovy-relay; internal/chassis does NOT import
// internal/adapters/streams.
type CatalogSettingsManager interface {
    // Providers returns the renderable Catalog-pane state. Stable order
    // matches StreamsCatalogViewer.Catalog() so the two surfaces agree
    // on ID/order. Safe to call before adapter Start (same posture as
    // the existing StreamsCatalogViewer).
    Providers() []CatalogProviderState

    // UpdateProvider applies the patch's non-nil flags to providers.<id>
    // in a single snapshot/save/apply cycle. Either pointer may be nil
    // (means "do not change that field"); both nil is a no-op the chassis
    // handler rejects before invoking the interface. Returns the
    // wrapper-reported ApplyScope after applying the declared-scope floor
    // and any required runtime side effect.
    UpdateProvider(id string, patch CatalogProviderPatch) (adapters.ApplyScope, error)

    // SetDirectStreamHLSBuffer flips providers.<id>.hls_buffer_disabled
    // for every provider where Live == true in one save. Returns the
    // wrapper-reported scope (RECAST via the declared-scope floor).
    SetDirectStreamHLSBuffer(disabled bool) (adapters.ApplyScope, error)
}

// CatalogProviderPatch is the optional-field patch consumed by
// UpdateProvider. Pointer-to-bool encodes the tri-state {unset, true,
// false} that the chassis handler needs to distinguish "this form key
// was omitted" from "this form key was set to false."
type CatalogProviderPatch struct {
    Enabled           *bool
    HLSBufferDisabled *bool
}

// CatalogProviderState is the chassis-shaped per-provider state for
// rendering and mutation. All fields are populated by the production
// wrapper from streams.Config + adapters.CatalogProvider; the chassis
// renders directly from this struct.
type CatalogProviderState struct {
    ID                string
    DisplayName       string
    BadgeLabel        string
    BadgeClass        string
    Origin            string
    Kind              string
    DefaultChannel    string
    Live              bool
    ChannelCount      int
    Enabled           bool
    HLSBufferDisabled bool
}

// ConfigReset is the chassis-side interface for the restore-defaults
// action. Production passes a wrapper that calls config.DefaultConfigTOML
// and config.WriteAtomic. Scope is REBOOT.
type ConfigReset interface {
    // ResetToDefaults atomically rewrites the on-disk config.toml with
    // application defaults. MUST NOT touch data_dir, MUST NOT mutate
    // in-memory bridge/adapter state. Disk-write failures return a
    // *configResetError satisfying settingsChipError so the chassis can
    // map to {chip:"WRITE FAILED"} cleanly.
    ResetToDefaults() error
}
```

`Server.Config` gains:

```go
type Config struct {
    // … existing 4A/4B fields …
    CatalogManager CatalogSettingsManager // 4C
    ConfigReset    ConfigReset            // 4C
}
```

New mux routes in `Server.Mount`:

```go
mux.Handle("POST /receiver/settings/catalog/provider/{id}",
    requireSameOrigin(http.HandlerFunc(s.handleSettingsCatalogProviderPost)))
mux.Handle("POST /receiver/settings/catalog/direct-stream-hls-buffer",
    requireSameOrigin(http.HandlerFunc(s.handleSettingsCatalogDirectStreamHLSBufferPost)))
mux.Handle("POST /receiver/settings/action/restore-defaults",
    requireSameOrigin(http.HandlerFunc(s.handleSettingsActionRestoreDefaults)))
```

## Architecture — `SettingsData` and Snapshot Wiring

### `internal/chassis/data.go`

```go
type SettingsData struct {
    // … existing 4A/4B fields …
    CatalogProviderCount          int                    // existing tab badge count from StreamsCatalogViewer
    CatalogPaneProviderCount      int                    // 4C — len(CatalogProviders)
    CatalogProviders              []CatalogProviderState // 4C
    CatalogChannelCount           int                    // 4C — sum across CatalogProviders
    DirectStreamHLSBufferDisabled bool                   // 4C — true iff every Live provider has hls_buffer_disabled
}
```

`buildSettingsData` gains a fourth argument, `catalogManager CatalogSettingsManager`, and the extension below runs after the existing 4A/4B body. The existing `CatalogProviderCount` remains the tab badge count from `StreamsCatalogViewer.Catalog()`; the Catalog pane heading uses the new `CatalogPaneProviderCount` so nil-manager/offline renders do not show a nonzero heading with no rows.

```go
if catalogManager != nil {
    providers := catalogManager.Providers()
    out.CatalogProviders = providers
    out.CatalogPaneProviderCount = len(providers)
    channelTotal := 0
    liveCount := 0
    liveDisabledCount := 0
    for _, p := range providers {
        channelTotal += p.ChannelCount
        if p.Live {
            liveCount++
            if p.HLSBufferDisabled {
                liveDisabledCount++
            }
        }
    }
    out.CatalogChannelCount = channelTotal
    // Global switch is on iff every Live provider is currently disabled.
    // Mixed state renders as off (operator simplicity).
    out.DirectStreamHLSBufferDisabled = liveCount > 0 && liveDisabledCount == liveCount
}
```

`settingsDataFromConfig` passes `cfg.CatalogManager` through to `buildSettingsData(bridge, cfg.Registry, cfg.StreamsCatalogViewer, cfg.CatalogManager)`. Nil-manager fallback: the existing 4A `CatalogProviderCount` (computed from `StreamsCatalogViewer.Catalog()`) remains as the tab badge fallback for nil-saver tests / offline render paths. `CatalogProviders` stays nil and `CatalogPaneProviderCount` stays zero; the Catalog pane renders the empty-state shape (heading reads `0 PROVIDERS · 0 CHANNELS`, no rows, global HLS section still visible).

## Architecture — Templates

### New: `internal/chassis/templates/settings-catalog.html`

```html
{{define "settings-catalog"}}
  <div class="settings-pane single-col" data-pane="catalog">

    <div class="settings-section wide">
      <h4>Bundled providers <span class="hint">{{ .CatalogPaneProviderCount }} PROVIDERS · {{ .CatalogChannelCount }} CHANNELS</span></h4>
      {{ range .CatalogProviders }}
        {{ template "settings-catalog-provider-row" . }}
      {{ end }}
    </div>

    <div class="settings-section wide">
      <h4>Per-provider HLS buffer override</h4>
      <div class="field-row" id="direct-stream-hls-buffer-row">
        <label>Disable HLS buffer for direct-stream providers <span class="help">Toonami Aftermath etc. bypass the shared HLS cache. Safer for live feeds with strict origin policies.</span></label>
        <div></div>
        <span class="row-end">
          <button class="switch{{ if .DirectStreamHLSBufferDisabled }} on{{ end }}"
                  type="button"
                  data-catalog-direct-hls
                  aria-pressed="{{ .DirectStreamHLSBufferDisabled }}"></button>
          <span class="scope recast">RECAST</span>
        </span>
      </div>
    </div>

  </div>
{{end}}

{{define "settings-catalog-provider-row"}}
  <div class="provider-row" data-provider="{{ .ID }}">
    <div class="icon{{ if .BadgeClass }} {{ .BadgeClass }}{{ end }}">{{ .BadgeLabel }}</div>
    <div class="meta">
      <div class="name">{{ .DisplayName }}</div>
      <div class="stat">
        {{- if .Origin }}{{ .Origin }}{{ end -}}
        {{- if and .Origin .Kind }} · {{ end -}}
        {{- if .Kind }}{{ .Kind }}{{ end -}}
        {{- if and (or .Origin .Kind) .DefaultChannel }} · {{ end -}}
        {{- if .DefaultChannel }}default: <code>{{ .DefaultChannel }}</code>{{ end -}}
      </div>
    </div>
    <div class="channel-count">{{ .ChannelCount }} CH</div>
    <button class="switch{{ if .Enabled }} on{{ end }}"
            type="button"
            data-catalog-provider="{{ .ID }}"
            data-catalog-field="enabled"
            aria-pressed="{{ .Enabled }}"></button>
  </div>
{{end}}
```

### Edit: `internal/chassis/templates/settings-drawer.html`

One-line swap:

```html
{{- /* before */ -}}
{{ template "settings-stub" (stub "catalog" "Streams catalog" "4C") }}

{{- /* after */ -}}
{{ template "settings-catalog" .Settings }}
```

(Adapters stub stays; 4D-4F swaps it.)

### Edit: `internal/chassis/templates/settings-advanced.html`

Append a new `.settings-section` after the Logging section:

```html
<div class="settings-section">
  <h4>Diagnostics <span class="hint">read-only</span></h4>
  <div class="field-row" id="restore-defaults-row">
    <label>Reset to defaults <span class="help">Rewrites <code>config.toml</code> with application defaults. Persisted state in <code>data_dir</code> (device UUID, Plex token, streams cache) is preserved. Requires a container restart to apply.</span></label>
    <div>
      <button class="action-btn"
              id="restore-defaults-btn"
              type="button"
              style="color:oklch(0.78 0.16 25);border-color:oklch(0.40 0.16 25);">⚠ Reset…</button>
      <div class="action-result" id="restore-defaults-result"></div>
    </div>
    <span class="scope reboot">REBOOT</span>
  </div>
</div>
```

The `⚠ Reset…` button color/border styles are copied verbatim from the mockup's inline style at [v24:4669](../reference/2026-05-21-receiver-v24.html#L4669). Phase 5 may extract these into a shared `.action-btn.destructive` rule once activity-ring and build-info rows land alongside.

### CSS additions in `internal/chassis/static/chassis.css`

Ported verbatim from [v24:2098-2121](../reference/2026-05-21-receiver-v24.html#L2098-L2121), scoped under `body.receiver`:

```css
body.receiver .settings-panel .provider-row { /* flex layout, gap, border-bottom */ }
body.receiver .settings-panel .provider-row:last-child { border-bottom: 0; }
body.receiver .settings-panel .provider-row .icon { /* badge box, DSEG14 typography */ }
body.receiver .settings-panel .provider-row .icon.cartoon { color: oklch(0.62 0.06 80); }
body.receiver .settings-panel .provider-row .icon.toonami { color: oklch(0.62 0.06 280); }
body.receiver .settings-panel .provider-row .meta .name { /* Inter 600 12px */ }
body.receiver .settings-panel .provider-row .meta .stat { /* DSEG14 10px dim */ }
body.receiver .settings-panel .provider-row .meta .stat code { font-family: inherit; color: var(--vfd); background: transparent; }
body.receiver .settings-panel .provider-row .channel-count { /* DSEG7 11px VFD glow */ }
```

Plus:

```css
body.receiver .settings-pane.single-col.active { grid-template-columns: 1fr; }

body.receiver .settings-panel .field-row.confirming > .scope { visibility: hidden; }
body.receiver .settings-panel .field-row.confirming > div { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
body.receiver .settings-panel .confirm-prompt { font-size: 11px; color: var(--vfd-dim); }

body.receiver .settings-panel .action-btn.cancel { /* neutral cyan default */ }
body.receiver .settings-panel .action-btn.confirm { color: oklch(0.78 0.16 25); border-color: oklch(0.40 0.16 25); }
```

Total CSS additions: ~40 lines, all scoped under `body.receiver`.

## Architecture — Client JS

Extension to [internal/chassis/static/settings-drawer.js](../../../internal/chassis/static/settings-drawer.js). Three additions; all reuse 4A/4B's `toastChip` / `toastReboot` helpers (the latter gains an optional override-label argument — see below).

```js
// Provider-row switches. The catalog switches deliberately use
// data-catalog-field instead of data-field so the existing 4A bridge
// switch handler at internal/chassis/static/settings-drawer.js:187
// (`button.switch[data-field]`) does NOT match — otherwise both
// handlers would fire on click and the 4A path would POST to
// /receiver/settings/bridge with an unknown key.
drawer.querySelectorAll('button.switch[data-catalog-provider]').forEach(el => {
  el.addEventListener('click', () => {
    const id = el.dataset.catalogProvider;
    const field = el.dataset.catalogField; // "enabled" today
    const next = !el.classList.contains('on');
    el.classList.toggle('on', next);
    el.setAttribute('aria-pressed', next ? 'true' : 'false');
    saveCatalogProvider(id, field, next ? 'true' : 'false', () => {
      el.classList.toggle('on', !next);
      el.setAttribute('aria-pressed', !next ? 'true' : 'false');
    });
  });
});

// Global HLS-override switch.
const directHlsBtn = drawer.querySelector('button.switch[data-catalog-direct-hls]');
if (directHlsBtn) directHlsBtn.addEventListener('click', () => {
  const next = !directHlsBtn.classList.contains('on');
  directHlsBtn.classList.toggle('on', next);
  directHlsBtn.setAttribute('aria-pressed', next ? 'true' : 'false');
  saveDirectStreamHLS(next ? 'true' : 'false', () => {
    directHlsBtn.classList.toggle('on', !next);
    directHlsBtn.setAttribute('aria-pressed', !next ? 'true' : 'false');
  });
});

async function saveCatalogProvider(id, field, value, onError) {
  const form = new FormData();
  form.set(field, value);
  let body = {};
  try {
    const res = await fetch(`/receiver/settings/catalog/provider/${encodeURIComponent(id)}`,
      { method: 'POST', body: new URLSearchParams(form), credentials: 'same-origin' });
    body = await res.json().catch(() => ({}));
    if (res.ok && body.ok) return;
  } catch (_) {
    body = { chip: 'WRITE FAILED' };
  }
  if (body.errors) {
    // Shouldn't happen for switches (server only emits "must be true or false"),
    // but defensively toast and revert.
    toastChip('BAD INPUT');
  } else if (body.error) {
    toastChip(body.error);
  } else if (body.chip) {
    toastChip(body.chip);
  } else {
    toastChip('WRITE FAILED');
  }
  if (onError) onError();
}

async function saveDirectStreamHLS(value, onError) {
  // Same shape as saveCatalogProvider; only the URL and form key differ.
  // … omitted for brevity; implementation plan elaborates …
}

// Restore-defaults — inline two-step confirm state machine.
(function initRestoreDefaults() {
  const row = document.getElementById('restore-defaults-row');
  if (!row) return;
  const idleBtn = document.getElementById('restore-defaults-btn');
  const result = document.getElementById('restore-defaults-result');
  if (!idleBtn || !result) return;

  let armTimer = null;

  // DOM construction uses createElement + textContent + replaceChildren
  // rather than innerHTML, even though the content is fully static. The
  // confirm-prompt text and button labels are template-literal constants,
  // not interpolated user input, but modelling safe DOM patterns here
  // prevents an implementer from later sprinkling interpolation into the
  // same code path and reintroducing an XSS surface. See §Edge Cases.
  function toIdle() {
    if (armTimer) { clearTimeout(armTimer); armTimer = null; }
    row.classList.remove('confirming');
    const rightCell = idleBtn.parentElement;
    rightCell.replaceChildren(idleBtn, result);
    idleBtn.disabled = false;
  }

  function toArmed() {
    row.classList.add('confirming');
    const rightCell = idleBtn.parentElement;
    const prompt = document.createElement('span');
    prompt.className = 'confirm-prompt';
    prompt.textContent = 'This wipes config.toml. ';
    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'action-btn cancel';
    cancelBtn.type = 'button';
    cancelBtn.textContent = 'Cancel';
    cancelBtn.addEventListener('click', toIdle);
    const confirmBtn = document.createElement('button');
    confirmBtn.className = 'action-btn confirm';
    confirmBtn.type = 'button';
    confirmBtn.textContent = 'Confirm reset';
    confirmBtn.addEventListener('click', fire);
    rightCell.replaceChildren(prompt, cancelBtn, confirmBtn, result);
    armTimer = setTimeout(toIdle, 10_000);
  }

  async function fire() {
    if (armTimer) { clearTimeout(armTimer); armTimer = null; }
    row.querySelectorAll('button').forEach(b => { b.disabled = true; });
    result.className = 'action-result';
    result.textContent = '';
    let body = {};
    try {
      const res = await fetch('/receiver/settings/action/restore-defaults',
        { method: 'POST', credentials: 'same-origin' });
      body = await res.json().catch(() => ({}));
      if (res.ok && body.ok && body.scope === 'reboot') {
        toIdle();
        result.className = 'action-result shown ok';
        result.textContent = '▸ Defaults restored · restart to apply';
        toastReboot(null, 'Defaults restored — restart container to apply');
        return;
      }
    } catch (_) {
      body = { chip: 'WRITE FAILED' };
    }
    toIdle();
    if (body.chip) {
      toastChip(body.chip);
    } else if (body.error) {
      result.className = 'action-result shown err';
      result.textContent = `▸ ERROR · ${body.error}`;
    } else {
      result.className = 'action-result shown err';
      result.textContent = '▸ ERROR · unknown';
    }
  }

  idleBtn.addEventListener('click', toArmed);
})();
```

The helper names used in the JS sketches above (`toastReboot`, `toastChip`, `showNotice`) are illustrative — the actual 4A implementation in [internal/chassis/static/settings-drawer.js](../../../internal/chassis/static/settings-drawer.js) calls them `showNotice(text, variant)` and `clearNotice()`. The 4C extension does not need to rename anything; the implementation plan binds the sketch's `toastReboot` / `toastChip` calls to the existing `showNotice` calls with the appropriate `'ok'` / `'err'` variant. `showNotice` already gains an optional override-label argument as 4A's:

```js
// 4A signature: toastReboot(fieldLabel)
// 4C signature: toastReboot(fieldLabel, overrideText)
// When overrideText is non-null, the notice text becomes overrideText
// verbatim (used by restore-defaults). When null, the 4A formatting
// "Restart container to apply <field>" applies as before.
```

## Architecture — Streams Adapter Changes

### Why no `scopeForField` edit

Earlier drafts of 4C edited `internal/adapters/streams/config.go`'s `scopeForField` to return `ScopeRestartCast` for `providers.<id>.hls_buffer_disabled` keys. That edit was dropped after verifying that `scopeForField` is **not consumed by any production code path**: a repo-wide search confirms it is referenced only by its own unit test (`config_test.go:97`). The production scope surfaces inside the streams adapter are `FieldDef.ApplyScope` returned by `Adapter.Fields()` at [adapter.go:197](../../../internal/adapters/streams/adapter.go#L197) (consumed by the legacy `/ui/*` generic adapter form) and `configChangeScope` at [adapter.go:390](../../../internal/adapters/streams/adapter.go#L390) (consumed by `Adapter.ApplyConfig` and the new `ApplyConfigValue`). Neither currently treats per-provider HLS-disabled keys as RECAST; editing them would require touching both surfaces and runs counter to 4C's "narrow chassis-isolated change" posture. The chassis Catalog pane gets the right wire scope (RECAST) regardless, via the production wrapper's declared-scope floor — see §Architecture — Production Wrappers.

**Why `shouldBufferDirectHLS` semantics still justify RECAST as the chassis-side declared scope.** [`shouldBufferDirectHLS`](internal/adapters/streams/playback.go#L493) reads `providerCfg.HLSBufferDisabled` and `channelCfg.HLSBufferDisabled` per queue-item start. The flag has no effect on a stream once the pipeline is wired (no live re-evaluation hook). The operator-visible intent for the Catalog pane toggle is "flip the HLS posture for this provider" — RECAST honors that by dropping the active cast so the next play picks up the new flag.

### Edit 1: `Adapter.ConfigSnapshot() Config`

New public read-only method in [internal/adapters/streams/adapter.go](../../../internal/adapters/streams/adapter.go):

```go
func (a *Adapter) ConfigSnapshot() Config {
    a.mu.Lock()
    defer a.mu.Unlock()
    return deepCopyConfig(a.cfg)
}

func deepCopyConfig(in Config) Config {
    out := in
    if in.RemoteProviderAllowedHosts != nil {
        out.RemoteProviderAllowedHosts = append([]string(nil), in.RemoteProviderAllowedHosts...)
    }
    if in.Providers != nil {
        out.Providers = make(map[string]ProviderConfig, len(in.Providers))
        for id, pc := range in.Providers {
            pcCopy := pc
            if pc.Channels != nil {
                pcCopy.Channels = make(map[string]ChannelConfig, len(pc.Channels))
                for cid, cc := range pc.Channels {
                    pcCopy.Channels[cid] = cc
                }
            }
            out.Providers[id] = pcCopy
        }
    }
    return out
}
```

### Edit 2: `Adapter.ApplyConfigValue`

New public method in [internal/adapters/streams/adapter.go](../../../internal/adapters/streams/adapter.go):

```go
func (a *Adapter) ApplyConfigValue(newCfg Config, save func(name string, raw []byte) error) (adapters.ApplyScope, error) {
    if err := newCfg.Validate(); err != nil {
        return 0, err
    }
    defs, catalogs, err := buildStartupSnapshot(context.Background(), newCfg, a.cacheDir)
    if err != nil {
        return 0, err
    }
    tomlBytes, err := encodeSectionTOML(newCfg)
    if err != nil {
        return 0, fmt.Errorf("streams: encode section: %w", err)
    }
    if err := save("streams", tomlBytes); err != nil {
        return 0, err
    }
    a.mu.Lock()
    oldCfg := a.cfg
    a.cfg = newCfg
    a.installSnapshotLocked(defs, catalogs)
    a.mu.Unlock()
    a.reconcileRefreshLoop()
    return configChangeScope(oldCfg, newCfg), nil
}

func encodeSectionTOML(cfg Config) ([]byte, error) {
    var buf bytes.Buffer
    if err := toml.NewEncoder(&buf).Encode(configToWire(cfg)); err != nil {
        return nil, err
    }
    return buf.Bytes(), nil
}
```

`buildStartupSnapshot` intentionally runs before `save`: rejected snapshot rebuilds leave both disk and in-memory streams state untouched. `ApplyConfigValue` returns the diff-derived `configChangeScope(oldCfg, newCfg)` only; the Catalog wrapper applies the declared-scope floor and dispatches `StopActiveCast` after a successful save/apply cycle.

### Edit 3: `Adapter.StopActiveCast`

New public method in [internal/adapters/streams/adapter.go](../../../internal/adapters/streams/adapter.go):

```go
func (a *Adapter) StopActiveCast() error {
    a.playbackMu.Lock()
    defer a.playbackMu.Unlock()

    a.mu.Lock()
    ref := activeAdapterRef(a.active)
    hadActive := a.active != nil
    coreManager := a.core
    if hadActive {
        a.clearActiveLocked()
    }
    a.mu.Unlock()

    if hadActive && coreManager != nil && ref != "" {
        _, err := coreManager.StopIfAdapterRef(ref)
        return err
    }
    return nil
}
```

This is the Catalog-side RECAST side effect. It uses the streams adapter's existing active `AdapterRef` and `SessionManager.StopIfAdapterRef` guard, so it cannot stop a non-streams core session and it does not require adding `DropActiveCast` to `streams.SessionManager` or `internal/core`. It differs from `Adapter.Stop()` by leaving the manifest refresh loop and adapter lifecycle state alone; only the active queue/session is cleared. Legacy `/ui/*` still calls `ApplyConfig` and therefore keeps its current behavior.

### Edit 4: `Catalog()` populates `Origin` and `Kind`

[internal/adapters/streams/adapter.go](../../../internal/adapters/streams/adapter.go) — extend the existing `Catalog()` builder so each returned `adapters.CatalogProvider` carries:

- `Origin string` — `url.Parse(providerDef.BaseURL).Host`. `BaseURL` is present on every `ProviderDefinition` ([provider.go:30](../../../internal/adapters/streams/provider.go#L30)). If `url.Parse` returns an error or an empty host (e.g., a malformed base URL slipped through the manifest validation), the wrapper falls back to `providerDef.PlaylistURL` parsed the same way; if both yield empty, `Origin = ""` and the template omits that segment of the `.stat` line. For the three bundled providers the host comes out as `wantmymtv.vercel.app` / `cartoonrewind.tv` / `api.toonamiaftermath.com`, matching the mockup verbatim.
- `Kind string` — `providerDef.Type` ([provider.go:25](../../../internal/adapters/streams/provider.go#L25)). Existing values are the constants `youtubeChannelJSONProviderType = "youtube-channel-json"` and `directStreamsProviderType = "direct-streams"` ([assets.go:13-14](../../../internal/adapters/streams/assets.go#L13-L14)) — already hyphenated, already matching the mockup `.stat` line.

### Edit 5: `adapters.CatalogProvider` shape extension

[internal/adapters/catalog.go](../../../internal/adapters/catalog.go) — add two string fields:

```go
type CatalogProvider struct {
    ID             string
    DisplayName    string
    BadgeLabel     string
    BadgeClass     string
    Origin         string // 4C: manifest URL host, e.g. "wantmymtv.vercel.app"
    Kind           string // 4C: provider-type tag, e.g. "youtube-channel-json"
    Live           bool
    DefaultChannel string
    Groups         []CatalogGroup
}
```

Backwards compatible — empty strings render cleanly in both the existing 3B browse drawer (which ignores them) and the new 4C Catalog pane (which omits the relevant stat-line segments when empty). No callers outside `internal/adapters/streams` populate these fields today.

## Architecture — Production Wrappers (`cmd/mister-groovy-relay/`)

### `cmd/mister-groovy-relay/catalog_manager.go` (new file)

```go
type catalogManager struct {
    adapter      *streams.Adapter
    adapterSaver *uiserver.AdapterSaver
}

func (m *catalogManager) Providers() []chassis.CatalogProviderState {
    cfg := m.adapter.ConfigSnapshot()
    cat := m.adapter.Catalog()
    out := make([]chassis.CatalogProviderState, 0, len(cat))
    for _, p := range cat {
        channels := 0
        for _, g := range p.Groups {
            channels += len(g.Channels)
        }
        pc := cfg.Providers[p.ID]
        out = append(out, chassis.CatalogProviderState{
            ID:                p.ID,
            DisplayName:       p.DisplayName,
            BadgeLabel:        p.BadgeLabel,
            BadgeClass:        p.BadgeClass,
            Origin:            p.Origin,
            Kind:              p.Kind,
            DefaultChannel:    p.DefaultChannel,
            Live:              p.Live,
            ChannelCount:      channels,
            Enabled:           !pc.Disabled,
            HLSBufferDisabled: pc.HLSBufferDisabled,
        })
    }
    return out
}

func (m *catalogManager) UpdateProvider(id string, patch chassis.CatalogProviderPatch) (adapters.ApplyScope, error) {
    scope, err := m.patch(func(cfg *streams.Config) {
        ensureProvider(cfg, id)
        pc := cfg.Providers[id]
        if patch.Enabled != nil {
            pc.Disabled = !*patch.Enabled
        }
        if patch.HLSBufferDisabled != nil {
            pc.HLSBufferDisabled = *patch.HLSBufferDisabled
        }
        cfg.Providers[id] = pc
    })
    if err != nil {
        return 0, err
    }
    return m.reportAndDispatch(scope, declaredProviderScope(patch))
}

// declaredProviderScope returns the max-wins declared scope across the
// patch's non-nil fields. Used as a floor so a no-op save (operator
// clicks a switch to its current value) still reports the wire scope
// the field's declared scope demands, matching the §Wire Contract.
func declaredProviderScope(patch chassis.CatalogProviderPatch) adapters.ApplyScope {
    s := adapters.ApplyScope(0)
    if patch.Enabled != nil {
        s = maxScope(s, adapters.ScopeHotSwap)
    }
    if patch.HLSBufferDisabled != nil {
        s = maxScope(s, adapters.ScopeRestartCast)
    }
    return s
}

func maxScope(a, b adapters.ApplyScope) adapters.ApplyScope {
    if a > b {
        return a
    }
    return b
}

func (m *catalogManager) SetDirectStreamHLSBuffer(disabled bool) (adapters.ApplyScope, error) {
    cat := m.adapter.Catalog()
    scope, err := m.patch(func(cfg *streams.Config) {
        if cfg.Providers == nil {
            cfg.Providers = map[string]streams.ProviderConfig{}
        }
        for _, p := range cat {
            if !p.Live {
                continue
            }
            pc := cfg.Providers[p.ID]
            pc.HLSBufferDisabled = disabled
            cfg.Providers[p.ID] = pc
        }
    })
    if err != nil {
        return 0, err
    }
    // Declared-scope floor: the global HLS toggle's declared scope is
    // RECAST regardless of diff outcome. Zero Live providers, or all-Live
    // already at the requested value, produce a no-op diff that
    // configChangeScope would otherwise report as HOT — mismatching the
    // §Wire Contract guarantee of scope:"recast".
    return m.reportAndDispatch(scope, adapters.ScopeRestartCast)
}

func (m *catalogManager) patch(apply func(*streams.Config)) (adapters.ApplyScope, error) {
    cfg := m.adapter.ConfigSnapshot()
    apply(&cfg)
    return m.adapter.ApplyConfigValue(cfg, m.adapterSaver.Save)
}

func (m *catalogManager) reportAndDispatch(actual, floor adapters.ApplyScope) (adapters.ApplyScope, error) {
    reported := maxScope(actual, floor)
    if reported == adapters.ScopeRestartCast {
        if err := m.adapter.StopActiveCast(); err != nil {
            return reported, err
        }
    }
    return reported, nil
}

func ensureProvider(cfg *streams.Config, id string) {
    if cfg.Providers == nil {
        cfg.Providers = map[string]streams.ProviderConfig{}
    }
    if _, ok := cfg.Providers[id]; !ok {
        cfg.Providers[id] = streams.ProviderConfig{}
    }
}
```

For the combined `enabled` + `hls_buffer_disabled` POST shape, the chassis handler builds a single `CatalogProviderPatch{Enabled: &e, HLSBufferDisabled: &h}` from the decoded form values and invokes `UpdateProvider` once. The wrapper's `patch` helper applies both fields to the same snapshot under one `ApplyConfigValue` cycle — one save, one disk write, one post-apply scope dispatch, one returned scope. `reportAndDispatch` runs only after the save/apply succeeds, so a failed write or rejected streams snapshot never drops the active cast.

### `cmd/mister-groovy-relay/config_reset.go` (new file)

```go
type configReset struct {
    path      string
    mu        *sync.Mutex       // BridgeSaver.Mu() — shared with bridge and adapter writes
    sectioned *config.Sectioned // source of truth for the operator's current data_dir
}

func (r *configReset) ResetToDefaults() error {
    r.mu.Lock()
    defer r.mu.Unlock()

    // Preserve the operator's current data_dir so persistent state
    // (device UUID, plex.tv token, streams cache, .first-run-complete)
    // stays chained to the post-reset config. Read sectioned.Bridge directly
    // while holding BridgeSaver.Mu(); do NOT call BridgeSaver.Current() here,
    // because Current() takes the same mutex and would deadlock.
    dataDir := ""
    if r.sectioned != nil {
        dataDir = r.sectioned.Bridge.DataDir
    }
    rendered, err := config.DefaultConfigTOML(dataDir)
    if err != nil {
        return &configResetError{cause: fmt.Errorf("render defaults: %w", err)}
    }
    if err := config.WriteAtomic(r.path, rendered); err != nil {
        return &configResetError{cause: fmt.Errorf("write: %w", err)}
    }
    return nil
}

type configResetError struct{ cause error }

func (e *configResetError) Error() string   { return e.cause.Error() }
func (e *configResetError) Unwrap() error   { return e.cause }
func (e *configResetError) StatusCode() int { return http.StatusInternalServerError }
func (e *configResetError) Chip() string    { return "WRITE FAILED" }
```

### `cmd/mister-groovy-relay/main.go` extension

After the streams adapter and `AdapterSaver` are constructed (already done by step 7 of the startup sequence), wrap them and pass into `chassis.Config`:

```go
cm := &catalogManager{adapter: streamsAdapter, adapterSaver: adapterSaver}
cr := &configReset{path: cfgPath, mu: bridgeSaver.Mu(), sectioned: sec}
chassisCfg.CatalogManager = cm
chassisCfg.ConfigReset = cr
```

## Edge Cases

| Case | Behavior |
|---|---|
| Provider id appears in `Catalog()` but not in `cfg.Providers` (default state) | `Providers()` reports `Enabled=true, HLSBufferDisabled=false` (zero-value `ProviderConfig`). The save path's `ensureProvider` helper allocates the key on first mutation. |
| Operator toggles a provider rapidly (double-click) | Two POSTs fire; the second wins on disk via the shared `AdapterSaver` mutex. UI shows the last response's state (re-render on response). No optimistic-lock machinery. |
| Operator toggles a row while a save is in flight from another tab | Two-tab edits: last write wins; stale tab is stale until refresh. Same as 4A/4B's bridge save semantics. |
| `HLSBufferDisabled` flipped via the global switch while a Live-provider cast is in flight | The wrapper reports RECAST after the declared-scope floor, then calls `streams.Adapter.StopActiveCast()`. That method clears the active streams queue and calls `SessionManager.StopIfAdapterRef(ref)` with the active streams `AdapterRef`; the cast stops and the next play uses the new HLS posture. Operator sees the cast end. |
| `Enabled` flipped to false on the provider currently being cast | HOT → `streams.Adapter.ApplyConfigValue` updates `cfg.Providers[id].Disabled` to true. The cast continues — the streams adapter does not preempt in-flight casts for the disabled flag (matches legacy adapter behavior). On next channel switch or queue advance within that provider, the disabled flag gates queue selection. To stop the in-flight cast immediately, the operator clicks the chassis transport stop. |
| Global HLS switch toggled when zero providers are Live | `SetDirectStreamHLSBuffer` no-ops on the in-memory config; `ApplyConfigValue` still writes the byte-identical TOML (mtime refreshes via atomic rename — no inotify watcher exists on `config.toml` today, but the rename is observable to external file-watchers if added later) and still serializes through `a.mu` and `reconcileRefreshLoop`. Observable state: no change. Response: `200 scope:"recast"` via the declared-scope floor inside the wrapper. Switch state on next render reflects "mixed → all-off → all-on" semantics over zero items (none flipped, still all-off). |
| Operator flips global HLS switch from mixed-state (renders as off) | Mockup contract: mixed state renders as off (§Design Decisions row "Global HLS-override semantic"). First click toggles to on → POST `disabled=true` → wrapper applies `HLSBufferDisabled=true` to **every** Live provider, regardless of prior individual state. Second click toggles back to off → POST `disabled=false` → wrapper applies `HLSBufferDisabled=false` to every Live provider. This means a click on a mixed-state-rendered-as-off switch is a destructive "synchronize all live providers to disabled" operation, not a "leave alone" no-op. Operator who edited per-provider HLS flags via `config.toml` (or via a future per-row UI) and then clicks the global switch will lose that individual customization. Manual verification (§Testing — JS) should walk the mixed → on → off sequence explicitly so this is exercised on every implementer's test pass. |
| Provider id contains URL-unsafe characters | The chassis handler URL-decodes `{id}` via `r.PathValue("id")` (Go 1.22+ handles percent-encoding). The streams adapter's `Catalog()` IDs are constrained to kebab-case slugs at definition time; arbitrary unicode is not a concern. The handler's "unknown provider" 404 covers any garbage id that survives decoding. |
| Concurrent chassis save + legacy `/ui/*` streams form save | Both writes serialize through the shared `AdapterSaver` mutex; last writer wins on disk. Both paths call `ApplyConfig` (the legacy form's path) or `ApplyConfigValue` (chassis path); both end at the same `configChangeScope`. No data corruption. |
| `config.DefaultConfigTOML(dataDir)` produces output that fails `LoadSectioned` or `Sectioned.Validate()` | Theoretical bug, not a runtime concern. The new exported helper has a precondition unit test asserting it parses and validates cleanly for both empty and arbitrary-absolute `dataDir`. The `configReset` wrapper does not call `Validate()` before writing — defaults are trusted by construction (the embedded `example.toml` is committed to the repo and exercised by `LoadSectioned` first-run code paths). |
| Encode failure inside `DefaultConfigTOML` (e.g., `data_dir` template marker missing in the embedded TOML) | Wrapper returns `*configResetError{cause: fmt.Errorf("render defaults: ...")}` with `Chip()=="WRITE FAILED"`. Same chip as disk-write failures because both are indistinguishable to the operator and the operator-visible action is the same (re-attempt). |
| `ResetToDefaults` is invoked while a bridge save is in flight | The shared mutex blocks. The reset waits for the in-flight save; then writes defaults. `configReset` reads `sectioned.Bridge.DataDir` under the already-held mutex and never calls `BridgeSaver.Current()`, so there is no self-deadlock. No partial-state interleaving. |
| `ResetToDefaults` is invoked while a chassis Catalog save is in flight | Same — shared mutex serializes. Order is operator-determined (whichever request reached the lock first). |
| Hand-edit of `config.toml` between page load and a Catalog save | Drawer renders the stale state at page load; the Catalog save reads the live in-memory config (via `ConfigSnapshot`), so the hand edits made through the file system are visible only after a chassis page refresh **and** they participate correctly in the save. Worth noting that the chassis save then overwrites whichever hand edit conflicts with the operator's toggle, but other untouched fields are preserved. |
| Operator JS-disabled | The Catalog pane renders all rows + switches but clicks have no effect. Direct curl POSTs against `/receiver/settings/catalog/provider/<id>` still work (LAN tooling). The restore-defaults row renders the button but the inline-confirm cannot arm; direct curl POSTs against `/receiver/settings/action/restore-defaults` still work (and bypass the confirm step). Same posture as 4A/4B. |
| Help-text characters needing escape | `html/template` auto-escapes every `.stat`, `.name`, and `.DefaultChannel` value at render time. The mockup's `<code>` element on the `.stat` line wraps user-provided `DefaultChannel` (auto-escaped); no XSS surface. The fixed help text on the restore-defaults row contains `<code>config.toml</code>` and `<code>data_dir</code>` literal markup that the chassis templates render verbatim (template author trust). |
| Client-side DOM construction for the confirm prompt | The restore-defaults JS does **not** use `innerHTML` even for the static confirm-prompt content; it uses `createElement` + `textContent` + `replaceChildren` so a future implementer who adds dynamic content (e.g., echoing the failing field name into a future confirm dialog) does not introduce an XSS surface by extending the existing pattern. The same posture applies to any future inline-confirm reuse (4E link-cascade flows are the likely next consumer). |
| Two providers share the same id (manifest misconfiguration) | The chassis `Providers()` returns whichever entry appears last in `Catalog()` (map-style override). A 4D-level fix in the streams adapter is the right home for duplicate-id rejection; 4C tolerates it (rare; operator-visible at the catalog drawer surface). |
| Operator deletes `config.toml` between drawer open and a save | The chassis save fails at `AdapterSaver.Save` with a typed error (`os.IsNotExist`). The handler emits `500 chip:"WRITE FAILED"`. Recovery: operator restarts the container (the bridge's startup re-creates `config.toml` with current in-memory state). |

## Testing

### Unit — chassis handlers (`internal/chassis/settings_test.go` extension)

Per-provider route:
- Success, `enabled=false` only → mock `UpdateProvider` called with `patch.Enabled=&false, patch.HLSBufferDisabled=nil`; `200 scope:"hot"`.
- Success, `hls_buffer_disabled=true` only → mock `UpdateProvider` called with `patch.Enabled=nil, patch.HLSBufferDisabled=&true`; `200 scope:"recast"`.
- Success, both keys → atomic save; `200 scope:"recast"` (max-wins).
- Unknown provider id → 404 `{ok:false, error:"unknown provider"}`; mock NOT called.
- Bad bool (`enabled=maybe`) → 400 `errors:{enabled:"must be true or false"}`; mock NOT called.
- Empty body → 400 `chip:"BAD INPUT"`.
- Mock returns `settingsChipError` (status 409, chip "PORT IN USE") → 409 chip propagated.
- Mock returns unexpected error → 500 `chip:"WRITE FAILED"`.
- Nil manager → 503 `chip:"NOT READY"`.
- Wrong origin → 403.

Global HLS-override route:
- Success, `disabled=true` → mock `SetDirectStreamHLSBuffer(true)` called; `200 scope:"recast"`.
- Success, `disabled=false` → opposite arg.
- Bad bool → 400 `errors:{disabled:"must be true or false"}`.
- Empty / missing key → 400 `chip:"BAD INPUT"`.
- Chip error / unexpected / nil / wrong origin — same shapes as per-provider route.

Restore-defaults route:
- Success → mock `ResetToDefaults()` called; `200 scope:"reboot"`.
- Mock returns `*configResetError` → 500 `chip:"WRITE FAILED"`.
- Nil `ConfigReset` → 503 `chip:"NOT READY"`.
- Wrong origin → 403.

Approx 20 handler tests.

### Unit — template rendering (`internal/chassis/chassis_test.go` extension)

Catalog pane:
- Renders one `.provider-row` per `SettingsData.CatalogProviders` entry; `<h4>` hint shows correct PROVIDERS + CHANNELS counts.
- Provider row DOM matches the mockup structure for each badge variant (default, `cartoon`, `toonami`).
- `.stat` line omits the `· default: <code>` segment when `DefaultChannel == ""`, and omits origin/kind separators when either optional value is empty.
- Switch reflects `Enabled` (class + aria-pressed); `data-catalog-provider` + `data-catalog-field="enabled"` attributes present; **`data-field` attribute is absent** (collision guard against the existing 4A bridge-switch handler).
- Global HLS switch reflects `DirectStreamHLSBufferDisabled`; `data-catalog-direct-hls` attribute present; **`data-field` attribute is absent**; `.scope.recast` badge present.
- Empty providers list → section heading shows `0 PROVIDERS · 0 CHANNELS`; no rows; HLS-override section still renders.
- Catalog stub gone; `settings-drawer.html` invokes `settings-catalog`.

Advanced pane / restore-defaults:
- Diagnostics section present after Logging.
- `#restore-defaults-btn` renders with `⚠ Reset…` text and destructive inline style.
- `#restore-defaults-result` renders empty.
- `.scope.reboot` badge present on the row.

`buildSettingsData`:
- 3-provider input with one Live (`HLSBufferDisabled=false`) → `CatalogPaneProviderCount=3`, `CatalogChannelCount=sum`, `DirectStreamHLSBufferDisabled=false`.
- All Live providers disabled → `DirectStreamHLSBufferDisabled=true`.
- Mixed state (one Live disabled, one Live enabled) → `DirectStreamHLSBufferDisabled=false`.
- No Live providers → `DirectStreamHLSBufferDisabled=false`.
- Nil `CatalogManager` → fallback to `StreamsCatalogViewer.Catalog()` for the tab-badge `CatalogProviderCount`; `CatalogPaneProviderCount=0`, `CatalogProviders` empty.

Approx 12 template tests.

### Unit — streams adapter

`internal/adapters/streams/config_test.go`: no edits. 4C does not modify `scopeForField` (see §Architecture — Why no `scopeForField` edit). The existing assertions are unaffected.

`internal/adapters/streams/adapter_test.go`:
- `ConfigSnapshot()` has independent `Providers` map (mutate → no effect on adapter).
- `ConfigSnapshot()` has independent per-provider `Channels` map.
- `ConfigSnapshot()` has independent `RemoteProviderAllowedHosts` slice.
- `ApplyConfigValue` success → saver called once with non-empty TOML; adapter's `cfg` updated; returns expected `ApplyScope`.
- `ApplyConfigValue` validation failure → no save call; no mutation; returns validation error.
- `ApplyConfigValue` startup-snapshot rebuild failure → no save call; no mutation; returns rebuild error.
- `ApplyConfigValue` save failure → no in-memory mutation; returns save error.
- `StopActiveCast()` with an active streams queue → calls fake core `StopIfAdapterRef` with the active `AdapterRef`, clears the active queue, and leaves the refresh loop running.
- `StopActiveCast()` with no active queue or a foreign core session → no-op / guarded no-op; does not stop unrelated sessions.
- `Catalog()` populates `Origin` (parsed `BaseURL.Host`, with fallback to `PlaylistURL.Host`) and `Kind` (`providerDef.Type`) on each returned `CatalogProvider`. Test the three bundled providers' expected outputs verbatim: `mtv-rewind → wantmymtv.vercel.app + youtube-channel-json`, `cartoon-rewind → cartoonrewind.tv + youtube-channel-json`, `toonami-aftermath → api.toonamiaftermath.com + direct-streams`.

Approx 11 streams tests.

### Unit — production wrappers (`cmd/mister-groovy-relay/`)

`catalog_manager_test.go` (new):
- `Providers()` enriches `adapters.CatalogProvider` with `cfg.Providers[id].Disabled` and `HLSBufferDisabled`; absent provider → `Enabled=true, HLSBufferDisabled=false`.
- `UpdateProvider("mtv-rewind", {Enabled: &false})` → on-disk + in-memory updated; returns HOT scope.
- `UpdateProvider("toonami-aftermath", {HLSBufferDisabled: &true})` → updated; returns RECAST scope (proves §6 edit 1 is wired through).
- `UpdateProvider("toonami-aftermath", {Enabled: &true, HLSBufferDisabled: &true})` → both fields updated in one save; returns RECAST (max-wins).
- `UpdateProvider("mtv-rewind", {HLSBufferDisabled: &false})` on a provider already at `HLSBufferDisabled=false` → no-op diff; **returns RECAST**, not HOT (declared-scope floor).
- `UpdateProvider("toonami-aftermath", {HLSBufferDisabled: &true})` with an active streams cast → calls `StopActiveCast()` after the successful save/apply cycle.
- `UpdateProvider("mtv-rewind", {Enabled: &false})` with an active streams cast → does **not** call `StopActiveCast()`.
- `SetDirectStreamHLSBuffer(true)` with one Live + one non-Live → only Live flipped; returns RECAST.
- `SetDirectStreamHLSBuffer(true)` with zero Live providers → no providers flipped; **returns RECAST** via the declared-scope floor (not HOT).
- `SetDirectStreamHLSBuffer(true)` with all Live providers already at `HLSBufferDisabled=true` → no-op diff; **returns RECAST** via the declared-scope floor.

`config_reset_test.go` (new):
- `ResetToDefaults()` rewrites `config.toml` with `config.DefaultConfigTOML(currentDataDir)` (where `currentDataDir` comes from the shared `*config.Sectioned` while `BridgeSaver.Mu()` is held); resulting file's `bridge.data_dir` matches the supplied value byte-for-byte.
- `ResetToDefaults()` does not call `BridgeSaver.Current()` while holding the shared mutex; the test completes under a short timeout to catch self-deadlock regressions.
- Disk write failure (read-only directory) → `*configResetError`; `StatusCode()==500`, `Chip()=="WRITE FAILED"`.
- Atomic rename: original file intact on simulated mid-write failure.
- `data_dir` sentinel file untouched after reset.

Approx 13 wrapper tests.

### Cross-side drift catchers — `tests/integration/catalog_scope_test.go` (new)

Boots real `streams.Adapter` + real `catalogManager` against a fixture `config.toml`:
- `UpdateProvider("toonami-aftermath", {HLSBufferDisabled: &true})` → returned scope is `ScopeRestartCast`; chassis wire label is `"recast"`.
- `UpdateProvider("toonami-aftermath", {Enabled: &false})` → returned scope is `ScopeHotSwap`; chassis wire label is `"hot"`.
- `UpdateProvider("toonami-aftermath", {HLSBufferDisabled: &true})` while streams owns the active core session → active session is stopped via `StopIfAdapterRef`.

Approx 3 tests. Reasoning per 4B precedent: catches drift between the wrapper-reported scope and the chassis label mapping / runtime side effect that would silently mis-toast or fail to recast for operators.

### Integration — `tests/integration/chassis_test.go` extension

End-to-end against real chassis + real streams + real saver + tmp `config.toml`:
- `GET /receiver` renders `.provider-row` × N, HLS-override row, restore-defaults row in Advanced.
- `POST /receiver/settings/catalog/provider/mtv-rewind` with `enabled=false` → `200 scope:"hot"`; disk + memory updated; re-render shows `.switch` (no `.on`).
- `POST /receiver/settings/catalog/provider/toonami-aftermath` with `hls_buffer_disabled=true` → `200 scope:"recast"`; `shouldBufferDirectHLS` returns false for a queue item from this provider after the save.
- Same HLS toggle while a streams cast is active → the active streams session is stopped before the response path is considered successful.
- `POST /receiver/settings/catalog/provider/toonami-aftermath` with `enabled=true&hls_buffer_disabled=true` → `200 scope:"recast"` (max-wins); both flags persisted.
- `POST /receiver/settings/catalog/direct-stream-hls-buffer` with `disabled=true` → `200 scope:"recast"`; every Live provider gets the flag.
- `POST /receiver/settings/catalog/provider/does-not-exist` → 404; no disk write.
- `POST /receiver/settings/action/restore-defaults` against a non-default tmp config → `200 scope:"reboot"`; disk content equals `config.DefaultConfigTOML(currentDataDir)`; `bridge.data_dir` value is preserved verbatim from the pre-reset config; in-memory adapter/bridge state unchanged; a sentinel file in `data_dir` survives.
- `POST /receiver/settings/action/restore-defaults` against a read-only tmp `config.toml` → 500 `chip:"WRITE FAILED"`; file content intact.

Approx 9 integration tests.

### JS behavior — manual verification checklist

Same pattern as 4A/4B (no JS test runner today). The implementer walks this before declaring 4C done:

- Click each provider-row switch → POST fires with correct id + form key; optimistic toggle holds on success.
- Click the global HLS-override switch → POST fires; receives `scope:"recast"`; in-flight cast (if any) drops as the RECAST side effect lands (verify against fake-mister or real MiSTer).
- Click `⚠ Reset…` → button row morphs to inline confirm (`Cancel | Confirm reset`); scope badge hides; `.field-row.confirming` is on the row.
- Click `Cancel` → row reverts to idle; no network request in DevTools Network panel.
- Click `⚠ Reset…`, wait 11 seconds → row auto-reverts silently; DevTools Network panel shows zero requests fired by the timeout.
- Click `⚠ Reset…` → `Confirm reset` → drawer-local notice toasts `Defaults restored — restart container to apply`; `.action-result.shown.ok` reads `▸ Defaults restored · restart to apply`; on-disk `config.toml` matches defaults.
- Force WRITE FAILED (chmod 0444) → `.action-result.shown.err` reads `▸ ERROR · ...`; drawer-local notice toasts `WRITE FAILED`.
- Catalog tab badge count matches live provider count after each enabled flip (badge re-renders on page refresh).
- Toggle off all Live providers' HLS via curl → reload drawer → global HLS switch reads `.switch.on` (mixed → all-off transition).
- Mixed-state walk-through (Important: covers the per-provider-data-loss case): start with one Live provider HLS-on, one HLS-off (mixed). Reload drawer — global switch renders off. Click on → all Live providers go to HLS-disabled (verify via two GETs of `/receiver` between clicks). Click off → all Live providers go to HLS-enabled. Confirms the "click destroys mixed state" semantics documented in §Edge Cases.
- Refresh between any save and the next interaction → page renders current saved state from `CatalogManager.Providers()`.
- Network exception during any save → drawer-local notice toasts `WRITE FAILED`; controls remain interactive.
- JS disabled → Catalog pane and restore-defaults row render; no click handlers fire.

### Test pattern budget

Estimated: ~20 handler + ~12 template + ~11 streams + ~13 wrapper + ~3 cross-side + ~9 integration ≈ **68 tests**. Mid-pack between 4A (~55) and 4B (~75).

## Forward Compatibility

- **Phase 4D** (Adapters, simple cases) introduces `POST /receiver/settings/adapter/{name}` and the `AdapterSettingsSaver` chassis-owned interface satisfied by `*uiserver.AdapterSaver`. Same JSON envelope; per-adapter scope dispatch. 4D's streams adapter form is the natural home for `manifest_url` editing, `additional_manifest_urls` (if added later), per-provider `catalog_refresh_hours`, and a `RefreshNow` action button — all deferred from 4C. 4D may extend `adapters.CatalogProvider` further (e.g., `LastFetchAt time.Time`) if observability-adjacent fields land in the catalog drawer's read view.
- **`Origin` / `Kind` field semantics.** 4C's canonical surface for these new `adapters.CatalogProvider` fields is the chassis Catalog pane's `.stat` line. Any future panel that consumes them inherits the "URL host of `BaseURL` + provider-type tag" semantics; if a future feature needs a richer provider-description string, the right move is a new field (`StatLine string`?) rather than overloading `Origin` / `Kind`. The existing 3B browse drawer ignores both fields today; it can adopt them in a future spec without coordination because empty strings render cleanly.
- **Phase 4E** (Plex / Jellyfin link cascades) may introduce a richer confirm-modal pattern for "Unlink Plex" / "Re-link Plex" actions. 4C's inline two-step confirm is intentionally minimal; the new CSS surface (`.field-row.confirming`, `.action-btn.cancel`, `.action-btn.confirm`) does not preclude a richer modal — the modal pattern can ignore the inline classes without conflict, or 4E can extract a shared confirm primitive that subsumes both.
- **Phase 4F** (URL adapter custom widgets) introduces yt-dlp host tag-list + cookies textarea. The tag-list pattern could become relevant to a future "custom manifest URLs" feature in the streams adapter (4D-level) — same chip-style add/remove pattern. 4F's CSS is reusable.
- **Phase 5 polish candidates from 4C-touched surfaces:**
  - Activity-ring and build-info rows in Advanced > Diagnostics (mockup [v24:4654-4671](../reference/2026-05-21-receiver-v24.html#L4654-L4671) lines 4656-4665).
  - "Reset creates `config.toml.pre-reset` backup" affordance.
  - Per-provider last-fetch timestamp + last-error in the Catalog pane provider row (observability surface).
  - `streams.Adapter.SaveTouched(apply func(*Config))` mirror of `BridgeSettingsSaver.SaveTouched` to close the snapshot → mutate → save TOCTOU window if concurrent-tab edits become a reported issue.
  - Richer confirm-modal pattern (typed confirmation) for irreversible destructive actions, sharing CSS with 4E's link-cascade flows.
- **Final chassis cutover** retires `/ui/*` once 4F lands. The `uiserver.{Bridge,Adapter}Saver` instances continue to exist (they're the saver layer regardless of UI); only the legacy `internal/ui/*` templates and routes are removed. The chassis Catalog pane becomes the only settings surface for per-provider toggles + global HLS override + restore-defaults. The three new streams adapter helpers (`ConfigSnapshot`, `ApplyConfigValue`, `StopActiveCast`) remain useful — none is a legacy-UI API.

## Appendix A — Catalog Pane Field Inventory

Total: 1 dynamic per-provider switch + 1 global switch + 1 destructive action.

**Catalog pane (variable rows):**

| Row | Form name | Scope | Field type | Where rendered |
|---|---|---|---|---|
| 1..N (per provider) | `enabled` on `POST /receiver/settings/catalog/provider/{id}` | **HOT** | switch (in provider-row partial) | Bundled providers section |
| Global | `disabled` on `POST /receiver/settings/catalog/direct-stream-hls-buffer` | **RECAST** | switch (in field-row) | Per-provider HLS buffer override section |

**Advanced pane Diagnostics extension:**

| Row | Form name | Scope | Field type |
|---|---|---|---|
| Diagnostics 1 | empty body on `POST /receiver/settings/action/restore-defaults` | **REBOOT** | action button (inline two-step confirm) |

Scope totals: 1 HOT (per provider), 1 RECAST (global HLS), 1 REBOOT (reset), 0 NEXT. Confirms 4C does not activate the dormant `.scope.next` badge — that activation awaits a feature with genuinely silent-for-current-cast semantics.
