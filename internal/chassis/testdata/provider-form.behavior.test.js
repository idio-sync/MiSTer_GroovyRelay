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
    child.parentElement = this;
    this.children.push(child);
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
    const evt = { type, target: event.target || this, currentTarget: this, preventDefault() {}, ...event };
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

function createHarness(fetchImpl) {
  const body = new FakeElement('body');
  const drawer = new FakeElement('div', { id: 'catalog-drawer' });
  const formPanel = new FakeElement('div', { id: 'catalog-form' });
  formPanel.setAttribute('aria-hidden', 'true');
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
  const cancel = new FakeElement('button', { id: 'cf-cancel' });
  const newButton = new FakeElement('button', { id: 'catalog-provider-new' });
  const pencil = new FakeElement('button', { dataset: { editProvider: 'user:abc' } });
  const swatchButtons = ['amber', 'red', 'teal', 'blue', 'purple', 'green', 'cyan', 'slate'].map((color) => {
    const button = new FakeElement('button', { className: 'cf-swatch', dataset: { color } });
    button.setAttribute('aria-checked', 'false');
    swatches.appendChild(button);
    return button;
  });

  formPanel.appendChild(id);
  formPanel.appendChild(name);
  formPanel.appendChild(glyph);
  formPanel.appendChild(swatches);
  groups.appendChild(groupChips);
  groups.appendChild(addGroup);
  formPanel.appendChild(groups);
  formPanel.appendChild(channels);
  formPanel.appendChild(addChannel);
  formPanel.appendChild(del);
  formPanel.appendChild(cancel);
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
  const context = {
    document,
    window: {
      Chassis: {
        settings: {
          showNotice(text, variant) {
            notices.push([text, variant]);
          },
        },
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
  return { context, drawer, formPanel, id, name, glyph, swatches, swatchButtons, groups, groupChips, addGroup, channels, addChannel, del, cancel, newButton, pencil, notices };
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
