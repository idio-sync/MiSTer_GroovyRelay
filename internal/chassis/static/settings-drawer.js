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
    close.addEventListener('click', () => {
      body.classList.remove('settings-open');
      stopAllPolls(); // stop any active PIN polls when drawer hides
    });
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
      stopAllPolls(); // leaving the Adapters pane (or any pane) stops stale polls
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

  // Drawer-local notice slot (#settings-notice).
  const notice = document.getElementById('settings-notice');
  let noticeTimer = null;

  function showNotice(text, variant) {
    if (!notice) return;
    notice.className = 'settings-notice ' + (variant || '');
    notice.textContent = text;
    notice.hidden = false;
    if (noticeTimer) clearTimeout(noticeTimer);
    noticeTimer = setTimeout(() => {
      notice.hidden = true;
    }, 5000);
  }
  function clearNotice() {
    if (!notice) return;
    notice.hidden = true;
    notice.className = 'settings-notice';
    notice.textContent = '';
    if (noticeTimer) { clearTimeout(noticeTimer); noticeTimer = null; }
  }

  // SkipEmpty guard: capture-phase blur listener on inputs flagged with
  // data-skip-empty="true" (today: only mister_ssh_password). When the
  // value is empty, stop propagation so the 4A bubble-phase blur handler
  // never fires the no-op POST. Server-side overlay still applies
  // preserve-on-empty as defence in depth.
  drawer.querySelectorAll('input.field-input[data-skip-empty="true"]').forEach(el => {
    el.addEventListener('blur', evt => {
      if (el.value === '') evt.stopImmediatePropagation();
    }, true); // capture phase — runs before the bubble-phase handler below
  });

  // Wire blur on text/number/password/path inputs; change on selects.
  drawer.querySelectorAll('input.field-input[data-field], select.field-input[data-field]').forEach(el => {
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
        clearNotice();
        if (body.scope === 'reboot') {
          // Extract only the label's first text node, excluding the help <span>.
          // fieldHelper renders <label>Host <span class="help">...</span></label>;
          // textContent would concatenate the help text into the toast.
          const labelEl = el.closest('.field-row')?.querySelector('label');
          const labelText = labelEl?.childNodes[0]?.textContent?.trim() || labelEl?.textContent?.trim() || name;
          showNotice(`Restart container to apply new ${labelText}`, 'ok');
        }
        return;
      }
      if (body && body.errors) {
        for (const fname of Object.keys(body.errors)) {
          paintFieldError(fname, body.errors[fname]);
        }
        return;
      }
      if (body && body.chip) {
        showNotice(body.chip, 'err');
        paintFieldError(name, body.chip);
        return;
      }
      paintFieldError(name, 'save failed');
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
      } else if (body && body.chip) {
        showNotice(body.chip, 'err');
        paintFieldError(name, body.chip);
      } else {
        paintFieldError(name, 'save failed');
      }
    });
  });

  // 4C: provider-row switches. The catalog switches deliberately use
  // data-catalog-field instead of data-field so the existing 4A bridge
  // switch handler at this same file's button.switch[data-field] selector
  // does NOT match — otherwise both handlers would fire on click and
  // the 4A path would POST a stray enabled=true to /receiver/settings/bridge.
  drawer.querySelectorAll('button.switch[data-catalog-provider]').forEach(el => {
    el.addEventListener('click', async () => {
      if (el.disabled) return;
      const id = el.dataset.catalogProvider;
      const field = el.dataset.catalogField;
      const next = !el.classList.contains('on');
      el.classList.toggle('on', next);
      el.setAttribute('aria-pressed', next ? 'true' : 'false');
      const form = new URLSearchParams();
      form.set(field, next ? 'true' : 'false');
      let body = {};
      try {
        const res = await fetch(`/receiver/settings/catalog/provider/${encodeURIComponent(id)}`, {
          method: 'POST', body: form, credentials: 'same-origin'
        });
        body = await res.json().catch(() => ({}));
        if (res.ok && body.ok) return;
      } catch (_) {
        body = { chip: 'WRITE FAILED' };
      }
      // Revert optimistic toggle.
      el.classList.toggle('on', !next);
      el.setAttribute('aria-pressed', !next ? 'true' : 'false');
      if (body.errors) {
        showNotice('BAD INPUT', 'err');
      } else if (body.error) {
        showNotice(body.error, 'err');
      } else if (body.chip) {
        showNotice(body.chip, 'err');
      } else {
        showNotice('WRITE FAILED', 'err');
      }
    });
  });

  // 4C: global HLS-override switch — single switch under the "Per-provider
  // HLS buffer override" section. Flips hls_buffer_disabled on every Live
  // provider in one save (server side). Same optimistic-toggle pattern
  // as the per-provider switches.
  const directHlsBtn = drawer.querySelector('button.switch[data-catalog-direct-hls]');
  if (directHlsBtn) directHlsBtn.addEventListener('click', async () => {
    if (directHlsBtn.disabled) return;
    const next = !directHlsBtn.classList.contains('on');
    directHlsBtn.classList.toggle('on', next);
    directHlsBtn.setAttribute('aria-pressed', next ? 'true' : 'false');
    const form = new URLSearchParams();
    form.set('disabled', next ? 'true' : 'false');
    let body = {};
    try {
      const res = await fetch('/receiver/settings/catalog/direct-stream-hls-buffer', {
        method: 'POST', body: form, credentials: 'same-origin'
      });
      body = await res.json().catch(() => ({}));
      if (res.ok && body.ok) return;
    } catch (_) {
      body = { chip: 'WRITE FAILED' };
    }
    directHlsBtn.classList.toggle('on', !next);
    directHlsBtn.setAttribute('aria-pressed', !next ? 'true' : 'false');
    if (body.errors) {
      showNotice('BAD INPUT', 'err');
    } else if (body.chip) {
      showNotice(body.chip, 'err');
    } else {
      showNotice('WRITE FAILED', 'err');
    }
  });

  // Probe action: single-flight, renders result into #probe-mister-result.
  const probeBtn = document.getElementById('probe-mister-btn');
  const probeOut = document.getElementById('probe-mister-result');

  function renderProbeResult(out, body) {
    if (!out) return;
    out.className = 'action-result shown';
    if (!body || typeof body !== 'object') {
      out.classList.add('err');
      out.textContent = '▸ ERROR · empty response';
      return;
    }
    if (body.ok) {
      out.classList.add('ok');
      out.textContent = `▸ ACK in ${body.latency_ms.toFixed(1)}ms · MiSTer ${body.host}:${body.port}`;
      return;
    }
    out.classList.add('err');
    if (body.error === 'timeout') {
      const elapsed = body.elapsed_ms ? `${body.elapsed_ms.toFixed(0)}ms` : '1000ms';
      out.textContent = `▸ NO ACK · ${elapsed} timeout · check host/port`;
      return;
    }
    if (body.chip) {
      out.textContent = `▸ ${body.chip}`;
      showNotice(body.chip, 'err');
      return;
    }
    out.textContent = `▸ ERROR · ${body.error || 'unknown'}`;
  }

  if (probeBtn) {
    probeBtn.addEventListener('click', async () => {
      if (probeBtn.disabled) return;
      probeBtn.disabled = true;
      if (probeOut) {
        probeOut.className = 'action-result';
        probeOut.textContent = '';
      }
      let res, body = {};
      try {
        res = await fetch('/receiver/settings/action/probe-mister', {
          method: 'POST',
          credentials: 'same-origin',
        });
        body = await res.json();
      } catch (err) {
        renderProbeResult(probeOut, { ok: false, error: String(err) });
        probeBtn.disabled = false;
        return;
      }
      renderProbeResult(probeOut, body);
      probeBtn.disabled = false;
    });
  }

  // Launch-core action: single-flight, renders into #launch-core-result,
  // toasts chip responses into the drawer-local notice slot.
  const launchBtn = document.getElementById('launch-core-btn');
  const launchOut = document.getElementById('launch-core-result');

  function renderLaunchResult(out, body) {
    if (!out) return;
    out.className = 'action-result shown';
    if (!body || typeof body !== 'object') {
      out.classList.add('err');
      out.textContent = '▸ ERROR · empty response';
      return;
    }
    if (body.ok) {
      out.classList.add('ok');
      out.textContent = `▸ Core sent · ${body.host || ''}`.trim();
      return;
    }
    if (body.chip) {
      // Chip responses (NOT READY) toast into the drawer notice slot;
      // the action-result slot stays empty for chip cases per spec.
      out.className = 'action-result';
      out.textContent = '';
      showNotice(body.chip, 'err');
      return;
    }
    out.classList.add('err');
    out.textContent = `▸ ERROR · ${body.error || 'unknown'}`;
  }

  if (launchBtn) {
    launchBtn.addEventListener('click', async () => {
      if (launchBtn.disabled) return;
      launchBtn.disabled = true;
      if (launchOut) {
        launchOut.className = 'action-result';
        launchOut.textContent = '';
      }
      let body = {};
      try {
        const res = await fetch('/receiver/settings/action/launch-core', {
          method: 'POST',
          credentials: 'same-origin',
        });
        body = await res.json();
      } catch (err) {
        renderLaunchResult(launchOut, { ok: false, error: 'network error' });
        launchBtn.disabled = false;
        return;
      }
      renderLaunchResult(launchOut, body);
      launchBtn.disabled = false;
    });
  }

  // 4C: restore-defaults inline two-step confirm. Click ⚠ Reset… → button
  // row morphs to "[This wipes config.toml.] [Cancel] [Confirm reset]";
  // confirm POSTs; cancel or 10s armed timeout returns to idle.
  // DOM construction uses createElement + textContent + replaceChildren
  // rather than innerHTML — even though the prompt content is fully
  // static, modeling safe patterns here prevents an implementer from
  // later interpolating dynamic content into the same code path and
  // reintroducing an XSS surface.
  (function initRestoreDefaults() {
    const row = document.getElementById('restore-defaults-row');
    if (!row) return;
    const idleBtn = document.getElementById('restore-defaults-btn');
    const result = document.getElementById('restore-defaults-result');
    if (!idleBtn || !result) return;

    let armTimer = null;

    function toIdle() {
      if (armTimer) { clearTimeout(armTimer); armTimer = null; }
      row.classList.remove('confirming');
      const rightCell = idleBtn.parentElement;
      rightCell.replaceChildren(idleBtn, result);
      idleBtn.disabled = false;
    }

    function toArmed() {
      row.classList.add('confirming');
      const rightCell = idleBtn.parentElement;
      const prompt = document.createElement('span');
      prompt.className = 'confirm-prompt';
      prompt.textContent = 'This wipes config.toml. ';
      const cancelBtn = document.createElement('button');
      cancelBtn.className = 'action-btn cancel';
      cancelBtn.type = 'button';
      cancelBtn.textContent = 'Cancel';
      cancelBtn.addEventListener('click', toIdle);
      const confirmBtn = document.createElement('button');
      confirmBtn.className = 'action-btn confirm';
      confirmBtn.type = 'button';
      confirmBtn.textContent = 'Confirm reset';
      confirmBtn.addEventListener('click', fire);
      rightCell.replaceChildren(prompt, cancelBtn, confirmBtn, result);
      armTimer = setTimeout(toIdle, 10_000);
    }

    async function fire() {
      if (armTimer) { clearTimeout(armTimer); armTimer = null; }
      row.querySelectorAll('button').forEach(b => { b.disabled = true; });
      result.className = 'action-result';
      result.textContent = '';
      let body = {};
      try {
        const res = await fetch('/receiver/settings/action/restore-defaults', {
          method: 'POST', credentials: 'same-origin'
        });
        body = await res.json().catch(() => ({}));
        if (res.ok && body.ok && body.scope === 'reboot') {
          toIdle();
          result.className = 'action-result shown ok';
          result.textContent = '▸ Defaults restored · restart to apply';
          showNotice('Defaults restored — restart container to apply', 'ok');
          return;
        }
      } catch (_) {
        body = { chip: 'WRITE FAILED' };
      }
      toIdle();
      if (body.chip) {
        showNotice(body.chip, 'err');
      } else if (body.error) {
        result.className = 'action-result shown err';
        result.textContent = `▸ ERROR · ${body.error}`;
      } else {
        result.className = 'action-result shown err';
        result.textContent = '▸ ERROR · unknown';
      }
    }

    idleBtn.addEventListener('click', toArmed);
  })();

  // Adapter save handlers — mirror the 4A bridge handlers but POST to
  // /receiver/settings/adapter/{adapter} with the adapter name pulled
  // from the data-adapter attribute. [data-field] and [data-adapter]
  // never coexist on one element, so bridge + adapter paths never both fire.

  document.addEventListener('click', (ev) => {
    const sw = ev.target.closest('button.switch[data-adapter]');
    if (!sw) return;
    ev.preventDefault();
    toggleAdapterSwitch(sw);
  });

  document.addEventListener('blur', (ev) => {
    const inp = ev.target.closest && ev.target.closest('input.field-input[data-adapter]');
    if (!inp) return;
    saveAdapterField(inp);
  }, true);

  async function toggleAdapterSwitch(btn) {
    const adapter = btn.getAttribute('data-adapter');
    const key = btn.getAttribute('name');
    const wasOn = btn.classList.contains('on');
    btn.classList.toggle('on');
    const body = new URLSearchParams();
    body.set(key, wasOn ? 'false' : 'true');
    try {
      const res = await fetch(`/receiver/settings/adapter/${encodeURIComponent(adapter)}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });
      const payload = await res.json();
      if (!payload.ok) {
        btn.classList.toggle('on', wasOn); // revert optimistic toggle to pre-click state
      }
      handleAdapterSaveResponse(btn, payload);
    } catch (e) {
      btn.classList.toggle('on');
      showNotice('NETWORK ERROR', 'err');
    }
  }

  async function saveAdapterField(inp) {
    const adapter = inp.getAttribute('data-adapter');
    const key = inp.getAttribute('name');
    if (inp.dataset.lastSaved === inp.value) return;
    const body = new URLSearchParams();
    body.set(key, inp.value);
    try {
      const res = await fetch(`/receiver/settings/adapter/${encodeURIComponent(adapter)}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });
      const payload = await res.json();
      handleAdapterSaveResponse(inp, payload);
      if (payload.ok) inp.dataset.lastSaved = inp.value;
    } catch (e) {
      showNotice('NETWORK ERROR', 'err');
    }
  }

  function handleAdapterSaveResponse(target, payload) {
    const key = target.getAttribute('name');
    if (payload.ok) {
      if (payload.scope === 'reboot') {
        const labelEl = target.closest('.field-row')?.querySelector('label');
        const labelText = labelEl?.childNodes[0]?.textContent?.trim() || labelEl?.textContent?.trim() || key;
        showNotice(`Restart container to apply new ${labelText}`, 'ok');
      }
      clearFieldError(key);
      return;
    }
    if (payload.errors) {
      const msg = payload.errors[key];
      if (msg) paintFieldError(key, msg);
      return;
    }
    if (payload.chip) {
      showNotice(payload.chip, 'err');
    }
  }

  // Streams-refresh action handler: single-flight, renders result into
  // #streams-refresh-result. Toasts chip errors into the drawer notice slot.
  document.addEventListener('click', async (ev) => {
    const btn = ev.target.closest('button[data-settings-action="streams-refresh"]');
    if (!btn) return;
    ev.preventDefault();
    if (btn.disabled) return;
    btn.disabled = true;
    const slot = document.getElementById('streams-refresh-result');
    if (slot) {
      slot.textContent = '…';
      slot.classList.remove('ok', 'err', 'shown');
      slot.classList.add('shown');
    }
    try {
      const res = await fetch('/receiver/settings/action/streams-refresh', { method: 'POST' });
      const payload = await res.json();
      if (slot) {
        if (payload.ok) {
          slot.textContent = payload.summary || 'Refreshed';
          slot.classList.add('ok');
        } else if (payload.chip) {
          showNotice(payload.chip, 'err');
          slot.textContent = '';
          slot.classList.remove('shown');
        } else if (payload.error) {
          slot.textContent = payload.error;
          slot.classList.add('err');
        }
      }
    } catch (e) {
      if (slot) {
        slot.textContent = 'Network error';
        slot.classList.add('err');
      }
    } finally {
      btn.disabled = false;
    }
  });

  // ─── Task 16: Link sub-section renderer + handlers ───────────────────────
  //
  // renderLinkView rebuilds a .settings-link container's inner DOM from a
  // LinkView object. Untrusted strings (error, linkedAs, code) go through
  // textContent — never innerHTML — so remote/operator text can't inject markup.

  function el(tag, cls, text) {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  function renderLinkView(container, view) {
    container.setAttribute('data-link-kind', view.kind || '');
    container.setAttribute('data-link-phase', view.phase || '');
    container.replaceChildren();
    container.appendChild(el('h5', 'settings-subhead', 'Account'));

    if (view.phase === 'linked') {
      const line = el('div', 'link-line ok');
      line.appendChild(el('span', 'link-status', view.linkedAs ? `✓ Linked as ${view.linkedAs}` : '✓ Linked'));
      const btn = el('button', 'action-btn ghost', 'Unlink');
      btn.type = 'button'; btn.setAttribute('data-link-action', 'unlink');
      line.appendChild(btn);
      container.appendChild(line);
      if (view.error) container.appendChild(el('div', 'link-warn', view.error));
      return;
    }
    if (view.phase === 'pending') {
      if (view.kind === 'pin') {
        const wrap = el('div', 'link-pin-wrap');
        wrap.appendChild(el('div', 'help', 'Enter this code at plex.tv/link:'));
        wrap.appendChild(el('div', 'link-pin', view.code || ''));
        const c = el('div', 'link-count');
        c.setAttribute('data-link-expires', String(view.expiresInSec || 0));
        c.appendChild(document.createTextNode('expires in '));
        c.appendChild(el('span', 'link-count-val', String(view.expiresInSec || 0)));
        c.appendChild(document.createTextNode('s'));
        wrap.appendChild(c);
        wrap.appendChild(el('div', 'link-waiting', '● waiting for plex.tv…'));
        container.appendChild(wrap);
      } else {
        container.appendChild(el('div', 'link-waiting', '↻ Linking…'));
      }
      return;
    }
    if (view.phase === 'unlinked' && view.kind === 'pin') {
      const line = el('div', 'link-line');
      const left = el('div');
      left.appendChild(el('span', 'badge off', 'OFF · not linked'));
      left.appendChild(el('div', 'help', 'Link this bridge to your Plex account to receive casts.'));
      line.appendChild(left);
      const btn = el('button', 'action-btn', 'Link Plex Account');
      btn.type = 'button'; btn.setAttribute('data-link-action', 'start');
      line.appendChild(btn);
      container.appendChild(line);
      return;
    }
    if (view.kind === 'credential' && view.phase === 'unlinked' && view.needsServerURL) {
      container.appendChild(el('div', 'help', 'Set a Server URL in the fields below — it saves automatically — then link.'));
      return;
    }
    // credential unlinked-with-url OR error → form
    const form = el('form', 'link-credform');
    form.setAttribute('data-link-action', 'start');
    (view.fields || []).forEach((f) => {
      const row = el('div', 'field-row');
      row.appendChild(el('label', null, f.label));
      const inp = el('input', 'field-input');
      inp.type = f.kind === 'secret' ? 'password' : 'text';
      inp.setAttribute('data-link-field', f.key);
      inp.setAttribute('name', f.key);
      inp.setAttribute('autocomplete', 'off');
      row.appendChild(inp);
      row.appendChild(el('span'));
      form.appendChild(row);
    });
    if (view.error) form.appendChild(el('div', 'link-warn', view.error));
    const actionRow = el('div', 'field-row action-row');
    actionRow.appendChild(el('label'));
    const submit = el('button', 'action-btn', 'Link ▸');
    submit.type = 'submit'; submit.setAttribute('data-link-submit', '');
    actionRow.appendChild(submit);
    actionRow.appendChild(el('span'));
    form.appendChild(actionRow);
    container.appendChild(form);
  }

  function adapterOfLink(node) {
    const sec = node.closest('[data-adapter-section]');
    return sec ? sec.getAttribute('data-adapter-section') : null;
  }

  async function postLink(adapter, action, body) {
    const res = await fetch(`/receiver/settings/adapter/${encodeURIComponent(adapter)}/link/${action}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body ? body.toString() : '',
    });
    return res.json();
  }

  // Start (PIN button or credential form submit) + Unlink, delegated.
  document.addEventListener('click', async (ev) => {
    const startBtn = ev.target.closest('button[data-link-action="start"]');
    const unlinkBtn = ev.target.closest('button[data-link-action="unlink"]');
    if (!startBtn && !unlinkBtn) return;
    ev.preventDefault();
    const btn = startBtn || unlinkBtn;
    if (btn.disabled) return;
    const container = btn.closest('.settings-link');
    const adapter = adapterOfLink(btn);
    if (!adapter) return;
    btn.disabled = true;
    try {
      const payload = await postLink(adapter, unlinkBtn ? 'unlink' : 'start', null);
      if (payload.ok && payload.view) {
        renderLinkView(container, payload.view);
        if (unlinkBtn) {
          stopPoll(adapter); // cancel any active PIN poll on successful unlink
        } else if (payload.view.phase === 'pending' && payload.view.kind === 'pin') {
          startPoll(adapter, container);
        }
      } else if (payload.chip) {
        showNotice(payload.chip, 'err');
      }
    } catch (e) {
      showNotice('NETWORK ERROR', 'err');
    } finally {
      btn.disabled = false;
    }
  });

  document.addEventListener('submit', async (ev) => {
    const form = ev.target.closest('form.link-credform');
    if (!form) return;
    ev.preventDefault();
    const container = form.closest('.settings-link');
    const adapter = adapterOfLink(form);
    if (!adapter) return;
    // Capture the field definitions before the optimistic repaint destroys the
    // live form, so we can rebuild it (submit re-enabled, inputs cleared) if the
    // link fails — otherwise "Linking…" stays stuck with no way to retry.
    const fields = Array.from(form.querySelectorAll('[data-link-field]')).map((inp) => {
      const row = inp.closest('.field-row');
      const label = row && row.querySelector('label');
      return {
        key: inp.getAttribute('data-link-field'),
        label: label ? label.textContent : inp.getAttribute('data-link-field'),
        kind: inp.type === 'password' ? 'secret' : 'text',
      };
    });
    const body = new URLSearchParams();
    form.querySelectorAll('[data-link-field]').forEach((inp) => body.set(inp.getAttribute('data-link-field'), inp.value));
    // Optimistic "Linking…" — rebuilds form with empty inputs (passwords cleared).
    renderLinkView(container, { kind: 'credential', phase: 'pending' });
    try {
      const payload = await postLink(adapter, 'start', body);
      if (payload.ok && payload.view) {
        renderLinkView(container, payload.view); // linked, or error with the form
      } else {
        // chip (BUSY/NOT READY) or {ok:false,error}: restore the form so the
        // operator can retry instead of being stranded on "Linking…".
        if (payload.chip) showNotice(payload.chip, 'err');
        renderLinkView(container, { kind: 'credential', phase: 'error', error: payload.error || '', fields });
      }
    } catch (e) {
      showNotice('NETWORK ERROR', 'err');
      renderLinkView(container, { kind: 'credential', phase: 'error', error: 'Network error', fields });
    }
  });

  // ─── Task 17: PIN poll controller ────────────────────────────────────────
  //
  // One poller per adapter. Uses setTimeout (never setInterval) so a slow
  // network response cannot stack concurrent requests. Stops on terminal
  // phase (linked|error), on expiry (expiresInSec ≤ 0), on explicit
  // stopPoll/stopAllPolls, or when the drawer/Adapters pane is hidden.

  // pollers: adapter name → { container, stopped, timer }
  const pollers = {};

  async function pollOnce(adapter) {
    const p = pollers[adapter];
    if (!p || p.stopped) return;
    let payload;
    try {
      const res = await fetch(
        `/receiver/settings/adapter/${encodeURIComponent(adapter)}/link/status`,
        { method: 'GET' }
      );
      payload = await res.json();
    } catch (e) {
      // Transient network error — keep polling until expiry.
      scheduleNextPoll(adapter);
      return;
    }
    // The drawer/pane may have closed (stopAllPolls) during the await — don't
    // repaint a now-hidden container with a late response.
    if (p.stopped) return;
    if (!payload.ok || !payload.view) { stopPoll(adapter); return; }
    renderLinkView(p.container, payload.view);
    const v = payload.view;
    // Stop priority: (1) terminal phase, (2) expiry; unlink + pane-close
    // are handled by stopPoll/stopAllPolls callers.
    if (v.phase === 'linked' || v.phase === 'error' || (v.expiresInSec || 0) <= 0) {
      stopPoll(adapter);
      return;
    }
    scheduleNextPoll(adapter);
  }

  function scheduleNextPoll(adapter) {
    const p = pollers[adapter];
    if (!p || p.stopped) return;
    p.timer = setTimeout(() => pollOnce(adapter), 2000);
  }

  function startPoll(adapter, container) {
    // Single-flight: no-op if already polling.
    if (pollers[adapter] && !pollers[adapter].stopped) return;
    pollers[adapter] = { container, stopped: false, timer: null };
    scheduleNextPoll(adapter);
  }

  function stopPoll(adapter) {
    const p = pollers[adapter];
    if (!p) return;
    p.stopped = true;
    if (p.timer) { clearTimeout(p.timer); p.timer = null; }
  }

  function stopAllPolls() {
    Object.keys(pollers).forEach(stopPoll);
  }

  // Expose internals for Tasks 25-27 and tests.
  window.Chassis.settings.saveField = saveField;
  window.Chassis.settings.paintFieldError = paintFieldError;
  window.Chassis.settings.clearFieldError = clearFieldError;
  window.Chassis.settings.markHasValue = markHasValue;
  window.Chassis.settings.showNotice = showNotice;
  window.Chassis.settings.clearNotice = clearNotice;
  window.Chassis.settings.renderLinkView = renderLinkView;
  // Task 17 — poll controller (used by Task 16 handlers + tests).
  window.Chassis.settings.startPoll = startPoll;
  window.Chassis.settings.stopPoll = stopPoll;
  window.Chassis.settings.stopAllPolls = stopAllPolls;
  // Test hooks — deterministic control without real timers.
  window.Chassis.settings.__pollTick = (adapter) => pollOnce(adapter);
  window.Chassis.settings.__pollActive = (adapter) => !!(pollers[adapter] && !pollers[adapter].stopped);
})();
