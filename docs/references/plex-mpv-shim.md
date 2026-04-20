# plex-mpv-shim

## Metadata
- URL: https://github.com/iwalton3/plex-mpv-shim (branch `master`)
- License: MIT
- Language: Python 3 (stdlib `http.server`, `threading`, `requests`, `python-mpv`)
- Activity: 406 stars, last push 2025-10-20, last update 2026-04-14 (active)

## Purpose
A long-lived, unofficial Plex cast target that turns MPV into a "remote-controllable" player. The Plex mobile/web apps see it on the LAN (via GDM) and push media at it over the Plex Companion HTTP protocol; plex-mpv-shim translates each Companion request into MPV commands and streams a timeline back.

## Relevance to MiSTer_GroovyRelay
This is the closest architectural analog to what our control plane must do: accept Companion HTTP, resolve a PMS media URL, start a backend player, and keep Plex's UI synchronized via the timeline model. We copy the HTTP surface, the GDM announcement, the capability string, and the timeline/subscribe state machine almost verbatim; only the backend differs (FFmpeg -> Groovy UDP instead of MPV).

## Device Capability Profile
plex-mpv-shim does **not** ship a local XML profile. It advertises itself by *name* and lets PMS apply a server-side profile by that name. The relevant settings default (`plex_mpv_shim/conf.py`):

```
"client_profile": "Plex Home Theater"
```

That string is attached to every PMS request as `X-Plex-Client-Profile-Name` in `utils.py::get_plex_url`, along with:

```
X-Plex-Version:              2.0
X-Plex-Client-Identifier:    <uuid4, persisted>
X-Plex-Provides:             player
X-Plex-Device-Name:          <hostname>
X-Plex-Model:                RaspberryPI
X-Plex-Device:               RaspberryPI
X-Plex-Product:              Plex MPV Shim
X-Plex-Platform:             Plex Home Theater
X-Plex-Client-Profile-Name:  Plex Home Theater
```

Per-request *overrides* layered on top when a decision/start URL is built (`media.py::get_formats`):

```
X-Plex-Client-Capabilities:
  protocols=http-video,http-live-streaming,http-mp4-streaming,
  http-mp4-video,http-mp4-video-720p,http-streaming-video,
  http-streaming-video-720p;
  videoDecoders=mpeg4,h264{profile:high&resolution:1080&level:51};
  audioDecoders=mp3,aac{channels:8}
  [+ ac3{...} / dts{...} when passthrough toggles are on]

X-Plex-Client-Profile-Extra:
  add-transcode-target-audio-codec(type=videoProfile&context=streaming&protocol=hls&audioCodec=ac3|eac3|dca)
```

Key takeaway for us: claiming "Plex Home Theater" + overriding capabilities per-decision is how mpv-shim tunes PMS behavior without maintaining an XML. For MiSTer_GroovyRelay v1 we will instead claim a LOW profile (H.264 480p progressive, level 3.0, stereo AAC, no passthrough) to force transcode; the override mechanism above is the precise lever.

## Plex Companion HTTP Endpoints
All handled by `client.py::HttpHandler` (threaded `HTTPServer` on `http_port=3000`). Each handler parses query args from `self.path`, runs side effects, then returns an `<Response code="200" status="OK"/>` XML envelope via `send_end`.

| Path | Method | What it does |
|---|---|---|
| `/resources` | `resources` | Returns `<MediaContainer><Player .../></MediaContainer>` advertising `protocolCapabilities=timeline,playback,navigation[,playqueues]`, `deviceClass=pc`, `machineIdentifier=client_uuid`, product/version. |
| `/player/playback/playMedia`, `/player/application/playMedia` | `playMedia` | Parses `address`, `protocol`, `port`, `key`, `offset` (ms -> s), `containerKey`, `type`, `token`. Builds `url = protocol://address:port + key`. Calls `upd_token(address, token)`. Instantiates `Media(url, ...)` -> `get_media_item(0)` -> `playerManager.play(item, offset)`. Finally `timelineManager.SendTimelineToSubscribers()`. |
| `/player/playback/pause`, `.../play` | `pausePlay` | `playerManager.toggle_pause()` + broadcast. |
| `/player/playback/stop` | `stop` | `playerManager.stop()` + broadcast. Terminates transcode session server-side. |
| `/player/playback/seekTo` | `seekTo` | `offset = int(arg)/1000`; `playerManager.seek(offset)`. |
| `/player/playback/skipTo` | `skipTo` | `playerManager.skip_to(key)` (play-queue jump). |
| `/player/playback/skipNext` / `skipPrevious` | - | play-queue nav. |
| `/player/playback/setParameters` | `set` | Volume + subtitle visuals (`subtitleSize/Position/Color`), `autoPlay`. |
| `/player/playback/setStreams` | `setStreams` | `audioStreamID`, `subtitleStreamID` -> `playerManager.set_streams(audio_uid, sub_uid)`. |
| `/player/timeline/subscribe` | `subscribe` | Register `RemoteSubscriber` and immediately push a timeline. |
| `/player/timeline/unsubscribe` | `unsubscribe` | Drop subscriber. |
| `/player/timeline/poll` | `poll` | Long-poll (`?wait=1`, up to 30 s) or snapshot of current timeline XML. |
| `/player/application/setText`, `sendString`, `sendVirtualKey`, `sendKey` | - | On-screen-keyboard support. |
| `/player/navigation/*` | `navigation` | 7-way D-pad to menu (`down/up/ok/left/right/home/back`). |
| `/player/playback/refreshPlayQueue` | - | Re-fetch current `/playQueues` container. |
| `/player/mirror/details` | `mirror` | Resets the idle timer ("we're still here"). |

