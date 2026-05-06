# DLNA / UPnP MediaRenderer Adapter Design

**Date:** 2026-05-03  
**Status:** Design approved for spec; implementation plan not started  
**Scope:** Add a native Go `internal/adapters/dlna/` adapter that makes MiSTer_GroovyRelay appear as a DLNA / UPnP AV MediaRenderer target on the LAN.

## Problem

MiSTer_GroovyRelay currently appears as a cast target for Plex and Jellyfin and accepts direct URL casts through the URL adapter. The README lists DLNA / UPnP as a future relay source, but there is no design for how the bridge should participate in UPnP AV networks.

The desired behavior is renderer-side support: control-point apps such as VLC, BubbleUPnP, Kodi, Windows "Cast to device", or phone DLNA controllers should discover the bridge as a DLNA MediaRenderer and send it media to play. The bridge should then feed the selected media URL through the existing FFmpeg and Groovy_MiSTer data plane.

## Research Summary

Official UPnP AV MediaRenderer devices expose a device description plus service descriptions over HTTP, advertise themselves through SSDP, accept SOAP control actions, and publish state changes through UPnP eventing. A MediaRenderer requires `RenderingControl` and `ConnectionManager`; `AVTransport` is technically optional in the device template, but it is practically required for control points to set media URIs and issue play / pause / stop / seek commands.

Useful references:

- UPnP MediaRenderer:2 device template: <https://upnp.org/specs/av/UPnP-av-MediaRenderer-v2-Device.pdf>
- AVTransport:3 service: <https://upnp.org/specs/av/UPnP-av-AVTransport-v3-Service-20101231.pdf>
- ConnectionManager:3 service: <https://upnp.org/specs/av/UPnP-av-ConnectionManager-v3-Service-20101231.pdf>
- RenderingControl:3 service: <https://upnp.org/specs/av/UPnP-av-RenderingControl-v3-Service-20101231.pdf>
- gmrender-resurrect, a small headless UPnP / DLNA renderer: <https://github.com/hzeller/gmrender-resurrect>
- upmpdcli, a UPnP MediaRenderer front-end to MPD: <https://github.com/triplem/upmpdcli>
- upmpdcli compatibility note for auto-playing after `SetAVTransportURI`: <https://www.lesbonscomptes.com/upmpdcli/pages/upmpdcli-manual.html>

The existing bridge architecture is a good fit. `core.Manager` already accepts a generic `core.SessionRequest`, supports one active session, and exposes play / pause / stop / seek / status operations. The DLNA work should be mostly protocol translation, state/event bookkeeping, and SSDP networking.

## Goals

- Add a DLNA / UPnP AV MediaRenderer adapter named `dlna`.
- Discover on the LAN through SSDP and show a friendly device name in DLNA control-point target lists.
- Accept HTTP media URLs through `SetAVTransportURI` and start playback through the existing `core.Manager`.
- Support the minimum useful AVTransport control surface: `SetAVTransportURI`, `Play`, `Pause`, `Stop`, `Seek`, `GetMediaInfo`, `GetTransportInfo`, `GetPositionInfo`, `GetDeviceCapabilities`, `GetTransportSettings`, and `GetCurrentTransportActions`.
- Support enough `ConnectionManager` for control points to see the renderer as compatible with common HTTP-delivered video and audio resources.
- Support enough `RenderingControl` for common control points to query and set volume/mute without failing their UI flow, even though bridge-local volume is not a real output mixer today.
- Implement UPnP event subscriptions and `LastChange` notifications for AVTransport and RenderingControl.
- Keep the data plane unchanged. DLNA should call the same manager APIs as URL/Jellyfin rather than adding a second media path.

## Non-Goals

- Browsing DLNA MediaServers from the bridge UI. This design is renderer target support only.
- Implementing a UPnP MediaServer or ContentDirectory.
- Full DLNA certification.
- DRM-protected DLNA content.
- RTSP/RTP streaming in the first version. The first version targets HTTP/HTTPS resources that FFmpeg can ingest.
- Image-only rendering support. Images may be accepted later by converting to a short video or still frame, but they are out of scope for the first implementation.
- Playlist semantics beyond optional `SetNextAVTransportURI` storage. Queueing and `Next`/`Previous` compatibility can come after basic renderer reliability.
- Pulling in gmrender, libupnp, GStreamer, MPD, or another renderer sidecar.

## Recommended Approach

Build a native Go implementation inside `internal/adapters/dlna/`.

This is the best fit because the repo already has a Go adapter registry, a shared HTTP listener, a shared `core.Manager`, and native release archives for multiple platforms. A sidecar renderer would fight the existing data plane, add packaging complexity, and likely introduce cgo or extra runtime dependencies. A Go UPnP server library was considered, but the mature libraries are mostly control-point/client oriented, and the server-oriented libraries are either old, GPL-3, or not obviously compatible with the bridge's single-listener adapter model. We can still use those projects as behavioral references.

## Architecture

### Package Layout

