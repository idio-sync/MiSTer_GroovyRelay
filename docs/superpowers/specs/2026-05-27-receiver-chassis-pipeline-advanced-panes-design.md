# Receiver Chassis Pipeline + Advanced Panes — Phase 4B Design

**Status:** Brainstormed; awaiting implementation plan.

**Scope:** Second sub-spec of Phase 4 (Settings & Adapters). Replaces the two "Spec 4B — implementation in progress" stub panes that ship in [Phase 4A](2026-05-27-receiver-chassis-settings-drawer-network-pane-design.md) with the real **Pipeline** pane (Video × 5, Audio × 2, MiSTer control × 2 + Launch core action) and **Advanced** pane (HLS buffer × 11, Logging × 1). Adds one new action route (`POST /receiver/settings/action/launch-core`), one chassis-owned interface (`CoreLauncher`), and one template helper (`humanizeBytes`). Reuses every primitive 4A established — field renderer, JSON envelope, scope dispatch, drawer-local toast slot, route prefix, same-origin posture — without modification.

**Repo location:** Committed under `docs/superpowers/specs/`. Force-added per the receiver chassis rollout convention.

## Context

[Phase 4A](2026-05-27-receiver-chassis-settings-drawer-network-pane-design.md) ships the settings drawer chrome, the Network pane (9 bridge fields + probe-mister action), the `field` template helper covering six input types, the JSON error envelope (`{ok, scope, errors, chip, error}`), the 4-tier scope badge taxonomy (HOT/NEXT/RECAST/REBOOT), the drawer-local `.settings-notice` slot, the `BridgeSettingsSaver`/`Prober`/`settingsChipError` chassis-owned interfaces, and the additive `*uiserver.settingsError` typed wrapper.

What 4A leaves behind: a working drawer with two visible-but-empty placeholder panes labelled "Spec 4B — implementation in progress." 4B fills them in.

