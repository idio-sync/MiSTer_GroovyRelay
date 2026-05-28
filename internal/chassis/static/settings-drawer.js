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

  window.Chassis = window.Chassis || {};
  window.Chassis.settings = {}; // shared namespace for sub-modules

  // Save helper: POSTs one field-value pair, returns parsed JSON or null
  // on network error.
  async function saveField(name, value) {
    const form = new URLSearchParams();
    form.set(name, value);
    let res;
    try {
      res = await fetch('/receiver/settings/bridge', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: form.toString(),
      });
    } catch (err) {
      console.warn('settings save network error:', err);
      return { netErr: true };
    }
    let body = {};
    try { body = await res.json(); } catch (e) { /* leave empty */ }
    return { status: res.status, body };
  }

  function findRow(input) {
    let el = input;
    while (el && !el.classList.contains('field-row')) el = el.parentElement;
    return el;
  }

  // Field error painting + clearing (full impl lands in Task 27).
  function clearFieldError(name) {
    const input = drawer.querySelector(`[name="${name}"]`);
    if (!input) return;
    const row = findRow(input);
    if (!row) return;
    row.classList.remove('has-err');
    const err = row.querySelector('.field-err');
    if (err) err.remove();
  }
  function paintFieldError(name, msg) {
    const input = drawer.querySelector(`[name="${name}"]`);
    if (!input) return;
    const row = findRow(input);
    if (!row) return;
    row.classList.add('has-err');
    let err = row.querySelector('.field-err');
    if (!err) {
      err = document.createElement('div');
      err.className = 'field-err';
      input.parentElement.insertAdjacentElement('afterend', err);
    }
    err.textContent = msg;
  }
  function markHasValue(name, value) {
    const input = drawer.querySelector(`[name="${name}"]`);
    if (!input) return;
    if (value !== '') input.classList.add('has-value');
    else input.classList.remove('has-value');
  }

  // Wire blur on text/number/password/path inputs; change on selects.
  drawer.querySelectorAll('input.field-input, select.field-input').forEach(el => {
    const evt = el.tagName === 'SELECT' ? 'change' : 'blur';
    el.addEventListener(evt, async () => {
      const name = el.name;
      if (!name) return;
      clearFieldError(name);
      const result = await saveField(name, el.value);
      if (!result) return;
      if (result.netErr) {
        // Task 27 renders into #settings-notice; for now log.
        console.warn('settings save: network error');
        return;
      }
      const { status, body } = result;
      if (status >= 200 && status < 300 && body.ok) {
        markHasValue(name, el.value);
        // Task 27 handles the REBOOT toast.
        return;
      }
      if (body && body.errors) {
        for (const fname of Object.keys(body.errors)) {
          paintFieldError(fname, body.errors[fname]);
        }
        return;
      }
      // Chip-style failure or unknown shape — Task 27 handles the notice.
      paintFieldError(name, (body && body.chip) || 'save failed');
    });
  });

  // Switch click handler: optimistic toggle, revert on 4xx.
  drawer.querySelectorAll('button.switch[data-field]').forEach(btn => {
    btn.addEventListener('click', async () => {
      if (btn.disabled) return;
      const name = btn.dataset.field;
      const next = !btn.classList.contains('on');
      btn.classList.toggle('on', next);
      btn.setAttribute('aria-pressed', next ? 'true' : 'false');
      clearFieldError(name);
      const result = await saveField(name, next ? 'true' : 'false');
      if (!result) return;
      if (result.netErr) {
        // Revert.
        btn.classList.toggle('on', !next);
        btn.setAttribute('aria-pressed', !next ? 'true' : 'false');
        return;
      }
      const { status, body } = result;
      if (status >= 200 && status < 300 && body.ok) return;
      // Revert + paint error.
      btn.classList.toggle('on', !next);
      btn.setAttribute('aria-pressed', !next ? 'true' : 'false');
      if (body && body.errors && body.errors[name]) {
        paintFieldError(name, body.errors[name]);
      } else {
        paintFieldError(name, (body && body.chip) || 'save failed');
      }
    });
  });

  // Expose internals for Tasks 25-27 and tests.
  window.Chassis.settings.saveField = saveField;
  window.Chassis.settings.paintFieldError = paintFieldError;
  window.Chassis.settings.clearFieldError = clearFieldError;
  window.Chassis.settings.markHasValue = markHasValue;
})();