```text
internal/adapters/dlna/
  adapter.go              # Adapter lifecycle, config decode/apply/status
  config.go               # [adapters.dlna] config and validation
  ssdp.go                 # SSDP advertisements, byebye, M-SEARCH responses
  descriptors.go          # device.xml and SCPD XML generation/serving
  soap.go                 # SOAP envelope parse/response/error helpers
  avtransport.go          # AVTransport state and action handlers
  connection_manager.go   # ConnectionManager state and action handlers
  rendering_control.go    # RenderingControl state and action handlers
  eventing.go             # SUBSCRIBE/UNSUBSCRIBE and NOTIFY delivery
  lastchange.go           # LastChange XML builders
  metadata.go             # DIDL-Lite metadata extraction helpers
  routes.go               # public /dlna/* route mounting helper
  ui.go                   # CurrentValues / optional panel details
  *_test.go               # Pure protocol/action/state tests
```

### Integration Boundary

The adapter owns all UPnP-specific behavior. It imports `internal/core`, but `core` remains adapter-agnostic.

Declare a package-local `SessionManager` interface covering the methods already used by URL/Jellyfin:

- `StartSession(core.SessionRequest) error`
- `Status() core.SessionStatus`
- `Pause() error`
- `Play() error`
- `Stop() error`
- `SeekTo(offsetMs int) error`

`core.Manager` satisfies this structurally. Tests can inject a fake manager.

Locking discipline follows the existing adapters: never hold the DLNA adapter mutex while calling `core.Manager` or while doing network I/O to event subscribers.

The adapter constructor should receive the shared bridge identity and advertised network coordinates from `cmd/mister-groovy-relay/main.go`:

- `DeviceUUID`: `store.DeviceUUID` from the existing persisted bridge store. DLNA must not mint its own stable UUID and must not import the Plex package just to reach the store type. If the store is later renamed or extracted, `main.go` still owns passing a stable UUID into the adapter.
- `HostIP`: the resolved bridge host IP already used for Plex advertisement.
- `HTTPPort`: `bridge.ui.http_port`.

### Public Route Mounting

DLNA must not use `adapters.RouteProvider` / `UIRoutes` for protocol routes. That extension point mounts under `/ui/adapter/<name>/` and wraps mutating methods in settings-UI CSRF middleware, which is correct for browser UI routes and wrong for UPnP control points.

Add a small non-UI route extension, for example:

```go
type PublicRouteProvider interface {
	MountPublicRoutes(*http.ServeMux)
}
```

`cmd/mister-groovy-relay/main.go` should mount public adapter routes on the shared mux before `uiSrv.Mount(mux)`. Plex can keep its existing explicit `MountRoutes` call until a later cleanup; DLNA should implement the new public route provider so `/dlna/*` is wired through the registry without weakening `/ui/*` CSRF behavior.

### HTTP Surface

Mount the DLNA HTTP surface on the existing bridge HTTP listener (`bridge.ui.http_port`) with disjoint paths:

- `GET /dlna/device.xml`
- `GET /dlna/AVTransport.xml`
- `GET /dlna/ConnectionManager.xml`
- `GET /dlna/RenderingControl.xml`
- `POST /dlna/control/AVTransport`
- `POST /dlna/control/ConnectionManager`
- `POST /dlna/control/RenderingControl`
- `SUBSCRIBE /dlna/event/AVTransport`
- `UNSUBSCRIBE /dlna/event/AVTransport`
- `SUBSCRIBE /dlna/event/RenderingControl`
- `UNSUBSCRIBE /dlna/event/RenderingControl`
- `SUBSCRIBE /dlna/event/ConnectionManager`
- `UNSUBSCRIBE /dlna/event/ConnectionManager`

These routes should bypass the settings UI CSRF middleware. UPnP control points are not browsers operating inside the UI origin model and will not send the CSRF headers expected by `/ui/*`.

Protocol handlers should still apply their own admission checks:

- Accept control, event, and descriptor requests only from private/LAN or loopback remote addresses by default. Loopback is allowed for local tests and same-host controllers; public remote addresses are rejected.
- Bound SOAP request bodies before XML parsing.
- Return UPnP SOAP faults for protocol errors, not UI fragments or plain text bodies.

### Device Description

The root device should advertise:

- `deviceType`: `urn:schemas-upnp-org:device:MediaRenderer:1` for broad compatibility.
- Stable `UDN`: `uuid:<store.DeviceUUID>`, passed from `main.go`.
- `friendlyName`: `adapters.dlna.device_name`, defaulting to `MiSTer`.
- Manufacturer/model fields identifying MiSTer_GroovyRelay.
- One service each for `AVTransport:1`, `ConnectionManager:1`, and `RenderingControl:1`.

Use version 1 service types in the exposed descriptors even if the service descriptions borrow ideas from newer documents. Older control points tend to target `:1`, and the first implementation does not need level-2/3 additions.

### Service Action Surface

The SCPD documents published at `/dlna/AVTransport.xml`, `/dlna/RenderingControl.xml`, and `/dlna/ConnectionManager.xml` must list every action that v1 either implements or stubs. The disposition for each action is one of:

- **Impl** — full implementation per the per-section spec.
- **Stub** — declared in SCPD with the spec-required argument list, but the handler returns `501 Action Failed` with a stable error string. This is required when a controller refuses to bind to a renderer whose SCPD omits a "mandatory" action.
- **Omit** — not declared in SCPD at all.

