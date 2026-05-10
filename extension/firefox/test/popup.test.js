import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import * as bridge from "../src/lib/bridge.js";
import { initPopup, truncate } from "../src/popup/popup.js";

function popupMarkup() {
  return `
    <div id="popup" class="popup-shell" data-state="loading">
      <section id="unconfigured" class="view" hidden>
        <div class="brand-row">
          <span class="brand">GroovyRelay</span>
          <span class="chip">SETUP</span>
        </div>
        <h1>Configure bridge</h1>
        <p class="empty">Bridge URL is not set.</p>
        <button type="button" id="configure" class="primary">Configure</button>
      </section>

      <section id="configured" class="view" hidden>
        <div class="brand-row">
          <span class="brand">GroovyRelay</span>
          <span id="state-chip" class="chip">LOADING</span>
        </div>

        <div id="active-view" class="remote-view" hidden>
          <p class="eyebrow">Now playing</p>
          <h1 id="media-title">Unknown source</h1>
          <p id="source-line" class="source-line"></p>
          <div id="progress-wrap" class="progress-wrap" hidden>
            <div class="time-row">
              <span id="position-label">0:00</span>
              <span id="duration-label">0:00</span>
            </div>
            <input id="seek" type="range" min="0" value="0" step="1000">
          </div>
          <div class="control-grid">
            <button type="button" id="pause">Pause</button>
            <button type="button" id="resume">Resume</button>
            <button type="button" id="stop">Stop</button>
            <button type="button" id="replay">Replay</button>
          </div>
        </div>

        <div id="idle-view" class="remote-view" hidden>
          <p class="eyebrow">Cast this tab</p>
          <div id="tab-url" class="tab-url"></div>
          <button type="button" id="cast" class="primary">Cast tab</button>
          <div class="health-grid">
            <div><span>Bridge</span><strong id="health-bridge">--</strong></div>
            <div><span>MiSTer</span><strong id="health-mister">--</strong></div>
            <div><span>URL</span><strong id="health-url">--</strong></div>
          </div>
        </div>

        <div id="history" class="history-list"></div>

        <div class="actions">
          <button type="button" id="launch-groovy">Launch GroovyMiSTer</button>
          <button type="button" id="open-webui">Open Web UI</button>
          <button type="button" id="open-options">Settings</button>
        </div>
        <div id="status" class="status" role="status" aria-live="polite"></div>
      </section>
    </div>
  `;
}

function renderPopup(doc = document) {
  doc.body.innerHTML = popupMarkup();
}

async function flushEvents() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

function playingStatus(overrides = {}) {
  const { session: sessionOverrides = {}, ...rest } = overrides;
  return {
    ok: true,
    configured: true,
    bridge_url: "http://192.168.1.50:32500",
    session: {
      state: "playing",
      title: "Night",
      source_display: "archive.org/night",
      position_ms: 1000,
      duration_ms: 10000,
      capabilities: {
        can_pause: true,
        can_resume: false,
        can_stop: true,
        can_replay: true,
        can_seek: true,
      },
      ...sessionOverrides,
    },
    health: { bridge: "online", mister: "unknown", url_adapter: "online" },
    history: [],
    ...rest,
  };
}

