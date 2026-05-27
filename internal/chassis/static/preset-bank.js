(function () {
  'use strict';
  const bank = document.querySelector('.preset-bank');
  if (!bank) return;

  let lastProviderId = '';
  let lastChannelId = '';

  function slots() { return Array.from(bank.querySelectorAll('.preset')); }

  function clearLit() {
    slots().forEach((el) => el.classList.remove('lit'));
  }

  function applyLit(providerId, channelId) {
    clearLit();
    if (!providerId || !channelId) return;
    for (const el of slots()) {
      if (el.dataset.provider === providerId && el.dataset.channel === channelId) {
        el.classList.add('lit');
        break;
      }
    }
  }

  function parseAdapterRef(ref) {
    if (!ref || typeof ref !== 'string') return [null, null];
    if (!ref.startsWith('streams:')) return [null, null];
    const parts = ref.split(':');
    if (parts.length < 3) return [null, null];
    return [parts[1], parts[2]];
  }

  function onTransport(ev) {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    const [providerId, channelId] = parseAdapterRef(data.adapterRef);
    lastProviderId = providerId || '';
    lastChannelId = channelId || '';
    applyLit(providerId, channelId);
  }

  function pad2(n) {
    return n < 10 ? '0' + n : '' + n;
  }

  function applyPresets(payload) {
    if (!payload || !Array.isArray(payload.slots)) return;
    const elements = slots();
    payload.slots.forEach((s, i) => {
      const el = elements[i];
      if (!el) return;
      const filled = !!s.provider && !!s.channel;
      el.classList.toggle('empty', !filled);
      el.classList.toggle('live', !!s.live);
      el.dataset.slot = String(s.slot);
      el.dataset.provider = s.provider || '';
      el.dataset.channel = s.channel || '';
      // Re-render slot content.
      let num = el.querySelector('.num');
      if (!num) {
        num = document.createElement('div');
        num.className = 'num';
        el.insertBefore(num, el.firstChild);
      }
      num.textContent = pad2(s.slot);
      const name = el.querySelector('.name');
      const badge = el.querySelector('.badge');
      if (filled) {
        if (!name) {
          const div = document.createElement('div');
          div.className = 'name';
          div.textContent = s.title || '';
          el.appendChild(div);
        } else {
          name.textContent = s.title || '';
        }
        const badgeText = (s.badgeLabel || '') + (s.live ? ' · LIVE' : '');
        if (!badge) {
          const div = document.createElement('div');
          div.className = 'badge ' + (s.badgeClass || '');
          div.textContent = badgeText;
          el.appendChild(div);
        } else {
          badge.className = 'badge ' + (s.badgeClass || '');
          badge.textContent = badgeText;
        }
      } else {
        if (name) name.remove();
        if (badge) badge.remove();
      }
    });
  }

  function onPresets(ev) {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    applyPresets(data);
    // After re-render, re-apply LIT if a cast is still active. The
    // transport SSE event will fire on its own cadence, but we want
    // the LIT highlight to survive a presets-only mutation (e.g., the
    // currently-tuned channel just got starred into a new slot).
    applyLit(lastProviderId, lastChannelId);
    document.dispatchEvent(new CustomEvent('chassis:preset-rerendered'));
  }

  function reportError(chip) {
    if (window.Chassis && window.Chassis.input && typeof window.Chassis.input.showError === 'function') {
      window.Chassis.input.showError(chip || 'CAST FAILED');
    }
  }

  bank.addEventListener('click', async (e) => {
    const btn = e.target.closest('.preset');
    if (!btn || btn.classList.contains('empty')) return;
    if (e.target.closest('.preset-drag-clone')) return;
    const slot = btn.dataset.slot;
    if (!slot) return;
    try {
      const resp = await fetch('/receiver/preset/' + encodeURIComponent(slot) + '/cast', {
        method: 'POST',
        credentials: 'same-origin',
      });
      const body = await resp.json().catch(() => ({ ok: false, chip: 'CAST FAILED' }));
      if (!body.ok) reportError(body.chip);
    } catch (_) {
      reportError('CAST FAILED');
    }
  });

  if (window.Chassis && window.Chassis.events && typeof window.Chassis.events.subscribe === 'function') {
    window.Chassis.events.subscribe('transport', onTransport);
    window.Chassis.events.subscribe('presets', onPresets);
  }
})();