These tables are normative for the v1 SCPD. Phase 1 ships exactly this surface; broader support waits for phased delivery.

#### AVTransport:1

| Action | v1 Disposition | Notes |
|---|---|---|
| `SetAVTransportURI` | Impl | Includes URI validation per `### SetAVTransportURI`. |
| `SetNextAVTransportURI` | Stub | Spec'd to store-and-discard so `GetMediaInfo.NextURI` is always empty in v1. |
| `GetMediaInfo` | Impl | |
| `GetTransportInfo` | Impl | |
| `GetPositionInfo` | Impl | |
| `GetDeviceCapabilities` | Impl | |
| `GetTransportSettings` | Impl | |
| `Stop` | Impl | |
| `Play` | Impl | Only `Speed=1`. |
| `Pause` | Impl | |
| `Seek` | Impl | `REL_TIME` unit only; other units return `710`. |
| `Next` | Stub | Returns `501`; `GetCurrentTransportActions` never advertises it. |
| `Previous` | Stub | Returns `501`; `GetCurrentTransportActions` never advertises it. |
| `SetPlayMode` | Stub | Only `NORMAL` accepted; other modes return `712 Play mode not supported`. |
| `GetCurrentTransportActions` | Impl | |

`Record`, `SetRecordQualityMode`, and other recording-related actions are Omit.

#### RenderingControl:1

| Action | v1 Disposition | Notes |
|---|---|---|
| `ListPresets` | Impl | Returns `FactoryDefaults`. |
| `SelectPreset` | Stub | `FactoryDefaults` resets `Volume=100`/`Mute=false`; other presets return `701`. |
| `GetMute` | Impl | `Channel=Master` only. |
| `SetMute` | Impl | `Channel=Master` only. |
| `GetVolume` | Impl | `Channel=Master` only. |
| `SetVolume` | Impl | `Channel=Master` only; range `0..100`. |

All RC:1 actions outside this set (brightness, contrast, sharpness, channel-specific volume, `GetVolumeDB`, `GetVolumeDBRange`, loudness, etc.) are Omit. Controllers that try them receive `401 Invalid Action`.

#### ConnectionManager:1

| Action | v1 Disposition | Notes |
|---|---|---|
| `GetProtocolInfo` | Impl | Returns `SourceProtocolInfo`/`SinkProtocolInfo` per `## ConnectionManager`. |
| `PrepareForConnection` | Impl | No-op returning `ConnectionID=0`/`AVTransportID=0`/`RcsID=0`. |
| `ConnectionComplete` | Impl | No-op for `ConnectionID=0`. |
| `GetCurrentConnectionIDs` | Impl | Always returns `0`. |
| `GetCurrentConnectionInfo` | Impl | Returns the single virtual input connection. |

All evented variables required by CM:1 (`SourceProtocolInfo`, `SinkProtocolInfo`, `CurrentConnectionIDs`) are declared evented in the SCPD.

### SSDP

The adapter should bind UDP multicast for SSDP on `239.255.255.250:1900`. Port 1900 is a shared system port on many hosts, so the socket setup must be explicit:

- Prefer an implementation path that enables address/port reuse where the platform supports it.
- If SSDP bind or multicast join fails, `Start` should set adapter state to `StateError` and return the error; an enabled DLNA adapter without SSDP is effectively invisible.
- Keep descriptor routes mounted even when the adapter is disabled or failed, but only advertise and answer M-SEARCH while the adapter is running.

On start:

- Send `ssdp:alive` NOTIFY messages for:
  - `upnp:rootdevice`
  - the device UUID
  - `urn:schemas-upnp-org:device:MediaRenderer:1`
  - `urn:schemas-upnp-org:service:AVTransport:1`
  - `urn:schemas-upnp-org:service:ConnectionManager:1`
  - `urn:schemas-upnp-org:service:RenderingControl:1`
- Include `LOCATION: http://<host-ip>:<http-port>/dlna/device.xml`.
- Include a conservative `CACHE-CONTROL: max-age=1800`.

During runtime:

- Reply to M-SEARCH requests for `ssdp:all`, `upnp:rootdevice`, the UUID, MediaRenderer, and the supported service URNs.
- Periodically refresh advertisements before expiry.
- On stop, send `ssdp:byebye` for the same NT/USN set.

Interface selection should initially mirror existing Plex GDM behavior: use the bridge's advertised host IP and default network behavior. A future multi-NIC improvement can add `adapters.dlna.interface` if needed. In v1, every `LOCATION` header must be generated from the configured/resolved `HostIP` and `HTTPPort`, not from the UDP packet's local address, so M-SEARCH responses are stable and match the device descriptor.

## Control Flow

### Common Action Rules

All AVTransport and RenderingControl actions operate on `InstanceID=0` only. Invalid instance IDs return UPnP error `718 Invalid InstanceID`.

Before any action mutates `core.Manager`, the adapter must enforce a DLNA ownership guard:

