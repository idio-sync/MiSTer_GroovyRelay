(function () {
  'use strict';
  const bank = document.querySelector('.preset-bank');
  if (!bank) return;

  function preset(el) {
    return el && el.classList && el.classList.contains('preset') ? el : (el ? el.closest('.preset') : null);
  }

  // Shared reorder.js owns pointerdown, pointermove, pointerup, and Escape drag cancellation.

  function syncSlotNumber(el) {
    const num = el.querySelector('.num');
    if (num) num.textContent = String(el.dataset.slot || '').padStart(2, '0');
  }

  function snapshotVisual(el) {
    return {
      className: el.className,
      html: el.innerHTML,
      provider: el.dataset.provider || '',
      channel: el.dataset.channel || '',
    };
  }

  function restoreVisual(el, state, slot) {
    el.className = state.className;
    el.innerHTML = state.html;
    el.dataset.slot = String(slot);
    el.dataset.provider = state.provider;
    el.dataset.channel = state.channel;
    syncSlotNumber(el);
  }

  function swapVisual(a, b) {
    const aSlot = a.dataset.slot;
    const bSlot = b.dataset.slot;
    const aState = snapshotVisual(a);
    const bState = snapshotVisual(b);
    restoreVisual(a, bState, aSlot);
    restoreVisual(b, aState, bSlot);
    return function revert() {
      restoreVisual(a, aState, aSlot);
      restoreVisual(b, bState, bSlot);
    };
  }

  function onPresetDrop(fromEl, toEl) {
    const from = parseInt(fromEl.dataset.slot, 10);
    const to = parseInt(toEl.dataset.slot, 10);
    if (!Number.isFinite(from) || !Number.isFinite(to)) return;
    const revert = swapVisual(fromEl, toEl);
    postMove(from, to).then(function (ok) {
      if (!ok) revert();
    });
  }

  if (window.Chassis && window.Chassis.reorder) {
    window.Chassis.reorder.makeSortable({
      container: bank,
      itemSelector: '.preset',
      cloneClass: 'preset-drag-clone',
      onDrop: onPresetDrop,
    });
  }

  // Keyboard reorder: Ctrl+ArrowLeft / Ctrl+ArrowRight swap with neighbor.
  bank.addEventListener('keydown', async (e) => {
    if (!e.ctrlKey) return;
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
    const target = preset(e.target);
    if (!target || target.classList.contains('empty')) return;
    const from = parseInt(target.dataset.slot, 10);
    if (!Number.isFinite(from)) return;
    let to;
    if (e.key === 'ArrowLeft') {
      to = from === 1 ? 12 : from - 1;
    } else {
      to = from === 12 ? 1 : from + 1;
    }
    e.preventDefault();
    const toEl = bank.querySelector(`.preset[data-slot="${to}"]`);
    const revert = toEl ? swapVisual(target, toEl) : null;
    const ok = await postMove(from, to);
    if (!ok && revert) revert();
  });

  function reportChip(chip) {
    if (window.Chassis && window.Chassis.input && typeof window.Chassis.input.showError === 'function') {
      window.Chassis.input.showError(chip || 'CAST FAILED');
    }
  }

  async function postMove(from, to) {
    try {
      const body = new URLSearchParams({ from: String(from), to: String(to) });
      const resp = await fetch('/ui/preset/move', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });
      const json = await resp.json().catch(() => ({ ok: false, chip: 'CAST FAILED' }));
      if (!json.ok) {
        reportChip(json.chip);
        return false;
      }
      return true;
    } catch (_) {
      reportChip('CAST FAILED');
      return false;
    }
  }
})();
