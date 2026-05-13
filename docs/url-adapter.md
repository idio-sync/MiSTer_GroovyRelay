# URL Adapter

The URL adapter lets the bridge play direct media URLs and page URLs from sites that `yt-dlp` can resolve. Paste an `http://` or `https://` URL into the **URL** panel in the settings UI and click **Play**.

Sessions run until EOF or until another cast starts. Basic pause and seek controls are available in the web UI.

## What it accepts

| URL type | Examples | Resolver |
| --- | --- | --- |
| Direct media | MP4, MKV, HLS `.m3u8`, DASH `.mpd` | FFmpeg |
| Supported pages | YouTube, Twitch, Vimeo, Internet Archive, SoundCloud, Bandcamp | `yt-dlp` |
| Streaming catalogs | Cartoon Rewind, MTV Rewind, Toonami Aftermath bundled channels | URL/Streams adapters |

The curated auto-resolve list lives in the URL panel. More `yt-dlp` sites can be added as the bridge grows.

## Cookies for auth-walled content

Age-gated YouTube videos, members-only Twitch VODs, and similar content require login cookies. The URL panel has a collapsed **Cookies** section that accepts a Netscape-format `cookies.txt`.

1. Install a cookies export extension such as [Get cookies.txt LOCALLY](https://github.com/kairi003/Get-cookies.txt-LOCALLY) for Chrome/Edge or [cookies.txt](https://addons.mozilla.org/firefox/addon/cookies-txt/) for Firefox.
2. Log in to the site you want to cast from.
3. Export the cookies file.
4. Open the URL panel, expand **Cookies**, paste the file contents, and click **Save Cookies**.

Cookies are saved to `<bridge.data_dir>/url_cookies.txt` with mode `0600` on POSIX systems and survive container restarts through the existing `data_dir` volume mount. Click **Clear** to remove them.

Saved cookies are never echoed back into the textarea. The form also sets `autocomplete="off"` so password managers do not offer to save them.

## Scripted playback

Scripts can POST to the same endpoint the UI uses.

```bash
# htmx form-style
curl -X POST \
  -H "Origin: http://<bridge-host>:32500" \
  -d 'url=https://youtu.be/dQw4w9WgXcQ&mode=auto' \
  http://<bridge-host>:32500/ui/adapter/url/play

# JSON
curl -X POST \
  -H "Origin: http://<bridge-host>:32500" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://youtu.be/dQw4w9WgXcQ","mode":"ytdlp"}' \
  http://<bridge-host>:32500/ui/adapter/url/play
```

The `Origin` header is required because this endpoint runs through the bridge's CSRF middleware. Browsers set the expected fetch headers automatically; `curl` and other scripted clients must send an `Origin` matching the bridge host and port. Without it, the bridge returns `403`.

htmx callers receive an HTML fragment. Other callers receive JSON.

Credentials in URLs such as `https://user:pass@host/path` are redacted in the panel display, success response body, and logs. JSON responses echo the submitted URL because the API caller already provided it.

## yt-dlp self-update

The Docker image bundles a recent `yt-dlp` binary. On container start, the entrypoint runs `yt-dlp -U`, gated by a daily marker file so frequent restarts do not hammer GitHub.

Failed updates log a warning and keep using the bundled version.
