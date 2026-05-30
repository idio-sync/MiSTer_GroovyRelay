import { ensureBridgeHostPermission } from "./permissions.js";

const DEFAULT_TIMEOUT_MS = 15000;
let timeoutMs = DEFAULT_TIMEOUT_MS;
let plexIdentityCache = { bridgeURL: "", clientID: "" };

export function _setTimeoutForTest(ms) {
  timeoutMs = ms ?? DEFAULT_TIMEOUT_MS;
}

export function _resetPlexTimelineBridgeCacheForTest() {
  plexIdentityCache = { bridgeURL: "", clientID: "" };
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

export async function volume(level) {
  return companionFetch("/ui/companion/volume", {
    method: "POST",
    body: { output_volume: level },
  });
}

export async function launchGroovyMister() {
  return companionFetch("/ui/companion/launch", { method: "POST" });
}

export async function plexTimelinePoll(originalURL) {
  if (!isPlexTimelinePollURL(originalURL)) {
    return { ok: true, handled: false };
  }

  const bridgeURL = await getBridgeURL();
  if (!bridgeURL) return { ok: false, handled: false, error: "Bridge not configured" };

  const targetID = plexTargetClientIdentifier(originalURL);
  if (!targetID) return { ok: true, handled: false };

  const permission = await ensureBridgeHostPermission(bridgeURL);
  if (!permission.ok) return { ...permission, handled: false };

  const relayClientID = await relayClientIdentifier(bridgeURL);
  if (!relayClientID || relayClientID !== targetID) {
    return { ok: true, handled: false };
  }

  const ctrl = new AbortController();
  const timeout = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    const res = await fetch(relayTimelinePollURL(originalURL, bridgeURL), {
      method: "GET",
      headers: {
        Accept: "text/xml",
        "X-Bridge-Extension": "1",
      },
      signal: ctrl.signal,
    });
    clearTimeout(timeout);
    const body = await res.text();
    const headers = headersObject(res.headers);
    headers["content-type"] ||= "text/xml";
    headers["x-plex-client-identifier"] ||= relayClientID;
    return {
      ok: res.ok,
      handled: true,
      status: res.status,
      statusText: res.statusText,
      headers,
      body,
    };
  } catch (e) {
    clearTimeout(timeout);
    return {
      ok: false,
      handled: false,
      error: e.name === "AbortError" ? "Bridge timed out" : `Bridge unreachable: ${e.message}`,
    };
  }
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

function isPlexTimelinePollURL(rawURL) {
  try {
    return new URL(rawURL).pathname === "/player/timeline/poll";
  } catch {
    return false;
  }
}

function plexTargetClientIdentifier(rawURL) {
  try {
    return new URL(rawURL).searchParams.get("X-Plex-Target-Client-Identifier") || "";
  } catch {
    return "";
  }
}

function relayTimelinePollURL(originalURL, bridgeURL) {
  const source = new URL(originalURL);
  const relay = new URL(bridgeURL);
  const out = new URL("/player/timeline/poll", relay.origin);
  out.search = source.search;
  return out.toString();
}

async function relayClientIdentifier(bridgeURL) {
  if (plexIdentityCache.bridgeURL === bridgeURL && plexIdentityCache.clientID) {
    return plexIdentityCache.clientID;
  }

  const res = await fetch(new URL("/resources", new URL(bridgeURL).origin).toString(), {
    method: "GET",
    headers: {
      Accept: "text/xml",
      "X-Bridge-Extension": "1",
    },
  });
  const headerID = res.headers.get("X-Plex-Client-Identifier") || "";
  let clientID = headerID;
  if (!clientID) {
    const body = await res.text().catch(() => "");
    clientID = playerMachineIdentifier(body);
  }

  plexIdentityCache = { bridgeURL, clientID };
  return clientID;
}

function playerMachineIdentifier(xml) {
  const match = xml.match(/\bmachineIdentifier="([^"]+)"/);
  return match?.[1] || "";
}

function headersObject(headers) {
  const out = {};
  for (const [key, value] of headers.entries()) {
    out[key.toLowerCase()] = value;
  }
  return out;
}
