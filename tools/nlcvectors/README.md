# nlcvectors

Golden test vectors for `internal/groovy/nlc`, the pure-Go port of
GroovyNLC (the Groovy_MiSTer near-lossless video codec). This tool builds
the codec's own C++ reference implementation, runs it over a fixed matrix
of synthetic and real-looking inputs, and captures the encoded/decoded
bytes so the Go port can be checked for byte-exact parity.

It is **not** run in CI and is **not** part of the bridge build. It exists
purely to (re)generate `internal/groovy/nlc/testdata/`, which is what the
Go tests actually consume. Regenerate it only when the vendored codec or
the case matrix changes.

## Provenance

`nlc_codec.h` and `nlc_codec.cpp` are byte-for-byte copies (a three-line
provenance comment aside) of `api/nlc_codec.h` / `api/nlc_codec.cpp` from
the [verbst/Groovy_MiSTer](https://github.com/verbst/Groovy_MiSTer) fork
at commit `e60f52a` (release v1.4), licensed GPL-2.0-or-later. They are
vendored here only to generate test data offline; they are never linked
into `mister-groovy-relay`.

`main.cpp` (the CLI harness) and `gen.sh` (the driver script) are original
to this repo.

## Requirements

- A C++17 compiler on `PATH` as `clang++`, or set `$CXX` to override.
- `gzip`, `sha256sum`, `python3`, `ffmpeg` on `PATH`.

## Regenerating

From the repo root:

```bash
bash tools/nlcvectors/gen.sh
# or
make nlc-vectors
```

This compiles the harness into a temp directory (never inside the repo),
generates the input images, runs the full case matrix through the
reference codec, and overwrites `internal/groovy/nlc/testdata/` from
scratch (stale files are deleted first, so a re-run produces an identical
tree — verified byte-for-byte as part of this step).

## Case matrix

8 inputs × 4 `near` levels (0-3) × 2 pack modes (tiled, rice) = 64 cases.

| input | size | source |
|---|---|---|
| flat | 96x24 | solid (64, 200, 90) |
| gradient | 96x24 | R/G ramps, B = (x+y) & 255 |
| edges | 96x24 | 11px alternating black/white bars + a diagonal line |
| noise | 96x24 | 32-bit LCG byte stream, seed 12345 |
| primaries | 96x24 | 4px checkerboard of the six RGB/CMY primaries |
| spikes | 96x24 | flat (40,40,40) with periodic full-white/full-black pixels |
| odd | 250x7 | gradient at a width not divisible by the 16px tile |
| field | 720x240 | one `ffmpeg testsrc2` frame, rgb24 |

All cases use the same `nlc_params` the fork's client actually builds
(`GroovyMister::buildNlcParams`, `api/groovymister.cpp`): `rgb =
NLC_RGB888`, `color = NLC_COLOR_YCOCG`, `tile = 16`, `width_bits = 4`,
`rice_k = -1`, `pack` and `near_lvl` varying per case.

## testdata format (`internal/groovy/nlc/testdata/`)

- `inputs/<input>.rgb.gz` — gzip (`-n -9`, no timestamp) of the raw
  synthetic RGB888 input.
- `encoded/<input>_n<near>_<pack>.nlc.gz` — gzip of the exact encoded
  bytes from `nlc_encode`.
- `vectors.json` — a JSON array, one object per case, sorted by `name`,
  with keys in this order: `name`, `input`, `width`, `height`, `near`,
  `pack` (`"tiled"`/`"rice"`), `encoded_size`, `encoded_sha256`,
  `decoded_sha256`. Both hashes are SHA-256 over the raw, un-gzipped
  bytes; `decoded_sha256` hashes the reference decoder's output, which at
  `near=0` equals the input's own hash (lossless round-trip).

`internal/groovy/nlc/testdata/**` is marked `-text` in `.gitattributes`
so these bytes are never touched by `core.autocrlf` on any platform.
