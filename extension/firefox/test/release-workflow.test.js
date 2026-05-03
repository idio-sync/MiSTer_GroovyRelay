import { describe, expect, it } from "vitest";
import fs from "node:fs";

const pkg = JSON.parse(fs.readFileSync("package.json", "utf8"));
const workflow = fs.readFileSync("../../.github/workflows/release.yml", "utf8");

describe("release workflow extension automation", () => {
  it("has npm scripts for AMO signing and version checks", () => {
    expect(pkg.scripts["check:versions"]).toBeTypeOf("string");
    expect(pkg.scripts["sign:amo"]).toBeTypeOf("string");
    expect(pkg.scripts["check:versions"]).toContain("scripts/check-versions.mjs");
    expect(pkg.scripts["sign:amo"]).toContain("web-ext sign");
    expect(pkg.scripts["sign:amo"]).toContain("--channel=unlisted");
    expect(pkg.scripts["sign:amo"]).toContain("--artifacts-dir=dist/amo-signed");
    expect(pkg.scripts["sign:amo"]).toContain("\"scripts/**\"");
  });

  it("signs the Firefox extension and uploads extension assets on release tags", () => {
    expect(workflow).toContain("AMO_JWT_ISSUER");
    expect(workflow).toContain("AMO_JWT_SECRET");
    expect(workflow).toContain("npm run sign:amo");
    expect(workflow).toContain("companion-extension-${version}-signed.xpi");
    expect(workflow).toContain("gh release upload");
  });
});
