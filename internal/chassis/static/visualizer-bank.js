// Receiver chassis visualizer bank. Phase 1 / Spec 4.
//
// Uses the shared /receiver/events EventSource exposed by vfd-live.js and
// posts visualizer mode changes back to /receiver/visualizer.
(() => {
  'use strict';

  if (!window.Chassis) {
    console.warn('visualizer-bank: window.Chassis missing; chassis.js failed to load?');
    return;
  }

  let source = null;

  function buttons() {
    return Array.from(document.querySelectorAll('[data-viz]'));
  }

  function isPreview(btn) {
    return btn.classList.contains('viz-btn--preview') || btn.disabled || btn.getAttribute('aria-disabled') === 'true';
  }

  function setMode(mode) {
    if (!mode) {
      return;
    }

    buttons().forEach((btn) => {
      const active = !isPreview(btn) && btn.dataset.viz === mode;
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

  function attachSource(nextSource) {
    if (!nextSource || nextSource === source) {
      return;
    }
    if (source) {
      source.removeEventListener('visualizer', handleVisualizerEvent);
    }
    source = nextSource;
    source.addEventListener('visualizer', handleVisualizerEvent);
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
    return fetch('/receiver/visualizer', {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
      },
      body,
    });
  }

  function bindClicks() {
    buttons().forEach((btn) => {
      if (isPreview(btn)) {
        return;
      }
      btn.addEventListener('click', () => {
        const mode = btn.dataset.viz;
        if (!mode) {
          return;
        }
        press(btn);
        postMode(mode).catch((err) => {
          console.warn('visualizer-bank: mode POST failed', err);
        });
      });
    });
  }

  function init() {
    bindClicks();
    if (window.Chassis.events && window.Chassis.events.source) {
      attachSource(window.Chassis.events.source);
    }
    document.addEventListener('chassis:eventsource', (ev) => {
      attachSource(ev.detail && ev.detail.source);
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
