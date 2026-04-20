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
RUN apk add --no-cache ffmpeg ca-certificates tzdata
COPY --from=build /out/mister-groovy-relay /usr/local/bin/mister-groovy-relay
COPY config.example.toml /config/config.example.toml
VOLUME /config
EXPOSE 32500/tcp
EXPOSE 32412/udp
ENTRYPOINT ["/usr/local/bin/mister-groovy-relay", "--config", "/config/config.toml"]
