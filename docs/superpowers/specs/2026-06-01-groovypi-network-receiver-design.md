# GroovyPi Network Receiver Design

## Summary

GroovyPi is a headless Raspberry Pi 4 appliance that behaves like a
Groovy_MiSTer-compatible network receiver. It receives the Groovy UDP protocol
from MiSTer_GroovyRelay and other Groovy senders, decodes RGB fields and PCM
audio, and presents them over a VGA666/DPI RGB output to a 15 kHz RGB PVM.

The v1 release target is a dumb receiver: power on the Pi, wait for the
bouncing-logo idle screen, and cast to it over wired Ethernet. It requires no
keyboard, desktop, menu, or local user interaction during normal operation.

## Goals

- Support Raspberry Pi 4 with wired Ethernet, VGA666, and an RGB PVM as the
  v1 hardware target.
- Boot directly into the receiver app through systemd.
- Run from a Raspberry Pi OS Lite based SD card image with a read-only root
  filesystem for normal operation.
- Listen on the standard Groovy UDP port, `32100`.
- Implement the Groovy_MiSTer AV receiver path for `INIT`, `SWITCHRES`,
  `BLIT_FIELD_VSYNC`, `AUDIO`, `CLOSE`, ACK/status replies, raw fields, LZ4
  fields, delta-LZ4 fields, and duplicate fields.
- Keep the Groovy_MiSTer bouncing logo/screensaver behavior when no sender is
  active.
- Prioritize protocol-compatible behavior with GroovyRelay as the first tested
  sender and MiSTerCast/GroovyMAME-style senders as best-effort v1 targets.
- Start development in the MiSTer_GroovyRelay monorepo while keeping package
  boundaries clean enough to split the receiver and image tooling later.

## Non-Goals

- V1 does not replace a Plex, Jellyfin, or media server host. The Pi is the CRT
  endpoint.
- V1 does not support Pi 2, Pi 3B, Pi Zero, Wi-Fi, or non-Pi SBCs.
- V1 does not dynamically program every incoming Groovy modeline.
- V1 does not require USB HID controller feedback. The protocol boundary should
  reserve the path, but input can ship in v2 unless the basic joystick path is
  small after AV is stable.
- V1 does not need a local web UI or on-device setup menu.

## Hardware Target

The supported v1 target is Raspberry Pi 4, wired Ethernet, VGA666, and a 15 kHz
RGB PVM. Pi 4 is the floor because raw 720x240 RGB fields at roughly 60 fields
per second require about 250 Mbps before UDP/IP overhead, and Groovy senders can
fall back to raw fields on hard-to-compress content.

Pi 3B+ may be explored later as an experimental target, but its Ethernet path
has little headroom for worst-case raw fields. Pi 2 and Pi 3B are not targeted
because 100 Mbps Ethernet is below the receiver's worst-case stream rate.

## Architecture

Runtime data flow:

```text
Groovy sender -> UDP 32100 -> groovypi-receiver -> KMS/DPI renderer -> VGA666 -> RGB PVM
```

The first implementation lives in this repo as a new receiver command, for
example `cmd/groovypi-receiver`. It reuses `internal/groovy` and extracts the
receiver-safe pieces of `internal/fakemister` into reusable packages instead of
duplicating protocol logic.

The receiver should be divided into small components:

- `groovyrecv`: UDP protocol/session layer. It owns sender tracking, command
  parsing, ACK/status replies, payload reassembly, LZ4/delta decode, duplicate
  fields, idle detection, and session lifecycle.
- `modelinemap`: maps incoming Groovy `SWITCHRES` values to supported Pi output
  presets. V1 maps to NTSC 240p or NTSC 480i.
- `render`: renderer interface plus a Raspberry Pi KMS/DPI implementation. It
  owns DRM/KMS resources, buffers, page flips, field presentation, and idle logo
  rendering.
- `audio`: ALSA output for Groovy PCM audio with a small delay/resync buffer.
- `appliance`: systemd unit, boot configuration, read-only root setup, and boot
  partition config.
- `input`: reserved package/daemon boundary for later USB HID joystick feedback.

## Video Modes

V1 supports two Pi-side output presets:

- NTSC 240p, used for initial KMS/DPI bring-up and progressive senders.
- NTSC 480i, used as the main media acceptance target for GroovyRelay.

Incoming modelines are not applied dynamically in v1. `modelinemap` accepts the
sender's modeline, classifies it by shape and interlace, and selects the nearest
supported Pi output preset. Unknown PAL or arcade modelines should not crash the
receiver. They either map to the nearest NTSC mode when reasonable or show an
unsupported-mode idle/status screen while keeping the process alive.

Dynamic KMS/DPI timing changes remain a v2 compatibility goal after the fixed
presets prove stable on real hardware.

## Protocol Compatibility

GroovyPi treats Groovy_MiSTer as the reference receiver. V1 should implement the
wire behavior needed by existing senders without sender-specific branches.

Required v1 protocol behavior:

- `INIT`: validate the packet, store compression/audio/RGB mode, and reply with
  a 13-byte ACK/status packet. V1 must accept at least `rgb888`, no-audio,
  44.1 kHz stereo, and 48 kHz stereo. Unsupported RGB modes are accepted at the
  protocol layer but move the renderer to an unsupported-mode screen and drop
  incoming fields until a new supported `INIT` arrives. Unsupported audio modes
  disable audio for that session without stopping video.
