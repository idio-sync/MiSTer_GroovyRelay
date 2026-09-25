(function () {
  'use strict';
  if (!window.Chassis || !window.Chassis.events || typeof window.Chassis.events.subscribe !== 'function') {
    console.warn('source-cluster: chassis events bus missing');
    return;
  }

  const KNOWN_SOURCES = ['streams', 'plex', 'jellyfin', 'dlna', 'spotify', 'url', 'local', 'localfiles', 'aux'];

  function normalizeSourceID(source) {
    if (!source || typeof source !== 'string') return '';
    const id = source.toLowerCase();
    return KNOWN_SOURCES.indexOf(id) >= 0 ? id : '';
  }

  function parseAdapterRefSource(ref) {
    if (!ref || typeof ref !== 'string') return '';
    const colon = ref.indexOf(':');
    if (colon <= 0) return '';
    const id = ref.slice(0, colon);
    return normalizeSourceID(id);
  }

  function lampLabel(el) {
    return el.dataset.label || (el.getAttribute('data-source-id') || '').toUpperCase();
  }

  function syncLampText(el) {
    const label = lampLabel(el);
    const configured = el.classList.contains('configured-idle');
    const casting = el.classList.contains('casting');
    const issue = el.classList.contains('issue');
    const lastError = el.dataset.lastError || '';
    const stateEl = el.querySelector('.state');
    const ledWell = el.querySelector('.led-well');
    let status = 'not configured';
    let titleStatus = 'not configured';
    let shortStatus = 'OFF';
    if (issue) {
      status = lastError ? `issue: ${lastError}` : 'issue';
      titleStatus = status;
      shortStatus = 'ISSUE';
    } else if (casting) {
      status = 'currently casting';
      titleStatus = 'currently casting';
      shortStatus = 'LIVE';
    } else if (configured) {
      status = 'ready';
      titleStatus = 'linked, idle';
      shortStatus = 'READY';
    }
    if (stateEl) stateEl.textContent = shortStatus;
    if (ledWell) ledWell.dataset.status = shortStatus.toLowerCase();
    el.dataset.sourceStatus = shortStatus.toLowerCase();
    el.setAttribute('aria-label', `${label}, ${status}`);
    el.setAttribute('title', `${label} - ${titleStatus}`);
  }

  function setLampState(el, configured, casting, issue, lastError, label) {
    if (label) el.dataset.label = label;
    el.classList.toggle('configured-idle', configured);
    el.classList.toggle('unavailable', !configured && !issue);
    el.classList.toggle('casting', casting);
    el.classList.toggle('issue', issue);
    if (lastError) {
      el.dataset.lastError = lastError;
    } else {
      delete el.dataset.lastError;
    }
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
      setLampState(
        el,
        !!button.configured,
        !!button.casting,
        !!button.issue,
        button.lastError || '',
        button.label || id.toUpperCase()
      );
    });
  }

  function onTransport(ev) {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    applyCasting(normalizeSourceID(data.source) || parseAdapterRefSource(data.adapterRef));
  }

  function onSource(ev) {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    applySource(data);
  }

  // --- Input keys ---------------------------------------------------
  // Sources push casts to the bridge, so there is no input to switch to.
  // Pressing a lamp key does what a receiver does when you select an
  // input: the VFD shows that input's name and status (READY with how to
  // use it, ON AIR, NOT CONFIGURED, or the ISSUE detail that otherwise
  // lives only in a tooltip). A second press while that is up opens the
  // adapter's settings. A configured AUX press is left to chassis.js,
  // which starts capture.
  const INFO_MS = 4000; // matches vfd-live.js FLASH_MS
  const HINTS = {
    streams: 'PICK A PRESET OR BROWSE',
    plex: 'CAST FROM THE PLEX APP',
    jellyfin: 'CAST FROM JELLYFIN',
    dlna: 'CAST FROM A DLNA APP',
    spotify: 'CAST FROM THE SPOTIFY APP',
    aux: 'PRESS TO START CAPTURE',
  };
  const SETUP_HINT = 'PRESS AGAIN FOR SETUP';
  let armedID = '';
  let armTimer = null;

  function disarm() {
    armedID = '';
    if (armTimer) {
      clearTimeout(armTimer);
      armTimer = null;
    }
  }

  function inputInfo(el) {
    const id = el.getAttribute('data-source-id') || '';
    const label = lampLabel(el);
    if (el.classList.contains('issue')) {
      const detail = (el.dataset.lastError || 'CHECK SETUP').toUpperCase();
      return { lines: [label, `ISSUE: ${detail}`, SETUP_HINT], setup: true };
    }
    if (el.classList.contains('casting')) {
      return { lines: [label, 'ON AIR', ''], setup: false };
    }
    if (el.classList.contains('configured-idle')) {
      return { lines: [label, `READY · ${HINTS[id] || 'CAST FROM ITS APP'}`, SETUP_HINT], setup: true };
    }
    return { lines: [label, 'NOT CONFIGURED', SETUP_HINT], setup: true };
  }

  function openSetup(id) {
    document.body.classList.add('settings-open');
    const tab = document.querySelector('.settings-tab[data-tab="adapters"]');
    if (tab && typeof tab.click === 'function') tab.click();
    const section = document.querySelector(`[data-adapter-section="${id}"]`);
    const field = section ? null : document.querySelector(`.settings-pane[data-pane="adapters"] [data-adapter="${id}"]`);
    const target = section || (field && (field.closest('.settings-section') || field));
    if (target && typeof target.scrollIntoView === 'function') {
      target.scrollIntoView({ block: 'start' });
    }
  }

  function onKey(el) {
    const id = el.getAttribute('data-source-id') || '';
    const isAUX = el.getAttribute('data-source-action') === 'aux-start';
    if (isAUX && el.classList.contains('configured-idle') && !el.classList.contains('casting')) {
      return; // chassis.js starts capture
    }
    if (armedID && armedID === id) {
      disarm();
      openSetup(id);
      return;
    }
    const info = inputInfo(el);
    const vfd = window.Chassis.vfd;
    if (vfd && typeof vfd.flash === 'function') vfd.flash(...info.lines);
    disarm();
    if (info.setup) {
      armedID = id;
      armTimer = setTimeout(disarm, INFO_MS);
    }
  }

  document.querySelectorAll('.source-cluster .lamp').forEach((el) => {
    if (typeof el.addEventListener === 'function') {
      el.addEventListener('click', () => onKey(el));
    }
  });

  window.Chassis.events.subscribe('source', onSource);
  window.Chassis.events.subscribe('transport', onTransport);
})();
