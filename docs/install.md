# Installation details

The [README quick start](../README.md#quick-start-docker) covers most installs. This page covers the rest.

## Headless Plex linking

```bash
docker run --rm -it --network=host \
  -v /opt/mister-groovy-relay:/config \
  idiosync000/mister-groovy-relay:latest --link
```

The token is saved in `data.json` under `data_dir`.

## Docker networking

Host networking gives the bridge the host's LAN address, avoids Docker NAT on the MiSTer UDP source port, and lets Plex GDM multicast work. Bridge-mode port publishing (`-p 32500:32500/tcp -p 32101:32101/udp -p 32412:32412/udp`) is not equivalent: Docker still NATs outbound UDP and multicast is unreliable.

macvlan/ipvlan on `br0` (or another LAN-facing interface) also works: give the container its own LAN IP, set `bridge.host_ip` to it, and make sure the MiSTer, Plex Media Server, and Plex controllers can reach it. No ports need publishing; the container owns `32500/tcp`, `bridge.mister.source_port` (default `32101/udp`), and Plex GDM multicast (`239.0.0.250`, UDP `32412/32413`).

Don't pin the container to Unraid's isolated cores; see [CPU pinning and isolated cores](operations.md#cpu-pinning-and-isolated-cores-unraid).

## Native builds

Native binaries are built for Windows, macOS, and Linux. On first run the bridge writes a config file and exits; set `bridge.mister.host` and relaunch. These builds are supported but may lag Docker on features and fixes.

| OS | Default config path |
| --- | --- |
| Windows | `%APPDATA%\mister-groovy-relay\config.toml` |
| macOS | `~/Library/Application Support/mister-groovy-relay/config.toml` |
| Linux | `$XDG_CONFIG_HOME/mister-groovy-relay/config.toml` or `~/.config/mister-groovy-relay/config.toml` |

Release archives bundle `ffmpeg`, `ffprobe`, and `yt-dlp`. To use system-installed tools, set `bridge.ffmpeg_path`, `bridge.ffprobe_path`, or `bridge.ytdlp_path`.

On macOS, if Gatekeeper blocks the binary, right-click it and choose **Open**, or run:

```bash
xattr -dr com.apple.quarantine /path/to/mister-groovy-relay-folder
```

## Local Files

Local Files (off by default) browses named folders the bridge can read. Enable it in Settings, add one or more libraries, then cast a file from the Local Files browse drawer.

For Docker, bind-mount media into the container and use the container path as the library root:

```bash
docker run -d --name mister-groovy-relay --restart unless-stopped \
  --network=host \
  -v /opt/mister-groovy-relay:/config \
  -v /mnt/user/media:/media:ro \
  idiosync000/mister-groovy-relay:latest
```

Here the library root is `/media`, not `/mnt/user/media`: paths are validated inside the container. The container user must be able to read and list the directory; if a library fails validation in Docker, check ownership, mode bits, ACLs, and NAS UID/GID mapping.

Native builds use real OS paths, such as `/home/me/Videos`, `/Volumes/Media`, `D:\Movies`, or `\\server\share\movies`.