beforeEach(() => {
  vi.restoreAllMocks();
  renderPopup();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("truncate", () => {
  it("returns input unchanged when shorter than max", () => {
    expect(truncate("hello", 10)).toBe("hello");
  });

  it("truncates with ellipsis when longer than max", () => {
    expect(truncate("hello world", 8)).toBe("hello...");
  });

  it("handles edge case where input length equals max", () => {
    expect(truncate("hello", 5)).toBe("hello");
  });
});

describe("initPopup companion remote", () => {
  it("renders active remote first when status is playing", async () => {
    vi.spyOn(bridge, "getStatus").mockResolvedValue(playingStatus());
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const state = await initPopup(document);

    expect(document.getElementById("active-view").hidden).toBe(false);
    expect(document.getElementById("idle-view").hidden).toBe(true);
    expect(document.getElementById("media-title").textContent).toBe("Night");
    expect(document.getElementById("source-line").textContent).toBe("archive.org/night");
    clearInterval(state.pollTimer);
  });

  it("renders idle cast-first state when nothing is playing", async () => {
    vi.spyOn(bridge, "getStatus").mockResolvedValue(playingStatus({
      session: { state: "idle", capabilities: { can_play: true } },
      history: [],
    }));
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
    browser.tabs.query.mockResolvedValueOnce([{ url: "https://example.com/movie.mp4" }]);

    const state = await initPopup(document);

    expect(document.getElementById("active-view").hidden).toBe(true);
    expect(document.getElementById("idle-view").hidden).toBe(false);
    expect(document.getElementById("tab-url").textContent).toBe("https://example.com/movie.mp4");
    expect(document.getElementById("cast").disabled).toBe(false);
    clearInterval(state.pollTimer);
  });

  it("hides seek when duration is missing", async () => {
    vi.spyOn(bridge, "getStatus").mockResolvedValue(playingStatus({
      session: {
        duration_ms: 0,
        capabilities: { can_pause: true, can_stop: true, can_replay: true, can_seek: false },
      },
    }));
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const state = await initPopup(document);

    expect(document.getElementById("progress-wrap").hidden).toBe(true);
    clearInterval(state.pollTimer);
  });

  it("disables stale controls after a failed poll until the next successful poll", async () => {
    const getStatus = vi.spyOn(bridge, "getStatus")
      .mockResolvedValue(playingStatus({
        session: {
          capabilities: { can_pause: true, can_stop: true, can_replay: true, can_seek: false },
        },
      }));
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const state = await initPopup(document);
    getStatus.mockImplementationOnce(async () => ({ ok: false, status: 500, error: "bridge restarted" }));
    await state.refresh();
    expect(document.getElementById("pause").disabled).toBe(true);
    getStatus.mockImplementationOnce(async () => playingStatus({
      session: {
        capabilities: { can_pause: true, can_stop: true, can_replay: true, can_seek: false },
      },
    }));
    await state.refresh();
    expect(document.getElementById("pause").disabled).toBe(false);
    clearInterval(state.pollTimer);
  });

  it("pauses polling while a command is in flight and resumes after", async () => {
    // Spec testing strategy line 564: "polling pauses during commands and
    // refreshes after." popup.js:36-39 implements that by skipping the
    // setInterval tick while state.commanding is true. This test pins the
    // contract by toggling the flag directly (mirroring what runCommand
    // does) and asserting refreshes only happen when the flag is false.
    vi.useFakeTimers();
    const getStatus = vi.spyOn(bridge, "getStatus").mockResolvedValue(playingStatus());
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const state = await initPopup(document);
    const initialCalls = getStatus.mock.calls.length;

    // Simulate an in-flight command. runCommand sets this flag for the
    // duration of its bridge call (popup.js:99-114).
    state.commanding = true;

    // Advance through three full poll intervals (2 s each, see POLL_MS in
    // popup.js). The setInterval callback fires but the early-return at
    // popup.js:37 skips state.refresh().
    await vi.advanceTimersByTimeAsync(2000 * 3 + 100);
    expect(getStatus.mock.calls.length).toBe(initialCalls);

    // Command finishes; the next interval tick must refresh once.
    state.commanding = false;
    await vi.advanceTimersByTimeAsync(2000 + 100);
    expect(getStatus.mock.calls.length).toBe(initialCalls + 1);

    clearInterval(state.pollTimer);
  });

  it("plays and deletes history rows by opaque id", async () => {
    const historyPlay = vi.spyOn(bridge, "historyPlay").mockResolvedValue({ ok: true, state: "playing" });
    const historyDelete = vi.spyOn(bridge, "historyDelete").mockResolvedValue({ ok: true });
    vi.spyOn(bridge, "getStatus").mockResolvedValue(playingStatus({
      session: { state: "idle", capabilities: { can_play: true } },
      history: [{
        id: "h_11111111111111111111111111111111",
        title: "Saved Clip",
        url_display: "example.com/clip.mp4",
      }],
    }));
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    const state = await initPopup(document);
    document.querySelector("[data-history-play]").click();
    await flushEvents();
    document.querySelector("[data-history-delete]").click();
    await flushEvents();

    expect(historyPlay).toHaveBeenCalledWith("h_11111111111111111111111111111111");
    expect(historyDelete).toHaveBeenCalledWith("h_11111111111111111111111111111111");
    clearInterval(state.pollTimer);
  });

  it("opens the configured bridge URL from the Web UI button", async () => {
    vi.spyOn(bridge, "getStatus").mockResolvedValue(playingStatus({
      session: { state: "idle", capabilities: { can_play: true } },
    }));
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
    const state = await initPopup(document);

    document.getElementById("open-webui").click();
    await flushEvents();

    expect(browser.tabs.create).toHaveBeenCalledWith({
      url: "http://192.168.1.50:32500",
    });
    clearInterval(state.pollTimer);
  });

  it("launches GroovyMiSTer through the companion API", async () => {
    vi.spyOn(bridge, "getStatus").mockResolvedValue(playingStatus({
      session: { state: "idle", capabilities: { can_play: true } },
    }));
    const launch = vi.spyOn(bridge, "launchGroovyMister").mockResolvedValue({ ok: true });
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
    const state = await initPopup(document);

    document.getElementById("launch-groovy").click();
    await flushEvents();

    expect(launch).toHaveBeenCalled();
    expect(document.getElementById("status").textContent).toBe("GroovyMiSTer launch sent.");
    clearInterval(state.pollTimer);
  });
});
