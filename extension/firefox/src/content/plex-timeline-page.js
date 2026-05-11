(() => {
  const SOURCE = "mister-groovy-relay-plex-timeline";
  const REQUEST = "plexTimelinePoll";
  const RESPONSE = "plexTimelinePollResult";
  const TIMEOUT_MS = 12000;
  const pending = new Map();
  let nextRequestID = 1;

  window.addEventListener("message", (event) => {
    if (event.source !== window) return;
    const msg = event.data;
    if (!msg || msg.source !== SOURCE || msg.type !== RESPONSE) return;

    const entry = pending.get(msg.requestID);
    if (!entry) return;
    pending.delete(msg.requestID);
    clearTimeout(entry.timeout);
    entry.resolve(msg.result || { ok: true, handled: false });
  });

  installFetchBridge();
  installXHRBridge();

  function relayTimelinePoll(url) {
    return new Promise((resolve) => {
      const requestID = `${Date.now()}-${nextRequestID++}`;
      const timeout = setTimeout(() => {
        pending.delete(requestID);
        resolve({ ok: false, handled: false, error: "GroovyRelay bridge timed out" });
      }, TIMEOUT_MS);

      pending.set(requestID, { resolve, timeout });
      window.postMessage({ source: SOURCE, type: REQUEST, requestID, url }, "*");
    });
  }

  function installFetchBridge() {
    if (typeof window.fetch !== "function" || typeof window.Response !== "function") return;

    const nativeFetch = window.fetch.bind(window);
    window.fetch = async (input, init) => {
      const url = requestURL(input);
      if (requestMethod(input, init) === "GET" && isTimelinePollURL(url)) {
        const result = await relayTimelinePoll(url);
        if (result?.handled) {
          return new Response(result.body || "", {
            status: result.status || 200,
            statusText: result.statusText || "OK",
            headers: result.headers || { "content-type": "text/xml" },
          });
        }
      }
      return nativeFetch(input, init);
    };
  }

  function installXHRBridge() {
    if (typeof window.XMLHttpRequest !== "function") return;

    const NativeXHR = window.XMLHttpRequest;

    class GroovyRelayXMLHttpRequest {
      constructor() {
        this._native = new NativeXHR();
        this._listeners = new Map();
        this._method = "GET";
        this._url = "";
        this._async = true;
        this._handled = false;
        this._headers = {};
        this._readyState = NativeXHR.UNSENT;
        this._status = 0;
        this._statusText = "";
        this._responseText = "";
        this._response = "";
        this._responseXML = null;
        this.onreadystatechange = null;
        this.onload = null;
        this.onloadend = null;
        this.onerror = null;
        this.onabort = null;
        this.ontimeout = null;
        this.onprogress = null;

        for (const type of [
          "readystatechange",
          "load",
          "loadend",
          "error",
          "abort",
          "timeout",
          "progress",
        ]) {
          this._native.addEventListener(type, () => this._dispatch(type));
        }
      }

      open(method, url, async = true, user, password) {
        this._method = String(method || "GET").toUpperCase();
        this._url = String(url);
        this._async = async !== false;
        this._handled = false;
        this._native.open(method, url, async, user, password);
      }

      setRequestHeader(name, value) {
        this._native.setRequestHeader(name, value);
      }

      async send(body) {
        if (this._async && this._method === "GET" && isTimelinePollURL(this._url)) {
          const result = await relayTimelinePoll(this._url);
          if (result?.handled) {
            this._complete(result);
            return;
          }
        }
        this._native.send(body);
      }

      abort() {
        if (this._handled) {
          this._dispatch("abort");
          this._dispatch("loadend");
          return;
        }
        this._native.abort();
      }

      getResponseHeader(name) {
        if (!this._handled) return this._native.getResponseHeader(name);
        return this._headers[String(name).toLowerCase()] || null;
      }

      getAllResponseHeaders() {
        if (!this._handled) return this._native.getAllResponseHeaders();
        return Object.entries(this._headers)
          .map(([key, value]) => `${key}: ${value}`)
          .join("\r\n");
      }

      overrideMimeType(mime) {
        this._native.overrideMimeType(mime);
      }

      addEventListener(type, listener) {
        if (!this._listeners.has(type)) this._listeners.set(type, new Set());
        this._listeners.get(type).add(listener);
      }

      removeEventListener(type, listener) {
        this._listeners.get(type)?.delete(listener);
      }

      dispatchEvent(event) {
        this._dispatch(event.type, event);
        return true;
      }

      _complete(result) {
        const body = result.body || "";
        this._handled = true;
        this._headers = normalizeHeaders(result.headers || { "content-type": "text/xml" });
        this._status = result.status || 200;
        this._statusText = result.statusText || "OK";
        this._responseText = body;
        this._response = body;
        this._responseXML = parseXML(body);

        this._readyState = NativeXHR.HEADERS_RECEIVED;
        this._dispatch("readystatechange");
        this._readyState = NativeXHR.LOADING;
        this._dispatch("readystatechange");
        this._readyState = NativeXHR.DONE;
        this._dispatch("readystatechange");
        this._dispatch("load");
        this._dispatch("loadend");
      }

      _dispatch(type, event = null) {
        const e = event || makeEvent(type);
        const handler = this[`on${type}`];
        if (typeof handler === "function") handler.call(this, e);
        for (const listener of this._listeners.get(type) || []) {
          if (typeof listener === "function") listener.call(this, e);
          else if (listener && typeof listener.handleEvent === "function") {
            listener.handleEvent(e);
          }
        }
      }
    }

    for (const [name, value] of [
      ["UNSENT", NativeXHR.UNSENT],
      ["OPENED", NativeXHR.OPENED],
      ["HEADERS_RECEIVED", NativeXHR.HEADERS_RECEIVED],
      ["LOADING", NativeXHR.LOADING],
      ["DONE", NativeXHR.DONE],
    ]) {
      Object.defineProperty(GroovyRelayXMLHttpRequest, name, { value });
      Object.defineProperty(GroovyRelayXMLHttpRequest.prototype, name, { value });
    }

    defineNativeGetter(GroovyRelayXMLHttpRequest, "readyState", "_readyState");
    defineNativeGetter(GroovyRelayXMLHttpRequest, "status", "_status");
    defineNativeGetter(GroovyRelayXMLHttpRequest, "statusText", "_statusText");
    defineNativeGetter(GroovyRelayXMLHttpRequest, "responseText", "_responseText");
    defineNativeGetter(GroovyRelayXMLHttpRequest, "response", "_response");
    defineNativeGetter(GroovyRelayXMLHttpRequest, "responseXML", "_responseXML");
    defineNativeGetter(GroovyRelayXMLHttpRequest, "responseURL", "_url");
    defineNativePassThrough(GroovyRelayXMLHttpRequest, "responseType");
    defineNativePassThrough(GroovyRelayXMLHttpRequest, "timeout");
    defineNativePassThrough(GroovyRelayXMLHttpRequest, "withCredentials");
    Object.defineProperty(GroovyRelayXMLHttpRequest.prototype, "upload", {
      get() {
        return this._native.upload;
      },
    });

    window.XMLHttpRequest = GroovyRelayXMLHttpRequest;
  }

  function requestURL(input) {
    if (typeof input === "string") return input;
    if (input instanceof URL) return input.href;
    return input?.url || "";
  }

  function requestMethod(input, init) {
    return String(init?.method || input?.method || "GET").toUpperCase();
  }

  function isTimelinePollURL(rawURL) {
    try {
      return new URL(rawURL, window.location.href).pathname === "/player/timeline/poll";
    } catch {
      return false;
    }
  }

  function normalizeHeaders(headers) {
    const out = {};
    for (const [key, value] of Object.entries(headers)) {
      out[key.toLowerCase()] = value;
    }
    return out;
  }

  function parseXML(body) {
    if (typeof DOMParser !== "function") return null;
    try {
      return new DOMParser().parseFromString(body, "text/xml");
    } catch {
      return null;
    }
  }

  function makeEvent(type) {
    try {
      return new Event(type);
    } catch {
      return { type };
    }
  }

  function defineNativeGetter(Ctor, prop, handledField) {
    Object.defineProperty(Ctor.prototype, prop, {
      get() {
        return this._handled ? this[handledField] : this._native[prop];
      },
    });
  }

  function defineNativePassThrough(Ctor, prop) {
    Object.defineProperty(Ctor.prototype, prop, {
      get() {
        return this._native[prop];
      },
      set(value) {
        this._native[prop] = value;
      },
    });
  }
})();
