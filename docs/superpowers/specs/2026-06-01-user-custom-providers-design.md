# User-Defined Custom Providers — Design

- **Date:** 2026-06-01
- **Status:** Approved design (pre-plan)
- **Topic:** Let operators add their own catalog providers (YouTube playlists,
  Twitch/live URLs, direct m3u8 streams) that behave identically to the bundled
  Streams providers and are fully customizable (name, glyph, color).

## 1. Summary & motivation

Today the receiver catalog drawer exposes exactly three **bundled** Streams
providers (MTV Rewind, Cartoon Rewind, Toonami Aftermath), discovered from a
single remote `manifest_url`. Operators cannot add their own sources. This
design adds **user-authored providers**: an operator builds a full
`provider → groups → channels` hierarchy from a guided form in the catalog
drawer, and the result is indistinguishable from a built-in — it appears as a
catalog tab, its channels are castable and starrable into the 12-slot preset
bank, and it participates in the existing queue/shuffle/auto-advance/resolve
machinery.

The guiding constraint, per the brainstorm: **a user-added source must function
exactly like a built-in preset.** The cheapest way to guarantee that is to make
user providers *the same data type* the built-ins already are, merged into the
same maps — so "behaves like a built-in" is free rather than re-implemented.

## 2. Decisions captured (from brainstorm)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Unit model | **Full custom providers** — provider + groups + channels, mirroring the three built-ins. |
| 2 | Management surface | **Receiver page catalog drawer** (not the htmx Settings UI). |
| 3 | Authoring method | **Guided form** (no JSON import/export in v1). |
| 4 | Channel source-type | **Auto-detect from URL, with manual override.** |
| 5 | URL trust posture | **Allow LAN, block internals** (loopback / link-local / cloud-metadata / `file://` always blocked; public + RFC1918/LAN allowed). |
| 6 | Icon model | **Text glyph + color only** (2–4 chars + curated color palette; no emoji, no logo images). |
| 7 | Architecture | **Extend the Streams adapter** (local manifest merged into existing definitions/catalogs). |
| 8 | v1 enhancements | Verify button, live-vs-VOD detection, glyph auto-suggest, drag-reorder, delete-with-cleanup (see §9). |

## 3. Goals / non-goals

**Goals**
- Author/edit/delete full custom providers from the catalog drawer.
- Three channel kinds: `playlist`, `single`, `direct` — covering YouTube
  playlists, Twitch/live/single videos, and direct m3u8/HLS.
- User channels reuse the existing catalog drawer, preset bank, queue, yt-dlp
  resolver, direct-stream playback, and HLS buffering with no behavior change.
- Per-provider customization: display name, 2–4 char glyph, color (curated
  palette). Optional groups. Per-channel name, URL, kind, play mode, group.
- "Allow LAN, block internals" SSRF posture at both authoring and play time.

**Non-goals (v1)**
- JSON import/export / shareable provider blobs (future).
- Uploaded or remote **logo images** (text glyph only).
- Per-channel authentication/cookies UI (the global Streams cookies file is
  reused as-is; no per-channel auth).
- Browsing Plex/Jellyfin/DLNA libraries in the drawer (separate effort).
- A generic multi-adapter catalog-aggregation layer (Approach 3, deferred).

## 4. Data model & persistence

### 4.1 Local manifest file

A new file `{data_dir}/user_providers.json`, structured identically to the
remote manifest:

```json
{ "version": 1, "providers": [ { /* ProviderDefinition */ } ] }
```

At startup and after every edit, the Streams adapter loads this file and
**merges** its providers into the same `definitions`/`catalogs` maps that hold
the bundled/remote providers (see `internal/adapters/streams/adapter.go`,
`refresh.go`). User channels therefore become first-class catalog entries with
no special-casing downstream.

### 4.2 New store (`user_provider_store.go`)

