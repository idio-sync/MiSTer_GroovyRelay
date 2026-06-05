import { describe, it, expect, beforeAll, afterAll, afterEach } from "vitest";
import { http, HttpResponse, delay } from "msw";
import { setupServer } from "msw/node";
import * as bridge from "../src/lib/bridge.js";
import {
  control,
  getBridgeURL,
  getStatus,
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
  it("POSTs to /ui/companion/play with the right headers and body", async () => {
    let captured;
    server.use(
      http.post("http://192.168.1.50:32500/ui/companion/play", async ({ request }) => {
        captured = {
          headers: Object.fromEntries(request.headers),
          body: await request.json(),
        };
        return HttpResponse.json(
          { ok: true, adapter_ref: "url:abc123", state: "playing", resolved_via: "direct" },
          { status: 202 }
        );
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await play("https://youtu.be/x");

    expect(result).toEqual({
      ok: true,
      adapter_ref: "url:abc123",
      state: "playing",
      resolved_via: "direct",
    });
    expect(captured.headers["content-type"]).toBe("application/json");
    expect(captured.headers["x-bridge-extension"]).toBe("1");
    expect(captured.body).toEqual({ url: "https://youtu.be/x", mode: "auto" });
  });

  it("requests bridge host permission before POSTing when it is missing", async () => {
    browser.permissions.contains.mockResolvedValueOnce(false);
    browser.permissions.request.mockResolvedValueOnce(true);
    server.use(
      http.post("http://192.168.1.50:32500/ui/companion/play", () => {
        return HttpResponse.json({ ok: true, adapter_ref: "url:abc123" }, { status: 202 });
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
      http.post("http://192.168.1.50:32500/ui/companion/play", async ({ request }) => {
        capturedBody = await request.json();
        return HttpResponse.json({ ok: true, adapter_ref: "url:xxx" }, { status: 202 });
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
      http.post("http://192.168.1.50:32500/ui/companion/play", async () => {
        await delay(500);
        return HttpResponse.json({ ok: true, adapter_ref: "url:never" }, { status: 202 });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await play("https://example.com/x.mp4");

    expect(result).toEqual({ ok: false, error: "Bridge timed out" });
  });

  it("returns 'Bridge unreachable: ...' on network error", async () => {
    server.use(
      http.post("http://192.168.1.50:32500/ui/companion/play", () => {
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
      http.post("http://192.168.1.50:32500/ui/companion/play", () => {
        return HttpResponse.json(
          { ok: false, error: "not a valid URL" },
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
      http.post("http://192.168.1.50:32500/ui/companion/play", () => {
        return HttpResponse.json(
          { ok: false, error: "probe source: ffprobe: exit status 1" },
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
      http.post("http://192.168.1.50:32500/ui/companion/play", () => {
        return new HttpResponse("internal server error (text body)", { status: 500 });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await play("https://example.com/x.mp4");

    expect(result).toEqual({ ok: false, status: 500, error: "HTTP 500" });
  });
});

describe("getStatus()", () => {
  it("GETs companion status with the extension header", async () => {
    let captured;
    server.use(
      http.get("http://192.168.1.50:32500/ui/companion/status", ({ request }) => {
        captured = { headers: Object.fromEntries(request.headers) };
        return HttpResponse.json({
          configured: true,
          health: { bridge: "online", mister: "unknown", url_adapter: "enabled" },
        });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await getStatus();

    expect(result.ok).toBe(true);
    expect(result.configured).toBe(true);
    expect(captured.headers["x-bridge-extension"]).toBe("1");
  });
});

describe("plexTimelinePoll()", () => {
  const targetID = "6e83bef1-a873-4a8f-b98d-b7fa46272b44";
  const browserID = "37er4k14mzozjwaxf72oadqi";

  function originalPollURL(target = targetID) {
    return `https://192-168-50-137.example.plex.direct:32400/player/timeline/poll?wait=1&commandID=7&X-Plex-Client-Identifier=${browserID}&X-Plex-Target-Client-Identifier=${target}&X-Plex-Product=Plex%20Web`;
  }

  it("proxies MiSTer timeline polls to the configured bridge URL", async () => {
    expect(bridge.plexTimelinePoll).toBeTypeOf("function");

    let resourcesRequested = false;
    let timelineRequested;
    server.use(
      http.get("http://192.168.50.138:32500/resources", () => {
        resourcesRequested = true;
        return new HttpResponse("<MediaContainer />", {
          headers: {
            "Content-Type": "text/xml",
            "X-Plex-Client-Identifier": targetID,
          },
        });
      }),
      http.get("http://192.168.50.138:32500/player/timeline/poll", ({ request }) => {
        const url = new URL(request.url);
        timelineRequested = {
          wait: url.searchParams.get("wait"),
          commandID: url.searchParams.get("commandID"),
          clientID: url.searchParams.get("X-Plex-Client-Identifier"),
          targetID: url.searchParams.get("X-Plex-Target-Client-Identifier"),
          product: url.searchParams.get("X-Plex-Product"),
          headers: Object.fromEntries(request.headers),
        };
        return new HttpResponse(
          `<MediaContainer commandID="7"><Timeline type="video" state="playing" time="1234" playbackTime="1234" duration="9000"></Timeline></MediaContainer>`,
          {
            status: 200,
            headers: {
              "Content-Type": "text/xml",
              "X-Plex-Client-Identifier": targetID,
            },
          }
        );
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.50.138:32500" });

    const result = await bridge.plexTimelinePoll(originalPollURL());

    expect(resourcesRequested).toBe(true);
    expect(timelineRequested).toMatchObject({
      wait: "1",
      commandID: "7",
      clientID: browserID,
      targetID,
      product: "Plex Web",
    });
    expect(timelineRequested.headers["x-bridge-extension"]).toBe("1");
    expect(result).toMatchObject({
      ok: true,
      handled: true,
      status: 200,
    });
    expect(result.headers["x-plex-client-identifier"]).toBe(targetID);
    expect(result.body).toContain('time="1234"');
  });

  it("leaves other Plex targets alone", async () => {
    expect(bridge.plexTimelinePoll).toBeTypeOf("function");

    let timelineCalled = false;
    server.use(
      http.get("http://192.168.50.138:32500/resources", () => {
        return new HttpResponse("<MediaContainer />", {
          headers: { "X-Plex-Client-Identifier": targetID },
        });
      }),
      http.get("http://192.168.50.138:32500/player/timeline/poll", () => {
        timelineCalled = true;
        return HttpResponse.text("should not be called");
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.50.138:32500" });

    const result = await bridge.plexTimelinePoll(originalPollURL("ps4-client-id"));

    expect(result).toEqual({ ok: true, handled: false });
    expect(timelineCalled).toBe(false);
  });
});

describe("control()", () => {
  it("POSTs control actions to the companion control route", async () => {
    let captured;
    server.use(
      http.post("http://192.168.1.50:32500/ui/companion/control", async ({ request }) => {
        captured = await request.json();
        return HttpResponse.json({ ok: true, state: "paused" });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await control("seek", { offset_ms: 90000 });

    expect(result).toEqual({ ok: true, state: "paused" });
    expect(captured).toEqual({ action: "seek", offset_ms: 90000 });
  });
});

describe("volume()", () => {
  it("POSTs output_volume to the companion volume endpoint", async () => {
    let captured;
    server.use(
      http.post("http://192.168.1.50:32500/ui/companion/volume", async ({ request }) => {
        captured = {
          headers: Object.fromEntries(request.headers),
          body: await request.json(),
        };
        return HttpResponse.json({ ok: true, output_volume: 55 });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await bridge.volume(55);

    expect(result).toEqual({ ok: true, output_volume: 55 });
    expect(captured.body).toEqual({ output_volume: 55 });
    expect(captured.headers["x-bridge-extension"]).toBe("1");
    expect(captured.headers["content-type"]).toBe("application/json");
  });
});

describe("launchGroovyMister()", () => {
  it("POSTs launch to the companion launch route", async () => {
    let captured;
    server.use(
      http.post("http://192.168.1.50:32500/ui/companion/launch", ({ request }) => {
        captured = { headers: Object.fromEntries(request.headers) };
        return HttpResponse.json({ ok: true });
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
      http.post("http://192.168.1.50:32500/ui/companion/launch", async () => {
        await delay(500);
        return HttpResponse.json({ ok: true });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await launchGroovyMister();

    expect(result).toEqual({ ok: false, error: "Bridge timed out" });
  });

  it("returns 'Bridge unreachable: ...' on network error", async () => {
    server.use(
      http.post("http://192.168.1.50:32500/ui/companion/launch", () => {
        return HttpResponse.error();
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await launchGroovyMister();

    expect(result.ok).toBe(false);
    expect(result.error).toMatch(/^Bridge unreachable:/);
  });

  it("returns JSON launch failure on HTTP error", async () => {
    server.use(
      http.post("http://192.168.1.50:32500/ui/companion/launch", () => {
        return HttpResponse.json({ ok: false, error: "SSH failed: dial timeout" }, { status: 500 });
      })
    );
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const result = await launchGroovyMister();

    expect(result).toEqual({
      ok: false,
      status: 500,
      error: "SSH failed: dial timeout",
    });
  });
});
