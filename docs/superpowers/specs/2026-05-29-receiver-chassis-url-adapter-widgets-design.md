# Receiver Chassis Settings Drawer — Phase 4F Design

**URL adapter custom widgets: yt-dlp host tag-list + cookies textarea.**

Spec date: 2026-05-29. Branch from `main` (picks up 4D, merge `dbddef6`). 4F is
independent of the in-flight 4E branch; where this doc cites "the 4E pattern"
it means the per-adapter action-route shape, which 4F re-derives from the same
4A primitives rather than depending on 4E code.

---

## 1. Context

Phase 4 (the chassis settings drawer, per the foundation spec
[`2026-05-21-receiver-chassis-foundation-design.md`](2026-05-21-receiver-chassis-foundation-design.md))
is decomposed into 4A–4F. 4A–4D have landed on `main`; 4E (Plex/Jellyfin link
cascades) is in flight. **4F is the last pane.** Once it lands, the final
`/ui/*` cutover becomes possible (that cutover is a *separate* step — see §9).

4D ([`2026-05-28-receiver-chassis-adapters-simple-pane-design.md`](2026-05-28-receiver-chassis-adapters-simple-pane-design.md))
built the Adapters pane for the three "simple" adapters (DLNA, Torrent,
Streams) and rendered Plex, Jellyfin, and **URL** as stubs. 4D deliberately
left the URL pane a stub because its mockup ([v24:4363-4412](../reference/2026-05-21-receiver-v24.html#L4363-L4412))
contains two bespoke widgets that don't reduce to the six standard field
types: an **editable yt-dlp host tag-list** and a **cookies textarea** with
Save/Clear and a status pill. Per the 4A non-goals
([line 48](2026-05-27-receiver-chassis-settings-drawer-network-pane-design.md)),
both "need their own data model and POST shapes. Phase 4F."

This is the first pane in the whole series where 4A's "4B–4F add zero new
field-type primitives" invariant bends — but it bends in *bespoke per-adapter
code and dedicated action routes*, **not** in the shared field renderer or the
shared `uiserver` saver overlay.

### The URL pane has six rows

From the v24 mockup ([v24:4363-4412](../reference/2026-05-21-receiver-v24.html#L4363-L4412)):

| Row | Widget | Backing | Mechanism | 4F work |
|---|---|---|---|---|
| 1 | Enabled (switch) | `enabled` (bool) | standard field | none — already in `Fields()` |
| 2 | yt-dlp resolver (switch) | `ytdlp_enabled` (bool) | standard field | **add FieldDef** |
| 3 | yt-dlp hosts (tag-list) | `ytdlp_hosts` ([]string) | bespoke widget → action route | **new** |
| 4 | yt-dlp format (text) | `ytdlp_format` (string) | standard field | **add FieldDef** |
| 5 | Resolve timeout (number) | `ytdlp_resolve_timeout_seconds` (int) | standard field | **add FieldDef** |
| 6 | Cookies (textarea + Save/Clear + pill) | `url_cookies.txt` file | bespoke widget → action routes | **new (port legacy)** |

All six rows are **HOT** scope, matching the mockup.

The decisive constraint: the URL adapter's `Fields()`
([`internal/adapters/url/adapter.go:190`](../../internal/adapters/url/adapter.go))
currently returns **only `enabled`**. Rows 2/4/5 are standard fields whose
schema entries don't exist yet; row 3's data lives in `ytdlp_hosts` (a TOML
array, not a scalar the saver overlay handles); and row 6 is a file on disk
(`filepath.Join(Bridge.DataDir, "url_cookies.txt")`), never a TOML value.

---

## 2. Goals

1. Replace the 4D URL stub with a real `settings-adapter-url.html` pane
   rendering all six rows, visually matching the v24 mockup.
2. Extend the URL adapter's `Fields()` so rows 2/4/5 render and auto-save
   through the **existing 4D `SaveTouched` field path** with no new chassis
   machinery for those rows.
3. Make the yt-dlp host tag-list **editable** (add/remove individual hosts) —
   genuinely new behavior; the legacy `/ui/*` host display was read-only.
4. Port the cookies Save/Clear/status semantics from the legacy URL adapter
   handlers onto chassis action routes with the JSON envelope and a
   paint-time status pill.
5. Preserve every Phase 4 invariant: chassis imports no concrete adapter and
   no `uiserver`; `import_check_test.go` stays green with zero edits; the
   shared field renderer and the shared `uiserver` saver overlay gain no new
   field-type primitive.

---

## 3. Non-Goals

- **The final `/ui/*` cutover.** 4F makes it *possible* but does not perform
  it. Retiring `internal/ui/*` templates and routes is a separate follow-up
  (§9).
- **New shared field-type primitive.** The host tag-list and cookies widgets
  are bespoke per-adapter widgets with their own POST shapes. We do **not**
  add a `KindList` (or similar) to `adapters.FieldKind` / the shared
  `field` template helper / `overlayTouched`.
- **Changes to other adapters** (DLNA, Torrent, Streams, Plex, Jellyfin) or to
  4A/4B/4C/4D panes beyond the additive swap of the URL stub.
- **Host import/export, per-host metadata, or reordering.** The tag-list is a
  flat add/remove set editor, matching the mockup.

---

## 4. Key decisions

| Decision | Choice | Rejected alternatives |
|---|---|---|
| URL pane field scope | **Extend `Fields()`** with the three yt-dlp FieldDefs so rows 2/4/5 use the standard 4D save path. | *Minimal (widgets only):* ships an incomplete pane that doesn't match the mockup; the three fields would have no home. |
| Host edit wire model | **Whole-list action route** (`POST …/url/hosts`, full set as JSON), validated and persisted atomically. | *Per-host add/remove routes:* two routes + per-host concurrency reasoning for no UX gain. *New `KindList` in the shared saver:* widest blast radius; bends the "no new primitives" invariant inside shared code consumed by every adapter. |
| Host persistence | A **new `*uiserver.AdapterSaver` method** that runs the same read→validate→write→`ApplyConfig` pipeline as `SaveTouched`, but writes a typed value (the `[]string` array) directly into the merged section map, bypassing the scalar-only `overlayTouched`. The cmd wrapper calls it. | *cmd wrapper does its own read-modify-write:* duplicates the atomic-write + shared-mutex + section-preservation logic that already lives in the saver; risks drifting from `SaveTouched`'s subtable-preservation guarantees. |
| Cookies wire model | **Dedicated action routes** (`POST …/url/cookies` + `POST …/url/cookies/clear`) over a chassis-owned `AdapterCookieStore` interface; paint-time status via the same interface. | *Routing cookies through TOML save:* cookies are a file, not config — impossible through the saver. *DELETE for clear:* would bypass `requireSameOrigin` (POST-only) — §5.2. |
| Isolation | Two **new chassis-owned interfaces** (`AdapterHostEditor`, `AdapterCookieStore`) satisfied by `cmd/` wrappers, exactly like 4D's `AdapterSettingsSaver`. | *chassis imports `internal/adapters/url`:* breaks the §5 invariant and `import_check_test.go`. |

---

## 5. Architecture

Four units, each independently testable.

### 5.1 URL adapter package — `internal/adapters/url/`

Extend `Fields()` from 1 → 4 entries (additive; ordering matches the mockup):

| Key | Kind | Default | Scope | Help |
|---|---|---|---|---|
| `enabled` | Bool | false | HotSwap | (existing) |
| `ytdlp_enabled` | Bool | true | HotSwap | "Master switch. When on, `mode=auto` routes URLs whose host matches the list below through yt-dlp." |
| `ytdlp_format` | Text | `bv*[height<=720]+ba/bv*+ba/b` | HotSwap | "Format selector. Default prefers ≤720p video with broad codec compatibility." |
| `ytdlp_resolve_timeout_seconds` | Int | 30 | HotSwap | "Per-URL timeout for yt-dlp resolution (5–120s)." |

`Validate()` and `ApplyConfig()` already cover these keys
([`config.go:68-117`](../../internal/adapters/url/config.go),
[`adapter.go:339-366`](../../internal/adapters/url/adapter.go)), so the autosave
path works as-is. `ytdlp_hosts` and cookies are **not** added to `Fields()` —
they are driven by the bespoke widgets below.

`CurrentValues()` must also expand from `enabled`-only to include the three
new standard fields (`ytdlp_enabled`, `ytdlp_format`,
`ytdlp_resolve_timeout_seconds`) so chassis paint reflects both defaults and
saved values. Without this, the 4D `AdapterSettingsSaver.Current()` path would
render the new rows blank/off even though `Validate()` and `ApplyConfig()` can
save them.

The package also gains a tiny exported surface for the cmd wrappers (the
wrappers may import `internal/adapters/url`):
- A way for the wrapper to read the current hosts and to **purely validate +
  normalize** a proposed set. Reuse the existing host validation in
  `Validate()` / `ytdlp` rules — no new validation logic — but do not live-apply
  from this surface. `SaveValues` (§5.5) is the only code path that persists and
  calls `ApplyConfig`, which avoids double-apply and apply-before-disk-write
  hazards. Naming is the plan's call; it must not collide with the chassis-side
  `AdapterHostEditor.SetHosts`, which is a *different* method.
- Exported cookie methods/functions for the wrapper to save, clear, stat, and
  validate cookies without reusing HTTP handlers. The existing low-level
  helpers (`saveCookies`/`clearCookies`/`statCookies`/`validateCookies`,
  [`cookies.go:42-125`](../../internal/adapters/url/cookies.go)) are currently
  unexported, so 4F either wraps them with exported adapter methods or promotes
  package functions with the same behavior. `CookiesPath()` remains available
  for path ownership. The legacy
  `handleCookiesSet`/`handleCookiesClear` HTTP handlers remain (they belong to
  the soon-to-be-retired `/ui/*` routes) and are **not** reused directly — 4F
  calls the lower-level functions so the chassis route owns its own
  envelope.

**Verification gate (plan):** confirm the legacy `/ui/*` URL panel
(`renderPanel` in [`internal/adapters/url/ui.go`](../../internal/adapters/url/ui.go),
a custom panel — *not* the generic field renderer) does not double-render the
three newly-exposed fields. Legacy is retired right after 4F, so even a
transient surfacing is low-risk, but the plan checks it.

### 5.2 Host tag-list — `AdapterHostEditor` (chassis-owned)

```go
// internal/chassis/settings.go
type AdapterHostEditor interface {
    // Hosts returns the current host list for paint, ok=false if the
    // adapter is unknown / not a host-editing adapter.
    Hosts(name string) (hosts []string, ok bool)
    // SetHosts validates and persists the whole list atomically and
    // returns the wire scope ("hot") plus the normalized list.
    SetHosts(name string, hosts []string) (scope string, normalized []string, err error)
}
```

- **Route:** `POST /receiver/settings/adapter/url/hosts`, behind
  `requireSameOrigin`, body `{"hosts":["youtube.com", …]}`. **POST, not PUT:**
  `requireSameOrigin` (`sameorigin.go:14`) only enforces the origin check for
  `r.Method == http.MethodPost` — PUT/DELETE bypass CSRF protection entirely.
  Every chassis mutation route is POST for this reason; 4F follows suit.
- **Handler** (`handleSettingsAdapterHostsPost` in `internal/chassis/settings.go`):
  nil-interface → `503` via `writeSettingsChip(w, 503, "NOT READY")` (the
  established chassis chip — see settings.go:1046); unknown adapter →
  `404`; validation failure → `400 {ok:false, errors:{hosts:"<msg>"}}`
  (the cmd wrapper maps adapter `FieldError{Key:"ytdlp_hosts"}` to the widget
  wire key `hosts`); success → `200 {ok:true, scope:"hot", hosts:[…]}`.
- **Production wrapper** `cmd/mister-groovy-relay/adapter_host_editor.go`:
  delegates persistence to the new `*uiserver.AdapterSaver` array-save method
  (§5.5), which is the **sole** validator + atomic-writer + applier (it runs
  the adapter's `Validate` and `ApplyConfig` exactly as `SaveTouched` does).
  The wrapper does **not** call `ApplyConfig` itself — avoiding the
  double-apply hazard (the URL adapter's `ApplyConfig` rebuilds the resolver,
  adapter.go:339-365). It maps the returned `adapters.ApplyScope` → the `"hot"`
  wire label (same conversion `AdapterSettingsSaver` performs) and returns the
  normalized list. An optional cheap pre-validate may run first purely to
  surface the `400` path before touching disk; it must not also apply.
- **Client (JS):** each ✕ / "+ add host" mutates a client-side `Set`, then
  `POST`s the *whole* list (immediate, like a switch — no explicit Save). A
  rejected host paints the error chip on the widget and rolls the set back.

### 5.3 Cookies — `AdapterCookieStore` (chassis-owned)

```go
// internal/chassis/settings.go
type CookieStatusView struct {
    Loaded bool   // false → "not loaded"
    Bytes  int64  // file size when loaded
    SetAt  string // display string, format "2006-01-02 15:04:05Z" (UTC); "" when absent
}
type AdapterCookieStore interface {
    CookieStatus(name string) (CookieStatusView, bool)
    SaveCookies(name, raw string) (CookieStatusView, error)
    ClearCookies(name string) (CookieStatusView, error)
}
```

- **Routes** (POST only — see §5.2 on `requireSameOrigin`; DELETE would bypass
  the origin check, so clear is a POST):
  - `POST /receiver/settings/adapter/url/cookies` — save. Accepts form
    (`cookies=`) or JSON (`{"cookies":"…"}`), mirroring the legacy body
    parsing ([`cookies.go:136-175`](../../internal/adapters/url/cookies.go),
    1 MiB cap preserved).
  - `POST /receiver/settings/adapter/url/cookies/clear` — clear.
- **Handlers:** nil-interface → `503` via `writeSettingsChip(w, 503, "NOT READY")`;
  invalid Netscape format → `400 {ok:false, errors:{cookies:"<msg>"}}`; success →
  `200 {ok:true, cookie:{loaded, bytes, set_at}}`; filesystem save/clear/stat
  failures → `500 {ok:false, chip:"WRITE FAILED"}` without echoing the path or
  raw OS error. The `SetAt` display string uses one fixed format
  (`2006-01-02 15:04:05Z`, UTC) for both the pill and the `set_at` JSON field —
  not the legacy split where the fragment used that format but the JSON used
  RFC3339 (cookies.go:231 vs 238).
- **Production wrapper** `cmd/mister-groovy-relay/adapter_cookie_store.go`
  over the URL adapter's exported cookie surface (wrapping the existing
  lower-level helpers) plus `CookiesPath()`. Netscape validation already lives
  in `validateCookies` ([`cookies.go:109-125`](../../internal/adapters/url/cookies.go))
  and must remain the single source of truth; the wrapper should not duplicate
  the format checks.
- **Status pill:** `not loaded` (dim) when `!Loaded`; `{bytes} B · set {SetAt}`
  when loaded. Painted at drawer load via `CookieStatus`; repainted from each
  action's response. Save/Clear are **explicit buttons** — the textarea is not
  autosaved.

### 5.4 Chassis isolation (unchanged invariant)

`AdapterHostEditor`, `AdapterCookieStore`, and `CookieStatusView` are defined
in `internal/chassis`. `chassis.Config`
([`server.go:115`](../../internal/chassis/server.go)) gains two nil-safe
fields. `internal/chassis` continues to import **neither**
`internal/adapters/url` **nor** `internal/uiserver`;
`import_check_test.go` needs **zero edits** and stays green. The cmd wrappers
(which may import both) bind the interfaces, exactly as 4D bound
`AdapterSettingsSaver` to `*uiserver.AdapterSaver`.

### 5.5 `uiserver` array-save helper

Add one method to `*uiserver.AdapterSaver` (alongside `SaveTouched`):

```go
// SaveValues writes explicitly-allowed typed values (including non-scalar
// values such as []string arrays) into the [adapters.<name>] section, reusing
// SaveTouched's read → merge → encode → validate → write-atomic →
// ApplyConfig pipeline under the shared saver mutex. Unlike SaveTouched it
// does not run the scalar overlayTouched step; callers pass already-typed Go
// values that the TOML encoder handles directly (encodeAdapterMap already
// serializes []string and nested tables).
func (r *AdapterSaver) SaveValues(name string, values map[string]any,
    allowedKeys []string, adapter adapters.Adapter) (adapters.ApplyScope, error)
```

This keeps the atomic-write, shared-mutex serialization, and
descendant-subtable preservation guarantees that already exist in
`SaveTouched` (4D Tasks 5–8), without duplicating them in `cmd/`. The host
editor wrapper calls `SaveValues("url", map[string]any{"ytdlp_hosts": hosts},
[]string{"ytdlp_hosts"}, …)`. `allowedKeys` is intentionally separate from
`adapter.Fields()`: `ytdlp_hosts` is not a standard field, so a FieldDef
allowlist would reject the one key this helper exists to persist.
`encodeAdapterMap` already round-trips arrays and nested tables (4D Task 3), so
no encoder change is needed.

**Ordering is load-bearing:** `SaveValues` must encode → decode → run
`adapter.Validate` **strictly before** `config.WriteAtomic`, exactly as
`SaveTouched` does (adapter_saver.go:419-426 validate, :433 apply). Skipping the
overlay step does not skip validation — an invalid `ytdlp_hosts` entry must be
rejected before anything hits disk. `SaveValues` returns `adapters.ApplyScope`
(the uiserver layer's type); the cmd wrapper converts it to the `"hot"` wire
label.

**Normalization is also load-bearing:** host validation lowercases entries on
the decoded `url.Config`. `SaveValues` must persist the normalized values, not
the pre-validation input. The simplest safe contract is: validate/decode into
the adapter config, recover the normalized section map from that decoded config,
re-encode that normalized map, then write atomically and call `ApplyConfig` with
the same normalized primitive. Returning `hosts:[…]` from the chassis route uses
this normalized list.

### 5.6 SettingsData / paint + templates + assets

- `AdapterPaneData` ([`data.go:300`](../../internal/chassis/data.go)) gains
  explicit widget-presence flags plus nil/zero-safe data:
  `HasHostEditor bool`, `Hosts []string`, `HasCookieStore bool`, and
  `Cookie *CookieStatusView`. Other adapters leave the flags false; the URL
  template renders the bespoke widgets from the flags, not from `len(Hosts)`,
  so a valid empty host list still displays the tag editor.
- `buildSettingsData` reads `AdapterHostEditor.Hosts("url")` and
  `AdapterCookieStore.CookieStatus("url")` (both nil-guarded) to populate the
  URL pane. Signature extends with the two interfaces (or they ride on a
  struct already threaded through — plan decides), consistent with how 4C
  threaded `catalogManager`.
- New `internal/chassis/templates/settings-adapter-url.html`
  (`{{ define "settings-adapter-url" }}`): rows 1/2/4/5 via the existing
  `field` helper; row 3 the tag-list partial; row 6 the cookies partial. The
  4D container template (`settings-adapters.html`) swaps its URL stub call for
  `{{ template "settings-adapter-url" . }}`.
- **CSS** (port from v24 into `chassis.css`): `.tag-list`, `.tag`, `.tag .x`,
  `.tag.add`, `.cookies-form`, `.cookies-actions`, `.status-pill`,
  `.status-pill.dim`.
- **JS** (`settings-drawer.js`, additive): `[data-host-editor]` handler
  (add/remove → POST whole list, optimistic with rollback on error) and
  `[data-cookies]` handler (Save → `POST …/cookies`, Clear → `POST …/cookies/clear`,
  repaint pill). `showNotice` ([`settings-drawer.js:114`](../../internal/chassis/static/settings-drawer.js))
  is reusable as-is (it targets `#settings-notice`). **The field-error helpers
  are NOT drop-in:** `paintFieldError(name,…)` / `clearFieldError` (lines 89 /
  80) resolve `[name="<name>"]` and walk up to a `.field-row` via `findRow`
  (line 74) — the bespoke widgets are deliberately not `[data-field]` inputs
  and have no such named row, so a naive reuse silently no-ops. Each widget
  therefore renders its **own** inline error element, and the JS writes errors
  to it directly (or via a small widget-scoped painter). The 4A `[data-field]`
  selector narrowing (4D Task 29) already prevents the standard-field blur
  handler from firing on these widgets.

---

## 6. Wire contract

All routes are **POST** (so `requireSameOrigin` guards them — §5.2) and return
the `503` chip as `"NOT READY"` (the established chassis chip — §5.2).

| Method + path | Body | Success | Errors |
|---|---|---|---|
| `POST /receiver/settings/adapter/url/hosts` | `{"hosts":[…]}` | `200 {ok:true, scope:"hot", hosts:[…]}` | `400 {ok:false, errors:{hosts:"…"}}`; `404` unknown adapter; `503 {ok:false, chip:"NOT READY"}`; `403` cross-origin |
| `POST /receiver/settings/adapter/url/cookies` | form `cookies=` or `{"cookies":"…"}` | `200 {ok:true, cookie:{loaded,bytes,set_at}}` | `400 {ok:false, errors:{cookies:"…"}}` (invalid format or oversize, >1 MiB); `500 {ok:false, chip:"WRITE FAILED"}` on storage failure; `503 {ok:false, chip:"NOT READY"}`; `403` |
| `POST /receiver/settings/adapter/url/cookies/clear` | — | `200 {ok:true, cookie:{loaded:false,bytes:0,set_at:""}}` | `500 {ok:false, chip:"WRITE FAILED"}` on storage failure; `503 {ok:false, chip:"NOT READY"}`; `403` |
| `POST /receiver/settings/adapter/url` (rows 1/2/4/5) | `touched` keys | `200 {ok:true, scope:"hot"}` (existing 4D envelope) | existing 4D field/chip errors |

Envelope shapes are the 4A/4D JSON contract verbatim; no new envelope.

---

## 7. Testing

- **`internal/adapters/url`:** `Fields()` now returns the four entries with
  correct kinds/defaults/scopes; `CurrentValues()` returns the three new
  standard fields; exported host validation/normalization lowercases without
  applying; exported cookie wrappers preserve the existing validation,
  save/clear/stat behavior.
- **`internal/uiserver`:** `SaveValues` round-trips `ytdlp_hosts` (array
  survives encode/decode), preserves other URL keys and any descendant
  subtables, leaves disk untouched on validation failure, persists normalized
  lowercase hosts, rejects keys outside its explicit allowlist, serializes under
  the shared mutex (`-race`).
- **`internal/chassis`:** handler tests for the new routes — success,
  field-error, `404`, nil-interface `503 "NOT READY"`, cross-origin `403`
  (now meaningful because the routes are POST), cookie storage failure `500`
  without path leakage; template render tests for the URL pane (tag rows incl.
  empty list; cookies pill in loaded vs not-loaded states); a test asserting
  `ytdlp_hosts`/cookies never render as plain `[data-field]` inputs (mirror of
  4D's Catalog-key projection guard).
- **`cmd/mister-groovy-relay`:** integration-tagged end-to-end — host
  add/remove persists to `config.toml` and re-reads correctly; cookies
  Save/Clear round-trips through the real adapter to `url_cookies.txt` with
  correct status; both apply HOT live.
- CI's four gates (`go vet`, `go test`, `go test -race`, integration) stay
  green.

---

## 8. Phase progression

4F is the terminal pane. After it merges, the only remaining Phase 4 work is
the cutover.

## 9. Final chassis cutover (out of scope here; next after 4F)

Retire `internal/ui/*` templates and routes; the chassis settings drawer
becomes the sole settings surface. The `uiserver.{Bridge,Adapter}Saver`
instances persist (they are the saver layer regardless of UI). The URL
adapter's legacy `handleCookiesSet`/`handleCookiesClear` HTTP handlers and the
`renderPanel` UI code are removed at cutover, since 4F's chassis routes replace
them. This is a distinct spec/plan.