Mirrors `internal/adapters/streams/preset_store.go`:
- In-memory slice guarded by a mutex.
- Atomic writes: temp file + `os.Rename`.
- Load-time validation: malformed/invalid providers are **dropped with a debug
  log**, never fatal (self-healing).
- Enforces the ID prefix and limits (§4.5, §8).

### 4.3 `ProviderDefinition` extensions

Reuse the existing `ProviderDefinition`/`ChannelDefinition` (`provider.go`
lines 23-65) — they already carry `Type`, inline `Groups`, and inline
`Channels`. This adds one new `Type` value plus two persisted fields (none exist
today; confirmed against `provider.go`):

| Type | New field | Purpose |
|------|-----------|---------|
| `ProviderDefinition` | `Type: "user"` (value, not field) | Selects the inline catalog builder (§5). Unlike `youtube-channel-json` (remote JSON) / `direct-streams` (bundled, host-locked), a `user` provider's `Groups`/`Channels` are authored inline. |
| `ProviderDefinition` | `BadgeColor string` | Palette **token** (e.g. `"amber"`, `"teal"`), not raw hex (§8). |
| `ChannelDefinition` | `Kind string` | `playlist` \| `single` \| `direct`, set by auto-detection (§4.7), overridable. Drives the catalog builder. |

`PlayMode` (existing) is meaningful only for `playlist` channels.

**Badge rendering (CSP-safe).** User data cannot supply a hardcoded CSS
`BadgeClass`, and we deliberately avoid arbitrary inline `style=` (CSP +
contrast risk). Instead the chassis maps `BadgeColor`'s token to one of ~8
**predefined palette classes** added to `chassis.css` (e.g. `.ic.u-amber`,
`.badge.u-amber` — each a CRT-tuned bg/fg pair). The template prefers
`BadgeClass` (built-ins) and falls back to the `u-<token>` class derived from
`BadgeColor`. Save-time validation rejects unknown/empty tokens; load-time
validation drops or normalizes malformed entries before merge; and the template
still has a default palette class as defense-in-depth, so malformed data can
never brick rendering.

### 4.4 Channel kinds

| Kind | Example URL | Items produced | Resolution path |
|------|-------------|----------------|-----------------|
| `playlist` | `youtube.com/playlist?list=…` | N items (enumerated, §6) | yt-dlp resolve per item (existing) |
| `single` | `twitch.tv/foo`, single video | 1 non-direct item | yt-dlp resolve (existing) |
| `direct` | `…/stream.m3u8` | 1 `Direct:true` item | straight to FFmpeg + HLS buffer (existing) |

### 4.5 ID namespacing and stable identities

User provider IDs carry a reserved prefix `user:` (e.g. `user:f1-tv`, slugified
from the display name with a uniqueness suffix on collision). The bundled/remote
manifests are **forbidden from using the `user:` prefix** (validated on remote
manifest load; offending remote providers are dropped). This guarantees user
providers can never shadow — or be shadowed by — a built-in in the merged map,
and makes "is this provider user-deletable?" a pure prefix check.

`ProviderID` is auto-derived (slug of the display name) and **locked** — it is
not user-editable, so renaming "F1 TV" → "Formula 1" keeps `user:f1-tv` and does
not orphan presets/refs. Collisions append a numeric suffix: `user:f1-tv`,
`user:f1-tv-2`, `user:f1-tv-3`.

`ChannelDefinition.ID` follows the same stability rule **within its provider**:
auto-derived from the channel name on create, locked thereafter, and de-duped
with numeric suffixes (`f1-live`, `f1-live-2`, ...). Renaming "F1 Live" →
"Formula 1 Live" keeps the original channel ID, so starred preset references
remain valid. Existing channel rows carry their ID as hidden/read-only form data;
new rows omit the ID and the server assigns it.

