const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class FakeClassList {
  constructor(owner) {
    this.owner = owner;
  }

  add(name) {
    this.owner.classes.add(name);
  }

  remove(name) {
    this.owner.classes.delete(name);
  }

  contains(name) {
    return this.owner.classes.has(name);
  }

  toggle(name, on) {
    const next = on === undefined ? !this.owner.classes.has(name) : on;
    if (next) {
      this.owner.classes.add(name);
    } else {
      this.owner.classes.delete(name);
    }
    return next;
  }
}

class FakeElement {
  constructor(tag = 'div', opts = {}) {
    this.tagName = tag.toUpperCase();
    this.id = opts.id || '';
    this.classes = new Set(String(opts.className || '').split(/\s+/).filter(Boolean));
    this.classList = new FakeClassList(this);
    this.dataset = { ...(opts.dataset || {}) };
    this.value = opts.value || '';
    this.hidden = Boolean(opts.hidden);
    this.listeners = new Map();
    this.attributes = new Map();
    this.children = [];
    this.parentElement = null;
    this.textContent = '';
    if (this.id) this.attributes.set('id', this.id);
  }

  appendChild(child) {
    if (child.parentElement) {
      child.parentElement.children = child.parentElement.children.filter((item) => item !== child);
    }
    child.parentElement = this;
    this.children.push(child);
    return child;
  }

  insertBefore(child, before) {
    if (child.parentElement) {
      child.parentElement.children = child.parentElement.children.filter((item) => item !== child);
    }
    child.parentElement = this;
    const index = this.children.indexOf(before);
    if (index < 0) {
      this.children.push(child);
    } else {
      this.children.splice(index, 0, child);
    }
    return child;
  }

  replaceChildren(...children) {
    this.children = [];
    for (const child of children) this.appendChild(child);
  }

  addEventListener(type, fn) {
    const list = this.listeners.get(type) || [];
    list.push(fn);
    this.listeners.set(type, list);
  }

  dispatch(type, event = {}) {
    const evt = {
      type,
      target: event.target || this,
      currentTarget: this,
      preventDefault() {},
      stopPropagation() {
        this.bubbles = false;
      },
      ...event,
    };
    let node = this;
    while (node) {
      evt.currentTarget = node;
      for (const fn of node.listeners.get(type) || []) fn(evt);
      if (evt.bubbles === false) break;
      node = node.parentElement;
    }
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }

  getAttribute(name) {
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }

  removeAttribute(name) {
    this.attributes.delete(name);
  }

  closest(selector) {
    let node = this;
    while (node) {
      if (matchesSelector(node, selector)) return node;
      node = node.parentElement;
    }
    return null;
  }

  querySelector(selector) {
    return walkFind(this, selector, false);
  }

  querySelectorAll(selector) {
    const results = [];
    walkFind(this, selector, true, results);
    return results;
  }
}

function matchesSelector(node, selector) {
  const parts = String(selector || '').trim().split(/\s+/).filter(Boolean);
  if (parts.length > 1) return matchesSelectorPath(node, parts, parts.length - 1);
  return matchesSimpleSelector(node, parts[0] || '');
}

function matchesSelectorPath(node, parts, index) {
  if (!matchesSimpleSelector(node, parts[index])) return false;
  if (index === 0) return true;
  let parent = node.parentElement;
  while (parent) {
    if (matchesSelectorPath(parent, parts, index - 1)) return true;
    parent = parent.parentElement;
  }
  return false;
}

