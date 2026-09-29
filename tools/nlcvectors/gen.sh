#!/usr/bin/env bash
# gen.sh: regenerate the GroovyNLC golden test vectors in
# internal/groovy/nlc/testdata/ from the vendored C++ reference codec.
#
# Run from the repo root:
#   bash tools/nlcvectors/gen.sh
# or:
#   make nlc-vectors
#
# Requires: a C++17 compiler (clang++ by default, override with $CXX),
# gzip, sha256sum, python3, ffmpeg. See tools/nlcvectors/README.md.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TOOLS_DIR="$REPO_ROOT/tools/nlcvectors"
TESTDATA_DIR="$REPO_ROOT/internal/groovy/nlc/testdata"

CXX="${CXX:-clang++}"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

BIN="$WORK_DIR/nlcvectors"
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*|Windows_NT)
        BIN="$BIN.exe"
        PTHREAD_FLAG=()
        ;;
    *)
        PTHREAD_FLAG=(-pthread)
        ;;
esac

echo "==> compiling nlcvectors harness ($CXX)"
"$CXX" -std=c++17 -O2 "${PTHREAD_FLAG[@]}" \
    "$TOOLS_DIR/main.cpp" "$TOOLS_DIR/nlc_codec.cpp" \
    -o "$BIN"

INPUTS_DIR="$WORK_DIR/inputs"
mkdir -p "$INPUTS_DIR"

echo "==> generating synthetic inputs"
"$BIN" gen flat      96  24 "$INPUTS_DIR/flat.rgb"
"$BIN" gen gradient  96  24 "$INPUTS_DIR/gradient.rgb"
"$BIN" gen edges     96  24 "$INPUTS_DIR/edges.rgb"
"$BIN" gen noise     96  24 "$INPUTS_DIR/noise.rgb"
"$BIN" gen primaries 96  24 "$INPUTS_DIR/primaries.rgb"
"$BIN" gen spikes    96  24 "$INPUTS_DIR/spikes.rgb"
"$BIN" gen gradient  250  7 "$INPUTS_DIR/odd.rgb"

echo "==> capturing a real-looking field with ffmpeg"
ffmpeg -v error -f lavfi -i testsrc2=size=720x240:rate=1 -frames:v 1 \
    -pix_fmt rgb24 -f rawvideo "$INPUTS_DIR/field.rgb"

# name -> "width height"
declare -A DIMS=(
    [flat]="96 24"
    [gradient]="96 24"
    [edges]="96 24"
    [noise]="96 24"
    [primaries]="96 24"
    [spikes]="96 24"
    [odd]="250 7"
    [field]="720 240"
)
NAMES=(flat gradient edges noise primaries spikes odd field)

echo "==> clearing stale testdata"
rm -rf "$TESTDATA_DIR/inputs" "$TESTDATA_DIR/encoded" "$TESTDATA_DIR/vectors.json"
mkdir -p "$TESTDATA_DIR/inputs" "$TESTDATA_DIR/encoded"

echo "==> writing gzip'd inputs"
for name in "${NAMES[@]}"; do
    gzip -n -9 -c "$INPUTS_DIR/$name.rgb" > "$TESTDATA_DIR/inputs/$name.rgb.gz"
done

RUN_DIR="$WORK_DIR/run"
mkdir -p "$RUN_DIR"

VECTORS_JSON="$WORK_DIR/vectors.json"
: > "$WORK_DIR/vectors.jsonl"

echo "==> running the reference codec over the case matrix"
for name in "${NAMES[@]}"; do
    read -r width height <<<"${DIMS[$name]}"
    for near in 0 1 2 3; do
        for pack in tiled rice; do
            case_name="${name}_n${near}_${pack}"
            enc_out="$RUN_DIR/$case_name.nlc"
            dec_out="$RUN_DIR/$case_name.decoded"

            "$BIN" run "$INPUTS_DIR/$name.rgb" "$width" "$height" "$near" "$pack" \
                "$enc_out" "$dec_out"

            encoded_size=$(wc -c < "$enc_out" | tr -d ' ')
            encoded_sha256=$(sha256sum "$enc_out" | cut -d' ' -f1)
            decoded_sha256=$(sha256sum "$dec_out" | cut -d' ' -f1)

            gzip -n -9 -c "$enc_out" > "$TESTDATA_DIR/encoded/$case_name.nlc.gz"

            printf '{"name":"%s","input":"%s","width":%s,"height":%s,"near":%s,"pack":"%s","encoded_size":%s,"encoded_sha256":"%s","decoded_sha256":"%s"}\n' \
                "$case_name" "$name" "$width" "$height" "$near" "$pack" \
                "$encoded_size" "$encoded_sha256" "$decoded_sha256" \
                >> "$WORK_DIR/vectors.jsonl"
        done
    done
done

echo "==> writing vectors.json"
python3 - "$WORK_DIR/vectors.jsonl" "$TESTDATA_DIR/vectors.json" <<'PY'
import json
import sys

src, dst = sys.argv[1], sys.argv[2]
with open(src, "r", encoding="utf-8") as f:
    records = [json.loads(line) for line in f if line.strip()]
records.sort(key=lambda r: r["name"])

keys = ["name", "input", "width", "height", "near", "pack",
        "encoded_size", "encoded_sha256", "decoded_sha256"]
ordered = [{k: r[k] for k in keys} for r in records]

with open(dst, "w", encoding="utf-8", newline="\n") as f:
    json.dump(ordered, f, indent=2)
    f.write("\n")
PY

count=$(python3 -c "import json, sys; print(len(json.load(open(sys.argv[1]))))" \
    "$TESTDATA_DIR/vectors.json")
echo "==> done: $count cases written to $TESTDATA_DIR"