Updates are treated as edits to existing IDs plus explicit additions/removals:
- Known channel ID present in the update → mutate that channel in place.
- Missing channel ID on a row → create a new channel with a server-assigned ID.
- Previously-known channel ID absent from the submitted provider → delete that
  channel and clear any preset slots that reference `(providerID, channelID)`.
- Unknown, malformed, or duplicate channel IDs in a submitted update are rejected
  rather than trusted from the browser.

### 4.6 Cache keys for `user:` IDs

The existing Streams cache key validator allows only `[a-z0-9-]`, so raw
`user:` IDs can never be passed to `catalogCacheKey(providerID)`. User provider
and playlist enumeration caches therefore use deterministic sanitized keys,
not raw IDs:
- Provider/catalog cache: `user-provider-<sha256(providerID)>`.
- Per-playlist item cache: `user-playlist-<sha256(providerID + "\x00" + channelID)>`.

The hash input uses the locked provider/channel IDs, so renames do not discard
cache entries, while the emitted key remains compatible with the existing cache
file validator.

### 4.7 Channel-kind auto-detection

When the operator pastes a channel URL, the form computes `Kind` with these
ordered rules (first match wins); the result is shown as a chip the operator can
override:

1. **`direct`** — URL path ends in a recognized HLS/DASH manifest suffix:
   `.m3u8`, `.m3u`, or `.mpd`.
2. **`playlist`** — a recognized playlist URL: YouTube `…/playlist?list=…` or a
   `watch?…&list=…` with no isolated video, or another host yt-dlp treats as a
   playlist. (Conservative: only YouTube `list=` is auto-detected in v1; other
   hosts default to `single` and the operator can override to `playlist`.)
3. **`single`** — the default for anything else (Twitch channel, single video,
   Vimeo, etc.).

Detection is **purely syntactic** (no network call), so it works offline and
before Verify (§9 item 4). If the operator overrides the chip, the chosen `Kind`
is stored verbatim and detection does not re-run on reopen.

## 5. Playback (reuse, near-zero new code)

Each kind maps onto existing machinery in
`internal/adapters/streams/playback.go`:

- **`direct`:** catalog builder emits one `StreamItem{URL, Direct:true}`
  (identical to Toonami direct streams). Flows through the `isDirectStreamItem`
  branch → strict `MediaInputPolicy` → FFmpeg, and auto-qualifies for HLS
  buffering (`shouldBufferDirectHLS` keys off the `.m3u8` suffix). Only the
  **validation** differs (§7) — user direct URLs use the new LAN-aware check,
  not the hardcoded per-provider validator.
- **`single`:** one `StreamItem{URL:pageURL, Direct:false}` → existing
  `resolver.Resolve(pageURL, format, cookies)` yt-dlp path. Live (Twitch) →
  HLS edge URL, `CanPause:false`; VOD → pausable (see §9 live-vs-VOD).
- **`playlist`:** items are enumerated at catalog-build time (§6); thereafter
  `buildQueue` provides shuffle/sequential/next/auto-advance unchanged.

Everything downstream of "channel has items" is untouched.

**Chassis catalog exposure still needs one intentional code change.** The
runtime maps make user providers first-class for queue/build/resolve, but the
receiver drawer currently iterates the fixed bundled-provider list. `Catalog()`
must switch to an ordered view of enabled bundled providers followed by user
providers (using the same `buildChassisCatalogProvider` conversion), while
`BundledCatalog()` remains the settings-only view of all bundled providers,
including disabled ones.

## 6. Playlist enumeration

At catalog-build/refresh time (within the existing `refresh.go` cycle), a
`playlist` channel is enumerated via `yt-dlp --flat-playlist` into a list of
video IDs/URLs, cached exactly like other provider catalogs
(`{data_dir}/streams/…`, governed by the existing `catalog_refresh_hours` TTL
and `MaxItemsPerChannel` cap). Those become the channel's `Items[]`.

- Enumeration runs **async** of the form save (§8): the channel saves
  immediately in an "Enumerating…" pending state and fills in "N videos" when
  the refresh completes.
