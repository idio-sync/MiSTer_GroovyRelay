// settings-link.behavior.test.js — Task 16
// Tests renderLinkView safe-repaint guarantees:
//   - untrusted strings go through textContent, never innerHTML injection
//   - credential inputs use data-link-field, not data-adapter/data-field
//   - password inputs are cleared (rebuilt empty) on every repaint
//
// Runner: node --test internal/chassis/testdata/settings-link.behavior.test.js

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// ─── Minimal fake DOM ──────────────────────────────────────────────────────
// Supports: createElement, createTextNode, appendChild, replaceChildren,
// setAttribute/getAttribute/removeAttribute, textContent (r/w), className,
// innerHTML (read — built by serialising the tree so <img tests work),
// querySelector / querySelectorAll (attribute + class selectors), closest.

class FakeNode {
  constructor(nodeType) {
    this._nodeType = nodeType; // 1=element, 3=text
    this._parent = null;
    this._children = [];
  }

  appendChild(child) {
    child._parent = this;
    this._children.push(child);
    if (this._tag === 'select' && !this.value && child.value) this.value = child.value;
    return child;
  }

  replaceChildren(...nodes) {
    this._children = [];
    if (this._tag === 'select') this.value = '';
    for (const n of nodes) this.appendChild(n);
  }

  append(...nodes) {
    for (const n of nodes) this.appendChild(n);
  }

  insertBefore(child, before) {
    child._parent = this;
    const idx = this._children.indexOf(before);
    if (idx < 0) this._children.push(child);
    else this._children.splice(idx, 0, child);
    return child;
  }
}

class FakeTextNode extends FakeNode {
  constructor(text) {
    super(3);
    this._text = String(text);
  }

  get textContent() { return this._text; }
  set textContent(v) { this._text = String(v); }
}

class FakeElement extends FakeNode {
  constructor(tag) {
    super(1);
    this._tag = (tag || 'div').toLowerCase();
    this._attrs = new Map();
    this._className = '';
    this._type = 'text'; // mirrors inp.type
    this.value = '';
    this.disabled = false;
    this.hidden = false;
    this.classList = {
      add: (cls) => this._setClass(cls, true),
      remove: (cls) => this._setClass(cls, false),
      contains: (cls) => this._className.split(/\s+/).includes(cls),
      toggle: (cls, on) => this._setClass(cls, on !== undefined ? on : !this.classList.contains(cls)),
    };
  }

  get tagName() { return this._tag.toUpperCase(); }
  get parentElement() { return this._parent && this._parent._nodeType === 1 ? this._parent : null; }

  // ── className (syncs to data-class attribute for serialisation) ────────
  get className() { return this._className; }
  set className(v) { this._className = v || ''; }

  _setClass(cls, on) {
    const parts = new Set(this._className.split(/\s+/).filter(Boolean));
    if (on) parts.add(cls);
    else parts.delete(cls);
    this._className = Array.from(parts).join(' ');
  }

  // ── type (for <input>) ─────────────────────────────────────────────────
  get type() { return this._type; }
  set type(v) { this._type = v; this._attrs.set('type', v); }

  // ── attributes ─────────────────────────────────────────────────────────
  setAttribute(name, value) { this._attrs.set(name, String(value)); }
  getAttribute(name) { return this._attrs.has(name) ? this._attrs.get(name) : null; }
  removeAttribute(name) { this._attrs.delete(name); }
  hasAttribute(name) { return this._attrs.has(name); }

  // ── textContent (set: replaces all children with one text node) ────────
  get textContent() {
    return this._children.map((c) => c.textContent).join('');
  }
  set textContent(v) {
    this._children = [];
    if (v != null && v !== '') {
      const t = new FakeTextNode(v);
      t._parent = this;
      this._children.push(t);
    }
  }

  // ── innerHTML — serialises the tree so test can assert absence of <img ─
  get innerHTML() {
    return this._children.map((c) => serializeNode(c)).join('');
  }

  // ── querySelector / querySelectorAll ───────────────────────────────────
  querySelector(selector) {
    return walkFind(this, selector, false);
  }

  querySelectorAll(selector) {
    const results = [];
    walkFind(this, selector, true, results);
    return results;
  }

  // ── closest ────────────────────────────────────────────────────────────
  closest(selector) {
    // eslint-disable-next-line consistent-this
    let node = this;
    while (node && node._nodeType === 1) {
      if (matchesSelector(node, selector)) return node;
      node = node._parent;
    }
    return null;
  }

  // ── stub event hooks (unused in these tests but prevent crashes) ───────
  addEventListener() {}

