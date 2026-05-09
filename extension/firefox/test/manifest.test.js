import { describe, expect, it } from "vitest";
import fs from "node:fs";

const manifest = JSON.parse(fs.readFileSync("manifest.json", "utf8"));

describe("manifest data collection declaration", () => {
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
});
