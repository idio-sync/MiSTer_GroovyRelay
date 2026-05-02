const DEFAULT_TIMEOUT_MS = 15000;
let timeoutMs = DEFAULT_TIMEOUT_MS;

export function _setTimeoutForTest(ms) {
  timeoutMs = ms ?? DEFAULT_TIMEOUT_MS;
}

export async function getBridgeURL() {
  const result = await browser.storage.sync.get("bridgeURL");
  return result.bridgeURL || "";
}

export async function play(url, mode = "auto") {
  const bridgeURL = await getBridgeURL();
  if (!bridgeURL) return { ok: false, error: "Bridge not configured" };

  const ctrl = new AbortController();
  const timeout = setTimeout(() => ctrl.abort(), timeoutMs);

  let res;
  try {
    res = await fetch(`${bridgeURL}/ui/adapter/url/play`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Bridge-Extension": "1",
      },
      body: JSON.stringify({ url, mode }),
      signal: ctrl.signal,
    });
  } catch (e) {
    clearTimeout(timeout);
    if (e.name === "AbortError") return { ok: false, error: "Bridge timed out" };
    return { ok: false, error: `Bridge unreachable: ${e.message}` };
  }
  clearTimeout(timeout);

  if (res.ok) {
    const data = await res.json().catch(() => ({}));
    return { ok: true, adapter_ref: data.adapter_ref };
  }

  const errText = await res.text().catch(() => "");
  let errMsg = `HTTP ${res.status}`;
  try {
    const body = JSON.parse(errText);
    if (body.error) errMsg = body.error;
  } catch {
    // Keep the HTTP <code> fallback for non-JSON responses.
  }
  return { ok: false, status: res.status, error: errMsg };
}

export function formatPlayError(result) {
  if (!result || result.ok) return "";
  if (!result.status || result.error === `HTTP ${result.status}`) {
    return result.error || "Cast failed";
  }
  if (result.status >= 400 && result.status < 500) {
    return `Bridge rejected: ${result.error}`;
  }
  if (result.status >= 500) {
    return `Cast failed: ${result.error}`;
  }
  return result.error || `HTTP ${result.status}`;
}
