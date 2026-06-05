(function () {
  'use strict';

  window.Chassis = window.Chassis || {};

  const palette = ['amber', 'red', 'teal', 'blue', 'purple', 'green', 'cyan', 'slate'];
  const state = {
    mode: 'new',
    id: '',
    color: 'slate',
    glyphTouched: false,
    groups: [],
    channels: [],
  };

  function byID(id) {
    return document.getElementById(id);
  }

  function cloneList(items) {
    return Array.isArray(items) ? items.map((item) => ({ ...item })) : [];
  }

  function isYouTubeHost(hostname) {
    const host = String(hostname || '').toLowerCase().replace(/^www\./, '');
    return host === 'youtube.com' || host === 'm.youtube.com' || host === 'music.youtube.com';
  }

  function detectKind(rawURL) {
    let parsed;
    try {
      parsed = new URL(String(rawURL || '').trim());
    } catch (_) {
      return 'single';
    }
    if (!parsed.host) {
      return 'single';
    }
    const path = parsed.pathname.toLowerCase();
    if (path.endsWith('.m3u8') || path.endsWith('.m3u') || path.endsWith('.mpd')) {
      return 'direct';
    }
    if (isYouTubeHost(parsed.hostname) && parsed.searchParams.get('list')) {
      return 'playlist';
    }
    return 'single';
  }

  function suggestGlyph(name) {
    const words = String(name || '').toUpperCase().match(/[A-Z0-9]+/g) || [];
    if (words.length === 0) return '';
    if (words.length === 1) return words[0].slice(0, 2);
    if (/[0-9]/.test(words[0])) return words[0].slice(0, 4);
    return words.map((word) => word[0]).join('').slice(0, 4);
  }

  function hostHint(rawURL) {
    let parsed;
    try {
      parsed = new URL(String(rawURL || ''));
    } catch (_) {
      return { ok: false, message: 'Use an http or https URL.' };
    }
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return { ok: false, message: 'Use an http or https URL.' };
    }
    const host = parsed.hostname.toLowerCase().replace(/^\[|\]$/g, '');
    if (
      host === 'localhost' ||
      host === '::1' ||
      host.startsWith('fe80:') ||
      host.startsWith('127.') ||
      host.startsWith('169.254.') ||
      host === '0.0.0.0'
    ) {
      return { ok: false, message: 'Local-only hosts are blocked here.' };
    }
    return { ok: true, message: '' };
  }

  async function postJSON(method, url, body) {
    const init = {
      method,
      credentials: 'same-origin',
      headers: {},
    };
    if (body !== undefined) {
      init.headers['Content-Type'] = 'application/json';
      init.body = JSON.stringify(body);
    }
    return fetch(url, init);
  }

  function showNotice(text, variant) {
    const settings = window.Chassis && window.Chassis.settings;
    if (settings && typeof settings.showNotice === 'function') {
      settings.showNotice(text, variant);
    }
  }

  function selectColor(token) {
    state.color = palette.includes(token) ? token : 'slate';
    const swatches = byID('cf-swatches');
    if (!swatches) return;
    swatches.querySelectorAll('.cf-swatch[data-color]').forEach((button) => {
      const selected = button.dataset.color === state.color;
      button.classList.toggle('selected', selected);
      button.setAttribute('aria-checked', selected ? 'true' : 'false');
    });
  }

  function renderGroups() {}

  function renderChannels() {}

  function openForm() {
    document.body.classList.add('catalog-form-open');
    const form = byID('catalog-form');
    if (form) form.setAttribute('aria-hidden', 'false');
  }

  function closeForm() {
    document.body.classList.remove('catalog-form-open');
    const form = byID('catalog-form');
    if (form) form.setAttribute('aria-hidden', 'true');
  }

  function setValue(id, value) {
    const el = byID(id);
    if (el) el.value = value == null ? '' : String(value);
  }

  function newProvider() {
    state.mode = 'new';
    state.id = '';
    state.glyphTouched = false;
    state.groups = [];
    state.channels = [];
    setValue('cf-provider-id', '');
    setValue('cf-name', '');
    setValue('cf-glyph', '');
    selectColor('slate');
    const del = byID('cf-delete');
    if (del) del.hidden = true;
    renderGroups();
    renderChannels();
    openForm();
  }

  function populate(form) {
    const data = form || {};
    state.mode = data.id ? 'edit' : 'new';
    state.id = data.id || '';
    state.glyphTouched = true;
    state.groups = cloneList(data.groups);
    state.channels = cloneList(data.channels);
    setValue('cf-provider-id', state.id);
    setValue('cf-name', data.displayName || '');
    setValue('cf-glyph', data.badgeLabel || '');
    selectColor(data.badgeColor || 'slate');
    const del = byID('cf-delete');
    if (del) del.hidden = !state.id;
    renderGroups();
    renderChannels();
    openForm();
  }

  async function editProvider(id) {
    if (!id) return;
    try {
      const resp = await postJSON('GET', '/ui/catalog/provider/' + encodeURIComponent(id));
      if (!resp.ok) {
        showNotice('Unable to load provider.', 'err');
        return;
      }
      const body = await resp.json();
      populate(body);
    } catch (_) {
      showNotice('Unable to load provider.', 'err');
    }
  }

  function wireEvents() {
    const drawer = byID('catalog-drawer');
    if (drawer) {
      drawer.addEventListener('click', (event) => {
        const edit = event.target.closest && event.target.closest('[data-edit-provider]');
        if (edit) {
          event.preventDefault();
          editProvider(edit.dataset.editProvider);
          return;
        }
        const next = event.target.closest && event.target.closest('#catalog-provider-new');
        if (next) {
          event.preventDefault();
          newProvider();
        }
      });
    }
    const cancel = byID('cf-cancel');
    if (cancel) {
      cancel.addEventListener('click', closeForm);
    }
    const swatches = byID('cf-swatches');
    if (swatches) {
      swatches.addEventListener('click', (event) => {
        const swatch = event.target.closest && event.target.closest('.cf-swatch[data-color]');
        if (!swatch) return;
        event.preventDefault();
        selectColor(swatch.dataset.color);
      });
    }
    const name = byID('cf-name');
    if (name) {
      name.addEventListener('input', () => {
        if (state.glyphTouched) return;
        const glyph = byID('cf-glyph');
        if (glyph) glyph.value = suggestGlyph(name.value);
      });
    }
    const glyph = byID('cf-glyph');
    if (glyph) {
      glyph.addEventListener('input', () => {
        state.glyphTouched = true;
      });
    }
  }

  window.Chassis.providerForm = {
    detectKind,
    suggestGlyph,
    hostHint,
    selectColor,
    openForm,
    closeForm,
    newProvider,
    editProvider,
    populate,
    _state: state,
    _palette: palette,
  };

  wireEvents();
}());
