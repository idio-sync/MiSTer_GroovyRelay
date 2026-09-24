// Receiver chassis DISPLAY key. Like the DISPLAY button on a real deck,
// it switches the meter window off and on; the state lives on
// body[data-meter-display] (chassis.css removes the meter row when off)
// and is remembered per browser. Storage failures leave the key working.
(() => {
  'use strict';

  const KEY = 'chassis.meterDisplay';

  function load() {
    try {
      return window.localStorage.getItem(KEY) === 'off' ? 'off' : 'on';
    } catch (err) {
      return 'on';
    }
  }

  function save(state) {
    try {
      window.localStorage.setItem(KEY, state);
    } catch (err) {
      // Private mode or blocked storage: the choice just is not remembered.
    }
  }

  function apply(btn, state) {
    document.body.setAttribute('data-meter-display', state);
    btn.setAttribute('aria-pressed', state === 'on' ? 'true' : 'false');
  }

  function init() {
    const btn = document.querySelector('[data-meter-display-toggle]');
    if (!btn) return;
    let state = load();
    apply(btn, state);
    btn.addEventListener('click', () => {
      state = state === 'on' ? 'off' : 'on';
      apply(btn, state);
      save(state);
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
