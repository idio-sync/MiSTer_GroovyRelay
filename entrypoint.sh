#!/bin/sh
# entrypoint.sh — bridge container init.
#
# Best-effort yt-dlp self-update on each container start, gated by a
# daily marker file so hourly restarts don't hammer GitHub. Bundled
# version (from Dockerfile static-binary download) is the floor and is
# always usable. We do NOT block bridge startup on network reachability.
#
# Spec: docs/specs/2026-04-25-url-ytdlp-design.md §"entrypoint.sh".

# NB: do NOT use `set -e` here. The whole point of this script is
# best-effort: any failure in the self-update path (full disk, read-only
# /var, missing /var/log, transient touch error) must NOT prevent the
# bridge from starting. The exec at the bottom is the only thing that
# matters; everything above it is documented as "best-effort" and each
# write is individually guarded with `|| true`.

MARKER=/var/lib/ytdlp-last-update
mkdir -p "$(dirname "$MARKER")" 2>/dev/null || true

OLD_VER="$(yt-dlp --version 2>/dev/null || echo unknown)"

# Run the update if the marker is missing OR older than ~24h.
# `find -mmin +1440` matches files modified more than 1440 minutes
# (24h) ago. Using -mmin instead of -mtime because busybox -mtime
# uses integer days with strict-greater-than: -mtime +1 actually
# matches >48h, not >24h. -mmin +1440 gives the spec-intended
# ~24h cadence.
should_update=1
if [ -f "$MARKER" ]; then
    if [ -z "$(find "$MARKER" -mmin +1440 2>/dev/null)" ]; then
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
        touch "$MARKER" 2>/dev/null || true
    else
        echo "yt-dlp self-update failed (using bundled $OLD_VER)" >&2
    fi
else
    echo "yt-dlp self-update skipped (last attempt < 24h ago, version $OLD_VER)" >&2
fi

exec /usr/local/bin/mister-groovy-relay --config /config/config.toml "$@"