  contains(node) {
    let cur = node;
    while (cur) {
      if (cur === this) return true;
      cur = cur._parent;
    }
    return false;
  }

  remove() {
    if (!this._parent) return;
    this._parent._children = this._parent._children.filter((child) => child !== this);
    this._parent = null;
  }
}

// Serialise a node back to HTML markup so innerHTML can be checked.
function serializeNode(node) {
  if (node._nodeType === 3) {
    // Text node — escape critical HTML entities to match browser behaviour.
    return node._text
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');
  }
  const tag = node._tag;
  let open = `<${tag}`;
  for (const [k, v] of node._attrs) {
    open += ` ${k}="${v.replace(/"/g, '&quot;')}"`;
  }
  if (node._className) open += ` class="${node._className}"`;
  open += '>';
  const inner = node._children.map(serializeNode).join('');
  return `${open}${inner}</${tag}>`;
}

// Parse simple selectors used in this test:
//   [attr]          → element has attr
//   [attr="val"]    → element has attr with exact value
//   .cls            → element className contains cls
//   tag             → element tag matches
//   tag.cls         → both
// Compound (space-separated) not needed here.
function matchesSelector(node, selector) {
  if (node._nodeType !== 1) return false;
  // Attribute selector — possibly prefixed with a tag or class.
  const attrMatch = selector.match(/\[([^\]="]+)(?:="([^"]*)")?\]/);
  if (attrMatch) {
    const attrName = attrMatch[1];
    const attrVal = attrMatch[2]; // may be undefined
    if (!node.hasAttribute(attrName)) return false;
    if (attrVal !== undefined && node.getAttribute(attrName) !== attrVal) return false;
    return true;
  }
  // Class selector .foo
  if (selector.startsWith('.')) {
    const cls = selector.slice(1);
    return node._className.split(/\s+/).includes(cls);
  }
  // Tag selector
  return node._tag === selector.toLowerCase();
}

function walkFind(root, selector, all, acc) {
  for (const child of root._children) {
    if (child._nodeType !== 1) continue;
    if (matchesSelector(child, selector)) {
      if (!all) return child;
      acc.push(child);
    }
    const found = walkFind(child, selector, all, acc);
    if (!all && found) return found;
  }
  return null;
}

// ─── Harness ───────────────────────────────────────────────────────────────
// Stubs the minimum surface the settings-drawer.js IIFE touches at init time
// so the module loads without crashing. Actual tests operate on
// window.Chassis.settings.renderLinkView after load.

function createHarness() {
  const noopEl = new FakeElement('div');
  noopEl.querySelectorAll = () => [];
  noopEl.querySelector = () => null;

  const drawerEl = new FakeElement('div');
  drawerEl.querySelectorAll = () => [];
  drawerEl.querySelector = () => null;

  const bodyEl = new FakeElement('body');
  bodyEl.classList = {
    toggle() {},
    remove() {},
    add() {},
    contains() { return false; },
  };

  const clickHandlers = [];
  const submitHandlers = [];
  const blurHandlers = [];

  const document = {
    body: bodyEl,
    querySelector(sel) {
      if (sel === '.settings-panel') return drawerEl;
      return null;
    },
    querySelectorAll() { return []; },
    getElementById() { return null; },
    addEventListener(type, fn) {
      if (type === 'click') clickHandlers.push(fn);
      else if (type === 'submit') submitHandlers.push(fn);
      else if (type === 'blur') blurHandlers.push(fn);
    },
    createElement(tag) { return new FakeElement(tag); },
    createTextNode(text) { return new FakeTextNode(text); },
  };

  const window = {
    Chassis: { settings: {} },
  };

  const context = {
    document,
    window,
    fetch: async () => ({ json: async () => ({}) }),
    URLSearchParams,
    setTimeout() {},
    clearTimeout() {},
    console: { warn() {} },
  };
  vm.createContext(context);
  const code = fs.readFileSync(
    path.join(__dirname, '..', 'static', 'settings-drawer.js'),
    'utf8'
  );
  vm.runInContext(code, context, { filename: 'settings-drawer.js' });

  return { window: context.window, document, clickHandlers, submitHandlers, context };
}

function createLocalFilesHarness() {
  const drawerEl = new FakeElement('div');
  drawerEl.className = 'settings-panel';
  const bodyEl = new FakeElement('body');
  const localSection = new FakeElement('section');
  localSection.setAttribute('data-adapter-section', 'localfiles');

  const list = new FakeElement('div');
  list.setAttribute('data-localfiles-library-list', '');
  const row = new FakeElement('div');
  row.setAttribute('data-localfiles-library-row', '');
  const name = new FakeElement('input');
  name.setAttribute('data-localfiles-library-name', '');
  name.value = 'Array';
  const root = new FakeElement('input');
  root.setAttribute('data-localfiles-library-root', '');
  root.value = '/array';
  row.append(name, root);
  const add = new FakeElement('button');
  add.setAttribute('data-localfiles-add-library', '');
  list.append(row, add);

  const select = new FakeElement('select');
  select.setAttribute('data-localfiles-browse-lib', '');
  const open = new FakeElement('button');
  open.setAttribute('data-localfiles-open-browser', '');
  const libraryErr = new FakeElement('div');
  libraryErr.setAttribute('data-localfiles-library-err', '');
  const browseErr = new FakeElement('div');
  browseErr.setAttribute('data-localfiles-browse-err', '');
  const modal = new FakeElement('div');
  modal.setAttribute('data-localfiles-browse-modal', '');
  const crumb = new FakeElement('div');
  crumb.setAttribute('data-localfiles-breadcrumb', '');
  const entries = new FakeElement('div');
  entries.setAttribute('data-localfiles-entries', '');
  modal.append(crumb, entries);
  localSection.append(list, select, open, libraryErr, browseErr, modal);

  const clickHandlers = [];
  const requests = [];
  const document = {
    body: bodyEl,
    querySelector(sel) {
      if (sel === '.settings-panel') return drawerEl;
      if (sel === '[data-adapter-section="localfiles"]') return localSection;
      return null;
    },
    querySelectorAll() { return []; },
    getElementById() { return null; },
    addEventListener(type, fn) {
      if (type === 'click') clickHandlers.push(fn);
    },
    createElement(tag) { return new FakeElement(tag); },
    createTextNode(text) { return new FakeTextNode(text); },
  };

  const context = {
    document,
    window: { Chassis: { settings: {} } },
    fetch: async (url, options) => {
      requests.push({ url, body: String(options.body) });
      if (url === '/ui/settings/adapter/localfiles/libraries') {
        return { json: async () => ({ ok: true, libraries: [{ name: 'Array', root: '/array' }] }) };
      }
      if (url === '/ui/settings/adapter/localfiles/browse') {
        return { json: async () => ({ ok: true, entries: [] }) };
      }
      return { json: async () => ({ ok: true }) };
    },
    URLSearchParams,
    setTimeout() {},
    clearTimeout() {},
    console: { warn() {} },
  };
  vm.createContext(context);
  const code = fs.readFileSync(
    path.join(__dirname, '..', 'static', 'settings-drawer.js'),
    'utf8'
  );
  vm.runInContext(code, context, { filename: 'settings-drawer.js' });

  async function click(target) {
    const ev = { target, preventDefault() {} };
    for (const handler of clickHandlers) await handler(ev);
  }

  return {
    click,
    open,
    requests,
    select,
    modal,
    clickHandlerCount: clickHandlers.length,
  };
}

async function settle() {
  for (let i = 0; i < 4; i += 1) await Promise.resolve();
}

// ─── Tests ─────────────────────────────────────────────────────────────────

test('renderLinkView escapes error text via textContent', () => {
  const h = createHarness();
  const renderLinkView = h.window.Chassis.settings.renderLinkView;
  assert.equal(typeof renderLinkView, 'function', 'renderLinkView must be exported');

  const container = h.document.createElement('div');
  container.className = 'settings-link';
  renderLinkView(container, {
    kind: 'credential',
    phase: 'error',
    error: '<img src=x onerror=alert(1)>',
    fields: [{ key: 'username', label: 'Username', kind: 'text' }],
  });

  const html = container.innerHTML;
  assert.equal(
    html.includes('<img'),
    false,
    `error text must not be injected as HTML; got innerHTML: ${html}`
  );
  assert.ok(
    container.textContent.includes('<img src=x'),
    `error text must be present as plain text; textContent: ${container.textContent}`
  );
});

test('credential inputs avoid 4D autosave selectors', () => {
  const h = createHarness();
  const renderLinkView = h.window.Chassis.settings.renderLinkView;
  assert.equal(typeof renderLinkView, 'function', 'renderLinkView must be exported');

  const container = h.document.createElement('div');
  container.className = 'settings-link';
  renderLinkView(container, {
    kind: 'credential',
    phase: 'unlinked',
    fields: [
      { key: 'username', label: 'Username', kind: 'text' },
      { key: 'password', label: 'Password', kind: 'secret' },
    ],
  });

  assert.equal(
    container.querySelector('[data-adapter]'),
    null,
    'no data-adapter on link inputs'
  );
  assert.equal(
    container.querySelector('[data-field]'),
    null,
    'no data-field on link inputs'
  );
  assert.notEqual(
    container.querySelector('[data-link-field="username"]'),
    null,
    'must use data-link-field="username"'
  );
});

test('password input type is password for secret fields', () => {
  const h = createHarness();
  const renderLinkView = h.window.Chassis.settings.renderLinkView;

  const container = h.document.createElement('div');
  container.className = 'settings-link';
  renderLinkView(container, {
    kind: 'credential',
    phase: 'unlinked',
    fields: [
      { key: 'username', label: 'Username', kind: 'text' },
      { key: 'password', label: 'Password', kind: 'secret' },
    ],
  });

  const pwInput = container.querySelector('[data-link-field="password"]');
  assert.notEqual(pwInput, null, 'password input must exist');
  assert.equal(pwInput.getAttribute('type'), 'password', 'secret field must use type=password');

  const userInput = container.querySelector('[data-link-field="username"]');
  assert.notEqual(userInput, null, 'username input must exist');
  assert.equal(userInput.getAttribute('type'), 'text', 'text field must use type=text');
});

test('credential error view renders a retryable form (I-2 recovery)', () => {
  // When a credential link fails (network error / chip / {ok:false}), the submit
  // handler re-renders the form via renderLinkView({phase:'error', fields}) so the
  // operator can retry — it must NOT leave the form stuck on "Linking…".
  const h = createHarness();
  const renderLinkView = h.window.Chassis.settings.renderLinkView;

  const container = h.document.createElement('div');
  container.className = 'settings-link';
  renderLinkView(container, {
    kind: 'credential',
    phase: 'error',
    error: 'Network error',
    fields: [
      { key: 'username', label: 'Username', kind: 'text' },
      { key: 'password', label: 'Password', kind: 'secret' },
    ],
  });

  assert.notEqual(container.querySelector('[data-link-submit]'), null, 'submit button restored for retry');
  assert.notEqual(container.querySelector('[data-link-field="username"]'), null, 'username input restored');
  assert.notEqual(container.querySelector('[data-link-field="password"]'), null, 'password input restored');
  assert.ok(container.textContent.includes('Network error'), 'error message shown to operator');
});

test('renderLinkView clears inputs on repaint (password not retained)', () => {
  const h = createHarness();
  const renderLinkView = h.window.Chassis.settings.renderLinkView;

  const container = h.document.createElement('div');
  container.className = 'settings-link';

  // First render — simulate user having typed a password.
  renderLinkView(container, {
    kind: 'credential',
    phase: 'unlinked',
    fields: [{ key: 'password', label: 'Password', kind: 'secret' }],
  });
  const firstInput = container.querySelector('[data-link-field="password"]');
  assert.notEqual(firstInput, null);
  firstInput.value = 'hunter2'; // simulate typed value

  // Second render — password must be cleared (new input element).
  renderLinkView(container, {
    kind: 'credential',
    phase: 'unlinked',
    fields: [{ key: 'password', label: 'Password', kind: 'secret' }],
  });
  const secondInput = container.querySelector('[data-link-field="password"]');
  assert.notEqual(secondInput, null);
  assert.equal(secondInput.value, '', 'password must be empty after repaint');
});

test('linked phase renders unlink button not a form', () => {
  const h = createHarness();
  const renderLinkView = h.window.Chassis.settings.renderLinkView;

  const container = h.document.createElement('div');
  container.className = 'settings-link';
  renderLinkView(container, { kind: 'pin', phase: 'linked', linkedAs: null });

  assert.equal(container.querySelector('[data-link-action="unlink"]') !== null, true, 'must have unlink button');
  assert.equal(container.querySelector('form'), null, 'must not have a form');
  assert.equal(container.getAttribute('data-link-phase'), 'linked', 'phase attr set');
});

test('pin unlinked phase renders start button not a form', () => {
  const h = createHarness();
  const renderLinkView = h.window.Chassis.settings.renderLinkView;

  const container = h.document.createElement('div');
  container.className = 'settings-link';
  renderLinkView(container, { kind: 'pin', phase: 'unlinked' });

  assert.equal(container.querySelector('[data-link-action="start"]') !== null, true, 'must have start button');
  assert.equal(container.querySelector('form'), null, 'must not have a form in pin unlinked');
});

// ─── Task 17: poll controller ─────────────────────────────────────────────
// Uses __pollTick / __pollActive test hooks for deterministic control —
// no real setTimeout fires in tests.

test('poll controller single-flight + stop on terminal', async () => {
  const h = createHarness();
  const { startPoll, __pollTick, __pollActive } = h.window.Chassis.settings;

  assert.equal(typeof startPoll, 'function', 'startPoll must be exported');
  assert.equal(typeof __pollTick, 'function', '__pollTick must be exported');
  assert.equal(typeof __pollActive, 'function', '__pollActive must be exported');

  let calls = 0;
  // Override fetch in the sandbox context so pollOnce sees it.
  h.context.fetch = async () => {
    calls++;
    const phase = calls >= 2 ? 'linked' : 'pending';
    return { json: async () => ({ ok: true, view: { kind: 'pin', phase, code: 'K3F9', expiresInSec: 100 } }) };
  };

  const container = h.document.createElement('div');
  container.className = 'settings-link';

  startPoll('plex', container);
  startPoll('plex', container); // second call must be a no-op (single-flight)

  assert.equal(__pollActive('plex'), true, 'poller should be active after startPoll');

  await __pollTick('plex'); // first tick → pending (calls=1)
  assert.equal(container.getAttribute('data-link-phase'), 'pending', 'should be pending after first tick');
  assert.equal(__pollActive('plex'), true, 'still active after pending tick');

  await __pollTick('plex'); // second tick → linked (calls=2), stops
  assert.equal(container.getAttribute('data-link-phase'), 'linked', 'should reach linked');
  assert.equal(__pollActive('plex'), false, 'poller stopped on terminal phase');
});

test('poll controller stops on error phase', async () => {
  const h = createHarness();
  const { startPoll, __pollTick, __pollActive } = h.window.Chassis.settings;

  h.context.fetch = async () => ({
    json: async () => ({ ok: true, view: { kind: 'pin', phase: 'error', expiresInSec: 0 } }),
  });

  const container = h.document.createElement('div');
  container.className = 'settings-link';

  startPoll('plex', container);
  await __pollTick('plex');
  assert.equal(__pollActive('plex'), false, 'poller must stop on error phase');
});

test('poll controller stops on expiry (expiresInSec <= 0)', async () => {
  const h = createHarness();
  const { startPoll, __pollTick, __pollActive } = h.window.Chassis.settings;

  h.context.fetch = async () => ({
    json: async () => ({ ok: true, view: { kind: 'pin', phase: 'pending', expiresInSec: 0 } }),
  });

  const container = h.document.createElement('div');
  container.className = 'settings-link';

  startPoll('plex', container);
  await __pollTick('plex');
  assert.equal(__pollActive('plex'), false, 'poller must stop when expiresInSec reaches 0');
});

test('stopPoll cancels an active poller', () => {
  const h = createHarness();
  const { startPoll, stopPoll, __pollActive } = h.window.Chassis.settings;

  const container = h.document.createElement('div');
  container.className = 'settings-link';

  startPoll('plex', container);
  assert.equal(__pollActive('plex'), true, 'should be active');
  stopPoll('plex');
  assert.equal(__pollActive('plex'), false, 'should be stopped after stopPoll');
});

test('stopAllPolls cancels all active pollers', () => {
  const h = createHarness();
  const { startPoll, stopAllPolls, __pollActive } = h.window.Chassis.settings;

  const c1 = h.document.createElement('div');
  c1.className = 'settings-link';
  const c2 = h.document.createElement('div');
  c2.className = 'settings-link';

  startPoll('plex', c1);
  startPoll('jellyfin', c2);
  assert.equal(__pollActive('plex'), true, 'plex active');
  assert.equal(__pollActive('jellyfin'), true, 'jellyfin active');
  stopAllPolls();
  assert.equal(__pollActive('plex'), false, 'plex stopped');
  assert.equal(__pollActive('jellyfin'), false, 'jellyfin stopped');
});

test('local files Open saves current libraries before browsing', async () => {
  const h = createLocalFilesHarness();
  assert.ok(h.clickHandlerCount > 0, 'settings-drawer.js should register delegated click handlers');

  await h.click(h.open);
  await settle();

  assert.deepEqual(
    h.requests.map((req) => req.url),
    [
      '/ui/settings/adapter/localfiles/libraries',
      '/ui/settings/adapter/localfiles/browse',
    ],
    'Open should persist the edited library rows before browse uses them'
  );
  assert.equal(h.select.value, 'Array');
  assert.equal(h.requests[1].body, 'lib=Array&path=');
  assert.equal(h.modal.hidden, false);
  assert.equal(h.modal.classList.contains('localfiles-open'), true);
});