- Failure (private/region-locked/deleted playlist) puts the **channel** into an
  error state with an error chip; the **provider stays usable** — same graceful
  degradation the bundled providers use.

**New enumerator surface.** Today the resolver runs yt-dlp with `--no-playlist`
(`url_resolver.go`), so a flat-playlist call is genuinely new. Add:

```go
// EnumeratePlaylist lists a playlist's item IDs without resolving media.
// Bounded by maxItems; runs under a CatalogRequestTimeoutSeconds context.
EnumeratePlaylist(ctx, pageURL, cookiesPath string, maxItems int) ([]StreamItem, error)
```

- Invokes `yt-dlp --flat-playlist --playlist-end <maxItems>` (maxItems =
  `MaxItemsPerChannel`); each entry → a non-direct `StreamItem` (video URL/ID).
- **Caching/TTL:** results cache under `{data_dir}/streams/…` keyed by the
  sanitized hash key from §4.6, refreshed on the existing
  `catalog_refresh_hours` cycle — same machinery as bundled catalogs.
- **Timeout:** each enumeration runs under `CatalogRequestTimeoutSeconds` (20s
  default). Enumerations run **sequentially within the existing refresh loop**
  (no new parallelism), so a provider with many playlists can't fan out and
  hammer yt-dlp.
- **Failure → persisted channel error.** A private/region-locked/deleted
  playlist sets a persisted per-channel error state (visible on form reopen,
  §9 item 3) and leaves the provider usable. A previously-cached item list is
  retained on transient refresh failure (serve-stale), matching bundled-catalog
  behavior.

## 7. Security & URL validation

Posture: **allow LAN, block internals.** Enforced at two checkpoints.

### 7.1 Authoring time (form save)

A **new** `validateUserProviderHost` helper does this — the existing
`normalizeConfigHost` (`config.go` lines 325-338) **rejects all IP literals**
("IP literals are not allowed"), which is the opposite of what "allow LAN"
needs. We reuse only its scheme/userinfo/port checks; the IP logic is new and
uses `net/netip` `IsLoopback`/`IsLinkLocalUnicast`/`IsPrivate`/etc.:
- **Reject always:** non-`http(s)` schemes incl. `file://`; loopback
  (`127.0.0.0/8`, `::1`); link-local + cloud-metadata (`169.254.0.0/16`,
  incl. `169.254.169.254`, and `fe80::/10`); unspecified/multicast/reserved;
  URL userinfo.
- **Allow:** public hosts **and** private/LAN ranges (`10/8`, `172.16/12`,
  `192.168/16`, `fc00::/7`) — so a local HDHomeRun / restreamer works.
- Hostnames (not IP literals) are allowed through this checkpoint and
  re-validated by IP after resolution (§7.2), since a name can resolve to a
  blocked address.
- Rejected URLs are surfaced inline by the form (validate-before-write) and
  never written to `user_providers.json`.

### 7.2 Play time (defense in depth)

Authoring-time checks are insufficient alone (DNS rebind, HTTP redirects), so
revalidate at dereference:
- **`direct` items** get a **new** `userDirectInputPolicy()` — separate from the
  existing `directHLSInputPolicy()` (`playback.go` lines 537-547), which
  whitelists `file` because it is bundled/host-locked. The user policy:
  protocol whitelist `http,https,tcp,tls,crypto` (**no `file`**); blocked
  `Cookie`/`Authorization`/`Proxy-Authorization`/`Referer` headers; reconnect
  disabled; `RWTimeout` set explicitly (same 5s default as direct HLS unless
  implementation evidence supports a different value).
- **Redirect handling.** Per the long comment in `policy.go` (lines 34-59),
  `DisableRedirects` emits **no FFmpeg flag** — the adapter is responsible for
  resolving each `Location` server-side (max 3 hops) and re-running the §7.1 IP
  check on every hop before the final URL reaches FFmpeg. The spec's "redirects
  revalidated" claim means **this adapter-side prevalidation**, not an FFmpeg
  flag.
