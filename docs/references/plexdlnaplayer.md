# plexdlnaplayer

## Metadata
- URL: https://github.com/songchenwen/plexdlnaplayer
- License: GPL-3.0 (matches ours)
- Language: Python 3.9 (FastAPI + asyncio + aiohttp + uvicorn)
- Stars/Forks: 43 / 15; 20 open issues
- Created 2021-09-06; last push 2023-04-28 (effectively dormant)

## Purpose
Bridges Plex Companion control to DLNA/UPnP renderers. Plex clients (and Plexamp via plex.tv link) see the DLNA speakers as native Plex players; the daemon proxies play/pause/seek/etc. into UPnP AVTransport SOAP calls. Same architectural shape as MiSTer_GroovyRelay: Plex Companion in, a different downstream protocol out.

## Relevance to MiSTer_GroovyRelay
Direct one-to-one for the control plane. The HTTP endpoints in `plex/plexserver.py`, the GDM advertiser in `plex/gdm.py`, the pin/link flow in `plex/pin_login.py`, and the subscriber/poller in `plex/subscribe.py` are exactly the surface we need. Swap DLNA AVTransport SOAP for Groovy UDP and FFmpeg lifecycle. The `PlexDlnaAdapter` in `plex/adapters.py` is the per-device state machine; our Go equivalent becomes `PlexGroovyAdapter` driving one MiSTer.

## Key Files and Code Paths
- `main.py` — trivial entry; just calls `start_plex_server()`.
- `plex/plexserver.py` — FastAPI app, all Plex Companion endpoints, discovery wiring, plex.tv link UI (`/` GET/POST), DLNA UPnP event callback (`/dlna/callback/{uuid}`), startup/shutdown hooks. This is the single most important file.
- `plex/gdm.py` — Plex GDM multicast advertiser on `239.0.0.250:32413`, bound locally to UDP 32412. Sends `HELLO * HTTP/1.0` on connect and replies to `M-SEARCH * HTTP/1.*` with `HTTP/1.0 200 OK` plus a headers block.
- `plex/subscribe.py` — `SubscribeManager` + `Subscriber`. Holds subscriber list per-device, XML templates (`TIMELINE_STOPPED`, `TIMELINE_PLAYING`, `TIMELINE_DISCONNECTED`), and the notify loop. Posts to `{protocol}://{host}:{port}/:/timeline` on each subscriber.
- `plex/pin_login.py` — `POST https://plex.tv/api/v2/pins` to get code+id, then `GET /api/v2/pins/{id}` to poll for `authToken`. Twelve lines.
- `plex/adapters.py` — `PlexLib` (remembers the source PMS connection params from the playMedia query), `DlnaState` (background thread polling device), `PlexDlnaAdapter` (per-device state machine, auto-next detection, `update_plex_tv_connection` loop pinging `PUT https://plex.tv/devices/{uuid}` every 60s).
- `plex/play_queue.py` — wraps Plex playqueue API; our Go port will need a slimmer version (video containerKey handling).
- `settings/__init__.py` — pydantic BaseSettings; persists a `data.json` keyed by device UUID with `token` and `alias` fields under `/config`.
- `dlna/discover.py`, `dlna/dlna_device.py` — DLNA SSDP discovery and SOAP client; not directly applicable, but the discovery-callback pattern (`on_new_dlna_device`) is worth mirroring for MiSTer discovery/heartbeat.
- `Dockerfile` — `python:3` base, exposes `1910/udp`, `32412/udp`, `$HTTP_PORT`; `VOLUME /config`. README mandates `--network host` (required for GDM multicast).
- `templates/bind.html` — minimal Jinja UI for the pairing page.

## Protocol / API Insights

Plex Companion endpoints implemented (`plex/plexserver.py`). All take `commandID` (client-side monotonic int) plus `X-Plex-Target-Client-Identifier` / `X-Plex-Client-Identifier` headers and return a small XML envelope `<Response code="200" status="OK"/>` via `build_response()`. `sub_man.update_command_id()` is called on every endpoint.