function matchesSimpleSelector(node, selector) {
  if (!node) return false;
  let rest = selector;
  const tagMatch = rest.match(/^[a-z][a-z0-9-]*/i);
  if (tagMatch) {
    if (node.tagName.toLowerCase() !== tagMatch[0].toLowerCase()) return false;
    rest = rest.slice(tagMatch[0].length);
  }
  while (rest.length > 0) {
    if (rest.startsWith('#')) {
      const match = rest.match(/^#([a-zA-Z0-9_-]+)/);
      if (!match || node.id !== match[1]) return false;
      rest = rest.slice(match[0].length);
      continue;
    }
    if (rest.startsWith('.')) {
      const match = rest.match(/^\.([a-zA-Z0-9_-]+)/);
      if (!match || !node.classes.has(match[1])) return false;
      rest = rest.slice(match[0].length);
      continue;
    }
    if (rest.startsWith('[')) {
      const match = rest.match(/^\[([^=\]]+)(?:="([^"]*)")?\]/);
      if (!match) return false;
      const value = datasetValue(node, match[1]);
      if (value == null) return false;
      if (match[2] !== undefined && value !== match[2]) return false;
      rest = rest.slice(match[0].length);
      continue;
    }
    return false;
  }
  return true;
}

function datasetValue(node, attr) {
  if (!attr.startsWith('data-')) return node.attributes.has(attr) ? node.attributes.get(attr) : null;
  const key = attr.slice(5).replace(/-([a-z])/g, (_, ch) => ch.toUpperCase());
  return Object.prototype.hasOwnProperty.call(node.dataset, key) ? node.dataset[key] : null;
}

function walkFind(root, selector, all, acc = []) {
  for (const child of root.children) {
    if (matchesSelector(child, selector)) {
      if (!all) return child;
      acc.push(child);
    }
    const found = walkFind(child, selector, all, acc);
    if (!all && found) return found;
  }
  return all ? acc : null;
}

function createHarness(fetchImpl, opts = {}) {
  const body = new FakeElement('body');
  const drawer = new FakeElement('div', { id: 'catalog-drawer' });
  const formPanel = new FakeElement('div', { id: 'catalog-form' });
  formPanel.setAttribute('aria-hidden', 'true');
  const form = new FakeElement('form', { id: 'cf-form' });
  const id = new FakeElement('input', { id: 'cf-provider-id' });
  const name = new FakeElement('input', { id: 'cf-name' });
  const glyph = new FakeElement('input', { id: 'cf-glyph' });
  const swatches = new FakeElement('div', { id: 'cf-swatches' });
  const groups = new FakeElement('div', { id: 'cf-groups' });
  const groupChips = new FakeElement('span', { id: 'cf-group-chips', className: 'cf-group-chips' });
  const addGroup = new FakeElement('button', { id: 'cf-add-group', className: 'cf-add-group' });
  const channels = new FakeElement('div', { id: 'cf-channels' });
  const addChannel = new FakeElement('button', { id: 'cf-add-channel', className: 'cf-add-channel' });
  const del = new FakeElement('button', { id: 'cf-delete', hidden: true });
  const save = new FakeElement('button', { id: 'cf-save', className: 'cf-save' });
  const cancel = new FakeElement('button', { id: 'cf-cancel' });
  const newButton = new FakeElement('button', { id: 'catalog-provider-new' });
  const pencil = new FakeElement('button', { dataset: { editProvider: 'user:abc' } });
  const swatchButtons = ['amber', 'red', 'teal', 'blue', 'purple', 'green', 'cyan', 'slate'].map((color) => {
    const button = new FakeElement('button', { className: 'cf-swatch', dataset: { color } });
    button.setAttribute('aria-checked', 'false');
    swatches.appendChild(button);
    return button;
  });

  form.appendChild(id);
  form.appendChild(name);
  form.appendChild(glyph);
  form.appendChild(swatches);
  groups.appendChild(groupChips);
  groups.appendChild(addGroup);
  form.appendChild(groups);
  form.appendChild(channels);
  form.appendChild(addChannel);
  form.appendChild(del);
  form.appendChild(save);
  form.appendChild(cancel);
  formPanel.appendChild(form);
  drawer.appendChild(pencil);
  drawer.appendChild(newButton);
  drawer.appendChild(formPanel);
  body.appendChild(drawer);

  const byID = new Map([
    ['catalog-drawer', drawer],
    ['catalog-form', formPanel],
    ['cf-provider-id', id],
    ['cf-name', name],
    ['cf-glyph', glyph],
    ['cf-swatches', swatches],
    ['cf-groups', groups],
    ['cf-group-chips', groupChips],
    ['cf-add-group', addGroup],
    ['cf-channels', channels],
    ['cf-add-channel', addChannel],
    ['cf-delete', del],
    ['cf-save', save],
    ['cf-form', form],
    ['cf-cancel', cancel],
    ['catalog-provider-new', newButton],
  ]);

  const document = {
    body,
    getElementById(idValue) {
      return byID.get(idValue) || null;
    },
    querySelector(selector) {
      return body.querySelector(selector);
    },
    querySelectorAll(selector) {
      return body.querySelectorAll(selector);
    },
    createElement(tag) {
      return new FakeElement(tag);
    },
  };

  const notices = [];
  const reorderCalls = [];
  const eventHandlers = new Map();
  const confirmMessages = [];
  const chassis = {
    settings: {
      showNotice(text, variant) {
        notices.push([text, variant]);
      },
    },
    events: {
      subscribe(name, fn) {
        const list = eventHandlers.get(name) || [];
        list.push(fn);
        eventHandlers.set(name, list);
      },
    },
  };
  if (opts.fakeReorder) {
    chassis.reorder = {
      makeSortable(config) {
        reorderCalls.push(config);
        return { cancel() {} };
      },
    };
  }
  if (opts.reorder) chassis.reorder = opts.reorder;
  const context = {
    document,
    window: {
      Chassis: chassis,
      confirm(message) {
        confirmMessages.push(String(message || ''));
        return opts.confirmResult !== undefined ? opts.confirmResult : true;
      },
    },
    fetch: fetchImpl || (async () => ({ ok: true, json: async () => ({}) })),
    URL,
    console,
  };
  context.window.window = context.window;
  context.window.document = document;
  vm.createContext(context);

  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'provider-form.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'provider-form.js' });
  function emitEvent(name, payload) {
    for (const fn of eventHandlers.get(name) || []) fn({ data: JSON.stringify(payload) });
  }
  return { context, drawer, formPanel, form, id, name, glyph, swatches, swatchButtons, groups, groupChips, addGroup, channels, addChannel, del, save, cancel, newButton, pencil, notices, reorderCalls, eventHandlers, emitEvent, confirmMessages };
}

function jsonResponse(body, ok = true) {
  return {
    ok,
    json: async () => body,
  };
}

function sortableCall(h, selector) {
  return h.reorderCalls.find((call) => call.itemSelector === selector);
}

function nextTick() {
  return new Promise((resolve) => setImmediate(resolve));
}

function requestBody(req) {
  return JSON.parse(req[1].body);
}

test('detectKind mirrors Go channel kind detection cases', () => {
  const h = createHarness();
  const { detectKind } = h.context.window.Chassis.providerForm;

  assert.equal(detectKind('https://cdn.example.com/live.m3u8'), 'direct');
  assert.equal(detectKind('https://host/stream.mpd'), 'direct');
  assert.equal(detectKind('https://www.youtube.com/playlist?list=PL123'), 'playlist');
  assert.equal(detectKind('https://youtube.com/watch?v=abc&list=PL123'), 'playlist');
  assert.equal(detectKind('https://youtube.com/watch?list='), 'single');
  assert.equal(detectKind('https://twitch.tv/foo'), 'single');
  assert.equal(detectKind('not a url'), 'single');
  assert.equal(detectKind('file:///tmp/live.m3u8'), 'single');
});

test('suggestGlyph uses words first and caps output at four characters', () => {
  const h = createHarness();
  const { suggestGlyph } = h.context.window.Chassis.providerForm;

  assert.equal(suggestGlyph('F1 TV'), 'F1');
  assert.equal(suggestGlyph('Cartoon Network'), 'CN');
  assert.equal(suggestGlyph('Lofi'), 'LO');
  assert.equal(suggestGlyph('Very Long Provider'), 'VLP');
});

test('selectColor marks exactly one swatch selected and tracks color', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  assert.equal(typeof api.selectColor, 'function');

  api.selectColor('teal');

  assert.deepEqual(h.swatchButtons.filter((button) => button.classList.contains('selected')).map((button) => button.dataset.color), ['teal']);
  assert.equal(h.swatchButtons.find((button) => button.dataset.color === 'teal').getAttribute('aria-checked'), 'true');
  assert.equal(h.swatchButtons.find((button) => button.dataset.color === 'slate').getAttribute('aria-checked'), 'false');
  assert.equal(api._state.color, 'teal');

  api.selectColor('not-a-palette-token');

  assert.deepEqual(h.swatchButtons.filter((button) => button.classList.contains('selected')).map((button) => button.dataset.color), ['slate']);
  assert.equal(api._state.color, 'slate');

  h.swatches.dispatch('click', { target: h.swatchButtons.find((button) => button.dataset.color === 'purple') });

  assert.deepEqual(h.swatchButtons.filter((button) => button.classList.contains('selected')).map((button) => button.dataset.color), ['purple']);
  assert.equal(api._state.color, 'purple');
});

