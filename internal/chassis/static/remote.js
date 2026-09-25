// Receiver chassis phone remote. Below 600px the chassis defaults to a
// remote-control view (chassis.css). This script owns:
//   - the PANEL/REMOTE toggle: body[data-phone-view] = remote | panel,
//     remembered per browser (shell.html's pre-paint script applies a
//     remembered panel view before first paint);
//   - the 1-12 keypad: a key presses the matching preset tile, so casting
//     and setup gating stay in preset-bank.js; an empty slot flashes EMPTY
//     on the VFD instead;
//   - VOL-/VOL+: step the existing volume range and fire its input/change
//     events, so volume-knob.js posts and paints as if it were dragged.
(() => {
  'use strict';

  const KEY = 'chassis.phoneView';

  function load() {
    try {
      return window.localStorage.getItem(KEY) === 'panel' ? 'panel' : 'remote';
    } catch (err) {
      return 'remote';
    }
  }

  function save(view) {
    try {
      window.localStorage.setItem(KEY, view);
    } catch (err) {
      // Private mode or blocked storage: the choice just is not remembered.
    }
  }

  function flash(primary, secondary, tertiary) {
    const vfd = window.Chassis && window.Chassis.vfd;
    if (vfd && typeof vfd.flash === 'function') vfd.flash(primary, secondary, tertiary);
  }

  function applyView(toggle, view) {
    document.body.setAttribute('data-phone-view', view);
    if (!toggle) return;
    const toRemote = view === 'panel';
    toggle.textContent = toRemote ? 'Remote' : 'Panel';
    toggle.setAttribute('aria-label', toRemote ? 'Show the remote' : 'Show the full panel');
  }

  function recallPreset(slot) {
    const tile = document.querySelector(`.preset-bank .preset[data-slot="${slot}"]`);
    if (!tile || tile.classList.contains('empty')) {
      flash(`PRESET ${slot}`, 'EMPTY', '');
      return;
    }
    tile.click();
  }

  function stepVolume(delta) {
    const range = document.querySelector('[data-volume-range]');
    if (!range) return;
    const next = Math.max(0, Math.min(100, (Number(range.value) || 0) + delta));
    range.value = String(next);
    range.dispatchEvent(new Event('input', { bubbles: true }));
    range.dispatchEvent(new Event('change', { bubbles: true }));
    flash('VOLUME', String(next), '');
  }

  function init() {
    const toggle = document.querySelector('[data-phone-view-toggle]');
    let view = load();
    applyView(toggle, view);
    if (toggle) {
      toggle.addEventListener('click', () => {
        view = view === 'remote' ? 'panel' : 'remote';
        applyView(toggle, view);
        save(view);
      });
    }
    document.querySelectorAll('[data-remote-preset]').forEach((key) => {
      key.addEventListener('click', () => recallPreset(key.getAttribute('data-remote-preset')));
    });
    document.querySelectorAll('[data-remote-volume]').forEach((key) => {
      key.addEventListener('click', () => stepVolume(Number(key.getAttribute('data-remote-volume')) || 0));
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