- **`single`/`playlist` items** are resolved by yt-dlp; every resolved media
  URL is re-checked against the §7.1 rules before handing to FFmpeg, including
  both `URL` and `AudioURL` when yt-dlp returns separate DASH video/audio
  streams. Redirect prevalidation applies to each resolved input URL.

### 7.3 yt-dlp extraction trust boundary (accepted)

yt-dlp dereferences the **page** URL itself during extraction and may follow
redirects to internal hosts *before* we ever see an output URL — the §7.1
authoring-time page-host check reduces but does not eliminate this. We accept
this as the **same operator/yt-dlp trust boundary that already exists today**:
the Streams (bundled) and URL adapters already hand operator-supplied page URLs
to yt-dlp. User providers add no *new* class of risk versus the existing
quick-cast URL path; the §7.2 output-URL revalidation is the FFmpeg-side guard.
The spec explicitly does **not** sandbox yt-dlp in v1 (noted as future work,
§13). Enumeration/resolve runs are time-bounded (§6) to cap resource abuse.

### 7.4 Trust scope

The receiver page is reachable by anyone on the LAN, so adding a provider is a
LAN-trust action — the same trust level as today's quick-cast URL/torrent
features. No new auth is added; the SSRF guardrails above are the boundary. User
providers are thus exactly as trusted as the existing operator-supplied URL
paths, no more.

## 8. Authoring UX (catalog drawer)

A guided form rendered inside the catalog drawer (see mockup
`authoring-flow.html`).

- **Provider tab strip:** built-ins, then user providers (each marked with a ✎
  edit pencil), then a dashed **＋ New** tab to create one.
- **Identity row:** Name · Glyph (2–4 chars, **auto-suggested** from name, §9) ·
  **Color** chosen from a fixed swatch palette (~8 bg/fg pairs tuned for the dark
  CRT — *not* a free hex picker, to keep contrast/aesthetic coherent).
- **Groups (optional):** a chip row + "＋ Group". With no groups, channels list
  flat under the provider.
- **Channels list:** each existing row carries a hidden locked channel ID and
  shows Name · URL · an **AUTO**-detected kind chip (`LIVE`/`VIDEO`/`PLAYLIST`/
  `DIRECT`) with a ▾ override · a play-mode control shown **only for
  `playlist`** (sequential/shuffle) · a group dropdown · delete. New rows have
  no ID until save; the server assigns one.
