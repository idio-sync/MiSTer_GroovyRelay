import { describe, expect, it } from "vitest";
import fs from "node:fs";

const manifest = JSON.parse(fs.readFileSync("manifest.json", "utf8"));
const extensionFiles = [
  "src/fonts/InterTight-400.woff2",
  "src/fonts/InterTight-500.woff2",
  "src/fonts/JetBrainsMono-400.woff2",
  "src/fonts/SpaceGrotesk-600.woff2",
];

describe("manifest data collection declaration", () => {
  it("injects the Plex timeline bridge on Plex Web", () => {
    expect(manifest.content_scripts).toContainEqual({
      matches: ["https://app.plex.tv/desktop/*"],
      js: ["src/content/plex-timeline-content.js"],
      run_at: "document_start",
    });
  });

  it("exposes the page-context Plex timeline shim to Plex Web", () => {
    expect(manifest.web_accessible_resources).toContainEqual({
      resources: ["src/content/plex-timeline-page.js"],
      matches: ["https://app.plex.tv/*"],
    });
  });

  it("declares optional bridge host permissions for runtime requests", () => {
    expect(manifest.optional_host_permissions).toEqual(["http://*/*", "https://*/*"]);
  });

  it("declares the URL data sent to the bridge for AMO signing", () => {
    expect(
      manifest.browser_specific_settings?.gecko?.data_collection_permissions
    ).toEqual({
      required: ["browsingActivity", "websiteContent"],
    });
  });

  it("requires a Firefox version that supports built-in data collection consent", () => {
    expect(manifest.browser_specific_settings?.gecko?.strict_min_version).toBe("140.0");
  });

  it("bundles popup font assets locally", () => {
    for (const file of extensionFiles) {
      expect(fs.existsSync(file), file).toBe(true);
    }
  });
});
