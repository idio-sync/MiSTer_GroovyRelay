# DLNA MediaRenderer Compatibility Matrix

Phase 4 tracks controller behavior against the native DLNA MediaRenderer path.
A row is complete only when the controller discovers the renderer, subscribes
to events, and exercises playback controls against at least one HTTP media
source.

| Controller | Platform | Renderer Appears | SUBSCRIBE Succeeds | Set Media | Play Starts | Pause/Resume | Stop | Seek | Position/Duration | Autoplay Needed | SetNext Behavior | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| VLC | Desktop | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run |
| BubbleUPnP | Android | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run |
| Kodi | Cross-platform | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run |
| Windows Cast-to-Device | Windows | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run |
| Samsung TV controller | Samsung TV | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run | Pending manual run |

## Media Server Sources

| Source | Platform | HTTP Item | MIME Metadata | Playback Controls | Notes |
| --- | --- | --- | --- | --- | --- |
| Jellyfin DLNA | Jellyfin | Pending manual run | Pending manual run | Pending manual run | Pending manual run |
| Plex DLNA | Plex | Pending manual run | Pending manual run | Pending manual run | Pending manual run |
| Gerbera or MiniDLNA | Linux | Pending manual run | Pending manual run | Pending manual run | Pending manual run |

## Manual Test Script

1. Start the bridge with DLNA enabled and `bridge.host_ip` set to a reachable
   LAN address.
2. Confirm the controller discovers the MiSTer Groovy Relay renderer.
3. Confirm the controller receives HTTP 200 from each `SUBSCRIBE` request.
4. Confirm each event subscription receives an initial `NOTIFY` with `SEQ: 0`.
5. Cast an MP4/H.264/AAC HTTP media item from the controller or media server.
6. Exercise Play, Pause, Resume, Stop, and Seek from the controller.
7. Confirm transport controls stay hidden or disabled until `LastChange` events
   advertise the available actions.
8. Repeat the set-media path with `autoplay_on_set_uri` enabled for controllers
   that set a URI but do not send `Play`.
9. Check bridge logs for failed SOAP actions, unsupported MIME types, or
   controller-specific request quirks.
