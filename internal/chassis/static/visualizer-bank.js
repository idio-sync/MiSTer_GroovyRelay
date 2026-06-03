// Receiver chassis visualizer bank. Phase 1 / Spec 4.
//
// Uses the shared /ui/events EventSource exposed by vfd-live.js and
// posts visualizer mode changes back to /ui/visualizer.
(() => {
  'use strict';

  if (!window.Chassis) {
    console.warn('visualizer-bank: window.Chassis missing; chassis.js failed to load?');
    return;
  }

  let inFlightMode = false;
  let queuedMode = null;

  function buttons() {
    return Array.from(document.querySelectorAll('[data-viz]'));
  }

  function setMode(mode) {
    if (!mode) {
      return;
    }

    buttons().forEach((btn) => {
      const active = btn.dataset.viz === mode;
      btn.classList.toggle('active', active);
      btn.classList.toggle('lit', active);
      btn.setAttribute('aria-checked', active ? 'true' : 'false');
    });
  }

  function handleVisualizerEvent(ev) {
    try {
      const { mode } = JSON.parse(ev.data);
      setMode(mode);
    } catch (err) {
      console.warn('visualizer-bank: bad visualizer payload', ev.data, err);
    }
  }

  function press(btn) {
    btn.classList.add('pressed');
    window.setTimeout(() => {
      btn.classList.remove('pressed');
    }, 180);
  }

  function postMode(mode) {
    const body = new URLSearchParams();
    body.set('mode', mode);
    return fetch('/ui/visualizer', {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
      },
      body,
    });
  }

  async function drainSaves() {
    if (inFlightMode) {
      return;
    }
    inFlightMode = true;
    try {
      while (queuedMode) {
        const mode = queuedMode;
        queuedMode = null;
        try {
          const res = await postMode(mode);
          if (res.status !== 204) {
            const text = await res.text().catch(() => '');
            console.warn('visualizer-bank: save failed', res.status, text);
          }
        } catch (err) {
          console.warn('visualizer-bank: mode POST failed', err);
        }
      }
    } finally {
      inFlightMode = false;
    }
  }

  function saveMode(mode) {
    queuedMode = mode;
    drainSaves();
  }

  function bindClicks() {
    buttons().forEach((btn) => {
      btn.addEventListener('click', () => {
        const mode = btn.dataset.viz;
        if (!mode) {
          return;
        }
        press(btn);
        saveMode(mode);
      });
    });
  }

  function init() {
    bindClicks();
    window.Chassis.events.subscribe('visualizer', handleVisualizerEvent);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
