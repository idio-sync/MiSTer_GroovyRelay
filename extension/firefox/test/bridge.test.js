import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { http, HttpResponse, delay } from "msw";
import { setupServer } from "msw/node";
import {
  getBridgeURL,
  launchGroovyMister,
  play,
  _setTimeoutForTest,
} from "../src/lib/bridge.js";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  _setTimeoutForTest(undefined);
  server.resetHandlers();
});
afterAll(() => server.close());

describe("getBridgeURL", () => {
  it("returns empty string when no bridge URL is set", async () => {
    const url = await getBridgeURL();
    expect(url).toBe("");
  });

  it("returns stored bridgeURL", async () => {
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
    const url = await getBridgeURL();
    expect(url).toBe("http://192.168.1.50:32500");
  });
});

describe("play() happy path", () => {
  it("POSTs to /ui/adapter/url/play with the right headers and body", async () => {
    let captured;
    server.use(
      http.post("http://192.168.1.50:32500/ui/adapter/url/play", async ({ request }) => {
        captured = {
          headers: Object.fromEntries(request.headers),
          body: await request.json(),
        };
        return HttpResponse.json(
          { adapter_ref: "url:abc123", state: "running", url: "https://youtu.be/x" },
          { status: 202 }
        );
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await play("https://youtu.be/x");

    expect(result).toEqual({ ok: true, adapter_ref: "url:abc123" });
    expect(captured.headers["content-type"]).toBe("application/json");
    expect(captured.headers["x-bridge-extension"]).toBe("1");
    expect(captured.body).toEqual({ url: "https://youtu.be/x", mode: "auto" });
  });

  it("requests bridge host permission before POSTing when it is missing", async () => {
    browser.permissions.contains.mockResolvedValueOnce(false);
    browser.permissions.request.mockResolvedValueOnce(true);
    server.use(
      http.post("http://192.168.1.50:32500/ui/adapter/url/play", () => {
        return HttpResponse.json({ adapter_ref: "url:abc123" }, { status: 202 });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await play("https://youtu.be/x");

    expect(result).toEqual({ ok: true, adapter_ref: "url:abc123" });
    expect(browser.permissions.request).toHaveBeenCalledWith({
      origins: ["http://192.168.1.50/*"],
    });
  });

  it("uses provided mode parameter", async () => {
    let capturedBody;
    server.use(
      http.post("http://192.168.1.50:32500/ui/adapter/url/play", async ({ request }) => {
        capturedBody = await request.json();
        return HttpResponse.json({ adapter_ref: "url:xxx" }, { status: 202 });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    await play("https://example.com/x.mp4", "ytdlp");

    expect(capturedBody.mode).toBe("ytdlp");
  });
});

describe("play() error paths", () => {
  it("returns 'Bridge not configured' when bridgeURL is empty", async () => {
    const result = await play("https://example.com/x.mp4");
    expect(result).toEqual({ ok: false, error: "Bridge not configured" });
  });

  it("returns 'Bridge timed out' when fetch aborts", async () => {
    _setTimeoutForTest(50);
    server.use(
      http.post("http://192.168.1.50:32500/ui/adapter/url/play", async () => {
        await delay(500);
        return HttpResponse.json({ adapter_ref: "url:never" }, { status: 202 });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await play("https://example.com/x.mp4");

    expect(result).toEqual({ ok: false, error: "Bridge timed out" });
  });

  it("returns 'Bridge unreachable: ...' on network error", async () => {
    server.use(
      http.post("http://192.168.1.50:32500/ui/adapter/url/play", () => {
        return HttpResponse.error();
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await play("https://example.com/x.mp4");

    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/^Bridge unreachable:/);
  });

  it("returns the bridge's error message on 4xx with JSON body", async () => {
    server.use(
      http.post("http://192.168.1.50:32500/ui/adapter/url/play", () => {
        return HttpResponse.json(
          { error: "not a valid URL", field: "url" },
          { status: 400 }
        );
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await play("blob:moz-extension://abc/123");

    expect(result).toEqual({
      ok: false,
      status: 400,
      error: "not a valid URL",
    });
  });

  it("returns the bridge's error message on 5xx with JSON body", async () => {
    server.use(
      http.post("http://192.168.1.50:32500/ui/adapter/url/play", () => {
        return HttpResponse.json(
          { error: "probe source: ffprobe: exit status 1" },
          { status: 500 }
        );
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await play("https://example.com/notmedia.html");

    expect(result).toEqual({
      ok: false,
      status: 500,
      error: "probe source: ffprobe: exit status 1",
    });
  });

  it("falls back to 'HTTP <status>' on non-JSON error response", async () => {
    server.use(
      http.post("http://192.168.1.50:32500/ui/adapter/url/play", () => {
        return new HttpResponse("internal server error (text body)", { status: 500 });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await play("https://example.com/x.mp4");

    expect(result).toEqual({ ok: false, status: 500, error: "HTTP 500" });
  });
});

describe("launchGroovyMister()", () => {
  it("POSTs to /ui/bridge/mister/launch with the extension header", async () => {
    let captured;
    server.use(
      http.post("http://192.168.1.50:32500/ui/bridge/mister/launch", ({ request }) => {
        captured = { headers: Object.fromEntries(request.headers) };
        return new HttpResponse(
          '<div class="status-line run">Sent</div>',
          { status: 200, headers: { "Content-Type": "text/html" } }
        );
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await launchGroovyMister();

    expect(result).toEqual({ ok: true });
    expect(captured.headers["x-bridge-extension"]).toBe("1");
  });

  it("returns 'Bridge not configured' when bridgeURL is empty", async () => {
    const result = await launchGroovyMister();
    expect(result).toEqual({ ok: false, error: "Bridge not configured" });
  });

  it("returns 'Bridge timed out' when launch fetch aborts", async () => {
    _setTimeoutForTest(50);
    server.use(
      http.post("http://192.168.1.50:32500/ui/bridge/mister/launch", async () => {
        await delay(500);
        return new HttpResponse('<div class="status-line run">Sent</div>', { status: 200 });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await launchGroovyMister();

    expect(result).toEqual({ ok: false, error: "Bridge timed out" });
  });

  it("returns 'Bridge unreachable: ...' on network error", async () => {
    server.use(
      http.post("http://192.168.1.50:32500/ui/bridge/mister/launch", () => {
        return HttpResponse.error();
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await launchGroovyMister();

    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/^Bridge unreachable:/);
  });

  it("returns launch failure text on HTTP error", async () => {
    server.use(
      http.post("http://192.168.1.50:32500/ui/bridge/mister/launch", () => {
        return new HttpResponse("SSH failed: dial timeout", { status: 500 });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await launchGroovyMister();

    expect(result).toEqual({
      ok: false,
      status: 500,
      error: "Launch failed: SSH failed: dial timeout",
    });
  });
});
