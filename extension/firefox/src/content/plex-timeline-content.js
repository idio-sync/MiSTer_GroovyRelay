(() => {
  const SOURCE = "mister-groovy-relay-plex-timeline";
  const REQUEST = "plexTimelinePoll";
  const RESPONSE = "plexTimelinePollResult";
  const runtime = globalThis.browser?.runtime || globalThis.chrome?.runtime;

  if (!runtime) return;

  window.addEventListener("message", async (event) => {
    if (event.source !== window) return;
    const msg = event.data;
    if (!msg || msg.source !== SOURCE || msg.type !== REQUEST || !msg.requestID) return;

    try {
      const result = await runtime.sendMessage({
        type: REQUEST,
        url: msg.url,
      });
      window.postMessage(
        {
          source: SOURCE,
          type: RESPONSE,
          requestID: msg.requestID,
          result: result || { ok: true, handled: false },
        },
        "*"
      );
    } catch (e) {
      window.postMessage(
        {
          source: SOURCE,
          type: RESPONSE,
          requestID: msg.requestID,
          result: { ok: false, handled: false, error: e.message },
        },
        "*"
      );
    }
  });

  injectPageShim();

  function injectPageShim() {
    const parent = document.documentElement || document.head || document.body;
    if (!parent) {
      document.addEventListener("readystatechange", injectPageShim, { once: true });
      return;
    }
    const script = document.createElement("script");
    script.src = runtime.getURL("src/content/plex-timeline-page.js");
    script.dataset.misterGroovyRelay = "plex-timeline";
    parent.appendChild(script);
    script.remove();
  }
})();
