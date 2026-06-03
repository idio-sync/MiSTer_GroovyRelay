// Receiver chassis VFD live wire. Phase 1 / Spec 2.
//
// Subscribes to /ui/events (SSE), routes named events:
//   state -> window.Chassis.State.set(idle|live)
//   vfd   -> textContent updates on data-vfd-{primary,secondary,tertiary,uptime}
//            plus queue-memory slots
//            (overflowing tier rows marquee-scroll; empty tiers collapse)
//
// Loaded after chassis.js (Phase 0) so window.Chassis is populated.
// Each later spec ships its own JS file that hangs off window.Chassis
// the same way.
(() => {
  'use strict';

  if (!window.Chassis) {
    console.warn('vfd-live: window.Chassis missing; chassis.js failed to load?');
    return;
  }

  let source = null;
  const subscriptions = new Map();

  function subscribe(eventName, handler) {
    if (!subscriptions.has(eventName)) {
      subscriptions.set(eventName, new Set());
    }
    const handlers = subscriptions.get(eventName);
    handlers.add(handler);
    if (source) {
      source.removeEventListener(eventName, handler);
      source.addEventListener(eventName, handler);
    }
    return function unsubscribe() {
      handlers.delete(handler);
      if (source) {
        source.removeEventListener(eventName, handler);
      }
    };
  }

  function attachSubscriptions(nextSource) {
    subscriptions.forEach((handlers, eventName) => {
      handlers.forEach((handler) => {
        nextSource.removeEventListener(eventName, handler);
        nextSource.addEventListener(eventName, handler);
      });
    });
  }

  function handleStateEvent(ev) {
    try {
      const { state } = JSON.parse(ev.data);
      if (state === 'idle' || state === 'live') {
        window.Chassis.State.set(state);
        // The .vfd-state element's --idle/--live modifier is only set at
        // server-render time. chassis.css hides the modifier that does not
        // match the body class (body:not(.idle) .vfd-state--idle, and
        // body.idle .vfd-state--live both display:none). Since State.set
        // only flips the body class, the modifier must be re-synced here or
        // the whole VFD block is hidden on the idle->live transition.
        const vfdState = document.querySelector('.vfd-state');
        if (vfdState) {
          vfdState.classList.toggle('vfd-state--live', state === 'live');
          vfdState.classList.toggle('vfd-state--idle', state === 'idle');
        }
      }
    } catch (err) {
      console.warn('vfd-live: bad state payload', ev.data, err);
    }
  }

  function applyTier(attr, text) {
    const el = document.querySelector(`[${attr}]`);
    if (!el) return;
    el.textContent = text || '';
    const row = el.closest('.vfd-row');
    if (row) row.classList.toggle('is-empty', !text);
    measureScroll(row, el);
  }

  // measureScroll toggles marquee animation when a tier's text overflows
  // its column. Distance + duration are set as CSS custom properties so
  // the @keyframes can translate by exactly the overflow (constant ~40px/s
  // so long titles aren't dizzyingly fast).
  function measureScroll(row, el) {
    if (!row || !el) return;
    row.classList.remove('is-scrolling');
    row.style.removeProperty('--vfd-scroll-dist');
    row.style.removeProperty('--vfd-scroll-dur');
    const overflow = el.scrollWidth - row.clientWidth;
    if (overflow > 4) {
      const dist = overflow + 24; // trailing gap before the loop restarts
      const dur = Math.max(6, dist / 40);
      row.style.setProperty('--vfd-scroll-dist', dist + 'px');
      row.style.setProperty('--vfd-scroll-dur', dur + 's');
      row.classList.add('is-scrolling');
    }
  }

  function remeasureAllTiers() {
    ['[data-vfd-primary]', '[data-vfd-secondary]', '[data-vfd-tertiary]'].forEach((sel) => {
      const el = document.querySelector(sel);
      if (el) measureScroll(el.closest('.vfd-row'), el);
    });
  }

  const densityClasses = [
    'vfd-density--dense',
    'vfd-density--sparse-two',
    'vfd-density--sparse-one',
    'vfd-density--empty',
  ];

  function densityClass(primary, secondary, tertiary) {
    if (primary && secondary && tertiary) return 'vfd-density--dense';
    if (primary && secondary) return 'vfd-density--sparse-two';
    if (primary) return 'vfd-density--sparse-one';
    return 'vfd-density--empty';
  }

  function applyDensity(primary, secondary, tertiary) {
    const root = document.querySelector('.vfd');
    if (!root) return;
    root.classList.remove(...densityClasses);
    root.classList.add(densityClass(primary, secondary, tertiary));
  }

  function paddedQueueNumber(value) {
    return String(value).padStart(2, '0');
  }

  function normalizedQueueCurrent(current, total) {
    const parsedTotal = Number(total) || 0;
    if (parsedTotal <= 0) return 0;
    const parsedCurrent = Number(current) || 1;
    return Math.min(Math.max(parsedCurrent, 1), parsedTotal);
  }

  function queueWindow(current, total) {
    const parsedTotal = Number(total) || 0;
    if (parsedTotal <= 0) return [];
    const active = normalizedQueueCurrent(current, parsedTotal);
    if (parsedTotal <= 12) {
      return Array.from({ length: parsedTotal }, (_, idx) => idx + 1);
    }
    const windowSize = 5;
    let start = active - 2;
    let end = active + 2;
    if (start < 1) {
      end += 1 - start;
      start = 1;
    }
    if (end > parsedTotal) {
      start -= end - parsedTotal;
      end = parsedTotal;
    }
    start = Math.max(start, 1);
    return Array.from({ length: Math.min(windowSize, end - start + 1) }, (_, idx) => start + idx);
  }

  function makeQueueSlot(text, state) {
    const slot = document.createElement('span');
    slot.classList.add('queue-slot');
    if (state) slot.classList.add(state);
    slot.textContent = text;
    return slot;
  }

  function renderQueue(current, total) {
    const slots = document.querySelector('[data-vfd-queue-slots]');
    const totalLabel = document.querySelector('[data-vfd-queue-total-label]');
    if (!slots) return;
    slots.replaceChildren();
    const parsedTotal = Number(total) || 0;
    const active = normalizedQueueCurrent(current, parsedTotal);
    if (parsedTotal <= 0) {
      for (let idx = 0; idx < 8; idx += 1) {
        slots.appendChild(makeQueueSlot('', 'dormant'));
      }
      if (totalLabel) totalLabel.textContent = '';
      return;
    }
    queueWindow(active, parsedTotal).forEach((item) => {
      let state = 'future';
      if (item < active) state = 'past';
      if (item === active) state = 'current';
      slots.appendChild(makeQueueSlot(paddedQueueNumber(item), state));
    });
    if (totalLabel) {
      totalLabel.textContent = parsedTotal > 1 ? `TOTAL ${paddedQueueNumber(parsedTotal)}` : '';
    }
  }

  // Retune flicker: when the primary title genuinely changes (a new cast or a
  // source switch — not a telemetry tick), pulse the VFD glass so the swap
  // reads like a tuner re-locking. Suppressed while the power-on/off rituals
  // own the glass, and never on the first render (lastPrimary seeds from the
  // server-rendered title).
  let lastPrimary = null;
  let retuneTimer = null;

  function triggerRetune() {
    const vfd = document.querySelector('.vfd');
    if (!vfd) return;
    const body = document.body;
    if (body && (body.classList.contains('warming') || body.classList.contains('cooling'))) {
      return;
    }
    vfd.classList.remove('retune');
    void vfd.offsetWidth; // restart the keyframe on back-to-back changes
    vfd.classList.add('retune');
    if (retuneTimer) clearTimeout(retuneTimer);
    retuneTimer = setTimeout(() => {
      vfd.classList.remove('retune');
      retuneTimer = null;
    }, 400);
  }

  function notePrimary(primary) {
    const next = primary || '';
    if (lastPrimary !== null && next && next !== lastPrimary) {
      triggerRetune();
    }
    lastPrimary = next;
  }

  function handleVfdEvent(ev) {
    try {
      const data = JSON.parse(ev.data);
      notePrimary(data.primary);
      applyTier('data-vfd-primary', data.primary);
      applyTier('data-vfd-secondary', data.secondary);
      applyTier('data-vfd-tertiary', data.tertiary);
      const uptime = document.querySelector('[data-vfd-uptime]');
      applyDensity(data.primary, data.secondary, data.tertiary);
      renderQueue(data.queueCurrent, data.queueTotal);
      if (uptime) uptime.textContent = data.uptime || '';
      // Re-measure once fonts are final (DSEG14 metrics differ from the
      // fallback monospace; a pre-font measurement mis-sizes the marquee).
      if (document.fonts && document.fonts.ready) {
        document.fonts.ready.then(remeasureAllTiers);
      }
    } catch (err) {
      console.warn('vfd-live: bad vfd payload', ev.data, err);
    }
  }

  function initialVfdRender() {
    const primary = document.querySelector('[data-vfd-primary]');
    const secondary = document.querySelector('[data-vfd-secondary]');
    const tertiary = document.querySelector('[data-vfd-tertiary]');
    lastPrimary = primary ? primary.textContent : '';
    applyDensity(
      primary ? primary.textContent : '',
      secondary ? secondary.textContent : '',
      tertiary ? tertiary.textContent : '',
    );
    const queueRail = document.querySelector('[data-vfd-queue-current]');
    if (queueRail) {
      renderQueue(queueRail.getAttribute('data-vfd-queue-current'), queueRail.getAttribute('data-vfd-queue-total'));
    }
  }

  function handleSourceEvent(ev) {
    try {
      const data = JSON.parse(ev.data);
      (data.buttons || []).forEach((button) => {
        if (!button.action) {
          return;
        }
        const btn = document.querySelector(`[data-source-action="${button.action}"]`);
        if (!btn) {
          return;
        }
        btn.classList.toggle('active', !!button.active);
        btn.classList.toggle('lit', !!button.lit);
        btn.setAttribute('aria-checked', button.active ? 'true' : 'false');
        btn.setAttribute('aria-disabled', button.unavailable ? 'true' : 'false');
        if (button.unavailable) {
          btn.setAttribute('disabled', '');
        } else {
          btn.removeAttribute('disabled');
        }
        btn.setAttribute('data-input-id', button.inputId || '');
        const label = `${button.label || btn.textContent || ''}${button.active ? ' selected' : ''}${button.lit ? ' casting' : ''}`;
        btn.setAttribute('aria-label', label);
        btn.setAttribute('title', label);
      });
    } catch (err) {
      console.warn('vfd-live: bad source payload', ev.data, err);
    }
  }

  function connect() {
    source = new EventSource('/ui/events');
    source.addEventListener('state', handleStateEvent);
    source.addEventListener('vfd', handleVfdEvent);
    source.addEventListener('source', handleSourceEvent);
    source.addEventListener('error', () => {
      console.info('vfd-live: stream interrupted; browser will retry using the SSE retry directive');
    });
    attachSubscriptions(source);

    if (!window.Chassis.events) {
      window.Chassis.events = {};
    }
    window.Chassis.events.source = source;
    document.dispatchEvent(new CustomEvent('chassis:eventsource', {
      detail: { source },
    }));
  }

  // Expose for the ?dev=1 toggle and integration debugging.
  window.Chassis.events = window.Chassis.events || {};
  Object.assign(window.Chassis.events, {
    subscribe,
    reconnect() {
      if (source) source.close();
      connect();
    },
  });

  // Register the resize re-measure exactly once at load. connect() can run
  // multiple times (reconnect() re-invokes it), so binding inside connect()
  // would stack a new handler per SSE reconnect in a long-running kiosk.
  window.addEventListener('resize', remeasureAllTiers);

  document.addEventListener('DOMContentLoaded', () => {
    initialVfdRender();
    connect();
  });
})();