test('hostHint allows routable hosts and rejects obvious local-only targets', () => {
  const h = createHarness();
  const { hostHint } = h.context.window.Chassis.providerForm;

  assert.equal(hostHint('https://192.168.1.15/live.m3u8').ok, true);
  assert.equal(hostHint('https://cdn.example.com/live.m3u8').ok, true);
  assert.equal(hostHint('not a url').ok, false);
  assert.equal(hostHint('http://localhost/live.m3u8').ok, false);
  assert.equal(hostHint('http://127.0.0.1/live.m3u8').ok, false);
  assert.equal(hostHint('http://[::1]/x').ok, false);
  assert.equal(hostHint('http://[fe80::1]/x').ok, false);
  assert.equal(hostHint('http://169.254.169.254/latest/meta-data').ok, false);
  assert.equal(hostHint('file:///tmp/video.m3u8').ok, false);
});

test('newProvider resets blank state with slate color and opens the form', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  api.newProvider();

  assert.equal(h.id.value, '');
  assert.equal(h.name.value, '');
  assert.equal(h.glyph.value, '');
  assert.equal(h.del.hidden, true);
  assert.equal(api._state.mode, 'new');
  assert.equal(api._state.color, 'slate');
  assert.equal(api._state.glyphTouched, false);
  assert.equal(h.context.document.body.classList.contains('catalog-form-open'), true);
  assert.equal(h.formPanel.getAttribute('aria-hidden'), 'false');
});

test('name input suggests glyph until glyph is touched by the operator', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  api.newProvider();

  h.name.value = 'Cartoon Network';
  h.name.dispatch('input');

  assert.equal(h.glyph.value, 'CN');
  assert.equal(api._state.glyphTouched, false);

  h.name.value = 'Lofi';
  h.name.dispatch('input');

  assert.equal(h.glyph.value, 'LO');

  h.glyph.value = 'OP';
  h.glyph.dispatch('input');
  h.name.value = 'Very Long Provider';
  h.name.dispatch('input');

  assert.equal(h.glyph.value, 'OP');
  assert.equal(api._state.glyphTouched, true);
});

test('populate fills identity, marks editing, shows delete, and opens the form', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  api.populate({
    id: 'user:abc',
    displayName: 'Cartoon Network',
    badgeLabel: 'CN',
    badgeColor: 'teal',
    groups: [{ id: 'g', name: 'Group', order: 0 }],
    channels: [{ id: 'c', name: 'Channel', url: 'https://cdn.example.com/live.m3u8', order: 0 }],
  });

  assert.equal(h.id.value, 'user:abc');
  assert.equal(h.name.value, 'Cartoon Network');
  assert.equal(h.glyph.value, 'CN');
  assert.equal(h.del.hidden, false);
  assert.equal(api._state.mode, 'edit');
  assert.equal(api._state.color, 'teal');
  assert.equal(api._state.glyphTouched, true);
  assert.deepEqual(JSON.parse(JSON.stringify(api._state.groups)), [{ id: 'g', name: 'Group', order: 0 }]);
  assert.deepEqual(JSON.parse(JSON.stringify(api._state.channels)), [{ id: 'c', name: 'Channel', url: 'https://cdn.example.com/live.m3u8', order: 0 }]);
  assert.equal(h.formPanel.getAttribute('aria-hidden'), 'false');
});

test('populate treats existing glyph as touched so name edits preserve badge label', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  api.populate({
    id: 'user:abc',
    displayName: 'Cartoon Network',
    badgeLabel: 'CN',
    badgeColor: 'teal',
    groups: [],
    channels: [],
  });
  h.name.value = 'Lofi Girl';
  h.name.dispatch('input');

  assert.equal(h.glyph.value, 'CN');
  assert.equal(api._state.glyphTouched, true);
});

test('drawer delegation handles sibling pencil buttons and new provider button', async () => {
  const requests = [];
  const h = createHarness(async (url, init) => {
    requests.push([url, init]);
    return {
      ok: true,
      json: async () => ({
        id: 'user:abc',
        displayName: 'Edited',
        badgeLabel: 'ED',
        badgeColor: 'red',
        groups: [],
        channels: [],
      }),
    };
  });

  h.drawer.dispatch('click', { target: h.pencil });
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(requests.length, 1);
  assert.equal(requests[0][0], '/ui/catalog/provider/user%3Aabc');
  assert.equal(requests[0][1].method, 'GET');
  assert.equal(requests[0][1].credentials, 'same-origin');
  assert.equal(h.name.value, 'Edited');

  h.drawer.dispatch('click', { target: h.newButton });
  assert.equal(h.context.window.Chassis.providerForm._state.mode, 'new');
  assert.equal(h.name.value, '');
});

test('renderChannels builds rows and resolves direct kind for m3u8 URLs', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  api.renderGroups([{ id: 'races', name: 'Races', order: 0 }]);
  api.renderChannels([
    { id: 'live', name: 'Live', url: 'https://cdn.example.com/live.m3u8', kind: '', groupId: 'races', order: 9 },
    { id: 'clips', name: 'Clips', url: 'https://videos.example.com/watch', kind: '', groupId: '', order: 3 },
  ]);

  const rows = Array.prototype.slice.call(h.channels.children);
  assert.equal(rows.length, 2);
  assert.equal(rows[0].classList.contains('cf-channel'), true);
  assert.equal(rows[0].dataset.channelId, 'live');
  assert.equal(rows[0].dataset.kind, 'direct');
  assert.equal(rows[0]._name.value, 'Live');
  assert.equal(rows[0]._url.value, 'https://cdn.example.com/live.m3u8');
  assert.equal(rows[0]._kindChip.textContent, 'DIRECT');
  assert.equal(rows[0]._override.value, 'auto');
  assert.equal(rows[0]._group.value, 'races');
  assert.equal(rows[1].dataset.kind, 'single');
});