- `GET /player/playback/playMedia` — takes `containerKey`, `key`, `offset`, `paused`, `type`. Calls `adapter.play_media(...)`. The `query_params` (protocol/address/port/token/machineIdentifier) are how the player learns where the source PMS lives; stored in `PlexLib.update()`.
- `GET /player/playback/play`, `pause`, `stop`, `skipNext`, `skipPrevious`, `seekTo`, `skipTo`, `setParameters` (shuffle/repeat/volume), `refreshPlayQueue`.
- `GET /player/timeline/subscribe` — registers a subscriber at `{protocol}://{request.client.host}:{port}/:/timeline`; protocol defaults to `http`.
- `GET /player/timeline/unsubscribe`.
- `GET /player/timeline/poll?wait=0|1` — long-poll fallback for Plexamp. With `wait=1` the handler awaits `adapter.wait_for_event(plex_notify_interval*20, interesting_fields=['state','volume','current_uri','elapsed_jump'])` before returning the XML. Uses custom `timeline_poll_headers` with `X-Plex-Protocol: 1.0`.
- `GET /resources` — returns `<MediaContainer><Player .../></MediaContainer>` with `protocolCapabilities="timeline,playback,playqueues"` and `deviceClass="stb"`.
- `GET /player/mirror/details` — stubbed, returns empty.

GDM advertising (`plex/gdm.py`): binds UDP `32412` (joins multicast `239.0.0.250`), sends to `239.0.0.250:32413`. The payload:

```
Name: <device name>
Port: <HTTP_PORT>
Content-Type: plex/media-player
Product: <device model>
Protocol: plex
Protocol-Version: 1
Protocol-Capabilities: timeline,playback,playqueues
Version: 1
Resource-Identifier: <uuid>
Device-Class: stb
```

Note: README mentions ports 32410/32412/32414 but the code only uses 32412 (listen) / 32413 (send). No SSDP or 32410/32414 handling.

Plex.tv PIN flow (`plex/pin_login.py`): `POST https://plex.tv/api/v2/pins` returns `pin.@code` + `pin.@id`; operator enters the code at plex.tv/link; daemon polls `GET /api/v2/pins/{id}` until `pin.@authToken` is present, persists it keyed by device UUID. Then `PUT https://plex.tv/devices/{uuid}` with `Connection[][uri]=http://HOST_IP:HTTP_PORT` and `X-Plex-Token` registers the device's address (refreshed every 60s via `_update_plex_tv_connection_loop`).

Timeline XML emitted to subscribers and poll clients uses three `<Timeline>` rows (music/video/photo), only the active type has `controllable=...` and parameters like `state`, `time`, `duration`, `ratingKey`, `key`, `playQueueItemID`, `containerKey`, `shuffle`, `repeat`, `volume`, `mute`, plus the PMS location (`protocol/address/port/machineIdentifier/token`).

## Patterns Worth Adopting
- Per-device adapter registry (`adapters = {uuid: PlexDlnaAdapter}` in `plex/adapters.py`). In Go, a `map[string]*Adapter` guarded by a mutex, or a `sync.Map`. One goroutine family per device.
- `PlexLib` as a mutable struct refreshed from each playMedia query — it is the "which PMS is sourcing this session" handle; a Go `type PlexSource struct {Protocol,Address,Port,Token,MachineID string}` works.
- `SubscribeManager` as one goroutine pumping XML to a slice of subscribers at `plex_notify_interval` (0.5s default), woken early by `wait_for_event`. In Go: a channel-driven broadcaster with per-subscriber send goroutines and a 1s POST timeout.
- `guess_host_ip()` opportunistically learning the advertised host IP from the first incoming request URL — cheap and effective for Docker host networking.
- Persisting just `{uuid: {token, alias}}` JSON under `/config`. A single small `data.json` is plenty. Avoid a DB.
- `build_response()` centralizes Plex response headers; define one Go helper.
- `sub_man.update_command_id()` on every endpoint keeps the server/client command IDs in sync — easy to forget.

