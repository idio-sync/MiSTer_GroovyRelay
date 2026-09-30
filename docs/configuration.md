# Configuration reference

Every setting lives in `config.toml` (written on first run; see [install.md](install.md#native-builds) for its location). Most are also editable in the Settings UI, which marks whether a change applies live, restarts the current cast, or needs a bridge restart. The generated file carries a one-line comment for each setting.

Topics with their own pages:

- Video mode, aspect, field order, flicker filter, picture size and OSD: [picture.md](picture.md)
- Compression (`codec`, `nlc_*`): [groovynlc.md](groovynlc.md); `delta_lz4_enabled`: [operations.md](operations.md#experimental-adaptive-delta-lz4-blits)
- Visualizer mode, AUX, Spotify Connect, AirPlay: [music.md](music.md)
- Live HLS buffer (`[bridge.hls_buffer]`): [operations.md](operations.md#live-hls-buffering)
- URL adapter: [url-adapter.md](url-adapter.md); DLNA: [dlna.md](dlna.md); torrents: [torrent.md](torrent.md)

## Bridge

| Setting | Default | What it does |
| --- | --- | --- |
| `bridge.data_dir` | per OS | Where state (`data.json`, caches) lives. |
| `bridge.ffmpeg_path`, `ffprobe_path`, `ytdlp_path` | empty | Tool overrides. Empty uses the bundled copy, then `PATH`. |
| `bridge.host_ip` | auto-detect | LAN IP advertised to Plex. Set it on multi-NIC hosts or with macvlan/ipvlan. |
| `bridge.ui.http_port` | `32500` | Port for the UI and Plex Companion. |

## MiSTer (`[bridge.mister]`)

| Setting | Default | What it does |
| --- | --- | --- |
| `host` | — | MiSTer IP or hostname. Required. |
| `port` | `32100` | Groovy UDP port on the MiSTer. |
| `source_port` | `32101` | The bridge's fixed UDP source port. Keep it stable; see [operations.md](operations.md#general-troubleshooting). |
| `ssh_user`, `ssh_password` | `root`, `1` | MiSTer login used only by the **Launch Groovy** button. The password is stored in plain text. |

## Audio (`[bridge.audio]`)

| Setting | Default | What it does |
| --- | --- | --- |
| `sample_rate` | `48000` | Output sample rate sent to the MiSTer. |
| `channels` | `2` | Output channels. |
| `output_volume` | `100` | Global output level, `0`–`100` (the receiver's volume knob). |

The tone controls (`[bridge.audio.dsp]`) are the receiver's audio strip:

| Setting | Default | What it does |
| --- | --- | --- |
| `enabled` | `true` | `false` bypasses all shaping. |
| `mono` | `false` | Downmix stereo to mono. |
| `subsonic` | `false` | 20 Hz subsonic cut. |
| `loudness` | `false` | Equal-loudness boost at low volume. |
| `bass`, `mid`, `treble` | `0.0` | Tone gains, `-12`–`+12` dB. |
| `balance` | `0` | Left/right balance, `-100`–`+100` (negative is left). |
| `eq` | ten `0.0` | 10-band octave EQ (31 Hz–16 kHz), `-12`–`+12` dB each. |

## Plex (`[adapters.plex]`)

| Setting | Default | What it does |
| --- | --- | --- |
| `device_name` | `MiSTer` | Name in Plex's cast-target list. |
| `profile_name` | `Plex Home Theater` | Client profile the bridge presents to Plex. |
| `server_url` | auto-discovered | Pin a specific Plex Media Server URL. |
| `device_uuid` | generated | Persistent device ID; leave it alone. |

## URL adapter (`[adapters.url]`)

| Setting | Default | What it does |
| --- | --- | --- |
| `ytdlp_enabled` | `true` | Resolve pages from `ytdlp_hosts` through yt-dlp. |
| `ytdlp_hosts` | curated list | Sites routed through yt-dlp (YouTube, Twitch, Vimeo, Archive.org, …). Override to add more. |
| `ytdlp_format` | `bv*[height<=720]+ba/bv*+ba/b` | yt-dlp format selector. |
| `ytdlp_resolve_timeout_seconds` | `30` | How long yt-dlp may take to resolve a page. |

Cookies for sites that need a login are pasted in the URL panel's **Cookies** section, not set in `config.toml`.

## Spotify Connect and AirPlay

Both have `name`, `audio_output` (`monitor` or `visual_only`), `pause_grace_seconds` (default `30`), and `binary_path`. Spotify adds `bitrate` (`96`, `160`, or `320`, default `320`) and `zeroconf_port` (`0` = random); AirPlay adds `port` (RTSP, default `5000`). See [music.md](music.md).

## AUX (`[adapters.aux.input]`)

`mode`, `url`, `format`, `device`, and `audio_output` are covered in [music.md](music.md#aux-analog-visualizer). The rest rarely need changing: `sample_rate` (`48000`), `channels` (`2`), `thread_queue_size` (`64`), and the FFmpeg probe limits `analyze_duration_ms` (`100`) and `probe_size` (`32768`).

## Torrent (`[adapters.torrent]`)

| Setting | Default | What it does |
| --- | --- | --- |
| `traffic_acknowledged` | `false` | Must be `true` before torrent casts run; see [torrent.md](torrent.md). |
| `download_dir` | `data_dir` | Where piece data is cached. |
| `keep_completed` | `false` | Keep downloaded data after playback instead of deleting it. |
| `max_cache_bytes` | 20 GiB | Cache size ceiling. |
| `metadata_timeout_seconds` | `60` | How long to wait for a magnet link's metadata (`5`–`600`). |
| `startup_buffer_seconds` | `10` | Media buffered before playback starts. |
| `max_upload_rate_kbps` | `512` | Upload cap in KiB/s; `0` = unlimited. |
| `max_download_rate_kbps` | `0` | Download cap in KiB/s; `0` = unlimited. |
| `listen_port` | `0` | Peer listen port; `0` = random. |
