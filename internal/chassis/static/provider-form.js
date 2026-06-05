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
  const starredPresetKeys = new Set();
  let mutationSeq = 0;
  let pendingMutation = 0;

  function byID(id) {
    return document.getElementById(id);
  }

  function mk(tag, className) {
    const el = document.createElement(tag);
    if (className) {
      el.className = className;
      if (el.classList) {
        className.split(/\s+/).filter(Boolean).forEach((name) => el.classList.add(name));
      }
    }
    return el;
  }

  function kids(host) {
    return host && host.children ? Array.prototype.slice.call(host.children) : [];
  }

  function cloneList(items) {
    return Array.isArray(items) ? items.map((item) => ({ ...item })) : [];
  }

  function orderValue(value, fallback) {
    const next = Number(value);
    return Number.isFinite(next) ? next : fallback;
  }

  function normalizeGroup(group, index) {
    const src = group || {};
    return {
      id: src.id == null ? '' : String(src.id),
      name: src.name == null ? '' : String(src.name),
      order: orderValue(src.order, index),
    };
  }

  function normalizeGroups(groups) {
    return Array.isArray(groups) ? groups.map(normalizeGroup) : [];
  }

  function normalizeKind(kind) {
    const value = String(kind || '');
    return value === 'single' || value === 'direct' || value === 'playlist' ? value : '';
  }

  function normalizePlayMode(mode) {
    const value = String(mode || '');
    return value === 'sequential' || value === 'shuffle' || value === 'first_then_shuffle' ? value : '';
  }

  function normalizeChannel(channel, index) {
    const src = channel || {};
    return {
      id: src.id == null ? '' : String(src.id),
      name: src.name == null ? '' : String(src.name),
      url: src.url == null ? '' : String(src.url),
      kind: normalizeKind(src.kind),
      playMode: normalizePlayMode(src.playMode),
      groupId: src.groupId == null ? '' : String(src.groupId),
      order: orderValue(src.order, index),
    };
  }

  function normalizeChannels(channels) {
    return Array.isArray(channels) ? channels.map(normalizeChannel) : [];
  }

  function currentGroups() {
    return state.groups || [];
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

  function setMutationControlsDisabled(disabled) {
    ['cf-save', 'cf-delete'].forEach((id) => {
      const el = byID(id);
      if (el) el.disabled = disabled;
    });
  }

  function invalidateMutation() {
    mutationSeq += 1;
    pendingMutation = 0;
    setMutationControlsDisabled(false);
  }

  function beginMutation() {
    if (pendingMutation) return 0;
    pendingMutation = mutationSeq + 1;
    mutationSeq = pendingMutation;
    setMutationControlsDisabled(true);
    return pendingMutation;
  }

  function isCurrentMutation(token) {
    return token !== 0 && pendingMutation === token;
  }

  function finishMutation(token) {
    if (pendingMutation !== token) return;
    pendingMutation = 0;
    setMutationControlsDisabled(false);
  }

  function noticeText(result, fallback) {
    return (result && (result.message || result.chip || result.error)) || fallback;
  }

  function clearedSlotsCount(result) {
    const slots = result && result.clearedSlots;
    if (Array.isArray(slots)) return slots.length;
    const n = Number(slots);
    return Number.isFinite(n) && n > 0 ? n : 0;
  }

  function showClearedSlots(result) {
    const count = clearedSlotsCount(result);
    if (count <= 0) return;
    showNotice(count + ' preset slot' + (count === 1 ? '' : 's') + ' cleared.', 'warn');
  }

  function showAutoEnableNotice(result) {
    const status = result && result.autoEnabledStreams;
    if (status === 'on') {
      showNotice('Streams source turned on.', 'ok');
      return;
    }
    if (status === 'restart-required') {
      showNotice('Restart the bridge to activate the Streams source.', 'warn');
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

  function addOption(select, value, label) {
    const option = mk('option');
    option.value = value;
    option.textContent = label;
    select.appendChild(option);
    return option;
  }

  function fillGroupSelect(select, groups, selected) {
    if (!select) return;
    const groupList = normalizeGroups(groups);
    select.replaceChildren();
    addOption(select, '', 'No group');
    groupList.forEach((item) => addOption(select, item.id, item.name || item.id || 'Group'));
    select.value = groupList.some((item) => item.id === selected) ? selected : '';
  }

  function stopDragStart(event) {
    if (event && typeof event.stopPropagation === 'function') event.stopPropagation();
  }

  function keepDragOffControls(nodes) {
    nodes.forEach((node) => {
      if (node && typeof node.addEventListener === 'function') {
        node.addEventListener('pointerdown', stopDragStart);
      }
    });
  }

  function nextID(prefix, items) {
    const list = items || [];
    const used = new Set(list.map((item) => item.id));
    let n = list.length + 1;
    let id = prefix + '-' + n;
    while (used.has(id)) {
      n += 1;
      id = prefix + '-' + n;
    }
    return id;
  }

  function resolvedRowKind(row) {
    const override = row._override ? row._override.value : 'auto';
    if (override === 'auto') {
      return detectKind(row._url ? row._url.value : '');
    }
    return normalizeKind(override) || detectKind(row._url ? row._url.value : '');
  }

  function refreshChannelRow(row) {
    const rawURL = row._url ? row._url.value : '';
    const url = String(rawURL || '').trim();
    const detected = detectKind(rawURL);
    const resolved = resolvedRowKind(row);
    const hint = hostHint(url);

    row.dataset.kind = resolved;
    row._kindChip.textContent = resolved.toUpperCase();
    row._playModeWrap.hidden = resolved !== 'playlist';
    row._hint.textContent = 'Detected: ' + detected + '; ' + (url ? (hint.ok ? 'Host allowed' : hint.message) : 'Host unchecked');
    row._hint.classList.toggle('err', url !== '' && !hint.ok);
  }

  function setVerifyChip(row, variant, text) {
    if (!row || !row._verifyChip) return;
    row._verifyChip.hidden = false;
    row._verifyChip.classList.remove('pending');
    row._verifyChip.classList.remove('ok');
    row._verifyChip.classList.remove('err');
    if (variant) row._verifyChip.classList.add(variant);
    row._verifyChip.textContent = text;
  }

  function clearVerifyChip(row) {
    if (!row || !row._verifyChip) return;
    row._verifySeq = (row._verifySeq || 0) + 1;
    row._verifyChip.hidden = true;
    row._verifyChip.classList.remove('pending');
    row._verifyChip.classList.remove('ok');
    row._verifyChip.classList.remove('err');
    row._verifyChip.textContent = '';
    if (row._verify) row._verify.disabled = false;
  }

  function statusChipLabel(status) {
    const stateName = status && status.state;
    if (stateName === 'error') return 'X ERROR';
    if (stateName === 'pending') return 'ENUMERATING...';
    const count = Number(status && status.itemCount);
    return (Number.isFinite(count) && count > 0 ? count : 0) + ' VIDEOS';
  }

  function setEnumChip(row, status) {
    if (!row) return;
    let chip = row._enumChip;
    if (!chip) {
      chip = mk('span', 'cf-chip cf-enum-chip');
      row._enumChip = chip;
      row.appendChild(chip);
    }
    chip.hidden = false;
    chip.textContent = statusChipLabel(status);
    chip.classList.toggle('pending', status && status.state === 'pending');
    chip.classList.toggle('ok', status && status.state === 'ready');
    chip.classList.toggle('err', status && status.state === 'error');
  }

  function applyProviderStatus(payload) {
    if (!payload || !state.id || payload.provider !== state.id) return;
    const form = byID('catalog-form');
    if (form && form.getAttribute('aria-hidden') === 'true') return;
    const host = byID('cf-channels');
    if (!host || !Array.isArray(payload.channels)) return;

    const byChannel = new Map();
    payload.channels.forEach((status) => {
      if (status && status.channel) byChannel.set(status.channel, status);
    });
    kids(host).forEach((row) => {
      if (!row || !row.dataset || row.dataset.kind !== 'playlist') return;
      const status = byChannel.get(row.dataset.channelId || '');
      if (!status) return;
      setEnumChip(row, status);
    });
  }

  async function responseJSON(resp) {
    try {
      return await resp.json();
    } catch (_) {
      return {};
    }
  }

  function verifySuccessText(result, row) {
    const kind = normalizeKind(result && result.kind) || resolvedRowKind(row);
    if (kind === 'playlist') {
      const count = Number(result && result.itemCount);
      return '✓ ' + (Number.isFinite(count) ? count : 0) + ' videos';
    }
    if (kind === 'direct') return '✓ DIRECT';
    if (kind === 'single') return result && result.isLive ? '✓ LIVE' : '✓ VIDEO';
    return '✓ OK';
  }

  async function verifyRow(row) {
    if (!row) return;
    const verify = row._verify;
    const override = row._override ? row._override.value : 'auto';
    const body = {
      url: String(row._url ? row._url.value : '').trim(),
      kind: override === 'auto' ? '' : override,
    };
    const seq = (row._verifySeq || 0) + 1;
    row._verifySeq = seq;

    setVerifyChip(row, 'pending', 'VERIFYING');
    if (verify) verify.disabled = true;
    try {
      const resp = await postJSON('POST', '/ui/catalog/channel/verify', body);
      const result = await responseJSON(resp);
      if (row._verifySeq !== seq) return result;
      if (resp.ok && result && result.ok) {
        setVerifyChip(row, 'ok', verifySuccessText(result, row));
      } else {
        setVerifyChip(row, 'err', '✗ ' + (result && result.message ? result.message : 'Unable to verify channel.'));
      }
      return result;
    } catch (_) {
      if (row._verifySeq !== seq) return {};
      setVerifyChip(row, 'err', '✗ Unable to verify channel.');
      return {};
    } finally {
      if (verify && row._verifySeq === seq) verify.disabled = false;
    }
  }

  function removeElement(el) {
    if (!el || !el.parentElement) return;
    if (typeof el.remove === 'function') {
      el.remove();
      return;
    }
    const parent = el.parentElement;
    parent.replaceChildren(...kids(parent).filter((child) => child !== el));
  }

  function insertBeforeCompat(host, child, before) {
    if (!host || !child) return false;
    if (typeof host.insertBefore === 'function') {
      host.insertBefore(child, before || null);
      return true;
    }
    if (typeof host.replaceChildren !== 'function') return false;

    const next = kids(host).filter((item) => item !== child);
    const index = before ? next.indexOf(before) : -1;
    if (index < 0) {
      next.push(child);
    } else {
      next.splice(index, 0, child);
    }
    host.replaceChildren(...next);
    return true;
  }

  function reorderListMove(host, fromEl, toEl) {
    if (!host || !fromEl || !toEl || fromEl === toEl) return false;
    const children = kids(host);
    const fromIndex = children.indexOf(fromEl);
    const toIndex = children.indexOf(toEl);
    if (fromIndex < 0 || toIndex < 0) return false;

    if (fromIndex < toIndex) {
      return insertBeforeCompat(host, fromEl, children[toIndex + 1] || null);
    }
    return insertBeforeCompat(host, fromEl, toEl);
  }

  function syncGroupsFromHost(host) {
    const source = host || byID('cf-group-chips');
    if (!source) return currentGroups();
    const existing = new Map(currentGroups().map((group) => [group.id, group]));
    state.groups = kids(source).filter((chip) => chip.dataset && Object.prototype.hasOwnProperty.call(chip.dataset, 'groupId')).map((chip, index) => {
      const id = chip.dataset.groupId || '';
      const prev = existing.get(id) || {};
      return normalizeGroup({ ...prev, id, order: index }, index);
    });
    return state.groups;
  }

  function refreshGroupOptionsOnRows() {
    const host = byID('cf-channels');
    if (!host) return;
    kids(host).filter((row) => row.classList && row.classList.contains('cf-channel')).forEach((row) => {
      const selected = row._group ? row._group.value : '';
      fillGroupSelect(row._group, currentGroups(), selected);
    });
    state.channels = collectChannelModels();
  }

  function buildChannelRow(ch, groups) {
    const model = normalizeChannel(ch, 0);
    const groupList = normalizeGroups(groups);
    const row = mk('div', 'cf-channel');
    row.dataset.channelId = model.id;
    row._model = model;

    const name = mk('input', 'cf-input');
    name.type = 'text';
    name.maxLength = 96;
    name.placeholder = 'Channel name';
    name.value = model.name;

    const url = mk('input', 'cf-input');
    url.type = 'url';
    url.placeholder = 'https://example.com/live.m3u8';
    url.value = model.url;

    const kindChip = mk('span', 'cf-chip');

    const override = mk('select', 'cf-input');
    addOption(override, 'auto', 'Auto');
    addOption(override, 'single', 'Single');
    addOption(override, 'direct', 'Direct');
    addOption(override, 'playlist', 'Playlist');
    override.value = model.kind || 'auto';

    const playModeWrap = mk('span', 'cf-play-mode');
    const play = mk('select', 'cf-input');
    addOption(play, 'sequential', 'Sequential');
    addOption(play, 'shuffle', 'Shuffle');
    addOption(play, 'first_then_shuffle', 'First then shuffle');
    play.value = model.playMode || 'sequential';
    playModeWrap.appendChild(play);

    const group = mk('select', 'cf-input');
    fillGroupSelect(group, groupList, model.groupId);

    const verify = mk('button', 'cf-channel-verify');
    verify.type = 'button';
    verify.textContent = 'Verify';

    const verifyChip = mk('span', 'cf-chip');
    verifyChip.hidden = true;

    const del = mk('button', 'cf-channel-delete');
    del.type = 'button';
    del.textContent = 'x';

    const hint = mk('div', 'cf-hint');

    row.appendChild(name);
    row.appendChild(url);
    row.appendChild(kindChip);
    row.appendChild(override);
    row.appendChild(playModeWrap);
    row.appendChild(group);
    row.appendChild(verify);
    row.appendChild(verifyChip);
    row.appendChild(del);
    row.appendChild(hint);

    row._name = name;
    row._url = url;
    row._kindChip = kindChip;
    row._override = override;
    row._play = play;
    row._playModeWrap = playModeWrap;
    row._group = group;
    row._verify = verify;
    row._verifyChip = verifyChip;
    row._hint = hint;
    row._refresh = () => refreshChannelRow(row);

    url.addEventListener('input', () => {
      clearVerifyChip(row);
      row._refresh();
    });
    override.addEventListener('change', () => {
      clearVerifyChip(row);
      row._refresh();
    });
    verify.addEventListener('click', (event) => {
      event.preventDefault();
      verifyRow(row);
    });
    del.addEventListener('click', (event) => {
      event.preventDefault();
      removeElement(row);
      state.channels = collectChannelModels();
    });
    keepDragOffControls([name, url, override, play, group, verify, del]);

    row._refresh();
    return row;
  }

  function renderGroups(groups) {
    const models = normalizeGroups(groups === undefined ? state.groups : groups);
    state.groups = cloneList(models);
    const host = byID('cf-group-chips');
    if (!host) return;

    const chips = models.map((group) => {
      const chip = mk('span', 'cf-chip cf-group-chip');
      chip.dataset.groupId = group.id;
      chip.textContent = group.name || group.id || 'Group';

      const del = mk('button', 'cf-group-delete');
      del.type = 'button';
      del.dataset.groupDelete = group.id;
      del.textContent = 'x';
      keepDragOffControls([del]);
      del.addEventListener('click', (event) => {
        event.preventDefault();
        renderGroups(currentGroups().filter((item) => item.id !== group.id));
        refreshGroupOptionsOnRows();
      });
      chip.appendChild(del);
      return chip;
    });

    host.replaceChildren(...chips);
  }

  function renderChannels(channels) {
    const models = normalizeChannels(channels === undefined ? state.channels : channels);
    if (channels !== undefined) state.channels = cloneList(models);
    const host = byID('cf-channels');
    if (!host) return;

    host.replaceChildren(...models.map((channel) => buildChannelRow(channel, currentGroups())));
  }

  function collectChannelModels() {
    const host = byID('cf-channels');
    if (!host) return cloneList(state.channels);
    return kids(host).filter((row) => row.classList && row.classList.contains('cf-channel')).map((row, index) => {
      const resolved = resolvedRowKind(row);
      const override = row._override ? normalizeKind(row._override.value) : '';
      return {
        id: row.dataset.channelId || '',
        name: row._name ? row._name.value : '',
        url: row._url ? row._url.value : '',
        kind: override,
        playMode: resolved === 'playlist' && row._play ? normalizePlayMode(row._play.value) : '',
        groupId: row._group ? row._group.value : '',
        order: index,
      };
    });
  }

  function inputValue(id) {
    const el = byID(id);
    return el ? el.value : '';
  }

  function collectPayload() {
    const groups = syncGroupsFromHost().map((group, index) => normalizeGroup({ ...group, order: index }, index));
    const channels = collectChannelModels();
    state.groups = cloneList(groups);
    state.channels = cloneList(channels);
    return {
      displayName: inputValue('cf-name'),
      badgeLabel: inputValue('cf-glyph'),
      badgeColor: state.color,
      groups,
      channels,
    };
  }

  function setRowURL(row, value) {
    if (!row || !row._url) return;
    row._url.value = value == null ? '' : String(value);
    if (typeof row._url.dispatchEvent === 'function' && typeof Event === 'function') {
      row._url.dispatchEvent(new Event('input', { bubbles: true }));
      return;
    }
    if (typeof row._url.dispatch === 'function') {
      row._url.dispatch('input');
      return;
    }
    if (typeof row._refresh === 'function') row._refresh();
  }

  function addChannel() {
    const channels = collectChannelModels();
    channels.push({
      id: '',
      name: '',
      url: '',
      kind: '',
      playMode: '',
      groupId: '',
      order: channels.length,
    });
    renderChannels(channels);
  }

  function addGroup() {
    const groups = cloneList(currentGroups());
    const id = nextID('group', groups);
    groups.push({ id, name: 'Group ' + (groups.length + 1), order: groups.length });
    renderGroups(groups);
    refreshGroupOptionsOnRows();
  }

  async function postReorder() {
    if (!state.id) return true;
    const body = {
      channels: collectChannelModels().filter((channel) => channel.id).map((channel) => ({ id: channel.id, order: channel.order })),
      groups: currentGroups().filter((group) => group.id).map((group, index) => ({ id: group.id, order: index })),
    };
    try {
      const resp = await postJSON('POST', '/ui/catalog/provider/' + encodeURIComponent(state.id) + '/reorder', body);
      const result = await responseJSON(resp);
      if (resp.ok && result.ok !== false) return true;
      showNotice((result && (result.message || result.chip)) || 'Unable to reorder provider.', 'err');
      return false;
    } catch (_) {
      showNotice('Unable to reorder provider.', 'err');
      return false;
    }
  }

  function sortableItem(el, selector) {
    return el && el.closest ? el.closest(selector) : el;
  }

  function wireChannelReorder() {
    const host = byID('cf-channels');
    const reorder = window.Chassis && window.Chassis.reorder;
    if (!host || !reorder || typeof reorder.makeSortable !== 'function') return;
    reorder.makeSortable({
      container: host,
      itemSelector: '.cf-channel',
      onDrop(fromEl, toEl) {
        const from = sortableItem(fromEl, '.cf-channel');
        const to = sortableItem(toEl, '.cf-channel');
        const before = collectChannelModels();
        if (!reorderListMove(host, from, to)) return;
        state.channels = collectChannelModels();
        return postReorder().then((ok) => {
          if (ok) return;
          renderChannels(before);
        });
      },
    });
  }

  function wireGroupReorder() {
    const host = byID('cf-group-chips');
    const reorder = window.Chassis && window.Chassis.reorder;
    if (!host || !reorder || typeof reorder.makeSortable !== 'function') return;
    reorder.makeSortable({
      container: host,
      itemSelector: '.cf-group-chip',
      onDrop(fromEl, toEl) {
        const from = sortableItem(fromEl, '.cf-group-chip');
        const to = sortableItem(toEl, '.cf-group-chip');
        const beforeGroups = cloneList(currentGroups());
        const beforeChannels = collectChannelModels();
        if (!reorderListMove(host, from, to)) return;
        syncGroupsFromHost(host);
        refreshGroupOptionsOnRows();
        return postReorder().then((ok) => {
          if (ok) return;
          renderGroups(beforeGroups);
          renderChannels(beforeChannels);
        });
      },
    });
  }

  function openForm() {
    document.body.classList.add('catalog-form-open');
    const form = byID('catalog-form');
    if (form) form.setAttribute('aria-hidden', 'false');
  }

  function closeForm() {
    invalidateMutation();
    document.body.classList.remove('catalog-form-open');
    const form = byID('catalog-form');
    if (form) form.setAttribute('aria-hidden', 'true');
  }

  function setValue(id, value) {
    const el = byID(id);
    if (el) el.value = value == null ? '' : String(value);
  }

  function newProvider() {
    invalidateMutation();
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
    invalidateMutation();
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

  function saveURL() {
    if (!state.id) return '/ui/catalog/provider';
    return '/ui/catalog/provider/' + encodeURIComponent(state.id);
  }

  async function save() {
    const token = beginMutation();
    if (!token) return false;
    const creating = !state.id;
    const url = saveURL();
    const payload = collectPayload();
    try {
      const resp = await postJSON(creating ? 'POST' : 'PUT', url, payload);
      const result = await responseJSON(resp);
      if (!isCurrentMutation(token)) return false;
      if (!resp.ok || (result && result.ok === false)) {
        showNotice(noticeText(result, 'Unable to save provider.'), 'err');
        return false;
      }
      if (creating) showAutoEnableNotice(result);
      showClearedSlots(result);
      closeForm();
      return true;
    } catch (_) {
      if (isCurrentMutation(token)) showNotice('Unable to save provider.', 'err');
      return false;
    } finally {
      finishMutation(token);
    }
  }

  function presetKey(provider, channel) {
    return String(provider || '') + ':' + String(channel || '');
  }

  function applyPresetPayload(payload) {
    starredPresetKeys.clear();
    if (!payload || !Array.isArray(payload.slots)) return;
    payload.slots.forEach((slot) => {
      if (slot && slot.provider && slot.channel) {
        starredPresetKeys.add(presetKey(slot.provider, slot.channel));
      }
    });
  }

  function onPresets(ev) {
    let payload = ev || {};
    if (ev && typeof ev.data === 'string') {
      try {
        payload = JSON.parse(ev.data);
      } catch (_) {
        return;
      }
    }
    applyPresetPayload(payload);
  }

  function onProviderStatus(ev) {
    let payload = ev || {};
    if (ev && typeof ev.data === 'string') {
      try {
        payload = JSON.parse(ev.data);
      } catch (_) {
        return;
      }
    }
    applyProviderStatus(payload);
  }

  function anyStarredChannel() {
    if (!state.id) return false;
    const prefix = state.id + ':';
    for (const key of starredPresetKeys) {
      if (key.indexOf(prefix) === 0) return true;
    }
    return false;
  }

  function deleteConfirmMessage() {
    if (anyStarredChannel()) {
      return 'Delete this provider? Starred preset slots for its channels will be cleared.';
    }
    return 'Delete this provider?';
  }

  async function deleteProvider() {
    if (!state.id) {
      closeForm();
      return true;
    }
    if (pendingMutation) return false;
    if (typeof window.confirm === 'function' && !window.confirm(deleteConfirmMessage())) {
      return false;
    }
    const token = beginMutation();
    if (!token) return false;
    const id = state.id;
    try {
      const resp = await postJSON('DELETE', '/ui/catalog/provider/' + encodeURIComponent(id));
      const result = await responseJSON(resp);
      if (!isCurrentMutation(token)) return false;
      if (!resp.ok || (result && result.ok === false)) {
        showNotice(noticeText(result, 'Unable to delete provider.'), 'err');
        return false;
      }
      showClearedSlots(result);
      closeForm();
      return true;
    } catch (_) {
      if (isCurrentMutation(token)) showNotice('Unable to delete provider.', 'err');
      return false;
    } finally {
      finishMutation(token);
    }
  }

  function wirePresetEvents() {
    const events = window.Chassis && window.Chassis.events;
    if (events && typeof events.subscribe === 'function') {
      events.subscribe('presets', onPresets);
      events.subscribe('providerStatus', onProviderStatus);
    }
  }

  function wireEvents() {
    wirePresetEvents();
    wireChannelReorder();
    wireGroupReorder();

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
    const form = byID('cf-form');
    if (form) {
      form.addEventListener('submit', (event) => {
        event.preventDefault();
        save();
      });
    }
    const saveButton = byID('cf-save');
    if (saveButton) {
      saveButton.addEventListener('click', (event) => {
        event.preventDefault();
        save();
      });
    }
    const deleteButton = byID('cf-delete');
    if (deleteButton) {
      deleteButton.addEventListener('click', (event) => {
        event.preventDefault();
        deleteProvider();
      });
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
    const addChannelButton = byID('cf-add-channel');
    if (addChannelButton) {
      addChannelButton.addEventListener('click', (event) => {
        event.preventDefault();
        addChannel();
      });
    }
    const addGroupButton = byID('cf-add-group');
    if (addGroupButton) {
      addGroupButton.addEventListener('click', (event) => {
        event.preventDefault();
        addGroup();
      });
    }
  }

  window.Chassis.providerForm = {
    detectKind,
    suggestGlyph,
    hostHint,
    selectColor,
    renderGroups,
    renderChannels,
    collectChannelModels,
    openForm,
    closeForm,
    newProvider,
    editProvider,
    populate,
    _collectPayload: collectPayload,
    _save: save,
    _delete: deleteProvider,
    _anyStarredChannel: anyStarredChannel,
    _reorderListMove: reorderListMove,
    _syncGroupsFromHost: syncGroupsFromHost,
    _currentGroups: currentGroups,
    _buildChannelRow: buildChannelRow,
    _setRowURL: setRowURL,
    _verifyRow: verifyRow,
    _statusChipLabel: statusChipLabel,
    _applyProviderStatus: applyProviderStatus,
    _state: state,
    _palette: palette,
  };

  wireEvents();
}());
