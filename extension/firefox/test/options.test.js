import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { initOptionsPage, validateBridgeURL, testConnection } from "../src/options/options.js";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function optionsMarkup() {
  return `
    <input type="text" id="bridge-url">
    <button type="button" id="save">Save</button>
    <button type="button" id="test">Test</button>
    <div id="status"></div>
  `;
}

async function flushEvents() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

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
    expect(validateBridgeURL("http://192.168.1.50:32500/ui/companion/status")).toEqual({
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
  it("returns ok:true on 200 from /ui/companion/status with the extension header", async () => {
    let captured;
    server.use(
      http.get("http://192.168.1.50:32500/ui/companion/status", ({ request }) => {
        captured = { headers: Object.fromEntries(request.headers) };
        return HttpResponse.json({ ok: true, health: { bridge: "online" } });
      })
    );
    const result = await testConnection("http://192.168.1.50:32500");
    expect(result).toEqual({ ok: true });
    expect(captured.headers["x-bridge-extension"]).toBe("1");
  });

  it("requests bridge host permission before testing when it is missing", async () => {
    browser.permissions.contains.mockResolvedValueOnce(false);
    browser.permissions.request.mockResolvedValueOnce(true);
    server.use(
      http.get("http://192.168.1.50:32500/ui/companion/status", () => {
        return HttpResponse.json({ ok: true });
      })
    );

    const result = await testConnection("http://192.168.1.50:32500");

    expect(result).toEqual({ ok: true });
    expect(browser.permissions.contains).toHaveBeenCalledWith({
      origins: ["http://192.168.1.50/*"],
    });
    expect(browser.permissions.request).toHaveBeenCalledWith({
      origins: ["http://192.168.1.50/*"],
    });
  });

  it("returns a permission error when bridge host permission is denied", async () => {
    browser.permissions.contains.mockResolvedValueOnce(false);
    browser.permissions.request.mockResolvedValueOnce(false);
    server.use(
      http.get("http://192.168.1.50:32500/ui/companion/status", () => {
        return HttpResponse.json({ ok: true });
      })
    );

    const result = await testConnection("http://192.168.1.50:32500");

    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/permission denied/i);
    expect(browser.permissions.request).toHaveBeenCalledWith({
      origins: ["http://192.168.1.50/*"],
    });
  });

  it("returns ok:false with status code on 404", async () => {
    server.use(
      http.get("http://192.168.1.50:32500/ui/companion/status", () => {
        return HttpResponse.json({ ok: false, error: "not found" }, { status: 404 });
      })
    );
    const result = await testConnection("http://192.168.1.50:32500");
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/404/);
  });

  it("returns ok:false on network error", async () => {
    server.use(
      http.get("http://192.168.1.50:32500/ui/companion/status", () => {
        return HttpResponse.error();
      })
    );
    const result = await testConnection("http://192.168.1.50:32500");
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/unreachable/i);
  });

  it("suggests http when an https bridge URL cannot be reached", async () => {
    server.use(
      http.get("https://192.168.1.50:32500/ui/companion/status", () => {
        return HttpResponse.error();
      })
    );
    const result = await testConnection("https://192.168.1.50:32500");
    expect(result.ok).toBe(false);
    expect(result.error).toContain("built-in relay listens on HTTP");
    expect(result.error).toContain("http://192.168.1.50:32500");
  });

  it("explains that localhost means the browser machine", async () => {
    server.use(
      http.get("http://127.0.0.1:32500/ui/companion/status", () => {
        return HttpResponse.error();
      })
    );
    const result = await testConnection("http://127.0.0.1:32500");
    expect(result.ok).toBe(false);
    expect(result.error).toContain("127.0.0.1 points at this browser");
    expect(result.error).toContain("relay's LAN IP");
  });

  it("returns ok:false when bridgeURL is empty", async () => {
    const result = await testConnection("");
    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/bridge not configured/i);
  });
});

describe("initOptionsPage", () => {
  it("requests bridge host permission before saving a bridge URL", async () => {
    document.body.innerHTML = optionsMarkup();
    browser.permissions.contains.mockResolvedValueOnce(false);
    browser.permissions.request.mockResolvedValueOnce(true);
    await initOptionsPage(document);

    document.getElementById("bridge-url").value = "http://192.168.1.50:32500";
    document.getElementById("save").click();
    await flushEvents();

    expect(browser.permissions.contains).toHaveBeenCalledWith({
      origins: ["http://192.168.1.50/*"],
    });
    expect(browser.permissions.request).toHaveBeenCalledWith({
      origins: ["http://192.168.1.50/*"],
    });
    expect(browser.storage.sync.set).toHaveBeenCalledWith({
      bridgeURL: "http://192.168.1.50:32500",
    });
    expect(document.getElementById("status").textContent).toBe("Saved.");
  });
});
