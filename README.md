# MiSTer_GroovyRelay

<img align="right" width="200" src=".github/screenshots/plex_dash.png">

Note: this is in heavy dev and still has some bugs. Video gets a bit choppy sometimes and seek/skip/etc. is wonky, depending on the source. 100% usable though.

A video cast-target bridge for the MiSTer. Run it alongside your Plex/Jellyfin Media Server; it advertises itself as a cast target on the LAN, and when you pick it from the client's "Cast" menu it transcodes the output through FFmpeg and streams raw RGB fields + PCM audio over the [Groovy_MiSTer](https://github.com/psakhis/Groovy_MiSTer) UDP protocol into a MiSTer FPGA. The MiSTer drives a 15 kHz analog CRT directly, giving you genuine NTSC/PAL video.

## Video Sources
- Plex
- Jellyfin (initial support, still needs polish)
- URL to video file (Archive.org .mkv, .mp4, etc.)
- YouTube/Vimeo/etc. URL (and other sites supported by yt-dlp)

## Future Plans
- Support for more relay sources:
  - DLNA/UPnP
  - IPTV/M3U playlists
  - Moonlight/Sunshine
- Companion browser extension, cast video from right click
- Music visualizer
- Better webui/dashboard and setup wizard
- Home Assistant integration

## Hardware requirements

- MiSTer FPGA with Analogue I/O board (or equivalent) wired to a 15 kHz-capable CRT (consumer, PVM, arcade, etc.)
- Groovy_MiSTer installed on your MiSTer
- A host on the same LAN running Docker (Linux, Unraid, Synology, a Raspberry Pi 4/5), anything with a few spare CPU cycles and gigabit-class networking.
- A Plex/Jellyfin Media Server reachable from that host (optional)

The bridge itself is stateless and light, just a few hundred MB of RAM and one FFmpeg worker per active cast. Video transcode is primarily handled by the media server, FFmpeg in this container takes 480p form the server to 480i.

## Quick start (Docker)

```bash
# 1. Start the bridge. It auto-creates a default config.toml on first
#    run if the file is missing, then exits. Edit that file to point at
#    your MiSTer.
mkdir -p /opt/mister-groovy-relay
docker run --rm --network=host \
  -v /opt/mister-groovy-relay:/config \
  idiosync000/mister-groovy-relay:latest
$EDITOR /opt/mister-groovy-relay/config.toml   # set bridge.mister.host

# 2. Long-run: detach and let it broadcast.
docker run -d --name mister-groovy-relay --restart unless-stopped \
  --network=host \
  -v /opt/mister-groovy-relay:/config \
  idiosync000/mister-groovy-relay:latest

# 3. Link Plex in your browser. Open http://<host>:32500/, click Plex
#    in the sidebar, click Link Plex Account, enter the 4-character code
#    at plex.tv/link. Done. The token persists in data.json inside
#    data_dir so you only link once.
```

The CLI `--link` flow for headless / automation setups:

```bash
docker run --rm -it --network=host \
  -v /opt/mister-groovy-relay:/config \
  idiosync000/mister-groovy-relay:latest --link
```

`--network=host` is required. The bridge needs a stable source UDP port (the MiSTer keys its session by sender `IP:port`) and it needs to receive the Plex GDM multicast on `239.0.0.250:32414`. Bridged Docker networking does not pass multicast and would rewrite the source port on every restart, neither is workable.

## URL adapter

In addition to Plex, the bridge ships a URL adapter: paste an `http://`
or `https://` URL into the **URL** panel in the settings UI and click
**Play**. The bridge supports two flavors of URL:

- **Direct media URLs** — anything `ffmpeg` can ingest natively over HTTP/HTTPS:
  direct MP4/MKV files, HLS playlists (`.m3u8`), DASH manifests (`.mpd`).