## Pitfalls / Known Issues
- 20 open issues, many device-specific (Marantz M-CR611 `GetPositionInfo` error, Oppo UDP-203, TechniSat Digiradio). Relevant for us: the auto-next heuristics in `check_auto_next()` are fragile and depend on the renderer reporting elapsed/duration cleanly; we will face the same class of bug tracking MiSTer playback progress. Plan a clean state source.
- Issue #21 "Can't skip tracks or play new ones" and #13 "lost connection to UPnP renderer | device uuid not found" hint at sub_man/adapter lifecycle races when devices flap.
- `plex_notify_interval = 0.5` is aggressive; `wait=1` poll waits up to `interval*20 = 10s`.
- `guess_host_ip` skips `127.0.0.*` but will happily pick the first external hostname it sees — if Plex clients reach the daemon via different networks, it picks one and sticks. Consider making `HOST_IP` required in our config.
- Host networking is mandatory (multicast). Same constraint applies to us; document it.
- `from pydantic import BaseSettings` is pydantic v1; modern replacement is `pydantic-settings`. Not our problem but signals the code is stale.
- Threading in `DlnaState` (dedicated `Thread` per device running its own asyncio loop, cross-thread state via `__setattr__`/`__getattr__`) is clever but creates confusing GIL/thread-safety hazards. Our Go port should collapse this to a single goroutine per device; `DlnaState.changing_attrs` maps naturally to atomic fields or a state struct behind a `sync.Mutex`.

## Notes for Our Go Implementation

Control plane (1:1 port of what's here):
- `internal/plex/http.go` — Plex Companion HTTP server. Use `net/http` + `chi` (or stdlib). Port every endpoint in `plex/plexserver.py`; replace `type_ == "music"` gating with `type_ == "video"` gating (we are a video player).
- `internal/plex/gdm.go` — GDM advertiser. One goroutine: join `239.0.0.250`, bind `udp:0.0.0.0:32412`, send initial HELLO, reply to `M-SEARCH`. Headers block identical except `Content-Type: plex/media-player` and our own `Product`/`Device-Class` (try `stb`).
- `internal/plex/plextv.go` — PIN flow + `PUT /devices/{uuid}` refresher (60s ticker). Persist tokens in `config/data.json`.
- `internal/plex/subscribe.go` — SubscribeManager with `map[uuid][]*Subscriber`, `notify()` broadcaster at 500ms tick, `msg_for_device()` producing timeline XML from adapter state. Use `context.Context` for shutdown. HTTP client with 1s timeout for POSTs to `/:/timeline`.
- `internal/session/adapter.go` — `Adapter` per MiSTer target: current `PlexSource`, PlayQueue, `State` (stopped/playing/paused/transitioning), token, auto-next watchdog. Single goroutine per adapter; commands land via channels.
- `internal/session/state.go` — the Go version of `DlnaState`, but single-goroutine.
- `internal/config` — pydantic-settings equivalent; env first, then `config/data.json` for per-device state.

Data plane (new, no DLNA analog):
- `internal/groovy/udp.go` — 5 Groovy commands on UDP 32100. Driven by adapter; isolated from HTTP goroutines.
- `internal/ffmpeg/pipeline.go` — FFmpeg process manager (start on playMedia, stop on stop/disconnect, output 480i NTSC by default). Pixel pump feeds Groovy sender.
- `internal/bridge` — glue translating adapter events (play/pause/seek) into ffmpeg + Groovy commands. The DLNA SOAP methods in `dlna/dlna_device.py` (`SetAVTransportURI`, `Play`, `Pause`, `Stop`, `Seek`, `GetPositionInfo`) map to our bridge ops.

Docker: mirror their layout. `--network host` required. Expose `32488/tcp` (HTTP), `32412/udp` (GDM), `32100/udp` (Groovy). Volume mount `/config` for token persistence. Unraid template can follow the same shape as their docker-compose example.
