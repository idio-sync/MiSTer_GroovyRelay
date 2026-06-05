(function () {
  'use strict';

  function makeSortable(opts) {
    opts = opts || {};
    const container = opts.container;
    const itemSelector = opts.itemSelector;
    const onDrop = opts.onDrop;
    const cloneClass = opts.cloneClass || 'reorder-drag-clone';
    const threshold = 5;
    let drag = null;

    function item(el) {
      return el && el.closest ? el.closest(itemSelector) : null;
    }

    function clearTarget() {
      if (drag && drag.lastTarget) {
        drag.lastTarget.classList.remove('drop-target');
        drag.lastTarget = null;
      }
    }

    function cancel() {
      if (!drag) return;
      if (drag.clone) {
        if (drag.clone.parentNode && drag.clone.parentNode.removeChild) {
          drag.clone.parentNode.removeChild(drag.clone);
        } else if (drag.clone.remove) {
          drag.clone.remove();
        }
      }
      drag.source.classList.remove('pressed');
      drag.source.removeAttribute('data-dragging');
      clearTarget();
      document.body.style.cursor = '';
      drag = null;
    }

    if (!container || !itemSelector || typeof onDrop !== 'function') {
      return { cancel: cancel };
    }

    container.addEventListener('pointerdown', function (e) {
      if (e.button !== 0 && e.pointerType === 'mouse') return;
      const target = item(e.target);
      if (!target || target.classList.contains('empty')) return;
      drag = {
        source: target,
        clone: null,
        startX: e.clientX,
        startY: e.clientY,
        lastTarget: null,
        pointerId: e.pointerId,
      };
      target.classList.add('pressed');
      if (target.setPointerCapture) target.setPointerCapture(e.pointerId);
      if (e.preventDefault) e.preventDefault();
    });

    container.addEventListener('pointermove', function (e) {
      if (!drag || e.pointerId !== drag.pointerId) return;
      const dx = e.clientX - drag.startX;
      const dy = e.clientY - drag.startY;
      if (!drag.clone) {
        if (Math.hypot(dx, dy) < threshold) return;
        const rect = drag.source.getBoundingClientRect();
        const clone = drag.source.cloneNode(true);
        clone.classList.add(cloneClass);
        clone.style.position = 'fixed';
        clone.style.left = rect.left + 'px';
        clone.style.top = rect.top + 'px';
        clone.style.width = rect.width + 'px';
        clone.style.height = rect.height + 'px';
        clone.style.pointerEvents = 'none';
        clone.style.zIndex = '9999';
        document.body.appendChild(clone);
        drag.clone = clone;
        drag.source.classList.remove('pressed');
        drag.source.setAttribute('data-dragging', 'source');
        document.body.style.cursor = 'grabbing';
      }
      drag.clone.style.transform = 'translate(' + dx + 'px, ' + dy + 'px)';

      const below = document.elementFromPoint(e.clientX, e.clientY);
      const target = item(below);
      if (target === drag.lastTarget) return;
      clearTarget();
      if (target) {
        if (target !== drag.source) target.classList.add('drop-target');
        drag.lastTarget = target;
      }
    });

    container.addEventListener('pointerup', function (e) {
      if (!drag || e.pointerId !== drag.pointerId) return;
      const source = drag.source;
      const target = drag.lastTarget;
      const hadClone = !!drag.clone;
      cancel();
      if (!hadClone || !target || target === source) return;
      onDrop(source, target);
    });

    container.addEventListener('pointercancel', function (e) {
      if (!drag || e.pointerId !== drag.pointerId) return;
      cancel();
    });

    document.addEventListener('keydown', function (e) {
      if (e.key !== 'Escape' || !drag) return;
      if (e.preventDefault) e.preventDefault();
      cancel();
    });

    return { cancel: cancel };
  }

  window.Chassis = window.Chassis || {};
  window.Chassis.reorder = { makeSortable: makeSortable };
})();
