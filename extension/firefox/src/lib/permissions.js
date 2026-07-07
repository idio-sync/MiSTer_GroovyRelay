export function bridgeHostPermissionPattern(bridgeURL) {
  const parsed = new URL(bridgeURL);
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return "";
  return `${parsed.protocol}//${formatMatchHost(parsed.hostname)}/*`;
}

export async function ensureBridgeHostPermission(bridgeURL) {
  return bridgeHostPermission(bridgeURL, { request: true });
}

export async function requireBridgeHostPermission(bridgeURL) {
  return bridgeHostPermission(bridgeURL, { request: false });
}

async function bridgeHostPermission(bridgeURL, { request }) {
  const pattern = bridgeHostPermissionPattern(bridgeURL);
  const permissions = { origins: [pattern] };
  const permissionsAPI = globalThis.browser?.permissions;

  if (!pattern || !permissionsAPI?.contains) return { ok: true };

  let granted = false;
  try {
    granted = await permissionsAPI.contains(permissions);
  } catch {
    return { ok: true };
  }
  if (granted) return { ok: true };

  if (!request) {
    return {
      ok: false,
      error: `Bridge permission missing for ${pattern}. Open extension settings, test the bridge connection, and grant access to the bridge host.`,
    };
  }

  if (!permissionsAPI.request) {
    return {
      ok: false,
      error: `Bridge permission missing for ${pattern}. Grant this extension access to the bridge host and try again.`,
    };
  }

  try {
    granted = await permissionsAPI.request(permissions);
  } catch (e) {
    return {
      ok: false,
      error: `Bridge permission request failed: ${e.message}`,
    };
  }

  if (granted) return { ok: true };
  return {
    ok: false,
    error: `Bridge permission denied for ${pattern}. Allow this extension to access the bridge host and try again.`,
  };
}

function formatMatchHost(hostname) {
  if (hostname.includes(":") && !hostname.startsWith("[")) {
    return `[${hostname}]`;
  }
  return hostname;
}
