import "./lib/browser-polyfill.js";
import { play, getBridgeURL, formatBridgeError } from "./lib/bridge.js";

const MENU_LINK = "mgr-cast-link";
const MENU_VIDEO = "mgr-cast-video";

browser.runtime.onInstalled.addListener(async (details) => {
  browser.contextMenus.create({
    id: MENU_LINK,
    title: "Cast link to MiSTer",
    contexts: ["link"],
  });
  browser.contextMenus.create({
    id: MENU_VIDEO,
    title: "Cast video to MiSTer",
    contexts: ["video"],
  });

  if (details.reason === "install") {
    const url = await getBridgeURL();
    if (!url) {
      browser.runtime.openOptionsPage();
    }
  }
});

browser.contextMenus.onClicked.addListener(async (info) => {
  let url;
  if (info.menuItemId === MENU_LINK) {
    url = info.linkUrl;
  } else if (info.menuItemId === MENU_VIDEO) {
    url = info.srcUrl;
  } else {
    return;
  }

  if (!url) {
    notify("error", "No URL on click target");
    return;
  }

  const result = await play(url);
  if (result.ok) {
    notify("ok", `Playing on MiSTer: ${truncateForToast(url)}`);
  } else {
    notify("error", formatBridgeError(result, "Cast failed"));
  }
});

function notify(kind, message) {
  browser.notifications.create({
    type: "basic",
    iconUrl: browser.runtime.getURL("icons/icon-48.png"),
    title: kind === "ok" ? "MiSTer GroovyRelay" : "MiSTer GroovyRelay (error)",
    message: message.slice(0, 200),
  });
}

function truncateForToast(s) {
  if (s.length <= 50) return s;
  const display = s.replace(/^https?:\/\//, "");
  if (display.length <= 50) return display;
  return display.slice(0, 47) + "...";
}
