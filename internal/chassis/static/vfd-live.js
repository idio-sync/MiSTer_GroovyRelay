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

  function handleStateEvent(ev) {
    try {
      const { state } = JSON.parse(ev.data);
      if (state === 'idle' || state === 'live') {
        window.Chassis.State.set(state);
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

  function connect() {
    source = new EventSource('/receiver/events');
    source.addEventListener('state', handleStateEvent);
    source.addEventListener('vfd', handleVfdEvent);
    source.addEventListener('error', () => {
      console.info('vfd-live: stream interrupted; browser will retry using the SSE retry directive');
    });
  }

  // Expose for the ?dev=1 toggle and integration debugging.
  window.Chassis.events = {
    reconnect() {
      if (source) source.close();
      connect();
    },
  };

  document.addEventListener('DOMContentLoaded', connect);
})();
