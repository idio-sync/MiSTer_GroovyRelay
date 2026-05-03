# Third-Party Notices

Native release archives bundle external command-line tools next to the bridge
binary so the resolver can find them without requiring PATH changes.

Sidecar versions and checksums are pinned in `scripts/sidecar-manifest.txt`.
The manifest currently includes downloaded binary assets for:

- FFmpeg and FFprobe LGPL builds from BtbN/FFmpeg-Builds for Linux amd64,
  Linux arm64, and Windows amd64.
- yt-dlp 2026.03.17 binaries from yt-dlp/yt-dlp for Linux, Windows, and macOS.

macOS FFmpeg and FFprobe are built during the macOS release job from the
official FFmpeg 8.1 source tarball:

- Source: https://ffmpeg.org/releases/ffmpeg-8.1.tar.xz
- SHA256: `b072aed6871998cce9b36e7774033105ca29e33632be5b6347f3206898e0756a`

The macOS source build disables GPL, nonfree, and version3 components, disables
external-library autodetection, and enables only LGPL-compatible system-backed
features needed by the sidecar (`securetransport`, `zlib`). OSXExperts and
Martin Riedl macOS builds were reviewed on 2026-05-02 and were not added because
their published build information includes GPL codec stacks.

FFmpeg is distributed by its upstream project under LGPL/GPL terms depending
on build configuration. This project uses BtbN assets with `lgpl` in the
artifact name for the pinned Linux/Windows sidecars, and an LGPL-safe official
source build for macOS. See:
https://ffmpeg.org/legal.html

yt-dlp is distributed under The Unlicense. See:
https://github.com/yt-dlp/yt-dlp/blob/master/LICENSE
