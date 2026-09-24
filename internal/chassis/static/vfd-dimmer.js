// Receiver chassis DIMMER key. Like the Pioneer/Sony front-panel button,
// each press steps the display glass bright -> dim -> dimmest -> bright.
// The level lives on body[data-dimmer] (chassis.css dims .screen) and is
// remembered per browser; storage failures leave the key working.
(() => {
  'use strict';

  const KEY = 'chassis.vfdDimmer';
  const NAMES = ['bright', 'dim', 'dimmest'];

  function load() {
    try {
      const n = Number(window.localStorage.getItem(KEY));
      return Number.isInteger(n) && n >= 0 && n < NAMES.length ? n : 0;
    } catch (err) {
      return 0;
    }
  }

  function save(level) {
    try {
      window.localStorage.setItem(KEY, String(level));
    } catch (err) {
      // Private mode or blocked storage: the level just is not remembered.
    }
  }

  function apply(btn, level) {
    document.body.setAttribute('data-dimmer', String(level));
    btn.setAttribute('aria-label', `Display dimmer: ${NAMES[level]}`);
  }

  function init() {
    const btn = document.querySelector('[data-vfd-dimmer]');
    if (!btn) return;
    let level = load();
    apply(btn, level);
    btn.addEventListener('click', () => {
      level = (level + 1) % NAMES.length;
      apply(btn, level);
      save(level);
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
