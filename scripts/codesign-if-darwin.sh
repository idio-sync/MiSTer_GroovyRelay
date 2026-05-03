#!/usr/bin/env bash
set -euo pipefail

binary="${1:-}"
target="${2:-}"

case "$target" in
  darwin_*) ;;
  *) exit 0 ;;
esac

[ -n "$binary" ] || {
  echo "codesign-if-darwin: missing binary path" >&2
  exit 1
}
command -v codesign >/dev/null 2>&1 || {
  echo "codesign-if-darwin: codesign not found on PATH" >&2
  exit 1
}

arch="${target#darwin_}"
sidecar_dir="dist/sidecars/darwin_${arch}"

sign_one() {
  local path="$1"
  [ -f "$path" ] || return 0
  chmod +x "$path"
  codesign --sign - --force "$path"
}

sign_one "$binary"
for name in ffmpeg ffprobe yt-dlp; do
  sign_one "${sidecar_dir}/${name}"
done