- **Page URLs from sites supported by [yt-dlp](https://github.com/yt-dlp/yt-dlp)** —
  ~1,800 sites including:
  - YouTube videos (incl. Shorts, Live)
  - Twitch VODs and live streams
  - Vimeo, Dailymotion
  - Internet Archive items
  - SoundCloud, Bandcamp tracks
  - ...and most other yt-dlp-supported sites (see "auto-resolves" list in
    the panel for the curated default, can add more upon request)

Sessions are fire-and-forget: they run to EOF or until preempted by
another POST. In-session pause/seek exists in a basic state in the web-ui, but I will polish it in a later pass.

### How routing works

The Mode radio in the URL panel picks the resolution path per-paste:

- **Auto** (default) — hostname matched against the allowlist; if matched,
  yt-dlp resolves the URL to a direct media URL and ffmpeg streams it.
  Else, the URL is passed to ffmpeg directly.
- **yt-dlp** — always run through yt-dlp, regardless of hostname. Use
  for sites you've added to your config but haven't enabled in the
  default allowlist.
- **Direct** — never run through yt-dlp. Use for direct media URLs.

### Cookies for auth-walled content

Age-gated YouTube videos, members-only Twitch VODs, and similar
content require login cookies. The URL panel has a collapsed
**Cookies** section that accepts a Netscape-format `cookies.txt`:

1. Install a browser extension like
   [Get cookies.txt LOCALLY](https://github.com/kairi003/Get-cookies.txt-LOCALLY)
   (Chrome/Edge) or
   [cookies.txt](https://addons.mozilla.org/firefox/addon/cookies-txt/)
   (Firefox).
2. Log in to the site you want to cast from.
3. Click the extension and download the cookies file.
4. Open the URL panel in the bridge, expand **Cookies**, paste the
   file content, click **Save Cookies**.

Cookies are saved to `<bridge.data_dir>/url_cookies.txt` (mode 0600
on POSIX) and survive container restart through the operator's
existing `data_dir` volume mount. The bridge reuses them on every
yt-dlp resolve. Click **Clear** to remove them.

The Cookies section never echoes saved content back into the
textarea, and the form sets `autocomplete="off"` so password
managers don't offer to save them.

### Scripts can also POST a URL programmatically

```bash
# htmx form-style (matches what the panel sends)
curl -X POST \
  -H "Origin: http://<bridge-host>:32500" \
  -d 'url=https://youtu.be/dQw4w9WgXcQ&mode=auto' \
  http://<bridge-host>:32500/ui/adapter/url/play

# JSON
curl -X POST \
  -H "Origin: http://<bridge-host>:32500" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://youtu.be/dQw4w9WgXcQ","mode":"ytdlp"}' \
  http://<bridge-host>:32500/ui/adapter/url/play
```

The `Origin` header is required because the adapter's POST endpoint runs through the bridge's CSRF middleware. Browsers (htmx) set `Sec-Fetch-Site` automatically and pass without ceremony; `curl` and other scripted clients must include `Origin` matching the bridge's host:port. Without it, the request returns 403. The response shape also branches: htmx callers (which set `HX-Request: true`) get an HTML fragment back; everyone else gets JSON.

URL credentials in the form `https://user:pass@host/path` are redacted in the panel display, the success response body, and all log lines. The JSON response echoes the URL verbatim, the API caller already submitted it.

### Yt-dlp self-update

The Docker image bundles a recent yt-dlp binary at build time. On each
container start, the entrypoint runs `yt-dlp -U` (gated by a daily
marker file so hourly restarts don't hammer GitHub). Failed updates
log a warning; the bundled version stays usable.

## Settings UI

Once the bridge is running, point a browser at `http://<host>:32500/` (or whatever `bridge.ui.http_port` is set to). The settings page lets you:

- Flip `interlace_field_order` live — No cast drop, no restart. Flip, look at the CRT, flip back.
- Link your Plex/Jellyfin account in-browser — Click **Link Plex/Jellyfin Account**, enter the 4-character code at plex.tv/link, for Plex, your JF user/pass for JF.
- Enable or disable adapters with a toggle
- See at a glance which adapters are running (green dot), stopped (grey), or erroring (red + last error as tooltip).

Each field is tagged with an apply scope so the UI tells you what it just did: *applied live* (hot-swap), *cast restarted* (next play rebuilds the pipeline), or *restart the container* (for bindable/identity fields where live propagation would produce split-brain state).

## First-time setup walkthrough

1. **Install.** Pull the image (`docker pull idiosync000/mister-groovy-relay:latest`)
   or `go build ./cmd/mister-groovy-relay` for a native binary.

2. **Mount a config dir.** `docker run -v /opt/mister-groovy-relay:/config …`.
   The bridge auto-creates `config.toml` from defaults on first start if the file is missing.

3. **Open the UI.** Browse to `http://<docker-host>:32500/`. You'll land on the Bridge panel with a quick-start banner. Fill in your MiSTer's IP under **Network → MiSTer Host**, click **Save Bridge**. Because `bridge.mister.host` is a restart-bridge field (the UDP sender is bound at startup), the UI tells you to restart the
   container. `docker restart mister-groovy-relay` and reload.

4. **Link Plex/JF.** Link servers as outlined above. The UI transitions to *Linked · RUN* within ~2 seconds and you're good to go.

5. **First cast.** Open Plex/Jellyfin on your phone or browser, pick a video, tap the cast icon, pick your bridge from the target list. Video appears in 1–2 seconds.

## Operational notes

### Multi-NIC hosts

The bridge advertises its own LAN address to Plex (in the `/resources` response and in the plex.tv device registration PUT). By default it auto-detects that
address by asking the kernel which interface it would use to reach 8.8.8.8.

On hosts with multiple network interfaces (typical combinations are LAN + WireGuard, LAN + Docker bridge, or LAN + secondary subnet) the default route may not be the Plex-facing one. Symptoms: the cast target shows up in the Plex picker but "commands never arrive." The controller is trying to reach the bridge on an unreachable NIC.

Fix: set `host_ip` explicitly to the LAN IP the Plex controller can reach. Find it with `ip -4 addr show | grep inet` on the host; the `br0` or `eth0` interface IP on the same subnet as your Plex Media Server is what you want.

```toml
host_ip = "192.168.1.20"
```

Restart the bridge. Check the startup log for the `host_ip not set` warning. If it's gone, your override took effect.

### CPU contention under Docker

The data plane pushes fields at 59.94 Hz regardless of scheduling pressure.
Under heavy CPU contention the FFmpeg decoder can fall behind; the bridge covers with duplicate-field BLITs, which the FPGA rescans, so the symptom is visible motion glitches, not A/V drift. (This is by design, the clock-push architecture trades a graceful fallback against a hard drift bug.)

If you see glitches cap container CPU with
`docker run --cpus=2 ...` so the bridge has dedicated cores that aren't preempted. 2 cores is typically sufficient for a single 480p transcode plus Groovy packet framing.

## Troubleshooting

**"The target didn't show up in Plex's cast menu."**

The bridge uses GDM multicast discovery (port 32414). Confirm: `--network=host` is set; your LAN is not carving off mDNS/multicast between client and server; you linked successfully (`--link`); and the bridge process is running (`docker logs mister-groovy-relay`).

**"No video on the CRT."**

Check the MiSTer is running the Groovy_MiSTer core and is listening on the configured `mister_port` (default 32100). Try `fake-mister` locally to confirm the bridge is sending packets at all: `go run ./cmd/fake-mister -addr :32100` on the same host as the bridge, point `mister_host = "127.0.0.1"` at it, start a cast, and watch for `cmd 2/3/7/...` counts in the fake's summary output. If you see packets there but nothing on the real MiSTer, it's network routing or a Groovy core config issue, not the bridge.

**"Audio drifts over long playback."**

This bridge uses a single FFmpeg process with shared A/V timestamps, so long-term drift is structurally mitigated. Short-term offsets usually indicate host CPU contention.

**"The picture shimmers / fields look wrong."**

Flip `interlace_field_order` between `tff` and `bff`. The "correct" value depends on your MiSTer core + cable path; once you pick the right one it stays right.

**"Plex says the target is offline moments after casting."**

Almost always a `source_port` regression. If the bridge restarted and bound a different ephemeral port, the MiSTer's session key no longer matches. Make sure `source_port` is set to a fixed number in `config.toml` and that nothing else on the host is using it.

## License

[GPL-3.0](https://www.gnu.org/licenses/gpl-3.0.en.html). See the design notes for why: this project stands on the shoulders of several GPL-3 references (plexdlnaplayer, plex-mpv-shim, Groovy_MiSTer) and carries that license forward.
