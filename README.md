# MiSTer_GroovyRelay

A Plex-to-MiSTer cast-target bridge. Run it alongside your Plex Media
Server; it advertises itself as a Plex cast target on the LAN, and when
you pick it from the Plex client's "Cast" menu it transcodes the
selected title through FFmpeg and streams raw RGB fields + PCM audio
over the [GroovyMiSTer](docs/references/groovy_mister.md) UDP protocol
into a MiSTer FPGA. The MiSTer drives a 15 kHz analog CRT directly,
giving you genuine 480i NTSC video for Plex content — no HDMI-to-CRT
scaler, no tearing, correct interlace motion.

## Hardware requirements

- MiSTer FPGA with Analogue I/O board (or equivalent) wired to a
  15 kHz-capable CRT (consumer, PVM, arcade, etc.)
- A host on the same LAN running Docker (Linux, Unraid, Synology, a
  Raspberry Pi 4/5 — anything with a few spare CPU cycles and
  gigabit-class networking)
- A Plex Media Server reachable from that host
- A GroovyMiSTer-capable MiSTer core running (e.g., `Groovy`, `MARS`,
  or any other receiver that speaks the same protocol)

The bridge itself is stateless and light — a few hundred MB of RAM and
one FFmpeg worker per active cast.

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

The CLI `--link` flow still works for headless / automation setups:

```bash
docker run --rm -it --network=host \
  -v /opt/mister-groovy-relay:/config \
  idiosync000/mister-groovy-relay:latest --link
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

Config is sectioned into a `[bridge]` block (shared by every adapter)
and per-adapter `[adapters.<name>]` blocks. Legacy flat-format configs
are migrated on first load — the original is preserved at
`config.toml.pre-ui-migration`.

| Key                                    | Default              | Meaning                                                                     |
| -------------------------------------- | -------------------- | --------------------------------------------------------------------------- |
| `bridge.data_dir`                      | `/config`            | Where the device UUID and plex.tv auth token live.                          |
| `bridge.host_ip`                       | auto-detect          | LAN IP advertised to Plex. Override on multi-NIC hosts (see below).         |
| `bridge.video.modeline`                | `"NTSC_480i"`        | Video mode. v1 supports `NTSC_480i` only.                                   |
| `bridge.video.interlace_field_order`   | `"tff"`              | `tff` or `bff`. Flip if you see field-order shimmer on the CRT.             |
| `bridge.video.aspect_mode`             | `"auto"`             | `letterbox`, `zoom`, or `auto` (ffmpeg cropdetect probe).                   |
| `bridge.video.rgb_mode`                | `"rgb888"`           | Wire pixel format. v1: `rgb888` only.                                       |
| `bridge.video.lz4_enabled`             | `true`               | LZ4-compress BLIT payloads. Strongly recommended.                           |
| `bridge.audio.sample_rate`             | `48000`              | PCM sample rate. `22050`, `44100`, or `48000`.                              |
| `bridge.audio.channels`                | `2`                  | `1` (mono) or `2` (stereo).                                                 |
| `bridge.mister.host`                   | *(required)*         | IP or hostname of your MiSTer on the LAN.                                   |
| `bridge.mister.port`                   | `32100`              | UDP port the MiSTer's Groovy core is listening on.                          |
| `bridge.mister.source_port`            | `32101`              | Our stable source UDP port. MUST stay the same across restarts.             |
| `bridge.ui.http_port`                  | `32500`              | Port the bridge serves the Plex Companion HTTP API + Settings UI on.        |
| `adapters.plex.enabled`                | `true`               | Toggle the Plex adapter on or off.                                          |
| `adapters.plex.device_name`            | `"MiSTer"`           | Name shown in the Plex cast-target list.                                    |
| `adapters.plex.device_uuid`            | auto                 | Stable identifier; auto-generated on first run and persisted to `data_dir`. |
| `adapters.plex.profile_name`           | `"Plex Home Theater"`| Client-capability profile name advertised to PMS.                           |
| `adapters.plex.server_url`             | auto-discover        | Optional: pin a specific PMS (`http://host:32400`) instead of GDM discovery.|

## Settings UI

Once the bridge is running, point a browser at `http://<host>:32500/`
(or whatever `bridge.ui.http_port` is set to). The settings page lets
you:

- Edit every field in `config.toml` with inline help and validation.
- Flip `interlace_field_order` live — no cast drop, no restart. Flip,
  look at the CRT, flip back. Four-click workflow per guess.