CORS preflight (`OPTIONS`) is handled with `Access-Control-Allow-Origin: *`. `X-Plex-Client-Identifier` is echoed as both a header and an `Access-Control-Expose-Headers` entry.

## Timeline Subscribe Mechanics
- **Subscription list** lives in `subscribers.py::RemoteSubscriberManager` keyed by `X-Plex-Client-Identifier` UUID. Each `RemoteSubscriber` holds `url = f"{protocol}://{ipaddress}:{port}"`, `commandID`, `name`, and a `Timer` updated on every request. `shouldRemove()` prunes subscribers idle >90 s (`SUBSCRIBER_REMOVE_INTERVAL`).
- **Push**: `TimelineManager.run()` ticks every 1 s (`trigger.wait(1)`). If the player is active (or `force_next` from a state change) it calls `SendTimelineToSubscribers()`, which fans out via a 5-worker `multiprocessing.dummy.Pool`. Each subscriber gets an HTTP POST to `{subscriber.url}/:/timeline` with `Content-Type: application/x-www-form-urlencoded`, `X-Plex-Client-Identifier` of *our* UUID, and a 5 s timeout. The same timeline is simultaneously POSTed to the PMS at `{server_url}/:/timeline` (guarded by a non-blocking `Lock`, but always sent when `state=stopped`).
- **Poll path**: `/player/timeline/poll?wait=1` parks on a per-subscriber `threading.Event` (`get_poll_evt`) for up to 30 s; any state change calls `set_poll_evt` to wake all pollers. Without `wait=1`, it returns immediately.
- **XML format**:
  ```xml
  <MediaContainer commandID="N" location="fullScreenVideo">
    <Timeline state="playing|paused|stopped|buffering"
              type="video" time="<ms>" duration="<ms>"
              ratingKey="..." key="..." containerKey="..." guid="..."
              address="..." port="..." protocol="..."
              machineIdentifier="..." seekRange="0-<ms>"
              audioStreamID="..." subtitleStreamID="..."
              volume="0-100" autoPlay="0|1"
              controllable="playPause,stop,stepBack,stepForward,seekTo,
                            skipTo,autoPlay,subtitleStream,audioStream,
                            skipNext,skipPrevious,volume,..."/>
  </MediaContainer>
  ```
  `location=""` is sent when idle to suppress the remote's nav-menu popup.
- **Cadence**: effectively 1 Hz while playing (loop tick) + immediate re-broadcast on any handler side effect (pause, seek, setStreams, stop).

## Seek: Direct-Play vs Transcode
The decision is made once, at `play()`, by `media.py::is_transcode_suggested` calling `/video/:/transcode/universal/decision`. After that, `media_item.is_transcode` is a boolean the rest of the code branches on.

- **Direct play** (`is_transcode = False`): `seek()` just sets `self._player.playback_time = offset` on MPV. No PMS round-trip, HLS segment boundaries don't apply.
- **Transcode** (`is_transcode = True`): the MPV source URL is an HLS manifest from `/video/:/transcode/universal/start.m3u8?offset=...`. A plain MPV seek inside the manifest works for small jumps (`fastSeek=1`, `copyts=1`). But *stream changes* during transcode are handled by `restart_playback()` — tear the transcode session down (`/video/:/transcode/universal/stop`), re-call `get_playback_url()` with the new offset, and `_player.play(new_url)`. In other words: seek itself is the same primitive, but `set_streams` on a transcoded session triggers a full session restart, not an MPV track switch.

## Subtitle Handling
- **Embedded text**: `media.py::map_streams` builds `subtitle_uid[index] = streamID` and the inverse `subtitle_seq`. On play, `get_subtitle_idx()` finds the PMS-selected track (`@selected='1' and @key is None`) and MPV selects it by index. When PMS sends `setStreams?subtitleStreamID=X`, we map via `subtitle_seq[X]` and set `_player.sub`.
- **External sidecar (key != None)**: `get_external_sub_id()` returns the stream id; `load_external_sub(id)` calls `/library/streams/{id}` (tokenized) and `mpv.sub_add(url)`. The assigned MPV sub index is remembered in `external_subtitles[id]` so subsequent toggles are free.
- **Burn-in (transcode only)**: decision and start URLs set `subtitles=burn` + `subtitleSize=...`. Changing subtitle track during a transcoded session therefore requires `restart_playback()` — the burn-in is baked into the transcoded video.
- **"None"**: subtitleStreamID == `'0'` maps to `_player.sub = 'no'`.
- **Reporting back**: `get_track_ids()` derives `(audioStreamID, subtitleStreamID)` from MPV state when direct-playing, or from the transcode decision (`get_transcode_streams()` reads `Stream[@selected='1']` off the part node) when transcoding.

