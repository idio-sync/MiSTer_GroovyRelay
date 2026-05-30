import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import * as bridge from "../src/lib/bridge.js";
import { initPopup, truncate } from "../src/popup/popup.js";

function popupMarkup() {
  return `
    <div id="popup" class="popup-shell" data-state="loading">
      <div class="status-bar">
        <div class="leds">
          <span class="led green on" id="led-pwr"><span class="light"></span><span class="lbl">PWR</span></span>
          <span class="led aqua" id="led-link"><span class="light"></span><span class="lbl">LINK</span></span>
          <span class="led red" id="led-cast"><span class="light"></span><span class="lbl">CAST</span></span>
        </div>
        <div class="brand-plate"><span class="name">GROOVYRELAY</span></div>
      </div>

      <!-- unconfigured -->
      <section id="unconfigured" class="view" hidden>
        <div class="cast-box">
          <p class="eyebrow">Setup required</p>
          <div class="tab-url">Bridge URL is not set.</div>
          <button type="button" id="configure" class="cast-btn">Configure</button>
        </div>
      </section>

      <!-- active -->
      <section id="active-view" class="view" hidden>
        <div class="screen-frame"><div class="vfd">
          <div>
            <div id="vfd-primary" class="tier tier-primary"></div>
            <div id="vfd-secondary" class="tier tier-secondary"></div>
            <div id="vfd-tertiary" class="tier tier-tertiary"></div>
          </div>
          <div class="vfd-right">
            <div class="k">State</div>
            <div id="vfd-state" class="v">--</div>
          </div>
        </div></div>
        <div class="transport">
          <div class="transport-row">
            <button type="button" id="replay" class="trn" title="Replay">&#x27f2;</button>
            <button type="button" id="pause-resume" class="trn primary" title="Pause / Resume">
              <span data-state-icon="playing">&#x23f8;</span><span data-state-icon="paused" hidden>&#x25b6;</span>
            </button>
            <button type="button" id="stop" class="trn" title="Stop">&#x23f9;</button>
          </div>
          <div id="progress-wrap" class="progress-wrap" hidden>
            <div class="seek" id="seek" style="--seek-percent:0%;"><div class="fill"></div></div>
            <div class="seek-time">
              <span id="position-label">0:00</span><span class="sep">/</span>
              <span id="duration-label">0:00</span><span id="percent-label" class="pct"></span>
            </div>
          </div>
        </div>
      </section>

      <!-- idle -->
      <section id="idle-view" class="view" hidden>
        <div class="cast-box">
          <p class="eyebrow">Cast this tab</p>
          <div id="tab-url" class="tab-url"></div>
          <button type="button" id="cast" class="cast-btn">Cast tab</button>
        </div>
        <div class="health-grid">
          <div><span>Bridge</span><strong id="health-bridge">--</strong></div>
          <div><span>MiSTer</span><strong id="health-mister">--</strong></div>
          <div><span>URL</span><strong id="health-url">--</strong></div>
        </div>
      </section>

      <!-- shared volume control -->
      <div class="volume-control" id="volume-control" data-volume-value="0" style="--volume-angle:-135deg;">
        <div class="volume-dial" aria-hidden="true"><span class="volume-notch"></span></div>
        <div class="volume-meta">
          <div class="volume-top"><span class="volume-label">Volume</span><span class="volume-value" id="volume-value">0</span></div>
          <div class="volume-tick-ring" id="volume-tick-ring" aria-hidden="true"></div>
        </div>
        <input class="volume-range" id="volume-range" type="range" min="0" max="100" step="1" value="0" aria-label="Volume">
      </div>

      <div class="actions">
        <button type="button" id="launch-groovy" class="primary">&#9656; Launch</button>
        <button type="button" id="open-webui">Web UI</button>
        <button type="button" id="open-options">Setup</button>
      </div>
      <div id="status" class="status" role="status" aria-live="polite"></div>
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
    output_volume: 50,
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
    expect(document.getElementById("vfd-primary").textContent).toBe("Night");
    expect(document.getElementById("vfd-secondary").textContent).toBe("archive.org/night");
    clearInterval(state.pollTimer);
  });

  it("renders idle cast-first state when nothing is playing", async () => {
    vi.spyOn(bridge, "getStatus").mockResolvedValue(playingStatus({
      session: { state: "idle", capabilities: { can_play: true } },
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
    expect(document.getElementById("pause-resume").disabled).toBe(true);
    getStatus.mockImplementationOnce(async () => playingStatus({
      session: {
        capabilities: { can_pause: true, can_stop: true, can_replay: true, can_seek: false },
      },
    }));
    await state.refresh();
    expect(document.getElementById("pause-resume").disabled).toBe(false);
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

  it("renders VFD tiers and volume from status", async () => {
    vi.spyOn(bridge, "getStatus").mockResolvedValue(playingStatus({
      session: { state: "playing", title: "BIG BUCK BUNNY", source_display: "archive.org",
        resolved_via: "direct", position_ms: 30000, duration_ms: 120000,
        capabilities: { can_pause: true, can_stop: true, can_replay: true, can_seek: true } },
      output_volume: 64,
    }));
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
    const state = await initPopup(document);
    expect(document.getElementById("vfd-primary").textContent).toBe("BIG BUCK BUNNY");
    expect(document.getElementById("vfd-tertiary").textContent).toBe("direct");
    expect(document.getElementById("volume-value").textContent).toBe("64");
    clearInterval(state.pollTimer);
  });

  it("posts volume on range change", async () => {
    const spy = vi.spyOn(bridge, "volume").mockResolvedValue({ ok: true, output_volume: 80 });
    vi.spyOn(bridge, "getStatus").mockResolvedValue(playingStatus({ output_volume: 50 }));
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
    const state = await initPopup(document);
    const range = document.getElementById("volume-range");
    range.value = "80";
    range.dispatchEvent(new Event("change"));
    await new Promise((r) => setTimeout(r, 0));
    expect(spy).toHaveBeenCalledWith(80);
    clearInterval(state.pollTimer);
  });
});
