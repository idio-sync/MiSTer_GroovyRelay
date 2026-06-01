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

## Design Principles

- Prefer Groovy_MiSTer compatibility over fastest-first shortcuts. The v1
  implementation may phase sender validation, but it should not knowingly choose
  protocol behavior that prevents MiSTerCast, GroovyMAME-style senders, or
  GroovyRelay from working later.
- Keep the Pi receiver a dumb endpoint. It does not discover media, transcode
  sources, or expose a local playback UI in v1.
- Prove the physical VGA666/KMS output path early. Receiver protocol work should
  not get far ahead of a real Pi 4 + VGA666 + PVM smoke test.

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

## Target Device Rationale

The receiver code itself is expected to be lightweight. The target-device
constraint comes from the combination of network headroom and 15 kHz RGB output,
not from ordinary CPU/RAM needs.

Raspberry Pi 4 is the first supported target because it combines:

- true gigabit-class wired Ethernet for worst-case raw field bursts;
- a known VGA666/DPI path;
- Raspberry Pi OS Lite and KMS/DRM support;
- existing real-world proof from a working Pi 4 + VGA666 + Recalbox + RGB PVM
  setup.

Pi 3B+ is a later experiment, not a v1 promise. It has more Ethernet headroom
than a Pi 3B, but the Ethernet path is still constrained enough that raw
720x240x3 fields at about 60 Hz can run close to the edge once UDP/IP overhead,
field bursts, audio, ACK traffic, and OS jitter are included.

Pi 2, Pi 3B, and Pi Zero-class boards are outside v1 because they either lack
enough wired Ethernet bandwidth or require USB networking paths that turn a
real-time RGB receiver into a support problem.

Other SBCs may be cheaper on paper, but v1 does not target them because the
unknown is not whether a small daemon can run. The unknown is whether the board
can produce a stable, documented, supportable 15 kHz RGB path comparable to Pi
4 + VGA666. Alternate SBC support belongs after the renderer boundary is proven
and after the Pi 4 appliance works.

## Architecture

Runtime data flow:

```text
Groovy sender -> UDP 32100 -> groovypi-receiver -> KMS/DPI renderer -> VGA666 -> RGB PVM
```

The first implementation lives in this repo as a new receiver command, for
example `cmd/groovypi-receiver`. It reuses `internal/groovy` and extracts the
receiver-safe pieces of `internal/fakemister` into reusable packages instead of
duplicating protocol logic.

`internal/fakemister` is a test harness, not the production receiver. Extraction
must produce production protocol primitives with explicit status, ACK, timeout,
and session semantics. The appliance must not depend on fake/test package
behavior as the source of truth.

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
supported Pi output preset. V1 mapping rules:

- Progressive, 57-61 Hz, 200-288 active lines maps to NTSC 240p. The source
  raster is scaled or padded into the fixed 720x240 output buffer.
- Interlaced, 57-61 Hz, 400-525 active lines maps to NTSC 480i. Each source
  field is scaled or padded into the fixed 720x240 field buffer.
- 50 Hz PAL, off-rate arcade modes, and rasters outside those bands are
  unsupported in v1. They show an unsupported-mode idle/status screen, drop
  incoming fields for that session, and keep the receiver process alive for the
  next `INIT`.

Dynamic KMS/DPI timing changes remain a v2 compatibility goal after the fixed
presets prove stable on real hardware.

## Interlace And Field Order

For NTSC 480i, the `BLIT_FIELD_VSYNC` field byte is the source of truth:

- field `0` is the top/even field.
- field `1` is the bottom/odd field.

The renderer preserves that polarity when presenting fields to KMS/DPI. V1 must
include a boot-partition config option to invert field order if a specific
VGA666/PVM setup shows shimmer. Fake renderer tests must assert field polarity
and duplicate-field behavior, and hardware acceptance must include a field-order
test pattern or known interlaced clip.

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
- ACK/status replies: follow the explicit session contract below for frame echo,
  raster/count, and audio-ready behavior.
- `GET_VERSION` and `GET_STATUS`: respond compatibly if senders use them.

The upstream Groovy API also has a second input socket path for joystick and
PS2 keyboard/mouse feedback. The receiver design should reserve this path, but
v1 does not block on implementing USB HID to Groovy input packets.

## Protocol And Session Contract

The receiver is single-session. A valid `INIT` establishes the active sender as
the datagram's source IP and source UDP port. Payload chunks and commands from
other sources are ignored while that session is active, except that a new valid
`INIT` from another source preempts the old session, clears decode/audio state,
and becomes the new active sender.

Replies always go to the active sender's source address and port. The receiver
emits 13-byte ACK/status packets:

- immediately after accepting `INIT`;
- when `GET_STATUS` is received;
- after each complete field is accepted, decoded, and queued for presentation;
- after each duplicate-field header is processed.

The ACK frame echo reports the last accepted Groovy frame number. The raster
fields report the receiver's current output-frame counter and an approximate
scanline within the active NTSC preset. V1 does not need cycle-perfect
Groovy_MiSTer raster timing, but the values must be monotonic and consistent
enough for sender drift-correction code to avoid stalls.

Status bits:

- VRAM-ready is true while the renderer is initialized and can accept a field.
- Audio-ready is true when audio is enabled for the session and the ALSA queue is
  below its high-water mark.