test('_buildChannelRow and _setRowURL refresh resolved kind, play mode visibility, and host hint', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  const row = api._buildChannelRow({ id: 'yt', name: 'YouTube', url: '', kind: '', playMode: 'shuffle' }, []);

  assert.equal(row.dataset.kind, 'single');
  assert.equal(row._playModeWrap.hidden, true);
  assert.match(row._hint.textContent, /Detected: single/);

  api._setRowURL(row, 'https://www.youtube.com/watch?v=abc&list=PL123');

  assert.equal(row.dataset.kind, 'playlist');
  assert.equal(row._kindChip.textContent, 'PLAYLIST');
  assert.equal(row._playModeWrap.hidden, false);
  assert.match(row._hint.textContent, /Detected: playlist/);
  assert.match(row._hint.textContent, /Host allowed/);
  assert.equal(row._hint.classList.contains('err'), false);

  api._setRowURL(row, 'http://127.0.0.1/live.m3u8');

  assert.equal(row.dataset.kind, 'direct');
  assert.equal(row._playModeWrap.hidden, true);
  assert.match(row._hint.textContent, /Local-only hosts are blocked here/);
  assert.equal(row._hint.classList.contains('err'), true);

  row._override.value = 'single';
  row._override.dispatch('change');

  assert.equal(row.dataset.kind, 'single');
  assert.equal(row._playModeWrap.hidden, true);
});

test('playlist play mode uses authoring wire values, including first-then-shuffle', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  api.renderChannels([
    {
      id: 'yt',
      name: 'YouTube',
      url: 'https://www.youtube.com/watch?v=abc&list=PL123',
      kind: 'playlist',
      playMode: 'first_then_shuffle',
      groupId: '',
      order: 0,
    },
  ]);

  const row = h.channels.children[0];
  assert.deepEqual(Array.prototype.slice.call(row._play.children).map((option) => option.value), ['sequential', 'shuffle', 'first_then_shuffle']);
  assert.equal(row._play.value, 'first_then_shuffle');
  assert.equal(api.collectChannelModels()[0].playMode, 'first_then_shuffle');

  row._play.value = 'shuffle';
  assert.equal(api.collectChannelModels()[0].playMode, 'shuffle');
});

test('_verifyRow renders playlist count ok chip', async () => {
  const h = createHarness(async () => jsonResponse({ ok: true, kind: 'playlist', itemCount: 47 }));
  const api = h.context.window.Chassis.providerForm;
  const row = api._buildChannelRow({ name: 'Playlist', url: 'https://www.youtube.com/playlist?list=PL123' }, []);

  await api._verifyRow(row);

  assert.equal(row._verifyChip.hidden, false);
  assert.equal(row._verifyChip.classList.contains('ok'), true);
  assert.equal(row._verifyChip.classList.contains('err'), false);
  assert.equal(row._verifyChip.textContent, '✓ 47 videos');
});

test('_verifyRow renders LIVE chip for live single result', async () => {
  const h = createHarness(async () => jsonResponse({ ok: true, kind: 'single', isLive: true }));
  const api = h.context.window.Chassis.providerForm;
  const row = api._buildChannelRow({ name: 'Live', url: 'https://youtube.com/watch?v=abc' }, []);

  await api._verifyRow(row);

  assert.equal(row._verifyChip.hidden, false);
  assert.equal(row._verifyChip.classList.contains('ok'), true);
  assert.equal(row._verifyChip.textContent, '✓ LIVE');
});

test('_verifyRow renders VIDEO chip for non-live single result', async () => {
  const h = createHarness(async () => jsonResponse({ ok: true, kind: 'single', isLive: false }));
  const api = h.context.window.Chassis.providerForm;
  const row = api._buildChannelRow({ name: 'Video', url: 'https://youtube.com/watch?v=abc' }, []);

  await api._verifyRow(row);

  assert.equal(row._verifyChip.hidden, false);
  assert.equal(row._verifyChip.classList.contains('ok'), true);
  assert.equal(row._verifyChip.textContent, '✓ VIDEO');
});

test('_verifyRow renders DIRECT chip for direct result', async () => {
  const h = createHarness(async () => jsonResponse({ ok: true, kind: 'direct' }));
  const api = h.context.window.Chassis.providerForm;
  const row = api._buildChannelRow({ name: 'Direct', url: 'https://cdn.example.com/live.m3u8' }, []);

  await api._verifyRow(row);

  assert.equal(row._verifyChip.hidden, false);
  assert.equal(row._verifyChip.classList.contains('ok'), true);
  assert.equal(row._verifyChip.textContent, '✓ DIRECT');
});

test('_verifyRow renders error chip with message on failed response', async () => {
  const h = createHarness(async () => jsonResponse({ ok: false, message: 'Local-only hosts are blocked here.' }, false));
  const api = h.context.window.Chassis.providerForm;
  const row = api._buildChannelRow({ name: 'Bad', url: 'http://127.0.0.1/live.m3u8' }, []);

  await api._verifyRow(row);

  assert.equal(row._verifyChip.hidden, false);
  assert.equal(row._verifyChip.classList.contains('err'), true);
  assert.equal(row._verifyChip.classList.contains('ok'), false);
  assert.match(row._verifyChip.textContent, /^✗ /);
  assert.match(row._verifyChip.textContent, /Local-only hosts are blocked here/);
});

test('verify button posts channel URL with auto or manually overridden kind', async () => {
  const requests = [];
  const h = createHarness(async (url, init) => {
    requests.push([url, init]);
    return jsonResponse({ ok: true, kind: init && JSON.parse(init.body).kind === 'direct' ? 'direct' : 'single' });
  });
  const api = h.context.window.Chassis.providerForm;
  const autoRow = api._buildChannelRow({ name: 'Auto', url: 'https://cdn.example.com/live.m3u8', kind: '' }, []);
  const manualRow = api._buildChannelRow({ name: 'Manual', url: 'https://video.example.com/watch', kind: 'direct' }, []);

  assert.equal(autoRow._verify.type, 'button');
  assert.equal(autoRow._verify.textContent, 'Verify');
  assert.equal(autoRow._verifyChip.hidden, true);

  autoRow._verify.dispatch('click');
  manualRow._verify.dispatch('click');
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(requests.length, 2);
  assert.equal(requests[0][0], '/ui/catalog/channel/verify');
  assert.equal(requests[0][1].method, 'POST');
  assert.equal(requests[0][1].credentials, 'same-origin');
  assert.equal(requests[0][1].headers['Content-Type'], 'application/json');
  assert.deepEqual(JSON.parse(requests[0][1].body), { url: 'https://cdn.example.com/live.m3u8', kind: '' });
  assert.deepEqual(JSON.parse(requests[1][1].body), { url: 'https://video.example.com/watch', kind: 'direct' });
});

