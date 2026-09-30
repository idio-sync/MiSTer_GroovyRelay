# Operations

This page holds the longer networking, performance, and diagnostic notes that do not need to live in the README.

## Multi-NIC hosts

The bridge advertises its own LAN address to Plex in the `/resources` response and in the plex.tv device registration PUT. By default, it asks the kernel which interface it would use to reach `8.8.8.8`.

On hosts with multiple interfaces, such as LAN plus WireGuard, LAN plus Docker bridge, or LAN plus a secondary subnet, the default route may not be the Plex-facing one. A common symptom is that the cast target appears in Plex, but commands never arrive.

Set `host_ip` explicitly to the LAN IP the Plex controller can reach:

```toml
host_ip = "192.168.1.20"
```

Find the right address with `ip -4 addr show | grep inet` on the host. The `br0` or `eth0` address on the same subnet as your Plex Media Server is usually the one you want.

Restart the bridge and check that the startup log no longer warns that `host_ip` is unset.

## Experimental adaptive delta-LZ4 BLITs

The Groovy wire protocol defines a 13-byte BLIT variant carrying an LZ4-compressed byte-wrap subtraction of the current field against the previous same-polarity field:

```text
delta[i] = current[i] - prev[i] mod 256
```

The FPGA reconstructs the field by adding the previous framebuffer bytes back to the decompressed delta. On motion-light content, the delta compresses better than the full field, reducing UDP chunk count and lowering the chance of hitting the 500 KB congestion backoff threshold.

When enabled, the bridge emits 13-byte BLITs alongside the standard 12-byte LZ4 path, choosing the delta variant only when it is at least 5 percent smaller (the upstream Groovy_MiSTer reference threshold).

It is **off by default**. Turn on **Delta-LZ4** in Settings → Video & Audio, set `delta_lz4_enabled = true` under `[bridge.video]`, or override either with `GROOVY_DELTA_LZ4=1` (or `=0`) on the bridge process. It only applies when `codec` resolves to LZ4. Configs generated before delta became opt-in contain `delta_lz4_enabled = true` and keep it until changed.

**Known issue: the picture can freeze.** On a real Groovy core, a delta stream froze the picture (audio kept playing) after a burst of MiSTer frame skips, and it stayed frozen through the periodic full-field resyncs below until the cast restarted with delta off. The bridge kept sending and the MiSTer kept acknowledging every field, so nothing in the bridge log flags it beyond the `vga_frameskip` warnings before it. If you see this, turn delta-LZ4 off.

**Known limitation: delta loss is not acknowledged per chunk.** UDP packet loss is structural in this protocol: there are no per-chunk sequence numbers, and the receiver concatenates by arrival order. If a delta-LZ4 field is lost, sender and receiver history diverge until a full BLIT resyncs them. The bridge forces that resync early whenever the receiver may be out of step:

- when the MiSTer's ACK frame echo skips a frame or goes backwards (a BLIT or its ACK was lost), rate-limited to once per ~0.5 s;
- after any duplicate-field run (video underrun);
- after any payload send that aborted mid-field;
- and unconditionally about once per second per field polarity.

On a lossy link where resyncs fire constantly, leave delta-LZ4 off.

The bridge only computes the compression that is likely to win. If delta keeps losing on a polarity (film grain, noise, heavy motion), it stops trying delta for ~0.5 s and re-checks; if delta keeps winning, it skips the full-field compression. Both payloads are always valid on the wire.

Useful fields on the 5-second `dataplane stats` line:

- `delta_selected`: fields sent as delta in the window.
- `delta_resyncs_total`: forced full-field resyncs. A steady climb on a wired LAN would mean the receiver is not ACKing every BLIT.
- `full_lz4_attempts_total` / `delta_lz4_attempts_total`: compressions actually performed, useful when judging CPU headroom on small hosts.

## Live HLS buffering

The bridge buffers eligible live HLS before FFmpeg sees it. V1 is default-on for:

- bundled Streams direct HLS entries, including Toonami Aftermath;
- URL adapter direct public HTTP(S) URLs whose path ends in `.m3u8`.

The buffer fetches playlists and segments into the bridge data directory, publishes a local playlist, and keeps refreshing the playlist in the background. FFmpeg reads local files instead of chasing remote HLS children itself. Expect a small live delay: the default target starts around three HLS segments behind the live edge and keeps a small rolling cache, capped by both segment count and bytes.

Useful controls:

- Set `enabled = false` under `[bridge.hls_buffer]` to disable the shared buffer through config.
- Set `GROOVY_HLS_BUFFER=0` on the bridge process for a quick diagnostic or rollback bypass.
- For bundled Streams, provider/channel `hls_buffer_disabled` settings can opt out a direct stream without disabling the whole catalog.

Tuning, under `[bridge.hls_buffer]` (defaults rarely need changing):

