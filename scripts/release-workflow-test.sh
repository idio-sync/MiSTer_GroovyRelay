#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKFLOW="${ROOT}/.github/workflows/release.yml"

if grep -Eq '^[[:space:]]*runs-on:[[:space:]]*macos-latest[[:space:]]*$' "$WORKFLOW"; then
  echo "release-workflow-test: release job must not use mutable macos-latest" >&2
  exit 1
fi

if ! grep -Eq '^[[:space:]]*runs-on:[[:space:]]*macos-15-intel[[:space:]]*$' "$WORKFLOW"; then
  echo "release-workflow-test: release job must run on macos-15-intel" >&2
  exit 1
fi

echo "release-workflow-test: release runner label is pinned to Intel macOS"
