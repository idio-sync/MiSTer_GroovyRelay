(function () {
  const bank = document.querySelector('.preset-bank');
  if (!bank) return;
  const slots = Array.from(bank.querySelectorAll('.preset'));

  function clearLit() {
    slots.forEach((el) => el.classList.remove('lit'));
  }

  function applyLit(providerId, channelId) {
    clearLit();
    if (!providerId || !channelId) return;
    for (const el of slots) {
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
    applyLit(providerId, channelId);
  }

  function reportError(chip) {
    if (window.Chassis && window.Chassis.input && typeof window.Chassis.input.showError === 'function') {
      window.Chassis.input.showError(chip || 'CAST FAILED');
    }
  }

  bank.addEventListener('click', async (e) => {
    const btn = e.target.closest('.preset');
    if (!btn || btn.classList.contains('empty')) return;
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
  }
})();
