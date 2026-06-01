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

Reuse the existing `ProviderDefinition` (`provider.go`) with additions:
- `Type: "user"` — new provider type. Unlike `youtube-channel-json` (fetches a
  remote JSON) or `direct-streams` (bundled, host-locked), a `user` provider
  carries its `Groups` and `Channels` **inline** in the definition.
- `BadgeColor string` (new) — an explicit color token from the curated palette
  (see §8). User data cannot supply a hardcoded CSS `BadgeClass`, so the
  chassis renders `BadgeColor` inline. Built-ins keep `BadgeClass`; the template
  prefers `BadgeClass` and falls back to `BadgeColor`.
- `ChannelDefinition.Kind string` (new) — `playlist` | `single` | `direct`,
  set by auto-detection (overridable). Drives the catalog builder.

`PlayMode` (existing) is meaningful only for `playlist` channels.

### 4.4 Channel kinds

| Kind | Example URL | Items produced | Resolution path |
|------|-------------|----------------|-----------------|
| `playlist` | `youtube.com/playlist?list=…` | N items (enumerated, §6) | yt-dlp resolve per item (existing) |
| `single` | `twitch.tv/foo`, single video | 1 non-direct item | yt-dlp resolve (existing) |
| `direct` | `…/stream.m3u8` | 1 `Direct:true` item | straight to FFmpeg + HLS buffer (existing) |

### 4.5 ID namespacing

User provider IDs carry a reserved prefix `user:` (e.g. `user:f1-tv`, slugified
from the display name with a uniqueness suffix on collision). The bundled/remote
manifests are **forbidden from using the `user:` prefix** (validated on remote
manifest load; offending remote providers are dropped). This guarantees user
providers can never shadow — or be shadowed by — a built-in in the merged map,
and makes "is this provider user-deletable?" a pure prefix check.

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

A new flat-playlist enumeration capability is added to the resolver surface
(the existing resolver only does single-item resolve).

## 7. Security & URL validation

Posture: **allow LAN, block internals.** Enforced at two checkpoints.

### 7.1 Authoring time (form save)

Each channel URL's host is parsed/normalized (reuse the
`normalizeConfigHost`-style logic in `config.go` + `net/netip`):
- **Reject always:** non-`http(s)` schemes incl. `file://`; loopback
  (`127.0.0.0/8`, `::1`); link-local + cloud-metadata (`169.254.0.0/16`,
  incl. `169.254.169.254`); unspecified/multicast/reserved; URL userinfo.
- **Allow:** public hosts **and** private/LAN ranges (`10/8`, `172.16/12`,
  `192.168/16`) — so a local HDHomeRun / restreamer works.
- Rejected URLs are surfaced inline by the form (validate-before-write) and
  never written to `user_providers.json`.

### 7.2 Play time (defense in depth)

Authoring-time checks are insufficient alone (DNS rebind, HTTP redirects), so
revalidate at dereference:
- **`direct` items** get a dedicated `MediaInputPolicy` (see
  `internal/ffmpeg/policy.go`): protocol whitelist `http,https,tcp,tls,crypto`
  — **no `file`** (unlike the bundled-Toonami policy, which allows `file` for
  FFmpeg HLS internals); blocked `Cookie`/`Authorization`/`Referer` headers;
  redirects revalidated against the §7.1 IP rules (max 3 hops); reconnect
  disabled. This is the DLNA spec's proven pattern minus `file`.
- **`single`/`playlist` items** are resolved by yt-dlp; the **resolved** media
  URL's host is re-checked against the §7.1 rules before handing to FFmpeg. The
  entered page URL is host-checked at authoring time.

### 7.3 Trust scope

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
- **Channels list:** each row shows Name · URL · an **AUTO**-detected kind chip
  (`LIVE`/`VIDEO`/`PLAYLIST`/`DIRECT`) with a ▾ override · a play-mode control
  shown **only for `playlist`** (sequential/shuffle) · a group dropdown · delete.
- **Inline channel editor:** as the operator types a URL, the detected kind and
  host-allow/deny status appear live (e.g. "Detected: DIRECT (.m3u8) · LAN host
  allowed", or a red "Host not allowed (loopback blocked)").
- **Footer:** Save provider / Cancel / Delete provider.
- **Result:** once saved, the provider appears as a catalog tab, its channels as
  starrable cards, and starred channels as preset tiles — identical to built-ins.

### New chassis routes & interface

A new `adapters.UserProviderEditor` interface (Create / Update / Delete /
Verify) implemented by the Streams adapter and consumed by the chassis — keeping
the chassis→adapter boundary interface-only, mirroring `PresetEditor`. Backing
HTTP routes live under the receiver namespace, e.g.
`POST /receiver/catalog/provider`, `PUT/DELETE …/provider/{id}`,
`POST …/channel/verify`, `POST …/provider/{id}/reorder`.

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
   "single."
6. **Glyph auto-suggest** — pre-fill the glyph from the provider name
   ("F1 TV" → "F1").
7. **Drag-to-reorder** channels & groups (order drives sequential play +
   display); reuses the preset-bank drag-reorder code.
8. **Delete-with-cleanup + confirm** — deleting a provider warns if its channels
   are starred into presets and clears those slots. (The preset store already
   self-heals stale refs on load; this is friendly polish on safe-by-default.)

## 10. Integration & lifecycle

- **Edits are data, not TOML config.** Create/edit/delete go through the new
  store + chassis routes (like preset star/move), *not* the `ApplyConfig`/TOML
  path. On save: rebuild that provider's catalog, swap it into
  `definitions`/`catalogs`, re-derive presets. **No bridge restart.** Editing the
  currently-casting channel takes effect on the next cast; the active stream
  keeps playing.
- **Enable gating:** saving the *first* user provider while the Streams adapter
  is disabled **auto-enables Streams** with a toast ("Streams source turned on
  so your provider can play").
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
  playlists; build `user` catalogs.
- `url_resolver.go` — flat-playlist enumeration capability; `is_live` surfacing.
- `playback.go` — LAN-aware `MediaInputPolicy` for user `direct` items;
  resolved-URL host revalidation.
- `internal/chassis/data.go`, `catalog-drawer.html`, badge CSS — render
  `BadgeColor`; ✎ edit affordance; ＋ New tab.
- Remote manifest load — reject `user:`-prefixed provider IDs.
- `cmd/mister-groovy-relay/main.go` — wire the store/`data_dir`; auto-enable hook.

## 12. Testing

- **Unit:** store load/save/atomic-write/malformed-drop; ID-prefix enforcement +
  collision rejection; URL-validation matrix (loopback/link-local/metadata/`file`
  rejected; LAN + public allowed); kind auto-detection; glyph suggest; `user`
  catalog builder per kind.
- **Playlist enumeration:** drive with the existing fake-resolver harness —
  enumerate, cache, TTL, private/region-locked error path.
- **Integration** (`tests/integration`, ffmpeg + fake-mister): add provider →
  appears in catalog → cast direct m3u8, cast single, star→cast preset, delete →
  preset-slot cleanup.
- **Chassis JS:** `*.behavior.test.js` (`node --test`) for auto-detect / verify /
  reorder; Go handler tests for the new routes.
- All four CI gates stay green: `go vet`, `go test`, `go test -race`,
  `go test -tags=integration ./...`.

## 13. Future (out of scope)

- JSON import/export of provider definitions (sharing, bulk edit).
- Logo-image icons.
- Per-channel cookies/auth UI.
- Generic multi-adapter catalog aggregation (Plex/Jellyfin library browsing in
  the same drawer) — the eventual home if more sources want catalog presence.
