(function () {
  'use strict';
  const bank = document.querySelector('.preset-bank');
  if (!bank) return;

  let dragging = null;     // { from, sourceEl, clone, startX, startY, lastTarget }
  const DRAG_THRESHOLD = 5; // px before pointermove transitions to drag

  function preset(el) {
    return el && el.classList && el.classList.contains('preset') ? el : (el ? el.closest('.preset') : null);
  }

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

  function cancelDrag() {
    if (!dragging) return;
    if (dragging.clone) dragging.clone.remove();
    if (dragging.sourceEl) {
      dragging.sourceEl.classList.remove('pressed');
      dragging.sourceEl.removeAttribute('data-dragging');
    }
    if (dragging.lastTarget) dragging.lastTarget.classList.remove('drop-target');
    document.body.style.cursor = '';
    dragging = null;
  }

  bank.addEventListener('pointerdown', (e) => {
    if (e.button !== 0 && e.pointerType === 'mouse') return;
    const target = preset(e.target);
    if (!target || target.classList.contains('empty')) return;
    dragging = {
      from: parseInt(target.dataset.slot, 10),
      sourceEl: target,
      clone: null,
      startX: e.clientX,
      startY: e.clientY,
      lastTarget: null,
      pointerId: e.pointerId,
    };
    target.classList.add('pressed');
    target.setPointerCapture(e.pointerId);
    e.preventDefault();
  });

  bank.addEventListener('pointermove', (e) => {
    if (!dragging || e.pointerId !== dragging.pointerId) return;
    const dx = e.clientX - dragging.startX;
    const dy = e.clientY - dragging.startY;
    if (!dragging.clone) {
      if (Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
      // Begin drag.
      const rect = dragging.sourceEl.getBoundingClientRect();
      const clone = dragging.sourceEl.cloneNode(true);
      clone.classList.add('preset-drag-clone');
      clone.style.position = 'fixed';
      clone.style.left = rect.left + 'px';
      clone.style.top = rect.top + 'px';
      clone.style.width = rect.width + 'px';
      clone.style.height = rect.height + 'px';
      clone.style.pointerEvents = 'none';
      clone.style.zIndex = '9999';
      document.body.appendChild(clone);
      dragging.clone = clone;
      dragging.sourceEl.classList.remove('pressed');
      dragging.sourceEl.setAttribute('data-dragging', 'source');
      document.body.style.cursor = 'grabbing';
    }
    dragging.clone.style.transform = `translate(${dx}px, ${dy}px)`;
    const below = document.elementFromPoint(e.clientX, e.clientY);
    const target = preset(below);
    if (target && target !== dragging.lastTarget) {
      if (dragging.lastTarget) dragging.lastTarget.classList.remove('drop-target');
      if (target !== dragging.sourceEl) target.classList.add('drop-target');
      dragging.lastTarget = target;
    }
  });

  bank.addEventListener('pointerup', async (e) => {
    if (!dragging || e.pointerId !== dragging.pointerId) return;
    if (!dragging.clone) { cancelDrag(); return; }
    const source = dragging.sourceEl;
    const target = dragging.lastTarget;
    if (!target || target === source) { cancelDrag(); return; }
    const to = parseInt(target.dataset.slot, 10);
    const from = dragging.from;
    cancelDrag();
    if (!Number.isFinite(from) || !Number.isFinite(to)) return;
    const revert = swapVisual(source, target);
    const ok = await postMove(from, to);
    if (!ok) revert();
  });

  bank.addEventListener('pointercancel', cancelDrag);

  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && dragging) {
      e.preventDefault();
      cancelDrag();
    }
  });

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
      const resp = await fetch('/receiver/preset/move', {
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