test('_verifyRow ignores stale results after URL or kind edits', async () => {
  let resolveFetch;
  const h = createHarness(async () => new Promise((resolve) => {
    resolveFetch = resolve;
  }));
  const api = h.context.window.Chassis.providerForm;
  const row = api._buildChannelRow({ name: 'Playlist', url: 'https://www.youtube.com/watch?v=abc&list=PL123' }, []);

  const pending = api._verifyRow(row);
  assert.equal(row._verifyChip.hidden, false);
  assert.equal(row._verifyChip.classList.contains('pending'), true);

  api._setRowURL(row, 'https://www.youtube.com/watch?v=changed&list=PL456');

  assert.equal(row._verifyChip.hidden, true);
  assert.equal(row._verifyChip.classList.contains('pending'), false);

  resolveFetch(jsonResponse({ ok: true, kind: 'playlist', itemCount: 47 }));
  await pending;

  assert.equal(row._verifyChip.hidden, true);
  assert.equal(row._verifyChip.classList.contains('ok'), false);

  const second = api._verifyRow(row);
  row._override.value = 'single';
  row._override.dispatch('change');
  resolveFetch(jsonResponse({ ok: true, kind: 'playlist', itemCount: 3 }));
  await second;

  assert.equal(row._verifyChip.hidden, true);
  assert.equal(row._verifyChip.classList.contains('ok'), false);
});

test('renderGroups builds chips and group delete updates channel dropdown options', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  api.renderGroups([
    { id: 'sports', name: 'Sports', order: 0 },
    { id: 'news', name: 'News', order: 1 },
  ]);
  api.renderChannels([
    { id: 'daily', name: 'Daily', url: 'https://cdn.example.com/daily.m3u8', kind: '', groupId: 'news', order: 0 },
  ]);

  let chips = Array.prototype.slice.call(h.groupChips.children);
  let row = h.channels.children[0];
  assert.deepEqual(chips.map((chip) => chip.dataset.groupId), ['sports', 'news']);
  assert.deepEqual(Array.prototype.slice.call(row._group.children).map((option) => option.value), ['', 'sports', 'news']);
  assert.equal(row._group.value, 'news');

  chips[1].querySelector('[data-group-delete="news"]').dispatch('click');

  chips = Array.prototype.slice.call(h.groupChips.children);
  row = h.channels.children[0];
  assert.deepEqual(chips.map((chip) => chip.dataset.groupId), ['sports']);
  assert.deepEqual(Array.prototype.slice.call(row._group.children).map((option) => option.value), ['', 'sports']);
  assert.equal(row._group.value, '');
  assert.equal(api.collectChannelModels()[0].groupId, '');
});

test('add channel and add group buttons append rows and chips', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  api.newProvider();

  h.addChannel.dispatch('click');

  let rows = Array.prototype.slice.call(h.channels.children);
  assert.equal(rows.length, 1);
  assert.equal(api.collectChannelModels()[0].id, '');
  assert.equal(api.collectChannelModels()[0].order, 0);

  h.addGroup.dispatch('click');

  const chips = Array.prototype.slice.call(h.groupChips.children);
  rows = Array.prototype.slice.call(h.channels.children);
  assert.equal(chips.length, 1);
  assert.equal(api._state.groups.length, 1);
  assert.deepEqual(Array.prototype.slice.call(rows[0]._group.children).map((option) => option.value), ['', api._state.groups[0].id]);

  h.addChannel.dispatch('click');

  rows = Array.prototype.slice.call(h.channels.children);
  assert.equal(rows.length, 2);
  assert.deepEqual(JSON.parse(JSON.stringify(api.collectChannelModels())).map((channel) => channel.order), [0, 1]);
});

test('_collectPayload returns camelCase provider form payload with current rows', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  api.newProvider();
  h.name.value = 'F1 TV';
  h.glyph.value = 'F1';
  api.selectColor('cyan');
  api.renderGroups([
    { id: 'sports', name: 'Sports', order: 0 },
    { id: 'news', name: 'News', order: 1 },
  ]);
  api.renderChannels([
    { id: 'live', name: 'Live', url: 'https://cdn.example.com/live.m3u8', kind: '', playMode: '', groupId: 'sports', order: 3 },
    { id: '', name: 'Playlist', url: 'https://www.youtube.com/watch?v=abc&list=PL123', kind: 'playlist', playMode: 'first_then_shuffle', groupId: 'news', order: 9 },
  ]);

  const payload = api._collectPayload();

  assert.deepEqual(JSON.parse(JSON.stringify(payload)), {
    displayName: 'F1 TV',
    badgeLabel: 'F1',
    badgeColor: 'cyan',
    groups: [
      { id: 'sports', name: 'Sports', order: 0 },
      { id: 'news', name: 'News', order: 1 },
    ],
    channels: [
      {
        id: 'live',
        name: 'Live',
        url: 'https://cdn.example.com/live.m3u8',
        kind: '',
        playMode: '',
        groupId: 'sports',
        order: 0,
      },
      {
        id: '',
        name: 'Playlist',
        url: 'https://www.youtube.com/watch?v=abc&list=PL123',
        kind: 'playlist',
        playMode: 'first_then_shuffle',
        groupId: 'news',
        order: 1,
      },
    ],
  });
});

test('save create posts JSON and closes the form on ok response', async () => {
  const requests = [];
  const h = createHarness(async (url, init) => {
    requests.push([url, init]);
    return jsonResponse({ ok: true, provider: { id: 'user:f1-tv' } });
  });
  const api = h.context.window.Chassis.providerForm;

  api.newProvider();
  h.name.value = 'F1 TV';
  h.glyph.value = 'F1';
  api.selectColor('teal');
  api.renderChannels([{ id: '', name: 'Live', url: 'https://cdn.example.com/live.m3u8', kind: '', order: 0 }]);

  h.save.dispatch('click');
  await nextTick();

  assert.equal(requests.length, 1);
  assert.equal(requests[0][0], '/ui/catalog/provider');
  assert.equal(requests[0][1].method, 'POST');
  assert.equal(requests[0][1].credentials, 'same-origin');
  assert.equal(requests[0][1].headers['Content-Type'], 'application/json');
  assert.deepEqual(requestBody(requests[0]).channels.map((channel) => channel.id), ['']);
  assert.equal(requestBody(requests[0]).displayName, 'F1 TV');
  assert.equal(h.formPanel.getAttribute('aria-hidden'), 'true');
  assert.equal(h.context.document.body.classList.contains('catalog-form-open'), false);
});

test('form submit also saves the provider', async () => {
  const requests = [];
  const h = createHarness(async (url, init) => {
    requests.push([url, init]);
    return jsonResponse({ ok: true });
  });
  const api = h.context.window.Chassis.providerForm;

  api.newProvider();
  h.name.value = 'Submit Save';
  h.form.dispatch('submit');
  await nextTick();

  assert.equal(requests.length, 1);
  assert.equal(requests[0][0], '/ui/catalog/provider');
  assert.equal(requests[0][1].method, 'POST');
});

