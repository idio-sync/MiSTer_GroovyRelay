# Torrent Adapter

The Torrent adapter can cast a magnet link, uploaded `.torrent` file, or HTTP(S) `.torrent` URL through MiSTer Groovy Relay.

## Enablement

The adapter is disabled by default. It also requires `traffic_acknowledged = true` before any BitTorrent client is created or any BitTorrent listen port opens.

Enable it only when you understand the traffic it creates and have the right to download and upload the content.

## Torrent URLs

The Torrent adapter can also cast an HTTP(S) URL that points to BitTorrent metainfo. Use the **Torrent URL** quick-cast tab and paste a URL such as:

```text
https://example.com/movie.torrent
```

Remote torrent URL fetching is public HTTP(S) only. The bridge rejects URL credentials, IP-literal hosts, private/local/link-local/multicast/special-use addresses, unsafe redirects, and responses over 4 MiB. If the URL path does not end in `.torrent`, the server must support `HEAD` and return a BitTorrent content type (`application/x-bittorrent` or `application/x-torrent`) before the bridge will download the body.

Torrent URL casts require the adapter to be enabled and BitTorrent traffic acknowledged, just like magnet links and uploads.

Servers that only allow presigned `GET` requests should use a URL path ending in `.torrent`. Otherwise the bridge returns an error instead of downloading an arbitrary body to sniff it.

## Traffic visibility

BitTorrent traffic can be visible to peers and network operators.

The default upload limit is `512 KiB/s`. Set `max_upload_rate_kbps = 0` for unlimited upload, or use a lower positive value for a stricter cap.

## Cache behavior

Torrent media is served only to the local bridge process through `/torrent/session/{token}/media`, and that route rejects non-loopback clients.

Cache data lives under `<download_dir-or-data_dir>/groovyrelay-torrent/`. Session cache data is deleted after playback unless `keep_completed = true`.
