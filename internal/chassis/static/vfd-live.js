// Receiver chassis VFD live wire. Phase 1 / Spec 2.
//
// Subscribes to /receiver/events (SSE), routes named events:
//   state -> window.Chassis.State.set(idle|live)
//   vfd   -> textContent updates on data-vfd-{title,marquee,queue,uptime}
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

  function handleVfdEvent(ev) {
    try {
      const data = JSON.parse(ev.data);
      const title = document.querySelector('[data-vfd-title]');
      const marquee = document.querySelector('[data-vfd-marquee]');
      const queue = document.querySelector('[data-vfd-queue]');
      const uptime = document.querySelector('[data-vfd-uptime]');
      if (title) title.textContent = data.title || '';
      if (marquee) marquee.textContent = data.marquee || '';
      if (queue) queue.textContent = `${data.queueCurrent} / ${data.queueTotal}`;
      if (uptime) uptime.textContent = data.uptime || '';
    } catch (err) {
      console.warn('vfd-live: bad vfd payload', ev.data, err);
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
    source = new EventSource('/receiver/events');
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

  document.addEventListener('DOMContentLoaded', connect);
})();