Every Phase 4B field already exists in [internal/config/config.go](../../../internal/config/config.go) under `BridgeConfig.Video`, `BridgeConfig.Audio`, `BridgeConfig.MiSTer`, `BridgeConfig.HLSBuffer`, and `BridgeConfig.Logging`. Every per-field scope is already assigned in [`scopeForBridgeField`](../../../internal/uiserver/bridge_saver.go#L522-L563). Every hot-swap side effect (interlace order, SSH credential hot reload, debug log-level flip) is already wired in [`BridgeSaver.applyHotSwapSideEffects`](../../../internal/uiserver/bridge_saver.go#L313-L332). The SSH launcher already exists at [internal/misterctl/launcher.go](../../../internal/misterctl/launcher.go) and is wrapped by [`bridgeMisterLauncher` in cmd/mister-groovy-relay/launcher.go](../../../cmd/mister-groovy-relay/launcher.go#L25) for `internal/ui`'s consumption. 4B's job is wiring, decoding, and rendering — there is no new bridge-side behaviour to design.

**Mockup reference:** [`docs/superpowers/reference/2026-05-21-receiver-v24.html`](../reference/2026-05-21-receiver-v24.html). Pipeline pane lives at lines 4190-4269; Advanced pane at lines 4585-4651.

**What changed since 4A** (everything else is reuse):

| Change | Scope |
|---|---|
| New route `POST /receiver/settings/action/launch-core` | One handler, one CSS-free template row |
| New chassis-owned interface `CoreLauncher` | One method, one production binding (existing `bridgeMisterLauncher`) |
| New template helper `humanizeBytes(int64) string` | Used in 3 HLS rows |
| Four trivial template helpers (`boolStr`, `i64toa`, `list`, `passwordPlaceholder`) | One-line stringification each |
| New `FieldArgs.SkipEmpty bool` flag | Rendered as `data-skip-empty="true"` for password fields; client JS reads it |
| Add `internal/misterctl` to chassis forbidden-imports list | One-line edit to `import_check_test.go` |

**4A patterns 4B reuses verbatim (do not redesign):**

- `field` template helper with six types (text / number / password / path / select / switch) and the option-bag `dict` calling convention.
- Per-field auto-save: blur on text/number/password/path, change on select, click on switch.
- JSON envelopes — settings save: `{ok:true, scope:"hot|next|recast|reboot"}`, `{ok:false, errors:{<field>:"<msg>"}}`, `{ok:false, chip:"<chip>"}`; actions: `{ok:true, ...}`, `{ok:false, error:"<msg>"}`, `{ok:false, chip:"NOT READY"}` for wiring failures.
- Route prefix: `POST /receiver/settings/bridge` accepts any subset of bridge fields; `POST /receiver/settings/action/<name>` for action buttons.
- `BridgeSettingsSaver.Current()` is the source of truth for prefill and overlay base — never `cfg.Bridge`.
- Drawer-local `.settings-notice` slot for chip/REBOOT toasts; `.field-row .field-err` for per-field errors.
- `requireSameOrigin` middleware on every save and action route; same-origin posture, no CSRF token.
- `settingsChipError` structural interface for saver-layer typed errors.
- `internal/chassis/import_check_test.go` forbids `internal/ui`, `internal/uiserver`, and every concrete adapter package for `internal/chassis`. 4B **extends** the forbidden list by adding `internal/misterctl` — the `CoreLauncher` chassis-owned interface is the boundary, satisfied by the existing `bridgeMisterLauncher` from outside. Without this rule update, the implementer could accidentally `import "...internal/misterctl"` from `internal/chassis/settings.go` and pass tests.

## Goals

1. **Pipeline tab fully functional.** All 9 Pipeline fields (Video × 5, Audio × 2, MiSTer control × 2) render with current values, autosave on blur/change/click, validate inline, dispatch through `BridgeSaver.Save()` with the correct scope (HOT for interlace order, SSH user, SSH password; RECAST for everything else).
2. **Advanced tab fully functional.** All 12 Advanced fields (HLS buffer × 11, Logging × 1) render and autosave with correct scope (HOT for `logging.debug`; RECAST for every HLS field).
3. **Launch-core action works.** Clicking `▶ Launch core` in the MiSTer control section SSH-sends `load_core /media/fat/_Utility/Groovy_20240928.rbf` to `/dev/MiSTer_cmd` using the saved credentials and renders the result in the action's `.action-result` slot.
4. **Three new humanized byte labels render.** `hls_max_cache_bytes`, `hls_max_playlist_bytes`, `hls_max_segment_bytes` show their stored values converted to `"256 MB"`, `"1 MB"`, `"50 MB"` style hints in `.row-end` next to the input.
5. **Per-field error UX for HLS bounds.** Numeric out-of-range submissions surface as per-field `errors` messages (e.g., `"must be in [1, 12]"`) rather than a generic `BAD INPUT` chip.
6. **Zero new structural wire primitives.** No new envelope keys, no new scope tier, no new route prefix, no new field-renderer field type. The only non-trivial new template helper is `humanizeBytes`; four small stringification helpers (`boolStr`, `i64toa`, `list`, `passwordPlaceholder`) and one new `FieldArgs.SkipEmpty bool` flag are introduced as the minimum surface to render the two panes. 4C–4F continue to layer onto the same surface without further extensions to the core renderer.
7. **Chassis isolation contract extended.** `internal/chassis` still has zero imports of `internal/uiserver` or any concrete adapter, and 4B **adds** `internal/misterctl` to the chassis forbidden list at [internal/chassis/import_check_test.go:29-39](../../../internal/chassis/import_check_test.go#L29-L39). `import_check_test.go` then enforces all of these.
8. **`/ui/*` unchanged.** 4B is purely additive under `/receiver/*`. Cutover happens after 4F.

## Non-Goals

- **Diagnostics section in the Advanced pane.** The mockup shows three read-only diagnostic rows (activity ring, build info, reset-to-defaults) at [v24:4654-4671](../reference/2026-05-21-receiver-v24.html#L4654-L4671). Activity-ring/event-log integration belongs to Phase 5 (Observability). "Reset to defaults" is a destructive whole-config action that deserves its own brainstorm (confirm modal, scope dispatch, sentinel handling) — not folded into 4B.
- **Catalog, Adapters tabs.** Stubs continue to render the "Spec 4X — implementation in progress" placeholder card. 4C, 4D/4E/4F own those.
- **Probe-after-launch chaining.** The mockup's `"▸ Core loaded · session up · 4.2ms ACK"` suggests a status probe after the SSH command lands. 4B does not chain a probe — the success line is `"▸ Core sent · {host}"`, which is what the SSH call actually proves. An ACK-after-launch confirmation feature could be a Phase 5 polish.
- **Active-cast guard for launch-core.** Loading a core re-flashes the FPGA, which by definition terminates any in-flight session. The chassis does not block this — clicking `▶ Launch core` is the operator's deliberate action. Matches legacy `/ui/*` behaviour in `handleBridgeMisterLaunch` at [internal/ui/bridge.go:489-508](../../../internal/ui/bridge.go#L489-L508), which calls `MisterLauncher.Launch(ctx)` with no session-state check.
- **NEXT-scope exercise.** The 4-tier scope vocabulary 4A established is reused verbatim, but no 4B field is `ScopeNextCast` per [`scopeForBridgeField`](../../../internal/uiserver/bridge_saver.go#L522-L563) — the only NEXT field, `visualizer.mode`, lives on the visualizer bank and saves via `SaveVisualizerMode`, not the drawer. The `.scope.next` badge CSS still ships in 4A but stays dormant in 4B. 4C may activate it (Catalog per-provider HLS overrides are a possibility) — that decision belongs to 4C's brainstorm.
- **Field-renderer extensions.** No new field type. No bespoke widget. The Pipeline and Advanced panes are entirely buildable from 4A's six primitives.
- **`mister.host` / `mister.port` overlap.** Network owns those (REBOOT scope). Pipeline owns the SSH credentials (HOT). The `[bridge.mister]` TOML section header is intentionally split between two panes; the Pipeline section heading is "MiSTer control · SSH credentials" so the split reads honestly.
- **Cookies / yt-dlp hosts / tag-list patterns.** Those are URL-adapter specifics owned by 4F.
- **Live byte-unit recomputation.** The `humanizeBytes` hint goes mildly stale between an HLS save and the next page-load (input shows new value, hint shows old). Recomputing client-side as the operator types is feature creep for three rarely-edited fields. Page refresh corrects it.
- **Authentication / CSRF token.** Same LAN-only trust model and same-origin posture as 4A.

## Design Decisions

| Decision | Resolution |
|---|---|
| Pane file organization | Extract Pipeline and Advanced into their own template partial files (`settings-pipeline.html`, `settings-advanced.html`) defining `{{define "settings-pipeline"}}` / `{{define "settings-advanced"}}`. `settings-drawer.html` invokes them via `{{template "settings-pipeline" .Settings}}` etc. Mirrors the foundation spec's component-partial pattern (one file per chassis surface). Phase 4A's plan can collapse the placeholder stubs into the same shape so 4B is a clean swap; if 4A ships them as inline `{{define …}}` blocks instead, 4B extracts them as part of its work. |
| 4B field count vs 4A | 4A ships 9 Network fields. 4B ships 21 fields across two panes. The combined `bridgeFieldDecoders` table grows from 9 entries to 30 — still readable, still one file. No split. |
| Per-field decoder bounds for HLS | Each HLS numeric decoder enforces the same single-field bound `validateHLSBufferConfig` checks (e.g., `live_edge_segments ∈ [1, 12]`). Per-field error messages mirror the validator phrasing (`"must be in [1, 12]"`). Cross-field rules (`live_edge_segments ≥ start_segments`, `max_cached_segments ≥ start_segments`) stay in `Sectioned.Validate` and surface as `400 chip:"BAD INPUT"` — rare in practice, and worth catching server-side anyway. A unit test asserts every chassis-accepted boundary value passes `validateHLSBufferConfig`. |
| Humanized byte labels | New `humanizeBytes(int64) string` template helper rendering `"256 MB"`, `"1 MB"`, etc. Plugged through the field renderer's `Unit` option for `hls_max_cache_bytes`, `hls_max_playlist_bytes`, `hls_max_segment_bytes`. Server-rendered at template execution time. Goes stale between save and refresh — accepted. |
| Byte-unit base | Base-1024 (IEC) with SI suffixes (`KB`, `MB`, `GB`) — matches the mockup hints verbatim (`268435456 → "256 MB"` which is `256 × 1024 × 1024`). The technically-correct `KiB`/`MiB`/`GiB` suffixes are NOT used. Operator familiarity wins over technical purity. The decoder error messages (`"must be in [16 MB, 2 GB]"`) use the same convention. |
| Static "px" unit | `hls_max_variant_height` passes `Unit: "px"` to the field renderer — static string, no helper. |
| SSH password rendering | Render with `value=""` (empty attribute) regardless of stored value. Placeholder is `"••••••••"` when `BridgeConfig.MiSTer.SSHPassword != ""` and `"not set"` otherwise. The empty `value=""` keeps the stored plaintext out of the HTML response body. |
| SSH password autosave skip | Two layers. **Server overlay** preserves-on-empty: the `mister_ssh_password` overlay entry leaves `BridgeConfig.MiSTer.SSHPassword` unchanged when the submitted value is empty. Mirrors the legacy UI's behaviour at [internal/ui/bridge.go:126-133](../../../internal/ui/bridge.go#L126-L133). **Client guard** avoids the no-op POST: when an `<input class="field-input">` carries `data-skip-empty="true"` and blurs with `value === ""`, the JS save handler short-circuits without issuing a request. Only the password field carries the attribute today. |
| SSH password "clear" UX | Out of scope. Operators who need to clear the stored password edit `config.toml` directly. The LAN-only trust model and the rarity of the operation justify deferring the explicit-clear affordance. |
| Launch-core route shape | `POST /receiver/settings/action/launch-core`, empty body, reuses the action JSON envelope. Mirrors 4A's `probe-mister` exactly. |
| Launch-core interface | New chassis-owned `CoreLauncher` interface with one method, `Launch(ctx context.Context) error`. Satisfied structurally by the existing `bridgeMisterLauncher` from [cmd/mister-groovy-relay/launcher.go](../../../cmd/mister-groovy-relay/launcher.go). No chassis import of `internal/misterctl` or `internal/ui`. |
| Launch-core timeout | 6s context budget, matching legacy `/ui/*` at [internal/ui/bridge.go:494](../../../internal/ui/bridge.go#L494). 5s for the SSH dial + 1s slack. |
| Launch-core success line | `▸ Core sent · {host}` — what the SSH call actually proves. The host comes from `BridgeSettingsSaver.Current().MiSTer.Host`, snapshotted in the handler for the response body. |
| Launch-core empty-host handling | If `BridgeSettingsSaver.Current().MiSTer.Host == ""` → `400 {ok:false, error:"MiSTer host not configured (set bridge.mister.host)"}`. Operator sees `▸ ERROR · MiSTer host not configured (set bridge.mister.host)` in the result slot. The chassis pre-checks empty host (instead of letting `bridgeMisterLauncher.Launch` short-circuit and returning its error as a 500) so the response status is `400` (operator action required) rather than `500` (server error). The error string **matches the launcher's verbatim** at [cmd/mister-groovy-relay/launcher.go:33](../../../cmd/mister-groovy-relay/launcher.go#L33); a unit test asserts the two strings stay in sync. |
| Launch-core active-cast policy | Match legacy: no guard. Documented in the field-row help text via "Sends `load_core` to `/dev/MiSTer_cmd`." — the existing language implies a deliberate FPGA core swap. |
| Launch-core single-flight | Client disables `#launch-core-btn` while the POST is in flight; re-enables on response. Same pattern as `probe-mister`. |
| Cross-pane scope semantics | Each save touches at most one form key (autosave is per-field). The `BridgeSaver.Save` max-wins scope dispatch from 4A still applies — but with one-field touched, the returned scope equals `scopeForBridgeField(touchedKey)`. No new max-wins surprises from 4B. |
| Logging.debug HOT behaviour | No chassis-side wiring. `BridgeSaver.applyHotSwapSideEffects` already calls `logging.SetLevel("debug"|"info")` at [internal/uiserver/bridge_saver.go:324-330](../../../internal/uiserver/bridge_saver.go#L324-L330). 4B just adds the field decoder and overlay entry. |
| CSS additions in 4B | **None.** Every selector the two new panes need ships in 4A: `.field-row`, `.field-input` (+ `.has-value`, `.num`), `.switch` (+ `.on`), `.action-btn.primary`, `.action-result` (+ `.shown`, `.ok`, `.err`), `.scope` (+ `.hot`, `.recast`, `.next`, `.reboot`), `.row-end`, `.settings-section` (+ `.wide`), `.field-row.has-err`, `.field-err`. The mockup's inline `style="font-family:'Inter',sans-serif;"` on selects is dropped — `.field-input` already declares Inter. |
| Test cross-check | A single `TestHLSDecoders_BoundsSubsetOfValidator` unit test enumerates each chassis decoder's accepted boundary values and confirms `validateHLSBufferConfig` accepts a config containing each. Prevents drift between chassis bounds and validator bounds. |

## Implementation Checklist (sketch — implementation plan elaborates)

- [internal/chassis/settings.go](../../../internal/chassis/settings.go): extend with `handleSettingsActionLaunchCore` and the `CoreLauncher` interface declaration. The new handler reuses 4A's `sanitizeProbeError` helper for the error response field. Extend `bridgeFieldDecoders` and the form-name → `*config.BridgeConfig` overlay table with 21 new entries.
- [internal/chassis/import_check_test.go](../../../internal/chassis/import_check_test.go): add `modulePath + "/internal/misterctl"` to the `internal/chassis` `forbidden` slice at lines 29-39. The chassis-owned `CoreLauncher` interface is satisfied by the existing `bridgeMisterLauncher` from outside; the rule prevents an accidental future direct import.
- [internal/chassis/settings_test.go](../../../internal/chassis/settings_test.go): per-decoder branch tests for every new field (~30 tests), handler tests for launch-core (success / empty-host / launcher error / nil launcher / wrong origin), cross-check test that chassis HLS bounds ⊆ `validateHLSBufferConfig` bounds, and end-to-end overlay round-trip tests for one field per type.
- [internal/chassis/templates.go](../../../internal/chassis/templates.go): register `humanizeBytes` in the template FuncMap. Function signature `func(int64) string`, output style `"256 MB"` / `"1 MB"` / `"50 MB"` matching the mockup hints.
- [internal/chassis/templates/settings-pipeline.html](../../../internal/chassis/templates/settings-pipeline.html) (new): defines `{{define "settings-pipeline"}}`. Three `.settings-section` blocks (Video, Audio, MiSTer control). Each field is a `{{field (dict …) }}` invocation. The MiSTer control section ends with a manually-templated action-button row + `.action-result#launch-core-result` slot (the field helper does not render action buttons; the existing `.action-btn` / `.action-result` chassis CSS pattern is templated inline, same as 4A's probe-mister row).
- [internal/chassis/templates/settings-advanced.html](../../../internal/chassis/templates/settings-advanced.html) (new): defines `{{define "settings-advanced"}}`. Two `.settings-section` blocks (HLS buffer × 11 fields with humanized byte hints where applicable, Logging × 1 field).
- [internal/chassis/templates/settings-drawer.html](../../../internal/chassis/templates/settings-drawer.html) (one-line edit each): replace the two Pipeline / Advanced stub blocks with `{{template "settings-pipeline" .Settings}}` / `{{template "settings-advanced" .Settings}}`.
- [internal/chassis/static/settings-drawer.js](../../../internal/chassis/static/settings-drawer.js): extend with the launch-core click handler (single-flight, result rendering, chip/error path). Add the `data-skip-empty` guard for the SSH password input. No structural changes to the existing field save flow.
- [internal/chassis/server.go](../../../internal/chassis/server.go): extend `Config` with `CoreLauncher CoreLauncher` field; mount `POST /receiver/settings/action/launch-core` route in `RegisterRoutes` (or wherever 4A lands the action mount).
- [cmd/mister-groovy-relay/main.go](../../../cmd/mister-groovy-relay/main.go): pass the existing `bridgeMisterLauncher` instance into `chassis.Config.CoreLauncher`. The same instance already powers `/ui/*` via `ui.Config.MisterLauncher` — single source of truth.
- [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go): template render tests for the two new partials — every field row present, scope badges correct per field, humanized byte hint renders for the three bytes fields, SSH password placeholder reflects stored-value presence, launch-core action row renders with empty result slot.
- [tests/integration/chassis_test.go](../../../tests/integration/chassis_test.go): end-to-end coverage — one save per new field type (select / switch / number / password / text), launch-core success against a fake launcher, launch-core with empty host, out-of-range HLS field returns per-field error.

**Files intentionally unchanged in 4B:** `internal/uiserver/*` (4A's typed-error wrapper covers 4B without modification — the scope table and side-effect dispatch already handle every 4B field), `internal/misterctl/*`, `internal/core/*`, `internal/ui/*`, `internal/chassis/static/chassis.css` (4A ports every selector 4B needs), `internal/chassis/data.go` (4A's `SettingsData` already carries `Bridge config.BridgeConfig`, which holds all 4B fields).

## Wire Contract — HTTP Routes

### `POST /receiver/settings/bridge`

**Unchanged from 4A.** 4B adds 21 new accepted form keys to the decoder table; the route shape, envelope, status codes, and middleware are identical.

New decoder branches and their per-field error strings:

| Form name | Type | Validation | Error message |
|---|---|---|---|
| `video_modeline` | enum | one of `NTSC_480i`, `NTSC_240p`, `PAL_576i`, `PAL_288p` | `"must be one of NTSC_480i, NTSC_240p, PAL_576i, PAL_288p"` |
| `video_interlace_field_order` | enum | one of `tff`, `bff` | `"must be tff or bff"` |
| `video_aspect_mode` | enum | one of `auto`, `letterbox`, `zoom` | `"must be auto, letterbox, or zoom"` |
| `video_lz4_enabled` | bool | one of `true`, `false` | `"must be true or false"` |
| `video_delta_lz4_enabled` | bool | one of `true`, `false` | `"must be true or false"` |
| `audio_sample_rate` | enum-int | one of `22050`, `44100`, `48000` | `"must be 22050, 44100, or 48000"` |
| `audio_channels` | enum-int | one of `1`, `2` | `"must be 1 or 2"` |
| `mister_ssh_user` | text | non-empty after trim; no SSH-illegal characters (`:`, NUL, newline) | `"is required"` / `"contains an illegal character"` |
| `mister_ssh_password` | password | empty allowed (preserve-on-empty applies in overlay) | none |
| `hls_enabled` | bool | one of `true`, `false` | `"must be true or false"` |
| `hls_live_edge_segments` | int | `[1, 12]` | `"must be in [1, 12]"` |
| `hls_start_segments` | int | `[1, 6]` | `"must be in [1, 6]"` |
| `hls_max_cached_segments` | int | `[2, 24]` | `"must be in [2, 24]"` |
| `hls_max_cache_bytes` | int64 | `[16777216, 2147483648]` | `"must be in [16 MB, 2 GB]"` |
| `hls_max_playlist_bytes` | int64 | `[4096, 8388608]` | `"must be in [4 KB, 8 MB]"` |
| `hls_max_segment_bytes` | int64 | `[1048576, 536870912]` | `"must be in [1 MB, 512 MB]"` |
| `hls_segment_timeout_seconds` | int | `[1, 60]` | `"must be in [1, 60]"` |
| `hls_playlist_timeout_seconds` | int | `[1, 60]` | `"must be in [1, 60]"` |
| `hls_max_variant_height` | int | `[240, 2160]` | `"must be in [240, 2160]"` |
| `hls_stale_cache_reap_hours` | int | `[1, 168]` | `"must be in [1, 168]"` |
| `logging_debug` | bool | one of `true`, `false` | `"must be true or false"` |

Overlay entries map each form key to a `func(*config.BridgeConfig, any)` writing into the right struct path (e.g., `mister_ssh_password` → `func(c *config.BridgeConfig, v any) { if s, _ := v.(string); s != "" { c.MiSTer.SSHPassword = s } }` to bake preserve-on-empty into the overlay).

The byte bounds in error messages use `humanizeBytes`-style strings (`"16 MB"`) rather than raw integers (`"16777216"`) so the operator can match the message against the input. Internal validation still uses int64.

### `POST /receiver/settings/action/launch-core` (new)

**Headers:** browser-supplied `Sec-Fetch-Site: same-origin` or `same-site`. Enforced by `requireSameOrigin`; client JS must not set `Sec-*` headers manually.

**Body:** Empty.

**Server logic:**

1. `requireSameOrigin` middleware. Wrong origin → 403.
2. If `s.cfg.CoreLauncher == nil` or `s.cfg.BridgeSaver == nil` → 503 `{ok:false, chip:"NOT READY"}`.
3. Snapshot `cur := s.cfg.BridgeSaver.Current()`. If `cur.MiSTer.Host == ""` → 400 `{ok:false, error:"MiSTer host not configured (set bridge.mister.host)"}`.
4. `ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second); defer cancel()`.
5. Call `s.cfg.CoreLauncher.Launch(ctx)`.
6. On success → 200 `{ok:true, host:"192.168.1.42"}`.
7. On error → 500 `{ok:false, error:"<sanitized message>"}`. The handler **reuses 4A's `sanitizeProbeError` helper** from [internal/chassis/settings.go](../../../internal/chassis/settings.go) (introduced by 4A's final-review fix commit `d778940`), which redacts dotted-quad IPv4 (with optional `:port`) tokens and caps the message at 200 chars. SSH-layer errors rarely contain raw IPs (they read like `"ssh: handshake failed"`), but the redaction is defence-in-depth against future launcher changes that surface socket details.

**Responses:**

| Status | Body | When |
|---|---|---|
| 200 | `{"ok":true,"host":"192.168.1.42"}` | SSH dial + exec succeeded |
| 400 | `{"ok":false,"error":"MiSTer host not configured (set bridge.mister.host)"}` | Saved `mister.host` is empty |
| 403 | (middleware) | Wrong origin |
| 500 | `{"ok":false,"error":"ssh: <sanitized>"}` | Dial/auth/exec failure |
| 503 | `{"ok":false,"chip":"NOT READY"}` | `CoreLauncher` or `BridgeSettingsSaver` not wired (defensive; `main.go` always wires both) |

**Client renders into `#launch-core-result` (the `.action-result` div under the button):**

- Success: `▸ Core sent · {host}` with `.action-result.shown.ok`.
- Empty-host or SSH error: `▸ ERROR · {error}` with `.action-result.shown.err`.
- Chip response (NOT READY) or network exception: toast into the drawer-local `.settings-notice` slot; `.action-result` stays empty.

Single-flight: button is disabled while the POST is in flight.

## Architecture — `CoreLauncher` Interface

Declared in [internal/chassis/settings.go](../../../internal/chassis/settings.go) alongside `BridgeSettingsSaver`, `Prober`, and `settingsChipError`:

```go
// CoreLauncher is the chassis-side interface the launch-core action
// invokes. Production passes the bridgeMisterLauncher from
// cmd/mister-groovy-relay, which wraps internal/misterctl.LaunchGroovy
// with credentials snapshotted from BridgeSaver.Current() on each call.
// internal/chassis does not import internal/misterctl.
type CoreLauncher interface {
    Launch(ctx context.Context) error
}
```

`Server.Config` gains:

```go
type Config struct {
    // … existing 4A fields …
    CoreLauncher CoreLauncher // 4B: SSH-sends the canonical load_core command
}
```

`Server.RegisterRoutes` gains one new route mount:

```go
mux.Handle("POST /receiver/settings/action/launch-core",
    requireSameOrigin(http.HandlerFunc(s.handleSettingsActionLaunchCore)))
```

`cmd/mister-groovy-relay/main.go` passes the existing `bridgeMisterLauncher` instance into the new field. The same instance already serves `internal/ui` via `ui.Config.MisterLauncher` — one launcher, one timeout policy, one credential snapshot path. No duplication.

The chassis `CoreLauncher` interface is intentionally narrow (one method) rather than mirroring the legacy `ui.MisterLauncher` interface directly. Future launcher operations (e.g., a hypothetical "load arbitrary core" or "reset MiSTer" action) would extend this interface, not the legacy one.

## Architecture — Pipeline Pane Template

`internal/chassis/templates/settings-pipeline.html`:

```html
{{define "settings-pipeline"}}
  <div class="settings-pane" data-pane="pipeline">
    <div class="settings-section">
      <h4>Video <span class="hint">[bridge.video]</span></h4>

      {{ field (dict
          "Name"  "video_modeline"
          "Type"  "select"
          "Label" "Modeline"
          "Help"  "CRT output. PAL modes work over the wire but aren't tested on real PAL CRT hardware."
          "Value" .Bridge.Video.Modeline
          "Scope" "recast"
          "Options" (list
            (dict "Value" "NTSC_480i")
            (dict "Value" "NTSC_240p")
            (dict "Value" "PAL_576i" "Label" "PAL_576i (experimental)")
            (dict "Value" "PAL_288p" "Label" "PAL_288p (experimental)"))
          "Error" (errOf .Errors "video_modeline")
      ) }}

      {{ field (dict
          "Name"  "video_interlace_field_order"
          "Type"  "select"
          "Label" "Interlace order"
          "Help"  "Flip if you see shimmer on the CRT. Live hot-swappable."
          "Value" .Bridge.Video.InterlaceFieldOrder
          "Scope" "hot"
          "Options" (list (dict "Value" "bff") (dict "Value" "tff"))
          "Error" (errOf .Errors "video_interlace_field_order")
      ) }}

      {{ field (dict
          "Name"  "video_aspect_mode"
          "Type"  "select"
          "Label" "Aspect mode"
          "Help"  "How the source fits to 4:3 NTSC."
          "Value" .Bridge.Video.AspectMode
          "Scope" "recast"
          "Options" (list (dict "Value" "auto") (dict "Value" "letterbox") (dict "Value" "zoom"))
          "Error" (errOf .Errors "video_aspect_mode")
      ) }}

      {{ field (dict
          "Name"  "video_lz4_enabled"
          "Type"  "switch"
          "Label" "LZ4 compression"
          "Help"  "Compresses BLIT payloads. Strongly recommended."
          "Value" (boolStr .Bridge.Video.LZ4Enabled)
          "Scope" "recast"
      ) }}

      {{ field (dict
          "Name"  "video_delta_lz4_enabled"
          "Type"  "switch"
          "Label" "Delta-LZ4"
          "Help"  "Adaptive delta-compressed BLITs when they beat full-field LZ4."
          "Value" (boolStr .Bridge.Video.DeltaLZ4Enabled)
          "Scope" "recast"
      ) }}
    </div>

    <div class="settings-section">
      <h4>Audio <span class="hint">[bridge.audio]</span></h4>

      {{ field (dict
          "Name"  "audio_sample_rate"
          "Type"  "select"
          "Label" "Sample rate"
          "Help"  "PCM sample rate."
          "Value" (itoa .Bridge.Audio.SampleRate)
          "Scope" "recast"
          "Options" (list (dict "Value" "48000") (dict "Value" "44100") (dict "Value" "22050"))
          "Error" (errOf .Errors "audio_sample_rate")
      ) }}

      {{ field (dict
          "Name"  "audio_channels"
          "Type"  "select"
          "Label" "Channels"
          "Help"  "1 = mono · 2 = stereo"
          "Value" (itoa .Bridge.Audio.Channels)
          "Scope" "recast"
          "Options" (list (dict "Value" "2") (dict "Value" "1"))
          "Error" (errOf .Errors "audio_channels")
      ) }}
    </div>

    <div class="settings-section wide">
      <h4>MiSTer control <span class="hint">SSH credentials</span></h4>

      {{ field (dict
          "Name"  "mister_ssh_user"
          "Type"  "text"
          "Label" "SSH user"
          "Help"  "MiSTer's stock user is root."
          "Value" .Bridge.MiSTer.SSHUser
          "Scope" "hot"
          "Error" (errOf .Errors "mister_ssh_user")
      ) }}

      {{ field (dict
          "Name"  "mister_ssh_password"
          "Type"  "password"
          "Label" "SSH password"
          "Help"  "Leave empty to keep existing. Stored plaintext in config.toml (LAN-only trust model)."
          "Value" ""
          "Placeholder" (passwordPlaceholder .Bridge.MiSTer.SSHPassword)
          "SkipEmpty" true
          "Scope" "hot"
      ) }}

      <div class="field-row">
        <label>Launch GroovyMiSTer <span class="help">Sends <code>load_core /media/fat/_Utility/Groovy_20240928.rbf</code> to <code>/dev/MiSTer_cmd</code> using the credentials above.</span></label>
        <div></div>
        <button class="action-btn primary" id="launch-core-btn" type="button">▶ Launch core</button>
      </div>
      <div class="action-result" id="launch-core-result"></div>
    </div>
  </div>
{{end}}
```

**Two new tiny helpers** (sibling of `errOf`, `itoa`, `dict` from 4A):

- `boolStr(bool) string` — returns `"true"` or `"false"` for switch value coercion.
- `passwordPlaceholder(stored string) string` — returns `"••••••••"` when `stored != ""`, `"not set"` otherwise.
- `list(args ...any) []any` — wraps args into a slice for `Options` lists. Trivial.

The `SkipEmpty` field on `FieldArgs` is a new bool flag the field renderer translates into `data-skip-empty="true"` on the rendered `<input>`. Client JS reads the attribute to decide whether to skip an empty blur. Only `password` fields set it today; `text` and `path` fields always POST on blur because empty *is* a meaningful value for those.

## Architecture — Advanced Pane Template

`internal/chassis/templates/settings-advanced.html`:

```html
{{define "settings-advanced"}}
  <div class="settings-pane" data-pane="advanced">
    <div class="settings-section wide">
      <h4>HLS buffer <span class="hint">[bridge.hls_buffer] · SHARED CACHE</span></h4>

      {{ field (dict
          "Name"  "hls_enabled"
          "Type"  "switch"
          "Label" "Enabled"
          "Help"  "Buffer eligible live .m3u8 casts through a local segment cache."
          "Value" (boolStr .Bridge.HLSBuffer.Enabled)
          "Scope" "recast"
      ) }}

      {{ field (dict
          "Name"  "hls_live_edge_segments"
          "Type"  "number"
          "Label" "Live edge segments"
          "Help"  "Segments to stay behind the live edge."
          "Value" (itoa .Bridge.HLSBuffer.LiveEdgeSegments)
          "Scope" "recast"
          "InputWidth" "90px"
          "Error" (errOf .Errors "hls_live_edge_segments")
      ) }}

      <!-- … hls_start_segments, hls_max_cached_segments — same shape, InputWidth 90px … -->

      {{ field (dict
          "Name"  "hls_max_cache_bytes"
          "Type"  "number"
          "Label" "Max cache bytes"
          "Help"  "Per-session cache byte ceiling."
          "Value" (i64toa .Bridge.HLSBuffer.MaxCacheBytes)
          "Scope" "recast"
          "InputWidth" "130px"
          "Unit" (humanizeBytes .Bridge.HLSBuffer.MaxCacheBytes)
          "Error" (errOf .Errors "hls_max_cache_bytes")
      ) }}

      <!-- … hls_max_playlist_bytes (Unit: humanizeBytes …) … -->
      <!-- … hls_max_segment_bytes (Unit: humanizeBytes …) … -->
      <!-- … hls_segment_timeout_seconds, hls_playlist_timeout_seconds — InputWidth 90px … -->

      {{ field (dict
          "Name"  "hls_max_variant_height"
          "Type"  "number"
          "Label" "Max variant height"
          "Help"  "Highest master-playlist variant height eligible for buffering."
          "Value" (itoa .Bridge.HLSBuffer.MaxVariantHeight)
          "Scope" "recast"
          "InputWidth" "90px"
          "Unit" "px"
          "Error" (errOf .Errors "hls_max_variant_height")
      ) }}

      <!-- … hls_stale_cache_reap_hours … -->
    </div>

    <div class="settings-section">
      <h4>Logging</h4>

      {{ field (dict
          "Name"  "logging_debug"
          "Type"  "switch"
          "Label" "Debug logging"
          "Help"  "Emit verbose slog records (request traces, timeline pushes, subscriber prunes). Persisted across restarts."
          "Value" (boolStr .Bridge.Logging.Debug)
          "Scope" "hot"
      ) }}
    </div>
  </div>
{{end}}
```

New tiny helper: `i64toa(int64) string` (alongside `itoa`).

## Architecture — Client JS (`settings-drawer.js` extension)

Adds two small things to the file 4A introduces:

```js
// SSH password (and any future password-type field): skip empty-blur POST.
// The HTML carries data-skip-empty="true" on these inputs.
drawer.querySelectorAll('input.field-input[data-skip-empty="true"]').forEach(el => {
  el.addEventListener('blur', evt => {
    if (el.value === '') evt.stopImmediatePropagation();
  }, true);  // capture phase — runs before 4A's bound blur handler
});

// Launch core action — same shape as probe-mister.
const launchBtn = document.getElementById('launch-core-btn');
const launchOut = document.getElementById('launch-core-result');
if (launchBtn) launchBtn.addEventListener('click', async () => {
  if (launchBtn.disabled) return;
  launchBtn.disabled = true;
  launchOut.className = 'action-result';
  launchOut.textContent = '';
  try {
    const res = await fetch('/receiver/settings/action/launch-core',
      { method: 'POST', credentials: 'same-origin' });
    const body = await res.json().catch(() => ({}));
    if (body.chip) {
      toastChip(body.chip);
    } else if (body.ok) {
      launchOut.className = 'action-result shown ok';
      launchOut.textContent = `▸ Core sent · ${body.host || ''}`.trim();
    } else {
      launchOut.className = 'action-result shown err';
      launchOut.textContent = `▸ ERROR · ${body.error || 'unknown error'}`;
    }
  } catch (_) {
    launchOut.className = 'action-result shown err';
    launchOut.textContent = '▸ ERROR · network error';
  } finally {
    launchBtn.disabled = false;
  }
});
```

The capture-phase `stopImmediatePropagation` on the password field cleanly suppresses 4A's bound blur handler without 4A needing knowledge of the password-specific guard.

## Edge Cases

| Case | Behavior |
|---|---|
| Operator clicks Launch core during an active cast | SSH command lands; FPGA core swaps; the live Groovy session dies as the MiSTer reboots into the new core. The chassis does not block, does not warn beyond the help text. Matches legacy `/ui/*`. Operator should expect this — they just clicked `▶ Launch core`. |
| Operator tabs through SSH password field without typing | Client capture-phase blur listener stops propagation. No POST. Server is never told the operator looked at the field. |
| Empty `mister_ssh_password` POST reaches the server (e.g., via curl) | Overlay no-ops; `BridgeSaver.Save` sees zero diff; `diffBridgeConfig` returns no keys; the initial `scope = ScopeHotSwap` survives; handler returns `200 {ok:true, scope:"hot"}`. UX side effect is none: the password input always renders `value=""` so `.has-value` is unchanged, and HOT scope produces no toast per 4A. The no-op save is invisibly successful — acceptable. |
| Operator types new SSH password, then blurs | POST fires with the new value; `BridgeSaver.Save` writes it; `applyHotSwapSideEffects` does nothing (no SSH-credential side effect needed — the launcher snapshots on each call). Next launch-core POST uses the new password. |
| Operator submits HLS field with value out of bounds | Per-field decoder rejects; response is `400 {ok:false, errors:{<field>:"must be in [...]"}}`; saver not called; `.field-row.has-err` paints inline. |
| Operator submits HLS field violating cross-field rule (e.g., live_edge_segments=2 when start_segments=3) | Per-field decoder accepts (both are in their individual bounds); `BridgeSaver.Save` calls `Sectioned.Validate`, which fails; saver returns 400 `settingsError{chip:"BAD INPUT", cause: <validator error>}`; toast in the drawer-local notice slot. 4B keeps the toast text at the bare chip ("BAD INPUT") and does not surface the cause text, matching 4A's chip convention. A future polish pass could include a one-line excerpt of `errors.Unwrap(err).Error()` in the toast so the operator sees `"live_edge_segments must be ≥ start_segments"` — explicitly out of scope here to keep 4B aligned with 4A's chip vocabulary. |
| Empty HLS field on blur (e.g., operator cleared the value) | Numeric decoder rejects empty → per-field error `"must be a whole number"`. No HLS field accepts empty. (Contrast: HOT external-tool path fields in 4A accept empty as "use default.") |
| Saving any HLS field while a cast is active | RECAST scope → `BridgeSaver` calls `core.DropActiveCast`. Cast stops. Disk holds new value. Next play uses new config. |
| `humanizeBytes` hint stale after save | Mockup shows `268435456 → "256 MB"`. If operator changes to 134217728, the input updates immediately, but the adjacent `"256 MB"` hint stays until next page-load. Acceptable. |
| SSH password help mentions plaintext storage | Help text reads "Stored plaintext in config.toml (LAN-only trust model)." Same disclosure the legacy UI shows. Reinforces operator awareness without imposing key-management machinery. |
| Toggle `logging_debug` while cast active | HOT scope → `logging.SetLevel()` flips in process. Slog records reflect new level on the next emitted record. No restart. |
| Operator changes `video_interlace_field_order` while cast active | HOT scope → `core.SetInterlaceFieldOrder()` mutates the live plane's field order via the dual-write path documented in CLAUDE.md (in-memory config + live plane). Next vsync writes the new order. |
| Operator changes `video_modeline` while cast active | RECAST scope, but `BridgeSaver.saveLocked` has special handling for `video.modeline`: it notifies `VideoConfigSubscriber` adapters before dropping the cast. 4B does not touch this path. |
| Two tabs editing simultaneously | `BridgeSaver` uses `SaveTouched` semantics in 4A — read-modify-write under `r.mu`. Concurrent saves on different fields don't clobber each other. Last writer wins per field. Stale tab is stale until refresh. |
| Operator-disabled JS | Drawer opens closed at page load and cannot be opened — the gear toggle, tab switch, autosave, and action-button click handlers are JS-only. Same posture as 4A. The bridge save and launch-core routes still respond to direct same-origin POSTs via curl (LAN tooling). |
| Help-text characters needing escape | `html/template` auto-escapes every `Help` and `Label` value 4B passes to the field helper. None of 4B's `Help` strings contain HTML markup — the mockup's `<code>` styling appears only in the launch-core action row's directly-templated help text (not via the `field` helper), where the markup renders normally. 4F (URL adapter) may need a `HelpHTML template.HTML` alternative on `FieldArgs` for richer help text; that decision belongs to 4F's brainstorm. |

## Testing

### Per-decoder tests — `internal/chassis/settings_test.go`

For every new decoder, one test per success/failure branch. Approximate count: 35.

- Three video selects: each accepts every listed option, rejects an unknown value with its phrased error.
- Two bool switches: accept `"true"`, `"false"`, reject `"yes"`, reject empty.
- Two audio enum-ints: accept listed values, reject unlisted (e.g., `96000`), reject non-numeric.
- `mister_ssh_user`: accept `"root"`, reject empty (`"is required"`), reject `"root:bar"` (`"contains an illegal character"`).
- `mister_ssh_password`: accept any non-empty string verbatim; empty is allowed at decoder level (overlay handles preserve-on-empty).
- All 11 HLS numerics: accept low boundary, accept high boundary, reject low-1, reject high+1, reject non-numeric, reject empty.
- `logging_debug`: accept both bool values.

### Decoder/validator cross-check

`TestHLSDecoders_BoundsSubsetOfValidator` — enumerates each HLS decoder's accepted boundary values (low, high, mid) and asserts that a `Sectioned` config containing each value passes `validateHLSBufferConfig` (assuming the other HLS fields hold their defaults). Catches drift if either side's bounds change.

### Handler tests — launch-core

- Success path: mock `CoreLauncher` returns nil; `BridgeSaver.Current()` returns host `"192.168.1.42"`; expect `200 {ok:true, host:"192.168.1.42"}`.
- Empty-host path: `BridgeSaver.Current()` returns host `""`; expect `400 {ok:false, error:"MiSTer host not configured (set bridge.mister.host)"}`; mock launcher NOT called. A companion `TestLaunchCore_EmptyHostMessageMatchesLauncher` asserts the chassis empty-host message equals what `bridgeMisterLauncher.Launch` returns for the same scenario, so the two strings can't drift.
- Launcher error path: mock `CoreLauncher` returns `errors.New("ssh: handshake failed")`; expect `500 {ok:false, error:"ssh: handshake failed"}` — `sanitizeProbeError` is a no-op for messages with no IPv4:port tokens. A second test with a synthetic leaky error (`"dial tcp 192.168.1.42:22: connection refused"`) expects the redacted form `"dial tcp <host>: connection refused"`.
- Nil-launcher path: `s.cfg.CoreLauncher == nil`; expect `503 {ok:false, chip:"NOT READY"}`.
- Nil-saver path (defensive): `s.cfg.BridgeSaver == nil`; expect `503 {ok:false, chip:"NOT READY"}`.
- Wrong-origin path: bad `Sec-Fetch-Site`; expect `403` (middleware).

### Handler tests — extended bridge POST coverage

For each new form key, one success test confirming `BridgeSettingsSaver.Save` is called with the expected overlaid `config.BridgeConfig`. For each invalid-value test, confirm saver NOT called and `errors` map contains the named key.

For `mister_ssh_password`:
- Submit empty → overlay no-ops; `BridgeConfig` passed to `Save()` matches the current snapshot byte-for-byte; `diffBridgeConfig` returns no keys; saver returns `(ScopeHotSwap, nil)` and the handler responds `200 {ok:true, scope:"hot"}`.
- Submit non-empty → overlay sets the new password; `diffBridgeConfig` includes `"mister.ssh_password"`; saver returns `(ScopeHotSwap, nil)`; handler responds `200 {ok:true, scope:"hot"}`.

### Template render tests — `internal/chassis/chassis_test.go`

- Pipeline pane renders three sections, all 9 fields, the launch-core action button row, and an empty `#launch-core-result` slot.
- Advanced pane renders two sections, all 12 fields, three humanized byte hints (`"256 MB"`, `"1 MB"`, `"50 MB"`) for default-config values.
- SSH password input renders with `value=""` and the correct placeholder (`"••••••••"` when stored, `"not set"` when empty); `data-skip-empty="true"` attribute present.
- Scope badges render with the right CSS class per field. Pipeline expectations: HOT on `video_interlace_field_order`, `mister_ssh_user`, `mister_ssh_password`; RECAST on the other six Pipeline fields. Advanced expectations: HOT on `logging_debug`; RECAST on the 11 HLS fields. Aggregate over both panes: 4 HOT + 17 RECAST = 21 field rows. The test asserts each form-name input row carries the expected `.scope.hot` or `.scope.recast` class on its badge span.
- `humanizeBytes` helper test: 0 → `"0 B"`, 1024 → `"1 KB"`, 1048576 → `"1 MB"`, 1073741824 → `"1 GB"`, 268435456 → `"256 MB"`, 52428800 → `"50 MB"`.

### Integration tests — `tests/integration/chassis_test.go`

- Start chassis with real `BridgeSaver` against a tmp `config.toml`. `GET /receiver` includes both new panes' content (sentinel comments + a few known field labels).
- `POST /receiver/settings/bridge` with `video_interlace_field_order=tff` → 200, `scope:"hot"`, disk + in-memory updated, `core.SetInterlaceFieldOrder("tff")` was called on the fake core.
- `POST /receiver/settings/bridge` with `audio_sample_rate=44100` → 200, `scope:"recast"`, disk updated, `core.DropActiveCast` was called.
- `POST /receiver/settings/bridge` with `mister_ssh_password=newpass` → 200, `scope:"hot"`, disk shows `"newpass"`. Then `POST /receiver/settings/bridge` with `mister_ssh_password=` (empty) → 200, `scope:"hot"` (initial scope when `diffBridgeConfig` returns no changes); disk still shows `"newpass"`.
- `POST /receiver/settings/bridge` with `hls_live_edge_segments=15` → 400, `errors:{"hls_live_edge_segments":"must be in [1, 12]"}`, no disk write.
- `POST /receiver/settings/bridge` with `logging_debug=true` → 200, `scope:"hot"`; verify `logging.GetLevel()` is now debug.
- `POST /receiver/settings/action/launch-core` with a fake `CoreLauncher` that records the call and returns nil → 200, `host` populated; fake recorded one call.
- `POST /receiver/settings/action/launch-core` against a real `BridgeSaver` holding `MiSTer.Host=""` → 400, `error:"MiSTer host not configured (set bridge.mister.host)"`, fake launcher never called.

### JS behavior — manual verification checklist

Same pattern as 4A's manual checklist (no JS test runner today). The implementer walks this before declaring 4B done:

- Pipeline tab click switches the pane without a network request.
- Toggling `video_lz4_enabled` POSTs `video_lz4_enabled=false` (or `true`), receives `scope:"recast"`, switch UI updates from optimistic toggle.
- Changing `video_modeline` to a different option POSTs `video_modeline=PAL_576i` (or whatever) and receives `scope:"recast"` with no field error.
- Editing `mister_ssh_password` to a new value POSTs and receives `scope:"hot"`; subsequent click on `▶ Launch core` shows `▸ Core sent · <host>` against a configured MiSTer (verified manually against a real or fake).
- Tabbing through the SSH password input with no typing → no POST appears in DevTools Network panel.
- Editing `hls_live_edge_segments` to 99 → response 400, `.field-err` div paints below the input with the bound message; clearing the value to a valid number on next blur clears the error and adds `.has-value` to the input.
- Toggling `logging_debug` → bridge process emits debug-level slog records visible in the running container's stdout immediately on next request.
- `▶ Launch core` with the bridge's `CoreLauncher` not wired (devmode flag — implementation plan picks the test seam) → drawer-local notice slot toasts `NOT READY`; `.action-result` stays empty.
- Network exception (kill the bridge mid-click) → `.action-result` paints `▸ ERROR · network error`.

## Forward Compatibility

- **Phase 4C** (Catalog) reuses the same field helper, route shape, error envelope, scope dispatch, and toast pattern. Adds: `POST /receiver/settings/action/restore-defaults` (single action), provider-row sub-template (icon + meta + count + switch). Per-provider HLS-buffer overrides may activate the `.scope.next` badge if Catalog decides per-provider HLS settings are NextCast scope. The decision belongs to 4C's brainstorm; 4A's CSS already covers it.
- **Phase 4D** (Adapters, simple cases) extends the same `BridgeSettingsSaver` pattern with a chassis-owned `AdapterSettingsSaver` interface satisfied by `*uiserver.AdapterSaver`. Same JSON envelope; per-adapter scope dispatch.
- **Phase 4E** (Plex / Jellyfin link cascades) adds per-adapter "link state" sub-templates and a small set of action routes (`/receiver/settings/adapter/plex/link`, `…/unlink`, `…/poll`). The launch-core pattern in 4B is the closest precedent for these multi-step action flows (single-flight button, render result in slot, chip on wiring failures).
- **Phase 4F** (URL adapter custom widgets) adds yt-dlp host tag-list + cookies textarea. Bespoke widgets with their own minimal POST shapes; the JSON envelope still applies.
- **Phase 5 polish candidates from 4B-touched surfaces:** live byte-unit recomputation, probe-after-launch chain ("Core sent · 4.2ms ACK"), Diagnostics section (activity ring + build info read-only display), "Reset to defaults" destructive action with confirm modal.
- **Final chassis cutover** retires `/ui/*` once 4F lands. The chassis settings drawer becomes the only settings surface. The launcher and saver instances continue to exist (they're service-layer regardless of UI); only the legacy `internal/ui/*` templates and routes are removed.

## Appendix A — Pipeline & Advanced Field Inventory

Total: 21 fields + 1 action.

**Pipeline (10 rows):**

| Row | Form name | Scope | Field type |
|---|---|---|---|
| 1 | `video_modeline` | RECAST | select |
| 2 | `video_interlace_field_order` | **HOT** | select |
| 3 | `video_aspect_mode` | RECAST | select |
| 4 | `video_lz4_enabled` | RECAST | switch |
| 5 | `video_delta_lz4_enabled` | RECAST | switch |
| 6 | `audio_sample_rate` | RECAST | select |
| 7 | `audio_channels` | RECAST | select |
| 8 | `mister_ssh_user` | **HOT** | text |
| 9 | `mister_ssh_password` | **HOT** | password |
| 10 | `launch-core` action | — | action button |

**Advanced (12 rows):**

| Row | Form name | Scope | Field type | Unit hint |
|---|---|---|---|---|
| 1 | `hls_enabled` | RECAST | switch | — |
| 2 | `hls_live_edge_segments` | RECAST | number | — |
| 3 | `hls_start_segments` | RECAST | number | — |
| 4 | `hls_max_cached_segments` | RECAST | number | — |
| 5 | `hls_max_cache_bytes` | RECAST | number | `humanizeBytes` |
| 6 | `hls_max_playlist_bytes` | RECAST | number | `humanizeBytes` |
| 7 | `hls_max_segment_bytes` | RECAST | number | `humanizeBytes` |
| 8 | `hls_segment_timeout_seconds` | RECAST | number | — |
| 9 | `hls_playlist_timeout_seconds` | RECAST | number | — |
| 10 | `hls_max_variant_height` | RECAST | number | `px` (static) |
| 11 | `hls_stale_cache_reap_hours` | RECAST | number | — |
| 12 | `logging_debug` | **HOT** | switch | — |

Scope totals: 4 HOT (interlace, ssh_user, ssh_password, logging_debug), 17 RECAST (everything else), 0 NEXT, 0 REBOOT. Confirms 4B is a coverage exercise of HOT + RECAST against a single-field-per-save model.
