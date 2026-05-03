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
  routes.go               # UIRoutes or HTTP mount helper
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

### Device Description

The root device should advertise:

- `deviceType`: `urn:schemas-upnp-org:device:MediaRenderer:1` for broad compatibility.
- Stable `UDN`: `uuid:<bridge device UUID>`.
- `friendlyName`: `adapters.dlna.device_name`, defaulting to `MiSTer`.
- Manufacturer/model fields identifying MiSTer_GroovyRelay.
- One service each for `AVTransport:1`, `ConnectionManager:1`, and `RenderingControl:1`.

Use version 1 service types in the exposed descriptors even if the service descriptions borrow ideas from newer documents. Older control points tend to target `:1`, and the first implementation does not need level-2/3 additions.

### SSDP

The adapter should bind UDP multicast for SSDP on `239.255.255.250:1900`.

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

Interface selection should initially mirror existing Plex GDM behavior: use the bridge's advertised host IP and default network behavior. A future multi-NIC improvement can add `adapters.dlna.interface` if needed.

## Control Flow

### SetAVTransportURI

`SetAVTransportURI(InstanceID=0, CurrentURI, CurrentURIMetaData)` validates and stores the URI plus metadata. It does not have to start playback according to the strict model. For compatibility, the adapter should support `autoplay_on_set_uri` because some control points set a URI and never send `Play`.

Validation rules:

- `InstanceID` must be `0`.
- URI must parse as an absolute URL.
- First version accepts only `http` and `https`.
- Resolve hostname targets before playback. Reject loopback, link-local, multicast, and unspecified addresses. Reject public addresses unless `allow_public_source_urls = true`. Private RFC1918/ULA LAN targets are accepted by default.
- Metadata may be empty. If present, parse enough DIDL-Lite to capture title, duration, class, and protocolInfo when available.

### Play

`Play(InstanceID=0, Speed=1)` starts or resumes playback:

- If state is `PAUSED_PLAYBACK`, call `core.Play()`.
- If a URI is loaded and no active DLNA session is running, build `core.SessionRequest`:
  - `StreamURL`: stored URI
  - `Capabilities`: `{CanSeek: true, CanPause: true}`
  - `AdapterRef`: stable per DLNA URI/session, e.g. `dlna:<random-id>`
  - `DirectPlay`: `true` for HTTP/HTTPS URLs, since FFmpeg can seek with `-ss`
  - `OnStop`: adapter callback to update transport state and fire `LastChange`
- If already playing, return success and refresh state.

### Pause, Stop, Seek

- `Pause` calls `core.Pause()` and sets UPnP state to `PAUSED_PLAYBACK`.
- `Stop` calls `core.Stop()` and sets state to `STOPPED`.
- `Seek` supports `REL_TIME` first. Convert `HH:MM:SS` or `HH:MM:SS.FFF` to milliseconds and call `core.SeekTo`.
- Unsupported seek units return a UPnP action error instead of guessing.

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

## ConnectionManager

First version should return:

- `SourceProtocolInfo`: empty
- `SinkProtocolInfo`: a comma-separated list covering common HTTP inputs:
  - `http-get:*:video/mp4:*`
  - `http-get:*:video/x-matroska:*`
  - `http-get:*:video/mpeg:*`
  - `http-get:*:video/vnd.dlna.mpeg-tts:*`
  - `http-get:*:application/vnd.apple.mpegurl:*`
  - `http-get:*:application/x-mpegURL:*`
  - `http-get:*:audio/mpeg:*`
  - `http-get:*:audio/mp4:*`
  - `http-get:*:audio/flac:*`
  - `http-get:*:audio/x-flac:*`

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

## Eventing

Implement UPnP GENa-style event subscriptions:

- Accept `CALLBACK`, `NT: upnp:event`, and `TIMEOUT` on `SUBSCRIBE`.
- Generate a `SID: uuid:<subscription-id>`.
- Support renewal when `SID` is supplied without `CALLBACK`.
- Support `UNSUBSCRIBE`.
- Send initial `NOTIFY` immediately after subscribe.
- Send subsequent `NOTIFY` when evented state changes.

Event payloads:

- AVTransport: event `LastChange` with `TransportState`, `TransportStatus`, `CurrentTrackURI`, `CurrentTrackDuration`, `RelativeTimePosition`, and `CurrentTransportActions`.
- RenderingControl: event `LastChange` with `Volume` and `Mute`.
- ConnectionManager: event `SourceProtocolInfo`, `SinkProtocolInfo`, and `CurrentConnectionIDs` if the chosen SCPD marks them evented.

Network I/O to callback URLs must be bounded by short timeouts and done outside adapter locks. Failed notifications should eventually prune the subscription.

## Security

DLNA renderers are unauthenticated by design. This is acceptable only under the same LAN-trusted assumption as the current settings UI and Plex/Jellyfin control surfaces.