- A loaded-but-not-playing URI is adapter-local state and can be replaced by `SetAVTransportURI`.
- Starting **fresh** playback (the adapter has no current `dlna:` session) is allowed to preempt any active session, including a foreign Plex/Jellyfin/URL session. A control point that asks this renderer to play media is asserting renderer ownership, and `core.Manager.StartSession` already preempts by design. This asymmetry mirrors the URL adapter's documented preempt behavior in `internal/adapters/url/controls.go`.
- Mutating an existing session — `Pause`, paused-session `Play`, `Stop`, or `Seek` — must first check `core.Status().AdapterRef`. If it is non-empty and does not match the adapter's current `dlna:` session ref, return `701 Transition not available` and leave the foreign session untouched.

#### Session Ref Lifecycle

The DLNA adapter holds one `currentRef string` field guarded by the adapter mutex. The lifecycle is:

1. When building a `core.SessionRequest`, the adapter mints a fresh ref (`dlna:<random-id>`) synchronously.
2. Under the adapter mutex: store the new ref in `currentRef`, build the `OnStop` closure that **captures the new ref by value** (not by pointer to `currentRef`), then release the mutex.
3. Call `core.Manager.StartSession` with the lock released. `core.Manager` may fire a prior session's `OnStop` from a goroutine; that prior `OnStop` carries its own captured ref and is unaffected by step 2.
4. On any `OnStop` invocation: re-acquire the adapter mutex, compare the captured ref against `currentRef`, and only update transport state / clear `currentRef` / fire `LastChange` on equality. Mismatch is a silent no-op — that callback belongs to a superseded session.
5. `Pause`, `Stop`, paused-`Play`, and `Seek` read `currentRef` under the mutex, drop the mutex before calling `core.Manager`, then re-acquire it for state mutation. The mutex is never held across a `core.Manager` call (consistent with the `Manager.mu` discipline documented in `CLAUDE.md`).

This ordering guarantees that a fast `core.StartSession` whose plane fails immediately and fires the new session's `OnStop` before the preempted session's `OnStop` cannot corrupt `currentRef`: the preempted session's late `OnStop` carries the old ref and no-ops on compare-and-clear.

SOAP fault mapping:

| Condition | UPnP error |
|---|---|
| Unknown action | `401 Invalid Action` |
| Missing or malformed argument | `402 Invalid Args` |
| Invalid `InstanceID` | `718 Invalid InstanceID` |
| Invalid transition or foreign active session | `701 Transition not available` |
| Unsupported or disallowed URI scheme/address | `716 Resource not found` |
| Container/MIME advertised in metadata that is not in `SinkProtocolInfo` | `714 Illegal MIME-type` |
| Backend probe/playback failure | `716 Resource not found` when the resource cannot be reached, otherwise `501 Action Failed` |
| Unsupported seek unit | `710 Seek mode not supported` |
| Invalid seek target/time | `711 Illegal seek target` |
| Unsupported play speed | `717 Play speed not supported` |

State is updated only after the corresponding manager call succeeds. On `StartSession`, `Play`, or `SeekTo` failure, keep the prior transport state when possible, set `TransportStatus=ERROR_OCCURRED`, store a redacted last error for status/debugging, and fire `LastChange`.

### SetAVTransportURI

`SetAVTransportURI(InstanceID=0, CurrentURI, CurrentURIMetaData)` validates and stores the URI plus metadata. It does not have to start playback according to the strict model. For compatibility, the adapter should support `autoplay_on_set_uri` because some control points set a URI and never send `Play`.

Validation rules:

- `InstanceID` must be `0`.
- URI must parse as an absolute URL.
- First version accepts only `http` and `https`.
- Resolve hostname targets before playback. Reject loopback, link-local, multicast, and unspecified addresses. Reject public addresses unless `allow_public_source_urls = true`. Private RFC1918/ULA LAN targets are accepted by default.
- Follow at most three redirects during validation, and re-validate every `Location` target before accepting the final URL. Redirects to a disallowed scheme or address fail before FFmpeg sees the URL.
- FFmpeg/ffprobe must not be allowed to bypass the validator through their own redirect handling or playlist child fetches. For v1 the implementation strategy is **path A: disable FFmpeg's own redirect/playlist following and only hand it the prevalidated final URL**. The validating-proxy alternative is explicitly out of scope for v1; if real-world Phase 2 testing shows path A is insufficient for a target controller, raise it as a follow-up spec.
- Concrete FFmpeg/ffprobe invocation rules for path A:
  - Pass `-protocol_whitelist file,http,https,tcp,tls,crypto` (no `udp`, no `rtp`, no `rtsp`, no `concat`, no `subfile`).
  - Do not pass `-allowed_extensions ALL` and do not enable HLS/DASH demuxers.
  - Set `-reconnect 0` and `-reconnect_at_eof 0`; reconnects re-issue the request, which can race the validator after a server-side rebind.
  - Pass `-rw_timeout` (microseconds) bounded by an adapter constant so a stalled remote does not pin the data plane.
  - Do not pass `-headers` containing `Referer:` or other URL-bearing headers that would let a redirect server discover internal hosts.
- Container formats whose demuxers fetch child resources (HLS `application/vnd.apple.mpegurl`, DASH MPD, segmented `vnd.dlna.mpeg-tts` over chunked transfer that the demuxer follows by URL) are **deferred to Phase 5** along with HLS protocolInfo. Until then, omit their entries from `SinkProtocolInfo` and reject them at `SetAVTransportURI` validation if a control point sends them anyway.
- Metadata may be empty. If present, parse enough DIDL-Lite to capture title, duration, class, and protocolInfo when available.

