import { describe, expect, it } from "vitest";
import { execFileSync } from "node:child_process";
import fs from "node:fs";

const pkg = JSON.parse(fs.readFileSync("package.json", "utf8"));
const workflow = fs.readFileSync("../../.github/workflows/release.yml", "utf8");
const goreleaser = fs.readFileSync("../../.goreleaser.yaml", "utf8");
const rootGitignore = fs.readFileSync("../../.gitignore", "utf8");
const gitignore = fs.readFileSync(".gitignore", "utf8");

describe("release workflow extension automation", () => {
  it("has npm scripts for AMO signing and version checks", () => {
    expect(pkg.scripts["check:versions"]).toBeTypeOf("string");
    expect(pkg.scripts["sign:amo"]).toBeTypeOf("string");
    expect(pkg.scripts["check:versions"]).toContain("scripts/check-versions.mjs");
    expect(pkg.scripts["sign:amo"]).toContain("web-ext sign");
    expect(pkg.scripts["sign:amo"]).toContain("--channel=unlisted");
    expect(pkg.scripts["sign:amo"]).toContain("--artifacts-dir=dist/amo-signed");
    expect(pkg.scripts["sign:amo"]).toContain("\"scripts/**\"");
    expect(pkg.scripts["sign:amo"]).toContain(".amo-upload-uuid");
  });

  it("signs the Firefox extension and uploads extension assets on release tags", () => {
    expect(workflow).toContain("AMO_JWT_ISSUER");
    expect(workflow).toContain("AMO_JWT_SECRET");
    expect(workflow).toContain("npm run sign:amo");
    expect(workflow).toContain("companion-extension-${version}-signed.xpi");
    expect(workflow).toContain("gh release upload");
  });

  it("uploads only the signed Firefox extension artifact", () => {
    expect(workflow).toContain("signed_asset=\"$release_dir/companion-extension-${version}-signed.xpi\"");
    expect(workflow).toContain("\"${{ steps.extension_assets.outputs.asset }}\"");
    expect(workflow).not.toContain("\"extension/firefox/dist/companion-extension-${version}.zip\"");
  });

  it("reuses the previous signed Firefox artifact when the extension payload is unchanged", () => {
    expect(workflow).toContain("id: extension_release");
    expect(workflow).toContain("git describe --tags --abbrev=0");
    expect(workflow).toContain("git diff --quiet \"$previous_tag\" \"$GITHUB_SHA\" --");
    expect(workflow).toContain("extension/firefox/manifest.json");
    expect(workflow).toContain("extension/firefox/src");
    expect(workflow).toContain("extension/firefox/icons");
    expect(workflow).toContain("if: steps.extension_release.outputs.changed == 'true'");
    expect(workflow).toContain("gh release download \"$previous_tag\"");
    expect(workflow).toContain("No signed Firefox XPI asset found on previous release");
  });

  it("signs with AMO only after GoReleaser succeeds", () => {
    expect(workflow.indexOf("goreleaser/goreleaser-action@v6")).toBeGreaterThan(-1);
    expect(workflow.indexOf("Sign Firefox extension through AMO")).toBeGreaterThan(
      workflow.indexOf("goreleaser/goreleaser-action@v6")
    );
  });

  it("allows project release tags to differ from the extension version", () => {
    const output = execFileSync(process.execPath, ["scripts/check-versions.mjs"], {
      cwd: process.cwd(),
      encoding: "utf8",
      env: {
        ...process.env,
        GITHUB_REF_NAME: "v1.0",
      },
    });

    expect(output).toContain(`project tag v1.0`);
    expect(output).toContain(`extension version ${pkg.version}`);
  });

  it("ignores the AMO upload UUID generated during CI signing", () => {
    expect(gitignore).toContain(".amo-upload-uuid");
  });

  it("publishes only platform archives from GoReleaser", () => {
    expect(goreleaser).toContain("dist: build/goreleaser");
    expect(goreleaser).toContain("dist/sidecars/{{ .Os }}_{{ .Arch }}/*");
    expect(goreleaser).not.toContain("dist/sidecar-cache/*");
    expect(goreleaser).not.toContain("extra_files:");
    expect(goreleaser).toContain("checksum:");
    expect(goreleaser).toContain("  disable: true");
    expect(goreleaser).not.toContain("checksums.txt");
    expect(rootGitignore).toContain("/build/goreleaser/");
  });
});
