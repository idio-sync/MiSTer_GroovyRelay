# MiSTer_GroovyRelay

<img align="right" width="200" src=".github/screenshots/plex_dash.png">

A cast-target bridge for the MiSTer. Run it alongside your Plex/Jellyfin Media Server; it advertises itself as a cast target on the LAN, and when you pick it from the client's "Cast" menu it transcodes the output through FFmpeg and streams raw RGB fields + PCM audio over the [Groovy_MiSTer](https://github.com/psakhis/Groovy_MiSTer) UDP protocol into a MiSTer FPGA. The MiSTer drives a 15 kHz analog CRT directly, giving you genuine NTSC/PAL video.

Note: The primary deployment target is a Docker container running on the same host as your media server, but Win/Mac/Linux binaries are provided as well. Running on a different host adds networking overhead and is slightly less stable (but I'm working on it). 

## Cast Sources
- Plex
- Jellyfin
- Plex/Jellyfin music tracks with CRT visualizer output
- YouTube/Vimeo/etc. URL (and other sites supported by yt-dlp)
- URL to video file (Archive.org .mkv, .mp4, etc.)
- URL to M3U/M3U8 playlist ([ws4channels](https://github.com/rice9797/ws4channels), etc.)
- Torrent streaming (uploaded .torrent files and magnet links)
- DLNA / UPnP MediaRenderer
- Built-in catalog of streaming "channels" (Cartoon Rewind, MTV Rewind, Toonami Aftermath; bundled-only, not user IPTV import)

## Future Plans
- Support for more relay sources:
  - Moonlight/Sunshine
- More music visualizer modes
- Better webui/dashboard and setup wizard
- Home Assistant integration

## Hardware requirements

- MiSTer FPGA with Analogue I/O board or direct video adapter wired to a 15 kHz-capable CRT (consumer, PVM, arcade, etc.)
- Groovy_MiSTer installed on your MiSTer, ideally the [44.1khz audio fix release](https://github.com/iequalshane/Groovy_MiSTer/releases/tag/0.8)
- A host on the same LAN running Docker (Linux, Unraid, Synology, a Raspberry Pi 4/5), anything with a few spare CPU cycles and gigabit-class networking.
- A Plex/Jellyfin Media Server reachable from that host (optional)

The bridge itself is stateless and light, just a few hundred MB of RAM and one FFmpeg worker per active cast. Video transcode is primarily handled by the media server, FFmpeg in this container takes 480p from the server to 480i.

## Quick start (Docker)

Docker with host networking is the primary deployment path.

```bash
# 1. Generate config.toml, then edit bridge.mister.host.
mkdir -p /opt/mister-groovy-relay
docker run --rm --network=host \
  -v /opt/mister-groovy-relay:/config \
  idiosync000/mister-groovy-relay:latest
$EDITOR /opt/mister-groovy-relay/config.toml

# 2. Run the bridge.
docker run -d --name mister-groovy-relay --restart unless-stopped \
  --network=host \
  -v /opt/mister-groovy-relay:/config \
  idiosync000/mister-groovy-relay:latest
```

Open `http://<host>:32500/`, choose Plex or Jellyfin in the sidebar, and link your server. The token is saved in `data.json` under `data_dir`.

For headless Plex linking:

```bash
docker run --rm -it --network=host \
  -v /opt/mister-groovy-relay:/config \
  idiosync000/mister-groovy-relay:latest --link
```

`--network=host` is required. The bridge needs a stable UDP source port for the MiSTer session and Plex GDM multicast on `239.0.0.250:32414`; bridged Docker networking breaks both.

## Native builds

Native archives are built for Windows, macOS, and Linux. On first run, the bridge writes a platform-specific config file and exits so you can set `bridge.mister.host`, then relaunch it.

| OS | Default config path |
| --- | --- |
| Windows | `%APPDATA%\mister-groovy-relay\config.toml` |
| macOS | `~/Library/Application Support/mister-groovy-relay/config.toml` |
| Linux | `$XDG_CONFIG_HOME/mister-groovy-relay/config.toml` or `~/.config/mister-groovy-relay/config.toml` |

Release archives bundle `ffmpeg`, `ffprobe`, and `yt-dlp` beside the bridge binary. To use system-installed tools instead, set `bridge.ffmpeg_path`, `bridge.ffprobe_path`, or `bridge.ytdlp_path` in `config.toml`.

On macOS, right-click the binary and choose **Open** the first time if Gatekeeper blocks it. As a fallback:

```bash
xattr -dr com.apple.quarantine /path/to/mister-groovy-relay-folder
```

## First-time setup

1. Install the Docker image or native binary.
2. Start once to generate `config.toml`.
3. Set `bridge.mister.host` to your MiSTer's LAN IP.
4. Restart the bridge.
5. Open `http://<host-ip>:32500/` and link Plex or Jellyfin.
6. Cast from Plex, Jellyfin, a URL, a supported streaming catalog, DLNA, or a torrent source.

The settings UI labels whether a saved field applies live, restarts the current cast, or requires a bridge restart.

## Cast sources

The list above is the quick promise. This table shows the main control surface for each source.

| Source | How to start | Notes |
| --- | --- | --- |
| Plex / Jellyfin video | Cast from the normal client picker | Requires server linking in the UI. |
| Plex / Jellyfin music | Cast an album, playlist, or track | Renders a CRT visualizer while audio plays through the MiSTer. |
| Direct media URL | Paste into the URL panel | Supports files, HLS, and DASH that FFmpeg can ingest. |
| Site URL | Paste into the URL panel | Uses `yt-dlp` for supported pages. See [URL adapter](docs/url-adapter.md). |
| Streaming catalogs | Use bundled channel entries or supported links | Includes Cartoon Rewind, MTV Rewind, and Toonami Aftermath bundled channels. |
| Torrent | Upload `.torrent` or paste a magnet link | Disabled by default. See [Torrent adapter](docs/torrent.md). |
| DLNA / UPnP | Choose the bridge from a DLNA controller | Disabled by default. See [DLNA adapter](docs/dlna.md). |
| Browser extension | Cast a page URL from the browser | WIP extension lives in [`extension/firefox/`](extension/firefox/README.md). |

## Adapters

| Adapter | Default | Deeper notes |
| --- | --- | --- |
| Plex | On after linking | Needs multicast discovery and stable bridge address. |
| Jellyfin | On after linking | Link through the settings UI. |
| URL | On | Cookies, scripted playback, and `yt-dlp` notes: [docs/url-adapter.md](docs/url-adapter.md). |
| Streams | On | Bundled catalog channels only; not a user IPTV importer. |
| Torrent | Off | Requires explicit traffic acknowledgement: [docs/torrent.md](docs/torrent.md). |
| DLNA / UPnP | Off | Exposes unauthenticated LAN control: [docs/dlna.md](docs/dlna.md). |

## Settings UI

Open `http://<host>:32500/` after the bridge starts. The UI lets you:

- Link Plex and Jellyfin accounts.
- Enable or disable adapters.
- Flip `interlace_field_order` live while watching the CRT.
- See adapter state at a glance: running, stopped, or erroring.
- Save bridge settings with clear apply scope.

## Operations

Most installs only need the quick start. Use [docs/operations.md](docs/operations.md) for the longer notes:

| Topic | When it matters |
| --- | --- |
| Multi-NIC hosts | Cast target appears, but commands never reach the bridge. |
| Adaptive delta-LZ4 | You want to experiment with lower UDP payloads on motion-light content. |
| Docker CPU contention | Playback shows motion glitches under host load. |
| Fake MiSTer diagnostics | You need to prove the bridge is sending packets before debugging the real MiSTer path. |

DLNA controller findings live in [docs/dlna-compatibility.md](docs/dlna-compatibility.md).

## Troubleshooting

| Symptom | First check | More detail |
| --- | --- | --- |
| Target missing from Plex | `--network=host`, multicast, server link, bridge logs | [Operations](docs/operations.md) |
| Cast target duplicates another Plex target | Run the bridge from a different IP than the Plex server | [Operations](docs/operations.md) |
| No video on CRT | MiSTer is running Groovy_MiSTer and listening on `mister_port` | [Operations](docs/operations.md) |
| Audio drift or motion glitches | Host CPU contention | [Operations](docs/operations.md) |
| Field shimmer | Flip `interlace_field_order` | Settings UI |
| Plex reports target offline after cast | Fixed `source_port` and no port conflict | [Operations](docs/operations.md) |
| DLNA renderer missing or uncontrollable | `bridge.host_ip`, UDP 1900, trusted LAN only | [DLNA adapter](docs/dlna.md) |

## License

[GPL-3.0](https://www.gnu.org/licenses/gpl-3.0.en.html). See the design notes for why: this project stands on the shoulders of several GPL-3 references (plexdlnaplayer, plex-mpv-shim, Groovy_MiSTer) and carries that license forward.