## plex.tv / Account Linking
There is **no plex.tv OAuth flow**. Authentication is purely per-LAN-session:
1. GDM broadcast (`gdm.py`) registers with local PMS instances on multicast `239.0.0.250:32413`, advertising `Resource-Identifier=client_uuid`, `Port`, `Protocol-Capabilities`, etc. This makes us appear in the Plex app's cast picker.
2. When a Plex app invokes `playMedia`, it supplies `?token=...` in the URL. `client.py::playMedia` calls `upd_token(address, token)`, storing `plex_eph_tokens[domain] = token` in-process.
3. Every subsequent PMS request goes through `utils.py::get_plex_url`, which appends `X-Plex-Token` from that dict.
4. `client_uuid` is generated on first run (`uuid.uuid4()`) and persisted in `conf.json`. Tokens are **ephemeral** — never written to disk, lost on restart, refreshed on the next playMedia.

## Session Lifecycle and Preemption
- A second `playMedia` arriving mid-playback is *not* rejected. `playerManager.play()` calls `_play_media()` under `@synchronous('_lock')`, which issues `self._player.play(url)` (MPV replaces its source) and resets state. The previous transcode session is torn down in `get_playback_url()` via `terminate_transcode()` *before* the new decision call.
- `stop` calls `terminate_transcode()` (`/video/:/transcode/universal/stop?session=...`), issues MPV `command("stop")`, clears `_media_item`, and broadcasts a `state=stopped` timeline.
- On idle (no media >60 s), `TimelineManager.run()` optionally fires `settings.idle_cmd` / `stop_idle`. The last-known media is remembered in `last_media_item` so timelines after stop still carry a `ratingKey` until a new session begins.

## Patterns Worth Adopting
1. **Handler dispatch table** (`client.py` tuple of `(paths, method_name)`) keeps routing compile-time-obvious and matches our Go mux cleanly.
2. **`@synchronous('_lock')` around every player mutation.** Port as a `sync.Mutex` guarding a `session` struct; every RPC path acquires it.
3. **90 s subscriber TTL + per-subscriber commandID.** Required for Plex UI consistency; cheap to implement.
4. **Per-subscriber `Event` for long-poll wakeup.** In Go: a `chan struct{}` per subscriber, closed and re-created on each state change.
5. **Non-blocking send to PMS `/:/timeline`** (`sending_to_ps.acquire(False)`) except when `state=stopped`, which is always sent. Prevents PMS latency from backing up the UI thread.
6. **Single session UUID per PMS domain** (`get_transcode_session`) — reused across decision + start + stop calls. We need the same to keep PMS's transcoder from spawning duplicates.
7. **Ephemeral token store keyed by host**, populated from playMedia. No disk, no refresh, no OAuth.
8. **`fastSeek=1`, `copyts=1`, `protocol=hls`, `directPlay=0`, `directStream=1`** on the transcode-start URL — these are the flags that give sub-second seek on a transcoded session.
9. **Restart-playback-on-setStreams when transcoding.** Encode this as a Boolean branch in `set_streams`.
10. **GDM announce loop** on `239.0.0.250:32413` with the same Resource-Identifier header set — cheap LAN discovery without mDNS.

## Notes for Our Go Implementation
Essentially everything above lives in the **control plane**: HTTP mux, subscriber registry, timeline builder, GDM responder, decision/start URL construction, token cache, session UUIDs. The only places behavior leaks into the **data plane** (our FFmpeg + Groovy UDP pipeline):

- **Subtitle burn-in on transcode**: PMS already burns subtitles into the HLS output when we pass `subtitles=burn`. If we instead do our own burn-in (e.g. ASS overlay via FFmpeg's `subtitles=` or `overlay` filter because PMS gives us clean video), the filter graph is a data-plane concern triggered by a control-plane `setStreams` call. Treat subtitle-track id as a data-plane restart input.
- **Direct-play vs transcode**: determines whether data plane receives an HLS `.m3u8` (segmented TS stream from PMS) or a raw part URL. FFmpeg input args differ; both are reached via the *same* control message but the data-plane ingest module must switch on `is_transcode`.
- **Seek semantics**: direct-play seek is a data-plane input offset (`-ss` before `-i`, or mid-stream flush); transcode seek on a large jump requires tearing the HLS session and starting a new one via `/video/:/transcode/universal/start.m3u8?offset=...`. Control plane owns the session, data plane owns the demuxer restart.
- **State reporting**: `playback_time` that we stuff into the `<Timeline time=>` attribute must come from the data plane's current PTS (FFmpeg filter/muxer progress), proxied to the control plane over an in-process channel. This is the single most important control<->data signal; design it up front.

Everything else — `/resources`, `playMedia` URL parsing, subscribe/poll, GDM, token cache, profile overrides — is pure control plane and should be portable line-for-line from the Python.
