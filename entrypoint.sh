#!/bin/sh
# entrypoint.sh — bridge container init.
#
# Best-effort yt-dlp self-update on each container start, gated by a
# daily marker file so hourly restarts don't hammer GitHub. Bundled
# version (from Dockerfile static-binary download) is the floor and is
# always usable. We do NOT block bridge startup on network reachability.
#
# Spec: docs/specs/2026-04-25-url-ytdlp-design.md §"entrypoint.sh".

set -e

MARKER=/var/lib/ytdlp-last-update
mkdir -p "$(dirname "$MARKER")"

OLD_VER="$(yt-dlp --version 2>/dev/null || echo unknown)"

# Run the update if the marker is missing OR older than 24h.
should_update=1
if [ -f "$MARKER" ]; then
    # `find -mtime +1` returns the path iff the file was modified MORE
    # than 24h ago. If the find is empty, the marker is fresh; skip.
    if [ -z "$(find "$MARKER" -mtime +1 2>/dev/null)" ]; then
        should_update=0
    fi
fi

if [ "$should_update" = "1" ]; then
    if yt-dlp -U >/var/log/ytdlp-update.log 2>&1; then
        NEW_VER="$(yt-dlp --version 2>/dev/null || echo unknown)"
        if [ "$OLD_VER" = "$NEW_VER" ]; then
            echo "yt-dlp already current: $NEW_VER" >&2
        else
            echo "yt-dlp updated: $OLD_VER -> $NEW_VER" >&2
        fi
        touch "$MARKER"
    else
        echo "yt-dlp self-update failed (using bundled $OLD_VER)" >&2
    fi
else
    echo "yt-dlp self-update skipped (last attempt < 24h ago, version $OLD_VER)" >&2
fi

exec /usr/local/bin/mister-groovy-relay --config /config/config.toml "$@"