### Play

`Play(InstanceID=0, Speed=1)` starts or resumes playback:

- Only `Speed=1` is supported; other speeds return `717 Play speed not supported`.
- If state is `PAUSED_PLAYBACK` and the active core session is the current `dlna:` session:
  - For seekable sources with known duration, call `core.Play()`.
  - For live or unknown-duration sources, rebuild the same `core.SessionRequest` with `SeekOffsetMs=0` and call `core.StartSession` to reconnect from the live edge, matching the URL adapter's live-resume behavior.
- If a URI is loaded and no active DLNA session is running, build `core.SessionRequest`:
  - `StreamURL`: stored URI
  - `Capabilities`: `{CanSeek: <see below>, CanPause: true}`
  - `AdapterRef`: stable per DLNA URI/session, e.g. `dlna:<random-id>`
  - `DirectPlay`: `true` only when seek is implemented with FFmpeg `-ss` against the accepted final URL. For live/unknown-duration resources this may still be true for playback, but `CanSeek` must be false and `Seek` must not be advertised.
  - `OnStop`: adapter callback to update transport state and fire `LastChange`

  Pre-probe `CanSeek` derivation: the DLNA adapter does not run its own ffprobe before calling `core.StartSession` (that would duplicate `core.Manager.probeForStart`). Instead:
  - Seed `CanSeek` from DIDL-Lite metadata: `res@duration > 0` and `res@protocolInfo` indicating a non-live container ⇒ `CanSeek=true`.
  - Empty, missing, or zero-duration metadata ⇒ `CanSeek=false` for the initial `SessionRequest`.
  - After playback starts, `core.Status().Duration` reflects the post-probe duration. `GetCurrentTransportActions` reads the live `core.Status()` rather than the seeded `Capabilities`, so a seekable stream that arrived with empty metadata correctly advertises `Seek` in the controller UI once probe completes — even though the in-flight `SessionRequest.Capabilities.CanSeek` was conservative.
  - This conservative seeding is acceptable because `core.Manager` enforces `Capabilities.CanSeek` only at the `SeekTo` boundary; `GetCurrentTransportActions` is a read-only query independent of that gate.
- If already playing, return success and refresh state.
- Set `TransportState=TRANSITIONING` before a long-running `StartSession` call only if the state change is immediately evented; otherwise keep the prior state until success. On success, set `PLAYING` and fire `LastChange`.

### Pause, Stop, Seek

- `Pause` applies the ownership guard, calls `core.Pause()`, and sets UPnP state to `PAUSED_PLAYBACK` only after success.
- `Stop` applies the ownership guard, calls `core.Stop()`, and sets state to `STOPPED` only after success. Stopping while already stopped is success.
- `Seek` supports `REL_TIME` first. Convert `HH:MM:SS` or `HH:MM:SS.FFF` to milliseconds, reject negative targets, reject targets beyond known duration, and call `core.SeekTo` only when the active `dlna:` session has `CanSeek=true`.
- Unsupported seek units return `710 Seek mode not supported`; bad targets return `711 Illegal seek target`.

### Query Actions

`GetTransportInfo`, `GetPositionInfo`, `GetMediaInfo`, and `GetCurrentTransportActions` should derive their answers from the adapter's stored URI/metadata plus `core.Status()`.

Map core states to UPnP states:

| Core state | UPnP TransportState |
|---|---|
| idle / stopped | `STOPPED` |
| playing | `PLAYING` |
| paused | `PAUSED_PLAYBACK` |
| error | `STOPPED` with `TransportStatus=ERROR_OCCURRED` |

Position and duration should be formatted as `HH:MM:SS`. Unknown duration should be `00:00:00`.

Additional AVTransport query/action return values:

- `GetDeviceCapabilities`: `PlayMedia=NETWORK`, `RecMedia=NOT_IMPLEMENTED`, `RecQualityModes=NOT_IMPLEMENTED`.
- `GetTransportSettings`: `PlayMode=NORMAL`, `RecQualityMode=NOT_IMPLEMENTED`.
- `GetCurrentTransportActions`: include only actions valid in the current state and source shape. Examples:
  - No URI loaded: empty.
  - Stopped with URI loaded: `Play`.
  - Playing seekable VOD: `Pause,Stop,Seek`.
  - Playing live/unknown-duration: `Pause,Stop`.
  - Paused seekable VOD: `Play,Stop,Seek`.
  - Paused live/unknown-duration: `Play,Stop`.
- `GetPositionInfo`: `Track=1` when a URI is loaded, `Track=0` otherwise; `TrackURI` is the stored URI; `RelTime` comes from `core.Status().Position` only when the active session ref matches the current `dlna:` ref.
- `GetMediaInfo`: `NrTracks=1` when a URI is loaded, `MediaDuration` from metadata or probe status when known, `CurrentURI` as stored URI, `NextURI` empty unless `SetNextAVTransportURI` is later implemented.

## ConnectionManager

First version should return:

