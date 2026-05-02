import { play, getBridgeURL, formatPlayError } from "../lib/bridge.js";

export async function initPopup(doc = document) {
  const bridgeURL = await getBridgeURL();
  const configured = doc.getElementById("configured");
  const unconfigured = doc.getElementById("unconfigured");

  if (!bridgeURL) {
    unconfigured.hidden = false;
    doc.getElementById("configure").addEventListener("click", () => {
      browser.runtime.openOptionsPage();
    });
    return;
  }

  configured.hidden = false;
  const tabUrlEl = doc.getElementById("tab-url");
  const castBtn = doc.getElementById("cast");
  const statusEl = doc.getElementById("status");
  const openOptions = doc.getElementById("open-options");

  const tabs = await browser.tabs.query({ active: true, currentWindow: true });
  const activeTab = tabs[0];
  const url = activeTab?.url || "";

  if (!url || !/^https?:/i.test(url)) {
    tabUrlEl.textContent = url || "(no active tab)";
    castBtn.disabled = true;
    setStatus(statusEl, "err", "Active tab has no http(s) URL");
  } else {
    tabUrlEl.textContent = url;
  }

  castBtn.addEventListener("click", async () => {
    castBtn.disabled = true;
    setStatus(statusEl, "", "Casting...");
    const result = await play(url);
    if (result.ok) {
      setStatus(statusEl, "ok", `Playing: ${truncate(url, 60)}`);
    } else {
      setStatus(statusEl, "err", formatPlayError(result));
    }
    castBtn.disabled = false;
  });

  openOptions.addEventListener("click", (e) => {
    e.preventDefault();
    browser.runtime.openOptionsPage();
  });
}

function setStatus(el, kind, msg) {
  el.className = "status" + (kind ? " " + kind : "");
  el.textContent = msg;
}

export function truncate(s, max) {
  if (s.length <= max) return s;
  if (max <= 3) return ".".repeat(Math.max(0, max));
  return s.slice(0, max - 3) + "...";
}

if (typeof document !== "undefined" && document.getElementById("popup")) {
  initPopup();
}