- **Inline channel editor:** as the operator types a URL, the detected kind and
  host-allow/deny status appear live (e.g. "Detected: DIRECT (.m3u8) · LAN host
  allowed", or a red "Host not allowed (loopback blocked)").
- **Footer:** Save provider / Cancel / Delete provider.
- **Result:** once saved, the provider appears as a catalog tab, its channels as
  starrable cards, and starred channels as preset tiles — identical to built-ins.

### New chassis routes & interface

A new `adapters.UserProviderEditor` interface (Create / Update / Delete /
Verify) implemented by the Streams adapter and consumed by the chassis — keeping
the chassis→adapter boundary interface-only, mirroring `PresetEditor` (the
chassis imports `internal/adapters/*` interfaces only, never the concrete
`streams` package). Routes mount on the **existing single chassis mux** under
`/receiver/*` (honoring the one-HTTP-listener invariant in CLAUDE.md — no new
listener):
- `POST /receiver/catalog/provider` — create; body = the provider form payload
  (name, glyph, color token, groups[], channels[]). Returns the saved provider
  (with locked `user:` ID) or a typed validation error rendered inline.
- `PUT /receiver/catalog/provider/{id}` — update (full replace of the user
  definition while preserving locked provider/channel IDs; removed channels
  clear matching preset slots and the response reports the cleared slots).
- `DELETE /receiver/catalog/provider/{id}` — delete; response reports how many
  preset slots were cleared (§9 item 8).
- `POST /receiver/catalog/channel/verify` — **Verify** (§9 item 4): a dry-run.
  `direct` → HEAD/GET probe of the (revalidated) URL; `single`/`playlist` →
  `yt-dlp --simulate` (and `--flat-playlist` count for playlists). Returns
  `{ok, kind, itemCount?, isLive?}` or a typed error → green "✓ 47 videos" /
  red "✗ private" chip. No persistence; purely advisory.
- `POST /receiver/catalog/provider/{id}/reorder` — persists a new channel/group
  order (see below).

**Reorder persistence.** Channels and groups carry the existing `Order` field.
A drag updates order **in memory and persists on drop** (one atomic write via
the store), not on every pointer move. Reorder touches only `Order` — it does
**not** re-enumerate playlists or rebuild item lists (cached items are reused);
it just re-sorts for display and sequential-play order.

**Form lifecycle.** Save closes the form back to the provider tab on success;
validation errors keep it open with inline chips. Playlist channels save
immediately (their enumeration is async, §6) — the operator can close the form
and the "N videos"/error chip resolves via a new `catalog`/`providerStatus`
envelope on the existing `GET /receiver/events` SSE stream (no dedicated
polling endpoint). The event fires after user-provider edits and after async
playlist enumeration status changes so an open drawer can update without a page
reload.

## 9. Enhancements (v1 scope)

**Baked in (correctness/UX necessities):**
1. **ID namespacing** (§4.5).
2. **Inline error states** in the channel editor (§8) surfacing §7.1 rejections.
3. **Async playlist enumeration** with pending/error states (§6).

**Selected options:**
4. **Verify button** — per-channel on-demand resolve/test before save (yt-dlp
   dry-run / m3u8 HEAD), catching dead URLs, private playlists, offline streams
   at authoring time.
5. **Live-vs-VOD detection** — yt-dlp `is_live` drives a `LIVE` chip (no pause,
   `CanPause:false`) vs `VIDEO` (pausable); sharper than lumping both as
   "single." `is_live` is **not persisted** in the definition — it is derived at
   Verify time (for the chip) and re-derived from the resolver result at play
   time (to set `Capabilities`), so it never goes stale.
6. **Glyph auto-suggest** — pre-fill the glyph from the provider name: take the
   initials of the first two words, else the first 2-4 alphanumerics, uppercased
   ("F1 TV" → "F1", "Cartoon Network" → "CN", "Lofi" → "LO"). Editable; only
   pre-fills while the glyph field is untouched.
7. **Drag-to-reorder** channels & groups (order drives sequential play +
   display); reuses the preset-bank drag-reorder code.
8. **Delete-with-cleanup + confirm** — deleting a channel or provider warns if
   its channels are starred into presets and clears those slots immediately.
   (The preset store already self-heals stale refs on load; this is friendly
   polish on safe-by-default and avoids waiting until restart.)

## 10. Integration & lifecycle

- **Edits are data, not TOML config.** Create/edit/delete go through the new
  store + chassis routes (like preset star/move), *not* the `ApplyConfig`/TOML
  path. On save: rebuild that provider's catalog, swap it into
  `definitions`/`catalogs`, re-derive presets. **No bridge restart.** Editing the
  currently-casting channel takes effect on the next cast; the active stream
  keeps playing. Because this is not a TOML field, it **bypasses the
  `ApplyScope` tiers entirely** (`ScopeHotSwap`/`ScopeRestartCast`/
  `ScopeRestartBridge` do not apply) — exactly like the existing preset
  star/move actions.
- **Enable gating:** saving the *first* user provider while the Streams adapter
  is disabled **auto-enables Streams** with a toast ("Streams source turned on
  so your provider can play"). This is the one path in this feature that must
  touch persistent TOML config: update `[adapters.streams].enabled`, persist it
  through the existing config/apply machinery, and start/initialize the Streams
  adapter runtime in the same process if it was skipped at startup. Ordinary
  user-provider edits still bypass `ApplyScope`; the auto-enable step does not.
- **Persistence:** load + merge at startup; atomic temp+rename on each edit;
  malformed entries dropped with a log.
- **Limits:** ≤ ~32 user providers; ≤ ~100 channels per provider; playlist
  enumeration bounded by the existing `MaxItemsPerChannel`.

## 11. Files

**New**
- `internal/adapters/streams/user_provider_store.go` — local manifest store
  (load/save/validate/atomic-write, ID-prefix + limit enforcement).
- `internal/adapters/streams/provider_user.go` — `user`-type catalog builder
  (per-kind item construction).
- `internal/adapters/streams/user_provider_routes.go` — Create/Update/Delete/
  Verify/reorder handlers (or fold into existing `routes.go`).
- `internal/adapters/user_provider.go` — `UserProviderEditor` interface (in the
  shared adapters package, like `preset.go`).
- `internal/chassis/templates/catalog-provider-form.html` — the authoring form.
- `internal/chassis/static/provider-form.js` — auto-detect/verify/reorder client
  logic (+ `*.behavior.test.js`).
- URL-validation helper (LAN-aware) — likely
  `internal/adapters/streams/url_security.go` or a shared netguard helper.

**Touched**
- `provider.go` — `Type:"user"`, `BadgeColor`, `ChannelDefinition.Kind`.
- `refresh.go` / `catalog.go` (streams) — merge local manifest; enumerate
  playlists; build `user` catalogs; replace the receiver catalog's fixed
  bundled-only loop with bundled + user provider ordering.
- `url_resolver.go` — flat-playlist enumeration capability; `is_live` surfacing.
- `playback.go` — LAN-aware `MediaInputPolicy` for user `direct` items;
  resolved video/audio URL host revalidation.
- `internal/chassis/data.go`, `catalog-drawer.html`, badge CSS — render
  `BadgeColor`; ✎ edit affordance; ＋ New tab.
- Remote manifest load — reject `user:`-prefixed provider IDs.
- `cmd/mister-groovy-relay/main.go` — wire the store/`data_dir`; auto-enable hook.

## 12. Testing

- **Unit:** store load/save/atomic-write/malformed-drop; provider/channel ID
  stability, prefix enforcement + collision rejection; channel/provider delete
  preset cleanup; sanitized cache-key generation for `user:` IDs; URL-validation
  matrix (loopback/link-local/metadata/`file` rejected; LAN + public allowed);
  kind auto-detection; glyph suggest; `user` catalog builder per kind.
- **Playlist enumeration:** drive with the existing fake-resolver harness —
  enumerate, cache, TTL, private/region-locked error path.
- **Integration** (`tests/integration`, ffmpeg + fake-mister): add provider →
  appears in catalog → cast direct m3u8, cast single, star→cast preset, delete →
  preset-slot cleanup.
- **Chassis JS:** `*.behavior.test.js` (`node --test`) for auto-detect / verify /
  reorder; Go handler tests for the new routes and catalog/status SSE envelope.
- **Lifecycle/security:** tests for disabled-Streams auto-enable/start,
  redirect prevalidation for user direct URLs, and yt-dlp `URL` + `AudioURL`
  revalidation before FFmpeg handoff.
- All four CI gates stay green: `go vet`, `go test`, `go test -race`,
  `go test -tags=integration ./...`.

## 13. Future (out of scope)

- JSON import/export of provider definitions (sharing, bulk edit).
- Logo-image icons.
- Per-channel cookies/auth UI.
- Generic multi-adapter catalog aggregation (Plex/Jellyfin library browsing in
  the same drawer) — the eventual home if more sources want catalog presence.
