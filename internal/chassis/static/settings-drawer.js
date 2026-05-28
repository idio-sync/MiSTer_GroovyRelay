// settings-drawer.js — Phase 4A
// Wires the chassis settings drawer:
//   - gear button + close button toggle body.settings-open
//   - tab clicks switch active .settings-pane purely client-side
//   - blur on text/number/password/path/select fields POSTs to
//     /receiver/settings/bridge (added in Task 24)
//   - switch click optimistically toggles and reverts on 4xx (Task 25)
//   - probe button single-flights against /receiver/settings/action/probe-mister (Task 26)
//   - field-error JSON paints inline; chip JSON renders into the
//     drawer-local #settings-notice slot (Task 27)
//
// IMPORTANT: Sec-Fetch-Site is browser-controlled (forbidden request
// header). Client code must NOT attempt to set it.

(function () {
  'use strict';
  const body = document.body;
  const drawer = document.querySelector('.settings-panel');
  if (!drawer) return;

  // Gear button toggle (drawer open <-> closed; clicking gear while
  // open closes it).
  const gear = document.querySelector('[data-settings-toggle], #gear-btn');
  if (gear) {
    gear.addEventListener('click', () => body.classList.toggle('settings-open'));
  }

  // Close button always closes.
  const close = document.getElementById('settings-close');
  if (close) {
    close.addEventListener('click', () => body.classList.remove('settings-open'));
  }

  // Tab switching — each tab carries a data-tab attribute whose value
  // names the corresponding .settings-pane[data-pane] target.
  const tabs = drawer.querySelectorAll('.settings-tab');
  const panes = drawer.querySelectorAll('.settings-pane');
  tabs.forEach(t => {
    t.addEventListener('click', () => {
      tabs.forEach(x => x.classList.remove('active'));
      panes.forEach(x => x.classList.remove('active'));
      t.classList.add('active');
      const target = drawer.querySelector(`.settings-pane[data-pane="${t.dataset.tab}"]`);
      if (target) target.classList.add('active');
    });
  });

  // Tasks 24-27 extend this module below.
  window.Chassis = window.Chassis || {};
  window.Chassis.settings = {}; // shared namespace for sub-modules
})();
