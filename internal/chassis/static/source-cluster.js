(function () {
  'use strict';
  if (!window.Chassis || !window.Chassis.events || typeof window.Chassis.events.subscribe !== 'function') {
    console.warn('source-cluster: chassis events bus missing');
    return;
  }

  const KNOWN_SOURCES = ['streams', 'plex', 'jellyfin', 'dlna'];

  function parseAdapterRefSource(ref) {
    if (!ref || typeof ref !== 'string') return '';
    const colon = ref.indexOf(':');
    if (colon <= 0) return '';
    const id = ref.slice(0, colon);
    return KNOWN_SOURCES.indexOf(id) >= 0 ? id : '';
  }

  function applyCasting(activeSourceID) {
    document.querySelectorAll('.source-cluster .lamp').forEach((el) => {
      const id = el.getAttribute('data-source-id') || '';
      el.classList.toggle('casting', id !== '' && id === activeSourceID);
    });
  }

  function onTransport(ev) {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    applyCasting(parseAdapterRefSource(data.adapterRef));
  }

  window.Chassis.events.subscribe('transport', onTransport);
})();
