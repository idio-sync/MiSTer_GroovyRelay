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

  function lampLabel(el) {
    return el.dataset.label || (el.getAttribute('data-source-id') || '').toUpperCase();
  }

  function syncLampText(el) {
    const label = lampLabel(el);
    const configured = el.classList.contains('configured-idle');
    const casting = el.classList.contains('casting');
    let status = 'not configured';
    let titleStatus = 'not configured';
    if (casting) {
      status = 'currently casting';
      titleStatus = 'currently casting';
    } else if (configured) {
      status = 'ready';
      titleStatus = 'linked, idle';
    }
    el.setAttribute('aria-label', `${label}, ${status}`);
    el.setAttribute('title', `${label} - ${titleStatus}`);
  }

  function setLampState(el, configured, casting, label) {
    if (label) el.dataset.label = label;
    el.classList.toggle('configured-idle', configured);
    el.classList.toggle('unavailable', !configured);
    el.classList.toggle('casting', casting);
    syncLampText(el);
  }

  function applyCasting(activeSourceID) {
    document.querySelectorAll('.source-cluster .lamp').forEach((el) => {
      const id = el.getAttribute('data-source-id') || '';
      el.classList.toggle('casting', id !== '' && id === activeSourceID);
      syncLampText(el);
    });
  }

  function applySource(payload) {
    if (!payload || !Array.isArray(payload.buttons)) return;
    const bySource = new Map();
    payload.buttons.forEach((button) => {
      const label = button.label || '';
      const id = label.toLowerCase();
      if (KNOWN_SOURCES.indexOf(id) >= 0) bySource.set(id, button);
    });
    document.querySelectorAll('.source-cluster .lamp').forEach((el) => {
      const id = el.getAttribute('data-source-id') || '';
      const button = bySource.get(id);
      if (!button) return;
      setLampState(el, !!button.configured, !!button.casting, button.label || id.toUpperCase());
    });
  }

  function onTransport(ev) {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    applyCasting(parseAdapterRefSource(data.adapterRef));
  }

  function onSource(ev) {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    applySource(data);
  }

  window.Chassis.events.subscribe('source', onSource);
  window.Chassis.events.subscribe('transport', onTransport);
})();
