# mister_plex

## Metadata
- URL: https://github.com/mrchrisster/mister_plex
- License: not specified in repo
- Primary language: Shell (100%)
- Activity: ~55 commits on `main`; low recent activity (historical / maintenance-mode project)
- Variants: `mister_plex.sh` (CRT), `mister_plex_hdmi.sh`, `mister_plex_groovy.sh`, `mister_plex_playlist.sh`

## Purpose
A set of bash scripts that let a MiSTer FPGA play a single Plex video by SSH-ing in, pasting a Plex "View XML" URL, and letting the script request a low-res transcoded HLS stream from PMS. Playback happens via `mplayer` on the framebuffer (CRT/HDMI variants) or via the custom `mp4_play` helper that loads an `mp4_player.rbf` core (Groovy variant).

## Is It a Cast Target?
**No — it is a puller.** The user manually opens Plex Web, finds a video, clicks Get Info -> View XML, copies the URL (which already contains `X-Plex-Token`), SSHes to the MiSTer, and pastes it into the script. The MiSTer never registers as a Plex Companion device, never publishes an SSDP/GDM advert, never receives `/player/playback/playMedia` calls from a Plex client. There is no control-plane listener at all.

For our project (Plex Companion cast target) this means mister_plex's control-plane story gives us **zero** reusable scaffolding — we have to implement Companion from scratch. Its value is entirely on the **media-plane**: which transcode URL parameters PMS actually honors.

## Relevance to MiSTer_GroovyRelay
Medium-low. Useful specifically for:
1. Confirmed-working Plex transcode URL shape (parameters below).
2. Evidence that PMS will honor arbitrary sub-SD resolutions (320x240, 640x480) when you force `directPlay=0&directStream=0`.
3. A reminder that mplayer-without-SSL forces server reconfiguration — we avoid this entirely by using Go's stdlib HTTPS.

Not useful for: control protocol, session management, subtitles, audio track selection, timeline reporting, or anything resembling a Companion handshake.

## Plex API Usage
Exactly one endpoint is hit across all variants:

```
GET {PMS_BASE}/video/:/transcode/universal/start.m3u8?<params>
```

That's it. No `/library/sections`, no `/status/sessions`, no `/player/timeline`, no `/:/timeline`, no decision endpoint. Authentication is just `X-Plex-Token=<token>` as a query param, lifted verbatim from the user-supplied XML URL via `sed 's/.*X-Plex-Token=\([^&]*\).*/\1/'`. No `myplex.plex.tv` sign-in flow; the token has to already exist.

## Media URL Construction
Full parameter set observed across variants:

| Param | Value | Notes |
|---|---|---|
| `path` | `/library/metadata/{ratingKey}` | URL-encoded metadata path |
| `mediaIndex` | `0` | First media entry |
| `offset` | `0` | Start of file |
| `videoResolution` | `320x240` (CRT) / `640x480` (CRT alt) / `480` (HDMI) | Arbitrary sub-SD accepted |
| `maxVideoBitrate` | `1000` | kbps |
| `directPlay` | `0` | **Force transcode** |
| `directStream` | `0` | **Force full transcode** (not just remux) |
| `copyts` | `1` | Preserve timestamps across segments |
| `stretch` | `1` (HDMI only) | Stretch to target res |
| `X-Plex-Platform` | `Chrome` | Masquerade as web client |
| `X-Plex-Token` | `<token>` | Auth |

Transcode is **always forced** — no direct-play path, no codec-capability negotiation, no `protocols`/`videoCodec`/`audioCodec` hints. PMS picks codecs based on the `Chrome` platform profile. Output is HLS (`.m3u8`) with H.264 + AAC segments.

## FFmpeg Invocation
**No ffmpeg.** Playback is:
- CRT / HDMI: `nice -n -20 env LD_LIBRARY_PATH=${mrsampath} ${mrsampath}/mplayer -fs "$TRANSCODE_URL"` — mplayer pulls the HLS directly, decodes to framebuffer.
- Groovy: `/media/fat/mp4_play/mp4_play "$TRANSCODE_URL" -t 2` — custom MiSTer binary paired with `mp4_player.rbf` core.

mplayer is linked without SSL, so users must set PMS "Secure connections" to **Preferred** (so PMS will answer plain HTTP on LAN). This is the single biggest pain point in the project and one we should deliberately engineer out.

## Patterns Worth Adopting
1. **Force transcode with `directPlay=0&directStream=0`.** For 480i NTSC output we need a fully baked H.264/AAC stream at a known bitrate; don't bother negotiating direct-play.
2. **`X-Plex-Platform=Chrome` as the profile identity.** Gets you a sane, well-tested transcode profile without having to author a custom `profile.xml`.
3. **`copyts=1`.** Keeps HLS segment timestamps monotonic; important for our FFmpeg ingest.
4. **Arbitrary low resolutions are honored.** We can request `videoResolution=720x480` (or even `640x480`) and PMS complies. Worth testing `videoResolution=720x480&videoBitrate=2000` as our baseline.
5. **Token-in-query-string works fine.** No need for `X-Plex-Token` header; both are accepted.

## Limitations / What to Avoid
- **Puller, not cast target.** Users paste URLs over SSH. Useless UX.
- **No SSL.** We must do HTTPS properly in Go.
- **No session tracking.** Plex dashboard shows nothing useful; no pause/resume/scrub from Plex clients.
- **No subtitle, audio-track, or chapter handling.** Plex's `subtitles=burn` / `audioStreamID` / `subtitleStreamID` params are unused. We should support at least burn-in subs and audio-stream selection.
- **No playlist/queue semantics beyond a trivial bash loop in `mister_plex_playlist.sh`.**
- **Hardcoded 1000 kbps cap.** Fine for SD but we should make it configurable.
- **No timeline reporting back to PMS** (`/:/timeline?... state=playing&time=...`). Watched status never updates.
- **Token extraction via regex** on a user-pasted URL — brittle and unnecessary once we implement proper Companion/myplex auth.

## Notes for Our Go Implementation
- **Control plane: greenfield.** Implement Plex Companion (GDM advert on UDP 32412/32413, HTTP server exposing `/resources`, `/player/playback/playMedia`, `/player/timeline/subscribe`, `/player/playback/{play,pause,stop,seekTo}`). mister_plex contributes nothing here.
- **Media plane: copy the URL shape.** Starting template for our transcode request:
  ```
  /video/:/transcode/universal/start.m3u8
    ?path=/library/metadata/{ratingKey}
    &mediaIndex=0&offset={seekMs}
    &videoResolution=720x480&maxVideoBitrate=2500
    &directPlay=0&directStream=0&copyts=1
    &protocol=hls&X-Plex-Platform=Chrome
    &X-Plex-Token={token}
  ```
  Add `subtitles=burn&subtitleStreamID={id}` and `audioStreamID={id}` as needed — mister_plex skipped these but PMS supports them.
- **Use `/video/:/transcode/universal/decision` first** (mister_plex doesn't) to let PMS tell us whether it's willing to transcode before we commit.
- **Pipe HLS into FFmpeg** (`ffmpeg -i <m3u8> ... -f rawvideo ...`) to convert to 480i NTSC + whatever Groovy UDP expects. mister_plex doesn't do this step — it relies on mplayer's framebuffer output, which is not applicable to our Groovy pipeline.
- **Report timelines** to PMS so watched state and "Continue Watching" work. That's a differentiator over mister_plex worth having on day one.
