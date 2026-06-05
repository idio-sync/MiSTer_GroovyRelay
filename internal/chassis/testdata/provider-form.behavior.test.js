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
    const evt = { type, target: this, preventDefault() {}, ...event };
    for (const fn of this.listeners.get(type) || []) fn(evt);
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
  if (!attr.startsWith('data-')) return node.attributes.get(attr) || null;
  const key = attr.slice(5).replace(/-([a-z])/g, (_, ch) => ch.toUpperCase());
  return node.dataset[key] || null;
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
  const channels = new FakeElement('div', { id: 'cf-channels' });
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
  formPanel.appendChild(groups);
  formPanel.appendChild(channels);
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
    ['cf-channels', channels],
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
  return { context, drawer, formPanel, id, name, glyph, swatches, swatchButtons, del, cancel, newButton, pencil, notices };
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