Specific risks:

- SSRF: any LAN client can ask the bridge to fetch a URL. There are public examples of MediaRenderer SSRF issues in TVs via unauthenticated `SetAVTransportURI`.
- LAN nuisance control: any client can interrupt playback.
- Event callback abuse: a malicious control point can subscribe with callback URLs.

First-version mitigations:

- Adapter disabled by default.
- Explicit README warning that DLNA exposes unauthenticated LAN control.
- Restrict accepted control requests to same-LAN/private source addresses by default.
- Reject `file://`, `ftp://`, `rtsp://`, loopback, link-local, and non-HTTP schemes in v1.
- Bound all outbound event callbacks with a short timeout.
- Cap subscription count and expire idle subscriptions.
- Redact query credentials in logs using the existing core-style redaction approach, expanded if needed for DLNA tokens.

## Config

Add:

```toml
[adapters.dlna]
enabled = false
device_name = "MiSTer"
autoplay_on_set_uri = false
allow_public_source_urls = false
```

Field meanings:

- `enabled`: hot-swappable adapter enable/disable.
- `device_name`: shown in DLNA control points; `ScopeRestartBridge` because it affects SSDP identity and descriptor output mounted at process startup.
- `autoplay_on_set_uri`: compatibility mode for controllers that do not send `Play`.
- `allow_public_source_urls`: default false. When false, first version should accept media URLs that resolve to private/LAN addresses and reject public internet targets. This is deliberately stricter than many TVs because the bridge runs on a general-purpose host.

Potential follow-up fields:

- `interface`
- `advertise_interval_seconds`
- `accepted_mime_types`
- `volume_controls_audio_gain`

## Testing Strategy

### Unit Tests

- SSDP response formatting for every supported search target.
- Device descriptor XML contains required device/service elements.
- SCPD XML contains action and state variable definitions expected by common control points.
- SOAP parser handles namespaces, empty metadata, missing arguments, and invalid actions.
- SOAP responses and SOAP faults match UPnP shape.
- AVTransport action handlers update state and call the fake manager correctly.
- `Seek` time parser accepts `HH:MM:SS` and `HH:MM:SS.FFF`.
- LastChange XML escapes URI/metadata safely.
- Event subscription add/renew/remove/expire behavior.
- SSRF/source URL validation.

### Integration Tests

- Start adapter on localhost with fake manager and issue SOAP requests over HTTP.
- Simulate M-SEARCH over UDP and verify unicast SSDP response.
- Subscribe a local HTTP callback server and assert initial/subsequent NOTIFY payloads.
- Run a real `core.Manager` + fake MiSTer path with an HTTP-served MP4, matching the URL adapter integration style.

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
- HTTP descriptors.
- SSDP alive/byebye/M-SEARCH.
- Minimal SOAP dispatch with `GetProtocolInfo` and query actions.
- Unit tests prove discoverability and descriptor correctness.

### Phase 2: Playback

- `SetAVTransportURI`, `Play`, `Stop`.
- `core.StartSession` integration.
- OnStop state updates.
- Manual smoke with one controller and one direct MP4.

### Phase 3: Controls and State

- `Pause`, `Seek`, `GetPositionInfo`, `GetCurrentTransportActions`.
- Metadata/duration mapping.
- RenderingControl virtual volume/mute.
- Integration tests with fake manager.

### Phase 4: Eventing and Compatibility

- SUBSCRIBE/UNSUBSCRIBE/NOTIFY.
- LastChange moderation.
- Compatibility matrix.
- Add `autoplay_on_set_uri` if tests show it is needed.

### Phase 5: Hardening

- Source URL restrictions.
- Subscription caps and cleanup.
- README documentation and troubleshooting.
- Broaden MIME/protocolInfo list based on real controllers.

## Risks and Open Questions

- Some control points are strict about SCPD completeness. We should expose a conservative v1 service surface rather than a large v3 surface we do not fully implement.
- Some controllers require eventing before showing transport controls. Skipping eventing would make the first version look deceptively simpler but would likely create compatibility churn.
- Windows compatibility may require vendor-specific descriptor tags or exact `DLNA.ORG_PN` protocolInfo values. Defer until manual testing proves the need.
- Public internet URLs are useful but increase SSRF risk. The safer first version defaults to private/LAN media URLs only and exposes a deliberate opt-in for public URLs.
- UPnP and the settings UI share one HTTP listener. Route mounting must keep DLNA control paths outside UI CSRF middleware without weakening `/ui/*`.

## Decision

Proceed with a native Go DLNA MediaRenderer adapter implemented as a peer of Plex, Jellyfin, and URL. Do not use a renderer sidecar. Keep v1 focused on HTTP-delivered media, core playback controls, descriptors, SSDP, SOAP, and eventing.
