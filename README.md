# MiSTer_GroovyRelay

A Plex-to-MiSTer cast-target bridge. Run it alongside your Plex Media
Server; it advertises itself as a Plex cast target on the LAN, and when
you pick it from the Plex client's "Cast" menu it transcodes the
selected title through FFmpeg and streams raw RGB fields + PCM audio
over the [Groovy_MiSTer](https://github.com/psakhis/Groovy_MiSTer) UDP protocol
into a MiSTer FPGA. The MiSTer drives a 15 kHz analog CRT directly,
giving you genuine 480i NTSC video for Plex content.

## Hardware requirements

- MiSTer FPGA with Analogue I/O board (or equivalent) wired to a
  15 kHz-capable CRT (consumer, PVM, arcade, etc.)
  - GroovyMiSTer installed on your MiSTer
- A host on the same LAN running Docker (Linux, Unraid, Synology, a
  Raspberry Pi 4/5 — anything with a few spare CPU cycles and
  gigabit-class networking)
- A Plex Media Server reachable from that host

The bridge itself is stateless and light, just a few hundred MB of RAM and
one FFmpeg worker per active cast.

## Quick start (Docker)

```bash
# 1. Create a config directory and do a throwaway first run.
#    The bridge sees no config.toml, writes a default one into your
#    mounted /config, and exits 2. Edit that file to point at your MiSTer.
mkdir -p /opt/mister-groovy-relay
docker run --rm --network=host \
  -v /opt/mister-groovy-relay:/config \
  idiosync000/mister-groovy-relay:latest
$EDITOR /opt/mister-groovy-relay/config.toml

# 2. Link to your plex.tv account (interactive).
docker run --rm -it --network=host \
  -v /opt/mister-groovy-relay:/config \
  idiosync000/mister-groovy-relay:latest --link
# → prints a 4-char code; open https://plex.tv/link and paste it.

# 3. Long-run: detach and let it broadcast.
docker run -d --name mister-groovy-relay --restart unless-stopped \
  --network=host \
  -v /opt/mister-groovy-relay:/config \
  idiosync000/mister-groovy-relay:latest
```

`--network=host` is required. The bridge needs a stable source UDP port
(the MiSTer keys its session by sender `IP:port`) and it needs to
receive the Plex GDM multicast on `239.0.0.250:32414`. Bridged Docker
networking does not pass multicast and would rewrite the source port on
every restart — neither is workable.

## Configuration reference

Everything is in `config.toml`. The bridge writes a fully-commented
default at `<data_dir>/config.toml` on first run if the file is missing,
then exits so you can edit it. The canonical copy lives at
[`internal/config/example.toml`](internal/config/example.toml) if you'd
rather read it without starting the container.

| Key                      | Default              | Meaning                                                                          |
| ------------------------ | -------------------- | -------------------------------------------------------------------------------- |
| `device_name`            | `"MiSTer"`           | Name shown in the Plex cast-target list.                                         |
| `device_uuid`            | auto                 | Stable identifier; auto-generated on first run and persisted to `data_dir`.      |
| `mister_host`            | *(required)*         | IP or hostname of your MiSTer on the LAN.                                        |
| `mister_port`            | `32100`              | UDP port the MiSTer's Groovy core is listening on.                               |
| `source_port`            | `32101`              | Our stable source UDP port. MUST stay the same across restarts.                  |
| `http_port`              | `32500`              | Port the bridge serves the Plex Companion HTTP API on.                           |
| `modeline`               | `"NTSC_480i"`        | Video mode. v1 supports `NTSC_480i` only.                                        |
| `interlace_field_order`  | `"tff"`              | `tff` or `bff`. Flip if you see field-order shimmer on the CRT.                  |
| `aspect_mode`            | `"auto"`             | `letterbox`, `zoom`, or `auto` (ffmpeg cropdetect probe).                        |
| `rgb_mode`               | `"rgb888"`           | Wire pixel format. `rgb888`, `rgba8888`, or `rgb565`.                            |
| `lz4_enabled`            | `true`               | LZ4-compress BLIT payloads. Strongly recommended.                                |
| `audio_sample_rate`      | `48000`              | PCM sample rate. `22050`, `44100`, or `48000`.                                   |
| `audio_channels`         | `2`                  | `1` (mono) or `2` (stereo).                                                      |
| `plex_profile_name`      | `"Plex Home Theater"`| Client-capability profile name advertised to PMS.                                |
| `plex_server_url`        | auto-discover        | Optional: pin a specific PMS (`http://host:32400`) instead of GDM discovery.     |
| `data_dir`               | `/config`            | Where the device UUID and plex.tv auth token live.                               |

## First-time setup walkthrough

1. **Install.** Pull the image (`docker pull idiosync000/mister-groovy-relay:latest`)
   or `go build ./cmd/mister-groovy-relay` if you want a native binary.
2. **Configure.** Start the bridge once with no config present — it writes
   a default `config.toml` into `data_dir` (default `/config`) and exits
   with code 2. Edit that file; the only mandatory change is
   `mister_host` — point it at your MiSTer's IP.
3. **Link.** Run with `--link`. The bridge prints a 4-character code
   and the plex.tv link URL; paste the code at `https://plex.tv/link`
   while signed in to the Plex account that owns your PMS. The returned
   auth token is persisted to `data_dir/data.json` (mode 0600) so you
   only do this once.
4. **First cast.** Drop the `--link` flag and start the bridge
   normally. Open Plex on your phone (or web client), pick a video,
   tap the cast icon, and select your `device_name` from the list.
   Playback should start on the CRT within 1–2 seconds.

## Operational notes

### Multi-NIC Unraid hosts

The bridge advertises its own LAN address to Plex (in the `/resources` response
and in the plex.tv device registration PUT). By default it auto-detects that
address by asking the kernel which interface it would use to reach 8.8.8.8.

On hosts with multiple network interfaces (typical combinations are
LAN + WireGuard, LAN + Docker bridge, or LAN + secondary subnet) the default
route may not be the Plex-facing one. Symptoms: the cast target shows up in
the Plex picker but "commands never arrive." The controller is trying to
reach the bridge on an unreachable NIC.

Fix: set `host_ip` explicitly to the LAN IP the Plex controller can reach.
Find it with `ip -4 addr show | grep inet` on the host; the `br0` or `eth0`
interface IP on the same subnet as your Plex Media Server is what you want.

```toml
host_ip = "192.168.1.20"
```

Restart the bridge. Check the startup log for the `host_ip not set` warning —
if it's gone, your override took effect.

### CPU contention under Docker

The data plane pushes fields at 59.94 Hz regardless of scheduling pressure.
Under heavy CPU contention the FFmpeg decoder can fall behind; the bridge covers with duplicate-field BLITs, which the FPGA rescans, so the symptom is visible
motion glitches, not A/V drift. (This is by design, the clock-push architecture
trades a graceful fallback against a hard drift bug.)

If you see glitches cap container CPU with
`docker run --cpus=2 ...` so the bridge has dedicated cores that aren't preempted. 2 cores is typically sufficient for a single 480p transcode plus Groovy packet framing.

## Troubleshooting

**"The target didn't show up in Plex's cast menu."**
The bridge uses GDM multicast discovery (port 32414). Confirm:
`--network=host` is set; your LAN is not carving off mDNS/multicast
between client and server; you linked successfully (`--link`); and the
bridge process is running (`docker logs mister-groovy-relay`).

**"No video on the CRT."**
Check the MiSTer is running the Groovy_MiSTer core and is listening on
the configured `mister_port` (default 32100). Try `fake-mister` locally
to confirm the bridge is sending packets at all:
`go run ./cmd/fake-mister -addr :32100` on the same host as the
bridge, point `mister_host = "127.0.0.1"` at it, start a cast, and
watch for `cmd 2/3/7/...` counts in the fake's summary output. If you
see packets there but nothing on the real MiSTer, it's network routing
or a Groovy core config issue, not the bridge.

**"Audio drifts over long playback."**
This bridge uses a single FFmpeg process with shared A/V timestamps, so
long-term drift is structurally mitigated. Short-term offsets usually
indicate host CPU contention — check for parity checks, scrubs, or
co-tenant transcodes competing with the ffmpeg worker.

**"The picture shimmers / fields look wrong."**
Flip `interlace_field_order` between `tff` and `bff`. The "correct"
value depends on your MiSTer core + cable path; once you pick the right
one it stays right.

**"Plex says the target is offline moments after casting."**
Almost always a `source_port` regression — if the bridge restarted and
bound a different ephemeral port, the MiSTer's session key no longer
matches. Make sure `source_port` is set to a fixed number in
`config.toml` and that nothing else on the host is using it.

## License

[GPL-3.0](https://www.gnu.org/licenses/gpl-3.0.en.html). See the design
notes for why: this project stands on the shoulders of several GPL-3
references (plexdlnaplayer, plex-mpv-shim, Groovy_MiSTer) and carries
that license forward.

## Further reading

- [Design spec](docs/specs/2026-04-19-mister-groovy-relay-design.md) —
  architecture, packet-level flow, testing strategy.
- [Implementation plan](docs/plans/2026-04-19-mister-groovy-relay-v1.md) —
  phased build plan that produced the current binary.
- [References](docs/references/) — upstream projects studied during
  design: `groovy_mister.md`, `mistercast.md`, `plex-mpv-shim.md`,
  `plexdlnaplayer.md`, `mistglow.md`, `mister_plex.md`.
