#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="${ROOT}/scripts/sidecar-manifest.txt"
OUT_DIR="${ROOT}/dist/sidecars"
CACHE_DIR="${ROOT}/dist/sidecar-cache"
WORK_DIR="${ROOT}/dist/sidecar-work"

TARGETS="${MISTER_GROOVY_SIDECAR_TARGETS:-linux_amd64 linux_arm64 windows_amd64 darwin_amd64 darwin_arm64}"
TOOLS="ffmpeg ffprobe yt-dlp"

FFMPEG_MACOS_VERSION="${FFMPEG_MACOS_VERSION:-8.1}"
FFMPEG_MACOS_SOURCE_URL="${FFMPEG_MACOS_SOURCE_URL:-https://ffmpeg.org/releases/ffmpeg-${FFMPEG_MACOS_VERSION}.tar.xz}"
FFMPEG_MACOS_SOURCE_SHA256="${FFMPEG_MACOS_SOURCE_SHA256:-b072aed6871998cce9b36e7774033105ca29e33632be5b6347f3206898e0756a}"

die() {
  echo "fetch-sidecars: $*" >&2
  exit 1
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

manifest_has() {
  local target="$1"
  local tool="$2"
  awk -F'|' -v target="$target" -v tool="$tool" '
    $1 == target && $2 == tool { found = 1 }
    END { exit(found ? 0 : 1) }
  ' "$MANIFEST"
}

is_macos_source_tool() {
  local target="$1"
  local tool="$2"
  case "${target}/${tool}" in
    darwin_*/ffmpeg|darwin_*/ffprobe) return 0 ;;
    *) return 1 ;;
  esac
}

validate_manifest_complete() {
  local missing=0
  for target in $TARGETS; do
    for tool in $TOOLS; do
      if ! manifest_has "$target" "$tool"; then
        if is_macos_source_tool "$target" "$tool"; then
          continue
        fi
        echo "fetch-sidecars: missing manifest row for ${target}/${tool}" >&2
        missing=1
      fi
    done
  done
  if [ "$missing" -ne 0 ] && [ "${MISTER_GROOVY_ALLOW_INCOMPLETE_SIDECARS:-}" != "1" ]; then
    die "sidecar manifest incomplete; set MISTER_GROOVY_ALLOW_INCOMPLETE_SIDECARS=1 only for local partial testing"
  fi
}

