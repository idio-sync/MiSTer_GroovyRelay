# Music and the CRT visualizer

## Visualizer modes

Audio-only Plex and Jellyfin casts render a CRT visualizer in the globally configured mode:

- `retro_analyzer`: classic spectrum bars.
- `oscilloscope_wave`: horizontal waveform trace.
- `stereo_scope`: stereo/vector-scope display.
- `vu_cabinet`: stereo VU meter cabinet display.
- `spectrum_waterfall`: scrolling spectrogram waterfall (showspectrum).
- `raster_pulse`: mirrored reactive waveform bands.
- `cover_vu`: cached album art with VU meters.
- `cover_spectrum`: cached album art with spectrum bars.

Changing the mode does not interrupt the current cast; it applies to the next music cast.

## AUX analog visualizer

The `AUX` source drives the CRT visualizer from a line-in or USB audio interface.

Native binaries use `mode = "local_capture"` with the host's FFmpeg capture format and device. Docker/Unraid should use `mode = "stream_url"` with a small FFmpeg producer on the machine that has the audio input:

```bash
while true; do
  ffmpeg -nostdin -f alsa -thread_queue_size 64 -sample_rate 48000 -channels 2 -i hw:1,0 \
    -vn -ac 2 -ar 48000 -f wav -listen 1 http://0.0.0.0:8090/aux.wav
  sleep 0.2
done
```

The loop is required because the bridge opens the stream twice per AUX start: once to probe, once to play.

If your FFmpeg build does not stream `-f wav` cleanly over HTTP (some write a fixed `RIFF` header), use `-f mpegts` or `-f ogg` and update `url` to match. Check the producer with `ffprobe http://capture-host:8090/aux.wav` from the bridge host first.

`audio_output = "visual_only"` drives the visualizer without sending audio to the MiSTer; `audio_output = "monitor"` also plays the captured audio through the MiSTer.

## Spotify Connect

Enable `[adapters.spotify]` and the bridge appears in the Spotify app's device list (Spotify Premium required). Picking it plays the music through the MiSTer and drives the CRT visualizer; track title, artist, and album update on screen without interrupting playback, and the `cover_vu` / `cover_spectrum` modes show the album art.

- The bridge runs [librespot](https://github.com/librespot-org/librespot) as a supervised helper. The Docker image bundles it; native installs need `librespot` on `PATH` or `binary_path` set.
- Discovery uses mDNS, so the container needs `--network=host` (already required). Set `zeroconf_port` if a firewall needs a fixed port.
- Pausing keeps the cast on the CRT for `pause_grace_seconds` (default 30), then ends it. Starting another cast (Plex, DLNA, …) takes over and disconnects the phone.
- `audio_output = "visual_only"` drives the visualizer without sending audio to the MiSTer.

## AirPlay

Enable `[adapters.airplay]` and the bridge appears as an AirPlay speaker on iPhones, iPads, and Macs. It behaves like Spotify Connect above: live track text, album art in the cover modes, a pause grace window, and another cast taking over.

- The bridge runs [shairport-sync](https://github.com/mikebrady/shairport-sync) in classic AirPlay (AirPlay 1) mode. The Docker image bundles it; native Linux and macOS installs need `shairport-sync` on `PATH` (built with `--with-stdout --with-metadata --with-metadata-multicast`) or `binary_path` set. Windows is not supported.
- It listens on RTSP port 5000 plus UDP ports 6001–6010. Change `port` if another AirPlay receiver on the host (for example macOS's own AirPlay Receiver) already uses 5000.
- Discovery uses shairport-sync's built-in mDNS responder. If the speaker does not appear on a host that runs its own mDNS daemon (avahi), check the helper's log lines for port 5353 errors.
- AirPlay 2 (multi-room grouping with HomePods) is not supported yet.
