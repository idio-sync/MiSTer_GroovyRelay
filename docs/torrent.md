# Torrent Adapter

The Torrent adapter can cast a magnet link or uploaded `.torrent` file through MiSTer Groovy Relay.

## Enablement

The adapter is disabled by default. It also requires `traffic_acknowledged = true` before any BitTorrent client is created or any BitTorrent listen port opens.

Enable it only when you understand the traffic it creates and have the right to download and upload the content.

## Traffic visibility

BitTorrent traffic can be visible to peers and network operators.

The default upload limit is `512 KiB/s`. Set `max_upload_rate_kbps = 0` for unlimited upload, or use a lower positive value for a stricter cap.

## Cache behavior

Torrent media is served only to the local bridge process through `/torrent/session/{token}/media`, and that route rejects non-loopback clients.

Cache data lives under `<download_dir-or-data_dir>/groovyrelay-torrent/`. Session cache data is deleted after playback unless `keep_completed = true`.
