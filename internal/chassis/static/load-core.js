// Receiver chassis status-bar "Load core" button.
//
// Wires the status-bar Load-core button to the same backend action the
// settings drawer uses (POST /receiver/settings/action/launch-core), which
// SSH-sends the canonical load_core command to the MiSTer using the saved
// credentials. Single-flight: clicks are ignored while a launch is in
// flight. Visual states mirror the existing CSS:
//   - .loading  amber pulse while the request is in flight
//   - .loaded   green resting/success state (also the default at render)
//   - .error    red state on failure
// Failures switch to the error state and surface the reason via the button
// title + a console warning (the main page has no toast surface).
(() => {
  'use strict';

  const ENDPOINT = '/receiver/settings/action/launch-core';

  function button() {
    return document.getElementById('status-load-core-btn');
  }

  function setState(btn, state, title) {
    btn.classList.remove('loading', 'loaded', 'error');
    if (state) {
      btn.classList.add(state);
    }
    if (title) {
      btn.title = title;
    } else {
      btn.removeAttribute('title');
    }
  }

  async function launch(btn) {
    if (btn.disabled || btn.classList.contains('loading')) {
      return;
    }
    btn.disabled = true;
    setState(btn, 'loading', 'Sending load_core to MiSTer…');

    let body = {};
    try {
      const res = await fetch(ENDPOINT, {
        method: 'POST',
        credentials: 'same-origin',
      });
      body = await res.json().catch(() => ({}));
    } catch (err) {
      console.warn('load-core: request failed', err);
      setState(btn, 'error', 'Load failed · network error');
      btn.disabled = false;
      return;
    }

    if (body && body.ok) {
      setState(btn, 'loaded', `Core sent · ${body.host || ''}`.trim());
    } else if (body && body.chip) {
      console.warn('load-core: not ready', body.chip);
      setState(btn, 'error', `Load failed · ${body.chip}`);
    } else {
      const reason = (body && body.error) || 'unknown error';
      console.warn('load-core: launch failed', reason);
      setState(btn, 'error', `Load failed · ${reason}`);
    }
    btn.disabled = false;
  }

  function init() {
    const btn = button();
    if (!btn) {
      return;
    }
    btn.addEventListener('click', () => launch(btn));
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