- `SourceProtocolInfo`: empty
- `SinkProtocolInfo`: a comma-separated list covering common HTTP inputs:
  - `http-get:*:video/mp4:*`
  - `http-get:*:video/x-matroska:*`
  - `http-get:*:video/mpeg:*`
  - `http-get:*:video/vnd.dlna.mpeg-tts:*`
  - `http-get:*:audio/mpeg:*`
  - `http-get:*:audio/mp4:*`
  - `http-get:*:audio/flac:*`
  - `http-get:*:audio/x-flac:*`

Do not advertise HLS/M3U8 in v1 until the source validator can constrain playlist child URLs. Add these later only with tests that prove nested playlist requests cannot escape the allowed address policy:

- `http-get:*:application/vnd.apple.mpegurl:*`
- `http-get:*:application/x-mpegURL:*`

Actions:

- `GetProtocolInfo`: return source/sink protocol strings.
- `GetCurrentConnectionIDs`: return `0` always. This models the renderer's single virtual input connection and avoids control points seeing an empty connection list as "not ready."
- `GetCurrentConnectionInfo`: return connection details for ID `0`, with `Direction=Input`, `Status=OK`, `AVTransportID=0`, `RcsID=0`.
- `PrepareForConnection`: include it in SCPD and implement it as a no-op that returns `ConnectionID=0`, `AVTransportID=0`, and `RcsID=0`.
- `ConnectionComplete`: include it in SCPD and implement it as a no-op for `ConnectionID=0`.

## RenderingControl

The bridge does not currently have a true output mixer. Still, many control points probe or manipulate volume and mute as part of their renderer UI.

Implement an adapter-local virtual volume:

- `Volume`: 0-100, default 100.
- `Mute`: bool, default false.
- `ListPresets`: `FactoryDefaults`.
- `GetVolume`, `SetVolume`, `GetMute`, `SetMute`.

`SetVolume` and `SetMute` update only adapter state and event subscribers. They do not change FFmpeg or Groovy audio volume in the first version. A later bridge-level audio-gain feature could connect this virtual volume to the data plane.

RenderingControl action rules:

- `InstanceID` must be `0`.
- `Channel` must be `Master`; unsupported channels return a SOAP action error.
- `Volume` accepts `0..100`; out-of-range values return invalid args instead of clamping silently.
- `Mute` accepts UPnP boolean forms `0`/`1` and `false`/`true`.
- `GetVolume`, `SetVolume`, `GetMute`, `SetMute`, and `ListPresets` must all appear in the SCPD with their v1 argument names so controllers can build their UI without probing failures.

## Eventing

Implement UPnP GENa-style event subscriptions:

- Accept `CALLBACK`, `NT: upnp:event`, and `TIMEOUT` on `SUBSCRIBE`.
- Generate a `SID: uuid:<subscription-id>`.
- Support renewal when `SID` is supplied without `CALLBACK`.
- Support `UNSUBSCRIBE`.
- Send initial `NOTIFY` immediately after subscribe.
- Send subsequent `NOTIFY` when evented state changes.
- Cap total subscriptions per service and per remote address. When a cap is hit, **reject the new SUBSCRIBE with `5xx Service Unavailable`** rather than evicting an existing subscription. LRU/oldest-idle eviction is weaponizable: an attacker on the LAN can churn subscriptions to displace a legitimate controller's subscription. Reject-new is the safer default; revisit only if a real controller's resubscribe-on-failure behavior is incompatible.
- Reject callback URLs that are not `http` or `https`, resolve to loopback, link-local, multicast, unspecified, or public addresses, or do not resolve to a private/LAN target. `allow_public_source_urls` does not loosen event callback policy.
- Bound callback request timeouts and response body reads. The adapter never needs to read a large NOTIFY response body.
- Prune subscriptions after repeated delivery failures or expiration.

Event payloads:

- AVTransport: event `LastChange` with `TransportState`, `TransportStatus`, `CurrentTrackURI`, `CurrentTrackDuration`, `RelativeTimePosition`, and `CurrentTransportActions`.
- RenderingControl: event `LastChange` with `Volume` and `Mute`.
- ConnectionManager: event `SourceProtocolInfo`, `SinkProtocolInfo`, and `CurrentConnectionIDs`. Per the Service Action Surface table, the v1 SCPD marks all three evented (CM:1 mandates it). In practice these never change at runtime, so subscribers receive only the initial NOTIFY.

Network I/O to callback URLs must be bounded by short timeouts and done outside adapter locks. Failed notifications should eventually prune the subscription.

Initial `NOTIFY` ordering is precise to avoid losing or duplicating an evented state change that races the subscribe handshake:

1. Acquire the subscription-table lock.
2. Commit the new subscription record with its assigned `SID` and timeout.
3. Build an **initial-state snapshot** of every evented variable for the subscribed service, taken from the same evented-state field set the regular LastChange path reads.
4. Release the subscription-table lock.
5. Send the `200 OK` SUBSCRIBE response carrying the `SID`.
6. Dispatch the snapshot as the initial `NOTIFY` (sequence `0`) on the regular event-delivery goroutine.
7. Subsequent state changes flow through the normal LastChange path with monotonically increasing sequence numbers.