- The field/parity bit reflects the receiver's current NTSC 480i output field
  when interlaced, and remains false for progressive output.

Payload reassembly failure resets the current payload only. It does not tear
down the session unless repeated failures cross the configured idle/error
threshold.

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

The service should not wait for `network-online.target` before showing the logo.
Video initialization and idle rendering start as soon as local devices are
available. UDP listening binds as soon as the network stack allows it, and DHCP
completion is not required for the local idle screen.

Normal operation should not write to the root filesystem. Runtime state, logs,
PID files, and scratch files live on `tmpfs`. The release image should run with
a read-only root filesystem. Configuration can be edited on the boot partition
before boot, using a small file such as `groovypi.toml`.

Development can also support copying a binary and systemd unit onto an existing
Pi OS Lite installation before the image builder is polished.

## SD Image Rationale

The desired user experience is closer to a console or arcade appliance than a
general-purpose Linux machine: flash the image, insert the SD card, power on the
Pi, and see the idle logo without a keyboard.

The release path is therefore an SD card image, while the development path can
remain a normal binary copied onto a Pi OS Lite install. Keeping both paths
matters: the binary/dev-service path keeps iteration fast, and the image path
keeps the eventual user experience simple.

The image is Raspberry Pi OS Lite based for v1 because it reduces kernel, KMS,
DPI, ALSA, USB input, and networking risk. Buildroot is attractive for a later
small appliance image, but it would move too much work into kernel/userspace
packaging before the receiver and VGA666 renderer are proven. Recalbox is useful
as hardware proof for the VGA666/PVM setup, but it is not the right base for a
dumb receiver image because it brings an emulator/front-end stack that v1 does
not need.

The read-only setup is meant to survive normal power removal. It is not a
physical write-protect switch. The root filesystem should be mounted read-only
during normal operation; mutable runtime paths live on `tmpfs`; operator config
lives on the boot partition; and any action that writes persistent state must be
explicit and rare. A powered-off receiver should not corrupt itself merely
because it was waiting for fields or rendering the idle logo.

## Hardware And Image Defaults

The release image defaults to the Raspberry Pi 4 analog audio output for PCM
monitor audio. The ALSA device name is configurable in the boot-partition
`groovypi.toml` so a USB DAC can be selected without rebuilding the image. HDMI
audio is not part of the v1 appliance path.

The boot partition owns operator-editable configuration, including hostname,
receiver port, video preset family, optional field-order inversion, SSH
development switch, and ALSA device override. The read-only root owns binaries,
systemd units, and static assets. Writable runtime paths are limited to `tmpfs`
locations such as `/run`, `/tmp`, and the system journal strategy chosen for the
image.

Practical image defaults:

- root filesystem mounted read-only after boot;
- `/run` and `/tmp` mounted as `tmpfs`;
- logs kept volatile by default, with optional developer diagnostics mode;
- no desktop, launcher, or emulator front end;
- `groovypi-receiver.service` starts automatically and restarts on failure;
- SSH disabled in release images unless enabled through boot-partition config;
- boot-partition config is readable from Windows, macOS, and Linux before first
  boot.

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

## Decision History

This project intentionally starts as a network receiver rather than a full
all-in-one Pi cast appliance. An all-in-one appliance is a plausible end-user
goal, but the risky part is the receiver/rendering path: Groovy protocol
compatibility, field decode, audio sync, KMS/DPI presentation, and 15 kHz RGB
timing. Proving that as a dumb target first keeps the scope clear. Once the
receiver works, running GroovyRelay on the same Pi and sending to loopback can
be evaluated as a packaging step.

The design also starts inside the MiSTer_GroovyRelay monorepo. That keeps the
first implementation close to existing Groovy protocol builders, modelines,
tests, and `fakemister` receiver code. The package boundaries are deliberately
named so the receiver can split into a standalone project later without
entangling itself with Plex/Jellyfin/DLNA adapter code.

Controller input is preserved as a compatibility requirement but not a v1
release blocker. The upstream Groovy API has a second socket path for joystick
and PS2 input feedback, so the design reserves an `input` package boundary.
Actual USB HID mapping can ship in v2 unless it falls out cheaply after the AV
receiver is stable.

Compatibility is preferred over fastest-first shortcuts. GroovyRelay is the
first-class test sender because it is the local app, but the receiver should be
shaped as a Groovy_MiSTer-compatible endpoint so MiSTerCast, GroovyMAME-style
senders, and future Groovy senders are not excluded by early design choices.

## References

- Groovy_MiSTer is the reference receiver behavior and protocol target:
  https://github.com/psakhis/Groovy_MiSTer
- Raspberry Pi KMS/DPI display configuration is the v1 output stack:
  https://www.raspberrypi.com/documentation/computers/raspberry-pi.html#manually-configure-a-display
- Raspberry Pi 4 specifications ground the supported v1 hardware target:
  https://www.raspberrypi.com/products/raspberry-pi-4-model-b/specifications/
- Raspberry Pi 3B+ specifications ground the experimental-only hardware note:
  https://www.raspberrypi.com/products/raspberry-pi-3-model-b-plus/
- Recalbox VGA666/RGB Pi documentation is useful hardware background for the
  known Pi 4 + VGA666 + RGB PVM setup:
  https://wiki.recalbox.com/en/tutorials/video/crt/crt-screen-dpi-vga666-piscart-rgbpi
