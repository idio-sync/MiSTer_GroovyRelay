import * as bridge from "../lib/bridge.js";

const POLL_MS = 2000;

function createState() {
  return {
    bridgeURL: "",
    activeTabURL: "",
    lastBridgeHost: "",
    snapshot: null,
    stale: true,
    commanding: false,
    pollTimer: null,
    refresh: null,
  };
}

export async function initPopup(doc = document) {
  const state = createState();
  state.bridgeURL = await bridge.getBridgeURL();
  state.refresh = () => refreshStatus(doc, state);
  bindStaticActions(doc, state);

  if (!state.bridgeURL) {
    showUnconfigured(doc);
    return state;
  }

  showConfigured(doc);
  const [tabs] = await Promise.all([
    browser.tabs.query({ active: true, currentWindow: true }),
    state.refresh(),
  ]);
  state.activeTabURL = tabs[0]?.url || "";
  render(doc, state);
  state.pollTimer = globalThis.setInterval(() => {
    if (!state.commanding) return state.refresh();
    return undefined;
  }, POLL_MS);
  return state;
}

function bindStaticActions(doc, state) {
  doc.getElementById("configure")?.addEventListener("click", () => {
    browser.runtime.openOptionsPage();
  });
  doc.getElementById("open-options")?.addEventListener("click", () => {
    browser.runtime.openOptionsPage();
  });
  doc.getElementById("open-webui")?.addEventListener("click", async () => {
    try {
      await browser.tabs.create({ url: state.bridgeURL });
    } catch (e) {
      setStatus(doc, "err", `Open Web UI failed: ${e.message}`);
    }
  });
  doc.getElementById("launch-groovy")?.addEventListener("click", () => {
    runCommand(doc, state, () => bridge.launchGroovyMister(), "Launch failed", {
      success: "GroovyMiSTer launch sent.",
    });
  });
  doc.getElementById("cast")?.addEventListener("click", () => {
    runCommand(doc, state, () => bridge.play(state.activeTabURL, "auto"), "Cast failed");
  });
  doc.getElementById("pause")?.addEventListener("click", () => {
    runCommand(doc, state, () => bridge.control("pause"), "Pause failed");
  });
  doc.getElementById("resume")?.addEventListener("click", () => {
    runCommand(doc, state, () => bridge.control("resume"), "Resume failed");
  });
  doc.getElementById("stop")?.addEventListener("click", () => {
    runCommand(doc, state, () => bridge.control("stop"), "Stop failed");
  });
  doc.getElementById("replay")?.addEventListener("click", () => {
    runCommand(doc, state, () => bridge.control("replay"), "Replay failed");
  });
  doc.getElementById("seek")?.addEventListener("change", (event) => {
    const offsetMs = Number(event.target.value || 0);
    runCommand(doc, state, () => bridge.control("seek", { offset_ms: offsetMs }), "Seek failed");
  });
}

async function refreshStatus(doc, state) {
  const result = await bridge.getStatus();
  if (!result.ok) {
    state.stale = true;
    setStatus(doc, "err", bridge.formatBridgeError(result, "Status failed"));
    render(doc, state);
    return;
  }

  const host = hostOf(result.bridge_url || state.bridgeURL);
  state.stale = Boolean(state.lastBridgeHost && host && host !== state.lastBridgeHost);
  if (host) state.lastBridgeHost = host;
  state.snapshot = result;
  render(doc, state);
}

async function runCommand(doc, state, fn, fallback, opts = {}) {
  state.commanding = true;
  render(doc, state);
  const result = await fn();
  if (result.ok) {
    if (opts.success) setStatus(doc, "ok", opts.success);
  } else {
    const message = fallback === "Cast failed"
      ? bridge.formatPlayError(result)
      : bridge.formatBridgeError(result, fallback);
    setStatus(doc, "err", message);
  }
  await refreshStatus(doc, state);
  state.commanding = false;
  render(doc, state);
}

function render(doc, state) {
  const snapshot = state.snapshot || {};
  const session = snapshot.session || { state: "idle", capabilities: {} };
  const caps = session.capabilities || {};
  const active = session.state === "playing" || session.state === "paused";

  doc.getElementById("popup").dataset.state = state.stale ? "stale" : session.state || "idle";
  renderChip(doc, state, session);
  renderHealth(doc, snapshot.health || {});
  renderHistory(doc, state);

  const activeView = doc.getElementById("active-view");
  const idleView = doc.getElementById("idle-view");
  activeView.hidden = !active;
  idleView.hidden = active;

  if (active) {
    renderActive(doc, state, session, caps);
  } else {
    renderIdle(doc, state, caps);
  }
  doc.getElementById("launch-groovy").disabled = state.commanding;
}