- `SWITCHRES`: parse and store the sender modeline, then map it to a supported
  Pi preset.
- `BLIT_FIELD_VSYNC`: support raw full fields, LZ4 full fields, delta-LZ4
  fields, and duplicate-field headers.
- `AUDIO`: reassemble PCM payload chunks and feed the ALSA output buffer.
- `CLOSE`: tear down the active session and return to idle/screensaver.
- ACK/status replies: report enough frame echo, raster/count, and audio-ready
  behavior to keep GroovyRelay and other Groovy senders paced correctly.
- `GET_VERSION` and `GET_STATUS`: respond compatibly if senders use them.

The upstream Groovy API also has a second input socket path for joystick and
PS2 keyboard/mouse feedback. The receiver design should reserve this path, but
v1 does not block on implementing USB HID to Groovy input packets.

## Idle Screensaver

When no active sender is connected, the renderer shows a bouncing logo
screensaver. The screen should appear after boot without a keyboard or local UI.

Idle behavior:

- Show the logo immediately after video output is configured.
- Keep animating while waiting for Groovy UDP traffic.
- On a valid `INIT`, transition into session mode.
- On `CLOSE`, sender timeout, repeated decode failure, or receiver restart,
  return to the logo.

## SD Image And Boot Behavior

The v1 release artifact is a compressed Raspberry Pi OS Lite based SD image,
for example:

```text
groovypi-receiver-rpi4-vga666-v0.x.img.xz
```

Normal boot behavior:

1. Pi powers on.
2. Raspberry Pi OS Lite boots without desktop or menu.
3. `systemd` starts `groovypi-receiver`.
4. VGA666 output is configured.
5. The PVM shows the bouncing logo.
6. Wired DHCP comes up.
7. The receiver waits on UDP `32100`.

Normal operation should not write to the root filesystem. Runtime state, logs,
PID files, and scratch files live on `tmpfs`. The release image should run with
a read-only root filesystem. Configuration can be edited on the boot partition
before boot, using a small file such as `groovypi.toml`.

Development can also support copying a binary and systemd unit onto an existing
Pi OS Lite installation before the image builder is polished.

## Error Handling

- Bad field payloads are dropped while the last good picture remains visible.
- LZ4 decode failure, delta without history, and size mismatch are logged and
  counted; repeated failures return to idle.
- Audio underrun or overflow resyncs the audio path without stopping video.
- Missing sender traffic without `CLOSE` returns to idle after a timeout.
- Renderer initialization failure exits nonzero so systemd can restart the
  receiver during development.
- Process crashes are restarted by systemd.
- Unsupported modelines do not panic the process.

## Testing Strategy

Most receiver behavior should be testable without Pi hardware.

Unit and integration layers:

- Packet parsing and ACK generation tests.
- Modeline mapping tests for known NTSC 240p, NTSC 480i, and unknown inputs.
- Field decode tests for raw, LZ4, delta-LZ4, and duplicate fields.
- Audio reassembly tests.
- Session lifecycle tests for `INIT`, `SWITCHRES`, streaming, `CLOSE`, timeout,
  and idle return.
- Fake renderer tests for output mode selection, field order, idle logo state,
  and recovery from bad packets.
- Loopback tests where GroovyRelay sends to `groovypi-receiver` on localhost
  with a fake renderer.

Hardware validation:

- A small KMS/DPI smoke-test command draws color bars and a moving logo over
  VGA666 before full receiver integration.
- End-to-end Pi test: fresh boot, no keyboard, bouncing logo visible, DHCP
  network up, GroovyRelay cast starts, stable NTSC 240p output, stable NTSC
  480i output, stop cast, return to logo.

## V1 Acceptance Criteria

- A Raspberry Pi 4 boots directly into the receiver app with no keyboard.
- The RGB PVM shows the bouncing logo after boot.
- Wired DHCP works.
- The receiver listens on UDP `32100`.
- GroovyRelay can cast stable NTSC 240p and NTSC 480i.
- Audio plays through ALSA when the sender provides PCM audio.
- Stopping the cast or losing the sender returns to the bouncing logo.
- The process restarts cleanly under systemd if it crashes.
- The release image performs normal operation with a read-only root filesystem.

## Phasing

Phase 0 proves the hardware output path with a KMS/DPI smoke test on Pi 4 and
VGA666.

Phase 1 builds the receiver command with a fake renderer and local tests for
Groovy protocol/session behavior.

Phase 2 connects the receiver to the Pi KMS/DPI renderer and validates NTSC
240p on the PVM.

Phase 3 adds NTSC 480i presentation and audio output.

Phase 4 packages the systemd service, boot configuration, read-only root setup,
and SD image.

Phase 5 validates best-effort compatibility with MiSTerCast and GroovyMAME-style
senders and documents any sender-specific gaps.

Phase 6 explores USB HID joystick feedback and Pi 3B+ as v2 candidates.

## References

- Groovy_MiSTer is the reference receiver behavior and protocol target:
  https://github.com/psakhis/Groovy_MiSTer
- Raspberry Pi KMS/DPI display configuration is the v1 output stack:
  https://www.raspberrypi.com/documentation/computers/raspberry-pi.html#manually-configure-a-display
- Recalbox VGA666/RGB Pi documentation is useful hardware background for the
  known Pi 4 + VGA666 + RGB PVM setup:
  https://wiki.recalbox.com/en/tutorials/video/crt/crt-screen-dpi-vga666-piscart-rgbpi