test('create auto-enable responses show stream activation notices', async () => {
  const on = createHarness(async () => jsonResponse({ ok: true, autoEnabledStreams: 'on' }));
  on.context.window.Chassis.providerForm.newProvider();
  await on.context.window.Chassis.providerForm._save();
  assert.match(on.notices.map((notice) => notice[0]).join(' '), /turned on|activated/i);

  const restart = createHarness(async () => jsonResponse({ ok: true, autoEnabledStreams: 'restart-required' }));
  restart.context.window.Chassis.providerForm.newProvider();
  await restart.context.window.Chassis.providerForm._save();
  assert.match(restart.notices.map((notice) => notice[0]).join(' '), /restart/i);
});

test('save update uses encoded PUT route and reports cleared preset slots', async () => {
  const requests = [];
  const h = createHarness(async (url, init) => {
    requests.push([url, init]);
    return jsonResponse({ ok: true, clearedSlots: [2, 5] });
  });
  const api = h.context.window.Chassis.providerForm;

  api.populate({ id: 'user:mix/alpha', displayName: 'Mix', badgeLabel: 'MX', badgeColor: 'blue', groups: [], channels: [] });
  await api._save();

  assert.equal(requests.length, 1);
  assert.equal(requests[0][0], '/ui/catalog/provider/user%3Amix%2Falpha');
  assert.equal(requests[0][1].method, 'PUT');
  assert.equal(requests[0][1].headers['Content-Type'], 'application/json');
  assert.match(h.notices.map((notice) => notice[0]).join(' '), /2.*slot|slot.*2/i);
  assert.equal(h.formPanel.getAttribute('aria-hidden'), 'true');
});

test('save failures keep the form open and show error notices', async () => {
  const httpFail = createHarness(async () => jsonResponse({ ok: false, chip: 'BAD INPUT' }, false));
  httpFail.context.window.Chassis.providerForm.newProvider();
  await httpFail.context.window.Chassis.providerForm._save();

  assert.equal(httpFail.formPanel.getAttribute('aria-hidden'), 'false');
  assert.deepEqual(httpFail.notices, [['BAD INPUT', 'err']]);

  const jsonFail = createHarness(async () => jsonResponse({ ok: false, message: 'Name is required.' }, true));
  jsonFail.context.window.Chassis.providerForm.newProvider();
  await jsonFail.context.window.Chassis.providerForm._save();

  assert.equal(jsonFail.formPanel.getAttribute('aria-hidden'), 'false');
  assert.deepEqual(jsonFail.notices, [['Name is required.', 'err']]);
});

test('save ignores duplicate submissions and stale responses after form changes', async () => {
  const requests = [];
  const resolvers = [];
  const h = createHarness(async (url, init) => {
    requests.push([url, init]);
    return new Promise((resolve) => {
      resolvers.push(() => resolve(jsonResponse({ ok: true, autoEnabledStreams: 'on' })));
    });
  });
  const api = h.context.window.Chassis.providerForm;

  api.newProvider();
  h.name.value = 'Slow Save';
  const first = api._save();
  const second = api._save();

  assert.equal(requests.length, 1);
  assert.equal(h.save.disabled, true);
  assert.equal(h.del.disabled, true);

  api.populate({ id: 'user:later', displayName: 'Later', groups: [], channels: [] });
  assert.equal(h.save.disabled, false);
  assert.equal(h.del.disabled, false);
  assert.equal(h.formPanel.getAttribute('aria-hidden'), 'false');
  assert.equal(h.id.value, 'user:later');

  resolvers.forEach((resolve) => resolve());
  assert.equal(await second, false);
  assert.equal(await first, false);

  assert.equal(h.formPanel.getAttribute('aria-hidden'), 'false');
  assert.equal(h.id.value, 'user:later');
  assert.deepEqual(h.notices, []);
});

test('delete for a new provider closes without confirming or posting', async () => {
  const requests = [];
  const h = createHarness(async (url, init) => {
    requests.push([url, init]);
    return jsonResponse({ ok: true });
  });
  const api = h.context.window.Chassis.providerForm;

  api.newProvider();
  await api._delete();

  assert.equal(requests.length, 0);
  assert.equal(h.confirmMessages.length, 0);
  assert.equal(h.formPanel.getAttribute('aria-hidden'), 'true');
});

test('delete ignores duplicate submissions and stale responses after form changes', async () => {
  const requests = [];
  const resolvers = [];
  const h = createHarness(async (url, init) => {
    requests.push([url, init]);
    return new Promise((resolve) => {
      resolvers.push(() => resolve(jsonResponse({ ok: true })));
    });
  });
  const api = h.context.window.Chassis.providerForm;

  api.populate({ id: 'user:old', displayName: 'Old', groups: [], channels: [] });
  const first = api._delete();
  const second = api._delete();

  assert.equal(h.confirmMessages.length, 1);
  assert.equal(requests.length, 1);
  assert.equal(h.save.disabled, true);
  assert.equal(h.del.disabled, true);

  api.populate({ id: 'user:later', displayName: 'Later', groups: [], channels: [] });
  assert.equal(h.save.disabled, false);
  assert.equal(h.del.disabled, false);
  assert.equal(h.formPanel.getAttribute('aria-hidden'), 'false');
  assert.equal(h.id.value, 'user:later');

  resolvers.forEach((resolve) => resolve());
  assert.equal(await second, false);
  assert.equal(await first, false);

  assert.equal(h.formPanel.getAttribute('aria-hidden'), 'false');
  assert.equal(h.id.value, 'user:later');
  assert.deepEqual(h.notices, []);
});

test('delete existing provider confirms, sends DELETE, closes, and reports cleared slots', async () => {
  const requests = [];
  const h = createHarness(async (url, init) => {
    requests.push([url, init]);
    return jsonResponse({ ok: true, clearedSlots: [3] });
  });
  const api = h.context.window.Chassis.providerForm;

  api.populate({ id: 'user:mix/alpha', displayName: 'Mix', groups: [], channels: [] });
  h.del.dispatch('click');
  await nextTick();

  assert.equal(h.confirmMessages.length, 1);
  assert.equal(requests.length, 1);
  assert.equal(requests[0][0], '/ui/catalog/provider/user%3Amix%2Falpha');
  assert.equal(requests[0][1].method, 'DELETE');
  assert.equal(h.formPanel.getAttribute('aria-hidden'), 'true');
  assert.match(h.notices.map((notice) => notice[0]).join(' '), /1.*slot|slot.*1/i);
});