validate_url_is_pinned() {
  local url="$1"
  local base
  base="$(basename "$url")"
  case "$url" in
    */latest/*|*/latest/download/*)
      die "moving release URL is not allowed: $url"
      ;;
  esac
  case "$base" in
    *latest*)
      die "moving asset filename is not allowed: $url"
      ;;
  esac
}

download_asset() {
  local url="$1"
  local want_sha="$2"
  local base="$3"
  local dest="${CACHE_DIR}/${want_sha}-${base}"
  local tmp="${dest}.tmp"

  if [ -f "$dest" ]; then
    local got
    got="$(sha256_file "$dest")"
    if [ "$got" = "$want_sha" ]; then
      printf '%s\n' "$dest"
      return 0
    fi
    rm -f "$dest"
  fi

  echo "fetch-sidecars: downloading $base" >&2
  if ! curl -fL --retry 3 --connect-timeout 20 --max-time 600 -o "$tmp" "$url"; then
    rm -f "$tmp"
    if [ -n "${MISTER_GROOVY_SIDECAR_CACHE_BASE:-}" ]; then
      local cached_base
      for cached_base in "${want_sha}-${base}" "$base"; do
        local fallback="${MISTER_GROOVY_SIDECAR_CACHE_BASE%/}/${cached_base}"
        echo "fetch-sidecars: upstream failed; trying cache $fallback" >&2
        if curl -fL --retry 2 --connect-timeout 20 --max-time 600 -o "$tmp" "$fallback"; then
          break
        fi
        rm -f "$tmp"
      done
      [ -f "$tmp" ] || die "download failed for $url and cache fallback"
    else
      die "download failed for $url"
    fi
  fi

  local got
  got="$(sha256_file "$tmp")"
  if [ "$got" != "$want_sha" ]; then
    rm -f "$tmp"
    die "checksum mismatch for $url: got $got want $want_sha"
  fi
  mv "$tmp" "$dest"
  printf '%s\n' "$dest"
}

macos_source_build_needed() {
  local target
  for target in $TARGETS; do
    case "$target" in
      darwin_amd64|darwin_arm64)
        if ! manifest_has "$target" ffmpeg || ! manifest_has "$target" ffprobe; then
          return 0
        fi
        ;;
    esac
  done
  return 1
}

macos_make_jobs() {
  sysctl -n hw.ncpu 2>/dev/null || printf '2\n'
}

build_one_macos_ffmpeg() {
  local target="$1"
  local archive="$2"
  local ffmpeg_arch
  local clang_arch
  local min_version

  case "$target" in
    darwin_amd64)
      ffmpeg_arch="x86_64"
      clang_arch="x86_64"
      min_version="10.13"
      ;;
    darwin_arm64)
      ffmpeg_arch="aarch64"
      clang_arch="arm64"
      min_version="11.0"
      ;;
    *) return 0 ;;
  esac

  local dest_dir="${OUT_DIR}/${target}"
  if [ -x "${dest_dir}/ffmpeg" ] && [ -x "${dest_dir}/ffprobe" ]; then
    return 0
  fi

  local src_dir="${WORK_DIR}/ffmpeg-${FFMPEG_MACOS_VERSION}-${target}"
  local sdkroot
  local jobs

  rm -rf "$src_dir"
  mkdir -p "$src_dir" "$dest_dir"
  tar -xJf "$archive" -C "$src_dir" --strip-components=1

  sdkroot="$(xcrun --sdk macosx --show-sdk-path)"
  jobs="$(macos_make_jobs)"

  echo "fetch-sidecars: building ffmpeg/ffprobe ${FFMPEG_MACOS_VERSION} for ${target}" >&2
  (
    cd "$src_dir"
    configure_args=(
      --prefix="${src_dir}/prefix"
      --arch="$ffmpeg_arch"
      --target-os=darwin
      --enable-cross-compile
      --cc=clang
      --pkg-config=false
      --extra-cflags="-isysroot ${sdkroot} -arch ${clang_arch} -mmacosx-version-min=${min_version}"
      --extra-ldflags="-isysroot ${sdkroot} -arch ${clang_arch} -mmacosx-version-min=${min_version}"
      --disable-autodetect
      --disable-debug
      --disable-doc
      --disable-ffplay
      --disable-gpl
      --disable-nonfree
      --disable-version3
      --disable-shared
      --enable-static
      --enable-securetransport
      --enable-zlib
    )
    if [ "$target" = "darwin_amd64" ]; then
      configure_args+=(--disable-x86asm)
    fi
    ./configure "${configure_args[@]}"
    make -j"$jobs" ffmpeg ffprobe
  )

  cp "${src_dir}/ffmpeg" "${dest_dir}/ffmpeg"
  cp "${src_dir}/ffprobe" "${dest_dir}/ffprobe"
  chmod 0755 "${dest_dir}/ffmpeg" "${dest_dir}/ffprobe"
  check_output "$target" ffmpeg
  check_output "$target" ffprobe
  echo "fetch-sidecars: ${target}/ffmpeg and ffprobe from FFmpeg ${FFMPEG_MACOS_VERSION} source (LGPL-2.1-or-later)" >&2
}

build_macos_ffmpeg_sidecars() {
  macos_source_build_needed || return 0

  case "$(uname -s)" in
    Darwin) ;;
    *)
      if [ "${MISTER_GROOVY_ALLOW_INCOMPLETE_SIDECARS:-}" = "1" ]; then
        echo "fetch-sidecars: skipping macOS FFmpeg source build outside macOS for local partial testing" >&2
        return 0
      fi
      die "macOS FFmpeg sidecars must be built on macOS; run this script on a macOS release runner or provide pinned manifest rows"
      ;;
  esac

  command -v clang >/dev/null 2>&1 || die "clang is required to build macOS FFmpeg sidecars"
  command -v make >/dev/null 2>&1 || die "make is required to build macOS FFmpeg sidecars"
  command -v tar >/dev/null 2>&1 || die "tar is required to build macOS FFmpeg sidecars"
  command -v xcrun >/dev/null 2>&1 || die "xcrun is required to build macOS FFmpeg sidecars"

  validate_url_is_pinned "$FFMPEG_MACOS_SOURCE_URL"
  local base
  local source_archive
  base="$(basename "$FFMPEG_MACOS_SOURCE_URL")"
  source_archive="$(download_asset "$FFMPEG_MACOS_SOURCE_URL" "$FFMPEG_MACOS_SOURCE_SHA256" "$base")"

  local target
  for target in $TARGETS; do
    case "$target" in
      darwin_amd64|darwin_arm64)
        if ! manifest_has "$target" ffmpeg || ! manifest_has "$target" ffprobe; then
          build_one_macos_ffmpeg "$target" "$source_archive"
        fi
        ;;
    esac
  done
}

extract_tool() {
  local archive="$1"
  local target="$2"
  local tool="$3"
  local output="$4"
  local dest_dir="${OUT_DIR}/${target}"
  local dest="${dest_dir}/${output}"
  local base
  base="$(basename "$archive")"

  mkdir -p "$dest_dir"
  rm -f "$dest"

  case "$base" in
    *.zip)
      rm -rf "$WORK_DIR"
      mkdir -p "$WORK_DIR"
      unzip -q "$archive" -d "$WORK_DIR"
      ;;
    *.tar.xz)
      rm -rf "$WORK_DIR"
      mkdir -p "$WORK_DIR"
      tar -xJf "$archive" -C "$WORK_DIR"
      ;;
    *)
      cp "$archive" "$dest"
      chmod 0755 "$dest"
      return 0
      ;;
  esac

  local search="$tool"
  case "$output" in
    *.exe) search="$output" ;;
  esac

  local src
  src="$(find "$WORK_DIR" -type f -name "$search" | head -n 1 || true)"
  if [ -z "$src" ]; then
    die "could not find $search inside $base"
  fi
  cp "$src" "$dest"

  case "$target" in
    windows_*) ;;
    *) chmod 0755 "$dest" ;;
  esac
}

check_output() {
  local target="$1"
  local output="$2"
  local path="${OUT_DIR}/${target}/${output}"
  if [ ! -s "$path" ]; then
    die "missing sidecar output: $path"
  fi
  case "$target" in
    windows_*)
      case "$output" in
        *.exe) ;;
        *) die "Windows sidecar must end in .exe: $path" ;;
      esac
      ;;
    *)
      if [ ! -x "$path" ]; then
        die "sidecar is not executable: $path"
      fi
      ;;
  esac
}

mkdir -p "$OUT_DIR" "$CACHE_DIR"
validate_manifest_complete
build_macos_ffmpeg_sidecars

while IFS='|' read -r target tool version url sha license output extra; do
  case "$target" in
    ''|\#*) continue ;;
  esac
  [ -z "${extra:-}" ] || die "too many fields in manifest row for ${target}/${tool}"
  [ -n "$output" ] || die "empty output field in manifest row for ${target}/${tool}"
  validate_url_is_pinned "$url"

  include=0
  for required in $TARGETS; do
    if [ "$required" = "$target" ]; then
      include=1
      break
    fi
  done
  [ "$include" -eq 1 ] || continue

  base="$(basename "$url")"
  asset="$(download_asset "$url" "$sha" "$base")"
  extract_tool "$asset" "$target" "$tool" "$output"
  check_output "$target" "$output"
  echo "fetch-sidecars: ${target}/${output} from ${version} (${license})" >&2
done < "$MANIFEST"

rm -rf "$WORK_DIR"

for target in $TARGETS; do
  for tool in $TOOLS; do
    if ! manifest_has "$target" "$tool" && ! is_macos_source_tool "$target" "$tool"; then
      continue
    fi
    if is_macos_source_tool "$target" "$tool" && [ "${MISTER_GROOVY_ALLOW_INCOMPLETE_SIDECARS:-}" = "1" ]; then
      if [ ! -e "${OUT_DIR}/${target}/${tool}" ]; then
        continue
      fi
    fi
    if is_macos_source_tool "$target" "$tool" && ! manifest_has "$target" "$tool"; then
      output="$tool"
      check_output "$target" "$output"
      continue
    fi
    output="$tool"
    case "$target" in
      windows_*) output="${tool}.exe" ;;
    esac
    check_output "$target" "$output"
  done
done
