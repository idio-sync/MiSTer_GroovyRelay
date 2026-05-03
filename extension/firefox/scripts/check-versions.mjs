import fs from "node:fs";

const pkg = JSON.parse(fs.readFileSync("package.json", "utf8"));
const manifest = JSON.parse(fs.readFileSync("manifest.json", "utf8"));

const errors = [];

if (pkg.version !== manifest.version) {
  errors.push(
    `package.json version ${pkg.version} does not match manifest.json version ${manifest.version}`
  );
}

const refName = process.env.GITHUB_REF_NAME || "";
if (refName.startsWith("v")) {
  const tagVersion = refName.slice(1);
  if (tagVersion !== pkg.version) {
    errors.push(`release tag ${refName} does not match extension version ${pkg.version}`);
  }
}

if (errors.length > 0) {
  for (const error of errors) {
    console.error(`check-versions: ${error}`);
  }
  process.exit(1);
}

console.log(`check-versions: extension version ${pkg.version}`);