test('delete confirmation warns about starred preset cleanup only for current provider starred channels', async () => {
  const starred = createHarness(async () => jsonResponse({ ok: true }));
  const starredAPI = starred.context.window.Chassis.providerForm;
  starred.emitEvent('presets', { slots: [{ slot: 1, provider: 'user:mix', channel: 'live' }] });
  starredAPI.populate({
    id: 'user:mix',
    displayName: 'Mix',
    groups: [],
    channels: [{ id: 'live', name: 'Live', url: 'https://cdn.example.com/live.m3u8', order: 0 }],
  });

  assert.equal(starredAPI._anyStarredChannel(), true);
  await starredAPI._delete();
  assert.match(starred.confirmMessages[0], /starred|preset/i);

  const removed = createHarness(async () => jsonResponse({ ok: true }));
  const removedAPI = removed.context.window.Chassis.providerForm;
  removed.emitEvent('presets', { slots: [{ slot: 1, provider: 'user:mix', channel: 'removed' }] });
  removedAPI.populate({
    id: 'user:mix',
    displayName: 'Mix',
    groups: [],
    channels: [{ id: 'live', name: 'Live', url: 'https://cdn.example.com/live.m3u8', order: 0 }],
  });

  assert.equal(removedAPI._anyStarredChannel(), true);
  await removedAPI._delete();
  assert.match(removed.confirmMessages[0], /starred|preset/i);

  const plain = createHarness(async () => jsonResponse({ ok: true }));
  const plainAPI = plain.context.window.Chassis.providerForm;
  plain.emitEvent('presets', { slots: [{ slot: 1, provider: 'user:other', channel: 'live' }] });
  plainAPI.populate({
    id: 'user:mix',
    displayName: 'Mix',
    groups: [],
    channels: [{ id: 'live', name: 'Live', url: 'https://cdn.example.com/live.m3u8', order: 0 }],
  });

  assert.equal(plainAPI._anyStarredChannel(), false);
  await plainAPI._delete();
  assert.doesNotMatch(plain.confirmMessages[0], /starred|preset/i);
});

test('_reorderListMove moves C before A and channel collection follows DOM order', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  api.renderChannels([
    { id: 'a', name: 'A', url: 'https://cdn.example.com/a.m3u8', order: 0 },
    { id: 'b', name: 'B', url: 'https://cdn.example.com/b.m3u8', order: 1 },
    { id: 'c', name: 'C', url: 'https://cdn.example.com/c.m3u8', order: 2 },
  ]);

  const [a, , c] = Array.prototype.slice.call(h.channels.children);
  api._reorderListMove(h.channels, c, a);

  assert.deepEqual(Array.prototype.slice.call(h.channels.children).map((row) => row.dataset.channelId), ['c', 'a', 'b']);
  assert.deepEqual(JSON.parse(JSON.stringify(api.collectChannelModels().map((channel) => ({ id: channel.id, order: channel.order })))), [
    { id: 'c', order: 0 },
    { id: 'a', order: 1 },
    { id: 'b', order: 2 },
  ]);
});

test('_reorderListMove moves A after C when dragging downward', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  api.renderChannels([
    { id: 'a', name: 'A', url: 'https://cdn.example.com/a.m3u8', order: 0 },
    { id: 'b', name: 'B', url: 'https://cdn.example.com/b.m3u8', order: 1 },
    { id: 'c', name: 'C', url: 'https://cdn.example.com/c.m3u8', order: 2 },
  ]);

  const [a, , c] = Array.prototype.slice.call(h.channels.children);
  api._reorderListMove(h.channels, a, c);

  assert.deepEqual(Array.prototype.slice.call(h.channels.children).map((row) => row.dataset.channelId), ['b', 'c', 'a']);
});

test('group reorder syncs state from chip order', () => {
  const h = createHarness();
  const api = h.context.window.Chassis.providerForm;

  api.renderGroups([
    { id: 'a', name: 'A', order: 0 },
    { id: 'b', name: 'B', order: 1 },
    { id: 'c', name: 'C', order: 2 },
  ]);

  const [a, , c] = Array.prototype.slice.call(h.groupChips.children);
  api._reorderListMove(h.groupChips, c, a);
  api._syncGroupsFromHost(h.groupChips);

  assert.deepEqual(Array.prototype.slice.call(h.groupChips.children).map((chip) => chip.dataset.groupId), ['c', 'a', 'b']);
  assert.deepEqual(JSON.parse(JSON.stringify(api._currentGroups().map((group) => ({ id: group.id, order: group.order })))), [
    { id: 'c', order: 0 },
    { id: 'a', order: 1 },
    { id: 'b', order: 2 },
  ]);
});

test('provider form wires channel and group containers to Chassis reorder', () => {
  const h = createHarness(null, { fakeReorder: true });

  assert.equal(h.reorderCalls.length, 2);
  const channelCall = sortableCall(h, '.cf-channel');
  const groupCall = sortableCall(h, '.cf-group-chip');
  assert.equal(channelCall.container, h.channels);
  assert.equal(groupCall.container, h.groupChips);
  assert.equal(typeof channelCall.onDrop, 'function');
  assert.equal(typeof groupCall.onDrop, 'function');
});

test('sortable pointerdown ignores row form controls but allows row/chip drags', () => {
  let starts = 0;
  const h = createHarness(null, {
    reorder: {
      makeSortable(config) {
        config.container.addEventListener('pointerdown', () => {
          starts += 1;
        });
        return { cancel() {} };
      },
    },
  });
  const api = h.context.window.Chassis.providerForm;

  api.renderGroups([{ id: 'news', name: 'News', order: 0 }]);
  api.renderChannels([{ id: 'a', name: 'A', url: 'https://cdn.example.com/a.m3u8', groupId: 'news', order: 0 }]);

  const row = h.channels.children[0];
  row._name.dispatch('pointerdown');
  row._url.dispatch('pointerdown');
  row._override.dispatch('pointerdown');
  row._play.dispatch('pointerdown');
  row._group.dispatch('pointerdown');
  row._verify.dispatch('pointerdown');
  row.querySelector('.cf-channel-delete').dispatch('pointerdown');
  h.groupChips.children[0].querySelector('.cf-group-delete').dispatch('pointerdown');

  assert.equal(starts, 0);

  row.dispatch('pointerdown');
  h.groupChips.children[0].dispatch('pointerdown');

  assert.equal(starts, 2);
});

