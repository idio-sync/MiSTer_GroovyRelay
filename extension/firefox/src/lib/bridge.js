import { ensureBridgeHostPermission } from "./permissions.js";

const DEFAULT_TIMEOUT_MS = 15000;
let timeoutMs = DEFAULT_TIMEOUT_MS;

export function _setTimeoutForTest(ms) {
  timeoutMs = ms ?? DEFAULT_TIMEOUT_MS;
}

export async function getBridgeURL() {
  const result = await browser.storage.sync.get("bridgeURL");
  return result.bridgeURL || "";
}

async function companionFetch(path, options = {}) {
  const bridgeURL = await getBridgeURL();
  if (!bridgeURL) return { ok: false, error: "Bridge not configured" };

  const permission = await ensureBridgeHostPermission(bridgeURL);
  if (!permission.ok) return permission;

  const ctrl = new AbortController();
  const timeout = setTimeout(() => ctrl.abort(), timeoutMs);

  const headers = {
    "X-Bridge-Extension": "1",
    ...(options.body ? { "Content-Type": "application/json" } : {}),
    ...(options.headers || {}),
  };

  try {
    const res = await fetch(`${bridgeURL}${path}`, {
      method: options.method || "GET",
      headers,
      body: options.body ? JSON.stringify(options.body) : undefined,
      signal: ctrl.signal,
    });
    clearTimeout(timeout);

    const text = await res.text().catch(() => "");
    let data = {};
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = {};
      }
    }
    if (res.ok) return { ok: true, ...data };
    return {
      ok: false,
      status: res.status,
      error: data.error || `HTTP ${res.status}`,
    };
  } catch (e) {
    clearTimeout(timeout);
    if (e.name === "AbortError") return { ok: false, error: "Bridge timed out" };
    return { ok: false, error: `Bridge unreachable: ${e.message}` };
  }
}

export async function getStatus() {
  return companionFetch("/ui/companion/status");
}

export async function play(url, mode = "auto") {
  return companionFetch("/ui/companion/play", {
    method: "POST",
    body: { url, mode },
  });
}

export async function control(action, extra = {}) {
  return companionFetch("/ui/companion/control", {
    method: "POST",
    body: { action, ...extra },
  });
}

export async function historyPlay(id) {
  return companionFetch("/ui/companion/history/play", {
    method: "POST",
    body: { id },
  });
}

export async function historyDelete(id) {
  return companionFetch("/ui/companion/history/delete", {
    method: "POST",
    body: { id },
  });
}

export async function launchGroovyMister() {
  return companionFetch("/ui/companion/launch", { method: "POST" });
}

export function formatBridgeError(result, fallback = "Command failed") {
  if (!result || result.ok) return "";
  if (!result.status || result.error === `HTTP ${result.status}`) {
    return result.error || fallback;
  }
  if (result.status >= 400 && result.status < 500) {
    return `Bridge rejected: ${result.error}`;
  }
  if (result.status >= 500) {
    return `${fallback}: ${result.error}`;
  }
  return result.error || `HTTP ${result.status}`;
}

export function formatPlayError(result) {
  return formatBridgeError(result, "Cast failed");
}
