#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

if command -v node >/dev/null 2>&1; then
  NODE_BIN=node
elif command -v node.exe >/dev/null 2>&1; then
  NODE_BIN=node.exe
else
  echo "build.sh: node or node.exe is required" >&2
  exit 1
fi

rm -rf dist
mkdir -p dist

"$NODE_BIN" --input-type=module <<'NODE'
import fs from "node:fs";
import path from "node:path";

const pkg = JSON.parse(fs.readFileSync("package.json", "utf8"));
const name = `companion-extension-${pkg.version}`;
const out = `dist/${name}.zip`;
const includes = ["manifest.json", "src", "icons"];

for (const item of includes) {
  if (!fs.existsSync(item)) {
    console.error(`build.sh: missing required path: ${item}`);
    process.exit(1);
  }
}

function walk(entry) {
  const st = fs.statSync(entry);
  if (st.isDirectory()) {
    return fs.readdirSync(entry)
      .flatMap((child) => walk(path.join(entry, child)));
  }
  return [entry.replaceAll(path.sep, "/")];
}

const files = includes.flatMap(walk).sort();

function crc32(buf) {
  let c = -1;
  for (let i = 0; i < buf.length; i++) {
    c = (c >>> 8) ^ table[(c ^ buf[i]) & 0xff];
  }
  return (c ^ -1) >>> 0;
}

const table = new Uint32Array(256);
for (let n = 0; n < 256; n++) {
  let c = n;
  for (let k = 0; k < 8; k++) {
    c = (c & 1) ? (0xedb88320 ^ (c >>> 1)) : (c >>> 1);
  }
  table[n] = c >>> 0;
}

function u16(n) {
  const b = Buffer.alloc(2);
  b.writeUInt16LE(n);
  return b;
}

function u32(n) {
  const b = Buffer.alloc(4);
  b.writeUInt32LE(n >>> 0);
  return b;
}

let offset = 0;
const local = [];
const central = [];

for (const file of files) {
  const data = fs.readFileSync(file);
  const nameBuf = Buffer.from(file);
  const crc = crc32(data);

  const localHeader = Buffer.concat([
    u32(0x04034b50),
    u16(20),
    u16(0),
    u16(0),
    u16(0),
    u16(0),
    u32(crc),
    u32(data.length),
    u32(data.length),
    u16(nameBuf.length),
    u16(0),
    nameBuf,
  ]);
  local.push(localHeader, data);

  central.push(Buffer.concat([
    u32(0x02014b50),
    u16(20),
    u16(20),
    u16(0),
    u16(0),
    u16(0),
    u16(0),
    u32(crc),
    u32(data.length),
    u32(data.length),
    u16(nameBuf.length),
    u16(0),
    u16(0),
    u16(0),
    u16(0),
    u32(0),
    u32(offset),
    nameBuf,
  ]));

  offset += localHeader.length + data.length;
}

const centralDir = Buffer.concat(central);
const end = Buffer.concat([
  u32(0x06054b50),
  u16(0),
  u16(0),
  u16(files.length),
  u16(files.length),
  u32(centralDir.length),
  u32(offset),
  u16(0),
]);

fs.writeFileSync(out, Buffer.concat([...local, centralDir, end]));
fs.copyFileSync(out, `dist/${name}.xpi`);

console.log(`Built:`);
console.log(`  dist/${name}.zip  (Chrome / Edge / unpacked-load)`);
console.log(`  dist/${name}.xpi  (Firefox AMO submission)`);
NODE