test('dropping channel rows for existing providers posts reorder JSON and new providers skip POST', async () => {
  const requests = [];
  const h = createHarness(async (url, init) => {
    requests.push([url, init]);
    return jsonResponse({ ok: true });
  }, { fakeReorder: true });
  const api = h.context.window.Chassis.providerForm;

  api.populate({
    id: 'user:mix/alpha',
    displayName: 'Mix',
    groups: [
      { id: 'sports', name: 'Sports', order: 0 },
      { id: 'news', name: 'News', order: 1 },
    ],
    channels: [
      { id: 'a', name: 'A', url: 'https://cdn.example.com/a.m3u8', groupId: 'sports', order: 0 },
      { id: 'b', name: 'B', url: 'https://cdn.example.com/b.m3u8', groupId: 'news', order: 1 },
      { id: 'c', name: 'C', url: 'https://cdn.example.com/c.m3u8', groupId: '', order: 2 },
    ],
  });

  const [a, , c] = Array.prototype.slice.call(h.channels.children);
  sortableCall(h, '.cf-channel').onDrop(a, c);
  await nextTick();

  assert.equal(requests.length, 1);
  assert.equal(requests[0][0], '/ui/catalog/provider/user%3Amix%2Falpha/reorder');
  assert.equal(requests[0][1].method, 'POST');
  assert.equal(requests[0][1].credentials, 'same-origin');
  assert.equal(requests[0][1].headers['Content-Type'], 'application/json');
  assert.deepEqual(JSON.parse(requests[0][1].body), {
    channels: [
      { id: 'b', order: 0 },
      { id: 'c', order: 1 },
      { id: 'a', order: 2 },
    ],
    groups: [
      { id: 'sports', order: 0 },
      { id: 'news', order: 1 },
    ],
  });

  const newRequests = [];
  const next = createHarness(async (url, init) => {
    newRequests.push([url, init]);
    return jsonResponse({ ok: true });
  }, { fakeReorder: true });
  const nextAPI = next.context.window.Chassis.providerForm;
  nextAPI.newProvider();
  nextAPI.renderChannels([
    { id: '', name: 'Unsaved A', url: 'https://cdn.example.com/a.m3u8', order: 0 },
    { id: '', name: 'Unsaved B', url: 'https://cdn.example.com/b.m3u8', order: 1 },
  ]);

  const [unsavedA, unsavedB] = Array.prototype.slice.call(next.channels.children);
  sortableCall(next, '.cf-channel').onDrop(unsavedA, unsavedB);
  await nextTick();

  assert.equal(newRequests.length, 0);
  assert.deepEqual(JSON.parse(JSON.stringify(nextAPI.collectChannelModels().map((channel) => channel.name))), ['Unsaved B', 'Unsaved A']);
});

test('failed reorder responses show a notice and roll back channel order', async () => {
  const h = createHarness(async () => jsonResponse({ ok: false, message: 'REJECTED' }, false), { fakeReorder: true });
  const api = h.context.window.Chassis.providerForm;

  api.populate({
    id: 'user:mix',
    displayName: 'Mix',
    groups: [],
    channels: [
      { id: 'a', name: 'A', url: 'https://cdn.example.com/a.m3u8', order: 0 },
      { id: 'b', name: 'B', url: 'https://cdn.example.com/b.m3u8', order: 1 },
      { id: 'c', name: 'C', url: 'https://cdn.example.com/c.m3u8', order: 2 },
    ],
  });

  const [a, , c] = Array.prototype.slice.call(h.channels.children);
  await sortableCall(h, '.cf-channel').onDrop(a, c);

  assert.deepEqual(Array.prototype.slice.call(h.channels.children).map((row) => row.dataset.channelId), ['a', 'b', 'c']);
  assert.deepEqual(h.notices, [['REJECTED', 'err']]);
});

test('group drag reorder preserves unsaved channel values and selections while refreshing dropdown order', async () => {
  const requests = [];
  const h = createHarness(async (url, init) => {
    requests.push([url, init]);
    return jsonResponse({ ok: true });
  }, { fakeReorder: true });
  const api = h.context.window.Chassis.providerForm;

  api.populate({
    id: 'user:abc',
    displayName: 'Mix',
    groups: [
      { id: 'sports', name: 'Sports', order: 0 },
      { id: 'news', name: 'News', order: 1 },
    ],
    channels: [
      {
        id: 'live',
        name: 'Live',
        url: 'https://cdn.example.com/live.m3u8',
        kind: '',
        playMode: '',
        groupId: 'sports',
        order: 0,
      },
    ],
  });

  const row = h.channels.children[0];
  row._name.value = 'Unsaved live';
  row._url.value = 'https://www.youtube.com/watch?v=abc&list=PL123';
  row._override.value = 'playlist';
  row._play.value = 'first_then_shuffle';
  row._group.value = 'news';
  row._verifyChip.hidden = false;
  row._verifyChip.classList.add('ok');
  row._verifyChip.textContent = '✓ DIRECT';

  const [sports, news] = Array.prototype.slice.call(h.groupChips.children);
  await sortableCall(h, '.cf-group-chip').onDrop(news, sports);

  const nextRow = h.channels.children[0];
  assert.equal(nextRow, row);
  assert.deepEqual(JSON.parse(JSON.stringify(api._currentGroups().map((group) => ({ id: group.id, order: group.order })))), [
    { id: 'news', order: 0 },
    { id: 'sports', order: 1 },
  ]);
  assert.deepEqual(Array.prototype.slice.call(nextRow._group.children).map((option) => option.value), ['', 'news', 'sports']);
  assert.equal(nextRow._name.value, 'Unsaved live');
  assert.equal(nextRow._url.value, 'https://www.youtube.com/watch?v=abc&list=PL123');
  assert.equal(nextRow._override.value, 'playlist');
  assert.equal(nextRow._play.value, 'first_then_shuffle');
  assert.equal(nextRow._group.value, 'news');
  assert.equal(nextRow._verifyChip.hidden, false);
  assert.equal(nextRow._verifyChip.classList.contains('ok'), true);
  assert.equal(nextRow._verifyChip.textContent, '✓ DIRECT');
  assert.deepEqual(JSON.parse(requests[0][1].body), {
    channels: [{ id: 'live', order: 0 }],
    groups: [
      { id: 'news', order: 0 },
      { id: 'sports', order: 1 },
    ],
  });
});
