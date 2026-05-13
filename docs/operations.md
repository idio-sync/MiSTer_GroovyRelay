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

The bridge can opt in to emit 13-byte BLITs alongside the standard 12-byte LZ4 path. It chooses the delta variant only when it is at least 5 percent smaller, matching the upstream Groovy_MiSTer reference threshold.

Enable it with:

```bash
GROOVY_DELTA_LZ4=1 ./mister-groovy-relay --config /path/to/config.toml
```

The default is off. The feature has no effect unless `bridge.lz4_enabled` is also true, which is the default.

**Known limitation: delta loss is silent.** UDP packet loss is structural in this protocol: there are no per-chunk sequence numbers, and the receiver concatenates by arrival order. If a delta-LZ4 field is dropped, sender and receiver history diverge until the next full BLIT resyncs them. On a stable LAN this usually recovers within a few frames; on a lossy link, leave delta-LZ4 off.

Grep logs for `delta_selected` to see how often the adaptive selector chose the delta path in each 5-second window.

## CPU contention under Docker

The data plane pushes fields at 59.94 Hz regardless of scheduling pressure. Under heavy CPU contention, FFmpeg can fall behind; the bridge covers with duplicate-field BLITs, so the symptom is visible motion glitches rather than A/V drift.

If you see glitches, cap container CPU so the bridge has dedicated cores:

```bash
docker run --cpus=2 ...
```

Two cores is typically enough for one 480p transcode plus Groovy packet framing.

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

**Plex says the target is offline moments after casting.**

This is usually a `source_port` problem. If the bridge restarts and binds a different ephemeral port, the MiSTer's session key no longer matches. Set `source_port` to a fixed number in `config.toml` and confirm nothing else on the host is using it.