- Link your Plex account in-browser — no more `docker run … --link`
  terminal step. Click **Link Plex Account**, enter the 4-character
  code at plex.tv/link, done.
- Enable or disable adapters with a toggle (v1 ships Plex; Jellyfin,
  DLNA, URL arrive via the same interface in v2+).
- See at a glance which adapters are running (green dot), stopped
  (grey), or erroring (red + last error as tooltip).

Changes are written to `config.toml` atomically (`os.Rename` semantics,
no torn writes). Each field is tagged with an apply scope so the UI
tells you what it just did: *applied live* (hot-swap), *cast
restarted* (next play rebuilds the pipeline), or *restart the
container* (for bindable/identity fields where live propagation would
produce split-brain state).

### Authentication and LAN exposure

The settings UI has **no authentication**. Only expose the
`http_port` on networks you trust. The Plex Companion API (which
runs on the same port and predates the UI) has the same posture —
nothing has regressed, but the attack surface is larger now that
config is writable over HTTP.

If stronger isolation is needed:

- Put the bridge behind a reverse proxy (nginx, Caddy) with basic
  auth, and keep `http_port` bound LAN-only.
- Restrict access with host firewall rules (iptables / nftables /
  Unraid's Bridge Network Access setting).
- Use a WireGuard tunnel for out-of-LAN administration.

The bridge requires `--network=host` for GDM multicast discovery, so
binding to `127.0.0.1` would make the UI unreachable from other LAN
devices — which is almost certainly where you want to access it
from. LAN-layer isolation is the v1 answer.

## First-time setup walkthrough

1. **Install.** Pull the image (`docker pull idiosync000/mister-groovy-relay:latest`)
   or `go build ./cmd/mister-groovy-relay` for a native binary.

2. **Mount a config dir.** `docker run -v /opt/mister-groovy-relay:/config …`.
   The bridge auto-creates `config.toml` from defaults on first start
   if the file is missing.

3. **Open the UI.** Browse to `http://<docker-host>:32500/`. You'll
   land on the Bridge panel with a quick-start banner. Fill in your
   MiSTer's IP under **Network → MiSTer Host**, click **Save Bridge**.
   Because `bridge.mister.host` is a restart-bridge field (the UDP
   sender is bound at startup), the UI tells you to restart the
   container. `docker restart mister-groovy-relay` and reload.

4. **Link Plex.** Click **Plex** in the sidebar → **Link Plex
   Account**. Copy the 4-character code, open `plex.tv/link` in a new
   tab, paste, click **Allow**. The UI transitions to *Linked · RUN*
   within ~2 seconds.

5. **First cast.** Open Plex on your phone, pick a video, tap the
   cast icon, pick your bridge from the target list. The CRT lights
   up in 1–2 seconds.

If you prefer the terminal, `docker run --rm -it … --link` still
prints the code to stdout (the CLI flag is retained for headless /
automation use).

## Operational notes

### Multi-NIC Unraid hosts

The bridge advertises its own LAN address to Plex (in the `/resources` response
and in the plex.tv device registration PUT). By default it auto-detects that
address by asking the kernel which interface it would use to reach 8.8.8.8 — a
trick that works when the default route points at the LAN.

On Unraid hosts with multiple network interfaces — typical combinations are
LAN + WireGuard, LAN + Docker bridge, or LAN + secondary subnet — the default
route may not be the Plex-facing one. Symptoms: the cast target shows up in
the Plex picker but "commands never arrive" — the controller is trying to
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
Under heavy CPU contention (Unraid parity check, mover, a co-tenant container
spiking CPU) the FFmpeg decoder can fall behind; the bridge covers with
duplicate-field BLITs, which the FPGA rescans — so the symptom is visible
motion glitches, not A/V drift. (This is by design — the clock-push architecture
trades a graceful fallback against a hard drift bug.)

If you see glitches during parity checks, cap container CPU with
`docker run --cpus=2 ...` or the Unraid template's CPU-pinning option so the
bridge has dedicated cores that aren't preempted. 2 cores is typically
sufficient for a single 480p transcode plus Groovy packet framing.

## Troubleshooting

**"The target didn't show up in Plex's cast menu."**
The bridge uses GDM multicast discovery (port 32414). Confirm:
`--network=host` is set; your LAN is not carving off mDNS/multicast
between client and server; you linked successfully (`--link`); and the
bridge process is running (`docker logs mister-groovy-relay`).

**"No video on the CRT."**
Check the MiSTer is running a Groovy-capable core and is listening on
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
