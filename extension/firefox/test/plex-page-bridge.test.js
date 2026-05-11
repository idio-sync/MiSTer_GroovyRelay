import { describe, expect, it, vi } from "vitest";
import { JSDOM } from "jsdom";
import fs from "node:fs";

const pageScript = fs.readFileSync("src/content/plex-timeline-page.js", "utf8");

function makeWindow() {
  const dom = new JSDOM("<!doctype html><html><head></head><body></body></html>", {
    url: "https://app.plex.tv/desktop/",
    runScripts: "dangerously",
  });
  const win = dom.window;
  const originalPostMessage = win.postMessage.bind(win);
  win.postMessage = (data, targetOrigin) => {
    originalPostMessage(data, targetOrigin);
    setTimeout(() => {
      win.dispatchEvent(new win.MessageEvent("message", { data, source: win }));
    }, 0);
  };
  win.Response = Response;
  win.fetch = vi.fn(async () => new Response("native"));
  return win;
}

function installReply(win, body = "<MediaContainer />") {
  win.addEventListener("message", (event) => {
    const msg = event.data;
    if (msg?.source !== "mister-groovy-relay-plex-timeline") return;
    if (msg.type !== "plexTimelinePoll") return;
    setTimeout(() => {
      win.dispatchEvent(
        new win.MessageEvent("message", {
          source: win,
          data: {
            source: "mister-groovy-relay-plex-timeline",
            type: "plexTimelinePollResult",
            requestID: msg.requestID,
            result: {
              ok: true,
              handled: true,
              status: 200,
              statusText: "OK",
              headers: {
                "content-type": "text/xml",
                "x-plex-client-identifier": "relay-id",
              },
              body,
            },
          },
        })
      );
    }, 0);
  });
}

describe("Plex page timeline bridge", () => {
  it("intercepts fetch timeline polls and returns the bridged XML", async () => {
    const win = makeWindow();
    const nativeFetch = win.fetch;
    installReply(win, '<MediaContainer><Timeline time="42"></Timeline></MediaContainer>');
    win.eval(pageScript);

    const response = await win.fetch("https://example.plex.direct/player/timeline/poll?wait=1");

    expect(await response.text()).toContain('time="42"');
    expect(response.headers.get("x-plex-client-identifier")).toBe("relay-id");
    expect(nativeFetch).not.toHaveBeenCalled();
  });

  it("intercepts XHR timeline polls and returns the bridged XML", async () => {
    const win = makeWindow();
    installReply(win, '<MediaContainer><Timeline time="99"></Timeline></MediaContainer>');
    win.eval(pageScript);

    const result = await new Promise((resolve) => {
      const xhr = new win.XMLHttpRequest();
      xhr.open("GET", "https://example.plex.direct/player/timeline/poll?wait=1");
      xhr.onload = () => {
        resolve({
          status: xhr.status,
          body: xhr.responseText,
          clientID: xhr.getResponseHeader("X-Plex-Client-Identifier"),
        });
      };
      xhr.send();
    });

    expect(result).toEqual({
      status: 200,
      body: '<MediaContainer><Timeline time="99"></Timeline></MediaContainer>',
      clientID: "relay-id",
    });
  });
});
