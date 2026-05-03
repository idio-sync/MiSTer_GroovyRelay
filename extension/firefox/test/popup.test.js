import { describe, it, expect, beforeEach, vi } from "vitest";
import { initPopup, truncate } from "../src/popup/popup.js";

function renderPopup() {
  document.body.innerHTML = `
    <div id="popup">
      <div id="configured" hidden>
        <div class="label">Active tab:</div>
        <div id="tab-url" class="tab-url"></div>
        <button type="button" id="cast" class="primary">Cast this tab</button>
        <div id="status" class="status" role="status" aria-live="polite"></div>
        <div class="actions">
          <button type="button" id="launch-groovy">Launch GroovyMiSTer</button>
          <button type="button" id="open-webui">Open Web UI</button>
        </div>
        <div id="launch-status" class="status" role="status" aria-live="polite"></div>
        <a href="#" id="open-options" class="settings-link">Settings</a>
      </div>
      <div id="unconfigured" hidden>
        <p class="empty">Bridge not configured.</p>
        <button type="button" id="configure" class="primary">Configure bridge</button>
      </div>
    </div>
  `;
}

async function flushEvents() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

beforeEach(() => {
  renderPopup();
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

describe("initPopup configured actions", () => {
  it("renders launch and open Web UI buttons when bridge is configured", async () => {
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });

    await initPopup(document);

    expect(document.getElementById("launch-groovy")).not.toBeNull();
    expect(document.getElementById("open-webui")).not.toBeNull();
  });

  it("keeps launch and open Web UI enabled when active tab cannot be cast", async () => {
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
    browser.tabs.query.mockResolvedValueOnce([{ url: "about:blank" }]);

    await initPopup(document);

    expect(document.getElementById("cast").disabled).toBe(true);
    expect(document.getElementById("launch-groovy").disabled).toBe(false);
    expect(document.getElementById("open-webui").disabled).toBe(false);
  });

  it("opens the configured bridge URL from the Web UI button", async () => {
    await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
    await initPopup(document);

    document.getElementById("open-webui").click();
    await flushEvents();

    expect(browser.tabs.create).toHaveBeenCalledWith({
      url: "http://192.168.1.50:32500",
    });
  });

  it("launches GroovyMiSTer and reports success", async () => {
    const fetchMock = vi.fn(async () => {
      return new Response('<div class="status-line run">Sent</div>', { status: 200 });
    });
    const originalFetch = globalThis.fetch;
    globalThis.fetch = fetchMock;
    try {
      await browser.storage.sync.set({ bridgeURL: "http://192.168.1.50:32500" });
      await initPopup(document);

      document.getElementById("launch-groovy").click();
      await flushEvents();

      expect(fetchMock).toHaveBeenCalledWith(
        "http://192.168.1.50:32500/ui/bridge/mister/launch",
        expect.objectContaining({
          method: "POST",
          headers: expect.objectContaining({ "X-Bridge-Extension": "1" }),
        })
      );
      expect(document.getElementById("launch-status").textContent).toBe(
        "GroovyMiSTer launch sent."
      );
      expect(document.getElementById("launch-groovy").disabled).toBe(false);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});