Building the snapshot under the subscription lock is what guarantees that no LastChange between subscribe-commit and initial-NOTIFY-dispatch is lost: any state change that happens after step 4 is published with a sequence number > 0 and the subscriber receives a coherent ordered stream.

## Security

DLNA renderers are unauthenticated by design. This is acceptable only under the same LAN-trusted assumption as the current settings UI and Plex/Jellyfin control surfaces.

Specific risks:

- SSRF: any LAN client can ask the bridge to fetch a URL. There are public examples of MediaRenderer SSRF issues in TVs via unauthenticated `SetAVTransportURI`.
- LAN nuisance control: any client can interrupt playback.
- Event callback abuse: a malicious control point can subscribe with callback URLs.

First-version mitigations:

- Adapter disabled by default.
- Explicit README warning that DLNA exposes unauthenticated LAN control.
- Restrict accepted control/event requests to private/LAN or loopback remote addresses by default.
- Reject `file://`, `ftp://`, `rtsp://`, loopback, link-local, multicast, unspecified, and non-HTTP media source URLs in v1.
- Revalidate every media redirect before playback and prevent FFmpeg/ffprobe from following unvalidated redirects or playlist child URLs.
- Reject event callback URLs outside private/LAN `http`/`https` targets, independent of `allow_public_source_urls`.
- Bound all outbound event callbacks with a short timeout and cap response body reads.
- Cap SOAP request body size before XML parsing.
- Cap subscription count and expire idle subscriptions.
- Redact userinfo and query credentials in logs using the existing core-style redaction approach (`internal/core/manager.go` already redacts `api_key`, `x-plex-token`, `token`). For DLNA, also redact: `X-Plex-Token` (Plex DLNA gateway), `api_key` and `ApiKey` (Jellyfin DLNA gateway), `auth` and `authorization` query params, and the `userinfo` portion of any `http://user:pass@host/...` URL. Cover both URL query strings and DIDL-Lite `<res>` element bodies, since some servers embed credentials there.

## Config

Add:

```toml
[adapters.dlna]
enabled = false
device_name = "MiSTer"
autoplay_on_set_uri = false
allow_public_source_urls = false
```

Field meanings and `ApplyScope`:

| Field | ApplyScope | Reason |
|---|---|---|
| `enabled` | `ScopeHotSwap` | Toggled through the existing `SetEnabled` path that starts/stops SSDP background work without rebuilding the listener. |
| `device_name` | `ScopeRestartBridge` | Baked into SSDP identity and the device descriptor at process startup. SSDP controllers cache the friendly name against the `UDN`; renaming mid-run leaves stale entries on the LAN. |
| `autoplay_on_set_uri` | `ScopeHotSwap` | Read at `SetAVTransportURI` request time. |
| `allow_public_source_urls` | `ScopeHotSwap` | Read at URI validation time. Tightening this field while a public-URL session is already playing does NOT abort the session — the validation gate runs at start, not mid-session. |

Field meanings:

- `enabled`: hot-swappable adapter enable/disable.
- `device_name`: shown in DLNA control points.
- `autoplay_on_set_uri`: compatibility mode for controllers that do not send `Play`.
- `allow_public_source_urls`: default false. When false, first version should accept media URLs that resolve to private/LAN addresses and reject public internet targets. This is deliberately stricter than many TVs because the bridge runs on a general-purpose host.

`autoplay_on_set_uri` is part of v1, not a deferred compatibility toggle. Keep it default false and include it in Phase 2 playback tests. `allow_public_source_urls` applies only to media source URLs, not event callback targets.

Config/UI implementation tasks:

- Add `[adapters.dlna]` defaults to `internal/config/example.toml` and the embedded first-run template in `internal/config/example.go`.
- Expose `enabled`, `device_name`, `autoplay_on_set_uri`, and `allow_public_source_urls` through `Fields()` and `CurrentValues()`.
- Implement `Validate` so the settings UI rejects invalid values before disk write.
- Implement `SetEnabled` so the existing adapter save/toggle lifecycle can start and stop SSDP background work.

Potential follow-up fields:

- `interface`
- `advertise_interval_seconds`
- `accepted_mime_types`
- `volume_controls_audio_gain`

## Testing Strategy

### Unit Tests

- Config defaults, `Fields()`, `CurrentValues()`, `Validate`, `ApplyConfig`, and `SetEnabled` behavior.
- Public route mounting: `/dlna/*` routes mount outside `/ui/adapter/<name>/` and are not CSRF-wrapped.
- `main.go`/registry route wiring calls `MountPublicRoutes` for public providers before UI mounting.
- SSDP response formatting for every supported search target.
- Device descriptor XML contains required device/service elements.
- SCPD XML contains action and state variable definitions expected by common control points.
- SOAP parser handles namespaces, empty metadata, missing arguments, and invalid actions.
- SOAP responses and SOAP faults match UPnP shape.
- AVTransport action handlers update state and call the fake manager correctly.
- Foreign `AdapterRef` sessions are not paused, resumed, stopped, or sought by DLNA control actions.
- `StartSession`, `Play`, `Pause`, and `SeekTo` failures leave state consistent and emit the expected SOAP fault/`LastChange`.
- `GetCurrentTransportActions` omits `Seek` for live/unknown-duration sources.
- `Seek` time parser accepts `HH:MM:SS` and `HH:MM:SS.FFF`.
- LastChange XML escapes URI/metadata safely.
- Event subscription add/renew/remove/expire behavior.
- SSRF/source URL validation for disallowed schemes, public/private toggle, loopback/link-local/multicast/unspecified targets, redirect chains, and playlist child URLs when playlist support is enabled.
- Event callback validation for disallowed schemes/addresses, subscription caps, timeout behavior, and failed-delivery pruning.
- SOAP body size limits and XML parser error paths.

