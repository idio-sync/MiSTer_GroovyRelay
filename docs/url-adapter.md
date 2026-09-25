# URL Adapter

The URL adapter lets the bridge play direct media URLs and page URLs from sites that `yt-dlp` can resolve. Paste an `http://` or `https://` URL into the **Input** field in the web UI and click **CAST**.

Sessions run until EOF or until another cast starts. Basic pause and seek controls are available in the web UI.

## What it accepts

| URL type | Examples | Resolver |
| --- | --- | --- |
| Direct media | MP4, MKV, HLS `.m3u8`, DASH `.mpd`, Owncast homepage URLs | FFmpeg |
| Supported pages | YouTube, Twitch, Vimeo, Internet Archive, SoundCloud, Bandcamp | `yt-dlp` |
| Streaming catalogs | Cartoon Rewind, MTV Rewind, Toonami Aftermath bundled channels | URL/Streams adapters |

Owncast sites can be pasted as their homepage URL. The adapter detects Owncast through the same-origin `/api/status` endpoint and plays `/hls/stream.m3u8`.

The curated auto-resolve list lives under **Settings → URL → yt-dlp hosts**. More `yt-dlp` sites can be added there.

## Live HLS buffering

Direct public HTTP(S) URLs whose path ends in `.m3u8` use the shared live HLS buffer by default. The adapter fetches the playlist and media segments into `<bridge.data_dir>/url/hls`, starts a few segments behind the live edge, and hands FFmpeg a local playlist with a local-only media policy. That adds a small live delay, but helps absorb uneven remote playlist reloads and segment downloads.

History replay preserves the HLS buffer mode stored with each entry.

Set `GROOVY_HLS_BUFFER=0` on the bridge process to bypass the buffer globally for diagnostics or rollback. Unsupported HLS features fail clearly rather than silently falling back through FFmpeg.

The CRT `BUFFERING...` slate is not part of this v1 path yet; the current behavior still relies on the existing dataplane underrun handling if a source runs dry.

## Cookies for auth-walled content

Age-gated YouTube videos, members-only Twitch VODs, and similar content require login cookies. The URL adapter settings have a **Cookies** field that accepts a Netscape-format `cookies.txt`.

1. Install a cookies export extension such as [Get cookies.txt LOCALLY](https://github.com/kairi003/Get-cookies.txt-LOCALLY) for Chrome/Edge or [cookies.txt](https://addons.mozilla.org/firefox/addon/cookies-txt/) for Firefox.
2. Log in to the site you want to cast from.
3. Export the cookies file.
4. Open **Settings → URL**, paste the file contents into **Cookies**, and click **Save cookies**.

Cookies are saved to `<bridge.data_dir>/url_cookies.txt` with mode `0600` on POSIX systems and survive container restarts through the existing `data_dir` volume mount. Click **Clear** to remove them.

Saved cookies are never echoed back into the textarea. The field also sets `autocomplete="off"` so password managers do not offer to save them.

## Scripted playback

Scripts can POST to the same cast endpoint the web UI uses. The bridge picks the resolver automatically (`auto` mode).

```bash
curl -X POST \
  -H "Origin: http://<bridge-host>:32500" \
  --data-urlencode 'payload=https://youtu.be/dQw4w9WgXcQ' \
  http://<bridge-host>:32500/ui/cast
```

The `Origin` header is required: cast requests must come from the bridge's own origin. Browsers set the expected fetch headers automatically; `curl` and other scripted clients must send an `Origin` matching the bridge host and port (or `Sec-Fetch-Site: same-origin`). Without it, the bridge returns `403`. Responses are JSON.

Credentials in URLs such as `https://user:pass@host/path` are redacted in the UI display and logs.

## yt-dlp self-update

The Docker image bundles a recent `yt-dlp` binary. On container start, the entrypoint runs `yt-dlp -U`, gated by a daily marker file so frequent restarts do not hammer GitHub.

Failed updates log a warning and keep using the bundled version.