function renderActive(doc, state, session, caps) {
  doc.getElementById("media-title").textContent =
    session.title || session.source_display || session.adapter_name || "Unknown source";
  doc.getElementById("source-line").textContent = session.source_display || session.adapter_name || "";

  const duration = Number(session.duration_ms || 0);
  const position = Number(session.position_ms || 0);
  const progress = doc.getElementById("progress-wrap");
  progress.hidden = duration <= 0;
  if (duration > 0) {
    doc.getElementById("position-label").textContent = formatTime(position);
    doc.getElementById("duration-label").textContent = formatTime(duration);
    const seek = doc.getElementById("seek");
    seek.max = String(duration);
    seek.value = String(Math.min(position, duration));
    seek.disabled = disabledFor(state, caps.can_seek);
  }

  doc.getElementById("pause").disabled = disabledFor(state, caps.can_pause);
  doc.getElementById("resume").disabled = disabledFor(state, caps.can_resume);
  doc.getElementById("stop").disabled = disabledFor(state, caps.can_stop);
  doc.getElementById("replay").disabled = disabledFor(state, caps.can_replay);
}

function renderIdle(doc, state, caps) {
  const url = state.activeTabURL || "";
  const castable = /^https?:/i.test(url);
  doc.getElementById("tab-url").textContent = url || "(no active tab)";
  doc.getElementById("cast").disabled = state.stale || state.commanding || !castable || caps.can_play === false;
  if (!castable && url) {
    setStatus(doc, "err", "Active tab has no http(s) URL");
  }
}

function renderChip(doc, state, session) {
  const chip = doc.getElementById("state-chip");
  const label = state.stale ? "STALE" : String(session.state || "idle").toUpperCase();
  chip.textContent = label;
  chip.className = "chip" + (!state.stale && session.state === "playing" ? " live" : "");
}

function renderHealth(doc, health) {
  doc.getElementById("health-bridge").textContent = health.bridge || "--";
  doc.getElementById("health-mister").textContent = health.mister || "--";
  doc.getElementById("health-url").textContent = health.url_adapter || "--";
}

function renderHistory(doc, state) {
  const root = doc.getElementById("history");
  root.textContent = "";
  for (const item of state.snapshot?.history || []) {
    const row = doc.createElement("div");
    row.className = "history-row";

    const playBtn = doc.createElement("button");
    playBtn.type = "button";
    playBtn.dataset.historyPlay = item.id;
    playBtn.textContent = item.title || item.url_display;
    playBtn.disabled = state.stale || state.commanding;
    playBtn.addEventListener("click", () => {
      runCommand(doc, state, () => bridge.historyPlay(item.id), "History play failed");
    });

    const delBtn = doc.createElement("button");
    delBtn.type = "button";
    delBtn.dataset.historyDelete = item.id;
    delBtn.textContent = "Delete";
    delBtn.disabled = state.stale || state.commanding;
    delBtn.addEventListener("click", () => {
      runCommand(doc, state, () => bridge.historyDelete(item.id), "History delete failed");
    });

    row.append(playBtn, delBtn);
    root.append(row);
  }
}

function showUnconfigured(doc) {
  doc.getElementById("configured").hidden = true;
  doc.getElementById("unconfigured").hidden = false;
}

function showConfigured(doc) {
  doc.getElementById("unconfigured").hidden = true;
  doc.getElementById("configured").hidden = false;
}

function disabledFor(state, capability) {
  return state.stale || state.commanding || capability === false;
}

function setStatus(doc, kind, msg) {
  const el = doc.getElementById("status");
  el.className = "status" + (kind ? " " + kind : "");
  el.textContent = msg;
}

function hostOf(url) {
  try {
    return new URL(url).host;
  } catch {
    return "";
  }
}

function formatTime(ms) {
  const totalSeconds = Math.max(0, Math.floor(ms / 1000));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) {
    return `${hours}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
  }
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

export function truncate(s, max) {
  if (s.length <= max) return s;
  if (max <= 3) return ".".repeat(Math.max(0, max));
  return s.slice(0, max - 3) + "...";
}

if (typeof document !== "undefined" && document.getElementById("popup")) {
  initPopup();
}
