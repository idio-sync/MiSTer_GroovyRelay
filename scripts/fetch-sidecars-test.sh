#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cleanup_empty_dist() {
  rmdir "${ROOT}/dist/sidecar-cache" "${ROOT}/dist/sidecars" "${ROOT}/dist" 2>/dev/null || true
}
trap cleanup_empty_dist EXIT

case "$(uname -s)" in
  Darwin)
    echo "fetch-sidecars-test: skipping non-Darwin guard test on macOS"
    exit 0
    ;;
esac

status=0
output="$(
  MISTER_GROOVY_SIDECAR_TARGETS=darwin_amd64 \
    bash "${ROOT}/scripts/fetch-sidecars.sh" 2>&1
)" || status=$?

if [ "$status" -eq 0 ]; then
  echo "fetch-sidecars-test: expected darwin_amd64 request to fail on non-macOS" >&2
  exit 1
fi

case "$output" in
  *"macOS FFmpeg sidecars must be built on macOS"*) ;;
  *)
    echo "fetch-sidecars-test: unexpected failure output" >&2
    printf '%s\n' "$output" >&2
    exit 1
    ;;
esac

echo "fetch-sidecars-test: non-Darwin macOS source-build guard passed"
