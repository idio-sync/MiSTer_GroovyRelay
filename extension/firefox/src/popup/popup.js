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
    setView(doc, "unconfigured");
    return state;
  }

  buildTickRing(doc);
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
  doc.getElementById("pause-resume")?.addEventListener("click", () => {
    const playing = state.snapshot?.session?.state === "playing";
    runCommand(doc, state, () => bridge.control(playing ? "pause" : "resume"),
      playing ? "Pause failed" : "Resume failed");
  });
  doc.getElementById("replay")?.addEventListener("click", () =>
    runCommand(doc, state, () => bridge.control("replay"), "Replay failed"));
  doc.getElementById("stop")?.addEventListener("click", () =>
    runCommand(doc, state, () => bridge.control("stop"), "Stop failed"));
  doc.getElementById("volume-range")?.addEventListener("input", (e) => {
    // live visual feedback only; no network until change
    renderVolumeVisual(doc, Number(e.target.value));
  });
  doc.getElementById("volume-range")?.addEventListener("change", (e) => {
    // Capture the value before runCommand's render() resets the range to the
    // current snapshot volume (renderVolume clobbers #volume-range.value).
    const value = Number(e.target.value);
    runCommand(doc, state, () => bridge.volume(value), "Volume failed");
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

function setView(doc, view) {
  // view ∈ "unconfigured" | "active" | "idle"
  doc.getElementById("unconfigured").hidden = view !== "unconfigured";
  doc.getElementById("active-view").hidden = view !== "active";
  doc.getElementById("idle-view").hidden = view !== "idle";
}

function render(doc, state) {
  const snapshot = state.snapshot || {};
  const session = snapshot.session || { state: "idle", capabilities: {} };
  const caps = session.capabilities || {};
  const active = session.state === "playing" || session.state === "paused";

  doc.getElementById("popup").dataset.state = state.stale ? "stale" : session.state || "idle";

  renderLeds(doc, state, snapshot);

  if (active) {
    setView(doc, "active");
    renderActive(doc, state, session, caps);
  } else {
    setView(doc, "idle");
    renderIdle(doc, state, caps);
  }

  renderVolume(doc, snapshot.output_volume ?? 0);

  doc.getElementById("launch-groovy").disabled = state.commanding;
}

function renderLeds(doc, state, snapshot) {
  const session = snapshot.session || {};
  const health = snapshot.health || {};
  const reachable = !state.stale;                 // a successful poll happened
  const playing = session.state === "playing" || session.state === "paused";
  doc.getElementById("led-pwr").classList.toggle("on", reachable);
  doc.getElementById("led-link").classList.toggle("on", reachable && health.bridge === "online");
  doc.getElementById("led-cast").classList.toggle("on", playing);
}

function setTier(doc, id, text) {
  const el = doc.getElementById(id);
  el.textContent = text || "";
  el.classList.toggle("is-empty", !text);
}

function renderActive(doc, state, session, caps) {
  setTier(doc, "vfd-primary", session.title || session.source_display);
  setTier(doc, "vfd-secondary", session.source_display);
  setTier(doc, "vfd-tertiary", session.resolved_via);
  doc.getElementById("vfd-state").textContent = (session.state || "").toUpperCase();

  const playing = session.state === "playing";
  doc.querySelector('[data-state-icon="playing"]').hidden = !playing;
  doc.querySelector('[data-state-icon="paused"]').hidden = playing;

  doc.getElementById("replay").disabled = disabledFor(state, caps.can_replay);
  doc.getElementById("stop").disabled = disabledFor(state, caps.can_stop);
  // pause/resume button enabled if either capability is available for the current state:
  const canToggle = playing ? caps.can_pause : caps.can_resume;
  doc.getElementById("pause-resume").disabled = disabledFor(state, canToggle);

  const duration = Number(session.duration_ms || 0);
  const position = Number(session.position_ms || 0);
  const wrap = doc.getElementById("progress-wrap");
  wrap.hidden = duration <= 0;
  if (duration > 0) {
    doc.getElementById("position-label").textContent = formatTime(position);
    doc.getElementById("duration-label").textContent = formatTime(duration);
    const pct = Math.max(0, Math.min(100, Math.round((position / duration) * 100)));
    doc.getElementById("percent-label").textContent = `${pct}%`;
    doc.getElementById("seek").style.setProperty("--seek-percent", `${pct}%`);
  }
}

function renderIdle(doc, state, caps) {
  const url = state.activeTabURL || "";
  const castable = /^https?:/i.test(url);
  doc.getElementById("tab-url").textContent = url || "(no active tab)";
  doc.getElementById("cast").disabled = state.stale || state.commanding || !castable || caps.can_play === false;
  if (!castable && url) {
    setStatus(doc, "err", "Active tab has no http(s) URL");
  }

  const snapshot = state.snapshot || {};
  const health = snapshot.health || {};
  doc.getElementById("health-bridge").textContent = health.bridge || "--";
  doc.getElementById("health-mister").textContent = health.mister || "--";
  doc.getElementById("health-url").textContent = health.url_adapter || "--";
}

function buildTickRing(doc) {
  const ring = doc.getElementById("volume-tick-ring");
  ring.textContent = "";
  for (let i = 0; i < 21; i++) {
    const t = doc.createElement("span");
    t.className = "volume-tick";
    ring.appendChild(t);
  }
}

function renderVolumeVisual(doc, v) {
  v = Math.max(0, Math.min(100, Number(v) || 0));
  doc.getElementById("volume-value").textContent = String(v);
  doc.getElementById("volume-control").style.setProperty("--volume-angle", `${-135 + (v / 100) * 270}deg`);
  const onCount = Math.round((v / 100) * 21);
  doc.querySelectorAll(".volume-tick").forEach((t, i) => t.classList.toggle("on", i < onCount));
}

function renderVolume(doc, v) {
  renderVolumeVisual(doc, v);
  doc.getElementById("volume-range").value = String(Math.max(0, Math.min(100, Number(v) || 0)));
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
