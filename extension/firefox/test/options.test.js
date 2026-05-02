import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { validateBridgeURL, testConnection } from "../src/options/options.js";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

describe("validateBridgeURL", () => {
  it("accepts http URL", () => {
    expect(validateBridgeURL("http://192.168.1.50:32500")).toEqual({
      ok: true,
      url: "http://192.168.1.50:32500",
    });
  });

  it("accepts https URL", () => {
    expect(validateBridgeURL("https://bridge.lan:32500")).toEqual({
      ok: true,
      url: "https://bridge.lan:32500",
    });
  });

  it("trims whitespace", () => {
    expect(validateBridgeURL("  http://192.168.1.50:32500  ")).toEqual({
      ok: true,
      url: "http://192.168.1.50:32500",
    });
  });

  it("strips trailing slash", () => {
    expect(validateBridgeURL("http://192.168.1.50:32500/")).toEqual({
      ok: true,
      url: "http://192.168.1.50:32500",
    });
  });

  it("normalizes a pasted bridge UI path to the origin", () => {
    expect(validateBridgeURL("http://192.168.1.50:32500/ui/adapter/url/panel")).toEqual({
      ok: true,
      url: "http://192.168.1.50:32500",
    });
  });

  it("strips query strings and fragments by normalizing to the origin", () => {
    expect(validateBridgeURL("http://bridge.lan:32500/?x=1#settings")).toEqual({
      ok: true,
      url: "http://bridge.lan:32500",
    });
  });

  it("returns ok with empty url for empty input", () => {
    expect(validateBridgeURL("")).toEqual({ ok: true, url: "" });
    expect(validateBridgeURL("   ")).toEqual({ ok: true, url: "" });
  });

  it("rejects malformed URL", () => {
    const r = validateBridgeURL("not a url");
    expect(r.ok).toBe(false);
    expect(r.error).toMatch(/invalid url/i);
  });

  it("rejects non-http/https scheme", () => {
    const r = validateBridgeURL("file:///etc/passwd");
    expect(r.ok).toBe(false);
    expect(r.error).toMatch(/scheme/i);
  });

  it("rejects ftp scheme", () => {
    const r = validateBridgeURL("ftp://example.com");
    expect(r.ok).toBe(false);
    expect(r.error).toMatch(/scheme/i);
  });
});

describe("testConnection", () => {
  it("returns ok:true on 200 from /ui/adapter/url/panel", async () => {
    server.use(
      http.get("http://192.168.1.50:32500/ui/adapter/url/panel", () => {
        return new HttpResponse("<div>panel</div>", { status: 200 });
      })
    );
    const result = await testConnection("http://192.168.1.50:32500");
    expect(result).toEqual({ ok: true });
  });

  it("returns ok:false with status code on 404", async () => {
    server.use(
      http.get("http://192.168.1.50:32500/ui/adapter/url/panel", () => {
        return new HttpResponse("not found", { status: 404 });
      })
    );
    const result = await testConnection("http://192.168.1.50:32500");
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/404/);
  });

  it("returns ok:false on network error", async () => {
    server.use(
      http.get("http://192.168.1.50:32500/ui/adapter/url/panel", () => {
        return HttpResponse.error();
      })
    );
    const result = await testConnection("http://192.168.1.50:32500");
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/unreachable/i);
  });

  it("returns ok:false when bridgeURL is empty", async () => {
    const result = await testConnection("");
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/bridge not configured/i);
  });
});
