# Multi-stage build for the MiSTer GroovyRelay bridge.
#
# Stage 1 (build): compile a fully-static Linux binary from the vendored
# Go source. CGO_ENABLED=0 so the result has no libc dependency and can
# run on a bare alpine runtime.
#
# Stage 2 (runtime): alpine with ffmpeg (and ffprobe — same apk package)
# installed. The bridge shells out to ffmpeg for the video/audio
# pipeline; ca-certificates is required for plex.tv TLS; tzdata makes
# scheduled-recording log timestamps legible.
#
# Expected host-networking deployment: docker run --network=host so the
# stable source UDP port (config.source_port, default 32101) is
# reachable at the MiSTer's IP-level session key and GDM multicast on
# 239.0.0.250:32414 works. The EXPOSE directives below document the
# ports the bridge uses when someone chooses bridged networking — they
# do not publish by themselves.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/mister-groovy-relay ./cmd/mister-groovy-relay

FROM alpine:3.20
RUN apk add --no-cache ffmpeg ca-certificates tzdata curl
# Install the yt-dlp_linux static binary. Bundles its own Python via
# zipapp + standalone interpreter — no python3/py3-pip apk packages
# needed. Native `yt-dlp -U` works for in-place self-update (used by
# entrypoint.sh on container start). +12 MiB image growth.
#
# Pinned to "latest" at build time; entrypoint.sh refreshes daily.
# Spec: docs/specs/2026-04-25-url-ytdlp-design.md §"Distribution and
# self-update".
# TARGETARCH is set automatically by buildx (amd64, arm64, arm, ...).
# yt-dlp ships per-arch standalone binaries; picking the wrong one yields
# a confusing "/bin/sh: yt-dlp: not found" at exec time (ELF arch
# mismatch, not a missing file).
ARG TARGETARCH
RUN case "$TARGETARCH" in \
      amd64) ytdlp_asset=yt-dlp_linux ;; \
      arm64) ytdlp_asset=yt-dlp_linux_aarch64 ;; \
      arm)   ytdlp_asset=yt-dlp_linux_armv7l ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac \
    && curl -fsSL -o /usr/local/bin/yt-dlp \
         "https://github.com/yt-dlp/yt-dlp/releases/latest/download/${ytdlp_asset}" \
    && chmod +x /usr/local/bin/yt-dlp \
    && /usr/local/bin/yt-dlp --version
COPY --from=build /out/mister-groovy-relay /usr/local/bin/mister-groovy-relay
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
# /config/config.toml is auto-created from the embedded example on first
# run when missing — no COPY here. A COPY into /config would be shadowed
# by the operator's bind mount anyway.
VOLUME /config
EXPOSE 32500/tcp
EXPOSE 32412/udp
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
