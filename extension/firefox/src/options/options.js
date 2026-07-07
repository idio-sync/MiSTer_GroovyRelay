import { ensureBridgeHostPermission } from "../lib/permissions.js";

export function validateBridgeURL(input) {
  const trimmed = (input || "").trim();
  if (trimmed === "") return { ok: true, url: "" };

  let parsed;
  try {
    parsed = new URL(trimmed);
  } catch {
    return { ok: false, error: "Invalid URL" };
  }

  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return {
      ok: false,
      error: `Scheme must be http or https (got ${parsed.protocol.replace(":", "")})`,
    };
  }

  return { ok: true, url: parsed.origin };
}

export async function testConnection(bridgeURL) {
  if (!bridgeURL) return { ok: false, error: "Bridge not configured" };

  const permission = await ensureBridgeHostPermission(bridgeURL);
  if (!permission.ok) return permission;

  const ctrl = new AbortController();
  const timeout = setTimeout(() => ctrl.abort(), 10000);

  try {
    const res = await fetch(`${bridgeURL}/ui/companion/status`, {
      method: "GET",
      headers: { "X-Bridge-Extension": "1" },
      signal: ctrl.signal,
    });
    clearTimeout(timeout);
    if (res.ok) return { ok: true };
    const data = await res.json().catch(() => ({}));
    const suffix = data.error ? `: ${data.error}` : "";
    return { ok: false, error: `Bridge returned HTTP ${res.status}${suffix}` };
  } catch (e) {
    clearTimeout(timeout);
    if (e.name === "AbortError") return { ok: false, error: "Bridge timed out" };
    return { ok: false, error: bridgeUnreachableMessage(bridgeURL, e) };
  }
}

function bridgeUnreachableMessage(bridgeURL, error) {
  const base = `Bridge unreachable: ${error.message}`;
  const hint = bridgeURLHint(bridgeURL);
  return hint ? `${base}. ${hint}` : base;
}

function bridgeURLHint(bridgeURL) {
  let parsed;
  try {
    parsed = new URL(bridgeURL);
  } catch {
    return "";
  }

  if (parsed.protocol === "https:") {
    const httpURL = new URL(parsed.href);
    httpURL.protocol = "http:";
    return `The built-in relay listens on HTTP; try ${httpURL.origin}.`;
  }

  if (parsed.hostname === "localhost" || parsed.hostname === "127.0.0.1" || parsed.hostname === "::1") {
    return `${parsed.hostname} points at this browser. If the relay runs on another machine, use the relay's LAN IP.`;
  }

  return "";
}

export async function initOptionsPage(doc = document) {
  const input = doc.getElementById("bridge-url");
  const saveBtn = doc.getElementById("save");
  const testBtn = doc.getElementById("test");
  const statusEl = doc.getElementById("status");

  const stored = await browser.storage.sync.get("bridgeURL");
  input.value = stored.bridgeURL || "";

  saveBtn.addEventListener("click", async () => {
    const result = validateBridgeURL(input.value);
    if (!result.ok) {
      setStatus(statusEl, "err", result.error);
      return;
    }
    if (result.url) {
      const permission = await ensureBridgeHostPermission(result.url);
      if (!permission.ok) {
        setStatus(statusEl, "err", permission.error);
        return;
      }
    }
    await browser.storage.sync.set({ bridgeURL: result.url });
    input.value = result.url;
    setStatus(statusEl, "ok", result.url ? "Saved." : "Cleared.");
  });

  testBtn.addEventListener("click", async () => {
    const result = validateBridgeURL(input.value);
    if (!result.ok) {
      setStatus(statusEl, "err", result.error);
      return;
    }
    if (!result.url) {
      setStatus(statusEl, "err", "Enter a bridge URL first.");
      return;
    }
    setStatus(statusEl, "", "Testing...");
    const t = await testConnection(result.url);
    setStatus(statusEl, t.ok ? "ok" : "err", t.ok ? "Bridge is healthy." : t.error);
  });
}

function setStatus(el, kind, msg) {
  el.className = "status" + (kind ? " " + kind : "");
  el.textContent = msg;
}

if (typeof document !== "undefined" && document.getElementById("bridge-url")) {
  initOptionsPage();
}