### Integration Tests

- Start adapter on localhost with fake manager and issue SOAP requests over HTTP.
- Verify `/dlna/*` bypasses settings UI CSRF while `/ui/*` still requires CSRF on writes.
- Simulate M-SEARCH over UDP and verify unicast SSDP response.
- Subscribe a local HTTP callback server and assert initial/subsequent NOTIFY payloads.
- Run a real `core.Manager` + fake MiSTer path with an HTTP-served MP4, matching the URL adapter integration style.
- Run a fake redirecting media server and prove disallowed redirects are rejected before FFmpeg/ffprobe execution.
- Start/Stop/Start the adapter and verify SSDP goroutines, sockets, and subscription cleanup are idempotent.

### Manual Compatibility Matrix

Required before marking stable:

- VLC control point.
- BubbleUPnP or equivalent Android controller.
- Kodi.
- Windows "Cast to device".
- At least one UPnP MediaServer such as Jellyfin DLNA, Plex DLNA, Gerbera, or MiniDLNA.

Record for each:

- Renderer appears.
- Controller can set media.
- Playback starts.
- Pause/resume/stop work.
- Seek behavior.
- Position/duration display.
- Whether `autoplay_on_set_uri` is needed.

## Phased Delivery

### Phase 1: Protocol Skeleton

- Config and adapter registration.
- Public route provider and HTTP descriptors.
- SSDP alive/byebye/M-SEARCH.
- Minimal SOAP dispatch with `GetProtocolInfo` and query actions.
- Unit tests prove discoverability and descriptor correctness.

### Phase 2: Playback

- `SetAVTransportURI`, `Play`, `Stop`, and `autoplay_on_set_uri`.
- Source URL validation, redirect policy, and first-pass SSRF tests before any URL reaches FFmpeg/ffprobe.
- `core.StartSession` integration.
- OnStop state updates.
- DLNA ownership guard for mutating controls.
- Manual smoke with one controller and one direct MP4.

### Phase 3: Controls and State

- `Pause`, `Seek`, `GetPositionInfo`, `GetCurrentTransportActions`.
- Metadata/duration mapping.
- Failure rollback and SOAP fault mapping for manager errors.
- RenderingControl virtual volume/mute.
- Integration tests with fake manager.

### Phase 4: Eventing and Compatibility

- SUBSCRIBE/UNSUBSCRIBE/NOTIFY.
- Callback URL validation, subscription caps, cleanup, and LastChange moderation.
- Compatibility matrix.

### Phase 5: Hardening

- README documentation and troubleshooting.
- `internal/config/example.toml`, embedded first-run template, and UI field polish.
- Broaden MIME/protocolInfo list based on real controllers.
- Consider HLS/M3U8 only after playlist child URL validation is implemented and tested.

## Risks and Open Questions

- Some control points are strict about SCPD completeness. We should expose a conservative v1 service surface rather than a large v3 surface we do not fully implement.
- Some controllers require eventing before showing transport controls. Skipping eventing would make the first version look deceptively simpler but would likely create compatibility churn.
- Windows compatibility may require vendor-specific descriptor tags or exact `DLNA.ORG_PN` protocolInfo values. Defer until manual testing proves the need.
- Public internet URLs are useful but increase SSRF risk. The safer first version defaults to private/LAN media URLs only and exposes a deliberate opt-in for public URLs.
- UPnP and the settings UI share one HTTP listener. Route mounting must keep DLNA control paths outside UI CSRF middleware without weakening `/ui/*`.
- **Multi-NIC hosts have a single advertised `LOCATION`.** v1 derives `LOCATION` from `bridge.host_ip`/`HTTPPort`, the same coordinate used for Plex advertisement. On a host with two NICs (e.g. management + media VLANs), an M-SEARCH arriving on the non-`host_ip` NIC will be answered with a URL pointing at the other NIC, which a controller on that subnet may be unable to reach. Documented as a v1 limitation; the deferred `adapters.dlna.interface` field plus per-interface advertisement loops (listed in `Potential follow-up fields`) is the planned remedy.
- **UDP 1900 may already be bound by a host process** (Linux `minissdpd`, Windows `SSDPSRV`, another UPnP daemon). v1's bind-failure rule sets the adapter to `StateError` and surfaces the error; this is the right behavior, but operators may need a troubleshooting note in `README.md` since the failure mode is silent from the controller side (the renderer simply does not appear).

## Decision

Proceed with a native Go DLNA MediaRenderer adapter implemented as a peer of Plex, Jellyfin, and URL. Do not use a renderer sidecar. Keep v1 focused on HTTP-delivered media, core playback controls, descriptors, SSDP, SOAP, and eventing.
