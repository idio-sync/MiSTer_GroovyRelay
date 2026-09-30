# GroovyNLC core and frame compression

The bridge works with the original [Groovy_MiSTer](https://github.com/psakhis/Groovy_MiSTer) core and the [GroovyNLC fork](https://github.com/verbst/Groovy_MiSTer). It detects the core at the start of every cast, logs it (`core=groovy` or `core=groovynlc` on the cast start line), and shows it on hover over the meter's pipe readout.

## Using GroovyNLC

1. Install the fork's `.rbf` and `MiSTer_groovyNLC` binary, and add to `MiSTer.ini`:
   ```ini
   [GroovyNLC]
   main=MiSTer_groovyNLC
   ```

2. Launch GroovyNLC from the MiSTer menu; the UI's "Launch Groovy" button always starts the stock core.

3. Leave the core's OSD **Volatile framebuffer** option **Off**. The NLC codec needs it off, and it does no harm with LZ4.

The bridge sends a status ping after every 2 s of silence (during startup or a stall) so GroovyNLC v1.1–v1.3 doesn't close the session.

## Codec setting

`bridge.video.codec` controls frame compression:

| Value | Behaviour |
|-------|-----------|
| `auto` (default) | LZ4 on both cores today. |
| `lz4` | Always LZ4. |
| `raw` | Uncompressed. |
| `nlc` | GroovyNLC's near-lossless codec. Needs the GroovyNLC core; on the original core the bridge falls back to LZ4 and logs a warning. |

Old `lz4_enabled` configs migrate automatically (`true` → `auto`, `false` → `raw`). A future release may make `auto` pick NLC once it is verified on hardware; set `codec = "lz4"` to keep LZ4 regardless.

When `codec = "nlc"`, two more settings apply:

| Value | Behaviour |
|-------|-----------|
| `nlc_near` | `0` (lossless) to `3`; higher values trade picture fidelity for a smaller encoded size. |
| `nlc_pack` | `tiled` (default) or `rice`. Rice only works with a Rice-capable GroovyNLC build; otherwise expect a garbled picture. |

**NLC status:** the Go encoder is a bit-exact port of the fork's reference codec and costs about 2 ms per 720x240 field (about 4 ms of CPU across 3 cores) on a desktop CPU; benchmark NAS-class hosts before enabling it. It has **not yet been verified on real hardware**: try 240p first (480i is unconfirmed on the fork), with **Volatile framebuffer** Off.