| Setting | Default | What it does |
| --- | --- | --- |
| `live_edge_segments` | `3` | Stay this many segments behind the live edge. |
| `start_segments` | `2` | Segments to fetch before starting FFmpeg. |
| `max_cached_segments` | `6` | Rolling segment count kept per cast. |
| `max_cache_bytes` | 256 MiB | Per-cast cache ceiling. |
| `max_playlist_bytes` / `max_segment_bytes` | 1 MiB / 50 MiB | Largest playlist or segment accepted from the origin. |
| `playlist_timeout_seconds` / `segment_timeout_seconds` | `10` / `10` | HTTP timeouts for playlist refreshes and segment downloads. |
| `max_variant_height` | `720` | Tallest master-playlist variant eligible for buffering. |
| `stale_cache_reap_hours` | `24` | Age after which abandoned cache directories are removed. |

Cache roots live under `<bridge.data_dir>/streams/hls` and `<bridge.data_dir>/url/hls`. Startup reaps stale session directories older than `stale_cache_reap_hours`, while active sessions keep a lock marker so they are left alone.

Unsupported HLS features such as encrypted streams, byte ranges, discontinuities, alternate audio renditions, low-latency parts, fragmented MP4 init maps, and audio-only HLS fail clearly. Use the global bypass if you need to fall back to the old direct-FFmpeg path for a specific stream.

The TV-side `BUFFERING...` slate is deferred. If a live source stops publishing long enough to drain the local cache, the existing dataplane underrun behavior still applies.

## CPU contention under Docker

The data plane pushes fields at 59.94 Hz regardless of scheduling pressure. Under heavy CPU contention, FFmpeg can fall behind; the bridge covers with duplicate-field BLITs, so the symptom is visible motion glitches rather than A/V drift.

If you see occasional hitches on a busy host, give the bridge a larger CPU share so the kernel runs it first when cores are contended. It costs nothing while cores are idle:

```bash
docker run --cpu-shares=8192 ...
```

On Unraid, add `--cpu-shares=8192` to the container's **Extra Parameters**. To try it on a running container, use `docker update --cpu-shares 8192 <name>` (lost when Unraid recreates the container). Two free cores are typically enough for one 480p cast. Avoid `--cpus` limits, which throttle the bridge instead of prioritizing it.

### CPU pinning and isolated cores (Unraid)

Do not pin the container only to isolated cores (`isolcpus`, which Unraid sets from its CPU Pinning page for VMs). Linux never moves threads between isolated cores, so every bridge and FFmpeg thread stays on one of them however many you pin. Field sends then stall behind FFmpeg, and the MiSTer shows the lower part of the picture tearing or flashing. Pinning to a single core has the same effect.

The bridge detects both cases at startup, logs a warning, and repeats it at the top of Settings → System. To fix it, remove the container's CPU pinning or pin it to cores that are not isolated (check `/sys/devices/system/cpu/isolated` on the host).

## General troubleshooting

**The target did not appear in Plex's cast menu.**

The bridge uses Plex GDM multicast on `239.0.0.250`; this implementation listens on UDP `32412` and sends HELLO advertisements to UDP `32413`. Confirm host networking, or an L2 container network with its own LAN IP, multicast is not blocked between client and server, linking succeeded, and the bridge process is running.

**Another Plex cast target overwrote this one.**

Run the bridge from a different IP than the Plex Media Server. Plex cast discovery can confuse targets that appear to come from the same IP. Use macvlan/ipvlan Docker networking to give the container its own IP address if it runs on the same physical host as the Plex server.

**No video appears on the CRT.**

Confirm the MiSTer is running Groovy_MiSTer and listening on `mister_port`, default `32100`. To confirm the bridge is sending packets, run `fake-mister` on the bridge host:

```bash
go run ./cmd/fake-mister -addr :32100
```

Point `mister_host = "127.0.0.1"` at it, start a cast, and watch for command counts in the fake summary output. If packets appear there but not on the real MiSTer, the problem is network routing or Groovy core configuration.

**Audio drifts over long playback.**

The bridge uses a single FFmpeg process with shared A/V timestamps, so long-term drift is structurally mitigated. Short-term offsets usually indicate host CPU contention.

**The picture shimmers or fields look wrong.**

Flip `interlace_field_order` between `tff` and `bff`. The correct value depends on the MiSTer core and cable path.

**Subtitles, text, or thin horizontal lines flicker on an interlaced mode.**

That is interlace twitter. Set **Flicker filter** (`bridge.video.interlace_filter`) to `full`; see [Flicker filter](picture.md#flicker-filter).

**Plex says the target is offline moments after casting.**

This is usually a `source_port` problem. If the bridge restarts and binds a different ephemeral port, the MiSTer's session key no longer matches. Set `source_port` to a fixed number in `config.toml` and confirm nothing else on the host is using it.

## Scripting the UI

Scripted POSTs to UI endpoints need the browser's same-origin header, or they return 403. For example, to change the visualizer mode:

```bash
curl -i -X POST http://localhost:32500/ui/visualizer \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -H 'Sec-Fetch-Site: same-origin' \
  --data 'mode=stereo_scope'
```
